#!/bin/bash
# fleet-admin — toggle this device's fleet-admin access in Headscale by swapping
# its tags:
#   on  → tag:admin + tag:fleet-admin   (full fleet admin)
#   off → tag:admin                     (normal fleet device)
#
# This script ships with the network-utility app. The installer writes a
# fleet-admin.env next to it holding the Headscale API key (and this device's
# node id, auto-resolved at install time). Those can also come from the
# environment; anything still missing is asked for interactively.
#   FLEET_API_URL   Headscale API base   (default https://vpn.viptronik.si/api/v1)
#   FLEET_API_KEY   Headscale API key    (hskey-api-…)
#   FLEET_NODE_ID   this device's node id in Headscale
#
# Usage: fleet-admin.sh on|off
set -euo pipefail

action="${1:-}"
case "$action" in
  on|off) ;;
  *) echo "usage: $0 on|off" >&2; exit 2 ;;
esac

# Load the fleet-admin.env the installer wrote next to this script (KEY=VALUE
# lines), without clobbering anything already set in the environment.
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
env_file="$script_dir/fleet-admin.env"
if [[ -f "$env_file" ]]; then
  while IFS='=' read -r key val; do
    key="${key#"${key%%[![:space:]]*}"}"   # ltrim
    [[ -z "$key" || "$key" == \#* ]] && continue
    val="${val%\"}"; val="${val#\"}"        # strip surrounding double quotes
    [[ -z "${!key:-}" ]] && export "$key=$val"
  done < "$env_file"
fi

API_URL="${FLEET_API_URL:-https://vpn.viptronik.si/api/v1}"
API_KEY="${FLEET_API_KEY:-}"
NODE_ID="${FLEET_NODE_ID:-}"

# Ask for anything the installer didn't save.
if [[ -z "$API_KEY" ]]; then
  read -r -s -p "Paste the Headscale API key (hskey-api-…): " API_KEY; echo
fi
if [[ -z "$NODE_ID" ]]; then
  read -r -p "Headscale node id for this device: " NODE_ID
fi
if [[ -z "$API_KEY" || -z "$NODE_ID" ]]; then
  echo "error: API key and node id are both required" >&2
  exit 1
fi

set_tags() {
  curl -fsS -H "Authorization: Bearer $API_KEY" \
    --json "{\"tags\": $1}" \
    -X POST "$API_URL/node/$NODE_ID/tags"
}

case "$action" in
  on)  set_tags '["tag:admin","tag:fleet-admin"]' ;;
  off) set_tags '["tag:admin"]' ;;
esac
