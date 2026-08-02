// Package hashing implements the directory content hash used for build change
// detection, plus the on-disk lockfile that stores it.
package hashing

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// algoTag is mixed into every digest, so changing the algorithm below
// automatically invalidates existing lockfiles. Bump it on any change to what
// gets hashed or how it is composed. The only cost is one forced rebuild.
//
// v1 replaced the nushell hash_utils.nu implementation and is deliberately NOT
// compatible with it, since the old digests used host-specific separators and a
// stray leading separator. Migrating a repo forces exactly one rebuild of each
// addon, which is expected and harmless.
const algoTag = "dayzmod-dirhash-v1"

// Options tunes DirHash. The zero value is correct for normal use.
type Options struct {
	// Workers caps concurrent file reads. Zero means runtime.NumCPU().
	Workers int
	// Skip reports whether a path, relative to the hashed root and always
	// slash-separated, should be excluded. Nil hashes everything.
	Skip func(rel string) bool
}

func (o Options) workers() int {
	if o.Workers > 0 {
		return o.Workers
	}
	return runtime.NumCPU()
}

// DirHash returns a hex digest covering every regular file under dir, taking in
// each file's path relative to dir and its exact bytes.
//
// Properties that matter:
//   - Paths are relative and slash-separated, so a digest comes out identical
//     whether the addon was hashed from P:\projects\... or E:\projects\... .
//   - Directories contribute nothing on their own, so an empty one is invisible.
//   - Symlinks get skipped instead of followed, so a link cannot smuggle content
//     in from outside the tree or create a cycle.
//   - File contents are read concurrently, but composition is strictly ordered by
//     relative path, which keeps the digest deterministic.
func DirHash(dir string, opts Options) (string, error) {
	files, err := collect(dir, opts.Skip)
	if err != nil {
		return "", err
	}
	sort.Strings(files)

	sums, err := hashFiles(dir, files, opts.workers())
	if err != nil {
		return "", err
	}

	h := sha256.New()
	fmt.Fprintf(h, "%s\n", algoTag)
	for _, rel := range files {
		fmt.Fprintf(h, "%s:%s\n", rel, sums[rel])
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// collect walks dir and returns every regular file as a slash-separated path
// relative to dir.
func collect(dir string, skip func(string) bool) ([]string, error) {
	var out []string

	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if skip != nil && skip(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		// Skips symlinks, devices and sockets, i.e. anything that is not content.
		if !d.Type().IsRegular() {
			return nil
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("hashing: walk %s: %w", dir, err)
	}
	return out, nil
}

// hashFiles digests each file concurrently, returning rel path -> hex digest.
func hashFiles(dir string, files []string, workers int) (map[string]string, error) {
	if workers > len(files) {
		workers = len(files)
	}

	var (
		mu   sync.Mutex
		sums = make(map[string]string, len(files))

		errMu   sync.Mutex
		firstEr error

		next = make(chan string)
		wg   sync.WaitGroup
	)

	fail := func(err error) {
		errMu.Lock()
		defer errMu.Unlock()
		if firstEr == nil {
			firstEr = err
		}
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for rel := range next {
				sum, err := fileHash(filepath.Join(dir, filepath.FromSlash(rel)))
				if err != nil {
					fail(err)
					continue
				}
				mu.Lock()
				sums[rel] = sum
				mu.Unlock()
			}
		}()
	}

	for _, rel := range files {
		next <- rel
	}
	close(next)
	wg.Wait()

	if firstEr != nil {
		return nil, firstEr
	}
	return sums, nil
}

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("hashing: open %s: %w", path, err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hashing: read %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// SkipNames returns a Skip func that drops any path whose final element matches
// one of names. It keeps build scratch out of an addon's digest.
func SkipNames(names ...string) func(string) bool {
	set := make(map[string]struct{}, len(names))
	for _, n := range names {
		set[n] = struct{}{}
	}
	return func(rel string) bool {
		base := rel
		if i := strings.LastIndex(rel, "/"); i >= 0 {
			base = rel[i+1:]
		}
		_, ok := set[base]
		return ok
	}
}
