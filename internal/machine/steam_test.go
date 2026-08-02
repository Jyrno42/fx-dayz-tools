package machine

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseVDF(t *testing.T) {
	src := []byte(`
// a comment
"libraryfolders"
{
	"0"
	{
		"path"		"C:\\Program Files (x86)\\Steam"
		"label"		""
		"apps"
		{
			"221100"		"12345678"
		}
	}
	"1"
	{
		"path"		"D:\\SteamLibrary"
	}
}
`)
	root, err := parseVDF(src)
	if err != nil {
		t.Fatal(err)
	}

	folders := root.child("libraryfolders")
	if folders == nil {
		t.Fatal("libraryfolders section missing")
	}
	if got, want := folders.str("0", "path"), `C:\Program Files (x86)\Steam`; got != want {
		t.Errorf("path = %q, want %q -- doubled backslashes must be unescaped", got, want)
	}
	if got, want := folders.str("1", "path"), `D:\SteamLibrary`; got != want {
		t.Errorf("second library = %q, want %q", got, want)
	}
	if got := folders.str("0", "apps", "221100"); got != "12345678" {
		t.Errorf("app entry = %q", got)
	}
	// Declaration order matters, so the first matching library should win.
	if got := folders.keys(); len(got) != 2 || got[0] != "0" || got[1] != "1" {
		t.Errorf("keys = %v, want [0 1] in declaration order", got)
	}
}

func TestParseVDFIsCaseInsensitive(t *testing.T) {
	root, err := parseVDF([]byte(`"AppState" { "InstallDir" "DayZ" }`))
	if err != nil {
		t.Fatal(err)
	}
	// Steam has varied the casing of these keys across versions.
	if got := root.str("appstate", "installdir"); got != "DayZ" {
		t.Errorf("lookup should ignore case, got %q", got)
	}
	if got := root.str("AppState", "InstallDir"); got != "DayZ" {
		t.Errorf("lookup = %q", got)
	}
}

func TestParseVDFMissingKeysAreEmpty(t *testing.T) {
	root, err := parseVDF([]byte(`"a" { "b" "c" }`))
	if err != nil {
		t.Fatal(err)
	}
	if got := root.str("a", "nope", "deeper"); got != "" {
		t.Errorf("missing key should read as empty, got %q", got)
	}
}

func TestParseVDFRejectsMalformed(t *testing.T) {
	for name, src := range map[string]string{
		"unterminated string": `"key" "value`,
		"unclosed section":    `"key" { "a" "b"`,
		"stray brace":         `}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseVDF([]byte(src)); err == nil {
				t.Error("expected a parse error")
			}
		})
	}
}

// steamTree builds a fake Steam layout, i.e. a root library plus optional extra
// libraries, each with the given apps installed.
func steamTree(t *testing.T, libs map[string]map[string]string) string {
	t.Helper()
	base := t.TempDir()

	var root string
	vdf := "\"libraryfolders\"\n{\n"
	i := 0
	for lib, apps := range libs {
		dir := filepath.Join(base, lib)
		if root == "" {
			root = dir
		}
		for appID, installDir := range apps {
			common := filepath.Join(dir, "steamapps", "common", installDir)
			if err := os.MkdirAll(common, 0o755); err != nil {
				t.Fatal(err)
			}
			manifest := filepath.Join(dir, "steamapps", "appmanifest_"+appID+".acf")
			body := "\"AppState\"\n{\n\t\"appid\"\t\"" + appID + "\"\n\t\"installdir\"\t\"" + installDir + "\"\n}\n"
			if err := os.WriteFile(manifest, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.MkdirAll(filepath.Join(dir, "steamapps"), 0o755); err != nil {
			t.Fatal(err)
		}
		vdf += "\t\"" + itoa(i) + "\"\n\t{\n\t\t\"path\"\t\"" + escape(dir) + "\"\n\t}\n"
		i++
	}
	vdf += "}\n"

	if err := os.WriteFile(filepath.Join(root, "steamapps", "libraryfolders.vdf"), []byte(vdf), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func itoa(i int) string { return string(rune('0' + i)) }

func escape(p string) string {
	out := ""
	for _, r := range p {
		if r == '\\' {
			out += `\\`
			continue
		}
		out += string(r)
	}
	return out
}

func TestFindSteamAppsInDefaultLibrary(t *testing.T) {
	root := steamTree(t, map[string]map[string]string{
		"Steam": {AppDayZ: "DayZ", AppDayZServer: "DayZServer", AppDayZTools: "DayZ Tools"},
	})

	apps, err := FindSteamApps(root, AppDayZ, AppDayZServer, AppDayZTools)
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 3 {
		t.Fatalf("found %d apps, want 3", len(apps))
	}
	if got := filepath.Base(apps[AppDayZ].Dir); got != "DayZ" {
		t.Errorf("DayZ dir = %q", got)
	}
}

// Games on a second drive, which is the whole reason for reading the vdf.
func TestFindSteamAppsAcrossLibraries(t *testing.T) {
	root := steamTree(t, map[string]map[string]string{
		"Steam":        {AppDayZTools: "DayZ Tools"},
		"OtherLibrary": {AppDayZ: "DayZ", AppDayZServer: "DayZServer"},
	})

	apps, err := FindSteamApps(root, AppDayZ, AppDayZServer, AppDayZTools)
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 3 {
		t.Fatalf("found %d apps, want 3 -- an app in a secondary library was missed", len(apps))
	}
	if apps[AppDayZ].Library == apps[AppDayZTools].Library {
		t.Error("DayZ and DayZ Tools should have been found in different libraries")
	}
}

func TestFindSteamAppsIgnoresUninstalledApps(t *testing.T) {
	root := steamTree(t, map[string]map[string]string{"Steam": {AppDayZ: "DayZ"}})

	apps, err := FindSteamApps(root, AppDayZ, AppDayZServer)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := apps[AppDayZServer]; ok {
		t.Error("an app with no manifest must not be reported as installed")
	}
}

// A manifest can outlive the files it describes, e.g. after a partial removal.
func TestFindSteamAppsIgnoresManifestWithoutFiles(t *testing.T) {
	root := steamTree(t, map[string]map[string]string{"Steam": {AppDayZ: "DayZ"}})
	if err := os.RemoveAll(filepath.Join(root, "steamapps", "common", "DayZ")); err != nil {
		t.Fatal(err)
	}

	apps, err := FindSteamApps(root, AppDayZ)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := apps[AppDayZ]; ok {
		t.Error("a manifest with no install directory must not count as installed")
	}
}

// A default install with no libraryfolders.vdf still has to work.
func TestSteamLibrariesWithoutVDF(t *testing.T) {
	root := t.TempDir()
	libs, err := SteamLibraries(root)
	if err != nil {
		t.Fatalf("a missing libraryfolders.vdf should degrade gracefully, got %v", err)
	}
	if len(libs) != 1 || libs[0] != root {
		t.Errorf("libraries = %v, want just the Steam root", libs)
	}
}
