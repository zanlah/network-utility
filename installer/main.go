// network-utility installer — one interactive command on any OS.
//
//	go run ./installer                 interactive install (asks what + where)
//	go run ./installer install         same, with flags to skip the prompts
//	go run ./installer uninstall       stop, remove autostart entries and binaries
//	go run ./installer build           just build the binaries into ./bin
//
// It shells out to `go build` (Go is already the prerequisite) and then wires up
// autostart with whatever mechanism the OS uses: launchd on macOS, a per-user
// registry Run key on Windows, an XDG autostart entry on Linux.
//
// Flags on `install` / `build` (any you omit are asked interactively, or take
// their default when stdin isn't a terminal):
//
//	--apps ports,netscan   which tools (also "all"/"both"; default: all)
//	--dir  <path>          install location (default: per-OS user folder)
//	--no-autostart         don't register login autostart
//	--no-start             don't launch right after installing
//	-y, --yes              accept all defaults, never prompt
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// app is one installable tool. For our own tray tools, name is both the
// utilities/ subdir and the binary. external tools (RustDesk) aren't built from
// source — they're fetched and installed by a dedicated handler instead.
type app struct {
	name     string // e.g. "systray-ports", or "rustdesk"
	icon     string
	title    string
	desc     string
	external bool // not a go-built tray binary — installed by a custom handler
}

// rustDeskAppName is the pseudo-app entry for the RustDesk remote-desktop client
// in the picker. Kept as a const so the special-casing reads clearly.
const rustDeskAppName = "rustdesk"

var allApps = []app{
	{name: "systray-ports", icon: "🔌", title: "Ports monitor", desc: "listening TCP ports + one-click kill"},
	{name: "systray-netscan", icon: "📡", title: "Subnet scanner", desc: "live hosts, PLC/Loxone detection, Tailscale peers"},
	{name: "systray-keyswap", icon: "⌨️", title: "Key swap", desc: "swap Ctrl ⇄ ⊞ Win for VMs (Windows only)"},
	{name: rustDeskAppName, icon: "🖥️", title: "RustDesk", desc: "remote-desktop client (self-hosted)", external: true},
}

// optOutByDefault holds tools that are NOT installed unless the user picks them
// explicitly. keyswap installs a global keyboard hook and RustDesk is a separate
// remote-desktop download, so both stay opt-in.
var optOutByDefault = map[string]bool{"systray-keyswap": true, rustDeskAppName: true}

// defaultApps is the selection used when the user doesn't choose explicitly
// (non-interactive install, or pressing Enter at the picker): everything except
// the opt-out tools.
func defaultApps() []app {
	out := make([]app, 0, len(allApps))
	for _, a := range allApps {
		if !optOutByDefault[a.name] {
			out = append(out, a)
		}
	}
	return out
}

const label = "si.viptronik" // reverse-DNS prefix for autostart labels

// options is a fully-resolved install request (after flags + prompts).
type options struct {
	apps       []app
	dir        string
	autostart  bool
	startNow   bool
	rustDesk   bool           // also download + launch the RustDesk remote-desktop client
	rustDeskRD rustDeskConfig // self-host preconfiguration for that client
}

func main() {
	cmd := "install"
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		cmd = os.Args[1]
	}

	root, err := repoRoot()
	if err != nil {
		fail(err)
	}

	switch cmd {
	case "install":
		mustHaveGo()
		opts := resolveOptions(os.Args[2:], true)
		if err := install(root, opts); err != nil {
			fail(err)
		}
	case "build":
		mustHaveGo()
		opts := resolveOptions(os.Args[2:], false)
		if _, err := buildAll(root, opts.dir, opts.apps); err != nil {
			fail(err)
		}
		fmt.Printf("→ binaries in %s\n", opts.dir)
	case "uninstall":
		dir := uninstallDir(os.Args[2:])
		if err := uninstall(dir); err != nil {
			fail(err)
		}
	case "rustdesk":
		if err := downloadRustDesk(resolveRustDesk(os.Args[2:])); err != nil {
			fail(err)
		}
	case "seal-rustdesk":
		if err := sealRustDesk(); err != nil {
			fail(err)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\nusage: go run ./installer [install|uninstall|build|rustdesk|seal-rustdesk] [flags]\n", cmd)
		os.Exit(2)
	}
}

// resolveOptions parses flags and fills any gaps from interactive prompts (when
// stdin is a terminal and --yes wasn't passed). withAutostart is false for
// `build`, which never touches login items.
func resolveOptions(args []string, withAutostart bool) options {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	appsFlag := fs.String("apps", "", "which tools: comma list of ports,netscan (or all)")
	dirFlag := fs.String("dir", "", "install directory")
	noAutostart := fs.Bool("no-autostart", false, "don't register login autostart")
	noStart := fs.Bool("no-start", false, "don't launch right after installing")
	rustdesk := fs.Bool("rustdesk", false, "also download + launch the RustDesk remote-desktop client")
	rdHost := fs.String("rustdesk-host", "", "self-hosted RustDesk server host (overrides the sealed blob)")
	rdKey := fs.String("rustdesk-key", "", "self-hosted RustDesk server public key")
	rdPass := fs.String("rustdesk-password", "", "deployment password to unlock the baked-in server")
	yes := fs.Bool("yes", false, "accept defaults, never prompt")
	fs.BoolVar(yes, "y", false, "accept defaults, never prompt (shorthand)")
	_ = fs.Parse(args)

	interactive := !*yes && isInteractive()
	if interactive {
		fmt.Printf("\n  network-utility installer\n\n")
	}

	// Which tools.
	var apps []app
	if *appsFlag != "" {
		var ok bool
		if apps, ok = parseAppSelection(*appsFlag); !ok {
			fail(fmt.Errorf("--apps %q: use ports, netscan, or all", *appsFlag))
		}
	} else if interactive {
		apps = promptApps()
	} else {
		apps = defaultApps()
	}

	// Where.
	dir := *dirFlag
	if dir == "" && interactive {
		dir = promptString("Install location", defaultInstallDir())
	} else if dir == "" {
		dir = defaultInstallDir()
	}
	dir = expandPath(dir)

	// Autostart + start-now (install only).
	autostart := withAutostart && !*noAutostart
	startNow := withAutostart && !*noStart
	if withAutostart && interactive {
		if !*noAutostart {
			autostart = promptYesNo("Start automatically at login?", true)
		}
		if !*noStart {
			startNow = promptYesNo("Start now?", true)
		}
	}

	// The RustDesk remote-desktop client is now a checkbox entry, so it's
	// "wanted" when the user ticked it. The --rustdesk flag force-adds it (handy
	// for a non-interactive install that also wants the client).
	if withAutostart && *rustdesk && !contains(apps, rustDeskAppName) {
		apps = append(apps, appByName(rustDeskAppName))
	}
	rustDesk := withAutostart && contains(apps, rustDeskAppName)

	// Self-host preconfiguration for that client. Explicit --rustdesk-host/-key
	// win; otherwise unlock the sealed blob with the deployment password (flag or
	// prompt). Blank result = stock client, no preconfig.
	rd := rustDeskConfig{host: strings.TrimSpace(*rdHost), key: strings.TrimSpace(*rdKey)}
	if rustDesk && rd.host == "" {
		rd = unlockOrPrompt(strings.TrimSpace(*rdPass), interactive)
	}

	if interactive {
		printSummary(apps, dir, autostart, startNow, rustDesk, rd)
		if !promptYesNo("Proceed?", true) {
			fmt.Println("Cancelled.")
			os.Exit(0)
		}
	}
	return options{apps: apps, dir: dir, autostart: autostart, startNow: startNow, rustDesk: rustDesk, rustDeskRD: rd}
}

func printSummary(apps []app, dir string, autostart, startNow, rustDesk bool, rd rustDeskConfig) {
	fmt.Printf("\n  Summary\n")
	names := make([]string, len(apps))
	for i, a := range apps {
		names[i] = a.icon + " " + a.title
	}
	fmt.Printf("    tools:     %s\n", strings.Join(names, ", "))
	fmt.Printf("    location:  %s\n", dir)
	fmt.Printf("    autostart: %s\n", yesNo(autostart))
	fmt.Printf("    start now: %s\n", yesNo(startNow))
	// RustDesk itself already shows up in the tools line above; add a server line
	// only when it's ticked and preconfigured for a self-hosted server.
	if rustDesk && rd.enabled() {
		fmt.Printf("    rustdesk:  self-hosted → %s\n", rd.host)
	}
	fmt.Printf("\n")
}

// install builds the selected binaries and registers autostart per options.
func install(root string, opts options) error {
	// Stop any running instances first. On Windows a running .exe can't be
	// overwritten by `go build` (the rebuild would fail and leave the old binary
	// in place), and stopping also prevents duplicate tray instances after the
	// start-now launch below. External tools (RustDesk) aren't ours to stop.
	for _, a := range opts.apps {
		if a.external {
			continue
		}
		stop(filepath.Join(opts.dir, exeName(a.name)))
	}

	built, err := buildAll(root, opts.dir, opts.apps)
	if err != nil {
		return err
	}
	for _, bin := range built {
		if opts.autostart {
			if err := registerAutostart(bin); err != nil {
				return fmt.Errorf("autostart for %s: %w", filepath.Base(bin), err)
			}
		}
		if opts.startNow {
			tryLaunch(bin)
		}
		fmt.Printf("  → %s%s\n", bin, statusSuffix(opts))
	}
	fmt.Println("\nInstalled. 🔌 / 📡 should appear in your menu bar / system tray.")
	if runtime.GOOS == "darwin" && contains(opts.apps, "systray-netscan") {
		fmt.Println("(the scanner asks for Local Network permission on its first scan — allow it.)")
	}
	if opts.rustDesk {
		fmt.Println()
		if err := downloadRustDesk(opts.rustDeskRD); err != nil {
			// Don't fail the whole install over the optional extra.
			fmt.Printf("  (could not fetch RustDesk: %v — get it at https://rustdesk.com/download)\n", err)
		}
	}
	return nil
}

func statusSuffix(o options) string {
	switch {
	case o.autostart && o.startNow:
		return " (running + starts at login)"
	case o.autostart:
		return " (starts at login)"
	case o.startNow:
		return " (running)"
	}
	return ""
}

func tryLaunch(bin string) {
	if err := launch(bin); err != nil {
		fmt.Printf("  (could not start %s now: %v)\n", filepath.Base(bin), err)
	}
}

// uninstall removes autostart entries and installed binaries for both tools.
func uninstall(dir string) error {
	for _, a := range allApps {
		if a.external {
			continue // RustDesk installs itself; we don't manage its files
		}
		bin := filepath.Join(dir, exeName(a.name))
		if err := unregisterAutostart(bin); err != nil {
			// Usually "not found" — fine, just note it.
			fmt.Printf("  (autostart cleanup for %s: %v)\n", a.name, err)
		}
		stop(bin)
		_ = os.Remove(bin)
		fmt.Printf("removed %s\n", a.name)
	}
	fmt.Printf("\nUninstalled. (Settings left in place; delete %s to purge them too.)\n", dir)
	return nil
}

// uninstallDir honours --dir, otherwise the per-OS default.
func uninstallDir(args []string) string {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	dirFlag := fs.String("dir", "", "install directory to remove from")
	_ = fs.Parse(args)
	if *dirFlag != "" {
		return expandPath(*dirFlag)
	}
	return defaultInstallDir()
}

// buildAll compiles every selected app into dstDir and returns the paths.
func buildAll(root, dstDir string, apps []app) ([]string, error) {
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return nil, err
	}
	var out []string
	for _, a := range apps {
		if a.external {
			continue // fetched + installed by a dedicated handler, not `go build`
		}
		dst := filepath.Join(dstDir, exeName(a.name))
		ensureReplaceable(dst) // wait for a just-stopped Windows instance to release the .exe
		fmt.Printf("building %s…\n", a.name)
		args := []string{"build", "-o", dst}
		if runtime.GOOS == "windows" {
			// -H=windowsgui marks the binary as a GUI app so the tray process
			// itself has no console window. (Child processes are hidden
			// separately via CREATE_NO_WINDOW in each collector.)
			args = append(args, "-ldflags", "-H=windowsgui")
		}
		args = append(args, ".")
		cmd := exec.Command("go", args...)
		cmd.Dir = filepath.Join(root, "utilities", a.name)
		// Stream build output live, but also keep a copy of stderr so we can
		// recognise the classic missing-GTK/appindicator failure on Linux and
		// print an actionable hint instead of a raw pkg-config error.
		var errBuf bytes.Buffer
		cmd.Stdout = os.Stdout
		cmd.Stderr = io.MultiWriter(os.Stderr, &errBuf)
		cmd.Env = os.Environ()
		if runtime.GOOS == "windows" {
			// The tray needs no C compiler on Windows; disabling cgo keeps the
			// build self-contained. macOS (Cocoa) and Linux (GTK) do need cgo.
			cmd.Env = append(cmd.Env, "CGO_ENABLED=0")
		}
		if err := cmd.Run(); err != nil {
			if runtime.GOOS == "linux" && looksLikeMissingTrayDeps(errBuf.String()) {
				return nil, fmt.Errorf("build %s: %w\n\n%s", a.name, err, linuxTrayDepHint())
			}
			return nil, fmt.Errorf("build %s: %w", a.name, err)
		}
		out = append(out, dst)
	}
	return out, nil
}

// looksLikeMissingTrayDeps reports whether a Linux `go build` failure is the
// familiar "the GTK / appindicator dev libraries (or a C compiler) aren't
// installed" case, so we can offer the fix instead of a raw pkg-config dump.
func looksLikeMissingTrayDeps(stderr string) bool {
	s := strings.ToLower(stderr)
	for _, needle := range []string{"appindicator", "gtk-3.0", "pkg-config", "exec: \"gcc\"", "exec: \"cc\"", "c compiler"} {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}

// linuxTrayDepHint tells the user which system packages the cgo tray build needs.
// The systray library links GTK 3 + libayatana-appindicator3 via pkg-config, and
// cgo needs a C compiler — none of which ship by default on a minimal install.
func linuxTrayDepHint() string {
	return "The tray tools need GTK 3 + the AppIndicator dev libraries (and a C compiler) to build on Linux.\n" +
		"Install them for your distro, then re-run the installer:\n" +
		"  Debian/Ubuntu/Mint: sudo apt install gcc libgtk-3-dev libayatana-appindicator3-dev\n" +
		"  Fedora/RHEL:        sudo dnf install gcc gtk3-devel libayatana-appindicator-gtk3-devel\n" +
		"  Arch/Manjaro:       sudo pacman -S gcc gtk3 libayatana-appindicator\n" +
		"(RustDesk needs none of this — it's a plain download.)"
}

// ensureReplaceable waits (Windows only) for a stopped instance to release its
// .exe so `go build` can overwrite it. taskkill /F terminates the process but the
// file handle can linger briefly; we retry removing it for up to ~2s.
func ensureReplaceable(path string) {
	if runtime.GOOS != "windows" {
		return
	}
	for i := 0; i < 20; i++ {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return
		}
		if err := os.Remove(path); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// parseAppSelection maps a comma list ("ports,netscan", "all", full names) to apps.
func parseAppSelection(s string) ([]app, bool) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" || s == "all" || s == "both" {
		return allApps, true
	}
	var out []app
	seen := map[string]bool{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		var match *app
		for i := range allApps {
			a := &allApps[i]
			if part == a.name || part == strings.TrimPrefix(a.name, "systray-") {
				match = a
				break
			}
		}
		if match == nil || seen[match.name] {
			if match == nil {
				return nil, false
			}
			continue
		}
		seen[match.name] = true
		out = append(out, *match)
	}
	return out, len(out) > 0
}

func contains(apps []app, name string) bool {
	for _, a := range apps {
		if a.name == name {
			return true
		}
	}
	return false
}

// appByName returns the catalog entry with the given name (empty app if none).
func appByName(name string) app {
	for _, a := range allApps {
		if a.name == name {
			return a
		}
	}
	return app{name: name}
}

// defaultInstallDir is where the binaries live, per OS (all user-writable, no admin).
func defaultInstallDir() string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		if base := os.Getenv("LOCALAPPDATA"); base != "" {
			return filepath.Join(base, "network-utility")
		}
		return filepath.Join(home, "AppData", "Local", "network-utility")
	case "darwin":
		return filepath.Join(home, "Applications", "network-utility")
	default: // linux and friends
		return filepath.Join(home, ".local", "share", "network-utility")
	}
}

// expandPath resolves a leading ~ and makes the path absolute.
func expandPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[1:])
		}
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// repoRoot walks up from the working directory until it finds the utilities dir.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if fi, err := os.Stat(filepath.Join(dir, "utilities")); err == nil && fi.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find the repo (no utilities/ dir above %s) — run this from the network-utility folder", mustWd())
		}
		dir = parent
	}
}

func mustHaveGo() {
	if _, err := exec.LookPath("go"); err != nil {
		fail(fmt.Errorf("Go is not installed or not on PATH — get it at https://go.dev/dl"))
	}
}

func mustWd() string {
	wd, _ := os.Getwd()
	return wd
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

// runCmd runs a command and returns combined output on failure for context.
func runCmd(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
