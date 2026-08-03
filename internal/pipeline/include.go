package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Jyrno42/fx-dayz-tools/internal/modcfg"
)

// stageIncludes copies prebuilt PBOs into the mod folder without repacking them.
//
// A server pack is one mod folder holding many PBOs, and not all of them get
// built here. Included PBOs keep the signatures they shipped with, since
// re-signing would throw away the original chain of trust. A pack legitimately
// mixes signed and unsigned PBOs, so an unsigned one gets reported but is never
// an error.
// An entry can name its own @mod folder with mod_name, which is how a prebuilt
// server-only PBO gets a folder an operator can -serverMod=. Empty resolves to
// the primary stage, the only behaviour there used to be.
func (b *Builder) stageIncludes(
	includes []modcfg.IncludeSpec,
	stages []modStage,
	primary modStage,
	packed []packedAddon,
	shipKeys bool,
) ([]packedAddon, error) {
	var staged []packedAddon

	stageDir := map[string]string{}
	for _, st := range stages {
		stageDir[st.ModName] = st.Dir
	}
	// Guards against a silent overwrite. packRelease has already written into
	// these directories, and an include's PBO names are not known until the
	// source directory is read, so validation cannot catch this statically.
	packedInto := map[string]string{}
	for _, a := range packed {
		packedInto[strings.ToLower(a.ModName+"/"+a.Name)] = a.Name
	}

	for _, inc := range includes {
		if inc.From == "" {
			return nil, fmt.Errorf("include: an entry has no `from` directory")
		}

		modName := inc.ModName
		if modName == "" {
			modName = primary.ModName
		}
		dir, ok := stageDir[modName]
		if !ok {
			return nil, fmt.Errorf("include: %s names mod_name %q, which is not a staged mod folder", inc.From, modName)
		}
		addonsDir := filepath.Join(dir, "Addons")

		src := filepath.Join(b.Mod.Root, filepath.FromSlash(inc.From))

		if _, err := os.Stat(src); err != nil {
			if inc.Optional && os.IsNotExist(err) {
				b.Report.Detail("skipped %s (optional, not present)", inc.From)
				continue
			}
			return nil, fmt.Errorf("include: %s: %w", inc.From, err)
		}

		pbos, err := pbosIn(src)
		if err != nil {
			return nil, err
		}
		if len(pbos) == 0 && !inc.Optional {
			return nil, fmt.Errorf("include: %s contains no .pbo files", inc.From)
		}

		side := inc.Side
		if side == "" {
			side = modcfg.SideBoth
		}

		signed, unsigned := 0, 0
		for _, pbo := range pbos {
			name := filepath.Base(pbo)
			addonName := strings.TrimSuffix(name, filepath.Ext(name))
			if other, clash := packedInto[strings.ToLower(modName+"/"+addonName)]; clash {
				return nil, fmt.Errorf(
					"include: %s would copy %s into %s, where this repo already packed %s; one would overwrite the other",
					inc.From, name, modName, other)
			}
			if !b.Runner.DryRun() {
				if err := os.MkdirAll(addonsDir, 0o755); err != nil {
					return nil, fmt.Errorf("include: %w", err)
				}
				if err := copyInto(pbo, filepath.Join(addonsDir, name)); err != nil {
					return nil, fmt.Errorf("include: %s: %w", name, err)
				}
			}

			sigs, err := filepath.Glob(pbo + ".*.bisign")
			if err != nil {
				return nil, fmt.Errorf("include: %w", err)
			}
			if len(sigs) == 0 {
				unsigned++
			} else {
				signed++
			}
			for _, sig := range sigs {
				if b.Runner.DryRun() {
					continue
				}
				if err := copyInto(sig, filepath.Join(addonsDir, filepath.Base(sig))); err != nil {
					return nil, fmt.Errorf("include: %s: %w", filepath.Base(sig), err)
				}
			}

			staged = append(staged, packedAddon{
				Name:     addonName,
				Side:     side,
				PBO:      filepath.Join(addonsDir, name),
				ModName:  modName,
				Included: true,
			})
			packedInto[strings.ToLower(modName+"/"+addonName)] = addonName
		}

		detail := fmt.Sprintf("%d PBO(s), %d signed", len(pbos), signed)
		if unsigned > 0 {
			detail += fmt.Sprintf(", %d unsigned", unsigned)
		}
		b.Report.Step("%-28s %s", "include "+inc.From, detail)

		// The key goes in the folder the operator actually installs, which for a
		// server-only include is its own folder rather than the primary one.
		if err := b.stageIncludeKeys(inc, dir, shipKeys); err != nil {
			return nil, err
		}
	}
	return staged, nil
}

// stageIncludeKeys ships the public keys belonging to included PBOs, when the
// channel ships keys at all.
func (b *Builder) stageIncludeKeys(inc modcfg.IncludeSpec, stage string, shipKeys bool) error {
	if inc.Keys == "" {
		return nil
	}
	if !shipKeys {
		b.Report.Detail("not shipping keys from %s (ship_keys is off)", inc.Keys)
		return nil
	}
	src := filepath.Join(b.Mod.Root, filepath.FromSlash(inc.Keys))
	entries, err := os.ReadDir(src)
	if err != nil {
		if inc.Optional && os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("include: keys %s: %w", inc.Keys, err)
	}

	n := 0
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".bikey") {
			continue
		}
		if !b.Runner.DryRun() {
			dst := filepath.Join(stage, "keys", e.Name())
			if err := copyInto(filepath.Join(src, e.Name()), dst); err != nil {
				return fmt.Errorf("include: key %s: %w", e.Name(), err)
			}
		}
		n++
	}
	if n > 0 {
		b.Report.Detail("shipped %d key(s) from %s", n, inc.Keys)
	}
	return nil
}

// pbosIn lists the PBOs in a directory, sorted.
func pbosIn(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("include: reading %s: %w", dir, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".pbo") {
			continue
		}
		out = append(out, filepath.Join(dir, e.Name()))
	}
	sort.Strings(out)
	return out, nil
}
