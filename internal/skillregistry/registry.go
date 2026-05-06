// Package skillregistry tracks where `dibbla skills install` has written
// skill files, so `dibbla uninstall` can find and remove them later.
//
// The registry lives at $HOME/.dibbla/skill-installs.json. Each entry
// records a (skill id, root directory) pair. The file is treated as
// best-effort: a missing or malformed file is not an error — it just
// means we have no record of past installs.
package skillregistry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Entry is a single record of a skill install.
type Entry struct {
	ID         string    `json:"id"`
	Root       string    `json:"root"`
	InstalledAt time.Time `json:"installed_at"`
}

// Registry is the on-disk envelope.
type Registry struct {
	Version int     `json:"version"`
	Entries []Entry `json:"entries"`
}

const currentVersion = 1

// pathFn is the resolved path of the registry file. Overridable from
// tests so they can isolate a temp HOME without HOMEDIR juggling.
var pathFn = defaultPath

func defaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, ".dibbla", "skill-installs.json"), nil
}

// Path returns the registry file path.
func Path() (string, error) { return pathFn() }

// SetPathForTest swaps the path resolver. Returns a cleanup func that
// restores the previous resolver.
func SetPathForTest(path string) func() {
	prev := pathFn
	pathFn = func() (string, error) { return path, nil }
	return func() { pathFn = prev }
}

// Load reads the registry. A missing file returns an empty Registry
// with no error. A malformed file returns an empty Registry — we don't
// want a corrupt file to break uninstall.
func Load() *Registry {
	path, err := pathFn()
	if err != nil {
		return &Registry{Version: currentVersion}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return &Registry{Version: currentVersion}
	}
	var r Registry
	if err := json.Unmarshal(data, &r); err != nil {
		return &Registry{Version: currentVersion}
	}
	if r.Version == 0 {
		r.Version = currentVersion
	}
	return &r
}

// Record appends or updates an entry for (id, root). Existing entries
// with the same (id, root) pair are refreshed in place rather than
// duplicated. Errors writing the registry are non-fatal — install must
// still succeed even if we couldn't track it.
func Record(id, root string) error {
	root = filepath.Clean(root)
	r := Load()
	now := time.Now().UTC()

	for i := range r.Entries {
		if r.Entries[i].ID == id && r.Entries[i].Root == root {
			r.Entries[i].InstalledAt = now
			return save(r)
		}
	}
	r.Entries = append(r.Entries, Entry{
		ID:          id,
		Root:        root,
		InstalledAt: now,
	})
	return save(r)
}

// Forget removes any entry matching (id, root). Missing entries are a
// silent no-op. Used by uninstall after it's cleaned a location.
func Forget(id, root string) error {
	root = filepath.Clean(root)
	r := Load()
	out := r.Entries[:0]
	for _, e := range r.Entries {
		if e.ID == id && e.Root == root {
			continue
		}
		out = append(out, e)
	}
	r.Entries = out
	return save(r)
}

// Clear removes the registry file entirely. Used by `dibbla uninstall`
// when wiping all CLI state.
func Clear() error {
	path, err := pathFn()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Entries returns the registry contents sorted by root for stable
// output in uninstall plans and tests.
func Entries() []Entry {
	r := Load()
	out := append([]Entry(nil), r.Entries...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].Root < out[j].Root
	})
	return out
}

func save(r *Registry) error {
	path, err := pathFn()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating registry dir: %w", err)
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".skill-installs.tmp.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}
