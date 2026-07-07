# systray-ports — cross-platform tray tool

Lists listening TCP ports in the menu bar / tray, on **macOS and Windows** from one
Go codebase ([`getlantern/systray`](https://github.com/getlantern/systray)).

## Menu

- Each row: process · `:port` · PID. A `◆` marks **dev ports** (a broad set of
  frontend/backend dev servers + the usual Dockerized services — DBs, queues,
  caches, search, dashboards) **and every port published by Docker**, on whatever
  number (flagged by process, via `isDocker()`, not just a fixed list).
- Per-row actions: **Open in browser** (`http://localhost:<port>`), **Terminate
  (SIGTERM)**, **Force Kill (SIGKILL)**.
- **Settings** submenu:
  - **Dev ports only** (checkbox, remembered): hides everything that isn't a dev port
    or a Docker-published port — so Spotify, Control Center, Raycast, etc. drop out and
    you're left with just `node`, `docker`, your databases, and friends.
  - **Open config folder** — reveals where settings + logs are stored.
- **Report bug…** and **Refresh now**.

## Config

Settings and logs are stored **next to the executable**, in a `config/` folder
beside the binary (portable — copy the app folder and your settings come along):

```
systray-ports          the binary
config/
  config.json          settings (JSON)
  log.txt              diagnostics log
```

`config.json` looks like:

```json
{
  "devOnly": false
}
```

If the app lives somewhere read-only (e.g. `/Applications`), it falls back to the
user config dir automatically.

## The architecture (the point of this example)

```
main.go               PRESENTER — builds the tray menu, handles clicks.
                      One file, identical on every OS. Never mentions lsof/netstat.
collector.go          DATA CONTRACT — the Listener struct + shared helpers.
collector_darwin.go   macOS ADAPTER — lsof + kill        (//go:build darwin)
collector_windows.go  Windows ADAPTER — netstat + taskkill (//go:build windows)
```

The presenter calls `listListeners()`, `terminate()`, `forceKill()`. Go's build
tags link in the correct adapter for the target OS at compile time — no runtime
`if windows` branching, no shared-file-that-can't-actually-be-shared problem. This
is the honest version of "core + adapters": the **data contract** is shared, each
**platform adapter** is its own file, and the **presenter** is genuinely
write-once.

## Build & run

Needs Go 1.21+.

```sh
# macOS (native; uses Cocoa via cgo)
go build -o systray-ports .
./systray-ports

# Windows (cross-compile from anywhere)
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o systray-ports.exe .
```

Both targets are known to compile from this source (verified on macOS with
Go 1.24).

## Extras

- **Report bug…** menu item — copies a diagnostics report (OS, Go version, listener
  count, and the last 300 log lines) to the clipboard and opens a pre-filled email to
  `zan.lah@viptronik.si`. Startup, kill actions (`SIGTERM`/`SIGKILL` + result),
  collector errors and panics are logged to a ring buffer and
  `<config>/systray-ports/log.txt`.

## What this buys you vs. the SwiftBar plugin

- **One binary runs on macOS and Windows.** The tray library is the platform
  abstraction, so the menu is written once.
- Real language (types, tests, concurrency) instead of bash string-printing.

## What it costs (be honest)

- **More code for the same UI.** SwiftBar's model is "print lines, it draws the
  menu." systray is imperative: you pre-create a fixed pool of menu rows and update
  their titles/visibility on refresh, because items can't be cleanly added/removed
  at runtime. That's the `maxRows` pool in `main.go`.
- **You give up SwiftBar's niceties** — SF Symbols, `color=`, per-line fonts,
  auto-refresh intervals, the plugin folder. You'd rebuild what you need.
- **Windows tray ≠ macOS menu bar.** Windows shows an *icon* (no text label), so the
  tool embeds `icon.ico` and calls `SetIcon()` (see `icon_windows.go`); the `🔌 N`
  count only renders next to the macOS clock via `SetTitle`.
- **You own the lifecycle.** SwiftBar launches/refreshes plugins for you. Here you
  ship a persistent process and arrange autostart yourself (a LaunchAgent on macOS,
  a Startup shortcut or Task Scheduler entry on Windows).

## Takeaway

If cross-platform is a real requirement, **this** is the path — one codebase, one
tray abstraction — not "bash on mac + PowerShell on Windows behind adapters" (which
is two of everything). If macOS is all you need, the SwiftBar plugin is far less
code and nicer-looking; keep it.
