//go:build windows

package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// On Windows autostart is a per-user Run key: every value under
// HKCU\Software\Microsoft\Windows\CurrentVersion\Run launches at sign-in.
// No admin rights, no Startup-folder shortcut plumbing.
const runKey = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`

func runValueName(bin string) string {
	return "network-utility-" + strings.TrimSuffix(filepath.Base(bin), filepath.Ext(bin))
}

func registerAutostart(bin string) error {
	// /f overwrites an existing value so re-install is idempotent.
	return runCmd("reg", "add", runKey, "/v", runValueName(bin), "/t", "REG_SZ", "/d", bin, "/f")
}

func unregisterAutostart(bin string) error {
	return runCmd("reg", "delete", runKey, "/v", runValueName(bin), "/f")
}

// launch starts the tray app detached so it keeps running after the installer exits.
func launch(bin string) error {
	cmd := exec.Command(bin)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x00000008} // DETACHED_PROCESS
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func stop(bin string) {
	_ = exec.Command("taskkill", "/IM", filepath.Base(bin), "/F").Run()
}
