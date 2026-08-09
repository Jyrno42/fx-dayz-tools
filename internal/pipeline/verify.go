package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Jyrno42/fx-dayz-tools/internal/packer"
	"github.com/Jyrno42/fx-dayz-tools/internal/proc"
)

// verifyPacked checks that what the packer said it took matches what the source
// directory holds.
//
// A pack can succeed, exit zero and still produce a PBO with almost nothing in
// it, with every downstream step reporting success. Both packers name the files
// they read in their own log, so this compares the two lists without opening the
// PBO.
func (b *Builder) verifyPacked(p packer.Packer, res proc.Result, j packer.Job, sourceDir string) error {
	if b.Runner.DryRun() {
		return nil
	}

	took, known, err := p.PackedFiles(res, j)
	if err != nil {
		return err
	}
	if !known {
		// The packer did not say. Silence is not evidence of a bad pack.
		return nil
	}

	want, err := b.includableFiles(sourceDir)
	if err != nil {
		return err
	}
	if len(want) == 0 {
		return nil
	}

	// pboProject logs configs, texture headers and binarised models through
	// phases that do not emit a per-file line, so an exact comparison would
	// report files that are present. A floor still catches the case that matters,
	// which is a pack that took nothing.
	if !p.Caps().ReportsEveryFile {
		if len(took) == 0 {
			return fmt.Errorf(
				"addon %s: %s reported packing no files, but the source has %d that belong in the PBO. "+
					"The PBO is almost certainly missing its content",
				j.AddonName, p.Name(), len(want))
		}
		return nil
	}

	missing := missingFrom(want, took)
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf(
		"addon %s: %s packed %d of %d source files. Missing %d, starting with:\n  %s",
		j.AddonName, p.Name(), len(took), len(want), len(missing),
		strings.Join(missing[:min(len(missing), 8)], "\n  "))
}

// includableFiles lists the source files that belong in the PBO, relative to the
// source directory, using the same extension list handed to the packer.
func (b *Builder) includableFiles(sourceDir string) ([]string, error) {
	exts := map[string]bool{}
	for _, pattern := range b.Mod.IncludeExtensions {
		exts[strings.ToLower(strings.TrimPrefix(pattern, "*"))] = true
	}

	var out []string
	err := filepath.WalkDir(sourceDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if !exts[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		rel, relErr := filepath.Rel(sourceDir, path)
		if relErr != nil {
			return relErr
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("verify: reading %s: %w", sourceDir, err)
	}
	sort.Strings(out)
	return out, nil
}

// missingFrom returns the wanted paths that the packer did not report, compared
// case-insensitively and with separators normalised, since the logs use whatever
// the tool felt like.
func missingFrom(want, took []string) []string {
	seen := map[string]bool{}
	for _, t := range took {
		seen[normalisePath(t)] = true
	}
	var missing []string
	for _, w := range want {
		if !seen[normalisePath(w)] {
			missing = append(missing, w)
		}
	}
	return missing
}

func normalisePath(p string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimPrefix(p, string(filepath.Separator)), "/", `\`))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
