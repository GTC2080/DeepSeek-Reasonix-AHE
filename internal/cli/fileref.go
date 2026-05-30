package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// maxFileRefBytes caps how much of an @-referenced file is injected into a
// message, so "@somehuge.log" can't blow the context window. The head is kept
// and the rest noted as truncated.
const maxFileRefBytes = 64 * 1024

// refKind distinguishes the two things an @reference can resolve to.
type refKind int

const (
	refResource refKind = iota // an MCP resource: @<server>:<uri>
	refFile                    // a local file or directory: @<path>
)

// ref is a resolved @reference found in a submitted line.
type ref struct {
	kind   refKind
	server string // refResource
	uri    string // refResource
	path   string // refFile
	raw    string // the original token after '@', for labelling
}

// refTokenRe matches an @reference token: '@' then a run of non-space chars.
var refTokenRe = regexp.MustCompile(`@([^\s]+)`)

// parseRefTokens extracts the deduped, punctuation-trimmed tokens following '@'
// in a line. Pure: classification (server? file?) happens in classifyRef.
func parseRefTokens(line string) []string {
	var toks []string
	seen := map[string]bool{}
	for _, g := range refTokenRe.FindAllStringSubmatch(line, -1) {
		t := strings.TrimRight(g[1], ".,;!?)]}")
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		toks = append(toks, t)
	}
	return toks
}

// classifyRef decides what a token refers to. A "server:uri" token whose server
// is connected is an MCP resource; otherwise a token that names an existing path
// is a file. Anything else (an @mention, an email) is not a reference. exists is
// injected so the rule is testable without touching the filesystem.
func classifyRef(token string, known map[string]bool, exists func(string) bool) (ref, bool) {
	if i := strings.Index(token, ":"); i > 0 && i+1 < len(token) && known[token[:i]] {
		return ref{kind: refResource, server: token[:i], uri: token[i+1:], raw: token}, true
	}
	if exists(token) {
		return ref{kind: refFile, path: token, raw: token}, true
	}
	return ref{}, false
}

// detectRefs finds the @references in a submitted line: MCP resources for
// connected servers, and local paths that exist on disk.
func (m *chatTUI) detectRefs(line string) []ref {
	known := map[string]bool{}
	if m.host != nil {
		for _, n := range m.host.ServerNames() {
			known[n] = true
		}
	}
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }

	var refs []ref
	for _, tok := range parseRefTokens(line) {
		if r, ok := classifyRef(tok, known, exists); ok {
			refs = append(refs, r)
		}
	}
	return refs
}

// fetchRefs resolves each reference off the event loop and returns a
// refsResolvedMsg with the concatenated, tagged content (or per-ref errors).
func (m *chatTUI) fetchRefs(line string, refs []ref) tea.Cmd {
	host := m.host
	return func() tea.Msg {
		var b strings.Builder
		var errs []string
		for _, r := range refs {
			switch r.kind {
			case refResource:
				text, err := host.ReadResource(context.Background(), r.server, r.uri)
				if err != nil {
					errs = append(errs, "@"+r.raw+" — "+err.Error())
					continue
				}
				appendRefBlock(&b, "resource", `ref="@`+r.raw+`"`, text)
			case refFile:
				text, isDir, err := readFileRef(r.path)
				if err != nil {
					errs = append(errs, "@"+r.raw+" — "+err.Error())
					continue
				}
				tag := "file"
				if isDir {
					tag = "dir"
				}
				appendRefBlock(&b, tag, `path="`+r.path+`"`, text)
			}
		}
		return refsResolvedMsg{line: line, block: b.String(), errs: errs}
	}
}

func appendRefBlock(b *strings.Builder, tag, attr, body string) {
	if b.Len() > 0 {
		b.WriteString("\n\n")
	}
	fmt.Fprintf(b, "<%s %s>\n%s\n</%s>", tag, attr, body, tag)
}

// readFileRef reads an @-referenced path for injection. A directory yields a
// one-level listing; a binary file (NUL in the first 8 KiB) is noted rather than
// dumped; a large file is truncated to maxFileRefBytes with a marker. isDir lets
// the caller pick the wrapping tag.
func readFileRef(path string) (content string, isDir bool, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", false, err
	}
	if info.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil {
			return "", true, err
		}
		var b strings.Builder
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() {
				name += "/"
			}
			b.WriteString(name)
			b.WriteByte('\n')
		}
		return b.String(), true, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()

	buf := make([]byte, maxFileRefBytes+1)
	n, rerr := io.ReadFull(f, buf)
	if rerr != nil && rerr != io.ErrUnexpectedEOF && rerr != io.EOF {
		return "", false, rerr
	}
	data := buf[:n]

	if bytes.IndexByte(data[:min(n, 8192)], 0) >= 0 {
		return fmt.Sprintf("[binary file %s, %d bytes — not shown]", path, info.Size()), false, nil
	}
	if n > maxFileRefBytes {
		return string(data[:maxFileRefBytes]) + fmt.Sprintf("\n…[truncated; file is %d bytes]…", info.Size()), false, nil
	}
	return string(data), false, nil
}
