package proc

import (
	"fmt"
	"os/exec"
)

// Spawn starts a command and returns without waiting for it.
//
// The game and the dedicated server outlive the tool, so they get started
// detached. dayzmod exits and they keep running. It is what `start ""` did in
// the old Taskfiles, minus the shell.
func Spawn(c Cmd) (pid int, err error) {
	cmd := exec.Command(c.Name, c.Args...)
	cmd.Dir = c.Dir
	if len(c.Env) > 0 {
		cmd.Env = c.Env
	}
	detach(cmd)

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("proc: starting %s: %w", c.Name, err)
	}
	pid = cmd.Process.Pid

	// Release the handle so the child is not tied to this process, and so we do
	// not leave a zombie behind on platforms that would otherwise keep one.
	if err := cmd.Process.Release(); err != nil {
		return pid, fmt.Errorf("proc: releasing %s: %w", c.Name, err)
	}
	return pid, nil
}
