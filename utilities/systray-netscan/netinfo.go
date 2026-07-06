package main

import (
	"fmt"
	"net"
)

// LocalNet is a directly-attached private network detected on this machine.
type LocalNet struct {
	Iface string
	CIDR  string // e.g. 192.168.1.0/24
	IP    string // this host's address on it
}

func isPrivate(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	switch {
	case ip4[0] == 10:
		return true
	case ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31:
		return true
	case ip4[0] == 192 && ip4[1] == 168:
		return true
	}
	return false
}

// DetectedCIDRs enumerates private IPv4 networks on up, non-loopback interfaces.
// Pure Go (net package) — no ifconfig/route, so it's identical on macOS and Windows.
func DetectedCIDRs() []LocalNet {
	var res []LocalNet
	ifaces, err := net.Interfaces()
	if err != nil {
		return res
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipnet.IP.To4()
			if ip4 == nil || !isPrivate(ip4) {
				continue
			}
			ones, _ := ipnet.Mask.Size()
			netAddr := ip4.Mask(ipnet.Mask)
			res = append(res, LocalNet{
				Iface: ifc.Name,
				CIDR:  fmt.Sprintf("%s/%d", netAddr.String(), ones),
				IP:    ip4.String(),
			})
		}
	}
	return res
}

// isRouted reports whether base.1 is NOT inside any directly-attached network —
// i.e. reachable only over a router/VPN (Tailscale). Drives the gentle sweep.
func isRouted(base string) bool {
	target := net.ParseIP(base + ".1")
	if target == nil {
		return true
	}
	for _, ln := range DetectedCIDRs() {
		if _, n, err := net.ParseCIDR(ln.CIDR); err == nil && n.Contains(target) {
			return false
		}
	}
	return true
}
