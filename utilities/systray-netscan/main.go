// systray-netscan: the subnet scanner (netscan.1h.sh) ported to a cross-platform
// Go tray app, same architecture as systray-ports.
//
//   main.go               PRESENTER — tray menu, click handling, in-memory state.
//   scan.go               shared: ping sweep, PLC/Loxone fingerprint, OUI, sorting.
//   netinfo.go            shared: interface/CIDR detection via the net package.
//   tailscale.go          shared: `tailscale status --json` parsed with encoding/json.
//   store.go              shared: saved subnets / active / sort persisted to disk.
//   platform_darwin.go    macOS ADAPTER: ping, arp, mdns, tailscale path, clipboard…
//   platform_windows.go   Windows ADAPTER: the same funcs with Windows commands.
//
// Note how much the bash version's ceremony disappears here: no scanning-marker
// file, no swiftbar://refreshplugin, no jq/python/nc/dig required for the core —
// scan state is just a struct guarded by a mutex, and we repaint() the menu.
package main

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/getlantern/systray"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const (
	maxHosts = 150
	maxNets  = 30
	maxPeers = 60
)

// ---- in-memory app state (replaces the bash cache + marker files) ----
type state struct {
	mu       sync.Mutex
	hosts    []Host
	scanning bool
	scanSub  string
	active   string
	sortMode string
}

var st = state{sortMode: "ip"}

// ---- menu widgets ----
type hostRow struct {
	item, copyIt, httpIt, sshIt *systray.MenuItem
	ip                          string
}
type netRow struct {
	item *systray.MenuItem
	cidr string
}

var (
	mTitleStatus *systray.MenuItem
	mScanNow     *systray.MenuItem
	mSortIP      *systray.MenuItem
	mSortHost    *systray.MenuItem
	mAdd         *systray.MenuItem
	mTS          *systray.MenuItem
	hostRows     []*hostRow
	netRows      []*netRow
	peerItems    []*systray.MenuItem
	rowMu        sync.Mutex
)

func main() { systray.Run(onReady, func() {}) }

func onReady() {
	initLogging()
	logf("started")
	st.active = LoadActive()
	st.sortMode = LoadSort()
	if st.active == "" { // first run: default to the first detected network
		if d := DetectedCIDRs(); len(d) > 0 {
			st.active = d[0].CIDR
		}
	}

	systray.SetTitle("📡")
	systray.SetTooltip("Subnet scanner")

	mScanNow = systray.AddMenuItem("Scan active subnet", "")
	mTitleStatus = systray.AddMenuItem("", "")
	mTitleStatus.Hide()
	mSortIP = systray.AddMenuItem("Sort by IP", "")
	mSortHost = systray.AddMenuItem("Sort by host", "")
	systray.AddSeparator()

	// Live-host pool, each with a small actions submenu.
	for i := 0; i < maxHosts; i++ {
		it := systray.AddMenuItem("", "")
		it.Hide()
		r := &hostRow{
			item:   it,
			copyIt: it.AddSubMenuItem("Copy IP", ""),
			httpIt: it.AddSubMenuItem("Open http://", ""),
			sshIt:  it.AddSubMenuItem("SSH", ""),
		}
		hostRows = append(hostRows, r)
		go func(r *hostRow) {
			for {
				select {
				case <-r.copyIt.ClickedCh:
					if ip := rowIP(r); ip != "" {
						copyToClipboard(ip)
					}
				case <-r.httpIt.ClickedCh:
					if ip := rowIP(r); ip != "" {
						openURL("http://" + ip)
					}
				case <-r.sshIt.ClickedCh:
					if ip := rowIP(r); ip != "" {
						openURL("ssh://" + ip)
					}
				}
			}
		}(r)
	}
	systray.AddSeparator()

	// Networks submenu: detected + saved + Tailscale-advertised, each scannable.
	mNets := systray.AddMenuItem("Networks", "Pick a subnet to scan")
	for i := 0; i < maxNets; i++ {
		it := mNets.AddSubMenuItem("", "")
		it.Hide()
		nr := &netRow{item: it}
		netRows = append(netRows, nr)
		go func(nr *netRow) {
			for range nr.item.ClickedCh {
				if c := rowCIDR(nr); c != "" {
					startScan(c)
				}
			}
		}(nr)
	}
	mAdd = mNets.AddSubMenuItem("Add subnet…", "")
	go func() {
		for range mAdd.ClickedCh {
			if c := promptSubnet(); c != "" {
				SaveSubnet(c)
				repaint()
			}
		}
	}()

	// Tailscale submenu: peer list (informational; advertised routes appear in Networks).
	mTS = systray.AddMenuItem("Tailscale", "")
	for i := 0; i < maxPeers; i++ {
		it := mTS.AddSubMenuItem("", "")
		it.Hide()
		peerItems = append(peerItems, it)
	}

	systray.AddSeparator()
	mReport := systray.AddMenuItem("Report bug…", "Copy diagnostics and email "+bugEmail)
	go func() {
		for range mReport.ClickedCh {
			reportBug()
		}
	}()
	mQuit := systray.AddMenuItem("Quit", "")

	// fixed-item handlers
	go func() {
		for range mScanNow.ClickedCh {
			if a := activeOrDefault(); a != "" {
				startScan(a)
			}
		}
	}()
	go func() {
		for range mSortIP.ClickedCh {
			setSort("ip")
		}
	}()
	go func() {
		for range mSortHost.ClickedCh {
			setSort("host")
		}
	}()
	go func() { <-mQuit.ClickedCh; systray.Quit() }()

	repaint()
}

func rowIP(r *hostRow) string  { rowMu.Lock(); defer rowMu.Unlock(); return r.ip }
func rowCIDR(n *netRow) string { rowMu.Lock(); defer rowMu.Unlock(); return n.cidr }

func setSort(mode string) {
	st.mu.Lock()
	st.sortMode = mode
	SortHosts(st.hosts, mode)
	st.mu.Unlock()
	SaveSort(mode)
	repaint()
}

// activeOrDefault returns the active subnet, or the first detected network if none.
func activeOrDefault() string {
	st.mu.Lock()
	a := st.active
	st.mu.Unlock()
	if a != "" {
		return a
	}
	if d := DetectedCIDRs(); len(d) > 0 {
		return d[0].CIDR
	}
	return ""
}

// startScan runs a scan in a goroutine and repaints when it finishes. One at a time.
func startScan(cidr string) {
	st.mu.Lock()
	if st.scanning {
		st.mu.Unlock()
		return
	}
	st.scanning = true
	st.scanSub = cidr
	st.active = cidr
	mode := st.sortMode
	st.mu.Unlock()
	SaveActive(cidr)
	repaint()
	go spin() // animate the menu-bar title + banner while scanning
	logf("scan start %s", cidr)

	go func() {
		defer guard("scan " + cidr)
		t0 := time.Now()
		hosts := ScanSubnet(cidr, true)
		SortHosts(hosts, mode)
		st.mu.Lock()
		st.hosts = hosts
		st.scanning = false
		st.mu.Unlock()
		logf("scan done %s: %d hosts in %s", cidr, len(hosts), time.Since(t0).Round(time.Millisecond))
		repaint()
	}()
}

// spin animates a loading indicator until the scan finishes.
func spin() {
	for i := 0; ; i++ {
		st.mu.Lock()
		scanning, sub := st.scanning, st.scanSub
		st.mu.Unlock()
		if !scanning {
			return
		}
		frame := spinnerFrames[i%len(spinnerFrames)]
		systray.SetTitle("📡 " + frame)
		mTitleStatus.SetTitle(frame + " Scanning " + sub + "…")
		time.Sleep(120 * time.Millisecond)
	}
}

type netEntry struct{ label, cidr string }

// networkEntries builds the scannable list: detected + saved + Tailscale-advertised.
func networkEntries(peers []TSPeer) []netEntry {
	seen := map[string]bool{}
	var out []netEntry
	add := func(label, cidr string) {
		if cidr == "" || seen[cidr] {
			return
		}
		seen[cidr] = true
		out = append(out, netEntry{label, cidr})
	}
	for _, d := range DetectedCIDRs() {
		add(d.Iface+"  "+d.CIDR, d.CIDR)
	}
	for _, s := range LoadSubnets() {
		add("saved  "+s, s)
	}
	for _, p := range peers {
		for _, r := range p.Routes {
			add("tailscale  "+r, r)
		}
	}
	return out
}

// repaint pushes current state into the menu widgets. Safe to call from any goroutine.
func repaint() {
	st.mu.Lock()
	hosts := make([]Host, len(st.hosts))
	copy(hosts, st.hosts)
	scanning := st.scanning
	scanSub := st.scanSub
	active := st.active
	mode := st.sortMode
	st.mu.Unlock()

	// title + status line
	if scanning {
		systray.SetTitle("📡 ⏳")
		mTitleStatus.SetTitle("⏳ Scanning " + scanSub + "…")
		mTitleStatus.Show()
		mScanNow.Disable()
	} else {
		systray.SetTitle(fmt.Sprintf("📡 %d", len(hosts)))
		mTitleStatus.Hide()
		mScanNow.Enable()
	}
	if active != "" {
		mScanNow.SetTitle("Scan active subnet (" + active + ")")
	}
	setCheck(mSortIP, "Sort by IP", mode == "ip")
	setCheck(mSortHost, "Sort by host", mode == "host")

	// host rows
	rowMu.Lock()
	for i, r := range hostRows {
		if i < len(hosts) {
			h := hosts[i]
			r.ip = h.IP
			r.item.SetTitle(hostTitle(h))
			r.item.Show()
		} else {
			r.ip = ""
			r.item.Hide()
		}
	}
	rowMu.Unlock()

	// tailscale peers + advertised subnets
	tsOK, peers := TSPeers()
	entries := networkEntries(peers)

	rowMu.Lock()
	for i, nr := range netRows {
		if i < len(entries) {
			nr.cidr = entries[i].cidr
			nr.item.SetTitle(entries[i].label)
			nr.item.Show()
		} else {
			nr.cidr = ""
			nr.item.Hide()
		}
	}
	rowMu.Unlock()

	if tsOK {
		mTS.SetTitle("Tailscale (connected)")
		mTS.Show()
	} else {
		mTS.SetTitle("Tailscale (not connected)")
	}
	for i, it := range peerItems {
		if tsOK && i < len(peers) {
			it.SetTitle(peerTitle(peers[i]))
			it.Show()
		} else {
			it.Hide()
		}
	}
}

func setCheck(it *systray.MenuItem, label string, on bool) {
	if on {
		it.SetTitle("✓ " + label)
	} else {
		it.SetTitle("  " + label)
	}
}

func hostTitle(h Host) string {
	name := h.Name
	if name == "" {
		name = "—"
	}
	badge := ""
	switch {
	case h.IsIndustrial():
		badge = "  ⚙ " + h.Kind
	case h.Vendor != "":
		badge = "  · " + h.Vendor
	}
	return fmt.Sprintf("%-15s  %-24s%s", h.IP, name, badge)
}

func peerTitle(p TSPeer) string {
	status := "online"
	if !p.Online {
		status = "offline"
	}
	extra := ""
	if len(p.Routes) > 0 {
		extra += "  ⇄ " + strings.Join(p.Routes, ",")
	}
	if p.ExitNode {
		extra += "  ⇱ exit"
	}
	name := p.Name
	if name == "" {
		name = "—"
	}
	return fmt.Sprintf("%-15s  %-20s  %s%s", p.IP, name, status, extra)
}
