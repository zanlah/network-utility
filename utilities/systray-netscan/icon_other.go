//go:build !windows

package main

// On macOS/Linux the menu-bar text title (SetTitle) is used instead of a tray
// icon, so there's nothing to set here.
func applyIcon() {}
