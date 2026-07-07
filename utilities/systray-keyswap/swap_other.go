//go:build !windows

package main

import (
	"errors"
	"os/exec"
	"runtime"
	"strings"
)

// Non-Windows adapter: the swap relies on a Windows keyboard hook, so here it's
// a no-op stub. The tool still builds and runs (the tray shows it's unavailable),
// which keeps `go build`, `go vet`, and cross-compiles working on macOS/Linux.

func swapSupported() bool { return false }

func setSwap(on bool) error {
	if on {
		return errors.New("key swap is only supported on Windows")
	}
	return nil
}

// clipboard + URL opener (used by the bug reporter) for macOS/Linux.
func copyToClipboard(s string) {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("pbcopy")
	default: // linux, best-effort
		if _, err := exec.LookPath("wl-copy"); err == nil {
			c = exec.Command("wl-copy")
		} else {
			c = exec.Command("xclip", "-selection", "clipboard")
		}
	}
	c.Stdin = strings.NewReader(s)
	_ = c.Run()
}

func openURL(u string) {
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}
	_ = exec.Command(opener, u).Start()
}
