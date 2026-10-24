//go:build windows

package proc

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32       = windows.NewLazySystemDLL("user32.dll")
	procPostMsg  = user32.NewProc("PostMessageW")
	procGetOwner = user32.NewProc("GetWindow")
)

const (
	wmClose = 0x0010
	gwOwner = 4
)

// CloseByName asks every process with this executable name to exit by posting
// WM_CLOSE to its main window, which is what clicking the window's X does. The
// server then runs its normal shutdown and rewrites the storage folder, where
// TerminateProcess can lose the last write.
//
// It returns the pids that were asked and the pids that had no window to ask.
func CloseByName(name string) (asked, noWindow []int, err error) {
	pids, err := findByName(name)
	if err != nil {
		return nil, nil, err
	}
	for _, pid := range pids {
		hwnd := mainWindow(pid)
		if hwnd == 0 {
			noWindow = append(noWindow, int(pid))
			continue
		}
		r, _, callErr := procPostMsg.Call(uintptr(hwnd), wmClose, 0, 0)
		if r == 0 {
			return asked, noWindow, fmt.Errorf("proc: closing %s (pid %d): %w", name, pid, callErr)
		}
		asked = append(asked, int(pid))
	}
	return asked, noWindow, nil
}

// mainWindow finds a process's main window the way .NET's Process.MainWindow
// does: a visible top-level window with no owner. 0 means none.
func mainWindow(pid uint32) windows.HWND {
	var found windows.HWND
	cb := windows.NewCallback(func(hwnd windows.HWND, _ uintptr) uintptr {
		var owner uint32
		windows.GetWindowThreadProcessId(hwnd, &owner)
		if owner != pid || !windows.IsWindowVisible(hwnd) {
			return 1
		}
		if parent, _, _ := procGetOwner.Call(uintptr(hwnd), gwOwner); parent != 0 {
			return 1
		}
		found = hwnd
		return 0
	})
	// EnumWindows reports an error when the callback stops it early, which is
	// how a hit is signalled, so its return value is not useful here.
	_ = windows.EnumWindows(cb, unsafe.Pointer(nil))
	return found
}
