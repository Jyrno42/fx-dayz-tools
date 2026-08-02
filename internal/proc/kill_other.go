//go:build !windows

package proc

import "fmt"

// The DayZ client and server are Windows-only. These stubs keep the package
// building on other platforms so the rest of the suite runs in CI.

func KillByName(name string) (int, error) {
	return 0, fmt.Errorf("proc: process control is Windows-only")
}

func IsRunning(name string) (bool, error) {
	return false, fmt.Errorf("proc: process control is Windows-only")
}
