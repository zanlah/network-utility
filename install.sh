#!/usr/bin/env bash
# network-utility one-line installer (macOS / Linux).
#
#   curl -fsSL https://raw.githubusercontent.com/zanlah/network-utility/main/install.sh | bash
#
# Pass installer flags after `-- ` (they go straight to `go run ./installer install`):
#
#   curl -fsSL https://raw.githubusercontent.com/zanlah/network-utility/main/install.sh | bash -s -- --apps ports -y
#
# What it does: fetches (or updates) the repo into a cache dir, then runs the
# existing Go installer from there. Re-running pulls the latest source first, so
# there's nothing to download or `git pull` by hand.
set -euo pipefail

REPO="https://github.com/zanlah/network-utility.git"
TARBALL="https://github.com/zanlah/network-utility/archive/refs/heads/main.tar.gz"
SRC="${NETUTIL_SRC:-${XDG_CACHE_HOME:-$HOME/.cache}/network-utility}"

echo "network-utility installer"

# 1. Go is the one hard prerequisite — the installer builds the tray tools from source.
if ! command -v go >/dev/null 2>&1; then
  echo "error: Go is not installed or not on PATH." >&2
  echo "       Install Go 1.21+ from https://go.dev/dl and re-run this." >&2
  exit 1
fi

# 2. Fetch (or update) the source. Prefer git so re-runs are a fast fast-forward;
#    fall back to a tarball when git isn't available.
if command -v git >/dev/null 2>&1; then
  if [ -d "$SRC/.git" ]; then
    echo "updating source in $SRC…"
    git -C "$SRC" pull --ff-only
  else
    echo "cloning into $SRC…"
    rm -rf "$SRC"
    git clone --depth 1 "$REPO" "$SRC"
  fi
else
  echo "git not found — downloading tarball into $SRC…"
  rm -rf "$SRC"
  mkdir -p "$SRC"
  curl -fsSL "$TARBALL" | tar -xz -C "$SRC" --strip-components=1
fi

# 3. Hand off to the real installer, passing through any flags after `-- `.
cd "$SRC"
exec go run ./installer install "$@"
