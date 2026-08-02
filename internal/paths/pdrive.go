package paths

import (
	"fmt"
	"os"
	"strings"
)

// DriveKind describes how a drive letter is backed.
type DriveKind string

const (
	// DriveSubst is a letter mapped onto a directory by `subst`. That is what P:
	// is, a view of C:\Users\me\Documents\DayZ Projects. It does NOT survive a
	// reboot, which is the single most common cause of a baffling first build
	// after restarting.
	DriveSubst DriveKind = "subst"
	// DriveVolume is a real volume.
	DriveVolume DriveKind = "volume"
	// DriveAbsent means no such drive letter is defined.
	DriveAbsent DriveKind = "absent"
)

// Drive describes the state of a drive letter.
type Drive struct {
	Letter string // "P:"
	Kind   DriveKind
	// Backing is the directory a subst drive points at. Empty for other kinds.
	Backing string
	// Device is the raw NT device path, e.g. `\Device\HarddiskVolume4`, for a
	// real volume. Empty for a subst.
	Device string
}

// Mounted reports whether the drive letter currently resolves to anything.
func (d Drive) Mounted() bool { return d.Kind != DriveAbsent }

// Describe renders the drive for doctor output.
func (d Drive) Describe() string {
	switch d.Kind {
	case DriveSubst:
		return fmt.Sprintf("%s -> %s (subst)", d.Letter, d.Backing)
	case DriveVolume:
		return fmt.Sprintf("%s -> %s (volume)", d.Letter, d.Device)
	default:
		return fmt.Sprintf("%s is not mounted", d.Letter)
	}
}

// normaliseLetter accepts "P", "p", "P:", `P:\` and returns "P:".
func normaliseLetter(letter string) (string, error) {
	l := strings.TrimSpace(letter)
	l = strings.TrimRight(l, `\/`)
	l = strings.TrimSuffix(l, ":")
	if len(l) != 1 || !isDriveLetter(l[0]) {
		return "", fmt.Errorf("paths: %q is not a drive letter", letter)
	}
	return strings.ToUpper(l) + ":", nil
}

// substPrefix is what QueryDosDevice returns in front of a subst target, e.g.
// `\??\C:\Users\me\Documents\DayZ Projects`.
const substPrefix = `\??\`

// classify turns a raw QueryDosDevice result into a Drive.
func classify(letter, target string) Drive {
	d := Drive{Letter: letter}
	switch {
	case target == "":
		d.Kind = DriveAbsent
	case strings.HasPrefix(target, substPrefix):
		d.Kind = DriveSubst
		d.Backing = strings.TrimPrefix(target, substPrefix)
	default:
		d.Kind = DriveVolume
		d.Device = target
	}
	return d
}

// Visible reports whether a drive letter resolves in THIS process.
//
// This is deliberately not the same question LookupDrive answers. A subst
// mapping lives in a per-logon-session device map, so a process started under a
// different session or token never inherits it. The drive really is unreachable
// for that process, even though the files behind it are untouched and another
// shell sees them fine.
func Visible(letter string) bool {
	l, err := normaliseLetter(letter)
	if err != nil {
		return false
	}
	_, statErr := os.Stat(l + `\`)
	return statErr == nil
}

// Rebase maps a path on the given drive letter onto the directory that drive is
// mapped to, so a file can still be inspected when the drive itself is invisible
// to this process.
//
// For inspection only. The DayZ tools resolve their own paths and genuinely need
// the drive, so a build still has to fail instead of quietly rebasing.
func Rebase(path, letter, backing string) (string, bool) {
	if backing == "" {
		return path, false
	}
	l, err := normaliseLetter(letter)
	if err != nil {
		return path, false
	}
	drive, rest := SplitDrive(path)
	if !strings.EqualFold(drive, l) {
		return path, false
	}
	if rest == "" {
		return backing, true
	}
	return strings.TrimRight(ToWindows(backing), `\`) + `\` + rest, true
}
