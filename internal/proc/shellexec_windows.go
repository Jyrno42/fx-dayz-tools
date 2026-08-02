//go:build windows

package proc

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// runViaShellExecute launches a command the way Explorer would, by way of
// PowerShell's Start-Process, and waits for its exit code.
//
// This exists for exactly one tool. Mikero's pboProject.exe refuses to work when
// started with CreateProcess, which is what Go's os/exec uses. It exits 1
// immediately having done nothing, whatever the arguments. Start it through
// Start-Process with byte-identical arguments and it packs correctly.
//
// The difference is not the command line. A hand-built raw command line, an 8.3
// executable path and every quoting variant were all tried, and all failed the
// same way. It is the creation call itself.
//
// Nothing else in the tool needs this, and nothing should adopt it without the
// same kind of evidence.
func runViaShellExecute(ctx context.Context, c Cmd) (Result, error) {
	script := buildStartProcess(c)

	ps := exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	ps.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}

	start := time.Now()
	out, err := ps.CombinedOutput()
	res := Result{
		Cmd:      c,
		Stdout:   string(out),
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

// buildStartProcess renders the Start-Process call. Every value gets single
// quoted, which is literal in PowerShell, so backslashes and the wildcards in an
// exclude list pass through untouched.
func buildStartProcess(c Cmd) string {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference='Stop'; ")
	b.WriteString("$p = Start-Process -FilePath " + psQuote(c.Name))

	if len(c.Args) > 0 {
		quoted := make([]string, len(c.Args))
		for i, a := range c.Args {
			quoted[i] = psQuote(a)
		}
		b.WriteString(" -ArgumentList @(" + strings.Join(quoted, ",") + ")")
	}
	if c.Dir != "" {
		b.WriteString(" -WorkingDirectory " + psQuote(c.Dir))
	}
	b.WriteString(" -Wait -PassThru; exit $p.ExitCode")
	return b.String()
}

// psQuote renders a PowerShell single-quoted literal.
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
