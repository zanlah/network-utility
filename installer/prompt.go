package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// stdin is a shared buffered reader so successive prompts don't drop input.
var stdin = bufio.NewReader(os.Stdin)

// isInteractive reports whether stdin is a real terminal (not a pipe/redirect).
func isInteractive() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// promptApps asks which tools to install. It uses an arrow-key + space checkbox
// picker when the terminal supports raw mode, and falls back to typing numbers
// (e.g. 1,2) on terminals that don't.
func promptApps() []app {
	if apps, ok := selectApps(); ok {
		return apps
	}
	return promptAppsByNumber()
}

// promptAppsByNumber is the typed fallback: a numbered list, Enter/"all" for
// everything, or a comma list of numbers like "1,2".
func promptAppsByNumber() []app {
	fmt.Printf("Which tools do you want to install?\n\n")
	for i, a := range allApps {
		fmt.Printf("  %d) %s %-16s — %s\n", i+1, a.icon, a.title, a.desc)
	}
	fmt.Println()
	for {
		line := readLine("Enter numbers (e.g. 1,2), or 'all'", "all")
		if sel, ok := parseNumberSelection(line); ok {
			return sel
		}
		fmt.Printf("  ↳ didn't understand %q — pick from 1–%d, or 'all'.\n", line, len(allApps))
	}
}

// parseNumberSelection turns "all" / "1" / "1,2" into the matching apps.
func parseNumberSelection(s string) ([]app, bool) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "all" || s == "both" || s == "" {
		return allApps, true
	}
	var out []app
	seen := map[int]bool{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		n, err := strconv.Atoi(part)
		if err != nil || n < 1 || n > len(allApps) || seen[n] {
			if seen[n] {
				continue
			}
			return nil, false
		}
		seen[n] = true
		out = append(out, allApps[n-1])
	}
	return out, len(out) > 0
}

// promptString asks a free-text question with a default shown in brackets.
func promptString(question, def string) string {
	return readLine(question, def)
}

// promptYesNo asks a yes/no question; def is the answer for a bare Enter.
func promptYesNo(question string, def bool) bool {
	hint := "Y/n"
	if !def {
		hint = "y/N"
	}
	for {
		line := strings.ToLower(strings.TrimSpace(readRaw(fmt.Sprintf("%s [%s]: ", question, hint))))
		switch line {
		case "":
			return def
		case "y", "yes":
			return true
		case "n", "no":
			return false
		}
		fmt.Println("  ↳ please answer y or n.")
	}
}

// readLine prints "question [default]:" and returns the trimmed answer or default.
func readLine(question, def string) string {
	ans := strings.TrimSpace(readRaw(fmt.Sprintf("%s [%s]: ", question, def)))
	if ans == "" {
		return def
	}
	return ans
}

// readRaw prints a prompt and reads one line, returning "" on EOF.
func readRaw(prompt string) string {
	fmt.Print(prompt)
	line, err := stdin.ReadString('\n')
	if err != nil && line == "" {
		return ""
	}
	return strings.TrimRight(line, "\r\n")
}
