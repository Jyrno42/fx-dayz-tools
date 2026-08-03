package packer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

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
// The consequence that shapes everything here: every option in the emitted set
// gets an explicit polarity, so nothing is inherited from whatever the GUI was
// last set to. An omitted +O/-O would mean "obfuscate if the registry says so".
//
// -R is NOT emitted, despite being the obvious companion to that rule. 3.91
// rejects the whole command line when it is present, and it restores settings
// *after* processing, so it does not help the case that matters, a failed run
// blanking the Setup dialog. Whether 4.31 still rejects it is untested.
type PboProject struct {
	// Exe is the full path to pboProject.exe.
	Exe string
	// Timeout bounds a single addon's pack. Zero means DefaultPackTimeout.
	Timeout time.Duration
}

// DefaultPackTimeout is generous enough for an obfuscated model PBO and short
// enough that a stuck run fails the same day.
const DefaultPackTimeout = 30 * time.Minute

func (p *PboProject) timeout() time.Duration {
	if p.Timeout > 0 {
		return p.Timeout
	}
	return DefaultPackTimeout
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
	// NoPrefix is +/-$. Read the polarity carefully: 4.31's own help reads
	// "+/-$ do/don't allow no prefix for pbo" and "+$: enable no prefix in pbo",
	// so +$ means ship WITHOUT a prefix. The 3.91 documentation described the
	// same letter as "do/don't encode prefix in pbo", i.e. the exact opposite,
	// which is why this is named for what it does rather than for the flag.
	//
	// 4.31 also refuses two combinations outright: "you cannot use no prefix if
	// obfuscating" and "you cannot use no prefix AND a rename", a rename being
	// the +L= label. Argv rejects both before pboProject gets the chance.
	NoPrefix bool
	// BinariseCpp is +/-B, which covers config.cpp and mission.sqm ONLY. Model
	// binarisation is a separate thing and this flag has no bearing on it.
	BinariseCpp bool
	// DisablePngConvert is +/-H, DayZ only, where +H DISABLES png conversion.
	// Another letter whose polarity reads backwards; the GUI persists it as
	// m_disable_png_conversion, which is the sense the name follows.
	DisablePngConvert bool
	// RenameCfgPatches is +/-@, persisted as m_rename_cfgpatches. It rewrites
	// class names in cfgPatches, so it changes what the engine sees.
	RenameCfgPatches bool
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
		CanObfuscate: true,
		CanBinarize:  true,
		// Signing happens during packing, so the release must not sign again.
		//
		// It only works because Pack gives pboProject a console: signing makes it
		// shell out to copy the .bikey into the mod folder's keys\ directory. The
		// alternative considered was packing unsigned and signing afterwards with
		// DSSignFile, and it was rejected: obfuscation deliberately leaves a PBO
		// malformed to third-party readers, and DSSignFile is one. Signing inline
		// keeps an obfuscated release on the exact path that is known to produce
		// working PBOs.
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

	// 4.31 rejects these two combinations itself. Catching them here turns what
	// would be a GUI-only error message into an error the caller can read.
	if opts.NoPrefix {
		if j.Obfuscate {
			return nil, fmt.Errorf("packer: addon %q sets no_prefix and obfuscate together; pboProject refuses to obfuscate a PBO with no prefix", j.AddonName)
		}
		if j.Label != "" {
			return nil, fmt.Errorf("packer: addon %q sets no_prefix and a label; pboProject refuses a rename on a PBO with no prefix", j.AddonName)
		}
	}

	// This exact vector, including +/-H and +/-@, is verified to pack on 4.31.
	// Options beyond it are NOT passed: on 3.91, adding -R, +W, -D, -G, -T or +$
	// made pboProject reject the command line, blank its persisted settings and
	// fall back to its GUI with no log and no message. 4.31 has not been tested
	// with those, and its way of rejecting a command line is to wait forever on a
	// keypress prompt, so widening this set is a deliberate experiment rather
	// than a tidy-up.
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
		flag(opts.DisablePngConvert, "H"),
		flag(opts.RenameCfgPatches, "@"),
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

// committedPrefixFile reports whether the repo ships its own $PBOPREFIX$, in
// either the current or the deprecated extensionless spelling.
func committedPrefixFile(sourceDir string) bool {
	for _, name := range []string{prefixFileName, legacyPrefixFileName} {
		if _, err := os.Stat(filepath.Join(sourceDir, name)); err == nil {
			return true
		}
	}
	return false
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
	// prefixFileName carries the extension deliberately. 4.31 treats the
	// extensionless spelling as deprecated, and carries the string
	// "$PBOPREFIX$ (no ext) deprecated" in every language it ships. With warnings
	// promoted to errors, a deprecation notice is a failed pack.
	prefixFileName = "$PBOPREFIX$.txt"
	// legacyPrefixFileName is the extensionless spelling, still recognised so a
	// repo that committed one is left alone rather than shadowed by a second file.
	legacyPrefixFileName = "$PBOPREFIX$"
	noScrambleFile       = "noscramble.lst"
	prefixFileComment    = "prefix="
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

	// 4.06 made a source\ subfolder a hard stop ("now stops if a source \ folder
	// is present") and 4.25 added the warning explaining why: pboProject will
	// never put its contents in a PBO, so a folder named that is assumed to be a
	// mistake. Caught here because the alternative is a failed pack whose only
	// explanation is a line in a log nobody has opened yet.
	if info, err := os.Stat(filepath.Join(j.SourceDir, "source")); err == nil && info.IsDir() {
		return cleanup, fmt.Errorf(
			"packer: addon %q has a source\\ subfolder, which pboProject refuses to pack; move or rename %s",
			j.AddonName, filepath.Join(j.SourceDir, "source"))
	}

	if j.WritePrefixFile && j.Prefix != "" {
		path := filepath.Join(j.SourceDir, prefixFileName)
		// Either spelling counts as committed. Writing ours alongside a repo's
		// extensionless one would leave pboProject picking between two files.
		if committedPrefixFile(j.SourceDir) {
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

	// pboProject will not create its own -M= folder: it reports "mod folder '...'
	// does not exist", concludes the command line is bad, and then waits on a
	// keypress prompt that -P does not suppress and a console-less process can
	// never receive. Creating the Addons directory creates the mod folder above
	// it, which is what stops that happening.
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

	// NeedsConsole runs it under cmd.exe. Signing makes pboProject shell out to
	// copy the .bikey, and without a console that child fails, taking the whole
	// run with it before anything is packed (see proc.runViaConsole).
	//
	// 3.91 additionally would not run under CreateProcess at all, whatever the
	// arguments, which is why this used to go through PowerShell's Start-Process.
	// 4.31 fixed that, so the console is now the only reason to interpose a shell.
	//
	// The timeout earns its place. pboProject's -P ("do not pause") does NOT
	// cover its command-line error path: a bad argv prints "press the ANY key"
	// and waits. It is a GUI-subsystem binary, so nothing can ever press one, and
	// without a deadline a malformed invocation hangs a release forever rather
	// than failing it.
	ctx, cancel := context.WithTimeout(ctx, p.timeout())
	defer cancel()

	res, err := r.Run(ctx, proc.Cmd{
		Name:         argv[0],
		Args:         argv[1:],
		Dir:          filepath.Dir(argv[0]),
		NeedsConsole: true,
	})
	if ctx.Err() != nil {
		return res, fmt.Errorf(
			"packing %s timed out after %s. pboProject was most likely waiting on a keypress it can never receive, "+
				"which is what it does when it rejects a command line (-P does not suppress that prompt).\n%s",
			j.AddonName, p.timeout(), p.failureHint(j))
	}
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
