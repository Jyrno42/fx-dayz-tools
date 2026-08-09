package machine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// PlacementsFile is where the ledger lives, beside the machine config.
const PlacementsFile = "placements.json"

// Placement kinds. Two reconcilers share this ledger and must not collect each
// other's entries: the include deployer owns loose files, and the addon deployer
// owns PBOs it packed. Without the distinction, whichever runs last treats the
// other's entries as stale and deletes them.
const (
	// KindLoose is a prebuilt PBO copied to an into: destination.
	KindLoose = "loose"
	// KindAddon is a PBO this repo packed and deployed into its @mod folder.
	KindAddon = "addon"
)

// Placement records one file the tool copied into a directory it does not own.
type Placement struct {
	// Repo is the mod repo that put the file there. It is what lets a build in
	// one repo clean up after a build in another.
	Repo string `json:"repo"`
	// From is the source the file was copied from, for reporting.
	From string `json:"from"`
	// SHA256 is the content as placed. A file that no longer matches was changed
	// by someone else, and is left alone rather than deleted.
	SHA256 string `json:"sha256"`
	// Kind is which reconciler owns this entry.
	Kind string `json:"kind"`
}

// Placements is a ledger of files written outside the tool's own output folders.
//
// It exists for `include[].into`, which copies a loose PBO into a game install's
// own addons directory. An @mod folder is namespaced and the tool clears it
// wholesale, so nothing needs remembering; that directory holds hundreds of
// vanilla PBOs and belongs to the game.
//
// Machine-level rather than repo-level, deliberately. The whole point is that a
// build in one repo can remove what a build in another repo left in a shared
// install, and a file inside a repo cannot see across to its neighbours.
type Placements struct {
	path string
	// Installs maps an install root to the files placed under it, keyed by the
	// destination path relative to that root.
	Installs map[string]map[string]Placement `json:"installs"`
	Version  int                             `json:"version"`
}

// PlacementsPath returns the ledger location next to the machine config.
func PlacementsPath(configPath string) (string, error) {
	if configPath == "" {
		p, err := DefaultPath()
		if err != nil {
			return "", err
		}
		configPath = p
	}
	return filepath.Join(filepath.Dir(configPath), PlacementsFile), nil
}

// LoadPlacements reads the ledger. A missing file is an empty ledger, not an
// error: the first build on a machine has placed nothing yet.
func LoadPlacements(path string) (*Placements, error) {
	p := &Placements{path: path, Installs: map[string]map[string]Placement{}, Version: 1}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return p, nil
		}
		return nil, fmt.Errorf("placements: reading %s: %w", path, err)
	}
	if err := json.Unmarshal(data, p); err != nil {
		return nil, fmt.Errorf("placements: %s: %w", path, err)
	}
	if p.Installs == nil {
		p.Installs = map[string]map[string]Placement{}
	}
	p.path = path
	p.Version = 1
	return p, nil
}

// installKey normalises an install root so the same directory is one key however
// it was spelled in the config.
func installKey(root string) string {
	return strings.ToLower(filepath.Clean(root))
}

// destKey normalises a destination path the same way.
func destKey(rel string) string {
	return strings.ToLower(filepath.ToSlash(filepath.Clean(rel)))
}

// Record notes that a file now exists at rel under install root.
func (p *Placements) Record(root, rel, repo, from, sum, kind string) {
	ik := installKey(root)
	if p.Installs[ik] == nil {
		p.Installs[ik] = map[string]Placement{}
	}
	p.Installs[ik][destKey(rel)] = Placement{Repo: repo, From: from, SHA256: sum, Kind: kind}
}

// Placed reports the recorded placement for a destination.
func (p *Placements) Placed(root, rel string) (Placement, bool) {
	got, ok := p.Installs[installKey(root)][destKey(rel)]
	return got, ok
}

// Stale lists recorded destinations under an install that the current run no
// longer wants, in a stable order.
//
// Entries from another repo count as stale. Two repos dropping loose PBOs into
// one install is a real conflict rather than a merge, and the build running now
// is the one that gets to say what should be there. Running the other repo again
// puts its files back.
func (p *Placements) Stale(root string, wanted []string, kind string) []string {
	keep := map[string]bool{}
	for _, w := range wanted {
		keep[destKey(w)] = true
	}
	var out []string
	for rel, placed := range p.Installs[installKey(root)] {
		// Only this reconciler's own entries. Collecting another kind here is how
		// one deployer ends up deleting what the other just wrote.
		if placed.Kind != kind || keep[rel] {
			continue
		}
		out = append(out, rel)
	}
	sort.Strings(out)
	return out
}

// Forget drops a recorded destination.
func (p *Placements) Forget(root, rel string) {
	delete(p.Installs[installKey(root)], destKey(rel))
	if len(p.Installs[installKey(root)]) == 0 {
		delete(p.Installs, installKey(root))
	}
}

// Save writes the ledger, creating its directory if the machine config has not
// been written yet.
func (p *Placements) Save() error {
	if p.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p.path), 0o755); err != nil {
		return fmt.Errorf("placements: %w", err)
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("placements: %w", err)
	}
	if err := os.WriteFile(p.path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("placements: writing %s: %w", p.path, err)
	}
	return nil
}

// FileSum hashes a file's contents. An absent file has no sum and no error, so
// callers can treat "gone" as a normal outcome.
func FileSum(path string) (string, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", false, err
	}
	return hex.EncodeToString(h.Sum(nil)), true, nil
}
