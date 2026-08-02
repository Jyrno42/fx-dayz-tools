package cli

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/Jyrno42/fx-dayz-tools/internal/modcfg"
	"github.com/Jyrno42/fx-dayz-tools/internal/netwait"
	"github.com/Jyrno42/fx-dayz-tools/internal/scriptlog"
	"github.com/Jyrno42/fx-dayz-tools/internal/ui"
)

func newScriptCheckCmd(g *global) *cobra.Command {
	l := &launchFlags{}
	var (
		settle     time.Duration
		keepServer bool
		noBuild    bool
		force      bool
		logPath    string
		showMods   bool
	)

	cmd := &cobra.Command{
		Use:   "scriptcheck",
		Short: "Boot the server headless and fail on Enforce compile errors",
		Long: "Enforce is compiled when the server starts, so booting it is the only way to\n" +
			"find out whether the scripts compile. This builds, boots the server without a\n" +
			"client, scans the script log, and exits 4 if anything failed to compile.\n\n" +
			"Safe to use as a pre-commit or CI gate.\n\n" +
			"With --log it scans an existing log instead of booting anything.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			mod, err := g.mod()
			if err != nil {
				return err
			}
			p := g.printer()

			opts := scriptlog.Options{
				ExtraErrorPatterns: mod.ScriptCheck.ExtraErrorPatterns,
				ExtraNoisePatterns: mod.ScriptCheck.ExtraNoisePatterns,
			}

			// --log skips the boot entirely and just reads a log.
			if logPath != "" {
				rep, err := scriptlog.ScanFile(logPath, opts)
				if err != nil {
					return coded(ExitPreflight, "%v", err)
				}
				return g.reportScriptLog(rep, mod.ScriptCheck.MaxHitsShown, showMods)
			}

			lc, err := g.launchCtx(cmd, l, "dev")
			if err != nil {
				return err
			}

			p.Section("Stopping anything still running")
			if err := g.killAll(true, true); err != nil {
				return err
			}

			if !noBuild {
				if err := g.runBuild(cmd.Context(), lc, force); err != nil {
					return err
				}
			}

			p.Section("Booting the server")
			// Anything the server writes from here on belongs to this run.
			started := time.Now()
			if err := g.startServer(lc); err != nil {
				return err
			}
			if !keepServer {
				defer func() {
					if err := g.killAll(false, true); err != nil {
						p.Warn("could not stop the server: %v", err)
					}
				}()
			}

			if g.dryRun {
				return nil
			}

			profileDir := serverProfileDir(lc)

			// A fatal startup error puts DayZ behind a modal dialog and the
			// process only exits once someone dismisses it, so it may never
			// bind the port at all. Give up the moment the process goes.
			if err := g.waitForServer(cmd.Context(), lc); err != nil {
				var aborted *netwait.AbortedError
				if !errors.As(err, &aborted) {
					return err
				}
				p.Line("  %v", aborted)
				return g.scanAfterFailure(profileDir, started, opts, mod, showMods)
			}

			if settle == 0 {
				settle = mod.ScriptCheck.Settle.Duration()
			}
			rep, err := g.watchLog(cmd.Context(), lc, profileDir, started, settle, opts)
			if err != nil {
				return err
			}
			return g.reportScriptLog(rep, mod.ScriptCheck.MaxHitsShown, showMods)
		},
	}

	l.bind(cmd)
	f := cmd.Flags()
	f.DurationVar(&settle, "settle", 0, "how long to wait after the port binds before reading the log")
	f.BoolVar(&keepServer, "keep-server", false, "leave the server running afterwards")
	f.BoolVar(&noBuild, "no-build", false, "skip the build step")
	f.BoolVar(&force, "force", false, "rebuild every addon")
	f.StringVar(&logPath, "log", "", "scan this log instead of booting the server")
	f.BoolVar(&showMods, "modules", false, "list the compiled script modules")
	return cmd
}

// watchLog tails the script log while the scripts compile and returns as soon as
// it has an answer, instead of always sitting out the full settle.
//
// Three things stop it early: an error shows up in the log, the server process
// exits, or the settle elapses. The first two are what make this quick in the
// cases you actually care about, since a compile failure is usually known within
// a second or two of the log appearing.
func (g *global) watchLog(
	ctx context.Context,
	lc *launchCtx,
	profileDir string,
	started time.Time,
	settle time.Duration,
	opts scriptlog.Options,
) (scriptlog.Report, error) {
	p := g.printer()
	p.Line("  watching the script log (up to %s)", settle)

	const poll = 250 * time.Millisecond
	deadline := time.Now().Add(settle)

	var (
		tailer *scriptlog.Tailer
		acc    scriptlog.Accumulator
	)

	for {
		// The log shows up a moment after the server starts, so keep looking for
		// it until it does.
		if tailer == nil {
			if path, err := scriptlog.FindNewest(profileDir, started); err == nil {
				tailer = scriptlog.NewTailer(path, opts)
			}
		}
		if tailer != nil {
			rep, err := tailer.Poll()
			if err != nil {
				return acc.Report, coded(ExitPreflight, "%v", err)
			}
			acc.Add(rep)

			if len(acc.Report.Hits) > 0 {
				p.Line("  errors appeared after %s; not waiting out the rest", time.Since(started).Round(time.Millisecond))
				return acc.Report, nil
			}
		}

		if died, reason := lc.serverDied(); died {
			p.Line("  %s after %s", reason, time.Since(started).Round(time.Millisecond))
			// Drain whatever it managed to write on the way out.
			if tailer != nil {
				if rep, err := tailer.Poll(); err == nil {
					acc.Add(rep)
				}
			}
			if acc.Report.Path == "" {
				return acc.Report, coded(ExitScript,
					"the server exited before writing a script log; check %s for a crash report", profileDir)
			}
			return acc.Report, nil
		}

		if time.Now().After(deadline) {
			if tailer == nil {
				return acc.Report, coded(ExitPreflight,
					"the server bound its port but wrote no script log within %s (looked in %s)", settle, profileDir)
			}
			return acc.Report, nil
		}

		select {
		case <-ctx.Done():
			return acc.Report, ctx.Err()
		case <-time.After(poll):
		}
	}
}

// scanAfterFailure reports whatever the server managed to log before dying.
func (g *global) scanAfterFailure(
	profileDir string,
	started time.Time,
	opts scriptlog.Options,
	mod *modcfg.Config,
	showMods bool,
) error {
	logFile, err := scriptlog.FindNewest(profileDir, started)
	if err != nil {
		// No log at all means the failure happened before script compilation, so
		// point at where the real explanation lives.
		return coded(ExitScript,
			"the server exited without writing a script log, so it failed before compiling any scripts; "+
				"check the RPT and crash logs in %s", profileDir)
	}
	rep, err := scriptlog.ScanFile(logFile, opts)
	if err != nil {
		return coded(ExitPreflight, "%v", err)
	}
	if rep.OK() {
		// It died, but not from a script error, so do not report a pass.
		return coded(ExitScript,
			"the server exited early but its script log shows no Enforce errors; "+
				"check the RPT and crash logs in %s", profileDir)
	}
	return g.reportScriptLog(rep, mod.ScriptCheck.MaxHitsShown, showMods)
}

// reportScriptLog prints the outcome and returns the exit-coded error.
func (g *global) reportScriptLog(rep scriptlog.Report, maxHits int, showMods bool) error {
	p := g.printer()

	if showMods {
		p.Section("Compiled modules")
		for _, m := range rep.Modules {
			p.Line("  %s", m.Summary)
		}
	}

	p.Section("Enforce script check")
	p.Check(ui.Info, "log", rep.Path)
	p.Check(ui.Info, "lines scanned", plural(rep.Lines, "line"))
	p.Check(ui.Info, "modules compiled", plural(len(rep.Modules), "module"))

	if rep.OK() {
		p.Check(ui.OK, "errors", "none")
		return nil
	}

	p.Check(ui.Fail, "errors", plural(len(rep.Hits), "line"))
	p.Line("")
	if maxHits <= 0 {
		maxHits = 40
	}
	for i, h := range rep.Hits {
		if i == maxHits {
			p.Line("  ... and %d more", len(rep.Hits)-maxHits)
			break
		}
		p.Line("  %6d  %s", h.Line, h.Text)
	}

	// Its own exit code, so a CI step can tell a compile failure apart from a
	// missing tool or a bad config.
	return coded(ExitScript, "%d Enforce error line(s)", len(rep.Hits))
}

// serverProfileDir is where the server writes its logs.
func serverProfileDir(lc *launchCtx) string {
	return filepath.Join(lc.host.Paths.DayZServer, lc.mod.Launch.Profile)
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}
