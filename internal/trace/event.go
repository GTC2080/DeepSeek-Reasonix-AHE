// Package trace writes the agent's typed event stream as JSONL for local
// observability and eval tooling.
package trace

import "time"

const Version = "trace.v0.1"

// Event is one JSONL trace record.
type Event struct {
	Version   string         `json:"version"`
	RunID     string         `json:"run_id"`
	SessionID string         `json:"session_id"`
	Seq       int64          `json:"seq"`
	Type      string         `json:"type"`
	Time      time.Time      `json:"time"`
	Turn      int            `json:"turn"`
	Data      map[string]any `json:"data,omitempty"`
}
