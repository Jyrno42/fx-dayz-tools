//go:build windows

package proc

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// createNoWindow gives the child its own console without putting a window on
// screen. A console flashing up per addon during a release would be
// intolerable, and CREATE_NEW_CONSOLE does exactly that.
const createNoWindow = 0x08000000

// runViaConsole launches a command as a child of cmd.exe, so that it inherits a
// real console.
//
// This exists for exactly one tool. Mikero's pboProject shells out to copy the
// .bikey into the mod folder's keys\ directory when it signs. Run it in a
// terminal and cmd's own "1 File(s) copied" goes past. Started directly with
// CreateProcess it has no console for that child: the copy fails, the keys\
// directory is left empty, and the run exits 1 before packing anything, without
// writing a packing log to say why.
//
// Verified against the same addon: captured pipes, NUL handles, inherited
// handles, CREATE_NEW_CONSOLE, CREATE_NO_WINDOW and DETACHED_PROCESS all fail
// when applied to pboProject directly. Interposing cmd.exe fixes it, with or
// without a window.
//
// Packing WITHOUT signing works fine under plain CreateProcess, and 3.91's
// refusal to run that way at all is gone in 4.31. So this is narrowly about
// giving pboProject a shell to sign through, not about how it is started.
func runViaConsole(ctx context.Context, c Cmd) (Result, error) {
	cmd := exec.CommandContext(ctx, "cmd.exe")
	cmd.Dir = c.Dir
	if len(c.Env) > 0 {
		cmd.Env = c.Env
	}
	// CmdLine is built by hand because cmd.exe's quoting rules are its own: with
	// /s and a single outer quote pair it takes everything between the first and
	// last quote literally, which is the only form that survives a path with a
	// space in it. Go's own argv escaping produces something cmd mis-splits.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine:       buildCmdLine(c),
		CreationFlags: createNoWindow,
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	res := Result{
		Cmd:      c,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: time.Since(start),
	}

	if err != nil {
		var ee *exec.ExitError
		if ok := asExitError(err, &ee); ok {
			res.ExitCode = ee.ExitCode()
			return res, fmt.Errorf("%s exited with code %d", c.Name, res.ExitCode)
		}
		return res, fmt.Errorf("%s: %w", c.Name, err)
	}
	return res, nil
}

// buildCmdLine renders `cmd.exe /s /c "<exe> <args...>"`.
func buildCmdLine(c Cmd) string {
	parts := make([]string, 0, len(c.Args)+1)
	parts = append(parts, `"`+c.Name+`"`)
	for _, a := range c.Args {
		if strings.ContainsAny(a, " \t") {
			a = `"` + a + `"`
		}
		parts = append(parts, a)
	}
	return `cmd.exe /s /c "` + strings.Join(parts, " ") + `"`
}
