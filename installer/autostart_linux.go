//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// On Linux autostart is an XDG desktop entry in ~/.config/autostart, which the
// desktop session launches at login. The tray needs a running graphical session
// (getlantern/systray uses libappindicator/AppIndicator).
const desktopTemplate = `[Desktop Entry]
Type=Application
Name=%s
Exec=%s
X-GNOME-Autostart-enabled=true
Terminal=false
`

func appName(bin string) string {
	return strings.TrimSuffix(filepath.Base(bin), filepath.Ext(bin))
}

func desktopPath(bin string) string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "autostart", appName(bin)+".desktop")
}

func registerAutostart(bin string) error {
	p := desktopPath(bin)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	body := fmt.Sprintf(desktopTemplate, appName(bin), bin)
	return os.WriteFile(p, []byte(body), 0o644)
}

func unregisterAutostart(bin string) error {
	return os.Remove(desktopPath(bin))
}

// launch starts the tray app detached so it survives the installer exiting.
func launch(bin string) error {
	cmd := exec.Command(bin)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func stop(bin string) {
	_ = exec.Command("pkill", "-f", filepath.Base(bin)).Run()
}
