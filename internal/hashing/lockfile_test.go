package hashing

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadLockfileMissingIsEmpty(t *testing.T) {
	lf, err := LoadLockfile(filepath.Join(t.TempDir(), ".build_lockfile.json"))
	if err != nil {
		t.Fatalf("a missing lockfile must not be an error, got %v", err)
	}
	if got := lf.Keys(); len(got) != 0 {
		t.Fatalf("expected no entries, got %v", got)
	}
	if lf.Fresh(Key("main", "PWE_Core"), "abc") {
		t.Fatal("an absent entry must never report fresh")
	}
}

func TestLockfileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".build_lockfile.json")

	lf, err := LoadLockfile(path)
	if err != nil {
		t.Fatal(err)
	}
	lf.Set(Key("main", "PWE_Core"), "aaa")
	lf.Set(Key("main", "PWE_Parts"), "bbb")
	lf.Set(Key("devspawn", "PWE_DevSpawn"), "ccc")
	if err := lf.Save(); err != nil {
		t.Fatal(err)
	}

	got, err := LoadLockfile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Fresh(Key("main", "PWE_Core"), "aaa") {
		t.Error("PWE_Core did not survive the round trip")
	}
	if got.Fresh(Key("main", "PWE_Core"), "different") {
		t.Error("a changed hash must not report fresh")
	}
	if h, ok := got.Get(Key("devspawn", "PWE_DevSpawn")); !ok || h != "ccc" {
		t.Errorf("devspawn entry = %q, %v; want ccc, true", h, ok)
	}
}

// Two sets sharing an addon name should not alias each other.
func TestLockfileQualifiesBySet(t *testing.T) {
	lf, _ := LoadLockfile(filepath.Join(t.TempDir(), "lock.json"))
	lf.Set(Key("main", "Shared"), "one")
	lf.Set(Key("devspawn", "Shared"), "two")

	if h, _ := lf.Get(Key("main", "Shared")); h != "one" {
		t.Errorf("main/Shared = %q, want one", h)
	}
	if h, _ := lf.Get(Key("devspawn", "Shared")); h != "two" {
		t.Errorf("devspawn/Shared = %q, want two", h)
	}
}

func TestLockfileClearAndDelete(t *testing.T) {
	lf, _ := LoadLockfile(filepath.Join(t.TempDir(), "lock.json"))
	lf.Set(Key("main", "A"), "a")
	lf.Set(Key("main", "B"), "b")

	lf.Delete(Key("main", "A"))
	if _, ok := lf.Get(Key("main", "A")); ok {
		t.Error("deleted entry still present")
	}
	if _, ok := lf.Get(Key("main", "B")); !ok {
		t.Error("Delete removed the wrong entry")
	}

	lf.Clear()
	if got := lf.Keys(); len(got) != 0 {
		t.Errorf("Clear left %v", got)
	}
}

// A truncated or hand-mangled lockfile should fail loudly instead of quietly
// reading as empty. That would look like "everything needs rebuilding" while
// just as easily masking a real problem.
func TestLoadLockfileRejectsGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock.json")
	if err := os.WriteFile(path, []byte(`{"main/PWE_Core": `), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadLockfile(path); err == nil {
		t.Fatal("expected a parse error for a truncated lockfile")
	}
}

func TestLoadLockfileEmptyFileIsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock.json")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	lf, err := LoadLockfile(path)
	if err != nil {
		t.Fatalf("a zero-byte lockfile should read as empty, got %v", err)
	}
	if len(lf.Keys()) != 0 {
		t.Fatal("expected no entries")
	}
}

// Saving over an existing lockfile has to work on Windows too, where rename onto
// an existing file fails.
func TestLockfileSaveOverwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock.json")

	first, _ := LoadLockfile(path)
	first.Set(Key("main", "A"), "one")
	if err := first.Save(); err != nil {
		t.Fatal(err)
	}

	second, _ := LoadLockfile(path)
	second.Set(Key("main", "A"), "two")
	if err := second.Save(); err != nil {
		t.Fatalf("overwriting an existing lockfile failed: %v", err)
	}

	got, _ := LoadLockfile(path)
	if h, _ := got.Get(Key("main", "A")); h != "two" {
		t.Errorf("main/A = %q, want two", h)
	}

	// The atomic write should not leave scratch files behind.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("expected only the lockfile, found %d entries", len(entries))
	}
}
