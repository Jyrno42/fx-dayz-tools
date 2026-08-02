package cli

import (
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Jyrno42/fx-dayz-tools/internal/paths"
)

func newPDriveCmd(g *global) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pdrive",
		Short: "Manage the P: work drive and repo junctions",
		Long: "The DayZ tools only see the work drive, so every repo must be reachable\n" +
			"beneath it. A subst mapping does not survive a reboot, and a repo kept outside\n" +
			"that tree needs a junction into it -- both of which used to be hand-written\n" +
			"prerequisites in each Taskfile.",
	}
	cmd.AddCommand(newPDriveStatusCmd(g), newPDriveMountCmd(g), newPDriveUnmountCmd(g), newPDriveEnsureCmd(g))
	return cmd
}

func newPDriveStatusCmd(g *global) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Report how the work drive is mounted",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			host, err := g.host()
			if err != nil {
				return err
			}
			drive, err := paths.LookupDrive(host.PDrive.Letter)
			if err != nil {
				return coded(ExitInternal, "%v", err)
			}
			g.printer().Line("%s", drive.Describe())
			if !drive.Mounted() {
				return coded(ExitPreflight, "%s is not mounted", drive.Letter)
			}
			return nil
		},
	}
}

func newPDriveMountCmd(g *global) *cobra.Command {
	return &cobra.Command{
		Use:   "mount",
		Short: "Map the work drive onto its backing directory",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			host, err := g.host()
			if err != nil {
				return err
			}
			p := g.printer()

			if host.PDrive.Backing == "" {
				return coded(ExitConfig, "pdrive.backing is not set in %s", host.Path)
			}

			drive, err := paths.LookupDrive(host.PDrive.Letter)
			if err != nil {
				return coded(ExitInternal, "%v", err)
			}
			if drive.Mounted() {
				p.Line("%s", drive.Describe())
				return nil
			}

			if g.dryRun {
				p.Line("would run: subst %s %s", host.PDrive.Letter, host.PDrive.Backing)
				return nil
			}
			if err := paths.MountSubst(host.PDrive.Letter, host.PDrive.Backing); err != nil {
				return coded(ExitPreflight, "%v", err)
			}
			p.Line("%s -> %s", host.PDrive.Letter, host.PDrive.Backing)
			return nil
		},
	}
}

func newPDriveUnmountCmd(g *global) *cobra.Command {
	return &cobra.Command{
		Use:   "unmount",
		Short: "Remove the work drive mapping",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			host, err := g.host()
			if err != nil {
				return err
			}
			if g.dryRun {
				g.printer().Line("would run: subst %s /d", host.PDrive.Letter)
				return nil
			}
			if err := paths.UnmountSubst(host.PDrive.Letter); err != nil {
				return coded(ExitPreflight, "%v", err)
			}
			g.printer().Line("%s unmounted", host.PDrive.Letter)
			return nil
		},
	}
}

func newPDriveEnsureCmd(g *global) *cobra.Command {
	return &cobra.Command{
		Use:   "ensure",
		Short: "Make this repo reachable under the work drive",
		Long: "Creates the junction declared by repo.pdrive_link_from, or verifies the repo is\n" +
			"already in place. This replaces the one-time `mklink /J` step that each repo\n" +
			"documented in a comment and that nothing enforced.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := g.mod()
			if err != nil {
				return err
			}
			host, err := g.host()
			if err != nil {
				return err
			}
			p := g.printer()

			want := host.RepoPDrivePath(cfg.Repo.PDrivePath)

			if fi, statErr := os.Stat(want); statErr == nil && fi.IsDir() {
				target, isLink, lerr := paths.LinkTarget(want)
				if lerr != nil {
					return coded(ExitPreflight, "%v", lerr)
				}
				switch {
				case isLink && cfg.Repo.PDriveLinkFrom != "" && !paths.Equal(target, cfg.Repo.PDriveLinkFrom):
					return coded(ExitPreflight,
						"%s already exists and points at %s, but dayz.yml expects %s; remove the stale junction first",
						want, target, cfg.Repo.PDriveLinkFrom)
				case isLink:
					p.Line("%s -> %s (already linked)", want, target)
				default:
					p.Line("%s (already in place)", want)
				}
				return nil
			}

			if cfg.Repo.PDriveLinkFrom == "" {
				return coded(ExitConfig,
					"%s does not exist and dayz.yml sets no repo.pdrive_link_from, so there is nothing to link from", want)
			}
			if !dirExists(cfg.Repo.PDriveLinkFrom) {
				return coded(ExitConfig, "repo.pdrive_link_from %s does not exist", cfg.Repo.PDriveLinkFrom)
			}

			if g.dryRun {
				p.Line("would run: mklink /J %s %s", want, cfg.Repo.PDriveLinkFrom)
				return nil
			}
			if err := os.MkdirAll(filepath.Dir(want), 0o755); err != nil {
				return coded(ExitPreflight, "%v", err)
			}
			if err := paths.CreateJunction(want, cfg.Repo.PDriveLinkFrom); err != nil {
				return coded(ExitPreflight, "%v", err)
			}
			p.Line("%s -> %s", want, cfg.Repo.PDriveLinkFrom)
			return nil
		},
	}
}
