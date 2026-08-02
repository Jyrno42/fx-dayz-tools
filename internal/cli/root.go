// Package cli defines the dayzmod command surface. Commands parse flags,
// resolve configuration and delegate. The actual logic lives in the internal
// packages.
package cli

import (
	"errors"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/spf13/cobra"

	"github.com/Jyrno42/fx-dayz-tools/internal/machine"
	"github.com/Jyrno42/fx-dayz-tools/internal/modcfg"
	"github.com/Jyrno42/fx-dayz-tools/internal/ui"
)

// Exit codes. CI depends on these, so keep them stable and specific.
const (
	ExitOK        = 0
	ExitInternal  = 1
	ExitConfig    = 2 // dayz.yml or machine config invalid, or tool too old
	ExitPreflight = 3 // missing tool, P: not mounted, LFS pointer stub
	ExitScript    = 4 // scriptcheck found Enforce errors
	ExitPacker    = 5 // the packer failed
	ExitSigning   = 6 // signing failed
)

// CodedError carries an explicit exit code.
type CodedError struct {
	Code int
	Err  error
}

func (e *CodedError) Error() string { return e.Err.Error() }
func (e *CodedError) Unwrap() error { return e.Err }

func coded(code int, format string, args ...any) error {
	return &CodedError{Code: code, Err: fmt.Errorf(format, args...)}
}

// global holds flags shared by every command.
type global struct {
	machineConfig string
	dir           string
	verbose       bool
	dryRun        bool

	print *ui.Printer
}

func (g *global) printer() *ui.Printer {
	if g.print == nil {
		g.print = ui.New()
		g.print.Verbose = g.verbose
	}
	return g.print
}

// mod loads the mod manifest for the working directory.
func (g *global) mod() (*modcfg.Config, error) {
	dir := g.dir
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		dir = wd
	}
	cfg, err := modcfg.LoadFrom(dir)
	if err != nil {
		return nil, &CodedError{Code: ExitConfig, Err: err}
	}
	return cfg, nil
}

// host loads the machine configuration.
func (g *global) host() (*machine.Config, error) {
	cfg, err := machine.Load(g.machineConfig)
	if err != nil {
		code := ExitConfig
		if errors.Is(err, machine.ErrNotConfigured) {
			code = ExitPreflight
		}
		return nil, &CodedError{Code: code, Err: err}
	}
	return cfg, nil
}

// Execute runs the CLI and returns a process exit code.
func Execute() int {
	g := &global{}
	root := newRoot(g)

	if err := root.Execute(); err != nil {
		var c *CodedError
		if errors.As(err, &c) {
			fmt.Fprintf(os.Stderr, "dayzmod: %v\n", c.Err)
			return c.Code
		}
		// cobra has already reported usage errors.
		return ExitInternal
	}
	return ExitOK
}

func newRoot(g *global) *cobra.Command {
	root := &cobra.Command{
		Use:   "dayzmod",
		Short: "Build, run and release DayZ mods",
		Long: "dayzmod builds, runs and releases DayZ mods.\n\n" +
			"Per-mod settings live in dayz.yml at the repo root.\n" +
			"Machine paths live in the user config; run `dayzmod config init` once, then `dayzmod doctor`.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version(),
	}

	pf := root.PersistentFlags()
	pf.StringVar(&g.machineConfig, "machine-config", "", "path to the machine config (default: user config dir)")
	pf.StringVarP(&g.dir, "dir", "C", "", "run as if started in this directory")
	pf.BoolVarP(&g.verbose, "verbose", "v", false, "verbose output")
	pf.BoolVar(&g.dryRun, "dry-run", false, "print the external commands that would run, then exit")

	root.AddCommand(
		newInitCmd(g),
		newConfigCmd(g),
		newDoctorCmd(g),
		newPDriveCmd(g),
		newBuildCmd(g),
		newServerCmd(g),
		newClientCmd(g),
		newDevCmd(g),
		newWaitCmd(g),
		newKillCmd(g),
		newReleaseCmd(g),
		newScriptCheckCmd(g),
		newHooksCmd(g),
		newHashCmd(g),
		newLintCmd(g),
	)
	return root
}

// version reports the module version recorded at install time, so
// `go install ...@latest` gives you a real version without any ldflags.
func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" {
		return "(devel)"
	}
	return info.Main.Version
}
