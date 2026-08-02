package serverdz

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHasInstanceID(t *testing.T) {
	cases := map[string]bool{
		"instanceId = 1;":                      true,
		"instanceId=1;":                        true,
		"  instanceId   =   42 ;":              true,
		"instanceid = 1;":                      true,
		"instanceId = 0;":                      true,
		"a=1;\r\ninstanceId = 1;\r\n":          true,
		"// instanceId = 1;":                   false,
		"hostname = \"x\"; // instanceId = 1;": false,
		"instanceId = 1":                       false,
		"instanceId = abc;":                    false,
		"instanceId = -1;":                     false,
		"":                                     false,
	}
	for cfg, want := range cases {
		if got := HasInstanceID(cfg); got != want {
			t.Errorf("HasInstanceID(%q) = %v, want %v", cfg, got, want)
		}
	}
}

// A diag client can only join a BattlEye-off server, so this patch has to work
// across the spacing variants a hand-edited config might use.
func TestSetBattlEye(t *testing.T) {
	cases := []struct {
		in   string
		on   bool
		want string
	}{
		{"BattlEye = 1;", false, "BattlEye = 0;"},
		{"BattlEye=1;", false, "BattlEye=0;"},
		{"  BattlEye   =   1  ;", false, "  BattlEye   =   0  ;"},
		{"battleye = 1;", false, "battleye = 0;"},
		{"BattlEye = 0;", true, "BattlEye = 1;"},
		{"a = 1;\nBattlEye = 1;\nb = 2;", false, "a = 1;\nBattlEye = 0;\nb = 2;"},
	}
	for _, tc := range cases {
		got, _ := SetBattlEye(tc.in, tc.on)
		if got != tc.want {
			t.Errorf("SetBattlEye(%q, %v) = %q, want %q", tc.in, tc.on, got, tc.want)
		}
	}
}

func TestSetBattlEyeIsIdempotent(t *testing.T) {
	once, changed := SetBattlEye("BattlEye = 1;", false)
	if !changed {
		t.Error("the first patch should report a change")
	}
	twice, changed := SetBattlEye(once, false)
	if changed {
		t.Error("re-applying the same value should report no change")
	}
	if once != twice {
		t.Errorf("not idempotent: %q then %q", once, twice)
	}
}

func TestBattlEyeEnabled(t *testing.T) {
	if !BattlEyeEnabled("hostname = \"x\";") {
		t.Error("absent BattlEye should read as enabled, matching the server default")
	}
	if BattlEyeEnabled("BattlEye = 0;") {
		t.Error("BattlEye = 0 should read as disabled")
	}
	if !BattlEyeEnabled("BattlEye = 1;") {
		t.Error("BattlEye = 1 should read as enabled")
	}
	if BattlEyeEnabled("// BattlEye = 1;\nBattlEye = 0;") {
		t.Error("a commented-out setting must not win over a live one")
	}
}

func TestDeployPatchesTheCopyNotTheSource(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "serverdz_local.cfg")
	dst := filepath.Join(dir, "deployed.cfg")

	original := "instanceId = 1;\nBattlEye = 1;\n"
	if err := os.WriteFile(src, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Deploy(src, dst, false); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "BattlEye = 0;") {
		t.Errorf("deployed config was not patched: %q", got)
	}

	// The tracked file in the repo should come out exactly as it went in.
	unchanged, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if string(unchanged) != original {
		t.Errorf("the source config was modified: %q", unchanged)
	}
}

// Deploy a config with no instanceId and you get a server that terminates during
// config validation, before mission init, with nothing useful logged.
func TestDeployRefusesConfigWithoutInstanceID(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "cfg")
	if err := os.WriteFile(src, []byte("BattlEye = 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Deploy(src, filepath.Join(dir, "out"), true)
	if err == nil {
		t.Fatal("expected an error for a config with no instanceId")
	}
	if !strings.Contains(err.Error(), "instanceId") {
		t.Errorf("the error should name instanceId, got: %v", err)
	}
}
