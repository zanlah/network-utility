package main

import (
	"encoding/json"
	"net"
	"os/exec"
	"strings"
)

// tailscaleCGNAT is Tailscale's IPv4 range (100.64.0.0/10). A local interface
// address inside it means we're on a tailnet — a signal that, unlike the CLI,
// doesn't depend on reaching the macOS GUI daemon from our launchd session.
var tailscaleCGNAT = mustCIDR("100.64.0.0/10")

func mustCIDR(s string) *net.IPNet { _, n, _ := net.ParseCIDR(s); return n }

// tailnetInterfaceIP returns this host's Tailscale IPv4 from the local
// interfaces, or "". Env/session-independent, so it works even when the
// tailscale CLI can't be reached (e.g. netscan started by launchd).
func tailnetInterfaceIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		var ip net.IP
		switch v := a.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if ip4 := ip.To4(); ip4 != nil && tailscaleCGNAT.Contains(ip4) {
			return ip4.String()
		}
	}
	return ""
}

// Native JSON parsing — no jq/python3 needed (that was a bash limitation).

type tsNode struct {
	HostName       string   `json:"HostName"`
	OS             string   `json:"OS"`
	TailscaleIPs   []string `json:"TailscaleIPs"`
	Online         bool     `json:"Online"`
	ExitNodeOption bool     `json:"ExitNodeOption"`
	PrimaryRoutes  []string `json:"PrimaryRoutes"`
	Tags           []string `json:"Tags"`
}

type tsStatus struct {
	BackendState string             `json:"BackendState"`
	Self         *tsNode            `json:"Self"`
	Peer         map[string]*tsNode `json:"Peer"`
}

// TSPeer is the flattened view the UI uses.
type TSPeer struct {
	IP       string
	Name     string
	OS       string
	Online   bool
	ExitNode bool
	Routes   []string // advertised subnet routes (excluding exit-node 0.0.0.0/0)
	Tags     []string // ACL tags (e.g. tag:fleet)
}

// hasTag reports whether the peer carries the given ACL tag.
func (p TSPeer) hasTag(tag string) bool {
	for _, t := range p.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

// tailscaleStatus runs `tailscale status --json` (bin located per-OS) and parses it.
func tailscaleStatus() (*tsStatus, bool) {
	bin := tailscaleBin()
	if bin == "" {
		logf("tailscale: no CLI found (PATH + /Applications/Tailscale.app)")
		return nil, false
	}
	out, err := command(bin, "status", "--json").Output()
	if err != nil {
		msg := err.Error()
		if ee, ok := err.(*exec.ExitError); ok {
			msg += ": " + strings.TrimSpace(string(ee.Stderr))
		}
		logf("tailscale status error (bin=%s): %s", bin, msg)
		return nil, false
	}
	var st tsStatus
	if json.Unmarshal(out, &st) != nil {
		logf("tailscale: status JSON parse failed (bin=%s)", bin)
		return nil, false
	}
	logf("tailscale: bin=%s state=%s", bin, st.BackendState)
	return &st, true
}

func (n *tsNode) firstIP() string {
	if len(n.TailscaleIPs) > 0 {
		return n.TailscaleIPs[0]
	}
	return ""
}

func (n *tsNode) subnetRoutes() []string {
	var r []string
	for _, x := range n.PrimaryRoutes {
		if x != "0.0.0.0/0" && x != "::/0" {
			r = append(r, x)
		}
	}
	return r
}

// TSPeers returns (connected, peers). Peers include Self.
func TSPeers() (bool, []TSPeer) {
	st, ok := tailscaleStatus()
	if !ok || st.BackendState != "Running" {
		return false, nil
	}
	var peers []TSPeer
	add := func(n *tsNode) {
		if n == nil || n.firstIP() == "" {
			return
		}
		peers = append(peers, TSPeer{
			IP: n.firstIP(), Name: n.HostName, OS: n.OS,
			Online: n.Online, ExitNode: n.ExitNodeOption, Routes: n.subnetRoutes(), Tags: n.Tags,
		})
	}
	add(st.Self)
	for _, p := range st.Peer {
		add(p)
	}
	return true, peers
}
