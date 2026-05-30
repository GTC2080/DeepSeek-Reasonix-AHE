package cli

import (
	"strings"
	"testing"
)

// TestCommitReasoningWrapsToWidth guards the ghost-border fix: every line a
// reasoning commit queues for scrollback must fit the viewport width, so
// bubbletea's Println erases each line to its end and the old input-box border
// can't bleed through after a wrapped reasoning row.
func TestCommitReasoningWrapsToWidth(t *testing.T) {
	const width = 40
	commit := []string{}
	m := &chatTUI{
		width:         width,
		reasoning:     &strings.Builder{},
		pendingCommit: &commit,
	}
	// Header (short, indented) + a long single-line reasoning paragraph, both
	// dim-wrapped exactly as the agent streams them.
	m.reasoning.WriteString("\x1b[2m  ▎ thinking\x1b[0m\n")
	m.reasoning.WriteString("\x1b[2m" + strings.Repeat("reason ", 30) + "\x1b[0m")

	m.commitReasoning()

	if len(commit) == 0 {
		t.Fatal("commitReasoning queued nothing")
	}
	for _, block := range commit {
		for _, line := range strings.Split(block, "\n") {
			if w := visibleWidth(line); w > width {
				t.Errorf("committed line exceeds width %d: width=%d %q", width, w, line)
			}
		}
	}
	// The header keeps its leading indent (short lines pass through verbatim).
	if !strings.Contains(commit[0], "  ▎ thinking") {
		t.Errorf("header indent not preserved: %q", commit[0])
	}
	// Reasoning must be cleared after committing.
	if m.reasoning.Len() != 0 {
		t.Error("reasoning buffer not reset")
	}
}
