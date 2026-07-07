package main

import (
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Host is the normalized data contract for a discovered device (same on every OS).
type Host struct {
	IP     string
	Name   string
	MAC    string
	Vendor string // from MAC OUI (LAN only)
	Kind   string // protocol/industrial hint, e.g. "S7/ISO-TSAP (likely Siemens)", "Loxone Miniserver 17.0.3.31"
	Ports  []int  // open industrial ports
}

// IsIndustrial reports whether the row should be flagged as PLC/Loxone/etc.
func (h Host) IsIndustrial() bool { return h.Kind != "" }

// plcPorts are the industrial ports we fingerprint. 102 S7, 502 Modbus,
// 44818 EtherNet/IP, 80 web (Loxone).
var plcPorts = []int{102, 502, 44818, 80}

// ouiTable — starter set focused on automation gear (extend from maclookup.app).
var ouiTable = map[string]string{
	"50:4F:94": "Loxone", "78:52:49": "Loxone",
	"00:1B:1B": "Siemens", "00:0E:8C": "Siemens", "00:1F:F8": "Siemens",
	"00:0C:F1": "Siemens", "8C:F3:19": "Siemens",
	"00:01:05": "Beckhoff", "00:30:DE": "WAGO",
	"00:80:F4": "Schneider", "00:00:54": "Schneider",
	"00:00:BC": "Rockwell/AB", "00:1D:9C": "Rockwell/AB",
	"00:A0:45": "Phoenix Contact", "00:60:65": "B&R", "00:90:E8": "Moxa",
	"B8:27:EB": "Raspberry Pi", "DC:A6:32": "Raspberry Pi", "E4:5F:01": "Raspberry Pi",
	"18:E8:29": "Ubiquiti", "FC:EC:DA": "Ubiquiti",
}

func ouiLabel(mac string) string {
	parts := strings.Split(mac, ":")
	if len(parts) < 3 {
		return ""
	}
	oui := ""
	for i := 0; i < 3; i++ {
		v, err := strconv.ParseInt(parts[i], 16, 0)
		if err != nil {
			return ""
		}
		if i > 0 {
			oui += ":"
		}
		oui += fmt.Sprintf("%02X", v)
	}
	return ouiTable[oui]
}

// probePort: a cross-platform TCP "is it open" check (replaces `nc`).
func probePort(ip string, port int, timeout time.Duration) bool {
	c, err := net.DialTimeout("tcp", net.JoinHostPort(ip, strconv.Itoa(port)), timeout)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

var (
	loxVer = regexp.MustCompile(`[0-9]+(\.[0-9]+){2,}`)
	loxSnr = regexp.MustCompile(`[0-9A-Fa-f]{2}(:[0-9A-Fa-f]{2}){5}`) // serial == MAC
)

// loxone: query the Miniserver's unauthenticated API; returns firmware + serial +
// true if Loxone. The serial is a stable identity even over Tailscale (no ARP MAC).
func loxone(ip string) (version, serial string, ok bool) {
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + ip + "/dev/cfg/api")
	if err != nil {
		return "", "", false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	s := string(body)
	if !strings.Contains(s, `control="dev/cfg/api"`) {
		return "", "", false
	}
	return loxVer.FindString(s), strings.ToUpper(loxSnr.FindString(s)), true
}

// fingerprint: probe the industrial ports and classify. Also returns a Loxone
// serial when found (used as a stable device identity).
func fingerprint(ip, vendor string) (kind string, open []int, serial string) {
	for _, p := range plcPorts {
		if probePort(ip, p, time.Second) {
			open = append(open, p)
		}
	}
	has := func(p int) bool {
		for _, o := range open {
			if o == p {
				return true
			}
		}
		return false
	}
	// An open port identifies the PROTOCOL, not the vendor. Port 102 = S7/ISO-TSAP,
	// typical of Siemens S7 PLCs but also S7-compatible controllers, soft-PLCs and
	// gateways — a strong hint, not a positive ID. (Loxone + MAC/OUI are positive IDs.)
	switch {
	case has(102):
		kind = "S7/ISO-TSAP (likely Siemens)"
	case has(502):
		kind = "Modbus TCP"
	case has(44818):
		kind = "EtherNet/IP (likely Rockwell)"
	}
	if kind == "" && has(80) {
		if ver, snr, ok := loxone(ip); ok {
			kind = "Loxone Miniserver"
			if ver != "" {
				kind += " " + ver
			}
			serial = snr
		}
	}
	if vendor == "Loxone" {
		kind = "Loxone Miniserver"
	}
	return
}

// enrich fills in MAC/vendor/name/fingerprint for one live host.
func enrich(ip string, probe bool) Host {
	h := Host{IP: ip}
	h.MAC = arpLookup(ip) // platform adapter
	if h.MAC != "" {
		h.Vendor = ouiLabel(h.MAC)
	}
	h.Name = hostName(ip)
	if probe {
		var serial string
		h.Kind, h.Ports, serial = fingerprint(ip, h.Vendor)
		// Give routed Loxones a stable identity (they have no ARP MAC).
		if h.MAC == "" && serial != "" {
			h.MAC = serial
		}
		// Last-resort name: many web-enabled devices (PLCs, gateways, printers,
		// Loxone) have no DNS/mDNS record but do serve a page <title> or Server
		// header — often the model, e.g. "IF2E011 Configuration".
		if h.Name == "" && hasPort(h.Ports, 80) {
			h.Name = httpTitle(ip)
		}
	}
	return h
}

func hostName(ip string) string {
	if names, _ := net.LookupAddr(ip); len(names) > 0 {
		return strings.TrimSuffix(names[0], ".")
	}
	if n := mdnsName(ip); n != "" { // Apple/Bonjour .local names
		return n
	}
	return netbiosName(ip) // Windows/NetBIOS names (IGOR-PC, MATIC)
}

func hasPort(ports []int, p int) bool {
	for _, o := range ports {
		if o == p {
			return true
		}
	}
	return false
}

var titleRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

// genericServers are web-server names that say nothing about the device.
var genericServers = []string{"nginx", "apache", "lighttpd", "microsoft-iis", "openresty", "caddy", "werkzeug", "cherrypy"}

// httpTitle fetches http://ip/ and returns the page <title>, or the Server header
// when it names an actual device (skipping generic web servers like nginx/Apache).
func httpTitle(ip string) string {
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + ip + "/")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if m := titleRe.FindSubmatch(body); m != nil {
		t := strings.Join(strings.Fields(html.UnescapeString(string(m[1]))), " ")
		if len(t) > 40 {
			t = t[:40]
		}
		if t != "" {
			return t
		}
	}
	srv := resp.Header.Get("Server")
	low := strings.ToLower(srv)
	for _, g := range genericServers {
		if strings.HasPrefix(low, g) {
			return "" // "nginx"/"Apache" is not a device name
		}
	}
	return srv
}

// ScanSubnet: ping-sweep the /24 of cidr, then enrich live hosts. Concurrency and
// packet count adapt to whether the subnet is local or routed — exactly like the
// bash version (local 60/1, routed 20/2).
func ScanSubnet(cidr string, probePLC bool) []Host {
	base := baseOf(cidr)
	if base == "" {
		return nil
	}
	par, count := 60, 1
	if isRouted(base) {
		par, count = 20, 2
	}

	// --- ping sweep ---
	sem := make(chan struct{}, par)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var alive []string
	for i := 1; i <= 254; i++ {
		ip := fmt.Sprintf("%s.%d", base, i)
		wg.Add(1)
		sem <- struct{}{}
		go func(ip string) {
			defer wg.Done()
			defer func() { <-sem }()
			if pingHost(ip, count) {
				mu.Lock()
				alive = append(alive, ip)
				mu.Unlock()
			}
		}(ip)
	}
	wg.Wait()

	// --- enrich ---
	esem := make(chan struct{}, 20)
	var ewg sync.WaitGroup
	var emu sync.Mutex
	hosts := make([]Host, 0, len(alive))
	for _, ip := range alive {
		ewg.Add(1)
		esem <- struct{}{}
		go func(ip string) {
			defer ewg.Done()
			defer func() { <-esem }()
			h := enrich(ip, probePLC)
			emu.Lock()
			hosts = append(hosts, h)
			emu.Unlock()
		}(ip)
	}
	ewg.Wait()

	SortHosts(hosts, "ip")
	return hosts
}

// baseOf returns the first three octets of a CIDR/IP ("172.16.180.0/24" -> "172.16.180").
func baseOf(cidr string) string {
	s := cidr
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	p := strings.Split(s, ".")
	if len(p) < 3 {
		return ""
	}
	return p[0] + "." + p[1] + "." + p[2]
}

// SortHosts orders in place: "ip" numeric, "host" named A→Z then unnamed by IP.
func SortHosts(hosts []Host, mode string) {
	ipKey := func(ip string) uint32 {
		var a, b, c, d uint32
		fmt.Sscanf(ip, "%d.%d.%d.%d", &a, &b, &c, &d)
		return a<<24 | b<<16 | c<<8 | d
	}
	if mode == "host" {
		sort.SliceStable(hosts, func(i, j int) bool {
			ni, nj := hosts[i].Name, hosts[j].Name
			if (ni == "") != (nj == "") {
				return ni != "" // named first
			}
			if ni != nj {
				return strings.ToLower(ni) < strings.ToLower(nj)
			}
			return ipKey(hosts[i].IP) < ipKey(hosts[j].IP)
		})
		return
	}
	sort.SliceStable(hosts, func(i, j int) bool { return ipKey(hosts[i].IP) < ipKey(hosts[j].IP) })
}
