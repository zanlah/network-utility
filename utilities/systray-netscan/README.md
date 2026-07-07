# systray-netscan — cross-platform POC

The subnet scanner (`netscan.1h.sh`) ported to a single Go tray app that runs on
**macOS and Windows**, same architecture as [`systray-ports`](../systray-ports).
Feature parity with the SwiftBar version's core, validated against a live network.

## Architecture

```
main.go               PRESENTER — tray menu, clicks, in-memory scan state.
scan.go               shared: ping sweep, PLC/Loxone fingerprint, OUI, sort.
netinfo.go            shared: interface/CIDR detection (net package).
tailscale.go          shared: `tailscale status --json` via encoding/json.
store.go              shared: saved subnets / active / sort (os.UserConfigDir).
platform_darwin.go    macOS ADAPTER  (//go:build darwin)
platform_windows.go   Windows ADAPTER (//go:build windows)
```

Only a handful of things are genuinely OS-specific and live in the two adapter
files: `pingHost`, `arpLookup`, `mdnsName`, `tailscaleBin`, `copyToClipboard`,
`openURL`, `promptSubnet`. Everything else is shared, portable Go.

## Why Go is *less* code than bash here

The bash version needed a lot of scaffolding that Go makes disappear:

| Bash version | Go version |
|---|---|
| `jq` / `python3` to parse Tailscale JSON | `encoding/json` (built in) |
| `nc` for port probes | `net.DialTimeout` |
| `ifconfig` + `route` + mask math | `net.Interfaces()` |
| `dig` for Loxone API | `net/http` |
| scanning-marker file + `swiftbar://refreshplugin` | a `bool` in a struct + `repaint()` |
| atomic temp-file writes to avoid concurrent-scan dupes | one goroutine, guarded state |

## Features (parity with the SwiftBar plugin)

- Subnet ping-sweep with **adaptive concurrency** (local 60/1 packet, routed 20/2 —
  the routed-Tailscale case that needed the gentle sweep).
- Per-host enrichment: **MAC + OUI vendor**, hostname (reverse DNS → mDNS →
  **NetBIOS** for Windows PC names like `IGOR-PC` → HTTP `<title>`/Server for web
  devices), and the
  **PLC/Loxone fingerprint** (S7/ISO-TSAP 102 / Modbus 502 / EtherNet/IP 44818 /
  Loxone via `/dev/cfg/api` with firmware version). An open port names the
  **protocol, not the vendor** — 102 shows as `S7/ISO-TSAP (likely Siemens)`, a hint,
  not a positive ID. Loxone and MAC/OUI vendor are the positive IDs.
- **Networks** menu aggregates detected interfaces + saved subnets + Tailscale
  **advertised routes** — all one-click scannable (that's how you scan a remote
  subnet through a subnet router).
- **Tailscale** submenu: peers with online/offline + `⇄ route` / `⇱ exit` badges.
- **Loxone** submenu: **Scan for Loxone (broadcast)** uses Loxone's UDP discovery
  (a `0x00` byte to :7070, replies on :7071) to find Miniservers **by identity,
  regardless of their IP** — so it catches a Miniserver sitting on a different subnet
  than the one you'd scan (installer static IPs, factory defaults), as long as it
  shares an L2 broadcast domain. Each hit shows name · IP:port · firmware, with
  **Open web UI**, **Copy IP**, and **Scan its subnet** (jump straight to sweeping the
  network you just discovered it on). Reaches every attached interface's broadcast
  address, not just the default route's.
- **Sort by IP / host**, **Add subnet…** (native dialog), background scan with a
  `📡 ⏳` title + `⏳ Scanning…` banner, persisted state.
- **Device tracking / history** (per subnet, in `inventory.json`):
  - Each device is remembered with **first-seen / last-seen**, keyed by a stable
    identity (**Loxone serial** → MAC → IP) so it survives even over Tailscale where
    there's no ARP MAC.
  - **Union + aging** instead of replace: a device missed by one (flaky, routed)
    scan doesn't vanish — it shows `offline Xm ago` and only drops from view after
    the aging window (30 min). So packet loss no longer looks like a device
    disappearing.
  - **What changed** is surfaced: a `Changes: +N new · M back · −K offline` line
    after each scan, a `· NEW` tag on newly-seen devices, and a `•` on the menu-bar
    icon while there are new devices.
  - **Bounded confirm-retry:** if a scan finds < 70% of the devices it knew were
    online, it re-scans **once** and merges the results — catching a bad routed
    sweep without looping or hiding a genuine outage.
- **Report bug…** — copies a diagnostics report (OS, version, detected networks,
  active state, and the last 300 log lines) to the clipboard and opens a pre-filled
  email to `zan.lah@viptronik.si`. Events, scan timings, Tailscale errors and any
  panic are logged to the ring buffer and to `<config>/systray-netscan/log.txt`.

## Build & run

```sh
go build -o systray-netscan . && ./systray-netscan          # macOS
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o systray-netscan.exe .   # Windows
```

Verify the scan logic (no GUI needed) against your own network:

```sh
go test -v -run TestLive -timeout 180s
```

## Trade-offs / not-yet-done (POC honesty)

- systray uses **fixed pools** of menu rows (`maxHosts`, `maxNets`, `maxPeers`) since
  items can't be cleanly added/removed at runtime — the same imperative-vs-declarative
  tax as `systray-ports`.
- **No colors / SF Symbols** per row — industrial devices are marked with a `⚙` text
  badge instead of the SwiftBar amber `cpu` icon.
- **Windows tray shows an icon, not text**, so the tool embeds `icon.ico` and calls
  `SetIcon()` (see `icon_windows.go`); the `📡 N` count/title is macOS-only.
- "Ping in Terminal" was dropped (terminal-spawning is fiddly per-OS); Copy IP / Open
  http / SSH are kept.
- No last-scan cache persisted to disk yet (state is in-memory; a restart starts empty).
- `scan_test.go` is a live-probe harness, not a hermetic unit test.
