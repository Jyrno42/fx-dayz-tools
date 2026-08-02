package cli

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/Jyrno42/fx-dayz-tools/internal/hashing"
	"github.com/Jyrno42/fx-dayz-tools/internal/modcfg"
)

func newHashCmd(g *global) *cobra.Command {
	var addonFilter, setFilter string

	cmd := &cobra.Command{
		Use:   "hash",
		Short: "Print the content hash of each addon",
		Long: "Shows the digest used for build change detection, alongside the value recorded\n" +
			"in the lockfile. Useful for understanding why a build did or did not rebuild.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := g.mod()
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()

			lf, err := hashing.LoadLockfile(filepath.Join(cfg.Root, cfg.Repo.Lockfile))
			if err != nil {
				return coded(ExitConfig, "%v", err)
			}

			opts := hashing.Options{Skip: hashing.SkipNames(cfg.Repo.BuildlogDir)}

			for _, sname := range cfg.AddonSetNames() {
				if setFilter != "" && sname != setFilter {
					continue
				}
				set := cfg.AddonSets[sname]
				for _, aname := range set.AddonNames() {
					if addonFilter != "" && aname != addonFilter {
						continue
					}
					dir := filepath.Join(cfg.Root, filepath.FromSlash(set.Source), aname)
					sum, err := hashing.DirHash(dir, opts)
					if err != nil {
						return coded(ExitPreflight, "%v", err)
					}

					key := hashing.Key(sname, aname)
					state := "changed"
					if lf.Fresh(key, sum) {
						state = "up to date"
					} else if _, seen := lf.Get(key); !seen {
						state = "never built"
					}
					fmt.Fprintf(out, "%-40s %s  %s\n", key, sum, state)
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&addonFilter, "addon", "", "only this addon")
	cmd.Flags().StringVar(&setFilter, "set", "", "only this addon set")
	return cmd
}

func newLintCmd(g *global) *cobra.Command {
	return &cobra.Command{
		Use:   "lint",
		Short: "Validate dayz.yml without needing the DayZ tools",
		Long: "Parses and validates the mod manifest. Touches no external tool and no game\n" +
			"install, so it is safe to run in CI or a pre-commit hook.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dir := g.dir
			if dir == "" {
				dir = "."
			}
			path, err := modcfg.Find(dir)
			if err != nil {
				return coded(ExitConfig, "%v", err)
			}
			cfg, err := modcfg.Load(path)
			if err != nil {
				return coded(ExitConfig, "%v", err)
			}

			p := g.printer()
			p.Line("%s is valid", path)
			p.Line("  mod       %s (%s)", cfg.Mod.Name, cfg.Mod.Visibility)
			for _, sname := range cfg.AddonSetNames() {
				set := cfg.AddonSets[sname]
				p.Line("  set %-9s %s -> %s: %v", sname, set.Source, set.ModName, set.AddonNames())
			}
			for _, cname := range cfg.ChannelNames() {
				ch := cfg.Channels[cname]
				p.Line("  channel %-5s packer=%s change_detection=%s sign=%v", cname, ch.Packer, ch.ChangeDetection, ch.Sign.Enabled)
			}
			return nil
		},
	}
}
