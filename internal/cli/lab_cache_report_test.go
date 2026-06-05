package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunDispatchesLabCacheReport(t *testing.T) {
	tempChdir(t)
	tracePath := writeCLICacheTrace(t, false)

	out := captureStdout(t, func() {
		if rc := Run([]string{"lab", "cache-report", tracePath}, "test-version"); rc != 0 {
			t.Fatalf("lab cache-report rc = %d, want 0", rc)
		}
	})
	if !strings.Contains(out, "Reasonix-AHE Cache Report") ||
		!strings.Contains(out, "Model calls:") ||
		!strings.Contains(out, "Cache hit ratio:") ||
		!strings.Contains(out, "Stable prefix hash drift:   yes") {
		t.Fatalf("cache report output missing expected lines:\n%s", out)
	}
}

func TestRunDispatchesLabCacheReportJSON(t *testing.T) {
	tempChdir(t)
	tracePath := writeCLICacheTrace(t, true)

	out := captureStdout(t, func() {
		if rc := Run([]string{"lab", "cache-report", tracePath, "--json"}, "test-version"); rc != 0 {
			t.Fatalf("lab cache-report --json rc = %d, want 0", rc)
		}
	})
	var decoded map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("cache report JSON is invalid: %v\n%s", err, out)
	}
	if decoded["model_calls"] != float64(1) {
		t.Fatalf("model_calls = %v, want 1", decoded["model_calls"])
	}
	if decoded["contract_violations"] != float64(1) {
		t.Fatalf("contract_violations = %v, want 1", decoded["contract_violations"])
	}
}

func TestLabCacheReportGateRequiresExplicitGate(t *testing.T) {
	tempChdir(t)
	tracePath := writeCLICacheTrace(t, true)

	if rc := Run([]string{"lab", "cache-report", tracePath, "--max-contract-violations", "0"}, "test-version"); rc != 0 {
		t.Fatalf("cache-report without --gate rc = %d, want 0", rc)
	}
	if rc := Run([]string{"lab", "cache-report", tracePath, "--gate", "--max-contract-violations", "0"}, "test-version"); rc != 1 {
		t.Fatalf("cache-report violation gate rc = %d, want 1", rc)
	}
	if rc := Run([]string{"lab", "cache-report", tracePath, "--gate", "--min-hit-ratio", "0.90"}, "test-version"); rc != 1 {
		t.Fatalf("cache-report ratio gate rc = %d, want 1", rc)
	}
}

func TestLabCacheReportRejectsBadArguments(t *testing.T) {
	tempChdir(t)
	for _, args := range [][]string{
		{"lab", "cache-report"},
		{"lab", "cache-report", "trace.jsonl", "--min-hit-ratio", "loud"},
		{"lab", "cache-report", "trace.jsonl", "--max-contract-violations", "-1"},
		{"lab", "cache-report", "trace.jsonl", "--unknown"},
	} {
		if rc := Run(args, "test-version"); rc != 2 {
			t.Fatalf("Run(%v) rc = %d, want 2", args, rc)
		}
	}
}

func writeCLICacheTrace(t *testing.T, violation bool) string {
	t.Helper()
	tracePath := filepath.Join(t.TempDir(), "trace.jsonl")
	body := `{"version":"trace.v0.1","run_id":"r","session_id":"s","seq":1,"type":"session_start","time":"2026-06-05T00:00:00Z","turn":0,"data":{"harness_snapshot":"h-0001"}}
{"version":"trace.v0.1","run_id":"r","session_id":"s","seq":2,"type":"model_response","time":"2026-06-05T00:00:00Z","turn":1,"data":{}}
{"version":"trace.v0.1","run_id":"r","session_id":"s","seq":3,"type":"cache_stats","time":"2026-06-05T00:00:00Z","turn":1,"data":{"prompt_cache_hit_tokens":50,"prompt_cache_miss_tokens":50,"prefix_changed":true,"prefix_change_reasons":["tools"]}}
`
	if violation {
		body += `{"version":"trace.v0.1","run_id":"r","session_id":"s","seq":4,"type":"cache_contract_violation","time":"2026-06-05T00:00:00Z","turn":1,"data":{"reasons":["tool_schema_hash"]}}
`
	}
	if err := os.WriteFile(tracePath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return tracePath
}
