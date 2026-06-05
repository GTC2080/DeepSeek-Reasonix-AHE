package lab

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"reasonix/internal/harness"
	"reasonix/internal/trace"
)

const (
	DefaultAHERoot = ".reasonix-ahe"
	bytesMiB       = 1024 * 1024
	bytesGiB       = 1024 * bytesMiB
)

// GCPolicy is the local retention and quota policy used by the dry-run GC
// planner.
type GCPolicy struct {
	KeepRawDays              int   `json:"keep_raw_days"`
	KeepFailedRawDays        int   `json:"keep_failed_raw_days"`
	KeepEvidenceDays         int   `json:"keep_evidence_days"`
	KeepProposalDraftDays    int   `json:"keep_proposal_draft_days"`
	KeepRejectedDays         int   `json:"keep_rejected_days"`
	KeepLastAccepted         int   `json:"keep_last_accepted"`
	MaxTotalTraceBytes       int64 `json:"max_total_trace_bytes"`
	MaxTotalReasonixAHEBytes int64 `json:"max_total_reasonix_ahe_bytes"`
	MaxSnapshots             int   `json:"max_snapshots"`
	MaxEvalRuns              int   `json:"max_eval_runs"`
}

// DefaultGCPolicy returns the P8 v0.1 built-in dry-run policy.
func DefaultGCPolicy() GCPolicy {
	return GCPolicy{
		KeepRawDays:              14,
		KeepFailedRawDays:        30,
		KeepEvidenceDays:         365,
		KeepProposalDraftDays:    14,
		KeepRejectedDays:         7,
		KeepLastAccepted:         50,
		MaxTotalTraceBytes:       10 * bytesGiB,
		MaxTotalReasonixAHEBytes: 20 * bytesGiB,
		MaxSnapshots:             200,
		MaxEvalRuns:              50,
	}
}

// GCOptions controls one dry-run planning pass.
type GCOptions struct {
	AHERoot     string
	HarnessRoot string
	Policy      GCPolicy
	Now         func() time.Time
	DryRun      bool
}

// GCReport is the stable result of a local dry-run GC scan.
type GCReport struct {
	DryRun      bool         `json:"dry_run"`
	WouldDelete []GCDecision `json:"would_delete"`
	WouldKeep   []GCDecision `json:"would_keep"`
	Quota       GCQuota      `json:"quota"`
	Warnings    []string     `json:"warnings,omitempty"`
}

// GCDecision explains why one local artifact would be deleted or kept.
type GCDecision struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Bytes  int64  `json:"bytes"`
	Reason string `json:"reason"`
}

// GCQuota summarizes local AHE artifact usage.
type GCQuota struct {
	ReasonixAHEBytes int64 `json:"reasonix_ahe_bytes"`
	TraceBytes       int64 `json:"trace_bytes"`
	Snapshots        int   `json:"snapshots"`
	EvalRuns         int   `json:"eval_runs"`
	Proposals        int   `json:"proposals"`
	MaxAHEBytes      int64 `json:"max_reasonix_ahe_bytes"`
	MaxTraceBytes    int64 `json:"max_trace_bytes"`
	MaxSnapshots     int   `json:"max_snapshots"`
	MaxEvalRuns      int   `json:"max_eval_runs"`
}

// PlanGC scans local Reasonix-AHE artifacts and returns a dry-run deletion plan.
// It never deletes files.
func PlanGC(opts GCOptions) (GCReport, error) {
	policy := opts.Policy.withDefaults()
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	aheRoot := opts.AHERoot
	if strings.TrimSpace(aheRoot) == "" {
		aheRoot = DefaultAHERoot
	}
	harnessRoot := opts.HarnessRoot
	if strings.TrimSpace(harnessRoot) == "" {
		harnessRoot = harness.RootDir
	}

	report := GCReport{
		DryRun: opts.DryRun,
		Quota: GCQuota{
			MaxAHEBytes:   policy.MaxTotalReasonixAHEBytes,
			MaxTraceBytes: policy.MaxTotalTraceBytes,
			MaxSnapshots:  policy.MaxSnapshots,
			MaxEvalRuns:   policy.MaxEvalRuns,
		},
	}
	if err := checkReadableRoot(aheRoot); err != nil {
		return report, err
	}
	if err := checkReadableRoot(harnessRoot); err != nil {
		return report, err
	}

	var warnings []string
	report.Quota.ReasonixAHEBytes = pathSize(aheRoot, &warnings)
	references := collectSnapshotReferences(aheRoot, &warnings)
	scanRawTraceRoot(aheRoot, policy, now(), &report, &warnings)
	scanEvalRuns(aheRoot, policy, now(), &report, &warnings)
	scanProposals(aheRoot, policy, now(), &report, &warnings)
	scanHarnessSnapshots(harnessRoot, references, policy, &report, &warnings)
	addQuotaWarnings(policy, &report, &warnings)
	report.Warnings = append(report.Warnings, warnings...)
	sortGCReport(&report)
	return report, nil
}

// FormatGCReport returns the default human-readable dry-run GC report.
func FormatGCReport(report GCReport) string {
	var b strings.Builder
	b.WriteString("Reasonix-AHE GC Dry Run\n\n")
	b.WriteString("Quota:\n")
	fmt.Fprintf(&b, "- .reasonix-ahe: %s / %s\n", formatBytes(report.Quota.ReasonixAHEBytes), formatBytes(report.Quota.MaxAHEBytes))
	fmt.Fprintf(&b, "- traces: %s / %s\n", formatBytes(report.Quota.TraceBytes), formatBytes(report.Quota.MaxTraceBytes))
	fmt.Fprintf(&b, "- snapshots: %d / %d\n", report.Quota.Snapshots, report.Quota.MaxSnapshots)
	fmt.Fprintf(&b, "- eval runs: %d / %d\n", report.Quota.EvalRuns, report.Quota.MaxEvalRuns)
	fmt.Fprintf(&b, "- proposals: %d\n\n", report.Quota.Proposals)
	writeGCDecisionList(&b, "Would delete", report.WouldDelete)
	b.WriteByte('\n')
	writeGCDecisionList(&b, "Would keep", report.WouldKeep)
	if len(report.Warnings) > 0 {
		b.WriteString("\nWarnings:\n")
		for _, warning := range report.Warnings {
			fmt.Fprintf(&b, "- %s\n", warning)
		}
	}
	return b.String()
}

func (p GCPolicy) withDefaults() GCPolicy {
	def := DefaultGCPolicy()
	if p.KeepRawDays == 0 {
		p.KeepRawDays = def.KeepRawDays
	}
	if p.KeepFailedRawDays == 0 {
		p.KeepFailedRawDays = def.KeepFailedRawDays
	}
	if p.KeepEvidenceDays == 0 {
		p.KeepEvidenceDays = def.KeepEvidenceDays
	}
	if p.KeepProposalDraftDays == 0 {
		p.KeepProposalDraftDays = def.KeepProposalDraftDays
	}
	if p.KeepRejectedDays == 0 {
		p.KeepRejectedDays = def.KeepRejectedDays
	}
	if p.KeepLastAccepted == 0 {
		p.KeepLastAccepted = def.KeepLastAccepted
	}
	if p.MaxTotalTraceBytes == 0 {
		p.MaxTotalTraceBytes = def.MaxTotalTraceBytes
	}
	if p.MaxTotalReasonixAHEBytes == 0 {
		p.MaxTotalReasonixAHEBytes = def.MaxTotalReasonixAHEBytes
	}
	if p.MaxSnapshots == 0 {
		p.MaxSnapshots = def.MaxSnapshots
	}
	if p.MaxEvalRuns == 0 {
		p.MaxEvalRuns = def.MaxEvalRuns
	}
	return p
}

func checkReadableRoot(path string) error {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", path)
	}
	return nil
}

func scanRawTraceRoot(aheRoot string, policy GCPolicy, now time.Time, report *GCReport, warnings *[]string) {
	root := filepath.Join(aheRoot, "traces")
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			*warnings = append(*warnings, fmt.Sprintf("scan trace %s: %v", path, err))
			return nil
		}
		if d.IsDir() || filepath.Ext(path) != ".jsonl" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			*warnings = append(*warnings, fmt.Sprintf("stat trace %s: %v", path, err))
			return nil
		}
		scanTraceFile(path, "trace", info, false, policy, now, report, warnings)
		return nil
	})
}

func scanTraceFile(path, kind string, info fs.FileInfo, evalFailed bool, policy GCPolicy, now time.Time, report *GCReport, warnings *[]string) {
	report.Quota.TraceBytes += info.Size()
	traceFailed, malformed := traceFailureState(path, warnings)
	failed := evalFailed || traceFailed
	if malformed {
		addKeep(report, path, kind, info.Size(), "malformed trace retained")
		return
	}
	retention := policy.KeepRawDays
	label := "raw trace"
	if failed {
		retention = policy.KeepFailedRawDays
		label = "failed raw trace"
	}
	if olderThan(info.ModTime(), now, retention) {
		addDelete(report, path, kind, info.Size(), fmt.Sprintf("%s older than %d days", label, retention))
		return
	}
	addKeep(report, path, kind, info.Size(), fmt.Sprintf("%s within %d day retention", label, retention))
}

func traceFailureState(path string, warnings *[]string) (failed bool, malformed bool) {
	f, err := os.Open(path)
	if err != nil {
		*warnings = append(*warnings, fmt.Sprintf("read trace %s: %v", path, err))
		return false, true
	}
	defer f.Close()

	reader := bufio.NewReader(f)
	for lineNo := 1; ; lineNo++ {
		line, readErr := reader.ReadBytes('\n')
		trimmed := strings.TrimSpace(string(line))
		if trimmed != "" {
			var ev trace.Event
			if err := json.Unmarshal([]byte(trimmed), &ev); err != nil {
				*warnings = append(*warnings, fmt.Sprintf("parse trace %s line %d: %v", path, lineNo, err))
				return false, true
			}
			if stringData(ev.Data, "error") != "" {
				failed = true
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			*warnings = append(*warnings, fmt.Sprintf("read trace %s line %d: %v", path, lineNo, readErr))
			return failed, true
		}
	}
	return failed, false
}

type evalRunInfo struct {
	Path       string
	Info       fs.FileInfo
	Bytes      int64
	Passed     bool
	ResultRead bool
}

func scanEvalRuns(aheRoot string, policy GCPolicy, now time.Time, report *GCReport, warnings *[]string) {
	root := filepath.Join(aheRoot, "evals")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		*warnings = append(*warnings, fmt.Sprintf("scan evals %s: %v", root, err))
		return
	}

	runs := make([]evalRunInfo, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "run-") {
			continue
		}
		path := filepath.Join(root, entry.Name())
		info, err := entry.Info()
		if err != nil {
			*warnings = append(*warnings, fmt.Sprintf("stat eval run %s: %v", path, err))
			continue
		}
		var result Result
		resultRead := true
		if err := readJSONFile(filepath.Join(path, "result.json"), &result); err != nil {
			resultRead = false
			*warnings = append(*warnings, fmt.Sprintf("read eval result %s: %v", path, err))
		}
		runs = append(runs, evalRunInfo{
			Path: path, Info: info, Bytes: pathSize(path, warnings),
			Passed: result.Passed, ResultRead: resultRead,
		})
	}
	report.Quota.EvalRuns = len(runs)
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].Info.ModTime().Equal(runs[j].Info.ModTime()) {
			return runs[i].Path < runs[j].Path
		}
		return runs[i].Info.ModTime().Before(runs[j].Info.ModTime())
	})
	excess := len(runs) - policy.MaxEvalRuns
	for i, run := range runs {
		overLimit := excess > 0 && i < excess
		failed := !run.Passed
		evidenceProtected := protectEvalEvidence(run, failed, policy, now, report, warnings)
		if overLimit && !evidenceProtected {
			addDelete(report, run.Path, "eval_run", run.Bytes, fmt.Sprintf("eval run exceeds max_eval_runs=%d", policy.MaxEvalRuns))
			continue
		}
		if overLimit && evidenceProtected {
			addKeep(report, run.Path, "eval_run", run.Bytes, fmt.Sprintf("failed eval evidence retained for %d days", policy.KeepEvidenceDays))
		} else {
			addKeep(report, run.Path, "eval_run", run.Bytes, fmt.Sprintf("within max_eval_runs=%d", policy.MaxEvalRuns))
		}
		scanEvalTraceFiles(run.Path, failed || !run.ResultRead, policy, now, report, warnings)
	}
}

func protectEvalEvidence(run evalRunInfo, failed bool, policy GCPolicy, now time.Time, report *GCReport, warnings *[]string) bool {
	if !failed {
		return false
	}
	path := filepath.Join(run.Path, "evidence")
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false
	}
	if err != nil {
		*warnings = append(*warnings, fmt.Sprintf("stat eval evidence %s: %v", path, err))
		return false
	}
	if !info.IsDir() || olderThan(info.ModTime(), now, policy.KeepEvidenceDays) {
		return false
	}
	size := pathSize(path, warnings)
	addKeep(report, path, "eval_evidence", size, fmt.Sprintf("failed eval evidence within %d day retention", policy.KeepEvidenceDays))
	return true
}

func scanEvalTraceFiles(runPath string, failed bool, policy GCPolicy, now time.Time, report *GCReport, warnings *[]string) {
	_ = filepath.WalkDir(runPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			*warnings = append(*warnings, fmt.Sprintf("scan eval trace %s: %v", path, err))
			return nil
		}
		if d.IsDir() || filepath.Base(path) != "trace.jsonl" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			*warnings = append(*warnings, fmt.Sprintf("stat eval trace %s: %v", path, err))
			return nil
		}
		scanTraceFile(path, "eval_trace", info, failed, policy, now, report, warnings)
		return nil
	})
}

type gcProposalInfo struct {
	Path      string
	Info      fs.FileInfo
	Bytes     int64
	Status    ProposalStatusResult
	UpdatedAt time.Time
}

func scanProposals(aheRoot string, policy GCPolicy, now time.Time, report *GCReport, warnings *[]string) {
	root := filepath.Join(aheRoot, "proposals")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		*warnings = append(*warnings, fmt.Sprintf("scan proposals %s: %v", root, err))
		return
	}
	var accepted []gcProposalInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name())
		info, err := entry.Info()
		if err != nil {
			*warnings = append(*warnings, fmt.Sprintf("stat proposal %s: %v", path, err))
			continue
		}
		size := pathSize(path, warnings)
		report.Quota.Proposals++
		status, err := ReadProposalStatus(path)
		if err != nil {
			*warnings = append(*warnings, fmt.Sprintf("read proposal status %s: %v", path, err))
			addKeep(report, path, "proposal", size, "proposal status unreadable")
			continue
		}
		updatedAt := status.Status.UpdatedAt
		if updatedAt.IsZero() {
			updatedAt = info.ModTime()
		}
		switch status.Status.State {
		case ProposalStateDraft:
			if olderThan(info.ModTime(), now, policy.KeepProposalDraftDays) {
				addDelete(report, path, "proposal", size, fmt.Sprintf("draft proposal older than %d days", policy.KeepProposalDraftDays))
				continue
			}
			addKeep(report, path, "proposal", size, fmt.Sprintf("draft proposal within %d day retention", policy.KeepProposalDraftDays))
		case ProposalStateReady:
			addKeep(report, path, "proposal", size, "ready proposal retained")
		case ProposalStateRejected:
			if olderThan(updatedAt, now, policy.KeepRejectedDays) {
				addDelete(report, path, "proposal", size, fmt.Sprintf("rejected proposal older than %d days", policy.KeepRejectedDays))
				continue
			}
			addKeep(report, path, "proposal", size, fmt.Sprintf("rejected proposal within %d day retention", policy.KeepRejectedDays))
		case ProposalStateAccepted:
			accepted = append(accepted, gcProposalInfo{
				Path: path, Info: info, Bytes: size, Status: status, UpdatedAt: updatedAt,
			})
		default:
			*warnings = append(*warnings, fmt.Sprintf("proposal %s has unknown state %q", path, status.Status.State))
			addKeep(report, path, "proposal", size, "proposal state unknown")
		}
	}
	sort.Slice(accepted, func(i, j int) bool {
		if accepted[i].UpdatedAt.Equal(accepted[j].UpdatedAt) {
			return accepted[i].Path > accepted[j].Path
		}
		return accepted[i].UpdatedAt.After(accepted[j].UpdatedAt)
	})
	for i, proposal := range accepted {
		if i >= policy.KeepLastAccepted {
			addDelete(report, proposal.Path, "proposal", proposal.Bytes, fmt.Sprintf("accepted proposal exceeds keep_last_accepted=%d", policy.KeepLastAccepted))
			continue
		}
		addKeep(report, proposal.Path, "proposal", proposal.Bytes, fmt.Sprintf("accepted proposal within keep_last_accepted=%d", policy.KeepLastAccepted))
	}
}

type snapshotInfo struct {
	ID      string
	Path    string
	Info    fs.FileInfo
	Bytes   int64
	Created time.Time
}

func scanHarnessSnapshots(root string, references map[string]bool, policy GCPolicy, report *GCReport, warnings *[]string) {
	layout := harness.NewLayout(root)
	active, err := layout.Active()
	if err != nil {
		*warnings = append(*warnings, fmt.Sprintf("read active snapshot: %v", err))
	}
	pinned := readPinnedSnapshots(root, warnings)

	entries, err := os.ReadDir(layout.SnapshotsDir())
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		*warnings = append(*warnings, fmt.Sprintf("scan snapshots %s: %v", layout.SnapshotsDir(), err))
		return
	}
	snapshots := make([]snapshotInfo, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !isSnapshotID(entry.Name()) {
			continue
		}
		path := layout.SnapshotDir(entry.Name())
		info, err := entry.Info()
		if err != nil {
			*warnings = append(*warnings, fmt.Sprintf("stat snapshot %s: %v", path, err))
			continue
		}
		created := info.ModTime()
		if lock, err := layout.Inspect(entry.Name()); err == nil && !lock.CreatedAt.IsZero() {
			created = lock.CreatedAt
		}
		snapshots = append(snapshots, snapshotInfo{
			ID: entry.Name(), Path: path, Info: info, Created: created, Bytes: pathSize(path, warnings),
		})
	}
	report.Quota.Snapshots = len(snapshots)
	sort.Slice(snapshots, func(i, j int) bool {
		if snapshots[i].Created.Equal(snapshots[j].Created) {
			return snapshots[i].ID < snapshots[j].ID
		}
		return snapshots[i].Created.Before(snapshots[j].Created)
	})
	deleteBudget := len(snapshots) - policy.MaxSnapshots
	for _, snapshot := range snapshots {
		switch {
		case snapshot.ID == active:
			addKeep(report, snapshot.Path, "harness_snapshot", snapshot.Bytes, "active snapshot")
		case pinned[snapshot.ID]:
			addKeep(report, snapshot.Path, "harness_snapshot", snapshot.Bytes, "pinned snapshot")
		case references[snapshot.ID]:
			addKeep(report, snapshot.Path, "harness_snapshot", snapshot.Bytes, "snapshot referenced by local artifact")
		case deleteBudget > 0:
			addDelete(report, snapshot.Path, "harness_snapshot", snapshot.Bytes, fmt.Sprintf("snapshot exceeds max_snapshots=%d", policy.MaxSnapshots))
			deleteBudget--
		default:
			addKeep(report, snapshot.Path, "harness_snapshot", snapshot.Bytes, fmt.Sprintf("within max_snapshots=%d", policy.MaxSnapshots))
		}
	}
	if deleteBudget > 0 {
		*warnings = append(*warnings, fmt.Sprintf("snapshot quota remains over limit by %d protected snapshots", deleteBudget))
	}
}

func readPinnedSnapshots(root string, warnings *[]string) map[string]bool {
	out := map[string]bool{}
	pinned, err := harness.NewLayout(root).Pinned()
	if err != nil {
		*warnings = append(*warnings, fmt.Sprintf("read pinned snapshots: %v", err))
		return out
	}
	for _, id := range pinned {
		out[id] = true
	}
	return out
}

func collectSnapshotReferences(root string, warnings *[]string) map[string]bool {
	refs := map[string]bool{}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			*warnings = append(*warnings, fmt.Sprintf("scan references %s: %v", path, err))
			return nil
		}
		if d.IsDir() {
			return nil
		}
		switch filepath.Ext(path) {
		case ".json":
			collectSnapshotReferencesFromJSON(path, refs, warnings)
		case ".jsonl":
			collectSnapshotReferencesFromJSONL(path, refs, warnings)
		}
		return nil
	})
	return refs
}

func collectSnapshotReferencesFromJSON(path string, refs map[string]bool, warnings *[]string) {
	b, err := os.ReadFile(path)
	if err != nil {
		*warnings = append(*warnings, fmt.Sprintf("read references %s: %v", path, err))
		return
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		*warnings = append(*warnings, fmt.Sprintf("parse references %s: %v", path, err))
		return
	}
	collectSnapshotReferencesFromValue(v, refs)
}

func collectSnapshotReferencesFromJSONL(path string, refs map[string]bool, warnings *[]string) {
	f, err := os.Open(path)
	if err != nil {
		*warnings = append(*warnings, fmt.Sprintf("read references %s: %v", path, err))
		return
	}
	defer f.Close()

	reader := bufio.NewReader(f)
	for lineNo := 1; ; lineNo++ {
		line, readErr := reader.ReadBytes('\n')
		trimmed := strings.TrimSpace(string(line))
		if trimmed != "" {
			var v any
			if err := json.Unmarshal([]byte(trimmed), &v); err != nil {
				*warnings = append(*warnings, fmt.Sprintf("parse references %s line %d: %v", path, lineNo, err))
				return
			}
			collectSnapshotReferencesFromValue(v, refs)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			*warnings = append(*warnings, fmt.Sprintf("read references %s line %d: %v", path, lineNo, readErr))
			return
		}
	}
}

func collectSnapshotReferencesFromValue(v any, refs map[string]bool) {
	switch typed := v.(type) {
	case map[string]any:
		for key, value := range typed {
			if isSnapshotReferenceKey(key) {
				if s, ok := value.(string); ok && isSnapshotID(s) {
					refs[s] = true
				}
			}
			collectSnapshotReferencesFromValue(value, refs)
		}
	case []any:
		for _, item := range typed {
			collectSnapshotReferencesFromValue(item, refs)
		}
	}
}

func isSnapshotReferenceKey(key string) bool {
	switch key {
	case "harness_snapshot", "base_snapshot", "target_snapshot":
		return true
	default:
		return false
	}
}

func addQuotaWarnings(policy GCPolicy, report *GCReport, warnings *[]string) {
	if report.Quota.ReasonixAHEBytes > policy.MaxTotalReasonixAHEBytes {
		*warnings = append(*warnings, fmt.Sprintf(".reasonix-ahe size %s exceeds quota %s", formatBytes(report.Quota.ReasonixAHEBytes), formatBytes(policy.MaxTotalReasonixAHEBytes)))
	}
	if report.Quota.TraceBytes > policy.MaxTotalTraceBytes {
		*warnings = append(*warnings, fmt.Sprintf("trace size %s exceeds quota %s", formatBytes(report.Quota.TraceBytes), formatBytes(policy.MaxTotalTraceBytes)))
	}
}

func addDelete(report *GCReport, path, kind string, bytes int64, reason string) {
	report.WouldDelete = append(report.WouldDelete, GCDecision{Path: path, Kind: kind, Bytes: bytes, Reason: reason})
}

func addKeep(report *GCReport, path, kind string, bytes int64, reason string) {
	report.WouldKeep = append(report.WouldKeep, GCDecision{Path: path, Kind: kind, Bytes: bytes, Reason: reason})
}

func sortGCReport(report *GCReport) {
	sort.Slice(report.WouldDelete, func(i, j int) bool {
		return gcDecisionLess(report.WouldDelete[i], report.WouldDelete[j])
	})
	sort.Slice(report.WouldKeep, func(i, j int) bool {
		return gcDecisionLess(report.WouldKeep[i], report.WouldKeep[j])
	})
	sort.Strings(report.Warnings)
}

func gcDecisionLess(a, b GCDecision) bool {
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	return a.Path < b.Path
}

func pathSize(path string, warnings *[]string) int64 {
	var total int64
	err := filepath.WalkDir(path, func(path string, d fs.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			*warnings = append(*warnings, fmt.Sprintf("scan size %s: %v", path, err))
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			*warnings = append(*warnings, fmt.Sprintf("stat size %s: %v", path, err))
			return nil
		}
		total += info.Size()
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		*warnings = append(*warnings, fmt.Sprintf("scan size %s: %v", path, err))
	}
	return total
}

func olderThan(t, now time.Time, days int) bool {
	if days <= 0 {
		return false
	}
	return now.Sub(t) > time.Duration(days)*24*time.Hour
}

func isSnapshotID(id string) bool {
	if len(id) != len("h-0001") || !strings.HasPrefix(id, "h-") {
		return false
	}
	for _, r := range id[2:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func writeGCDecisionList(b *strings.Builder, title string, decisions []GCDecision) {
	fmt.Fprintf(b, "%s:\n", title)
	if len(decisions) == 0 {
		b.WriteString("- (none)\n")
		return
	}
	for _, decision := range decisions {
		fmt.Fprintf(b, "- %s (%s, %s): %s\n", decision.Path, decision.Kind, formatBytes(decision.Bytes), decision.Reason)
	}
}

func formatBytes(v int64) string {
	if v < bytesMiB {
		return strconv.FormatInt(v, 10) + "B"
	}
	if v < bytesGiB {
		return fmt.Sprintf("%.1fMB", float64(v)/float64(bytesMiB))
	}
	return fmt.Sprintf("%.1fGB", float64(v)/float64(bytesGiB))
}
