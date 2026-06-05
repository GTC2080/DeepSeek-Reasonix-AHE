package trace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Writer consumes trace events.
type Writer interface {
	WriteEvent(Event) error
	Close() error
}

// JSONLWriter appends trace events to a JSON Lines file.
type JSONLWriter struct {
	mu sync.Mutex
	f  *os.File
}

// OpenJSONL creates parent directories and opens path in append mode.
func OpenJSONL(path string) (*JSONLWriter, error) {
	if path == "" {
		return nil, errors.New("trace path is empty")
	}
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &JSONLWriter{f: f}, nil
}

// WriteEvent writes one JSON object followed by a newline.
func (w *JSONLWriter) WriteEvent(e Event) error {
	if w == nil || w.f == nil {
		return errors.New("trace writer is closed")
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.f.Write(b)
	if err != nil {
		return err
	}
	if n != len(b) {
		return fmt.Errorf("short trace write: %d of %d bytes", n, len(b))
	}
	return nil
}

// Close closes the underlying file.
func (w *JSONLWriter) Close() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}

// Noop is a Writer that discards trace events.
type Noop struct{}

func (Noop) WriteEvent(Event) error { return nil }
func (Noop) Close() error           { return nil }
