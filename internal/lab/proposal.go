package lab

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"reasonix/internal/harness"
)

const (
	DefaultProposalRoot = ".reasonix-ahe/proposals"
	ProposalStatusFile  = "status.json"
)

// ProposalState is the local manual-review state for one proposal.
type ProposalState string

const (
	ProposalStateDraft    ProposalState = "draft"
	ProposalStateReady    ProposalState = "ready"
	ProposalStateAccepted ProposalState = "accepted"
	ProposalStateRejected ProposalState = "rejected"
)

// ProposalManifest is the local contract for future harness evolution proposals.
type ProposalManifest struct {
	ProposalID        string                   `json:"proposal_id"`
	BaseSnapshot      string                   `json:"base_snapshot"`
	TargetSnapshot    string                   `json:"target_snapshot,omitempty"`
	ComponentsChanged []string                 `json:"components_changed,omitempty"`
	Evidence          []string                 `json:"evidence,omitempty"`
	RootCause         string                   `json:"root_cause,omitempty"`
	ExpectedFixes     []string                 `json:"expected_fixes,omitempty"`
	RegressionRisks   []string                 `json:"regression_risks,omitempty"`
	CacheRisk         *ProposalCacheRisk       `json:"cache_risk,omitempty"`
	AcceptanceRules   *ProposalAcceptanceRules `json:"acceptance_rules,omitempty"`
	RollbackRule      string                   `json:"rollback_rule,omitempty"`
}

type ProposalCacheRisk struct {
	StablePrefixChanged   bool    `json:"stable_prefix_changed"`
	ExpectedHitRatioDelta float64 `json:"expected_hit_ratio_delta"`
}

type ProposalAcceptanceRules struct {
	MinSmokePassRate      float64 `json:"min_smoke_pass_rate"`
	MinCanaryPassRate     float64 `json:"min_canary_pass_rate"`
	MinCacheHitRatio      float64 `json:"min_cache_hit_ratio"`
	MaxContractViolations int     `json:"max_contract_violations"`
}

type ProposalCreateOptions struct {
	Root         string
	BaseSnapshot string
	Name         string
}

type ProposalCreateResult struct {
	ProposalID   string           `json:"proposal_id"`
	Dir          string           `json:"dir"`
	ManifestPath string           `json:"manifest_path"`
	EvidencePath string           `json:"evidence_path"`
	DiffPath     string           `json:"diff_path"`
	Manifest     ProposalManifest `json:"manifest"`
}

type ProposalCheckResult struct {
	Dir      string           `json:"dir"`
	Manifest ProposalManifest `json:"manifest"`
	Errors   []string         `json:"errors,omitempty"`
}

// ProposalStatus is the sidecar status.json written after a human review action.
type ProposalStatus struct {
	ProposalID string        `json:"proposal_id"`
	State      ProposalState `json:"state"`
	UpdatedAt  time.Time     `json:"updated_at"`
	Reason     string        `json:"reason,omitempty"`
}

type ProposalStatusResult struct {
	Dir      string           `json:"dir"`
	Manifest ProposalManifest `json:"manifest"`
	Status   ProposalStatus   `json:"status"`
	Errors   []string         `json:"errors,omitempty"`
}

type ProposalAcceptOptions struct {
	Dir         string
	HarnessRoot string
	Activate    bool
	PinTarget   bool
	Now         func() time.Time
}

type ProposalRejectOptions struct {
	Dir    string
	Reason string
	Now    func() time.Time
}

// CreateProposal writes a local draft proposal scaffold.
func CreateProposal(opts ProposalCreateOptions) (ProposalCreateResult, error) {
	root := opts.Root
	if root == "" {
		root = DefaultProposalRoot
	}
	base := strings.TrimSpace(opts.BaseSnapshot)
	name := strings.ToLower(strings.Trim(safeName(opts.Name), "-."))
	if base == "" {
		return ProposalCreateResult{}, fmt.Errorf("base snapshot is required")
	}
	if name == "" {
		return ProposalCreateResult{}, fmt.Errorf("proposal name is required")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return ProposalCreateResult{}, fmt.Errorf("create proposal root: %w", err)
	}
	proposalID := fmt.Sprintf("p-%04d-%s", nextProposalNumber(root), name)
	dir := filepath.Join(root, proposalID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ProposalCreateResult{}, fmt.Errorf("create proposal dir: %w", err)
	}
	manifest := ProposalManifest{ProposalID: proposalID, BaseSnapshot: base}
	manifestPath := filepath.Join(dir, "manifest.json")
	evidencePath := filepath.Join(dir, "evidence.md")
	diffPath := filepath.Join(dir, "diff.patch")
	if err := writeJSON(manifestPath, manifest); err != nil {
		return ProposalCreateResult{}, err
	}
	if err := os.WriteFile(evidencePath, []byte("# Evidence\n\nAdd distilled evidence links or summaries here.\n"), 0o644); err != nil {
		return ProposalCreateResult{}, fmt.Errorf("write evidence: %w", err)
	}
	if err := os.WriteFile(diffPath, nil, 0o644); err != nil {
		return ProposalCreateResult{}, fmt.Errorf("write diff: %w", err)
	}
	return ProposalCreateResult{
		ProposalID: proposalID, Dir: dir, ManifestPath: manifestPath,
		EvidencePath: evidencePath, DiffPath: diffPath, Manifest: manifest,
	}, nil
}

// CheckProposal reads and validates a proposal manifest.
func CheckProposal(dir string) (ProposalCheckResult, error) {
	var manifest ProposalManifest
	manifestPath := filepath.Join(dir, "manifest.json")
	if err := readJSONFile(manifestPath, &manifest); err != nil {
		return ProposalCheckResult{}, err
	}
	return ProposalCheckResult{Dir: dir, Manifest: manifest, Errors: ValidateProposalManifest(manifest)}, nil
}

// ReadProposalStatus returns the persisted proposal lifecycle status or derives
// draft/ready from manifest validation when no status sidecar exists.
func ReadProposalStatus(dir string) (ProposalStatusResult, error) {
	check, err := CheckProposal(dir)
	if err != nil {
		return ProposalStatusResult{}, err
	}
	statusPath := filepath.Join(dir, ProposalStatusFile)
	var status ProposalStatus
	if err := readJSONFile(statusPath, &status); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return ProposalStatusResult{}, err
		}
		status = ProposalStatus{ProposalID: check.Manifest.ProposalID, State: derivedProposalState(check.Errors)}
		return ProposalStatusResult{Dir: dir, Manifest: check.Manifest, Status: status, Errors: check.Errors}, nil
	}
	if status.ProposalID == "" {
		status.ProposalID = check.Manifest.ProposalID
	}
	if status.ProposalID != check.Manifest.ProposalID {
		return ProposalStatusResult{}, fmt.Errorf("proposal status id %q does not match manifest id %q", status.ProposalID, check.Manifest.ProposalID)
	}
	if !validProposalState(status.State) {
		return ProposalStatusResult{}, fmt.Errorf("proposal status state %q is invalid", status.State)
	}
	return ProposalStatusResult{Dir: dir, Manifest: check.Manifest, Status: status, Errors: check.Errors}, nil
}

// AcceptProposal records an accepted status for a ready proposal and optionally
// activates or pins its target harness snapshot.
func AcceptProposal(opts ProposalAcceptOptions) (ProposalStatusResult, error) {
	status, err := ReadProposalStatus(opts.Dir)
	if err != nil {
		return ProposalStatusResult{}, err
	}
	if err := ensureTransitionAllowed(status.Status.State); err != nil {
		return ProposalStatusResult{}, err
	}
	if len(status.Errors) > 0 {
		return ProposalStatusResult{}, fmt.Errorf("proposal is not ready: %s", strings.Join(status.Errors, "; "))
	}
	layout := harness.NewLayout(opts.HarnessRoot)
	if _, err := layout.Inspect(status.Manifest.TargetSnapshot); err != nil {
		return ProposalStatusResult{}, err
	}
	if opts.Activate {
		if err := layout.Activate(status.Manifest.TargetSnapshot); err != nil {
			return ProposalStatusResult{}, err
		}
	}
	if opts.PinTarget {
		if err := layout.Pin(status.Manifest.TargetSnapshot); err != nil {
			return ProposalStatusResult{}, err
		}
	}
	status.Status = ProposalStatus{
		ProposalID: status.Manifest.ProposalID,
		State:      ProposalStateAccepted,
		UpdatedAt:  proposalNow(opts.Now),
	}
	if err := writeProposalStatus(opts.Dir, status.Status); err != nil {
		return ProposalStatusResult{}, err
	}
	return status, nil
}

// RejectProposal records a rejected status for a draft or ready proposal.
func RejectProposal(opts ProposalRejectOptions) (ProposalStatusResult, error) {
	reason := strings.TrimSpace(opts.Reason)
	if reason == "" {
		return ProposalStatusResult{}, fmt.Errorf("reason is required")
	}
	status, err := ReadProposalStatus(opts.Dir)
	if err != nil {
		return ProposalStatusResult{}, err
	}
	if err := ensureTransitionAllowed(status.Status.State); err != nil {
		return ProposalStatusResult{}, err
	}
	status.Status = ProposalStatus{
		ProposalID: status.Manifest.ProposalID,
		State:      ProposalStateRejected,
		UpdatedAt:  proposalNow(opts.Now),
		Reason:     reason,
	}
	if err := writeProposalStatus(opts.Dir, status.Status); err != nil {
		return ProposalStatusResult{}, err
	}
	return status, nil
}

func ValidateProposalManifest(m ProposalManifest) []string {
	var errors []string
	requireString(&errors, "proposal_id", m.ProposalID)
	requireString(&errors, "base_snapshot", m.BaseSnapshot)
	requireString(&errors, "target_snapshot", m.TargetSnapshot)
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

func derivedProposalState(errors []string) ProposalState {
	if len(errors) > 0 {
		return ProposalStateDraft
	}
	return ProposalStateReady
}

func validProposalState(state ProposalState) bool {
	switch state {
	case ProposalStateDraft, ProposalStateReady, ProposalStateAccepted, ProposalStateRejected:
		return true
	default:
		return false
	}
}

func ensureTransitionAllowed(state ProposalState) error {
	switch state {
	case ProposalStateAccepted:
		return fmt.Errorf("proposal already accepted")
	case ProposalStateRejected:
		return fmt.Errorf("proposal already rejected")
	}
	return nil
}

func proposalNow(now func() time.Time) time.Time {
	if now == nil {
		return time.Now().UTC()
	}
	return now().UTC()
}

func writeProposalStatus(dir string, status ProposalStatus) error {
	return writeJSON(filepath.Join(dir, ProposalStatusFile), status)
}

func nextProposalNumber(root string) int {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 1
	}
	var nums []int
	re := regexp.MustCompile(`^p-(\d{4})-`)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		match := re.FindStringSubmatch(entry.Name())
		if len(match) != 2 {
			continue
		}
		n, err := strconv.Atoi(match[1])
		if err == nil {
			nums = append(nums, n)
		}
	}
	if len(nums) == 0 {
		return 1
	}
	sort.Ints(nums)
	return nums[len(nums)-1] + 1
}

func requireString(errors *[]string, name, value string) {
	if strings.TrimSpace(value) == "" {
		*errors = append(*errors, name+" is required")
	}
}

func requireNonEmptyStrings(errors *[]string, name string, values []string) {
	if len(values) == 0 {
		*errors = append(*errors, name+" must contain at least one item")
		return
	}
	for i, value := range values {
		if strings.TrimSpace(value) == "" {
			*errors = append(*errors, fmt.Sprintf("%s[%d] is required", name, i))
		}
	}
}

func requireRatio(errors *[]string, name string, value float64) {
	if value < 0 || value > 1 {
		*errors = append(*errors, name+" must be between 0 and 1")
	}
}
