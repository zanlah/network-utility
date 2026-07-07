//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// command builds an *exec.Cmd that runs its child without popping a console
// window. Without CREATE_NO_WINDOW, every ping/arp/powershell/tailscale spawn
// flashes a cmd window on Windows.
func command(name string, args ...string) *exec.Cmd {
	c := exec.Command(name, args...)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	return c
}
