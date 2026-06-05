package agent

import (
	"context"
	"errors"
	"testing"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/event"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

func TestModelRequestAndResponseEvents(t *testing.T) {
	usage := &provider.Usage{
		PromptTokens:     10,
		CompletionTokens: 5,
		TotalTokens:      15,
		CacheHitTokens:   7,
		CacheMissTokens:  3,
		FinishReason:     "tool_calls",
	}
	mp := testutil.NewMock("trace-model", testutil.Turn{
		ToolCalls: []provider.ToolCall{{ID: "c1", Name: "echo", Arguments: `{"text":"hi"}`}},
		Usage:     usage,
	})
	var got []event.Event
	sink := event.FuncSink(func(e event.Event) { got = append(got, e) })
	a := New(mp, echoRegistry(), NewSession("sys"), Options{MaxSteps: 1, Temperature: 0.25}, sink)

	_ = a.Run(context.Background(), "go")

	req := firstAgentEvent(t, got, event.ModelRequest)
	if req.Model.Provider != "trace-model" {
		t.Fatalf("provider = %q, want trace-model", req.Model.Provider)
	}
	if req.Model.MessageCount != 2 {
		t.Fatalf("message count = %d, want system+user", req.Model.MessageCount)
	}
	if req.Model.ToolCount != len(echoRegistry().Schemas()) {
		t.Fatalf("tool count = %d", req.Model.ToolCount)
	}
	if req.Model.Temperature != 0.25 {
		t.Fatalf("temperature = %v", req.Model.Temperature)
	}

	resp := firstAgentEvent(t, got, event.ModelResponse)
	if resp.Model.Usage != usage {
		t.Fatalf("usage not carried on model response")
	}
	if resp.Model.FinishReason != "tool_calls" {
		t.Fatalf("finish reason = %q", resp.Model.FinishReason)
	}
	if resp.Model.ToolCallCount != 1 {
		t.Fatalf("tool call count = %d, want 1", resp.Model.ToolCallCount)
	}
}

func TestModelResponseEventOnStreamError(t *testing.T) {
	wantErr := errors.New("network down")
	mp := testutil.NewMock("trace-model", testutil.ErrorTurn(wantErr))
	var got []event.Event
	sink := event.FuncSink(func(e event.Event) { got = append(got, e) })
	a := New(mp, tool.NewRegistry(), NewSession(""), Options{}, sink)

	err := a.Run(context.Background(), "go")
	if !errors.Is(err, wantErr) {
		t.Fatalf("Run error = %v, want %v", err, wantErr)
	}

	resp := firstAgentEvent(t, got, event.ModelResponse)
	if resp.Model.Err != wantErr.Error() {
		t.Fatalf("model error = %q, want %q", resp.Model.Err, wantErr.Error())
	}
}

func firstAgentEvent(t *testing.T, events []event.Event, kind event.Kind) event.Event {
	t.Helper()
	for _, e := range events {
		if e.Kind == kind {
			return e
		}
	}
	t.Fatalf("missing event kind %v", kind)
	return event.Event{}
}
