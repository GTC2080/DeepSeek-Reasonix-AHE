package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// DefaultTaskSystemPrompt steers a sub-agent toward focused, terse delivery —
// it doesn't see the parent's conversation so it must self-contain.
const DefaultTaskSystemPrompt = `You are a sub-agent invoked by a parent coding agent to carry out one focused task.
Use the provided tools to investigate or act. Return a single final answer that is concise
and self-contained — the parent will see only that answer, not your tool calls or reasoning.
If you need to ask for clarification, fail with a precise question instead of guessing.`

// TaskTool spawns a sub-agent in its own session for a focused sub-task. The
// sub-agent runs with a filtered tool whitelist, a lower default step cap,
// and a discard-output writer — only its final assistant message is returned
// to the parent. Use cases: keep noisy tool sequences (multi-file exploration,
// repeated grep / read_file) out of the parent's context budget, or parallel
// research across independent areas (the parallel-dispatch path picks these up
// only when readOnly, which task is not).
type TaskTool struct {
	prov          provider.Provider
	pricing       *provider.Pricing
	parentReg     *tool.Registry
	maxSteps      int
	contextWindow int
	temperature   float64
	archiveDir    string
	sysPrompt     string
	gate          Gate
}

// NewTaskTool wires a task tool to the parent agent's environment so its
// sub-agents can use the same provider and tools. sysPrompt is the system
// prompt every sub-agent starts with; pass "" for DefaultTaskSystemPrompt. gate
// is the permission gate sub-agents inherit — pass the headless variant so
// deny rules still bite while autonomous sub-agents are never blocked on an
// interactive prompt (there is no UI to answer one).
func NewTaskTool(prov provider.Provider, pricing *provider.Pricing, parentReg *tool.Registry,
	maxSteps, contextWindow int, temperature float64, archiveDir, sysPrompt string, gate Gate) *TaskTool {
	if sysPrompt == "" {
		sysPrompt = DefaultTaskSystemPrompt
	}
	return &TaskTool{
		prov:          prov,
		pricing:       pricing,
		parentReg:     parentReg,
		maxSteps:      maxSteps,
		contextWindow: contextWindow,
		temperature:   temperature,
		archiveDir:    archiveDir,
		sysPrompt:     sysPrompt,
		gate:          gate,
	}
}

func (t *TaskTool) Name() string { return "task" }

func (t *TaskTool) Description() string {
	return "Spawn a sub-agent for a focused sub-task. The sub-agent runs in its own session with the same provider and a filtered tool list (defaults to every parent tool except 'task' — no recursive nesting). Only its final answer is returned. Use this to (a) keep long exploration sequences out of the parent's context budget, or (b) delegate self-contained work like 'find every place that calls X and summarise the patterns'."
}

func (t *TaskTool) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"properties":{
  "prompt":{"type":"string","description":"What the sub-agent should accomplish. Be specific about the deliverable — the sub-agent does not see this conversation."},
  "description":{"type":"string","description":"Short label for the sub-task (3-7 words). Surfaced in the dispatch line so the user sees what's running."},
  "tools":{"type":"array","items":{"type":"string"},"description":"Optional tool whitelist. Defaults to every parent tool except 'task'."},
  "max_steps":{"type":"integer","description":"Optional cap on tool-call rounds. Defaults to half the parent's cap (min 5).","minimum":1}
},
"required":["prompt"]
}`)
}

// ReadOnly is false: a sub-agent can invoke any whitelisted tool, including
// writers. Conservative classification keeps the parallel-dispatch path from
// running two sub-agents at once and letting their writes race.
func (t *TaskTool) ReadOnly() bool { return false }

func (t *TaskTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Prompt      string   `json:"prompt"`
		Description string   `json:"description"`
		Tools       []string `json:"tools"`
		MaxSteps    int      `json:"max_steps"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if p.Prompt == "" {
		return "", fmt.Errorf("prompt is required")
	}

	maxSteps := p.MaxSteps
	if maxSteps <= 0 {
		maxSteps = t.maxSteps / 2
		if maxSteps < 5 {
			maxSteps = 5
		}
	}

	subReg := tool.NewRegistry()
	if len(p.Tools) > 0 {
		for _, name := range p.Tools {
			if name == t.Name() {
				continue // no recursive nesting
			}
			if tl, ok := t.parentReg.Get(name); ok {
				subReg.Add(tl)
			}
		}
	} else {
		for _, name := range t.parentReg.Names() {
			if name == t.Name() {
				continue
			}
			if tl, ok := t.parentReg.Get(name); ok {
				subReg.Add(tl)
			}
		}
	}

	// Sub-agent runs silently — its noise (tool dispatch lines, per-turn
	// usage, reasoning) would clutter the parent UI without buying anything,
	// since only the final answer surfaces to the caller anyway.
	subSession := NewSession(t.sysPrompt)
	subAgent := New(t.prov, subReg, subSession, Options{
		MaxSteps:      maxSteps,
		Temperature:   t.temperature,
		Pricing:       t.pricing,
		Gate:          t.gate,
		ContextWindow: t.contextWindow,
		ArchiveDir:    t.archiveDir,
	}, io.Discard)

	if err := subAgent.Run(ctx, p.Prompt); err != nil {
		return "", fmt.Errorf("sub-agent: %w", err)
	}

	// Walk the session backwards for the last assistant message with content —
	// that's the sub-agent's final answer. Intermediate assistant messages
	// with tool_calls but no text don't count.
	for i := len(subSession.Messages) - 1; i >= 0; i-- {
		m := subSession.Messages[i]
		if m.Role == provider.RoleAssistant && strings.TrimSpace(m.Content) != "" {
			return m.Content, nil
		}
	}
	return "", fmt.Errorf("sub-agent finished without producing a final answer")
}
