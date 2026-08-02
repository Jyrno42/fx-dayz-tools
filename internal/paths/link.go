package paths

import (
	"fmt"
	"os"
)

// LinkTarget returns the target of a junction or symlink at path.
//
// Windows junctions are a trap worth stating plainly. Lstat reports them as
// os.ModeIrregular, NOT os.ModeSymlink, so the obvious
//
//	fi.Mode()&os.ModeSymlink != 0
//
// check quietly returns false for every junction under P:\projects. Detect them
// by whether Readlink succeeds instead, which is what this function does.
//
// ok is false when path exists but is an ordinary file or directory. You only
// get an error back when path is missing or unreadable.
func LinkTarget(path string) (target string, ok bool, err error) {
	target, readErr := os.Readlink(path)
	if readErr == nil {
		return target, true, nil
	}
	// Readlink failed, so either path is not a reparse point or it is not there
	// at all. Lstat tells the two apart, and only the latter is an error.
	if _, statErr := os.Lstat(path); statErr != nil {
		return "", false, fmt.Errorf("paths: %s: %w", path, statErr)
	}
	return "", false, nil
}

// IsLink reports whether path is a junction or symlink.
func IsLink(path string) (bool, error) {
	_, ok, err := LinkTarget(path)
	return ok, err
}

// ResolvesTo reports whether linkPath is a junction or symlink pointing at want.
// doctor uses it to confirm that P:\projects\<repo> really is this repo, and not
// a stale link left behind by a repo that moved.
func ResolvesTo(linkPath, want string) (bool, error) {
	target, ok, err := LinkTarget(linkPath)
	if err != nil || !ok {
		return false, err
	}
	return Equal(target, want), nil
}
