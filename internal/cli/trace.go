package cli

import (
	"os"
	"path/filepath"
	"strings"

	"reasonix/internal/event"
	"reasonix/internal/harness"
	"reasonix/internal/trace"
)

const (
	tracePathEnv = "REASONIX_TRACE"
	traceModeEnv = "REASONIX_TRACE_MODE"
)

type traceCLIConfig struct {
	Enabled bool
	Path    string
	Mode    trace.Mode
}

func resolveTraceConfig(flagPath, flagMode string) (traceCLIConfig, error) {
	path := strings.TrimSpace(flagPath)
	if path == "" {
		path = strings.TrimSpace(os.Getenv(tracePathEnv))
	}
	modeRaw := strings.TrimSpace(flagMode)
	if modeRaw == "" {
		modeRaw = os.Getenv(traceModeEnv)
	}
	mode, err := trace.ParseMode(modeRaw)
	if err != nil {
		return traceCLIConfig{}, err
	}
	if path == "" {
		return traceCLIConfig{Mode: mode}, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return traceCLIConfig{}, err
	}
	return traceCLIConfig{Enabled: true, Path: abs, Mode: mode}, nil
}

func wrapTraceSink(inner event.Sink, cfg traceCLIConfig) (event.Sink, *trace.Sink, error) {
	if !cfg.Enabled {
		return inner, nil, nil
	}
	w, err := trace.OpenJSONL(cfg.Path)
	if err != nil {
		return nil, nil, err
	}
	activeSnapshot, err := harness.DefaultLayout().Active()
	if err != nil {
		_ = w.Close()
		return nil, nil, err
	}
	var activeStablePrefixHash string
	if activeSnapshot != "" {
		lock, err := harness.DefaultLayout().Inspect(activeSnapshot)
		if err != nil {
			_ = w.Close()
			return nil, nil, err
		}
		activeStablePrefixHash = lock.StablePrefixHash
	}
	s := trace.NewSink(inner, w, trace.Options{
		Mode:                    cfg.Mode,
		HarnessSnapshot:         activeSnapshot,
		HarnessStablePrefixHash: activeStablePrefixHash,
	})
	return s, s, nil
}
