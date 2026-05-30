package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/tool"
)

// argsJSON marshals m into the JSON form a tool expects. Tests must not build
// the JSON by concatenating Go strings: on Windows, t.TempDir() returns a path
// like C:\Users\… and the embedded backslashes are interpreted as JSON string
// escapes (\U triggers a parse error). json.Marshal handles the escaping.
func argsJSON(t *testing.T, m map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return json.RawMessage(b)
}

func runTool(t *testing.T, tl tool.Tool, m map[string]any) string {
	t.Helper()
	out, err := tl.Execute(context.Background(), argsJSON(t, m))
	if err != nil {
		t.Fatalf("%s: %v", tl.Name(), err)
	}
	return out
}

func TestBuiltinsRegistered(t *testing.T) {
	want := []string{"bash", "edit_file", "glob", "grep", "ls", "multi_edit", "read_file", "web_fetch", "write_file"}
	for _, name := range want {
		if _, ok := tool.LookupBuiltin(name); !ok {
			t.Errorf("built-in %q not registered", name)
		}
	}
}

// TestBuiltinReadOnlyClassification locks in which built-ins the agent may
// parallelise. Flipping a writer (write_file, edit_file, bash) to ReadOnly
// would re-order writes against reads in the same turn; this test fails fast
// if that ever happens. bash specifically must stay non-ReadOnly even though
// many invocations are pure reads — args aren't introspected.
func TestBuiltinReadOnlyClassification(t *testing.T) {
	readOnly := map[string]bool{
		"read_file": true, "ls": true, "glob": true, "grep": true, "web_fetch": true,
		"write_file": false, "edit_file": false, "multi_edit": false, "bash": false,
	}
	for name, want := range readOnly {
		tl, ok := tool.LookupBuiltin(name)
		if !ok {
			t.Fatalf("built-in %q not registered", name)
		}
		if got := tl.ReadOnly(); got != want {
			t.Errorf("%s.ReadOnly() = %v, want %v", name, got, want)
		}
	}
}

func TestReadFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "src.go")
	body := "package main\n\nfunc main() {}\n"
	os.WriteFile(f, []byte(body), 0o644)

	out := runTool(t, readFile{}, map[string]any{"path": f})
	// Line numbers must be present, right-aligned, with the arrow separator.
	for _, want := range []string{"1→package main", "2→", "3→func main"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestReadFileOffsetLimit(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "many.txt")
	var b strings.Builder
	for i := 1; i <= 50; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	os.WriteFile(f, []byte(b.String()), 0o644)

	out := runTool(t, readFile{}, map[string]any{"path": f, "offset": 10, "limit": 5})
	// Should see lines 11-15 only.
	for _, want := range []string{"11→line 11", "15→line 15"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	for _, leak := range []string{"line 5\n", "line 16\n", "line 20\n"} {
		if strings.Contains(out, leak) {
			t.Errorf("leaked %q (outside the slice)\n%s", leak, out)
		}
	}
	// Trailer announces what's left so the model can paginate.
	if !strings.Contains(out, "more line") || !strings.Contains(out, "offset=15") {
		t.Errorf("pagination hint missing:\n%s", out)
	}
}

func TestReadFileBinary(t *testing.T) {
	f := filepath.Join(t.TempDir(), "blob")
	os.WriteFile(f, []byte{0x7f, 'E', 'L', 'F', 0, 0, 0}, 0o644)

	_, err := readFile{}.Execute(context.Background(), argsJSON(t, map[string]any{"path": f}))
	if err == nil || !strings.Contains(err.Error(), "binary") {
		t.Errorf("expected binary-file error, got %v", err)
	}
}

func TestReadFileEmpty(t *testing.T) {
	f := filepath.Join(t.TempDir(), "empty.txt")
	os.WriteFile(f, nil, 0o644)
	if out := runTool(t, readFile{}, map[string]any{"path": f}); !strings.Contains(out, "empty") {
		t.Errorf("empty file should report empty, got %q", out)
	}
}

func TestEditFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "a.txt")
	os.WriteFile(f, []byte("hello world\n"), 0o644)

	runTool(t, editFile{}, map[string]any{"path": f, "old_string": "world", "new_string": "reasonix"})
	if b, _ := os.ReadFile(f); string(b) != "hello reasonix\n" {
		t.Fatalf("after edit = %q", b)
	}

	// Non-unique old_string must error and not modify the file.
	os.WriteFile(f, []byte("x x x"), 0o644)
	args := argsJSON(t, map[string]any{"path": f, "old_string": "x", "new_string": "y"})
	if _, err := (editFile{}).Execute(context.Background(), args); err == nil {
		t.Fatal("expected not-unique error")
	}
	if b, _ := os.ReadFile(f); string(b) != "x x x" {
		t.Fatalf("file modified despite error: %q", b)
	}
}

func TestMultiEdit(t *testing.T) {
	f := filepath.Join(t.TempDir(), "src.go")
	body := "package old\n\nfunc old() {\n\told()\n}\n"
	os.WriteFile(f, []byte(body), 0o644)

	// Two edits: rename the package (unique) then sweep every old → new.
	out := runTool(t, multiEdit{}, map[string]any{
		"path": f,
		"edits": []map[string]any{
			{"old_string": "package old", "new_string": "package new"},
			{"old_string": "old", "new_string": "reasonix", "replace_all": true},
		},
	})
	if !strings.Contains(out, "multi_edit") || !strings.Contains(out, "2 edits applied") {
		t.Errorf("summary unexpected: %q", out)
	}
	got, _ := os.ReadFile(f)
	want := "package new\n\nfunc reasonix() {\n\treasonix()\n}\n"
	if string(got) != want {
		t.Errorf("after multi_edit = %q\n          want = %q", got, want)
	}
}

// TestMultiEditAtomicity is the safety guarantee: if any edit fails, the file
// stays exactly as it was. A chained sequence of single edit_file calls would
// have left a half-written intermediate state.
func TestMultiEditAtomicity(t *testing.T) {
	f := filepath.Join(t.TempDir(), "a.txt")
	original := "alpha\nbeta\ngamma\n"
	os.WriteFile(f, []byte(original), 0o644)

	args := argsJSON(t, map[string]any{
		"path": f,
		"edits": []map[string]any{
			{"old_string": "alpha", "new_string": "ALPHA"},
			{"old_string": "no-such-text", "new_string": "x"},
			{"old_string": "gamma", "new_string": "GAMMA"},
		},
	})
	if _, err := (multiEdit{}).Execute(context.Background(), args); err == nil {
		t.Fatal("expected failure on the missing edit")
	}
	got, _ := os.ReadFile(f)
	if string(got) != original {
		t.Errorf("file was modified despite failure:\n got %q\nwant %q", got, original)
	}
}

func TestGrep(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main\nfunc Foo() {}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.go"), []byte("var x = 1\n"), 0o644)

	out := runTool(t, grepTool{}, map[string]any{"pattern": "func ", "path": dir})
	if !strings.Contains(out, "Foo") || strings.Contains(out, "var x") {
		t.Fatalf("grep result = %q", out)
	}
}

// TestWebFetchHTML serves a tiny HTML page and checks the reducer keeps the
// readable text while removing scripts, styles, and tags.
func TestWebFetchHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html>
<html><head><title>T</title><style>body{color:red}</style></head>
<body>
<h1>Hello &amp; world</h1>
<script>alert("bad")</script>
<p>Visible text.</p>
</body></html>`))
	}))
	defer srv.Close()

	out := runTool(t, webFetch{}, map[string]any{"url": srv.URL})
	for _, want := range []string{"Hello & world", "Visible text", "text/html"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	for _, leak := range []string{"<script", "alert(", "<style", "<h1>", "&amp;"} {
		if strings.Contains(out, leak) {
			t.Errorf("leaked raw HTML/script %q", leak)
		}
	}
}

// TestWebFetchPlain confirms non-HTML bodies pass through untouched (apart
// from the prepended status header).
func TestWebFetchPlain(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("line1\nline2\n"))
	}))
	defer srv.Close()

	out := runTool(t, webFetch{}, map[string]any{"url": srv.URL})
	if !strings.Contains(out, "line1") || !strings.Contains(out, "line2") {
		t.Errorf("plain body content missing:\n%s", out)
	}
	if strings.Contains(out, "<") {
		t.Errorf("html reducer ran on text/plain: %s", out)
	}
}

// TestWebFetchSchemeRejected blocks anything that's not http(s) so the tool
// can't be tricked into reading file:// or arbitrary URI schemes.
func TestWebFetchSchemeRejected(t *testing.T) {
	_, err := webFetch{}.Execute(context.Background(), argsJSON(t, map[string]any{"url": "file:///etc/passwd"}))
	if err == nil || !strings.Contains(err.Error(), "http(s)") {
		t.Errorf("expected scheme rejection, got %v", err)
	}
}

func TestLsAndGlob(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "x.txt"), []byte("hi"), 0o644)
	os.Mkdir(filepath.Join(dir, "sub"), 0o755)

	ls := runTool(t, listDir{}, map[string]any{"path": dir})
	if !strings.Contains(ls, "x.txt") || !strings.Contains(ls, "sub/") {
		t.Fatalf("ls result = %q", ls)
	}

	g := runTool(t, globTool{}, map[string]any{"pattern": filepath.Join(dir, "*.txt")})
	if !strings.Contains(g, "x.txt") {
		t.Fatalf("glob result = %q", g)
	}
}
