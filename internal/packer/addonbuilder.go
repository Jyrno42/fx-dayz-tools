package packer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Jyrno42/fx-dayz-tools/internal/proc"
)

// AddonBuilder packs with the DayZ Tools Addon Builder. It is the dev-loop
// packer, so it is fast and does no obfuscation or signing.
type AddonBuilder struct {
	// Exe is the full path to AddonBuilder.exe.
	Exe string
	// TempDir overrides the sync-cache location, for tests. Leave it empty to
	// use the system temp directory, which is where AddonBuilder itself goes.
	TempDir string
}

func (a *AddonBuilder) Name() string { return "addonbuilder" }

func (a *AddonBuilder) Caps() Caps {
	return Caps{
		CanObfuscate:  false,
		CanBinarize:   true,
		CanSignInline: false,
		NeedsPDrive:   true,
		// This is why SyncDir exists. AddonBuilder never cleans up after itself.
		SelfCleansTemp: false,
		// -include is an explicit "sync these", and every synced file is logged,
		// so the log accounts for the whole PBO.
		ReportsEveryFile: true,
		StickyOptions:    false,
	}
}

// Argv builds the AddonBuilder command line.
//
//	AddonBuilder.exe -prefix=<prefix> -include=<.buildfiles> <source> <outdir>
func (a *AddonBuilder) Argv(j Job) ([]string, error) {
	if a.Exe == "" {
		return nil, fmt.Errorf("packer: AddonBuilder path is not configured (set paths.dayz_tools)")
	}
	if j.SourceDir == "" || j.OutDir == "" || j.Prefix == "" {
		return nil, fmt.Errorf("packer: addon %q needs a source, an output directory and a prefix", j.AddonName)
	}
	if j.Obfuscate {
		return nil, fmt.Errorf("packer: AddonBuilder cannot obfuscate; addon %q needs the pboproject packer", j.AddonName)
	}

	// -temp is stated on every run, never left out. From AddonBuilder's own
	// documentation: "if no value is present then the last folder is used". So an
	// omitted -temp means a folder persisted from some earlier run, which is how
	// the cache ends up somewhere the packer is not looking and a wipe clears a
	// directory nothing writes to.
	argv := []string{a.Exe, "-prefix=" + j.Prefix, "-temp=" + a.SyncDir(j)}
	if j.IncludeFile != "" {
		argv = append(argv, "-include="+j.IncludeFile)
	}
	if !j.Binarize {
		// Pack the files as they are instead of binarising them. That also means
		// model.cfg never gets applied.
		argv = append(argv, "-packonly")
	}
	argv = append(argv, j.SourceDir, j.OutDir)
	return argv, nil
}

// SyncDir is the directory AddonBuilder mirrors and binarises sources into. It
// is passed as -temp on every run, so this is where the cache is rather than
// where we hope it is.
//
// Namespaced by TempDir, which the pipeline sets per repo. Two repos with an
// addon of the same name would otherwise share one cache, and clearing it for
// one would throw away the other's binarised output.
func (a *AddonBuilder) SyncDir(j Job) string {
	base := a.TempDir
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, strings.ToLower(j.AddonName))
}

// stagedPbo is the intermediate PBO AddonBuilder writes into the temp directory
// before copying it to the output. Each run overwrites it, but a stale one left
// by a failed build should not get mistaken for a fresh result.
func (a *AddonBuilder) stagedPbo(j Job) string {
	base := a.TempDir
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, j.AddonName+".pbo")
}

// Preflight clears the two caches AddonBuilder leaves behind.
//
// This is the single most important thing this packer does. AddonBuilder syncs
// sources into %LOCALAPPDATA%\Temp\<addon lowercased> and NEVER removes files
// that have disappeared from the source tree. So a deleted script keeps getting
// packed into the PBO indefinitely, and turns up as an "Undefined function" for
// code that no longer exists anywhere in the repo.
//
// We remove the stale output PBO too, so a failed build cannot leave the
// previous one lying around to be deployed as if it were current.
func (a *AddonBuilder) Preflight(_ context.Context, j Job) (func() error, error) {
	for _, path := range []string{a.SyncDir(j), a.stagedPbo(j)} {
		if err := os.RemoveAll(path); err != nil {
			return noopCleanup, fmt.Errorf("packer: clearing AddonBuilder cache %s: %w", path, err)
		}
	}
	if err := os.RemoveAll(a.PboPath(j)); err != nil {
		return noopCleanup, fmt.Errorf("packer: removing stale %s: %w", a.PboPath(j), err)
	}
	if err := os.MkdirAll(j.OutDir, 0o755); err != nil {
		return noopCleanup, fmt.Errorf("packer: creating %s: %w", j.OutDir, err)
	}
	return noopCleanup, nil
}

// PboPath is where AddonBuilder leaves the finished PBO.
func (a *AddonBuilder) PboPath(j Job) string {
	name := j.AddonName
	if j.Label != "" {
		name = j.Label
	}
	return filepath.Join(j.OutDir, name+".pbo")
}

// Pack runs AddonBuilder.
func (a *AddonBuilder) Pack(ctx context.Context, r proc.Runner, j Job) (proc.Result, error) {
	argv, err := a.Argv(j)
	if err != nil {
		return proc.Result{}, err
	}

	res, err := r.Run(ctx, proc.Cmd{Name: argv[0], Args: argv[1:]})
	if err != nil {
		return res, fmt.Errorf("packing %s failed: %w\n%s", j.AddonName, err, res.Tail(15))
	}
	if r.DryRun() {
		return res, nil
	}

	// AddonBuilder can exit zero without producing anything, so trust the file
	// on disk over the exit code.
	if _, statErr := os.Stat(a.PboPath(j)); statErr != nil {
		return res, fmt.Errorf("packing %s reported success but wrote no PBO at %s\n%s",
			j.AddonName, a.PboPath(j), res.Tail(15))
	}
	return res, nil
}

// syncedFile matches AddonBuilder's per-file sync record, e.g.
//
//	[2026-08-09 12:47:50,889] [INFO ]: Syncing file "Scripts\3_Game\Credits.c"
var syncedFile = regexp.MustCompile(`Syncing file "([^"]+)"`)

// PackedFiles reads the files AddonBuilder reported syncing, which is the whole
// account of what went into the PBO.
func (a *AddonBuilder) PackedFiles(res proc.Result, _ Job) ([]string, bool, error) {
	out := res.Output()
	if strings.TrimSpace(out) == "" {
		return nil, false, nil
	}
	var files []string
	for _, m := range syncedFile.FindAllStringSubmatch(out, -1) {
		files = append(files, m[1])
	}
	return files, true, nil
}
