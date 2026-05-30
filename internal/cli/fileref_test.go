package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFileRef(t *testing.T) {
	dir := t.TempDir()

	textPath := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(textPath, []byte("line one\nline two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	binPath := filepath.Join(dir, "blob.bin")
	if err := os.WriteFile(binPath, []byte{'a', 0x00, 'b'}, 0o644); err != nil {
		t.Fatal(err)
	}
	bigPath := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(bigPath, []byte(strings.Repeat("a", maxFileRefBytes+100)), 0o644); err != nil {
		t.Fatal(err)
	}

	// Text file: content verbatim, not a directory.
	if got, isDir, err := readFileRef(textPath); err != nil || isDir || got != "line one\nline two\n" {
		t.Errorf("text file = (%q, %v, %v)", got, isDir, err)
	}

	// Binary file: noted, not dumped.
	if got, _, err := readFileRef(binPath); err != nil || !strings.Contains(got, "binary file") {
		t.Errorf("binary file = (%q, %v), want a binary note", got, err)
	}

	// Large file: truncated with a marker.
	if got, _, err := readFileRef(bigPath); err != nil || !strings.Contains(got, "truncated") {
		t.Errorf("big file should be truncated, got len=%d err=%v", len(got), err)
	}

	// Directory: one-level listing including a trailing slash for subdirs.
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, isDir, err := readFileRef(dir)
	if err != nil || !isDir {
		t.Fatalf("dir = (isDir=%v, err=%v)", isDir, err)
	}
	if !strings.Contains(got, "hello.txt") || !strings.Contains(got, "sub/") {
		t.Errorf("dir listing = %q, want hello.txt and sub/", got)
	}

	// Missing path: error.
	if _, _, err := readFileRef(filepath.Join(dir, "nope")); err == nil {
		t.Error("missing path should error")
	}
}
