// Package proc runs external tools.
//
// Everything goes through here so that --dry-run can print exactly what would
// have run, and so no command ever gets assembled as a shell string. The old
// pipeline shelled out to powershell -Command for mkdir and copy. Nothing here
// needs a shell at all.
package proc

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Cmd is an external command to run.
type Cmd struct {
	Name string
	Args []string
	Dir  string
	Env  []string
	// NeedsConsole runs the command as a child of cmd.exe, so it inherits a real
	// console. Exactly one tool needs it, because it shells out internally (see
	// runViaConsole), and nothing else should set it without the same evidence.
	NeedsConsole bool
}

// String renders the command the way a user could paste it back into a shell,
// quoting only what needs it.
func (c Cmd) String() string {
	parts := make([]string, 0, len(c.Args)+1)
	parts = append(parts, quote(c.Name))
	for _, a := range c.Args {
		parts = append(parts, quote(a))
	}
	return strings.Join(parts, " ")
}

func quote(s string) string {
	if s == "" {
		return `""`
	}
	if !strings.ContainsAny(s, " \t\"") {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// Result is the outcome of a run.
type Result struct {
	Cmd      Cmd
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
}

// Output returns stdout and stderr combined, for logging.
func (r Result) Output() string {
	if r.Stderr == "" {
		return r.Stdout
	}
	if r.Stdout == "" {
		return r.Stderr
	}
	return r.Stdout + "\n" + r.Stderr
}

// Tail returns the last n non-empty lines of the output, for error reporting.
// Tool output runs to hundreds of lines and usually only the end is diagnostic.
func (r Result) Tail(n int) string {
	lines := strings.Split(strings.ReplaceAll(r.Output(), "\r\n", "\n"), "\n")
	var kept []string
	for i := len(lines) - 1; i >= 0 && len(kept) < n; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		kept = append([]string{lines[i]}, kept...)
	}
	return strings.Join(kept, "\n")
}

// Runner executes commands. The dry-run implementation records them instead.
type Runner interface {
	Run(ctx context.Context, c Cmd) (Result, error)
	// DryRun reports whether commands are being recorded instead of executed.
	DryRun() bool
}

// Exec is the real runner.
type Exec struct {
	// OnStart is called before each command, for progress output.
	OnStart func(Cmd)
}

func (e *Exec) DryRun() bool { return false }

func (e *Exec) Run(ctx context.Context, c Cmd) (Result, error) {
	if e.OnStart != nil {
		e.OnStart(c)
	}
	if c.NeedsConsole {
		return runViaConsole(ctx, c)
	}
	return e.runDirect(ctx, c)
}

func (e *Exec) runDirect(ctx context.Context, c Cmd) (Result, error) {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...)
	cmd.Dir = c.Dir
	if len(c.Env) > 0 {
		cmd.Env = c.Env
	}
	configure(cmd)

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

// Dry records commands without running them.
type Dry struct {
	// OnRecord is called for each command that would have run.
	OnRecord func(Cmd)
	Commands []Cmd
}

func (d *Dry) DryRun() bool { return true }

func (d *Dry) Run(_ context.Context, c Cmd) (Result, error) {
	d.Commands = append(d.Commands, c)
	if d.OnRecord != nil {
		d.OnRecord(c)
	}
	return Result{Cmd: c}, nil
}

func asExitError(err error, target **exec.ExitError) bool {
	if ee, ok := err.(*exec.ExitError); ok {
		*target = ee
		return true
	}
	return false
}
