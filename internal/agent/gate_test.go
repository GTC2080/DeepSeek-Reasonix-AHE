package agent

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// stubGate denies any call whose tool name is in deny; everything else allows.
type stubGate struct {
	deny    map[string]bool
	checked []string
}

func (g *stubGate) Check(ctx context.Context, toolName string, args json.RawMessage, readOnly bool) (bool, string, error) {
	g.checked = append(g.checked, toolName)
	if g.deny[toolName] {
		return false, "denied by test policy", nil
	}
	return true, "", nil
}

// TestGateBlocksDeniedCall proves executeOne consults the gate after the
// plan-mode check: a denied tool returns a "blocked:" result plus a notice and
// never runs, while an allowed tool runs normally.
func TestGateBlocksDeniedCall(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "bash", readOnly: false})
	reg.Add(fakeTool{name: "read_file", readOnly: true})

	g := &stubGate{deny: map[string]bool{"bash": true}}
	a := New(nil, reg, NewSession(""), Options{Gate: g}, io.Discard)

	blocked, notice := a.executeOne(context.Background(), provider.ToolCall{Name: "bash", Arguments: `{"command":"rm -rf /"}`})
	if !strings.HasPrefix(blocked, "blocked:") {
		t.Errorf("denied call result = %q, want a 'blocked:' result", blocked)
	}
	if notice == "" {
		t.Errorf("denied call should surface a user-facing notice")
	}

	ok, _ := a.executeOne(context.Background(), provider.ToolCall{Name: "read_file", Arguments: `{"path":"/a"}`})
	if !strings.Contains(ok, "done") {
		t.Errorf("allowed call should run, got %q", ok)
	}

	if len(g.checked) != 2 {
		t.Errorf("gate consulted %d times, want 2 (%v)", len(g.checked), g.checked)
	}
}

// TestNilGateRunsEverything confirms gating is opt-in: with no gate wired, a
// writer call runs unimpeded (backward-compatible default).
func TestNilGateRunsEverything(t *testing.T) {
	reg := tool.NewRegistry()
	reg.Add(fakeTool{name: "write_file", readOnly: false})

	a := New(nil, reg, NewSession(""), Options{}, io.Discard) // no Gate
	out, _ := a.executeOne(context.Background(), provider.ToolCall{Name: "write_file", Arguments: `{"path":"/a"}`})
	if strings.HasPrefix(out, "blocked:") {
		t.Errorf("nil gate should not block: %q", out)
	}
}
