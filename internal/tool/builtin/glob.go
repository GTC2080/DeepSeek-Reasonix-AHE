package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"reasonix/internal/tool"
)

func init() { tool.RegisterBuiltin(globTool{}) }

type globTool struct{}

func (globTool) Name() string { return "glob" }

func (globTool) Description() string {
	return "Find files matching a glob pattern (e.g. \"*.go\", \"internal/*/*.go\"). Supports the shell metacharacters * ? [ ]; does not support ** — use bash find for recursive matching."
}

func (globTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string","description":"Glob pattern"}},"required":["pattern"]}`)
}

func (globTool) ReadOnly() bool { return true }

func (globTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Pattern string `json:"pattern"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if p.Pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}

	matches, err := filepath.Glob(p.Pattern)
	if err != nil {
		return "", fmt.Errorf("glob %q: %w", p.Pattern, err)
	}
	if len(matches) == 0 {
		return "(no matches)", nil
	}
	return strings.Join(matches, "\n"), nil
}
