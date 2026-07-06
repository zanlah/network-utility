#!/bin/bash
#
# <xbar.title>Active Ports</xbar.title>
# <xbar.version>v1.0</xbar.version>
# <xbar.author>zanlah</xbar.author>
# <xbar.desc>Lists listening TCP ports in the menu bar, grouped for web dev, with one-click kill.</xbar.desc>
# <xbar.dependencies>bash,lsof,ps</xbar.dependencies>
#
# <swiftbar.hideAbout>false</swiftbar.hideAbout>
# <swiftbar.hideRunInTerminal>true</swiftbar.hideRunInTerminal>
# <swiftbar.hideLastUpdated>false</swiftbar.hideLastUpdated>
# <swiftbar.hideDisablePlugin>false</swiftbar.hideDisablePlugin>
# <swiftbar.hideSwiftBar>false</swiftbar.hideSwiftBar>
#
# SwiftBar plugin: active listening TCP ports for web development.
# Filename convention: <name>.<interval>.sh  ->  ports.10s.sh means refresh every 10 seconds.
# Written for stock macOS: /bin/bash is 3.2, so NO associative arrays (declare -A) are used.

export PATH="/usr/sbin:/sbin:/usr/bin:/bin:$PATH"

# ---------------------------------------------------------------------------
# Config
# ---------------------------------------------------------------------------

# Ports commonly used by web dev tooling. Space padded for easy membership test.
WEBDEV_PORTS=" 3000 3001 3002 4200 4321 5000 5173 5174 8000 8080 8081 8888 9000 9229 \
1420 3306 5432 6379 27017 5672 15672 11211 9200 5601 "

MENU_SFIMAGE="network"          # SF Symbol shown in the menu bar next to the count
# 13pt so the row height is driven by the text, not the SF Symbol — keeps the
# title vertically centered against the icon (SwiftBar draws SF Symbols taller
# than the text, which otherwise pins the title to the top of the row).
MONO="font=Menlo size=13"       # text size for the whole list
MUTED="font=Menlo size=11 color=#8a8a8a"

# SF Symbol sizing. In SwiftBar 2.x, `sfsize`/`size` are IGNORED for SF Symbols
# (see github.com/swiftbar/SwiftBar/issues/245). The only working lever is
# `sfconfig`: a base64-encoded JSON with scale (small|medium|large) + weight.
# `renderingMode` and `colors` MUST be present or SwiftBar drops the whole config.
# Change "small" -> "medium" to enlarge the icons.
SF_JSON='{"renderingMode":"Hierarchical","colors":[],"scale":"small","weight":"regular"}'
SF_CFG="sfconfig=$(printf '%s' "$SF_JSON" | base64 | tr -d '\n')"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

# is_webdev <port> -> exit 0 if the port is in the web-dev set
is_webdev() {
  case "$WEBDEV_PORTS" in
    *" $1 "*) return 0 ;;
    *)        return 1 ;;
  esac
}

# proc_sfimage <command> -> an SF Symbol name for the process type.
# SF Symbols has no brand logos, so these are role-based shapes (color adds meaning).
proc_sfimage() {
  case "$1" in
    node|nodejs|next-server|npm|bun|deno|vite)       echo "hexagon.fill" ;;
    Python|python|python2|python3|gunicorn|uvicorn)  echo "chevron.left.forwardslash.chevron.right" ;;
    php|php-fpm*|php-cgi)                             echo "globe" ;;
    ruby|rails|puma|rackup)                           echo "diamond.fill" ;;
    java)                                             echo "cup.and.saucer.fill" ;;
    nginx|caddy|traefik|httpd|apache2)               echo "server.rack" ;;
    mysqld|mysql|mariadbd|mariadb)                    echo "cylinder.split.1x2" ;;
    postgres|postgresql|postmaster)                   echo "cylinder.split.1x2" ;;
    redis-server|redis)                               echo "bolt.fill" ;;
    mongod|mongodb)                                   echo "leaf.fill" ;;
    com.docke*|docker*|colima*|qemu*|containerd*)     echo "shippingbox.fill" ;;
    *)                                                echo "circle.fill" ;;
  esac
}

# proc_color <command> -> a hex color for the row
proc_color() {
  case "$1" in
    node|nodejs|next-server|npm|bun|deno|vite)       echo "#3fb950" ;;
    Python|python|python2|python3|gunicorn|uvicorn)  echo "#4b8bbe" ;;
    php|php-fpm*|php-cgi)                             echo "#8892bf" ;;
    ruby|rails|puma|rackup)                           echo "#cc342d" ;;
    nginx|caddy|traefik|httpd|apache2)               echo "#009639" ;;
    mysqld|mysql|mariadbd|mariadb|postgres|postgresql|postmaster) echo "#e48e00" ;;
    redis-server|redis)                               echo "#d82c20" ;;
    mongod|mongodb)                                   echo "#4faa41" ;;
    com.docke*|docker*|colima*|qemu*|containerd*)     echo "#2496ed" ;;
    *)                                                echo "#cfcfcf" ;;
  esac
}

# Print one port entry plus its action submenu.
# args: <port> <pid> <command>
render_entry() {
  port="$1"; pid="$2"; cmd="$3"

  # lsof truncates COMMAND to 9 chars and escapes spaces (e.g. "com.docke",
  # "Canva\x20Helper"). Prefer a clean basename from ps; fall back to lsof's cmd.
  name="$(basename "$(ps -p "$pid" -o comm= 2>/dev/null)" 2>/dev/null)"
  [ -z "$name" ] && name="$cmd"
  name="$(printf '%s' "$name" | tr '|' ' ' | cut -c1-16)"

  # Icon/color are matched on lsof's short command (reliable: "node", "com.docke*")
  # rather than the ps process title (which may read "next-server (v14...)").
  sficon="$(proc_sfimage "$cmd")"
  color="$(proc_color "$cmd")"

  # Full command line (best effort). Strip pipes so they don't break SwiftBar parsing.
  cmdline="$(ps -p "$pid" -o command= 2>/dev/null | tr '|' ' ' | cut -c1-90)"
  [ -z "$cmdline" ] && cmdline="$cmd"

  # Working directory (best effort; may fail without permission).
  cwd="$(lsof -a -p "$pid" -d cwd -Fn 2>/dev/null | sed -n 's/^n//p' | head -1)"

  # Main row (aligned with Menlo). Trailing tag for web-dev ports.
  tag=""
  is_webdev "$port" && tag="  ◆"
  printf '%-16s :%-5s  pid %-6s%s | sfimage=%s %s %s color=%s\n' \
    "$name" "$port" "$pid" "$tag" "$sficon" "$SF_CFG" "$MONO" "$color"

  # Submenu (one level deeper = "--" prefix)
  printf -- '--Port %s · %s · PID %s | %s\n' "$port" "$name" "$pid" "$MONO"
  printf -- '--%s | %s\n' "$cmdline" "$MUTED"
  [ -n "$cwd" ] && printf -- '--cwd: %s | %s\n' "$cwd" "$MUTED"
  printf -- '-----\n'
  printf -- '--Terminate (SIGTERM) | sfimage=stop.circle %s %s bash="/bin/kill" param1="-TERM" param2="%s" terminal=false refresh=true\n' "$SF_CFG" "$MONO" "$pid"
  printf -- '--Force Kill (SIGKILL) | sfimage=xmark.octagon.fill %s %s color=#d82c20 bash="/bin/kill" param1="-KILL" param2="%s" terminal=false refresh=true\n' "$SF_CFG" "$MONO" "$pid"
  printf -- '-----\n'
  printf -- '--Copy port %s | sfimage=doc.on.clipboard %s %s bash="/bin/bash" param1="-c" param2="printf %s | pbcopy" terminal=false\n' "$port" "$SF_CFG" "$MONO" "$port"
}

# ---------------------------------------------------------------------------
# Gather data
# ---------------------------------------------------------------------------
# lsof field mode is fussy across versions, so we parse the classic column output.
# NAME (col 9) looks like 127.0.0.1:3000 / *:8080 / [::1]:3000  -> port = after last ':'.
# Dedup by pid:port inside awk (a process listening on both IPv4 + IPv6 appears twice).

RAW="$(
  lsof -nP -iTCP -sTCP:LISTEN 2>/dev/null | awk '
    NR > 1 {
      cmd = $1; pid = $2; name = $9
      n = split(name, a, ":"); port = a[n]
      if (port !~ /^[0-9]+$/) next
      key = pid ":" port
      if (!(key in seen)) { seen[key] = 1; print port "|" pid "|" cmd }
    }
  ' | sort -t'|' -k1,1n
)"

# ---------------------------------------------------------------------------
# Menu bar title
# ---------------------------------------------------------------------------
if [ -z "$RAW" ]; then
  # No rows: could be genuinely empty OR lsof lacked permission.
  echo "0 | sfimage=$MENU_SFIMAGE $SF_CFG $MONO"
  echo "---"
  echo "Refresh now | sfimage=arrow.clockwise $SF_CFG $MONO refresh=true"
  echo "---"
  echo "No listening TCP ports found | $MUTED"
  echo "(or lsof needs more permission for other users' ports) | $MUTED"
  exit 0
fi

TOTAL="$(printf '%s\n' "$RAW" | grep -c '.')"
WEB_COUNT=0
while IFS='|' read -r port pid cmd; do
  [ -z "$port" ] && continue
  is_webdev "$port" && WEB_COUNT=$((WEB_COUNT + 1))
done <<EOF
$RAW
EOF

echo "$TOTAL | sfimage=$MENU_SFIMAGE $SF_CFG $MONO"
echo "---"
echo "Refresh now | sfimage=arrow.clockwise $SF_CFG $MONO refresh=true"
printf '%s listening · %s web-dev | %s\n' "$TOTAL" "$WEB_COUNT" "$MUTED"
echo "---"

# ---------------------------------------------------------------------------
# Section 1: web-dev ports
# ---------------------------------------------------------------------------
echo "Web Dev Ports | $MUTED"
FOUND_WEB=0
while IFS='|' read -r port pid cmd; do
  [ -z "$port" ] && continue
  if is_webdev "$port"; then
    FOUND_WEB=1
    render_entry "$port" "$pid" "$cmd"
  fi
done <<EOF
$RAW
EOF
[ "$FOUND_WEB" -eq 0 ] && echo "— none — | $MUTED"

echo "---"

# ---------------------------------------------------------------------------
# Section 2: everything else (system / background)
# ---------------------------------------------------------------------------
echo "Other / System Ports | $MUTED"
FOUND_OTHER=0
while IFS='|' read -r port pid cmd; do
  [ -z "$port" ] && continue
  if ! is_webdev "$port"; then
    FOUND_OTHER=1
    render_entry "$port" "$pid" "$cmd"
  fi
done <<EOF
$RAW
EOF
[ "$FOUND_OTHER" -eq 0 ] && echo "— none — | $MUTED"

exit 0
