package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/trace"
)

func TestResolveTraceConfigHonorsFlagOverEnv(t *testing.T) {
	dir := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Setenv(tracePathEnv, filepath.Join(t.TempDir(), "env.trace.jsonl"))
	t.Setenv(traceModeEnv, "metadata")

	cfg, err := resolveTraceConfig("flag.trace.jsonl", "full")
	if err != nil {
		t.Fatalf("resolveTraceConfig: %v", err)
	}
	if !cfg.Enabled {
		t.Fatal("trace should be enabled")
	}
	if cfg.Mode != trace.ModeFull {
		t.Fatalf("mode = %q, want full", cfg.Mode)
	}
	want := filepath.Join(dir, "flag.trace.jsonl")
	if cfg.Path != want {
		t.Fatalf("path = %q, want %q", cfg.Path, want)
	}
}

func TestResolveTraceConfigUsesEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "env.trace.jsonl")
	t.Setenv(tracePathEnv, path)
	t.Setenv(traceModeEnv, "metadata")

	cfg, err := resolveTraceConfig("", "")
	if err != nil {
		t.Fatalf("resolveTraceConfig: %v", err)
	}
	if !cfg.Enabled || cfg.Path != path || cfg.Mode != trace.ModeMetadata {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestResolveTraceConfigInvalidModeErrors(t *testing.T) {
	t.Setenv(tracePathEnv, "")
	t.Setenv(traceModeEnv, "")

	if _, err := resolveTraceConfig("", "loud"); err == nil {
		t.Fatal("expected invalid flag trace mode to error")
	}

	t.Setenv(traceModeEnv, "loud")
	if _, err := resolveTraceConfig("", ""); err == nil {
		t.Fatal("expected invalid env trace mode to error")
	}
}

func TestResolveTraceConfigDisabledDefaultsToPreview(t *testing.T) {
	t.Setenv(tracePathEnv, "")
	t.Setenv(traceModeEnv, "")

	cfg, err := resolveTraceConfig("", "")
	if err != nil {
		t.Fatalf("resolveTraceConfig: %v", err)
	}
	if cfg.Enabled {
		t.Fatal("trace should be disabled without path")
	}
	if cfg.Mode != trace.DefaultMode {
		t.Fatalf("mode = %q, want default %q", cfg.Mode, trace.DefaultMode)
	}
}

func TestWrapTraceSinkIncludesActiveHarnessSnapshot(t *testing.T) {
	dir := tempChdir(t)
	root := filepath.Join(dir, ".reasonix-harness")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "active"), []byte("h-0001\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "trace.jsonl")

	_, sink, err := wrapTraceSink(event.Discard, traceCLIConfig{
		Enabled: true,
		Path:    path,
		Mode:    trace.ModeMetadata,
	})
	if err != nil {
		t.Fatalf("wrapTraceSink: %v", err)
	}
	if err := sink.Close(nil); err != nil {
		t.Fatalf("Close: %v", err)
	}

	lines, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var first struct {
		Type string         `json:"type"`
		Data map[string]any `json:"data"`
	}
	end := bytes.IndexByte(lines, '\n')
	if end < 0 {
		end = len(lines)
	}
	if err := json.Unmarshal(lines[:end], &first); err != nil {
		t.Fatalf("first trace line is not JSON: %v\n%s", err, lines)
	}
	if first.Type != "session_start" {
		t.Fatalf("first type = %q, want session_start", first.Type)
	}
	if got := first.Data["harness_snapshot"]; got != "h-0001" {
		t.Fatalf("harness_snapshot = %#v, want h-0001", got)
	}
}
