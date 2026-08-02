package machine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Jyrno42/fx-dayz-tools/internal/paths"
)

// Discovery reports what autodiscovery found and, just as importantly, what it
// could not find. A missing entry is not fatal here. `dayzmod config init`
// writes what it knows, and `dayzmod doctor` names the exact YAML key to fill
// in.
type Discovery struct {
	Config *Config
	Notes  []string
	// Missing names fields that could not be discovered.
	Missing []string
}

func (d *Discovery) note(format string, args ...any) {
	d.Notes = append(d.Notes, fmt.Sprintf(format, args...))
}

func (d *Discovery) missing(field, why string) {
	d.Missing = append(d.Missing, fmt.Sprintf("paths.%s -- %s", field, why))
}

// Discover probes the machine for everything the tool needs.
func Discover() *Discovery {
	d := &Discovery{Config: &Config{Version: SchemaVersion}}
	cfg := d.Config
	cfg.applyDefaults()

	d.discoverSteam()
	d.discoverMikero()
	d.discoverPDrive()
	d.discoverKeys()

	return d
}

func (d *Discovery) discoverSteam() {
	cfg := d.Config

	root, err := steamRoot()
	if err != nil || root == "" {
		d.missing("dayz_client", "Steam installation not found; set dayz_client, dayz_server and dayz_tools by hand")
		return
	}
	d.note("Steam at %s", root)

	apps, err := FindSteamApps(root, AppDayZ, AppDayZServer, AppDayZTools)
	if err != nil {
		d.note("could not read Steam libraries: %v", err)
		return
	}

	if app, ok := apps[AppDayZ]; ok {
		cfg.Paths.DayZClient = app.Dir
		d.note("DayZ at %s", app.Dir)
		// The workshop content lives beside the app in the same library.
		ws := filepath.Join(app.Library, "steamapps", "workshop", "content", AppDayZ)
		if _, err := os.Stat(ws); err == nil {
			cfg.Paths.Workshop = ws
		}
	} else {
		d.missing("dayz_client", "DayZ (app 221100) is not installed in any Steam library")
	}

	if app, ok := apps[AppDayZServer]; ok {
		cfg.Paths.DayZServer = app.Dir
		d.note("DayZ Server at %s", app.Dir)
	} else {
		d.missing("dayz_server", "DayZ Server (app 223350) is not installed; the dev loop needs it")
	}

	if app, ok := apps[AppDayZTools]; ok {
		bin := filepath.Join(app.Dir, "Bin")
		if _, err := os.Stat(bin); err == nil {
			cfg.Paths.DayZTools = bin
		} else {
			cfg.Paths.DayZTools = app.Dir
		}
		d.note("DayZ Tools at %s", cfg.Paths.DayZTools)
	} else {
		d.missing("dayz_tools", "DayZ Tools (app 830640) is not installed; AddonBuilder and DSSignFile come from it")
	}
}

func (d *Discovery) discoverMikero() {
	cfg := d.Config
	if dir := mikeroBin(); dir != "" {
		cfg.Paths.Mikero = dir
		d.note("Mikero DePboTools at %s", dir)
		return
	}
	// Not an error, since only a pboProject release channel needs it.
	d.note("Mikero DePboTools not found (only needed for an obfuscated release)")
}

// discoverPDrive detects the work drive and everything derived from it.
func (d *Discovery) discoverPDrive() {
	cfg := d.Config
	cfg.PDrive.AutoMount = true

	drive, err := paths.LookupDrive(cfg.PDrive.Letter)
	if err != nil {
		d.note("could not inspect %s: %v", cfg.PDrive.Letter, err)
		return
	}

	switch drive.Kind {
	case paths.DriveSubst:
		cfg.PDrive.Mode = "subst"
		cfg.PDrive.Backing = drive.Backing
		d.note("%s is a subst of %s", drive.Letter, drive.Backing)
	case paths.DriveVolume:
		cfg.PDrive.Mode = "real"
		d.note("%s is a real volume", drive.Letter)
	default:
		d.note("%s is not mounted; `dayzmod pdrive mount` will create it once pdrive.backing is set", drive.Letter)
	}

	// These all live on the work drive.
	root := cfg.PDrive.Letter + `\`
	setIfExists(&cfg.Paths.KeysDir, filepath.Join(root, "keys"))
	setIfExists(&cfg.Paths.BuildDir, filepath.Join(root, "_tmp", "mod-build"))
	setIfExists(&cfg.Paths.ReleaseDir, filepath.Join(root, "_tmp", "mod-release"))

	if cfg.Paths.BuildDir == "" {
		cfg.Paths.BuildDir = filepath.Join(root, "_tmp", "mod-build")
	}
	if cfg.Paths.ReleaseDir == "" {
		cfg.Paths.ReleaseDir = filepath.Join(root, "_tmp", "mod-release")
	}
	// pboProject's temp is on the work drive and shared by every project. It goes
	// missing routinely because the clean-temp option removes it, so its absence
	// tells you nothing about whether pboProject has ever run.
	if cfg.Paths.PboTemp == "" {
		cfg.Paths.PboTemp = filepath.Join(root, "temp")
	}
}

// discoverKeys registers every key pair found in the keys directory.
func (d *Discovery) discoverKeys() {
	cfg := d.Config
	if cfg.Paths.KeysDir == "" {
		return
	}
	entries, err := os.ReadDir(cfg.Paths.KeysDir)
	if err != nil {
		return
	}
	if cfg.Keyrings == nil {
		cfg.Keyrings = map[string]Keyring{}
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".biprivatekey") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".biprivatekey")
		k := Keyring{Private: filepath.Join(cfg.Paths.KeysDir, e.Name())}
		if pub := filepath.Join(cfg.Paths.KeysDir, name+".bikey"); fileExists(pub) {
			k.Public = pub
		}
		cfg.Keyrings[name] = k
	}
	if n := len(cfg.Keyrings); n > 0 {
		d.note("%d signing key(s) in %s", n, cfg.Paths.KeysDir)
	}
}

func setIfExists(dst *string, candidate string) {
	if *dst != "" {
		return
	}
	if _, err := os.Stat(candidate); err == nil {
		*dst = candidate
	}
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// firstExisting returns the first path that exists, or "".
func firstExisting(candidates ...string) string {
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}
