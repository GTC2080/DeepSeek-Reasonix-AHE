package cli

import (
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/command"
)

// writeAt creates dir/rel (with parents) holding content, for fs-backed tests.
func writeAt(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSlashCompletionFilterAndAccept(t *testing.T) {
	m := newTestChatTUI()
	m.input.SetValue("/co")
	m.updateCompletion()

	if !m.completion.active || m.completion.kind != compSlash {
		t.Fatalf("typing /co should open the slash menu: %+v", m.completion)
	}
	// Only /compact matches the "/co" prefix among the built-ins.
	if len(m.completion.items) != 1 || m.completion.items[0].label != "/compact" {
		t.Fatalf("filter = %v, want just /compact", labels(m.completion.items))
	}

	m.acceptCompletion()
	if got := m.input.Value(); got != "/compact " {
		t.Errorf("accept should fill the input, got %q", got)
	}
	if m.completion.active {
		t.Error("menu should close after accept")
	}
}

func TestSlashCompletionIncludesCustomCommands(t *testing.T) {
	m := newTestChatTUI()
	m.commands = []command.Command{{Name: "review", Description: "review the diff"}}
	m.input.SetValue("/re")
	m.updateCompletion()

	if !hasLabel(m.completion.items, "/review") {
		t.Errorf("custom command should appear in completion: %v", labels(m.completion.items))
	}
}

func TestCompletionClosesOnSpaceAndNonMatch(t *testing.T) {
	m := newTestChatTUI()

	m.input.SetValue("/compact ") // space → typing args, not naming a command
	m.updateCompletion()
	if m.completion.active {
		t.Error("menu should close once a space is typed (now entering args)")
	}

	m.input.SetValue("/zzz") // no command matches
	m.updateCompletion()
	if m.completion.active {
		t.Error("menu should close when nothing matches")
	}

	m.input.SetValue("hello") // not a slash line
	m.updateCompletion()
	if m.completion.active {
		t.Error("menu should be inactive for non-slash input")
	}
}

func TestMoveCompletionWraps(t *testing.T) {
	m := newTestChatTUI()
	m.completion = completion{active: true, kind: compSlash, items: []compItem{{label: "/a"}, {label: "/b"}, {label: "/c"}}, sel: 0}
	m.moveCompletion(-1)
	if m.completion.sel != 2 {
		t.Errorf("up from first should wrap to last, got %d", m.completion.sel)
	}
	m.moveCompletion(1)
	if m.completion.sel != 0 {
		t.Errorf("down from last should wrap to first, got %d", m.completion.sel)
	}
}

func TestActiveAtToken(t *testing.T) {
	cases := []struct {
		val     string
		wantTok string
		wantOK  bool
		wantAt  int
	}{
		{"@", "", true, 0},
		{"look at @src/m", "src/m", true, 8},
		{"@internal/agent/", "internal/agent/", true, 0},
		{"a@b.com", "", false, 0},  // '@' not whitespace-preceded → not a ref
		{"@foo bar", "", false, 0}, // cursor token after the space isn't an @ref
		{"plain text", "", false, 0},
	}
	for _, c := range cases {
		at, tok, ok := activeAtToken(c.val)
		if ok != c.wantOK || (ok && (tok != c.wantTok || at != c.wantAt)) {
			t.Errorf("activeAtToken(%q) = (%d,%q,%v), want (%d,%q,%v)", c.val, at, tok, ok, c.wantAt, c.wantTok, c.wantOK)
		}
	}
}

func TestSplitPathToken(t *testing.T) {
	cases := []struct{ in, dir, frag string }{
		{"main", "", "main"},
		{"internal/age", "internal/", "age"},
		{"a/b/c", "a/b/", "c"},
		{"internal/", "internal/", ""},
	}
	for _, c := range cases {
		if d, f := splitPathToken(c.in); d != c.dir || f != c.frag {
			t.Errorf("splitPathToken(%q) = (%q,%q), want (%q,%q)", c.in, d, f, c.dir, c.frag)
		}
	}
}

// TestFileItemsOneLevel verifies @ completion lists exactly one directory level
// (no recursion): a subdir shows as a descendable entry, its contents do not.
func TestFileItemsOneLevel(t *testing.T) {
	dir := t.TempDir()
	writeAt(t, dir, "alpha.go", "x")
	writeAt(t, dir, "sub/deep.go", "y") // creates sub/ with a file inside
	writeAt(t, dir, ".hidden", "z")

	m := newTestChatTUI()
	items := m.fileItems(dir + "/") // token = "<tmp>/", frag = ""

	if !hasLabel(items, "alpha.go") {
		t.Errorf("file alpha.go should be listed: %v", labels(items))
	}
	if !hasLabel(items, "sub/") {
		t.Errorf("subdir should be listed as 'sub/': %v", labels(items))
	}
	if hasLabel(items, "deep.go") {
		t.Errorf("nested file deep.go must NOT be listed (one level only): %v", labels(items))
	}
	if hasLabel(items, ".hidden") {
		t.Errorf("hidden file should be skipped unless frag starts with '.': %v", labels(items))
	}
	// The subdir entry must be a descend (accepting it navigates into it).
	for _, it := range items {
		if it.label == "sub/" && !it.descend {
			t.Error("directory entry should be a descend")
		}
	}
}

func TestFileItemsHiddenWhenDotTyped(t *testing.T) {
	dir := t.TempDir()
	writeAt(t, dir, ".hidden", "z")
	m := newTestChatTUI()
	items := m.fileItems(dir + "/.") // frag = "." → show hidden
	if !hasLabel(items, ".hidden") {
		t.Errorf("hidden file should appear when frag starts with '.': %v", labels(items))
	}
}

func labels(items []compItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.label
	}
	return out
}

func hasLabel(items []compItem, label string) bool {
	for _, it := range items {
		if it.label == label {
			return true
		}
	}
	return false
}
