//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// On macOS autostart is a launchd LaunchAgent: RunAtLoad starts it now and at
// every login, KeepAlive restarts it if it ever exits. Loading the agent also
// starts it, so launch() is a no-op here.
const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
</dict>
</plist>
`

func agentLabel(bin string) string {
	return label + "." + strings.TrimSuffix(filepath.Base(bin), filepath.Ext(bin))
}

func plistPath(bin string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", agentLabel(bin)+".plist")
}

// registerAutostart writes the LaunchAgent but does not start it — launch()
// does that. launchd will load it at the next login regardless.
func registerAutostart(bin string) error {
	p := plistPath(bin)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	body := fmt.Sprintf(plistTemplate, agentLabel(bin), bin)
	return os.WriteFile(p, []byte(body), 0o644)
}

func unregisterAutostart(bin string) error {
	p := plistPath(bin)
	_ = runCmd("launchctl", "unload", p)
	return os.Remove(p)
}

// launch starts the tray app now. If a LaunchAgent was written, load it so the
// running copy is launchd-managed (RunAtLoad starts it, KeepAlive restarts it on
// crash); otherwise spawn the binary directly, detached.
func launch(bin string) error {
	if p := plistPath(bin); fileExists(p) {
		_ = runCmd("launchctl", "unload", p) // ignore: may not be loaded yet
		return runCmd("launchctl", "load", p)
	}
	cmd := exec.Command(bin)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func stop(bin string) { _ = runCmd("launchctl", "unload", plistPath(bin)) }
