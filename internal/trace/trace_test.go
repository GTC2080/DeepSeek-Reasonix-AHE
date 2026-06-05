package trace

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/provider"
)

func TestJSONLWriterWritesValidLinesAndCreatesParents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "trace.jsonl")
	w, err := OpenJSONL(path)
	if err != nil {
		t.Fatalf("OpenJSONL: %v", err)
	}
	if err := w.WriteEvent(Event{Version: Version, RunID: "run", SessionID: "sess", Seq: 1, Type: "test", Time: time.Unix(1, 0)}); err != nil {
		t.Fatalf("WriteEvent: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	events := readTraceFile(t, path)
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if events[0].Type != "test" || events[0].Seq != 1 {
		t.Fatalf("event = %+v", events[0])
	}
}

func TestJSONLWriterAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	for i := 1; i <= 2; i++ {
		w, err := OpenJSONL(path)
		if err != nil {
			t.Fatalf("OpenJSONL %d: %v", i, err)
		}
		if err := w.WriteEvent(Event{Version: Version, RunID: "run", SessionID: "sess", Seq: int64(i), Type: "line"}); err != nil {
			t.Fatalf("WriteEvent %d: %v", i, err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("Close %d: %v", i, err)
		}
	}

	events := readTraceFile(t, path)
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
}

func TestRedactStringRemovesSecrets(t *testing.T) {
	in := `{"api_key":"sk-1234567890abcdef","apiKey":"secret","token":"abc"} Authorization=Bearer abcdef password=hunter2`
	got := RedactString(in)
	for _, forbidden := range []string{"sk-1234567890abcdef", `"secret"`, `"abc"`, "hunter2"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("redacted string still contains %q: %s", forbidden, got)
		}
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("redacted string missing marker: %s", got)
	}
}

func TestSinkModesControlBodyFields(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mode    Mode
		wantKey string
		noKey   string
	}{
		{name: "metadata", mode: ModeMetadata, noKey: "text_preview"},
		{name: "preview", mode: ModePreview, wantKey: "text_preview", noKey: "text"},
		{name: "full", mode: ModeFull, wantKey: "text", noKey: "text_preview"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &memoryWriter{}
			s := NewSink(event.Discard, w, Options{
				Mode:      tc.mode,
				RunID:     "run",
				SessionID: "sess",
				Now:       fixedClock(),
			})
			s.Emit(event.Event{Kind: event.Text, Text: "hello sk-1234567890abcdef"})
			if err := s.Close(nil); err != nil {
				t.Fatalf("Close: %v", err)
			}
			ev := firstType(t, w.events, "text_delta")
			if tc.wantKey != "" {
				if _, ok := ev.Data[tc.wantKey]; !ok {
					t.Fatalf("missing %q in data: %+v", tc.wantKey, ev.Data)
				}
			}
			if tc.noKey != "" {
				if _, ok := ev.Data[tc.noKey]; ok {
					t.Fatalf("unexpected %q in data: %+v", tc.noKey, ev.Data)
				}
			}
			for _, v := range ev.Data {
				if s, ok := v.(string); ok && strings.Contains(s, "sk-1234567890abcdef") {
					t.Fatalf("secret leaked in data: %+v", ev.Data)
				}
			}
		})
	}
}

func TestSinkForwardsAndWritesSessionLifecycle(t *testing.T) {
	var forwarded []event.Event
	w := &memoryWriter{}
	s := NewSink(event.FuncSink(func(e event.Event) { forwarded = append(forwarded, e) }), w, Options{
		Mode:      ModeMetadata,
		RunID:     "run",
		SessionID: "sess",
		Now:       fixedClock(),
	})

	s.Emit(event.Event{Kind: event.TurnStarted})
	s.Emit(event.Event{Kind: event.Usage, Usage: &provider.Usage{PromptTokens: 10, CacheHitTokens: 7, CacheMissTokens: 3}, SessionHit: 7, SessionMiss: 3})
	if err := s.Close(errors.New("boom")); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if len(forwarded) != 2 {
		t.Fatalf("forwarded = %d, want 2", len(forwarded))
	}
	types := eventTypes(w.events)
	want := []string{"session_start", "turn_start", "cache_stats", "session_end"}
	if strings.Join(types, ",") != strings.Join(want, ",") {
		t.Fatalf("types = %v, want %v", types, want)
	}
	end := firstType(t, w.events, "session_end")
	if end.Data["error"] != "boom" {
		t.Fatalf("session_end error = %+v", end.Data)
	}
	usage := firstType(t, w.events, "cache_stats")
	if usage.Data["cache_hit_ratio"] != 0.7 {
		t.Fatalf("cache_hit_ratio = %+v", usage.Data["cache_hit_ratio"])
	}
}

func TestSinkWritesSessionStartHarnessSnapshot(t *testing.T) {
	w := &memoryWriter{}
	s := NewSink(event.Discard, w, Options{
		Mode:                    ModeMetadata,
		RunID:                   "run",
		SessionID:               "sess",
		HarnessSnapshot:         "h-0001",
		HarnessStablePrefixHash: "sha256:harness-prefix",
		Now:                     fixedClock(),
	})

	start := firstType(t, w.events, "session_start")
	if got := start.Data["harness_snapshot"]; got != "h-0001" {
		t.Fatalf("harness_snapshot = %#v, want h-0001", got)
	}
	if got := start.Data["harness_stable_prefix_hash"]; got != "sha256:harness-prefix" {
		t.Fatalf("harness_stable_prefix_hash = %#v, want sha256:harness-prefix", got)
	}
	if err := s.Close(nil); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestSinkComputesToolDuration(t *testing.T) {
	now := time.Unix(0, 0)
	w := &memoryWriter{}
	s := NewSink(event.Discard, w, Options{
		Mode:      ModePreview,
		RunID:     "run",
		SessionID: "sess",
		Now: func() time.Time {
			now = now.Add(10 * time.Millisecond)
			return now
		},
	})
	s.Emit(event.Event{Kind: event.ToolDispatch, Tool: event.Tool{ID: "call-1", Name: "bash", Args: `{"command":"echo hi"}`}})
	s.Emit(event.Event{Kind: event.ToolResult, Tool: event.Tool{ID: "call-1", Name: "bash", Output: "ok"}})

	result := firstType(t, w.events, "tool_result")
	if got, ok := result.Data["duration_ms"].(int64); !ok || got <= 0 {
		t.Fatalf("duration_ms = %#v, want positive int64", result.Data["duration_ms"])
	}
}

func TestSinkMapsCacheContractViolation(t *testing.T) {
	w := &memoryWriter{}
	s := NewSink(event.Discard, w, Options{
		Mode:      ModeMetadata,
		RunID:     "run",
		SessionID: "sess",
		Now:       fixedClock(),
	})
	s.Emit(event.Event{
		Kind: event.CacheContractViolation,
		CacheContract: event.CacheContractViolationPayload{
			SessionID:               "sess",
			HarnessSnapshot:         "h-0001",
			HarnessStablePrefixHash: "sha256:harness-prefix",
			Step:                    2,
			Expected: event.CacheContractShape{
				SystemPromptHash: "sha256:system",
				ToolSchemaHash:   "sha256:old-tools",
				StablePrefixHash: "sha256:old-prefix",
			},
			Actual: event.CacheContractShape{
				SystemPromptHash: "sha256:system",
				ToolSchemaHash:   "sha256:new-tools",
				StablePrefixHash: "sha256:new-prefix",
			},
			Reasons: []string{"tool_schema_hash"},
		},
	})

	ev := firstType(t, w.events, "cache_contract_violation")
	if got := ev.Data["session_id"]; got != "sess" {
		t.Fatalf("session_id = %#v, want sess", got)
	}
	if got := ev.Data["harness_snapshot"]; got != "h-0001" {
		t.Fatalf("harness_snapshot = %#v, want h-0001", got)
	}
	if got := ev.Data["harness_stable_prefix_hash"]; got != "sha256:harness-prefix" {
		t.Fatalf("harness_stable_prefix_hash = %#v, want sha256:harness-prefix", got)
	}
	if got := ev.Data["step"]; got != 2 {
		t.Fatalf("step = %#v, want 2", got)
	}
	reasons, ok := ev.Data["reasons"].([]string)
	if !ok || len(reasons) != 1 || reasons[0] != "tool_schema_hash" {
		t.Fatalf("reasons = %#v, want [tool_schema_hash]", ev.Data["reasons"])
	}
	actual, ok := ev.Data["actual"].(map[string]any)
	if !ok {
		t.Fatalf("actual = %#v, want map", ev.Data["actual"])
	}
	if got := actual["tool_schema_hash"]; got != "sha256:new-tools" {
		t.Fatalf("actual tool_schema_hash = %#v, want sha256:new-tools", got)
	}
}

func readTraceFile(t *testing.T, path string) []Event {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var events []Event
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("invalid JSONL line %q: %v", sc.Text(), err)
		}
		events = append(events, e)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return events
}

type memoryWriter struct {
	events []Event
}

func (w *memoryWriter) WriteEvent(e Event) error {
	cp := e
	if e.Data != nil {
		cp.Data = map[string]any{}
		for k, v := range e.Data {
			cp.Data[k] = v
		}
	}
	w.events = append(w.events, cp)
	return nil
}

func (w *memoryWriter) Close() error { return nil }

func fixedClock() func() time.Time {
	t := time.Unix(123, 0).UTC()
	return func() time.Time { return t }
}

func firstType(t *testing.T, events []Event, typ string) Event {
	t.Helper()
	for _, e := range events {
		if e.Type == typ {
			return e
		}
	}
	t.Fatalf("missing event type %q in %v", typ, eventTypes(events))
	return Event{}
}

func eventTypes(events []Event) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.Type)
	}
	return out
}
