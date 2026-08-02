package hashing

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Lockfile records the last successfully built content hash of each addon, so a
// build can skip addons whose sources have not changed.
//
// Keys are "<addon-set>/<addon>", e.g. "main/PWE_Core". Qualifying by set keeps
// two sets that happen to share an addon name from aliasing each other.
//
// An entry only gets written after the addon has both packed AND deployed
// successfully. A half-finished build has no business marking itself current.
type Lockfile struct {
	path    string
	entries map[string]string
}

// Key builds the lockfile key for an addon within a set.
func Key(set, addon string) string { return set + "/" + addon }

// LoadLockfile reads path. A missing file is not an error and just yields an
// empty lockfile, which means everything needs building.
func LoadLockfile(path string) (*Lockfile, error) {
	lf := &Lockfile{path: path, entries: map[string]string{}}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return lf, nil
	}
	if err != nil {
		return nil, fmt.Errorf("lockfile: read %s: %w", path, err)
	}
	if len(data) == 0 {
		return lf, nil
	}
	if err := json.Unmarshal(data, &lf.entries); err != nil {
		return nil, fmt.Errorf("lockfile: parse %s: %w", path, err)
	}
	if lf.entries == nil {
		lf.entries = map[string]string{}
	}
	return lf, nil
}

// Get returns the recorded hash for key, and whether one was present.
func (l *Lockfile) Get(key string) (string, bool) {
	h, ok := l.entries[key]
	return h, ok
}

// Fresh reports whether key is recorded with exactly this hash.
func (l *Lockfile) Fresh(key, hash string) bool {
	h, ok := l.entries[key]
	return ok && h == hash
}

// Set records a hash. Call this only once the addon is fully built and deployed.
func (l *Lockfile) Set(key, hash string) { l.entries[key] = hash }

// Delete drops an entry, forcing a rebuild next time.
func (l *Lockfile) Delete(key string) { delete(l.entries, key) }

// Clear drops every entry. This is what --force does.
func (l *Lockfile) Clear() { l.entries = map[string]string{} }

// Keys returns the recorded keys, sorted.
func (l *Lockfile) Keys() []string {
	out := make([]string, 0, len(l.entries))
	for k := range l.entries {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Save writes the lockfile atomically, so a crash mid-write leaves the previous
// file intact instead of a truncated one that would silently skip a rebuild.
func (l *Lockfile) Save() error {
	// json.Marshal sorts map keys, so the file stays diff-friendly.
	data, err := json.MarshalIndent(l.entries, "", "  ")
	if err != nil {
		return fmt.Errorf("lockfile: encode: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(l.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("lockfile: mkdir %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".build_lockfile-*.tmp")
	if err != nil {
		return fmt.Errorf("lockfile: temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("lockfile: write %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("lockfile: close %s: %w", tmpName, err)
	}
	// Windows will not rename onto an existing file.
	if err := os.Remove(l.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("lockfile: replace %s: %w", l.path, err)
	}
	if err := os.Rename(tmpName, l.path); err != nil {
		return fmt.Errorf("lockfile: rename onto %s: %w", l.path, err)
	}
	return nil
}
