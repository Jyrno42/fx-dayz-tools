package cli

import (
	"github.com/spf13/cobra"

	"github.com/Jyrno42/fx-dayz-tools/internal/pipeline"
	"github.com/Jyrno42/fx-dayz-tools/internal/ui"
)

func newReleaseCmd(g *global) *cobra.Command {
	var opts pipeline.ReleaseOptions

	cmd := &cobra.Command{
		Use:   "release",
		Short: "Pack, sign and assemble the shippable payloads",
		Long: "Runs the release channel end to end: pre-release hooks, a clean pack of every\n" +
			"addon, signing, the payload split, archives and a SHA-256 manifest.\n\n" +
			"The staging directory is always cleared first. A release that reused whatever\n" +
			"happened to be lying around is exactly how a stale PBO ships by accident.\n\n" +
			"For a private mod the public key is removed from the payload and its absence\n" +
			"is then asserted, because publishing it would let anyone whitelist a repack.",
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
			b := &pipeline.Builder{Mod: mod, Host: host, Runner: g.runner(rep), Report: rep}

			p.Section("Releasing " + mod.Mod.Name + " (" + opts.Channel + ")")

			res, err := b.Release(cmd.Context(), opts)
			if err != nil {
				return coded(ExitPacker, "%v", err)
			}

			p.Section("Release")
			p.Check(ui.Info, "staged", res.StageDir)
			if res.Included > 0 {
				p.Check(ui.Info, "included", plural(res.Included, "prebuilt PBO")+" (original signatures kept)")
			}
			if res.Signed {
				p.Check(ui.OK, "signed", "yes")
			} else {
				p.Check(ui.Warn, "signed", "no")
			}
			for _, pl := range res.Payloads {
				detail := pl.Dir
				if pl.Zip != "" {
					detail = pl.Zip
				}
				p.Check(ui.Info, "payload "+pl.Name, detail)
				p.Detail("%v", pl.Addons)
			}
			if res.Manifest != "" {
				p.Check(ui.Info, "manifest", res.Manifest)
			}
			return nil
		},
	}

	f := cmd.Flags()
	f.StringVar(&opts.Channel, "channel", "release", "channel to release")
	f.StringVar(&opts.Version, "version", "", "version stamped into archive names and the manifest")
	f.BoolVar(&opts.SkipHooks, "no-hooks", false, "skip the pre-release hooks")
	f.BoolVar(&opts.SkipSign, "no-sign", false, "pack without signing (not shippable)")
	f.BoolVar(&opts.NoZip, "no-zip", false, "stage the payloads without archiving them")
	f.StringSliceVar(&opts.Payloads, "payload", nil, "only these payloads")
	f.BoolVar(&opts.SkipObfuscation, "skip-obfuscation", false, "force every addon plain")
	return cmd
}
