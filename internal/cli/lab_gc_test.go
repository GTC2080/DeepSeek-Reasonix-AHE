package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunDispatchesLabGCDryRun(t *testing.T) {
	dir := tempChdir(t)
	tracePath := filepath.Join(dir, ".reasonix-ahe", "traces", "old.trace.jsonl")
	if err := os.MkdirAll(filepath.Dir(tracePath), 0o755); err != nil {
		t.Fatal(err)
	}
	trace := `{"version":"trace.v0.1","run_id":"r","session_id":"s","seq":1,"type":"session_start","time":"2026-05-01T00:00:00Z","turn":0,"data":{}}
`
	if err := os.WriteFile(tracePath, []byte(trace), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().AddDate(0, 0, -20)
	if err := os.Chtimes(tracePath, old, old); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if rc := Run([]string{"lab", "gc", "--dry-run"}, "test-version"); rc != 0 {
			t.Fatalf("lab gc rc = %d, want 0", rc)
		}
	})
	if !strings.Contains(out, "Reasonix-AHE GC Dry Run") || !strings.Contains(out, "Would delete:") || !strings.Contains(out, "Would keep:") {
		t.Fatalf("gc output missing expected sections:\n%s", out)
	}
	if !strings.Contains(out, "old.trace.jsonl") || !strings.Contains(out, "raw trace older than 14 days") {
		t.Fatalf("gc output missing old trace decision:\n%s", out)
	}
	if _, err := os.Stat(tracePath); err != nil {
		t.Fatalf("dry-run should not delete trace: %v", err)
	}
}

func TestLabGCRejectsBadArguments(t *testing.T) {
	tempChdir(t)
	for _, args := range [][]string{
		{"lab", "gc"},
		{"lab", "gc", "--delete"},
		{"lab", "gc", "--dry-run", "extra"},
	} {
		if rc := Run(args, "test-version"); rc != 2 {
			t.Fatalf("Run(%v) rc = %d, want 2", args, rc)
		}
	}
}

func TestHelpMentionsLabGC(t *testing.T) {
	out := captureStdout(t, func() {
		if rc := Run([]string{"help"}, "test-version"); rc != 0 {
			t.Fatalf("help rc = %d, want 0", rc)
		}
	})
	if !strings.Contains(out, "gc") {
		t.Fatalf("help output should mention gc:\n%s", out)
	}
}
