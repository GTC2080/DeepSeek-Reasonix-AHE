package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"reasonix/internal/lab"
)

func TestRunDispatchesLabProposalCreateAndCheck(t *testing.T) {
	dir := tempChdir(t)

	out := captureStdout(t, func() {
		if rc := Run([]string{"lab", "proposal", "create", "--base", "h-0001", "--name", "post-success-guard"}, "test-version"); rc != 0 {
			t.Fatalf("lab proposal create rc = %d, want 0", rc)
		}
	})
	if !strings.Contains(out, "proposal\t") || !strings.Contains(out, "p-0001-post-success-guard") {
		t.Fatalf("proposal create output = %q, want proposal path", out)
	}
	proposalDir := filepath.Join(dir, ".reasonix-ahe", "proposals", "p-0001-post-success-guard")
	if _, err := os.Stat(filepath.Join(proposalDir, "manifest.json")); err != nil {
		t.Fatalf("manifest missing: %v", err)
	}

	if rc := Run([]string{"lab", "proposal", "check", proposalDir}, "test-version"); rc != 1 {
		t.Fatalf("draft proposal check rc = %d, want 1", rc)
	}

	writeCLIJSON(t, filepath.Join(proposalDir, "manifest.json"), completeCLIProposalManifest())
	out = captureStdout(t, func() {
		if rc := Run([]string{"lab", "proposal", "check", proposalDir}, "test-version"); rc != 0 {
			t.Fatalf("complete proposal check rc = %d, want 0", rc)
		}
	})
	if !strings.Contains(out, "ok\t") || !strings.Contains(out, "p-0001-post-success-guard") {
		t.Fatalf("proposal check output = %q, want ok with id", out)
	}
}

func TestRunDispatchesLabProposalStatusAcceptAndReject(t *testing.T) {
	dir := tempChdir(t)
	createTwoCLIHarnessSnapshots(t)
	readyDir := filepath.Join(dir, ".reasonix-ahe", "proposals", "p-0001-ready")
	writeCLIJSON(t, filepath.Join(readyDir, "manifest.json"), completeCLIProposalManifest())

	out := captureStdout(t, func() {
		if rc := Run([]string{"lab", "proposal", "status", readyDir}, "test-version"); rc != 0 {
			t.Fatalf("proposal status rc = %d, want 0", rc)
		}
	})
	if !strings.Contains(out, "status\tp-0001-post-success-guard\tready") {
		t.Fatalf("proposal status output = %q, want ready", out)
	}

	out = captureStdout(t, func() {
		if rc := Run([]string{"lab", "proposal", "accept", readyDir, "--activate", "--pin-target"}, "test-version"); rc != 0 {
			t.Fatalf("proposal accept rc = %d, want 0", rc)
		}
	})
	if !strings.Contains(out, "accepted\tp-0001-post-success-guard") {
		t.Fatalf("proposal accept output = %q, want accepted", out)
	}
	active, err := os.ReadFile(filepath.Join(dir, ".reasonix-harness", "active"))
	if err != nil {
		t.Fatalf("active missing after accept: %v", err)
	}
	if strings.TrimSpace(string(active)) != "h-0002" {
		t.Fatalf("active = %q, want h-0002", active)
	}
	pinned, err := os.ReadFile(filepath.Join(dir, ".reasonix-harness", "pinned"))
	if err != nil {
		t.Fatalf("pinned missing after accept: %v", err)
	}
	if !strings.Contains(string(pinned), "h-0002") {
		t.Fatalf("pinned = %q, want h-0002", pinned)
	}

	rejectDir := filepath.Join(dir, ".reasonix-ahe", "proposals", "p-0002-reject")
	manifest := completeCLIProposalManifest()
	manifest.ProposalID = "p-0002-reject"
	writeCLIJSON(t, filepath.Join(rejectDir, "manifest.json"), manifest)
	out = captureStdout(t, func() {
		if rc := Run([]string{"lab", "proposal", "reject", rejectDir, "--reason", "cache risk"}, "test-version"); rc != 0 {
			t.Fatalf("proposal reject rc = %d, want 0", rc)
		}
	})
	if !strings.Contains(out, "rejected\tp-0002-reject") {
		t.Fatalf("proposal reject output = %q, want rejected", out)
	}
}

func TestLabProposalRejectsBadArguments(t *testing.T) {
	tempChdir(t)
	for _, args := range [][]string{
		{"lab", "proposal"},
		{"lab", "proposal", "unknown"},
		{"lab", "proposal", "create"},
		{"lab", "proposal", "create", "--base", "h-0001"},
		{"lab", "proposal", "create", "--name", "x"},
		{"lab", "proposal", "check"},
		{"lab", "proposal", "check", "one", "two"},
		{"lab", "proposal", "status"},
		{"lab", "proposal", "status", "one", "two"},
		{"lab", "proposal", "accept"},
		{"lab", "proposal", "accept", "dir", "--unknown"},
		{"lab", "proposal", "reject"},
		{"lab", "proposal", "reject", "dir"},
		{"lab", "proposal", "reject", "dir", "--reason"},
		{"lab", "proposal", "reject", "dir", "--unknown", "x"},
	} {
		if rc := Run(args, "test-version"); rc != 2 {
			t.Fatalf("Run(%v) rc = %d, want 2", args, rc)
		}
	}
}

func TestLabProposalCheckMissingManifestReturnsFailure(t *testing.T) {
	dir := tempChdir(t)
	proposalDir := filepath.Join(dir, ".reasonix-ahe", "proposals", "p-0001-missing")
	if err := os.MkdirAll(proposalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if rc := Run([]string{"lab", "proposal", "check", proposalDir}, "test-version"); rc != 1 {
		t.Fatalf("missing manifest check rc = %d, want 1", rc)
	}
}

func TestHelpMentionsLabProposal(t *testing.T) {
	out := captureStdout(t, func() {
		if rc := Run([]string{"help"}, "test-version"); rc != 0 {
			t.Fatalf("help rc = %d, want 0", rc)
		}
	})
	if !strings.Contains(out, "proposal") {
		t.Fatalf("help output should mention proposal:\n%s", out)
	}
}

func completeCLIProposalManifest() lab.ProposalManifest {
	return lab.ProposalManifest{
		ProposalID:        "p-0001-post-success-guard",
		BaseSnapshot:      "h-0001",
		TargetSnapshot:    "h-0002",
		ComponentsChanged: []string{"middleware/post_success_guard.toml"},
		Evidence:          []string{"task-python-bugfix-001 verifier failed"},
		RootCause:         "Agent finalized after a narrow success signal.",
		ExpectedFixes:     []string{"python-bugfix-001"},
		RegressionRisks:   []string{"Full verification may be slower."},
		CacheRisk: &lab.ProposalCacheRisk{
			StablePrefixChanged:   true,
			ExpectedHitRatioDelta: -0.01,
		},
		AcceptanceRules: &lab.ProposalAcceptanceRules{
			MinSmokePassRate:      0.8,
			MinCanaryPassRate:     0.8,
			MinCacheHitRatio:      0.9,
			MaxContractViolations: 0,
		},
		RollbackRule: "Revert if canary pass rate drops or cache_hit_ratio < 0.90.",
	}
}

func createTwoCLIHarnessSnapshots(t *testing.T) {
	t.Helper()
	if rc := Run([]string{"lab", "harness", "snapshot", "create"}, "test-version"); rc != 0 {
		t.Fatalf("snapshot create h-0001 rc = %d, want 0", rc)
	}
	if rc := Run([]string{"lab", "harness", "snapshot", "create"}, "test-version"); rc != 0 {
		t.Fatalf("snapshot create h-0002 rc = %d, want 0", rc)
	}
}
