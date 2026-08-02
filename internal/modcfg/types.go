package modcfg

import (
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Visibility decides whether the public signing key ships with a release.
type Visibility string

const (
	// VisibilityPrivate never distributes the .bikey. pboProject writes a keys/
	// folder unconditionally when signing, so the release pipeline deletes it and
	// then asserts that no .bikey survives anywhere in the payload.
	VisibilityPrivate Visibility = "private"
	// VisibilityPublic ships keys/<name>.bikey so server admins can whitelist it.
	VisibilityPublic Visibility = "public"
)

// PackerName selects the packing backend for a channel.
type PackerName string

const (
	// PackerAddonBuilder is the DayZ Tools packer used for the dev loop.
	PackerAddonBuilder PackerName = "addonbuilder"
	// PackerPboProject is Mikero pboProject, used for obfuscated signed releases.
	PackerPboProject PackerName = "pboproject"
)

// Side decides which release payloads an addon lands in.
type Side string

const (
	SideBoth   Side = "both"
	SideClient Side = "client"
	SideServer Side = "server"
)

// Target is a deploy destination for the dev loop.
type Target string

const (
	TargetClient Target = "client"
	TargetServer Target = "server"
)

// ChangeDetection decides whether a channel consults the lockfile.
type ChangeDetection string

const (
	// DetectLockfile skips addons whose content hash is unchanged.
	DetectLockfile ChangeDetection = "lockfile"
	// DetectNone always rebuilds. Releases use this, because shipping a stale PBO
	// on a hash match is far worse than repacking one you did not need to.
	DetectNone ChangeDetection = "none"
)

// ExeKind selects which client executable to launch.
type ExeKind string

const (
	// ExeBE is DayZ_BE.exe. Launching DayZ_x64.exe directly while BattlEye is on
	// makes BattlEye report "Game Restart Required", so this is the default.
	ExeBE ExeKind = "be"
	// ExePlain is DayZ_x64.exe, for offline use with BattlEye off.
	ExePlain ExeKind = "plain"
	// ExeDiag is DayZDiag_x64.exe, which can only join a BattlEye-off server.
	ExeDiag ExeKind = "diag"
)

// ModSource says where a launch-time mod dependency lives.
type ModSource string

const (
	// SourceWorkshop resolves under the client's !Workshop directory and bare on
	// the server. Getting that asymmetry wrong silently loads nothing, so it gets
	// declared once here instead of written out per side.
	SourceWorkshop ModSource = "workshop"
	// SourceSelf is a mod this repo builds.
	SourceSelf ModSource = "self"
	// SourcePath is an explicit directory, used the same way on both sides.
	SourcePath ModSource = "path"
)

// PrefixFilePolicy controls whether $PBOPREFIX$ is materialised for pboProject.
type PrefixFilePolicy string

const (
	// PrefixAlways writes $PBOPREFIX$ before packing and deletes it afterwards.
	// pboProject can derive the prefix on its own from the source folder's
	// position under P:, but a derived prefix is one of those things that fails
	// silently when it goes wrong, so we spell it out instead.
	PrefixAlways PrefixFilePolicy = "always"
	// PrefixNever leaves the source tree untouched, for repos that commit their
	// own $PBOPREFIX$ files.
	PrefixNever PrefixFilePolicy = "never"
	// PrefixVerify does not write anything, but checks the packed PBO's header
	// prefix afterwards.
	PrefixVerify PrefixFilePolicy = "verify"
)

// Duration is a time.Duration that reads from YAML as "40s", "3m", "180s".
type Duration time.Duration

func (d Duration) Duration() time.Duration { return time.Duration(d) }
func (d Duration) String() string          { return time.Duration(d).String() }

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	var s string
	if err := node.Decode(&s); err != nil {
		return fmt.Errorf("line %d: expected a duration like \"40s\" or \"3m\"", node.Line)
	}
	parsed, err := time.ParseDuration(strings.TrimSpace(s))
	if err != nil {
		return fmt.Errorf("line %d: %q is not a duration (use forms like \"40s\", \"3m\", \"180s\")", node.Line, s)
	}
	if parsed < 0 {
		return fmt.Errorf("line %d: duration %q must not be negative", node.Line, s)
	}
	*d = Duration(parsed)
	return nil
}

func (d Duration) MarshalYAML() (any, error) { return time.Duration(d).String(), nil }

// FileCopy is a file staged into a build output, e.g. mod.cpp into a release, or
// a JSON fixture into the dev server profile. From is relative to the repo root
// and may end in /* to copy a directory's contents.
type FileCopy struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
	// Optional skips the copy when the source is absent instead of failing.
	// meta.cpp needs this. It does not exist until the Workshop item has been
	// published once, and only then does it get committed to the repo.
	Optional bool `yaml:"optional"`
}

// Hook is an external command the tool runs on the repo's behalf, such as a
// mod-specific code generator. The tool deliberately knows nothing about what
// these do beyond how to invoke them and whether they worked.
type Hook struct {
	Name string   `yaml:"name"`
	Run  []string `yaml:"run"`
	// Dir is relative to the repo root. Empty means the repo root.
	Dir string `yaml:"dir"`
}

func (h Hook) String() string {
	if h.Name != "" {
		return h.Name
	}
	return strings.Join(h.Run, " ")
}
