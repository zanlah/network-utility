# systray-keyswap

A tray tool that **swaps the Ctrl and Command keys** so a Mac keyboard (or Mac
muscle memory) feels normal on Windows — especially inside a VM.

On Windows the "Command" key is the **⊞ Windows key**: a Mac keyboard's ⌘ sends
`LWIN`/`RWIN` there. With the swap **on**:

- **⌘ + C** behaves as **Ctrl + C** (copy), `⌘ + V` as paste, `⌘ + T`, `⌘ + W`, … — the
  shortcuts your fingers expect.
- The physical **Ctrl** key acts as **⊞ Win**.

It's a **live toggle** from the tray — flip it on while you're working in the VM,
off when you're back to normal Windows use. The state is remembered across restarts
and logins.

> Platform: **Windows only.** On macOS/Linux the tool still runs but the tray shows
> "Only available on Windows" (the swap needs a Windows keyboard hook).

## How it works

A low-level keyboard hook (`SetWindowsHookEx(WH_KEYBOARD_LL)`) sees every key before
apps do. When the swap is on and the key is one of the four modifiers we care about
(`LCONTROL`, `RCONTROL`, `LWIN`, `RWIN`), the hook **synthesizes the swapped key**
with `SendInput` and **swallows the original**. Every other key passes straight
through untouched.

- **No admin, no reboot** — unlike the registry `Scancode Map`, the hook applies
  instantly and can be toggled on/off live.
- **No infinite loop** — injected events are tagged in `dwExtraInfo` so the hook
  ignores its own output.
- The hook is installed **once** on a dedicated, message-pumping OS thread; toggling
  just flips an atomic flag the callback reads on each keystroke.

```
main.go          PRESENTER — tray menu + toggle. One file, identical on every OS.
swap_windows.go  Windows ADAPTER — the WH_KEYBOARD_LL hook.  (//go:build windows)
swap_other.go    non-Windows stub so it still builds on macOS/Linux. (//go:build !windows)
settings.go      persists the on/off state (config/config.json)
diag.go          logging + "Report bug…" diagnostics
```

## Build & run

```sh
# From the repo root, via the installer (interactive):
go run ./installer install            # tick "Key swap" in the checklist

# Or directly:
cd utilities/systray-keyswap
go build -o systray-keyswap.exe .     # on Windows (or cross-build: see the repo Makefile)
```

Then use the tray checkbox **"Swap Ctrl ⇄ ⊞ Win"** to turn it on/off.

## Notes

- **It's a full swap** (both directions), so with it on, the physical Ctrl key opens
  the Start menu on a lone tap — that's the nature of a swap.
- Some apps that install their own global hooks (or run elevated) may not see the
  swap. Elevated windows in particular ignore hooks from a non-elevated process.
- Windows builds target **amd64/arm64** (64-bit); a compile-time check enforces the
  `INPUT` struct size that `SendInput` requires.
