package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"reasonix/internal/agent/testutil"
	"reasonix/internal/event"
	"reasonix/internal/tool"
)

func TestRunEmitsCacheContractViolationWarningWithoutStopping(t *testing.T) {
	withActiveHarnessSnapshot(t, "h-0001")
	mp := testutil.NewMock("contract-model",
		testutil.Turn{Text: "first"},
		testutil.Turn{Text: "second"},
	)
	reg := tool.NewRegistry()
	reg.Add(contractTool{
		name:   "read_file",
		schema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
	})
	var events []event.Event
	sink := event.FuncSink(func(e event.Event) { events = append(events, e) })
	a := New(mp, reg, NewSession("stable system"), Options{}, sink)

	if err := a.Run(context.Background(), "one"); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	reg.Add(contractTool{
		name:   "write_file",
		schema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}}}`),
	})
	if err := a.Run(context.Background(), "two"); err != nil {
		t.Fatalf("second Run should continue after contract warning: %v", err)
	}

	var violations []event.CacheContractViolationPayload
	var warnings []string
	for _, e := range events {
		switch e.Kind {
		case event.CacheContractViolation:
			violations = append(violations, e.CacheContract)
		case event.Notice:
			if e.Level == event.LevelWarn && strings.Contains(e.Text, "cache contract drift") {
				warnings = append(warnings, e.Text)
			}
		}
	}
	if len(violations) != 1 {
		t.Fatalf("violations = %d, want 1; events=%v", len(violations), kinds(events))
	}
	if !reflect.DeepEqual(violations[0].Reasons, []string{"tool_schema_hash"}) {
		t.Fatalf("reasons = %v, want [tool_schema_hash]", violations[0].Reasons)
	}
	if violations[0].HarnessSnapshot != "h-0001" {
		t.Fatalf("harness snapshot = %q, want h-0001", violations[0].HarnessSnapshot)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %d, want 1", len(warnings))
	}

	requests := mp.Requests()
	if len(requests) != 2 {
		t.Fatalf("provider requests = %d, want 2", len(requests))
	}
	secondShape := CaptureShape(a.systemPrompt(), requests[1].Tools, a.session.RewriteVersion())
	if violations[0].Actual.ToolSchemaHash != secondShape.ToolsHash {
		t.Fatalf("violation hash %q should match provider request tools hash %q", violations[0].Actual.ToolSchemaHash, secondShape.ToolsHash)
	}
	if got, want := len(requests[1].Tools), 2; got != want {
		t.Fatalf("second request tools = %d, want %d", got, want)
	}
}

func kinds(events []event.Event) []event.Kind {
	out := make([]event.Kind, 0, len(events))
	for _, e := range events {
		out = append(out, e.Kind)
	}
	return out
}

func withActiveHarnessSnapshot(t *testing.T, id string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	root := filepath.Join(dir, ".reasonix-harness")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "active"), []byte(id+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

type contractTool struct {
	name   string
	schema json.RawMessage
}

func (t contractTool) Name() string        { return t.name }
func (t contractTool) Description() string { return t.name + " desc" }
func (t contractTool) Schema() json.RawMessage {
	if len(t.schema) > 0 {
		return t.schema
	}
	return json.RawMessage(`{"type":"object"}`)
}
func (t contractTool) Execute(context.Context, json.RawMessage) (string, error) { return "ok", nil }
func (t contractTool) ReadOnly() bool                                           { return true }

var _ tool.Tool = contractTool{}
