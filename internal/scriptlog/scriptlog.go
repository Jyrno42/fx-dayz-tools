// Package scriptlog scans a DayZ server script log for Enforce errors.
//
// Enforce compiles when the server starts, so booting the server is the only way
// to find out whether the scripts compile at all. This package is the gate that
// turns that into a pass or a fail.
package scriptlog

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DefaultErrorPatterns are the substrings that mean a real problem, i.e. a
// script that did not compile or a symbol that does not resolve.
//
// Matching is a plain case-insensitive substring test rather than a regular
// expression, so a pattern cannot accidentally match more than it says.
var DefaultErrorPatterns = []string{
	"Compiler Error",
	"Can't compile",
	"Undefined variable",
	"Undefined function",
	"Unknown class",
	"is not a member",
	"Bad type",
	"Error at line",
}

// DefaultNoisePatterns are pre-existing complaints from the local test missions
// that have nothing to do with any mod. They appear in the thousands on every
// boot and would drown out anything real.
var DefaultNoisePatterns = []string{
	"Scripted variables corrupted",
	"trying to set muzzle index",
}

// modulePrefix identifies the lines reporting a compiled script module.
const modulePrefix = "SCRIPT"

// Module is one compiled script module.
type Module struct {
	// Summary is the module name and file count, i.e. the useful head of the
	// line minus the defines list, which runs to several hundred characters.
	Summary string
}

// Hit is one line that matched an error pattern.
type Hit struct {
	Line    int
	Text    string
	Pattern string
}

// Report is the outcome of a scan.
type Report struct {
	Path    string
	Modules []Module
	Hits    []Hit
	Lines   int
}

// OK reports whether the scripts compiled without errors.
func (r Report) OK() bool { return len(r.Hits) == 0 }

// Options tunes a scan.
type Options struct {
	// ExtraErrorPatterns are added to the defaults.
	ExtraErrorPatterns []string
	// ExtraNoisePatterns are added to the defaults.
	ExtraNoisePatterns []string
}

// Scan reads a script log and reports compiled modules and error lines.
func Scan(r io.Reader, opts Options) (Report, error) {
	errorPatterns := lower(append(append([]string(nil), DefaultErrorPatterns...), opts.ExtraErrorPatterns...))
	noisePatterns := lower(append(append([]string(nil), DefaultNoisePatterns...), opts.ExtraNoisePatterns...))

	var rep Report

	sc := bufio.NewScanner(r)
	// Module lines carry the full defines list and run well past the default
	// 64 KiB limit.
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for sc.Scan() {
		rep.Lines++
		line := strings.TrimRight(sc.Text(), "\r")
		lowered := strings.ToLower(line)

		if mod, ok := moduleOf(line); ok {
			rep.Modules = append(rep.Modules, mod)
			continue
		}

		// Noise wins over errors. A noise line that happens to contain an error
		// word is still noise.
		if containsAny(lowered, noisePatterns) {
			continue
		}
		if pattern, ok := firstMatch(lowered, noisePatterns, errorPatterns); ok {
			rep.Hits = append(rep.Hits, Hit{Line: rep.Lines, Text: strings.TrimSpace(line), Pattern: pattern})
		}
	}
	if err := sc.Err(); err != nil {
		return rep, fmt.Errorf("scriptlog: reading log: %w", err)
	}
	return rep, nil
}

// ScanFile scans a log file.
func ScanFile(path string, opts Options) (Report, error) {
	f, err := os.Open(path)
	if err != nil {
		return Report{}, fmt.Errorf("scriptlog: %w", err)
	}
	defer f.Close()

	rep, err := Scan(f, opts)
	rep.Path = path
	return rep, err
}

// moduleOf extracts a compiled-module line, trimmed to its useful head.
func moduleOf(line string) (Module, bool) {
	if !strings.Contains(line, modulePrefix) || !strings.Contains(line, "Module:") {
		return Module{}, false
	}
	// The line is semicolon-separated. The first two fields are the module name
	// and its file count, and the rest is the defines list.
	parts := strings.Split(line, ";")
	head := parts[0]
	if len(parts) > 1 {
		head += ";" + parts[1]
	}
	return Module{Summary: strings.TrimSpace(head)}, true
}

func firstMatch(lowered string, noise, errors []string) (string, bool) {
	for _, p := range errors {
		if strings.Contains(lowered, p) {
			return p, true
		}
	}
	return "", false
}

func containsAny(lowered string, patterns []string) bool {
	for _, p := range patterns {
		if strings.Contains(lowered, p) {
			return true
		}
	}
	return false
}

func lower(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, strings.ToLower(s))
		}
	}
	return out
}

// ErrNoLog means no script log was produced.
type ErrNoLog struct {
	Dir   string
	Since time.Time
}

func (e *ErrNoLog) Error() string {
	if e.Since.IsZero() {
		return fmt.Sprintf("scriptlog: no script_*.log in %s -- did the server start?", e.Dir)
	}
	return fmt.Sprintf("scriptlog: no script_*.log written in %s since the server was started -- "+
		"it produced no log at all, which usually means it terminated during config validation, before mission init",
		e.Dir)
}

// FindNewest returns the most recent script log in a server profile directory.
//
// When since is non-zero, only logs written at or after that moment count. That
// matters more than it sounds. If the server dies before it can write a log, an
// unconditional "newest log" search quietly picks up the PREVIOUS run's log and
// reports a pass. Demanding a fresh log turns that false success back into the
// failure it actually is.
func FindNewest(profileDir string, since time.Time) (string, error) {
	entries, err := os.ReadDir(profileDir)
	if err != nil {
		return "", fmt.Errorf("scriptlog: reading %s: %w", profileDir, err)
	}

	type candidate struct {
		path string
		mod  time.Time
	}
	var found []candidate

	for _, e := range entries {
		if e.IsDir() || !isScriptLog(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		// A second of slack. The log appears moments after launch, and file
		// timestamps are not always finer-grained than that.
		if !since.IsZero() && info.ModTime().Before(since.Add(-time.Second)) {
			continue
		}
		found = append(found, candidate{filepath.Join(profileDir, e.Name()), info.ModTime()})
	}

	if len(found) == 0 {
		return "", &ErrNoLog{Dir: profileDir, Since: since}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].mod.After(found[j].mod) })
	return found[0].path, nil
}

func isScriptLog(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasPrefix(lower, "script_") && strings.HasSuffix(lower, ".log")
}
