#!/bin/bash
#
# <xbar.title>Subnet Scanner</xbar.title>
# <xbar.version>v1.0</xbar.version>
# <xbar.author>zanlah</xbar.author>
# <xbar.desc>Ping-sweeps a subnet for live hosts. Saves subnets, auto-detects the current network.</xbar.desc>
# <xbar.dependencies>bash,ping,arp,ifconfig,osascript</xbar.dependencies>
#
# <swiftbar.hideRunInTerminal>true</swiftbar.hideRunInTerminal>
# <swiftbar.hideLastUpdated>false</swiftbar.hideLastUpdated>
#
# Subnet scanner for SwiftBar. The menu RENDERS from a cache (instant); an actual
# ping sweep only runs when you click "Scan now" (the script calls itself with an
# argument). Filename netscan.1h.sh = the menu re-renders hourly; scanning is manual.
#
# NOTE: on macOS 14/15 the first scan triggers a "Local Network" privacy prompt.
# Allow SwiftBar under System Settings > Privacy & Security > Local Network.

export PATH="/usr/sbin:/sbin:/usr/bin:/bin:$PATH"

SELF="$0"                                                # path SwiftBar invokes us with
case "$SELF" in                                          # ensure it's absolute for self-calls
  /*) : ;;
  *)  SELF="$(cd "$(dirname "$SELF")" 2>/dev/null && pwd)/$(basename "$SELF")" ;;
esac
CONF_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/swiftbar-netscan"
SUBNETS_FILE="$CONF_DIR/subnets.txt"                     # one CIDR per line
ACTIVE_FILE="$CONF_DIR/active.txt"                       # currently selected subnet
CACHE_FILE="$CONF_DIR/last_scan.tsv"                     # results: ip \t mac \t name \t tag \t ports
SORT_FILE="$CONF_DIR/sort.txt"                           # "ip" (default) or "host"
SCAN_MARKER="$CONF_DIR/scanning.txt"                     # present while a scan runs: subnet \t HH:MM:SS \t epoch
mkdir -p "$CONF_DIR" 2>/dev/null

MONO="font=Menlo size=13"
MUTED="font=Menlo size=11 color=#8a8a8a"

# Tailscale CLI (PATH first, then the GUI/App Store app bundle). Responds even
# when Tailscale is stopped. Tailnet addresses live in the CGNAT range 100.64/10.
TS_BIN="$(command -v tailscale 2>/dev/null)"
[ -z "$TS_BIN" ] && [ -x "/Applications/Tailscale.app/Contents/MacOS/Tailscale" ] \
  && TS_BIN="/Applications/Tailscale.app/Contents/MacOS/Tailscale"

# SF Symbol size lever (see the ports plugin / SwiftBar issue #245). small is floor.
SF_JSON='{"renderingMode":"Hierarchical","colors":[],"scale":"small","weight":"regular"}'
SF_CFG="sfconfig=$(printf '%s' "$SF_JSON" | base64 | tr -d '\n')"

# PLC / industrial fingerprinting. 1 = probe each host's telltale ports (adds a
# few seconds to a scan); 0 = skip (just MAC/vendor + hostname). Works over routed
# links too (unlike MAC/OUI, which only resolves on your local L2 segment).
PROBE_PLC=1
PLC_PORTS="102 502 44818 80"   # S7(102) Modbus(502) EtherNet/IP(44818) web/Loxone(80)

# ---------------------------------------------------------------------------
# Subnet math (contiguous IPv4 masks only)
# ---------------------------------------------------------------------------

# mask_to_prefix 0xffffff00 -> 24
mask_to_prefix() {
  local hex="${1#0x}" dec p=0
  dec=$((16#$hex))
  while [ "$dec" -gt 0 ]; do p=$((p + (dec & 1))); dec=$((dec >> 1)); done
  echo "$p"
}

# net_addr <ip> <maskhex> -> network address (ip AND mask)
net_addr() {
  local ip="$1" hex="${2#0x}" i1 i2 i3 i4 m1 m2 m3 m4
  IFS=. read -r i1 i2 i3 i4 <<EOF
$ip
EOF
  m1=$((16#${hex:0:2})); m2=$((16#${hex:2:2})); m3=$((16#${hex:4:2})); m4=$((16#${hex:6:2}))
  printf '%s.%s.%s.%s' "$((i1 & m1))" "$((i2 & m2))" "$((i3 & m3))" "$((i4 & m4))"
}

# detected_cidrs -> lines "iface|network/prefix|ip (default?)" for private IPv4 ifaces
detected_cidrs() {
  local defif
  defif="$(route -n get default 2>/dev/null | awk '/interface:/{print $2}')"
  ifconfig 2>/dev/null | awk '
    /^[a-z0-9]+:/ { ifn=$1; sub(":","",ifn) }
    /inet / {
      ip=""; mask=""
      for (i=1;i<=NF;i++){ if($i=="inet") ip=$(i+1); if($i=="netmask") mask=$(i+1) }
      if (ip!="" && ip!="127.0.0.1" && mask!="") print ifn, ip, mask
    }' | while read -r ifn ip mask; do
      case "$ip" in
        10.*|192.168.*|172.1[6-9].*|172.2[0-9].*|172.3[0-1].*) : ;;
        *) continue ;;
      esac
      local pfx net star=""
      pfx="$(mask_to_prefix "$mask")"
      net="$(net_addr "$ip" "$mask")"
      [ "$ifn" = "$defif" ] && star=" (default route)"
      printf '%s|%s/%s|%s%s\n' "$ifn" "$net" "$pfx" "$ip" "$star"
  done
}

# sorted_hosts: emit cached host rows ordered per $SORT_MODE. "ip" = numeric by
# address; "host" = named hosts A→Z (case-insensitive), then unnamed ones by IP.
sorted_hosts() {
  local TAB; TAB="$(printf '\t')"
  {
    case "$SORT_MODE" in
      host)
        grep -v '^#' "$CACHE_FILE" | awk -F"$TAB" '$3!="–"' | sort -t"$TAB" -k3,3f
        grep -v '^#' "$CACHE_FILE" | awk -F"$TAB" '$3=="–"' | sort -t. -k1,1n -k2,2n -k3,3n -k4,4n
        ;;
      *)
        grep -v '^#' "$CACHE_FILE" | sort -t. -k1,1n -k2,2n -k3,3n -k4,4n
        ;;
    esac
  } | awk -F"$TAB" '!seen[$1]++'   # last-resort dedup (e.g. a cache from before the fix)
}

# ts_icon <os> -> an SF Symbol for a Tailscale peer's platform
ts_icon() {
  case "$1" in
    macOS|iOS)       echo "laptopcomputer" ;;
    linux)           echo "server.rack" ;;
    windows)         echo "pc" ;;
    android)         echo "candybarphone" ;;
    *)               echo "network" ;;
  esac
}

# ts_routes_map: for peers that advertise subnet routes or offer an exit node,
# emit "ip \t routes(csv) \t exit(1/0)". Uses jq, else python3, else nothing
# (the peer list still works without them — only these badges are skipped).
# Route/exit data isn't in the plain `status` text, only in `status --json`.
ts_routes_map() {
  local json
  json="$("$TS_BIN" status --json 2>/dev/null)"
  [ -z "$json" ] && return
  if command -v jq >/dev/null 2>&1; then
    printf '%s' "$json" | jq -r '
      (.Self, (.Peer // {} | .[]))
      | select(.TailscaleIPs != null) | . as $n
      | (($n.PrimaryRoutes // []) | map(select(. != "0.0.0.0/0" and . != "::/0"))) as $r
      | select(($r|length > 0) or ($n.ExitNodeOption == true))
      | [ $n.TailscaleIPs[0], ($r|join(",")), (if $n.ExitNodeOption then "1" else "0" end) ]
      | @tsv' 2>/dev/null
  elif python3 -c 'import json' 2>/dev/null; then
    printf '%s' "$json" | python3 -c '
import json, sys
try: d = json.load(sys.stdin)
except Exception: sys.exit(0)
nodes = ([d["Self"]] if d.get("Self") else []) + list((d.get("Peer") or {}).values())
for n in nodes:
    ips = n.get("TailscaleIPs") or []
    if not ips: continue
    r = [x for x in (n.get("PrimaryRoutes") or []) if x not in ("0.0.0.0/0", "::/0")]
    ex = bool(n.get("ExitNodeOption"))
    if r or ex:
        print("%s\t%s\t%s" % (ips[0], ",".join(r), "1" if ex else "0"))
' 2>/dev/null
  fi
}

# ---------------------------------------------------------------------------
# Persistence helpers
# ---------------------------------------------------------------------------

save_subnet() {
  [ -z "$1" ] && return
  touch "$SUBNETS_FILE"
  grep -qxF "$1" "$SUBNETS_FILE" 2>/dev/null || printf '%s\n' "$1" >> "$SUBNETS_FILE"
}

remove_subnet() {
  [ -f "$SUBNETS_FILE" ] || return
  grep -vxF "$1" "$SUBNETS_FILE" > "$SUBNETS_FILE.tmp" 2>/dev/null && mv "$SUBNETS_FILE.tmp" "$SUBNETS_FILE"
  [ "$(cat "$ACTIVE_FILE" 2>/dev/null)" = "$1" ] && : > "$ACTIVE_FILE"
}

add_subnet() {  # prompt via AppleScript (SwiftBar runs inside a GUI app, so this works)
  local in
  in="$(osascript -e 'text returned of (display dialog "Enter subnet as CIDR (e.g. 192.168.1.0/24):" default answer "192.168.1.0/24" with title "Subnet Scanner")' 2>/dev/null)" || return
  [ -z "$in" ] && return
  if printf '%s' "$in" | grep -Eq '^[0-9]{1,3}(\.[0-9]{1,3}){3}(/[0-9]{1,2})?$'; then
    save_subnet "$in"
  else
    osascript -e 'display notification "Not a valid subnet" with title "Subnet Scanner"' >/dev/null 2>&1
  fi
}

# ---------------------------------------------------------------------------
# The scan (parallel ping sweep of the /24 containing the subnet base)
# ---------------------------------------------------------------------------
do_scan() {
  local subnet="$1" base tmp ip mac name
  base="$(printf '%s' "$subnet" | awk -F'[./]' '{print $1"."$2"."$3}')"
  [ -z "$base" ] && return
  tmp="$(mktemp "${TMPDIR:-/tmp}/netscan.XXXXXX")"

  # Adapt to the path. A directly-attached L2 subnet handles a wide burst fine; a
  # routed one (Tailscale utun, VPN) drops replies under load, so go gentle and send
  # two packets. Measured on a routed /24: -P60/-c1 found 28 hosts, -P20/-c2 found 95.
  local sweep_if sweep_p sweep_c sweep_cmd
  sweep_if="$(route -n get "${base}.1" 2>/dev/null | awk '/interface:/{print $2}')"
  case "$sweep_if" in
    en*|bridge*|eth*|lo*) sweep_p=60; sweep_c=1 ;;   # local L2: fast
    *)                    sweep_p=20; sweep_c=2 ;;   # routed/VPN/unknown: gentle + retry
  esac
  sweep_cmd='ping -c'"$sweep_c"' -t2 -W800 "$1" >/dev/null 2>&1 && echo "$1"'
  seq 1 254 | sed "s#^#${base}.#" \
    | xargs -P "$sweep_p" -I{} sh -c "$sweep_cmd" _ {} \
    | sort -t. -k1,1n -k2,2n -k3,3n -k4,4n -u > "$tmp"

  # Enrich each live host (MAC + hostname) in parallel by re-invoking ourselves.
  # Write to a temp file and mv atomically so two overlapping scans can't interleave
  # their output into the cache; awk '!seen' drops any duplicate IP as a final guard.
  local out
  out="$(mktemp "${TMPDIR:-/tmp}/netscan.out.XXXXXX")"
  {
    printf '# subnet=%s ts=%s count=%s\n' "$subnet" "$(date '+%Y-%m-%d %H:%M')" "$(grep -c . "$tmp")"
    xargs -P 20 -I{} "$SELF" enrich {} < "$tmp" \
      | sort -t. -k1,1n -k2,2n -k3,3n -k4,4n \
      | awk -F'\t' '!seen[$1]++'
  } > "$out"
  mv -f "$out" "$CACHE_FILE"
  rm -f "$tmp"
}

# enrich <ip>: print "ip \t mac \t hostname". MAC from arp (populated by the sweep),
# hostname from the system resolver, falling back to an mDNS/Bonjour reverse query.
# oui_label <mac> -> vendor label from the first 3 octets. Starter set focused on
# automation gear + a few common vendors; extend from https://maclookup.app .
# (Only works for LAN hosts — routed hosts have no MAC on our segment.)
oui_label() {
  local p1 p2 p3 oui
  IFS=: read -r p1 p2 p3 _ <<EOF
$1
EOF
  oui="$(printf '%02X:%02X:%02X' "0x$p1" "0x$p2" "0x$p3" 2>/dev/null)" || return
  case "$oui" in
    50:4F:94|78:52:49)                            echo "Loxone" ;;
    00:1B:1B|00:0E:8C|00:1F:F8|00:0C:F1|8C:F3:19) echo "Siemens" ;;
    00:01:05)                                     echo "Beckhoff" ;;
    00:30:DE)                                     echo "WAGO" ;;
    00:80:F4|00:00:54)                            echo "Schneider" ;;
    00:00:BC|00:1D:9C)                            echo "Rockwell/AB" ;;
    00:A0:45)                                     echo "Phoenix Contact" ;;
    00:60:65)                                     echo "B&R" ;;
    00:90:E8)                                     echo "Moxa" ;;
    B8:27:EB|DC:A6:32|E4:5F:01|D8:3A:DD|28:CD:C1|2C:CF:67) echo "Raspberry Pi" ;;
    18:E8:29|24:5A:4C|74:AC:B9|FC:EC:DA|68:D7:9A) echo "Ubiquiti" ;;
    *)                                            echo "" ;;
  esac
}

# plc_fingerprint <ip> <openports> -> a device kind if it looks industrial, else ""
plc_fingerprint() {
  local ip="$1" ports=" $2 " kind=""
  case "$ports" in
    *" 102 "*)   kind="Siemens S7 PLC" ;;
    *" 502 "*)   kind="Modbus device" ;;
    *" 44818 "*) kind="EtherNet/IP PLC" ;;
  esac
  # Loxone: query its unauthenticated API (works over routed links, no OUI needed).
  # Returns e.g. <LL control="dev/cfg/api" value="{'snr':'50:4F:94:..','version':'17.0.3.31'..}">
  case "$ports" in
    *" 80 "*)
      if [ -z "$kind" ]; then
        local api ver
        api="$(curl -s -m 2 "http://$ip/dev/cfg/api" 2>/dev/null)"
        case "$api" in
          *'control="dev/cfg/api"'*)
            ver="$(printf '%s' "$api" | grep -oE '[0-9]+(\.[0-9]+){2,}' | head -1)"
            kind="Loxone Miniserver${ver:+ $ver}" ;;
          *)
            curl -s -m 2 "http://$ip/" 2>/dev/null | grep -qi loxone && kind="Loxone Miniserver" ;;
        esac
      fi ;;
  esac
  echo "$kind"
}

enrich_host() {
  local ip="$1" mac name vendor ports pp kind tag
  # MAC must contain ':' — routed/remote hosts (and "no entry") have none.
  mac="$(arp -n "$ip" 2>/dev/null | awk '{print $4}')"
  case "$mac" in *:*) : ;; *) mac="–" ;; esac
  name="$(dscacheutil -q host -a ip_address "$ip" 2>/dev/null | awk -F': ' '/^name:/{print $2; exit}')"
  if [ -z "$name" ]; then
    # +short still prints ";; connection timed out" to stdout on failure — drop it.
    name="$(dig +short +time=1 +tries=1 -x "$ip" @224.0.0.251 -p 5353 2>/dev/null \
      | grep -Ev '^;;|^$' | sed 's/\.$//' | head -1)"
  fi
  [ -z "$name" ] && name="–"

  vendor=""
  [ "$mac" != "–" ] && vendor="$(oui_label "$mac")"

  ports=""; kind=""
  if [ "$PROBE_PLC" = "1" ]; then
    for pp in $PLC_PORTS; do
      nc -z -G 1 -w 1 "$ip" "$pp" >/dev/null 2>&1 && ports="$ports $pp"
    done
    ports="${ports# }"
    kind="$(plc_fingerprint "$ip" "$ports")"
  fi
  # Loxone via OUI is authoritative
  [ "$vendor" = "Loxone" ] && kind="Loxone Miniserver"

  # tag: industrial classification wins, else OUI vendor
  tag="$kind"; [ -z "$tag" ] && tag="$vendor"; [ -z "$tag" ] && tag="–"
  printf '%s\t%s\t%s\t%s\t%s\n' "$ip" "$mac" "$name" "$tag" "${ports:-–}"
}

# ---------------------------------------------------------------------------
# Action dispatch: when SwiftBar clicks call us with an argument, do it and exit.
# ---------------------------------------------------------------------------
case "$1" in
  enrich)    enrich_host "$2"; exit 0 ;;
  sort)      printf '%s\n' "$2" > "$SORT_FILE"; exit 0 ;;
  # startscan: set active, drop a "scanning" marker, launch the real scan detached,
  # and return immediately so the click's refresh shows the "Scanning…" state at once.
  startscan)
    printf '%s\n' "$2" > "$ACTIVE_FILE"
    # Don't launch a second scan while one is running (concurrent writers duplicate
    # rows). Ignore a stale marker (>300s) left by an interrupted scan.
    if [ -s "$SCAN_MARKER" ]; then
      _me="$(awk -F'\t' 'NR==1{print $3}' "$SCAN_MARKER")"
      [ -n "$_me" ] && [ $(( $(date +%s) - _me )) -lt 300 ] && exit 0
    fi
    printf '%s\t%s\t%s\n' "$2" "$(date '+%H:%M:%S')" "$(date +%s)" > "$SCAN_MARKER"
    nohup "$SELF" _scan "$2" >/dev/null 2>&1 &
    disown 2>/dev/null
    exit 0 ;;
  # _scan: the detached worker. Does the slow sweep, clears the marker, then asks
  # SwiftBar to re-render so results appear without a manual refresh.
  _scan)
    do_scan "$2"
    rm -f "$SCAN_MARKER"
    # Ask SwiftBar to re-render. Param name varies across versions, so try both.
    _pn="$(basename "$SELF")"
    open "swiftbar://refreshplugin?plugin=$_pn" >/dev/null 2>&1
    open "swiftbar://refreshplugin?name=$_pn"   >/dev/null 2>&1
    exit 0 ;;
  scan)      do_scan "$2"; exit 0 ;;
  setactive) printf '%s\n' "$2" > "$ACTIVE_FILE"; do_scan "$2"; exit 0 ;;
  save)      save_subnet "$2"; exit 0 ;;
  add)       add_subnet; exit 0 ;;
  remove)    remove_subnet "$2"; exit 0 ;;
esac

# ---------------------------------------------------------------------------
# Default: render the menu (fast — reads cache + interface info, never scans)
# ---------------------------------------------------------------------------
ACTIVE=""
[ -s "$ACTIVE_FILE" ] && ACTIVE="$(head -1 "$ACTIVE_FILE")"

N=0
[ -s "$CACHE_FILE" ] && N="$(grep -c -v '^#' "$CACHE_FILE")"

SORT_MODE="ip"
[ -s "$SORT_FILE" ] && SORT_MODE="$(head -1 "$SORT_FILE")"

# Scan-in-progress state (marker written by startscan, cleared by the _scan worker)
SCANNING=""; SCAN_SUB=""; SCAN_AT=""; SCAN_ELAPSED=""
if [ -s "$SCAN_MARKER" ]; then
  SCANNING=1
  SCAN_SUB="$(awk -F'\t' 'NR==1{print $1}' "$SCAN_MARKER")"
  SCAN_AT="$(awk -F'\t' 'NR==1{print $2}' "$SCAN_MARKER")"
  SCAN_EPOCH="$(awk -F'\t' 'NR==1{print $3}' "$SCAN_MARKER")"
  [ -n "$SCAN_EPOCH" ] && SCAN_ELAPSED=$(( $(date +%s) - SCAN_EPOCH ))
fi

# Menu-bar title: a spinning-refresh icon (amber) while a scan is running.
if [ -n "$SCANNING" ]; then
  printf '%s… | sfimage=arrow.triangle.2.circlepath %s %s color=#e48e00\n' "$N" "$SF_CFG" "$MONO"
else
  printf '%s | sfimage=antenna.radiowaves.left.and.right %s %s\n' "$N" "$SF_CFG" "$MONO"
fi
echo "---"
printf 'Refresh view | sfimage=arrow.clockwise %s %s refresh=true\n' "$SF_CFG" "$MONO"

# Prominent scanning banner + auto-refresh note (menu updates itself when done).
if [ -n "$SCANNING" ]; then
  printf '⏳ Scanning %s…  (%ss) | %s color=#e48e00\n' "$SCAN_SUB" "${SCAN_ELAPSED:-0}" "$MONO"
  printf 'Started %s · updates automatically when done | %s\n' "$SCAN_AT" "$MUTED"
  printf 'Clear scanning state | sfimage=xmark %s %s bash="/bin/rm" param1="-f" param2="%s" terminal=false refresh=true\n' "$SF_CFG" "$MUTED" "$SCAN_MARKER"
fi

if [ -n "$ACTIVE" ]; then
  printf 'Active: %s | %s\n' "$ACTIVE" "$MONO"
  if [ -n "$SCANNING" ]; then
    printf 'Scan running… | sfimage=hourglass %s %s\n' "$SF_CFG" "$MUTED"
  else
    printf 'Scan now (%s) | sfimage=dot.radiowaves.left.and.right %s %s bash="%s" param1="startscan" param2="%s" terminal=false refresh=true\n' \
      "$ACTIVE" "$SF_CFG" "$MONO" "$SELF" "$ACTIVE"
  fi
else
  printf 'No active subnet — pick one below | %s\n' "$MUTED"
fi

if [ -s "$CACHE_FILE" ]; then
  HDR="$(head -1 "$CACHE_FILE")"
  TS="$(printf '%s' "$HDR" | sed -n 's/.*ts=\(.*\) count=.*/\1/p')"
  SN="$(printf '%s' "$HDR" | sed -n 's/.*subnet=\([^ ]*\).*/\1/p')"
  printf 'Last scan: %s · %s · %s hosts | %s\n' "$SN" "$TS" "$N" "$MUTED"
fi
echo "---"

# --- Live hosts ------------------------------------------------------------
echo "Live hosts | $MUTED"
# Sort toggle (render-time; no rescan needed)
ipck=""; hostck=""
if [ "$SORT_MODE" = "host" ]; then hostck="  ✓"; else ipck="  ✓"; fi
printf 'Sort by IP%s | sfimage=arrow.up.arrow.down %s %s bash="%s" param1="sort" param2="ip" terminal=false refresh=true\n' "$ipck" "$SF_CFG" "$MONO" "$SELF"
printf 'Sort by host%s | sfimage=arrow.up.arrow.down %s %s bash="%s" param1="sort" param2="host" terminal=false refresh=true\n' "$hostck" "$SF_CFG" "$MONO" "$SELF"
if [ "$N" -gt 0 ] 2>/dev/null; then
  sorted_hosts | while IFS="$(printf '\t')" read -r ip mac name tag ports; do
    icon="desktopcomputer"; colparam=""
    [ "${ip##*.}" = "1" ] && icon="wifi.router"
    # Industrial devices get a distinct icon + amber tint and their tag shown inline.
    badge=""
    case "$tag" in
      "–"|"") : ;;
      "Loxone Miniserver"|*PLC*|"Modbus device"|"EtherNet/IP"*)
        icon="cpu"; colparam="color=#e48e00"; badge="  ⚙ $tag" ;;
      *) badge="  · $tag" ;;   # a plain OUI vendor (e.g. Ubiquiti, Raspberry Pi)
    esac
    printf '%-15s %-20s%s | sfimage=%s %s %s %s\n' "$ip" "$name" "$badge" "$icon" "$SF_CFG" "$MONO" "$colparam"
    printf -- '--%s  ·  %s | %s\n' "$ip" "$name" "$MONO"
    [ "$tag" != "–" ]   && printf -- '--Device: %s | %s color=#e48e00\n' "$tag" "$MONO"
    [ "$ports" != "–" ] && printf -- '--Open industrial ports: %s | %s\n' "$ports" "$MUTED"
    printf -- '--MAC: %s | %s\n' "$mac" "$MUTED"
    printf -- '-----\n'
    printf -- '--Copy IP | sfimage=doc.on.clipboard %s %s bash="/bin/bash" param1="-c" param2="printf %s | pbcopy" terminal=false\n' "$SF_CFG" "$MONO" "$ip"
    printf -- '--Open http:// | sfimage=safari %s %s bash="/usr/bin/open" param1="http://%s" terminal=false\n' "$SF_CFG" "$MONO" "$ip"
    printf -- '--Ping in Terminal | sfimage=apple.terminal %s %s bash="/sbin/ping" param1="%s" terminal=true\n' "$SF_CFG" "$MONO" "$ip"
    printf -- '--SSH | sfimage=terminal %s %s bash="/usr/bin/open" param1="ssh://%s" terminal=false\n' "$SF_CFG" "$MONO" "$ip"
  done
else
  echo "— no results yet — run a scan — | $MUTED"
fi
echo "---"

# --- Tailscale -------------------------------------------------------------
if [ -n "$TS_BIN" ]; then
  echo "Tailscale | $MUTED"
  TS_OUT="$("$TS_BIN" status 2>/dev/null)"
  if printf '%s' "$TS_OUT" | head -1 | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+'; then
    printf 'Tailnet  100.64.0.0/10  ·  connected | %s color=#3fb950\n' "$MONO"
    TS_ROUTES="$(ts_routes_map)"   # ip\troutes\texit for subnet routers / exit nodes
    # Peer lines: IP  hostname  user-or-dns  os  <status...>
    printf '%s\n' "$TS_OUT" | awk '
      /^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+/ {
        st = (index($0,"offline")) ? "offline" : "online"
        print $1 "\t" $2 "\t" $4 "\t" st
      }' | while IFS="$(printf '\t')" read -r ip name os st; do
        icon="$(ts_icon "$os")"
        name="$(printf '%s' "$name" | cut -c1-22)"
        # online renders in the default (full-contrast) color; only offline is dimmed
        colparam=""; [ "$st" = "offline" ] && colparam="color=#8a8a8a"
        # overlay subnet-route / exit-node info for this IP
        routes=""; exitnode=""
        IFS="$(printf '\t')" read -r routes exitnode <<EOF
$(printf '%s' "$TS_ROUTES" | awk -F'\t' -v ip="$ip" '$1==ip{print $2"\t"$3; exit}')
EOF
        badge=""
        [ -n "$routes" ] && badge="  ⇄ $routes"
        [ "$exitnode" = "1" ] && badge="$badge  ⇱ exit"
        printf '%-15s %-22s %-8s%s | sfimage=%s %s %s %s\n' "$ip" "$name" "$st" "$badge" "$icon" "$SF_CFG" "$MONO" "$colparam"
        printf -- '--%s  ·  %s  (%s) | %s\n' "$ip" "$name" "$os" "$MONO"
        # Each advertised subnet becomes a one-click scan (routed via the tailnet).
        for r in $(printf '%s' "$routes" | tr ',' ' '); do
          [ -z "$r" ] && continue
          printf -- '--Scan advertised subnet %s | sfimage=dot.radiowaves.left.and.right %s %s color=#3fb950 bash="%s" param1="startscan" param2="%s" terminal=false refresh=true\n' \
            "$r" "$SF_CFG" "$MONO" "$SELF" "$r"
        done
        [ "$exitnode" = "1" ] && printf -- '--Offers exit node | %s color=#3fb950\n' "$MONO"
        printf -- '-----\n'
        printf -- '--Copy IP | sfimage=doc.on.clipboard %s %s bash="/bin/bash" param1="-c" param2="printf %s | pbcopy" terminal=false\n' "$SF_CFG" "$MONO" "$ip"
        printf -- '--Open http:// | sfimage=safari %s %s bash="/usr/bin/open" param1="http://%s" terminal=false\n' "$SF_CFG" "$MONO" "$ip"
        printf -- '--Ping in Terminal | sfimage=apple.terminal %s %s bash="/sbin/ping" param1="%s" terminal=true\n' "$SF_CFG" "$MONO" "$ip"
        printf -- '--SSH | sfimage=terminal %s %s bash="/usr/bin/open" param1="ssh://%s" terminal=false\n' "$SF_CFG" "$MONO" "$ip"
    done
  else
    printf 'Tailscale is stopped | %s\n' "$MONO"
    printf 'Open Tailscale to connect | sfimage=power %s %s bash="/usr/bin/open" param1="-a" param2="Tailscale" terminal=false refresh=true\n' "$SF_CFG" "$MONO"
  fi
  echo "---"
fi

# --- Auto-detected subnets -------------------------------------------------
echo "Detected on this Mac | $MUTED"
FOUND=0
detected_cidrs | while IFS='|' read -r ifn cidr ipinfo; do
  FOUND=1
  printf '%s  %s  (%s) | %s\n' "$ifn" "$cidr" "$ipinfo" "$MONO"
  printf -- '--Set active & scan | sfimage=dot.radiowaves.left.and.right %s %s bash="%s" param1="startscan" param2="%s" terminal=false refresh=true\n' "$SF_CFG" "$MONO" "$SELF" "$cidr"
  printf -- '--Save to list | sfimage=plus %s %s bash="%s" param1="save" param2="%s" terminal=false refresh=true\n' "$SF_CFG" "$MONO" "$SELF" "$cidr"
done
echo "---"

# --- Saved subnets ---------------------------------------------------------
echo "Saved subnets | $MUTED"
if [ -s "$SUBNETS_FILE" ]; then
  while IFS= read -r sn; do
    [ -z "$sn" ] && continue
    mark=""; [ "$sn" = "$ACTIVE" ] && mark="  ✓"
    printf '%s%s | %s\n' "$sn" "$mark" "$MONO"
    printf -- '--Set active & scan | sfimage=dot.radiowaves.left.and.right %s %s bash="%s" param1="startscan" param2="%s" terminal=false refresh=true\n' "$SF_CFG" "$MONO" "$SELF" "$sn"
    printf -- '--Remove | sfimage=trash %s %s color=#d82c20 bash="%s" param1="remove" param2="%s" terminal=false refresh=true\n' "$SF_CFG" "$MONO" "$SELF" "$sn"
  done < "$SUBNETS_FILE"
else
  echo "— none saved — | $MUTED"
fi
printf 'Add subnet… | sfimage=plus.circle %s %s bash="%s" param1="add" terminal=false refresh=true\n' "$SF_CFG" "$MONO" "$SELF"
printf 'Edit list in editor… | sfimage=pencil %s %s bash="/usr/bin/open" param1="-t" param2="%s" terminal=false\n' "$SF_CFG" "$MONO" "$SUBNETS_FILE"

exit 0
