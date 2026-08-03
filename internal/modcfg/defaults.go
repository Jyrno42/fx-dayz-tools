package modcfg

import "time"

// DefaultIncludeExtensions is AddonBuilder's -include allowlist, matching the
// .buildfiles shipped in every existing repo.
//
// *.cpp is missing on purpose, since AddonBuilder binarises config.cpp into
// config.bin itself. *.md is missing so READMEs stay out of the PBO.
var DefaultIncludeExtensions = []string{
	"*.emat", "*.edds", "*.ptc", "*.c", "*.imageset", "*.layout", "*.ogg",
	"*.png", "*.paa", "*.rvmat", "*.wrp", "*.csv", "*.xml", "*.p3d", "*.cfg",
	"*.txt", "*.json",
}

// Defaults that are referenced from more than one place.
const (
	DefaultPort         = 2302
	DefaultProfile      = "ServerProfile"
	DefaultLockfile     = ".build_lockfile.json"
	DefaultBuildlogDir  = "buildlog"
	DefaultSource       = "mod"
	DefaultEngine       = "dayz"
	DefaultMaxHitsShown = 40

	DefaultBootTimeout = 180 * time.Second
	// DefaultSettle is how long to wait after the port binds before scanning the
	// script log. The UDP port opens before Enforce finishes compiling.
	DefaultSettle = 40 * time.Second
)

// applyDefaults fills in every optional field so the rest of the tool never has
// to ask "was this set?". Runs after parsing and before validation.
func (c *Config) applyDefaults() {
	c.SetNames()

	if c.Mod.Visibility == "" {
		c.Mod.Visibility = VisibilityPrivate
	}
	if len(c.IncludeExtensions) == 0 {
		c.IncludeExtensions = append([]string(nil), DefaultIncludeExtensions...)
	}

	if c.Repo.Lockfile == "" {
		c.Repo.Lockfile = DefaultLockfile
	}
	if c.Repo.BuildlogDir == "" {
		c.Repo.BuildlogDir = DefaultBuildlogDir
	}

	c.defaultAddonSets()
	c.defaultChannels()
	c.defaultLaunch()
	c.defaultScriptCheck()
}

func (c *Config) defaultAddonSets() {
	// A single-addon repo can only omit addon_sets entirely if it also declares
	// no addons, and validation rejects that, so there is nothing to synthesise.
	for _, set := range c.AddonSets {
		if set.Source == "" {
			set.Source = DefaultSource
		}
		if set.ModName == "" {
			set.ModName = c.Mod.Name
		}
		for _, addon := range set.Addons {
			if addon.Policy.Side == "" {
				addon.Policy.Side = SideBoth
			}
			// AddonBuilder binarises by default, and model.cfg only gets applied
			// when binarising, so an addon that says nothing gets binarised.
			setDefault(&addon.Policy.Binarize, true)
			setDefault(&addon.Policy.Obfuscate, false)
		}
	}
}

func (c *Config) defaultChannels() {
	for _, ch := range c.Channels {
		if ch.Packer == "" {
			ch.Packer = PackerAddonBuilder
		}

		if ch.ChangeDetection == "" {
			// A release should never skip an addon just because a hash matched.
			if ch.Name == "release" || ch.Packer == PackerPboProject {
				ch.ChangeDetection = DetectNone
			} else {
				ch.ChangeDetection = DetectLockfile
			}
		}

		// Only the dev loop deploys into the game installs by default.
		if ch.Deploy == nil && ch.Name == "dev" {
			ch.Deploy = []Target{TargetClient, TargetServer}
		}

		if ch.Sign.Enabled {
			if ch.Sign.DistributeBikey == nil {
				pub := c.Mod.Visibility == VisibilityPublic
				ch.Sign.DistributeBikey = &pub
			}
		}

		if ch.Packer == PackerPboProject && ch.PboProject == nil {
			ch.PboProject = &PboProject{}
		}
		if ch.PboProject != nil {
			ch.PboProject.applyDefaults()
		}

		if ch.Manifest.Enabled && ch.Manifest.Algo == "" {
			ch.Manifest.Algo = "sha256"
		}
		if ch.Zip.Enabled && len(ch.Zip.Payloads) == 0 {
			ch.Zip.Payloads = ch.PayloadNames()
		}

		for _, p := range ch.Payloads {
			if len(p.Sides) == 0 {
				p.Sides = []Side{SideBoth, Side(p.Name)}
			}
		}
	}
}

func (p *PboProject) applyDefaults() {
	if p.Engine == "" {
		p.Engine = DefaultEngine
	}
	if p.PrefixFile == "" {
		p.PrefixFile = PrefixAlways
	}
	// Every one of these becomes an explicit +/- flag on every invocation, so
	// "unset" has to resolve to a definite value here. Otherwise it would inherit
	// whatever the pboProject registry happens to hold.
	setDefault(&p.Compress, true)           // +Z
	setDefault(&p.Noisy, true)              // +N
	setDefault(&p.AutomakeStale, true)      // +J
	setDefault(&p.CleanTemp, true)          // +C
	setDefault(&p.NoPrefix, false)          // -$, always ship a prefix
	setDefault(&p.RestoreGUISettings, true) // -R
	// -B: the proven release command does not binarise cpp/sqm. Separate thing
	// from model binarisation, which policy.binarize controls.
	setDefault(&p.BinariseCpp, false)
	// -W. This defaulted to true while it was never emitted, so the registry's
	// m_warnings=0 was doing the real work and nobody noticed the disagreement.
	// Stating it as false is what actually matches how releases have been built,
	// and pboProject 4.05 made +W mean ALL warnings are errors, which is fatal on
	// binarise warnings that originate in vanilla configs rather than the mod.
	// Per-warning severity belongs in the Setup dialog's Warnings & Errors page,
	// where each entry cycles Error -> Warning -> Disabled.
	setDefault(&p.Warnings, false)
	setDefault(&p.DeletePng, false)  // -D, never delete source art
	setDefault(&p.ConvertOgg, false) // -G
	setDefault(&p.ShrinkP3D, false)  // -T
	// -H leaves DayZ png conversion enabled; -@ leaves cfgPatches class names
	// alone, which is the only safe default for a mod other mods depend on.
	setDefault(&p.DisablePngConvert, false)
	setDefault(&p.RenameCfgPatches, false)
}

func (c *Config) defaultLaunch() {
	l := &c.Launch
	if l.Port == 0 {
		l.Port = DefaultPort
	}
	if l.Profile == "" {
		l.Profile = DefaultProfile
	}
	if l.BootTimeout == 0 {
		l.BootTimeout = Duration(DefaultBootTimeout)
	}
	if l.ClientExe == "" {
		// BattlEye on means the client must go through DayZ_BE.exe, or BattlEye
		// reports "Game Restart Required".
		if l.BattlEyeEnabled() {
			l.ClientExe = ExeBE
		} else {
			l.ClientExe = ExePlain
		}
	}
	if l.Diag.ClientExe == "" {
		l.Diag.ClientExe = ExeDiag
	}
	if l.Diag.ServerExe == "" {
		l.Diag.ServerExe = ExeDiag
	}
	for i := range l.Mods {
		if l.Mods[i].Source == "" {
			l.Mods[i].Source = SourceWorkshop
		}
		if l.Mods[i].Side == "" {
			l.Mods[i].Side = SideBoth
		}
	}
	// A diag client can only join a BattlEye-off server, so the built-in preset
	// is there even when the repo does not spell it out.
	if l.Diag.Enabled {
		if l.Presets == nil {
			l.Presets = map[string]Preset{}
		}
		if _, ok := l.Presets["diag"]; !ok {
			off := false
			l.Presets["diag"] = Preset{BattlEye: &off}
		}
	}
}

func (c *Config) defaultScriptCheck() {
	s := &c.ScriptCheck
	if s.Settle == 0 {
		s.Settle = Duration(DefaultSettle)
	}
	if s.Timeout == 0 {
		s.Timeout = c.Launch.BootTimeout
	}
	if s.MaxHitsShown == 0 {
		s.MaxHitsShown = DefaultMaxHitsShown
	}
}

func setDefault(p **bool, v bool) {
	if *p == nil {
		b := v
		*p = &b
	}
}
