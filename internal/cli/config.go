package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/Jyrno42/fx-dayz-tools/internal/machine"
	"github.com/Jyrno42/fx-dayz-tools/internal/ui"
)

func newConfigCmd(g *global) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage the machine configuration",
	}
	cmd.AddCommand(newConfigInitCmd(g), newConfigShowCmd(g))
	return cmd
}

func newConfigInitCmd(g *global) *cobra.Command {
	var force, printOnly bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Discover this machine's DayZ toolchain and write the config",
		Long: "Probes the registry and Steam's library index for DayZ, DayZ Server, DayZ Tools,\n" +
			"Mikero DePboTools, detects the P: work drive, and registers every\n" +
			"signing key found in the keys directory.\n\n" +
			"Anything it cannot find is left empty and reported; `dayzmod doctor` names the\n" +
			"exact key to fill in.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p := g.printer()

			path := g.machineConfig
			if path == "" {
				var err error
				if path, err = machine.DefaultPath(); err != nil {
					return coded(ExitInternal, "%v", err)
				}
			}

			if !force && !printOnly {
				if _, err := os.Stat(path); err == nil {
					return coded(ExitConfig,
						"%s already exists; pass --force to overwrite it or --print to preview what would be discovered", path)
				}
			}

			p.Section("Discovering the DayZ toolchain")
			d := machine.Discover()
			for _, note := range d.Notes {
				p.Check(ui.OK, note, "")
			}
			for _, miss := range d.Missing {
				p.Check(ui.Warn, miss, "")
			}

			if printOnly {
				p.Section("Config that would be written")
				return writeYAMLTo(cmd.OutOrStdout(), d.Config)
			}

			if err := d.Config.Save(path); err != nil {
				return coded(ExitInternal, "%v", err)
			}

			p.Line("\nWrote %s", path)
			if len(d.Missing) > 0 {
				p.Line("\n%d item(s) could not be discovered. Edit the file, then run `dayzmod doctor`.", len(d.Missing))
			} else {
				p.Line("\nRun `dayzmod doctor` to verify.")
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing config")
	cmd.Flags().BoolVar(&printOnly, "print", false, "print what would be discovered without writing")
	return cmd
}

func newConfigShowCmd(g *global) *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print the active machine configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := g.host()
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "# %s\n", cfg.Path)
			return writeYAMLTo(cmd.OutOrStdout(), cfg)
		},
	}
}
