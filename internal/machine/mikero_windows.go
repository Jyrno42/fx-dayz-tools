//go:build windows

package machine

import (
	"strconv"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// PboProjectVersion reports the installed pboProject version as it registers
// itself, e.g. "391" for 3.91. Empty when it is not installed or not
// registered.
func PboProjectVersion() string {
	return strings.TrimSpace(regString(registry.CURRENT_USER, `Software\Mikero\pboProject`, "version"))
}

// pboProjectVersionNumber parses the registered version for comparison.
func pboProjectVersionNumber() (int, bool) {
	v := PboProjectVersion()
	if v == "" {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	return n, err == nil
}

// TestedPboProjectVersion is the version the packer has actually been exercised
// against. Newer releases exist and are untested here, so a mismatch is worth
// surfacing up front instead of discovering through a failure.
const TestedPboProjectVersion = 391

// PboProjectUntested reports whether the installed version differs from the one
// the packer was verified against.
func PboProjectUntested() (installed string, untested bool) {
	n, ok := pboProjectVersionNumber()
	if !ok {
		return "", false
	}
	return PboProjectVersion(), n != TestedPboProjectVersion
}
