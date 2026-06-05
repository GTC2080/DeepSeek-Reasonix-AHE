package lab

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/harness"
)

const DefaultHarnessActionRoot = DefaultAHERoot + "/harness-actions"

type HarnessSnapshotActionOptions struct {
	SnapshotID  string
	HarnessRoot string
	OutputRoot  string
	Activate    bool
	Pin         bool
	Now         func() time.Time
	AttemptID   string
}

type HarnessSnapshotActionResult struct {
	Action         string    `json:"action"`
	SnapshotID     string    `json:"snapshot_id"`
	SafetySnapshot string    `json:"safety_snapshot,omitempty"`
	AttemptID      string    `json:"attempt_id"`
	AttemptDir     string    `json:"attempt_dir"`
	ResultPath     string    `json:"result_path"`
	CreatedAt      time.Time `json:"created_at"`
	Activated      bool      `json:"activated,omitempty"`
	Pinned         bool      `json:"pinned,omitempty"`
	Error          string    `json:"error,omitempty"`
}

func PromoteHarnessSnapshot(opts HarnessSnapshotActionOptions) (HarnessSnapshotActionResult, error) {
	return runHarnessSnapshotAction("promote", opts)
}

func RollbackHarnessSnapshot(opts HarnessSnapshotActionOptions) (HarnessSnapshotActionResult, error) {
	return runHarnessSnapshotAction("rollback", opts)
}

func runHarnessSnapshotAction(action string, opts HarnessSnapshotActionOptions) (HarnessSnapshotActionResult, error) {
	id := strings.TrimSpace(opts.SnapshotID)
	if id == "" {
		return HarnessSnapshotActionResult{}, fmt.Errorf("snapshot id is required")
	}
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	createdAt := now()
	attemptID := strings.TrimSpace(opts.AttemptID)
	if attemptID == "" {
		attemptID = harnessActionAttemptID(action, createdAt)
	}
	outputRoot := strings.TrimSpace(opts.OutputRoot)
	if outputRoot == "" {
		outputRoot = DefaultHarnessActionRoot
	}
	attemptDir := filepath.Join(outputRoot, attemptID)
	result := HarnessSnapshotActionResult{
		Action:     action,
		SnapshotID: id,
		AttemptID:  attemptID,
		AttemptDir: attemptDir,
		ResultPath: filepath.Join(attemptDir, "result.json"),
		CreatedAt:  createdAt,
	}
	finish := func(err error) (HarnessSnapshotActionResult, error) {
		if err != nil {
			result.Error = err.Error()
		}
		if writeErr := writeJSON(result.ResultPath, result); writeErr != nil && err == nil {
			err = writeErr
		}
		return result, err
	}

	layout := harness.NewLayout(opts.HarnessRoot)
	if _, err := layout.LoadSnapshotSource(id); err != nil {
		return finish(err)
	}
	safety, err := layout.CreateSnapshot(createdAt)
	if err != nil {
		return finish(err)
	}
	result.SafetySnapshot = safety.SnapshotID
	if err := layout.ReplaceSourceWithSnapshot(id); err != nil {
		return finish(err)
	}
	if opts.Activate {
		if err := layout.Activate(id); err != nil {
			return finish(err)
		}
		result.Activated = true
	}
	if opts.Pin {
		if err := layout.Pin(id); err != nil {
			return finish(err)
		}
		result.Pinned = true
	}
	return finish(nil)
}

func harnessActionAttemptID(action string, createdAt time.Time) string {
	var b [3]byte
	if _, err := rand.Read(b[:]); err == nil {
		return fmt.Sprintf("%s-%s-%s", action, createdAt.Format("20060102-150405"), hex.EncodeToString(b[:]))
	}
	return fmt.Sprintf("%s-%s", action, createdAt.Format("20060102-150405"))
}
