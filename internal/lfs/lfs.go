// Package lfs detects Git LFS pointer stubs.
//
// A fresh clone without `git lfs pull` leaves every tracked binary as a small
// text file. Pack that and you get a PBO full of pointer stubs instead of models
// and textures. It builds cleanly, signs cleanly, and is broken in a way nothing
// else notices.
package lfs

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// pointerHeader begins every LFS pointer file.
var pointerHeader = []byte("version https://git-lfs.github.com/spec/v1")

// pointerMax is a generous ceiling on a pointer file's size. Real assets are far
// larger, so anything bigger than this cannot be a stub.
const pointerMax = 1024

// binaryExtensions are the asset types these repos route through LFS. Limiting
// the scan to them keeps it cheap on a large addon.
var binaryExtensions = map[string]bool{
	".p3d": true, ".paa": true, ".edds": true, ".dds": true, ".rtm": true,
	".fbx": true, ".ogg": true, ".wss": true, ".wrp": true, ".tga": true,
	".png": true, ".jpg": true, ".psd": true, ".bik": true,
}

// Stub is one file that turned out to be a pointer instead of real content.
type Stub struct {
	// Path is relative to the scanned root, slash-separated.
	Path string
}

// Scan walks dir and reports every LFS pointer stub among its asset files.
func Scan(dir string) ([]Stub, error) {
	var stubs []Stub

	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		if !binaryExtensions[strings.ToLower(filepath.Ext(path))] {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > pointerMax {
			return nil
		}

		isStub, err := isPointer(path)
		if err != nil {
			return err
		}
		if isStub {
			rel, relErr := filepath.Rel(dir, path)
			if relErr != nil {
				rel = path
			}
			stubs = append(stubs, Stub{Path: filepath.ToSlash(rel)})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("lfs: scanning %s: %w", dir, err)
	}
	return stubs, nil
}

func isPointer(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	buf := make([]byte, len(pointerHeader))
	n, err := io.ReadFull(f, buf)
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return bytes.Equal(buf[:n], pointerHeader), nil
}

// Describe renders stubs for an error message, capped so a fresh clone with
// hundreds of them stays readable.
func Describe(stubs []Stub, max int) string {
	var b strings.Builder
	for i, s := range stubs {
		if i == max {
			fmt.Fprintf(&b, "\n  ... and %d more", len(stubs)-max)
			break
		}
		fmt.Fprintf(&b, "\n  %s", s.Path)
	}
	return b.String()
}
