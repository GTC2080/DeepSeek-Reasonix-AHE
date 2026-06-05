package lab

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/harness"
)

func TestCreateProposalWritesDraftAndIncrements(t *testing.T) {
	root := filepath.Join(t.TempDir(), "proposals")
	if err := os.MkdirAll(filepath.Join(root, "p-0001-existing"), 0o755); err != nil {
		t.Fatal(err)
	}

	created, err := CreateProposal(ProposalCreateOptions{
		Root:         root,
		BaseSnapshot: "h-0001",
		Name:         "Post Success Guard!",
	})
	if err != nil {
		t.Fatalf("CreateProposal: %v", err)
	}
	if created.ProposalID != "p-0002-post-success-guard" {
		t.Fatalf("proposal id = %q, want p-0002-post-success-guard", created.ProposalID)
	}
	for _, path := range []string{created.ManifestPath, created.EvidencePath, created.DiffPath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected artifact %s: %v", path, err)
		}
	}
	var manifest ProposalManifest
	if err := readJSONFile(created.ManifestPath, &manifest); err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if manifest.ProposalID != created.ProposalID || manifest.BaseSnapshot != "h-0001" {
		t.Fatalf("manifest = %+v, want id/base populated", manifest)
	}

	check, err := CheckProposal(created.Dir)
	if err != nil {
		t.Fatalf("CheckProposal draft: %v", err)
	}
	for _, want := range []string{"target_snapshot is required", "expected_fixes must contain at least one item", "regression_risks must contain at least one item", "cache_risk is required", "rollback_rule is required"} {
		if !stringListContains(check.Errors, want) {
			t.Fatalf("draft errors = %v, missing %q", check.Errors, want)
		}
	}
}

func TestCheckProposalAcceptsCompleteManifest(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "p-0001-complete")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeProposalManifest(t, filepath.Join(dir, "manifest.json"), completeProposalManifest())

	check, err := CheckProposal(dir)
	if err != nil {
		t.Fatalf("CheckProposal: %v", err)
	}
	if len(check.Errors) != 0 {
		t.Fatalf("check errors = %v, want none", check.Errors)
	}
}

func TestCheckProposalRejectsMissingMalformedAndInvalidFields(t *testing.T) {
	t.Run("missing manifest", func(t *testing.T) {
		if _, err := CheckProposal(t.TempDir()); err == nil || !strings.Contains(err.Error(), "manifest.json") {
			t.Fatalf("CheckProposal err = %v, want manifest error", err)
		}
	})

	t.Run("malformed manifest", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte("{bad json}"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := CheckProposal(dir); err == nil || !strings.Contains(err.Error(), "manifest.json") {
			t.Fatalf("CheckProposal err = %v, want malformed manifest error", err)
		}
	})

	t.Run("invalid fields", func(t *testing.T) {
		dir := t.TempDir()
		manifest := completeProposalManifest()
		manifest.ExpectedFixes = nil
		manifest.RegressionRisks = []string{}
		manifest.CacheRisk.ExpectedHitRatioDelta = -2
		manifest.AcceptanceRules.MinCacheHitRatio = 1.2
		manifest.AcceptanceRules.MaxContractViolations = -1
		writeProposalManifest(t, filepath.Join(dir, "manifest.json"), manifest)

		check, err := CheckProposal(dir)
		if err != nil {
			t.Fatalf("CheckProposal: %v", err)
		}
		for _, want := range []string{
			"expected_fixes must contain at least one item",
			"regression_risks must contain at least one item",
			"cache_risk.expected_hit_ratio_delta must be between -1 and 1",
			"acceptance_rules.min_cache_hit_ratio must be between 0 and 1",
			"acceptance_rules.max_contract_violations must be >= 0",
		} {
			if !stringListContains(check.Errors, want) {
				t.Fatalf("errors = %v, missing %q", check.Errors, want)
			}
		}
	})
}

func TestProposalStatusDerivesDraftAndReadyWhenNoSidecarExists(t *testing.T) {
	root := t.TempDir()
	draft := filepath.Join(root, "p-0001-draft")
	if err := os.MkdirAll(draft, 0o755); err != nil {
		t.Fatal(err)
	}
	writeProposalManifest(t, filepath.Join(draft, "manifest.json"), ProposalManifest{ProposalID: "p-0001-draft", BaseSnapshot: "h-0001"})
	draftStatus, err := ReadProposalStatus(draft)
	if err != nil {
		t.Fatalf("ReadProposalStatus draft: %v", err)
	}
	if draftStatus.Status.State != ProposalStateDraft {
		t.Fatalf("draft state = %q, want %q", draftStatus.Status.State, ProposalStateDraft)
	}

	ready := filepath.Join(root, "p-0002-ready")
	if err := os.MkdirAll(ready, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := completeProposalManifest()
	manifest.ProposalID = "p-0002-ready"
	writeProposalManifest(t, filepath.Join(ready, "manifest.json"), manifest)
	readyStatus, err := ReadProposalStatus(ready)
	if err != nil {
		t.Fatalf("ReadProposalStatus ready: %v", err)
	}
	if readyStatus.Status.State != ProposalStateReady {
		t.Fatalf("ready state = %q, want %q", readyStatus.Status.State, ProposalStateReady)
	}
}

func TestAcceptProposalRequiresReadyManifestAndExistingTarget(t *testing.T) {
	root := t.TempDir()
	proposalDir := filepath.Join(root, "p-0001-ready")
	writeReadyProposal(t, proposalDir, "p-0001-ready", "h-0002")
	layout := harness.NewLayout(filepath.Join(root, harness.RootDir))

	if _, err := AcceptProposal(ProposalAcceptOptions{Dir: proposalDir, HarnessRoot: layout.Root}); err == nil || !strings.Contains(err.Error(), "snapshot h-0002 not found") {
		t.Fatalf("AcceptProposal missing target err = %v, want snapshot missing", err)
	}
	if _, err := layout.CreateSnapshot(time.Unix(1, 0).UTC()); err != nil {
		t.Fatalf("CreateSnapshot h-0001: %v", err)
	}
	if _, err := layout.CreateSnapshot(time.Unix(2, 0).UTC()); err != nil {
		t.Fatalf("CreateSnapshot h-0002: %v", err)
	}

	accepted, err := AcceptProposal(ProposalAcceptOptions{
		Dir: proposalDir, HarnessRoot: layout.Root, Activate: true, PinTarget: true,
		Now: func() time.Time { return time.Unix(10, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("AcceptProposal: %v", err)
	}
	if accepted.Status.State != ProposalStateAccepted || accepted.Status.ProposalID != "p-0001-ready" {
		t.Fatalf("accepted status = %+v, want accepted p-0001-ready", accepted.Status)
	}
	active, err := layout.Active()
	if err != nil {
		t.Fatalf("Active: %v", err)
	}
	if active != "h-0002" {
		t.Fatalf("active = %q, want h-0002", active)
	}
	pinned, err := layout.Pinned()
	if err != nil {
		t.Fatalf("Pinned: %v", err)
	}
	if len(pinned) != 1 || pinned[0] != "h-0002" {
		t.Fatalf("pinned = %v, want h-0002", pinned)
	}
	if _, err := AcceptProposal(ProposalAcceptOptions{Dir: proposalDir, HarnessRoot: layout.Root}); err == nil || !strings.Contains(err.Error(), "already accepted") {
		t.Fatalf("repeat AcceptProposal err = %v, want already accepted", err)
	}
}

func TestAcceptProposalRejectsDraftManifest(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "p-0001-draft")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeProposalManifest(t, filepath.Join(dir, "manifest.json"), ProposalManifest{ProposalID: "p-0001-draft", BaseSnapshot: "h-0001"})
	if _, err := AcceptProposal(ProposalAcceptOptions{Dir: dir, HarnessRoot: filepath.Join(t.TempDir(), harness.RootDir)}); err == nil || !strings.Contains(err.Error(), "proposal is not ready") {
		t.Fatalf("AcceptProposal draft err = %v, want not ready", err)
	}
}

func TestRejectProposalRequiresReasonAndBlocksRepeatTransitions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "p-0001-draft")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeProposalManifest(t, filepath.Join(dir, "manifest.json"), ProposalManifest{ProposalID: "p-0001-draft", BaseSnapshot: "h-0001"})
	if _, err := RejectProposal(ProposalRejectOptions{Dir: dir}); err == nil || !strings.Contains(err.Error(), "reason is required") {
		t.Fatalf("RejectProposal empty reason err = %v, want reason required", err)
	}
	rejected, err := RejectProposal(ProposalRejectOptions{
		Dir: dir, Reason: "cache risk too high",
		Now: func() time.Time { return time.Unix(20, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("RejectProposal: %v", err)
	}
	if rejected.Status.State != ProposalStateRejected || rejected.Status.Reason != "cache risk too high" {
		t.Fatalf("rejected status = %+v, want rejected reason", rejected.Status)
	}
	if _, err := RejectProposal(ProposalRejectOptions{Dir: dir, Reason: "again"}); err == nil || !strings.Contains(err.Error(), "already rejected") {
		t.Fatalf("repeat RejectProposal err = %v, want already rejected", err)
	}
	if _, err := AcceptProposal(ProposalAcceptOptions{Dir: dir}); err == nil || !strings.Contains(err.Error(), "already rejected") {
		t.Fatalf("AcceptProposal rejected err = %v, want already rejected", err)
	}
}

func completeProposalManifest() ProposalManifest {
	return ProposalManifest{
		ProposalID:        "p-0001-post-success-guard",
		BaseSnapshot:      "h-0001",
		TargetSnapshot:    "h-0002",
		ComponentsChanged: []string{"middleware/post_success_guard.toml", "tool_descriptions/bash.md"},
		Evidence:          []string{"canary/post-success-verification-001 failed after partial test pass"},
		RootCause:         "Agent finalized after narrow success signal without running verifier-equivalent command.",
		ExpectedFixes:     []string{"canary/post-success-verification-001"},
		RegressionRisks:   []string{"Tasks with expensive full verification may become slower."},
		CacheRisk: &ProposalCacheRisk{
			StablePrefixChanged:   true,
			ExpectedHitRatioDelta: -0.01,
		},
		AcceptanceRules: &ProposalAcceptanceRules{
			MinSmokePassRate:      0.8,
			MinCanaryPassRate:     0.8,
			MinCacheHitRatio:      0.9,
			MaxContractViolations: 0,
		},
		RollbackRule: "Revert if canary pass rate drops or cache_hit_ratio < 0.90.",
	}
}

func writeReadyProposal(t *testing.T, dir, proposalID, targetSnapshot string) ProposalManifest {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := completeProposalManifest()
	manifest.ProposalID = proposalID
	manifest.TargetSnapshot = targetSnapshot
	writeProposalManifest(t, filepath.Join(dir, "manifest.json"), manifest)
	return manifest
}

func writeProposalManifest(t *testing.T, path string, manifest ProposalManifest) {
	t.Helper()
	if err := writeJSON(path, manifest); err != nil {
		t.Fatal(err)
	}
}

func stringListContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
