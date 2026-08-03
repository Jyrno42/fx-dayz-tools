package pipeline

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Jyrno42/fx-dayz-tools/internal/machine"
	"github.com/Jyrno42/fx-dayz-tools/internal/modcfg"
	"github.com/Jyrno42/fx-dayz-tools/internal/paths"
)

// splitRepo stands in for a mod whose server-authoritative logic builds into its
// own @mod folder, so it can be loaded through -serverMod= and never reach a
// client. Two addon sets, two mod_names, one release channel.
func splitRepo(t *testing.T) (*modcfg.Config, *machine.Config) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("needs a path that both carries a drive letter and exists on disk")
	}
	root := t.TempDir()

	for _, dir := range []string{`mod\Core`, `mod-server\ServerOnly`} {
		full := filepath.Join(root, dir)
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(full, "config.cpp"), []byte("class CfgPatches {};"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	drive, _ := paths.SplitDrive(root)

	yaml := `
version: 1
mod: { id: t, name: "@t", visibility: private }
repo: { pdrive_path: '` + root + `', lfs_guard: false }
addon_sets:
  main:
    source: mod
    mod_name: "@t"
    addons: { Core: { policy: { side: both } } }
  server:
    source: mod-server
    mod_name: "@t-server"
    addons: { ServerOnly: { policy: { side: server } } }
channels:
  release:
    packer: pboproject
    addon_sets: [main, server]
    change_detection: none
    sign: false
    payloads:
      client: { sides: [both, client] }
      server: { sides: [server] }
launch:
  mods:
    - { name: "@t", source: self }
    - { name: "@t-server", source: self, side: server }
`
	cfg, err := modcfg.Parse([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Root = root

	host := &machine.Config{}
	host.PDrive.Letter = drive
	host.Paths.DayZTools = filepath.Join(root, "tools")
	host.Paths.ReleaseDir = filepath.Join(root, "release")
	host.Paths.Mikero = filepath.Join(root, "mikero")
	return cfg, host
}

// An addon set that declares its own mod_name has to pack into its own @mod
// folder. Merging a server-only PBO into the client's folder ships it to every
// client, which is the one thing the split exists to prevent. It would do so
// silently too, because the pack itself succeeds either way.
func TestReleaseStagesEachAddonSetInItsOwnModFolder(t *testing.T) {
	cfg, host := splitRepo(t)
	b := &Builder{Mod: cfg, Host: host, Report: silent{}}

	ch, err := cfg.Channel("release")
	if err != nil {
		t.Fatal(err)
	}

	stages := b.releaseStages(ch)
	if len(stages) != 2 {
		t.Fatalf("expected one stage per mod_name, got %d: %+v", len(stages), stages)
	}
	if stages[0].ModName != "@t" || stages[1].ModName != "@t-server" {
		t.Errorf("stages should follow the declared set order: %+v", stages)
	}

	for _, set := range cfg.SetsFor(ch) {
		got := b.stageDirFor(ch, set)
		want := filepath.Join(host.Paths.ReleaseDir, set.ModName)
		if got != want {
			t.Errorf("set %s stages into %q, want %q", set.Name, got, want)
		}
	}
}

// The payload split decides which bundles a PBO reaches. Which FOLDER it lives
// in is a separate question, and answering both with one name is what put a
// server-only addon in the client's mod folder.
func TestPayloadsGroupByModFolder(t *testing.T) {
	cfg, host := splitRepo(t)
	b := &Builder{Mod: cfg, Host: host, Report: silent{}, Runner: &dryRunner{}}

	ch, err := cfg.Channel("release")
	if err != nil {
		t.Fatal(err)
	}
	stages := b.releaseStages(ch)
	packed := []packedAddon{
		{Name: "Core", Side: modcfg.SideBoth, ModName: "@t"},
		{Name: "ServerOnly", Side: modcfg.SideServer, ModName: "@t-server"},
	}

	payloads, err := b.assemblePayloads(ch, stages, packed, ReleaseOptions{NoZip: true})
	if err != nil {
		t.Fatal(err)
	}

	byName := map[string]Payload{}
	for _, pl := range payloads {
		byName[pl.Name] = pl
	}

	client, ok := byName["client"]
	if !ok {
		t.Fatal("no client payload")
	}
	// The whole split rests on this one assertion.
	for _, a := range client.Addons {
		if a == "ServerOnly" {
			t.Error("the server-only addon reached the client payload")
		}
	}
	if len(client.ModNames) != 1 || client.ModNames[0] != "@t" {
		t.Errorf("client payload folders = %v, want [@t]", client.ModNames)
	}

	server, ok := byName["server"]
	if !ok {
		t.Fatal("no server payload")
	}
	if len(server.Addons) != 1 || server.Addons[0] != "ServerOnly" {
		t.Errorf("server payload addons = %v, want [ServerOnly]", server.Addons)
	}
	if len(server.ModNames) != 1 || server.ModNames[0] != "@t-server" {
		t.Errorf("server payload folders = %v, want [@t-server]", server.ModNames)
	}
	// An operator extracting this must not get a folder that collides with the
	// client mod they already have.
	if filepath.Base(server.Dir) != "@t-server" {
		t.Errorf("server payload dir = %q, want it to end in @t-server", server.Dir)
	}
}

// A payload fed by two sets holds two folders, and they go into the archive
// side by side rather than merged into one.
func TestPayloadSpanningTwoSetsKeepsTheFoldersApart(t *testing.T) {
	cfg, host := splitRepo(t)
	ch, err := cfg.Channel("release")
	if err != nil {
		t.Fatal(err)
	}
	// Widen the server payload to take the shared addons too.
	ch.Payloads["server"].Sides = []modcfg.Side{modcfg.SideBoth, modcfg.SideServer}

	b := &Builder{Mod: cfg, Host: host, Report: silent{}, Runner: &dryRunner{}}
	packed := []packedAddon{
		{Name: "Core", Side: modcfg.SideBoth, ModName: "@t"},
		{Name: "ServerOnly", Side: modcfg.SideServer, ModName: "@t-server"},
	}

	payloads, err := b.assemblePayloads(ch, b.releaseStages(ch), packed, ReleaseOptions{NoZip: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, pl := range payloads {
		if pl.Name != "server" {
			continue
		}
		if len(pl.ModNames) != 2 {
			t.Fatalf("server payload folders = %v, want both", pl.ModNames)
		}
		// With several folders the payload directory is their parent, and
		// zipRoot is empty so they extract unmerged.
		if pl.zipRoot != "" {
			t.Errorf("zipRoot = %q, want empty so the folders stay separate", pl.zipRoot)
		}
		// Namespaced by mod id, because release_dir is shared between repos.
		if filepath.Base(pl.Dir) != "t-payload-server" {
			t.Errorf("payload dir = %q, want the folder holding both @mod dirs", pl.Dir)
		}
		return
	}
	t.Fatal("no server payload")
}

// A repo with no payloads: declared ships its staged folders as the single
// deliverable. Before includes could name their own folder there was only ever
// one, so the branch used the primary and ignored the rest. An include folder
// would then be staged, populated and signed around, and never zipped or hashed:
// a green build missing a folder.
func TestNoPayloadsShipsEveryStagedFolder(t *testing.T) {
	cfg, host := splitRepo(t)
	ch, err := cfg.Channel("release")
	if err != nil {
		t.Fatal(err)
	}
	ch.Payloads = nil

	b := &Builder{Mod: cfg, Host: host, Report: silent{}, Runner: &dryRunner{}}
	stages := b.releaseStages(ch)
	packed := []packedAddon{
		{Name: "Core", Side: modcfg.SideBoth, ModName: "@t"},
		{Name: "ServerOnly", Side: modcfg.SideServer, ModName: "@t-server"},
	}

	payloads, err := b.assemblePayloads(ch, stages, packed, ReleaseOptions{NoZip: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(payloads) != 1 {
		t.Fatalf("expected one payload, got %d", len(payloads))
	}
	pl := payloads[0]
	if len(pl.ModNames) != len(stages) {
		t.Errorf("payload folders = %v, want all %d staged folders", pl.ModNames, len(stages))
	}
	// zipRoot empty means Dir already holds the folders, so they extract unmerged.
	if pl.zipRoot != "" {
		t.Errorf("zipRoot = %q, want empty so the folders stay separate", pl.zipRoot)
	}
	// Gathered under its own directory, never the shared release dir, which
	// holds other repos' output too.
	if filepath.Base(pl.Dir) != "t-payload-all" {
		t.Errorf("payload dir = %q, want a namespaced payload-all directory", pl.Dir)
	}
	if pl.Dir == host.Paths.ReleaseDir {
		t.Error("zipping the shared release dir would sweep up other repos")
	}
}

// An include that names its own folder has to get a stage, and it has to come
// after the set stages so stages[0] stays the primary everything resolves
// against. Without the stage the folder is never created and payload assembly
// cannot find it.
func TestReleaseStagesIncludeModNames(t *testing.T) {
	cfg, host := splitRepo(t)
	cfg.Include = []modcfg.IncludeSpec{
		{From: "vendor/plain"},
		{From: "vendor/server", ModName: "@vendor-server"},
	}

	ch, err := cfg.Channel("release")
	if err != nil {
		t.Fatal(err)
	}
	b := &Builder{Mod: cfg, Host: host, Report: silent{}}
	stages := b.releaseStages(ch)

	if stages[0].ModName != "@t" {
		t.Errorf("stages[0] = %q, want the primary to stay first", stages[0].ModName)
	}
	var names []string
	for _, st := range stages {
		names = append(names, st.ModName)
	}
	if len(stages) != 3 {
		t.Fatalf("stages = %v, want the two sets plus the include folder", names)
	}
	if stages[2].ModName != "@vendor-server" {
		t.Errorf("stages = %v, want the include folder appended last", names)
	}
	// An include with no mod_name adds nothing; it rides in the primary.
	for _, st := range stages {
		if st.ModName == "" {
			t.Error("an include without mod_name should not create a stage")
		}
	}
}

type dryRunner struct{ fakeRunner }

func (dryRunner) DryRun() bool { return true }

// only narrows a run to one payload, so a fixture that declares several does not
// fail on the ones a test did not stage an addon for.
func only(name string) ReleaseOptions {
	return ReleaseOptions{NoZip: true, Payloads: []string{name}}
}

// realBuilder assembles for real, since these defects are all about what ends up
// on disk rather than what the payload struct says.
func realBuilder(t *testing.T) (*Builder, *modcfg.Config, *machine.Config) {
	t.Helper()
	cfg, host := splitRepo(t)
	return &Builder{Mod: cfg, Host: host, Report: silent{}, Runner: &fakeRunner{}}, cfg, host
}

// stageAddon puts a PBO where packRelease would have left it.
func stageAddon(t *testing.T, host *machine.Config, modName, addon string) string {
	t.Helper()
	dir := filepath.Join(host.Paths.ReleaseDir, modName, "Addons")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, addon+".pbo")
	if err := os.WriteFile(p, []byte(addon), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// The archive and the manifest are built by walking the payload directory, not
// from the addon list, so a file left there by a previous run ships and gets a
// hash line. Rename an addon or drop one and the old PBO goes out with the
// release while the summary lists only the current set.
func TestPayloadDirIsClearedBetweenRuns(t *testing.T) {
	b, cfg, host := realBuilder(t)
	ch, err := cfg.Channel("release")
	if err != nil {
		t.Fatal(err)
	}
	stages := b.releaseStages(ch)
	pbo := stageAddon(t, host, "@t", "Core")
	packed := []packedAddon{{Name: "Core", Side: modcfg.SideBoth, ModName: "@t", PBO: pbo}}

	if _, err := b.assemblePayloads(ch, stages, packed, only("client")); err != nil {
		t.Fatal(err)
	}

	// An addon that existed last time and does not any more.
	stale := filepath.Join(b.payloadDir(ch, "client"), "@t", "Addons", "Renamed.pbo")
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := b.assemblePayloads(ch, stages, packed, only("client")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Error("a PBO from a previous run survived into the payload and would ship")
	}
}

// The stage-level assertion cannot cover the artifact: the archive is built from
// the payload directory. Ship keys once, turn ship_keys off, and the old .bikey
// is still sitting there while the run reports that no keys shipped. For a
// private mod that hands anyone the ability to whitelist a repack.
func TestPayloadCannotShipAKeyAfterShipKeysIsTurnedOff(t *testing.T) {
	b, cfg, host := realBuilder(t)
	ch, err := cfg.Channel("release")
	if err != nil {
		t.Fatal(err)
	}
	stages := b.releaseStages(ch)
	pbo := stageAddon(t, host, "@t", "Core")
	packed := []packedAddon{{Name: "Core", Side: modcfg.SideBoth, ModName: "@t", PBO: pbo}}

	if _, err := b.assemblePayloads(ch, stages, packed, only("client")); err != nil {
		t.Fatal(err)
	}

	// What a previous run left behind while it was still distributing the key.
	keyDir := filepath.Join(b.payloadDir(ch, "client"), "@t", "keys")
	if err := os.MkdirAll(keyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	leaked := filepath.Join(keyDir, "t.bikey")
	if err := os.WriteFile(leaked, []byte("key"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := b.assemblePayloads(ch, stages, packed, only("client")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(leaked); !os.IsNotExist(err) {
		t.Error("a .bikey from an earlier run survived into a payload that ships no keys")
	}
}

// release_dir is shared between repos. Two of them both declaring a payload
// called client would assemble into one directory and merge, so the name has to
// carry something repo-specific.
func TestPayloadDirsAreNamespacedPerRepo(t *testing.T) {
	b, cfg, _ := realBuilder(t)
	ch, err := cfg.Channel("release")
	if err != nil {
		t.Fatal(err)
	}

	dir := b.payloadDir(ch, "client")
	if !strings.Contains(filepath.Base(dir), cfg.Mod.ID) {
		t.Errorf("payload dir %q does not carry the mod id, so two repos would collide", dir)
	}
	if filepath.Base(dir) == "payload-client" {
		t.Error("payload dir is not namespaced")
	}
}

// release_dir is shared between repos, so a bare RELEASE_MANIFEST.txt means
// whichever repo released last owns the only copy. The zip name already carries
// the mod id; the manifest has to as well.
func TestManifestNameIsNamespacedPerRepo(t *testing.T) {
	b, cfg, host := realBuilder(t)
	ch, err := cfg.Channel("release")
	if err != nil {
		t.Fatal(err)
	}
	ch.Manifest.Enabled = true
	ch.Manifest.Algo = "sha256"
	// Release creates this while staging; writeManifest on its own does not.
	if err := os.MkdirAll(host.Paths.ReleaseDir, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := b.writeManifest(ch, &ReleaseResult{}, ReleaseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(filepath.Base(got), cfg.Mod.ID) {
		t.Errorf("manifest %q does not carry the mod id, so two repos would overwrite each other", got)
	}
	if filepath.Base(got) == "RELEASE_MANIFEST.txt" {
		t.Error("manifest name is not namespaced")
	}

	// An explicit name is left exactly as written.
	ch.Manifest.Out = "MY_MANIFEST.txt"
	got, err = b.writeManifest(ch, &ReleaseResult{}, ReleaseOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "MY_MANIFEST.txt" {
		t.Errorf("explicit manifest.out was rewritten to %q", got)
	}
}
