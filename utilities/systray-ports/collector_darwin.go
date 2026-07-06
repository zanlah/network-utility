//go:build darwin

package main

import (
	"os/exec"
	"strconv"
	"strings"
)

// listListeners (macOS): parse `lsof -nP -iTCP -sTCP:LISTEN`, same as ports.10s.sh.
func listListeners() ([]Listener, error) {
	out, _ := exec.Command("lsof", "-nP", "-iTCP", "-sTCP:LISTEN").Output()
	// lsof exits non-zero when it finds nothing; that's not an error for us.

	seen := map[string]bool{}
	var res []Listener
	for i, ln := range strings.Split(string(out), "\n") {
		if i == 0 || ln == "" { // skip header + blanks
			continue
		}
		f := strings.Fields(ln)
		if len(f) < 9 {
			continue
		}
		name := f[8] // e.g. 127.0.0.1:3000 or [::1]:3000 or *:8080
		colon := strings.LastIndex(name, ":")
		if colon < 0 {
			continue
		}
		port, err := strconv.Atoi(name[colon+1:])
		if err != nil {
			continue
		}
		pid, _ := strconv.Atoi(f[1])
		key := f[1] + ":" + name[colon+1:] // dedup pid:port (IPv4 + IPv6 → one row)
		if seen[key] {
			continue
		}
		seen[key] = true
		res = append(res, Listener{Port: port, PID: pid, Process: f[0], Addr: name})
	}
	return res, nil
}

// terminate / forceKill (macOS): POSIX signals.
func terminate(pid int) error { return exec.Command("kill", "-TERM", strconv.Itoa(pid)).Run() }
func forceKill(pid int) error { return exec.Command("kill", "-KILL", strconv.Itoa(pid)).Run() }

// clipboard + URL opener (used by the bug reporter).
func copyToClipboard(s string) {
	c := exec.Command("pbcopy")
	c.Stdin = strings.NewReader(s)
	_ = c.Run()
}
func openURL(u string) { _ = exec.Command("open", u).Start() }
