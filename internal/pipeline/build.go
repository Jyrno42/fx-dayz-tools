// Package pipeline orchestrates a build, i.e. hash, pack, deploy, record.
package pipeline

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Jyrno42/fx-dayz-tools/internal/hashing"
	"github.com/Jyrno42/fx-dayz-tools/internal/lfs"
	"github.com/Jyrno42/fx-dayz-tools/internal/machine"
	"github.com/Jyrno42/fx-dayz-tools/internal/modcfg"
	"github.com/Jyrno42/fx-dayz-tools/internal/packer"
	"github.com/Jyrno42/fx-dayz-tools/internal/paths"
	"github.com/Jyrno42/fx-dayz-tools/internal/proc"
)

// Options controls a build run.
type Options struct {
	Channel string
	// Force rebuilds even when the content hash is unchanged.
	Force bool
	// Set and Addon narrow the build to one addon set or one addon.
	Set   string
	Addon string
	// SkipHooks omits the configured pre-build hooks.
	SkipHooks bool
	// SkipDeploy packs without copying into the game installs.
	SkipDeploy bool
	// NoPrune keeps PBOs this build placed but no longer produces, for a folder
	// someone is deliberately keeping something in.
	NoPrune bool
}

// Outcome is what happened to one addon.
type Outcome struct {
	Set     string
	Addon   string
	Hash    string
	Skipped bool
	Packed  bool
	// Deployed lists the destinations the PBO was copied to.
	Deployed []string
	Duration time.Duration
}

// Reporter receives progress. The CLI implements it, and tests can ignore it.
type Reporter interface {
	Step(format string, args ...any)
	Detail(format string, args ...any)
	Command(c proc.Cmd)
}

// Builder runs builds.
type Builder struct {
	Mod    *modcfg.Config
	Host   *machine.Config
	Runner proc.Runner
	Report Reporter
}

// Build packs every addon in the channel that needs it.
func (b *Builder) Build(ctx context.Context, opts Options) ([]Outcome, error) {
	ch, err := b.Mod.Channel(opts.Channel)
	if err != nil {
		return nil, err
	}

	p, err := b.packerFor(ch)
	if err != nil {
		return nil, err
	}

	lockPath := filepath.Join(b.Mod.Root, b.Mod.Repo.Lockfile)
	lock, err := hashing.LoadLockfile(lockPath)
	if err != nil {
		return nil, err
	}
	if opts.Force {
		lock.Clear()
	}

	if !opts.SkipHooks {
		if err := b.runHooks(ctx, b.Mod.Hooks.PreBuild); err != nil {
			return nil, err
		}
	}

	var outcomes []Outcome
	for _, set := range b.Mod.SetsFor(ch) {
		if opts.Set != "" && set.Name != opts.Set {
			continue
		}
		for _, addonName := range set.AddonNames() {
			if opts.Addon != "" && addonName != opts.Addon {
				continue
			}

			out, err := b.buildAddon(ctx, ch, set, addonName, p, lock, opts)
			if err != nil {
				// Keep what already succeeded so a later run does not redo it.
				if saveErr := lock.Save(); saveErr != nil {
					b.Report.Detail("could not save the lockfile: %v", saveErr)
				}
				return outcomes, err
			}
			outcomes = append(outcomes, out)
		}
	}

	// After the packed addons, so a failure here does not lose their lockfile
	// entries. Skipped when the run is narrowed to one set or addon, since the
	// caller asked for that one thing rather than a whole install.
	// Both are skipped on a narrowed run: the caller asked for one addon, not a
	// reconciliation of the whole install.
	if opts.Set == "" && opts.Addon == "" {
		if err := b.pruneDeployed(ch, opts); err != nil {
			return outcomes, err
		}
		if err := b.deployIncludes(ch, opts); err != nil {
			if saveErr := lock.Save(); saveErr != nil {
				b.Report.Detail("could not save the lockfile: %v", saveErr)
			}
			return outcomes, err
		}
	}

	if !b.Runner.DryRun() {
		b.pruneLockfile(lock)
		if err := lock.Save(); err != nil {
			return outcomes, err
		}
	}
	return outcomes, nil
}

// pruneLockfile drops entries no addon claims any more, meaning addons that were
// removed from the manifest plus the unqualified keys the old nushell scripts
// wrote. It checks keys against every addon set in the manifest, not just the
// channel being built, so a dev build cannot throw away release entries.
func (b *Builder) pruneLockfile(lock *hashing.Lockfile) {
	known := map[string]bool{}
	for _, sname := range b.Mod.AddonSetNames() {
		for _, aname := range b.Mod.AddonSets[sname].AddonNames() {
			known[hashing.Key(sname, aname)] = true
		}
	}
	for _, key := range lock.Keys() {
		if !known[key] {
			lock.Delete(key)
			b.Report.Detail("dropped stale lockfile entry %q", key)
		}
	}
}

func (b *Builder) buildAddon(
	ctx context.Context,
	ch *modcfg.Channel,
	set *modcfg.AddonSet,
	addonName string,
	p packer.Packer,
	lock *hashing.Lockfile,
	opts Options,
) (Outcome, error) {
	start := time.Now()
	out := Outcome{Set: set.Name, Addon: addonName}

	sourceDir := filepath.Join(b.Mod.Root, filepath.FromSlash(set.Source), addonName)
	if _, err := os.Stat(sourceDir); err != nil {
		return out, fmt.Errorf("addon %s: source directory %s: %w", addonName, sourceDir, err)
	}

	sum, err := hashing.DirHash(sourceDir, hashing.Options{
		Skip: hashing.SkipNames(b.Mod.Repo.BuildlogDir),
	})
	if err != nil {
		return out, err
	}
	out.Hash = sum

	key := hashing.Key(set.Name, addonName)
	if ch.ChangeDetection == modcfg.DetectLockfile && !opts.Force && lock.Fresh(key, sum) {
		b.Report.Step("%-28s unchanged", key)
		out.Skipped = true
		out.Duration = time.Since(start)
		return out, nil
	}

	// A pointer stub packs cleanly and gives you a silently broken mod, so
	// refuse outright instead of warning.
	if b.Mod.Repo.LFSGuardEnabled() {
		stubs, err := lfs.Scan(sourceDir)
		if err != nil {
			return out, err
		}
		if len(stubs) > 0 {
			return out, fmt.Errorf(
				"addon %s has %d Git LFS pointer stub(s) instead of real content; run `git lfs pull` first:%s",
				addonName, len(stubs), lfs.Describe(stubs, 10))
		}
	}

	job, err := b.job(ch, set, addonName, p.Caps())
	if err != nil {
		return out, err
	}

	b.Report.Step("%-28s building", key)

	cleanup, err := p.Preflight(ctx, job)
	if err != nil {
		return out, err
	}
	defer func() {
		if cerr := cleanup(); cerr != nil {
			b.Report.Detail("cleanup after %s: %v", addonName, cerr)
		}
	}()

	res, err := p.Pack(ctx, b.Runner, job)
	if err != nil {
		return out, err
	}
	// A pack that exits zero is not proof it packed anything.
	if err := b.verifyPacked(p, res, job, sourceDir); err != nil {
		return out, err
	}
	out.Packed = true

	if opts.SkipDeploy {
		// Packed but deliberately not deployed, so we no longer know what the
		// game installs are holding. Record the hash here and the next ordinary
		// build skips the addon and never deploys it at all.
		b.Report.Detail("%s packed but not deployed; leaving it marked as needing a build", key)
		out.Duration = time.Since(start)
		return out, nil
	}

	dests, err := b.deploy(ch, set, p.PboPath(job))
	if err != nil {
		return out, err
	}
	out.Deployed = dests

	// Only now is the addon genuinely up to date. Record the hash after a
	// successful pack but a failed deploy and the next run skips it, leaving the
	// game installs stale.
	lock.Set(key, sum)

	out.Duration = time.Since(start)
	return out, nil
}

// job assembles the packer job, deriving the prefix from the repo's declared
// work-drive path instead of wherever the repo happens to be checked out.
func (b *Builder) job(ch *modcfg.Channel, set *modcfg.AddonSet, addonName string, caps packer.Caps) (packer.Job, error) {
	addon := set.Addons[addonName]

	// An addon may state its prefix outright, which vendored third-party source
	// needs. Its scripts reference paths under the prefix upstream packs with,
	// and that is not always <set prefix>\<addon>.
	prefix := addon.Prefix
	if prefix == "" {
		prefixRoot := set.Prefix
		if prefixRoot == "" {
			derived, err := paths.Prefix(b.Mod.Repo.PDrivePath, set.Source)
			if err != nil {
				return packer.Job{}, err
			}
			prefixRoot = derived
		}
		prefix = paths.JoinPrefix(prefixRoot, addonName)
	} else {
		prefix = paths.JoinPrefix(prefix)
	}

	// The DayZ tools only see the work drive, so hand them that path even when
	// the repo is reached through a junction from somewhere else.
	pdriveRepo := b.Host.RepoPDrivePath(b.Mod.Repo.PDrivePath)
	sourceDir := filepath.Join(pdriveRepo, filepath.FromSlash(set.Source), addonName)
	if _, err := os.Stat(sourceDir); err != nil {
		// Tell "the drive is not reachable from this process" apart from "the
		// repo is not linked into it". Those need completely different fixes,
		// and the first one makes every path on the drive look missing.
		if !b.Host.WorkDriveVisible() {
			return packer.Job{}, fmt.Errorf(
				"addon %s: %s\n"+
					"The packer cannot be rebased onto the backing directory: the DayZ tools "+
					"resolve their own paths and genuinely need the drive.",
				addonName, b.Host.WorkDriveHint())
		}
		return packer.Job{}, fmt.Errorf(
			"addon %s is not reachable at %s; run `dayzmod pdrive ensure` (the DayZ tools only see the work drive)",
			addonName, sourceDir)
	}

	job := packer.Job{
		AddonName: addonName,
		SourceDir: sourceDir,
		OutDir:    filepath.Join(b.outDir(ch, set), addonName),
		Prefix:    prefix,
		Binarize:  addon.Policy.BinarizeOr(true),
		// Obfuscation is release intent. A packer that cannot obfuscate packs the
		// same addon plain, which is exactly how the dev loop is meant to work,
		// so it is not a configuration error.
		Obfuscate: addon.Policy.ObfuscateOr(false) && caps.CanObfuscate,
		Engine:    modcfg.DefaultEngine,
	}

	includes, err := b.includeFile(pdriveRepo)
	if err != nil {
		return packer.Job{}, err
	}
	job.IncludeFile = includes
	if ch.PboProject != nil {
		p := ch.PboProject
		job.Engine = p.Engine
		job.Excludes = p.Exclude
		job.NoScramble = addon.Policy.NoScramble
		job.WritePrefixFile = p.PrefixFile == modcfg.PrefixAlways
		job.PboProject = packer.Options{
			Engine:            p.Engine,
			Exclude:           p.Exclude,
			Compress:          boolOrTrue(p.Compress),
			Noisy:             boolOrTrue(p.Noisy),
			Warnings:          boolOrFalse(p.Warnings),
			AutomakeStale:     boolOrTrue(p.AutomakeStale),
			CleanTemp:         boolOrTrue(p.CleanTemp),
			NoPrefix:          boolOrFalse(p.NoPrefix),
			BinariseCpp:       boolOrFalse(p.BinariseCpp),
			DeletePng:         boolOrFalse(p.DeletePng),
			ConvertOgg:        boolOrFalse(p.ConvertOgg),
			ShrinkP3D:         boolOrFalse(p.ShrinkP3D),
			DisablePngConvert: boolOrFalse(p.DisablePngConvert),
			RenameCfgPatches:  boolOrFalse(p.RenameCfgPatches),
			LogDir:            b.Host.Paths.PboTemp,
		}
	}

	// Enforced here as well as in validation, so no path to the packer can slip
	// past it. The terms requiring some vendored source stay unobfuscated are
	// there to keep it auditable.
	if job.Obfuscate && !set.ObfuscationAllowed() {
		return packer.Job{}, fmt.Errorf(
			"addon %s is in addon_sets.%s, which sets allow_obfuscation: false; refusing to obfuscate it",
			addonName, set.Name)
	}
	return job, nil
}

// outDir is where packed PBOs land before deployment.
func (b *Builder) outDir(ch *modcfg.Channel, set *modcfg.AddonSet) string {
	if ch.Out != "" {
		return ch.Out
	}
	base := b.Host.Paths.BuildDir
	if ch.Name == "release" {
		base = b.Host.Paths.ReleaseDir
	}
	return filepath.Join(base, set.ModName)
}

// deploy copies the PBO into the game installs.
func (b *Builder) deploy(ch *modcfg.Channel, set *modcfg.AddonSet, pbo string) ([]string, error) {
	var done []string
	for _, target := range ch.Deploy {
		root := b.Host.Paths.DayZClient
		if target == modcfg.TargetServer {
			root = b.Host.Paths.DayZServer
		}
		if root == "" {
			return done, fmt.Errorf("cannot deploy to %s: the install path is not configured", target)
		}

		dest := filepath.Join(root, set.ModName, "Addons")
		if b.Runner.DryRun() {
			b.Report.Detail("would copy %s -> %s", filepath.Base(pbo), dest)
			done = append(done, dest)
			continue
		}
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return done, fmt.Errorf("creating %s: %w", dest, err)
		}
		dst := filepath.Join(dest, filepath.Base(pbo))
		if err := copyFile(pbo, dst); err != nil {
			return done, err
		}
		if err := b.recordDeployed(root, dst); err != nil {
			return done, err
		}
		done = append(done, dest)
	}
	return done, nil
}

// deployIncludes copies prebuilt PBOs into the game installs, so a pack's
// vendored dependencies are present for the dev loop.
//
// Without this a pack can only be tested by building it, releasing it, and
// installing the result by hand, because the vendored mod is not in the install
// at all. launch.mods cannot fill the gap either: source: path passes its value
// verbatim to both -mod= and -serverMod=, so it would need an absolute repo path
// in dayz.yml, which is the one thing that file is meant never to hold.
//
// An include naming its own mod_name lands in that folder, which is what lets a
// prebuilt server-only mod stay off the client through -serverMod= exactly as a
// packed one does.
func (b *Builder) deployIncludes(ch *modcfg.Channel, opts Options) error {
	includes := b.Mod.IncludesFor(ch.Name)
	if opts.SkipDeploy || len(ch.Deploy) == 0 {
		return nil
	}

	primary := b.Mod.Mod.Name
	if sets := b.Mod.SetsFor(ch); len(sets) > 0 {
		primary = sets[0].ModName
	}

	ledger, err := b.loadPlacements()
	if err != nil {
		return err
	}
	// Destinations this run wants, per install root, so anything else recorded
	// under those roots can be cleared afterwards.
	wanted := map[string][]string{}

	for _, inc := range includes {
		src := filepath.Join(b.Mod.Root, filepath.FromSlash(inc.From))
		if _, err := os.Stat(src); err != nil {
			if inc.Optional && os.IsNotExist(err) {
				b.Report.Detail("skipped %s (optional, not present)", inc.From)
				continue
			}
			return fmt.Errorf("include: %s: %w", inc.From, err)
		}

		pbos, err := pbosIn(src)
		if err != nil {
			return err
		}
		if len(pbos) == 0 {
			if inc.Optional {
				continue
			}
			return fmt.Errorf("include: %s contains no .pbo files", inc.From)
		}

		modName := inc.ModName
		if modName == "" && inc.Into == "" {
			modName = primary
		}

		copied := 0
		for _, target := range ch.Deploy {
			// side is honoured here, not just at payload assembly. It is
			// cosmetic for an @mod folder, since launch.mods decides what
			// loads. It is not cosmetic for into: a loose PBO in a client's
			// addons\ is loaded by the engine with nothing able to exclude it.
			if !deployTo(inc.Side, target) {
				continue
			}

			root := b.Host.Paths.DayZClient
			if target == modcfg.TargetServer {
				root = b.Host.Paths.DayZServer
			}
			if root == "" {
				return fmt.Errorf("cannot deploy %s to %s: the install path is not configured", inc.From, target)
			}

			dest := filepath.Join(root, modName, "Addons")
			if inc.Into != "" {
				dest = filepath.Join(root, filepath.FromSlash(inc.Into))
			}

			for _, pbo := range pbos {
				files := []string{pbo}
				sigs, err := filepath.Glob(pbo + ".*.bisign")
				if err != nil {
					return fmt.Errorf("include: %w", err)
				}
				files = append(files, sigs...)

				for _, f := range files {
					dst := filepath.Join(dest, filepath.Base(f))
					if b.Runner.DryRun() {
						b.Report.Detail("would copy %s -> %s", filepath.Base(f), dest)
						copied++
						continue
					}
					if inc.Into == "" {
						// The tool owns @mod folders, so it may overwrite freely.
						if !opts.Force && sameFile(f, dst) {
							continue
						}
					} else {
						rel, relErr := filepath.Rel(root, dst)
						if relErr != nil {
							return fmt.Errorf("include: %w", relErr)
						}
						wanted[root] = append(wanted[root], rel)
						placed, err := b.placeLoose(ledger, root, rel, f, dst, inc, opts)
						if err != nil {
							return err
						}
						if !placed {
							continue
						}
						copied++
						continue
					}
					if err := os.MkdirAll(dest, 0o755); err != nil {
						return fmt.Errorf("creating %s: %w", dest, err)
					}
					if err := copyFile(f, dst); err != nil {
						return fmt.Errorf("include: %s: %w", filepath.Base(f), err)
					}
					copied++
				}
			}
		}

		where := modName
		if inc.Into != "" {
			where = inc.Into
		}
		if copied > 0 {
			b.Report.Step("%-28s %d file(s) -> %s", "include "+inc.From, copied, where)
		} else {
			b.Report.Detail("include %s already current in %s", inc.From, where)
		}
	}

	return b.reconcilePlacements(ledger, ch, wanted, opts)
}

// includeFile is the -include allowlist handed to AddonBuilder.
//
// It is never omitted. Without -include, AddonBuilder falls back to a built-in
// extension list that does not cover .c, .p3d or .rvmat, so it binarises
// config.cpp, drops everything else and exits zero. The build reports an addon
// packed and the PBO holds a config and nothing to go with it.
//
// A repo's own .buildfiles wins when it has one. Otherwise the list is written
// from include_extensions in the manifest, which is the same content `init`
// would have scaffolded. That way the argv depends on the manifest rather than
// on whether a file happens to exist in the working tree.
func (b *Builder) includeFile(pdriveRepo string) (string, error) {
	if committed := filepath.Join(pdriveRepo, ".buildfiles"); fileExists(committed) {
		return committed, nil
	}

	dir := filepath.Join(os.TempDir(), "dayzmod", b.Mod.Mod.ID)
	generated := filepath.Join(dir, ".buildfiles")
	if b.Runner.DryRun() {
		return generated, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("include list: %w", err)
	}
	body := strings.Join(b.Mod.IncludeExtensions, ";")
	if err := os.WriteFile(generated, []byte(body+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("include list: writing %s: %w", generated, err)
	}
	return generated, nil
}

// loadPlacements opens the ledger of files written outside the tool's own
// output folders. A dry run gets an in-memory one it never saves.
func (b *Builder) loadPlacements() (*machine.Placements, error) {
	path, err := machine.PlacementsPath(b.Host.Path)
	if err != nil {
		return nil, err
	}
	return machine.LoadPlacements(path)
}

// placeLoose copies one file into a directory the tool does not own, and records
// it. It reports whether anything was written.
//
// The rules exist because the destination is a game install's own addons folder,
// holding hundreds of vanilla PBOs. Overwriting one of those, or a file an
// operator installed by hand, is not something a build should ever do quietly.
func (b *Builder) placeLoose(
	ledger *machine.Placements,
	root, rel, src, dst string,
	inc modcfg.IncludeSpec,
	opts Options,
) (bool, error) {
	sum, present, err := machine.FileSum(src)
	if err != nil {
		return false, fmt.Errorf("include: %s: %w", filepath.Base(src), err)
	}
	if !present {
		return false, fmt.Errorf("include: %s vanished before it could be copied", src)
	}

	onDisk, exists, err := machine.FileSum(dst)
	if err != nil {
		return false, fmt.Errorf("include: %s: %w", dst, err)
	}
	recorded, known := ledger.Placed(root, rel)

	switch {
	case exists && !known:
		return false, fmt.Errorf(
			"include: %s already exists at %s and this tool did not put it there; "+
				"move it aside if it should be replaced", filepath.Base(dst), dst)
	case exists && onDisk != recorded.SHA256 && onDisk != sum:
		return false, fmt.Errorf(
			"include: %s at %s has been changed since the build placed it; "+
				"delete it if the vendored copy should win", filepath.Base(dst), dst)
	case exists && onDisk == sum && !opts.Force:
		// Already the file we want.
		ledger.Record(root, rel, b.Mod.Root, inc.From, sum, machine.KindLoose)
		return false, nil
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, fmt.Errorf("creating %s: %w", filepath.Dir(dst), err)
	}
	if err := copyFile(src, dst); err != nil {
		return false, fmt.Errorf("include: %s: %w", filepath.Base(src), err)
	}
	ledger.Record(root, rel, b.Mod.Root, inc.From, sum, machine.KindLoose)
	return true, nil
}

// reconcilePlacements removes files this build no longer wants from the installs
// it writes to, then saves the ledger.
//
// This is what lets a build in one repo clean up after a build in another. The
// installs are shared, so a loose PBO left behind by a different pack is loaded
// by the engine just as readily as the current one.
func (b *Builder) reconcilePlacements(ledger *machine.Placements, ch *modcfg.Channel, wanted map[string][]string, opts Options) error {
	if b.Runner.DryRun() {
		return nil
	}

	for _, target := range ch.Deploy {
		root := b.Host.Paths.DayZClient
		if target == modcfg.TargetServer {
			root = b.Host.Paths.DayZServer
		}
		if root == "" {
			continue
		}
		for _, rel := range ledger.Stale(root, wanted[root], machine.KindLoose) {
			dst := filepath.Join(root, filepath.FromSlash(rel))
			recorded, _ := ledger.Placed(root, rel)

			onDisk, exists, err := machine.FileSum(dst)
			if err != nil {
				return fmt.Errorf("include: %s: %w", dst, err)
			}
			switch {
			case !exists:
				// Already gone, so there is nothing to report.
				ledger.Forget(root, rel)
			case onDisk != recorded.SHA256:
				b.Report.Detail("left %s alone: it has changed since the build placed it", rel)
			case opts.NoPrune:
				// The same escape hatch pruneDeployed honours: the folder is
				// deliberately holding something this build would remove.
				b.Report.Detail("stale %s left in place (--no-prune)", rel)
			default:
				if err := os.Remove(dst); err != nil {
					return fmt.Errorf("include: removing %s: %w", dst, err)
				}
				b.Report.Step("%-28s %s", "removed stale", rel)
				ledger.Forget(root, rel)
			}
		}
	}
	return ledger.Save()
}

// deployTo reports whether an include belongs in a given install.
func deployTo(side modcfg.Side, target modcfg.Target) bool {
	switch side {
	case "", modcfg.SideBoth:
		return true
	case modcfg.SideServer:
		return target == modcfg.TargetServer
	case modcfg.SideClient:
		return target == modcfg.TargetClient
	}
	return true
}

// sameFile reports whether dst already holds src, by size and modification
// time. Hashing a vendored mod on every build would cost more than the copy it
// saves.
func sameFile(src, dst string) bool {
	s, err := os.Stat(src)
	if err != nil {
		return false
	}
	d, err := os.Stat(dst)
	if err != nil {
		return false
	}
	return s.Size() == d.Size() && !s.ModTime().After(d.ModTime())
}

func (b *Builder) packerFor(ch *modcfg.Channel) (packer.Packer, error) {
	switch ch.Packer {
	case modcfg.PackerAddonBuilder:
		exe := b.Host.AddonBuilder()
		if exe == "" {
			return nil, fmt.Errorf("AddonBuilder is not configured; run `dayzmod config init`")
		}
		// Per repo, so two repos that both have an addon called Core do not share
		// one binarise cache. Under the system temp dir because that is what it
		// is: derived output that can be thrown away at any time.
		return &packer.AddonBuilder{
			Exe:     exe,
			TempDir: filepath.Join(os.TempDir(), "dayzmod", b.Mod.Mod.ID),
		}, nil
	case modcfg.PackerPboProject:
		exe := b.Host.PboProject()
		if exe == "" {
			return nil, fmt.Errorf("Mikero pboProject is not configured; set paths.mikero or run `dayzmod config init`")
		}
		return &packer.PboProject{Exe: exe}, nil
	default:
		return nil, fmt.Errorf("unknown packer %q", ch.Packer)
	}
}

// runHooks invokes the repo's own code generators. The tool knows nothing about
// what they do beyond how to run them and whether they worked.
func (b *Builder) runHooks(ctx context.Context, hooks []modcfg.Hook) error {
	return RunHooks(ctx, b.Mod.Root, b.Runner, b.Report, hooks)
}

// RunHooks executes a list of hooks, stopping at the first failure.
//
// Hook output only appears when a hook fails. A passing generator or test run
// has nothing to say, and nobody reads a build that prints hundreds of lines
// every time.
func RunHooks(ctx context.Context, root string, runner proc.Runner, report Reporter, hooks []modcfg.Hook) error {
	for _, h := range hooks {
		if len(h.Run) == 0 {
			continue
		}
		dir := root
		if h.Dir != "" {
			dir = filepath.Join(root, filepath.FromSlash(h.Dir))
		}
		report.Step("%-28s hook", h.String())

		res, err := runner.Run(ctx, proc.Cmd{Name: h.Run[0], Args: h.Run[1:], Dir: dir})
		if err != nil {
			return fmt.Errorf("hook %s failed: %w\n%s", h, err, res.Tail(30))
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("opening %s: %w", src, err)
	}
	defer in.Close()

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
	// The game may hold the previous PBO open, so replace it instead of
	// truncating it.
	if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
		os.Remove(tmp)
		return fmt.Errorf("replacing %s: %w (is the game or server still running?)", dst, err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		return fmt.Errorf("renaming onto %s: %w", dst, err)
	}
	return nil
}

func boolOrTrue(p *bool) bool {
	if p == nil {
		return true
	}
	return *p
}

func boolOrFalse(p *bool) bool {
	if p == nil {
		return false
	}
	return *p
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// recordDeployed notes a PBO the build copied into a game install, so a later
// run can tell its own output apart from anything else in that folder.
func (b *Builder) recordDeployed(root, dst string) error {
	ledger, err := b.loadPlacements()
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, dst)
	if err != nil {
		return fmt.Errorf("deploy: %w", err)
	}
	sum, _, err := machine.FileSum(dst)
	if err != nil {
		return fmt.Errorf("deploy: %w", err)
	}
	ledger.Record(root, rel, b.Mod.Root, "", sum, machine.KindAddon)
	return ledger.Save()
}

// pruneDeployed removes PBOs this build put in a game install and no longer
// produces.
//
// Renaming an addon leaves the old PBO in both installs, still registering its
// classes, and a rebuild does not touch it because nothing points at it any
// more. The ledger is what makes this safe: a PBO the tool never placed is left
// alone, whoever put it there.
func (b *Builder) pruneDeployed(ch *modcfg.Channel, opts Options) error {
	if b.Runner.DryRun() || opts.SkipDeploy || len(ch.Deploy) == 0 {
		return nil
	}

	// Every PBO this channel produces, by install-relative path.
	wanted := map[string]bool{}
	for _, set := range b.Mod.SetsFor(ch) {
		for _, addon := range set.AddonNames() {
			wanted[destKeyOf(filepath.Join(set.ModName, "Addons", addon+".pbo"))] = true
		}
	}

	ledger, err := b.loadPlacements()
	if err != nil {
		return err
	}

	for _, target := range ch.Deploy {
		root := b.Host.Paths.DayZClient
		if target == modcfg.TargetServer {
			root = b.Host.Paths.DayZServer
		}
		if root == "" {
			continue
		}
		for _, rel := range ledger.Stale(root, nil, machine.KindAddon) {
			// Only PBOs in a mod folder this channel builds into. Loose entries
			// and other repos' output are reconciled by deployIncludes.
			if !strings.EqualFold(filepath.Ext(rel), ".pbo") || wanted[destKeyOf(rel)] {
				continue
			}
			if placed, ok := ledger.Placed(root, rel); !ok || placed.Repo != b.Mod.Root {
				continue
			}
			dst := filepath.Join(root, filepath.FromSlash(rel))
			if opts.NoPrune {
				b.Report.Detail("stale %s left in place (--no-prune)", rel)
				continue
			}
			if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("deploy: removing %s: %w", dst, err)
			}
			b.Report.Step("%-28s %s", "removed stale", rel)
			ledger.Forget(root, rel)
		}
	}
	return ledger.Save()
}

// destKeyOf normalises an install-relative path the way the ledger does.
func destKeyOf(rel string) string {
	return strings.ToLower(filepath.ToSlash(filepath.Clean(rel)))
}
