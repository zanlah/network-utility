//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"
)

// Windows adapter: swap Ctrl ⇄ ⊞ Win with a low-level keyboard hook.
//
// A WH_KEYBOARD_LL hook sees every key before apps do. When the swap is on and
// the key is one of the four modifiers we care about, we synthesize the *other*
// modifier with SendInput and swallow the original. The hook is installed once
// on a dedicated, message-pumping OS thread and left in place; toggling just
// flips an atomic flag the callback reads, so there's no install/uninstall churn.
//
// Only Ctrl and Win are touched — every other key passes straight through.

// Win32 constants.
const (
	whKeyboardLL = 13

	wmKeyDown    = 0x0100
	wmKeyUp      = 0x0101
	wmSysKeyDown = 0x0104
	wmSysKeyUp   = 0x0105

	vkLControl = 0xA2
	vkRControl = 0xA3
	vkLWin     = 0x5B
	vkRWin     = 0x5C

	inputKeyboard      = 1
	keyEventFExtended  = 0x0001
	keyEventFKeyUp     = 0x0002
	hcAction           = 0
	llReturnEatMessage = 1 // non-zero return from the hook proc suppresses the key
)

// injectSig tags our own SendInput events so the hook ignores them (no recursion).
const injectSig = 0x1EE7C0DE

var (
	user32                  = syscall.NewLazyDLL("user32.dll")
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	procSetWindowsHookEx    = user32.NewProc("SetWindowsHookExW")
	procCallNextHookEx      = user32.NewProc("CallNextHookEx")
	procUnhookWindowsHookEx = user32.NewProc("UnhookWindowsHookEx")
	procGetMessage          = user32.NewProc("GetMessageW")
	procSendInput           = user32.NewProc("SendInput")
	procGetModuleHandle     = kernel32.NewProc("GetModuleHandleW")
)

// kbdllhookstruct mirrors KBDLLHOOKSTRUCT (the LL hook's lParam payload).
type kbdllhookstruct struct {
	vkCode      uint32
	scanCode    uint32
	flags       uint32
	time        uint32
	dwExtraInfo uintptr
}

// kbdInput mirrors KEYBDINPUT.
type kbdInput struct {
	wVk         uint16
	wScan       uint16
	dwFlags     uint32
	time        uint32
	dwExtraInfo uintptr
}

// input mirrors INPUT for a keyboard event. On 64-bit Windows sizeof(INPUT) is
// 40 bytes because the union is as large as MOUSEINPUT; the trailing pad makes
// our struct that size so SendInput's cbSize check passes. (amd64/arm64 only —
// the installer builds Windows as amd64.)
type input struct {
	inputType uint32
	_         uint32 // align the union to an 8-byte boundary
	ki        kbdInput
	_         [8]byte // reserve the rest of the union (MOUSEINPUT is larger)
}

// Compile-time guard: SendInput rejects the call unless cbSize == sizeof(INPUT),
// which is 40 on 64-bit Windows. If the struct layout ever drifts, this fails to
// build instead of silently doing nothing at runtime.
var _ [40]byte = [unsafe.Sizeof(input{})]byte{}

type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      struct{ x, y int32 }
}

var (
	swapOn     atomic.Bool // read by the hook callback on every keystroke
	hookOnce   sync.Once
	hookErr    error
	hookHandle uintptr
	// keep a reference so the callback isn't garbage-collected.
	hookCallback = syscall.NewCallback(hookProc)
)

// Diagnostics. When on, log every key-down (its virtual-key code, whether we
// swap it, and whether the injected key actually went in). Toggle it from the
// tray menu ("Debug logging") or start with KEYSWAP_DEBUG=1. Logging runs on a
// background goroutine drained from a buffered channel so the hook proc never
// blocks on file I/O — a slow WH_KEYBOARD_LL proc gets silently evicted by
// Windows (the LowLevelHooksTimeout), which would break the swap.
var (
	keyDebug atomic.Bool
	dbgCh    = make(chan string, 512)
)

// init starts the drain goroutine and honours KEYSWAP_DEBUG so logging works
// regardless of whether the swap/hook has been enabled yet.
func init() {
	if os.Getenv("KEYSWAP_DEBUG") != "" {
		keyDebug.Store(true)
	}
	go func() {
		for s := range dbgCh {
			logf("%s", s)
		}
	}()
}

// setDebug flips per-keystroke logging at runtime (menu-driven, no restart).
func setDebug(on bool) {
	keyDebug.Store(on)
	logf("debug logging = %v (VKs of interest: LWin=0x5B RWin=0x5C LCtrl=0xA2 RCtrl=0xA3)", on)
}

func debugEnabled() bool { return keyDebug.Load() }

func dbg(format string, a ...any) {
	if !keyDebug.Load() {
		return
	}
	select {
	case dbgCh <- fmt.Sprintf(format, a...):
	default: // buffer full: drop rather than block the hook proc
	}
}

// swapSupported reports that key swapping is available on this OS.
func swapSupported() bool { return true }

// setSwap turns the swap on or off. The first call lazily installs the hook.
func setSwap(on bool) error {
	if on {
		if err := ensureHook(); err != nil {
			return err
		}
	}
	swapOn.Store(on)
	return nil
}

// armHook installs the keyboard hook at startup so it is always live — for the
// swap (when enabled) and for the always-on modifier diagnostics below. This
// makes "keyboard hook installed" appear in every run's log; if it doesn't, the
// hook install itself failed, which is the finding we need. Idempotent.
func armHook() { _ = ensureHook() }

// modifier diagnostics: even with full debug logging off, log the first handful
// of modifier key-downs (Shift/Ctrl/Alt/Win/CapsLock/Menu — never character
// keys, so no typed text is recorded). This reveals what a key like ⌘ actually
// reports without the user having to enable anything. Capped per run.
var modSampleCount atomic.Int32

func isModifierVK(vk uint32) bool {
	switch {
	case vk == 0x10 || vk == 0x11 || vk == 0x12: // generic Shift/Ctrl/Alt
		return true
	case vk >= 0xA0 && vk <= 0xA5: // L/R Shift/Ctrl/Alt
		return true
	case vk == 0x5B || vk == 0x5C || vk == 0x5D: // LWin/RWin/Apps(Menu)
		return true
	case vk == 0x14: // CapsLock
		return true
	}
	return false
}

func sampleModifier(vk uint32, isSwapKey bool) {
	if modSampleCount.Load() >= 200 {
		return
	}
	modSampleCount.Add(1)
	select {
	case dbgCh <- fmt.Sprintf("modifier keydown vk=%#x isSwapKey=%v swapOn=%v (hook live)", vk, isSwapKey, swapOn.Load()):
	default:
	}
}

// ensureHook installs the keyboard hook exactly once and reports whether it's up.
func ensureHook() error {
	hookOnce.Do(func() {
		ready := make(chan error, 1)
		go hookLoop(ready)
		hookErr = <-ready
	})
	return hookErr
}

// hookLoop installs the hook and pumps its message queue for the process's life.
// WH_KEYBOARD_LL requires a message loop on the installing thread, so we lock it.
func hookLoop(ready chan error) {
	runtime.LockOSThread()
	defer guard("hookLoop")

	hMod, _, _ := procGetModuleHandle.Call(0)
	h, _, callErr := procSetWindowsHookEx.Call(uintptr(whKeyboardLL), hookCallback, hMod, 0)
	if h == 0 {
		ready <- fmt.Errorf("SetWindowsHookExW failed: %v", callErr)
		return
	}
	hookHandle = h
	ready <- nil
	logf("keyboard hook installed")

	var m msg
	for {
		r, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 { // 0 = WM_QUIT, -1 = error
			break
		}
	}
	procUnhookWindowsHookEx.Call(hookHandle)
}

// hookProc runs for every key event. When the swap is on and the key is one of
// our four modifiers, it injects the swapped key and swallows the original.
//
// For WH_KEYBOARD_LL, lParam is a KBDLLHOOKSTRUCT*, so we declare it with that
// type: syscall.NewCallback passes it through as a pointer and we avoid an
// unsafe uintptr→pointer conversion. We only read it during the call and never
// retain it, so the Windows-owned memory is never touched after we return.
func hookProc(nCode uintptr, wParam uintptr, k *kbdllhookstruct) uintptr {
	if int32(nCode) == hcAction {
		if wParam == wmKeyDown || wParam == wmSysKeyDown {
			_, matched := swapTarget(k.vkCode)
			if debugEnabled() {
				dbg("keydown vk=%#x swapOn=%v isModifier=%v ownInjected=%v",
					k.vkCode, swapOn.Load(), matched, k.dwExtraInfo == injectSig)
			} else if k.dwExtraInfo != injectSig && isModifierVK(k.vkCode) {
				sampleModifier(k.vkCode, matched) // always-on, capped, modifiers only
			}
		}
		if swapOn.Load() && k.dwExtraInfo != injectSig { // ignore our own injected events
			if target, ok := swapTarget(k.vkCode); ok {
				up := wParam == wmKeyUp || wParam == wmSysKeyUp
				sendKey(target, up)
				return llReturnEatMessage
			}
		}
	}
	r, _, _ := procCallNextHookEx.Call(0, nCode, wParam, uintptr(unsafe.Pointer(k)))
	return r
}

// swapTarget maps a modifier virtual-key to the one it should become, or false.
func swapTarget(vk uint32) (uint16, bool) {
	switch vk {
	case vkLControl:
		return vkLWin, true
	case vkRControl:
		return vkRWin, true
	case vkLWin:
		return vkLControl, true
	case vkRWin:
		return vkRControl, true
	}
	return 0, false
}

// sendKey injects a modifier key down/up event, tagged so the hook skips it.
func sendKey(vk uint16, keyUp bool) {
	var flags uint32
	// Win keys and the right Ctrl are "extended" keys.
	if vk == vkLWin || vk == vkRWin || vk == vkRControl {
		flags |= keyEventFExtended
	}
	if keyUp {
		flags |= keyEventFKeyUp
	}
	in := input{inputType: inputKeyboard}
	in.ki = kbdInput{wVk: vk, dwFlags: flags, dwExtraInfo: injectSig}
	n, _, callErr := procSendInput.Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in))
	if n == 0 {
		// 0 events inserted → injection was blocked. The usual cause is UIPI:
		// a non-elevated process can't inject into an elevated foreground window.
		dbg("SendInput BLOCKED vk=%#x up=%v err=%v (is the focused app running as admin?)", vk, keyUp, callErr)
	} else {
		dbg("SendInput ok vk=%#x up=%v", vk, keyUp)
	}
}

// command builds an *exec.Cmd that runs its child without popping a console
// window (CREATE_NO_WINDOW), so clipboard/URL helpers don't flash a cmd window.
func command(name string, args ...string) *exec.Cmd {
	c := exec.Command(name, args...)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	return c
}

// clipboard + URL opener (used by the bug reporter).
func copyToClipboard(s string) {
	c := command("cmd", "/c", "clip")
	c.Stdin = strings.NewReader(s)
	_ = c.Run()
}
func openURL(u string) {
	_ = command("rundll32", "url.dll,FileProtocolHandler", u).Start()
}
