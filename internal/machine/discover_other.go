//go:build !windows

package machine

// The DayZ toolchain is Windows-only. These stubs exist so the rest of the
// package (config parsing, the VDF reader, tool path composition) compiles and
// unit-tests on any platform.

func steamRoot() (string, error) { return "", nil }
func mikeroBin() string          { return "" }
