// Package scaffold generates a new mod repo, and refreshes the files the tool
// owns in an existing one.
package scaffold

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"unicode"

	"github.com/Jyrno42/fx-dayz-tools/internal/modcfg"
	"github.com/Jyrno42/fx-dayz-tools/internal/paths"
)

//go:embed templates/*.tmpl
var templates embed.FS

// Spec describes the repo to generate.
type Spec struct {
	// Dir is the repo root, already absolute.
	Dir string
	// ID is the short slug, e.g. "xy-toolkit".
	ID string
	// Name is the mod folder name including the @.
	Name string
	// Addon is the PBO name, e.g. "FXGitSync".
	Addon string
	// PDrivePath is where the DayZ tools need to see the repo.
	PDrivePath string
	// PDriveLinkFrom is set when the repo lives outside the work drive.
	PDriveLinkFrom string
	// ServerOnly generates a mod that loads through -serverMod=, never reaches
	// clients, and deploys only to the server.
	ServerOnly bool
}

// data is what the templates see.
type data struct {
	Spec
	// PrefixSlash is the PBO prefix with forward slashes, as config.cpp wants.
	PrefixSlash string
	// IncludeExtensions is the semicolon-joined AddonBuilder allowlist.
	IncludeExtensions string
}

// File is one generated file.
type File struct {
	// Path is relative to the repo root, slash-separated.
	Path string
	// Content is the rendered bytes.
	Content []byte
	// Owned marks files the tool maintains and may refresh in place.
	Owned bool
}

// Plan renders every file for a spec without writing anything.
func Plan(s Spec) ([]File, error) {
	prefix, err := paths.Prefix(s.PDrivePath, "mod", s.Addon)
	if err != nil {
		return nil, err
	}

	d := data{
		Spec:              s,
		PrefixSlash:       paths.ToSlash(prefix),
		IncludeExtensions: strings.Join(modcfg.DefaultIncludeExtensions, ";"),
	}

	specs := []struct {
		tmpl  string
		path  string
		owned bool
	}{
		// Tool-owned, so they are regenerable and `init --sync` refreshes them.
		{"buildfiles.tmpl", ".buildfiles", true},
		{"gitattributes.tmpl", ".gitattributes", true},
		{"gitignore.tmpl", ".gitignore", true},
		// Yours from here on. Generated once, then edit them however you like.
		{"dayz.yml.tmpl", modcfg.FileName, false},
		{"Taskfile.yml.tmpl", "Taskfile.yml", false},
		{"serverdz_local.cfg.tmpl", "tools/serverdz_local.cfg", false},
		{"config.cpp.tmpl", "mod/" + s.Addon + "/config.cpp", false},
	}

	out := make([]File, 0, len(specs))
	for _, sp := range specs {
		content, err := render(sp.tmpl, d)
		if err != nil {
			return nil, err
		}
		out = append(out, File{Path: sp.path, Content: content, Owned: sp.owned})
	}
	return out, nil
}

func render(name string, d data) ([]byte, error) {
	raw, err := templates.ReadFile("templates/" + name)
	if err != nil {
		return nil, fmt.Errorf("scaffold: %w", err)
	}
	t, err := template.New(name).Parse(string(raw))
	if err != nil {
		return nil, fmt.Errorf("scaffold: parsing %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, d); err != nil {
		return nil, fmt.Errorf("scaffold: rendering %s: %w", name, err)
	}
	return buf.Bytes(), nil
}

// ScriptDirs are the Enforce script module directories a new addon gets, so the
// paths named in the generated config.cpp actually exist.
var ScriptDirs = []string{"Scripts/3_Game", "Scripts/4_World", "Scripts/5_Mission"}

// Write creates the files under s.Dir.
//
// Existing files never get overwritten unless force is set, and each one is
// reported so the caller can say what it skipped. onlyOwned restricts the write
// to tool-maintained files, which is what `init --sync` does in an existing
// repo.
func Write(s Spec, files []File, force, onlyOwned bool) (written, skipped []string, err error) {
	for _, f := range files {
		if onlyOwned && !f.Owned {
			continue
		}
		full := filepath.Join(s.Dir, filepath.FromSlash(f.Path))

		if _, statErr := os.Stat(full); statErr == nil && !force {
			skipped = append(skipped, f.Path)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return written, skipped, fmt.Errorf("scaffold: %w", err)
		}
		if err := os.WriteFile(full, f.Content, 0o644); err != nil {
			return written, skipped, fmt.Errorf("scaffold: writing %s: %w", full, err)
		}
		written = append(written, f.Path)
	}

	if !onlyOwned {
		for _, dir := range ScriptDirs {
			full := filepath.Join(s.Dir, "mod", s.Addon, filepath.FromSlash(dir))
			if err := os.MkdirAll(full, 0o755); err != nil {
				return written, skipped, fmt.Errorf("scaffold: %w", err)
			}
		}
	}
	return written, skipped, nil
}

// Derive fills in whatever the caller left blank, from the repo directory and
// the machine's work-drive layout.
//
// backing is the directory the work drive is mapped onto. A repo beneath it is
// already visible to the DayZ tools, while one outside it needs a junction.
func Derive(s Spec, driveLetter, backing string) (Spec, error) {
	if s.Dir == "" {
		return s, fmt.Errorf("scaffold: no target directory")
	}
	abs, err := filepath.Abs(s.Dir)
	if err != nil {
		return s, err
	}
	s.Dir = abs

	if s.ID == "" {
		s.ID = strings.ToLower(filepath.Base(abs))
	}
	if s.Name == "" {
		s.Name = "@" + s.ID
	}
	if !strings.HasPrefix(s.Name, "@") {
		s.Name = "@" + s.Name
	}
	if s.Addon == "" {
		s.Addon = AddonName(s.ID)
	}

	if driveLetter == "" {
		driveLetter = "P:"
	}

	if s.PDrivePath == "" {
		if backing != "" && paths.Under(backing, abs) {
			// Already inside the work drive, so mirror its position, no junction.
			rel := strings.TrimPrefix(paths.ToWindows(abs), paths.ToWindows(backing))
			s.PDrivePath = driveLetter + `\` + strings.Trim(rel, `\`)
		} else {
			s.PDrivePath = driveLetter + `\projects\` + s.ID
			s.PDriveLinkFrom = abs
		}
	}
	return s, nil
}

// AddonName derives a PBO name from a slug, so "xy-toolkit" becomes "XYToolkit".
// Short leading segments get treated as an initialism, which matches how these
// repos are named. Override it when the guess is wrong. The name ends up in
// every PBO prefix, so it is worth getting right at the start.
func AddonName(id string) string {
	var b strings.Builder
	for _, seg := range strings.FieldsFunc(id, func(r rune) bool {
		return r == '-' || r == '_' || r == ' ' || r == '.'
	}) {
		if seg == "" {
			continue
		}
		if len(seg) <= 3 {
			b.WriteString(strings.ToUpper(seg))
			continue
		}
		runes := []rune(strings.ToLower(seg))
		runes[0] = unicode.ToUpper(runes[0])
		b.WriteString(string(runes))
	}
	if b.Len() == 0 {
		return "Main"
	}
	return b.String()
}
