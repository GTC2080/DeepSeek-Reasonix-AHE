package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInitCreatesExpectedSourceLayoutAndIsIdempotent(t *testing.T) {
	l := NewLayout(filepath.Join(t.TempDir(), RootDir))

	for i := 0; i < 2; i++ {
		if err := l.Init(); err != nil {
			t.Fatalf("Init %d: %v", i+1, err)
		}
	}

	for _, rel := range []string{
		"source",
		"source/prompts",
		"source/tool_descriptions",
		"source/skills",
		"source/middleware",
		"source/routing",
		"snapshots",
		"manifests",
	} {
		if info, err := os.Stat(filepath.Join(l.Root, filepath.FromSlash(rel))); err != nil || !info.IsDir() {
			t.Fatalf("%s should be a directory: info=%v err=%v", rel, info, err)
		}
	}
}

func TestCreateSnapshotWritesLockAndIncrementsID(t *testing.T) {
	l := NewLayout(filepath.Join(t.TempDir(), RootDir))
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)

	first, err := l.CreateSnapshot(now)
	if err != nil {
		t.Fatalf("CreateSnapshot first: %v", err)
	}
	second, err := l.CreateSnapshot(now.Add(time.Minute))
	if err != nil {
		t.Fatalf("CreateSnapshot second: %v", err)
	}

	if first.SnapshotID != "h-0001" || second.SnapshotID != "h-0002" {
		t.Fatalf("snapshot ids = %q, %q; want h-0001, h-0002", first.SnapshotID, second.SnapshotID)
	}
	if !strings.HasPrefix(first.StablePrefixHash, "sha256:") {
		t.Fatalf("stable prefix hash = %q, want sha256:<hex>", first.StablePrefixHash)
	}
	if _, err := os.Stat(filepath.Join(l.SnapshotDir(first.SnapshotID), LockFile)); err != nil {
		t.Fatalf("lock file missing: %v", err)
	}
}

func TestCreateSnapshotHashesSourceChanges(t *testing.T) {
	l := NewLayout(filepath.Join(t.TempDir(), RootDir))
	if err := l.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := os.WriteFile(filepath.Join(l.SourceDir(), "prompts", "system.md"), []byte("one\n"), 0o644); err != nil {
		t.Fatalf("write system.md: %v", err)
	}
	first, err := l.CreateSnapshot(time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatalf("CreateSnapshot first: %v", err)
	}
	if err := os.WriteFile(filepath.Join(l.SourceDir(), "prompts", "system.md"), []byte("two\n"), 0o644); err != nil {
		t.Fatalf("rewrite system.md: %v", err)
	}
	second, err := l.CreateSnapshot(time.Unix(2, 0).UTC())
	if err != nil {
		t.Fatalf("CreateSnapshot second: %v", err)
	}

	if first.SystemPromptHash == second.SystemPromptHash {
		t.Fatalf("system prompt hash should change after source edit: %q", first.SystemPromptHash)
	}
	if first.StablePrefixHash == second.StablePrefixHash {
		t.Fatalf("stable prefix hash should change after source edit: %q", first.StablePrefixHash)
	}
}

func TestActivateInspectAndListSnapshots(t *testing.T) {
	l := NewLayout(filepath.Join(t.TempDir(), RootDir))
	lock, err := l.CreateSnapshot(time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	if err := l.Activate(lock.SnapshotID); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	active, err := l.Active()
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	if active != lock.SnapshotID {
		t.Fatalf("active = %q, want %q", active, lock.SnapshotID)
	}
	inspected, err := l.Inspect(lock.SnapshotID)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if inspected.SnapshotID != lock.SnapshotID {
		t.Fatalf("inspected id = %q, want %q", inspected.SnapshotID, lock.SnapshotID)
	}
	listed, err := l.ListSnapshots()
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(listed) != 1 || listed[0].SnapshotID != lock.SnapshotID {
		t.Fatalf("listed = %+v, want one %q", listed, lock.SnapshotID)
	}
}

func TestPinUnpinAndPinnedSnapshots(t *testing.T) {
	l := NewLayout(filepath.Join(t.TempDir(), RootDir))
	if _, err := l.CreateSnapshot(time.Unix(1, 0).UTC()); err != nil {
		t.Fatalf("CreateSnapshot h-0001: %v", err)
	}
	if _, err := l.CreateSnapshot(time.Unix(2, 0).UTC()); err != nil {
		t.Fatalf("CreateSnapshot h-0002: %v", err)
	}
	if err := os.WriteFile(l.PinnedPath(), []byte("# comment\nh-0002\n\nh-0001\nh-0002\n"), 0o644); err != nil {
		t.Fatalf("seed pinned: %v", err)
	}
	pinned, err := l.Pinned()
	if err != nil {
		t.Fatalf("Pinned: %v", err)
	}
	if strings.Join(pinned, ",") != "h-0001,h-0002" {
		t.Fatalf("pinned = %v, want sorted unique h-0001,h-0002", pinned)
	}

	if err := l.Unpin("h-0001"); err != nil {
		t.Fatalf("Unpin: %v", err)
	}
	if err := l.Unpin("h-9999"); err != nil {
		t.Fatalf("Unpin missing should be idempotent: %v", err)
	}
	pinned, err = l.Pinned()
	if err != nil {
		t.Fatalf("Pinned after unpin: %v", err)
	}
	if len(pinned) != 1 || pinned[0] != "h-0002" {
		t.Fatalf("pinned after unpin = %v, want h-0002", pinned)
	}
	if err := l.Pin("h-0001"); err != nil {
		t.Fatalf("Pin h-0001: %v", err)
	}
	pinned, err = l.Pinned()
	if err != nil {
		t.Fatalf("Pinned after pin: %v", err)
	}
	if strings.Join(pinned, ",") != "h-0001,h-0002" {
		t.Fatalf("pinned after pin = %v, want sorted h-0001,h-0002", pinned)
	}
	if err := l.Pin("h-9999"); err == nil || !strings.Contains(err.Error(), "snapshot h-9999 not found") {
		t.Fatalf("Pin missing err = %v, want snapshot not found", err)
	}
}

func TestMissingSnapshotErrorsAreClear(t *testing.T) {
	l := NewLayout(filepath.Join(t.TempDir(), RootDir))
	if err := l.Activate("h-9999"); err == nil || !strings.Contains(err.Error(), "snapshot h-9999 not found") {
		t.Fatalf("Activate missing err = %v, want not found", err)
	}
	if _, err := l.Inspect("h-9999"); err == nil || !strings.Contains(err.Error(), "snapshot h-9999 not found") {
		t.Fatalf("Inspect missing err = %v, want not found", err)
	}
}
