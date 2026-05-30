package cli

import (
	"strings"
	"testing"
)

func TestVisibleWidthGraphemeClusters(t *testing.T) {
	cases := []struct {
		name string
		s    string
		want int
	}{
		{"ascii", "abc", 3},
		{"cjk", "中文", 4},
		{"emoji", "🔥", 2},
		// uniseg measures the keycap sequence as 1 (terminals disagree on VS16
		// width — this is the library's UAX#11 choice, and we follow it).
		{"keycap", "1️⃣", 1},
		// The regression that motivated the switch: a ZWJ family is one cluster
		// occupying one emoji's width, not the rune-by-rune sum (which was 8).
		{"zwj-family", "👨‍👩‍👧‍👦", 2},
		{"ansi-stripped", "\x1b[31mab\x1b[0m", 2},
		{"mixed", "a中🔥", 5},
	}
	for _, c := range cases {
		if got := visibleWidth(c.s); got != c.want {
			t.Errorf("%s: visibleWidth(%q) = %d, want %d", c.name, c.s, got, c.want)
		}
	}
}

func TestChunkByWidthKeepsClustersIntact(t *testing.T) {
	// A ZWJ family (width 2) fills a width-2 line; the trailing ascii spills to
	// the next chunk. The cluster must never be torn across chunks.
	family := "👨‍👩‍👧‍👦"
	chunks := chunkByWidth(family+"x", 2)
	if len(chunks) != 2 || chunks[0] != family || chunks[1] != "x" {
		t.Fatalf("family split wrong: %q (want [%q x])", chunks, family)
	}

	// CJK hard-breaks at the column boundary.
	cjk := chunkByWidth("中文字", 4)
	if len(cjk) != 2 || cjk[0] != "中文" || cjk[1] != "字" {
		t.Errorf("cjk chunks = %q, want [中文 字]", cjk)
	}

	// ANSI SGR escapes are preserved verbatim and counted as zero width, so the
	// two visible chars fit a width-2 line on one chunk with styling intact.
	styled := chunkByWidth("\x1b[31mab\x1b[0m", 2)
	if len(styled) != 1 || styled[0] != "\x1b[31mab\x1b[0m" {
		t.Errorf("styled chunks = %q, want one styled chunk", styled)
	}
	// Every chunk stays within the column budget.
	for _, ch := range chunkByWidth(strings.Repeat("中", 10), 6) {
		if visibleWidth(ch) > 6 {
			t.Errorf("chunk %q exceeds width 6 (got %d)", ch, visibleWidth(ch))
		}
	}
}
