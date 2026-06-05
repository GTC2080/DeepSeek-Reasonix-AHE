package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRunDispatchesLabEval(t *testing.T) {
	dir := tempChdir(t)
	taskDir := writeCLIEvalTask(t, filepath.Join(dir, "benchmarks", "ahe", "smoke", "python-bugfix-001"))
	fakeBin := buildCLIFakeReasonix(t)

	out := captureStdout(t, func() {
		rc := Run([]string{"lab", "eval", taskDir, "--bin", fakeBin, "--model", "fake-model", "--trace-mode", "metadata"}, "test-version")
		if rc != 0 {
			t.Fatalf("lab eval rc = %d, want 0", rc)
		}
	})
	if !strings.Contains(out, "python-bugfix-001") || !strings.Contains(out, "pass") {
		t.Fatalf("lab eval output = %q, want task pass", out)
	}
	matches, err := filepath.Glob(filepath.Join(dir, ".reasonix-ahe", "evals", "run-*", "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("result files = %v, want one run result", matches)
	}
}

func TestLabEvalRejectsBadArguments(t *testing.T) {
	tempChdir(t)
	for _, args := range [][]string{
		{"lab", "eval"},
		{"lab", "eval", "missing", "--trace-mode", "loud"},
	} {
		if rc := Run(args, "test-version"); rc != 2 {
			t.Fatalf("Run(%v) rc = %d, want 2", args, rc)
		}
	}
}

func writeCLIEvalTask(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "files"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "task.toml"), []byte("id = \"python-bugfix-001\"\ntimeout_seconds = 30\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "prompt.md"), []byte("fix calc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "files", "calc.py"), []byte("def add(a, b):\n    return a - b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	verify := "grep -q \"return a + b\" calc.py\n"
	if err := os.WriteFile(filepath.Join(dir, "verify.sh"), []byte(verify), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func buildCLIFakeReasonix(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte(cliFakeReasonixSource), 0o644); err != nil {
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
	if out, err := exec.Command(goExe, "build", "-o", bin, src).CombinedOutput(); err != nil {
		t.Fatalf("build fake reasonix: %v\n%s", err, out)
	}
	return bin
}

const cliFakeReasonixSource = `package main

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
		_ = os.WriteFile("calc.py", []byte(strings.ReplaceAll(string(b), "return a - b", "return a + b")), 0o644)
	}
	if tracePath != "" {
		_ = os.WriteFile(tracePath, []byte(` + "`" + `{"version":"trace.v0.1","run_id":"fake","session_id":"s","seq":1,"type":"session_start","time":"2026-06-05T00:00:00Z","turn":0,"data":{}}
{"version":"trace.v0.1","run_id":"fake","session_id":"s","seq":2,"type":"cache_stats","time":"2026-06-05T00:00:00Z","turn":1,"data":{"prompt_cache_hit_tokens":1,"prompt_cache_miss_tokens":0,"cache_hit_ratio":1}}
` + "`" + `), 0o644)
	}
}
`
