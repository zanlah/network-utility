package main

import "testing"

// selecting RustDesk must pull in Tailscale (the server is tailnet-only).
func TestEnforceDepsRustDeskAddsTailscale(t *testing.T) {
	apps := enforceDeps([]app{appByName(rustDeskAppName)})
	if !contains(apps, tailscaleAppName) {
		t.Fatalf("RustDesk selection did not add Tailscale; got %v", names(apps))
	}
}

// Tailscale on its own stays a single selection — no phantom RustDesk.
func TestEnforceDepsTailscaleAlone(t *testing.T) {
	apps := enforceDeps([]app{appByName(tailscaleAppName)})
	if len(apps) != 1 || apps[0].name != tailscaleAppName {
		t.Fatalf("Tailscale alone changed: %v", names(apps))
	}
}

// enforceDeps must not duplicate Tailscale when both are already picked.
func TestEnforceDepsNoDuplicate(t *testing.T) {
	apps := enforceDeps([]app{appByName(tailscaleAppName), appByName(rustDeskAppName)})
	if got := names(apps); len(got) != 2 {
		t.Fatalf("expected 2 apps, got %v", got)
	}
}

// The flag parser recognises the two external tools by name.
func TestParseAppSelectionExternals(t *testing.T) {
	apps, ok := parseAppSelection("tailscale,rustdesk")
	if !ok || !contains(apps, tailscaleAppName) || !contains(apps, rustDeskAppName) {
		t.Fatalf("parseAppSelection dropped an external tool: %v ok=%v", names(apps), ok)
	}
}

// The live picker's dependency enforcement mirrors enforceDeps.
func TestEnforceSelectedDeps(t *testing.T) {
	sel := make([]bool, len(allApps))
	sel[appIndex(rustDeskAppName)] = true
	enforceSelectedDeps(sel)
	if !sel[appIndex(tailscaleAppName)] {
		t.Fatal("ticking RustDesk did not auto-tick Tailscale")
	}
}
