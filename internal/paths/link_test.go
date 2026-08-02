package paths

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLinkTargetOnOrdinaryDirectory(t *testing.T) {
	dir := t.TempDir()
	target, ok, err := LinkTarget(dir)
	if err != nil {
		t.Fatalf("an ordinary directory must not be an error, got %v", err)
	}
	if ok {
		t.Errorf("ordinary directory reported as a link, target %q", target)
	}
}

func TestLinkTargetOnMissingPath(t *testing.T) {
	_, _, err := LinkTarget(filepath.Join(t.TempDir(), "nope"))
	if err == nil {
		t.Fatal("a missing path should be an error, so callers can tell it apart from a real directory")
	}
}

func TestLinkTargetFollowsALink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "real")
	link := filepath.Join(root, "link")

	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("directory symlinks need Developer Mode or elevation; junctions are covered by the manual pdrive checks")
		}
		t.Fatal(err)
	}

	got, ok, err := LinkTarget(link)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("link not detected")
	}
	if !Equal(got, target) {
		t.Errorf("target = %q, want %q", got, target)
	}

	same, err := ResolvesTo(link, target)
	if err != nil {
		t.Fatal(err)
	}
	if !same {
		t.Error("ResolvesTo said the link points elsewhere")
	}

	// A link pointing at a different repo has to be caught. This is how doctor
	// spots a stale P:\projects\<repo> junction left behind by a moved repo.
	elsewhere, err := ResolvesTo(link, filepath.Join(root, "somewhere-else"))
	if err != nil {
		t.Fatal(err)
	}
	if elsewhere {
		t.Error("ResolvesTo matched a target the link does not point at")
	}
}

func TestResolvesToOnNonLinkIsFalse(t *testing.T) {
	dir := t.TempDir()
	ok, err := ResolvesTo(dir, dir)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("a real directory is not a link to itself")
	}
}
