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
	maxHosts  = 150
	maxNets   = 30
	maxPeers  = 60
	maxLoxone = 16
	maxJanus  = 30
)

// ---- in-memory app state (replaces the bash cache + marker files) ----
type state struct {
	mu       sync.Mutex
	display  []DispHost
	changes  Changes
	scanning bool
	scanSub  string
	active   string
	sortMode string
	loxone   []LoxoneDevice

	// Tailscale view, refreshed by refreshTS() (CLI when reachable, else the
	// Headscale control server) so repaint() never blocks on exec/network.
	tsConnected bool
	tsSelfIP    string
	tsPeers     []TSPeer
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
type loxRow struct {
	item, openIt, copyIt, scanIt *systray.MenuItem
	dev                          LoxoneDevice
}

var (
	mTitleStatus *systray.MenuItem
	mChanges     *systray.MenuItem
	mScanNow     *systray.MenuItem
	mSortIP      *systray.MenuItem
	mSortHost    *systray.MenuItem
	mAdd         *systray.MenuItem
	mTS          *systray.MenuItem
	mTSInfo      *systray.MenuItem
	mTSRefresh   *systray.MenuItem
	mJanusHeader *systray.MenuItem
	janusItems   []*systray.MenuItem
	mLoxStatus   *systray.MenuItem
	hostRows     []*hostRow
	netRows      []*netRow
	peerItems    []*systray.MenuItem
	loxRows      []*loxRow
	rowMu        sync.Mutex
)

func main() { systray.Run(onReady, func() {}) }

func onReady() {
	initLogging()
	LoadInventory()
	logf("started")
	st.active = LoadActive()
	st.sortMode = LoadSort()
	if st.active == "" { // first run: default to the first detected network
		if d := DetectedCIDRs(); len(d) > 0 {
			st.active = d[0].CIDR
		}
	}
	st.display = DisplayHosts(st.active, st.sortMode) // show known devices from last run

	applyIcon() // Windows tray icon (no-op on macOS)
	systray.SetTitle("📡")
	systray.SetTooltip("Subnet scanner")

	mScanNow = systray.AddMenuItem("Scan active subnet", "")
	mTitleStatus = systray.AddMenuItem("", "")
	mTitleStatus.Hide()
	mChanges = systray.AddMenuItem("", "New / offline devices since last scan")
	mChanges.Hide()
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
	// Recheck the Tailscale connection on demand (state otherwise only updates
	// on a scan). repaint() re-reads the interface + CLI, so this is enough.
	mTSRefresh = mTS.AddSubMenuItem("↻ Refresh", "Recheck the Tailscale connection")
	go func() {
		for range mTSRefresh.ClickedCh {
			logf("tailscale: manual refresh")
			go refreshTS()
		}
	}()
	// Always-present submenu line (this device's tailnet IP / status) so the
	// submenu is never empty even when the peer list can't be loaded.
	mTSInfo = mTS.AddSubMenuItem("", "")
	mTSInfo.Disable()
	for i := 0; i < maxPeers; i++ {
		it := mTS.AddSubMenuItem("", "")
		it.Hide()
		peerItems = append(peerItems, it)
	}

	// Loxone submenu: UDP-broadcast discovery finds Miniservers by identity, even on a
	// different subnet than you'd scan (installer static IPs, factory defaults).
	mLoxone := systray.AddMenuItem("Loxone", "Find Miniservers by UDP broadcast")
	mLoxScan := mLoxone.AddSubMenuItem("Scan for Loxone (broadcast)", "")
	go func() {
		for range mLoxScan.ClickedCh {
			startLoxoneScan()
		}
	}()
	mLoxStatus = mLoxone.AddSubMenuItem("", "")
	mLoxStatus.Hide()
	for i := 0; i < maxLoxone; i++ {
		it := mLoxone.AddSubMenuItem("", "")
		it.Hide()
		lr := &loxRow{
			item:   it,
			openIt: it.AddSubMenuItem("Open web UI", ""),
			copyIt: it.AddSubMenuItem("Copy IP", ""),
			scanIt: it.AddSubMenuItem("Scan its subnet", ""),
		}
		loxRows = append(loxRows, lr)
		go func(lr *loxRow) {
			for {
				select {
				case <-lr.openIt.ClickedCh:
					if d := loxDev(lr); d.IP != "" {
						openURL(fmt.Sprintf("http://%s:%d", d.IP, d.Port))
					}
				case <-lr.copyIt.ClickedCh:
					if d := loxDev(lr); d.IP != "" {
						copyToClipboard(d.IP)
					}
				case <-lr.scanIt.ClickedCh:
					if d := loxDev(lr); d.IP != "" {
						startScan(d.Subnet())
					}
				}
			}
		}(lr)
	}

	// Fleet-admin toggle: flip this device between tag:admin and
	// tag:admin+tag:fleet-admin via the Headscale API. Only shown when the
	// installer saved fleet-admin.env next to this binary.
	if cfg, ok := loadFleetConfig(); ok {
		systray.AddSeparator()
		mFleet := systray.AddMenuItem("  "+fleetMenuLabel, fleetMenuTooltip)
		setupFleetMenu(mFleet, cfg)
		// JANUS devices: the tag:fleet peers, listed prominently under the toggle.
		mJanusHeader = systray.AddMenuItem("  "+fleetPeersHeader, "Naprave z oznako tag:fleet")
		mJanusHeader.Disable()
		mJanusHeader.Hide()
		for i := 0; i < maxJanus; i++ {
			it := systray.AddMenuItem("", "")
			it.Hide()
			janusItems = append(janusItems, it)
		}
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
	go refreshTS() // populate Tailscale state without blocking the tray startup
}

// refreshTS recomputes the Tailscale view: the CLI when it's reachable (richest
// data), otherwise the Headscale control server (works from any session), and
// falls back to the interface's tailnet IP for the connected/offline signal.
// Stores the result in state and repaints. Safe to call from any goroutine.
func refreshTS() {
	selfIP := tailnetInterfaceIP()
	var peers []TSPeer
	haveData := false
	if ok, p := TSPeers(); ok {
		peers, haveData = p, true
	} else if cfg, ok := loadFleetConfig(); ok {
		if hp, err := headscalePeers(cfg); err == nil {
			peers, haveData = hp, true
		} else {
			logf("tailscale: headscale peers error: %v", err)
		}
	}
	st.mu.Lock()
	st.tsConnected = haveData || selfIP != ""
	st.tsSelfIP = selfIP
	st.tsPeers = peers
	st.mu.Unlock()
	repaint()
}

func rowIP(r *hostRow) string      { rowMu.Lock(); defer rowMu.Unlock(); return r.ip }
func rowCIDR(n *netRow) string     { rowMu.Lock(); defer rowMu.Unlock(); return n.cidr }
func loxDev(l *loxRow) LoxoneDevice { rowMu.Lock(); defer rowMu.Unlock(); return l.dev }

// startLoxoneScan runs Loxone UDP discovery in the background, then repaints.
func startLoxoneScan() {
	logf("loxone discovery start")
	mLoxStatus.SetTitle("⏳ searching…")
	mLoxStatus.Show()
	go func() {
		defer guard("loxone")
		devs := DiscoverLoxone(3 * time.Second)
		st.mu.Lock()
		st.loxone = devs
		st.mu.Unlock()
		logf("loxone discovery: %d found", len(devs))
		repaint()
	}()
}

func setSort(mode string) {
	st.mu.Lock()
	st.sortMode = mode
	active := st.active
	st.mu.Unlock()
	SaveSort(mode)
	disp := DisplayHosts(active, mode)
	st.mu.Lock()
	st.display = disp
	st.mu.Unlock()
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
		prevOnline := OnlineCount(cidr)
		hosts := ScanSubnet(cidr, true)
		// Bounded confirm-retry: if this scan found far fewer than we knew were
		// online, it's probably a flaky routed sweep — scan once more and merge.
		if prevOnline >= 5 && len(hosts) < prevOnline*7/10 {
			logf("scan %s found %d vs %d known online — retrying once", cidr, len(hosts), prevOnline)
			hosts = unionByKey(hosts, ScanSubnet(cidr, true))
		}
		ch := MergeScan(cidr, hosts)
		disp := DisplayHosts(cidr, mode)
		st.mu.Lock()
		st.display = disp
		st.changes = ch
		st.scanning = false
		st.mu.Unlock()
		logf("scan done %s: %d live (+%d new, %d back, -%d offline) in %s",
			cidr, len(hosts), ch.New, ch.Returned, ch.Offline, time.Since(t0).Round(time.Millisecond))
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
	hosts := make([]DispHost, len(st.display))
	copy(hosts, st.display)
	changes := st.changes
	scanning := st.scanning
	scanSub := st.scanSub
	active := st.active
	mode := st.sortMode
	st.mu.Unlock()

	online, newCount := 0, 0
	for _, d := range hosts {
		if d.Online {
			online++
		}
		if d.New && d.Online {
			newCount++
		}
	}

	// title + status line
	if scanning {
		systray.SetTitle("📡 ⏳")
		mTitleStatus.SetTitle("⏳ Scanning " + scanSub + "…")
		mTitleStatus.Show()
		mScanNow.Disable()
	} else {
		t := fmt.Sprintf("📡 %d", online)
		if newCount > 0 {
			t += " •" // dot = there are new devices
		}
		systray.SetTitle(t)
		mTitleStatus.Hide()
		mScanNow.Enable()
	}
	if active != "" {
		mScanNow.SetTitle("Scan active subnet (" + active + ")")
	}

	// changes summary
	if !scanning && changes.Any() {
		mChanges.SetTitle(fmt.Sprintf("Changes: +%d new · %d back · −%d offline", changes.New, changes.Returned, changes.Offline))
		mChanges.Show()
	} else {
		mChanges.Hide()
	}

	setCheck(mSortIP, "Sort by IP", mode == "ip")
	setCheck(mSortHost, "Sort by host", mode == "host")

	// host rows
	rowMu.Lock()
	for i, r := range hostRows {
		if i < len(hosts) {
			r.ip = hosts[i].IP
			r.item.SetTitle(hostTitle(hosts[i]))
			r.item.Show()
		} else {
			r.ip = ""
			r.item.Hide()
		}
	}
	rowMu.Unlock()

	// tailscale peers + advertised subnets (from the cached view refreshTS built)
	st.mu.Lock()
	tsConnected := st.tsConnected
	tsSelfIP := st.tsSelfIP
	peers := append([]TSPeer(nil), st.tsPeers...)
	st.mu.Unlock()
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

	switch {
	case len(peers) > 0:
		// We have a peer list (from the CLI or the Headscale control server).
		mTS.SetTitle("Tailscale (connected)")
		mTS.Show()
		mTSInfo.Hide()
	case tsConnected:
		// On the tailnet (interface has a 100.x IP) but no peer list available.
		mTS.SetTitle("Tailscale (connected)")
		mTS.Show()
		info := "(peer list unavailable)"
		if tsSelfIP != "" {
			info = "this device · " + tsSelfIP + "   (peer list unavailable)"
		}
		mTSInfo.SetTitle(info)
		mTSInfo.Show()
	default:
		mTS.SetTitle("Tailscale (not connected)")
		mTS.Show()
		mTSInfo.SetTitle("(not connected)")
		mTSInfo.Show()
	}
	for i, it := range peerItems {
		if i < len(peers) {
			it.SetTitle(peerTitle(peers[i]))
			it.Show()
		} else {
			it.Hide()
		}
	}

	// JANUS section: the tag:fleet peers, shown prominently under the toggle.
	if mJanusHeader != nil {
		var janus []TSPeer
		for _, p := range peers {
			if p.hasTag("tag:fleet") {
				janus = append(janus, p)
			}
		}
		rowMu.Lock()
		for i, it := range janusItems {
			if i < len(janus) {
				it.SetTitle(janusTitle(janus[i]))
				it.Show()
			} else {
				it.Hide()
			}
		}
		rowMu.Unlock()
		if len(janus) > 0 {
			mJanusHeader.SetTitle(fmt.Sprintf("  %s (%d)", fleetPeersHeader, len(janus)))
			mJanusHeader.Show()
		} else {
			mJanusHeader.Hide()
		}
	}

	// discovered Loxone Miniservers
	st.mu.Lock()
	loxs := append([]LoxoneDevice(nil), st.loxone...)
	st.mu.Unlock()
	rowMu.Lock()
	for i, lr := range loxRows {
		if i < len(loxs) {
			d := loxs[i]
			lr.dev = d
			lr.item.SetTitle(fmt.Sprintf("%s · %s:%d · v%s", d.Name, d.IP, d.Port, d.Version))
			lr.item.Show()
		} else {
			lr.dev = LoxoneDevice{}
			lr.item.Hide()
		}
	}
	rowMu.Unlock()
	if len(loxs) > 0 {
		mLoxStatus.SetTitle(fmt.Sprintf("%d found", len(loxs)))
		mLoxStatus.Show()
	}
}

func setCheck(it *systray.MenuItem, label string, on bool) {
	if on {
		it.SetTitle("✓ " + label)
	} else {
		it.SetTitle("  " + label)
	}
}

func hostTitle(h DispHost) string {
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
	if h.New && h.Online {
		badge += "  · NEW"
	}
	if !h.Online {
		badge += "  · offline " + since(h.LastSeen)
	}
	return fmt.Sprintf("%-15s  %-24s%s", h.IP, name, badge)
}

func since(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
}

func peerTitle(p TSPeer) string {
	status := "online"
	if !p.Online {
		status = "offline"
	}
	extra := ""
	if p.hasTag("tag:fleet") {
		extra += "  🛰 JANUS"
	}
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

// janusTitle renders a fleet-tagged (JANUS) peer for the dedicated section,
// with a satellite badge so it stands out.
func janusTitle(p TSPeer) string {
	status := "online"
	if !p.Online {
		status = "offline"
	}
	name := p.Name
	if name == "" {
		name = "—"
	}
	return fmt.Sprintf("  🛰 %-22s  %-15s  %s", name, p.IP, status)
}
