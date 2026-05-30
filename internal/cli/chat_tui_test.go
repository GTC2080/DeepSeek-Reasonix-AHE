package cli

import (
	"strings"
	"testing"
)

// TestEventClassifierAgentPrefixes covers the agent-emitted event lines that
// must finalize the streaming answer before being committed: tool dispatch,
// usage / notice, finish warning, and the two coordinator markers.
func TestEventClassifierAgentPrefixes(t *testing.T) {
	cases := []string{
		"  · 1200 tok · in 1000 (900 cached / 100 new) · out 200\n",
		"  · tool output truncated: 12345 of 50000 bytes elided\n",
		"  · compacted 8 messages → summary\n",
		"  -> read_file {\"path\":\"foo\"}\n",
		"  ! response truncated: hit max output tokens\n",
		"[planner · planning]\n",
		"[executor · executing]\n",
	}
	for _, c := range cases {
		if !isEventChunk(c) {
			t.Errorf("agent-shape chunk should be an event line: %q", c)
		}
	}
}

// TestReasoningRoutedByDimPrefix locks in that reasoning is recognised by its
// dim prefix (handled before isEventChunk in ingestChunk), not by isEventChunk.
func TestReasoningRoutedByDimPrefix(t *testing.T) {
	for _, c := range []string{"\x1b[2m  ▎ thinking\x1b[0m\n", "\x1b[2mthinking continuation\x1b[0m"} {
		if !strings.HasPrefix(c, "\x1b[2m") {
			t.Errorf("reasoning chunk should carry the dim prefix: %q", c)
		}
		if isEventChunk(c) {
			t.Errorf("reasoning is routed by the dim prefix, not isEventChunk: %q", c)
		}
	}
}

// TestEventClassifierModelContent locks in that model output starting with "["
// — links, slice literals, admonitions — is NOT treated as an event line, so it
// stays in the answer buffer and renders as one markdown block.
func TestEventClassifierModelContent(t *testing.T) {
	cases := []string{
		"[]SessionInfo, error)\n",      // continuation of a table cell
		"[link](https://example.com)",  // markdown link at line start
		"[1, 2, 3]",                    // array literal
		"[]string{\"a\"}\n",            // Go slice literal
		"[Note]: this is important.\n", // admonition / footnote ref
		"[A] some content\n",           // bracketed tag
		"[planner | planning]\n",       // wrong separator, not coordinator
		"plain model text",             // no special prefix
		"## heading",                   // markdown heading
		"| col1 | col2 |\n",            // table row
	}
	for _, c := range cases {
		if isEventChunk(c) {
			t.Errorf("model content should NOT be an event line: %q", c)
		}
	}
}
