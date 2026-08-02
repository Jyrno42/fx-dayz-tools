package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jyrno42/fx-dayz-tools/internal/modcfg"
	"github.com/Jyrno42/fx-dayz-tools/internal/proc"
)

// vendorDir builds a directory of prebuilt PBOs, one signed and one not. A real
// pack mixes the two, so an unsigned entry should not be an error.
func vendorDir(t *testing.T) (dir, keys string) {
	t.Helper()
	root := t.TempDir()
	dir = filepath.Join(root, "prebuilt")
	keys = filepath.Join(dir, "keys")
	if err := os.MkdirAll(keys, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Signed.pbo", "a")
	write("Signed.pbo.Vendor.bisign", "sig")
	write("Unsigned.pbo", "b")
	write("notes.txt", "ignored")
	if err := os.WriteFile(filepath.Join(keys, "Vendor.bikey"), []byte("k"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, keys
}

func builderFor(t *testing.T, root string, includes []modcfg.IncludeSpec) *Builder {
	t.Helper()
	cfg, host := testRepo(t)
	cfg.Root = root
	cfg.Include = includes
	return &Builder{Mod: cfg, Host: host, Runner: &fakeRunner{}, Report: silent{}}
}

func TestStageIncludesCopiesPBOsAndSignatures(t *testing.T) {
	dir, _ := vendorDir(t)
	root := filepath.Dir(dir)
	stage := filepath.Join(t.TempDir(), "@pack")
	addons := filepath.Join(stage, "Addons")

	b := builderFor(t, root, nil)
	staged, err := b.stageIncludes([]modcfg.IncludeSpec{{From: "prebuilt"}}, addons, stage, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(staged) != 2 {
		t.Fatalf("staged %d, want 2 (the .txt must be ignored)", len(staged))
	}

	for _, name := range []string{"Signed.pbo", "Signed.pbo.Vendor.bisign", "Unsigned.pbo"} {
		if _, err := os.Stat(filepath.Join(addons, name)); err != nil {
			t.Errorf("%s was not staged: %v", name, err)
		}
	}
	// The original signature comes across as-is. Nothing gets re-signed here.
	if _, err := os.Stat(filepath.Join(addons, "notes.txt")); err == nil {
		t.Error("a non-PBO file was copied into the pack")
	}
	for _, a := range staged {
		if !a.Included {
			t.Errorf("%s should be marked as included, not packed", a.Name)
		}
	}
}

// A server pack is usually built for one operator who already trusts the keys,
// so ship_keys off has to suppress the vendored keys as well.
func TestStageIncludesHonoursShipKeys(t *testing.T) {
	dir, _ := vendorDir(t)
	root := filepath.Dir(dir)
	spec := []modcfg.IncludeSpec{{From: "prebuilt", Keys: "prebuilt/keys"}}

	stage := filepath.Join(t.TempDir(), "@pack")
	b := builderFor(t, root, nil)
	if _, err := b.stageIncludes(spec, filepath.Join(stage, "Addons"), stage, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(stage, "keys")); !os.IsNotExist(err) {
		t.Error("ship_keys off must not produce a keys folder")
	}

	stage2 := filepath.Join(t.TempDir(), "@pack")
	b2 := builderFor(t, root, nil)
	if _, err := b2.stageIncludes(spec, filepath.Join(stage2, "Addons"), stage2, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(stage2, "keys", "Vendor.bikey")); err != nil {
		t.Errorf("ship_keys on should ship the vendored key: %v", err)
	}
}

func TestStageIncludesSideAndOptional(t *testing.T) {
	dir, _ := vendorDir(t)
	root := filepath.Dir(dir)
	stage := filepath.Join(t.TempDir(), "@pack")

	b := builderFor(t, root, nil)
	staged, err := b.stageIncludes([]modcfg.IncludeSpec{
		{From: "prebuilt", Side: modcfg.SideServer},
		{From: "does-not-exist", Optional: true},
	}, filepath.Join(stage, "Addons"), stage, false)
	if err != nil {
		t.Fatalf("an optional missing directory should be skipped, got %v", err)
	}
	for _, a := range staged {
		if a.Side != modcfg.SideServer {
			t.Errorf("%s side = %q, want server", a.Name, a.Side)
		}
	}

	// Not optional, so a missing directory is a real error.
	b2 := builderFor(t, root, nil)
	_, err = b2.stageIncludes([]modcfg.IncludeSpec{{From: "does-not-exist"}},
		filepath.Join(stage, "Addons"), stage, false)
	if err == nil {
		t.Error("a missing non-optional include directory should fail")
	}
}

func TestStageIncludesRejectsEmptyFrom(t *testing.T) {
	b := builderFor(t, t.TempDir(), nil)
	_, err := b.stageIncludes([]modcfg.IncludeSpec{{}}, "addons", "stage", false)
	if err == nil || !strings.Contains(err.Error(), "from") {
		t.Errorf("expected an error naming `from`, got %v", err)
	}
}

func TestStageIncludesDryRunCopiesNothing(t *testing.T) {
	dir, _ := vendorDir(t)
	root := filepath.Dir(dir)
	stage := filepath.Join(t.TempDir(), "@pack")
	addons := filepath.Join(stage, "Addons")

	cfg, host := testRepo(t)
	cfg.Root = root
	b := &Builder{Mod: cfg, Host: host, Runner: &proc.Dry{}, Report: silent{}}

	staged, err := b.stageIncludes([]modcfg.IncludeSpec{{From: "prebuilt"}}, addons, stage, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(staged) != 2 {
		t.Errorf("a dry run should still report what it would stage, got %d", len(staged))
	}
	if _, err := os.Stat(addons); !os.IsNotExist(err) {
		t.Error("a dry run must not copy anything")
	}
}
