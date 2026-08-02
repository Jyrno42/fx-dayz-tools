// Package packer turns an addon source directory into a PBO.
//
// Argv is deliberately pure and kept separate from Pack. Command-line
// construction is the part that actually breaks, and keeping it free of I/O
// makes every invocation golden-testable on a machine with no DayZ tools.
package packer

import (
	"context"

	"github.com/Jyrno42/fx-dayz-tools/internal/proc"
)

// Side decides which release payloads an addon lands in.
type Side string

// Job is one addon to pack.
type Job struct {
	// AddonName is the PBO name, e.g. PWE_Core.
	AddonName string
	// SourceDir is the addon directory as the DayZ tools need to see it, i.e.
	// under the work drive.
	SourceDir string
	// OutDir is where the PBO is written.
	OutDir string
	// Prefix is the path space inside the PBO, e.g.
	// projects\project-with-everything\mod\PWE_Core.
	Prefix string

	// Binarize converts models and content to binary form. On by default, since
	// model.cfg only gets applied when binarising.
	Binarize bool
	// Obfuscate is honoured only by packers whose Caps report CanObfuscate.
	Obfuscate bool
	// NoScramble lists files to leave unscrambled inside an obfuscated PBO.
	NoScramble []string

	// IncludeFile is AddonBuilder's -include allowlist (.buildfiles).
	IncludeFile string
	// Excludes are pboProject's -X patterns.
	Excludes []string

	// SignKey is a .biprivatekey path, or empty to leave the PBO unsigned.
	SignKey string
	// Engine is pboProject's -e value.
	Engine string
	// Label overrides the PBO name when it differs from the folder name.
	Label string

	// PboProject carries the Mikero-specific options. Every one gets emitted as
	// an explicit flag, because an omitted option is inherited from the GUI
	// registry instead of defaulted.
	PboProject Options
	// WritePrefixFile materialises $PBOPREFIX$ into the source directory for the
	// duration of the pack.
	WritePrefixFile bool
}

// Caps describes what a packer can do, and what the caller has to look after.
type Caps struct {
	CanObfuscate  bool
	CanBinarize   bool
	CanSignInline bool
	// NeedsPDrive means source paths have to be under the work drive.
	NeedsPDrive bool
	// SelfCleansTemp is false for AddonBuilder, which never cleans its sync
	// directory and leaves that to the caller.
	SelfCleansTemp bool
	// StickyOptions means unspecified options get inherited from persisted
	// state, so every invocation has to state every option explicitly.
	StickyOptions bool
}

// Packer builds PBOs.
type Packer interface {
	// Name identifies the backend.
	Name() string
	// Caps reports what this backend supports.
	Caps() Caps
	// Argv builds the exact command line. Pure, with no I/O or side effects.
	Argv(Job) ([]string, error)
	// Preflight prepares for a pack and returns a cleanup to run afterwards.
	// The cleanup has to be safe to call more than once.
	Preflight(ctx context.Context, j Job) (cleanup func() error, err error)
	// Pack runs the packer.
	Pack(ctx context.Context, r proc.Runner, j Job) (proc.Result, error)
	// PboPath reports where the finished PBO will be.
	PboPath(j Job) string
}

func noopCleanup() error { return nil }
