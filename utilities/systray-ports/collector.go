package main

// Listener is the normalized data contract — the same shape on every OS.
// Each platform's collector (collector_darwin.go / collector_windows.go) fills
// these out from OS-specific commands; the presenter (main.go) never sees an OS.
type Listener struct {
	Port    int
	PID     int
	Process string
	Addr    string
}

// Web-dev ports we highlight. Shared across platforms.
var webDevPorts = map[int]bool{
	3000: true, 3001: true, 4200: true, 5000: true, 5173: true, 5174: true,
	8000: true, 8080: true, 8081: true, 8888: true, 9000: true,
	3306: true, 5432: true, 6379: true, 27017: true,
}

func isWebDev(p int) bool { return webDevPorts[p] }
