package paths

import "testing"

func TestPrefixMatchesExistingTaskfiles(t *testing.T) {
	// These are the exact strings the current Taskfiles pass to AddonBuilder via
	// -prefix=, and the ones pboProject derives on its own from the source
	// folder's position under P:. If this test ever changes, every PBO's internal
	// path space changes with it and mods silently stop loading.
	cases := []struct {
		name       string
		pdrivePath string
		parts      []string
		want       string
	}{
		{
			name:       "project-with-everything logic addon",
			pdrivePath: `P:\projects\project-with-everything`,
			parts:      []string{"mod", "PWE_Core"},
			want:       `projects\project-with-everything\mod\PWE_Core`,
		},
		{
			name:       "project-with-everything dev addon set uses its own source dir",
			pdrivePath: `P:\projects\project-with-everything`,
			parts:      []string{"mod-dev", "PWE_DevSpawn"},
			want:       `projects\project-with-everything\mod-dev\PWE_DevSpawn`,
		},
		{
			// The repo folder name is misspelled and the typo is baked into every
			// shipped script path. Renaming the folder is a breaking change.
			name:       "minimal-config-repo keeps the folder typo",
			pdrivePath: `P:\projects\minimal-config-repo`,
			parts:      []string{"mod", "PMC_Main"},
			want:       `projects\minimal-config-repo\mod\PMC_Main`,
		},
		{
			name:       "project_with_compat_presets",
			pdrivePath: `P:\projects\project-with-compat-presets`,
			parts:      []string{"mod", "ProjectWithCompatPresets"},
			want:       `projects\project-with-compat-presets\mod\ProjectWithCompatPresets`,
		},
		{
			name:       "forward slashes in config are accepted",
			pdrivePath: "P:/projects/project-with-everything",
			parts:      []string{"mod", "PWE_Core"},
			want:       `projects\project-with-everything\mod\PWE_Core`,
		},
		{
			name:       "trailing separator is ignored",
			pdrivePath: `P:\projects\project-with-everything\`,
			parts:      []string{"mod", "PWE_Core"},
			want:       `projects\project-with-everything\mod\PWE_Core`,
		},
		{
			name:       "lowercase drive letter",
			pdrivePath: `p:\projects\project-with-everything`,
			parts:      []string{"mod", "PWE_Core"},
			want:       `projects\project-with-everything\mod\PWE_Core`,
		},
		{
			name:       "nested source dir",
			pdrivePath: `P:\projects\some-mod`,
			parts:      []string{"src/addons", "MyAddon"},
			want:       `projects\some-mod\src\addons\MyAddon`,
		},
		{
			name:       "no parts yields the prefix root",
			pdrivePath: `P:\projects\project-with-everything`,
			want:       `projects\project-with-everything`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Prefix(tc.pdrivePath, tc.parts...)
			if err != nil {
				t.Fatalf("Prefix(%q, %v): %v", tc.pdrivePath, tc.parts, err)
			}
			if got != tc.want {
				t.Errorf("Prefix(%q, %v)\n  got  %q\n  want %q", tc.pdrivePath, tc.parts, got, tc.want)
			}
		})
	}
}

func TestPrefixRejectsUnusablePaths(t *testing.T) {
	for _, p := range []string{
		`P:\`,                    // drive root, nothing to derive a prefix from
		`P:`,                     // same
		``,                       // empty
		`\\server\share\project`, // UNC, and DayZ tools want a drive letter
	} {
		if got, err := PrefixRoot(p); err == nil {
			t.Errorf("PrefixRoot(%q) = %q, want an error", p, got)
		}
	}
}

func TestSplitDrive(t *testing.T) {
	cases := []struct{ in, drive, rest string }{
		{`P:\projects\x`, "P:", `projects\x`},
		{"P:/projects/x", "P:", `projects\x`},
		{`p:\projects\x\`, "P:", `projects\x`},
		{`P:\`, "P:", ""},
		{`projects\x`, "", `projects\x`},
	}
	for _, tc := range cases {
		drive, rest := SplitDrive(tc.in)
		if drive != tc.drive || rest != tc.rest {
			t.Errorf("SplitDrive(%q) = %q, %q; want %q, %q", tc.in, drive, rest, tc.drive, tc.rest)
		}
	}
}

func TestEqualIgnoresSeparatorStyleAndCase(t *testing.T) {
	equal := [][2]string{
		{`P:\projects\x`, "P:/projects/x"},
		{`P:\projects\x`, `p:\PROJECTS\X`},
		{`P:\projects\x\`, `P:\projects\x`},
		{`P:\projects\x`, `  P:\projects\x  `},
	}
	for _, tc := range equal {
		if !Equal(tc[0], tc[1]) {
			t.Errorf("Equal(%q, %q) = false, want true", tc[0], tc[1])
		}
	}

	if Equal(`P:\projects\x`, `P:\projects\y`) {
		t.Error("Equal reported two different paths as the same")
	}
	// A prefix should not count as equal.
	if Equal(`P:\projects\x`, `P:\projects\xy`) {
		t.Error("Equal treated a name prefix as the same path")
	}
}

func TestUnder(t *testing.T) {
	cases := []struct {
		root, path string
		want       bool
	}{
		{`P:\projects`, `P:\projects\project-with-everything`, true},
		{`P:\projects`, `P:\projects`, true},
		{`P:\projects`, "P:/projects/project-with-everything/mod", true},
		{`P:\projects`, `P:\projectsx`, false}, // sibling with a shared name prefix
		{`P:\projects`, `E:\projects\x`, false},
		{`P:\`, `P:\projects`, true},
	}
	for _, tc := range cases {
		if got := Under(tc.root, tc.path); got != tc.want {
			t.Errorf("Under(%q, %q) = %v, want %v", tc.root, tc.path, got, tc.want)
		}
	}
}

func TestClassifyDrive(t *testing.T) {
	subst := classify("P:", `\??\C:\Users\me\Documents\DayZ Projects`)
	if subst.Kind != DriveSubst {
		t.Errorf("kind = %q, want %q", subst.Kind, DriveSubst)
	}
	if want := `C:\Users\me\Documents\DayZ Projects`; subst.Backing != want {
		t.Errorf("backing = %q, want %q", subst.Backing, want)
	}
	if !subst.Mounted() {
		t.Error("a subst drive should report as mounted")
	}

	vol := classify("C:", `\Device\HarddiskVolume4`)
	if vol.Kind != DriveVolume {
		t.Errorf("kind = %q, want %q", vol.Kind, DriveVolume)
	}
	if vol.Backing != "" {
		t.Errorf("a real volume has no backing dir, got %q", vol.Backing)
	}

	absent := classify("Q:", "")
	if absent.Kind != DriveAbsent {
		t.Errorf("kind = %q, want %q", absent.Kind, DriveAbsent)
	}
	if absent.Mounted() {
		t.Error("an undefined drive letter must not report as mounted")
	}
}

func TestNormaliseLetter(t *testing.T) {
	for _, in := range []string{"P", "p", "P:", "p:", `P:\`, "P:/"} {
		got, err := normaliseLetter(in)
		if err != nil {
			t.Errorf("normaliseLetter(%q): %v", in, err)
			continue
		}
		if got != "P:" {
			t.Errorf("normaliseLetter(%q) = %q, want P:", in, got)
		}
	}
	for _, in := range []string{"", "PP", "1", `\\server`, "P:\\x"} {
		if got, err := normaliseLetter(in); err == nil {
			t.Errorf("normaliseLetter(%q) = %q, want an error", in, got)
		}
	}
}

// A subst mapping lives in a per-logon-session device map, so a path on it can
// be genuinely unreachable from one process while another shell on the same
// machine sees it fine. Rebasing onto the backing directory is how a check tells
// "the file is gone" apart from "this process cannot see the drive".
func TestRebase(t *testing.T) {
	const backing = `C:\Users\me\Documents\DayZ Projects`

	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{`P:\keys\signing-key.biprivatekey`, backing + `\keys\signing-key.biprivatekey`, true},
		{`P:\projects\project-with-everything`, backing + `\projects\project-with-everything`, true},
		{"P:/projects/x", backing + `\projects\x`, true},
		{`p:\projects\x`, backing + `\projects\x`, true},
		{`P:\`, backing, true},
		// Not on the work drive, so leave it alone.
		{`C:\Program Files\x`, `C:\Program Files\x`, false},
		{`E:\projects\x`, `E:\projects\x`, false},
	}
	for _, tc := range cases {
		got, ok := Rebase(tc.in, "P:", backing)
		if got != tc.want || ok != tc.ok {
			t.Errorf("Rebase(%q) = %q, %v; want %q, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}

	// With no backing recorded there is nothing to rebase onto.
	if got, ok := Rebase(`P:\x`, "P:", ""); ok || got != `P:\x` {
		t.Errorf("Rebase with no backing = %q, %v; want the input unchanged", got, ok)
	}
}

func TestVisible(t *testing.T) {
	// A letter that is almost certainly undefined should not report as visible,
	// and should not panic on a malformed argument either.
	if Visible("Q:") && Visible("Z:") && Visible("Y:") {
		t.Skip("this machine has unusually many drives; nothing to assert")
	}
	if Visible("not-a-drive") {
		t.Error("a malformed drive letter is not visible")
	}
}
