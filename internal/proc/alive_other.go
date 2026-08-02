//go:build !windows

package proc

import (
	"os"
	"syscall"
)

// Alive reports whether a process is still running.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
