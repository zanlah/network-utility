//go:build linux

package main

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// ssProcRe pulls the process name + pid out of ss's process column, e.g.
// `users:(("sshd",pid=1234,fd=3))` — the first ("name",pid=N) pair is enough.
var ssProcRe = regexp.MustCompile(`\("([^"]+)",pid=(\d+)`)

// listListeners (Linux): parse `ss -tlnpH` (iproute2 — present on essentially
// every modern distro). Flags: -t TCP, -l listening, -n numeric, -p processes,
// -H no header. Process/PID only show for your own sockets unless run as root;
// sockets owned by others degrade to an empty process name (still listed).
func listListeners() ([]Listener, error) {
	out, err := exec.Command("ss", "-tlnpH").Output()
	if err != nil {
		return nil, fmt.Errorf("could not run `ss` — install iproute2 (apt install iproute2): %w", err)
	}

	seen := map[string]bool{}
	var res []Listener
	for _, ln := range strings.Split(string(out), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		f := strings.Fields(ln)
		if len(f) < 4 || f[0] == "State" { // guard against a header on older ss builds
			continue
		}
		local := f[3] // e.g. 0.0.0.0:22, [::]:22, *:5555, 127.0.0.1:5432
		colon := strings.LastIndex(local, ":")
		if colon < 0 {
			continue
		}
		port, err := strconv.Atoi(local[colon+1:])
		if err != nil {
			continue
		}
		proc, pid := "", 0
		if m := ssProcRe.FindStringSubmatch(ln); m != nil {
			proc = m[1]
			pid, _ = strconv.Atoi(m[2])
		}
		key := strconv.Itoa(pid) + ":" + local[colon+1:] // dedup IPv4 + IPv6 rows → one
		if seen[key] {
			continue
		}
		seen[key] = true
		res = append(res, Listener{Port: port, PID: pid, Process: proc, Addr: local})
	}
	return res, nil
}

// terminate / forceKill (Linux): POSIX signals, same as macOS.
func terminate(pid int) error { return exec.Command("kill", "-TERM", strconv.Itoa(pid)).Run() }
func forceKill(pid int) error { return exec.Command("kill", "-KILL", strconv.Itoa(pid)).Run() }

// clipboard + URL opener (used by the bug reporter). Clipboard tooling varies by
// session: Wayland ships wl-copy, X11 has xclip or xsel — try them in turn.
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
