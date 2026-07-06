# network-utility

Cross-platform network tools that live in your **menu bar / system tray** — built in
Go with [`getlantern/systray`](https://github.com/getlantern/systray), running on
**macOS and Windows** from one codebase.

> https://github.com/zanlah/network-utility

| Tool | What it does |
|---|---|
| **[Ports monitor](utilities/systray-ports)** | Lists listening TCP ports (grouped for web dev) with one-click kill (SIGTERM / SIGKILL). |
| **[Subnet scanner](utilities/systray-netscan)** | Scans a subnet for live hosts; detects **PLCs & Loxone Miniservers**, resolves **Windows/mDNS/NetBIOS** names, lists **Tailscale** peers, and scans subnets advertised over Tailscale. |

## Platform support

| | macOS | Windows |
|---|:---:|:---:|
| Ports monitor | ✅ | ✅ |
| Subnet scanner | ✅ | ✅ |

Each tool is one Go binary. The tray UI and all logic are shared; only a small set
of OS-specific calls live behind build tags (`*_darwin.go` / `*_windows.go`).

## Repository layout

```
utilities/
  systray-ports/      Ports monitor    (Go, macOS + Windows)
  systray-netscan/    Subnet scanner   (Go, macOS + Windows)
```

Each tool has its own README with full details, build flags, and design notes.

## Quick start

Needs **Go 1.21+**.

```sh
# Ports monitor
cd utilities/systray-ports
go build -o systray-ports . && ./systray-ports          # macOS
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o systray-ports.exe .   # Windows

# Subnet scanner
cd utilities/systray-netscan
go build -o systray-netscan . && ./systray-netscan
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o systray-netscan.exe .
```

Each app runs as a persistent tray process — launch it from a terminal, or wire up
autostart (a LaunchAgent on macOS, a Startup shortcut / Task Scheduler entry on
Windows).

## Architecture

Both tools follow the same shape:

```
main.go                 PRESENTER — builds the tray menu, handles clicks. One file,
                        identical on every OS.
*.go (shared)           the logic: collectors, scanning, fingerprinting, parsing.
*_darwin.go             macOS ADAPTER   (//go:build darwin)
*_windows.go            Windows ADAPTER (//go:build windows)
```

The presenter calls plain functions (`listListeners()`, `pingHost()`, …); Go's build
tags link in the correct adapter for the target OS at compile time. Result: write the
UI and logic once, keep the handful of genuinely OS-specific commands in two small
files.

## Highlights (Subnet scanner)

- **Device names, best-effort:** reverse DNS → mDNS/Bonjour (`.local`) → **NetBIOS**
  (Windows PC names like `IGOR-PC`) → HTTP `<title>`/Server header for web devices.
- **PLC / Loxone detection:** industrial port fingerprint (S7 102 / Modbus 502 /
  EtherNet/IP 44818) + Loxone via its `/dev/cfg/api` endpoint (with firmware), plus
  MAC/OUI vendor lookup. An open port names the *protocol*, not the vendor — so port
  102 reads `S7/ISO-TSAP (likely Siemens)`, a hint, not a positive ID.
- **Tailscale (native, no jq/python):** peer list with online/offline, subnet-router
  and exit-node badges, and one-click scanning of advertised subnets.
- **Adaptive sweep:** fast on a local LAN, gentle on routed/Tailscale subnets so a
  wide ping burst doesn't overrun the subnet router.

## Report a bug

Both apps have a **Report bug…** tray item — it copies a diagnostics report
(OS/version, state, recent logs) to your clipboard and opens a pre-filled email.

## Notes

- The tray shows a text title next to the clock on **macOS**; on **Windows** the tray
  is icon-only, so counts surface in the tooltip.
- The subnet scanner sweeps a `/24`; on macOS its first scan triggers a **Local
  Network** privacy prompt — allow it or scans return nothing.
