package cli

import (
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Jyrno42/fx-dayz-tools/internal/modcfg"
	"github.com/Jyrno42/fx-dayz-tools/internal/pipeline"
)

func newHooksCmd(g *global) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hooks",
		Short: "Run the repo's own commands declared in dayz.yml",
		Long: "Hooks are the mod-specific commands a repo needs around a build -- a code\n" +
			"generator, a linter, a test suite. The tool knows nothing about what they do\n" +
			"beyond how to invoke them and whether they succeeded, which keeps repo-specific\n" +
			"tooling out of the shared binary.",
	}
	cmd.AddCommand(newHooksRunCmd(g), newHooksListCmd(g))
	return cmd
}

// hookStages maps a stage name to its list.
func hookStages(cfg *modcfg.Config) map[string][]modcfg.Hook {
	return map[string][]modcfg.Hook{
		"pre_build":   cfg.Hooks.PreBuild,
		"pre_release": cfg.Hooks.PreRelease,
		"check":       cfg.Hooks.Check,
	}
}

func stageNames(stages map[string][]modcfg.Hook) []string {
	out := make([]string, 0, len(stages))
	for name, hooks := range stages {
		if len(hooks) > 0 {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func newHooksRunCmd(g *global) *cobra.Command {
	return &cobra.Command{
		Use:   "run <stage>",
		Short: "Run every hook in a stage",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := g.mod()
			if err != nil {
				return err
			}
			stages := hookStages(cfg)

			hooks, ok := stages[args[0]]
			if !ok {
				return coded(ExitConfig, "unknown hook stage %q; valid stages are pre_build, pre_release and check", args[0])
			}
			if len(hooks) == 0 {
				return coded(ExitConfig, "no hooks defined under hooks.%s in %s (defined: %s)",
					args[0], modcfg.FileName, strings.Join(stageNames(stages), ", "))
			}

			p := g.printer()
			rep := &reporter{p: p, verbose: g.verbose}

			p.Section("Running " + args[0] + " hooks")
			if err := pipeline.RunHooks(cmd.Context(), cfg.Root, g.runner(rep), rep, hooks); err != nil {
				return coded(ExitPacker, "%v", err)
			}
			p.Line("")
			p.Line("%s passed.", args[0])
			return nil
		},
	}
}

func newHooksListCmd(g *global) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the hooks declared in dayz.yml",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := g.mod()
			if err != nil {
				return err
			}
			p := g.printer()
			stages := hookStages(cfg)

			names := stageNames(stages)
			if len(names) == 0 {
				p.Line("No hooks defined in %s.", modcfg.FileName)
				return nil
			}
			for _, stage := range names {
				p.Section(stage)
				for _, h := range stages[stage] {
					p.Line("  %-16s %s", h.Name, strings.Join(h.Run, " "))
				}
			}
			return nil
		},
	}
}
