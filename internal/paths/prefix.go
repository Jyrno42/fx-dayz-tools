// Package paths handles the P: drive, junctions, and the PBO prefix derivation
// that ties a repo's on-disk location to the path space inside its PBOs.
package paths

import (
	"fmt"
	"strings"
)

// ToWindows normalises separators to backslashes. PBO prefixes, AddonBuilder's
// -prefix= and $PBOPREFIX$ are all backslash-separated regardless of how the
// path was written in config.
func ToWindows(p string) string { return strings.ReplaceAll(p, "/", `\`) }

// ToSlash normalises separators to forward slashes.
func ToSlash(p string) string { return strings.ReplaceAll(p, `\`, "/") }

// WinJoin joins path elements with backslashes.
//
// Some of what this tool produces is a Windows path no matter what machine
// builds it: the `!Workshop\@Mod` the DayZ client is handed, the -M= folder
// pboProject receives. filepath.Join would render those with forward slashes on
// a Linux host, which is wrong output rather than a different-but-equal path.
func WinJoin(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.Trim(ToWindows(p), `\`); p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, `\`)
}

// WinBase returns the last element of a backslash-separated path, and WinDir
// everything before it. Both mirror filepath.Base/Dir but always read `\` as the
// separator, so they behave the same on any host.
func WinBase(p string) string {
	p = strings.TrimRight(ToWindows(p), `\`)
	if i := strings.LastIndex(p, `\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// WinDir returns p without its last element, keeping any drive letter.
func WinDir(p string) string {
	p = strings.TrimRight(ToWindows(p), `\`)
	i := strings.LastIndex(p, `\`)
	if i < 0 {
		return p
	}
	if dir := p[:i]; dir != "" {
		return dir
	}
	return `\`
}

// SplitDrive splits "P:\projects\x" into "P:" and `projects\x`. The remainder
// has no leading or trailing separator. If p has no drive letter, drive is "".
func SplitDrive(p string) (drive, rest string) {
	p = ToWindows(strings.TrimSpace(p))
	if len(p) >= 2 && p[1] == ':' && isDriveLetter(p[0]) {
		drive, p = strings.ToUpper(p[:2]), p[2:]
	}
	return drive, strings.Trim(p, `\`)
}

func isDriveLetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// PrefixRoot converts a repo's P:-drive location into the prefix space its PBOs
// live in, so "P:\projects\project-with-everything" becomes
// `projects\project-with-everything`.
//
// This is the string AddonBuilder receives via -prefix=, and the one pboProject
// derives on its own from the source folder's position under P:. The two agree
// exactly, which is why moving a repo between packers is not a prefix break. It
// also means the prefix encodes the repo's folder name, typos and all (see
// minimal-config-repo).
func PrefixRoot(pdrivePath string) (string, error) {
	_, rest := SplitDrive(pdrivePath)
	if rest == "" {
		return "", fmt.Errorf("paths: %q has no path below the drive letter; a PBO prefix cannot be derived from a drive root", pdrivePath)
	}
	if strings.HasPrefix(ToWindows(strings.TrimSpace(pdrivePath)), `\\`) {
		return "", fmt.Errorf("paths: %q is a UNC path; the DayZ tools require a drive-letter path under P:", pdrivePath)
	}
	return rest, nil
}

// Prefix builds the full PBO prefix for an addon, which is PrefixRoot joined
// with the addon set's source folder and the addon name.
//
//	Prefix(`P:\projects\project-with-everything`, "mod", "PWE_Core")
//	  -> `projects\project-with-everything\mod\PWE_Core`
func Prefix(pdrivePath string, parts ...string) (string, error) {
	root, err := PrefixRoot(pdrivePath)
	if err != nil {
		return "", err
	}
	return JoinPrefix(root, parts...), nil
}

// JoinPrefix appends parts to an already-derived prefix root, normalising
// separators and dropping empty segments.
func JoinPrefix(root string, parts ...string) string {
	out := strings.Trim(ToWindows(root), `\`)
	for _, p := range parts {
		p = strings.Trim(ToWindows(p), `\`)
		if p == "" {
			continue
		}
		if out == "" {
			out = p
			continue
		}
		out += `\` + p
	}
	return out
}

// Equal reports whether two filesystem paths refer to the same location,
// ignoring separator style, case (Windows paths are case-insensitive), and
// trailing separators. It is a string comparison and does not resolve links.
func Equal(a, b string) bool {
	return strings.EqualFold(normalise(a), normalise(b))
}

// Under reports whether path is root itself or lies beneath it.
func Under(root, path string) bool {
	r, p := normalise(root), normalise(path)
	if strings.EqualFold(r, p) {
		return true
	}
	if !strings.HasSuffix(r, `\`) {
		r += `\`
	}
	return strings.HasPrefix(strings.ToLower(p), strings.ToLower(r))
}

func normalise(p string) string {
	p = ToWindows(strings.TrimSpace(p))
	// Keep a bare drive root as "C:\" so Under("C:", "C:\x") behaves.
	if len(p) == 3 && p[1] == ':' && p[2] == '\\' {
		return p
	}
	return strings.TrimRight(p, `\`)
}
