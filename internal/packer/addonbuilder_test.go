package packer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const abExe = `C:\DayZ Tools\Bin\AddonBuilder\AddonBuilder.exe`

// These argv are what the previous Taskfile passed to AddonBuilder, checked
// against a real build log. Change one and you change the path space inside
// every PBO, which makes a mod silently load nothing.
func TestAddonBuilderArgvMatchesTheOldPipeline(t *testing.T) {
	cases := []struct {
		name string
		job  Job
		want []string
	}{
		{
			name: "project-with-everything logic addon",
			job: Job{
				AddonName:   "PWE_Core",
				SourceDir:   `P:\projects\project-with-everything\mod\PWE_Core`,
				OutDir:      `P:\_tmp\mod-build\@project-with-everything\PWE_Core`,
				Prefix:      `projects\project-with-everything\mod\PWE_Core`,
				IncludeFile: `P:\projects\project-with-everything\.buildfiles`,
				Binarize:    true,
			},
			want: []string{
				abExe,
				`-prefix=projects\project-with-everything\mod\PWE_Core`,
				`-include=P:\projects\project-with-everything\.buildfiles`,
				`P:\projects\project-with-everything\mod\PWE_Core`,
				`P:\_tmp\mod-build\@project-with-everything\PWE_Core`,
			},
		},
		{
			// The dev-only set has its own mod folder and its own prefix root.
			name: "dev addon set",
			job: Job{
				AddonName:   "PWE_DevSpawn",
				SourceDir:   `P:\projects\project-with-everything\mod-dev\PWE_DevSpawn`,
				OutDir:      `P:\_tmp\mod-build\@project-with-everything-dev\PWE_DevSpawn`,
				Prefix:      `projects\project-with-everything\mod-dev\PWE_DevSpawn`,
				IncludeFile: `P:\projects\project-with-everything\.buildfiles`,
				Binarize:    true,
			},
			want: []string{
				abExe,
				`-prefix=projects\project-with-everything\mod-dev\PWE_DevSpawn`,
				`-include=P:\projects\project-with-everything\.buildfiles`,
				`P:\projects\project-with-everything\mod-dev\PWE_DevSpawn`,
				`P:\_tmp\mod-build\@project-with-everything-dev\PWE_DevSpawn`,
			},
		},
		{
			name: "no include file",
			job: Job{
				AddonName: "PMC_Main",
				SourceDir: `P:\projects\minimal-config-repo\mod\PMC_Main`,
				OutDir:    `P:\_tmp\mod-build\@project-with-minimal-config\PMC_Main`,
				Prefix:    `projects\minimal-config-repo\mod\PMC_Main`,
				Binarize:  true,
			},
			want: []string{
				abExe,
				`-prefix=projects\minimal-config-repo\mod\PMC_Main`,
				`P:\projects\minimal-config-repo\mod\PMC_Main`,
				`P:\_tmp\mod-build\@project-with-minimal-config\PMC_Main`,
			},
		},
		{
			// Binarising is the default. Turning it off also stops model.cfg
			// being applied, so it has to be an explicit opt-out.
			name: "binarize off packs only",
			job: Job{
				AddonName: "Raw",
				SourceDir: `P:\projects\x\mod\Raw`,
				OutDir:    `P:\out\Raw`,
				Prefix:    `projects\x\mod\Raw`,
				Binarize:  false,
			},
			want: []string{
				abExe,
				`-prefix=projects\x\mod\Raw`,
				"-packonly",
				`P:\projects\x\mod\Raw`,
				`P:\out\Raw`,
			},
		},
	}

	ab := &AddonBuilder{Exe: abExe}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ab.Argv(tc.job)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("argv length %d, want %d\n  got  %q\n  want %q", len(got), len(tc.want), got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("argv[%d]\n  got  %q\n  want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// AddonBuilder has no obfuscation support, so asking for it should fail loudly
// instead of quietly handing back a plain PBO everyone assumes is protected.
func TestAddonBuilderRefusesObfuscation(t *testing.T) {
	ab := &AddonBuilder{Exe: abExe}
	_, err := ab.Argv(Job{
		AddonName: "X", SourceDir: `P:\a`, OutDir: `P:\b`, Prefix: `a`, Obfuscate: true,
	})
	if err == nil {
		t.Fatal("expected an error when obfuscation is requested")
	}
	if !strings.Contains(err.Error(), "pboproject") {
		t.Errorf("the error should point at the packer that can obfuscate, got: %v", err)
	}
}

func TestAddonBuilderArgvRejectsIncompleteJobs(t *testing.T) {
	ab := &AddonBuilder{Exe: abExe}
	for name, j := range map[string]Job{
		"no source": {AddonName: "X", OutDir: `P:\b`, Prefix: "a"},
		"no outdir": {AddonName: "X", SourceDir: `P:\a`, Prefix: "a"},
		"no prefix": {AddonName: "X", SourceDir: `P:\a`, OutDir: `P:\b`},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ab.Argv(j); err == nil {
				t.Error("expected an error")
			}
		})
	}

	noExe := &AddonBuilder{}
	if _, err := noExe.Argv(Job{AddonName: "X", SourceDir: `P:\a`, OutDir: `P:\b`, Prefix: "a"}); err == nil {
		t.Error("expected an error when AddonBuilder is not configured")
	}
}

// The sync directory is the addon name lowercased. It is the cache AddonBuilder
// never cleans, so getting the name wrong makes the wipe silently do nothing and
// deleted files keep shipping.
func TestAddonBuilderSyncDirIsLowercased(t *testing.T) {
	ab := &AddonBuilder{Exe: abExe, TempDir: `C:\Temp`}
	got := ab.SyncDir(Job{AddonName: "PWE_Core"})
	if want := filepath.Join(`C:\Temp`, "pwe_core"); got != want {
		t.Errorf("SyncDir = %q, want %q", got, want)
	}
}

// Preflight has to remove the stale sync cache. Otherwise a file deleted from
// source lingers there and keeps getting packed, turning up much later as an
// "Undefined function" for code that no longer exists.
func TestPreflightClearsTheSyncCache(t *testing.T) {
	tmp := t.TempDir()
	outDir := filepath.Join(t.TempDir(), "out")

	ab := &AddonBuilder{Exe: abExe, TempDir: tmp}
	job := Job{AddonName: "PWE_Core", SourceDir: `P:\a`, OutDir: outDir, Prefix: "a"}

	// A stale sync dir with a file that no longer exists in source, a stale
	// intermediate PBO, and a stale output PBO.
	sync := ab.SyncDir(job)
	if err := os.MkdirAll(sync, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(sync, "deleted_from_source.c")
	if err := os.WriteFile(stale, []byte("void Gone() {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldPbo := ab.PboPath(job)
	if err := os.WriteFile(oldPbo, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := ab.Preflight(t.Context(), job); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(sync); !os.IsNotExist(err) {
		t.Error("the sync cache survived preflight; deleted sources would keep shipping")
	}
	if _, err := os.Stat(oldPbo); !os.IsNotExist(err) {
		t.Error("the stale output PBO survived preflight; a failed build could deploy it as if fresh")
	}
	if _, err := os.Stat(outDir); err != nil {
		t.Error("preflight should leave the output directory in place")
	}
}

func TestPboPath(t *testing.T) {
	ab := &AddonBuilder{Exe: abExe}
	job := Job{AddonName: "PWE_Core", OutDir: `P:\out`}
	if got, want := ab.PboPath(job), filepath.Join(`P:\out`, "PWE_Core.pbo"); got != want {
		t.Errorf("PboPath = %q, want %q", got, want)
	}

	job.Label = "Renamed"
	if got, want := ab.PboPath(job), filepath.Join(`P:\out`, "Renamed.pbo"); got != want {
		t.Errorf("PboPath with label = %q, want %q", got, want)
	}
}

func TestAddonBuilderCaps(t *testing.T) {
	c := (&AddonBuilder{}).Caps()
	if c.CanObfuscate {
		t.Error("AddonBuilder cannot obfuscate")
	}
	if !c.CanBinarize {
		t.Error("AddonBuilder binarises")
	}
	if c.SelfCleansTemp {
		t.Error("AddonBuilder does not clean its sync cache; the caller must")
	}
}
