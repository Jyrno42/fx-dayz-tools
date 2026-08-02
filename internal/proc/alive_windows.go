//go:build windows

package proc

import "golang.org/x/sys/windows"

// stillActive is the exit code Windows reports for a process that has not
// exited yet.
const stillActive = 259

// Alive reports whether a process is still running.
//
// PID reuse is possible in principle, but the window is tiny and the callers
// here only ever watch a process they spawned moments earlier.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		// No such process, or it is gone and no longer queryable.
		return false
	}
	defer windows.CloseHandle(h)

	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}
