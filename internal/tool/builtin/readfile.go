// Package builtin provides Reasonix's compile-time built-in tools. Each tool
// self-registers via init(); main blank-imports this package to wire them in.
package builtin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"reasonix/internal/tool"
)

func init() { tool.RegisterBuiltin(readFile{}) }

type readFile struct{}

const (
	readFileDefaultLimit = 2000     // lines returned when limit is unset
	readFileBinaryPeek   = 8 * 1024 // bytes scanned for a NUL to flag binary
)

func (readFile) Name() string { return "read_file" }

func (readFile) Description() string {
	return "Read a text file with optional line offset/limit. Output prefixes each line with its 1-based number (e.g. `   42→...`) so subsequent edit_file calls can target exact lines. Use `offset` and `limit` to page through large files; the tool reports total length and pagination hints in a trailer."
}

func (readFile) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"properties":{
  "path":{"type":"string","description":"File path"},
  "offset":{"type":"integer","description":"0-based line offset to start reading from (default 0)","minimum":0},
  "limit":{"type":"integer","description":"Maximum lines to return (default 2000)","minimum":1}
},
"required":["path"]
}`)
}

func (readFile) ReadOnly() bool { return true }

func (readFile) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p struct {
		Path   string `json:"path"`
		Offset int    `json:"offset,omitempty"`
		Limit  int    `json:"limit,omitempty"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid args: %w", err)
	}
	if p.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	if p.Offset < 0 {
		p.Offset = 0
	}
	if p.Limit <= 0 {
		p.Limit = readFileDefaultLimit
	}

	f, err := os.Open(p.Path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", p.Path, err)
	}
	defer f.Close()

	// Refuse binary files up front. A NUL byte anywhere in the leading 8 KB
	// is the cheapest reliable signal — UTF-16 / textual config files don't
	// embed NULs in the way executables, archives, or images do.
	peek := make([]byte, readFileBinaryPeek)
	n, _ := io.ReadFull(f, peek)
	if bytes.IndexByte(peek[:n], 0) >= 0 {
		return "", fmt.Errorf("binary file %s (NUL byte detected); use `bash hexdump` or another tool", p.Path)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("seek %s: %w", p.Path, err)
	}

	// Scan up to offset+limit+1 lines (the extra is just to know whether
	// trimming a trailer is warranted). 1 MB per-line cap matches what other
	// scanners in this package allow — well above any reasonable source line.
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	upTo := p.Offset + p.Limit + 1

	var collected []string
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		if lineNo > p.Offset && len(collected) < p.Limit {
			collected = append(collected, scanner.Text())
		}
		if lineNo >= upTo {
			// Keep counting to know how many more lines remain.
			break
		}
	}
	// Drain any remainder to learn the true total without buffering the rest.
	remaining := 0
	for scanner.Scan() {
		remaining++
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read %s: %w", p.Path, err)
	}
	totalSeen := lineNo + remaining

	if totalSeen == 0 {
		return "(empty file)", nil
	}
	if len(collected) == 0 {
		return fmt.Sprintf("(offset %d is past EOF — file has %d lines)", p.Offset, totalSeen), nil
	}

	// Right-align line numbers to the largest one we'll print, so the arrow
	// "→" column lines up. Add 1 for the 1-based display.
	maxShown := p.Offset + len(collected)
	w := len(fmt.Sprint(maxShown))

	var b strings.Builder
	for i, line := range collected {
		fmt.Fprintf(&b, "%*d→%s\n", w, p.Offset+i+1, line)
	}
	more := totalSeen - (p.Offset + len(collected))
	if more > 0 {
		fmt.Fprintf(&b, "\n[%d more line(s); pass offset=%d to continue]\n",
			more, p.Offset+len(collected))
	}
	return b.String(), nil
}
