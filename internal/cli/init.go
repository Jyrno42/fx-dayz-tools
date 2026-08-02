package cli

import (
	"errors"
	"os"

	"github.com/spf13/cobra"

	"github.com/Jyrno42/fx-dayz-tools/internal/machine"
	"github.com/Jyrno42/fx-dayz-tools/internal/scaffold"
	"github.com/Jyrno42/fx-dayz-tools/internal/ui"
)

func newInitCmd(g *global) *cobra.Command {
	var (
		spec       scaffold.Spec
		force      bool
		sync       bool
		serverOnly bool
	)

	cmd := &cobra.Command{
		Use:   "init [dir]",
		Short: "Scaffold a new mod repo, or refresh the files the tool owns",
		Long: "Generates dayz.yml, a thin Taskfile, a dev server config, the AddonBuilder\n" +
			"allowlist, git attributes and a minimal addon with its script directories.\n\n" +
			"Whether the repo needs a junction into the work drive is worked out from where\n" +
			"you point it: inside the work drive it mirrors its position, outside it gets a\n" +
			"pdrive_link_from that `dayzmod pdrive ensure` will create.\n\n" +
			"Existing files are never overwritten without --force.\n\n" +
			"--sync refreshes only the tool-owned files (.buildfiles, .gitattributes,\n" +
			".gitignore) in an existing repo, leaving dayz.yml and the Taskfile alone.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p := g.printer()

			spec.Dir = "."
			if len(args) == 1 {
				spec.Dir = args[0]
			} else if g.dir != "" {
				spec.Dir = g.dir
			}
			spec.ServerOnly = serverOnly

			// The machine config supplies the work-drive layout. Without it we can
			// still scaffold, just falling back on the usual defaults.
			letter, backing := "P:", ""
			if host, err := machine.Load(g.machineConfig); err == nil {
				letter, backing = host.PDrive.Letter, host.PDrive.Backing
			} else if !errors.Is(err, machine.ErrNotConfigured) {
				return coded(ExitConfig, "%v", err)
			} else {
				p.Warn("no machine config yet; assuming the work drive is %s. Run `dayzmod config init`.", letter)
			}

			derived, err := scaffold.Derive(spec, letter, backing)
			if err != nil {
				return coded(ExitConfig, "%v", err)
			}

			files, err := scaffold.Plan(derived)
			if err != nil {
				return coded(ExitConfig, "%v", err)
			}

			if g.dryRun {
				p.Section("Would create")
				for _, f := range files {
					if sync && !f.Owned {
						continue
					}
					p.Line("  %s", f.Path)
				}
				return nil
			}

			if err := os.MkdirAll(derived.Dir, 0o755); err != nil {
				return coded(ExitPreflight, "%v", err)
			}

			written, skipped, err := scaffold.Write(derived, files, force, sync)
			if err != nil {
				return coded(ExitPreflight, "%v", err)
			}

			p.Section("Scaffolded " + derived.Name)
			p.Check(ui.Info, "directory", derived.Dir)
			p.Check(ui.Info, "addon", derived.Addon)
			p.Check(ui.Info, "prefix root", derived.PDrivePath)
			if derived.ServerOnly {
				p.Check(ui.Info, "loading", "-serverMod= (never sent to clients)")
			}

			p.Line("")
			for _, f := range written {
				p.Line("  created  %s", f)
			}
			for _, f := range skipped {
				p.Line("  kept     %s (already exists; --force to replace)", f)
			}

			if len(written) == 0 {
				p.Line("")
				p.Line("Nothing to do. Use --force to regenerate, or --sync to refresh the tool-owned files.")
				return nil
			}

			p.Line("")
			p.Line("Next:")
			if derived.PDriveLinkFrom != "" {
				p.Line("  dayzmod pdrive ensure    link the repo into the work drive")
			}
			p.Line("  dayzmod doctor           check the setup")
			p.Line("  dayzmod build            pack and deploy")
			return nil
		},
	}

	f := cmd.Flags()
	f.StringVar(&spec.ID, "id", "", "mod slug (default: the directory name)")
	f.StringVar(&spec.Name, "name", "", "mod folder name including @ (default: @<id>)")
	f.StringVar(&spec.Addon, "addon", "", "PBO name (default: derived from the slug)")
	f.BoolVar(&serverOnly, "server-only", false, "load through -serverMod= and deploy only to the server")
	f.BoolVar(&force, "force", false, "overwrite existing files")
	f.BoolVar(&sync, "sync", false, "refresh only the tool-owned files in an existing repo")
	return cmd
}
