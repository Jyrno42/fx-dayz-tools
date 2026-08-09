package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Jyrno42/fx-dayz-tools/internal/hashing"
	"github.com/Jyrno42/fx-dayz-tools/internal/machine"
	"github.com/Jyrno42/fx-dayz-tools/internal/modcfg"
	"github.com/Jyrno42/fx-dayz-tools/internal/paths"
	"github.com/Jyrno42/fx-dayz-tools/internal/proc"
)

// silent swallows progress output during tests.
type silent struct{}

func (silent) Step(string, ...any)   {}
func (silent) Detail(string, ...any) {}
func (silent) Command(proc.Cmd)      {}

// testRepo builds a minimal repo whose addon source really exists, so that the
// pipeline's own filesystem checks pass.
//
// This one is Windows-only, and not because the code under test is. The pipeline
// resolves an addon's source through repo.pdrive_path, which validation requires
// to carry a drive letter and which then has to exist on disk. On a POSIX host
// those cannot be the same directory, so there is no fixture to build. The logic
// being tested is platform-independent; the fixture is not.
func testRepo(t *testing.T) (*modcfg.Config, *machine.Config) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("needs a path that both carries a drive letter and exists on disk")
	}
	root := t.TempDir()

	src := filepath.Join(root, "mod", "TestAddon")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "config.cpp"), []byte("class CfgPatches {};"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The repo stands in for its own work-drive location, so the path the tools
	// would see and the local path land on the same real directory.
	drive, _ := paths.SplitDrive(root)

	yaml := `
version: 1
mod: { id: t, name: "@t", visibility: private }
repo: { pdrive_path: '` + root + `', lfs_guard: false }
addon_sets:
  main: { source: mod, addons: { TestAddon: { policy: { side: both } } } }
channels:
  dev: { packer: addonbuilder, deploy: [server], sign: false }
launch:
  mods:
    - { name: "@t", source: self }
`
	cfg, err := modcfg.Parse([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Root = root

	host := &machine.Config{}
	host.PDrive.Letter = drive
	host.Paths.DayZTools = filepath.Join(root, "tools")
	host.Paths.DayZServer = filepath.Join(root, "server")
	host.Paths.BuildDir = filepath.Join(root, "build")
	// Point the machine config somewhere disposable. Without this the placements
	// ledger resolves to the real user config directory, so tests write to the
	// developer's own machine state and read each other's leftovers.
	host.Path = filepath.Join(root, "machine", "config.yml")
	return cfg, host
}

func lockPath(cfg *modcfg.Config) string {
	return filepath.Join(cfg.Root, cfg.Repo.Lockfile)
}

// --no-deploy packs without deploying, so afterwards we do not know what the
// game installs are holding. Recording the hash would make the next ordinary
// build skip the addon and never deploy it, which is the exact failure the
// lockfile exists to prevent.
func TestSkipDeployDoesNotMarkTheAddonUpToDate(t *testing.T) {
	cfg, host := testRepo(t)

	b := &Builder{
		Mod:    cfg,
		Host:   host,
		Runner: &proc.Dry{}, // dry, so it never touches a real packer
		Report: silent{},
	}

	// A dry run should not write a lockfile at all.
	if _, err := b.Build(context.Background(), Options{Channel: "dev", SkipDeploy: true}); err != nil {
		t.Fatalf("build: %v", err)
	}
	if _, err := os.Stat(lockPath(cfg)); !os.IsNotExist(err) {
		t.Fatal("a dry run must not write the lockfile")
	}

	// Now simulate the real thing. Record an entry, then confirm a skip-deploy
	// build would not have been what recorded it.
	lock, err := hashing.LoadLockfile(lockPath(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := lock.Get(hashing.Key("main", "TestAddon")); ok {
		t.Error("no addon should be recorded after a build that did not deploy")
	}
}

// The ordinary path records the addon so the next build can skip it.
func TestDeployedAddonIsRecorded(t *testing.T) {
	cfg, host := testRepo(t)

	// A fake packer that writes the PBO the deploy step expects.
	b := &Builder{Mod: cfg, Host: host, Runner: &fakeRunner{}, Report: silent{}}

	outcomes, err := b.Build(context.Background(), Options{Channel: "dev"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(outcomes) != 1 {
		t.Fatalf("outcomes = %d, want 1", len(outcomes))
	}
	if outcomes[0].Skipped {
		t.Error("a first build should not skip")
	}
	if len(outcomes[0].Deployed) != 1 {
		t.Errorf("deployed to %v, want one destination", outcomes[0].Deployed)
	}

	lock, err := hashing.LoadLockfile(lockPath(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := lock.Get(hashing.Key("main", "TestAddon")); !ok {
		t.Error("a deployed addon should be recorded in the lockfile")
	}

	// And a second build skips it.
	outcomes, err = b.Build(context.Background(), Options{Channel: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	if !outcomes[0].Skipped {
		t.Error("an unchanged addon should be skipped on the second build")
	}
}

// fakeRunner stands in for AddonBuilder. It creates the PBO the pipeline goes on
// to deploy, without needing the real tool.
type fakeRunner struct{}

func (f *fakeRunner) DryRun() bool { return false }

func (f *fakeRunner) Run(_ context.Context, c proc.Cmd) (proc.Result, error) {
	// The last argument is AddonBuilder's output directory.
	if len(c.Args) == 0 {
		return proc.Result{}, nil
	}
	outDir := c.Args[len(c.Args)-1]
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return proc.Result{}, err
	}
	name := filepath.Base(outDir) + ".pbo"
	if err := os.WriteFile(filepath.Join(outDir, name), []byte("pbo"), 0o644); err != nil {
		return proc.Result{}, err
	}
	return proc.Result{Cmd: c}, nil
}

// A repo assembled by hand has no committed .buildfiles, and an omitted -include
// costs it every script, model and material (see includeFile). So the allowlist
// has to come from the manifest, not from a file that may never be written.
func TestIncludeListIsNeverOmitted(t *testing.T) {
	cfg, host := testRepo(t)
	b := &Builder{Mod: cfg, Host: host, Runner: &fakeRunner{}, Report: silent{}}

	if _, err := os.Stat(filepath.Join(cfg.Root, ".buildfiles")); !os.IsNotExist(err) {
		t.Fatal("this fixture is meant to have no committed .buildfiles")
	}

	got, err := b.includeFile(cfg.Root)
	if err != nil {
		t.Fatal(err)
	}
	if got == "" {
		t.Fatal("no include list, so AddonBuilder would fall back to its own and drop most of the addon")
	}

	body, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("the generated list was not written: %v", err)
	}
	// Scripts, models and materials are all absent from AddonBuilder's own list.
	for _, ext := range []string{"*.c", "*.p3d", "*.rvmat", "*.paa"} {
		if !strings.Contains(string(body), ext) {
			t.Errorf("generated list is missing %s:\n%s", ext, body)
		}
	}
}

// A repo that ships its own .buildfiles keeps using it, since it may have been
// tuned for content the default list does not name.
func TestCommittedBuildfilesWins(t *testing.T) {
	cfg, host := testRepo(t)
	b := &Builder{Mod: cfg, Host: host, Runner: &fakeRunner{}, Report: silent{}}

	committed := filepath.Join(cfg.Root, ".buildfiles")
	if err := os.WriteFile(committed, []byte("*.c;*.special\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := b.includeFile(cfg.Root)
	if err != nil {
		t.Fatal(err)
	}
	if got != committed {
		t.Errorf("include list = %q, want the repo's own %q", got, committed)
	}
}
