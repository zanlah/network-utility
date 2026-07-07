//go:build !windows

package main

import "os/exec"

// command is a plain exec.Command on non-Windows platforms (no console windows
// to hide). Kept so shared code can use command() uniformly.
func command(name string, args ...string) *exec.Cmd { return exec.Command(name, args...) }
