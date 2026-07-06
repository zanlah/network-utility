package main

import (
	"fmt"
	"testing"
)

// These aren't unit tests so much as live probes of the local machine/network,
// mirroring how the bash version was validated. Run with:
//   go test -v -run TestLive -timeout 180s

func TestLiveDetect(t *testing.T) {
	for _, d := range DetectedCIDRs() {
		fmt.Printf("detected: %-8s %-18s (%s)\n", d.Iface, d.CIDR, d.IP)
	}
}

func TestLiveTailscale(t *testing.T) {
	ok, peers := TSPeers()
	fmt.Printf("tailscale connected=%v peers=%d\n", ok, len(peers))
	for _, p := range peers {
		if len(p.Routes) > 0 || p.ExitNode {
			fmt.Printf("  router/exit: %-15s %-20s routes=%v exit=%v\n", p.IP, p.Name, p.Routes, p.ExitNode)
		}
	}
}

func TestLiveScanLAN(t *testing.T) {
	nets := DetectedCIDRs()
	if len(nets) == 0 {
		t.Skip("no detected network")
	}
	cidr := nets[0].CIDR
	hosts := ScanSubnet(cidr, true)
	named, industrial := 0, 0
	fmt.Printf("scan %s: %d hosts\n", cidr, len(hosts))
	for _, h := range hosts {
		if h.Name != "" {
			named++
		}
		if h.IsIndustrial() {
			industrial++
		}
		if h.Name != "" || h.IsIndustrial() || h.Vendor != "" {
			fmt.Printf("  %-15s %-30s %-12s %s\n", h.IP, h.Name, h.Vendor, h.Kind)
		}
	}
	fmt.Printf("=> %d named, %d industrial\n", named, industrial)
}
