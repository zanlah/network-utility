package main

import (
	"encoding/json"
	"os"
	"sync"
)

// Config is the app's settings, persisted as JSON next to the binary
// (config/config.json). Add fields here as new settings are introduced.
type Config struct {
	DevOnly bool `json:"devOnly"` // show only dev/Docker ports
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

func devOnlyEnabled() bool {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	return cfg.DevOnly
}

func setDevOnly(on bool) {
	cfgMu.Lock()
	cfg.DevOnly = on
	cfgMu.Unlock()
	saveConfig()
}
