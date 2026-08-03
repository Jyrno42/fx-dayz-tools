// Package machine holds the user-level configuration, i.e. where DayZ, the DayZ
// Tools, Mikero and the signing keys live on this computer.
//
// It stays deliberately separate from dayz.yml. Machine paths are a property of
// the computer rather than of a mod, and hardcoding them into every repo is what
// made the old Taskfiles impossible to share.
package machine

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Jyrno42/fx-dayz-tools/internal/paths"
)

// SchemaVersion is the machine config schema this build understands.
const SchemaVersion = 1

// EnvConfig overrides the config file location.
const EnvConfig = "DAYZMOD_CONFIG"

// Config is the user-level machine configuration.
type Config struct {
	Version  int                `yaml:"version"`
	PDrive   PDrive             `yaml:"pdrive"`
	Paths    Paths              `yaml:"paths"`
	Keyrings map[string]Keyring `yaml:"keyrings"`
	Defaults Defaults           `yaml:"defaults"`

	// Path is where this config was loaded from. Not read from the file.
	Path string `yaml:"-"`
}

// PDrive describes the work drive the DayZ tools require.
type PDrive struct {
	Letter  string `yaml:"letter"`
	Backing string `yaml:"backing"`
	// Mode is "subst" when the letter is mapped onto a directory, or "real" when
	// it is a genuine volume.
	Mode string `yaml:"mode"`
	// AutoMount re-creates the subst when it goes missing. A subst does not
	// survive a reboot, so without this the first build after a restart fails
	// for a reason that has nothing to do with the build.
	AutoMount bool `yaml:"auto_mount"`
}

// Paths locates every external tool and working directory.
type Paths struct {
	DayZClient string `yaml:"dayz_client"`
	DayZServer string `yaml:"dayz_server"`
	DayZTools  string `yaml:"dayz_tools"`
	Mikero     string `yaml:"mikero"`
	Workshop   string `yaml:"workshop"`
	KeysDir    string `yaml:"keys_dir"`
	BuildDir   string `yaml:"build_dir"`
	ReleaseDir string `yaml:"release_dir"`
	// PboTemp is pboProject's working directory. It lives on the P: drive and
	// every project shares it, which is why the clean-temp flag matters.
	PboTemp string `yaml:"pbo_temp"`
}

// Keyring is a signing key pair. A repo only ever references the name, so no key
// path ends up in a tracked file.
type Keyring struct {
	Private string `yaml:"private"`
	Public  string `yaml:"public"`
}

// Defaults are fallbacks for values a mod manifest may omit.
type Defaults struct {
	Port    int    `yaml:"port"`
	Profile string `yaml:"profile"`
}

// DefaultPath returns the standard config location,
// %AppData%\dayzmod\config.yml on Windows.
func DefaultPath() (string, error) {
	if p := os.Getenv(EnvConfig); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("machine: cannot determine the user config directory: %w", err)
	}
	return filepath.Join(dir, "dayzmod", "config.yml"), nil
}

// ErrNotConfigured is returned when no machine config exists yet.
var ErrNotConfigured = errors.New("machine: no configuration found; run `dayzmod config init`")

// Load reads the machine config from path, or the default location if empty.
func Load(path string) (*Config, error) {
	if path == "" {
		p, err := DefaultPath()
		if err != nil {
			return nil, err
		}
		path = p
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w (looked in %s)", ErrNotConfigured, path)
		}
		return nil, fmt.Errorf("machine: read %s: %w", path, err)
	}

	cfg, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	cfg.Path = path
	return cfg, nil
}

// Parse decodes machine config content. It rejects unknown fields, so a typo in
// a path never quietly leaves a tool undiscovered.
func Parse(data []byte) (*Config, error) {
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("file is empty; run `dayzmod config init`")
		}
		return nil, explainDecodeError(err)
	}

	if cfg.Version == 0 {
		return nil, fmt.Errorf("missing `version:`; add `version: %d`", SchemaVersion)
	}
	if cfg.Version > SchemaVersion {
		return nil, fmt.Errorf("declares schema version %d but this dayzmod understands up to %d", cfg.Version, SchemaVersion)
	}
	cfg.applyDefaults()
	return &cfg, nil
}

// unknownFieldRe matches what yaml.v3 says about a key the schema has no home
// for: "line 16: field inkscape not found in type machine.Paths".
var unknownFieldRe = regexp.MustCompile(`^(?:line (\d+): )?field (\S+) not found in type \S+$`)

// explainDecodeError turns a strict-decoding failure into advice.
//
// Rejecting unknown fields is what stops a typo leaving a tool quietly
// undiscovered, but it also means one dead key from an older config stops the
// whole tool. yaml.v3's own wording names a Go type the reader has never heard
// of and says nothing about what to do, so say it here instead.
func explainDecodeError(err error) error {
	var te *yaml.TypeError
	if !errors.As(err, &te) {
		return err
	}

	var out []string
	for _, msg := range te.Errors {
		m := unknownFieldRe.FindStringSubmatch(msg)
		if m == nil {
			out = append(out, msg)
			continue
		}
		where := ""
		if m[1] != "" {
			where = " on line " + m[1]
		}
		out = append(out, fmt.Sprintf(
			"unknown key %q%s: dayzmod does not read it. Delete the line, or correct the spelling if it was meant to be one of the keys above it",
			m[2], where))
	}
	if len(out) == 1 {
		return errors.New(out[0])
	}
	return errors.New(strings.Join(out, "\n  "))
}

func (c *Config) applyDefaults() {
	if c.PDrive.Letter == "" {
		c.PDrive.Letter = "P:"
	}
	if c.PDrive.Mode == "" {
		c.PDrive.Mode = "subst"
	}
	if c.Defaults.Port == 0 {
		c.Defaults.Port = 2302
	}
	if c.Defaults.Profile == "" {
		c.Defaults.Profile = "ServerProfile"
	}
	if c.Keyrings == nil {
		c.Keyrings = map[string]Keyring{}
	}
}

// Save writes the config, creating its directory if needed.
func (c *Config) Save(path string) error {
	if path == "" {
		p, err := DefaultPath()
		if err != nil {
			return err
		}
		path = p
	}
	if c.Version == 0 {
		c.Version = SchemaVersion
	}

	var buf bytes.Buffer
	buf.WriteString("# dayzmod machine configuration.\n")
	buf.WriteString("# Paths on THIS computer. Per-mod settings live in each repo's dayz.yml.\n")
	buf.WriteString("# Regenerate with `dayzmod config init --force`; check with `dayzmod doctor`.\n\n")

	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(c); err != nil {
		return fmt.Errorf("machine: encode: %w", err)
	}
	if err := enc.Close(); err != nil {
		return fmt.Errorf("machine: encode: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("machine: mkdir %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		return fmt.Errorf("machine: write %s: %w", path, err)
	}
	c.Path = path
	return nil
}

// --- tool locations -------------------------------------------------------
//
// Each accessor returns the full path to an executable. None of them check that
// the file exists. `dayzmod doctor` does that once and reports everything
// missing together, instead of failing one tool at a time mid-build.

func (c *Config) AddonBuilder() string { return c.tool("AddonBuilder", "AddonBuilder.exe") }
func (c *Config) DSSignFile() string   { return c.tool("DsUtils", "DSSignFile.exe") }
func (c *Config) DSCreateKey() string  { return c.tool("DsUtils", "DSCreateKey.exe") }
func (c *Config) Binarize() string     { return c.tool("Binarize", "binarize.exe") }
func (c *Config) CfgConvert() string   { return c.tool("CfgConvert", "CfgConvert.exe") }
func (c *Config) Publisher() string    { return c.tool("Publisher", "publisher.exe") }

func (c *Config) tool(sub, exe string) string {
	if c.Paths.DayZTools == "" {
		return ""
	}
	return filepath.Join(c.Paths.DayZTools, sub, exe)
}

// PboProject is Mikero's packer, used for obfuscated signed releases.
func (c *Config) PboProject() string { return c.mikero("pboProject.exe") }

// ExtractPbo unpacks a PBO, used to verify a packed prefix.
func (c *Config) ExtractPbo() string { return c.mikero("ExtractPbo.exe") }

// DeRap converts a binarised config back to text, used for release diffing.
func (c *Config) DeRap() string { return c.mikero("DeRap.exe") }

func (c *Config) mikero(exe string) string {
	if c.Paths.Mikero == "" {
		return ""
	}
	return filepath.Join(c.Paths.Mikero, exe)
}

// --- game executables -----------------------------------------------------

// ClientExe returns the client executable for a launch mode.
func (c *Config) ClientExe(kind string) string {
	if c.Paths.DayZClient == "" {
		return ""
	}
	name := map[string]string{
		"be":    "DayZ_BE.exe",
		"plain": "DayZ_x64.exe",
		"diag":  "DayZDiag_x64.exe",
	}[kind]
	if name == "" {
		name = "DayZ_BE.exe"
	}
	return filepath.Join(c.Paths.DayZClient, name)
}

// ServerExe returns the executable that runs the dedicated server.
//
// The diag server is not a separate binary in the server install. It is the diag
// CLIENT executable run with -server, so it comes from the client directory
// while still being launched with the server directory as its working directory,
// so that its config, mpmissions and @-mods resolve the way the retail server's
// do.
func (c *Config) ServerExe(diag bool) string {
	if diag {
		if c.Paths.DayZClient == "" {
			return ""
		}
		return filepath.Join(c.Paths.DayZClient, "DayZDiag_x64.exe")
	}
	if c.Paths.DayZServer == "" {
		return ""
	}
	return filepath.Join(c.Paths.DayZServer, "DayZServer_x64.exe")
}

// --- keyrings -------------------------------------------------------------

// Key looks up a signing key pair by name.
func (c *Config) Key(name string) (Keyring, error) {
	k, ok := c.Keyrings[name]
	if !ok {
		return Keyring{}, fmt.Errorf("machine: no keyring named %q; known keys: %s", name, strings.Join(c.KeyNames(), ", "))
	}
	return k, nil
}

// KeyNames returns the configured keyring names, sorted.
func (c *Config) KeyNames() []string {
	out := make([]string, 0, len(c.Keyrings))
	for k := range c.Keyrings {
		out = append(out, k)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return []string{"(none configured)"}
	}
	return out
}

// --- P: drive -------------------------------------------------------------

// RepoPDrivePath maps a repo's declared P: path through the configured drive
// letter, so a machine using a different letter still resolves correctly.
func (c *Config) RepoPDrivePath(declared string) string {
	drive, rest := paths.SplitDrive(declared)
	if drive == "" {
		return declared
	}
	return c.PDrive.Letter + `\` + rest
}

// WorkDriveVisible reports whether the configured work drive resolves in this
// process. A subst mapping is per-logon-session, so this can come back false
// while the files behind it are perfectly fine and another shell sees them.
func (c *Config) WorkDriveVisible() bool { return paths.Visible(c.PDrive.Letter) }

// Inspect returns a path usable for checking whether a file exists, falling back
// to the work drive's backing directory when the drive is invisible to this
// process. The second result reports whether that fallback was used.
//
// For inspection only. The DayZ tools resolve their own paths and need the real
// drive, so a build has to fail instead of quietly rebasing onto the backing.
func (c *Config) Inspect(p string) (string, bool) {
	if c.WorkDriveVisible() {
		return p, false
	}
	return paths.Rebase(p, c.PDrive.Letter, c.PDrive.Backing)
}

// WorkDriveHint explains an invisible work drive and how to get it back.
func (c *Config) WorkDriveHint() string {
	return fmt.Sprintf(
		"%s does not resolve in this process. subst mappings live in a per-logon-session "+
			"device map, so a process started from a different session or token does not inherit one. "+
			"The files themselves are fine. Run from the shell that created the mapping, or "+
			"`dayzmod pdrive mount` to re-create it here.", c.PDrive.Letter)
}
