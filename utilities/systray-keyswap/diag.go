package main

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
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

// confDir stores config next to the executable ("inside the app" / portable mode),
// falling back to the user config dir if the app's folder isn't writable.
func confDir() string {
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		d := filepath.Join(filepath.Dir(exe), "config")
		if os.MkdirAll(d, 0o755) == nil {
			probe := filepath.Join(d, ".w")
			if os.WriteFile(probe, nil, 0o644) == nil { // confirm it's actually writable
				_ = os.Remove(probe)
				return d
			}
		}
	}
	base, err := os.UserConfigDir()
	if err != nil {
		base, _ = os.UserHomeDir()
	}
	d := filepath.Join(base, "systray-keyswap")
	_ = os.MkdirAll(d, 0o755)
	return d
}

func confFile(name string) string { return filepath.Join(confDir(), name) }

// logWriter routes the standard library logger into our ring buffer too.
type logWriter struct{}

func (logWriter) Write(p []byte) (int, error) {
	logf("%s", strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

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
	fmt.Fprintf(&b, "systray-keyswap diagnostics\n")
	fmt.Fprintf(&b, "time:      %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(&b, "os/arch:   %s/%s   go: %s\n", runtime.GOOS, runtime.GOARCH, runtime.Version())
	fmt.Fprintf(&b, "supported: %v\n", swapSupported())
	fmt.Fprintf(&b, "enabled:   %v\n", swapEnabled())
	fmt.Fprintf(&b, "\n--- log (last %d lines) ---\n%s\n", maxLogLines, logSnapshot())
	return b.String()
}

func mailtoEscape(s string) string { return strings.ReplaceAll(url.QueryEscape(s), "+", "%20") }

// reportBug copies full diagnostics to the clipboard and opens a pre-filled email.
func reportBug() {
	full := diagnostics()
	copyToClipboard(full)
	logf("bug report prepared (%d bytes copied to clipboard)", len(full))

	subject := "Bug report: systray-keyswap"
	summary := full
	if i := strings.Index(full, "--- log"); i > 0 {
		summary = full[:i]
	}
	body := "Describe what happened:\n\n\n" +
		"(Full diagnostics are on your clipboard — paste them below this line.)\n\n" +
		summary
	openURL("mailto:" + bugEmail + "?subject=" + mailtoEscape(subject) + "&body=" + mailtoEscape(body))
}
