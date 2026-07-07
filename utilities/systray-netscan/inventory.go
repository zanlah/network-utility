package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// Device tracking with history + aging, so a single flaky scan doesn't drop devices
// and we can tell what's new / gone. Persisted per subnet in inventory.json.

const (
	agingWindow = 30 * time.Minute      // keep showing an offline device this long
	pruneAge    = 30 * 24 * time.Hour   // forget a device entirely after this
)

// DevRecord is one tracked device (stored).
type DevRecord struct {
	IP        string    `json:"ip"`
	Name      string    `json:"name,omitempty"`
	MAC       string    `json:"mac,omitempty"`
	Kind      string    `json:"kind,omitempty"`
	Vendor    string    `json:"vendor,omitempty"`
	FirstSeen time.Time `json:"firstSeen"`
	LastSeen  time.Time `json:"lastSeen"`
	Online    bool      `json:"online"`
}

// DispHost is a device as rendered (online now, or recently seen).
type DispHost struct {
	IP, Name, MAC, Kind, Vendor string
	Online                      bool
	New                         bool
	LastSeen                    time.Time
}

func (d DispHost) IsIndustrial() bool { return d.Kind != "" }

// Changes summarizes what a scan did to the inventory.
type Changes struct{ New, Returned, Offline int }

func (c Changes) Any() bool { return c.New > 0 || c.Returned > 0 || c.Offline > 0 }

type inventoryData struct {
	Subnets map[string]map[string]*DevRecord `json:"subnets"`
}

var (
	invMu sync.Mutex
	inv   = &inventoryData{Subnets: map[string]map[string]*DevRecord{}}
)

// deviceKey is the stable identity: MAC/Loxone-serial when known, else IP (industrial
// gear is static-IP, so IP is stable there; routed Loxones key on their serial=MAC).
func deviceKey(ip, mac string) string {
	if mac != "" {
		return "mac:" + strings.ToUpper(mac)
	}
	return "ip:" + ip
}

func LoadInventory() {
	invMu.Lock()
	defer invMu.Unlock()
	if b, err := os.ReadFile(confFile("inventory.json")); err == nil {
		var d inventoryData
		if json.Unmarshal(b, &d) == nil && d.Subnets != nil {
			inv = &d
		}
	}
}

func persistLocked() {
	b, _ := json.MarshalIndent(inv, "", "  ")
	_ = os.WriteFile(confFile("inventory.json"), b, 0o644)
}

// OnlineCount is how many devices are currently marked online for a subnet — the
// baseline for the "suspicious drop" retry.
func OnlineCount(subnet string) int {
	invMu.Lock()
	defer invMu.Unlock()
	n := 0
	for _, r := range inv.Subnets[subnet] {
		if r.Online {
			n++
		}
	}
	return n
}

// MergeScan folds a scan's live hosts into the inventory (union + aging) and returns
// what changed. Devices not seen this scan are marked offline (not deleted).
func MergeScan(subnet string, hosts []Host) Changes {
	now := time.Now()
	invMu.Lock()
	defer invMu.Unlock()

	sub := inv.Subnets[subnet]
	if sub == nil {
		sub = map[string]*DevRecord{}
		inv.Subnets[subnet] = sub
	}

	seen := map[string]bool{}
	var ch Changes
	for _, h := range hosts {
		key := deviceKey(h.IP, h.MAC)
		seen[key] = true
		rec := sub[key]
		switch {
		case rec == nil:
			rec = &DevRecord{FirstSeen: now}
			sub[key] = rec
			ch.New++
		case !rec.Online:
			ch.Returned++
		}
		rec.IP = h.IP
		if h.Name != "" {
			rec.Name = h.Name
		}
		if h.MAC != "" {
			rec.MAC = h.MAC
		}
		rec.Kind = h.Kind
		if h.Vendor != "" {
			rec.Vendor = h.Vendor
		}
		rec.LastSeen = now
		rec.Online = true
	}
	for key, rec := range sub {
		if seen[key] {
			continue
		}
		if rec.Online { // just went missing
			rec.Online = false
			ch.Offline++
		}
		if now.Sub(rec.LastSeen) > pruneAge {
			delete(sub, key)
		}
	}
	persistLocked()
	return ch
}

// DisplayHosts returns the devices to render for a subnet: online now, plus ones seen
// within the aging window (shown as offline). Sorted online-first, then by mode.
func DisplayHosts(subnet, mode string) []DispHost {
	now := time.Now()
	invMu.Lock()
	var out []DispHost
	for _, r := range inv.Subnets[subnet] {
		if !r.Online && now.Sub(r.LastSeen) > agingWindow {
			continue
		}
		out = append(out, DispHost{
			IP: r.IP, Name: r.Name, MAC: r.MAC, Kind: r.Kind, Vendor: r.Vendor,
			Online: r.Online, New: r.FirstSeen.Equal(r.LastSeen), LastSeen: r.LastSeen,
		})
	}
	invMu.Unlock()

	ipKey := func(ip string) uint32 {
		var a, b, c, d uint32
		fmt.Sscanf(ip, "%d.%d.%d.%d", &a, &b, &c, &d)
		return a<<24 | b<<16 | c<<8 | d
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Online != out[j].Online {
			return out[i].Online // online first
		}
		if mode == "host" {
			ni, nj := out[i].Name, out[j].Name
			if (ni == "") != (nj == "") {
				return ni != ""
			}
			if ni != nj {
				return strings.ToLower(ni) < strings.ToLower(nj)
			}
		}
		return ipKey(out[i].IP) < ipKey(out[j].IP)
	})
	return out
}

// unionByKey merges two scans (for the confirm-retry), keeping the richer record.
func unionByKey(a, b []Host) []Host {
	m := map[string]Host{}
	var order []string
	add := func(h Host) {
		k := deviceKey(h.IP, h.MAC)
		if ex, ok := m[k]; ok {
			if ex.Name == "" {
				ex.Name = h.Name
			}
			if ex.Kind == "" {
				ex.Kind = h.Kind
			}
			if ex.MAC == "" {
				ex.MAC = h.MAC
			}
			if ex.Vendor == "" {
				ex.Vendor = h.Vendor
			}
			m[k] = ex
		} else {
			m[k] = h
			order = append(order, k)
		}
	}
	for _, h := range a {
		add(h)
	}
	for _, h := range b {
		add(h)
	}
	out := make([]Host, 0, len(order))
	for _, k := range order {
		out = append(out, m[k])
	}
	return out
}
