package main

import "strings"

// Listener is the normalized data contract — the same shape on every OS.
// Each platform's collector (collector_darwin.go / collector_windows.go) fills
// these out from OS-specific commands; the presenter (main.go) never sees an OS.
type Listener struct {
	Port    int
	PID     int
	Process string
	Addr    string
}

// webDevPorts — ports we highlight as "dev". Covers common frontend/backend dev
// servers plus the usual services people run in Docker (databases, queues, caches,
// search, dashboards, mail catchers, etc.). Docker-published ports are ALSO flagged
// dynamically by isDocker() regardless of number, so this list is just the well-knowns.
var webDevPorts = map[int]bool{
	// frontend / app dev servers
	3000: true, 3001: true, 3002: true, 3003: true, 3004: true, 3005: true,   3333: true,
	4000: true, 4200: true, 4321: true, 5000: true, 5001: true,
	5173: true, 5174: true, 5175: true, 4173: true,
	8000: true, 8001: true, 8080: true, 8081: true, 8082: true, 8443: true,
	8100: true, 1234: true, 1420: true, 6006: true, 5555: true,
	9000: true, 9001: true, 9090: true, 9229: true, 24678: true,
	19000: true, 19001: true, 19002: true, 8888: true,
	// databases
	3306: true, 33060: true, 5432: true, 6432: true, 15432: true,
	1433: true, 1521: true, 27017: true, 27018: true, 27019: true,
	5984: true, 8529: true, 7474: true, 7687: true, 8123: true,
	// caches / queues / streaming
	6379: true, 11211: true, 5672: true, 15672: true,
	9092: true, 2181: true, 4222: true, 8222: true,
	// search / observability
	9200: true, 9300: true, 5601: true, 8086: true,
	9411: true, 16686: true, 3100: true,
	// tooling / admin / infra
	5050: true, 8025: true, 1025: true, 1080: true,
	8200: true, 8500: true, 2379: true, 2380: true, 2375: true, 2376: true,
}

func isWebDev(p int) bool { return webDevPorts[p] }

// isDev reports whether a listener is "dev-relevant": a known dev port, or any port
// published by Docker. Used by the "Dev ports only" filter.
func isDev(l Listener) bool { return isWebDev(l.Port) || isDocker(l.Process) }

// isDocker reports whether a listener belongs to a Docker process — so every port
// Docker publishes (on whatever number) counts as a dev port. Matches Docker
// Desktop's backend/proxy across macOS and Windows.
func isDocker(process string) bool {
	p := strings.ToLower(process)
	return strings.HasPrefix(p, "com.docke") ||
		strings.Contains(p, "docker") ||
		strings.Contains(p, "vpnkit")
}
