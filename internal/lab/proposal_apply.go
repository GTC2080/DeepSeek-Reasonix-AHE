package lab

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"reasonix/internal/harness"
	"reasonix/internal/trace"
)

const ProposalDiffFile = "diff.patch"

type ProposalApplyOptions struct {
	Dir         string
	HarnessRoot string
	EvalPath    string
	Bin         string
	Model       string
	TraceMode   trace.Mode
	Now         func() time.Time
	AttemptID   string
	Env         []string
	Bash        string
}

type ProposalApplyResult struct {
	ProposalID      string                  `json:"proposal_id"`
	ProposalDir     string                  `json:"proposal_dir"`
	AttemptID       string                  `json:"attempt_id"`
	AttemptDir      string                  `json:"attempt_dir"`
	LogPath         string                  `json:"log_path"`
	ResultPath      string                  `json:"result_path"`
	BaseSnapshot    string                  `json:"base_snapshot"`
	TargetSnapshot  string                  `json:"target_snapshot,omitempty"`
	CreatedAt       time.Time               `json:"created_at"`
	ManifestUpdated bool                    `json:"manifest_updated"`
	Passed          bool                    `json:"passed"`
	EvalPath        string                  `json:"eval_path,omitempty"`
	EvalRunID       string                  `json:"eval_run_id,omitempty"`
	EvalArtifactDir string                  `json:"eval_artifact_dir,omitempty"`
	Gate            ProposalApplyGateReport `json:"gate,omitempty"`
	GateFailures    []string                `json:"gate_failures,omitempty"`
	Warnings        []string                `json:"warnings,omitempty"`
	Error           string                  `json:"error,omitempty"`
}

type ProposalApplyGateReport struct {
	SmokePassed           int     `json:"smoke_passed"`
	SmokeTotal            int     `json:"smoke_total"`
	SmokePassRate         float64 `json:"smoke_pass_rate"`
	CanaryPassed          int     `json:"canary_passed"`
	CanaryTotal           int     `json:"canary_total"`
	CanaryPassRate        float64 `json:"canary_pass_rate"`
	CacheHitRatio         float64 `json:"cache_hit_ratio"`
	PromptCacheHitTokens  int64   `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens int64   `json:"prompt_cache_miss_tokens"`
	ContractViolations    int     `json:"contract_violations"`
	MaxContractViolations int     `json:"max_contract_violations"`
	MinSmokePassRate      float64 `json:"min_smoke_pass_rate"`
	MinCanaryPassRate     float64 `json:"min_canary_pass_rate"`
	MinCacheHitRatio      float64 `json:"min_cache_hit_ratio"`
}

func ApplyProposal(ctx context.Context, opts ProposalApplyOptions) (ProposalApplyResult, error) {
	dir := strings.TrimSpace(opts.Dir)
	if dir == "" {
		return ProposalApplyResult{}, fmt.Errorf("proposal dir is required")
	}
	manifest, err := readProposalManifest(dir)
	if err != nil {
		return ProposalApplyResult{}, err
	}
	if err := ensureProposalCanApply(dir, manifest); err != nil {
		return ProposalApplyResult{}, err
	}
	if strings.TrimSpace(manifest.TargetSnapshot) != "" {
		return ProposalApplyResult{}, fmt.Errorf("proposal already has target_snapshot %s", manifest.TargetSnapshot)
	}
	if errors := ValidateProposalApplyReady(manifest); len(errors) > 0 {
		return ProposalApplyResult{}, fmt.Errorf("proposal is not apply-ready: %s", strings.Join(errors, "; "))
	}

	createdAt := proposalNow(opts.Now)
	attemptID := opts.AttemptID
	if attemptID == "" {
		attemptID = proposalApplyAttemptID(createdAt)
	}
	attemptDir := filepath.Join(dir, "apply", attemptID)
	logPath := filepath.Join(attemptDir, "apply.log")
	resultPath := filepath.Join(attemptDir, "result.json")
	result := ProposalApplyResult{
		ProposalID: manifest.ProposalID, ProposalDir: dir, AttemptID: attemptID,
		AttemptDir: attemptDir, LogPath: logPath, ResultPath: resultPath,
		BaseSnapshot: manifest.BaseSnapshot, CreatedAt: createdAt, EvalPath: opts.EvalPath,
	}
	if err := os.MkdirAll(attemptDir, 0o755); err != nil {
		return result, err
	}
	var log strings.Builder
	finish := func(err error) (ProposalApplyResult, error) {
		if err != nil {
			result.Passed = false
			result.Error = err.Error()
			fmt.Fprintf(&log, "error: %s\n", err)
		}
		if writeErr := os.WriteFile(logPath, []byte(log.String()), 0o644); writeErr != nil {
			result.Warnings = append(result.Warnings, "write apply log: "+writeErr.Error())
			if err == nil {
				err = writeErr
			}
		}
		if writeErr := writeJSON(resultPath, result); writeErr != nil {
			result.Warnings = append(result.Warnings, "write apply result: "+writeErr.Error())
			if err == nil {
				err = writeErr
			}
		}
		return result, err
	}

	layout := harness.NewLayout(opts.HarnessRoot)
	baseLock, err := layout.Inspect(manifest.BaseSnapshot)
	if err != nil {
		return finish(err)
	}
	patchPath := filepath.Join(dir, ProposalDiffFile)
	if err := validateProposalPatch(patchPath); err != nil {
		return finish(err)
	}

	tmpRoot, err := os.MkdirTemp("", "reasonix-ahe-proposal-apply-")
	if err != nil {
		return finish(err)
	}
	defer os.RemoveAll(tmpRoot)
	stagedSource := filepath.Join(tmpRoot, "source")
	if err := prepareProposalApplySource(layout, baseLock, stagedSource, &log); err != nil {
		return finish(err)
	}
	patchAbs, err := filepath.Abs(patchPath)
	if err != nil {
		return finish(err)
	}
	if err := runGitApply(stagedSource, patchAbs, true, &log); err != nil {
		return finish(err)
	}
	if err := runGitApply(stagedSource, patchAbs, false, &log); err != nil {
		return finish(err)
	}

	targetLock, err := layout.CreateSnapshotFromSource(stagedSource, createdAt)
	if err != nil {
		return finish(err)
	}
	result.TargetSnapshot = targetLock.SnapshotID
	fmt.Fprintf(&log, "created target snapshot %s\n", targetLock.SnapshotID)

	if opts.EvalPath != "" {
		evalResult, gate, failures, warnings, err := runProposalApplyEval(ctx, opts, attemptDir, layout, targetLock.SnapshotID)
		result.EvalRunID = evalResult.RunID
		result.EvalArtifactDir = evalResult.ArtifactDir
		result.Gate = gate
		result.GateFailures = failures
		result.Warnings = append(result.Warnings, warnings...)
		if err != nil {
			return finish(err)
		}
		if len(failures) > 0 {
			return finish(fmt.Errorf("proposal apply gate failed: %s", strings.Join(failures, "; ")))
		}
	}

	manifest.TargetSnapshot = targetLock.SnapshotID
	if err := writeJSON(filepath.Join(dir, "manifest.json"), manifest); err != nil {
		return finish(err)
	}
	result.ManifestUpdated = true
	result.Passed = true
	return finish(nil)
}

func ValidateProposalApplyReady(m ProposalManifest) []string {
	var errors []string
	requireString(&errors, "proposal_id", m.ProposalID)
	requireString(&errors, "base_snapshot", m.BaseSnapshot)
	requireNonEmptyStrings(&errors, "components_changed", m.ComponentsChanged)
	requireNonEmptyStrings(&errors, "evidence", m.Evidence)
	requireString(&errors, "root_cause", m.RootCause)
	requireNonEmptyStrings(&errors, "expected_fixes", m.ExpectedFixes)
	requireNonEmptyStrings(&errors, "regression_risks", m.RegressionRisks)
	requireString(&errors, "rollback_rule", m.RollbackRule)
	if m.CacheRisk == nil {
		errors = append(errors, "cache_risk is required")
	} else if m.CacheRisk.ExpectedHitRatioDelta < -1 || m.CacheRisk.ExpectedHitRatioDelta > 1 {
		errors = append(errors, "cache_risk.expected_hit_ratio_delta must be between -1 and 1")
	}
	if m.AcceptanceRules == nil {
		errors = append(errors, "acceptance_rules is required")
	} else {
		requireRatio(&errors, "acceptance_rules.min_smoke_pass_rate", m.AcceptanceRules.MinSmokePassRate)
		requireRatio(&errors, "acceptance_rules.min_canary_pass_rate", m.AcceptanceRules.MinCanaryPassRate)
		requireRatio(&errors, "acceptance_rules.min_cache_hit_ratio", m.AcceptanceRules.MinCacheHitRatio)
		if m.AcceptanceRules.MaxContractViolations < 0 {
			errors = append(errors, "acceptance_rules.max_contract_violations must be >= 0")
		}
	}
	return errors
}

func readProposalManifest(dir string) (ProposalManifest, error) {
	var manifest ProposalManifest
	if err := readJSONFile(filepath.Join(dir, "manifest.json"), &manifest); err != nil {
		return ProposalManifest{}, err
	}
	return manifest, nil
}

func ensureProposalCanApply(dir string, manifest ProposalManifest) error {
	var status ProposalStatus
	if err := readJSONFile(filepath.Join(dir, ProposalStatusFile), &status); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if status.ProposalID == "" {
		status.ProposalID = manifest.ProposalID
	}
	if status.ProposalID != manifest.ProposalID {
		return fmt.Errorf("proposal status id %q does not match manifest id %q", status.ProposalID, manifest.ProposalID)
	}
	if !validProposalState(status.State) {
		return fmt.Errorf("proposal status state %q is invalid", status.State)
	}
	if status.State == ProposalStateAccepted || status.State == ProposalStateRejected {
		return fmt.Errorf("proposal already %s", status.State)
	}
	return nil
}

func prepareProposalApplySource(layout harness.Layout, base harness.Lock, stagedSource string, log *strings.Builder) error {
	snapshotSource := layout.SnapshotSourceDir(base.SnapshotID)
	if dirExists(snapshotSource) {
		fmt.Fprintf(log, "using base snapshot source %s\n", snapshotSource)
		return copyDir(snapshotSource, stagedSource)
	}
	currentLock, err := harness.CaptureSourceLock(layout.SourceDir(), base.SnapshotID, base.CreatedAt)
	if err != nil {
		return fmt.Errorf("base snapshot %s has no source copy and current source cannot be hashed: %w", base.SnapshotID, err)
	}
	if !sameHarnessLockShape(base, currentLock) {
		return fmt.Errorf("base snapshot %s has no source copy and current source does not match base snapshot lock", base.SnapshotID)
	}
	fmt.Fprintf(log, "using current source fallback for lock-only base snapshot %s\n", base.SnapshotID)
	return copyDir(layout.SourceDir(), stagedSource)
}

func sameHarnessLockShape(a, b harness.Lock) bool {
	return a.SystemPromptHash == b.SystemPromptHash &&
		a.ToolDescriptionHash == b.ToolDescriptionHash &&
		a.SkillIndexHash == b.SkillIndexHash &&
		a.MiddlewareHash == b.MiddlewareHash &&
		a.ModelRoutingHash == b.ModelRoutingHash &&
		a.StablePrefixHash == b.StablePrefixHash
}

func validateProposalPatch(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory", path)
	}
	if info.Size() == 0 {
		return fmt.Errorf("%s is empty", path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			fields := strings.Fields(line)
			if len(fields) < 4 {
				return fmt.Errorf("invalid patch header %q", line)
			}
			if err := validateProposalPatchPath(fields[2]); err != nil {
				return err
			}
			if err := validateProposalPatchPath(fields[3]); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") {
			token := strings.TrimSpace(line[4:])
			if i := strings.IndexByte(token, '\t'); i >= 0 {
				token = token[:i]
			}
			if err := validateProposalPatchPath(token); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateProposalPatchPath(token string) error {
	token = strings.TrimSpace(token)
	if token == "" || token == "/dev/null" {
		return nil
	}
	if strings.HasPrefix(token, "a/") || strings.HasPrefix(token, "b/") {
		token = token[2:]
	}
	fromSlash := filepath.FromSlash(token)
	if strings.Contains(token, "\x00") || strings.HasPrefix(token, "/") || filepath.IsAbs(fromSlash) || looksLikeWindowsAbs(token) {
		return fmt.Errorf("patch path %q must be relative to harness source", token)
	}
	clean := filepath.Clean(fromSlash)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("patch path %q escapes harness source", token)
	}
	return nil
}

func looksLikeWindowsAbs(path string) bool {
	return len(path) >= 3 && path[1] == ':' && (path[2] == '/' || path[2] == '\\') &&
		((path[0] >= 'A' && path[0] <= 'Z') || (path[0] >= 'a' && path[0] <= 'z'))
}

func runGitApply(dir, patch string, check bool, log *strings.Builder) error {
	args := []string{"apply"}
	label := "git apply"
	if check {
		args = append(args, "--check")
		label = "git apply --check"
	}
	args = append(args, patch)
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		fmt.Fprintf(log, "[%s]\n%s\n", label, strings.TrimSpace(string(out)))
	}
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s failed: %s", label, msg)
	}
	fmt.Fprintf(log, "%s succeeded\n", label)
	return nil
}

func runProposalApplyEval(ctx context.Context, opts ProposalApplyOptions, attemptDir string, layout harness.Layout, targetSnapshot string) (Result, ProposalApplyGateReport, []string, []string, error) {
	previousActive, err := layout.Active()
	if err != nil {
		return Result{}, ProposalApplyGateReport{}, nil, nil, fmt.Errorf("read active snapshot: %w", err)
	}
	if err := layout.Activate(targetSnapshot); err != nil {
		return Result{}, ProposalApplyGateReport{}, nil, nil, fmt.Errorf("activate target snapshot: %w", err)
	}
	mode := opts.TraceMode
	if mode == "" {
		mode = trace.ModeMetadata
	}
	evalResult, err := (Runner{Options: Options{
		Bin: opts.Bin, Model: opts.Model, TraceMode: mode,
		OutputDir: filepath.Join(attemptDir, "evals"),
		Now:       opts.Now,
		Env:       append([]string(nil), opts.Env...),
		Bash:      opts.Bash,
	}}).Run(ctx, opts.EvalPath)
	warnings := restoreActiveSnapshot(layout, previousActive)
	if len(warnings) > 0 && err == nil {
		err = errors.New(warnings[0])
	}
	if err != nil {
		return evalResult, ProposalApplyGateReport{}, nil, warnings, err
	}
	manifest, manifestErr := readProposalManifest(opts.Dir)
	if manifestErr != nil {
		return evalResult, ProposalApplyGateReport{}, nil, warnings, manifestErr
	}
	gate, failures := evaluateProposalApplyGate(manifest, opts.EvalPath, evalResult)
	return evalResult, gate, failures, warnings, nil
}

func restoreActiveSnapshot(layout harness.Layout, previousActive string) []string {
	if previousActive == "" {
		if err := layout.ClearActive(); err != nil {
			return []string{"restore active snapshot: " + err.Error()}
		}
		return nil
	}
	if err := layout.Activate(previousActive); err != nil {
		return []string{"restore active snapshot: " + err.Error()}
	}
	return nil
}

func evaluateProposalApplyGate(manifest ProposalManifest, evalPath string, result Result) (ProposalApplyGateReport, []string) {
	report := ProposalApplyGateReport{}
	if manifest.AcceptanceRules != nil {
		report.MinSmokePassRate = manifest.AcceptanceRules.MinSmokePassRate
		report.MinCanaryPassRate = manifest.AcceptanceRules.MinCanaryPassRate
		report.MinCacheHitRatio = manifest.AcceptanceRules.MinCacheHitRatio
		report.MaxContractViolations = manifest.AcceptanceRules.MaxContractViolations
	}
	tasks, err := LoadTasks(evalPath)
	if err != nil {
		return report, []string{"load eval tasks: " + err.Error()}
	}
	classes := map[string]string{}
	for _, task := range tasks {
		classes[task.ID] = proposalEvalClass(task)
	}
	var hit, miss int64
	for _, task := range result.Tasks {
		switch classes[task.TaskID] {
		case "canary":
			report.CanaryTotal++
			if task.Passed {
				report.CanaryPassed++
			}
		default:
			report.SmokeTotal++
			if task.Passed {
				report.SmokePassed++
			}
		}
		hit += task.PromptCacheHitTokens
		miss += task.PromptCacheMissTokens
		report.ContractViolations += task.ContractViolations
	}
	report.PromptCacheHitTokens = hit
	report.PromptCacheMissTokens = miss
	report.SmokePassRate = passRate(report.SmokePassed, report.SmokeTotal)
	report.CanaryPassRate = passRate(report.CanaryPassed, report.CanaryTotal)
	if total := hit + miss; total > 0 {
		report.CacheHitRatio = float64(hit) / float64(total)
	}

	var failures []string
	if manifest.AcceptanceRules == nil {
		return report, []string{"acceptance_rules is required"}
	}
	if manifest.AcceptanceRules.MinSmokePassRate > 0 {
		failures = appendPassRateFailure(failures, "smoke", report.SmokePassed, report.SmokeTotal, report.SmokePassRate, manifest.AcceptanceRules.MinSmokePassRate)
	}
	if manifest.AcceptanceRules.MinCanaryPassRate > 0 {
		failures = appendPassRateFailure(failures, "canary", report.CanaryPassed, report.CanaryTotal, report.CanaryPassRate, manifest.AcceptanceRules.MinCanaryPassRate)
	}
	if manifest.AcceptanceRules.MinCacheHitRatio > 0 && report.CacheHitRatio < manifest.AcceptanceRules.MinCacheHitRatio {
		failures = append(failures, fmt.Sprintf("cache hit ratio %.4f below %.4f", report.CacheHitRatio, manifest.AcceptanceRules.MinCacheHitRatio))
	}
	if report.ContractViolations > manifest.AcceptanceRules.MaxContractViolations {
		failures = append(failures, fmt.Sprintf("contract violations %d exceed %d", report.ContractViolations, manifest.AcceptanceRules.MaxContractViolations))
	}
	return report, failures
}

func proposalEvalClass(task Task) string {
	for _, value := range []string{task.Difficulty, task.Category} {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "canary":
			return "canary"
		case "smoke":
			return "smoke"
		}
	}
	return "smoke"
}

func appendPassRateFailure(failures []string, label string, passed, total int, got, want float64) []string {
	if total == 0 {
		return append(failures, label+" pass rate requires at least one "+label+" task")
	}
	if got < want {
		return append(failures, fmt.Sprintf("%s pass rate %s/%s (%.4f) below %.4f", label, strconv.Itoa(passed), strconv.Itoa(total), got, want))
	}
	return failures
}

func passRate(passed, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(passed) / float64(total)
}

func proposalApplyAttemptID(t time.Time) string {
	var b [3]byte
	if _, err := rand.Read(b[:]); err == nil {
		return "attempt-" + t.Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
	}
	return "attempt-" + t.Format("20060102-150405")
}
