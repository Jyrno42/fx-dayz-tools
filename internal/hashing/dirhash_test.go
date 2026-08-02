package hashing

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// write lays out a tree of "relative/path" -> contents and returns its root.
func write(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func hash(t *testing.T, dir string, opts Options) string {
	t.Helper()
	h, err := DirHash(dir, opts)
	if err != nil {
		t.Fatalf("DirHash(%s): %v", dir, err)
	}
	return h
}

func TestDirHashIsStable(t *testing.T) {
	files := map[string]string{
		"config.cpp":              "class CfgPatches {};",
		"Scripts/4_World/a.c":     "void A() {}",
		"Scripts/5_Mission/b.c":   "void B() {}",
		"data/nested/deep/c.json": `{"k":"v"}`,
	}
	a := hash(t, write(t, files), Options{})
	b := hash(t, write(t, files), Options{})
	if a != b {
		t.Fatalf("same content hashed differently across roots:\n  %s\n  %s", a, b)
	}
}

// The digest should not depend on where the tree lives, so hashing an addon
// through P:\projects\... and E:\projects\... agrees.
func TestDirHashIgnoresRootLocation(t *testing.T) {
	files := map[string]string{"a/b.c": "x"}

	outer := t.TempDir()
	nested := filepath.Join(outer, "some", "much", "deeper", "location")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, body := range files {
		full := filepath.Join(nested, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if got, want := hash(t, nested, Options{}), hash(t, write(t, files), Options{}); got != want {
		t.Fatalf("digest depends on root location:\n  nested %s\n  plain  %s", got, want)
	}
}

func TestDirHashDetectsChanges(t *testing.T) {
	base := map[string]string{"a.c": "one", "b.c": "two"}
	want := hash(t, write(t, base), Options{})

	cases := map[string]map[string]string{
		"content changed": {"a.c": "ONE", "b.c": "two"},
		"file added":      {"a.c": "one", "b.c": "two", "c.c": "three"},
		"file removed":    {"a.c": "one"},
		"file renamed":    {"a.c": "one", "renamed.c": "two"},
		"file moved":      {"a.c": "one", "sub/b.c": "two"},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			if got := hash(t, write(t, files), Options{}); got == want {
				t.Fatalf("%s did not change the digest (%s)", name, got)
			}
		})
	}
}

// A file deleted from source has to stop contributing. This is the failure the
// AddonBuilder temp-cache incident surfaced, where stale content kept shipping.
func TestDirHashDeletionIsVisible(t *testing.T) {
	root := write(t, map[string]string{"keep.c": "k", "gone.c": "g"})
	before := hash(t, root, Options{})

	if err := os.Remove(filepath.Join(root, "gone.c")); err != nil {
		t.Fatal(err)
	}
	if after := hash(t, root, Options{}); after == before {
		t.Fatal("deleting a file left the digest unchanged")
	}
}

// Empty directories carry no content, so they should not affect the digest.
// Git cannot represent them anyway.
func TestDirHashIgnoresEmptyDirs(t *testing.T) {
	root := write(t, map[string]string{"a.c": "x"})
	before := hash(t, root, Options{})

	if err := os.MkdirAll(filepath.Join(root, "empty", "deeper"), 0o755); err != nil {
		t.Fatal(err)
	}
	if after := hash(t, root, Options{}); after != before {
		t.Fatal("an empty directory changed the digest")
	}
}

func TestDirHashIncludesDotfiles(t *testing.T) {
	a := hash(t, write(t, map[string]string{"a.c": "x"}), Options{})
	b := hash(t, write(t, map[string]string{"a.c": "x", ".buildfiles": "*.c"}), Options{})
	if a == b {
		t.Fatal("a dotfile was ignored; AddonBuilder would still have packed it")
	}
}

// Concurrency should not reorder composition.
func TestDirHashWorkerCountDoesNotMatter(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 64; i++ {
		files[filepath.ToSlash(filepath.Join("d", string(rune('a'+i%26)), "f.c"))] += "x"
	}
	root := write(t, files)

	want := hash(t, root, Options{Workers: 1})
	for _, n := range []int{2, 4, 8, 32} {
		if got := hash(t, root, Options{Workers: n}); got != want {
			t.Fatalf("workers=%d changed the digest:\n  got  %s\n  want %s", n, got, want)
		}
	}
}

func TestSkipExcludesSubtree(t *testing.T) {
	withLog := write(t, map[string]string{
		"a.c":              "x",
		"buildlog/one.log": "noise",
		"buildlog/two.log": "more noise",
	})
	without := write(t, map[string]string{"a.c": "x"})

	opts := Options{Skip: SkipNames("buildlog")}
	if got, want := hash(t, withLog, opts), hash(t, without, opts); got != want {
		t.Fatalf("skipped subtree still affected the digest:\n  got  %s\n  want %s", got, want)
	}
}

// A symlink should not be followed. It could point outside the addon or form a
// cycle, and its target is not part of this addon's content anyway.
func TestDirHashSkipsSymlinks(t *testing.T) {
	root := write(t, map[string]string{"a.c": "x"})
	target := filepath.Join(root, "a.c")
	link := filepath.Join(root, "link.c")

	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("creating symlinks needs Developer Mode or elevation on Windows")
		}
		t.Fatal(err)
	}

	only := hash(t, write(t, map[string]string{"a.c": "x"}), Options{})
	if got := hash(t, root, Options{}); got != only {
		t.Fatalf("symlink contributed to the digest:\n  got  %s\n  want %s", got, only)
	}
}

func TestDirHashEmptyDir(t *testing.T) {
	if _, err := DirHash(t.TempDir(), Options{}); err != nil {
		t.Fatalf("hashing an empty directory should succeed, got %v", err)
	}
}

func TestDirHashMissingDir(t *testing.T) {
	if _, err := DirHash(filepath.Join(t.TempDir(), "nope"), Options{}); err == nil {
		t.Fatal("hashing a missing directory should fail")
	}
}
