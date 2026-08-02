//go:build windows

package proc

import (
	"os/exec"
	"syscall"
)

// configure keeps console tools from flashing a window. The DayZ tools are
// console applications, so without this a build pops a window per addon.
func configure(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}
