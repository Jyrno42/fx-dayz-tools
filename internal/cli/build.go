package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/Jyrno42/fx-dayz-tools/internal/pipeline"
	"github.com/Jyrno42/fx-dayz-tools/internal/proc"
	"github.com/Jyrno42/fx-dayz-tools/internal/ui"
)

// reporter adapts the printer to what the pipeline needs.
type reporter struct {
	p       *ui.Printer
	verbose bool
}

func (r *reporter) Step(format string, args ...any)   { r.p.Line("  "+format, args...) }
func (r *reporter) Detail(format string, args ...any) { r.p.Detail(format, args...) }

func (r *reporter) Command(c proc.Cmd) {
	if r.verbose {
		r.p.Detail("%s", c)
	}
}

func newBuildCmd(g *global) *cobra.Command {
	var opts pipeline.Options

	cmd := &cobra.Command{
		Use:   "build",
		Short: "Pack changed addons and deploy them to the game installs",
		Long: "Hashes each addon, packs the ones whose content changed, and copies the PBOs\n" +
			"into the DayZ client and server installs.\n\n" +
			"An addon is only recorded as up to date once it has both packed AND deployed,\n" +
			"so an interrupted build never leaves the game installs stale.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			mod, err := g.mod()
			if err != nil {
				return err
			}
			host, err := g.host()
			if err != nil {
				return err
			}
			p := g.printer()

			rep := &reporter{p: p, verbose: g.verbose}
			runner := g.runner(rep)

			b := &pipeline.Builder{Mod: mod, Host: host, Runner: runner, Report: rep}

			p.Section(fmt.Sprintf("Building %s (%s)", mod.Mod.Name, opts.Channel))

			start := time.Now()
			outcomes, err := b.Build(cmd.Context(), opts)

			built, skipped := 0, 0
			for _, o := range outcomes {
				if o.Skipped {
					skipped++
					continue
				}
				built++
			}

			if err != nil {
				if built > 0 || skipped > 0 {
					p.Line("")
					p.Line("%d built, %d unchanged before the failure", built, skipped)
				}
				return coded(ExitPacker, "%v", err)
			}

			p.Line("")
			switch {
			case len(outcomes) == 0:
				p.Line("Nothing to build: no addon matched.")
			case built == 0:
				p.Line("Everything up to date (%d addon(s)).", skipped)
			default:
				p.Line("Built %d addon(s), %d unchanged, in %s.", built, skipped, time.Since(start).Round(time.Millisecond))
				for _, o := range outcomes {
					if o.Skipped {
						continue
					}
					for _, d := range o.Deployed {
						p.Detail("%s -> %s", o.Addon, d)
					}
				}
			}
			return nil
		},
	}

	f := cmd.Flags()
	f.StringVar(&opts.Channel, "channel", "dev", "channel to build")
	f.BoolVar(&opts.Force, "force", false, "rebuild even when the content hash is unchanged")
	f.StringVar(&opts.Set, "set", "", "only this addon set")
	f.StringVar(&opts.Addon, "addon", "", "only this addon")
	f.BoolVar(&opts.SkipHooks, "no-hooks", false, "skip the configured pre-build hooks")
	f.BoolVar(&opts.SkipDeploy, "no-deploy", false, "pack without copying into the game installs")
	return cmd
}

// runner returns the real or dry-run command runner.
func (g *global) runner(rep *reporter) proc.Runner {
	if g.dryRun {
		return &proc.Dry{OnRecord: func(c proc.Cmd) { rep.p.Detail("would run: %s", c) }}
	}
	return &proc.Exec{OnStart: rep.Command}
}
