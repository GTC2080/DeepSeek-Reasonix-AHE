package cli

import (
	"regexp"
	"strings"

	"github.com/rivo/uniseg"
)

// ansiSGR matches ANSI Select-Graphic-Rendition sequences (\e[…m). Width
// measurement strips these so colored content lines fit the box correctly.
var ansiSGR = regexp.MustCompile("\x1b\\[[0-9;]*m")

// visibleWidth returns the printable column width of s after stripping ANSI
// SGR codes. It goes through rivo/uniseg, which measures by *grapheme cluster*
// rather than by rune: emoji ZWJ sequences (👨‍👩‍👧‍👦), keycaps (1️⃣), flags, and
// skin-tone modifiers each occupy one cell-pair, where a rune-by-rune sum (the
// old go-runewidth path) over-counted them and drifted the box/table rails.
// uniseg is already in the dep tree via bubbletea/lipgloss.
func visibleWidth(s string) int {
	return uniseg.StringWidth(ansiSGR.ReplaceAllString(s, ""))
}

// padRight returns s padded with spaces on the right until it occupies w
// terminal columns (visible width, not bytes). Strings already at or beyond
// width are returned unchanged. Use this instead of fmt's %-Ns when content
// may contain CJK or ANSI SGR codes.
func padRight(s string, w int) string {
	pad := w - visibleWidth(s)
	if pad <= 0 {
		return s
	}
	return s + strings.Repeat(" ", pad)
}

// boxed wraps content in a rounded box drawn with the brand accent. Width
// auto-fits the longest line plus one column of padding on each side. The
// result always ends with a trailing newline so callers can Print it directly.
func boxed(lines []string) string {
	inner := 0
	for _, l := range lines {
		if w := visibleWidth(l); w > inner {
			inner = w
		}
	}
	inner += 2 // one space of padding on each side
	bar := strings.Repeat("─", inner)

	var b strings.Builder
	b.WriteString(accent("╭" + bar + "╮"))
	b.WriteByte('\n')
	for _, l := range lines {
		gap := inner - visibleWidth(l) - 2
		if gap < 0 {
			gap = 0
		}
		b.WriteString(accent("│"))
		b.WriteByte(' ')
		b.WriteString(l)
		b.WriteString(strings.Repeat(" ", gap))
		b.WriteByte(' ')
		b.WriteString(accent("│"))
		b.WriteByte('\n')
	}
	b.WriteString(accent("╰" + bar + "╯"))
	b.WriteByte('\n')
	return b.String()
}
