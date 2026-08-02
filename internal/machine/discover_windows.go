//go:build windows

package machine

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// steamRoot finds the Steam installation. The per-user key is authoritative and
// is the one Steam itself updates, with the machine key and the default path as
// fallbacks.
func steamRoot() (string, error) {
	if p := regString(registry.CURRENT_USER, `Software\Valve\Steam`, "SteamPath"); p != "" {
		// Steam writes this with forward slashes.
		return filepath.Clean(p), nil
	}
	for _, key := range []struct {
		root registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Valve\Steam`},
		{registry.LOCAL_MACHINE, `SOFTWARE\Valve\Steam`},
	} {
		if p := regString(key.root, key.path, "InstallPath"); p != "" {
			return filepath.Clean(p), nil
		}
	}
	return firstExisting(
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Steam"),
		filepath.Join(os.Getenv("ProgramFiles"), "Steam"),
	), nil
}

// mikeroBin locates the DePboTools bin directory holding pboProject.exe.
func mikeroBin() string {
	// pboProject records its own install location under its registry key.
	if p := regString(registry.CURRENT_USER, `Software\Mikero\pboProject`, "path"); p != "" {
		if bin := filepath.Join(p, "bin"); fileExists(filepath.Join(bin, "pboProject.exe")) {
			return bin
		}
	}
	if exe := regString(registry.CURRENT_USER, `Software\Mikero\pboProject`, "exe"); exe != "" {
		if fileExists(exe) {
			return filepath.Dir(exe)
		}
	}
	return firstExisting(
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Mikero", "DePboTools", "bin"),
		filepath.Join(os.Getenv("ProgramFiles"), "Mikero", "DePboTools", "bin"),
	)
}

func regString(root registry.Key, path, name string) string {
	k, err := registry.OpenKey(root, path, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return ""
	}
	defer k.Close()

	v, _, err := k.GetStringValue(name)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(v)
}
