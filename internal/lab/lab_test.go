package lab

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLoadTasksSupportsTaskAndSuiteDirectories(t *testing.T) {
	root := t.TempDir()
	first := writeLabTask(t, filepath.Join(root, "suite", "first"), labTaskSpec{ID: "first", Prompt: "fix it"})
	second := writeLabTask(t, filepath.Join(root, "suite", "second"), labTaskSpec{ID: "second", Prompt: "ship it"})

	single, err := LoadTasks(first)
	if err != nil {
		t.Fatalf("LoadTasks single: %v", err)
	}
	if len(single) != 1 || single[0].ID != "first" || single[0].Prompt != "fix it" {
		t.Fatalf("single = %+v, want first task", single)
	}

	suite, err := LoadTasks(filepath.Join(root, "suite"))
	if err != nil {
		t.Fatalf("LoadTasks suite: %v", err)
	}
	if len(suite) != 2 || suite[0].ID != "first" || suite[1].ID != "second" {
		t.Fatalf("suite = %+v, want first, second", suite)
	}
	if suite[0].Dir != first || suite[1].Dir != second {
		t.Fatalf("task dirs = %q, %q", suite[0].Dir, suite[1].Dir)
	}
}

func TestLoadTasksRequiresPrompt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "task.toml"), []byte(`id = "missing-prompt"`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTasks(dir); err == nil || !strings.Contains(err.Error(), "prompt.md") {
		t.Fatalf("LoadTasks err = %v, want prompt.md error", err)
	}
}

func TestRunnerWritesArtifactsAndWarnsOnLowCacheRatio(t *testing.T) {
	taskDir := writeLabTask(t, filepath.Join(t.TempDir(), "python-bugfix-001"), labTaskSpec{
		ID:          "python-bugfix-001",
		Prompt:      "fix calc",
		MinHitRatio: 0.90,
		Files: map[string]string{
			"calc.py": "def add(a, b):\n    return a - b\n",
		},
		Setup:  "echo setup-ran > setup.txt\n",
		Verify: "grep -q \"return a + b\" calc.py\n",
	})
	outRoot := t.TempDir()
	result, err := Runner{
		Options: Options{
			Bin:       fakeReasonixBin(t),
			OutputDir: outRoot,
			RunID:     "run-test",
			Now:       fixedLabNow(),
		},
	}.Run(context.Background(), taskDir)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(result.Tasks) != 1 {
		t.Fatalf("tasks = %d, want 1", len(result.Tasks))
	}
	task := result.Tasks[0]
	if !task.Passed {
		t.Fatalf("task should pass with cache warning only: %+v", task)
	}
	if !task.CacheWarning {
		t.Fatalf("expected low-cache warning: %+v", task.CacheReport)
	}
	if task.CacheReport.CacheHitRatio != 0.5 {
		t.Fatalf("cache hit ratio = %v, want 0.5", task.CacheReport.CacheHitRatio)
	}
	if task.HarnessSnapshot != "h-0001" {
		t.Fatalf("harness snapshot = %q, want h-0001", task.HarnessSnapshot)
	}
	for _, rel := range []string{"trace.jsonl", "diff.patch", "verify.log", "cache_report.json", "result.json", filepath.Join("workdir", "calc.py")} {
		if _, err := os.Stat(filepath.Join(task.ArtifactDir, rel)); err != nil {
			t.Fatalf("artifact %s missing: %v", rel, err)
		}
	}
	if b, err := os.ReadFile(filepath.Join(task.ArtifactDir, "workdir", "setup.txt")); err != nil || !strings.Contains(string(b), "setup-ran") {
		t.Fatalf("setup artifact missing or wrong: %q err=%v", b, err)
	}
	var decoded TaskResult
	if err := readJSON(filepath.Join(task.ArtifactDir, "result.json"), &decoded); err != nil {
		t.Fatalf("read task result: %v", err)
	}
	if decoded.TaskID != task.TaskID || !decoded.Passed {
		t.Fatalf("decoded result = %+v, want passing %s", decoded, task.TaskID)
	}
	var summary Result
	if err := readJSON(filepath.Join(outRoot, "run-test", "result.json"), &summary); err != nil {
		t.Fatalf("read suite result: %v", err)
	}
	if !summary.Passed || len(summary.Tasks) != 1 {
		t.Fatalf("summary = %+v, want suite pass with one task", summary)
	}
}

func TestRunnerFailsWhenContractViolationsExceedLimit(t *testing.T) {
	taskDir := writeLabTask(t, filepath.Join(t.TempDir(), "contract-drift"), labTaskSpec{
		ID:                    "contract-drift",
		Prompt:                "fix calc",
		MaxContractViolations: 0,
		Files: map[string]string{
			"calc.py": "def add(a, b):\n    return a - b\n",
		},
		Verify: "grep -q \"return a + b\" calc.py\n",
	})
	result, err := Runner{
		Options: Options{
			Bin:       fakeReasonixBin(t),
			OutputDir: t.TempDir(),
			RunID:     "run-contract",
			Now:       fixedLabNow(),
			Env:       []string{"FAKE_CONTRACT_VIOLATION=1"},
		},
	}.Run(context.Background(), taskDir)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	task := result.Tasks[0]
	if task.Passed {
		t.Fatalf("task should fail on contract violation: %+v", task)
	}
	if task.ContractViolations != 1 {
		t.Fatalf("contract violations = %d, want 1", task.ContractViolations)
	}
}

func TestReportTraceAggregatesCacheStatsAndDrift(t *testing.T) {
	tracePath := filepath.Join(t.TempDir(), "trace.jsonl")
	trace := `{"version":"trace.v0.1","run_id":"r","session_id":"s","seq":1,"type":"session_start","time":"2026-06-05T00:00:00Z","turn":0,"data":{"harness_snapshot":"h-0007"}}
{"version":"trace.v0.1","run_id":"r","session_id":"s","seq":2,"type":"model_response","time":"2026-06-05T00:00:00Z","turn":1,"data":{"prompt_cache_hit_tokens":999,"prompt_cache_miss_tokens":999}}
{"version":"trace.v0.1","run_id":"r","session_id":"s","seq":3,"type":"cache_stats","time":"2026-06-05T00:00:00Z","turn":1,"data":{"prompt_cache_hit_tokens":80,"prompt_cache_miss_tokens":20,"prefix_changed":true,"prefix_change_reasons":["tools"]}}
{"version":"trace.v0.1","run_id":"r","session_id":"s","seq":4,"type":"model_response","time":"2026-06-05T00:00:00Z","turn":1,"data":{}}
{"version":"trace.v0.1","run_id":"r","session_id":"s","seq":5,"type":"cache_contract_violation","time":"2026-06-05T00:00:00Z","turn":1,"data":{"reasons":["tool_schema_hash"]}}
`
	if err := os.WriteFile(tracePath, []byte(trace), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := ReportTrace(tracePath)
	if err != nil {
		t.Fatalf("ReportTrace: %v", err)
	}
	if report.ModelCalls != 2 {
		t.Fatalf("model calls = %d, want 2", report.ModelCalls)
	}
	if report.PromptCacheHitTokens != 80 || report.PromptCacheMissTokens != 20 {
		t.Fatalf("cache tokens = %d/%d, want 80/20", report.PromptCacheHitTokens, report.PromptCacheMissTokens)
	}
	if report.CacheHitRatio != 0.8 {
		t.Fatalf("cache hit ratio = %v, want 0.8", report.CacheHitRatio)
	}
	if report.HarnessSnapshot != "h-0007" {
		t.Fatalf("harness snapshot = %q, want h-0007", report.HarnessSnapshot)
	}
	if !report.StablePrefixHashDrift || !contains(report.StablePrefixHashDriftReasons, "tools") {
		t.Fatalf("prefix drift = %v reasons=%v, want tools", report.StablePrefixHashDrift, report.StablePrefixHashDriftReasons)
	}
	if report.ContractViolations != 1 || !contains(report.ContractViolationReasons, "tool_schema_hash") {
		t.Fatalf("contract violations = %d reasons=%v, want tool_schema_hash", report.ContractViolations, report.ContractViolationReasons)
	}
}

func TestReportTraceErrorsOnMalformedJSONL(t *testing.T) {
	tracePath := filepath.Join(t.TempDir(), "trace.jsonl")
	if err := os.WriteFile(tracePath, []byte("{bad json}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReportTrace(tracePath); err == nil || !strings.Contains(err.Error(), "parse trace line 1") {
		t.Fatalf("ReportTrace err = %v, want parse line error", err)
	}
}

func TestExampleSmokeTaskLoads(t *testing.T) {
	tasks, err := LoadTasks(filepath.Join("..", "..", "benchmarks", "ahe", "smoke", "python-bugfix-001"))
	if err != nil {
		t.Fatalf("LoadTasks example: %v", err)
	}
	if len(tasks) != 1 || tasks[0].ID != "python-bugfix-001" {
		t.Fatalf("tasks = %+v, want python-bugfix-001", tasks)
	}
}

type labTaskSpec struct {
	ID                    string
	Prompt                string
	MinHitRatio           float64
	MaxContractViolations int
	Files                 map[string]string
	Setup                 string
	Verify                string
}

func writeLabTask(t *testing.T, dir string, spec labTaskSpec) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	toml := "id = " + quote(spec.ID) + "\nname = " + quote(spec.ID) + "\ntimeout_seconds = 30\n"
	if spec.MinHitRatio > 0 || spec.MaxContractViolations > 0 {
		toml += "\n[cache]\n"
		if spec.MinHitRatio > 0 {
			toml += "min_hit_ratio = 0.90\n"
		}
		toml += "max_contract_violations = " + strconv.Itoa(spec.MaxContractViolations) + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "task.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "prompt.md"), []byte(spec.Prompt), 0o644); err != nil {
		t.Fatal(err)
	}
	if spec.Setup != "" {
		if err := os.WriteFile(filepath.Join(dir, "setup.sh"), []byte(spec.Setup), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	verify := spec.Verify
	if verify == "" {
		verify = "test -f calc.py\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "verify.sh"), []byte(verify), 0o755); err != nil {
		t.Fatal(err)
	}
	if len(spec.Files) > 0 {
		for name, body := range spec.Files {
			path := filepath.Join(dir, "files", filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return dir
}

func fakeReasonixBin(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte(fakeReasonixSource), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "fake-reasonix")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	goExe := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goExe += ".exe"
	}
	cmd := exec.Command(goExe, "build", "-o", bin, src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake reasonix: %v\n%s", err, out)
	}
	return bin
}

const fakeReasonixSource = `package main

import (
	"os"
	"strings"
)

func main() {
	var tracePath string
	for i, arg := range os.Args {
		if arg == "--trace" && i+1 < len(os.Args) {
			tracePath = os.Args[i+1]
		}
	}
	if b, err := os.ReadFile("calc.py"); err == nil {
		fixed := strings.ReplaceAll(string(b), "return a - b", "return a + b")
		_ = os.WriteFile("calc.py", []byte(fixed), 0o644)
	}
	if tracePath != "" {
		trace := ` + "`" + `{"version":"trace.v0.1","run_id":"fake","session_id":"s","seq":1,"type":"session_start","time":"2026-06-05T00:00:00Z","turn":0,"data":{"harness_snapshot":"h-0001"}}
{"version":"trace.v0.1","run_id":"fake","session_id":"s","seq":2,"type":"cache_stats","time":"2026-06-05T00:00:00Z","turn":1,"data":{"prompt_cache_hit_tokens":50,"prompt_cache_miss_tokens":50,"cache_hit_ratio":0.5}}
` + "`" + `
		if os.Getenv("FAKE_CONTRACT_VIOLATION") == "1" {
			trace += ` + "`" + `{"version":"trace.v0.1","run_id":"fake","session_id":"s","seq":3,"type":"cache_contract_violation","time":"2026-06-05T00:00:00Z","turn":1,"data":{"reasons":["tool_schema_hash"]}}
` + "`" + `
		}
		_ = os.WriteFile(tracePath, []byte(trace), 0o644)
	}
	if os.Getenv("FAKE_REASONIX_EXIT") != "" {
		os.Exit(1)
	}
}
`

func fixedLabNow() func() time.Time {
	return func() time.Time { return time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC) }
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func contains(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
