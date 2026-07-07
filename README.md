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

## Installation

**Prerequisite (both OSes):** install **[Go 1.21+](https://go.dev/dl)**, then get the code:

```sh
git clone https://github.com/zanlah/network-utility.git
cd network-utility
```

---

### 🍎 macOS

**1. Install (one command, from the repo root):**

```sh
make install
```

This builds both tools, puts the binaries in `~/Applications/network-utility/`, and
registers a **LaunchAgent** for each (`~/Library/LaunchAgents/si.viptronik.*.plist`)
so they start now, again at every login, and restart if they crash. `🔌` and `📡`
appear in the menu bar within a second or two.

**2. Grant permission:** the first time the subnet scanner runs a scan, macOS asks
to let it **find devices on your local network** — click **Allow** (or later:
System Settings → Privacy & Security → Local Network). Without it, scans find nothing.

**3. Done.** They're running and will come back after every reboot.

**Uninstall:**

```sh
make uninstall            # stops them + removes the LaunchAgents and binaries
rm -rf ~/Applications/network-utility   # optional: also delete settings/logs
```

> No Gatekeeper "unidentified developer" warning — because you built it yourself, the
> binary isn't quarantined.

**Manual alternative (no `make`):** `cd utilities/systray-ports && go build -o ~/Applications/network-utility/systray-ports .` (repeat for `systray-netscan`), run each binary, and add them to **System Settings → General → Login Items** for autostart.

---

### 🪟 Windows

Windows has no `launchd`, so install is a few explicit steps.

**1. Build the two `.exe` files.** Open **PowerShell** in the repo folder:

```powershell
cd utilities\systray-ports
go build -o systray-ports.exe .
cd ..\systray-netscan
go build -o systray-netscan.exe .
```

> Or build them on a Mac/Linux with `make windows` (outputs to `./bin`) and copy the
> `.exe`s over.

**2. Put them somewhere permanent**, e.g. create `C:\Program Files\network-utility\`
(or a folder in your user profile) and move both `.exe`s there.

**3. Run them** — double-click each `.exe`. Icons appear in the **system tray** (the
`^` overflow near the clock). Windows may pop a **Firewall** prompt on first scan —
allow it on your private network.

**4. Start automatically at login:** press **Win + R**, type `shell:startup`, Enter —
this opens your Startup folder. Create a shortcut to each `.exe` there (right-drag the
`.exe` → *Create shortcuts here*). They'll launch to the tray at every sign-in.

**Uninstall:** delete the shortcuts from the Startup folder and remove the `.exe`s.

> On Windows the tray shows **icons only** (no text label next to the clock), so the
> port/host counts appear in the **tooltip** when you hover, and inside the menu.

---

### All `make` targets

```sh
make install      # macOS: build + install + start (see above)
make uninstall    # macOS: stop + remove
make build        # build both for the current OS into ./bin
make windows      # cross-build both as .exe into ./bin
make run-ports    # run one in the foreground (quick test, Ctrl-C to stop)
make run-netscan
make clean        # remove ./bin
```

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
