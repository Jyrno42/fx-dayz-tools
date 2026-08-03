//go:build !windows

package machine

// PboProjectVersion reports the installed pboProject version. Mikero's tools are
// Windows-only, so there is never one to report here.
func PboProjectVersion() string { return "" }

func pboProjectVersionNumber() (int, bool) { return 0, false }

// TestedPboProjectVersion mirrors the Windows constant so callers compile.
const TestedPboProjectVersion = 431

func PboProjectUntested() (string, bool) { return "", false }

// DePboDllVersion reports the installed DePbo dll version. Windows-only, as above.
func DePboDllVersion() string { return "" }

// MinDePboDllVersion mirrors the Windows constant so callers compile.
const MinDePboDllVersion = 1004

func DePboDllTooOld() (string, bool) { return "", false }
