// systray-ports: a cross-platform (macOS + Windows) tray version of the ports
// plugin, to show how the same idea looks without SwiftBar.
//
// Architecture — the point of this example:
//   main.go              the PRESENTER: builds the tray menu, handles clicks.
//                        One file, identical on every OS. Knows nothing about lsof
//                        or netstat.
//   collector.go         the shared DATA CONTRACT (the Listener struct + helpers).
//   collector_darwin.go  the macOS ADAPTER: lsof + kill.   (build tag: darwin)
//   collector_windows.go the Windows ADAPTER: netstat + taskkill. (build tag: windows)
//
// The presenter calls listListeners() / terminate() / forceKill(); the compiler
// links in the right adapter for the target OS. Build once per OS, no #ifdef soup.
//
// Contrast with SwiftBar: there you "print lines and SwiftBar draws the menu."
// Here you build the menu imperatively and manage its lifecycle yourself — more
// code, but one binary that runs on macOS *and* Windows.
package main

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/getlantern/systray"
)

// systray can't add/remove items after startup cleanly, so we pre-create a fixed
// pool of rows and update their titles/visibility on each refresh. (This awkwardness
// is exactly the tradeoff vs. SwiftBar's declarative "just reprint everything".)
const maxRows = 30

type row struct {
	item *systray.MenuItem
	open *systray.MenuItem
	term *systray.MenuItem
	kill *systray.MenuItem
	pid  int // the process this row currently maps to (guarded by mu)
	port int // its port, for the "open in browser" action
}

var (
	rows []*row
	mu   sync.Mutex
)

func main() { systray.Run(onReady, func() {}) }

func onReady() {
	initLogging()
	loadConfig()
	logf("started")
	applyIcon()                            // Windows tray icon (no-op on macOS)
	systray.SetTitle("🔌 …")               // shown next to the clock on macOS
	systray.SetTooltip("Listening TCP ports") // Windows tray shows the tooltip, not text

	mRefresh := systray.AddMenuItem("Refresh now", "Rescan listening ports")
	go func() {
		for range mRefresh.ClickedCh {
			refresh()
		}
	}()
	systray.AddSeparator()

	for i := 0; i < maxRows; i++ {
		it := systray.AddMenuItem("", "")
		it.Hide()
		r := &row{
			item: it,
			open: it.AddSubMenuItem("Open in browser", "Open http://localhost:<port>"),
			term: it.AddSubMenuItem("Terminate (SIGTERM)", ""),
			kill: it.AddSubMenuItem("Force Kill (SIGKILL)", ""),
		}
		rows = append(rows, r)
		go func(r *row) { // one click-handler goroutine per row; reads the live PID/port
			for {
				select {
				case <-r.open.ClickedCh:
					if port := r.currentPort(); port > 0 {
						url := fmt.Sprintf("http://localhost:%d", port)
						logf("open %s", url)
						openURL(url)
					}
				case <-r.term.ClickedCh:
					if pid := r.currentPID(); pid > 0 {
						logf("SIGTERM pid %d -> %v", pid, terminate(pid))
						refresh()
					}
				case <-r.kill.ClickedCh:
					if pid := r.currentPID(); pid > 0 {
						logf("SIGKILL pid %d -> %v", pid, forceKill(pid))
						refresh()
					}
				}
			}
		}(r)
	}

	systray.AddSeparator()

	// Settings submenu — groups the app's preferences in the UI.
	mSettings := systray.AddMenuItem("Settings", "Preferences")
	mDevOnly := mSettings.AddSubMenuItemCheckbox("Dev ports only", "Hide non-dev processes (Spotify, Control Center, …)", devOnlyEnabled())
	go func() {
		for range mDevOnly.ClickedCh {
			if mDevOnly.Checked() {
				mDevOnly.Uncheck()
				setDevOnly(false)
			} else {
				mDevOnly.Check()
				setDevOnly(true)
			}
			logf("dev-ports-only = %v", devOnlyEnabled())
			refresh()
		}
	}()
	mOpenCfg := mSettings.AddSubMenuItem("Open config folder", "Reveal the config/log folder")
	go func() {
		for range mOpenCfg.ClickedCh {
			openURL(confDir())
		}
	}()

	mReport := systray.AddMenuItem("Report bug…", "Copy diagnostics and email "+bugEmail)
	go func() {
		for range mReport.ClickedCh {
			reportBug()
		}
	}()
	mQuit := systray.AddMenuItem("Quit", "")
	go func() { <-mQuit.ClickedCh; systray.Quit() }()

	refresh()
	go func() { // periodic refresh, like SwiftBar's interval
		defer guard("refresh-loop")
		for range time.NewTicker(5 * time.Second).C {
			refresh()
		}
	}()
}

func (r *row) currentPID() int {
	mu.Lock()
	defer mu.Unlock()
	return r.pid
}

func (r *row) currentPort() int {
	mu.Lock()
	defer mu.Unlock()
	return r.port
}

// refresh re-collects listeners and repaints the row pool.
func refresh() {
	listeners, err := listListeners()
	if err != nil {
		logf("listListeners error: %v", err)
		systray.SetTitle("🔌 !")
		return
	}
	sort.Slice(listeners, func(i, j int) bool { return listeners[i].Port < listeners[j].Port })

	if devOnlyEnabled() { // filter out non-dev processes (Spotify, Control Center, …)
		var f []Listener
		for _, l := range listeners {
			if isDev(l) {
				f = append(f, l)
			}
		}
		listeners = f
	}
	systray.SetTitle(fmt.Sprintf("🔌 %d", len(listeners)))

	mu.Lock()
	defer mu.Unlock()
	for i, r := range rows {
		if i < len(listeners) {
			l := listeners[i]
			tag := ""
			if isWebDev(l.Port) || isDocker(l.Process) { // dev port OR any Docker-published port
				tag = "  ◆"
			}
			r.item.SetTitle(fmt.Sprintf("%-18s :%-5d  pid %d%s", l.Process, l.Port, l.PID, tag))
			r.open.SetTitle(fmt.Sprintf("Open http://localhost:%d", l.Port))
			r.pid = l.PID
			r.port = l.Port
			r.item.Show()
		} else {
			r.item.Hide()
			r.pid = 0
			r.port = 0
		}
	}
}
