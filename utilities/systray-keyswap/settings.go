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
