package main

import (
	"encoding/json"
	"os"
	"sync"
)

// Config is the app's settings, persisted as JSON next to the binary
// (config/config.json). Enabled remembers whether the swap was on, so it
// survives restarts and logins.
type Config struct {
	Enabled bool `json:"enabled"`
}

var (
	cfgMu sync.Mutex
	cfg   Config
)

// loadConfig reads config.json at startup (missing/invalid file → defaults).
func loadConfig() {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	// Default the swap ON: it's the tool's whole purpose, and requiring a menu
	// toggle first surprised users (especially in VMs, where ⌘ arrives as the
	// Windows key ready to be swapped). An existing config.json — written
	// whenever the user flips the toggle — overrides this default, so anyone who
	// deliberately turns it off stays off.
	cfg.Enabled = true
	if b, err := os.ReadFile(confFile("config.json")); err == nil {
		_ = json.Unmarshal(b, &cfg)
	}
}

func saveConfig() {
	cfgMu.Lock()
	b, _ := json.MarshalIndent(cfg, "", "  ")
	cfgMu.Unlock()
	_ = os.WriteFile(confFile("config.json"), append(b, '\n'), 0o644)
}

func swapEnabled() bool {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	return cfg.Enabled
}

func setEnabled(on bool) {
	cfgMu.Lock()
	cfg.Enabled = on
	cfgMu.Unlock()
	saveConfig()
}
