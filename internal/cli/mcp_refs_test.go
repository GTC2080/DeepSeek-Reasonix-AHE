package cli

import (
	"reflect"
	"testing"
)

func TestParseRefTokens(t *testing.T) {
	cases := []struct {
		line string
		want []string
	}{
		{"see @docs:doc://x and @src/main.go", []string{"docs:doc://x", "src/main.go"}},
		{"trailing @file.go.", []string{"file.go"}},
		{"dedup @a @a", []string{"a"}},
		{"no refs here", nil},
		{"email a@b.com keeps token", []string{"b.com"}},
	}
	for _, c := range cases {
		got := parseRefTokens(c.line)
		if len(got) == 0 && len(c.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseRefTokens(%q) = %v, want %v", c.line, got, c.want)
		}
	}
}

func TestClassifyRef(t *testing.T) {
	known := map[string]bool{"docs": true}
	files := map[string]bool{"src/main.go": true, "README.md": true}
	exists := func(p string) bool { return files[p] }

	cases := []struct {
		token   string
		wantOK  bool
		wantKnd refKind
	}{
		{"docs:doc://style", true, refResource}, // known server + uri
		{"src/main.go", true, refFile},          // existing file
		{"README.md", true, refFile},            // existing file
		{"ghost:issue://1", false, 0},           // unknown server, no such file
		{"missing.go", false, 0},                // nonexistent path → not a ref
		{"docs:", false, 0},                     // empty uri → not a resource, no file
	}
	for _, c := range cases {
		r, ok := classifyRef(c.token, known, exists)
		if ok != c.wantOK {
			t.Errorf("classifyRef(%q) ok = %v, want %v", c.token, ok, c.wantOK)
			continue
		}
		if ok && r.kind != c.wantKnd {
			t.Errorf("classifyRef(%q) kind = %v, want %v", c.token, r.kind, c.wantKnd)
		}
	}
}
