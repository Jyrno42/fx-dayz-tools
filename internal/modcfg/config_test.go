package modcfg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jyrno42/fx-dayz-tools/internal/paths"
)

func load(t *testing.T, name string) *Config {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name+".yml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Parse(data)
	if err != nil {
		t.Fatalf("%s.yml did not parse:\n%v", name, err)
	}
	return cfg
}

// Every pipeline variant I actually use has to be expressible. If one of these
// stops parsing, the schema has regressed against a real repo.
func TestRealManifestsParse(t *testing.T) {
	for _, name := range []string{
		"project_with_everything",
		"project_with_public_release",
		"project_with_minimal_config",
		"project_with_compat_presets",
	} {
		t.Run(name, func(t *testing.T) { load(t, name) })
	}
}

func TestFullManifestShape(t *testing.T) {
	cfg := load(t, "project_with_everything")

	if got := cfg.AddonSetNames(); len(got) != 2 {
		t.Fatalf("addon sets = %v, want 2", got)
	}

	dev, err := cfg.Channel("dev")
	if err != nil {
		t.Fatal(err)
	}
	rel, err := cfg.Channel("release")
	if err != nil {
		t.Fatal(err)
	}

	// The dev-only set should reach dev and never release. Test content in a
	// shipped mod is the whole thing mod-dev/ exists to prevent.
	if got := len(cfg.SetsFor(dev)); got != 2 {
		t.Errorf("dev builds %d sets, want 2", got)
	}
	relSets := cfg.SetsFor(rel)
	if len(relSets) != 1 || relSets[0].Name != "main" {
		t.Errorf("release builds %v, want only [main]", setNames(relSets))
	}

	// Each set builds into its own mod folder.
	if got := cfg.AddonSets["main"].ModName; got != "@project-with-everything" {
		t.Errorf("main mod_name = %q", got)
	}
	if got := cfg.AddonSets["devspawn"].ModName; got != "@project-with-everything-dev" {
		t.Errorf("devspawn mod_name = %q", got)
	}

	// The per-PBO obfuscation split, i.e. logic obfuscated and models plain.
	main := cfg.AddonSets["main"]
	if !main.Addons["PWE_Core"].Policy.ObfuscateOr(false) {
		t.Error("PWE_Core should be obfuscated")
	}
	if main.Addons["PWE_Parts"].Policy.ObfuscateOr(false) {
		t.Error("PWE_Parts is a model PBO and must not be obfuscated")
	}

	// A release should never skip an addon just because a hash matched.
	if rel.ChangeDetection != DetectNone {
		t.Errorf("release change_detection = %q, want %q", rel.ChangeDetection, DetectNone)
	}
	if dev.ChangeDetection != DetectLockfile {
		t.Errorf("dev change_detection = %q, want %q", dev.ChangeDetection, DetectLockfile)
	}

	// A private mod has no business shipping its public key.
	if cfg.DistributeBikey(rel) {
		t.Error("a private mod must not distribute its .bikey")
	}

	// The dev-only mod should drop out of a release load order.
	devMods := cfg.Launch.ModsFor("dev")
	relMods := cfg.Launch.ModsFor("release")
	if len(devMods) != 5 {
		t.Errorf("dev mods = %d, want 5", len(devMods))
	}
	if len(relMods) != 4 {
		t.Errorf("release mods = %d, want 4", len(relMods))
	}
	for _, m := range relMods {
		if m.Name == "@project-with-everything-dev" {
			t.Error("the dev-only mod leaked into the release load order")
		}
	}

	// Load order matters, so it has to survive parsing unchanged.
	want := []string{"@CF", "@VPPAdminTools", "@project-with-everything", "@project-with-everything-dev", "@Extra Workshop Mod"}
	for i, m := range devMods {
		if m.Name != want[i] {
			t.Errorf("mod[%d] = %q, want %q -- load order must be preserved", i, m.Name, want[i])
		}
	}
}

// Prefixes are derived instead of written out per repo, and they need to match
// what the existing Taskfiles pass to AddonBuilder.
func TestPrefixDerivation(t *testing.T) {
	cases := []struct {
		manifest, set, addon, want string
	}{
		{"project_with_everything", "main", "PWE_Core", `projects\project-with-everything\mod\PWE_Core`},
		{"project_with_everything", "devspawn", "PWE_DevSpawn", `projects\project-with-everything\mod-dev\PWE_DevSpawn`},
		{"project_with_minimal_config", "main", "PMC_Main", `projects\minimal-config-repo\mod\PMC_Main`},
		{"project_with_compat_presets", "main", "ProjectWithCompatPresets", `projects\project-with-compat-presets\mod\ProjectWithCompatPresets`},
	}
	for _, tc := range cases {
		t.Run(tc.manifest+"/"+tc.addon, func(t *testing.T) {
			cfg := load(t, tc.manifest)
			set := cfg.AddonSets[tc.set]
			got, err := paths.Prefix(cfg.Repo.PDrivePath, set.Source, tc.addon)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("prefix = %q, want %q", got, tc.want)
			}
		})
	}
}

// Binarising is AddonBuilder's own default, and model.cfg only gets applied when
// binarising. So an addon that says nothing still has to come out binarised, or
// its model configuration quietly stops working.
func TestBinarizeDefaultsOn(t *testing.T) {
	for _, tc := range []struct{ manifest, set, addon string }{
		{"project_with_everything", "main", "PWE_Parts"},
		{"project_with_everything", "devspawn", "PWE_DevSpawn"},
		{"project_with_minimal_config", "main", "PMC_Main"},
		{"project_with_compat_presets", "main", "ProjectWithCompatPresets"},
	} {
		t.Run(tc.manifest+"/"+tc.addon, func(t *testing.T) {
			cfg := load(t, tc.manifest)
			p := cfg.AddonSets[tc.set].Addons[tc.addon].Policy
			if !p.BinarizeOr(false) {
				t.Error("binarize should default to true")
			}
		})
	}
}

// pboProject's -B covers config.cpp and mission.sqm, not models. The proven
// release command passes -B while models are still binarised.
func TestBinariseCppIsSeparateFromModelBinarize(t *testing.T) {
	cfg := load(t, "project_with_everything")
	rel, _ := cfg.Channel("release")

	if rel.PboProject.BinariseCpp == nil {
		t.Fatal("binarise_cpp must resolve to an explicit value")
	}
	if *rel.PboProject.BinariseCpp {
		t.Error("binarise_cpp should default to false, matching the proven -B release command")
	}
	if !cfg.AddonSets["main"].Addons["PWE_Parts"].Policy.BinarizeOr(false) {
		t.Error("model binarisation must stay on even though cpp binarisation is off")
	}
}

func TestPublicModShipsItsKey(t *testing.T) {
	cfg := load(t, "project_with_public_release")
	rel, err := cfg.Channel("release")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.DistributeBikey(rel) {
		t.Error("a public mod must ship its .bikey or admins cannot whitelist it")
	}
	if rel.Packer != PackerAddonBuilder {
		t.Errorf("packer = %q; this release is a plain signed mod, not a Mikero build", rel.Packer)
	}
	if rel.PboProject != nil {
		t.Error("an addonbuilder channel must not carry pboproject options")
	}
}

func TestMinimalManifestGetsSensibleDefaults(t *testing.T) {
	cfg := load(t, "project_with_minimal_config")

	if cfg.Repo.Lockfile != DefaultLockfile {
		t.Errorf("lockfile = %q, want %q", cfg.Repo.Lockfile, DefaultLockfile)
	}
	if len(cfg.IncludeExtensions) != len(DefaultIncludeExtensions) {
		t.Errorf("include_extensions = %d entries, want the default %d", len(cfg.IncludeExtensions), len(DefaultIncludeExtensions))
	}
	if cfg.Launch.Profile != DefaultProfile {
		t.Errorf("profile = %q, want %q", cfg.Launch.Profile, DefaultProfile)
	}
	// BattlEye on means the client has to go through DayZ_BE.exe.
	if cfg.Launch.ClientExe != ExeBE {
		t.Errorf("client_exe = %q, want %q when BattlEye is on", cfg.Launch.ClientExe, ExeBE)
	}
	if cfg.ScriptCheck.Settle.Duration() != DefaultSettle {
		t.Errorf("scriptcheck.settle = %v, want %v", cfg.ScriptCheck.Settle, DefaultSettle)
	}
	// The set inherits the mod name.
	if got := cfg.AddonSets["main"].ModName; got != "@project-with-minimal-config" {
		t.Errorf("mod_name = %q, want it to default from mod.name", got)
	}
	if got := cfg.AddonSets["main"].Addons["PMC_Main"].Policy.Side; got != SideBoth {
		t.Errorf("side = %q, want %q by default", got, SideBoth)
	}
}

// A repo that overrides the allowlist should keep its override.
func TestIncludeExtensionsOverride(t *testing.T) {
	cfg := load(t, "project_with_compat_presets")
	if len(cfg.IncludeExtensions) != 6 {
		t.Fatalf("include_extensions = %v, want the repo's own 6-entry list", cfg.IncludeExtensions)
	}
}

func TestPboProjectDefaultsAreExplicit(t *testing.T) {
	cfg := load(t, "project_with_everything")
	rel, _ := cfg.Channel("release")
	p := rel.PboProject
	if p == nil {
		t.Fatal("release has no pboproject options")
	}
	// pboProject inherits unset options from the GUI registry, so every one of
	// these has to resolve to a definite value instead of nil.
	for name, ptr := range map[string]*bool{
		"compress":             p.Compress,
		"noisy":                p.Noisy,
		"automake_stale":       p.AutomakeStale,
		"clean_temp":           p.CleanTemp,
		"encode_prefix":        p.EncodePrefix,
		"restore_gui_settings": p.RestoreGUISettings,
	} {
		if ptr == nil {
			t.Errorf("pboproject.%s is nil; it must default to an explicit value", name)
		}
	}
	if p.PrefixFile != PrefixAlways {
		t.Errorf("prefix_file = %q, want %q", p.PrefixFile, PrefixAlways)
	}
}

func TestDiagPresetTurnsBattlEyeOff(t *testing.T) {
	cfg := load(t, "project_with_everything")
	p, ok := cfg.Launch.Presets["diag"]
	if !ok {
		t.Fatal("a repo with diag enabled should get a diag preset")
	}
	if p.BattlEye == nil || *p.BattlEye {
		t.Error("the diag preset must turn BattlEye off: a diag client can only join a BattlEye-off server")
	}
}

func TestSignAcceptsBooleanOrMapping(t *testing.T) {
	cfg := load(t, "project_with_everything")
	dev, _ := cfg.Channel("dev")
	rel, _ := cfg.Channel("release")

	if dev.Sign.Enabled {
		t.Error("`sign: false` should disable signing")
	}
	if !rel.Sign.Enabled || rel.Sign.Key != "release-key" {
		t.Errorf("release sign = %+v, want enabled with a key", rel.Sign)
	}
}

func setNames(sets []*AddonSet) []string {
	out := make([]string, len(sets))
	for i, s := range sets {
		out[i] = s.Name
	}
	return out
}

func TestDurationParsing(t *testing.T) {
	cfg := load(t, "project_with_everything")
	if got := cfg.Launch.BootTimeout.Duration().Seconds(); got != 180 {
		t.Errorf("boot_timeout = %vs, want 180s", got)
	}
	if got := cfg.ScriptCheck.Settle.Duration().Seconds(); got != 40 {
		t.Errorf("settle = %vs, want 40s", got)
	}
}

func TestFindWalksUp(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "mod", "PWE_Core", "Scripts")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(root, FileName)
	if err := os.WriteFile(manifest, []byte("version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := Find(nested)
	if err != nil {
		t.Fatal(err)
	}
	if !paths.Equal(got, manifest) {
		t.Errorf("Find = %q, want %q", got, manifest)
	}

	if _, err := Find(t.TempDir()); err == nil {
		t.Error("Find should fail when there is no manifest anywhere above")
	} else if !strings.Contains(err.Error(), "dayzmod init") {
		t.Errorf("the error should say how to fix it, got: %v", err)
	}
}
