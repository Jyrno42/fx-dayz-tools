// Package mission prepares the dev server's test mission before launch: clone
// it if missing, copy the repo's init.c on top, reset the persistence folder.
// Every step is re-runnable.
package mission

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Options says what to prepare. Paths are absolute unless stated otherwise.
type Options struct {
	// MissionsDir is the server's mpmissions folder.
	MissionsDir string
	// Name is the mission folder under MissionsDir, e.g. "fxstaff.deerisle".
	Name string
	// CloneFrom is the mission to copy when Name does not exist. A bare name
	// resolves under MissionsDir. Empty means a missing mission is an error.
	CloneFrom string
	// InitC is the file copied over <mission>/init.c, or "" to leave it alone.
	InitC string
	// InstanceID selects the persistence folder, storage_<InstanceID>.
	InstanceID int64

	// Clean deletes the persistence folder before launch.
	Clean bool
	// SeedFrom replaces the persistence folder with a copy of this directory.
	// It implies Clean.
	SeedFrom string

	// DryRun reports what would happen without touching the disk.
	DryRun bool
	// Log receives one line per action. Nil discards them.
	Log func(format string, args ...any)
}

// Prepare runs every configured step in order: clone, init.c, storage.
func Prepare(o Options) error {
	if o.Log == nil {
		o.Log = func(string, ...any) {}
	}
	if o.Name == "" {
		return fmt.Errorf("mission: no mission name")
	}
	if strings.ContainsAny(o.Name, `/\`) {
		return fmt.Errorf("mission: %q must be a folder name under mpmissions, not a path", o.Name)
	}
	dir := filepath.Join(o.MissionsDir, o.Name)

	if err := clone(o, dir); err != nil {
		return err
	}
	if err := installInitC(o, dir); err != nil {
		return err
	}
	return prepareStorage(o, dir)
}

// clone creates the mission from CloneFrom when it is missing. storage_*
// folders are skipped so the clone starts from a clean world.
func clone(o Options, dir string) error {
	if isDir(dir) {
		return nil
	}
	if o.CloneFrom == "" {
		return fmt.Errorf("mission: %s does not exist and launch.mission.clone_from is not set", dir)
	}
	src := o.CloneFrom
	if !filepath.IsAbs(src) && !strings.ContainsAny(src, `/\`) {
		src = filepath.Join(o.MissionsDir, src)
	}
	if !isDir(src) {
		return fmt.Errorf("mission: clone source %s does not exist", src)
	}
	if o.DryRun {
		o.Log("would clone %s -> %s (without storage_*)", src, dir)
		return nil
	}
	if err := copyTree(src, dir, isStorageDir); err != nil {
		// A half-copied mission would pass the exists check next run.
		os.RemoveAll(dir)
		return fmt.Errorf("mission: cloning %s: %w", src, err)
	}
	o.Log("cloned %s -> %s (without storage_*)", src, dir)
	return nil
}

func installInitC(o Options, dir string) error {
	if o.InitC == "" {
		return nil
	}
	if !fileExists(o.InitC) {
		return fmt.Errorf("mission: init.c not found: %s", o.InitC)
	}
	dst := filepath.Join(dir, "init.c")
	if o.DryRun {
		o.Log("would copy %s -> %s", o.InitC, dst)
		return nil
	}
	if err := copyFile(o.InitC, dst); err != nil {
		return fmt.Errorf("mission: %w", err)
	}
	o.Log("installed %s -> %s", o.InitC, dst)
	return nil
}

// prepareStorage clears or seeds storage_<instanceId>. Seeding replays a saved
// world, so an upgrade test does not need the save redone by hand.
func prepareStorage(o Options, dir string) error {
	if !o.Clean && o.SeedFrom == "" {
		return nil
	}
	storage := filepath.Join(dir, StorageName(o.InstanceID))

	if o.SeedFrom != "" && !isDir(o.SeedFrom) {
		return fmt.Errorf("mission: storage seed %s does not exist", o.SeedFrom)
	}

	if o.DryRun {
		if o.SeedFrom != "" {
			o.Log("would replace %s with a copy of %s", storage, o.SeedFrom)
		} else {
			o.Log("would clear %s", storage)
		}
		return nil
	}

	existed := isDir(storage)
	if err := os.RemoveAll(storage); err != nil {
		return fmt.Errorf("mission: clearing %s: %w (is the server still running?)", storage, err)
	}
	if o.SeedFrom == "" {
		if existed {
			o.Log("cleared %s", storage)
		}
		return nil
	}
	if err := copyTree(o.SeedFrom, storage, nil); err != nil {
		return fmt.Errorf("mission: seeding %s: %w", storage, err)
	}
	o.Log("seeded %s from %s", storage, o.SeedFrom)
	return nil
}

// StorageName is the persistence folder for an instance, storage_<instanceId>.
func StorageName(instanceID int64) string {
	return fmt.Sprintf("storage_%d", instanceID)
}

func isStorageDir(rel string, d os.DirEntry) bool {
	return d.IsDir() && !strings.ContainsAny(rel, `/\`) && strings.HasPrefix(strings.ToLower(rel), "storage_")
}

// copyTree copies src into dst. skip sees each entry's source-relative path,
// and a skipped directory is not descended.
func copyTree(src, dst string, skip func(rel string, d os.DirEntry) bool) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		if skip != nil && skip(rel, d) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(p, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("opening %s: %w", src, err)
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("creating %s: %w", tmp, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return fmt.Errorf("copying to %s: %w", tmp, err)
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		os.Remove(tmp)
		return fmt.Errorf("replacing %s: %w (is the server still running?)", dst, err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		return fmt.Errorf("renaming onto %s: %w", dst, err)
	}
	return nil
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
