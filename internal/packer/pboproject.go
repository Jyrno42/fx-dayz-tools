package packer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Jyrno42/fx-dayz-tools/internal/paths"
	"github.com/Jyrno42/fx-dayz-tools/internal/proc"
)

// PboProject packs with Mikero pboProject. It is the release packer, and the
// only one that can obfuscate.
//
// Its options persist in the GUI registry between runs. From the documentation:
// "These options persist across sessions unless -R is used. Persistence: As an
// eg. If you +Obfuscate a pbo, all subsequent invocations of pboProject will
// continue to obfuscate until turned off."
//
// Two consequences shape everything here. First, every option that affects
// output gets emitted with an explicit polarity, so nothing is inherited from
// whatever the GUI was last set to. An omitted +O/-O would mean "obfuscate if
// the registry says so". Second, -R is always passed, so a run never writes its
// own settings back and a later manual GUI build behaves as its user left it.
type PboProject struct {
	// Exe is the full path to pboProject.exe.
	Exe string
}

// Options are the pboProject settings a channel configures. Each one becomes an
// explicit flag, and none of them get left to the registry.
type Options struct {
	Engine string
	// Exclude are file patterns kept out of the PBO (-X).
	Exclude []string

	Compress      bool // +/-Z, compress per the GUI's list
	Noisy         bool // +/-N, warnings are errors in the log
	Warnings      bool // +/-W, warnings are errors
	AutomakeStale bool // +/-J, only affects wrps
	CleanTemp     bool // +/-C, erase this project's temp space first
	EncodePrefix  bool // +/-$, encode the prefix into the PBO header
	// BinariseCpp is +/-B, which covers config.cpp and mission.sqm ONLY. Model
	// binarisation is a separate thing and this flag has no bearing on it.
	BinariseCpp bool
	// DeletePng, ConvertOgg and ShrinkP3D get stated explicitly because each one
	// rewrites or removes source-derived content. Leave them to the registry and
	// the output starts depending on GUI state nobody remembers setting.
	DeletePng  bool // +/-D
	ConvertOgg bool // +/-G, wav/wss to ogg
	ShrinkP3D  bool // +/-T, DayZ only

	// LogDir is pboProject's temp directory, where it writes one
	// <Addon>.packing.log per addon. The binary has no console, so that file is
	// the only place a failure ever gets explained.
	LogDir string
}

func (p *PboProject) Name() string { return "pboproject" }

func (p *PboProject) Caps() Caps {
	return Caps{
		CanObfuscate:  true,
		CanBinarize:   true,
		CanSignInline: true,
		NeedsPDrive:   true,
		// +C clears this project's temp space, so unlike AddonBuilder it never
		// accumulates deleted files across runs.
		SelfCleansTemp: true,
		// This is why the flag vector is spelled out in full.
		StickyOptions: true,
	}
}

// modDir is the -M= target, meaning the @mod folder pboProject writes Addons/
// inside. The pipeline hands every packer an Addons directory, so step up one
// level.
func modDir(outDir string) string {
	// Windows-aware rather than filepath.*: outDir is a P: path being handed to
	// pboProject, so it stays backslash-separated on any host.
	if strings.EqualFold(paths.WinBase(outDir), "Addons") {
		return paths.WinDir(outDir)
	}
	return outDir
}

// Argv builds the pboProject command line.
//
//	pboProject.exe -P -R -e=dayz +N +C +Z +J +O -B -M=<mod> +K=<key> -X=<...> <source>
//
// -P means "do not pause", not "project". The source folder is positional.
func (p *PboProject) Argv(j Job) ([]string, error) {
	if p.Exe == "" {
		return nil, fmt.Errorf("packer: pboProject path is not configured (set paths.mikero)")
	}
	if j.SourceDir == "" || j.OutDir == "" {
		return nil, fmt.Errorf("packer: addon %q needs a source and an output directory", j.AddonName)
	}

	opts := j.PboProject
	engine := opts.Engine
	if engine == "" {
		engine = "dayz"
	}

	// Emitted in exactly the shape and order of the invocation known to work on
	// this machine. Options beyond this set are NOT passed. Adding -R, +W, -D,
	// -G, -T or +$ makes pboProject reject the command line, blank its persisted
	// settings and fall back to its GUI, with no log and no message. Whatever its
	// parser is doing with those, the proven vector unchanged beats being clever.
	//
	// The cost is that options outside this set come from the GUI registry.
	// Obfuscation is not one of them. +O/-O is always explicit, and that is the
	// polarity that actually matters.
	argv := []string{
		p.Exe,
		"-e=" + engine,
		flag(opts.Noisy, "N"),
		flag(opts.CleanTemp, "C"),
		flag(opts.Compress, "Z"),
		flag(opts.AutomakeStale, "J"),
		flag(j.Obfuscate, "O"),
		flag(opts.BinariseCpp, "B"),
		"-M=" + modDir(j.OutDir),
	}

	if j.SignKey != "" {
		argv = append(argv, "+K="+j.SignKey)
	} else {
		argv = append(argv, "-K")
	}
	if j.Label != "" {
		argv = append(argv, "+L="+j.Label)
	}

	// -P means "do not pause". The source folder is positional and follows it.
	argv = append(argv, "-P", j.SourceDir)

	if len(j.Excludes) > 0 {
		argv = append(argv, "-X="+strings.Join(j.Excludes, ","))
	}
	return argv, nil
}

// failureHint explains where to look. pboProject is a GUI-subsystem binary with
// no console, so it never writes to stdout or stderr and there is nothing to
// quote back. Everything it has to say ends up in its packing log.
func (p *PboProject) failureHint(j Job) string {
	log := j.AddonName + ".packing.log"
	if j.PboProject.LogDir != "" {
		log = filepath.Join(j.PboProject.LogDir, log)
	}
	return "pboProject has no console, so it produced no output to show. Read " + log + " for the reason.\n" +
		"If that file does not exist it failed before packing, and the usual cause is its Setup dialog:\n" +
		"the \"Exclude From Pbo\" list must not be empty, or every run exits 1 with no log and no message."
}

// flag renders an option as its explicit +X or -X form.
func flag(on bool, letter string) string {
	if on {
		return "+" + letter
	}
	return "-" + letter
}

// PboPath is where the finished PBO lands. pboProject writes Addons/ inside the
// -M= mod folder, which is the directory the pipeline already asked for.
func (p *PboProject) PboPath(j Job) string {
	name := j.AddonName
	if j.Label != "" {
		name = j.Label
	}
	return filepath.Join(j.OutDir, name+".pbo")
}

const (
	prefixFileName    = "$PBOPREFIX$"
	noScrambleFile    = "noscramble.lst"
	prefixFileComment = "prefix="
)

// Preflight materialises $PBOPREFIX$ and, when needed, noscramble.lst, and
// returns a cleanup that removes whatever it wrote.
//
// pboProject can derive the prefix on its own from the source folder's position
// under the work drive, and that derivation happens to be correct. Writing it
// out anyway removes a whole class of silent failure. If the repo ever gets
// reached through an unexpected path, a derived prefix would be wrong and
// nothing would say so until the mod failed to load.
//
// The cleanup is safe to call more than once and only removes files this call
// created, so a repo that commits its own $PBOPREFIX$ gets left alone.
func (p *PboProject) Preflight(_ context.Context, j Job) (func() error, error) {
	var written []string

	cleanup := func() error {
		var firstErr error
		for _, path := range written {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) && firstErr == nil {
				firstErr = fmt.Errorf("packer: removing %s: %w", path, err)
			}
		}
		written = nil
		return firstErr
	}

	if j.WritePrefixFile && j.Prefix != "" {
		path := filepath.Join(j.SourceDir, prefixFileName)
		if _, err := os.Stat(path); err == nil {
			// The repo committed this one, so leave it be, now and afterwards.
			return cleanup, nil
		}
		if err := os.WriteFile(path, []byte(prefixFileComment+j.Prefix+"\r\n"), 0o644); err != nil {
			return cleanup, fmt.Errorf("packer: writing %s: %w", path, err)
		}
		written = append(written, path)
	}

	if len(j.NoScramble) > 0 {
		path := filepath.Join(j.SourceDir, noScrambleFile)
		if _, err := os.Stat(path); err != nil {
			body := strings.Join(j.NoScramble, "\r\n") + "\r\n"
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				cleanup()
				return func() error { return nil }, fmt.Errorf("packer: writing %s: %w", path, err)
			}
			written = append(written, path)
		}
	}

	if err := os.MkdirAll(j.OutDir, 0o755); err != nil {
		cleanup()
		return func() error { return nil }, fmt.Errorf("packer: creating %s: %w", j.OutDir, err)
	}
	// Do not let a stale PBO survive a failed pack and ship as if it were fresh.
	if err := os.Remove(p.PboPath(j)); err != nil && !os.IsNotExist(err) {
		cleanup()
		return func() error { return nil }, fmt.Errorf("packer: removing stale %s: %w", p.PboPath(j), err)
	}
	return cleanup, nil
}

// Pack runs pboProject.
func (p *PboProject) Pack(ctx context.Context, r proc.Runner, j Job) (proc.Result, error) {
	argv, err := p.Argv(j)
	if err != nil {
		return proc.Result{}, err
	}

	// pboProject will not run under CreateProcess. It exits 1 immediately having
	// done nothing, whatever the arguments. Launch it the way Explorer would and
	// the identical command line packs correctly.
	res, err := r.Run(ctx, proc.Cmd{
		Name:         argv[0],
		Args:         argv[1:],
		Dir:          filepath.Dir(argv[0]),
		ShellExecute: true,
	})
	if err != nil {
		return res, fmt.Errorf("packing %s failed: %w\n%s", j.AddonName, err, res.Tail(20))
	}
	if r.DryRun() {
		return res, nil
	}

	// pboProject reports failures in its output as well as its exit code, so
	// trust the artefact over either of them.
	if _, statErr := os.Stat(p.PboPath(j)); statErr != nil {
		return res, fmt.Errorf("packing %s reported success but wrote no PBO at %s\n%s",
			j.AddonName, p.PboPath(j), res.Tail(20))
	}
	return res, nil
}
