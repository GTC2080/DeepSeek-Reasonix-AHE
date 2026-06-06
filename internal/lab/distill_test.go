package lab

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDistillWritesTaskReportsClustersAndRedacts(t *testing.T) {
	runDir := t.TempDir()
	writeDistillRun(t, runDir, []distillFixtureTask{{
		Result: TaskResult{
			RunID: "run-distill", TaskID: "python-bugfix-001", Model: "deepseek-v4-flash",
			HarnessSnapshot: "h-0001", Passed: false, AgentExitCode: 0, VerifyExitCode: 1,
			CacheHitRatio: 0.941, ContractViolations: 1,
			Warnings: []string{"contract violations 1 exceed 0"},
		},
		CacheReport: CacheReport{CacheHitRatio: 0.941, ContractViolations: 1, HarnessSnapshot: "h-0001"},
		VerifyLog:   "[verify]\nexpected 4 got 3\nAuthorization: Bearer abcdef123\nsk-abcdefghijkl\n",
		Diff:        "diff --git a/calc.py b/calc.py\n+return a + b\n",
		Trace: `{"version":"trace.v0.1","run_id":"r","session_id":"s","seq":1,"type":"tool_call","time":"2026-06-05T00:00:00Z","turn":1,"data":{"tool_name":"read_file","partial":false}}
{"version":"trace.v0.1","run_id":"r","session_id":"s","seq":2,"type":"tool_call","time":"2026-06-05T00:00:00Z","turn":1,"data":{"tool_name":"bash","partial":false}}
{"version":"trace.v0.1","run_id":"r","session_id":"s","seq":3,"type":"tool_result","time":"2026-06-05T00:00:00Z","turn":1,"data":{"tool_name":"bash","error":"test failed"}}
{"version":"trace.v0.1","run_id":"r","session_id":"s","seq":4,"type":"middleware_policy_decision","time":"2026-06-05T00:00:00Z","turn":1,"data":{"policy_id":"final_answer_readiness","stage":"final_answer","action":"block_and_nudge","reason_preview":"todo completion missing"}}
`,
	}})

	result, err := Distill(runDir)
	if err != nil {
		t.Fatalf("Distill: %v", err)
	}
	if result.EvidenceDir != filepath.Join(runDir, "evidence") || len(result.Tasks) != 1 {
		t.Fatalf("distill result = %+v, want one task under evidence dir", result)
	}
	taskReport := readText(t, filepath.Join(runDir, "evidence", "task-python-bugfix-001.md"))
	for _, want := range []string{
		"# Task Report: python-bugfix-001",
		"Result: FAILED",
		"Harness snapshot: h-0001",
		"Model: deepseek-v4-flash",
		"Cache hit ratio: 94.10%",
		"Contract violations: 1",
		"- read_file: 1",
		"- bash: 1",
		"middleware policy final_answer_readiness: block_and_nudge at final_answer: todo completion missing",
		"- verifier_failed",
		"- premature_success",
		"- cache_contract_broken",
		"- middleware/post_success_guard.toml",
		"- tool_descriptions/bash.md",
		"Bearer [REDACTED]",
		"sk-[REDACTED]",
	} {
		if !strings.Contains(taskReport, want) {
			t.Fatalf("task report missing %q:\n%s", want, taskReport)
		}
	}
	for _, forbidden := range []string{"abcdef123", "sk-abcdefghijkl"} {
		if strings.Contains(taskReport, forbidden) {
			t.Fatalf("task report leaked %q:\n%s", forbidden, taskReport)
		}
	}
	clusters := readText(t, filepath.Join(runDir, "evidence", "clusters.md"))
	for _, want := range []string{"# Failure Clusters", "cache_contract_broken", "python-bugfix-001", "middleware/cache_contract_guard.toml"} {
		if !strings.Contains(clusters, want) {
			t.Fatalf("clusters missing %q:\n%s", want, clusters)
		}
	}
}

func TestDistillDetectsNoPatchAndToolErrorLoop(t *testing.T) {
	runDir := t.TempDir()
	writeDistillRun(t, runDir, []distillFixtureTask{{
		Result: TaskResult{
			RunID: "run-distill", TaskID: "tool-loop", Passed: false,
			AgentExitCode: 1, VerifyExitCode: 1,
			Warnings: []string{"agent timeout while waiting for command"},
		},
		VerifyLog: "[verify]\npermission denied while applying patch\n",
		Trace: `{"version":"trace.v0.1","run_id":"r","session_id":"s","seq":1,"type":"tool_result","time":"2026-06-05T00:00:00Z","turn":1,"data":{"tool_name":"bash","error":"permission denied"}}
{"version":"trace.v0.1","run_id":"r","session_id":"s","seq":2,"type":"tool_result","time":"2026-06-05T00:00:00Z","turn":1,"data":{"tool_name":"bash","error":"permission denied"}}
{"version":"trace.v0.1","run_id":"r","session_id":"s","seq":3,"type":"tool_result","time":"2026-06-05T00:00:00Z","turn":1,"data":{"tool_name":"bash","error":"permission denied"}}
`,
	}})

	if _, err := Distill(runDir); err != nil {
		t.Fatalf("Distill: %v", err)
	}
	taskReport := readText(t, filepath.Join(runDir, "evidence", "task-tool-loop.md"))
	for _, want := range []string{
		"- timeout",
		"- tool_error_loop",
		"- permission_denied",
		"- verifier_failed",
		"- no_patch",
	} {
		if !strings.Contains(taskReport, want) {
			t.Fatalf("task report missing %q:\n%s", want, taskReport)
		}
	}
}

func TestDistillOutputIsStableAndOverwrites(t *testing.T) {
	runDir := t.TempDir()
	writeDistillRun(t, runDir, []distillFixtureTask{{
		Result:    TaskResult{RunID: "run-distill", TaskID: "stable", Passed: true, AgentExitCode: 0, VerifyExitCode: 0},
		VerifyLog: "[verify]\nok\n",
		Diff:      "diff --git a/file b/file\n+ok\n",
	}})

	first, err := Distill(runDir)
	if err != nil {
		t.Fatalf("Distill first: %v", err)
	}
	reportPath := first.Tasks[0].ReportPath
	firstBody := readText(t, reportPath)
	if err := os.WriteFile(reportPath, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Distill(runDir); err != nil {
		t.Fatalf("Distill second: %v", err)
	}
	if got := readText(t, reportPath); got != firstBody || strings.Contains(got, "stale") {
		t.Fatalf("distill output not stable overwrite:\nfirst=%s\nsecond=%s", firstBody, got)
	}
}

func TestDistillMissingRunResultErrors(t *testing.T) {
	if _, err := Distill(t.TempDir()); err == nil || !strings.Contains(err.Error(), "result.json") {
		t.Fatalf("Distill err = %v, want result.json error", err)
	}
}

type distillFixtureTask struct {
	Result      TaskResult
	CacheReport CacheReport
	VerifyLog   string
	Diff        string
	Trace       string
}

func writeDistillRun(t *testing.T, runDir string, tasks []distillFixtureTask) {
	t.Helper()
	var run Result
	run.RunID = "run-distill"
	run.ArtifactDir = runDir
	run.Passed = true
	for _, fixture := range tasks {
		task := fixture.Result
		if !task.Passed {
			run.Passed = false
		}
		taskDir := filepath.Join(runDir, "tasks", safeName(task.TaskID))
		task.ArtifactDir = taskDir
		task.TracePath = filepath.Join(taskDir, "trace.jsonl")
		task.DiffPath = filepath.Join(taskDir, "diff.patch")
		task.VerifyLogPath = filepath.Join(taskDir, "verify.log")
		task.CacheReportPath = filepath.Join(taskDir, "cache_report.json")
		run.Tasks = append(run.Tasks, task)
		if err := os.MkdirAll(taskDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := writeJSON(filepath.Join(taskDir, "result.json"), task); err != nil {
			t.Fatal(err)
		}
		if err := writeJSON(task.CacheReportPath, fixture.CacheReport); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(task.VerifyLogPath, []byte(fixture.VerifyLog), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(task.DiffPath, []byte(fixture.Diff), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(task.TracePath, []byte(fixture.Trace), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeJSON(filepath.Join(runDir, "result.json"), run); err != nil {
		t.Fatal(err)
	}
}

func readText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
