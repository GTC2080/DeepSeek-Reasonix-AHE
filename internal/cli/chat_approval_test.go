package cli

import (
	"context"
	"testing"
)

// TestChannelApproverAllowOnce drives the happy path: a request lands on reqCh,
// the (fake) TUI replies allow, and Approve returns allow with no session grant.
func TestChannelApproverAllowOnce(t *testing.T) {
	reqCh := make(chan approvalReq, 1)
	ap := newChannelApprover(reqCh)
	go func() {
		req := <-reqCh
		if req.tool != "bash" || req.subject != "go test" {
			t.Errorf("request = {%q,%q}, want {bash, go test}", req.tool, req.subject)
		}
		req.reply <- approvalResp{allow: true}
	}()

	allow, remember, err := ap.Approve(context.Background(), "bash", "go test", nil)
	if err != nil || !allow || remember {
		t.Fatalf("Approve = (%v,%v,%v), want allow once", allow, remember, err)
	}
}

// TestChannelApproverDeny confirms a declined call returns allow=false.
func TestChannelApproverDeny(t *testing.T) {
	reqCh := make(chan approvalReq, 1)
	ap := newChannelApprover(reqCh)
	go func() {
		req := <-reqCh
		req.reply <- approvalResp{allow: false}
	}()

	allow, _, err := ap.Approve(context.Background(), "bash", "rm -rf /", nil)
	if err != nil || allow {
		t.Fatalf("Approve = (%v,%v), want deny", allow, err)
	}
}

// TestChannelApproverSessionGrant proves an "allow this session" answer
// short-circuits later prompts for the same tool+subject: only the first call
// reaches the TUI.
func TestChannelApproverSessionGrant(t *testing.T) {
	reqCh := make(chan approvalReq, 1)
	ap := newChannelApprover(reqCh)
	prompts := 0
	go func() {
		for req := range reqCh {
			prompts++
			req.reply <- approvalResp{allow: true, session: true}
		}
	}()

	for i := 0; i < 3; i++ {
		allow, _, err := ap.Approve(context.Background(), "bash", "go build", nil)
		if err != nil || !allow {
			t.Fatalf("call %d = (%v,%v), want allow", i, allow, err)
		}
	}
	if prompts != 1 {
		t.Errorf("prompted %d times, want 1 (session grant should short-circuit)", prompts)
	}
}

// TestChannelApproverCtxCancel ensures a cancelled turn unblocks Approve with an
// error (rather than hanging) when no one is reading the request channel.
func TestChannelApproverCtxCancel(t *testing.T) {
	reqCh := make(chan approvalReq) // unbuffered + no reader → send blocks
	ap := newChannelApprover(reqCh)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	allow, _, err := ap.Approve(ctx, "bash", "x", nil)
	if err == nil || allow {
		t.Fatalf("Approve on cancelled ctx = (%v,%v), want (false, error)", allow, err)
	}
}
