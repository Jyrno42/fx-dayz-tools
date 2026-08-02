package scriptlog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func appendTo(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

// A log that does not exist yet is not an error. The server creates it a moment
// after starting, and the caller polls until it turns up.
func TestTailerMissingFileIsNotAnError(t *testing.T) {
	tl := NewTailer(filepath.Join(t.TempDir(), "script_x.log"), Options{})
	rep, err := tl.Poll()
	if err != nil {
		t.Fatalf("a missing log should poll cleanly, got %v", err)
	}
	if len(rep.Hits) != 0 || rep.Lines != 0 {
		t.Errorf("expected an empty report, got %+v", rep)
	}
}

// Each poll returns only what is new, so a caller can react the moment an error
// gets written instead of re-reading the whole file.
func TestTailerReturnsOnlyNewLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "script_x.log")
	tl := NewTailer(path, Options{})

	appendTo(t, path, "SCRIPT: starting\n")
	rep, err := tl.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Lines != 1 || len(rep.Hits) != 0 {
		t.Fatalf("first poll = %+v", rep)
	}

	// Nothing new.
	rep, err = tl.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Hits) != 0 {
		t.Errorf("an idle poll should report nothing new, got %+v", rep.Hits)
	}

	appendTo(t, path, "SCRIPT (E): Undefined function 'Gone'\n")
	rep, err = tl.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Hits) != 1 {
		t.Fatalf("expected the new error, got %+v", rep.Hits)
	}
	if rep.Hits[0].Line != 2 {
		t.Errorf("line = %d, want 2 (numbering continues across polls)", rep.Hits[0].Line)
	}

	// And it is not reported twice.
	rep, err = tl.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Hits) != 0 {
		t.Errorf("the same error was reported again: %+v", rep.Hits)
	}
}

// A line split across two writes has to be scanned once, whole. Otherwise a
// half-written error line gets missed or double-counted.
func TestTailerHandlesPartialLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "script_x.log")
	tl := NewTailer(path, Options{})

	appendTo(t, path, "SCRIPT (E): Undefined fun")
	rep, err := tl.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Hits) != 0 {
		t.Fatalf("a partial line must not be scanned yet, got %+v", rep.Hits)
	}

	appendTo(t, path, "ction 'Gone'\n")
	rep, err = tl.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Hits) != 1 {
		t.Fatalf("the completed line should be caught, got %+v", rep.Hits)
	}
	if !strings.Contains(rep.Hits[0].Text, "Undefined function") {
		t.Errorf("line was not reassembled: %q", rep.Hits[0].Text)
	}
}

// A replaced (shorter) file means a new run, so start over instead of seeking
// past the end and reading nothing forever.
func TestTailerRestartsOnTruncation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "script_x.log")
	tl := NewTailer(path, Options{})

	appendTo(t, path, "SCRIPT: a long first run line\nSCRIPT: another\n")
	if _, err := tl.Poll(); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("SCRIPT (E): Bad type\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := tl.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Hits) != 1 {
		t.Errorf("a truncated log should be re-read from the start, got %+v", rep.Hits)
	}
}

func TestTailerNoiseAndModules(t *testing.T) {
	path := filepath.Join(t.TempDir(), "script_x.log")
	tl := NewTailer(path, Options{})

	appendTo(t, path, "SCRIPT       : Module: Game; loaded 5x files; defines: \"X\"\n")
	appendTo(t, path, "SCRIPT (E): Scripted variables corrupted\n")
	rep, err := tl.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Modules) != 1 {
		t.Errorf("modules = %d, want 1", len(rep.Modules))
	}
	if len(rep.Hits) != 0 {
		t.Errorf("noise should stay filtered while tailing, got %+v", rep.Hits)
	}
}

func TestAccumulator(t *testing.T) {
	var acc Accumulator
	acc.Add(Report{Path: "a.log", Lines: 2, Hits: []Hit{{Line: 1, Text: "one"}}})
	acc.Add(Report{Lines: 5, Hits: []Hit{{Line: 4, Text: "two"}}, Modules: []Module{{Summary: "m"}}})

	if acc.Report.Path != "a.log" {
		t.Errorf("path = %q", acc.Report.Path)
	}
	if len(acc.Report.Hits) != 2 {
		t.Errorf("hits = %d, want 2", len(acc.Report.Hits))
	}
	if acc.Report.Lines != 5 {
		t.Errorf("lines = %d, want the running total 5", acc.Report.Lines)
	}
	if len(acc.Report.Modules) != 1 {
		t.Errorf("modules = %d, want 1", len(acc.Report.Modules))
	}
}
