// Package command loads custom slash commands from Markdown files. A command is
// a prompt template: invoking /name substitutes the arguments into the body and
// sends the result as a chat turn. Loading is pure and dependency-free — a small
// "key: value" frontmatter parser keeps Reasonix's single-(TOML)-dependency promise
// rather than pulling in a YAML library.
package command

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Command is a custom slash command loaded from a .md file.
type Command struct {
	Name        string // "review" or "git:commit", derived from the file path
	Description string // from frontmatter
	ArgHint     string // from frontmatter (argument-hint)
	Body        string // template with $ARGUMENTS / $1..$N / $$
	Source      string // originating file path, for diagnostics
}

// substRe matches the substitution tokens recognised in a command body.
var substRe = regexp.MustCompile(`\$(\$|ARGUMENTS|[0-9]+)`)

// Render substitutes args into the command body: $ARGUMENTS is all args joined
// by spaces, $1..$N are positional (empty when absent), and $$ is a literal $.
func (c Command) Render(args []string) string {
	return substRe.ReplaceAllStringFunc(c.Body, func(m string) string {
		switch tok := m[1:]; tok {
		case "$":
			return "$"
		case "ARGUMENTS":
			return strings.Join(args, " ")
		default:
			n, _ := strconv.Atoi(tok) // regex guarantees digits
			if n >= 1 && n <= len(args) {
				return args[n-1]
			}
			return ""
		}
	})
}

// Load reads every *.md command file under each dir, in order, so a later dir
// overrides an earlier one on a name clash (pass the user dir first, project
// dir last). Missing dirs are skipped. Individual file failures are collected
// into the returned error but don't prevent the others from loading. The result
// is sorted by name.
func Load(dirs ...string) ([]Command, error) {
	byName := map[string]Command{}
	var errs []string
	for _, dir := range dirs {
		root, err := filepath.Abs(dir)
		if err != nil {
			continue
		}
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, werr error) error {
			if werr != nil {
				return nil // missing dir / unreadable entry: skip, don't abort
			}
			if d.IsDir() || !strings.EqualFold(filepath.Ext(d.Name()), ".md") {
				return nil
			}
			c, perr := parseFile(root, path)
			if perr != nil {
				errs = append(errs, perr.Error())
				return nil
			}
			byName[c.Name] = c
			return nil
		})
	}
	cmds := make([]Command, 0, len(byName))
	for _, c := range byName {
		cmds = append(cmds, c)
	}
	sort.Slice(cmds, func(i, j int) bool { return cmds[i].Name < cmds[j].Name })
	if len(errs) > 0 {
		return cmds, fmt.Errorf("command load: %s", strings.Join(errs, "; "))
	}
	return cmds, nil
}

// parseFile reads one command file and derives its name from the path relative
// to root: drop the .md suffix and turn subdirectories into ":" namespaces
// (git/commit.md → git:commit).
func parseFile(root, path string) (Command, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Command{}, err
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = filepath.Base(path)
	}
	name := strings.ReplaceAll(strings.TrimSuffix(filepath.ToSlash(rel), ".md"), "/", ":")

	// Normalise line endings and strip a leading UTF-8 BOM if present.
	content := strings.TrimPrefix(strings.ReplaceAll(string(b), "\r\n", "\n"), string(rune(0xFEFF)))
	fm, body := splitFrontmatter(content)
	return Command{
		Name:        name,
		Description: fm["description"],
		ArgHint:     fm["argument-hint"],
		Body:        strings.TrimSpace(body),
		Source:      path,
	}, nil
}

// splitFrontmatter separates an optional leading ---fenced block of simple
// "key: value" lines from the body. Returns the parsed keys (lowercased) and the
// remaining body. With no opening/closing fence, the whole input is the body.
func splitFrontmatter(s string) (map[string]string, string) {
	fm := map[string]string{}
	lines := strings.Split(s, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return fm, s
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "---" {
			continue
		}
		for _, l := range lines[1:i] {
			if k, v, ok := strings.Cut(l, ":"); ok {
				fm[strings.ToLower(strings.TrimSpace(k))] = strings.Trim(strings.TrimSpace(v), `"'`)
			}
		}
		return fm, strings.Join(lines[i+1:], "\n")
	}
	return fm, s // opened but never closed: treat all as body
}
