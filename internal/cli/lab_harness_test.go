package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunDispatchesLabHarnessLifecycle(t *testing.T) {
	dir := tempChdir(t)

	out := captureStdout(t, func() {
		if rc := Run([]string{"lab", "harness", "init"}, "test-version"); rc != 0 {
			t.Fatalf("lab harness init rc = %d, want 0", rc)
		}
	})
	if !strings.Contains(out, ".reasonix-harness") {
		t.Fatalf("init output should mention .reasonix-harness, got:\n%s", out)
	}

	out = captureStdout(t, func() {
		if rc := Run([]string{"lab", "harness", "snapshot", "create"}, "test-version"); rc != 0 {
			t.Fatalf("snapshot create rc = %d, want 0", rc)
		}
	})
	if !strings.Contains(out, "h-0001") {
		t.Fatalf("snapshot create output = %q, want h-0001", out)
	}

	out = captureStdout(t, func() {
		if rc := Run([]string{"lab", "harness", "snapshot", "list"}, "test-version"); rc != 0 {
			t.Fatalf("snapshot list rc = %d, want 0", rc)
		}
	})
	if !strings.Contains(out, "h-0001") {
		t.Fatalf("snapshot list output = %q, want h-0001", out)
	}

	out = captureStdout(t, func() {
		if rc := Run([]string{"lab", "harness", "snapshot", "activate", "h-0001"}, "test-version"); rc != 0 {
			t.Fatalf("snapshot activate rc = %d, want 0", rc)
		}
	})
	if !strings.Contains(out, "h-0001") {
		t.Fatalf("snapshot activate output = %q, want h-0001", out)
	}
	active, err := os.ReadFile(filepath.Join(dir, ".reasonix-harness", "active"))
	if err != nil {
		t.Fatalf("read active: %v", err)
	}
	if strings.TrimSpace(string(active)) != "h-0001" {
		t.Fatalf("active = %q, want h-0001", active)
	}

	out = captureStdout(t, func() {
		if rc := Run([]string{"lab", "harness", "inspect", "h-0001"}, "test-version"); rc != 0 {
			t.Fatalf("inspect rc = %d, want 0", rc)
		}
	})
	var decoded map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("inspect output is not JSON: %v\n%s", err, out)
	}
	if decoded["snapshot_id"] != "h-0001" {
		t.Fatalf("inspect snapshot_id = %v, want h-0001", decoded["snapshot_id"])
	}

	out = captureStdout(t, func() {
		if rc := Run([]string{"lab", "harness", "snapshot", "pin", "h-0001"}, "test-version"); rc != 0 {
			t.Fatalf("snapshot pin rc = %d, want 0", rc)
		}
	})
	if !strings.Contains(out, "pinned h-0001") {
		t.Fatalf("snapshot pin output = %q, want h-0001", out)
	}
	out = captureStdout(t, func() {
		if rc := Run([]string{"lab", "harness", "snapshot", "list"}, "test-version"); rc != 0 {
			t.Fatalf("snapshot list after pin rc = %d, want 0", rc)
		}
	})
	if !strings.Contains(out, "h-0001") || !strings.Contains(out, "pinned") {
		t.Fatalf("snapshot list after pin output = %q, want pinned marker", out)
	}
	out = captureStdout(t, func() {
		if rc := Run([]string{"lab", "harness", "snapshot", "unpin", "h-0001"}, "test-version"); rc != 0 {
			t.Fatalf("snapshot unpin rc = %d, want 0", rc)
		}
	})
	if !strings.Contains(out, "unpinned h-0001") {
		t.Fatalf("snapshot unpin output = %q, want h-0001", out)
	}
}

func TestLabHarnessCommandRejectsBadArguments(t *testing.T) {
	tempChdir(t)

	for _, args := range [][]string{
		{"lab"},
		{"lab", "unknown"},
		{"lab", "harness", "snapshot"},
		{"lab", "harness", "snapshot", "activate"},
		{"lab", "harness", "snapshot", "pin"},
		{"lab", "harness", "snapshot", "unpin"},
		{"lab", "harness", "inspect"},
	} {
		if rc := Run(args, "test-version"); rc != 2 {
			t.Fatalf("Run(%v) rc = %d, want 2", args, rc)
		}
	}
}

func tempChdir(t *testing.T) string {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	return dir
}
