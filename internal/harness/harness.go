// Package harness manages local Reasonix-AHE harness source snapshots.
package harness

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
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
	id, err := l.nextSnapshotID()
	if err != nil {
		return Lock{}, err
	}
	lock, err := l.captureLock(id, createdAt)
	if err != nil {
		return Lock{}, err
	}
	dir := l.SnapshotDir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Lock{}, err
	}
	if err := writeLock(filepath.Join(dir, LockFile), lock); err != nil {
		return Lock{}, err
	}
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

func (l Layout) captureLock(id string, createdAt time.Time) (Lock, error) {
	systemHash, err := hashTree(filepath.Join(l.SourceDir(), "prompts"))
	if err != nil {
		return Lock{}, err
	}
	toolHash, err := hashTree(filepath.Join(l.SourceDir(), "tool_descriptions"))
	if err != nil {
		return Lock{}, err
	}
	skillHash, err := hashTree(filepath.Join(l.SourceDir(), "skills"))
	if err != nil {
		return Lock{}, err
	}
	middlewareHash, err := hashTree(filepath.Join(l.SourceDir(), "middleware"))
	if err != nil {
		return Lock{}, err
	}
	routingHash, err := hashTree(filepath.Join(l.SourceDir(), "routing"))
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
