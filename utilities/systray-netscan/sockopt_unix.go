//go:build darwin || linux

package main

import "syscall"

// enableBroadcast sets SO_BROADCAST so the UDP socket may send to broadcast addresses.
func enableBroadcast(fd uintptr) {
	_ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
}
