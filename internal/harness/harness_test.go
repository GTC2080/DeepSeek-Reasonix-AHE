package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/harnesspolicy"
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
	if err := l.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := os.WriteFile(filepath.Join(l.SourceDir(), "prompts", "system.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

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
	copied, err := os.ReadFile(filepath.Join(l.SnapshotSourceDir(first.SnapshotID), "prompts", "system.md"))
	if err != nil {
		t.Fatalf("snapshot source missing: %v", err)
	}
	if string(copied) != "base\n" {
		t.Fatalf("snapshot source = %q, want base", copied)
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

func TestCreateSnapshotFromSourceDoesNotModifyCurrentSource(t *testing.T) {
	l := NewLayout(filepath.Join(t.TempDir(), RootDir))
	if err := l.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	current := filepath.Join(l.SourceDir(), "prompts", "system.md")
	if err := os.WriteFile(current, []byte("current\n"), 0o644); err != nil {
		t.Fatalf("write current source: %v", err)
	}

	staged := filepath.Join(t.TempDir(), "staged")
	for _, rel := range []string{"prompts", "tool_descriptions", "skills", "middleware", "routing"} {
		if err := os.MkdirAll(filepath.Join(staged, rel), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(staged, "prompts", "system.md"), []byte("staged\n"), 0o644); err != nil {
		t.Fatalf("write staged source: %v", err)
	}

	lock, err := l.CreateSnapshotFromSource(staged, time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatalf("CreateSnapshotFromSource: %v", err)
	}
	got, err := os.ReadFile(current)
	if err != nil {
		t.Fatalf("read current source: %v", err)
	}
	if string(got) != "current\n" {
		t.Fatalf("current source = %q, want unchanged", got)
	}
	copied, err := os.ReadFile(filepath.Join(l.SnapshotSourceDir(lock.SnapshotID), "prompts", "system.md"))
	if err != nil {
		t.Fatalf("read snapshot source: %v", err)
	}
	if string(copied) != "staged\n" {
		t.Fatalf("snapshot source = %q, want staged", copied)
	}
}

func TestLoadActiveSnapshotReadsPromptOverlayAndToolDescriptions(t *testing.T) {
	l := NewLayout(filepath.Join(t.TempDir(), RootDir))
	if err := l.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := os.WriteFile(filepath.Join(l.SourceDir(), "prompts", "z.md"), []byte("second prompt\n"), 0o644); err != nil {
		t.Fatalf("write prompt z: %v", err)
	}
	if err := os.WriteFile(filepath.Join(l.SourceDir(), "prompts", "a.md"), []byte("first prompt\n"), 0o644); err != nil {
		t.Fatalf("write prompt a: %v", err)
	}
	if err := os.WriteFile(filepath.Join(l.SourceDir(), "middleware", "guard.txt"), []byte("middleware guard\n"), 0o644); err != nil {
		t.Fatalf("write middleware: %v", err)
	}
	if err := os.WriteFile(filepath.Join(l.SourceDir(), "routing", "model.toml"), []byte("routing hint\n"), 0o644); err != nil {
		t.Fatalf("write routing: %v", err)
	}
	if err := os.WriteFile(filepath.Join(l.SourceDir(), "tool_descriptions", "bash.md"), []byte("Harness bash description.\n"), 0o644); err != nil {
		t.Fatalf("write tool description: %v", err)
	}
	lock, err := l.CreateSnapshot(time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	if err := l.Activate(lock.SnapshotID); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	active, err := l.LoadActive()
	if err != nil {
		t.Fatalf("LoadActive: %v", err)
	}

	if active.SnapshotID != "h-0001" || active.Lock.StablePrefixHash != lock.StablePrefixHash {
		t.Fatalf("active snapshot = %+v, want h-0001 with lock hash %s", active, lock.StablePrefixHash)
	}
	first := strings.Index(active.PromptOverlay, "first prompt")
	second := strings.Index(active.PromptOverlay, "second prompt")
	middleware := strings.Index(active.PromptOverlay, "middleware guard")
	routing := strings.Index(active.PromptOverlay, "routing hint")
	if first < 0 || second < 0 || middleware < 0 || routing < 0 {
		t.Fatalf("prompt overlay missing expected content:\n%s", active.PromptOverlay)
	}
	if !(first < second && second < middleware && middleware < routing) {
		t.Fatalf("prompt overlay should be component/path sorted:\n%s", active.PromptOverlay)
	}
	if got := active.ToolDescriptions["bash"]; got != "Harness bash description." {
		t.Fatalf("bash tool description = %q, want harness override", got)
	}
}

func TestLoadActiveSnapshotReadsExecutableMiddlewarePolicies(t *testing.T) {
	l := NewLayout(filepath.Join(t.TempDir(), RootDir))
	if err := l.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := os.WriteFile(filepath.Join(l.SourceDir(), "middleware", "final_answer_readiness.toml"), []byte(`
version = "middleware.v0.1"
id = "final_answer_readiness"
enabled = true
stage = "final_answer"
action = "block_and_nudge"
max_final_answer_blocks = 2
`), 0o644); err != nil {
		t.Fatalf("write middleware: %v", err)
	}
	lock, err := l.CreateSnapshot(time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	if err := l.Activate(lock.SnapshotID); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	active, err := l.LoadActive()
	if err != nil {
		t.Fatalf("LoadActive: %v", err)
	}
	if got := active.Policies.FinalAnswerMaxBlocks(3); got != 2 {
		t.Fatalf("FinalAnswerMaxBlocks = %d, want 2", got)
	}
	if policy, ok := active.Policies.Enabled(harnesspolicy.PolicyFinalAnswerReadiness); !ok || policy.Stage != harnesspolicy.StageFinalAnswer {
		t.Fatalf("active policy = %+v, %v", policy, ok)
	}
}

func TestCreateSnapshotRejectsInvalidMiddlewarePolicy(t *testing.T) {
	l := NewLayout(filepath.Join(t.TempDir(), RootDir))
	if err := l.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := os.WriteFile(filepath.Join(l.SourceDir(), "middleware", "bad.toml"), []byte(`
version = "middleware.v0.1"
id = "bad"
enabled = true
stage = "not_a_stage"
action = "warn"
`), 0o644); err != nil {
		t.Fatalf("write middleware: %v", err)
	}

	_, err := l.CreateSnapshot(time.Unix(1, 0).UTC())
	if err == nil || !strings.Contains(err.Error(), "stage") {
		t.Fatalf("CreateSnapshot err = %v, want invalid stage", err)
	}
}

func TestLoadActiveSnapshotRequiresSnapshotSource(t *testing.T) {
	l := NewLayout(filepath.Join(t.TempDir(), RootDir))
	if err := os.MkdirAll(l.SnapshotDir("h-0001"), 0o755); err != nil {
		t.Fatalf("mkdir snapshot: %v", err)
	}
	if err := os.WriteFile(filepath.Join(l.SnapshotDir("h-0001"), LockFile), []byte(`{"snapshot_id":"h-0001"}`), 0o644); err != nil {
		t.Fatalf("write lock: %v", err)
	}
	if err := l.Activate("h-0001"); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	_, err := l.LoadActive()
	if err == nil || !strings.Contains(err.Error(), "has no source copy") {
		t.Fatalf("LoadActive err = %v, want source copy error", err)
	}
}

func TestReplaceSourceWithSnapshotCopiesSnapshotSource(t *testing.T) {
	l := NewLayout(filepath.Join(t.TempDir(), RootDir))
	if err := l.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := os.WriteFile(filepath.Join(l.SourceDir(), "prompts", "system.md"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write base: %v", err)
	}
	first, err := l.CreateSnapshot(time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatalf("CreateSnapshot first: %v", err)
	}
	if err := os.WriteFile(filepath.Join(l.SourceDir(), "prompts", "system.md"), []byte("current\n"), 0o644); err != nil {
		t.Fatalf("write current: %v", err)
	}

	if err := l.ReplaceSourceWithSnapshot(first.SnapshotID); err != nil {
		t.Fatalf("ReplaceSourceWithSnapshot: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(l.SourceDir(), "prompts", "system.md"))
	if err != nil {
		t.Fatalf("read replaced source: %v", err)
	}
	if string(got) != "base\n" {
		t.Fatalf("source content = %q, want snapshot content", got)
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
