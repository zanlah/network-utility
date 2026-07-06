//go:build darwin

package main

import (
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// pingHost (macOS): -c count, -t total-timeout(s), -W per-reply wait(ms).
func pingHost(ip string, count int) bool {
	return exec.Command("ping", "-c", strconv.Itoa(count), "-t", "2", "-W", "800", ip).Run() == nil
}

var macRe = regexp.MustCompile(`([0-9a-fA-F]{1,2}:){5}[0-9a-fA-F]{1,2}`)

// arpLookup (macOS): `arp -n <ip>` -> "? (ip) at aa:bb:.. on en0"
func arpLookup(ip string) string {
	out, _ := exec.Command("arp", "-n", ip).Output()
	return macRe.FindString(string(out))
}

// mdnsName (macOS): reverse mDNS query for .local names.
func mdnsName(ip string) string {
	out, _ := exec.Command("dig", "+short", "+time=1", "+tries=1", "-x", ip, "@224.0.0.251", "-p", "5353").Output()
	for _, ln := range strings.Split(string(out), "\n") {
		ln = strings.TrimSuffix(strings.TrimSpace(ln), ".")
		if ln != "" && !strings.HasPrefix(ln, ";;") {
			return ln
		}
	}
	return ""
}

func tailscaleBin() string {
	if p, err := exec.LookPath("tailscale"); err == nil {
		return p
	}
	const app = "/Applications/Tailscale.app/Contents/MacOS/Tailscale"
	if fileExists(app) {
		return app
	}
	return ""
}

func copyToClipboard(s string) {
	c := exec.Command("pbcopy")
	c.Stdin = strings.NewReader(s)
	_ = c.Run()
}

func openURL(u string) { _ = exec.Command("open", u).Start() }

func promptSubnet() string {
	script := `text returned of (display dialog "Enter subnet (CIDR, e.g. 192.168.1.0/24):" default answer "192.168.1.0/24" with title "Subnet Scanner")`
	out, err := exec.Command("osascript", "-e", script).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
