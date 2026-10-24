package cli

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Jyrno42/fx-dayz-tools/internal/proc"
)

var (
	clientExes = []string{"DayZ_x64.exe", "DayZ_BE.exe", "DayZDiag_x64.exe"}
	serverExes = []string{"DayZServer_x64.exe"}
)

func newStopCmd(g *global) *cobra.Command {
	var client, server, force bool
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop the client and server gracefully, so the server saves its storage",
		Long: "Asks each game window to close, the same as clicking its X, and waits for\n" +
			"the process to exit. The server then runs its normal shutdown and rewrites\n" +
			"the storage folder. `kill` terminates instead, which can lose the last\n" +
			"storage write.\n\n" +
			"The client goes first so the server sees the disconnect before it shuts\n" +
			"down. With --force, anything still running after the timeout is killed.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !client && !server {
				client, server = true, true
			}
			return g.stopAll(cmd.Context(), client, server, timeout, force)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&client, "client", false, "stop the client only")
	f.BoolVar(&server, "server", false, "stop the server only")
	f.BoolVar(&force, "force", false, "kill whatever has not exited by the timeout")
	f.DurationVar(&timeout, "timeout", 90*time.Second, "how long to wait for each role to exit")
	return cmd
}

// stopAll closes the client, waits, then closes the server and waits. Nothing
// running counts as success. A process that will not close is an error unless
// force is set, in which case it is killed.
func (g *global) stopAll(ctx context.Context, client, server bool, timeout time.Duration, force bool) error {
	roles := []struct {
		name string
		exes []string
		on   bool
	}{
		{"client", clientExes, client},
		{"server", serverExes, server},
	}
	for _, r := range roles {
		if !r.on {
			continue
		}
		if err := g.stopRole(ctx, r.name, r.exes, timeout, force); err != nil {
			return err
		}
	}
	return nil
}

func (g *global) stopRole(ctx context.Context, role string, exes []string, timeout time.Duration, force bool) error {
	p := g.printer()

	if g.dryRun {
		for _, exe := range exes {
			p.Detail("would close %s and wait up to %s", exe, timeout)
		}
		return nil
	}

	var asked, stuck []int
	for _, exe := range exes {
		a, noWindow, err := proc.CloseByName(exe)
		if err != nil {
			return coded(ExitPreflight, "%v", err)
		}
		asked = append(asked, a...)
		stuck = append(stuck, noWindow...)
	}
	if len(asked) == 0 && len(stuck) == 0 {
		p.Line("  %-8s nothing running", role)
		return nil
	}

	if len(asked) > 0 {
		p.Line("  %-8s asked %s to close, waiting up to %s", role, pidList(asked), timeout)
		stuck = append(stuck, proc.WaitGone(ctx, asked, timeout)...)
	}
	if len(stuck) == 0 {
		p.Line("  %-8s exited", role)
		return nil
	}

	if !force {
		return coded(ExitPreflight, "%s %s did not exit, rerun with --force or use `dayzmod kill`", role, pidList(stuck))
	}
	for _, exe := range exes {
		if _, err := proc.KillByName(exe); err != nil {
			return coded(ExitPreflight, "%v", err)
		}
	}
	p.Warn("%s %s did not exit in time and was killed", role, pidList(stuck))
	return nil
}

func pidList(pids []int) string {
	parts := make([]string, len(pids))
	for i, pid := range pids {
		parts[i] = "pid " + strconv.Itoa(pid)
	}
	return strings.Join(parts, ", ")
}
