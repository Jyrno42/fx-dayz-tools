// Package modcfg loads and validates dayz.yml, the per-mod build manifest.
//
// The binary is installed once and shared by every mod repo, so a repo needs a
// way to state what it needs and fail loudly instead of building the wrong
// thing. That is what version and requires_tool are for.
package modcfg

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// FileName is the manifest filename looked for at a repo root.
const FileName = "dayz.yml"

// SchemaVersion is the dayz.yml schema this build understands. If a repo
// declares something newer we refuse it instead of misreading it silently.
const SchemaVersion = 1

// Config is a parsed dayz.yml.
type Config struct {
	Version      int    `yaml:"version"`
	RequiresTool string `yaml:"requires_tool"`

	Mod  Mod  `yaml:"mod"`
	Repo Repo `yaml:"repo"`

	// IncludeExtensions is AddonBuilder's -include allowlist, written to
	// .buildfiles. pboProject ignores it and uses exclude lists instead.
	IncludeExtensions []string `yaml:"include_extensions"`

	AddonSets map[string]*AddonSet `yaml:"addon_sets"`
	Channels  map[string]*Channel  `yaml:"channels"`

	// Include lists prebuilt PBOs that ship alongside the ones this repo packs.
	// A server pack is one mod folder holding many PBOs, some built here and
	// some obtained already packed.
	Include []IncludeSpec `yaml:"include"`

	Hooks       Hooks                 `yaml:"hooks"`
	Launch      Launch                `yaml:"launch"`
	Fixtures    map[string][]FileCopy `yaml:"fixtures"`
	ScriptCheck ScriptCheck           `yaml:"scriptcheck"`

	// Root is the directory containing dayz.yml. Not read from the file.
	Root string `yaml:"-"`
	// Path is the full path to the loaded dayz.yml. Not read from the file.
	Path string `yaml:"-"`
}

// Mod identifies the mod being built.
type Mod struct {
	// ID is a short slug used in filenames, e.g. "project-with-everything".
	ID string `yaml:"id"`
	// Name is the mod folder name including the @, e.g. "@project-with-everything".
	Name string `yaml:"name"`
	// Visibility decides whether the .bikey ships. It lives here instead of on a
	// command line, where one typo would leak a private key.
	Visibility Visibility `yaml:"visibility"`
}

// Repo describes where this repo lives and how builds cache.
type Repo struct {
	// PDrivePath is where the DayZ tools must see this repo, e.g.
	// P:\projects\project-with-everything. It determines every PBO prefix, which
	// is why we spell it out: a broken subst or junction then fails loudly
	// instead of packing with a silently wrong prefix.
	PDrivePath string `yaml:"pdrive_path"`
	// PDriveLinkFrom is the real location, for repos that do not live under the
	// P: tree. Set it and `dayzmod pdrive ensure` creates and verifies the
	// junction that used to be a hand-written mklink comment in each Taskfile.
	PDriveLinkFrom string `yaml:"pdrive_link_from"`

	Lockfile    string `yaml:"lockfile"`
	BuildlogDir string `yaml:"buildlog_dir"`
	// LFSGuard refuses to pack a Git LFS pointer stub. A fresh clone without
	// `git lfs pull` would otherwise ship text stubs in place of every p3d.
	LFSGuard *bool `yaml:"lfs_guard"`
}

// AddonSet is a group of addons packed together into one mod folder. Most repos
// have exactly one. A repo that also ships test content adds a dev-only set with
// its own mod name and prefix, so that content never reaches a release.
type AddonSet struct {
	// Source is the directory holding the addons, relative to the repo root.
	Source string `yaml:"source"`
	// ModName is the @mod folder this set builds into. Defaults to mod.name.
	ModName string `yaml:"mod_name"`
	// Prefix overrides the derived PBO prefix root. Usually left empty, in which
	// case it comes from repo.pdrive_path plus source.
	Prefix string `yaml:"prefix"`
	// Channels restricts this set to the named channels. Empty means all.
	Channels []string `yaml:"channels"`

	// AllowObfuscation gates obfuscation for every addon in this set.
	//
	// Set it false for vendored source whose terms forbid redistributing it
	// obfuscated. Those terms are usually about auditability rather than the
	// author's protection. At least one widely used mod is community-maintained
	// precisely because its original author shipped obfuscated builds concealing
	// a backdoor, and staying unobfuscated was a condition of the handover. So an
	// addon in such a set asking to be obfuscated is a hard error, not something
	// a later config edit can quietly turn back on.
	AllowObfuscation *bool `yaml:"allow_obfuscation"`

	Addons map[string]*Addon `yaml:"addons"`

	// Name is the map key, filled in during loading.
	Name string `yaml:"-"`
}

// Addon is one PBO.
type Addon struct {
	Policy Policy `yaml:"policy"`
	// Prefix overrides this addon's PBO prefix outright instead of deriving it
	// from the set. Vendored source (a third-party mod checked out as a submodule
	// and built here) has to keep the prefix upstream uses, because its own
	// scripts and configs reference paths under it. That prefix does not always
	// follow the <root>\<addon> shape the set-level override produces.
	Prefix string `yaml:"prefix"`
	// Name is the map key, filled in during loading.
	Name string `yaml:"-"`
}

// Policy is how a single addon is packed and where it ships.
type Policy struct {
	// Binarize converts models and content to their binary form. It defaults to
	// true because that is AddonBuilder's own default, and because model.cfg only
	// gets applied when binarising, so turning it off silently breaks model
	// configuration. Only set it false for an addon that ships pre-binarised or
	// deliberately raw content.
	Binarize *bool `yaml:"binarize"`
	// Obfuscate is release intent, honoured only by packers that support it. It
	// is normal for a dev channel to pack the same addon plain with AddonBuilder.
	Obfuscate *bool `yaml:"obfuscate"`
	Side      Side  `yaml:"side"`
	// NoScramble lists files that must not be scrambled inside an obfuscated PBO.
	// Obfuscation forces compression and Mikero's DLL will not compress init*.*,
	// so anything reached from an init path belongs here.
	NoScramble []string `yaml:"noscramble"`
}

// IncludeSpec brings prebuilt PBOs into the mod folder without repacking them.
//
// Third-party PBOs keep the signatures they shipped with. Re-signing them would
// throw away the original chain of trust, and the operator can whitelist
// whichever key they already trust.
type IncludeSpec struct {
	// From is a directory of .pbo files, relative to the repo root. Any .bisign
	// beside a PBO is copied with it.
	From string `yaml:"from"`
	// Keys is an optional directory of .bikey files belonging to these PBOs.
	// They ship only when the channel ships keys at all.
	Keys string `yaml:"keys"`
	// Side decides which release payloads these PBOs land in.
	Side Side `yaml:"side"`
	// Optional skips the entry when the directory is absent.
	Optional bool `yaml:"optional"`
}

// Channel is a build configuration: dev deploys into the game installs, release
// stages signed payloads.
type Channel struct {
	Packer          PackerName      `yaml:"packer"`
	AddonSets       []string        `yaml:"addon_sets"`
	Out             string          `yaml:"out"`
	Deploy          []Target        `yaml:"deploy"`
	ChangeDetection ChangeDetection `yaml:"change_detection"`

	Sign       Sign        `yaml:"sign"`
	PboProject *PboProject `yaml:"pboproject"`

	// ShipKeys governs the keys/ folder as a whole, meaning this repo's public key
	// plus any belonging to included PBOs. A server pack is usually built for one
	// operator who already trusts the keys involved, so it ships none.
	//
	// When unset, this repo's key follows sign.distribute_bikey and included keys
	// ship if the include entry names a keys directory.
	ShipKeys *bool `yaml:"ship_keys"`

	Payloads   map[string]*Payload `yaml:"payloads"`
	ExtraFiles []FileCopy          `yaml:"extra_files"`
	Zip        Zip                 `yaml:"zip"`
	Manifest   Manifest            `yaml:"manifest"`

	// Name is the map key, filled in during loading.
	Name string `yaml:"-"`
}

// Sign controls PBO signing. In YAML it accepts either `sign: false` or a
// mapping, so a dev channel can turn it off in one word.
type Sign struct {
	Enabled bool `yaml:"-"`
	// Key names a keyring entry in the machine config rather than a path, so key
	// material stays out of every repo.
	Key             string `yaml:"key"`
	DistributeBikey *bool  `yaml:"distribute_bikey"`
	V2              bool   `yaml:"v2"`
}

func (s *Sign) UnmarshalYAML(node *yaml.Node) error {
	// `sign: false` / `sign: true`
	if node.Kind == yaml.ScalarNode {
		var on bool
		if err := node.Decode(&on); err != nil {
			return fmt.Errorf("line %d: sign must be a boolean or a mapping with a key", node.Line)
		}
		s.Enabled = on
		return nil
	}
	type raw Sign
	var r raw
	if err := node.Decode(&r); err != nil {
		return err
	}
	*s = Sign(r)
	s.Enabled = true
	return nil
}

// PboProject holds Mikero-specific release options.
//
// Every one of these becomes an explicit flag on every invocation. pboProject
// persists its options in the GUI registry ("If you +Obfuscate a pbo, all
// subsequent invocations of pboProject will continue to obfuscate until turned
// off"), so omitting a flag means inheriting whatever was last used, which on
// this machine is obfuscation ON.
type PboProject struct {
	Engine  string   `yaml:"engine"`
	Exclude []string `yaml:"exclude"`

	Compress      *bool `yaml:"compress"`       // +Z
	Noisy         *bool `yaml:"noisy"`          // +N, warnings are errors
	AutomakeStale *bool `yaml:"automake_stale"` // +J
	CleanTemp     *bool `yaml:"clean_temp"`     // +C
	// NoPrefix is +/-$, where +$ means ship the PBO WITHOUT a prefix. It replaces
	// an earlier encode_prefix, which named the same flag with the opposite sense
	// on the strength of the 3.91 documentation; 4.31's own help says "+$: enable
	// no prefix in pbo". Setting it true with obfuscation or a label is refused,
	// because pboProject refuses those combinations itself.
	NoPrefix *bool `yaml:"no_prefix"`
	// BinariseCpp controls pboProject's +/-B, which is specifically about
	// binarising config.cpp and mission.sqm, NOT about binarising models. Models
	// are governed by policy.binarize. The proven release command passes -B, so
	// this defaults to false.
	BinariseCpp *bool `yaml:"binarise_cpp"`
	// DisablePngConvert is +/-H, DayZ only, where +H disables png conversion.
	// RenameCfgPatches is +/-@. Both change what the engine sees, so both are
	// stated on every invocation rather than inherited from the GUI registry.
	DisablePngConvert *bool `yaml:"disable_png_convert"` // +/-H
	RenameCfgPatches  *bool `yaml:"rename_cfgpatches"`   // +/-@

	// These rewrite or remove source-derived content, so we state them instead of
	// inheriting them from the GUI registry. All default off, which matches how
	// the existing release has been building.
	Warnings   *bool `yaml:"warnings"`    // +/-W, warnings are errors
	DeletePng  *bool `yaml:"delete_png"`  // +/-D
	ConvertOgg *bool `yaml:"convert_ogg"` // +/-G, wav/wss to ogg
	ShrinkP3D  *bool `yaml:"shrink_p3d"`  // +/-T, DayZ only
	// RestoreGUISettings asks for -R, so the tool never writes its options back to
	// the registry. Without it, a -O run leaves the GUI set to "don't obfuscate"
	// and changes what a later manual build produces.
	//
	// It is intent rather than effect today: the packer does not emit -R, because
	// 3.91 rejects the command line when it is present. Whether 4.31 accepts it is
	// one of the open questions in TODO.md.
	RestoreGUISettings *bool `yaml:"restore_gui_settings"`

	PrefixFile PrefixFilePolicy `yaml:"prefix_file"`

	// SinglePass packs the whole source tree in one invocation, the way the
	// original Taskfile did, instead of once per addon. It stays around as the
	// parity baseline and as a one-flag revert.
	SinglePass bool `yaml:"single_pass"`

	// EncodePrefix is the old name for the +/-$ flag, kept only so a repo still
	// carrying it gets an explanation rather than "unknown field". It named the
	// flag with the opposite sense, so it cannot be migrated by copying the value
	// across: encode_prefix: true means no_prefix: false. Validation rejects it.
	EncodePrefix *bool `yaml:"encode_prefix"`
}

// Payload is one shippable bundle assembled from the packed PBOs.
type Payload struct {
	// Sides selects which addons land here by their policy.side.
	Sides []Side `yaml:"sides"`
	// ExtraFiles are staged in addition to the channel-level ones.
	ExtraFiles []FileCopy `yaml:"extra_files"`

	// Name is the map key, filled in during loading.
	Name string `yaml:"-"`
}

// Zip controls release archiving.
type Zip struct {
	Enabled  bool     `yaml:"enabled"`
	Out      string   `yaml:"out"`
	Payloads []string `yaml:"payloads"`
}

// Manifest controls the SHA-256 provenance manifest written next to a release.
type Manifest struct {
	Enabled bool   `yaml:"enabled"`
	Algo    string `yaml:"algo"`
	Out     string `yaml:"out"`
}

// Hooks are external commands run around the build.
type Hooks struct {
	PreBuild   []Hook `yaml:"pre_build"`
	PreRelease []Hook `yaml:"pre_release"`
	Check      []Hook `yaml:"check"`
}

// Launch configures the local dev server and client.
type Launch struct {
	Port         int      `yaml:"port"`
	Profile      string   `yaml:"profile"`
	ServerConfig string   `yaml:"server_config"`
	BootTimeout  Duration `yaml:"boot_timeout"`

	// Mods is one ordered list. The tool emits the server form bare and the
	// client form !Workshop-prefixed, so nobody has to remember the asymmetry.
	// Order matters here: dependencies must load before their dependents.
	Mods []ModRef `yaml:"mods"`

	ServerArgs []string `yaml:"server_args"`
	ClientArgs []string `yaml:"client_args"`

	ClientExe ExeKind `yaml:"client_exe"`
	// BattlEye is its own option rather than a property of diag mode. It drives
	// both the serverdz config patch and the default client executable.
	BattlEye *bool `yaml:"battleye"`

	Diag    Diag              `yaml:"diag"`
	Presets map[string]Preset `yaml:"presets"`
}

// ModRef is one entry in the launch-time mod load order.
type ModRef struct {
	Name   string    `yaml:"name"`
	Source ModSource `yaml:"source"`
	// Path is used when source is "path".
	Path string `yaml:"path"`
	// Channels restricts this mod to the named channels, e.g. a dev-only addon.
	Channels []string `yaml:"channels"`
	// Side decides how the mod is loaded. "server" puts it in -serverMod= instead
	// of -mod=, which keeps it off the client entirely, so clients neither list
	// nor download it. Default is "both".
	//
	// This is the mechanism for a server-side-only mod. Dropping a PBO into the
	// server's own Addons folder is NOT an alternative: the engine does add the
	// package and register its config, but it never compiles the mod's script
	// modules, so a script-based mod silently does nothing.
	Side Side `yaml:"side"`
}

// Diag configures the diagnostic executables.
type Diag struct {
	Enabled   bool    `yaml:"enabled"`
	ClientExe ExeKind `yaml:"client_exe"`
	ServerExe ExeKind `yaml:"server_exe"`
}

// Preset is a named set of launch overrides, applied with --preset.
type Preset struct {
	DropMods []string `yaml:"drop_mods"`
	AddMods  []ModRef `yaml:"add_mods"`
	BattlEye *bool    `yaml:"battleye"`
	Port     int      `yaml:"port"`
}

// ScriptCheck configures the Enforce compile gate.
type ScriptCheck struct {
	// Settle is how long to wait after the server binds its port before reading
	// the log, since the port opens before script compilation finishes.
	Settle  Duration `yaml:"settle"`
	Timeout Duration `yaml:"timeout"`

	ExtraErrorPatterns []string `yaml:"extra_error_patterns"`
	ExtraNoisePatterns []string `yaml:"extra_noise_patterns"`
	MaxHitsShown       int      `yaml:"max_hits_shown"`
}

// Find walks up from dir looking for dayz.yml, the way git finds .git.
func Find(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(abs, FileName)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", fmt.Errorf("no %s found in %s or any parent directory; run `dayzmod init` to create one", FileName, dir)
		}
		abs = parent
	}
}

// Load reads and validates the dayz.yml at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("modcfg: %w", err)
	}
	cfg, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	cfg.Path = path
	cfg.Root = filepath.Dir(path)
	return cfg, nil
}

// LoadFrom finds dayz.yml starting at dir and loads it.
func LoadFrom(dir string) (*Config, error) {
	path, err := Find(dir)
	if err != nil {
		return nil, err
	}
	return Load(path)
}

// Parse decodes and validates dayz.yml content.
//
// Unknown fields are an error. A silently ignored typo in a build manifest is
// exactly the kind of failure this tool exists to get rid of.
func Parse(data []byte) (*Config, error) {
	var cfg Config

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("file is empty; run `dayzmod init` to create a manifest")
		}
		return nil, err
	}

	if err := cfg.checkVersion(); err != nil {
		return nil, err
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) checkVersion() error {
	switch {
	case c.Version == 0:
		return fmt.Errorf("missing `version:`; add `version: %d`", SchemaVersion)
	case c.Version > SchemaVersion:
		return fmt.Errorf("declares schema version %d but this dayzmod understands up to %d; upgrade with `go install github.com/Jyrno42/fx-dayz-tools/cmd/dayzmod@latest`", c.Version, SchemaVersion)
	case c.Version < SchemaVersion:
		return fmt.Errorf("declares schema version %d, which this dayzmod no longer supports (current: %d)", c.Version, SchemaVersion)
	}
	return nil
}

// SetNames fills the Name fields in from their map keys. Go randomises map
// iteration order, so every consumer has to go through the sorted accessors
// below to stay deterministic.
func (c *Config) SetNames() {
	for name, set := range c.AddonSets {
		set.Name = name
		for aname, addon := range set.Addons {
			addon.Name = aname
		}
	}
	for name, ch := range c.Channels {
		ch.Name = name
		for pname, p := range ch.Payloads {
			p.Name = pname
		}
	}
}

// AddonSetNames returns the addon set names in a stable order.
func (c *Config) AddonSetNames() []string { return sortedKeys(c.AddonSets) }

// ChannelNames returns the channel names in a stable order.
func (c *Config) ChannelNames() []string { return sortedKeys(c.Channels) }

// AddonNames returns a set's addon names in a stable order.
func (s *AddonSet) AddonNames() []string { return sortedKeys(s.Addons) }

// PayloadNames returns a channel's payload names in a stable order.
func (c *Channel) PayloadNames() []string { return sortedKeys(c.Payloads) }

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Channel returns a channel by name.
func (c *Config) Channel(name string) (*Channel, error) {
	ch, ok := c.Channels[name]
	if !ok {
		return nil, fmt.Errorf("no channel %q in %s (have: %s)", name, FileName, strings.Join(c.ChannelNames(), ", "))
	}
	return ch, nil
}

// SetsFor returns the addon sets a channel builds, in a stable order, honouring
// both the channel's addon_sets list and each set's own channels restriction.
func (c *Config) SetsFor(ch *Channel) []*AddonSet {
	var out []*AddonSet
	names := ch.AddonSets
	if len(names) == 0 {
		names = c.AddonSetNames()
	}
	for _, name := range names {
		set, ok := c.AddonSets[name]
		if !ok {
			continue
		}
		if len(set.Channels) > 0 && !contains(set.Channels, ch.Name) {
			continue
		}
		out = append(out, set)
	}
	return out
}

// ModsFor returns the launch mod list for a channel, dropping entries that are
// restricted to other channels.
func (l *Launch) ModsFor(channel string) []ModRef {
	out := make([]ModRef, 0, len(l.Mods))
	for _, m := range l.Mods {
		if len(m.Channels) > 0 && !contains(m.Channels, channel) {
			continue
		}
		out = append(out, m)
	}
	return out
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// boolOr dereferences an optional bool with a default.
func boolOr(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}

// Binarize reports the addon's effective binarize setting.
func (p Policy) BinarizeOr(def bool) bool { return boolOr(p.Binarize, def) }

// ObfuscateOr reports the addon's effective obfuscate setting.
func (p Policy) ObfuscateOr(def bool) bool { return boolOr(p.Obfuscate, def) }

// ObfuscationAllowed reports whether this set's addons may be obfuscated.
func (s *AddonSet) ObfuscationAllowed() bool { return boolOr(s.AllowObfuscation, true) }

// LFSGuardEnabled reports whether to refuse packing LFS pointer stubs.
func (r Repo) LFSGuardEnabled() bool { return boolOr(r.LFSGuard, true) }

// BattlEyeEnabled reports the effective BattlEye setting.
func (l Launch) BattlEyeEnabled() bool { return boolOr(l.BattlEye, true) }

// DistributeBikey reports whether this repo's public key ships. It defaults from
// the mod's visibility, so a private mod cannot leak its key just by omission.
func (c *Config) DistributeBikey(ch *Channel) bool {
	// ship_keys false suppresses every key, including this one.
	if ch.ShipKeys != nil && !*ch.ShipKeys {
		return false
	}
	if ch.Sign.DistributeBikey != nil {
		return *ch.Sign.DistributeBikey
	}
	return c.Mod.Visibility == VisibilityPublic
}

// ShipKeysEnabled reports whether a keys/ folder is produced at all.
func (c *Config) ShipKeysEnabled(ch *Channel) bool {
	if ch.ShipKeys != nil {
		return *ch.ShipKeys
	}
	return true
}

// IncludesFor returns the include entries that belong in a payload side.
func (c *Config) IncludesFor(side Side) []IncludeSpec {
	var out []IncludeSpec
	for _, inc := range c.Include {
		if inc.Side == side || side == "" {
			out = append(out, inc)
		}
	}
	return out
}
