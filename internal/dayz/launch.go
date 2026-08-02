// Package dayz builds the command lines that start the game and the server.
package dayz

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Jyrno42/fx-dayz-tools/internal/modcfg"
)

// ModList resolves a launch mod list into the two forms DayZ needs.
//
// This asymmetry is the reason the manifest declares mods once instead of twice:
//
//	server: @CF;@VPPAdminTools;@project-with-everything
//	client: !Workshop\@CF;!Workshop\@VPPAdminTools;@project-with-everything
//
// Workshop mods get reached through !Workshop on the client but sit directly in
// the server root. Write the wrong form and nothing loads at all, silently, with
// no error from the game.
type ModList struct {
	Server []string
	Client []string
	// ServerOnly go through -serverMod= instead of -mod=. Clients neither list
	// nor download them, which is what makes a mod server-side.
	ServerOnly []string
}

// ServerArg returns the -mod= value for the dedicated server, or "" when there
// are none. An empty -mod= is not the same as omitting it, so callers skip it.
func (m ModList) ServerArg() string {
	if len(m.Server) == 0 {
		return ""
	}
	return "-mod=" + strings.Join(m.Server, ";")
}

// ClientArg returns the -mod= value for the game client, or "" when there are
// none, which is the normal case for a server-side-only mod.
func (m ModList) ClientArg() string {
	if len(m.Client) == 0 {
		return ""
	}
	return "-mod=" + strings.Join(m.Client, ";")
}

// ServerModArg returns the -serverMod= value, or "" when there are none.
func (m ModList) ServerModArg() string {
	if len(m.ServerOnly) == 0 {
		return ""
	}
	return "-serverMod=" + strings.Join(m.ServerOnly, ";")
}

// Empty reports whether no mods would be loaded.
func (m ModList) Empty() bool {
	return len(m.Server) == 0 && len(m.Client) == 0 && len(m.ServerOnly) == 0
}

// ResolveMods builds the mod list for a channel, after applying a preset.
//
// Order survives throughout, since dependencies have to load before the mods
// that need them. So CF before VPPAdminTools, and this repo's mod last.
func ResolveMods(cfg *modcfg.Config, channel string, preset string) (ModList, error) {
	mods := cfg.Launch.ModsFor(channel)

	if preset != "" {
		p, ok := cfg.Launch.Presets[preset]
		if !ok {
			return ModList{}, fmt.Errorf("no launch preset %q (have: %s)", preset, strings.Join(presetNames(cfg), ", "))
		}
		mods = applyPreset(mods, p)
	}

	var out ModList
	for _, m := range mods {
		serverName, clientName, err := modNames(m)
		if err != nil {
			return ModList{}, err
		}

		switch m.Side {
		case modcfg.SideServer:
			// -serverMod=, never -mod=, and nothing at all for the client.
			out.ServerOnly = append(out.ServerOnly, serverName)
		case modcfg.SideClient:
			out.Client = append(out.Client, clientName)
		default:
			out.Server = append(out.Server, serverName)
			out.Client = append(out.Client, clientName)
		}
	}
	return out, nil
}

// modNames returns how a mod is named on the server and on the client. Workshop
// mods sit directly in the server root but get reached through !Workshop on the
// client. Everything else is named the same on both.
func modNames(m modcfg.ModRef) (server, client string, err error) {
	switch m.Source {
	case modcfg.SourceWorkshop:
		return m.Name, filepath.Join("!Workshop", m.Name), nil
	case modcfg.SourceSelf:
		return m.Name, m.Name, nil
	case modcfg.SourcePath:
		return m.Path, m.Path, nil
	default:
		return "", "", fmt.Errorf("mod %q has unknown source %q", m.Name, m.Source)
	}
}

// applyPreset drops and adds mods while preserving the declared order. Added
// mods go on the end, which is where a wrapping mod belongs.
func applyPreset(mods []modcfg.ModRef, p modcfg.Preset) []modcfg.ModRef {
	drop := map[string]bool{}
	for _, d := range p.DropMods {
		drop[strings.ToLower(d)] = true
	}

	out := make([]modcfg.ModRef, 0, len(mods)+len(p.AddMods))
	for _, m := range mods {
		if drop[strings.ToLower(m.Name)] {
			continue
		}
		out = append(out, m)
	}

	existing := map[string]bool{}
	for _, m := range out {
		existing[strings.ToLower(m.Name)] = true
	}
	for _, m := range p.AddMods {
		if existing[strings.ToLower(m.Name)] {
			continue
		}
		if m.Source == "" {
			m.Source = modcfg.SourceWorkshop
		}
		out = append(out, m)
	}
	return out
}

// BattlEyeFor reports the effective BattlEye setting after a preset and an
// explicit override.
func BattlEyeFor(cfg *modcfg.Config, preset string, override *bool) bool {
	on := cfg.Launch.BattlEyeEnabled()
	if preset != "" {
		if p, ok := cfg.Launch.Presets[preset]; ok && p.BattlEye != nil {
			on = *p.BattlEye
		}
	}
	if override != nil {
		on = *override
	}
	return on
}

// PortFor reports the effective port after a preset and an explicit override.
func PortFor(cfg *modcfg.Config, preset string, override int) int {
	port := cfg.Launch.Port
	if preset != "" {
		if p, ok := cfg.Launch.Presets[preset]; ok && p.Port != 0 {
			port = p.Port
		}
	}
	if override != 0 {
		port = override
	}
	return port
}

// ClientExeKind picks which client executable to launch.
//
// Diag mode wins, because the diag client is the whole point of asking for it.
// Otherwise BattlEye decides. With BattlEye on, the launch has to go through
// DayZ_BE.exe, or BattlEye reports "Game Restart Required" and refuses.
func ClientExeKind(cfg *modcfg.Config, diag bool, battlEye bool) modcfg.ExeKind {
	if diag {
		if cfg.Launch.Diag.ClientExe != "" {
			return cfg.Launch.Diag.ClientExe
		}
		return modcfg.ExeDiag
	}
	if !battlEye && cfg.Launch.ClientExe == modcfg.ExeBE {
		return modcfg.ExePlain
	}
	if battlEye {
		return modcfg.ExeBE
	}
	return cfg.Launch.ClientExe
}

func presetNames(cfg *modcfg.Config) []string {
	out := make([]string, 0, len(cfg.Launch.Presets))
	for k := range cfg.Launch.Presets {
		out = append(out, k)
	}
	return out
}
