package scriptlog

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// Tailer reads a script log incrementally, so a long wait can react the moment
// an error is written instead of re-reading the whole file on every poll.
type Tailer struct {
	path string
	opts Options
	off  int64
	line int

	errorPatterns []string
	noisePatterns []string

	// carry holds a trailing partial line, so a line split across two polls is
	// scanned once, whole.
	carry string
}

// NewTailer starts tailing path from the beginning.
func NewTailer(path string, opts Options) *Tailer {
	return &Tailer{
		path:          path,
		opts:          opts,
		errorPatterns: lower(append(append([]string(nil), DefaultErrorPatterns...), opts.ExtraErrorPatterns...)),
		noisePatterns: lower(append(append([]string(nil), DefaultNoisePatterns...), opts.ExtraNoisePatterns...)),
	}
}

// Path returns the log being tailed.
func (t *Tailer) Path() string { return t.path }

// Poll reads whatever has been appended since the last call and returns the
// modules and error lines found in it.
//
// A file that does not exist yet is not an error. The server creates its log a
// moment after starting, and callers poll until it turns up.
func (t *Tailer) Poll() (Report, error) {
	rep := Report{Path: t.path}

	f, err := os.Open(t.path)
	if os.IsNotExist(err) {
		return rep, nil
	}
	if err != nil {
		return rep, fmt.Errorf("scriptlog: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return rep, fmt.Errorf("scriptlog: %w", err)
	}
	// A shrinking file means it got replaced, so start over instead of seeking
	// past the end and reading nothing forever.
	if info.Size() < t.off {
		t.off, t.line, t.carry = 0, 0, ""
	}
	if info.Size() == t.off {
		rep.Lines = t.line
		return rep, nil
	}

	if _, err := f.Seek(t.off, io.SeekStart); err != nil {
		return rep, fmt.Errorf("scriptlog: %w", err)
	}

	chunk, err := io.ReadAll(f)
	if err != nil {
		return rep, fmt.Errorf("scriptlog: %w", err)
	}
	t.off += int64(len(chunk))

	text := t.carry + string(chunk)
	t.carry = ""
	// Hold back an unterminated final line until the rest of it arrives.
	if !strings.HasSuffix(text, "\n") {
		if i := strings.LastIndex(text, "\n"); i >= 0 {
			t.carry = text[i+1:]
			text = text[:i+1]
		} else {
			t.carry = text
			text = ""
		}
	}

	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		t.line++
		line := strings.TrimRight(sc.Text(), "\r")

		if mod, ok := moduleOf(line); ok {
			rep.Modules = append(rep.Modules, mod)
			continue
		}
		lowered := strings.ToLower(line)
		if containsAny(lowered, t.noisePatterns) {
			continue
		}
		if pattern, ok := firstMatch(lowered, t.noisePatterns, t.errorPatterns); ok {
			rep.Hits = append(rep.Hits, Hit{Line: t.line, Text: strings.TrimSpace(line), Pattern: pattern})
		}
	}
	rep.Lines = t.line
	return rep, sc.Err()
}

// Accumulator collects successive Poll results into one report.
type Accumulator struct {
	Report Report
}

// Add merges a poll result.
func (a *Accumulator) Add(r Report) {
	if r.Path != "" {
		a.Report.Path = r.Path
	}
	a.Report.Modules = append(a.Report.Modules, r.Modules...)
	a.Report.Hits = append(a.Report.Hits, r.Hits...)
	if r.Lines > a.Report.Lines {
		a.Report.Lines = r.Lines
	}
}
