package trace

import (
	"fmt"
	"strings"
)

// Mode controls how much body text is included in trace data.
type Mode string

const (
	ModeMetadata Mode = "metadata"
	ModePreview  Mode = "preview"
	ModeFull     Mode = "full"
)

const DefaultMode = ModePreview

// ParseMode parses a CLI/env mode value. Empty means DefaultMode.
func ParseMode(raw string) (Mode, error) {
	switch m := Mode(strings.ToLower(strings.TrimSpace(raw))); m {
	case "":
		return DefaultMode, nil
	case ModeMetadata, ModePreview, ModeFull:
		return m, nil
	default:
		return "", fmt.Errorf("invalid trace mode %q (want metadata, preview, or full)", raw)
	}
}
