package cli

import (
	"os"

	"golang.org/x/term"
)

// colorEnabled is decided once at startup: only colorize when writing to a real
// terminal and the user hasn't opted out via NO_COLOR (https://no-color.org) or
// a dumb TERM. Piped/redirected output and CI stay plain so scripts aren't broken.
var colorEnabled = detectColor()

func detectColor() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	return term.IsTerminal(int(os.Stdout.Fd()))
}

const (
	ansiReset   = "\033[0m"
	ansiBold    = "\033[1m"
	ansiDim     = "\033[2m"
	ansiGreen   = "\033[32m"
	ansiYellow  = "\033[33m"
	ansiReverse = "\033[7m"
	// ansiAccent is reasonix's brand colour: a warm copper (xterm-256 #173) chosen
	// to read on both dark and light terminals. It marks titles, prompts, and
	// box borders so the eye finds the same hue every time.
	ansiAccent = "\033[38;5;173m"
)

func sgr(code, s string) string {
	if !colorEnabled {
		return s
	}
	return code + s + ansiReset
}

func bold(s string) string    { return sgr(ansiBold, s) }
func dim(s string) string     { return sgr(ansiDim, s) }
func green(s string) string   { return sgr(ansiGreen, s) }
func yellow(s string) string  { return sgr(ansiYellow, s) }
func accent(s string) string  { return sgr(ansiAccent, s) }
func reverse(s string) string { return sgr(ansiReverse, s) }
