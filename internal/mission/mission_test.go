package mission

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture builds mpmissions/empty.world with a nested file and a live
// persistence folder, plus a repo init.c, and returns the mpmissions dir.
func fixture(t *testing.T) (missions, initC string) {
	t.Helper()
	root := t.TempDir()
	missions = filepath.Join(root, "mpmissions")
	src := filepath.Join(missions, "empty.world")
	write(t, filepath.Join(src, "init.c"), "// stock init.c")
	write(t, filepath.Join(src, "db", "types.xml"), "<types/>")
	write(t, filepath.Join(src, "storage_1", "data", "players.db"), "live world")
	write(t, filepath.Join(src, "Storage_2", "x"), "another instance")
	initC = filepath.Join(root, "repo", "tools", "mission", "init.c")
	write(t, initC, "// repo init.c")
	return missions, initC
}

func write(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("reading %s: %v", p, err)
	}
	return string(b)
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// The source's storage_1 belongs to whatever last booted it. The clone must
// not carry it.
func TestCloneSkipsStorageAndInstallsInitC(t *testing.T) {
	missions, initC := fixture(t)
	dst := filepath.Join(missions, "test.world")

	var log []string
	err := Prepare(Options{
		MissionsDir: missions, Name: "test.world", CloneFrom: "empty.world",
		InitC: initC, InstanceID: 1,
		Log: func(f string, a ...any) { log = append(log, f) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dst, "db", "types.xml")); got != "<types/>" {
		t.Errorf("nested file not cloned: %q", got)
	}
	if got := read(t, filepath.Join(dst, "init.c")); got != "// repo init.c" {
		t.Errorf("init.c = %q, want the repo's copy on top of the clone", got)
	}
	if exists(filepath.Join(dst, "storage_1")) || exists(filepath.Join(dst, "Storage_2")) {
		t.Error("persistence folders must not be cloned")
	}
	if len(log) != 2 {
		t.Errorf("expected a clone line and an init.c line, got %q", log)
	}
}

// Re-runs only refresh init.c. Hand edits to the clone must survive.
func TestSecondRunLeavesTheCloneAlone(t *testing.T) {
	missions, initC := fixture(t)
	o := Options{MissionsDir: missions, Name: "test.world", CloneFrom: "empty.world", InitC: initC, InstanceID: 1}
	if err := Prepare(o); err != nil {
		t.Fatal(err)
	}
	edited := filepath.Join(missions, "test.world", "db", "types.xml")
	write(t, edited, "<types>edited</types>")
	write(t, initC, "// newer init.c")

	if err := Prepare(o); err != nil {
		t.Fatal(err)
	}
	if got := read(t, edited); got != "<types>edited</types>" {
		t.Errorf("hand edit lost on re-run: %q", got)
	}
	if got := read(t, filepath.Join(missions, "test.world", "init.c")); got != "// newer init.c" {
		t.Errorf("init.c not refreshed: %q", got)
	}
}

func TestMissingMissionWithoutCloneFromIsAnError(t *testing.T) {
	missions, initC := fixture(t)
	err := Prepare(Options{MissionsDir: missions, Name: "test.world", InitC: initC, InstanceID: 1})
	if err == nil || !strings.Contains(err.Error(), "clone_from") {
		t.Fatalf("want an error naming clone_from, got %v", err)
	}
}

func TestMissingCloneSourceIsAnError(t *testing.T) {
	missions, _ := fixture(t)
	err := Prepare(Options{MissionsDir: missions, Name: "test.world", CloneFrom: "nope.world", InstanceID: 1})
	if err == nil || !strings.Contains(err.Error(), "nope.world") {
		t.Fatalf("want an error naming the source, got %v", err)
	}
}

// The persistence folder is storage_<instanceId>, not always storage_1.
func TestCleanRemovesTheInstanceStorageOnly(t *testing.T) {
	missions, _ := fixture(t)
	dst := filepath.Join(missions, "test.world")
	write(t, filepath.Join(dst, "storage_1", "data", "players.db"), "one")
	write(t, filepath.Join(dst, "storage_2", "data", "players.db"), "two")

	if err := Prepare(Options{MissionsDir: missions, Name: "test.world", InstanceID: 2, Clean: true}); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(dst, "storage_2")) {
		t.Error("storage_2 should have been cleared")
	}
	if !exists(filepath.Join(dst, "storage_1")) {
		t.Error("storage_1 belongs to another instance and must be left alone")
	}
}

// Seeding replaces the world wholesale. Nothing from the previous boot may
// survive.
func TestSeedReplacesStorage(t *testing.T) {
	missions, _ := fixture(t)
	dst := filepath.Join(missions, "test.world")
	write(t, filepath.Join(dst, "storage_1", "data", "stale.bin"), "stale")
	seed := filepath.Join(t.TempDir(), "saved_world")
	write(t, filepath.Join(seed, "data", "players.db"), "saved")

	if err := Prepare(Options{MissionsDir: missions, Name: "test.world", InstanceID: 1, SeedFrom: seed}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dst, "storage_1", "data", "players.db")); got != "saved" {
		t.Errorf("seed not copied: %q", got)
	}
	if exists(filepath.Join(dst, "storage_1", "data", "stale.bin")) {
		t.Error("stale persistence survived the seed")
	}
	if read(t, filepath.Join(seed, "data", "players.db")) != "saved" {
		t.Error("the seed itself must not be touched")
	}
}

func TestMissingSeedIsAnErrorBeforeAnythingIsDeleted(t *testing.T) {
	missions, _ := fixture(t)
	dst := filepath.Join(missions, "test.world")
	write(t, filepath.Join(dst, "storage_1", "data", "players.db"), "precious")

	err := Prepare(Options{MissionsDir: missions, Name: "test.world", InstanceID: 1, SeedFrom: filepath.Join(missions, "missing")})
	if err == nil {
		t.Fatal("want an error for a missing seed")
	}
	if !exists(filepath.Join(dst, "storage_1", "data", "players.db")) {
		t.Error("storage was deleted even though the seed could not be copied in")
	}
}

func TestDryRunTouchesNothing(t *testing.T) {
	missions, initC := fixture(t)
	dst := filepath.Join(missions, "test.world")
	var log []string
	err := Prepare(Options{
		MissionsDir: missions, Name: "test.world", CloneFrom: "empty.world", InitC: initC,
		InstanceID: 1, Clean: true, DryRun: true,
		Log: func(f string, a ...any) { log = append(log, f) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if exists(dst) {
		t.Error("dry run created the mission")
	}
	for _, l := range log {
		if !strings.HasPrefix(l, "would ") {
			t.Errorf("dry-run log line does not say would: %q", l)
		}
	}
	if len(log) != 3 {
		t.Errorf("want clone, init.c and storage lines, got %q", log)
	}
}
