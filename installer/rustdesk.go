package main

// RustDesk fetcher: grab the latest official RustDesk installer for this OS/arch
// straight from GitHub releases, save it to the Downloads folder, and launch it.
// No hard-coded version — we ask the GitHub API for the latest release so this
// keeps working as RustDesk ships new builds.

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const rustDeskLatestAPI = "https://api.github.com/repos/rustdesk/rustdesk/releases/latest"

// sealedRustDesk is the self-hosted server's {host,key}, encrypted with the
// deployment password (see secret.go). It's safe to commit to a public repo:
// nothing is usable without the password. Empty ⇒ no baked-in server (users must
// pass --rustdesk-host/--rustdesk-key, or get the stock client).
//
// Regenerate after changing the server or password with:
//
//	go run ./installer seal-rustdesk
const sealedRustDesk = "EYqNqOsF8kYiPqmRs5sK4jzd6XeIMyFmZBcxI8Q4WQxHcFLVW7jreNbmW1mbi/KoqZhlFV56lzfvIFX74YCpBE719nkOJ8Tw/qeDpTDywBlrL8wtrkplGCmbKJxC1Y9OGQ1DTLDPSi8+tKEX5tGHBQWK8IsVFSkUxLe2"

// rustDeskConfig is the self-host preconfiguration written to RustDesk2.toml.
// host is the ID (rendezvous) server; relay and api default to it when empty.
type rustDeskConfig struct {
	host  string
	key   string
	relay string
	api   string
}

func (c rustDeskConfig) enabled() bool { return c.host != "" }

// rustDeskSealed is the JSON payload inside sealedRustDesk.
type rustDeskSealed struct {
	Host string `json:"host"`
	Key  string `json:"key"`
}

// unlockRustDesk decrypts sealedRustDesk with password into a config.
func unlockRustDesk(password string) (rustDeskConfig, error) {
	js, err := openSecret(sealedRustDesk, password)
	if err != nil {
		return rustDeskConfig{}, err
	}
	var s rustDeskSealed
	if err := json.Unmarshal([]byte(js), &s); err != nil {
		return rustDeskConfig{}, fmt.Errorf("decrypted data is not valid: %w", err)
	}
	return rustDeskConfig{host: strings.TrimSpace(s.Host), key: strings.TrimSpace(s.Key)}, nil
}

// resolveRustDesk builds the self-host config for the standalone `rustdesk`
// subcommand. --rustdesk-host/--rustdesk-key win; otherwise the sealed blob is
// unlocked with --rustdesk-password (or an interactive prompt).
func resolveRustDesk(args []string) rustDeskConfig {
	fs := flag.NewFlagSet("rustdesk", flag.ExitOnError)
	host := fs.String("rustdesk-host", "", "self-hosted RustDesk server host (overrides the sealed blob)")
	key := fs.String("rustdesk-key", "", "self-hosted RustDesk server public key")
	pass := fs.String("rustdesk-password", "", "deployment password to unlock the baked-in server")
	_ = fs.Parse(args)

	cfg := rustDeskConfig{host: strings.TrimSpace(*host), key: strings.TrimSpace(*key)}
	if cfg.host == "" {
		cfg = unlockOrPrompt(strings.TrimSpace(*pass), isInteractive())
	}
	return cfg
}

// unlockOrPrompt turns the sealed blob into a config using the given password,
// prompting (up to 3 tries) when interactive if none/incorrect. Returns an empty
// config (stock client) if there's no sealed blob or the user gives up.
func unlockOrPrompt(password string, interactive bool) rustDeskConfig {
	if sealedRustDesk == "" {
		return rustDeskConfig{}
	}
	if password != "" {
		if cfg, err := unlockRustDesk(password); err == nil {
			return cfg
		} else {
			fmt.Printf("  (RustDesk: %v)\n", err)
		}
	}
	if !interactive {
		return rustDeskConfig{}
	}
	for tries := 0; tries < 3; tries++ {
		p := strings.TrimSpace(promptString("RustDesk deployment password (blank to skip)", ""))
		if p == "" {
			return rustDeskConfig{}
		}
		cfg, err := unlockRustDesk(p)
		if err == nil {
			return cfg
		}
		fmt.Printf("  ↳ %v\n", err)
	}
	return rustDeskConfig{}
}

// sealRustDesk (subcommand `seal-rustdesk`) prompts for the server details and a
// password, then prints the sealed blob to paste into the sealedRustDesk const.
func sealRustDesk() error {
	fmt.Println("Seal RustDesk self-host credentials (encrypted for the public repo).")
	host := strings.TrimSpace(promptString("Server host (e.g. 192.168.1.67)", ""))
	key := strings.TrimSpace(promptString("Server public key (./data/id_ed25519.pub)", ""))
	if host == "" || key == "" {
		return fmt.Errorf("host and key are both required")
	}
	pw := strings.TrimSpace(promptString("Deployment password", ""))
	if len(pw) < 6 {
		return fmt.Errorf("choose a password of at least 6 characters")
	}
	if strings.TrimSpace(promptString("Confirm password", "")) != pw {
		return fmt.Errorf("passwords do not match")
	}
	js, err := json.Marshal(rustDeskSealed{Host: host, Key: key})
	if err != nil {
		return err
	}
	blob, err := sealSecret(string(js), pw)
	if err != nil {
		return err
	}
	fmt.Printf("\nPaste this into installer/rustdesk.go:\n\n\tconst sealedRustDesk = %q\n\n", blob)
	return nil
}

type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

type ghRelease struct {
	TagName string    `json:"tag_name"`
	Assets  []ghAsset `json:"assets"`
}

// downloadRustDesk resolves the latest release, downloads the right asset,
// preconfigures the client for a self-hosted server (when cfg is set), and
// launches the installer.
func downloadRustDesk(cfg rustDeskConfig) error {
	fmt.Println("Fetching the latest RustDesk release…")
	rel, err := latestRustDeskRelease()
	if err != nil {
		return err
	}
	asset, err := pickRustDeskAsset(rel.Assets)
	if err != nil {
		return err
	}
	fmt.Printf("Latest RustDesk: %s — %s\n", rel.TagName, asset.Name)

	dst := filepath.Join(downloadsDir(), asset.Name)
	fmt.Printf("Downloading to %s …\n", dst)
	if err := downloadFile(asset.URL, dst); err != nil {
		return err
	}

	if cfg.enabled() {
		if path, err := writeRustDeskConfig(cfg); err != nil {
			// Non-fatal: the client still installs, just not preconfigured.
			fmt.Printf("  (could not preconfigure RustDesk: %v — set the server manually in Settings › Network)\n", err)
		} else {
			fmt.Printf("Preconfigured to use server %q (%s)\n", cfg.host, path)
		}
	}

	fmt.Println("Launching the installer…")
	if err := openInstaller(dst); err != nil {
		return fmt.Errorf("downloaded to %s but could not launch it: %w", dst, err)
	}
	return nil
}

// writeRustDeskConfig drops a RustDesk2.toml into the per-user RustDesk config
// dir so the client connects to the self-hosted server on first run. Returns the
// path written.
func writeRustDeskConfig(cfg rustDeskConfig) (string, error) {
	dir, err := rustDeskConfigDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	relay := cfg.relay
	if relay == "" {
		relay = cfg.host // hbbr shares the host with hbbs
	}
	// Single-quoted (literal) TOML strings: base64 keys and hostnames never
	// contain a single quote, so no escaping needed.
	body := fmt.Sprintf("[options]\n"+
		"custom-rendezvous-server = '%s'\n"+
		"relay-server = '%s'\n"+
		"key = '%s'\n", cfg.host, relay, cfg.key)
	// api-server is only for the Pro web API (login/address book). OSS hbbs/hbbr
	// has none, so write it only when a caller explicitly provides one.
	if cfg.api != "" {
		body += fmt.Sprintf("api-server = '%s'\n", cfg.api)
	}

	path := filepath.Join(dir, "RustDesk2.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// rustDeskConfigDir is where the RustDesk client reads RustDesk2.toml per user.
func rustDeskConfigDir() (string, error) {
	switch runtime.GOOS {
	case "windows":
		base := os.Getenv("APPDATA")
		if base == "" {
			return "", fmt.Errorf("APPDATA is not set")
		}
		return filepath.Join(base, "RustDesk", "config"), nil
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Preferences", "com.carriez.RustDesk"), nil
	default: // linux and friends
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".config", "rustdesk"), nil
	}
}

func latestRustDeskRelease() (*ghRelease, error) {
	req, _ := http.NewRequest(http.MethodGet, rustDeskLatestAPI, nil)
	req.Header.Set("User-Agent", "network-utility-installer")
	req.Header.Set("Accept", "application/vnd.github+json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned HTTP %d", resp.StatusCode)
	}
	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	if len(rel.Assets) == 0 {
		return nil, fmt.Errorf("latest RustDesk release has no downloadable assets")
	}
	return &rel, nil
}

// pickRustDeskAsset chooses the installer asset matching this OS and CPU arch,
// preferring an exact arch match and falling back to any asset of the right kind.
//
// Linux gets a list of acceptable formats in preference order: the AppImage
// first, because it runs on any distro with no root and no package manager (which
// suits this installer's no-admin philosophy), then .deb / .rpm for people who'd
// rather install a native package.
func pickRustDeskAsset(assets []ghAsset) (ghAsset, error) {
	arch := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]

	var suffixes []string
	switch runtime.GOOS {
	case "windows":
		suffixes = []string{".exe"}
		if arch == "" {
			arch = "x86_64" // RustDesk ships Windows as x86_64
		}
	case "darwin":
		suffixes = []string{".dmg"}
	default: // linux and friends
		suffixes = []string{".appimage", ".deb", ".rpm"}
	}

	// Try each format in order; within a format, prefer an exact arch match but
	// accept any arch as a fallback before moving to the next (less-preferred)
	// format.
	for _, suffix := range suffixes {
		var fallback ghAsset
		for _, a := range assets {
			n := strings.ToLower(a.Name)
			if !strings.HasSuffix(n, suffix) {
				continue
			}
			if strings.Contains(n, "sciter") {
				continue // legacy Sciter UI build — prefer the default (Flutter) one
			}
			if arch != "" && strings.Contains(n, arch) {
				return a, nil // exact OS + arch match
			}
			if fallback.URL == "" {
				fallback = a
			}
		}
		if fallback.URL != "" {
			return fallback, nil
		}
	}
	return ghAsset{}, fmt.Errorf("no RustDesk installer (%s) found for %s/%s", strings.Join(suffixes, "/"), runtime.GOOS, runtime.GOARCH)
}

// downloadFile streams url to dst (following GitHub's redirect to its CDN).
func downloadFile(url, dst string) error {
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("User-Agent", "network-utility-installer")

	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst) // atomic swap so a partial download never looks complete
}

// openInstaller runs/opens the downloaded file so the user can install RustDesk.
func openInstaller(path string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command(path).Start() // run the installer .exe
	case "darwin":
		return exec.Command("open", path).Start() // mount the .dmg
	default: // linux and friends
		// An AppImage is the app itself, not a package: make it executable and
		// launch it straight away (no root, no package manager). Native packages
		// (.deb/.rpm) get handed to the desktop's GUI installer instead.
		if strings.HasSuffix(strings.ToLower(path), ".appimage") {
			if err := os.Chmod(path, 0o755); err != nil {
				return err
			}
			return exec.Command(path).Start()
		}
		return exec.Command("xdg-open", path).Start() // hand the .deb/.rpm to the OS
	}
}

// downloadsDir returns the user's Downloads folder, falling back to home.
func downloadsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	d := filepath.Join(home, "Downloads")
	if fi, err := os.Stat(d); err == nil && fi.IsDir() {
		return d
	}
	return home
}
