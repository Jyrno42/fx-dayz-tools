package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jyrno42/fx-dayz-tools/internal/machine"
	"github.com/Jyrno42/fx-dayz-tools/internal/modcfg"
	"github.com/Jyrno42/fx-dayz-tools/internal/proc"
)

// vendorDir builds a directory of prebuilt PBOs, one signed and one not. A real
// pack mixes the two, so an unsigned entry should not be an error.
func vendorDir(t *testing.T) (dir, keys string) {
	t.Helper()
	root := t.TempDir()
	dir = filepath.Join(root, "prebuilt")
	keys = filepath.Join(dir, "keys")
	if err := os.MkdirAll(keys, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Signed.pbo", "a")
	write("Signed.pbo.Vendor.bisign", "sig")
	write("Unsigned.pbo", "b")
	write("notes.txt", "ignored")
	if err := os.WriteFile(filepath.Join(keys, "Vendor.bikey"), []byte("k"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, keys
}

// oneStage wraps a single @mod folder as the stage table stageIncludes takes.
// Its ModName is the folder's own name, which is what an include with no
// mod_name resolves to.
func oneStage(dir string) ([]modStage, modStage) {
	st := modStage{ModName: filepath.Base(dir), Dir: dir}
	return []modStage{st}, st
}

func builderFor(t *testing.T, root string, includes []modcfg.IncludeSpec) *Builder {
	t.Helper()
	cfg, host := testRepo(t)
	cfg.Root = root
	cfg.Include = includes
	return &Builder{Mod: cfg, Host: host, Runner: &fakeRunner{}, Report: silent{}}
}

func TestStageIncludesCopiesPBOsAndSignatures(t *testing.T) {
	dir, _ := vendorDir(t)
	root := filepath.Dir(dir)
	stage := filepath.Join(t.TempDir(), "@pack")
	addons := filepath.Join(stage, "Addons")

	b := builderFor(t, root, nil)
	stages, primary := oneStage(stage)
	staged, err := b.stageIncludes([]modcfg.IncludeSpec{{From: "prebuilt"}}, stages, primary, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(staged) != 2 {
		t.Fatalf("staged %d, want 2 (the .txt must be ignored)", len(staged))
	}

	for _, name := range []string{"Signed.pbo", "Signed.pbo.Vendor.bisign", "Unsigned.pbo"} {
		if _, err := os.Stat(filepath.Join(addons, name)); err != nil {
			t.Errorf("%s was not staged: %v", name, err)
		}
	}
	// The original signature comes across as-is. Nothing gets re-signed here.
	if _, err := os.Stat(filepath.Join(addons, "notes.txt")); err == nil {
		t.Error("a non-PBO file was copied into the pack")
	}
	for _, a := range staged {
		if !a.Included {
			t.Errorf("%s should be marked as included, not packed", a.Name)
		}
	}
}

// A server pack is usually built for one operator who already trusts the keys,
// so ship_keys off has to suppress the vendored keys as well.
func TestStageIncludesHonoursShipKeys(t *testing.T) {
	dir, _ := vendorDir(t)
	root := filepath.Dir(dir)
	spec := []modcfg.IncludeSpec{{From: "prebuilt", Keys: "prebuilt/keys"}}

	stage := filepath.Join(t.TempDir(), "@pack")
	b := builderFor(t, root, nil)
	stages, primary := oneStage(stage)
	if _, err := b.stageIncludes(spec, stages, primary, nil, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(stage, "keys")); !os.IsNotExist(err) {
		t.Error("ship_keys off must not produce a keys folder")
	}

	stage2 := filepath.Join(t.TempDir(), "@pack")
	b2 := builderFor(t, root, nil)
	stages2, primary2 := oneStage(stage2)
	if _, err := b2.stageIncludes(spec, stages2, primary2, nil, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(stage2, "keys", "Vendor.bikey")); err != nil {
		t.Errorf("ship_keys on should ship the vendored key: %v", err)
	}
}

func TestStageIncludesSideAndOptional(t *testing.T) {
	dir, _ := vendorDir(t)
	root := filepath.Dir(dir)
	stage := filepath.Join(t.TempDir(), "@pack")

	b := builderFor(t, root, nil)
	stages, primary := oneStage(stage)
	staged, err := b.stageIncludes([]modcfg.IncludeSpec{
		{From: "prebuilt", Side: modcfg.SideServer},
		{From: "does-not-exist", Optional: true},
	}, stages, primary, nil, false)
	if err != nil {
		t.Fatalf("an optional missing directory should be skipped, got %v", err)
	}
	for _, a := range staged {
		if a.Side != modcfg.SideServer {
			t.Errorf("%s side = %q, want server", a.Name, a.Side)
		}
	}

	// Not optional, so a missing directory is a real error.
	b2 := builderFor(t, root, nil)
	_, err = b2.stageIncludes([]modcfg.IncludeSpec{{From: "does-not-exist"}},
		stages, primary, nil, false)
	if err == nil {
		t.Error("a missing non-optional include directory should fail")
	}
}

func TestStageIncludesRejectsEmptyFrom(t *testing.T) {
	b := builderFor(t, t.TempDir(), nil)
	stages, primary := oneStage("stage")
	_, err := b.stageIncludes([]modcfg.IncludeSpec{{}}, stages, primary, nil, false)
	if err == nil || !strings.Contains(err.Error(), "from") {
		t.Errorf("expected an error naming `from`, got %v", err)
	}
}

// The regression that matters. An entry with no mod_name has to keep landing in
// the primary folder, which is the only place they ever went.
func TestStageIncludesWithoutModNameLandInPrimary(t *testing.T) {
	dir, _ := vendorDir(t)
	root := filepath.Dir(dir)
	release := t.TempDir()

	primary := modStage{ModName: "@pack", Dir: filepath.Join(release, "@pack")}
	other := modStage{ModName: "@pack-server", Dir: filepath.Join(release, "@pack-server")}
	stages := []modStage{primary, other}

	b := builderFor(t, root, nil)
	staged, err := b.stageIncludes([]modcfg.IncludeSpec{{From: "prebuilt"}}, stages, primary, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range staged {
		if a.ModName != "@pack" {
			t.Errorf("%s went to %q, want the primary folder", a.Name, a.ModName)
		}
	}
	if _, err := os.Stat(filepath.Join(primary.Dir, "Addons", "Signed.pbo")); err != nil {
		t.Errorf("not staged into the primary folder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(other.Dir, "Addons", "Signed.pbo")); !os.IsNotExist(err) {
		t.Error("staged into a folder it did not ask for")
	}
}

// A prebuilt server-only PBO needs a folder of its own, because side alone only
// picks payloads and a client loading the folder loads every PBO in it. The key
// follows the folder, since that is the one the operator installs.
func TestStageIncludesModNameGetsItsOwnFolder(t *testing.T) {
	dir, _ := vendorDir(t)
	root := filepath.Dir(dir)
	release := t.TempDir()

	primary := modStage{ModName: "@pack", Dir: filepath.Join(release, "@pack")}
	other := modStage{ModName: "@pack-server", Dir: filepath.Join(release, "@pack-server")}
	stages := []modStage{primary, other}

	b := builderFor(t, root, nil)
	staged, err := b.stageIncludes([]modcfg.IncludeSpec{
		{From: "prebuilt", Keys: "prebuilt/keys", ModName: "@pack-server", Side: modcfg.SideServer},
	}, stages, primary, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range staged {
		if a.ModName != "@pack-server" {
			t.Errorf("%s went to %q, want @pack-server", a.Name, a.ModName)
		}
	}
	if _, err := os.Stat(filepath.Join(other.Dir, "Addons", "Signed.pbo")); err != nil {
		t.Errorf("not staged into its own folder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(primary.Dir, "Addons", "Signed.pbo")); !os.IsNotExist(err) {
		t.Error("a server-only include leaked into the primary folder")
	}
	if _, err := os.Stat(filepath.Join(other.Dir, "keys", "Vendor.bikey")); err != nil {
		t.Errorf("the key should follow the folder it belongs to: %v", err)
	}
	if _, err := os.Stat(filepath.Join(primary.Dir, "keys", "Vendor.bikey")); !os.IsNotExist(err) {
		t.Error("the key was shipped in the primary folder instead")
	}
}

// An included PBO landing on top of one this repo packed would overwrite it
// silently. The names are not known until the source directory is read, so
// validation cannot catch this and the pack has already written there.
func TestStageIncludesRefusesToOverwriteAPackedAddon(t *testing.T) {
	dir, _ := vendorDir(t)
	root := filepath.Dir(dir)
	stage := filepath.Join(t.TempDir(), "@pack")
	stages, primary := oneStage(stage)

	packed := []packedAddon{{Name: "Signed", ModName: primary.ModName}}

	b := builderFor(t, root, nil)
	_, err := b.stageIncludes([]modcfg.IncludeSpec{{From: "prebuilt"}}, stages, primary, packed, false)
	if err == nil {
		t.Fatal("expected an error when an include collides with a packed addon")
	}
	if !strings.Contains(err.Error(), "Signed") {
		t.Errorf("the error should name the colliding PBO, got %v", err)
	}
}

// A mod_name that is not a staged folder is caught rather than silently copying
// nowhere. Validation only checks that the name looks like a mod folder, since
// naming one no addon set builds into is allowed, so this is the check that
// catches a typo.
func TestStageIncludesRejectsUnstagedModName(t *testing.T) {
	dir, _ := vendorDir(t)
	root := filepath.Dir(dir)
	stage := filepath.Join(t.TempDir(), "@pack")
	stages, primary := oneStage(stage)

	b := builderFor(t, root, nil)
	_, err := b.stageIncludes([]modcfg.IncludeSpec{{From: "prebuilt", ModName: "@nope"}}, stages, primary, nil, false)
	if err == nil || !strings.Contains(err.Error(), "@nope") {
		t.Errorf("expected an error naming the unknown folder, got %v", err)
	}
}

func TestStageIncludesDryRunCopiesNothing(t *testing.T) {
	dir, _ := vendorDir(t)
	root := filepath.Dir(dir)
	stage := filepath.Join(t.TempDir(), "@pack")
	addons := filepath.Join(stage, "Addons")

	cfg, host := testRepo(t)
	cfg.Root = root
	b := &Builder{Mod: cfg, Host: host, Runner: &proc.Dry{}, Report: silent{}}

	stages, primary := oneStage(stage)
	staged, err := b.stageIncludes([]modcfg.IncludeSpec{{From: "prebuilt"}}, stages, primary, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(staged) != 2 {
		t.Errorf("a dry run should still report what it would stage, got %d", len(staged))
	}
	if _, err := os.Stat(addons); !os.IsNotExist(err) {
		t.Error("a dry run must not copy anything")
	}
}

// A pack's vendored dependency has to reach the game installs, or the dev loop
// cannot boot: the mod is simply not there. launch.mods cannot cover for it
// either, so the build has to put it in place.
func TestDeployIncludesReachesTheGameInstalls(t *testing.T) {
	dir, _ := vendorDir(t)
	root := filepath.Dir(dir)

	cfg, host := testRepo(t)
	cfg.Root = root
	cfg.Include = []modcfg.IncludeSpec{
		{From: "prebuilt"},
		{From: "prebuilt", ModName: "@vendor-server"},
	}
	client := t.TempDir()
	server := t.TempDir()
	host.Paths.DayZClient = client
	host.Paths.DayZServer = server

	b := &Builder{Mod: cfg, Host: host, Runner: &fakeRunner{}, Report: silent{}}
	ch, err := cfg.Channel("dev")
	if err != nil {
		t.Fatal(err)
	}
	ch.Deploy = []modcfg.Target{modcfg.TargetClient, modcfg.TargetServer}

	if err := b.deployIncludes(ch, Options{}); err != nil {
		t.Fatal(err)
	}

	for _, install := range []string{client, server} {
		// No mod_name rides in the primary folder, which is what the addon sets
		// build into.
		if _, err := os.Stat(filepath.Join(install, "@t", "Addons", "Signed.pbo")); err != nil {
			t.Errorf("%s: include did not reach the primary folder: %v", install, err)
		}
		// Its signature comes with it, since nothing re-signs an included PBO.
		if _, err := os.Stat(filepath.Join(install, "@t", "Addons", "Signed.pbo.Vendor.bisign")); err != nil {
			t.Errorf("%s: the original signature was left behind: %v", install, err)
		}
		// A named folder is what keeps a prebuilt server-only mod off the client,
		// the same way a packed one stays off it.
		if _, err := os.Stat(filepath.Join(install, "@vendor-server", "Addons", "Signed.pbo")); err != nil {
			t.Errorf("%s: include did not reach its own folder: %v", install, err)
		}
	}
}

// An include restricted to another channel must not reach the dev installs.
// Without this the deploy could ignore channels entirely and nothing would say
// so.
func TestDeployIncludesHonoursChannels(t *testing.T) {
	dir, _ := vendorDir(t)
	root := filepath.Dir(dir)

	cfg, host := testRepo(t)
	cfg.Root = root
	cfg.Include = []modcfg.IncludeSpec{{From: "prebuilt", Channels: []string{"release"}}}
	client := t.TempDir()
	host.Paths.DayZClient = client
	host.Paths.DayZServer = t.TempDir()

	b := &Builder{Mod: cfg, Host: host, Runner: &fakeRunner{}, Report: silent{}}
	ch, err := cfg.Channel("dev")
	if err != nil {
		t.Fatal(err)
	}
	ch.Deploy = []modcfg.Target{modcfg.TargetClient}

	if err := b.deployIncludes(ch, Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(client, "@t", "Addons", "Signed.pbo")); !os.IsNotExist(err) {
		t.Error("a release-only include reached the dev install")
	}
}

// Copying a vendored mod on every build would make the dev loop pay for files
// that never change.
func TestDeployIncludesSkipsUnchangedFiles(t *testing.T) {
	dir, _ := vendorDir(t)
	root := filepath.Dir(dir)

	cfg, host := testRepo(t)
	cfg.Root = root
	cfg.Include = []modcfg.IncludeSpec{{From: "prebuilt"}}
	client := t.TempDir()
	host.Paths.DayZClient = client
	host.Paths.DayZServer = t.TempDir()

	b := &Builder{Mod: cfg, Host: host, Runner: &fakeRunner{}, Report: silent{}}
	ch, err := cfg.Channel("dev")
	if err != nil {
		t.Fatal(err)
	}
	ch.Deploy = []modcfg.Target{modcfg.TargetClient}

	if err := b.deployIncludes(ch, Options{}); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(client, "@t", "Addons", "Signed.pbo")
	first, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}

	if err := b.deployIncludes(ch, Options{}); err != nil {
		t.Fatal(err)
	}
	second, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !first.ModTime().Equal(second.ModTime()) {
		t.Error("an unchanged include was copied again")
	}

	// --force copies regardless, which is what it is for.
	if err := b.deployIncludes(ch, Options{Force: true}); err != nil {
		t.Fatal(err)
	}
}

// devInstalls wires a builder to throwaway client and server installs.
func devInstalls(t *testing.T, root string, includes []modcfg.IncludeSpec) (*Builder, *modcfg.Channel, string, string) {
	t.Helper()
	cfg, host := testRepo(t)
	cfg.Root = root
	cfg.Include = includes
	client, server := t.TempDir(), t.TempDir()
	host.Paths.DayZClient = client
	host.Paths.DayZServer = server
	// Keep the ledger out of the real user config directory.
	host.Path = filepath.Join(t.TempDir(), "config.yml")

	b := &Builder{Mod: cfg, Host: host, Runner: &fakeRunner{}, Report: silent{}}
	ch, err := cfg.Channel("dev")
	if err != nil {
		t.Fatal(err)
	}
	ch.Deploy = []modcfg.Target{modcfg.TargetClient, modcfg.TargetServer}
	return b, ch, client, server
}

// The one with teeth. A loose PBO in a client's addons\ is loaded by the engine
// with no mod list able to exclude it, so side: server has to mean zero writes
// under the client install. An @mod folder tolerates a stray copy because
// launch.mods decides what loads; this does not.
func TestIntoWithServerSideNeverTouchesTheClient(t *testing.T) {
	dir, _ := vendorDir(t)
	root := filepath.Dir(dir)

	b, ch, client, server := devInstalls(t, root, []modcfg.IncludeSpec{
		{From: "prebuilt/Signed.pbo", Into: "addons", Side: modcfg.SideServer},
	})
	if err := b.deployIncludes(ch, Options{}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(server, "addons", "Signed.pbo")); err != nil {
		t.Errorf("the server install did not get it: %v", err)
	}
	// Nothing at all under the client, not just nothing in addons\.
	var found []string
	_ = filepath.WalkDir(client, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			found = append(found, p)
		}
		return nil
	})
	if len(found) != 0 {
		t.Errorf("side: server wrote %d file(s) into the client install: %v", len(found), found)
	}
}

// A single .pbo as `from` takes one file out of a folder holding several, which
// is what opt-in plugin sets need: taking the directory would ship the rest.
func TestIncludeFromASinglePbo(t *testing.T) {
	dir, _ := vendorDir(t)
	root := filepath.Dir(dir)

	b, ch, _, server := devInstalls(t, root, []modcfg.IncludeSpec{
		{From: "prebuilt/Signed.pbo", Into: "addons", Side: modcfg.SideServer},
	})
	if err := b.deployIncludes(ch, Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(server, "addons", "Signed.pbo")); err != nil {
		t.Errorf("the named PBO was not deployed: %v", err)
	}
	// Its signature rides along, and the sibling PBO does not.
	if _, err := os.Stat(filepath.Join(server, "addons", "Signed.pbo.Vendor.bisign")); err != nil {
		t.Errorf("the signature was left behind: %v", err)
	}
	if _, err := os.Stat(filepath.Join(server, "addons", "Unsigned.pbo")); !os.IsNotExist(err) {
		t.Error("naming one file still shipped the whole directory")
	}
}

// The install's addons directory holds hundreds of vanilla PBOs. Overwriting one
// of those, or anything an operator put there by hand, is not something a build
// should do quietly.
func TestIntoRefusesToClobberAFileItDidNotPlace(t *testing.T) {
	dir, _ := vendorDir(t)
	root := filepath.Dir(dir)

	b, ch, _, server := devInstalls(t, root, []modcfg.IncludeSpec{
		{From: "prebuilt/Signed.pbo", Into: "addons", Side: modcfg.SideServer},
	})
	if err := os.MkdirAll(filepath.Join(server, "addons"), 0o755); err != nil {
		t.Fatal(err)
	}
	squatter := filepath.Join(server, "addons", "Signed.pbo")
	if err := os.WriteFile(squatter, []byte("someone else's file"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := b.deployIncludes(ch, Options{})
	if err == nil {
		t.Fatal("expected a refusal to overwrite an unrecorded file")
	}
	if got, _ := os.ReadFile(squatter); string(got) != "someone else's file" {
		t.Error("the existing file was overwritten anyway")
	}
}

// The ledger is what makes into: reversible. Build one repo, then another, and
// the second clears what the first left in the shared install.
func TestLedgerRemovesWhatAnotherRepoLeftBehind(t *testing.T) {
	dir, _ := vendorDir(t)
	root := filepath.Dir(dir)

	first, ch, _, server := devInstalls(t, root, []modcfg.IncludeSpec{
		{From: "prebuilt/Signed.pbo", Into: "addons", Side: modcfg.SideServer},
	})
	if err := first.deployIncludes(ch, Options{}); err != nil {
		t.Fatal(err)
	}
	left := filepath.Join(server, "addons", "Signed.pbo")
	if _, err := os.Stat(left); err != nil {
		t.Fatalf("setup: the first repo did not place its file: %v", err)
	}

	// A second repo with its own source tree, sharing the install and the ledger.
	otherRoot := filepath.Join(t.TempDir(), "other-repo")
	if err := os.MkdirAll(filepath.Join(otherRoot, "prebuilt"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(otherRoot, "prebuilt", "Unsigned.pbo"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, ch2, _, _ := devInstalls(t, otherRoot, []modcfg.IncludeSpec{
		{From: "prebuilt/Unsigned.pbo", Into: "addons", Side: modcfg.SideServer},
	})
	second.Host.Paths.DayZServer = server
	second.Host.Path = first.Host.Path
	if err := second.deployIncludes(ch2, Options{}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(left); !os.IsNotExist(err) {
		t.Error("the other repo's loose PBO survived and would still be auto-loaded")
	}
	if _, err := os.Stat(filepath.Join(server, "addons", "Unsigned.pbo")); err != nil {
		t.Errorf("the second repo's file is missing: %v", err)
	}
}

// A file edited since the build placed it is left alone. Deleting someone's
// hand-edited copy without saying so is worse than leaving clutter behind.
func TestLedgerLeavesAChangedFileAlone(t *testing.T) {
	dir, _ := vendorDir(t)
	root := filepath.Dir(dir)

	b, ch, _, server := devInstalls(t, root, []modcfg.IncludeSpec{
		{From: "prebuilt/Signed.pbo", Into: "addons", Side: modcfg.SideServer},
	})
	if err := b.deployIncludes(ch, Options{}); err != nil {
		t.Fatal(err)
	}
	placed := filepath.Join(server, "addons", "Signed.pbo")
	if err := os.WriteFile(placed, []byte("edited by hand"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The include is gone, so reconciliation would otherwise remove the file.
	b.Mod.Include = nil
	if err := b.deployIncludes(ch, Options{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(placed); string(got) != "edited by hand" {
		t.Error("a file changed since it was placed should not be deleted")
	}
}

// The rename case: nothing points at the old PBO any more, so no rebuild removes
// it and it keeps registering its classes (see pruneDeployed).
func TestPruneRemovesARenamedAddonsOldPbo(t *testing.T) {
	cfg, host := testRepo(t)
	server := t.TempDir()
	host.Paths.DayZServer = server
	host.Path = filepath.Join(t.TempDir(), "config.yml")

	b := &Builder{Mod: cfg, Host: host, Runner: &fakeRunner{}, Report: silent{}}
	ch, err := cfg.Channel("dev")
	if err != nil {
		t.Fatal(err)
	}

	addons := filepath.Join(server, "@t", "Addons")
	if err := os.MkdirAll(addons, 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(addons, "OldName.pbo")
	if err := os.WriteFile(old, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The tool placed it on an earlier run, under its previous name.
	if err := b.recordDeployed(server, old); err != nil {
		t.Fatal(err)
	}

	if err := b.pruneDeployed(ch, Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("the renamed addon's old PBO survived and would still register its classes")
	}
}

// A PBO the tool never placed is left alone, whoever put it there. Without the
// ledger this would delete a hand-installed mod.
func TestPruneLeavesUnrecordedPbosAlone(t *testing.T) {
	cfg, host := testRepo(t)
	server := t.TempDir()
	host.Paths.DayZServer = server
	host.Path = filepath.Join(t.TempDir(), "config.yml")

	b := &Builder{Mod: cfg, Host: host, Runner: &fakeRunner{}, Report: silent{}}
	ch, err := cfg.Channel("dev")
	if err != nil {
		t.Fatal(err)
	}

	addons := filepath.Join(server, "@t", "Addons")
	if err := os.MkdirAll(addons, 0o755); err != nil {
		t.Fatal(err)
	}
	theirs := filepath.Join(addons, "SomeoneElses.pbo")
	if err := os.WriteFile(theirs, []byte("not ours"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := b.pruneDeployed(ch, Options{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(theirs); err != nil {
		t.Error("a PBO the tool never placed must not be removed")
	}
}

// --no-prune is the escape hatch for a folder someone is deliberately keeping
// something in.
func TestNoPruneKeepsStalePbos(t *testing.T) {
	cfg, host := testRepo(t)
	server := t.TempDir()
	host.Paths.DayZServer = server
	host.Path = filepath.Join(t.TempDir(), "config.yml")

	b := &Builder{Mod: cfg, Host: host, Runner: &fakeRunner{}, Report: silent{}}
	ch, err := cfg.Channel("dev")
	if err != nil {
		t.Fatal(err)
	}

	addons := filepath.Join(server, "@t", "Addons")
	if err := os.MkdirAll(addons, 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(addons, "OldName.pbo")
	if err := os.WriteFile(old, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := b.recordDeployed(server, old); err != nil {
		t.Fatal(err)
	}

	if err := b.pruneDeployed(ch, Options{NoPrune: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); err != nil {
		t.Error("--no-prune should keep it")
	}
}

// The regression: a full build must not delete the addons it just deployed.
func TestPruneKeepsWhatThisBuildJustDeployed(t *testing.T) {
	cfg, host := testRepo(t)
	server := t.TempDir()
	host.Paths.DayZServer = server
	host.Path = filepath.Join(t.TempDir(), "config.yml")

	b := &Builder{Mod: cfg, Host: host, Runner: &fakeRunner{}, Report: silent{}}

	if _, err := b.Build(context.Background(), Options{Channel: "dev"}); err != nil {
		t.Fatal(err)
	}

	deployed := filepath.Join(server, "@t", "Addons", "TestAddon.pbo")
	if _, err := os.Stat(deployed); err != nil {
		t.Fatalf("the addon this build produced was removed by the prune: %v", err)
	}
}

// The regression. Two reconcilers share the ledger: deployIncludes owns loose
// into: files, pruneDeployed owns packed PBOs. deployIncludes runs second, and
// when it treated every entry as its own it deleted every addon the build had
// just deployed, leaving an install holding only the vendored prebuilts.
//
// The earlier prune tests all passed because none of them declared an include,
// so the second reconciler never ran.
func TestIncludesAndPackedAddonsSurviveTheSameBuild(t *testing.T) {
	dir, _ := vendorDir(t)
	vendorRoot := filepath.Dir(dir)

	cfg, host := testRepo(t)
	server := t.TempDir()
	host.Paths.DayZServer = server

	// The include's source has to sit under the repo root.
	pre := filepath.Join(cfg.Root, "prebuilt")
	if err := os.MkdirAll(pre, 0o755); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(vendorRoot, "prebuilt", "Signed.pbo"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pre, "Vendored.pbo"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg.Include = []modcfg.IncludeSpec{{From: "prebuilt"}}

	b := &Builder{Mod: cfg, Host: host, Runner: &fakeRunner{}, Report: silent{}}
	if _, err := b.Build(context.Background(), Options{Channel: "dev"}); err != nil {
		t.Fatal(err)
	}

	addons := filepath.Join(server, "@t", "Addons")
	if _, err := os.Stat(filepath.Join(addons, "TestAddon.pbo")); err != nil {
		t.Errorf("the packed addon was deleted by the include reconciler: %v", err)
	}
	if _, err := os.Stat(filepath.Join(addons, "Vendored.pbo")); err != nil {
		t.Errorf("the include did not survive: %v", err)
	}

	// And again, since the second run is where a reconciler sees the other's
	// entries already in the ledger.
	if _, err := b.Build(context.Background(), Options{Channel: "dev", Force: true}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"TestAddon.pbo", "Vendored.pbo"} {
		if _, err := os.Stat(filepath.Join(addons, name)); err != nil {
			t.Errorf("%s did not survive a second build: %v", name, err)
		}
	}
}

func looseLeftBehind(t *testing.T, host *machine.Config) string {
	t.Helper()
	dst := filepath.Join(host.Paths.DayZServer, "addons", "Vendored_Server.pbo")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("theirs"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum, _, err := machine.FileSum(dst)
	if err != nil {
		t.Fatal(err)
	}
	path, err := machine.PlacementsPath(host.Path)
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := machine.LoadPlacements(path)
	if err != nil {
		t.Fatal(err)
	}
	ledger.Record(host.Paths.DayZServer, "addons/Vendored_Server.pbo",
		filepath.Join(t.TempDir(), "some-other-repo"), "vendor/x.pbo", sum, machine.KindLoose)
	if err := ledger.Save(); err != nil {
		t.Fatal(err)
	}
	return dst
}

func TestBuildWithNoIncludesSweepsOtherReposLoosePbos(t *testing.T) {
	cfg, host := testRepo(t)
	host.Paths.DayZServer = t.TempDir()
	dst := looseLeftBehind(t, host)

	b := &Builder{Mod: cfg, Host: host, Runner: &fakeRunner{}, Report: silent{}}
	ch, err := cfg.Channel("dev")
	if err != nil {
		t.Fatal(err)
	}
	if err := b.deployIncludes(ch, Options{}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Error("the other repo's loose PBO survived; the engine will keep auto-loading it")
	}
}

func TestNoPruneKeepsOtherReposLoosePbos(t *testing.T) {
	cfg, host := testRepo(t)
	host.Paths.DayZServer = t.TempDir()
	dst := looseLeftBehind(t, host)

	b := &Builder{Mod: cfg, Host: host, Runner: &fakeRunner{}, Report: silent{}}
	ch, err := cfg.Channel("dev")
	if err != nil {
		t.Fatal(err)
	}
	if err := b.deployIncludes(ch, Options{NoPrune: true}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(dst); err != nil {
		t.Errorf("--no-prune should keep it: %v", err)
	}
}
