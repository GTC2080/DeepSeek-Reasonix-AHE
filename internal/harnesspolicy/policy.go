// Package harnesspolicy parses executable Reasonix-AHE middleware policies.
package harnesspolicy

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

const Version = "middleware.v0.1"

type Stage string

const (
	StageFinalAnswer   Stage = "final_answer"
	StagePreTool       Stage = "pre_tool"
	StagePostTool      Stage = "post_tool"
	StageCacheContract Stage = "cache_contract"
)

type Action string

const (
	ActionObserve       Action = "observe"
	ActionWarn          Action = "warn"
	ActionNudge         Action = "nudge"
	ActionBlockAndNudge Action = "block_and_nudge"
)

const (
	PolicyFinalAnswerReadiness = "final_answer_readiness"
	PolicyToolErrorLoopGuard   = "tool_error_loop_guard"
	PolicyPermissionRecovery   = "permission_recovery"
	PolicyTimeoutBudget        = "timeout_budget"
	PolicyCacheContractGuard   = "cache_contract_guard"
)

// Policy is one middleware policy loaded from .reasonix-harness/source/middleware.
type Policy struct {
	Version string `toml:"version" json:"version"`
	ID      string `toml:"id" json:"id"`
	Enabled bool   `toml:"enabled" json:"enabled"`
	Stage   Stage  `toml:"stage" json:"stage"`
	Action  Action `toml:"action" json:"action"`

	MaxFinalAnswerBlocks     int    `toml:"max_final_answer_blocks,omitempty" json:"max_final_answer_blocks,omitempty"`
	StormFailureThreshold    int    `toml:"storm_failure_threshold,omitempty" json:"storm_failure_threshold,omitempty"`
	RepeatedSuccessThreshold int    `toml:"repeated_success_threshold,omitempty" json:"repeated_success_threshold,omitempty"`
	Nudge                    string `toml:"nudge,omitempty" json:"nudge,omitempty"`

	Source string `toml:"-" json:"source,omitempty"`
}

// PolicySet is the active session's executable middleware. Disabled policies are
// retained for observability but ignored by runtime lookup helpers.
type PolicySet struct {
	Policies []Policy `json:"policies"`
}

// LoadDir parses every *.toml middleware policy in root. A missing directory is
// a valid empty policy set.
func LoadDir(root string) (PolicySet, error) {
	if strings.TrimSpace(root) == "" {
		return PolicySet{}, nil
	}
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return PolicySet{}, nil
	}
	if err != nil {
		return PolicySet{}, fmt.Errorf("read middleware policies %s: %w", root, err)
	}
	var files []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".toml") {
			continue
		}
		files = append(files, filepath.Join(root, entry.Name()))
	}
	sort.Strings(files)

	set := PolicySet{}
	seen := map[string]string{}
	for _, path := range files {
		policy, err := loadFile(path)
		if err != nil {
			return PolicySet{}, err
		}
		if previous, ok := seen[policy.ID]; ok {
			return PolicySet{}, fmt.Errorf("%s: duplicate middleware policy id %q also defined in %s", path, policy.ID, previous)
		}
		seen[policy.ID] = path
		set.Policies = append(set.Policies, policy)
	}
	return set, nil
}

func loadFile(path string) (Policy, error) {
	var policy Policy
	meta, err := toml.DecodeFile(path, &policy)
	if err != nil {
		return Policy{}, fmt.Errorf("%s: parse middleware policy: %w", path, err)
	}
	policy.Source = path
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		return Policy{}, fmt.Errorf("%s: unknown field %s", path, undecodedKey(undecoded[0]))
	}
	if !meta.IsDefined("enabled") {
		return Policy{}, fmt.Errorf("%s: enabled is required", path)
	}
	if err := Validate(policy); err != nil {
		return Policy{}, fmt.Errorf("%s: %w", path, err)
	}
	return policy, nil
}

// Validate checks the stable middleware.v0.1 contract.
func Validate(policy Policy) error {
	if strings.TrimSpace(policy.Version) != Version {
		return fmt.Errorf("version must be %q", Version)
	}
	if strings.TrimSpace(policy.ID) == "" {
		return fmt.Errorf("id is required")
	}
	if !validStage(policy.Stage) {
		return fmt.Errorf("stage %q is invalid", policy.Stage)
	}
	if !validAction(policy.Action) {
		return fmt.Errorf("action %q is invalid", policy.Action)
	}
	if policy.MaxFinalAnswerBlocks < 0 {
		return fmt.Errorf("max_final_answer_blocks must be >= 0")
	}
	if policy.StormFailureThreshold < 0 {
		return fmt.Errorf("storm_failure_threshold must be >= 0")
	}
	if policy.RepeatedSuccessThreshold < 0 {
		return fmt.Errorf("repeated_success_threshold must be >= 0")
	}
	return nil
}

func (s PolicySet) Enabled(id string) (Policy, bool) {
	for _, policy := range s.Policies {
		if policy.Enabled && policy.ID == id {
			return policy, true
		}
	}
	return Policy{}, false
}

func (s PolicySet) FinalAnswerMaxBlocks(defaultValue int) int {
	policy, ok := s.Enabled(PolicyFinalAnswerReadiness)
	if !ok || policy.MaxFinalAnswerBlocks <= 0 {
		return defaultValue
	}
	return policy.MaxFinalAnswerBlocks
}

func (s PolicySet) LoopGuardThresholds(defaultStorm, defaultRepeat int) (int, int) {
	policy, ok := s.Enabled(PolicyToolErrorLoopGuard)
	if !ok {
		return defaultStorm, defaultRepeat
	}
	storm := defaultStorm
	if policy.StormFailureThreshold > 0 {
		storm = policy.StormFailureThreshold
	}
	repeat := defaultRepeat
	if policy.RepeatedSuccessThreshold > 0 {
		repeat = policy.RepeatedSuccessThreshold
	}
	return storm, repeat
}

func validStage(stage Stage) bool {
	switch stage {
	case StageFinalAnswer, StagePreTool, StagePostTool, StageCacheContract:
		return true
	default:
		return false
	}
}

func validAction(action Action) bool {
	switch action {
	case ActionObserve, ActionWarn, ActionNudge, ActionBlockAndNudge:
		return true
	default:
		return false
	}
}

func undecodedKey(key toml.Key) string {
	return strings.Join([]string(key), ".")
}
