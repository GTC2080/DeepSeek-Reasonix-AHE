package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"reasonix/internal/event"
	"reasonix/internal/harnesspolicy"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

func TestHarnessPolicyConfiguresFinalReadinessBlockLimit(t *testing.T) {
	todoWrite, ok := tool.LookupBuiltin("todo_write")
	if !ok {
		t.Fatal("todo_write builtin not registered")
	}
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "write_file", readOnly: false})
	reg.Add(todoWrite)
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{
			toolCallChunk("c1", "write_file", `{"path":"changed.go","content":"package main"}`),
			toolCallChunk("c2", "todo_write", `{"todos":[{"content":"Edit code","status":"in_progress"}]}`),
			{Type: provider.ChunkDone},
		},
		{{Type: provider.ChunkText, Text: "premature"}, {Type: provider.ChunkDone}},
	}}
	policies := harnesspolicy.PolicySet{Policies: []harnesspolicy.Policy{{
		Version:              harnesspolicy.Version,
		ID:                   harnesspolicy.PolicyFinalAnswerReadiness,
		Enabled:              true,
		Stage:                harnesspolicy.StageFinalAnswer,
		Action:               harnesspolicy.ActionBlockAndNudge,
		MaxFinalAnswerBlocks: 1,
	}}}
	a := New(prov, reg, NewSession(""), Options{Policies: policies}, event.Discard)

	err := a.Run(context.Background(), "edit with todo and never sign off")
	if err == nil {
		t.Fatal("expected readiness policy to stop the run")
	}
	if !strings.Contains(err.Error(), "final-answer readiness") {
		t.Fatalf("error = %v, want final-answer readiness", err)
	}
	if prov.call != 2 {
		t.Fatalf("provider calls = %d, want one blocked final answer after writer turn", prov.call)
	}
}

func TestHarnessPolicyEmitsDecisionWhenFinalReadinessBlocks(t *testing.T) {
	todoWrite, ok := tool.LookupBuiltin("todo_write")
	if !ok {
		t.Fatal("todo_write builtin not registered")
	}
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "write_file", readOnly: false})
	reg.Add(todoWrite)
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{
			toolCallChunk("c1", "write_file", `{"path":"changed.go","content":"package main"}`),
			toolCallChunk("c2", "todo_write", `{"todos":[{"content":"Edit code","status":"in_progress"}]}`),
			{Type: provider.ChunkDone},
		},
		{{Type: provider.ChunkText, Text: "premature"}, {Type: provider.ChunkDone}},
	}}
	policies := harnesspolicy.PolicySet{Policies: []harnesspolicy.Policy{{
		Version:              harnesspolicy.Version,
		ID:                   harnesspolicy.PolicyFinalAnswerReadiness,
		Enabled:              true,
		Stage:                harnesspolicy.StageFinalAnswer,
		Action:               harnesspolicy.ActionBlockAndNudge,
		MaxFinalAnswerBlocks: 1,
	}}}
	var decisions []event.MiddlewarePolicyDecisionPayload
	sink := event.FuncSink(func(e event.Event) {
		if e.Kind == event.MiddlewarePolicyDecision {
			decisions = append(decisions, e.MiddlewarePolicyDecision)
		}
	})
	a := New(prov, reg, NewSession(""), Options{
		Policies:        policies,
		HarnessSnapshot: "h-0001",
	}, sink)

	_ = a.Run(context.Background(), "edit with todo and never sign off")
	if len(decisions) != 1 {
		t.Fatalf("policy decisions = %d, want 1", len(decisions))
	}
	decision := decisions[0]
	if decision.HarnessSnapshot != "h-0001" || decision.PolicyID != harnesspolicy.PolicyFinalAnswerReadiness || decision.Stage != string(harnesspolicy.StageFinalAnswer) {
		t.Fatalf("decision = %+v", decision)
	}
	if !strings.Contains(decision.Reason, "todo_write") {
		t.Fatalf("decision reason = %q, want todo readiness reason", decision.Reason)
	}
}

func TestHarnessPolicyConfiguresRepeatedWriterLoopGuardThreshold(t *testing.T) {
	var calls int32
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "write_file", readOnly: false, calls: &calls})
	args := `{"path":"prompt.txt","content":"hello"}`
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{toolCallChunk("c1", "write_file", args), {Type: provider.ChunkDone}},
		{toolCallChunk("c2", "write_file", args), {Type: provider.ChunkDone}},
		{{Type: provider.ChunkText, Text: "done"}, {Type: provider.ChunkDone}},
	}}
	policies := harnesspolicy.PolicySet{Policies: []harnesspolicy.Policy{{
		Version:                  harnesspolicy.Version,
		ID:                       harnesspolicy.PolicyToolErrorLoopGuard,
		Enabled:                  true,
		Stage:                    harnesspolicy.StagePostTool,
		Action:                   harnesspolicy.ActionBlockAndNudge,
		RepeatedSuccessThreshold: 1,
	}}}
	a := New(prov, reg, NewSession(""), Options{Policies: policies}, event.Discard)

	if err := a.Run(context.Background(), "write twice"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("writer executed %d times, want one execution before policy guard", got)
	}
	if last := lastToolResult(a.session, "write_file"); !strings.Contains(last, "[loop guard]") {
		t.Fatalf("second repeated writer should be blocked by policy guard, got %q", last)
	}
}

func TestPermissionRecoveryPolicyNudgesBlockedToolCall(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "bash", readOnly: false})
	policies := harnesspolicy.PolicySet{Policies: []harnesspolicy.Policy{{
		Version: harnesspolicy.Version,
		ID:      harnesspolicy.PolicyPermissionRecovery,
		Enabled: true,
		Stage:   harnesspolicy.StagePreTool,
		Action:  harnesspolicy.ActionNudge,
		Nudge:   "Use a read-only command, request approval, or explain the blocker.",
	}}}
	var decisions []event.MiddlewarePolicyDecisionPayload
	sink := event.FuncSink(func(e event.Event) {
		if e.Kind == event.MiddlewarePolicyDecision {
			decisions = append(decisions, e.MiddlewarePolicyDecision)
		}
	})
	a := New(nil, reg, NewSession(""), Options{
		Gate:            denyGate{reason: "permission policy denied"},
		Policies:        policies,
		HarnessSnapshot: "h-0001",
	}, sink)

	result := a.executeBatch(context.Background(), []provider.ToolCall{{ID: "c1", Name: "bash", Arguments: `{}`}})[0]
	if !strings.Contains(result, "permission policy denied") || !strings.Contains(result, "Use a read-only command") {
		t.Fatalf("blocked result missing recovery nudge: %q", result)
	}
	if len(decisions) != 1 || decisions[0].PolicyID != harnesspolicy.PolicyPermissionRecovery || decisions[0].ToolName != "bash" {
		t.Fatalf("decisions = %+v", decisions)
	}
}

type denyGate struct {
	reason string
	err    error
}

func (g denyGate) Check(context.Context, string, json.RawMessage, bool) (bool, string, error) {
	if g.err != nil {
		return false, g.reason, g.err
	}
	if g.reason == "" {
		return false, "denied", nil
	}
	return false, g.reason, nil
}

var _ Gate = denyGate{err: errors.New("unused")}

func TestTimeoutBudgetPolicyNudgesTimedOutBash(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "bash", readOnly: false, err: errors.New("command timed out (> 120s)")})
	policies := harnesspolicy.PolicySet{Policies: []harnesspolicy.Policy{{
		Version: harnesspolicy.Version,
		ID:      harnesspolicy.PolicyTimeoutBudget,
		Enabled: true,
		Stage:   harnesspolicy.StagePostTool,
		Action:  harnesspolicy.ActionNudge,
		Nudge:   "Use run_in_background for long-running commands or split the command.",
	}}}
	var decisions []event.MiddlewarePolicyDecisionPayload
	sink := event.FuncSink(func(e event.Event) {
		if e.Kind == event.MiddlewarePolicyDecision {
			decisions = append(decisions, e.MiddlewarePolicyDecision)
		}
	})
	a := New(nil, reg, NewSession(""), Options{
		Policies:        policies,
		HarnessSnapshot: "h-0001",
	}, sink)

	result := a.executeBatch(context.Background(), []provider.ToolCall{{ID: "c1", Name: "bash", Arguments: `{}`}})[0]
	if !strings.Contains(result, "command timed out") || !strings.Contains(result, "run_in_background") {
		t.Fatalf("timeout result missing budget nudge: %q", result)
	}
	if len(decisions) != 1 || decisions[0].PolicyID != harnesspolicy.PolicyTimeoutBudget || decisions[0].ToolName != "bash" {
		t.Fatalf("decisions = %+v", decisions)
	}
}
