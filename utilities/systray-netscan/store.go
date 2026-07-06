package main

import (
	"os"
	"path/filepath"
	"strings"
)

// State persisted between runs, in the OS config dir (cross-platform via os.UserConfigDir).

func confDir() string {
	base, err := os.UserConfigDir()
	if err != nil {
		base, _ = os.UserHomeDir()
	}
	d := filepath.Join(base, "systray-netscan")
	_ = os.MkdirAll(d, 0o755)
	return d
}

func confFile(name string) string { return filepath.Join(confDir(), name) }

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func readLines(name string) []string {
	b, err := os.ReadFile(confFile(name))
	if err != nil {
		return nil
	}
	var out []string
	for _, ln := range strings.Split(string(b), "\n") {
		ln = strings.TrimSpace(ln)
		if ln != "" {
			out = append(out, ln)
		}
	}
	return out
}

func writeString(name, s string) { _ = os.WriteFile(confFile(name), []byte(s), 0o644) }

func LoadSubnets() []string { return readLines("subnets.txt") }

func SaveSubnet(cidr string) {
	for _, s := range LoadSubnets() {
		if s == cidr {
			return
		}
	}
	subs := append(LoadSubnets(), cidr)
	writeString("subnets.txt", strings.Join(subs, "\n")+"\n")
}

func RemoveSubnet(cidr string) {
	var keep []string
	for _, s := range LoadSubnets() {
		if s != cidr {
			keep = append(keep, s)
		}
	}
	writeString("subnets.txt", strings.Join(keep, "\n")+"\n")
}

func LoadActive() string {
	if l := readLines("active.txt"); len(l) > 0 {
		return l[0]
	}
	return ""
}
func SaveActive(cidr string) { writeString("active.txt", cidr+"\n") }

func LoadSort() string {
	if l := readLines("sort.txt"); len(l) > 0 && l[0] == "host" {
		return "host"
	}
	return "ip"
}
func SaveSort(mode string) { writeString("sort.txt", mode+"\n") }
