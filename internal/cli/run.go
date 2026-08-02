package cli

import (
	"context"
	"path/filepath"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/Jyrno42/fx-dayz-tools/internal/dayz"
	"github.com/Jyrno42/fx-dayz-tools/internal/machine"
	"github.com/Jyrno42/fx-dayz-tools/internal/modcfg"
	"github.com/Jyrno42/fx-dayz-tools/internal/netwait"
	"github.com/Jyrno42/fx-dayz-tools/internal/pipeline"
	"github.com/Jyrno42/fx-dayz-tools/internal/proc"
	"github.com/Jyrno42/fx-dayz-tools/internal/serverdz"
)

// launchFlags are shared by every command that starts or stops the game.
type launchFlags struct {
	diag     bool
	preset   string
	port     int
	battlEye bool
	noBE     bool
}

func (l *launchFlags) bind(cmd *cobra.Command) {
	f := cmd.Flags()
	f.BoolVar(&l.diag, "diag", false, "use the diagnostic executables")
	f.StringVar(&l.preset, "preset", "", "apply a launch preset from dayz.yml")
	f.IntVar(&l.port, "port", 0, "override the server port")
	f.BoolVar(&l.battlEye, "battleye", false, "force BattlEye on")
	f.BoolVar(&l.noBE, "no-battleye", false, "force BattlEye off")
}

// override returns an explicit BattlEye choice, or nil to use the config.
func (l *launchFlags) override(cmd *cobra.Command) (*bool, error) {
	on := cmd.Flags().Changed("battleye")
	off := cmd.Flags().Changed("no-battleye")
	switch {
	case on && off:
		return nil, coded(ExitConfig, "--battleye and --no-battleye contradict each other")
	case on:
		v := true
		return &v, nil
	case off:
		v := false
		return &v, nil
	}
	// The diag client can only join a BattlEye-off server, so asking for diag
	// implies that unless the config's diag preset already says otherwise.
	if l.diag {
		v := false
		return &v, nil
	}
	return nil, nil
}

// launchCtx is everything the run commands need, resolved once.
type launchCtx struct {
	mod      *modcfg.Config
	host     *machine.Config
	mods     dayz.ModList
	port     int
	battlEye bool
	diag     bool
	// serverPID is the process startServer launched, so waits can notice it exit
	// instead of sitting out their full timeout.
	serverPID int
}

func (g *global) launchCtx(cmd *cobra.Command, l *launchFlags, channel string) (*launchCtx, error) {
	mod, err := g.mod()
	if err != nil {
		return nil, err
	}
	host, err := g.host()
	if err != nil {
		return nil, err
	}
	be, err := l.override(cmd)
	if err != nil {
		return nil, err
	}

	mods, err := dayz.ResolveMods(mod, channel, l.preset)
	if err != nil {
		return nil, coded(ExitConfig, "%v", err)
	}

	return &launchCtx{
		mod:      mod,
		host:     host,
		mods:     mods,
		port:     dayz.PortFor(mod, l.preset, l.port),
		battlEye: dayz.BattlEyeFor(mod, l.preset, be),
		diag:     l.diag,
	}, nil
}

// --- server ---------------------------------------------------------------

func newServerCmd(g *global) *cobra.Command {
	l := &launchFlags{}
	cmd := &cobra.Command{
		Use:   "server",
		Short: "Start the local dedicated server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			lc, err := g.launchCtx(cmd, l, "dev")
			if err != nil {
				return err
			}
			return g.startServer(lc)
		},
	}
	l.bind(cmd)
	return cmd
}

func (g *global) startServer(lc *launchCtx) error {
	p := g.printer()

	exe := lc.host.ServerExe(lc.diag)
	if exe == "" {
		return coded(ExitPreflight, "the DayZ server path is not configured; run `dayzmod config init`")
	}
	if !fileExists(exe) {
		return coded(ExitPreflight, "server executable not found: %s", exe)
	}

	// The repo's config gets copied into the server install, with BattlEye applied
	// to the deployed copy only. We never touch the tracked file.
	cfgName := "serverdz_local.cfg"
	if lc.mod.Launch.ServerConfig != "" {
		src := filepath.Join(lc.mod.Root, filepath.FromSlash(lc.mod.Launch.ServerConfig))
		cfgName = filepath.Base(src)
		dst := filepath.Join(lc.host.Paths.DayZServer, cfgName)

		if g.dryRun {
			p.Detail("would deploy %s -> %s (BattlEye=%v)", src, dst, lc.battlEye)
		} else if err := serverdz.Deploy(src, dst, lc.battlEye); err != nil {
			return coded(ExitPreflight, "%v", err)
		}
	}

	var args []string
	if lc.diag {
		// The diag executable is a client. -server is what makes it host.
		args = append(args, "-server")
	}
	args = append(args,
		"-config="+cfgName,
		"-port="+strconv.Itoa(lc.port),
		"-profiles="+lc.mod.Launch.Profile,
	)
	args = append(args, lc.mod.Launch.ServerArgs...)
	if ma := lc.mods.ServerArg(); ma != "" {
		args = append(args, ma)
	}
	// Server-side-only mods go here instead of in -mod=, so clients neither list
	// nor download them.
	if sm := lc.mods.ServerModArg(); sm != "" {
		args = append(args, sm)
	}

	// Always the server directory, even for the diag exe, so that its config,
	// mpmissions and @-mod junctions resolve like the retail server's do.
	c := proc.Cmd{Name: exe, Args: args, Dir: lc.host.Paths.DayZServer}
	if g.dryRun {
		p.Line("would run: %s", c)
		return nil
	}

	pid, err := proc.Spawn(c)
	if err != nil {
		return coded(ExitPreflight, "%v", err)
	}
	lc.serverPID = pid
	p.Line("server started (pid %d) on port %d, BattlEye %s", pid, lc.port, onOff(lc.battlEye))
	if ma := lc.mods.ServerArg(); ma != "" {
		p.Detail("mods: %s", ma)
	}
	if sm := lc.mods.ServerModArg(); sm != "" {
		p.Detail("server-only: %s", sm)
	}
	return nil
}

// --- client ---------------------------------------------------------------

func newClientCmd(g *global) *cobra.Command {
	l := &launchFlags{}
	var join bool

	cmd := &cobra.Command{
		Use:   "client",
		Short: "Start the game client",
		Long: "Starts the client with this mod loaded. With --join it connects straight to\n" +
			"the local server.\n\n" +
			"With BattlEye on the launch goes through DayZ_BE.exe: starting DayZ_x64.exe\n" +
			"directly makes BattlEye report \"Game Restart Required\" and refuse to run.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			lc, err := g.launchCtx(cmd, l, "dev")
			if err != nil {
				return err
			}
			return g.startClient(lc, join)
		},
	}
	l.bind(cmd)
	cmd.Flags().BoolVar(&join, "join", false, "connect to the local server after starting")
	return cmd
}

func (g *global) startClient(lc *launchCtx, join bool) error {
	p := g.printer()

	kind := dayz.ClientExeKind(lc.mod, lc.diag, lc.battlEye)
	exe := lc.host.ClientExe(string(kind))
	if exe == "" {
		return coded(ExitPreflight, "the DayZ client path is not configured; run `dayzmod config init`")
	}
	if !fileExists(exe) {
		return coded(ExitPreflight, "client executable not found: %s", exe)
	}

	var args []string
	if ma := lc.mods.ClientArg(); ma != "" {
		args = append(args, ma)
	}
	if join {
		args = append(args, "-connect=127.0.0.1", "-port="+strconv.Itoa(lc.port))
	}
	args = append(args, lc.mod.Launch.ClientArgs...)

	c := proc.Cmd{Name: exe, Args: args, Dir: lc.host.Paths.DayZClient}
	if g.dryRun {
		p.Line("would run: %s", c)
		return nil
	}

	pid, err := proc.Spawn(c)
	if err != nil {
		return coded(ExitPreflight, "%v", err)
	}
	p.Line("client started (pid %d) using %s", pid, filepath.Base(exe))
	if ma := lc.mods.ClientArg(); ma != "" {
		p.Detail("mods: %s", ma)
	}
	return nil
}

// --- dev ------------------------------------------------------------------

func newDevCmd(g *global) *cobra.Command {
	l := &launchFlags{}
	var noBuild, force bool

	cmd := &cobra.Command{
		Use:   "dev",
		Short: "Kill, build, start the server, wait for it, then connect",
		Long: "The full test loop. Waits for the server to actually bind its port rather\n" +
			"than sleeping for a fixed time, so it is as fast as the machine allows and\n" +
			"never connects too early.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			lc, err := g.launchCtx(cmd, l, "dev")
			if err != nil {
				return err
			}
			p := g.printer()

			p.Section("Stopping anything still running")
			if err := g.killAll(true, true); err != nil {
				return err
			}

			if !noBuild {
				if err := g.runBuild(cmd.Context(), lc, force); err != nil {
					return err
				}
			}

			p.Section("Starting the server")
			if err := g.startServer(lc); err != nil {
				return err
			}

			if !g.dryRun {
				if err := g.waitForServer(cmd.Context(), lc); err != nil {
					return err
				}
			}

			p.Section("Connecting")
			return g.startClient(lc, true)
		},
	}
	l.bind(cmd)
	cmd.Flags().BoolVar(&noBuild, "no-build", false, "skip the build step")
	cmd.Flags().BoolVar(&force, "force", false, "rebuild every addon")
	return cmd
}

func (g *global) runBuild(ctx context.Context, lc *launchCtx, force bool) error {
	p := g.printer()
	p.Section("Building")

	rep := &reporter{p: p, verbose: g.verbose}
	b := &pipeline.Builder{Mod: lc.mod, Host: lc.host, Runner: g.runner(rep), Report: rep}

	if _, err := b.Build(ctx, pipeline.Options{Channel: "dev", Force: force}); err != nil {
		return coded(ExitPacker, "%v", err)
	}
	return nil
}

func (g *global) waitForServer(ctx context.Context, lc *launchCtx) error {
	p := g.printer()
	timeout := lc.mod.Launch.BootTimeout.Duration()

	p.Line("  waiting for UDP %d (timeout %s)", lc.port, timeout)
	start := time.Now()

	err := netwait.ForUDPPort(ctx, lc.port, netwait.Options{
		Timeout: timeout,
		Abort:   lc.serverDied,
	})
	if err != nil {
		return coded(ExitPreflight, "%v", err)
	}
	p.Line("  server up after %s", time.Since(start).Round(100*time.Millisecond))
	return nil
}

// serverDied reports whether the server process has gone.
//
// A fatal startup error puts DayZ behind a modal dialog, and the process only
// exits once someone dismisses it. At that point there is no reason to keep
// waiting for a port that is never going to be bound.
func (lc *launchCtx) serverDied() (bool, string) {
	if lc.serverPID == 0 {
		return false, ""
	}
	if proc.Alive(lc.serverPID) {
		return false, ""
	}
	return true, "the server process exited before binding its port"
}

// --- wait / kill ----------------------------------------------------------

func newWaitCmd(g *global) *cobra.Command {
	var port int
	var timeout time.Duration

	cmd := &cobra.Command{
		Use:   "wait",
		Short: "Block until the server binds its port",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			mod, err := g.mod()
			if err != nil {
				return err
			}
			if port == 0 {
				port = mod.Launch.Port
			}
			if timeout == 0 {
				timeout = mod.Launch.BootTimeout.Duration()
			}

			if err := netwait.ForUDPPort(cmd.Context(), port, netwait.Options{Timeout: timeout}); err != nil {
				return coded(ExitPreflight, "%v", err)
			}
			g.printer().Line("UDP %d is bound", port)
			return nil
		},
	}
	cmd.Flags().IntVar(&port, "port", 0, "port to wait for")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "how long to wait")
	return cmd
}

func newKillCmd(g *global) *cobra.Command {
	var client, server, all bool

	cmd := &cobra.Command{
		Use:   "kill",
		Short: "Stop the running client and server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Bare `dayzmod kill` stops everything. That is what the old kill-all
			// task did, and what anyone typing it expects.
			if !client && !server {
				all = true
			}
			return g.killAll(client || all, server || all)
		},
	}
	f := cmd.Flags()
	f.BoolVar(&client, "client", false, "stop the client only")
	f.BoolVar(&server, "server", false, "stop the server only")
	f.BoolVar(&all, "all", false, "stop both")
	return cmd
}

// killAll stops the game processes. Nothing running counts as success.
func (g *global) killAll(client, server bool) error {
	p := g.printer()

	targets := map[string][]string{}
	if client {
		targets["client"] = []string{"DayZ_x64.exe", "DayZ_BE.exe", "DayZDiag_x64.exe"}
	}
	if server {
		targets["server"] = []string{"DayZServer_x64.exe"}
	}

	for _, role := range []string{"client", "server"} {
		names, ok := targets[role]
		if !ok {
			continue
		}
		total := 0
		for _, name := range names {
			if g.dryRun {
				p.Detail("would stop %s", name)
				continue
			}
			n, err := proc.KillByName(name)
			if err != nil {
				return coded(ExitPreflight, "%v", err)
			}
			total += n
		}
		if !g.dryRun {
			if total == 0 {
				p.Line("  %-8s nothing running", role)
			} else {
				p.Line("  %-8s stopped %d process(es)", role, total)
			}
		}
	}
	return nil
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
