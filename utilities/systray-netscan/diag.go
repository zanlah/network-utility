package main

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Where bug reports are sent.
const bugEmail = "zan.lah@viptronik.si"

const maxLogLines = 300

var (
	logMu  sync.Mutex
	logBuf []string
)

// logWriter routes the standard library logger into our ring buffer too.
type logWriter struct{}

func (logWriter) Write(p []byte) (int, error) {
	logf("%s", strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

// initLogging wires up the buffer + a persistent log file. Call once at startup.
func initLogging() {
	log.SetFlags(0)
	log.SetOutput(logWriter{})
}

// logf appends a timestamped line to the in-memory ring buffer and the log file.
func logf(format string, args ...any) {
	line := time.Now().Format("15:04:05") + " " + fmt.Sprintf(format, args...)
	logMu.Lock()
	logBuf = append(logBuf, line)
	if len(logBuf) > maxLogLines {
		logBuf = logBuf[len(logBuf)-maxLogLines:]
	}
	logMu.Unlock()
	if f, err := os.OpenFile(confFile("log.txt"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		fmt.Fprintln(f, line)
		_ = f.Close()
	}
}

// guard recovers a panicking goroutine and records it (so a crash becomes a log line).
func guard(where string) {
	if r := recover(); r != nil {
		buf := make([]byte, 4096)
		n := runtime.Stack(buf, false)
		logf("PANIC in %s: %v\n%s", where, r, buf[:n])
	}
}

func logSnapshot() string {
	logMu.Lock()
	defer logMu.Unlock()
	return strings.Join(logBuf, "\n")
}

// diagnostics is the full report copied to the clipboard.
func diagnostics() string {
	var b strings.Builder
	fmt.Fprintf(&b, "systray-netscan diagnostics\n")
	fmt.Fprintf(&b, "time:    %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(&b, "os/arch: %s/%s   go: %s\n", runtime.GOOS, runtime.GOARCH, runtime.Version())
	st.mu.Lock()
	fmt.Fprintf(&b, "active:  %s   sort: %s   scanning: %v   hosts: %d\n",
		st.active, st.sortMode, st.scanning, len(st.hosts))
	st.mu.Unlock()
	fmt.Fprintf(&b, "tailscale bin: %q\n", tailscaleBin())
	fmt.Fprintf(&b, "networks:\n")
	for _, d := range DetectedCIDRs() {
		fmt.Fprintf(&b, "  %-8s %-18s (%s)\n", d.Iface, d.CIDR, d.IP)
	}
	fmt.Fprintf(&b, "\n--- log (last %d lines) ---\n%s\n", maxLogLines, logSnapshot())
	return b.String()
}

func mailtoEscape(s string) string { return strings.ReplaceAll(url.QueryEscape(s), "+", "%20") }

// reportBug copies full diagnostics to the clipboard and opens a pre-filled email.
func reportBug() {
	full := diagnostics()
	copyToClipboard(full)
	logf("bug report prepared (%d bytes copied to clipboard)", len(full))

	subject := "Bug report: systray-netscan"
	// Keep the mailto body short (clients cap the URL length); the full report is on
	// the clipboard. Include a compact summary so the email is useful on its own.
	summary := full
	if i := strings.Index(full, "--- log"); i > 0 {
		summary = full[:i]
	}
	body := "Describe what happened:\n\n\n" +
		"(Full diagnostics are on your clipboard — paste them below this line.)\n\n" +
		summary
	openURL("mailto:" + bugEmail + "?subject=" + mailtoEscape(subject) + "&body=" + mailtoEscape(body))
}
