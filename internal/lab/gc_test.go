package lab

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/harness"
)

func TestPlanGCDryRunClassifiesRawAndFailedTraces(t *testing.T) {
	root := t.TempDir()
	aheRoot := filepath.Join(root, DefaultAHERoot)
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	oldSuccess := filepath.Join(aheRoot, "traces", "old-success.trace.jsonl")
	oldFailed := filepath.Join(aheRoot, "traces", "old-failed.trace.jsonl")
	veryOldFailed := filepath.Join(aheRoot, "traces", "very-old-failed.trace.jsonl")
	writeGCTrace(t, oldSuccess, "h-0001", "")
	writeGCTrace(t, oldFailed, "h-0001", "boom")
	writeGCTrace(t, veryOldFailed, "h-0001", "boom")
	setGCModTime(t, oldSuccess, now.AddDate(0, 0, -20))
	setGCModTime(t, oldFailed, now.AddDate(0, 0, -20))
	setGCModTime(t, veryOldFailed, now.AddDate(0, 0, -40))

	report, err := PlanGC(GCOptions{
		AHERoot:     aheRoot,
		HarnessRoot: filepath.Join(root, harness.RootDir),
		Now:         func() time.Time { return now },
		DryRun:      true,
	})
	if err != nil {
		t.Fatalf("PlanGC: %v", err)
	}
	if !hasGCDecision(report.WouldDelete, oldSuccess, "raw trace older than 14 days") {
		t.Fatalf("delete decisions = %+v, want old success trace delete", report.WouldDelete)
	}
	if !hasGCDecision(report.WouldKeep, oldFailed, "failed raw trace within 30 day retention") {
		t.Fatalf("keep decisions = %+v, want failed trace retained", report.WouldKeep)
	}
	if !hasGCDecision(report.WouldDelete, veryOldFailed, "failed raw trace older than 30 days") {
		t.Fatalf("delete decisions = %+v, want very old failed trace delete", report.WouldDelete)
	}
	for _, path := range []string{oldSuccess, oldFailed, veryOldFailed} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("dry-run should not delete %s: %v", path, err)
		}
	}
}

func TestPlanGCKeepsProtectedSnapshotsAndDeletesOldExcess(t *testing.T) {
	root := t.TempDir()
	aheRoot := filepath.Join(root, DefaultAHERoot)
	harnessRoot := filepath.Join(root, harness.RootDir)
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	for i := 1; i <= 5; i++ {
		id := "h-000" + string(rune('0'+i))
		writeGCSnapshot(t, harnessRoot, id, now.AddDate(0, 0, -10-i))
	}
	if err := os.WriteFile(filepath.Join(harnessRoot, "active"), []byte("h-0002\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(harnessRoot, "pinned"), []byte("h-0003\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeGCTrace(t, filepath.Join(aheRoot, "traces", "referenced.trace.jsonl"), "h-0004", "")

	report, err := PlanGC(GCOptions{
		AHERoot:     aheRoot,
		HarnessRoot: harnessRoot,
		Policy:      GCPolicy{MaxSnapshots: 3},
		Now:         func() time.Time { return now },
		DryRun:      true,
	})
	if err != nil {
		t.Fatalf("PlanGC: %v", err)
	}
	if !hasGCDecision(report.WouldDelete, filepath.Join(harnessRoot, "snapshots", "h-0001"), "snapshot exceeds max_snapshots=3") {
		t.Fatalf("delete decisions = %+v, want h-0001 excess", report.WouldDelete)
	}
	for _, tc := range []struct {
		id     string
		reason string
	}{
		{"h-0002", "active snapshot"},
		{"h-0003", "pinned snapshot"},
		{"h-0004", "snapshot referenced by local artifact"},
	} {
		if !hasGCDecision(report.WouldKeep, filepath.Join(harnessRoot, "snapshots", tc.id), tc.reason) {
			t.Fatalf("keep decisions = %+v, missing %s %q", report.WouldKeep, tc.id, tc.reason)
		}
	}
	if report.Quota.Snapshots != 5 {
		t.Fatalf("snapshot count = %d, want 5", report.Quota.Snapshots)
	}
}

func TestPlanGCProposalDraftsAndEvalEvidence(t *testing.T) {
	root := t.TempDir()
	aheRoot := filepath.Join(root, DefaultAHERoot)
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	draft := filepath.Join(aheRoot, "proposals", "p-0001-draft")
	ready := filepath.Join(aheRoot, "proposals", "p-0002-ready")
	rejected := filepath.Join(aheRoot, "proposals", "p-0003-rejected")
	acceptedOld := filepath.Join(aheRoot, "proposals", "p-0004-accepted-old")
	acceptedNew := filepath.Join(aheRoot, "proposals", "p-0005-accepted-new")
	if err := os.MkdirAll(draft, 0o755); err != nil {
		t.Fatal(err)
	}
	writeProposalManifest(t, filepath.Join(draft, "manifest.json"), ProposalManifest{ProposalID: "p-0001-draft", BaseSnapshot: "h-0001"})
	writeReadyProposal(t, ready, "p-0002-ready", "h-0002")
	writeReadyProposal(t, rejected, "p-0003-rejected", "h-0002")
	writeGCProposalStatus(t, rejected, "p-0003-rejected", ProposalStateRejected, now.AddDate(0, 0, -10), "not enough evidence")
	writeReadyProposal(t, acceptedOld, "p-0004-accepted-old", "h-0002")
	writeGCProposalStatus(t, acceptedOld, "p-0004-accepted-old", ProposalStateAccepted, now.AddDate(0, 0, -20), "")
	writeReadyProposal(t, acceptedNew, "p-0005-accepted-new", "h-0002")
	writeGCProposalStatus(t, acceptedNew, "p-0005-accepted-new", ProposalStateAccepted, now.AddDate(0, 0, -1), "")
	setGCModTime(t, draft, now.AddDate(0, 0, -20))
	setGCModTime(t, ready, now.AddDate(0, 0, -20))
	setGCModTime(t, rejected, now.AddDate(0, 0, -10))
	setGCModTime(t, acceptedOld, now.AddDate(0, 0, -20))
	setGCModTime(t, acceptedNew, now.AddDate(0, 0, -1))

	oldFailedRun := filepath.Join(aheRoot, "evals", "run-20260501-000000-old")
	newRun := filepath.Join(aheRoot, "evals", "run-20260601-000000-new")
	writeGCEvalRun(t, oldFailedRun, false, "h-0007", true)
	writeGCEvalRun(t, newRun, true, "h-0008", false)
	oldTrace := filepath.Join(oldFailedRun, "tasks", "task", "trace.jsonl")
	setGCModTime(t, oldFailedRun, now.AddDate(0, 0, -40))
	setGCModTime(t, oldTrace, now.AddDate(0, 0, -40))
	setGCModTime(t, filepath.Join(oldFailedRun, "evidence"), now.AddDate(0, 0, -100))
	setGCModTime(t, newRun, now.AddDate(0, 0, -1))

	report, err := PlanGC(GCOptions{
		AHERoot:     aheRoot,
		HarnessRoot: filepath.Join(root, harness.RootDir),
		Policy:      GCPolicy{MaxEvalRuns: 1, KeepLastAccepted: 1},
		Now:         func() time.Time { return now },
		DryRun:      true,
	})
	if err != nil {
		t.Fatalf("PlanGC: %v", err)
	}
	if !hasGCDecision(report.WouldDelete, draft, "draft proposal older than 14 days") {
		t.Fatalf("delete decisions = %+v, want old draft proposal", report.WouldDelete)
	}
	if !hasGCDecision(report.WouldKeep, ready, "ready proposal retained") {
		t.Fatalf("keep decisions = %+v, want ready proposal retained", report.WouldKeep)
	}
	if !hasGCDecision(report.WouldDelete, rejected, "rejected proposal older than 7 days") {
		t.Fatalf("delete decisions = %+v, want old rejected proposal", report.WouldDelete)
	}
	if !hasGCDecision(report.WouldDelete, acceptedOld, "accepted proposal exceeds keep_last_accepted=1") {
		t.Fatalf("delete decisions = %+v, want excess accepted proposal", report.WouldDelete)
	}
	if !hasGCDecision(report.WouldKeep, acceptedNew, "within keep_last_accepted=1") {
		t.Fatalf("keep decisions = %+v, want latest accepted proposal retained", report.WouldKeep)
	}
	if !hasGCDecision(report.WouldKeep, filepath.Join(oldFailedRun, "evidence"), "failed eval evidence within 365 day retention") {
		t.Fatalf("keep decisions = %+v, want failed eval evidence kept", report.WouldKeep)
	}
	if !hasGCDecision(report.WouldKeep, oldFailedRun, "failed eval evidence retained for 365 days") {
		t.Fatalf("keep decisions = %+v, want old failed eval run retained", report.WouldKeep)
	}
	if !hasGCDecision(report.WouldDelete, oldTrace, "failed raw trace older than 30 days") {
		t.Fatalf("delete decisions = %+v, want old failed eval trace", report.WouldDelete)
	}
}

func writeGCProposalStatus(t *testing.T, dir, proposalID string, state ProposalState, updatedAt time.Time, reason string) {
	t.Helper()
	if err := writeJSON(filepath.Join(dir, ProposalStatusFile), ProposalStatus{
		ProposalID: proposalID,
		State:      state,
		UpdatedAt:  updatedAt,
		Reason:     reason,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPlanGCQuotaWarnings(t *testing.T) {
	root := t.TempDir()
	aheRoot := filepath.Join(root, DefaultAHERoot)
	tracePath := filepath.Join(aheRoot, "traces", "trace.jsonl")
	writeGCTrace(t, tracePath, "", "")
	report, err := PlanGC(GCOptions{
		AHERoot:     aheRoot,
		HarnessRoot: filepath.Join(root, harness.RootDir),
		Policy: GCPolicy{
			MaxTotalReasonixAHEBytes: 1,
			MaxTotalTraceBytes:       1,
		},
		Now: func() time.Time { return time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("PlanGC: %v", err)
	}
	if !hasGCWarning(report.Warnings, ".reasonix-ahe size") || !hasGCWarning(report.Warnings, "trace size") {
		t.Fatalf("warnings = %v, want AHE and trace quota warnings", report.Warnings)
	}
}

func writeGCTrace(t *testing.T, path, snapshotID, errText string) {
	t.Helper()
	data := "{}"
	if snapshotID != "" {
		data = `{"harness_snapshot":"` + snapshotID + `"}`
	}
	body := `{"version":"trace.v0.1","run_id":"r","session_id":"s","seq":1,"type":"session_start","time":"2026-06-05T00:00:00Z","turn":0,"data":` + data + `}
`
	if errText != "" {
		body += `{"version":"trace.v0.1","run_id":"r","session_id":"s","seq":2,"type":"session_end","time":"2026-06-05T00:00:00Z","turn":1,"data":{"error":"` + errText + `"}}
`
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeGCSnapshot(t *testing.T, root, id string, created time.Time) {
	t.Helper()
	dir := filepath.Join(root, "snapshots", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lock := harness.Lock{SnapshotID: id, CreatedAt: created, StablePrefixHash: "sha256:test"}
	if err := writeJSON(filepath.Join(dir, harness.LockFile), lock); err != nil {
		t.Fatal(err)
	}
	setGCModTime(t, dir, created)
}

func writeGCEvalRun(t *testing.T, dir string, passed bool, snapshotID string, evidence bool) {
	t.Helper()
	taskDir := filepath.Join(dir, "tasks", "task")
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tracePath := filepath.Join(taskDir, "trace.jsonl")
	writeGCTrace(t, tracePath, snapshotID, map[bool]string{true: "", false: "verify failed"}[passed])
	if err := writeJSON(filepath.Join(taskDir, "result.json"), TaskResult{
		RunID: "run", TaskID: "task", Passed: passed, HarnessSnapshot: snapshotID,
		TracePath: tracePath,
	}); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(dir, "result.json"), Result{
		RunID: "run", ArtifactDir: dir, Passed: passed,
		Tasks: []TaskResult{{RunID: "run", TaskID: "task", Passed: passed, HarnessSnapshot: snapshotID}},
	}); err != nil {
		t.Fatal(err)
	}
	if evidence {
		evidenceDir := filepath.Join(dir, "evidence")
		if err := os.MkdirAll(evidenceDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(evidenceDir, "task.md"), []byte("evidence"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func setGCModTime(t *testing.T, path string, mod time.Time) {
	t.Helper()
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatalf("chtimes %s: %v", path, err)
	}
}

func hasGCDecision(decisions []GCDecision, path, reasonPart string) bool {
	for _, decision := range decisions {
		if decision.Path == path && strings.Contains(decision.Reason, reasonPart) {
			return true
		}
	}
	return false
}

func hasGCWarning(warnings []string, part string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, part) {
			return true
		}
	}
	return false
}
