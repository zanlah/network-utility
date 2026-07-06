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
- **Sort by IP / host**, **Add subnet…** (native dialog), background scan with a
  `📡 ⏳` title + `⏳ Scanning…` banner, persisted state.
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
- **Windows tray shows an icon, not text**, so the `📡 N` count is macOS-only there;
  you'd `SetIcon()` + tooltip on Windows.
- "Ping in Terminal" was dropped (terminal-spawning is fiddly per-OS); Copy IP / Open
  http / SSH are kept.
- No last-scan cache persisted to disk yet (state is in-memory; a restart starts empty).
- `scan_test.go` is a live-probe harness, not a hermetic unit test.
