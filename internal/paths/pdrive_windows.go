//go:build windows

package paths

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// LookupDrive reports how a drive letter is currently backed.
func LookupDrive(letter string) (Drive, error) {
	l, err := normaliseLetter(letter)
	if err != nil {
		return Drive{}, err
	}

	target, err := queryDosDevice(l)
	if err != nil {
		// ERROR_FILE_NOT_FOUND simply means the letter is not defined.
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
			return classify(l, ""), nil
		}
		return Drive{}, fmt.Errorf("paths: QueryDosDevice(%s): %w", l, err)
	}
	return classify(l, target), nil
}

// queryDosDevice returns the NT target of a DOS device name such as "P:". A
// subst drive gives `\??\C:\some\dir`, a real volume gives `\Device\...`.
func queryDosDevice(name string) (string, error) {
	dev, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return "", err
	}
	// A device can map to several targets, but only the first matters here, and
	// 1024 code units comfortably covers any real path.
	buf := make([]uint16, 1024)
	n, err := windows.QueryDosDevice(dev, &buf[0], uint32(len(buf)))
	if err != nil {
		return "", err
	}
	if n == 0 {
		return "", nil
	}
	return windows.UTF16ToString(buf), nil
}

// MountSubst maps letter onto dir using `subst`. Windows exposes no public API
// for this, so shelling out to subst.exe is the supported route. It is a tiny
// system binary and the call is nowhere near a hot path.
func MountSubst(letter, dir string) error {
	l, err := normaliseLetter(letter)
	if err != nil {
		return err
	}
	fi, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("paths: subst target %s: %w", dir, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("paths: subst target %s is not a directory", dir)
	}
	return run("subst", l, ToWindows(dir))
}

// UnmountSubst removes a subst mapping.
func UnmountSubst(letter string) error {
	l, err := normaliseLetter(letter)
	if err != nil {
		return err
	}
	return run("subst", l, "/d")
}

// CreateJunction makes link a directory junction pointing at target.
//
// Junctions instead of symlinks, deliberately. Creating a directory symlink on
// Windows needs elevation or Developer Mode, while a junction needs neither.
// That is why every mod repo's setup notes say `mklink /J`.
func CreateJunction(link, target string) error {
	fi, err := os.Stat(target)
	if err != nil {
		return fmt.Errorf("paths: junction target %s: %w", target, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("paths: junction target %s is not a directory", target)
	}
	if _, err := os.Lstat(link); err == nil {
		return fmt.Errorf("paths: %s already exists", link)
	}
	// mklink is a cmd builtin and cannot be exec'd directly.
	return run("cmd", "/c", "mklink", "/J", ToWindows(link), ToWindows(target))
}

// RemoveJunction deletes a junction without touching what it points at.
// os.Remove on a junction unlinks only the reparse point, which is what we want.
// This wrapper refuses to run on an ordinary directory, so a mistyped path
// cannot delete real work.
func RemoveJunction(link string) error {
	ok, err := IsLink(link)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("paths: %s is a real directory, not a junction; refusing to remove it", link)
	}
	return os.Remove(link)
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("paths: %s %v: %w: %s", name, args, err, trimOutput(out))
	}
	return nil
}

func trimOutput(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}
