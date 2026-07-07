# network-utility

Cross-platform network tools that live in your **menu bar / system tray** — built in
Go with [`getlantern/systray`](https://github.com/getlantern/systray), running on
**macOS and Windows** from one codebase.

> https://github.com/zanlah/network-utility

| Tool | What it does |
|---|---|
| **[Ports monitor](utilities/systray-ports)** | Lists listening TCP ports (grouped for web dev) with one-click kill (SIGTERM / SIGKILL). |
| **[Subnet scanner](utilities/systray-netscan)** | Scans a subnet for live hosts; detects **PLCs & Loxone Miniservers**, resolves **Windows/mDNS/NetBIOS** names, lists **Tailscale** peers, and scans subnets advertised over Tailscale. |
| **[Key swap](utilities/systray-keyswap)** | Swaps **Ctrl ⇄ ⊞ Win** with a live toggle, so a Mac keyboard / muscle memory feels normal on Windows (e.g. in a VM). Windows only. |

## Platform support

| | macOS | Windows |
|---|:---:|:---:|
| Ports monitor | ✅ | ✅ |
| Subnet scanner | ✅ | ✅ |
| Key swap | — | ✅ |

Each tool is one Go binary. The tray UI and all logic are shared; only a small set
of OS-specific calls live behind build tags (`*_darwin.go` / `*_windows.go`).

## Repository layout

```
utilities/
  systray-ports/      Ports monitor    (Go, macOS + Windows)
  systray-netscan/    Subnet scanner   (Go, macOS + Windows)
  systray-keyswap/    Key swap         (Go, Windows; stub on macOS/Linux)
```

Each tool has its own README with full details, build flags, and design notes.

## Installation

**Prerequisite (both OSes):** install **[Go 1.21+](https://go.dev/dl)**, then get the code:

```sh
git clone https://github.com/zanlah/network-utility.git
cd network-utility
```

### One command, any OS

From the repo root:

```sh
go run ./installer install
```

Same command on **macOS, Windows, and Linux**. It runs an interactive setup (like
`npx shadcn add`) — a checkbox picker for the tools, then a couple of quick questions:

```
Which tools do you want to install?
↑/↓ move · space toggle · a all · enter confirm

❯ ◉ 🔌 Ports monitor    — listening TCP ports + one-click kill
  ◉ 📡 Subnet scanner   — live hosts, PLC/Loxone detection, Tailscale peers
  ◯ ⌨️ Key swap         — swap Ctrl ⇄ ⊞ Win for VMs (Windows only)
  ◯ 🖥️ RustDesk         — remote-desktop client (self-hosted)

Install location [~/Applications/network-utility]:
Start automatically at login? [Y/n]:
Start now? [Y/n]:
```

Use **↑/↓** to move, **space** to check/uncheck, **a** to toggle all, **enter** to
confirm. **Key swap** and **RustDesk** start unchecked — tick them if you want them.
RustDesk (macOS/Windows/Linux) is fetched from its official releases and, when you have
the deployment password, preconfigured for the self-hosted server. Press **Enter** to accept each of the remaining defaults. Then it builds only
the tools you picked, installs them, and wires up autostart so they come back at every
login:

| OS | Default location | Autostart mechanism |
|---|---|---|
| macOS | `~/Applications/network-utility/` | **LaunchAgent** (`~/Library/LaunchAgents/si.viptronik.*.plist`) — also restarts on crash |
| Windows | `%LOCALAPPDATA%\network-utility\` | per-user **Run** registry key (`HKCU\…\CurrentVersion\Run`) — no admin, no Startup-folder shuffling |
| Linux | `~/.local/share/network-utility/` | XDG autostart entry (`~/.config/autostart/*.desktop`) |

`🔌` and `📡` appear in the menu bar / system tray within a second or two.

> **Windows:** just open **PowerShell** in the repo folder and run the one command above
> — no `.exe` juggling, no `shell:startup` shortcuts. (Go must be installed.)

**Skip the prompts** with flags (handy for scripting or a second machine):

```sh
go run ./installer install --apps ports          # just the ports monitor
go run ./installer install --apps netscan --dir "D:\tools\netutil"
go run ./installer install -y                    # accept every default, no prompts
go run ./installer install --no-autostart --no-start   # build + place only
```

| Flag | Meaning |
|---|---|
| `--apps ports,netscan` | which tools (also `all`; default: all) |
| `--dir <path>` | install location (default: the per-OS folder above) |
| `--no-autostart` | don't register login autostart |
| `--no-start` | don't launch right after installing |
| `-y`, `--yes` | accept all defaults, never prompt |

Any flag you omit is asked interactively (or takes its default when input isn't a terminal).

**Grant permission on first scan:** the subnet scanner needs local-network access.
On macOS you'll get a **Local Network** prompt — click **Allow** (or later: System
Settings → Privacy & Security → Local Network). On Windows, allow the **Firewall**
prompt on your private network. Without it, scans find nothing.

**Uninstall (any OS):**

```sh
go run ./installer uninstall
```

Stops both tools, removes the autostart entries and binaries. (Settings are left in
place; delete the install folder above to purge them too.) If you installed to a custom
location, pass it back: `go run ./installer uninstall --dir <path>`.

> `make setup` / `make unsetup` are shorthands for the two commands above (macOS/Linux,
> where `make` is available). On macOS you built the binaries yourself, so there's no
> Gatekeeper "unidentified developer" warning.

---

### All `make` targets

```sh
make setup        # any OS: build + install + autostart (go run ./installer install)
make unsetup      # any OS: stop + remove autostart + binaries
make build        # build both for the current OS into ./bin
make windows      # cross-build both as .exe into ./bin
make run-ports    # run one in the foreground (quick test, Ctrl-C to stop)
make run-netscan
make clean        # remove ./bin
```

> `make install` / `make uninstall` still exist as the macOS-only launchd path, but
> `go run ./installer install` supersedes them and works everywhere.

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
