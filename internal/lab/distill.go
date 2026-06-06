package lab

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"reasonix/internal/trace"
)

// FailureKind is the deterministic v0.1 taxonomy for eval evidence reports.
type FailureKind string

const (
	FailureVerifierFailed      FailureKind = "verifier_failed"
	FailureTimeout             FailureKind = "timeout"
	FailureToolErrorLoop       FailureKind = "tool_error_loop"
	FailurePrematureSuccess    FailureKind = "premature_success"
	FailurePermissionDenied    FailureKind = "permission_denied"
	FailureCacheContractBroken FailureKind = "cache_contract_broken"
	FailureNoPatch             FailureKind = "no_patch"
	FailurePatchDoesNotApply   FailureKind = "patch_does_not_apply"
)

// DistillResult describes the evidence files generated for one eval run.
type DistillResult struct {
	RunDir      string          `json:"run_dir"`
	EvidenceDir string          `json:"evidence_dir"`
	Tasks       []DistilledTask `json:"tasks"`
}

// DistilledTask is one generated task evidence report.
type DistilledTask struct {
	TaskID              string        `json:"task_id"`
	ReportPath          string        `json:"report_path"`
	Passed              bool          `json:"passed"`
	FailureKinds        []FailureKind `json:"failure_kinds,omitempty"`
	SuggestedComponents []string      `json:"suggested_components,omitempty"`
}

type distillTaskData struct {
	Task       TaskResult
	Cache      CacheReport
	VerifyLog  string
	Diff       string
	Trace      distillTraceSummary
	Failures   []FailureKind
	Components []string
}

type distillTraceSummary struct {
	ToolCalls  map[string]int
	ErrorLoops bool
	Signals    []string
}

var failureOrder = []FailureKind{
	FailureVerifierFailed,
	FailureTimeout,
	FailureToolErrorLoop,
	FailurePrematureSuccess,
	FailurePermissionDenied,
	FailureCacheContractBroken,
	FailureNoPatch,
	FailurePatchDoesNotApply,
}

// Distill generates deterministic markdown evidence for a P4 eval run.
func Distill(runDir string) (DistillResult, error) {
	var run Result
	resultPath := filepath.Join(runDir, "result.json")
	if err := readJSONFile(resultPath, &run); err != nil {
		return DistillResult{}, err
	}
	if len(run.Tasks) == 0 {
		return DistillResult{}, fmt.Errorf("%s: no tasks", resultPath)
	}

	evidenceDir := filepath.Join(runDir, "evidence")
	if err := os.MkdirAll(evidenceDir, 0o755); err != nil {
		return DistillResult{}, fmt.Errorf("create evidence dir: %w", err)
	}

	tasks := append([]TaskResult(nil), run.Tasks...)
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].TaskID < tasks[j].TaskID })
	out := DistillResult{RunDir: runDir, EvidenceDir: evidenceDir}
	clusters := map[FailureKind][]DistilledTask{}
	for _, summary := range tasks {
		data, err := loadDistillTask(runDir, summary)
		if err != nil {
			return DistillResult{}, err
		}
		data.Failures = classifyFailures(data)
		data.Components = suggestedComponents(data.Failures)
		reportPath := filepath.Join(evidenceDir, "task-"+safeName(data.Task.TaskID)+".md")
		if err := os.WriteFile(reportPath, []byte(formatTaskEvidence(data)), 0o644); err != nil {
			return DistillResult{}, fmt.Errorf("write %s: %w", reportPath, err)
		}
		task := DistilledTask{
			TaskID: data.Task.TaskID, ReportPath: reportPath, Passed: data.Task.Passed,
			FailureKinds: data.Failures, SuggestedComponents: data.Components,
		}
		out.Tasks = append(out.Tasks, task)
		for _, kind := range data.Failures {
			clusters[kind] = append(clusters[kind], task)
		}
	}
	if err := os.WriteFile(filepath.Join(evidenceDir, "clusters.md"), []byte(formatClusters(clusters)), 0o644); err != nil {
		return DistillResult{}, fmt.Errorf("write clusters: %w", err)
	}
	return out, nil
}

func loadDistillTask(runDir string, summary TaskResult) (distillTaskData, error) {
	taskDir := distillTaskDir(runDir, summary)
	var task TaskResult
	if err := readJSONFile(filepath.Join(taskDir, "result.json"), &task); err != nil {
		return distillTaskData{}, err
	}
	mergeTaskResult(&task, summary)
	if task.TaskID == "" {
		task.TaskID = filepath.Base(taskDir)
	}
	task.ArtifactDir = taskDir
	task.TracePath = resolveArtifactPath(taskDir, task.TracePath, "trace.jsonl")
	task.DiffPath = resolveArtifactPath(taskDir, task.DiffPath, "diff.patch")
	task.VerifyLogPath = resolveArtifactPath(taskDir, task.VerifyLogPath, "verify.log")
	task.CacheReportPath = resolveArtifactPath(taskDir, task.CacheReportPath, "cache_report.json")

	var cache CacheReport
	if err := readJSONFile(task.CacheReportPath, &cache); err != nil {
		return distillTaskData{}, err
	}
	if cache.HarnessSnapshot == "" {
		cache.HarnessSnapshot = task.HarnessSnapshot
	}
	if cache.ContractViolations == 0 {
		cache.ContractViolations = task.ContractViolations
	}
	if cache.CacheHitRatio == 0 {
		cache.CacheHitRatio = task.CacheHitRatio
	}
	verifyLog, err := readTextFile(task.VerifyLogPath)
	if err != nil {
		return distillTaskData{}, err
	}
	diff, err := readTextFile(task.DiffPath)
	if err != nil {
		return distillTaskData{}, err
	}
	traceSummary, err := summarizeDistillTrace(task.TracePath)
	if err != nil {
		return distillTaskData{}, err
	}
	return distillTaskData{Task: task, Cache: cache, VerifyLog: verifyLog, Diff: diff, Trace: traceSummary}, nil
}

func distillTaskDir(runDir string, task TaskResult) string {
	if task.ArtifactDir != "" {
		if filepath.IsAbs(task.ArtifactDir) || dirExists(task.ArtifactDir) {
			return task.ArtifactDir
		}
	}
	return filepath.Join(runDir, "tasks", safeName(task.TaskID))
}

func resolveArtifactPath(taskDir, path, name string) string {
	if path == "" {
		return filepath.Join(taskDir, name)
	}
	if filepath.IsAbs(path) || fileExists(path) {
		return path
	}
	return filepath.Join(taskDir, filepath.Base(path))
}

func mergeTaskResult(task *TaskResult, fallback TaskResult) {
	if task.RunID == "" {
		task.RunID = fallback.RunID
	}
	if task.TaskID == "" {
		task.TaskID = fallback.TaskID
	}
	if task.Model == "" {
		task.Model = fallback.Model
	}
	if task.HarnessSnapshot == "" {
		task.HarnessSnapshot = fallback.HarnessSnapshot
	}
	if task.ArtifactDir == "" {
		task.ArtifactDir = fallback.ArtifactDir
	}
	if task.TracePath == "" {
		task.TracePath = fallback.TracePath
	}
	if task.DiffPath == "" {
		task.DiffPath = fallback.DiffPath
	}
	if task.VerifyLogPath == "" {
		task.VerifyLogPath = fallback.VerifyLogPath
	}
	if task.CacheReportPath == "" {
		task.CacheReportPath = fallback.CacheReportPath
	}
}

func classifyFailures(data distillTaskData) []FailureKind {
	if data.Task.Passed {
		return nil
	}
	text := strings.ToLower(strings.Join(append(append([]string{}, data.Task.Warnings...), data.VerifyLog, strings.Join(data.Trace.Signals, "\n")), "\n"))
	var kinds []FailureKind
	if data.Task.VerifyExitCode != 0 {
		kinds = append(kinds, FailureVerifierFailed)
	}
	if containsAny(text, "timeout", "timed out", "deadline exceeded", "context deadline") {
		kinds = append(kinds, FailureTimeout)
	}
	if data.Trace.ErrorLoops {
		kinds = append(kinds, FailureToolErrorLoop)
	}
	if data.Task.AgentExitCode == 0 && data.Task.VerifyExitCode != 0 {
		kinds = append(kinds, FailurePrematureSuccess)
	}
	if containsAny(text, "permission denied", "approval denied", "denied by policy", "access denied") {
		kinds = append(kinds, FailurePermissionDenied)
	}
	if data.Task.ContractViolations > 0 || data.Cache.ContractViolations > 0 {
		kinds = append(kinds, FailureCacheContractBroken)
	}
	if strings.TrimSpace(data.Diff) == "" {
		kinds = append(kinds, FailureNoPatch)
	}
	if containsAny(text, "patch does not apply", "failed to apply patch", "apply patch failed", "patch apply failed") {
		kinds = append(kinds, FailurePatchDoesNotApply)
	}
	return orderFailures(appendUniqueFailures(nil, kinds...))
}

func orderFailures(kinds []FailureKind) []FailureKind {
	have := map[FailureKind]bool{}
	for _, kind := range kinds {
		have[kind] = true
	}
	var out []FailureKind
	for _, kind := range failureOrder {
		if have[kind] {
			out = append(out, kind)
		}
	}
	return out
}

func suggestedComponents(kinds []FailureKind) []string {
	var out []string
	for _, kind := range kinds {
		switch kind {
		case FailureVerifierFailed, FailurePrematureSuccess:
			out = appendUnique(out, "middleware/post_success_guard.toml", "tool_descriptions/bash.md")
		case FailureTimeout:
			out = appendUnique(out, "middleware/timeout_budget.toml")
		case FailureToolErrorLoop:
			out = appendUnique(out, "middleware/tool_error_loop_guard.toml")
		case FailurePermissionDenied:
			out = appendUnique(out, "middleware/permission_recovery.toml")
		case FailureCacheContractBroken:
			out = appendUnique(out, "middleware/cache_contract_guard.toml")
		case FailureNoPatch, FailurePatchDoesNotApply:
			out = appendUnique(out, "tool_descriptions/edit_file.md")
		}
	}
	return out
}

func formatTaskEvidence(data distillTaskData) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Task Report: %s\n\n", data.Task.TaskID)
	fmt.Fprintf(&b, "Result: %s\n", passFail(data.Task.Passed))
	fmt.Fprintf(&b, "Harness snapshot: %s\n", displayReportValue(firstNonEmpty(data.Task.HarnessSnapshot, data.Cache.HarnessSnapshot)))
	fmt.Fprintf(&b, "Model: %s\n", displayReportValue(data.Task.Model))
	fmt.Fprintf(&b, "Cache hit ratio: %.2f%%\n", data.Cache.CacheHitRatio*100)
	fmt.Fprintf(&b, "Contract violations: %d\n\n", maxInt(data.Task.ContractViolations, data.Cache.ContractViolations))
	b.WriteString("## Last verifier output\n\n")
	b.WriteString("```text\n")
	b.WriteString(markdownCodeText(lastVerifierOutput(data.VerifyLog)))
	b.WriteString("\n```\n\n")
	b.WriteString("## Tool-call summary\n\n")
	writeStringCountList(&b, data.Trace.ToolCalls)
	if len(data.Trace.Signals) > 0 {
		b.WriteString("\n## Trace signals\n\n")
		writeStringList(&b, data.Trace.Signals)
	}
	b.WriteString("\n## Suspected failure pattern\n\n")
	writeFailureList(&b, data.Failures)
	b.WriteString("\n## Suggested components\n\n")
	writeStringList(&b, data.Components)
	return b.String()
}

func formatClusters(clusters map[FailureKind][]DistilledTask) string {
	var b strings.Builder
	b.WriteString("# Failure Clusters\n\n")
	if len(clusters) == 0 {
		b.WriteString("No failure clusters.\n")
		return b.String()
	}
	for _, kind := range failureOrder {
		tasks := clusters[kind]
		if len(tasks) == 0 {
			continue
		}
		sort.Slice(tasks, func(i, j int) bool { return tasks[i].TaskID < tasks[j].TaskID })
		fmt.Fprintf(&b, "## %s\n\n", kind)
		fmt.Fprintf(&b, "Tasks: %d\n\n", len(tasks))
		for _, task := range tasks {
			fmt.Fprintf(&b, "- %s\n", task.TaskID)
		}
		b.WriteString("\nSuggested components:\n")
		writeStringList(&b, suggestedComponents([]FailureKind{kind}))
		b.WriteByte('\n')
	}
	return b.String()
}

func summarizeDistillTrace(path string) (distillTraceSummary, error) {
	f, err := os.Open(path)
	if err != nil {
		return distillTraceSummary{}, fmt.Errorf("read trace: %w", err)
	}
	defer f.Close()

	summary := distillTraceSummary{ToolCalls: map[string]int{}}
	errorCounts := map[string]int{}
	reader := bufio.NewReader(f)
	for lineNo := 1; ; lineNo++ {
		line, readErr := reader.ReadBytes('\n')
		trimmed := strings.TrimSpace(string(line))
		if trimmed != "" {
			var ev trace.Event
			if err := json.Unmarshal([]byte(trimmed), &ev); err != nil {
				return summary, fmt.Errorf("parse trace line %d: %w", lineNo, err)
			}
			switch ev.Type {
			case "tool_call":
				if boolData(ev.Data, "partial") {
					break
				}
				name := stringData(ev.Data, "tool_name")
				if name == "" {
					name = "(unknown)"
				}
				summary.ToolCalls[name]++
			case "tool_result":
				name := stringData(ev.Data, "tool_name")
				errText := stringData(ev.Data, "error")
				if errText != "" {
					key := name + "\x00" + strings.ToLower(errText)
					errorCounts[key]++
					summary.Signals = append(summary.Signals, errText)
				}
			case "notice":
				if text := firstNonEmpty(stringData(ev.Data, "text"), stringData(ev.Data, "text_preview")); text != "" {
					summary.Signals = append(summary.Signals, text)
				}
			case "middleware_policy_decision":
				if signal := middlewarePolicySignal(ev.Data); signal != "" {
					summary.Signals = append(summary.Signals, signal)
				}
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return summary, fmt.Errorf("read trace line %d: %w", lineNo, readErr)
		}
	}
	for _, count := range errorCounts {
		if count >= 3 {
			summary.ErrorLoops = true
			break
		}
	}
	return summary, nil
}

func middlewarePolicySignal(data map[string]any) string {
	policyID := stringData(data, "policy_id")
	if policyID == "" {
		return ""
	}
	action := displayReportValue(stringData(data, "action"))
	stage := displayReportValue(stringData(data, "stage"))
	reason := firstNonEmpty(stringData(data, "reason"), stringData(data, "reason_preview"))
	if reason == "" {
		return fmt.Sprintf("middleware policy %s: %s at %s", policyID, action, stage)
	}
	return fmt.Sprintf("middleware policy %s: %s at %s: %s", policyID, action, stage, reason)
}

func writeStringCountList(b *strings.Builder, values map[string]int) {
	if len(values) == 0 {
		b.WriteString("- (none)\n")
		return
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(b, "- %s: %d\n", trace.RedactString(key), values[key])
	}
}

func writeFailureList(b *strings.Builder, values []FailureKind) {
	if len(values) == 0 {
		b.WriteString("- none\n")
		return
	}
	for _, value := range values {
		fmt.Fprintf(b, "- %s\n", value)
	}
}

func writeStringList(b *strings.Builder, values []string) {
	if len(values) == 0 {
		b.WriteString("- none\n")
		return
	}
	for _, value := range values {
		fmt.Fprintf(b, "- %s\n", trace.RedactString(value))
	}
}

func lastVerifierOutput(log string) string {
	if idx := strings.LastIndex(log, "[verify]"); idx >= 0 {
		log = log[idx+len("[verify]"):]
	}
	log = strings.TrimSpace(log)
	if log == "" {
		return "(empty)"
	}
	const maxBytes = 4096
	if len(log) > maxBytes {
		log = "(truncated to last 4096 bytes)\n" + log[len(log)-maxBytes:]
	}
	return trace.RedactString(log)
}

func markdownCodeText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "```", "` ` `")
	return s
}

func passFail(v bool) string {
	if v {
		return "PASSED"
	}
	return "FAILED"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func appendUniqueFailures(values []FailureKind, additions ...FailureKind) []FailureKind {
	seen := make(map[FailureKind]bool, len(values)+len(additions))
	for _, value := range values {
		seen[value] = true
	}
	for _, value := range additions {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		values = append(values, value)
	}
	return values
}

func readJSONFile(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func readTextFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	return string(b), nil
}
