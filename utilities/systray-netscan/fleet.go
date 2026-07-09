package main

// Fleet-admin toggle: a menu-bar item that flips this device between
// tag:admin and tag:admin+tag:fleet-admin via the Headscale API — the same
// thing scripts/fleet-admin.sh does, but native and cross-platform.
//
// Credentials come from the fleet-admin.env the installer wrote next to this
// binary (FLEET_API_URL / FLEET_API_KEY / FLEET_NODE_ID). When that file isn't
// present the menu item is never created, so stock installs are unaffected.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/getlantern/systray"
)

const (
	fleetAdminTag    = "tag:fleet-admin"
	fleetBaseTag     = "tag:admin"
	fleetDefaultAPI  = "https://vpn.viptronik.si/api/v1"
	fleetEnvFileName = "fleet-admin.env"

	// User-facing label (Slovenian): "visibility in the Janus network". Toggling
	// it on adds tag:fleet-admin so Janus peers can reach this device.
	fleetMenuLabel   = "Vidnost v Janus mreži"
	fleetMenuTooltip = "Preklopi vidnost te naprave v Janus mreži"
	fleetPeersHeader = "JANUS naprave"
)

var fleetHTTP = &http.Client{Timeout: 15 * time.Second}

type fleetConfig struct {
	apiURL string
	apiKey string
	nodeID string
}

// loadFleetConfig reads fleet-admin.env from next to this executable. ok is
// false when the file or its required creds (key + node id) are missing.
func loadFleetConfig() (fleetConfig, bool) {
	exe, err := os.Executable()
	if err != nil {
		return fleetConfig{}, false
	}
	f, err := os.Open(filepath.Join(filepath.Dir(exe), fleetEnvFileName))
	if err != nil {
		return fleetConfig{}, false
	}
	defer f.Close()

	env := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		env[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	cfg := fleetConfig{apiURL: env["FLEET_API_URL"], apiKey: env["FLEET_API_KEY"], nodeID: env["FLEET_NODE_ID"]}
	if cfg.apiURL == "" {
		cfg.apiURL = fleetDefaultAPI
	}
	if cfg.apiKey == "" || cfg.nodeID == "" {
		return fleetConfig{}, false // helper not fully configured (no node id yet)
	}
	return cfg, true
}

// headscalePeers fetches the tailnet's nodes from the Headscale control server
// and maps them to the UI's TSPeer view. Unlike the local `tailscale status`
// CLI, this works from any session (it's a plain HTTPS call to the control
// server), so netscan can show peers even when launched by launchd.
func headscalePeers(cfg fleetConfig) ([]TSPeer, error) {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(cfg.apiURL, "/")+"/node", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.apiKey)
	resp, err := fleetHTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Headscale API returned HTTP %d", resp.StatusCode)
	}
	var out struct {
		Nodes []struct {
			Name         string   `json:"name"`
			GivenName    string   `json:"givenName"`
			IPAddresses  []string `json:"ipAddresses"`
			Online       bool     `json:"online"`
			SubnetRoutes []string `json:"subnetRoutes"`
			Tags         []string `json:"tags"`
		} `json:"nodes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	var peers []TSPeer
	for _, n := range out.Nodes {
		ip := firstIPv4(n.IPAddresses)
		if ip == "" {
			continue
		}
		var routes []string
		exit := false
		for _, r := range n.SubnetRoutes {
			if r == "0.0.0.0/0" || r == "::/0" {
				exit = true
				continue
			}
			routes = append(routes, r)
		}
		name := n.Name
		if name == "" {
			name = n.GivenName
		}
		peers = append(peers, TSPeer{IP: ip, Name: name, Online: n.Online, ExitNode: exit, Routes: routes, Tags: n.Tags})
	}
	sort.Slice(peers, func(i, j int) bool { return peers[i].IP < peers[j].IP })
	return peers, nil
}

// firstIPv4 returns the first dotted-quad address in the list, or "".
func firstIPv4(addrs []string) string {
	for _, a := range addrs {
		if strings.Contains(a, ".") && !strings.Contains(a, ":") {
			return a
		}
	}
	return ""
}

// currentlyAdmin reports whether this device currently carries tag:fleet-admin.
func (c fleetConfig) currentlyAdmin() (bool, error) {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(c.apiURL, "/")+"/node/"+c.nodeID, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := fleetHTTP.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("Headscale API returned HTTP %d", resp.StatusCode)
	}
	var out struct {
		Node struct {
			Tags []string `json:"tags"`
		} `json:"node"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false, err
	}
	for _, t := range out.Node.Tags {
		if t == fleetAdminTag {
			return true, nil
		}
	}
	return false, nil
}

// setAdmin sets this device's tags to grant (on) or revoke fleet-admin.
func (c fleetConfig) setAdmin(on bool) error {
	tags := []string{fleetBaseTag}
	if on {
		tags = append(tags, fleetAdminTag)
	}
	body, err := json.Marshal(map[string][]string{"tags": tags})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(c.apiURL, "/")+"/node/"+c.nodeID+"/tags", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	resp, err := fleetHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Headscale API returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// setupFleetMenu drives the fleet-admin menu item: it shows the current state
// (checkmark when this device is fleet-admin) and toggles on click. Runs its own
// goroutine for clicks so it doesn't block the rest of the tray.
func setupFleetMenu(it *systray.MenuItem, cfg fleetConfig) {
	var mu sync.Mutex
	admin := false

	show := func(loading bool) {
		if loading {
			it.SetTitle("  " + fleetMenuLabel + "  ⏳")
			return
		}
		setCheck(it, fleetMenuLabel, admin)
	}
	refresh := func() {
		show(true)
		on, err := cfg.currentlyAdmin()
		if err != nil {
			it.SetTitle("  " + fleetMenuLabel + "  (ni na voljo)")
			logf("fleet: state check failed: %v", err)
			return
		}
		mu.Lock()
		admin = on
		mu.Unlock()
		show(false)
	}

	go refresh()
	go func() {
		for range it.ClickedCh {
			mu.Lock()
			want := !admin
			mu.Unlock()
			show(true)
			if err := cfg.setAdmin(want); err != nil {
				logf("fleet: toggle failed: %v", err)
				it.SetTitle("  " + fleetMenuLabel + "  (napaka)")
				time.Sleep(2 * time.Second)
				refresh()
				continue
			}
			mu.Lock()
			admin = want
			mu.Unlock()
			logf("fleet: set admin=%v", want)
			show(false)
		}
	}()
}
