package cli

import (
	"testing"

	"reasonix/internal/command"
)

func TestChatCommandLookupAndNames(t *testing.T) {
	m := chatTUI{commands: []command.Command{{Name: "review"}, {Name: "git:commit"}}}

	if _, ok := m.lookupCommand("review"); !ok {
		t.Error("review should be found")
	}
	if _, ok := m.lookupCommand("git:commit"); !ok {
		t.Error("git:commit should be found")
	}
	if _, ok := m.lookupCommand("missing"); ok {
		t.Error("missing should not be found")
	}
	if got := m.commandNames(); got != "/review · /git:commit" {
		t.Errorf("commandNames = %q", got)
	}

	if got := (&chatTUI{}).commandNames(); got != "" {
		t.Errorf("empty commandNames = %q, want \"\"", got)
	}
}
