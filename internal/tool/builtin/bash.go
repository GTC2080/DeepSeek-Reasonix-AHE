package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	"reasonix/internal/sandbox"
	"reasonix/internal/tool"
)

const bashTimeout = 120 * time.Second

func init() { tool.RegisterBuiltin(bash{}) }

// bash runs a shell command with a timeout to avoid hangs. sb, when it enforces,
// wraps the command in an OS sandbox; the zero value registered at init runs
// unconfined and is overridden per run by ConfineBash.
type bash struct{ sb sandbox.Spec }

func (bash) Name() string { return "bash" }

func (bash) Description() string {
	return "Execute a command in the shell and return combined stdout/stderr. Use for builds, tests, git, etc."
}

func (bash) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"command":{"type":"string","description":"Shell command to execute"}},"required":["command"]}`)
}

// ReadOnly is false: bash's effect cannot be inferred from args (rm, curl,
// git commit, etc. are all reachable). Conservative even when a particular
// command happens to be read-only — the agent batch decision can't tell.
func (bash) ReadOnly() bool { return false }

func (b bash) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if p.Command == "" {
		return "", fmt.Errorf("command is required")
	}

	ctx, cancel := context.WithTimeout(ctx, bashTimeout)
	defer cancel()

	// Wrap in the OS sandbox when configured; otherwise argv is just bash -c.
	argv, _ := sandbox.Command(b.sb, "bash", p.Command)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := buf.String()

	if ctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("command timed out (> %s)", bashTimeout)
	}
	if err != nil {
		// Non-zero exit: feed output and error back so the model can self-correct.
		return out, fmt.Errorf("command exited: %w", err)
	}
	return out, nil
}
