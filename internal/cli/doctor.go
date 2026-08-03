package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Jyrno42/fx-dayz-tools/internal/machine"
	"github.com/Jyrno42/fx-dayz-tools/internal/modcfg"
	"github.com/Jyrno42/fx-dayz-tools/internal/paths"
	"github.com/Jyrno42/fx-dayz-tools/internal/serverdz"
	"github.com/Jyrno42/fx-dayz-tools/internal/ui"
)

// doctor collects results so one run reports everything that is wrong, instead
// of failing on the first problem and hiding the rest.
type doctor struct {
	p    *ui.Printer
	host *machine.Config
	// rebased means the work drive is invisible to this process, so we are
	// checking paths on it against its backing directory instead.
	rebased  bool
	failures int
	warnings int
}

func (d *doctor) ok(label, detail string)   { d.p.Check(ui.OK, label, detail) }
func (d *doctor) info(label, detail string) { d.p.Check(ui.Info, label, detail) }

func (d *doctor) warn(label, detail string) {
	d.warnings++
	d.p.Check(ui.Warn, label, detail)
}

func (d *doctor) fail(label, detail string) {
	d.failures++
	d.p.Check(ui.Fail, label, detail)
}

// exists reports whether a path is really there, resolving it through the work
// drive's backing directory when the drive is invisible to this process. The
// second result means the file is present but only reachable that way.
func (d *doctor) exists(path string) (present, viaBacking bool) {
	if path == "" {
		return false, false
	}
	if _, err := os.Stat(path); err == nil {
		return true, false
	}
	if !d.rebased || d.host == nil {
		return false, false
	}
	alt, rebased := d.host.Inspect(path)
	if !rebased {
		return false, false
	}
	_, err := os.Stat(alt)
	return err == nil, err == nil
}

// exe checks that a required executable is present.
func (d *doctor) exe(label, path, why string) {
	switch {
	case path == "":
		d.fail(label, "not configured -- "+why)
	case !fileExists(path):
		d.fail(label, "missing: "+path)
	default:
		d.ok(label, path)
	}
}

// optionalExe checks a tool only some pipelines need.
func (d *doctor) optionalExe(label, path, why string) {
	switch {
	case path == "":
		d.info(label, "not configured ("+why+")")
	case !fileExists(path):
		d.warn(label, "missing: "+path)
	default:
		d.ok(label, path)
	}
}

func newDoctorCmd(g *global) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Verify the toolchain, the P: drive and this repo's setup",
		Long: "Checks everything a build depends on and reports all problems at once.\n\n" +
			"Run it from inside a mod repo to also validate that repo's dayz.yml and its\n" +
			"placement under the P: drive. Run it anywhere else to check the machine only.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			d := &doctor{p: g.printer()}

			host, err := g.host()
			if err != nil {
				// A file that exists but does not parse is a different problem
				// from no file at all, and `config init` is the wrong advice for
				// it: --force would rewrite a config that needed one line changed.
				if !errors.Is(err, machine.ErrNotConfigured) {
					d.p.Line("Machine configuration cannot be read:")
					d.p.Line("  %v", err)
					return coded(ExitConfig, "fix that, or regenerate the file with `dayzmod config init --force`")
				}
				d.p.Line("No machine configuration: %v", err)
				return coded(ExitPreflight, "run `dayzmod config init` first")
			}
			d.host = host

			d.checkDrive(host)
			d.checkTools(host)
			d.checkKeys(host)
			d.checkRepo(g, host)

			d.p.Line("")
			switch {
			case d.failures > 0:
				return coded(ExitPreflight, "%d problem(s) need fixing (%d warning(s))", d.failures, d.warnings)
			case d.warnings > 0:
				d.p.Line("No blocking problems. %d warning(s).", d.warnings)
			default:
				d.p.Line("Everything checks out.")
			}
			return nil
		},
	}
}

// checkDrive establishes whether the work drive resolves for THIS process, and
// records it so the downstream checks can report accurately instead of calling
// every file on it missing.
func (d *doctor) checkDrive(host *machine.Config) {
	d.p.Section("Work drive")

	visible := host.WorkDriveVisible()
	drive, err := paths.LookupDrive(host.PDrive.Letter)
	if err != nil {
		d.fail("work drive", err.Error())
		return
	}

	if !visible {
		// A subst lives in a per-logon-session device map, so a process started
		// from a different session or token simply does not have it, while another
		// shell on the same machine sees it fine. Report it once as the root
		// cause, because every path on the drive is about to look missing.
		d.fail(host.PDrive.Letter+" visible", "not reachable from this process")
		for _, line := range wrap(host.WorkDriveHint(), 72) {
			d.p.Detail("%s", line)
		}
		if host.PDrive.Backing != "" {
			d.rebased = true
			d.p.Detail("checking the paths below against %s instead", host.PDrive.Backing)
		}
		return
	}

	switch {
	case !drive.Mounted():
		// A subst does not survive a reboot, which makes this the most common
		// cause of a baffling first build after a restart.
		detail := "not mounted"
		if host.PDrive.Backing != "" {
			detail += fmt.Sprintf("; run `dayzmod pdrive mount` to map it onto %s", host.PDrive.Backing)
		}
		d.fail(drive.Letter+" mounted", detail)
	case drive.Kind == paths.DriveSubst:
		d.ok(drive.Letter+" mounted", "subst of "+drive.Backing)
		if host.PDrive.Backing != "" && !paths.Equal(host.PDrive.Backing, drive.Backing) {
			d.fail("work drive target",
				fmt.Sprintf("config says %s but it is mapped to %s", host.PDrive.Backing, drive.Backing))
		}
	default:
		d.ok(drive.Letter+" mounted", "real volume")
	}
}

// wrap breaks a long explanation into lines that fit the report.
func wrap(s string, width int) []string {
	var out []string
	var line string
	for _, word := range strings.Fields(s) {
		if line == "" {
			line = word
			continue
		}
		if len(line)+1+len(word) > width {
			out = append(out, line)
			line = word
			continue
		}
		line += " " + word
	}
	if line != "" {
		out = append(out, line)
	}
	return out
}

func (d *doctor) checkTools(host *machine.Config) {
	d.p.Section("Toolchain")

	d.exe("AddonBuilder", host.AddonBuilder(), "set paths.dayz_tools")
	d.exe("DSSignFile", host.DSSignFile(), "set paths.dayz_tools")
	d.optionalExe("DSCreateKey", host.DSCreateKey(), "needed only by `dayzmod keygen`")
	d.optionalExe("pboProject", host.PboProject(), "needed only for an obfuscated release")
	d.checkPboProjectVersion()
	d.checkDePboDllVersion()
	d.optionalExe("ExtractPbo", host.ExtractPbo(), "needed only to verify a packed prefix")

	d.p.Section("Game installs")
	d.exe("DayZ client", host.ClientExe("be"), "set paths.dayz_client")
	d.exe("DayZ server", host.ServerExe(false), "set paths.dayz_server")
	d.optionalExe("DayZ diag client", host.ClientExe("diag"), "needed only for diag mode")

	if host.Paths.Workshop == "" {
		d.info("workshop content", "not configured; !Workshop mod paths cannot be verified")
	} else if !dirExists(host.Paths.Workshop) {
		d.warn("workshop content", "missing: "+host.Paths.Workshop)
	} else {
		d.ok("workshop content", host.Paths.Workshop)
	}
}

// checkPboProjectVersion surfaces which pboProject is installed. The packer has
// only been exercised against one release and works around a version-specific
// quirk, so a mismatch is worth saying out loud instead of leaving someone to
// discover it through a failure.
func (d *doctor) checkPboProjectVersion() {
	installed, untested := machine.PboProjectUntested()
	if installed == "" {
		return
	}
	if untested {
		d.warn("pboProject version", "installed "+installed+", verified against "+
			strconv.Itoa(machine.TestedPboProjectVersion)+" -- the release packer is untested here")
		return
	}
	d.ok("pboProject version", installed+" (the version the packer was verified against)")
}

// checkDePboDllVersion reports the DePbo dll separately from pboProject. The dll
// is where obfuscation and compression run, it versions on its own cadence, and
// pboProject only states a minimum, so the pair can drift far enough apart to
// fail inside the dll with pboProject itself looking current.
func (d *doctor) checkDePboDllVersion() {
	installed, tooOld := machine.DePboDllTooOld()
	if installed == "" {
		return
	}
	if tooOld {
		d.warn("DePbo dll version", "installed "+installed+", but the packer needs at least "+
			strconv.Itoa(machine.MinDePboDllVersion)+" -- reinstall DePboTools")
		return
	}
	d.ok("DePbo dll version", installed)
}

func (d *doctor) checkKeys(host *machine.Config) {
	d.p.Section("Signing keys")

	if len(host.Keyrings) == 0 {
		d.info("keyrings", "none configured")
		return
	}
	for _, name := range host.KeyNames() {
		k, err := host.Key(name)
		if err != nil {
			continue
		}
		havePrivate, viaBacking := d.exists(k.Private)
		switch {
		case !havePrivate:
			// Only say "missing" when the file is genuinely absent. If the work
			// drive is invisible, the root cause is already reported above.
			d.fail("key "+name, "private key missing: "+k.Private)
		case k.Public == "":
			d.warn("key "+name, "private key only; a public mod also needs the .bikey")
		case viaBacking:
			d.ok("key "+name, "present (reached via the backing directory)")
		default:
			if havePublic, _ := d.exists(k.Public); !havePublic {
				d.warn("key "+name, "public key missing: "+k.Public)
			} else {
				d.ok("key "+name, filepath.Dir(k.Private))
			}
		}
	}
}

// checkRepo validates the mod manifest and the repo's placement under P:. It
// gets skipped outside a mod repo so `doctor` still works as a machine check.
func (d *doctor) checkRepo(g *global, host *machine.Config) {
	dir := g.dir
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return
		}
		dir = wd
	}
	manifest, err := modcfg.Find(dir)
	if err != nil {
		d.p.Section("Repository")
		d.info("dayz.yml", "not in a mod repo, skipping repo checks")
		return
	}

	d.p.Section("Repository")

	cfg, err := modcfg.Load(manifest)
	if err != nil {
		d.fail("dayz.yml", manifest)
		d.p.Detail("%v", err)
		return
	}
	d.ok("dayz.yml", manifest)

	d.checkRepoLocation(cfg, host)
	d.checkAddonSources(cfg)
	d.checkServerConfig(cfg)
	d.checkBuildFiles(cfg)
}

// checkRepoLocation replaces the hand-written `mklink /J` prerequisite comment
// that used to sit at the top of every Taskfile.
func (d *doctor) checkRepoLocation(cfg *modcfg.Config, host *machine.Config) {
	want := host.RepoPDrivePath(cfg.Repo.PDrivePath)

	if present, viaBacking := d.exists(want); !present {
		detail := want + " does not exist"
		if cfg.Repo.PDriveLinkFrom != "" {
			detail += "; run `dayzmod pdrive ensure` to create the junction"
		}
		d.fail("repo visible to the tools", detail)
		return
	} else if viaBacking {
		// The repo is where it should be. It is this process that cannot see the
		// drive.
		d.warn("repo visible to the tools", "present, but only via the backing directory")
		return
	}

	// The repo has to be reachable at that path AND actually be this repo.
	// Otherwise a stale junction left behind by a moved repo packs the wrong
	// sources.
	target, isLink, lerr := paths.LinkTarget(want)
	switch {
	case lerr != nil:
		d.fail("repo visible to the tools", lerr.Error())
	case isLink && cfg.Repo.PDriveLinkFrom != "":
		if paths.Equal(target, cfg.Repo.PDriveLinkFrom) {
			d.ok("repo visible to the tools", want+" -> "+target)
		} else {
			d.fail("repo junction target",
				fmt.Sprintf("%s points at %s, but dayz.yml says %s", want, target, cfg.Repo.PDriveLinkFrom))
		}
	case isLink:
		d.warn("repo visible to the tools",
			fmt.Sprintf("%s is a junction to %s, but dayz.yml declares no pdrive_link_from", want, target))
	case cfg.Repo.PDriveLinkFrom != "":
		d.warn("repo visible to the tools",
			fmt.Sprintf("%s is a real directory, but dayz.yml declares pdrive_link_from %s", want, cfg.Repo.PDriveLinkFrom))
	default:
		d.ok("repo visible to the tools", want)
	}

	// The derived prefix is what ends up inside every PBO, so show it.
	for _, sname := range cfg.AddonSetNames() {
		set := cfg.AddonSets[sname]
		prefix, err := paths.Prefix(cfg.Repo.PDrivePath, set.Source)
		if err != nil {
			d.fail("prefix for addon set "+sname, err.Error())
			continue
		}
		d.info("prefix ("+sname+")", prefix+`\<addon>`)
	}
}

func (d *doctor) checkAddonSources(cfg *modcfg.Config) {
	for _, sname := range cfg.AddonSetNames() {
		set := cfg.AddonSets[sname]
		for _, aname := range set.AddonNames() {
			dir := filepath.Join(cfg.Root, filepath.FromSlash(set.Source), aname)
			if !dirExists(dir) {
				d.fail("addon "+sname+"."+aname, "source directory missing: "+dir)
			}
		}
	}
}

func (d *doctor) checkServerConfig(cfg *modcfg.Config) {
	if cfg.Launch.ServerConfig == "" {
		return
	}
	path := filepath.Join(cfg.Root, filepath.FromSlash(cfg.Launch.ServerConfig))
	data, err := os.ReadFile(path)
	if err != nil {
		d.warn("server config", "missing: "+path)
		return
	}
	// Without a valid 32-bit instanceId the server does a graceful
	// config-validation termination about ten seconds in, BEFORE mission init.
	// It looks exactly like a mod failing to load.
	if !serverdz.HasInstanceID(string(data)) {
		d.fail("server config", path)
		d.p.Detail("no `instanceId` set: the server will terminate during config validation, before mission init")
		return
	}
	d.ok("server config", path)
}

func (d *doctor) checkBuildFiles(cfg *modcfg.Config) {
	path := filepath.Join(cfg.Root, ".buildfiles")
	if !fileExists(path) {
		d.info(".buildfiles", "not present; `dayzmod init --sync` writes it from include_extensions")
		return
	}
	d.ok(".buildfiles", path)
}

func fileExists(p string) bool {
	if p == "" {
		return false
	}
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func dirExists(p string) bool {
	if p == "" {
		return false
	}
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
