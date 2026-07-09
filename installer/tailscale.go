package main

// Tailscale bootstrap: install the Tailscale client and join our self-hosted
// Headscale tailnet, so the RustDesk client can reach the tailnet-only server.
// This runs BEFORE RustDesk is configured (see downloadRustDesk) — RustDesk must
// be able to reach 100.64.0.2 over the tailnet.
//
// Per OS: Windows gets the silent MSI (elevated via UAC), macOS the signed .pkg
// (installed with `installer` under sudo), Linux the official install script
// (which elevates with sudo itself). We then run `tailscale up` with the sealed
// login server + reusable pre-auth key, and wait until the tailnet IP is up.

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	tailscalePkgBase     = "https://pkgs.tailscale.com/stable/"
	tailscalePkgIndexURL = tailscalePkgBase + "?mode=json"
	tailscaleInstallSh   = "https://tailscale.com/install.sh"

	// tailscaleDefaultLoginServer is our self-hosted Headscale, used by the
	// standalone `tailscale` subcommand when neither --login-server nor
	// TS_LOGIN_SERVER is given.
	tailscaleDefaultLoginServer = "https://vpn.viptronik.si"
)

// resolveTailscale builds the tailnet enrollment config for the standalone
// `tailscale` subcommand. Precedence is flag > env (TS_LOGIN_SERVER/TS_AUTHKEY/
// TS_TAG, also read from installer/.env.local) > interactive paste > default.
// The pre-auth key is a secret and is never stored — you paste it each run
// (unless you cached it in .env.local yourself). Only the Tailscale fields of
// rustDeskConfig are used here.
func resolveTailscale(args []string) rustDeskConfig {
	fs := flag.NewFlagSet("tailscale", flag.ExitOnError)
	login := fs.String("login-server", "", "Headscale/Tailscale login server URL (or set TS_LOGIN_SERVER)")
	authKey := fs.String("auth-key", "", "reusable pre-auth key (or set TS_AUTHKEY; blank to paste)")
	tag := fs.String("tag", "", "advertise tag, e.g. tag:fleet (or set TS_TAG)")
	_ = fs.Parse(args)

	cfg := applyTailscaleEnv(rustDeskConfig{}) // env fills the defaults
	if v := strings.TrimSpace(*login); v != "" {
		cfg.tsLoginServer = v
	}
	if v := strings.TrimSpace(*authKey); v != "" {
		cfg.tsAuthKey = v
	}
	if v := strings.TrimSpace(*tag); v != "" {
		cfg.tsTag = v
	}

	// Paste anything still missing when we have a terminal — nothing lands on disk.
	if isInteractive() {
		if cfg.tsLoginServer == "" {
			cfg.tsLoginServer = strings.TrimSpace(promptString("Headscale login server URL", tailscaleDefaultLoginServer))
		}
		if cfg.tsAuthKey == "" {
			cfg.tsAuthKey = strings.TrimSpace(promptString("Paste the Tailscale reusable pre-auth key (hskey-auth-…)", ""))
		}
		if cfg.tsTag == "" {
			cfg.tsTag = strings.TrimSpace(promptString("Advertise tag (blank for none)", "tag:fleet"))
		}
	}
	if cfg.tsLoginServer == "" {
		cfg.tsLoginServer = tailscaleDefaultLoginServer
	}
	return cfg
}

// installTailscaleOnly is the standalone `tailscale` subcommand: install the
// client (if missing) and join the tailnet, without touching RustDesk.
func installTailscaleOnly(cfg rustDeskConfig) error {
	if cfg.tsAuthKey == "" {
		return fmt.Errorf("no pre-auth key — pass --auth-key or set TS_AUTHKEY (e.g. in installer/.env.local)")
	}
	return ensureTailscale(cfg)
}

// ensureTailscale installs Tailscale (if missing) and joins the tailnet, then
// blocks until the tailnet is up. Called before RustDesk is configured so the
// client can reach the tailnet-only server.
func ensureTailscale(cfg rustDeskConfig) error {
	fmt.Println("\nSetting up Tailscale (joining the self-hosted tailnet)…")
	bin := tailscaleBinary()
	if bin == "" {
		fmt.Println("Tailscale not found — installing it…")
		if err := installTailscale(); err != nil {
			return fmt.Errorf("install Tailscale: %w", err)
		}
		if bin = tailscaleBinary(); bin == "" {
			return fmt.Errorf("Tailscale installed but its CLI wasn't found — reopen your shell and re-run")
		}
	} else {
		fmt.Printf("Tailscale already present (%s).\n", bin)
	}
	if err := tailscaleUp(bin, cfg); err != nil {
		return err
	}
	return waitTailnetUp(bin, cfg.host)
}

// installTailscale fetches + installs the Tailscale client for this OS. The
// step needs elevation (admin/root); it's handled per-OS and fails with a clear
// message when it can't elevate.
func installTailscale() error {
	switch runtime.GOOS {
	case "windows":
		return installTailscaleWindows()
	case "darwin":
		return installTailscaleMacOS()
	default: // linux and friends
		return installTailscaleLinux()
	}
}

// installTailscaleLinux runs the official install script. The script elevates
// with sudo internally for the package install, so it needs an interactive
// terminal for the sudo prompt.
func installTailscaleLinux() error {
	fmt.Println("Running the official Tailscale install script…")
	return streamCmd("sh", "-c", "curl -fsSL "+tailscaleInstallSh+" | sh")
}

// installTailscaleWindows downloads the stable MSI and installs it silently.
// msiexec needs admin, so we elevate through UAC and wait for it to finish.
func installTailscaleWindows() error {
	url, name, err := resolveTailscalePackage()
	if err != nil {
		return err
	}
	dst := filepath.Join(downloadsDir(), name)
	fmt.Printf("Downloading Tailscale to %s …\n", dst)
	if err := downloadFile(url, dst); err != nil {
		return err
	}
	fmt.Println("Installing Tailscale (accept the UAC prompt)…")
	ps := fmt.Sprintf(
		"Start-Process msiexec.exe -ArgumentList '/i',\"%s\",'/qn','/norestart' -Verb RunAs -Wait",
		dst)
	return streamCmd("powershell", "-NoProfile", "-Command", ps)
}

// installTailscaleMacOS downloads the signed universal .pkg and installs it with
// `installer` under sudo (which prompts for the user's password).
func installTailscaleMacOS() error {
	url, name, err := resolveTailscalePackage()
	if err != nil {
		return err
	}
	dst := filepath.Join(downloadsDir(), name)
	fmt.Printf("Downloading Tailscale to %s …\n", dst)
	if err := downloadFile(url, dst); err != nil {
		return err
	}
	if strings.HasSuffix(strings.ToLower(name), ".pkg") {
		fmt.Println("Installing Tailscale (you may be asked for your password)…")
		return runPrivileged("installer", "-pkg", dst, "-target", "/")
	}
	// Not a .pkg (a zipped app bundle) — we can't install it unattended; open it
	// so the user can finish, then bail with a clear message.
	fmt.Println("Opening the Tailscale download — finish the install, then re-run.")
	_ = exec.Command("open", dst).Run()
	return fmt.Errorf("manual Tailscale install required on macOS (opened %s)", dst)
}

// tailscaleUp joins the tailnet with the sealed login server + pre-auth key.
// --accept-routes/--accept-dns are off (fleet devices only talk to the server)
// and --shields-up blocks inbound connections. It needs root on macOS/Linux.
func tailscaleUp(bin string, cfg rustDeskConfig) error {
	args := []string{
		"up",
		"--login-server=" + cfg.tsLoginServer,
		"--auth-key=" + cfg.tsAuthKey,
		"--accept-routes=false",
		"--accept-dns=false",
		"--shields-up",
	}
	// --unattended keeps the tunnel up with no user logged in, but it only exists
	// on the Windows CLI — passing it on macOS/Linux errors out ("flag not defined").
	if runtime.GOOS == "windows" {
		args = append(args, "--unattended")
	}
	if cfg.tsTag != "" {
		args = append(args, "--advertise-tags="+cfg.tsTag)
	}
	fmt.Printf("Joining the tailnet at %s …\n", cfg.tsLoginServer)
	if runtime.GOOS == "windows" {
		// The Windows service runs elevated; the CLI talks to it as the logged-in
		// user, so no UAC here. Surface an access-denied hint just in case.
		if err := streamCmd(bin, args...); err != nil {
			return fmt.Errorf("tailscale up failed: %w — if it says access denied, run from an elevated PowerShell", err)
		}
		return nil
	}
	return runPrivileged(bin, args...)
}

// waitTailnetUp blocks until the device has a tailnet IP, then does a best-effort
// reachability check to the RustDesk server. Times out after 60s.
func waitTailnetUp(bin, serverHost string) error {
	fmt.Println("Waiting for the tailnet to come up…")
	deadline := time.Now().Add(60 * time.Second)
	for {
		if ip := tailscaleIP(bin); ip != "" {
			fmt.Printf("Tailnet up — this device is %s.\n", ip)
			// Non-fatal: the server may block ICMP/other ports; we probe hbbs.
			if serverHost != "" {
				if reachable(serverHost, "21115", 3*time.Second) {
					fmt.Printf("Server %s is reachable over the tailnet.\n", serverHost)
				} else {
					fmt.Printf("  (couldn't confirm %s is reachable yet — continuing anyway)\n", serverHost)
				}
			}
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no tailnet IP assigned within 60s — check the pre-auth key and login server")
		}
		time.Sleep(3 * time.Second)
	}
}

// tailscaleIP returns this device's first IPv4 tailnet address, or "".
func tailscaleIP(bin string) string {
	out, err := exec.Command(bin, "ip", "-4").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0])
}

// reachable reports whether host:port accepts a TCP connection within timeout.
func reachable(host, port string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// tailscaleBinary returns the path to the installed tailscale CLI, or "" if it
// can't be found. Checks the well-known per-OS locations first, then PATH.
func tailscaleBinary() string {
	var candidates []string
	switch runtime.GOOS {
	case "windows":
		for _, base := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")} {
			if base != "" {
				candidates = append(candidates, filepath.Join(base, "Tailscale", "tailscale.exe"))
			}
		}
	case "darwin":
		candidates = append(candidates,
			"/Applications/Tailscale.app/Contents/MacOS/Tailscale",
			"/usr/local/bin/tailscale",
			"/opt/homebrew/bin/tailscale")
	default: // linux and friends
		candidates = append(candidates, "/usr/bin/tailscale", "/usr/local/bin/tailscale")
	}
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c
		}
	}
	if p, err := exec.LookPath("tailscale"); err == nil {
		return p
	}
	return ""
}

// resolveTailscalePackage picks the installer asset (Windows MSI / macOS pkg)
// for this OS + arch from the stable package index, returning its URL and name.
func resolveTailscalePackage() (url, name string, err error) {
	idx, err := tailscalePkgIndex()
	if err != nil {
		return "", "", err
	}
	switch runtime.GOOS {
	case "windows":
		arch := map[string]string{"amd64": "amd64", "arm64": "arm64", "386": "x86"}[runtime.GOARCH]
		if arch == "" {
			arch = "amd64"
		}
		if name = idx.MSIs[arch]; name == "" {
			name = idx.MSIs["amd64"] // Tailscale always ships amd64
		}
	case "darwin":
		// universal-package is the signed .pkg; universal is the app zip fallback.
		if name = idx.MacZips["universal-package"]; name == "" {
			name = idx.MacZips["universal"]
		}
	default:
		return "", "", fmt.Errorf("no packaged Tailscale download for %s (use the install script)", runtime.GOOS)
	}
	if name == "" {
		return "", "", fmt.Errorf("no Tailscale package found for %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	return tailscalePkgBase + name, name, nil
}

// tsPkgIndex is the subset of pkgs.tailscale.com/stable/?mode=json we use.
type tsPkgIndex struct {
	MSIs    map[string]string `json:"MSIs"`    // keyed by amd64/arm64/x86
	MacZips map[string]string `json:"MacZips"` // keyed by universal/universal-package
}

func tailscalePkgIndex() (*tsPkgIndex, error) {
	req, _ := http.NewRequest(http.MethodGet, tailscalePkgIndexURL, nil)
	req.Header.Set("User-Agent", "network-utility-installer")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Tailscale package index returned HTTP %d", resp.StatusCode)
	}
	var idx tsPkgIndex
	if err := json.NewDecoder(resp.Body).Decode(&idx); err != nil {
		return nil, err
	}
	return &idx, nil
}

// runPrivileged runs name+args as root on macOS/Linux, via sudo when we aren't
// already root. It needs an interactive terminal so sudo can prompt.
func runPrivileged(name string, args ...string) error {
	if os.Geteuid() == 0 {
		return streamCmd(name, args...)
	}
	if _, err := exec.LookPath("sudo"); err != nil {
		return fmt.Errorf("this step needs root and sudo isn't available — re-run the installer as root")
	}
	if !isInteractive() {
		return fmt.Errorf("this step needs root — re-run in a terminal so sudo can prompt, or run as root")
	}
	return streamCmd("sudo", append([]string{name}, args...)...)
}

// streamCmd runs a command with its stdio wired to ours, so prompts (sudo/UAC)
// and progress are visible. Command arguments are never logged (they carry the
// pre-auth key), only the streamed output.
func streamCmd(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
