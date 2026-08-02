//go:build windows

package proc

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
)

// KillByName terminates every running process whose executable matches name
// (case-insensitively, e.g. "DayZServer_x64.exe") and reports how many were
// stopped.
//
// Done natively instead of by shelling out to taskkill, so that "no such
// process" comes back as an ordinary zero result rather than a non-zero exit
// every caller has to remember to ignore.
func KillByName(name string) (int, error) {
	pids, err := findByName(name)
	if err != nil {
		return 0, err
	}

	killed := 0
	var firstErr error
	for _, pid := range pids {
		h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, pid)
		if err != nil {
			// The process may have exited between listing and opening. No harm
			// done, since all we wanted was for it to not be running.
			continue
		}
		err = windows.TerminateProcess(h, 1)
		windows.CloseHandle(h)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("proc: terminating %s (pid %d): %w", name, pid, err)
			}
			continue
		}
		killed++
	}
	return killed, firstErr
}

// IsRunning reports whether any process with this executable name is running.
func IsRunning(name string) (bool, error) {
	pids, err := findByName(name)
	return len(pids) > 0, err
}

func findByName(name string) ([]uint32, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, fmt.Errorf("proc: listing processes: %w", err)
	}
	defer windows.CloseHandle(snapshot)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafeSizeof(entry))

	if err := windows.Process32First(snapshot, &entry); err != nil {
		return nil, fmt.Errorf("proc: listing processes: %w", err)
	}

	var pids []uint32
	for {
		exe := windows.UTF16ToString(entry.ExeFile[:])
		if strings.EqualFold(exe, name) {
			pids = append(pids, entry.ProcessID)
		}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			break // ERROR_NO_MORE_FILES ends the walk
		}
	}
	return pids, nil
}
