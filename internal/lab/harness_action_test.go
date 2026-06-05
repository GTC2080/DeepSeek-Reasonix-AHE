package lab

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/harness"
)

func TestPromoteHarnessSnapshotWritesSafetySnapshotAndArtifact(t *testing.T) {
	root := t.TempDir()
	layout := harness.NewLayout(filepath.Join(root, harness.RootDir))
	if err := layout.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	sourceFile := filepath.Join(layout.SourceDir(), "prompts", "system.md")
	if err := os.WriteFile(sourceFile, []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write base: %v", err)
	}
	first, err := layout.CreateSnapshot(time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatalf("CreateSnapshot first: %v", err)
	}
	if err := os.WriteFile(sourceFile, []byte("current\n"), 0o644); err != nil {
		t.Fatalf("write current: %v", err)
	}

	result, err := PromoteHarnessSnapshot(HarnessSnapshotActionOptions{
		SnapshotID:  first.SnapshotID,
		HarnessRoot: layout.Root,
		OutputRoot:  filepath.Join(root, DefaultAHERoot, "harness-actions"),
		Activate:    true,
		Pin:         true,
		Now:         func() time.Time { return time.Unix(2, 0).UTC() },
		AttemptID:   "promote-test",
	})
	if err != nil {
		t.Fatalf("PromoteHarnessSnapshot: %v", err)
	}

	if result.SafetySnapshot != "h-0002" || result.ResultPath == "" || !result.Activated || !result.Pinned {
		t.Fatalf("result = %+v, want safety h-0002, result path, activated, pinned", result)
	}
	got, err := os.ReadFile(sourceFile)
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	if string(got) != "base\n" {
		t.Fatalf("source = %q, want base snapshot", got)
	}
	active, err := layout.Active()
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	if active != "h-0001" {
		t.Fatalf("active = %q, want h-0001", active)
	}
	pinned, err := layout.Pinned()
	if err != nil {
		t.Fatalf("Pinned: %v", err)
	}
	if strings.Join(pinned, ",") != "h-0001" {
		t.Fatalf("pinned = %v, want h-0001", pinned)
	}
	if _, err := os.Stat(result.ResultPath); err != nil {
		t.Fatalf("result artifact missing: %v", err)
	}
}
