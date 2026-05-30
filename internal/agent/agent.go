package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"unicode/utf8"

	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// maxToolOutputBytes caps a single tool result before it goes into the model's
// context. ~32KB is roughly 8K tokens — enough for a full file read or a busy
// grep, while preventing one accidental "read this 5 MB log" from blowing the
// window before the next compaction runs.
const maxToolOutputBytes = 32 * 1024

// Renderer redraws the assistant's final-answer text as styled output. It is
// applied only after a turn's text stream completes, so the user sees raw
// markdown stream live, then a single redraw replaces it with formatted
// output. The renderer is intentionally interface-shaped so the agent stays
// independent of the cli's markdown library choice.
type Renderer interface {
	Render(text string) string
}

// Gate decides, per tool call, whether it may run. The agent consults it at
// execute time (after the plan-mode gate). It is interface-shaped so the agent
// stays independent of the permission package and of how "ask" is resolved
// (silently in headless runs, interactively in the chat TUI). A nil gate means
// no gating — every call runs, preserving behaviour for callers that don't wire
// one in. reason is fed back to the model when allow is false; a non-nil err
// (e.g. ctx cancelled awaiting approval) is treated as a block for that call.
type Gate interface {
	Check(ctx context.Context, toolName string, args json.RawMessage, readOnly bool) (allow bool, reason string, err error)
}

// Agent drives a single task: a Provider, a tool Registry, and a Session wired
// into the main loop.
type Agent struct {
	prov        provider.Provider
	tools       *tool.Registry
	session     *Session
	maxSteps    int
	temperature float64
	pricing     *provider.Pricing
	out         io.Writer

	// Optional post-stream redraw of assistant text as styled markdown. nil
	// keeps the raw stream as-is (useful for non-tty / piped output and tests).
	renderer  Renderer
	termWidth int

	// lastUsage caches the most recent per-turn telemetry the provider
	// reported so the CLI can expose a context gauge without re-scraping the
	// usage line out of the output writer.
	lastUsage *provider.Usage

	// planMode, when true, refuses any tool call whose ReadOnly() is false.
	// The system prompt and tool list never change with the toggle so the
	// prompt-cache prefix stays valid; the gating happens at execute time
	// and the model sees a "blocked" result it can adapt to. Toggled from
	// the outside via SetPlanMode.
	planMode bool

	// gate, when non-nil, is the per-call permission gate consulted after the
	// plan-mode check. nil disables gating entirely.
	gate Gate

	// Context management: when a turn's prompt nears contextWindow, the older
	// middle of the session is summarized away, keeping recentKeep messages
	// verbatim and archiving the originals under archiveDir.
	contextWindow int
	compactRatio  float64
	recentKeep    int
	archiveDir    string
}

// SetPlanMode flips the read-only gate. While true, executeOne refuses any
// non-ReadOnly tool the model calls and returns a "blocked" result instead of
// running it. The cache-friendly bits — system prompt, tools schema, message
// history — are left untouched, so the toggle costs nothing in cache hits.
func (a *Agent) SetPlanMode(v bool) { a.planMode = v }

// SetGate installs the per-call permission gate. Used by `reasonix chat` to swap the
// headless gate built in setup for an interactive one that prompts the user;
// nil disables gating. Safe to call before the run loop starts.
func (a *Agent) SetGate(g Gate) { a.gate = g }

// Session returns the agent's current conversation, useful for persistence
// hooks that need to read the message log between turns.
func (a *Agent) Session() *Session { return a.session }

// SetSession replaces the agent's conversation wholesale. Used by
// `reasonix chat --resume` to load a saved JSONL transcript before the first turn,
// so the model picks up exactly where it left off.
func (a *Agent) SetSession(s *Session) { a.session = s }

// LastUsage returns the most recent per-turn token telemetry the provider
// reported (nil if no turn has run yet). The TUI uses it to show a context
// gauge alongside the prompt; the actual cache decisions still live inside
// maybeCompact.
func (a *Agent) LastUsage() *provider.Usage { return a.lastUsage }

// ContextWindow returns the configured context-window size in tokens. 0
// means compaction is disabled for this agent.
func (a *Agent) ContextWindow() int { return a.contextWindow }

// CompactNow runs one compaction pass immediately, regardless of the
// usage-ratio threshold maybeCompact normally honours. Used by the chat
// TUI's `/compact` command so the user can reset the prefix before it
// naturally fills up.
func (a *Agent) CompactNow(ctx context.Context) error { return a.compact(ctx) }

// Options configures an Agent.
type Options struct {
	MaxSteps    int
	Temperature float64
	Pricing     *provider.Pricing // optional, for per-turn cost display

	// Renderer, when set, replaces the streamed raw text with styled markdown
	// after each assistant turn. TermWidth is the column count used both for
	// the renderer's wrapping and for counting how many rows the raw stream
	// occupied (so the cursor lands on the right line before redrawing).
	Renderer  Renderer
	TermWidth int

	// Gate is the per-call permission gate. nil disables gating.
	Gate Gate

	// Context management. ContextWindow <= 0 disables compaction. CompactRatio
	// and RecentKeep fall back to defaults when unset.
	ContextWindow int
	CompactRatio  float64
	RecentKeep    int
	ArchiveDir    string
}

// New constructs an Agent. MaxSteps <= 0 defaults to 25.
func New(prov provider.Provider, tools *tool.Registry, session *Session, opts Options, out io.Writer) *Agent {
	if opts.MaxSteps <= 0 {
		opts.MaxSteps = 25
	}
	if opts.CompactRatio <= 0 {
		opts.CompactRatio = defaultCompactRatio
	}
	if opts.RecentKeep <= 0 {
		opts.RecentKeep = defaultRecentKeep
	}
	return &Agent{
		prov:          prov,
		tools:         tools,
		session:       session,
		maxSteps:      opts.MaxSteps,
		temperature:   opts.Temperature,
		pricing:       opts.Pricing,
		out:           out,
		renderer:      opts.Renderer,
		termWidth:     opts.TermWidth,
		gate:          opts.Gate,
		contextWindow: opts.ContextWindow,
		compactRatio:  opts.CompactRatio,
		recentKeep:    opts.RecentKeep,
		archiveDir:    opts.ArchiveDir,
	}
}

// Run appends the user input and runs the loop until the model stops requesting
// tools or maxSteps is reached.
func (a *Agent) Run(ctx context.Context, input string) error {
	a.session.Add(provider.Message{Role: provider.RoleUser, Content: input})

	for step := 0; step < a.maxSteps; step++ {
		text, reasoning, calls, usage, err := a.stream(ctx)
		if err != nil {
			return err
		}
		printUsage(a.out, usage, a.pricing)
		printFinishReasonWarning(a.out, usage)

		// Round-trip reasoning_content on the assistant turn so multi-turn
		// thinking chains stay coherent (MiMo / DeepSeek-reasoner ask for this).
		a.session.Add(provider.Message{
			Role:             provider.RoleAssistant,
			Content:          text,
			ReasoningContent: reasoning,
			ToolCalls:        calls,
		})

		if len(calls) == 0 {
			return nil // model gave a final answer
		}

		results := a.executeBatch(ctx, calls)
		for i, call := range calls {
			a.session.Add(provider.Message{
				Role:       provider.RoleTool,
				Content:    results[i],
				ToolCallID: call.ID,
				Name:       call.Name,
			})
		}

		// The prompt only grows from here; compact before the next turn so it
		// stays within the model's window.
		a.maybeCompact(ctx, usage)
	}
	return fmt.Errorf("reached max steps (%d) without completing", a.maxSteps)
}

// stream runs one completion, printing text deltas live and collecting complete
// tool calls. Reasoning deltas (thinking-mode chain-of-thought) are shown in
// muted prose under a "thinking" header so the user can follow the model's
// reasoning without confusing it with the final answer, and accumulated so the
// caller can round-trip it on the next turn.
func (a *Agent) stream(ctx context.Context) (string, string, []provider.ToolCall, *provider.Usage, error) {
	ch, err := a.prov.Stream(ctx, provider.Request{
		Messages:    a.session.Messages,
		Tools:       a.tools.Schemas(),
		Temperature: a.temperature,
	})
	if err != nil {
		return "", "", nil, nil, err
	}

	var text, reasoning strings.Builder
	var calls []provider.ToolCall
	var usage *provider.Usage
	wroteReasoningHeader := false
	for chunk := range ch {
		switch chunk.Type {
		case provider.ChunkReasoning:
			if !wroteReasoningHeader {
				fmt.Fprintln(a.out, dimText("  ▎ thinking"))
				wroteReasoningHeader = true
			}
			reasoning.WriteString(chunk.Text)
			fmt.Fprint(a.out, dimText(chunk.Text))
		case provider.ChunkText:
			if wroteReasoningHeader && text.Len() == 0 {
				fmt.Fprintln(a.out) // separate the reasoning block from the answer
			}
			text.WriteString(chunk.Text)
			fmt.Fprint(a.out, chunk.Text)
		case provider.ChunkToolCall:
			calls = append(calls, *chunk.ToolCall)
		case provider.ChunkUsage:
			usage = chunk.Usage
			a.lastUsage = chunk.Usage
		case provider.ChunkError:
			return "", "", nil, nil, chunk.Err
		}
	}
	// If a renderer is wired in, replace the raw streamed text with styled
	// markdown: move the cursor back to the row where text streaming began,
	// clear from there to the end of the screen, and re-emit the rendered
	// version. Reasoning above the text stays untouched. Very long messages
	// keep the raw stream — the move-up would otherwise sail past the screen
	// top and leave artifacts.
	if text.Len() > 0 && a.renderer != nil {
		if moved := streamedRows(text.String(), a.termWidth); moved < 200 {
			if moved == 0 {
				fmt.Fprint(a.out, "\r\033[0J")
			} else {
				fmt.Fprintf(a.out, "\r\033[%dA\033[0J", moved)
			}
			fmt.Fprint(a.out, a.renderer.Render(text.String()))
			return text.String(), reasoning.String(), calls, usage, nil
		}
	}
	if text.Len() > 0 || reasoning.Len() > 0 {
		fmt.Fprintln(a.out)
	}
	return text.String(), reasoning.String(), calls, usage, nil
}

// dimText wraps s in the ANSI dim SGR sequence so reasoning streams visually
// recede from the final answer. Lives here to avoid importing the cli style
// helpers — the agent must stay independent of CLI rendering choices, so this
// uses raw codes the writer can strip if needed.
func dimText(s string) string { return "\x1b[2m" + s + "\x1b[0m" }

// executeBatch dispatches one model turn's tool calls. The schedule lines
// "-> tool args" are always printed in call order up front so the timeline
// reads chronologically. Calls fan out across goroutines only when every
// call's tool is ReadOnly (canParallelise); a single non-ReadOnly call drops
// the whole batch back to sequential to preserve write/read ordering and
// keep effects deterministic. Truncation notices, if any, print serially in
// call order after the dispatch completes.
func (a *Agent) executeBatch(ctx context.Context, calls []provider.ToolCall) []string {
	for _, c := range calls {
		fmt.Fprintf(a.out, "  -> %s %s\n", c.Name, compactArgs(c.Arguments))
	}

	results := make([]string, len(calls))
	notices := make([]string, len(calls))
	run := func(i int) {
		results[i], notices[i] = a.executeOne(ctx, calls[i])
	}

	if canParallelise(a.tools, calls) && len(calls) > 1 {
		const maxParallel = 8
		sem := make(chan struct{}, maxParallel)
		var wg sync.WaitGroup
		for i := range calls {
			i := i
			sem <- struct{}{}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				run(i)
			}()
		}
		wg.Wait()
	} else {
		for i := range calls {
			run(i)
		}
	}

	for _, n := range notices {
		if n != "" {
			fmt.Fprintln(a.out, n)
		}
	}
	return results
}

// executeOne runs a single tool call and returns (resultText, noticeText).
// It is pure with respect to a.out — the caller is responsible for the live
// schedule line, so this is safe to invoke from parallel goroutines.
func (a *Agent) executeOne(ctx context.Context, call provider.ToolCall) (string, string) {
	t, ok := a.tools.Get(call.Name)
	if !ok {
		return fmt.Sprintf("error: unknown tool %q", call.Name), ""
	}
	if a.planMode && !t.ReadOnly() {
		return fmt.Sprintf("blocked: %q is a writer tool and plan mode is read-only — propose the change in your final answer instead. The user will toggle plan mode off (Tab) to execute.", call.Name), ""
	}
	if a.gate != nil {
		allow, reason, err := a.gate.Check(ctx, call.Name, json.RawMessage(call.Arguments), t.ReadOnly())
		if err != nil {
			return fmt.Sprintf("blocked: %s (%v)", reason, err), fmt.Sprintf("  ⊘ %s blocked: %v", call.Name, err)
		}
		if !allow {
			return "blocked: " + reason, fmt.Sprintf("  ⊘ %s blocked by permission policy", call.Name)
		}
	}
	result, err := t.Execute(ctx, json.RawMessage(call.Arguments))
	if err != nil {
		return truncateToolOutput(fmt.Sprintf("error: %v\n%s", err, result))
	}
	return truncateToolOutput(result)
}

// canParallelise returns true iff every call targets a known, ReadOnly tool.
// Any unknown tool name (let the sequential path produce a clean error) or any
// non-ReadOnly tool (preserve write ordering) forces serial execution.
func canParallelise(r *tool.Registry, calls []provider.ToolCall) bool {
	for _, c := range calls {
		t, ok := r.Get(c.Name)
		if !ok || !t.ReadOnly() {
			return false
		}
	}
	return true
}

// truncateToolOutput head+tails s when it exceeds maxToolOutputBytes, slicing
// on rune boundaries so we never split a multibyte glyph. Returns the possibly
// trimmed body plus a one-line user-facing notice when truncation happened
// (empty when it didn't), so callers can render notices in deterministic
// order even when called from parallel goroutines.
func truncateToolOutput(s string) (string, string) {
	if len(s) <= maxToolOutputBytes {
		return s, ""
	}
	keep := maxToolOutputBytes / 2
	head := snapToRuneBoundary(s, 0, keep)
	tail := snapToRuneBoundary(s, len(s)-keep, len(s))
	omitted := len(s) - len(head) - len(tail)
	notice := fmt.Sprintf("  · tool output truncated: %d of %d bytes elided", omitted, len(s))
	body := head + fmt.Sprintf("\n\n…[truncated %d of %d bytes — rerun with narrower args to see the middle]…\n\n", omitted, len(s)) + tail
	return body, notice
}

// snapToRuneBoundary returns s[lo:hi] with the bounds nudged outward until
// both land on rune-start positions.
func snapToRuneBoundary(s string, lo, hi int) string {
	for lo > 0 && !utf8.RuneStart(s[lo]) {
		lo--
	}
	for hi < len(s) && !utf8.RuneStart(s[hi]) {
		hi++
	}
	return s[lo:hi]
}

func compactArgs(s string) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) > 120 {
		return string(r[:120]) + "..."
	}
	return s
}

// printFinishReasonWarning emits a one-line notice when the model stopped for
// a non-normal reason — length truncation, content filter, or repetition
// truncation (MiMo-specific). "stop" / "tool_calls" are the normal terminations
// and produce no output.
func printFinishReasonWarning(w io.Writer, u *provider.Usage) {
	if u == nil {
		return
	}
	var msg string
	switch u.FinishReason {
	case "length":
		msg = "response truncated: hit max output tokens"
	case "content_filter":
		msg = "response blocked by content filter"
	case "repetition_truncation":
		msg = "response truncated: model repetition detected"
	default:
		return
	}
	fmt.Fprintln(w, "  ! "+msg)
}

// printUsage prints a one-line token/cache summary — the key signal for the
// cache-first design. Cache is reported as absolute "(N cached / M new)" so a
// turn that adds a lot of fresh content (e.g. a long tool result) doesn't read
// as "cache broke" the way a falling percentage would; the cached prefix is
// still hitting, the denominator just grew. Thinking-capable models also
// report reasoning_tokens, a subset of the completion count we display so
// users can see the cost of the chain-of-thought. No-op when usage is unset.
func printUsage(w io.Writer, u *provider.Usage, p *provider.Pricing) {
	if u == nil || u.TotalTokens == 0 {
		return
	}
	cacheCol := ""
	if u.PromptTokens > 0 {
		cached := u.CacheHitTokens
		fresh := u.CacheMissTokens
		if fresh == 0 {
			// Derive when the provider only reported the hit half; the miss
			// equals the remaining prompt unless something underflows.
			if d := u.PromptTokens - cached; d > 0 {
				fresh = d
			}
		}
		cacheCol = fmt.Sprintf(" (%d cached / %d new)", cached, fresh)
	}
	reasoning := ""
	if u.ReasoningTokens > 0 {
		reasoning = fmt.Sprintf(" (%d reasoning)", u.ReasoningTokens)
	}
	cost := ""
	if p != nil {
		cost = fmt.Sprintf(" · %s%.4f", p.Symbol(), p.Cost(u))
	}
	fmt.Fprintf(w, "  · %d tok · in %d%s · out %d%s%s\n",
		u.TotalTokens, u.PromptTokens, cacheCol, u.CompletionTokens, reasoning, cost)
}
