package main

import (
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// LoxoneDevice is a Miniserver found via UDP discovery.
type LoxoneDevice struct {
	Name    string
	IP      string
	Port    int
	Serial  string // e.g. 50:4F:11:22:33:44
	Version string
}

// Subnet returns the /24 the Miniserver lives on (handy: it may be a subnet you
// didn't know to scan).
func (d LoxoneDevice) Subnet() string { return baseOf(d.IP) + ".0/24" }

// LoxLIVE response, e.g.:
//   LoxLIVE: Loxone Miniserver 192.168.178.32:80 504F11223344 10.2.3.26 Prog:... Type:0 HwId:A0000 ...
// The device name is variable length, so we anchor on the IPv4:port that follows it.
var loxRe = regexp.MustCompile(`LoxLIVE:\s*(.*?)\s+(\d{1,3}(?:\.\d{1,3}){3}):(\d+)\s+([0-9A-Fa-f]{12})\s+(\S+)`)

func parseLoxone(s string) (LoxoneDevice, bool) {
	m := loxRe.FindStringSubmatch(s)
	if m == nil {
		return LoxoneDevice{}, false
	}
	port, _ := strconv.Atoi(m[3])
	// serial 504F11223344 -> 50:4F:11:22:33:44
	var sb strings.Builder
	for i := 0; i < len(m[4]); i += 2 {
		if i > 0 {
			sb.WriteByte(':')
		}
		sb.WriteString(strings.ToUpper(m[4][i : i+2]))
	}
	return LoxoneDevice{
		Name:    strings.TrimSpace(m[1]),
		IP:      m[2],
		Port:    port,
		Serial:  sb.String(),
		Version: m[5],
	}, true
}

// interfaceBroadcasts returns the directed broadcast address of each up IPv4 iface,
// so discovery reaches every attached L2 segment (not just the default route's).
func interfaceBroadcasts() []net.IP {
	var out []net.IP
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagBroadcast == 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := n.IP.To4()
			if ip4 == nil {
				continue
			}
			bc := make(net.IP, 4)
			for i := 0; i < 4; i++ {
				bc[i] = ip4[i] | ^n.Mask[i]
			}
			out = append(out, bc)
		}
	}
	return out
}

// DiscoverLoxone broadcasts the Loxone discovery ping and collects replies. It finds
// Miniservers by identity regardless of their IP — even ones on a different subnet
// than you'd scan (installer static IPs, factory defaults), as long as they share an
// L2 broadcast domain with this machine.
func DiscoverLoxone(timeout time.Duration) []LoxoneDevice {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 7071})
	if err != nil {
		logf("loxone: cannot bind :7071: %v", err)
		return nil
	}
	defer conn.Close()

	// Enable SO_BROADCAST (per-OS helper) so we can send to broadcast addresses.
	if rc, err := conn.SyscallConn(); err == nil {
		_ = rc.Control(func(fd uintptr) { enableBroadcast(fd) })
	}

	// A single 0x00 byte to UDP 7070, to global + each interface's broadcast address.
	targets := append([]net.IP{net.IPv4bcast}, interfaceBroadcasts()...)
	for _, ip := range targets {
		_, _ = conn.WriteToUDP([]byte{0x00}, &net.UDPAddr{IP: ip, Port: 7070})
	}

	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	seen := map[string]bool{}
	var out []LoxoneDevice
	buf := make([]byte, 2048)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			break // deadline
		}
		if d, ok := parseLoxone(string(buf[:n])); ok && !seen[d.Serial] {
			seen[d.Serial] = true
			out = append(out, d)
		}
	}
	return out
}
