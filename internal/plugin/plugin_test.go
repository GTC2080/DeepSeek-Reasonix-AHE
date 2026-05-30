package plugin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

// TestStdioEndToEnd drives a real subprocess (this test binary re-invoked in
// helper mode) through the full MCP handshake and a tool call, exercising
// StartAll, tools/list, and tools/call over stdio JSON-RPC.
func TestStdioEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	spec := Spec{
		Name:    "mock",
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcess", "--"},
		Env:     map[string]string{"GO_WANT_HELPER_PROCESS": "1"},
	}

	host, tools, err := StartAll(ctx, []Spec{spec})
	if err != nil {
		t.Fatalf("StartAll: %v", err)
	}
	defer host.Close()

	if len(tools) != 1 {
		t.Fatalf("want 1 tool, got %d", len(tools))
	}
	if got := tools[0].Name(); got != "mcp__mock__echo" {
		t.Fatalf("tool name: want mcp__mock__echo, got %q", got)
	}

	out, err := tools[0].Execute(ctx, json.RawMessage(`{"msg":"hi"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out != "echo: hi" {
		t.Fatalf("result: want %q, got %q", "echo: hi", out)
	}
}

// TestHelperProcess is not a real test; it acts as a minimal MCP stdio server
// when invoked by TestStdioEndToEnd. It exits before the test framework can
// print to stdout, keeping the JSON-RPC channel clean.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	defer os.Exit(0)

	in := bufio.NewReader(os.Stdin)
	for {
		line, err := in.ReadBytes('\n')
		if err != nil {
			return
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		var req struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		if req.ID == nil {
			continue // notification: no response
		}

		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": protocolVersion,
				"serverInfo":      map[string]any{"name": "mock", "version": "0"},
			}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{
				"name":        "echo",
				"description": "Echo back the message.",
				"inputSchema": map[string]any{
					"type":       "object",
					"properties": map[string]any{"msg": map[string]any{"type": "string"}},
				},
			}}}
		case "tools/call":
			var p struct {
				Arguments struct {
					Msg string `json:"msg"`
				} `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &p)
			result = map[string]any{"content": []map[string]any{
				{"type": "text", "text": "echo: " + p.Arguments.Msg},
			}}
		}

		resp := map[string]any{"jsonrpc": "2.0", "id": *req.ID, "result": result}
		b, _ := json.Marshal(resp)
		os.Stdout.Write(append(b, '\n'))
	}
}
