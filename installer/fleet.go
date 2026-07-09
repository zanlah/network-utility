package main

// fleet-admin helper: a small bash script (fleet-admin.sh, embedded below) that
// flips this device's Headscale tags between tag:admin and tag:admin+
// tag:fleet-admin. It ships with the app — the installer writes the script plus
// a fleet-admin.env holding the Headscale API key and this device's node id into
// the install directory, so the technician can just run it later.
//
// The API key is a secret and is never committed: it's pasted (or read from
// FLEET_API_KEY) at install time and written only into the per-machine
// fleet-admin.env (0600). The node id is per-device, so we resolve it from the
// Headscale API by matching this device's tailnet IP when possible.

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//go:embed fleet-admin.sh
var fleetAdminScript []byte

const fleetDefaultAPIURL = "https://vpn.viptronik.si/api/v1"

// fleetConfig is what gets written into the installed fleet-admin.env.
type fleetConfig struct {
	apiURL string // Headscale API base, e.g. https://vpn.viptronik.si/api/v1
	apiKey string // Headscale API key (hskey-api-…) — secret
	nodeID string // this device's node id (may be blank → script asks at runtime)
}

// wantFleet reports whether the fleet-admin helper should be installed.
func (c fleetConfig) wantFleet() bool { return c.apiKey != "" }

// resolveFleet decides whether to install the fleet-admin helper and with what
// credentials. Precedence: --fleet-key / FLEET_API_KEY, else an interactive
// paste when the user opts in. apiURL falls back to the login server's /api/v1.
func resolveFleet(fleetFlag bool, keyFlag, nodeFlag, loginServer string, interactive bool) fleetConfig {
	fc := fleetConfig{
		apiURL: fleetAPIBase(loginServer),
		apiKey: firstNonEmpty(strings.TrimSpace(keyFlag), strings.TrimSpace(os.Getenv("FLEET_API_KEY"))),
		nodeID: firstNonEmpty(strings.TrimSpace(nodeFlag), strings.TrimSpace(os.Getenv("FLEET_NODE_ID"))),
	}
	if v := strings.TrimSpace(os.Getenv("FLEET_API_URL")); v != "" {
		fc.apiURL = v
	}

	// Non-interactive: install the helper only when a key was supplied (flag/env)
	// or explicitly requested via --fleet.
	if !interactive {
		if fleetFlag && fc.apiKey == "" {
			fmt.Println("  (--fleet ignored: no API key — pass --fleet-key or set FLEET_API_KEY)")
		}
		return fc
	}

	// Interactive: offer it, then paste the key if we don't already have one.
	if fc.apiKey == "" {
		if !fleetFlag && !promptYesNo("Install the fleet-admin helper (save the Headscale API key to toggle this device's admin access)?", false) {
			return fleetConfig{} // declined
		}
		fc.apiKey = strings.TrimSpace(promptString("Paste the Headscale API key (hskey-api-…)", ""))
	}
	return fc
}

// installFleetHelper writes the embedded script and a fleet-admin.env holding the
// credentials into dir. It best-effort resolves this device's node id from the
// Headscale API (via its tailnet IP) when one wasn't supplied.
func installFleetHelper(dir string, fc fleetConfig) error {
	if !fc.wantFleet() {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	scriptPath := filepath.Join(dir, "fleet-admin.sh")
	if err := os.WriteFile(scriptPath, fleetAdminScript, 0o755); err != nil {
		return fmt.Errorf("write %s: %w", scriptPath, err)
	}

	// Fill in the node id from Headscale if we don't have it yet.
	if fc.nodeID == "" {
		if id, err := resolveNodeID(fc.apiURL, fc.apiKey); err == nil {
			fc.nodeID = id
		} else {
			fmt.Printf("  (couldn't auto-resolve this device's Headscale node id: %v — the helper will ask on first run)\n", err)
		}
	}

	envPath := filepath.Join(dir, "fleet-admin.env")
	if err := os.WriteFile(envPath, []byte(fleetEnvContents(fc)), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", envPath, err)
	}
	fmt.Printf("  → fleet-admin helper installed (%s). Run: %s on|off\n", envPath, scriptPath)
	return nil
}

// fleetEnvContents renders the KEY=VALUE file the script sources.
func fleetEnvContents(fc fleetConfig) string {
	var b strings.Builder
	b.WriteString("# Written by the network-utility installer — Headscale fleet-admin creds.\n")
	b.WriteString("# Secret: keep this file local (it's mode 0600 and outside the repo).\n")
	fmt.Fprintf(&b, "FLEET_API_URL=%s\n", fc.apiURL)
	fmt.Fprintf(&b, "FLEET_API_KEY=%s\n", fc.apiKey)
	if fc.nodeID != "" {
		fmt.Fprintf(&b, "FLEET_NODE_ID=%s\n", fc.nodeID)
	} else {
		b.WriteString("# FLEET_NODE_ID unresolved — the script prompts for it on first run.\n")
	}
	return b.String()
}

// resolveNodeID asks Headscale for the node whose tailnet IP matches this
// device's, returning its node id. Needs Tailscale up (for the IP) and a valid
// API key. Best-effort: any failure just leaves the id blank.
func resolveNodeID(apiBase, apiKey string) (string, error) {
	bin := tailscaleBinary()
	if bin == "" {
		return "", fmt.Errorf("Tailscale CLI not found")
	}
	ip := tailscaleIP(bin)
	if ip == "" {
		return "", fmt.Errorf("no tailnet IP yet")
	}
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(apiBase, "/")+"/node", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Headscale API returned HTTP %d", resp.StatusCode)
	}
	var out struct {
		Nodes []struct {
			ID          string   `json:"id"`
			IPAddresses []string `json:"ipAddresses"`
		} `json:"nodes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	for _, n := range out.Nodes {
		for _, a := range n.IPAddresses {
			if a == ip {
				return n.ID, nil
			}
		}
	}
	return "", fmt.Errorf("no node matches %s", ip)
}

// fleetAPIBase derives the Headscale API base from a login server URL, falling
// back to the default when the login server is blank.
func fleetAPIBase(loginServer string) string {
	loginServer = strings.TrimRight(strings.TrimSpace(loginServer), "/")
	if loginServer == "" {
		return fleetDefaultAPIURL
	}
	return loginServer + "/api/v1"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
