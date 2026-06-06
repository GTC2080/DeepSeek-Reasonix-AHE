package harnesspolicy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDirParsesEnabledMiddlewarePolicies(t *testing.T) {
	dir := t.TempDir()
	writePolicy(t, dir, "tool.toml", `
version = "middleware.v0.1"
id = "tool_error_loop_guard"
enabled = true
stage = "post_tool"
action = "block_and_nudge"
storm_failure_threshold = 4
repeated_success_threshold = 3
`)
	writePolicy(t, dir, "final.toml", `
version = "middleware.v0.1"
id = "final_answer_readiness"
enabled = true
stage = "final_answer"
action = "block_and_nudge"
max_final_answer_blocks = 2
`)

	set, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if got := set.FinalAnswerMaxBlocks(3); got != 2 {
		t.Fatalf("FinalAnswerMaxBlocks = %d, want 2", got)
	}
	storm, repeat := set.LoopGuardThresholds(3, 2)
	if storm != 4 || repeat != 3 {
		t.Fatalf("LoopGuardThresholds = %d/%d, want 4/3", storm, repeat)
	}
	p, ok := set.Enabled("final_answer_readiness")
	if !ok || p.Stage != StageFinalAnswer || p.Action != ActionBlockAndNudge {
		t.Fatalf("Enabled(final_answer_readiness) = %+v, %v", p, ok)
	}
}

func TestLoadDirIgnoresDisabledPoliciesForRuntimeDefaults(t *testing.T) {
	dir := t.TempDir()
	writePolicy(t, dir, "disabled.toml", `
version = "middleware.v0.1"
id = "final_answer_readiness"
enabled = false
stage = "final_answer"
action = "block_and_nudge"
max_final_answer_blocks = 1
`)

	set, err := LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if got := set.FinalAnswerMaxBlocks(3); got != 3 {
		t.Fatalf("disabled policy changed max blocks to %d", got)
	}
	if _, ok := set.Enabled("final_answer_readiness"); ok {
		t.Fatal("disabled policy should not be returned by Enabled")
	}
}

func TestLoadDirRejectsInvalidEnumsAndUnknownFields(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{
			name: "bad stage",
			body: `
version = "middleware.v0.1"
id = "x"
enabled = true
stage = "during_tool"
action = "warn"
`,
			want: "stage",
		},
		{
			name: "bad action",
			body: `
version = "middleware.v0.1"
id = "x"
enabled = true
stage = "pre_tool"
action = "silently_fix"
`,
			want: "action",
		},
		{
			name: "unknown field",
			body: `
version = "middleware.v0.1"
id = "x"
enabled = true
stage = "pre_tool"
action = "warn"
surprise = true
`,
			want: "unknown field",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writePolicy(t, dir, "bad.toml", tc.body)
			_, err := LoadDir(dir)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("LoadDir err = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestLoadDirMissingDirectoryIsEmptyPolicySet(t *testing.T) {
	set, err := LoadDir(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatalf("LoadDir missing: %v", err)
	}
	if len(set.Policies) != 0 {
		t.Fatalf("policies = %+v, want empty", set.Policies)
	}
}

func writePolicy(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.TrimSpace(body)+"\n"), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
}
