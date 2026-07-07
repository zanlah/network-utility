// systray-keyswap: a tray tool that swaps the Ctrl and Command keys, so a Mac
// keyboard (or Mac muscle memory) feels normal on Windows — e.g. inside a VM.
//
// On Windows the "Command" key is the ⊞ Windows key: a Mac keyboard's ⌘ sends
// LWIN/RWIN there. With the swap on, ⌘+C behaves as Ctrl+C (copy), and the
// physical Ctrl key acts as ⊞ Win. It's a live toggle — flip it on while you're
// in the VM, off when you're not.
//
// Architecture — same shape as the other tools in this repo:
//   main.go          the PRESENTER: builds the tray menu, handles clicks. One
//                    file, identical on every OS. Knows nothing about hooks.
//   swap_windows.go  the Windows ADAPTER: a low-level keyboard hook that swaps
//                    Ctrl ⇄ Win by intercepting and re-injecting. (build: windows)
//   swap_other.go    the non-Windows ADAPTER: a stub that reports "unsupported",
//                    so the tool still builds on macOS/Linux. (build: !windows)
//
// The presenter calls swapSupported() / setSwap(); the compiler links in the
// right adapter for the target OS.
package main

import (
	"github.com/getlantern/systray"
)

func main() { systray.Run(onReady, func() {}) }

func onReady() {
	initLogging()
	loadConfig()
	logf("started (os supports swap = %v)", swapSupported())

	applyIcon() // Windows tray icon (no-op on macOS); without it the tray is invisible
	systray.SetTitle("⌨︎")
	updateStatus(false)

	if !swapSupported() {
		// Keep the tool visible and honest on macOS/Linux rather than doing
		// nothing mysteriously.
		note := systray.AddMenuItem("Only available on Windows", "The Ctrl⇄Win swap uses a Windows keyboard hook")
		note.Disable()
		systray.AddSeparator()
	}

	mToggle := systray.AddMenuItemCheckbox("Swap Ctrl ⇄ ⊞ Win", "Make ⌘/Ctrl behave the Mac way", swapEnabled())
	if !swapSupported() {
		mToggle.Disable()
	}
	go func() {
		for range mToggle.ClickedCh {
			on := !mToggle.Checked()
			if err := setSwap(on); err != nil {
				logf("setSwap(%v) failed: %v", on, err)
				continue // leave the checkbox as-is; the swap didn't change
			}
			if on {
				mToggle.Check()
			} else {
				mToggle.Uncheck()
			}
			setEnabled(on)
			updateStatus(on)
			logf("swap = %v", on)
		}
	}()

	systray.AddSeparator()

	mOpenCfg := systray.AddMenuItem("Open config folder", "Reveal the config/log folder")
	go func() {
		for range mOpenCfg.ClickedCh {
			openURL(confDir())
		}
	}()
	mReport := systray.AddMenuItem("Report bug…", "Copy diagnostics and email "+bugEmail)
	go func() {
		for range mReport.ClickedCh {
			reportBug()
		}
	}()
	mQuit := systray.AddMenuItem("Quit", "")
	go func() { <-mQuit.ClickedCh; systray.Quit() }()

	// Apply the saved state on startup so it survives restarts/logins.
	if swapSupported() && swapEnabled() {
		if err := setSwap(true); err != nil {
			logf("could not restore swap on startup: %v", err)
		} else {
			updateStatus(true)
		}
	}
}

// updateStatus reflects the current state in the menu-bar title (macOS) and the
// tray tooltip (Windows shows the tooltip, not a text title).
func updateStatus(on bool) {
	if !swapSupported() {
		systray.SetTitle("⌨︎ –")
		systray.SetTooltip("Ctrl⇄Win swap (Windows only)")
		return
	}
	if on {
		systray.SetTitle("⌨︎ ⇄")
		systray.SetTooltip("Ctrl⇄Win swap: ON")
	} else {
		systray.SetTitle("⌨︎")
		systray.SetTooltip("Ctrl⇄Win swap: off")
	}
}
