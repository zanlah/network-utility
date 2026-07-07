//go:build windows

package main

import (
	_ "embed"

	"github.com/getlantern/systray"
)

// On Windows the tray shows an icon (not a text title), so an icon is required
// or the tool is invisible in the notification area. macOS uses the emoji
// SetTitle instead — see icon_other.go.
//
//go:embed icon.ico
var iconData []byte

func applyIcon() { systray.SetIcon(iconData) }
