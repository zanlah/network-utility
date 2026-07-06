package main

import (
	"encoding/json"
	"os/exec"
)

// Native JSON parsing — no jq/python3 needed (that was a bash limitation).

type tsNode struct {
	HostName       string   `json:"HostName"`
	OS             string   `json:"OS"`
	TailscaleIPs   []string `json:"TailscaleIPs"`
	Online         bool     `json:"Online"`
	ExitNodeOption bool     `json:"ExitNodeOption"`
	PrimaryRoutes  []string `json:"PrimaryRoutes"`
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
}

// tailscaleStatus runs `tailscale status --json` (bin located per-OS) and parses it.
func tailscaleStatus() (*tsStatus, bool) {
	bin := tailscaleBin()
	if bin == "" {
		return nil, false
	}
	out, err := exec.Command(bin, "status", "--json").Output()
	if err != nil {
		logf("tailscale status error: %v", err)
		return nil, false
	}
	var st tsStatus
	if json.Unmarshal(out, &st) != nil {
		return nil, false
	}
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
			Online: n.Online, ExitNode: n.ExitNodeOption, Routes: n.subnetRoutes(),
		})
	}
	add(st.Self)
	for _, p := range st.Peer {
		add(p)
	}
	return true, peers
}
