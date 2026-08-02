// Package sign signs PBOs with the DayZ Tools DSUtils signer.
package sign

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Jyrno42/fx-dayz-tools/internal/proc"
)

// Signer wraps DSSignFile.exe.
type Signer struct {
	// Exe is the full path to DSSignFile.exe.
	Exe string
}

// Options controls a signing run.
type Options struct {
	// PrivateKey is the .biprivatekey to sign with.
	PrivateKey string
	// V2 emits a v2 signature instead of the default v3.
	V2 bool
}

// Argv builds the DSSignFile command line. Pure, so it can be golden-tested.
func (s *Signer) Argv(pbo string, opts Options) ([]string, error) {
	if s.Exe == "" {
		return nil, fmt.Errorf("sign: DSSignFile is not configured (set paths.dayz_tools)")
	}
	if opts.PrivateKey == "" {
		return nil, fmt.Errorf("sign: no private key given")
	}
	argv := []string{s.Exe, opts.PrivateKey, pbo}
	if opts.V2 {
		argv = append(argv, "-v2")
	}
	return argv, nil
}

// SignDir signs every PBO in dir and returns the signatures it produced.
func (s *Signer) SignDir(ctx context.Context, r proc.Runner, dir string, opts Options) ([]string, error) {
	if r.DryRun() {
		// Nothing was packed, so there is nothing to enumerate. Report the intent
		// and leave it at that instead of failing on an empty directory.
		if _, err := s.Argv(filepath.Join(dir, "<each>.pbo"), opts); err != nil {
			return nil, err
		}
		return nil, nil
	}

	pbos, err := PBOsIn(dir)
	if err != nil {
		return nil, err
	}
	if len(pbos) == 0 {
		return nil, fmt.Errorf("sign: no PBOs to sign in %s", dir)
	}
	if _, err := os.Stat(opts.PrivateKey); err != nil {
		return nil, fmt.Errorf("sign: private key %s: %w", opts.PrivateKey, err)
	}

	var signed []string
	for _, pbo := range pbos {
		argv, err := s.Argv(pbo, opts)
		if err != nil {
			return nil, err
		}
		res, err := r.Run(ctx, proc.Cmd{Name: argv[0], Args: argv[1:]})
		if err != nil {
			return nil, fmt.Errorf("sign: %s: %w\n%s", filepath.Base(pbo), err, res.Tail(10))
		}
		signed = append(signed, pbo)
	}

	if r.DryRun() {
		return signed, nil
	}

	// DSSignFile can exit zero without producing anything, so check that each PBO
	// actually gained a signature. Finding out a release went unsigned when a
	// server refuses to load it is too late.
	for _, pbo := range pbos {
		sigs, err := filepath.Glob(pbo + ".*.bisign")
		if err != nil {
			return nil, err
		}
		if len(sigs) == 0 {
			return nil, fmt.Errorf("sign: %s reported success but produced no .bisign", filepath.Base(pbo))
		}
	}
	return signed, nil
}

// PBOsIn lists the PBOs in a directory, sorted.
func PBOsIn(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("sign: reading %s: %w", dir, err)
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

// DistributeKey copies the public .bikey into a keys/ directory beside the
// addons, which is what a server admin needs to whitelist the mod.
func DistributeKey(publicKey, modDir string) (string, error) {
	if publicKey == "" {
		return "", fmt.Errorf("sign: the keyring has no public key, so it cannot be distributed")
	}
	if _, err := os.Stat(publicKey); err != nil {
		return "", fmt.Errorf("sign: public key %s: %w", publicKey, err)
	}

	keysDir := filepath.Join(modDir, "keys")
	if err := os.MkdirAll(keysDir, 0o755); err != nil {
		return "", fmt.Errorf("sign: %w", err)
	}
	dst := filepath.Join(keysDir, filepath.Base(publicKey))

	data, err := os.ReadFile(publicKey)
	if err != nil {
		return "", fmt.Errorf("sign: %w", err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return "", fmt.Errorf("sign: %w", err)
	}
	return dst, nil
}

// AssertNoPublicKeys walks root and fails if any .bikey is present.
//
// This is a hard check, not a warning. pboProject writes a keys/ folder
// unconditionally whenever it signs, so a private mod's release would ship its
// public key by default, and publishing that lets anyone whitelist a repack.
func AssertNoPublicKeys(root string) error {
	var found []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.EqualFold(filepath.Ext(path), ".bikey") {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				rel = path
			}
			found = append(found, rel)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("sign: %w", err)
	}
	if len(found) > 0 {
		return fmt.Errorf("sign: this mod is private but its payload contains %d public key(s): %s",
			len(found), strings.Join(found, ", "))
	}
	return nil
}

// RemoveKeysDir deletes a keys/ directory. It undoes the one pboProject creates
// for a mod that should not be distributing its key.
func RemoveKeysDir(modDir string) error {
	keys := filepath.Join(modDir, "keys")
	if _, err := os.Stat(keys); os.IsNotExist(err) {
		return nil
	}
	if err := os.RemoveAll(keys); err != nil {
		return fmt.Errorf("sign: removing %s: %w", keys, err)
	}
	return nil
}
