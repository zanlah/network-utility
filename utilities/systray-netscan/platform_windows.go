//go:build windows

package main

import (
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// pingHost (Windows): -n count, -w timeout(ms). Windows ping can exit 0 even on
// "unreachable", so confirm a real reply by looking for "TTL=".
func pingHost(ip string, count int) bool {
	out, _ := command("ping", "-n", strconv.Itoa(count), "-w", "800", ip).Output()
	return strings.Contains(string(out), "TTL=")
}

var macRe = regexp.MustCompile(`([0-9a-fA-F]{2}-){5}[0-9a-fA-F]{2}`)

// arpLookup (Windows): `arp -a <ip>` -> "  ip   aa-bb-cc-..  dynamic"
func arpLookup(ip string) string {
	out, _ := command("arp", "-a", ip).Output()
	m := macRe.FindString(string(out))
	return strings.ReplaceAll(m, "-", ":")
}

// mdnsName (Windows): the resolver handles mDNS since Win10; net.LookupAddr already
// covers most cases, so this stays a best-effort no-op.
func mdnsName(ip string) string { return "" }

func tailscaleBin() string {
	if p, err := exec.LookPath("tailscale"); err == nil {
		return p
	}
	for _, p := range []string{`C:\Program Files\Tailscale\tailscale.exe`} {
		if fileExists(p) {
			return p
		}
	}
	return ""
}

func copyToClipboard(s string) {
	c := command("cmd", "/c", "clip")
	c.Stdin = strings.NewReader(s)
	_ = c.Run()
}

func openURL(u string) {
	_ = command("rundll32", "url.dll,FileProtocolHandler", u).Start()
}

func promptSubnet() string {
	ps := `Add-Type -AssemblyName Microsoft.VisualBasic; ` +
		`[Microsoft.VisualBasic.Interaction]::InputBox('Enter subnet (CIDR, e.g. 192.168.1.0/24):','Subnet Scanner','192.168.1.0/24')`
	out, err := command("powershell", "-NoProfile", "-Command", ps).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
