package scaffold

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Jyrno42/fx-dayz-tools/internal/modcfg"
	"github.com/Jyrno42/fx-dayz-tools/internal/paths"
)

func TestAddonName(t *testing.T) {
	cases := map[string]string{
		// A short leading segment gets treated as an initialism, which is how
		// these repos tend to be named.
		"xy-toolkit":                  "XYToolkit",
		"project_with_everything":     "ProjectWithEverything",
		"project_with_compat_presets": "ProjectWithCompatPresets",
		"simple":                      "Simple",
		"under_score":                 "UnderScore",
		"":                            "Main",
	}
	for in, want := range cases {
		if got := AddonName(in); got != want {
			t.Errorf("AddonName(%q) = %q, want %q", in, got, want)
		}
	}
}

// skipWithoutWindowsPaths guards the tests that hand Derive a drive-lettered
// path. Derive calls filepath.Abs, which on a POSIX host reads `E:\projects\x`
// as a relative name and prepends the working directory, so the assertions
// below describe nothing real there. What they cover is not Windows-specific;
// the notation is.
func skipWithoutWindowsPaths(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("drive-lettered paths are only absolute on Windows")
	}
}

// A repo inside the work drive mirrors its position and needs no junction.
func TestDerivePDriveNative(t *testing.T) {
	skipWithoutWindowsPaths(t)
	backing := `C:\Users\me\Documents\DayZ Projects`
	dir := filepath.Join(backing, "projects", "fx-thing")

	s, err := Derive(Spec{Dir: dir}, "P:", backing)
	if err != nil {
		t.Fatal(err)
	}
	if want := `P:\projects\fx-thing`; !paths.Equal(s.PDrivePath, want) {
		t.Errorf("pdrive_path = %q, want %q", s.PDrivePath, want)
	}
	if s.PDriveLinkFrom != "" {
		t.Errorf("a repo already under the work drive needs no junction, got %q", s.PDriveLinkFrom)
	}
	if s.ID != "fx-thing" {
		t.Errorf("id = %q", s.ID)
	}
	if s.Name != "@fx-thing" {
		t.Errorf("name = %q", s.Name)
	}
}

// A repo outside the work drive gets a junction declaration.
func TestDeriveNeedsJunction(t *testing.T) {
	skipWithoutWindowsPaths(t)
	backing := `C:\Users\me\Documents\DayZ Projects`

	s, err := Derive(Spec{Dir: `E:\projects\example-server-mod`}, "P:", backing)
	if err != nil {
		t.Fatal(err)
	}
	if want := `P:\projects\example-server-mod`; !paths.Equal(s.PDrivePath, want) {
		t.Errorf("pdrive_path = %q, want %q", s.PDrivePath, want)
	}
	if !paths.Equal(s.PDriveLinkFrom, `E:\projects\example-server-mod`) {
		t.Errorf("pdrive_link_from = %q, want the real location", s.PDriveLinkFrom)
	}
}

func TestDeriveRespectsExplicitValues(t *testing.T) {
	s, err := Derive(Spec{
		Dir:   `E:\projects\whatever`,
		ID:    "custom-id",
		Name:  "custom-name", // no @, should gain one
		Addon: "MyAddon",
	}, "P:", "")
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != "custom-id" || s.Addon != "MyAddon" {
		t.Errorf("explicit values were overwritten: %+v", s)
	}
	if s.Name != "@custom-name" {
		t.Errorf("name = %q, want the @ added", s.Name)
	}
}

// The generated manifest has to actually be valid, or init hands you a repo
// whose very first command fails.
func TestGeneratedManifestIsValid(t *testing.T) {
	skipWithoutWindowsPaths(t)
	for _, serverOnly := range []bool{false, true} {
		name := "normal"
		if serverOnly {
			name = "server-only"
		}
		t.Run(name, func(t *testing.T) {
			s, err := Derive(Spec{Dir: `E:\projects\example-server-mod`, ServerOnly: serverOnly}, "P:", "")
			if err != nil {
				t.Fatal(err)
			}
			files, err := Plan(s)
			if err != nil {
				t.Fatal(err)
			}

			manifest := fileNamed(t, files, modcfg.FileName)
			cfg, err := modcfg.Parse(manifest)
			if err != nil {
				t.Fatalf("the generated %s does not validate:\n%v\n\n%s", modcfg.FileName, err, manifest)
			}

			if cfg.Mod.Name != s.Name {
				t.Errorf("mod.name = %q, want %q", cfg.Mod.Name, s.Name)
			}
			set := cfg.AddonSets["main"]
			if set == nil || set.Addons[s.Addon] == nil {
				t.Fatalf("addon %q missing from the generated manifest", s.Addon)
			}

			dev, err := cfg.Channel("dev")
			if err != nil {
				t.Fatal(err)
			}
			if serverOnly {
				if got := set.Addons[s.Addon].Policy.Side; got != modcfg.SideServer {
					t.Errorf("side = %q, want %q", got, modcfg.SideServer)
				}
				if len(dev.Deploy) != 1 || dev.Deploy[0] != modcfg.TargetServer {
					t.Errorf("deploy = %v, want only the server", dev.Deploy)
				}
				if cfg.Launch.Mods[0].Side != modcfg.SideServer {
					t.Error("a server-only mod must be declared side: server so it loads through -serverMod=")
				}
			} else {
				if len(dev.Deploy) != 2 {
					t.Errorf("deploy = %v, want both client and server", dev.Deploy)
				}
			}
		})
	}
}

// The prefix baked into config.cpp has to match the one the packer uses, or the
// scripts named there resolve to nothing.
func TestConfigPrefixMatchesTheDerivedPrefix(t *testing.T) {
	s, err := Derive(Spec{Dir: `E:\projects\example-server-mod`}, "P:", "")
	if err != nil {
		t.Fatal(err)
	}
	files, err := Plan(s)
	if err != nil {
		t.Fatal(err)
	}

	config := string(fileNamed(t, files, "mod/"+s.Addon+"/config.cpp"))
	want, err := paths.Prefix(s.PDrivePath, "mod", s.Addon)
	if err != nil {
		t.Fatal(err)
	}
	wantSlash := paths.ToSlash(want)

	for _, module := range []string{"3_Game", "4_World", "5_Mission"} {
		needle := wantSlash + "/Scripts/" + module
		if !strings.Contains(config, needle) {
			t.Errorf("config.cpp does not reference %q", needle)
		}
	}
	if !strings.Contains(config, "class "+s.Addon) {
		t.Errorf("config.cpp has no CfgPatches entry for %q", s.Addon)
	}
}

func TestServerConfigHasInstanceID(t *testing.T) {
	s, _ := Derive(Spec{Dir: `E:\projects\x`}, "P:", "")
	files, err := Plan(s)
	if err != nil {
		t.Fatal(err)
	}
	cfg := string(fileNamed(t, files, "tools/serverdz_local.cfg"))
	// Without this the server terminates during config validation, before
	// mission init, and dayzmod refuses to deploy it.
	if !strings.Contains(cfg, "instanceId = 1;") {
		t.Error("the generated server config must set instanceId")
	}
}

func TestWriteDoesNotClobber(t *testing.T) {
	dir := t.TempDir()
	s, _ := Derive(Spec{Dir: dir}, "P:", "")
	files, err := Plan(s)
	if err != nil {
		t.Fatal(err)
	}

	// Pre-existing manifest whose content has to survive.
	existing := []byte("# hand written, do not clobber\n")
	if err := os.WriteFile(filepath.Join(dir, modcfg.FileName), existing, 0o644); err != nil {
		t.Fatal(err)
	}

	written, skipped, err := Write(s, files, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(skipped, modcfg.FileName) {
		t.Errorf("the existing manifest should have been skipped, skipped=%v", skipped)
	}
	if contains(written, modcfg.FileName) {
		t.Error("the existing manifest was overwritten")
	}

	got, err := os.ReadFile(filepath.Join(dir, modcfg.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(existing) {
		t.Error("the existing manifest's content changed")
	}

	// The script directories named in config.cpp have to exist on disk.
	for _, d := range ScriptDirs {
		full := filepath.Join(dir, "mod", s.Addon, filepath.FromSlash(d))
		if fi, err := os.Stat(full); err != nil || !fi.IsDir() {
			t.Errorf("script directory %s was not created", d)
		}
	}
}

// --sync refreshes the regenerable files and leaves everything else alone.
func TestWriteOnlyOwned(t *testing.T) {
	dir := t.TempDir()
	s, _ := Derive(Spec{Dir: dir}, "P:", "")
	files, err := Plan(s)
	if err != nil {
		t.Fatal(err)
	}

	written, _, err := Write(s, files, true, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range written {
		switch f {
		case ".buildfiles", ".gitattributes", ".gitignore":
		default:
			t.Errorf("--sync wrote %q, which is not a tool-owned file", f)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, modcfg.FileName)); !os.IsNotExist(err) {
		t.Error("--sync must not create the manifest")
	}
}

func TestBuildfilesMatchesTheSchemaDefault(t *testing.T) {
	s, _ := Derive(Spec{Dir: `E:\projects\x`}, "P:", "")
	files, err := Plan(s)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(string(fileNamed(t, files, ".buildfiles")))
	want := strings.Join(modcfg.DefaultIncludeExtensions, ";")
	if got != want {
		t.Errorf(".buildfiles drifted from the schema default:\n got  %s\n want %s", got, want)
	}
}

func fileNamed(t *testing.T, files []File, path string) []byte {
	t.Helper()
	for _, f := range files {
		if f.Path == path {
			return f.Content
		}
	}
	t.Fatalf("no generated file named %q", path)
	return nil
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// The .obf pboProject writes beside a packed source is the deobfuscation map:
// every mangled name paired with the real one. A scaffolded repo has to ignore
// it, because it appears as an ordinary new file in a directory full of them and
// committing it undoes what +O was for.
func TestGitignoreCoversObfuscationLeftovers(t *testing.T) {
	s, _ := Derive(Spec{Dir: `E:\projects\x`}, "P:", "")
	files, err := Plan(s)
	if err != nil {
		t.Fatal(err)
	}

	var body string
	for _, f := range files {
		if f.Path == ".gitignore" {
			body = string(f.Content)
		}
	}
	if body == "" {
		t.Fatal("no .gitignore in the scaffold plan")
	}

	for _, pattern := range []string{
		"*.obf",           // the deobfuscation map
		"*.biprivatekey",  // signing keys
		"$PBOPREFIX$.txt", // what the packer actually writes on 4.31
		"$PBOPREFIX$",     // and the spelling an older repo may still carry
		"noscramble.lst",
	} {
		if !strings.Contains(body, pattern) {
			t.Errorf(".gitignore does not cover %q:\n%s", pattern, body)
		}
	}
}
