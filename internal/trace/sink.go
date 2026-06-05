package trace

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/nilutil"
	"reasonix/internal/provider"
)

// Options configures a trace sink.
type Options struct {
	Mode                    Mode
	RunID                   string
	SessionID               string
	HarnessSnapshot         string
	HarnessStablePrefixHash string
	Now                     func() time.Time
}

// Sink writes trace records and forwards the original event stream unchanged.
type Sink struct {
	mu                      sync.Mutex
	inner                   event.Sink
	writer                  Writer
	mode                    Mode
	runID                   string
	sessionID               string
	harnessSnapshot         string
	harnessStablePrefixHash string
	now                     func() time.Time
	seq                     int64
	turn                    int
	firstErr                error
	closed                  bool
	toolStart               map[string]time.Time
}

// NewSink creates a trace sink and writes session_start immediately.
func NewSink(inner event.Sink, writer Writer, opts Options) *Sink {
	if nilutil.IsNil(inner) {
		inner = event.Discard
	}
	if writer == nil {
		writer = Noop{}
	}
	mode := opts.Mode
	if mode == "" {
		mode = DefaultMode
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	s := &Sink{
		inner:                   inner,
		writer:                  writer,
		mode:                    mode,
		runID:                   defaultID(opts.RunID, "run"),
		sessionID:               defaultID(opts.SessionID, "session"),
		harnessSnapshot:         opts.HarnessSnapshot,
		harnessStablePrefixHash: opts.HarnessStablePrefixHash,
		now:                     now,
		toolStart:               map[string]time.Time{},
	}
	startData := map[string]any{"mode": string(mode)}
	if s.harnessSnapshot != "" {
		startData["harness_snapshot"] = s.harnessSnapshot
	}
	if s.harnessStablePrefixHash != "" {
		startData["harness_stable_prefix_hash"] = s.harnessStablePrefixHash
	}
	s.writeLocked("session_start", 0, startData)
	return s
}

func defaultID(v, prefix string) string {
	if v != "" {
		return v
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err == nil {
		return prefix + "-" + hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

// Emit records a trace event and forwards the original event.
func (s *Sink) Emit(e event.Event) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if e.Kind == event.TurnStarted {
		s.turn++
	}
	typ, data, ok := s.convertLocked(e)
	turn := s.eventTurn(e)
	if ok {
		s.writeLocked(typ, turn, data)
	}
	s.mu.Unlock()
	s.inner.Emit(e)
}

func (s *Sink) eventTurn(e event.Event) int {
	return s.turn
}

// Close writes session_end and closes the writer. finalErr is recorded as
// metadata; the first trace I/O error is returned.
func (s *Sink) Close(finalErr error) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if !s.closed {
		data := map[string]any{}
		if finalErr != nil {
			data["error"] = finalErr.Error()
		}
		s.writeLocked("session_end", s.turn, data)
		s.closed = true
	}
	closeErr := s.writer.Close()
	if closeErr != nil && s.firstErr == nil {
		s.firstErr = closeErr
	}
	err := s.firstErr
	s.mu.Unlock()
	return err
}

// Err returns the first trace write or close error seen by the sink.
func (s *Sink) Err() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.firstErr
}

func (s *Sink) writeLocked(typ string, turn int, data map[string]any) {
	s.seq++
	err := s.writer.WriteEvent(Event{
		Version:   Version,
		RunID:     s.runID,
		SessionID: s.sessionID,
		Seq:       s.seq,
		Type:      typ,
		Time:      s.now(),
		Turn:      turn,
		Data:      data,
	})
	if err != nil && s.firstErr == nil {
		s.firstErr = err
	}
}

func (s *Sink) convertLocked(e event.Event) (string, map[string]any, bool) {
	switch e.Kind {
	case event.TurnStarted:
		return "turn_start", nil, true
	case event.TurnDone:
		data := map[string]any{}
		if e.Err != nil {
			data["error"] = e.Err.Error()
		}
		return "turn_done", data, true
	case event.Text:
		data := map[string]any{}
		modeText(s.mode, "text", e.Text, data)
		return "text_delta", data, true
	case event.Reasoning:
		data := map[string]any{}
		modeText(s.mode, "reasoning", e.Text, data)
		return "reasoning_delta", data, true
	case event.Message:
		data := map[string]any{}
		modeText(s.mode, "text", e.Text, data)
		modeText(s.mode, "reasoning", e.Reasoning, data)
		return "message", data, true
	case event.ToolDispatch:
		return "tool_call", s.toolDispatchData(e.Tool), true
	case event.ToolResult:
		return "tool_result", s.toolResultData(e.Tool), true
	case event.Usage:
		return "cache_stats", usageData(e.Usage, e.SessionHit, e.SessionMiss, e.CacheDiagnostics), true
	case event.Notice:
		data := map[string]any{"level": noticeLevel(e.Level)}
		modeText(s.mode, "text", e.Text, data)
		return "notice", data, true
	case event.Phase:
		data := map[string]any{}
		modeText(s.mode, "text", e.Text, data)
		return "phase", data, true
	case event.ApprovalRequest:
		return "approval_request", map[string]any{
			"id":      e.Approval.ID,
			"tool":    e.Approval.Tool,
			"subject": RedactString(e.Approval.Subject),
		}, true
	case event.AskRequest:
		return "ask_request", map[string]any{
			"id":             e.Ask.ID,
			"question_count": len(e.Ask.Questions),
		}, true
	case event.CompactionStarted:
		return "compaction_started", map[string]any{"trigger": e.Compaction.Trigger}, true
	case event.CompactionDone:
		data := map[string]any{
			"trigger":  e.Compaction.Trigger,
			"messages": e.Compaction.Messages,
			"archive":  e.Compaction.Archive,
		}
		modeText(s.mode, "summary", e.Compaction.Summary, data)
		return "compaction_done", data, true
	case event.ToolProgress:
		data := map[string]any{"tool_call_id": e.Tool.ID}
		modeText(s.mode, "output", e.Tool.Output, data)
		return "tool_progress", data, true
	case event.MCPSurfaceReady:
		data := map[string]any{}
		modeText(s.mode, "text", e.Text, data)
		return "mcp_surface_ready", data, true
	case event.Retrying:
		return "retrying", map[string]any{"attempt": e.RetryAttempt, "max": e.RetryMax}, true
	case event.ModelRequest:
		return "model_request", modelData(e.Model), true
	case event.ModelResponse:
		return "model_response", modelData(e.Model), true
	case event.CacheContractViolation:
		return "cache_contract_violation", cacheContractData(e.CacheContract), true
	default:
		return "", nil, false
	}
}

func (s *Sink) toolDispatchData(t event.Tool) map[string]any {
	data := map[string]any{
		"tool_call_id": t.ID,
		"tool_name":    t.Name,
		"read_only":    t.ReadOnly,
		"partial":      t.Partial,
	}
	if t.ParentID != "" {
		data["parent_id"] = t.ParentID
	}
	if t.FileDiff.Diff != "" {
		data["diff_added"] = t.FileDiff.Added
		data["diff_removed"] = t.FileDiff.Removed
		modeText(s.mode, "diff", t.FileDiff.Diff, data)
	}
	modeText(s.mode, "args", t.Args, data)
	if t.ID != "" {
		if !t.Partial {
			s.toolStart[t.ID] = s.now()
		} else if _, ok := s.toolStart[t.ID]; !ok {
			s.toolStart[t.ID] = s.now()
		}
	}
	return data
}

func (s *Sink) toolResultData(t event.Tool) map[string]any {
	data := map[string]any{
		"tool_call_id": t.ID,
		"tool_name":    t.Name,
		"read_only":    t.ReadOnly,
		"truncated":    t.Truncated,
	}
	if t.Err != "" {
		data["error"] = t.Err
	}
	if t.ID != "" {
		if started, ok := s.toolStart[t.ID]; ok {
			data["duration_ms"] = s.now().Sub(started).Milliseconds()
			delete(s.toolStart, t.ID)
		}
	}
	modeText(s.mode, "args", t.Args, data)
	modeText(s.mode, "output", t.Output, data)
	return data
}

func usageData(u *provider.Usage, sessionHit, sessionMiss int, d *event.CacheDiagnostics) map[string]any {
	data := map[string]any{
		"available":              u != nil,
		"session_cache_hit":      sessionHit,
		"session_cache_miss":     sessionMiss,
		"session_cache_hit_rate": ratio(sessionHit, sessionMiss),
	}
	if u != nil {
		data["prompt_tokens"] = u.PromptTokens
		data["completion_tokens"] = u.CompletionTokens
		data["total_tokens"] = u.TotalTokens
		data["prompt_cache_hit_tokens"] = u.CacheHitTokens
		data["prompt_cache_miss_tokens"] = u.CacheMissTokens
		data["cache_hit_ratio"] = ratio(u.CacheHitTokens, u.CacheMissTokens)
		data["reasoning_tokens"] = u.ReasoningTokens
		data["finish_reason"] = u.FinishReason
	}
	if d != nil {
		data["prefix_hash"] = d.PrefixHash
		data["prefix_changed"] = d.PrefixChanged
		data["prefix_change_reasons"] = d.PrefixChangeReasons
		data["system_hash"] = d.SystemHash
		data["tools_hash"] = d.ToolsHash
		data["log_rewrite_version"] = d.LogRewriteVersion
		data["tool_schema_tokens"] = d.ToolSchemaTokens
	}
	return data
}

func modelData(m event.ModelCall) map[string]any {
	data := map[string]any{
		"provider":        m.Provider,
		"model_step":      m.Turn,
		"message_count":   m.MessageCount,
		"tool_count":      m.ToolCount,
		"temperature":     m.Temperature,
		"duration_ms":     m.Duration.Milliseconds(),
		"tool_call_count": m.ToolCallCount,
	}
	if m.FinishReason != "" {
		data["finish_reason"] = m.FinishReason
	}
	if m.Err != "" {
		data["error"] = m.Err
	}
	if m.Usage != nil {
		for k, v := range usageData(m.Usage, 0, 0, nil) {
			if k == "session_cache_hit" || k == "session_cache_miss" || k == "session_cache_hit_rate" {
				continue
			}
			data[k] = v
		}
	}
	return data
}

func cacheContractData(v event.CacheContractViolationPayload) map[string]any {
	data := map[string]any{
		"session_id": v.SessionID,
		"turn":       v.Turn,
		"step":       v.Step,
		"expected":   cacheContractShapeData(v.Expected),
		"actual":     cacheContractShapeData(v.Actual),
		"reasons":    append([]string(nil), v.Reasons...),
	}
	if v.HarnessSnapshot != "" {
		data["harness_snapshot"] = v.HarnessSnapshot
	}
	if v.HarnessStablePrefixHash != "" {
		data["harness_stable_prefix_hash"] = v.HarnessStablePrefixHash
	}
	return data
}

func cacheContractShapeData(s event.CacheContractShape) map[string]any {
	return map[string]any{
		"system_prompt_hash": s.SystemPromptHash,
		"tool_schema_hash":   s.ToolSchemaHash,
		"stable_prefix_hash": s.StablePrefixHash,
	}
}

func ratio(hit, miss int) float64 {
	total := hit + miss
	if total <= 0 {
		return 0
	}
	return float64(hit) / float64(total)
}

func noticeLevel(l event.Level) string {
	if l == event.LevelWarn {
		return "warn"
	}
	return "info"
}
