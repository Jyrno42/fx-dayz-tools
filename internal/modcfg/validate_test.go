package modcfg

import (
	"strings"
	"testing"
)

// base is a minimal valid manifest that individual tests mutate.
const base = `
version: 1
mod:
  id: test-mod
  name: "@test-mod"
  visibility: private
repo:
  pdrive_path: 'P:\projects\test-mod'
addon_sets:
  main:
    source: mod
    addons:
      TestAddon:
        policy: { side: both }
channels:
  dev:
    packer: addonbuilder
    deploy: [client, server]
    sign: false
launch:
  mods:
    - { name: "@test-mod", source: self }
`

func TestBaseManifestIsValid(t *testing.T) {
	if _, err := Parse([]byte(base)); err != nil {
		t.Fatalf("the base fixture must be valid, got:\n%v", err)
	}
}

func TestValidationRejects(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantMsg string
	}{
		{
			name:    "unknown field",
			yaml:    base + "\nunknown_toplevel: 1\n",
			wantMsg: "unknown_toplevel",
		},
		{
			name:    "missing version",
			yaml:    strings.Replace(base, "version: 1", "", 1),
			wantMsg: "version",
		},
		{
			name:    "future schema version",
			yaml:    strings.Replace(base, "version: 1", "version: 99", 1),
			wantMsg: "upgrade",
		},
		{
			name:    "mod name without @",
			yaml:    strings.Replace(base, `name: "@test-mod"`, `name: "test-mod"`, 1),
			wantMsg: "must start with @",
		},
		{
			name:    "missing pdrive_path",
			yaml:    strings.Replace(base, `pdrive_path: 'P:\projects\test-mod'`, "lockfile: x.json", 1),
			wantMsg: "pdrive_path is required",
		},
		{
			name:    "pdrive_path at a drive root has no prefix",
			yaml:    strings.Replace(base, `P:\projects\test-mod`, `P:\`, 1),
			wantMsg: "prefix",
		},
		{
			name:    "unknown packer",
			yaml:    strings.Replace(base, "packer: addonbuilder", "packer: makepbo", 1),
			wantMsg: "packer",
		},
		{
			name:    "bad side",
			yaml:    strings.Replace(base, "side: both", "side: sideways", 1),
			wantMsg: "side",
		},
		{
			name:    "channel references a missing addon set",
			yaml:    strings.Replace(base, "packer: addonbuilder", "packer: addonbuilder\n    addon_sets: [nope]", 1),
			wantMsg: "not defined under addon_sets",
		},
		{
			name:    "duplicate mod in the load order",
			yaml:    base + `    - { name: "@test-mod", source: self }` + "\n",
			wantMsg: "twice",
		},
		{
			name:    "mod name without @ in the load order",
			yaml:    strings.Replace(base, `- { name: "@test-mod", source: self }`, `- { name: "test-mod", source: self }`, 1),
			wantMsg: "must start with @",
		},
		{
			name:    "preset drops a mod that is not loaded",
			yaml:    base + "  presets:\n    x:\n      drop_mods: [\"@NotThere\"]\n",
			wantMsg: "not in launch.mods",
		},
		{
			name:    "source path without a path",
			yaml:    strings.Replace(base, `source: self }`, `source: path }`, 1),
			wantMsg: "no path",
		},
		{
			name:    "hook with no command",
			yaml:    base + "hooks:\n  pre_build:\n    - { name: broken }\n",
			wantMsg: "no run command",
		},
		{
			name:    "bad duration",
			yaml:    base + "scriptcheck:\n  settle: soon\n",
			wantMsg: "not a duration",
		},
		{
			name:    "addon set with no addons",
			yaml:    strings.Replace(base, "      TestAddon:\n        policy: { side: both }", "", 1),
			wantMsg: "no addons",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.yaml))
			if err == nil {
				t.Fatalf("expected an error mentioning %q, got none", tc.wantMsg)
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.wantMsg)) {
				t.Errorf("error should mention %q so the user knows what to fix, got:\n%v", tc.wantMsg, err)
			}
		})
	}
}

// A private mod publishing its .bikey lets anyone whitelist a repack, so it
// should be impossible to do that by accident.
func TestPrivateModCannotDistributeItsKey(t *testing.T) {
	yaml := `
version: 1
mod: { id: t, name: "@t", visibility: private }
repo: { pdrive_path: 'P:\projects\t' }
addon_sets:
  main:
    source: mod
    addons:
      Logic: { policy: { side: both } }
channels:
  release:
    packer: addonbuilder
    sign:
      key: test-key
      distribute_bikey: true
    payloads:
      all: { sides: [both] }
launch:
  mods:
    - { name: "@t", source: self }
`
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Fatal("expected an error: a private mod must not distribute its .bikey")
	}
	if !strings.Contains(err.Error(), "private") {
		t.Errorf("error should explain the visibility conflict, got: %v", err)
	}
}

// Turn off -R and every run rewrites the persisted pboProject options, which
// quietly changes what a later manual GUI build does.
func TestPboProjectRequiresRestoreFlag(t *testing.T) {
	yaml := `
version: 1
mod: { id: t, name: "@t", visibility: private }
repo: { pdrive_path: 'P:\projects\t' }
addon_sets:
  main:
    source: mod
    addons:
      Logic: { policy: { side: both } }
channels:
  release:
    packer: pboproject
    sign: { key: test-key }
    pboproject:
      restore_gui_settings: false
    payloads:
      all: { sides: [both] }
launch:
  mods:
    - { name: "@t", source: self }
`
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Fatal("expected an error when restore_gui_settings is false")
	}
	if !strings.Contains(err.Error(), "-R") {
		t.Errorf("error should name the -R flag, got: %v", err)
	}
}

// An addon that gets built and then dropped on the floor is wasted time at best,
// and a missing file in a shipped payload at worst.
func TestServerOnlyAddonNeedsAPayload(t *testing.T) {
	yaml := `
version: 1
mod: { id: t, name: "@t", visibility: private }
repo: { pdrive_path: 'P:\projects\t' }
addon_sets:
  main:
    source: mod
    addons:
      Logic:  { policy: { side: both } }
      Server: { policy: { side: server } }
channels:
  release:
    packer: addonbuilder
    sign: { key: k }
    payloads:
      client: { sides: [both, client] }
launch:
  mods:
    - { name: "@t", source: self }
`
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Fatal("expected an error: the server-only addon has no payload to land in")
	}
	if !strings.Contains(err.Error(), "discarded") {
		t.Errorf("error should say the addon would be discarded, got: %v", err)
	}
}

// single_pass cannot honour a per-addon obfuscation policy, so a manifest that
// declares a mixed policy alongside it contradicts itself.
func TestSinglePassRejectsMixedObfuscation(t *testing.T) {
	yaml := `
version: 1
mod: { id: t, name: "@t", visibility: private }
repo: { pdrive_path: 'P:\projects\t' }
addon_sets:
  main:
    source: mod
    addons:
      Logic:  { policy: { obfuscate: true,  side: both } }
      Models: { policy: { obfuscate: false, side: both } }
channels:
  release:
    packer: pboproject
    sign: { key: k }
    pboproject: { single_pass: true }
    payloads:
      all: { sides: [both] }
launch:
  mods:
    - { name: "@t", source: self }
`
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Fatal("expected an error: single_pass cannot apply a per-addon policy")
	}
	if !strings.Contains(err.Error(), "single_pass") {
		t.Errorf("error should name single_pass, got: %v", err)
	}
}

// BattlEye on with the plain executable gets you "Game Restart Required" and no
// useful diagnostic, so catch it at config time instead.
func TestBattlEyeRequiresTheBELauncher(t *testing.T) {
	yaml := base + "  client_exe: plain\n  battleye: true\n"
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Fatal("expected an error: BattlEye requires launching through DayZ_BE.exe")
	}
	if !strings.Contains(err.Error(), "DayZ_BE.exe") {
		t.Errorf("error should name DayZ_BE.exe, got: %v", err)
	}
}

// Turning BattlEye off should make the plain launcher the default instead of
// leaving a contradiction sitting there.
func TestBattlEyeOffSelectsThePlainLauncher(t *testing.T) {
	cfg, err := Parse([]byte(base + "  battleye: false\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Launch.ClientExe != ExePlain {
		t.Errorf("client_exe = %q, want %q when BattlEye is off", cfg.Launch.ClientExe, ExePlain)
	}
}

// Several problems should come back together, not one per run.
func TestValidationReportsEveryProblemAtOnce(t *testing.T) {
	yaml := strings.NewReplacer(
		`name: "@test-mod"`, `name: "test-mod"`,
		"packer: addonbuilder", "packer: makepbo",
		"side: both", "side: sideways",
	).Replace(base)

	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Fatal("expected errors")
	}
	msg := err.Error()
	for _, want := range []string{"must start with @", "packer", "side"} {
		if !strings.Contains(msg, want) {
			t.Errorf("all problems should be reported at once; %q missing from:\n%v", want, msg)
		}
	}
}

// Some vendored source may not be redistributed obfuscated, a condition that
// exists so the code stays auditable. A channel setting does not get to override
// that, so asking for it is a config error instead of a silent override.
func TestAllowObfuscationFalseRefusesObfuscation(t *testing.T) {
	yaml := `
version: 1
mod: { id: pack, name: "@pack", visibility: private }
repo: { pdrive_path: 'P:\projects\pack' }
addon_sets:
  vendored:
    source: vendor/Upstream
    allow_obfuscation: false
    addons:
      Upstream: { policy: { obfuscate: true, side: server } }
channels:
  release:
    packer: pboproject
    sign: { key: k }
    payloads:
      all: { sides: [both, server] }
launch:
  mods:
    - { name: "@pack", source: self }
`
	_, err := Parse([]byte(yaml))
	if err == nil {
		t.Fatal("expected an error: this set may not be obfuscated")
	}
	if !strings.Contains(err.Error(), "allow_obfuscation") {
		t.Errorf("the error should name the setting, got: %v", err)
	}
	if !strings.Contains(err.Error(), "auditable") {
		t.Errorf("the error should say why the restriction exists, got: %v", err)
	}
}

// The same set is fine so long as nobody asks for it to be obfuscated.
func TestAllowObfuscationFalseIsOtherwiseValid(t *testing.T) {
	yaml := `
version: 1
mod: { id: pack, name: "@pack", visibility: private }
repo: { pdrive_path: 'P:\projects\pack' }
addon_sets:
  vendored:
    source: vendor/Upstream
    allow_obfuscation: false
    addons:
      Upstream: { prefix: 'Upstream', policy: { side: server } }
channels:
  dev: { packer: addonbuilder, deploy: [server], sign: false }
launch:
  mods:
    - { name: "@pack", source: self, side: server }
`
	cfg, err := Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("a vendored set that is not obfuscated should be valid: %v", err)
	}
	if cfg.AddonSets["vendored"].ObfuscationAllowed() {
		t.Error("allow_obfuscation: false was not applied")
	}
	if got := cfg.AddonSets["vendored"].Addons["Upstream"].Prefix; got != "Upstream" {
		t.Errorf("per-addon prefix = %q, want the exact upstream value", got)
	}
}
