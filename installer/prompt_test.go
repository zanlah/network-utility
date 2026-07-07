package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func names(apps []app) []string {
	out := make([]string, len(apps))
	for i, a := range apps {
		out[i] = a.name
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestParseNumberSelection(t *testing.T) {
	cases := []struct {
		in   string
		want []string
		ok   bool
	}{
		{"", []string{"systray-ports", "systray-netscan"}, true},
		{"all", []string{"systray-ports", "systray-netscan"}, true},
		{"both", []string{"systray-ports", "systray-netscan"}, true},
		{"1", []string{"systray-ports"}, true},
		{"2", []string{"systray-netscan"}, true},
		{"1,2", []string{"systray-ports", "systray-netscan"}, true},
		{"2, 1", []string{"systray-netscan", "systray-ports"}, true},
		{"1,1", []string{"systray-ports"}, true}, // dedup
		{"3", nil, false},
		{"x", nil, false},
		{"0", nil, false},
	}
	for _, c := range cases {
		got, ok := parseNumberSelection(c.in)
		if ok != c.ok || (ok && !eq(names(got), c.want)) {
			t.Errorf("parseNumberSelection(%q) = %v,%v; want %v,%v", c.in, names(got), ok, c.want, c.ok)
		}
	}
}

func TestParseAppSelection(t *testing.T) {
	cases := []struct {
		in   string
		want []string
		ok   bool
	}{
		{"all", []string{"systray-ports", "systray-netscan"}, true},
		{"ports", []string{"systray-ports"}, true},
		{"netscan", []string{"systray-netscan"}, true},
		{"systray-ports", []string{"systray-ports"}, true},
		{"ports,netscan", []string{"systray-ports", "systray-netscan"}, true},
		{"nope", nil, false},
	}
	for _, c := range cases {
		got, ok := parseAppSelection(c.in)
		if ok != c.ok || (ok && !eq(names(got), c.want)) {
			t.Errorf("parseAppSelection(%q) = %v,%v; want %v,%v", c.in, names(got), ok, c.want, c.ok)
		}
	}
}

func TestRenderFrame(t *testing.T) {
	// Cursor on row 0; row 0 selected, row 1 not.
	selected := make([]bool, len(allApps))
	selected[0] = true
	var buf bytes.Buffer
	render(&buf, 0, selected, true)
	out := buf.String()

	if !strings.Contains(out, "Which tools do you want to install?") {
		t.Error("missing header")
	}
	if !strings.Contains(out, "❯") {
		t.Error("missing cursor pointer")
	}
	if !strings.Contains(out, "◉") { // a selected row
		t.Error("missing filled checkbox")
	}
	if !strings.Contains(out, "◯") { // an unselected row
		t.Error("missing empty checkbox")
	}
	for _, a := range allApps {
		if !strings.Contains(out, a.title) {
			t.Errorf("frame missing tool %q", a.title)
		}
	}
	// First frame must NOT emit the cursor-up rewind sequence.
	if strings.Contains(out, "\x1b[4A") {
		t.Error("first frame should not rewind")
	}

	// A repeat frame should rewind by header+rows lines.
	buf.Reset()
	render(&buf, 1, selected, false)
	if !strings.Contains(buf.String(), "\x1b[4A") { // 2 header + 2 apps
		t.Error("repeat frame should rewind 4 lines")
	}
}

func TestExpandPath(t *testing.T) {
	home, _ := os.UserHomeDir()
	if got := expandPath("~/foo"); got != filepath.Join(home, "foo") {
		t.Errorf("expandPath(~/foo) = %q; want %q", got, filepath.Join(home, "foo"))
	}
	if got := expandPath("~"); got != home {
		t.Errorf("expandPath(~) = %q; want %q", got, home)
	}
	// Relative paths become absolute.
	if got := expandPath("relbin"); !filepath.IsAbs(got) {
		t.Errorf("expandPath(relbin) = %q; want absolute", got)
	}
}
