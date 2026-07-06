# SwiftBar Network Utilities

Two [SwiftBar](https://github.com/swiftbar/SwiftBar) menu-bar plugins for macOS,
aimed at web-dev and network/automation work. Both are plain `/bin/bash` (works on
the stock macOS **bash 3.2**) and use only tools that ship with macOS — no Homebrew
required for the core features.

| Plugin | What it does |
|---|---|
| **[`ports.10s.sh`](ports.10s.sh)** | Lists listening TCP ports in the menu bar, grouped for web development, with one-click kill (SIGTERM / SIGKILL). |
| **[`netscan.1h.sh`](netscan.1h.sh)** | Scans a subnet for live hosts; detects **PLCs & Loxone Miniservers**, lists **Tailscale** peers, auto-detects your networks, and can scan subnets advertised over Tailscale. |

> **Platform:** macOS only (built/tested on Apple Silicon, macOS 15). SwiftBar is a
> macOS app; these scripts also use macOS-specific tools, so they do **not** run on
> Windows or Linux as-is.

---

## Table of contents

- [Requirements](#requirements)
- [Installation](#installation)
- [Plugin 1 — Active Ports (`ports.10s.sh`)](#plugin-1--active-ports-ports10ssh)
- [Plugin 2 — Subnet Scanner (`netscan.1h.sh`)](#plugin-2--subnet-scanner-netscan1hsh)
  - [Menu walkthrough](#menu-walkthrough)
  - [Detecting PLCs & Loxone](#detecting-plcs--loxone)
  - [Tailscale integration](#tailscale-integration)
  - [How scanning works](#how-scanning-works)
- [SwiftBar syntax reference](#swiftbar-syntax-reference)
- [Customizing](#customizing)
- [Troubleshooting](#troubleshooting)
- [Files & state](#files--state)

---

## Requirements

- **macOS** with **[SwiftBar](https://github.com/swiftbar/SwiftBar)** installed.
- Everything the plugins need is built into macOS: `bash`, `lsof`, `ps`, `kill`,
  `ping`, `arp`, `ifconfig`, `route`, `dig`, `nc`, `curl`, `dscacheutil`,
  `osascript`.
- **Optional:** `jq` **or** `python3` — only used to show Tailscale subnet-router /
  exit-node badges. `python3` comes with the Xcode Command Line Tools; without
  either, everything else still works.
- **Optional:** Tailscale (CLI or the app) for the Tailscale section.

## Installation

Both plugins install the same way.

1. **Install SwiftBar**

   ```sh
   brew install --cask swiftbar
   ```

   …or download the `.dmg` from the [releases page](https://github.com/swiftbar/SwiftBar/releases)
   and drag it to `/Applications`.

2. **Pick a plugin folder.** SwiftBar asks for one on first launch; a common choice:

   ```sh
   mkdir -p ~/SwiftBar
   ```

   (Change it later via SwiftBar → **Preferences → Plugin Folder**.)

3. **Copy the plugins in and make them executable** (SwiftBar ignores non-executable files):

   ```sh
   cp ports.10s.sh netscan.1h.sh ~/SwiftBar/
   chmod +x ~/SwiftBar/ports.10s.sh ~/SwiftBar/netscan.1h.sh
   ```

4. **Load them:** SwiftBar → **Refresh All** (or wait for the auto-refresh).

**The number in the filename is the refresh interval.** `ports.10s.sh` re-renders
every 10 seconds; `netscan.1h.sh` every hour (it only reads a cache, so it's cheap —
scanning is manual/background). Rename to change it, e.g. `ports.30s.sh`,
`netscan.15m.sh`.

### Coexisting with a menu-bar manager (Ice, Bartender, etc.)

SwiftBar is an ordinary `NSStatusItem` app, so managers like **Ice** handle it like
any other icon. If an icon lands in the overflow, open **Ice → arrange menu bar
items** and drag SwiftBar's item into the always-visible zone. No special config.

---

## Plugin 1 — Active Ports (`ports.10s.sh`)

Shows listening TCP ports in the menu bar, focused on web-dev processes (Node,
Python, PHP, Docker, nginx, databases…), with one-click kill.

### What it shows

- **Menu bar:** an icon + the number of listening TCP ports.
- **Dropdown**, split into two sections:
  - **Web Dev Ports** — common dev ports (3000, 5173, 8080, 5432, 6379, …), marked `◆`.
  - **Other / System Ports** — everything else.
- **Each row:** a per-process-type icon, the process name, `:port`, and PID.
- **Each row's submenu:** the full command line, working directory, and actions:
  **Terminate (SIGTERM)**, **Force Kill (SIGKILL)**, **Copy port**. After a kill the
  menu auto-refreshes.

### How it works

- Enumerates ports with `lsof -nP -iTCP -sTCP:LISTEN`, parsed with `awk`.
- Deduplicates by `pid:port` (a process listening on IPv4 + IPv6 shows once).
- Icon/color are matched on `lsof`'s short command name (reliable); the display
  name comes from `ps` (nicer, un-truncated).
- Kill actions call `/bin/kill` directly with `refresh=true`.

### Permissions note

`lsof` run as your user sees your own processes and most system listeners, but
**ports owned by other users may not appear**. Showing everything would require
`sudo`, which isn't appropriate for an auto-refreshing menu-bar item — the current
output is the right choice for day-to-day web-dev use.

---

## Plugin 2 — Subnet Scanner (`netscan.1h.sh`)

Scans a subnet for live hosts and lists them (IP · hostname · MAC · device type). It
saves subnets for reuse, auto-detects the networks you're on, flags industrial gear,
and integrates with Tailscale.

**Key idea:** the menu renders instantly from a **cache**; it never scans on its own.
A real scan only runs when you click a scan item, and it runs **in the background**.

### Menu walkthrough

- **Refresh view** — re-render from cache (no scan).
- **Sort by IP / Sort by host** — reorder the host list instantly (no re-scan). IP is
  numeric; host is names A→Z, then unnamed hosts by IP. The choice is remembered.
- **Active + Scan now** — the currently selected subnet and a one-click scan.
- **Live hosts** — results of the last scan; each host's submenu has **Copy IP**,
  **Open `http://`**, **Ping in Terminal**, **SSH**. Industrial devices are flagged
  (see below).
- **Tailscale** — peer list and advertised-subnet scanning (see below).
- **Detected on this Mac** — every active private-IP interface with its computed CIDR
  (e.g. `en0 192.168.1.0/24`); submenu: *Set active & scan* or *Save to list*. Your
  UTM bridged/shared networks each appear here.
- **Saved subnets** — click to set active & scan, or remove. **Add subnet…** prompts
  for a CIDR (AppleScript dialog); **Edit list in editor…** opens the list file.

While a scan runs, the menu-bar icon becomes an amber ↻ spinner and a
`⏳ Scanning <subnet>… (Ns)` banner appears. When the scan finishes it clears itself
and asks SwiftBar to re-render (via `swiftbar://refreshplugin`), so results appear
without a manual refresh. Only one scan runs at a time; the cache is written
atomically and de-duplicated so overlapping scans can't create duplicate rows.

> **macOS closes any menu when you click an item** — that's the OS, not the plugin.
> Because scans run in the background and the menu re-renders itself when done, just
> reopen the menu to see results.

### Detecting PLCs & Loxone

Each host is classified two ways; industrial devices get a `cpu` icon, amber tint,
and a `⚙ <kind>` badge (details in the submenu):

- **MAC / OUI vendor** *(local LAN only)* — the MAC's manufacturer prefix. Recognises
  **Loxone** (`50:4F:94`, `78:52:49`), Siemens, Beckhoff, WAGO, Schneider, Rockwell,
  Phoenix Contact, B&R, Moxa, plus Ubiquiti / Raspberry Pi. Starter table in
  `oui_label()` — extend it from [maclookup.app](https://maclookup.app). Routed hosts
  (e.g. via Tailscale) have no MAC on your segment, so this can't see them.
- **Industrial port fingerprint** *(works everywhere, incl. routed subnets)* — probes
  each host with `nc`: **102** → Siemens S7, **502** → Modbus, **44818** →
  EtherNet/IP, **80** → Loxone. Loxone is confirmed via its unauthenticated
  `/dev/cfg/api` endpoint, which also yields the **firmware version** (e.g.
  `Loxone Miniserver 17.0.3.31`). Ports live in `PLC_PORTS`; toggle the whole feature
  with `PROBE_PLC` at the top of the script.

### Tailscale integration

Appears when the Tailscale CLI (or app at `/Applications/Tailscale.app`) is present.

- **Connected:** shows the tailnet (`100.64.0.0/10`) and lists every peer from
  `tailscale status` (IP · name · online/offline; offline dimmed), each with Copy IP /
  http / ping / SSH.
- **Stopped:** a one-click *Open Tailscale*.
- **Subnet routers & exit nodes** are flagged: `⇄ 192.168.1.0/24` = advertises a subnet
  route, `⇱ exit` = offers an exit node (from `tailscale status --json` via `jq`/`python3`).
- **Scan an advertised subnet:** each `⇄` route is a one-click *Scan advertised
  subnet…* — it sweeps that remote `/24` **through the tailnet** and drops results into
  Live hosts. Reaching a *remote* subnet requires route acceptance on this Mac
  (Tailscale app toggle, or `tailscale set --accept-routes`).

Peers are listed directly rather than ping-swept — a tailnet is a `/10` with hosts
scattered across it, so a sweep would be huge and wrong.

### How scanning works

- A scan is a parallel ping sweep of the subnet's `/24`, then per-host enrichment: MAC
  from `arp`, hostname from the system resolver with an **mDNS/Bonjour** fallback
  (`dig -x … @224.0.0.251`, resolving `.local` names), plus the PLC fingerprint.
- **Discovery adapts to the path.** A directly-attached LAN is swept fast (60 pings in
  flight). A **routed** subnet (Tailscale `utun`, VPN) is swept gently — 20 in flight,
  2 packets each — because a wide ping burst overruns the subnet router and drops
  replies. This matters: on a real routed `/24`, the fast setting found **28** hosts;
  the gentle setting found **103**. Cost: a routed industrial `/24` can take 60–90s; a
  local LAN scan stays ~10s.

### Notes / limits

- **Local Network privacy:** on macOS 14/15 the *first* scan triggers a "SwiftBar
  wants to find devices on your local network" prompt — allow it under **System
  Settings → Privacy & Security → Local Network**, or scans return nothing.
- **Scans a `/24`.** It sweeps `.1–.254` of the subnet's first three octets — covers
  virtually all home/office/UTM/industrial LANs. A saved `/16` is accepted but only its
  containing `/24` is swept.
- Hostnames are best-effort — devices that don't advertise mDNS show `–`.

---

## SwiftBar syntax reference

Handy when customizing. Every menu line is `Text | key=value key=value …`.

| Piece | Meaning |
|---|---|
| First line before `---` | The menu-bar title. |
| `---` | Separator. In a submenu, add `--` per level (a submenu separator is `-----`). |
| `--Text` | Submenu item (one level of nesting). |
| `bash="/bin/kill" param1="-TERM" param2="1234"` | Run a command. Params **must** be numbered in order. |
| `terminal=false` | Run silently, no Terminal window. |
| `refresh=true` | Re-run the plugin after the action (menu updates). |
| `color=#RRGGBB` / `color=red` | Text (and monochrome SF Symbol) color. |
| `sfimage=<name>` | An [SF Symbol](https://developer.apple.com/sf-symbols/) as the row icon. |
| `sfconfig=<base64 JSON>` | The only way to size SF Symbols in SwiftBar 2.x (see below). |
| `font=Menlo size=13` | Monospaced font keeps columns aligned. |

### Sizing SF Symbols (gotcha)

SwiftBar 2.x **ignores `sfsize`/`size` for SF Symbols**
([issue #245](https://github.com/swiftbar/SwiftBar/issues/245)). The only working
lever is `sfconfig` — a base64-encoded JSON with `scale` (`small`/`medium`/`large`)
and `weight`. **`renderingMode` and `colors` must be present** or SwiftBar silently
drops the whole config. Both plugins build this once at the top (`SF_JSON` → `SF_CFG`);
flip `"scale":"small"` to `"medium"` to enlarge every icon.

## Customizing

Both scripts have a config block at the top.

**`ports.10s.sh`**
- Add a web-dev port → append to `WEBDEV_PORTS`.
- Add a process icon/color → a `case` branch in `proc_sfimage` and `proc_color`.
- Menu-bar icon → `MENU_SFIMAGE`. Global font/size → `MONO` / `MUTED`.

**`netscan.1h.sh`**
- Add a vendor OUI → `oui_label()` (extend from [maclookup.app](https://maclookup.app)).
- Change probed industrial ports → `PLC_PORTS`; turn probing off → `PROBE_PLC=0`.
- Sweep parallelism/packets → the `case` in `do_scan` (local vs routed).
- Global font/size → `MONO` / `MUTED`.

Browse SF Symbol names in Apple's free **SF Symbols** app (or
`brew install --cask sf-symbols`).

## Troubleshooting

- **Plugin doesn't appear** → it must be executable (`chmod +x`) and in the plugin
  folder; then SwiftBar → *Refresh All*.
- **A blank gap instead of an icon** → that SF Symbol name doesn't exist on your macOS
  version; pick another in the SF Symbols app.
- **Subnet scan returns nothing** → grant SwiftBar **Local Network** permission
  (System Settings → Privacy & Security), and check the subnet is reachable.
- **Tailscale badges (`⇄`/`⇱`) missing** → install `jq` or ensure `python3` runs
  (`python3 --version`); the peer list works without them.
- **Icons look too big/small** → adjust `sfconfig` scale (see above), not `sfsize`.

## Files & state

```
ports.10s.sh      # Active Ports plugin (stateless)
netscan.1h.sh     # Subnet Scanner plugin
README.md
```

The scanner keeps its state in `~/.config/swiftbar-netscan/`:

```
subnets.txt       # saved subnets, one CIDR per line
active.txt        # currently selected subnet
last_scan.tsv     # cached results: ip \t mac \t name \t tag \t ports
sort.txt          # "ip" or "host"
scanning.txt      # present only while a scan is running
```
