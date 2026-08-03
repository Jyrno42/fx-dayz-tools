package pipeline

import (
	"os"
	"path/filepath"
	"runtime"
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
		if filepath.Base(pl.Dir) != "payload-server" {
			t.Errorf("payload dir = %q, want the folder holding both @mod dirs", pl.Dir)
		}
		return
	}
	t.Fatal("no server payload")
}

type dryRunner struct{ fakeRunner }

func (dryRunner) DryRun() bool { return true }
