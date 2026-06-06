// Package lab runs local Reasonix-AHE eval tasks and writes eval artifacts.
package lab

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"reasonix/internal/harness"
	"reasonix/internal/trace"
)

const DefaultEvalRoot = ".reasonix-ahe/evals"

// Task is one local AHE eval task loaded from task.toml and prompt.md.
type Task struct {
	ID             string `toml:"id" json:"id"`
	Name           string `toml:"name" json:"name,omitempty"`
	Category       string `toml:"category" json:"category,omitempty"`
	Difficulty     string `toml:"difficulty" json:"difficulty,omitempty"`
	TimeoutSeconds int    `toml:"timeout_seconds" json:"timeout_seconds"`
	Dir            string `toml:"-" json:"dir"`
	Prompt         string `toml:"-" json:"prompt"`
	Models         ModelConfig
	Cache          CacheConfig
	Verify         VerifyConfig
}

type ModelConfig struct {
	Default string `toml:"default" json:"default,omitempty"`
}

type CacheConfig struct {
	MinHitRatio           float64 `toml:"min_hit_ratio" json:"min_hit_ratio,omitempty"`
	MaxContractViolations int     `toml:"max_contract_violations" json:"max_contract_violations"`
}

type VerifyConfig struct {
	Command string `toml:"command" json:"command,omitempty"`
}

// Options configures a lab eval run.
type Options struct {
	Bin       string
	Model     string
	TraceMode trace.Mode
	OutputDir string
	RunID     string
	Now       func() time.Time
	Env       []string
	Bash      string
}

// Runner executes local AHE eval tasks.
type Runner struct {
	Options Options
}

// Result is the suite-level eval result written to result.json.
type Result struct {
	RunID       string       `json:"run_id"`
	StartedAt   time.Time    `json:"started_at"`
	ArtifactDir string       `json:"artifact_dir"`
	Passed      bool         `json:"passed"`
	Tasks       []TaskResult `json:"tasks"`
	Warnings    []string     `json:"warnings,omitempty"`
}

// TaskResult is the per-task result written under tasks/<task_id>/result.json.
type TaskResult struct {
	RunID                 string      `json:"run_id"`
	TaskID                string      `json:"task_id"`
	Model                 string      `json:"model,omitempty"`
	HarnessSnapshot       string      `json:"harness_snapshot,omitempty"`
	Passed                bool        `json:"passed"`
	DurationMS            int64       `json:"duration_ms"`
	AgentExitCode         int         `json:"agent_exit_code"`
	VerifyExitCode        int         `json:"verify_exit_code"`
	CacheHitRatio         float64     `json:"cache_hit_ratio"`
	PromptCacheHitTokens  int64       `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens int64       `json:"prompt_cache_miss_tokens"`
	ContractViolations    int         `json:"contract_violations"`
	CacheWarning          bool        `json:"cache_warning,omitempty"`
	Warnings              []string    `json:"warnings,omitempty"`
	ArtifactDir           string      `json:"artifact_dir"`
	TracePath             string      `json:"trace_path"`
	DiffPath              string      `json:"diff_path"`
	VerifyLogPath         string      `json:"verify_log_path"`
	CacheReportPath       string      `json:"cache_report_path"`
	CacheReport           CacheReport `json:"-"`
}

// CacheReport is aggregated from a task trace.
type CacheReport struct {
	ModelCalls                   int      `json:"model_calls"`
	PromptCacheHitTokens         int64    `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens        int64    `json:"prompt_cache_miss_tokens"`
	CacheHitRatio                float64  `json:"cache_hit_ratio"`
	StablePrefixHashDrift        bool     `json:"stable_prefix_hash_drift"`
	StablePrefixHashDriftReasons []string `json:"stable_prefix_hash_drift_reasons,omitempty"`
	ContractViolations           int      `json:"contract_violations"`
	ContractViolationReasons     []string `json:"contract_violation_reasons,omitempty"`
	MiddlewarePolicyDecisions    int      `json:"middleware_policy_decisions"`
	MiddlewarePolicyIDs          []string `json:"middleware_policy_ids,omitempty"`
	HarnessSnapshot              string   `json:"harness_snapshot,omitempty"`
	Warnings                     []string `json:"warnings,omitempty"`
}

// LoadTasks loads a single task directory or every direct child task in a suite.
func LoadTasks(path string) ([]Task, error) {
	if hasTaskFile(path) {
		task, err := loadTask(path)
		if err != nil {
			return nil, err
		}
		return []Task{task}, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	var tasks []Task
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(path, entry.Name())
		if !hasTaskFile(dir) {
			continue
		}
		task, err := loadTask(dir)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	if len(tasks) == 0 {
		return nil, fmt.Errorf("no AHE tasks found under %s", path)
	}
	return tasks, nil
}

func hasTaskFile(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "task.toml"))
	return err == nil
}

func loadTask(dir string) (Task, error) {
	var task Task
	if _, err := toml.DecodeFile(filepath.Join(dir, "task.toml"), &task); err != nil {
		return Task{}, err
	}
	if task.ID == "" {
		task.ID = filepath.Base(dir)
	}
	if task.Name == "" {
		task.Name = task.ID
	}
	if task.TimeoutSeconds <= 0 {
		task.TimeoutSeconds = 300
	}
	promptPath := filepath.Join(dir, "prompt.md")
	prompt, err := os.ReadFile(promptPath)
	if err != nil {
		return Task{}, fmt.Errorf("%s: %w", promptPath, err)
	}
	task.Dir = dir
	task.Prompt = strings.TrimSpace(string(prompt))
	return task, nil
}

// Run executes all tasks found at path and writes a run result.
func (r Runner) Run(ctx context.Context, path string) (Result, error) {
	opts := r.Options.withDefaults()
	if _, err := os.Stat(opts.Bin); err != nil {
		return Result{}, fmt.Errorf("reasonix binary %s: %w", opts.Bin, err)
	}
	tasks, err := LoadTasks(path)
	if err != nil {
		return Result{}, err
	}
	startedAt := opts.now()
	runID := opts.runID(startedAt)
	runDir := filepath.Join(opts.OutputDir, runID)
	if err := os.MkdirAll(filepath.Join(runDir, "tasks"), 0o755); err != nil {
		return Result{}, err
	}
	result := Result{RunID: runID, StartedAt: startedAt, ArtifactDir: runDir, Passed: true}
	for _, task := range tasks {
		taskResult := r.runTask(ctx, opts, runID, runDir, task)
		result.Tasks = append(result.Tasks, taskResult)
		if !taskResult.Passed {
			result.Passed = false
		}
		result.Warnings = append(result.Warnings, taskResult.Warnings...)
	}
	if err := writeJSON(filepath.Join(runDir, "result.json"), result); err != nil {
		return Result{}, err
	}
	return result, nil
}

type resolvedOptions struct {
	Bin       string
	Model     string
	TraceMode trace.Mode
	OutputDir string
	RunID     string
	Now       func() time.Time
	Env       []string
	Bash      string
}

func (o Options) withDefaults() resolvedOptions {
	mode := o.TraceMode
	if mode == "" {
		mode = trace.ModeMetadata
	}
	out := o.OutputDir
	if out == "" {
		out = DefaultEvalRoot
	}
	bin := o.Bin
	if bin == "" {
		bin = DefaultReasonixBin()
	}
	bash := o.Bash
	if bash == "" {
		bash = "bash"
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	return resolvedOptions{
		Bin: bin, Model: o.Model, TraceMode: mode, OutputDir: out,
		RunID: o.RunID, Now: now, Env: append([]string(nil), o.Env...), Bash: bash,
	}
}

func (o resolvedOptions) now() time.Time { return o.Now() }

func (o resolvedOptions) runID(t time.Time) string {
	if o.RunID != "" {
		return o.RunID
	}
	var b [3]byte
	if _, err := rand.Read(b[:]); err == nil {
		return "run-" + t.Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("run-%s-%d", t.Format("20060102-150405"), t.UnixNano())
}

// DefaultReasonixBin returns the local Reasonix binary path used by lab eval.
func DefaultReasonixBin() string {
	if _, err := os.Stat(filepath.Join("bin", "reasonix.exe")); err == nil {
		return filepath.Join("bin", "reasonix.exe")
	}
	return filepath.Join("bin", "reasonix")
}

func (r Runner) runTask(ctx context.Context, opts resolvedOptions, runID, runDir string, task Task) TaskResult {
	start := opts.now()
	artifactDir := filepath.Join(runDir, "tasks", safeName(task.ID))
	workDir := filepath.Join(artifactDir, "workdir")
	tracePath := filepath.Join(artifactDir, "trace.jsonl")
	diffPath := filepath.Join(artifactDir, "diff.patch")
	verifyLogPath := filepath.Join(artifactDir, "verify.log")
	cacheReportPath := filepath.Join(artifactDir, "cache_report.json")
	resultPath := filepath.Join(artifactDir, "result.json")
	taskResult := TaskResult{
		RunID: runID, TaskID: task.ID, Model: chooseModel(opts.Model, task),
		ArtifactDir: artifactDir, TracePath: tracePath, DiffPath: diffPath,
		VerifyLogPath: verifyLogPath, CacheReportPath: cacheReportPath,
		AgentExitCode: 1, VerifyExitCode: 1,
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		taskResult.Warnings = append(taskResult.Warnings, "mkdir workdir: "+err.Error())
		_ = writeJSON(resultPath, taskResult)
		return taskResult
	}
	if filesDir := filepath.Join(task.Dir, "files"); dirExists(filesDir) {
		if err := copyDir(filesDir, workDir); err != nil {
			taskResult.Warnings = append(taskResult.Warnings, "copy files: "+err.Error())
			_ = writeJSON(resultPath, taskResult)
			return taskResult
		}
	}

	var verifyLog strings.Builder
	if setup := filepath.Join(task.Dir, "setup.sh"); fileExists(setup) {
		code, out := runShell(ctx, opts.Bash, []string{"setup.sh"}, workDir, opts.Env, copyScript(setup, filepath.Join(workDir, "setup.sh")))
		verifyLog.WriteString("[setup]\n")
		verifyLog.WriteString(out)
		if code != 0 {
			taskResult.Warnings = append(taskResult.Warnings, fmt.Sprintf("setup exit code %d", code))
		}
	}

	baseline, err := os.MkdirTemp("", "reasonix-ahe-baseline-"+safeName(task.ID)+"-")
	if err != nil {
		taskResult.Warnings = append(taskResult.Warnings, "baseline temp: "+err.Error())
	} else {
		defer os.RemoveAll(baseline)
		_ = copyDir(workDir, baseline)
	}

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(task.TimeoutSeconds)*time.Second)
	defer cancel()
	taskResult.AgentExitCode = r.runReasonix(runCtx, opts, taskResult.Model, task, workDir, tracePath)
	if baseline != "" {
		if warning := writeDiff(baseline, workDir, diffPath); warning != "" {
			taskResult.Warnings = append(taskResult.Warnings, warning)
		}
	} else {
		_ = os.WriteFile(diffPath, nil, 0o644)
	}

	report, err := ReportTrace(tracePath)
	if err != nil {
		taskResult.Warnings = append(taskResult.Warnings, err.Error())
	}
	taskResult.Warnings = append(taskResult.Warnings, report.Warnings...)
	taskResult.CacheReport = report
	taskResult.HarnessSnapshot = report.HarnessSnapshot
	taskResult.CacheHitRatio = report.CacheHitRatio
	taskResult.PromptCacheHitTokens = report.PromptCacheHitTokens
	taskResult.PromptCacheMissTokens = report.PromptCacheMissTokens
	taskResult.ContractViolations = report.ContractViolations
	if taskResult.HarnessSnapshot == "" {
		if active, err := harness.DefaultLayout().Active(); err == nil {
			taskResult.HarnessSnapshot = active
		}
	}
	if task.Cache.MinHitRatio > 0 && report.CacheHitRatio < task.Cache.MinHitRatio {
		taskResult.CacheWarning = true
		taskResult.Warnings = append(taskResult.Warnings, fmt.Sprintf("cache hit ratio %.4f below %.4f", report.CacheHitRatio, task.Cache.MinHitRatio))
	}
	if err := writeJSON(cacheReportPath, report); err != nil {
		taskResult.Warnings = append(taskResult.Warnings, "write cache report: "+err.Error())
	}

	taskResult.VerifyExitCode = runVerify(ctx, opts, task, workDir, &verifyLog)
	_ = os.WriteFile(verifyLogPath, []byte(verifyLog.String()), 0o644)

	taskResult.DurationMS = opts.now().Sub(start).Milliseconds()
	taskResult.Passed = taskResult.AgentExitCode == 0 &&
		taskResult.VerifyExitCode == 0 &&
		taskResult.ContractViolations <= task.Cache.MaxContractViolations
	if taskResult.ContractViolations > task.Cache.MaxContractViolations {
		taskResult.Warnings = append(taskResult.Warnings, fmt.Sprintf("contract violations %d exceed %d", taskResult.ContractViolations, task.Cache.MaxContractViolations))
	}
	_ = writeJSON(resultPath, taskResult)
	return taskResult
}

func (r Runner) runReasonix(ctx context.Context, opts resolvedOptions, model string, task Task, workDir, tracePath string) int {
	args := []string{"run", "--trace", tracePath, "--trace-mode", string(opts.TraceMode)}
	if model != "" {
		args = append(args, "--model", model)
	}
	args = append(args, task.Prompt)
	cmd := exec.CommandContext(ctx, opts.Bin, args...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), opts.Env...)
	cmd.WaitDelay = 10 * time.Second
	if err := cmd.Run(); err != nil {
		return exitCode(err)
	}
	return 0
}

func runVerify(ctx context.Context, opts resolvedOptions, task Task, workDir string, log *strings.Builder) int {
	verify := filepath.Join(task.Dir, "verify.sh")
	if !fileExists(verify) && task.Verify.Command == "" {
		log.WriteString("[verify]\nmissing verify.sh\n")
		return 1
	}
	if fileExists(verify) {
		if err := copyScript(verify, filepath.Join(workDir, "verify.sh")); err != nil {
			log.WriteString("[verify]\ncopy verify.sh: " + err.Error() + "\n")
			return 1
		}
	}
	args := []string{"verify.sh"}
	if task.Verify.Command != "" {
		args = []string{"-lc", task.Verify.Command}
	}
	code, out := runShell(ctx, opts.Bash, args, workDir, opts.Env, nil)
	log.WriteString("[verify]\n")
	log.WriteString(out)
	return code
}

func runShell(ctx context.Context, bash string, args []string, dir string, env []string, preErr error) (int, string) {
	if preErr != nil {
		return 1, preErr.Error() + "\n"
	}
	cmd := exec.CommandContext(ctx, bash, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.WaitDelay = 10 * time.Second
	out, err := cmd.CombinedOutput()
	if err != nil {
		return exitCode(err), string(out)
	}
	return 0, string(out)
}

func int64Data(data map[string]any, key string) int64 {
	switch v := data[key].(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	default:
		return 0
	}
}

func stringData(data map[string]any, key string) string {
	if v, ok := data[key].(string); ok {
		return v
	}
	return ""
}

func chooseModel(override string, task Task) string {
	if override != "" {
		return override
	}
	return task.Models.Default
}

func writeDiff(before, after, out string) string {
	cmd := exec.Command("git", "diff", "--no-index", "--", before, after)
	diff, err := cmd.CombinedOutput()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() > 1 {
			_ = os.WriteFile(out, nil, 0o644)
			return "diff unavailable: " + err.Error()
		}
	}
	if err := os.WriteFile(out, diff, 0o644); err != nil {
		return "write diff: " + err.Error()
	}
	return ""
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return copyFileMode(path, target, info.Mode())
	})
}

func copyScript(src, dst string) error {
	return copyFileMode(src, dst, 0o755)
}

func copyFileMode(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func safeName(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "task"
	}
	var b strings.Builder
	for _, r := range v {
		if r == '-' || r == '_' || r == '.' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	return b.String()
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if exit, ok := err.(*exec.ExitError); ok {
		return exit.ExitCode()
	}
	return 1
}
