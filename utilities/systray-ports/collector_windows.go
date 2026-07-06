//go:build windows

package main

import (
	"os/exec"
	"strconv"
	"strings"
)

// listListeners (Windows): `netstat -ano -p TCP` for LISTENING sockets, then map
// PID → image name via `tasklist`. Same Listener contract as the macOS collector.
func listListeners() ([]Listener, error) {
	out, err := exec.Command("netstat", "-ano", "-p", "TCP").Output()
	if err != nil {
		return nil, err
	}
	names := processNames()

	seen := map[string]bool{}
	var res []Listener
	for _, ln := range strings.Split(string(out), "\n") {
		f := strings.Fields(ln) // Proto  Local  Foreign  State  PID
		if len(f) < 5 || f[0] != "TCP" || f[3] != "LISTENING" {
			continue
		}
		local := f[1] // 0.0.0.0:3000 or [::]:8080
		colon := strings.LastIndex(local, ":")
		if colon < 0 {
			continue
		}
		port, err := strconv.Atoi(local[colon+1:])
		if err != nil {
			continue
		}
		pid, _ := strconv.Atoi(f[4])
		key := f[4] + ":" + local[colon+1:]
		if seen[key] {
			continue
		}
		seen[key] = true
		name := names[pid]
		if name == "" {
			name = "pid" + f[4]
		}
		res = append(res, Listener{Port: port, PID: pid, Process: name, Addr: local})
	}
	return res, nil
}

// processNames maps PID → image name (without .exe) via tasklist CSV.
func processNames() map[int]string {
	m := map[int]string{}
	out, err := exec.Command("tasklist", "/FO", "CSV", "/NH").Output()
	if err != nil {
		return m
	}
	for _, ln := range strings.Split(string(out), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		// "image.exe","1234","Console","1","12,345 K"
		parts := strings.Split(ln, "\",\"")
		if len(parts) < 2 {
			continue
		}
		pid, _ := strconv.Atoi(strings.Trim(parts[1], "\""))
		if pid > 0 {
			m[pid] = strings.TrimSuffix(strings.Trim(parts[0], "\""), ".exe")
		}
	}
	return m
}

// terminate / forceKill (Windows): no SIGTERM, so use taskkill (/F = force).
func terminate(pid int) error { return exec.Command("taskkill", "/PID", strconv.Itoa(pid)).Run() }
func forceKill(pid int) error {
	return exec.Command("taskkill", "/F", "/PID", strconv.Itoa(pid)).Run()
}

// clipboard + URL opener (used by the bug reporter).
func copyToClipboard(s string) {
	c := exec.Command("cmd", "/c", "clip")
	c.Stdin = strings.NewReader(s)
	_ = c.Run()
}
func openURL(u string) {
	_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start()
}
