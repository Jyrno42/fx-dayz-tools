package modcfg

import (
	"fmt"
	"path"
	"strings"

	"github.com/Jyrno42/fx-dayz-tools/internal/paths"
)

// Errors collects every problem found in a manifest, so one run reports all of
// them instead of making you fix the config one line per invocation.
type Errors []string

func (e Errors) Error() string {
	if len(e) == 1 {
		return e[0]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d problems:", len(e))
	for _, msg := range e {
		fmt.Fprintf(&b, "\n  - %s", msg)
	}
	return b.String()
}

// Validate checks a manifest for anything that would produce a wrong build.
func (c *Config) Validate() error {
	var errs Errors
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Sprintf(format, args...))
	}

	c.validateMod(add)
	c.validateRepo(add)
	c.validateAddonSets(add)
	c.validateChannels(add)
	c.validateLaunch(add)
	c.validateHooks(add)
	c.validateObfuscationIsReachable(add)
	c.validateIncludes(add)

	if len(errs) > 0 {
		return errs
	}
	return nil
}

type addFunc func(string, ...any)

func (c *Config) validateMod(add addFunc) {
	if c.Mod.Name == "" {
		add("mod.name is required (the @mod folder name, e.g. \"@project-with-everything\")")
	} else if !strings.HasPrefix(c.Mod.Name, "@") {
		add("mod.name %q must start with @ -- DayZ resolves mod folders by that prefix", c.Mod.Name)
	}
	if c.Mod.ID == "" {
		add("mod.id is required (a short slug used in release filenames)")
	}
	switch c.Mod.Visibility {
	case VisibilityPrivate, VisibilityPublic:
	default:
		add("mod.visibility %q must be %q or %q", c.Mod.Visibility, VisibilityPrivate, VisibilityPublic)
	}
}

func (c *Config) validateRepo(add addFunc) {
	if c.Repo.PDrivePath == "" {
		add("repo.pdrive_path is required: it determines every PBO prefix, so it is stated rather than guessed")
		return
	}
	if _, err := paths.PrefixRoot(c.Repo.PDrivePath); err != nil {
		add("repo.pdrive_path: %v", err)
	}
	if drive, _ := paths.SplitDrive(c.Repo.PDrivePath); drive == "" {
		add("repo.pdrive_path %q needs a drive letter, e.g. P:\\projects\\%s", c.Repo.PDrivePath, c.Mod.ID)
	}
	if c.Repo.PDriveLinkFrom != "" {
		if drive, _ := paths.SplitDrive(c.Repo.PDriveLinkFrom); drive == "" {
			add("repo.pdrive_link_from %q needs a drive letter", c.Repo.PDriveLinkFrom)
		}
		if paths.Equal(c.Repo.PDriveLinkFrom, c.Repo.PDrivePath) {
			add("repo.pdrive_link_from and repo.pdrive_path are the same path; omit pdrive_link_from for a repo that already lives under P:")
		}
	}
}

func (c *Config) validateAddonSets(add addFunc) {
	if len(c.AddonSets) == 0 {
		add("at least one entry under addon_sets is required")
		return
	}

	// If two sets build into the same mod folder and share an addon name, they
	// overwrite each other's PBO.
	seen := map[string]string{} // modName/addon -> set name
	for _, name := range c.AddonSetNames() {
		set := c.AddonSets[name]
		if len(set.Addons) == 0 {
			add("addon_sets.%s has no addons", name)
		}
		if strings.ContainsAny(set.Source, `\/`) && path.IsAbs(paths.ToSlash(set.Source)) {
			add("addon_sets.%s.source %q must be relative to the repo root", name, set.Source)
		}
		if set.ModName == "" {
			add("addon_sets.%s.mod_name is empty and mod.name is not set", name)
		}

		for _, aname := range set.AddonNames() {
			addon := set.Addons[aname]
			// When terms require a mod stay unobfuscated, it is so the code can be
			// audited. A channel setting does not get to override that.
			if !set.ObfuscationAllowed() && addon.Policy.ObfuscateOr(false) {
				add("addon_sets.%s.addons.%s asks to be obfuscated, but addon_sets.%s sets allow_obfuscation: false -- "+
					"vendored source is marked that way when its terms require it stay auditable",
					name, aname, name)
			}
			switch addon.Policy.Side {
			case SideBoth, SideClient, SideServer:
			default:
				add("addon_sets.%s.addons.%s.policy.side %q must be one of %q, %q, %q",
					name, aname, addon.Policy.Side, SideBoth, SideClient, SideServer)
			}
			key := strings.ToLower(set.ModName + "/" + aname)
			if other, dup := seen[key]; dup {
				add("addon %q appears in both addon_sets.%s and addon_sets.%s, and both build into %s -- one would overwrite the other",
					aname, other, name, set.ModName)
			}
			seen[key] = name
		}
	}
}

func (c *Config) validateChannels(add addFunc) {
	if len(c.Channels) == 0 {
		add("at least one entry under channels is required (usually `dev`)")
		return
	}

	for _, name := range c.ChannelNames() {
		ch := c.Channels[name]
		where := "channels." + name

		switch ch.Packer {
		case PackerAddonBuilder, PackerPboProject:
		default:
			add("%s.packer %q must be %q or %q", where, ch.Packer, PackerAddonBuilder, PackerPboProject)
		}
		switch ch.ChangeDetection {
		case DetectLockfile, DetectNone:
		default:
			add("%s.change_detection %q must be %q or %q", where, ch.ChangeDetection, DetectLockfile, DetectNone)
		}

		for _, setName := range ch.AddonSets {
			if _, ok := c.AddonSets[setName]; !ok {
				add("%s.addon_sets references %q, which is not defined under addon_sets", where, setName)
			}
		}
		if len(c.SetsFor(ch)) == 0 {
			add("%s builds no addon sets: check addon_sets on the channel and the channels restriction on each set", where)
		}

		for _, t := range ch.Deploy {
			if t != TargetClient && t != TargetServer {
				add("%s.deploy contains %q; valid targets are %q and %q", where, t, TargetClient, TargetServer)
			}
		}

		c.validateChannelSigning(ch, where, add)
		c.validateChannelPacker(ch, where, add)
		c.validateChannelPayloads(ch, where, add)
	}
}

func (c *Config) validateChannelSigning(ch *Channel, where string, add addFunc) {
	if !ch.Sign.Enabled {
		if ch.Zip.Enabled {
			add("%s produces a zip but does not sign; a released mod that is not signed cannot be whitelisted by servers", where)
		}
		return
	}
	if ch.Sign.Key == "" {
		add("%s.sign.key is required: name a keyring entry from the machine config, not a path", where)
	}
	if c.Mod.Visibility == VisibilityPrivate && c.DistributeBikey(ch) {
		add("%s.sign.distribute_bikey is true but mod.visibility is %q; publishing the .bikey of a private mod lets anyone whitelist a repack",
			where, VisibilityPrivate)
	}
	// A public mod withholding its key is usually deliberate. A server pack is
	// built for one operator who already trusts the keys involved, and packs do
	// not ship keys in a Workshop release anyway. The dangerous direction, a
	// PRIVATE mod publishing its key, is still an error above.
}

func (c *Config) validateChannelPacker(ch *Channel, where string, add addFunc) {
	if ch.Packer != PackerPboProject {
		if ch.PboProject != nil {
			add("%s sets pboproject options but uses packer %q", where, ch.Packer)
		}
		// An addon's obfuscate policy is release intent. A dev channel packing
		// the same addon plain with AddonBuilder is normal, not an error, and it
		// is how every repo here works.
		return
	}

	p := ch.PboProject
	switch p.PrefixFile {
	case PrefixAlways, PrefixNever, PrefixVerify:
	default:
		add("%s.pboproject.prefix_file %q must be one of %q, %q, %q",
			where, p.PrefixFile, PrefixAlways, PrefixNever, PrefixVerify)
	}
	if p.Engine == "" {
		add("%s.pboproject.engine is required (use %q)", where, DefaultEngine)
	}
	if !boolOr(p.RestoreGUISettings, true) {
		add("%s.pboproject.restore_gui_settings is false: without -R every run rewrites the persisted pboProject options, so a later manual GUI build silently changes behaviour", where)
	}
	if p.EncodePrefix != nil {
		add("%s.pboproject.encode_prefix has been replaced by no_prefix, which states the +/-$ flag the way pboProject 4.31 does: +$ means ship WITHOUT a prefix. The sense is reversed, so encode_prefix: true is no_prefix: false. Delete the old key", where)
	}
	if boolOr(p.NoPrefix, false) {
		// pboProject refuses both of these itself, in a GUI nobody is watching.
		if p.PrefixFile == PrefixAlways {
			add("%s.pboproject.no_prefix is true but prefix_file is %q, so the pack would write a $PBOPREFIX$ it was told not to encode", where, PrefixAlways)
		}
		for _, set := range c.SetsFor(ch) {
			for _, aname := range set.AddonNames() {
				if set.Addons[aname].Policy.ObfuscateOr(false) {
					add("%s.pboproject.no_prefix is true but addon %s is obfuscated; pboProject refuses to obfuscate a PBO with no prefix", where, aname)
				}
			}
		}
	}
	if p.SinglePass {
		// Single pass cannot honour per-addon policy, so warn when the manifest
		// declares one that would end up ignored.
		var mixed bool
		var first *bool
		for _, set := range c.SetsFor(ch) {
			for _, aname := range set.AddonNames() {
				o := set.Addons[aname].Policy.ObfuscateOr(false)
				if first == nil {
					first = &o
				} else if *first != o {
					mixed = true
				}
			}
		}
		if mixed {
			add("%s.pboproject.single_pass packs the whole tree in one invocation, so the per-addon obfuscate policy cannot be applied; remove single_pass or make the policy uniform", where)
		}
	}
}

func (c *Config) validateChannelPayloads(ch *Channel, where string, add addFunc) {
	for _, pname := range ch.PayloadNames() {
		p := ch.Payloads[pname]
		for _, s := range p.Sides {
			if s != SideBoth && s != SideClient && s != SideServer {
				add("%s.payloads.%s.sides contains %q; valid sides are %q, %q, %q",
					where, pname, s, SideBoth, SideClient, SideServer)
			}
		}
	}
	for _, want := range ch.Zip.Payloads {
		if _, ok := ch.Payloads[want]; !ok {
			add("%s.zip.payloads references payload %q, which is not defined under %s.payloads", where, want, where)
		}
	}
	// An addon marked server-only with no payload to receive it gets built and
	// then silently dropped on the floor.
	if len(ch.Payloads) > 0 {
		covered := map[Side]bool{}
		for _, p := range ch.Payloads {
			for _, s := range p.Sides {
				covered[s] = true
			}
		}
		for _, set := range c.SetsFor(ch) {
			for _, aname := range set.AddonNames() {
				side := set.Addons[aname].Policy.Side
				if !covered[side] {
					add("%s: addon %q has side %q but no payload includes that side, so it would be packed and then discarded",
						where, aname, side)
				}
			}
		}
	}
}

func (c *Config) validateLaunch(add addFunc) {
	l := c.Launch
	if l.Port < 1 || l.Port > 65535 {
		add("launch.port %d is out of range", l.Port)
	}
	switch l.ClientExe {
	case ExeBE, ExePlain, ExeDiag:
	default:
		add("launch.client_exe %q must be one of %q, %q, %q", l.ClientExe, ExeBE, ExePlain, ExeDiag)
	}
	// Launch DayZ_x64.exe directly with BattlEye on and you get "Game Restart
	// Required" and nothing else useful.
	if l.BattlEyeEnabled() && l.ClientExe == ExePlain {
		add("launch.battleye is true but launch.client_exe is %q; BattlEye requires launching through DayZ_BE.exe (%q)", ExePlain, ExeBE)
	}

	seen := map[string]bool{}
	for i, m := range l.Mods {
		if m.Name == "" {
			add("launch.mods[%d].name is empty", i)
			continue
		}
		if !strings.HasPrefix(m.Name, "@") {
			add("launch.mods[%d].name %q must start with @", i, m.Name)
		}
		if seen[strings.ToLower(m.Name)] {
			add("launch.mods lists %q twice; load order is significant, so duplicates are a mistake", m.Name)
		}
		seen[strings.ToLower(m.Name)] = true

		switch m.Source {
		case SourceWorkshop, SourceSelf:
			if m.Path != "" {
				add("launch.mods[%d] (%s) sets a path but its source is %q; path applies only to source %q", i, m.Name, m.Source, SourcePath)
			}
		case SourcePath:
			if m.Path == "" {
				add("launch.mods[%d] (%s) has source %q but no path", i, m.Name, SourcePath)
			}
		default:
			add("launch.mods[%d] (%s) source %q must be one of %q, %q, %q", i, m.Name, m.Source, SourceWorkshop, SourceSelf, SourcePath)
		}

		switch m.Side {
		case SideBoth, SideClient, SideServer:
		default:
			add("launch.mods[%d] (%s) side %q must be one of %q, %q, %q", i, m.Name, m.Side, SideBoth, SideClient, SideServer)
		}
	}

	for name, p := range l.Presets {
		for _, drop := range p.DropMods {
			if !seen[strings.ToLower(drop)] {
				add("launch.presets.%s drops %q, which is not in launch.mods", name, drop)
			}
		}
	}
}

// validateObfuscationIsReachable catches a manifest that asks for obfuscation no
// channel can ever deliver. Per-channel that is fine, since a dev channel packs
// plain on purpose, but if nothing uses a packer capable of obfuscating then the
// policy is just a lie.
func (c *Config) validateObfuscationIsReachable(add addFunc) {
	var wanted []string
	for _, sname := range c.AddonSetNames() {
		set := c.AddonSets[sname]
		for _, aname := range set.AddonNames() {
			if set.Addons[aname].Policy.ObfuscateOr(false) {
				wanted = append(wanted, sname+"."+aname)
			}
		}
	}
	if len(wanted) == 0 {
		return
	}
	for _, ch := range c.Channels {
		if ch.Packer == PackerPboProject {
			return
		}
	}
	add("%s ask for obfuscation but no channel uses packer %q, so it would never happen",
		strings.Join(wanted, ", "), PackerPboProject)
}

func (c *Config) validateIncludes(add addFunc) {
	for i, inc := range c.Include {
		if inc.From == "" {
			add("include[%d] has no `from` directory", i)
			continue
		}
		if path.IsAbs(paths.ToSlash(inc.From)) {
			add("include[%d].from %q must be relative to the repo root", i, inc.From)
		}
		switch inc.Side {
		case "", SideBoth, SideClient, SideServer:
		default:
			add("include[%d].side %q must be one of %q, %q, %q", i, inc.Side, SideBoth, SideClient, SideServer)
		}
		if inc.Keys != "" && path.IsAbs(paths.ToSlash(inc.Keys)) {
			add("include[%d].keys %q must be relative to the repo root", i, inc.Keys)
		}
		// Deliberately NOT checked against addon_sets[*].mod_name. An include is
		// allowed to name a folder no addon set builds into, which is how a
		// prebuilt server-only PBO gets a folder of its own without this repo
		// packing anything into it. The folder still ships, because releaseStages
		// gives every distinct include mod_name a stage and a payload draws from
		// all of them.
		//
		// What is worth rejecting is a name DayZ could not resolve as a mod folder
		// at all, which is the same rule mod.name and launch.mods[] already carry.
		if inc.ModName != "" && !strings.HasPrefix(inc.ModName, "@") {
			add("include[%d].mod_name %q must start with @ -- DayZ resolves mod folders by that prefix", i, inc.ModName)
		}
	}
}

func (c *Config) validateHooks(add addFunc) {
	check := func(stage string, hooks []Hook) {
		for i, h := range hooks {
			if len(h.Run) == 0 {
				add("hooks.%s[%d] (%s) has no run command", stage, i, h)
			}
		}
	}
	check("pre_build", c.Hooks.PreBuild)
	check("pre_release", c.Hooks.PreRelease)
	check("check", c.Hooks.Check)
}
