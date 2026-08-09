package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jyrno42/fx-dayz-tools/internal/packer"
	"github.com/Jyrno42/fx-dayz-tools/internal/proc"
)

// stubPacker stands in for a packer that reports whatever we tell it to.
type stubPacker struct {
	packer.AddonBuilder
	files    []string
	known    bool
	everyOne bool
}

func (s *stubPacker) Name() string { return "stub" }
func (s *stubPacker) Caps() packer.Caps {
	return packer.Caps{ReportsEveryFile: s.everyOne}
}
func (s *stubPacker) PackedFiles(proc.Result, packer.Job) ([]string, bool, error) {
	return s.files, s.known, nil
}

// The Workshop bug: AddonBuilder given no -include syncs nothing, exits zero and
// writes a PBO holding only the converted config. Every downstream step reported
// success. Comparing what the packer says it took against the source is what
// turns that into a failed build.
func TestVerifyCatchesAPackThatTookNothing(t *testing.T) {
	cfg, host := testRepo(t)
	b := &Builder{Mod: cfg, Host: host, Runner: &fakeRunner{}, Report: silent{}}

	src := filepath.Join(cfg.Root, "mod", "TestAddon")
	for _, name := range []string{"Scripts/Thing.c", "data/model.p3d"} {
		full := filepath.Join(src, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	p := &stubPacker{known: true, everyOne: true} // reported no files at all
	err := b.verifyPacked(p, proc.Result{}, packer.Job{AddonName: "TestAddon"}, src)
	if err == nil {
		t.Fatal("a pack that took no files should fail verification")
	}
	for _, want := range []string{"Thing.c", "model.p3d"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error should name the missing files, got: %v", err)
		}
	}
}

// A packer that took everything passes, so the check does not cry wolf on an
// ordinary build.
func TestVerifyPassesWhenEverythingWasPacked(t *testing.T) {
	cfg, host := testRepo(t)
	b := &Builder{Mod: cfg, Host: host, Runner: &fakeRunner{}, Report: silent{}}

	src := filepath.Join(cfg.Root, "mod", "TestAddon")
	full := filepath.Join(src, "Scripts", "Thing.c")
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Reported with the separator and casing a tool log would use.
	p := &stubPacker{files: []string{`scripts\thing.c`}, known: true, everyOne: true}
	if err := b.verifyPacked(p, proc.Result{}, packer.Job{AddonName: "TestAddon"}, src); err != nil {
		t.Errorf("verification should pass when every file was reported: %v", err)
	}
}

// pboProject logs some phases without naming their files, so its list is a floor
// rather than the whole set. A partial report must not fail the build.
func TestVerifyTreatsAPartialReporterAsAFloor(t *testing.T) {
	cfg, host := testRepo(t)
	b := &Builder{Mod: cfg, Host: host, Runner: &fakeRunner{}, Report: silent{}}

	src := filepath.Join(cfg.Root, "mod", "TestAddon")
	for _, name := range []string{"a.c", "b.c", "c.c"} {
		if err := os.WriteFile(filepath.Join(src, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	partial := &stubPacker{files: []string{"a.c"}, known: true, everyOne: false}
	if err := b.verifyPacked(partial, proc.Result{}, packer.Job{AddonName: "TestAddon"}, src); err != nil {
		t.Errorf("a partial reporter should not fail on missing names: %v", err)
	}

	// Nothing at all is still a failure, which is the case that matters.
	none := &stubPacker{known: true, everyOne: false}
	if err := b.verifyPacked(none, proc.Result{}, packer.Job{AddonName: "TestAddon"}, src); err == nil {
		t.Error("a packer that reported nothing should still fail")
	}
}

// A packer that cannot report is not evidence of a bad pack.
func TestVerifySkipsAPackerThatCannotReport(t *testing.T) {
	cfg, host := testRepo(t)
	b := &Builder{Mod: cfg, Host: host, Runner: &fakeRunner{}, Report: silent{}}
	src := filepath.Join(cfg.Root, "mod", "TestAddon")

	p := &stubPacker{known: false}
	if err := b.verifyPacked(p, proc.Result{}, packer.Job{AddonName: "TestAddon"}, src); err != nil {
		t.Errorf("silence from the packer should not fail the build: %v", err)
	}
}
