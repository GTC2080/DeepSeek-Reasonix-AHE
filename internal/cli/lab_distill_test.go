package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/lab"
)

func TestRunDispatchesLabDistill(t *testing.T) {
	dir := tempChdir(t)
	runDir := writeCLIDistillRun(t, filepath.Join(dir, ".reasonix-ahe", "evals", "run-distill"))

	out := captureStdout(t, func() {
		if rc := Run([]string{"lab", "distill", runDir}, "test-version"); rc != 0 {
			t.Fatalf("lab distill rc = %d, want 0", rc)
		}
	})
	if !strings.Contains(out, "evidence\t") || !strings.Contains(out, "cli-task") {
		t.Fatalf("lab distill output = %q, want evidence path and task", out)
	}
	if _, err := os.Stat(filepath.Join(runDir, "evidence", "task-cli-task.md")); err != nil {
		t.Fatalf("task evidence missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(runDir, "evidence", "clusters.md")); err != nil {
		t.Fatalf("clusters evidence missing: %v", err)
	}
}

func TestLabDistillRejectsBadArguments(t *testing.T) {
	tempChdir(t)
	for _, args := range [][]string{
		{"lab", "distill"},
		{"lab", "distill", "run", "--json"},
		{"lab", "distill", "one", "two"},
	} {
		if rc := Run(args, "test-version"); rc != 2 {
			t.Fatalf("Run(%v) rc = %d, want 2", args, rc)
		}
	}
}

func writeCLIDistillRun(t *testing.T, runDir string) string {
	t.Helper()
	taskDir := filepath.Join(runDir, "tasks", "cli-task")
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatal(err)
	}
	task := lab.TaskResult{
		RunID: "run-distill", TaskID: "cli-task", Model: "fake-model",
		Passed: false, AgentExitCode: 0, VerifyExitCode: 1,
		ArtifactDir: taskDir,
		TracePath:   filepath.Join(taskDir, "trace.jsonl"), DiffPath: filepath.Join(taskDir, "diff.patch"),
		VerifyLogPath: filepath.Join(taskDir, "verify.log"), CacheReportPath: filepath.Join(taskDir, "cache_report.json"),
	}
	writeCLIJSON(t, filepath.Join(taskDir, "result.json"), task)
	writeCLIJSON(t, task.CacheReportPath, lab.CacheReport{CacheHitRatio: 0.5})
	if err := os.WriteFile(task.VerifyLogPath, []byte("[verify]\nfailed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(task.DiffPath, []byte("diff --git a/file b/file\n+change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(task.TracePath, []byte(`{"version":"trace.v0.1","run_id":"r","session_id":"s","seq":1,"type":"tool_call","time":"2026-06-05T00:00:00Z","turn":1,"data":{"tool_name":"bash","partial":false}}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeCLIJSON(t, filepath.Join(runDir, "result.json"), lab.Result{
		RunID: "run-distill", ArtifactDir: runDir, Passed: false, Tasks: []lab.TaskResult{task},
	})
	return runDir
}

func writeCLIJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}
