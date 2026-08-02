//go:build !windows

package paths

import "fmt"

// ErrWindowsOnly comes back from the drive and junction operations on platforms
// where they mean nothing. The rest of the package (prefix derivation, path
// comparison, link reading) works everywhere so it can be unit-tested on CI.
var ErrWindowsOnly = fmt.Errorf("paths: drive and junction management is Windows-only")

func LookupDrive(letter string) (Drive, error) {
	if _, err := normaliseLetter(letter); err != nil {
		return Drive{}, err
	}
	return Drive{}, ErrWindowsOnly
}

func MountSubst(letter, dir string) error      { return ErrWindowsOnly }
func UnmountSubst(letter string) error         { return ErrWindowsOnly }
func CreateJunction(link, target string) error { return ErrWindowsOnly }
func RemoveJunction(link string) error         { return ErrWindowsOnly }
