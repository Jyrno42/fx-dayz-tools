package machine

import (
	"fmt"
	"os"
	"path/filepath"
)

// Steam app IDs for everything this tool drives.
const (
	AppDayZ       = "221100"
	AppDayZServer = "223350"
	AppDayZTools  = "830640"
)

// SteamApp is a located Steam installation.
type SteamApp struct {
	AppID   string
	Name    string
	Dir     string // the install directory
	Library string // the library root that contains it
}

// FindSteamApps locates the given app IDs across every Steam library.
//
// Reading libraryfolders.vdf instead of assuming the default install path is
// what lets this work on a machine whose games live on a second drive. That is a
// very common setup, and one the old hardcoded Taskfile paths could not survive.
func FindSteamApps(steamRoot string, appIDs ...string) (map[string]SteamApp, error) {
	libs, err := SteamLibraries(steamRoot)
	if err != nil {
		return nil, err
	}

	found := map[string]SteamApp{}
	for _, lib := range libs {
		for _, id := range appIDs {
			if _, done := found[id]; done {
				continue
			}
			app, ok, err := readAppManifest(lib, id)
			if err != nil {
				return nil, err
			}
			if ok {
				found[id] = app
			}
		}
	}
	return found, nil
}

// SteamLibraries returns every library root, starting with the Steam install
// itself. A missing or unparseable libraryfolders.vdf degrades to just the root
// instead of failing, so a default install still works.
func SteamLibraries(steamRoot string) ([]string, error) {
	if steamRoot == "" {
		return nil, fmt.Errorf("machine: Steam install directory is unknown")
	}
	libs := []string{steamRoot}

	vdfPath := filepath.Join(steamRoot, "steamapps", "libraryfolders.vdf")
	data, err := os.ReadFile(vdfPath)
	if err != nil {
		if os.IsNotExist(err) {
			return libs, nil
		}
		return nil, fmt.Errorf("machine: read %s: %w", vdfPath, err)
	}

	root, err := parseVDF(data)
	if err != nil {
		return nil, fmt.Errorf("machine: parse %s: %w", vdfPath, err)
	}

	folders := root.child("libraryfolders")
	if folders == nil {
		// Very old Steam wrote the entries at the top level.
		folders = root
	}
	for _, key := range folders.keys() {
		entry := folders.child(key)
		if entry == nil {
			continue
		}
		// Modern format is an object with a "path". Ancient format is "1" -> path.
		path := entry.str("path")
		if path == "" {
			path = entry.Value
		}
		if path == "" {
			continue
		}
		if !containsPath(libs, path) {
			libs = append(libs, path)
		}
	}
	return libs, nil
}

// readAppManifest reads appmanifest_<id>.acf from a library and resolves the
// install directory it names.
func readAppManifest(library, appID string) (SteamApp, bool, error) {
	manifest := filepath.Join(library, "steamapps", "appmanifest_"+appID+".acf")
	data, err := os.ReadFile(manifest)
	if err != nil {
		if os.IsNotExist(err) {
			return SteamApp{}, false, nil
		}
		return SteamApp{}, false, fmt.Errorf("machine: read %s: %w", manifest, err)
	}

	root, err := parseVDF(data)
	if err != nil {
		return SteamApp{}, false, fmt.Errorf("machine: parse %s: %w", manifest, err)
	}

	state := root.child("AppState")
	if state == nil {
		return SteamApp{}, false, nil
	}
	installDir := state.str("installdir")
	if installDir == "" {
		return SteamApp{}, false, nil
	}

	dir := filepath.Join(library, "steamapps", "common", installDir)
	if _, err := os.Stat(dir); err != nil {
		// The manifest can outlive the files, e.g. a partially removed app.
		return SteamApp{}, false, nil
	}

	return SteamApp{
		AppID:   appID,
		Name:    state.str("name"),
		Dir:     dir,
		Library: library,
	}, true, nil
}

func containsPath(list []string, want string) bool {
	for _, v := range list {
		if filepath.Clean(v) == filepath.Clean(want) {
			return true
		}
	}
	return false
}
