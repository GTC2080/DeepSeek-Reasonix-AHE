// Package harness manages local Reasonix-AHE harness source snapshots.
package harness

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"reasonix/internal/harnesspolicy"
)

const (
	RootDir  = ".reasonix-harness"
	LockFile = "harness.lock"
)

var sourceDirs = []string{
	"prompts",
	"tool_descriptions",
	"skills",
	"middleware",
	"routing",
}

// Layout is the local harness workspace rooted at .reasonix-harness.
type Layout struct {
	Root string
}

// Lock is the stable metadata written for one harness snapshot.
type Lock struct {
	SnapshotID          string    `json:"snapshot_id"`
	CreatedAt           time.Time `json:"created_at"`
	SystemPromptHash    string    `json:"system_prompt_hash"`
	ToolDescriptionHash string    `json:"tool_description_hash"`
	SkillIndexHash      string    `json:"skill_index_hash"`
	MiddlewareHash      string    `json:"middleware_hash"`
	ModelRoutingHash    string    `json:"model_routing_hash"`
	StablePrefixHash    string    `json:"stable_prefix_hash"`
}

// ActiveSnapshot is the loaded, provider-facing content for the currently
// active harness snapshot.
type ActiveSnapshot struct {
	SnapshotID       string
	Lock             Lock
	SourceDir        string
	PromptOverlay    string
	ToolDescriptions map[string]string
	Policies         harnesspolicy.PolicySet
}

// NewLayout returns a harness layout rooted at root, or .reasonix-harness when
// root is empty.
func NewLayout(root string) Layout {
	if strings.TrimSpace(root) == "" {
		root = RootDir
	}
	return Layout{Root: root}
}

// DefaultLayout returns the project-local harness layout.
func DefaultLayout() Layout {
	return NewLayout("")
}

func (l Layout) SourceDir() string {
	return filepath.Join(l.Root, "source")
}

func (l Layout) SnapshotsDir() string {
	return filepath.Join(l.Root, "snapshots")
}

func (l Layout) ManifestsDir() string {
	return filepath.Join(l.Root, "manifests")
}

func (l Layout) ActivePath() string {
	return filepath.Join(l.Root, "active")
}

func (l Layout) PinnedPath() string {
	return filepath.Join(l.Root, "pinned")
}

func (l Layout) SnapshotDir(id string) string {
	return filepath.Join(l.SnapshotsDir(), id)
}

func (l Layout) SnapshotSourceDir(id string) string {
	return filepath.Join(l.SnapshotDir(id), "source")
}

// Init creates the local harness source, snapshot, and manifest directories.
func (l Layout) Init() error {
	for _, dir := range []string{l.SourceDir(), l.SnapshotsDir(), l.ManifestsDir()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	for _, dir := range sourceDirs {
		if err := os.MkdirAll(filepath.Join(l.SourceDir(), dir), 0o755); err != nil {
			return err
		}
	}
	return nil
}

// CreateSnapshot writes the next h-NNNN snapshot lock from the current source.
func (l Layout) CreateSnapshot(createdAt time.Time) (Lock, error) {
	if err := l.Init(); err != nil {
		return Lock{}, err
	}
	return l.CreateSnapshotFromSource(l.SourceDir(), createdAt)
}

// CreateSnapshotFromSource writes the next h-NNNN snapshot from sourceDir. New
// snapshots include both harness.lock and a source/ copy.
func (l Layout) CreateSnapshotFromSource(sourceDir string, createdAt time.Time) (Lock, error) {
	if strings.TrimSpace(sourceDir) == "" {
		return Lock{}, fmt.Errorf("source dir is required")
	}
	if err := l.ensureSnapshotLayout(); err != nil {
		return Lock{}, err
	}
	if _, err := harnesspolicy.LoadDir(filepath.Join(sourceDir, "middleware")); err != nil {
		return Lock{}, err
	}
	id, err := l.nextSnapshotID()
	if err != nil {
		return Lock{}, err
	}
	lock, err := CaptureSourceLock(sourceDir, id, createdAt)
	if err != nil {
		return Lock{}, err
	}
	dir := l.SnapshotDir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Lock{}, err
	}
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(dir)
		}
	}()
	if err := writeLock(filepath.Join(dir, LockFile), lock); err != nil {
		return Lock{}, err
	}
	if err := copyDir(sourceDir, l.SnapshotSourceDir(id)); err != nil {
		return Lock{}, err
	}
	success = true
	return lock, nil
}

// ListSnapshots reads all snapshot locks in id order.
func (l Layout) ListSnapshots() ([]Lock, error) {
	entries, err := os.ReadDir(l.SnapshotsDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && snapshotIDRE.MatchString(entry.Name()) {
			ids = append(ids, entry.Name())
		}
	}
	sort.Strings(ids)
	out := make([]Lock, 0, len(ids))
	for _, id := range ids {
		lock, err := l.Inspect(id)
		if err != nil {
			return nil, err
		}
		out = append(out, lock)
	}
	return out, nil
}

// Activate writes the active snapshot id.
func (l Layout) Activate(id string) error {
	if _, err := l.Inspect(id); err != nil {
		return err
	}
	if err := os.MkdirAll(l.Root, 0o755); err != nil {
		return err
	}
	return os.WriteFile(l.ActivePath(), []byte(id+"\n"), 0o644)
}

// Active returns the active snapshot id. A missing active file is not an error.
func (l Layout) Active() (string, error) {
	b, err := os.ReadFile(l.ActivePath())
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// ClearActive removes the active snapshot marker. Missing active marker is not
// an error.
func (l Layout) ClearActive() error {
	if err := os.Remove(l.ActivePath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// LoadActive reads the active snapshot and renders its provider-facing overlay.
// Missing active marker means no active harness and is not an error.
func (l Layout) LoadActive() (ActiveSnapshot, error) {
	id, err := l.Active()
	if err != nil {
		return ActiveSnapshot{}, err
	}
	if id == "" {
		return ActiveSnapshot{}, nil
	}
	return l.LoadSnapshotSource(id)
}

// LoadSnapshotSource reads a snapshot's lock and source copy.
func (l Layout) LoadSnapshotSource(id string) (ActiveSnapshot, error) {
	lock, err := l.Inspect(id)
	if err != nil {
		return ActiveSnapshot{}, err
	}
	sourceDir := l.SnapshotSourceDir(id)
	if !dirExists(sourceDir) {
		return ActiveSnapshot{}, fmt.Errorf("snapshot %s has no source copy", id)
	}
	actual, err := CaptureSourceLock(sourceDir, lock.SnapshotID, lock.CreatedAt)
	if err != nil {
		return ActiveSnapshot{}, err
	}
	if !sameLockHashes(lock, actual) {
		return ActiveSnapshot{}, fmt.Errorf("snapshot %s source does not match harness.lock", id)
	}
	overlay, err := renderPromptOverlay(sourceDir)
	if err != nil {
		return ActiveSnapshot{}, err
	}
	descriptions, err := readToolDescriptions(filepath.Join(sourceDir, "tool_descriptions"))
	if err != nil {
		return ActiveSnapshot{}, err
	}
	policies, err := harnesspolicy.LoadDir(filepath.Join(sourceDir, "middleware"))
	if err != nil {
		return ActiveSnapshot{}, err
	}
	return ActiveSnapshot{
		SnapshotID:       id,
		Lock:             lock,
		SourceDir:        sourceDir,
		PromptOverlay:    overlay,
		ToolDescriptions: descriptions,
		Policies:         policies,
	}, nil
}

// ReplaceSourceWithSnapshot replaces the editable source tree with a snapshot's
// source copy. The caller is responsible for creating any safety snapshot.
func (l Layout) ReplaceSourceWithSnapshot(id string) error {
	if _, err := l.Inspect(id); err != nil {
		return err
	}
	snapshotSource := l.SnapshotSourceDir(id)
	if !dirExists(snapshotSource) {
		return fmt.Errorf("snapshot %s has no source copy", id)
	}
	if err := os.MkdirAll(l.Root, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(l.Root, ".source-replace-")
	if err != nil {
		return err
	}
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(tmp)
		}
	}()
	if err := copyDir(snapshotSource, tmp); err != nil {
		return err
	}
	if err := os.RemoveAll(l.SourceDir()); err != nil {
		return err
	}
	if err := os.Rename(tmp, l.SourceDir()); err != nil {
		return err
	}
	success = true
	return nil
}

// Pinned returns the sorted unique snapshot ids in the pinned file. Missing
// pinned file means no pins.
func (l Layout) Pinned() ([]string, error) {
	b, err := os.ReadFile(l.PinnedPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !snapshotIDRE.MatchString(line) {
			continue
		}
		seen[line] = true
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

// Pin adds a snapshot to the pinned set. The snapshot must exist.
func (l Layout) Pin(id string) error {
	if _, err := l.Inspect(id); err != nil {
		return err
	}
	pinned, err := l.Pinned()
	if err != nil {
		return err
	}
	seen := map[string]bool{id: true}
	for _, existing := range pinned {
		seen[existing] = true
	}
	out := make([]string, 0, len(seen))
	for pin := range seen {
		out = append(out, pin)
	}
	sort.Strings(out)
	return l.writePinned(out)
}

// Unpin removes a snapshot from the pinned set. Removing a missing pin is
// idempotent.
func (l Layout) Unpin(id string) error {
	pinned, err := l.Pinned()
	if err != nil {
		return err
	}
	out := make([]string, 0, len(pinned))
	for _, existing := range pinned {
		if existing != id {
			out = append(out, existing)
		}
	}
	return l.writePinned(out)
}

// Inspect reads one snapshot lock.
func (l Layout) Inspect(id string) (Lock, error) {
	path := filepath.Join(l.SnapshotDir(id), LockFile)
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Lock{}, fmt.Errorf("snapshot %s not found", id)
	}
	if err != nil {
		return Lock{}, err
	}
	var lock Lock
	if err := json.Unmarshal(b, &lock); err != nil {
		return Lock{}, err
	}
	return lock, nil
}

func (l Layout) writePinned(ids []string) error {
	if err := os.MkdirAll(l.Root, 0o755); err != nil {
		return err
	}
	body := ""
	if len(ids) > 0 {
		body = strings.Join(ids, "\n") + "\n"
	}
	return os.WriteFile(l.PinnedPath(), []byte(body), 0o644)
}

func (l Layout) ensureSnapshotLayout() error {
	for _, dir := range []string{l.Root, l.SnapshotsDir(), l.ManifestsDir()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// CaptureSourceLock computes lock metadata for sourceDir without writing a
// snapshot.
func CaptureSourceLock(sourceDir, id string, createdAt time.Time) (Lock, error) {
	systemHash, err := hashTree(filepath.Join(sourceDir, "prompts"))
	if err != nil {
		return Lock{}, err
	}
	toolHash, err := hashTree(filepath.Join(sourceDir, "tool_descriptions"))
	if err != nil {
		return Lock{}, err
	}
	skillHash, err := hashTree(filepath.Join(sourceDir, "skills"))
	if err != nil {
		return Lock{}, err
	}
	middlewareHash, err := hashTree(filepath.Join(sourceDir, "middleware"))
	if err != nil {
		return Lock{}, err
	}
	routingHash, err := hashTree(filepath.Join(sourceDir, "routing"))
	if err != nil {
		return Lock{}, err
	}
	lock := Lock{
		SnapshotID:          id,
		CreatedAt:           createdAt,
		SystemPromptHash:    systemHash,
		ToolDescriptionHash: toolHash,
		SkillIndexHash:      skillHash,
		MiddlewareHash:      middlewareHash,
		ModelRoutingHash:    routingHash,
	}
	lock.StablePrefixHash = hashJSON(struct {
		SystemPromptHash    string `json:"system_prompt_hash"`
		ToolDescriptionHash string `json:"tool_description_hash"`
		SkillIndexHash      string `json:"skill_index_hash"`
		MiddlewareHash      string `json:"middleware_hash"`
		ModelRoutingHash    string `json:"model_routing_hash"`
	}{
		SystemPromptHash:    lock.SystemPromptHash,
		ToolDescriptionHash: lock.ToolDescriptionHash,
		SkillIndexHash:      lock.SkillIndexHash,
		MiddlewareHash:      lock.MiddlewareHash,
		ModelRoutingHash:    lock.ModelRoutingHash,
	})
	return lock, nil
}

var snapshotIDRE = regexp.MustCompile(`^h-(\d{4})$`)

func (l Layout) nextSnapshotID() (string, error) {
	entries, err := os.ReadDir(l.SnapshotsDir())
	if err != nil {
		return "", err
	}
	maxID := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		m := snapshotIDRE.FindStringSubmatch(entry.Name())
		if len(m) != 2 {
			continue
		}
		var n int
		if _, err := fmt.Sscanf(m[1], "%d", &n); err == nil && n > maxID {
			maxID = n
		}
	}
	return fmt.Sprintf("h-%04d", maxID+1), nil
}

func writeLock(path string, lock Lock) error {
	b, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func renderPromptOverlay(sourceDir string) (string, error) {
	type fragment struct {
		component string
		rel       string
		body      string
	}
	var fragments []fragment
	for _, component := range []string{"prompts", "middleware", "routing"} {
		root := filepath.Join(sourceDir, component)
		if !dirExists(root) {
			continue
		}
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !isHarnessTextFile(path) {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			body := strings.TrimSpace(string(b))
			if body == "" {
				return nil
			}
			fragments = append(fragments, fragment{
				component: component,
				rel:       filepath.ToSlash(rel),
				body:      body,
			})
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	if len(fragments) == 0 {
		return "", nil
	}
	sort.Slice(fragments, func(i, j int) bool {
		if fragments[i].component != fragments[j].component {
			return componentOrder(fragments[i].component) < componentOrder(fragments[j].component)
		}
		return fragments[i].rel < fragments[j].rel
	})
	var b strings.Builder
	b.WriteString("# AHE Harness Overlay")
	for _, f := range fragments {
		b.WriteString("\n\n## ")
		b.WriteString(f.component)
		b.WriteByte('/')
		b.WriteString(f.rel)
		b.WriteString("\n\n")
		b.WriteString(f.body)
	}
	return b.String(), nil
}

func readToolDescriptions(root string) (map[string]string, error) {
	out := map[string]string{}
	if !dirExists(root) {
		return out, nil
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !isHarnessTextFile(path) {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		ext := filepath.Ext(name)
		name = strings.TrimSuffix(name, ext)
		if name == "" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		body := strings.TrimSpace(string(b))
		if body != "" {
			out[name] = body
		}
		return nil
	})
	return out, err
}

func isHarnessTextFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".txt", ".toml":
		return true
	default:
		return false
	}
}

func componentOrder(component string) int {
	for i, name := range []string{"prompts", "middleware", "routing"} {
		if component == name {
			return i
		}
	}
	return 99
}

func sameLockHashes(a, b Lock) bool {
	return a.SystemPromptHash == b.SystemPromptHash &&
		a.ToolDescriptionHash == b.ToolDescriptionHash &&
		a.SkillIndexHash == b.SkillIndexHash &&
		a.MiddlewareHash == b.MiddlewareHash &&
		a.ModelRoutingHash == b.ModelRoutingHash &&
		a.StablePrefixHash == b.StablePrefixHash
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func hashTree(root string) (string, error) {
	entries := []string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		entries = append(entries, filepath.ToSlash(rel)+"\x00"+hashBytes(b))
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(entries)
	return hashJSON(entries), nil
}

func hashJSON(v any) string {
	b, _ := json.Marshal(v)
	return hashBytes(b)
}

func hashBytes(b []byte) string {
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
}
