package dayz

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jyrno42/fx-dayz-tools/internal/modcfg"
)

func load(t *testing.T, name string) *modcfg.Config {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "modcfg", "testdata", name+".yml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := modcfg.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// The whole reason mods get declared once. The server takes bare names and the
// client needs !Workshop prefixes. The wrong form loads nothing, silently, with
// no error at all from the game.
func TestResolveModsAsymmetry(t *testing.T) {
	cfg := load(t, "project_with_everything")

	mods, err := ResolveMods(cfg, "dev", "")
	if err != nil {
		t.Fatal(err)
	}

	wantServer := []string{
		"@CF", "@VPPAdminTools", "@project-with-everything", "@project-with-everything-dev", "@Extra Workshop Mod",
	}
	wantClient := []string{
		`!Workshop\@CF`, `!Workshop\@VPPAdminTools`,
		"@project-with-everything", "@project-with-everything-dev", `!Workshop\@Extra Workshop Mod`,
	}

	assertList(t, "server", mods.Server, wantServer)
	assertList(t, "client", mods.Client, wantClient)

	// The mods this repo builds are in the game root, not !Workshop.
	for _, m := range mods.Client {
		if strings.Contains(m, "!Workshop") && strings.Contains(m, "project_with_everything") {
			t.Errorf("a mod built by this repo must not be looked up under !Workshop: %q", m)
		}
	}
}

func TestModArgs(t *testing.T) {
	cfg := load(t, "project_with_minimal_config")
	mods, err := ResolveMods(cfg, "dev", "")
	if err != nil {
		t.Fatal(err)
	}

	wantServer := "-mod=@CF;@VPPAdminTools;@project-with-minimal-config"
	if got := mods.ServerArg(); got != wantServer {
		t.Errorf("ServerArg = %q, want %q", got, wantServer)
	}
	wantClient := `-mod=!Workshop\@CF;!Workshop\@VPPAdminTools;@project-with-minimal-config`
	if got := mods.ClientArg(); got != wantClient {
		t.Errorf("ClientArg = %q, want %q", got, wantClient)
	}
}

// A dev-only mod has no business in a release load order.
func TestResolveModsDropsOtherChannels(t *testing.T) {
	cfg := load(t, "project_with_everything")

	dev, err := ResolveMods(cfg, "dev", "")
	if err != nil {
		t.Fatal(err)
	}
	rel, err := ResolveMods(cfg, "release", "")
	if err != nil {
		t.Fatal(err)
	}

	if !contains(dev.Server, "@project-with-everything-dev") {
		t.Error("the dev-only mod should load in the dev channel")
	}
	if contains(rel.Server, "@project-with-everything-dev") {
		t.Error("the dev-only mod leaked into the release load order")
	}
}

func TestPresetDropsMods(t *testing.T) {
	cfg := load(t, "project_with_everything")

	mods, err := ResolveMods(cfg, "dev", "no_vpp")
	if err != nil {
		t.Fatal(err)
	}
	for _, unwanted := range []string{"@CF", "@VPPAdminTools"} {
		if contains(mods.Server, unwanted) {
			t.Errorf("%s should have been dropped by the preset", unwanted)
		}
	}
	if !contains(mods.Server, "@project-with-everything") {
		t.Error("the preset dropped too much")
	}
	// Order among the survivors should be unchanged.
	if mods.Server[0] != "@project-with-everything" {
		t.Errorf("order changed: %v", mods.Server)
	}
}

// Added mods go on the end, since a mod that wraps another loads after it.
func TestPresetAddsModsAtTheEnd(t *testing.T) {
	cfg := load(t, "project_with_compat_presets")

	mods, err := ResolveMods(cfg, "dev", "full")
	if err != nil {
		t.Fatal(err)
	}
	if len(mods.Server) != 4 {
		t.Fatalf("server mods = %v, want 4", mods.Server)
	}
	if mods.Server[0] != "@ProjectWithCompatPresets" {
		t.Errorf("the declared mod should stay first, got %v", mods.Server)
	}
	if !contains(mods.Server, "@DependencyCore") || !contains(mods.Server, "@DependencySkills") {
		t.Errorf("preset mods missing: %v", mods.Server)
	}
}

func TestUnknownPresetIsAnError(t *testing.T) {
	cfg := load(t, "project_with_everything")
	_, err := ResolveMods(cfg, "dev", "nope")
	if err == nil {
		t.Fatal("expected an error for an unknown preset")
	}
	if !strings.Contains(err.Error(), "no_vpp") {
		t.Errorf("the error should list the available presets, got: %v", err)
	}
}

// BattlEye on needs DayZ_BE.exe. DayZ_x64.exe reports "Game Restart Required"
// and refuses.
func TestClientExeKind(t *testing.T) {
	cfg := load(t, "project_with_everything")

	if got := ClientExeKind(cfg, false, true); got != modcfg.ExeBE {
		t.Errorf("BattlEye on -> %q, want %q", got, modcfg.ExeBE)
	}
	if got := ClientExeKind(cfg, false, false); got != modcfg.ExePlain {
		t.Errorf("BattlEye off -> %q, want %q", got, modcfg.ExePlain)
	}
	// Diag wins, since it is the reason the flag was passed.
	if got := ClientExeKind(cfg, true, false); got != modcfg.ExeDiag {
		t.Errorf("diag -> %q, want %q", got, modcfg.ExeDiag)
	}
}

func TestBattlEyeAndPortOverrides(t *testing.T) {
	cfg := load(t, "project_with_everything")

	if !BattlEyeFor(cfg, "", nil) {
		t.Error("BattlEye should default on for this manifest")
	}
	// The generated diag preset turns it off.
	if BattlEyeFor(cfg, "diag", nil) {
		t.Error("the diag preset should turn BattlEye off")
	}
	on := true
	if !BattlEyeFor(cfg, "diag", &on) {
		t.Error("an explicit override should beat the preset")
	}

	if got := PortFor(cfg, "", 0); got != 2302 {
		t.Errorf("port = %d, want 2302", got)
	}
	if got := PortFor(cfg, "", 2402); got != 2402 {
		t.Errorf("port override = %d, want 2402", got)
	}
}

func assertList(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s mods = %v, want %v", label, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("%s mod[%d] = %q, want %q (order is load-bearing)", label, i, got[i], want[i])
		}
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// A server-side-only mod goes in -serverMod=, never in -mod=, and never reaches
// the client at all.
//
// Dropping its PBO into the server's own Addons folder is NOT an alternative.
// Verified on DayZ 1.29: the engine adds the package and registers the config,
// and the mod name even shows up in every script module's defines list, but it
// never compiles the mod's script modules. A script-based mod loaded that way
// silently does nothing.
func TestServerOnlyModUsesServerMod(t *testing.T) {
	yaml := `
version: 1
mod: { id: srv, name: "@srv", visibility: private }
repo: { pdrive_path: 'P:\projects\srv' }
addon_sets:
  main:
    source: mod
    addons:
      SrvCore: { policy: { side: server } }
channels:
  dev: { packer: addonbuilder, deploy: [server], sign: false }
launch:
  mods:
    - { name: "@CF", source: workshop }
    - { name: "@srv", source: self, side: server }
`
	cfg, err := modcfg.Parse([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	mods, err := ResolveMods(cfg, "dev", "")
	if err != nil {
		t.Fatal(err)
	}

	if got := mods.ServerModArg(); got != "-serverMod=@srv" {
		t.Errorf("ServerModArg = %q, want -serverMod=@srv", got)
	}
	if contains(mods.Server, "@srv") {
		t.Error("a server-only mod must not appear in -mod=")
	}
	if contains(mods.Client, "@srv") {
		t.Error("a server-only mod must never reach the client")
	}
	// Ordinary mods are unaffected.
	if !contains(mods.Server, "@CF") || !contains(mods.Client, `!Workshop\@CF`) {
		t.Errorf("ordinary mods broke: server=%v client=%v", mods.Server, mods.Client)
	}
}

// With no server-only mods the flag drops out entirely instead of going empty.
func TestServerModArgOmittedWhenUnused(t *testing.T) {
	cfg := load(t, "project_with_everything")
	mods, err := ResolveMods(cfg, "dev", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := mods.ServerModArg(); got != "" {
		t.Errorf("ServerModArg = %q, want empty", got)
	}
}

func TestClientOnlyModSkipsTheServer(t *testing.T) {
	yaml := `
version: 1
mod: { id: c, name: "@c", visibility: private }
repo: { pdrive_path: 'P:\projects\c' }
addon_sets:
  main: { source: mod, addons: { CCore: { policy: { side: client } } } }
channels:
  dev: { packer: addonbuilder, deploy: [client], sign: false }
launch:
  mods:
    - { name: "@c", source: self, side: client }
`
	cfg, err := modcfg.Parse([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	mods, err := ResolveMods(cfg, "dev", "")
	if err != nil {
		t.Fatal(err)
	}
	if contains(mods.Server, "@c") || contains(mods.ServerOnly, "@c") {
		t.Error("a client-only mod must not be loaded by the server")
	}
	if !contains(mods.Client, "@c") {
		t.Error("a client-only mod should load on the client")
	}
}

// An empty -mod= is not the same as omitting it, and a server-only mod leaves
// the client with no mods at all.
func TestEmptyModArgsAreOmitted(t *testing.T) {
	yaml := `
version: 1
mod: { id: srv, name: "@srv", visibility: private }
repo: { pdrive_path: 'P:\projects\srv' }
addon_sets:
  main: { source: mod, addons: { SrvCore: { policy: { side: server } } } }
channels:
  dev: { packer: addonbuilder, deploy: [server], sign: false }
launch:
  mods:
    - { name: "@srv", source: self, side: server }
`
	cfg, err := modcfg.Parse([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	mods, err := ResolveMods(cfg, "dev", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := mods.ServerArg(); got != "" {
		t.Errorf("ServerArg = %q, want empty so the flag is omitted", got)
	}
	if got := mods.ClientArg(); got != "" {
		t.Errorf("ClientArg = %q, want empty so the flag is omitted", got)
	}
	if got := mods.ServerModArg(); got != "-serverMod=@srv" {
		t.Errorf("ServerModArg = %q", got)
	}
}
