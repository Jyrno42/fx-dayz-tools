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
// against: packed, obfuscated, signed and round-tripped with ExtractPbo. Newer
// releases exist and are untested here, so a mismatch is worth surfacing up
// front instead of discovering through a failure.
const TestedPboProjectVersion = 431

// PboProjectUntested reports whether the installed version differs from the one
// the packer was verified against.
func PboProjectUntested() (installed string, untested bool) {
	n, ok := pboProjectVersionNumber()
	if !ok {
		return "", false
	}
	return PboProjectVersion(), n != TestedPboProjectVersion
}

// DePboDllVersion reports the installed DePbo dll version as it registers
// itself, e.g. "1022" for 10.22. Empty when it is not installed.
//
// It is worth reporting separately from pboProject's own version because the dll
// is where obfuscation and compression actually happen, it ships on its own
// release cadence, and pboProject states a minimum it needs rather than pinning
// one. A pboProject upgrade that left the dll behind fails inside the dll.
func DePboDllVersion() string {
	return strings.TrimSpace(regString(registry.CURRENT_USER, `Software\Mikero\DePbo`, "version"))
}

// MinDePboDllVersion is the minimum the installed pboProject asks for. 4.31
// states "minimum dll is 10.04" at the top of its own change log.
const MinDePboDllVersion = 1004

// DePboDllTooOld reports the installed dll version and whether it is below what
// pboProject needs.
func DePboDllTooOld() (installed string, tooOld bool) {
	v := DePboDllVersion()
	if v == "" {
		return "", false
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return v, false
	}
	return v, n < MinDePboDllVersion
}
