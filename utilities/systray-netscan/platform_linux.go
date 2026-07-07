//go:build linux

package main

import (
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// pingHost (Linux, iputils): -c count, -w deadline(s), -W per-reply timeout(s).
// Linux ping exits non-zero when nothing replies, so the exit code is enough.
func pingHost(ip string, count int) bool {
	return exec.Command("ping", "-c", strconv.Itoa(count), "-w", "2", "-W", "1", ip).Run() == nil
}

var macRe = regexp.MustCompile(`([0-9a-fA-F]{1,2}:){5}[0-9a-fA-F]{1,2}`)

// arpLookup (Linux): prefer `ip neigh` (iproute2, always present) —
// "192.168.1.1 dev eth0 lladdr aa:bb:cc:dd:ee:ff REACHABLE" — and fall back to
// the legacy `arp -n` if `ip` somehow isn't there.
func arpLookup(ip string) string {
	if out, err := exec.Command("ip", "neigh", "show", ip).Output(); err == nil {
		if m := macRe.FindString(string(out)); m != "" {
			return m
		}
	}
	out, _ := exec.Command("arp", "-n", ip).Output()
	return macRe.FindString(string(out))
}

// mdnsName (Linux): best-effort reverse mDNS via avahi-resolve when Avahi is
// installed ("<ip>\t<hostname>.local"); otherwise nothing.
func mdnsName(ip string) string {
	out, err := exec.Command("avahi-resolve", "-a", ip).Output()
	if err != nil {
		return ""
	}
	f := strings.Fields(string(out))
	if len(f) >= 2 {
		return strings.TrimSuffix(f[len(f)-1], ".")
	}
	return ""
}

func tailscaleBin() string {
	if p, err := exec.LookPath("tailscale"); err == nil {
		return p
	}
	for _, p := range []string{"/usr/bin/tailscale", "/usr/local/bin/tailscale"} {
		if fileExists(p) {
			return p
		}
	}
	return ""
}

// clipboard: Wayland ships wl-copy, X11 has xclip or xsel — try them in turn.
func copyToClipboard(s string) {
	for _, argv := range [][]string{
		{"wl-copy"},
		{"xclip", "-selection", "clipboard"},
		{"xsel", "--clipboard", "--input"},
	} {
		c := exec.Command(argv[0], argv[1:]...)
		c.Stdin = strings.NewReader(s)
		if err := c.Run(); err == nil {
			return
		}
	}
}

func openURL(u string) { _ = exec.Command("xdg-open", u).Start() }

// promptSubnet (Linux): a GUI entry box via zenity or kdialog when present.
// Empty result (neither installed, or the user cancelled) lets the caller fall
// back to the auto-detected subnet.
func promptSubnet() string {
	if _, err := exec.LookPath("zenity"); err == nil {
		out, err := exec.Command("zenity", "--entry",
			"--title=Subnet Scanner",
			"--text=Enter subnet (CIDR, e.g. 192.168.1.0/24):",
			"--entry-text=192.168.1.0/24").Output()
		if err == nil {
			return strings.TrimSpace(string(out))
		}
		return ""
	}
	if _, err := exec.LookPath("kdialog"); err == nil {
		out, err := exec.Command("kdialog",
			"--title", "Subnet Scanner",
			"--inputbox", "Enter subnet (CIDR, e.g. 192.168.1.0/24):", "192.168.1.0/24").Output()
		if err == nil {
			return strings.TrimSpace(string(out))
		}
	}
	return ""
}
