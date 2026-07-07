package main

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

// ANSI helpers.
const (
	ansiHideCursor = "\x1b[?25l"
	ansiShowCursor = "\x1b[?25h"
	ansiClearDown  = "\x1b[J"
	ansiCyan       = "\x1b[36m"
	ansiGreen      = "\x1b[32m"
	ansiDim        = "\x1b[2m"
	ansiReset      = "\x1b[0m"
)

// selectApps runs a checkbox picker: ↑/↓ (or j/k) to move, space to toggle,
// 'a' to toggle all, enter to confirm. It returns the chosen apps. ok is false
// when a raw terminal isn't available, so the caller can fall back to typing.
func selectApps() (chosen []app, ok bool) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return nil, false
	}
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return nil, false
	}
	// Restore the terminal on every exit path, including Ctrl-C below.
	restore := func() {
		term.Restore(fd, oldState)
		fmt.Print(ansiShowCursor)
	}
	defer restore()

	selected := make([]bool, len(allApps))
	for i := range selected {
		selected[i] = !optOutByDefault[allApps[i].name] // opt-out tools (keyswap) start unchecked
	}
	cursor := 0
	n := len(allApps)

	fmt.Print(ansiHideCursor)
	render(os.Stdout, cursor, selected, true)

	buf := make([]byte, 4)
	for {
		read, err := os.Stdin.Read(buf)
		if err != nil || read == 0 {
			return nil, false
		}
		switch {
		case buf[0] == 3, buf[0] == 'q': // Ctrl-C or q → cancel the whole install
			restore()
			fmt.Println("\nCancelled.")
			os.Exit(0)

		case buf[0] == '\r' || buf[0] == '\n': // Enter → confirm
			var out []app
			for i, s := range selected {
				if s {
					out = append(out, allApps[i])
				}
			}
			if len(out) == 0 {
				continue // require at least one selection
			}
			fmt.Print("\r\n")
			return out, true

		case buf[0] == ' ': // toggle current
			selected[cursor] = !selected[cursor]

		case buf[0] == 'a': // toggle all on/off
			allOn := true
			for _, s := range selected {
				if !s {
					allOn = false
					break
				}
			}
			for i := range selected {
				selected[i] = !allOn
			}

		case buf[0] == 'k', read >= 3 && buf[0] == 27 && buf[1] == '[' && buf[2] == 'A': // up
			cursor = (cursor - 1 + n) % n

		case buf[0] == 'j', read >= 3 && buf[0] == 27 && buf[1] == '[' && buf[2] == 'B': // down
			cursor = (cursor + 1) % n
		}
		render(os.Stdout, cursor, selected, false)
	}
}

// render draws the checklist to w. On repeat draws it first rewinds over the
// previous block (header + one line per app) and clears downward, so it updates
// in place. Split out and writer-parameterised so the frame is unit-testable.
func render(w io.Writer, cursor int, selected []bool, first bool) {
	lines := len(allApps) + 2 // two header lines + one per app
	if !first {
		fmt.Fprintf(w, "\x1b[%dA", lines)
	}
	fmt.Fprint(w, "\r"+ansiClearDown)
	fmt.Fprint(w, "Which tools do you want to install?\r\n")
	fmt.Fprintf(w, "%s↑/↓ move · space toggle · a all · enter confirm%s\r\n", ansiDim, ansiReset)
	for i, a := range allApps {
		pointer := "  "
		if i == cursor {
			pointer = ansiCyan + "❯" + ansiReset + " "
		}
		box := "◯"
		if selected[i] {
			box = ansiGreen + "◉" + ansiReset
		}
		fmt.Fprintf(w, "%s%s %s %-16s %s— %s%s\r\n", pointer, box, a.icon, a.title, ansiDim, a.desc, ansiReset)
	}
}
