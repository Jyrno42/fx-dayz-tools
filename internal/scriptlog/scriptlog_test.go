package scriptlog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func scan(t *testing.T, log string, opts Options) Report {
	t.Helper()
	rep, err := Scan(strings.NewReader(log), opts)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestCleanLogPasses(t *testing.T) {
	log := `SCRIPT       : Module: Game; loaded 527x files; 1617x classes; defines: "DAYZ_1_29"
SCRIPT       : [PWE] ProjectWithEverything 0.3.0 loaded, 16 entries in registry
SCRIPT       : Mission loaded
`
	rep := scan(t, log, Options{})
	if !rep.OK() {
		t.Fatalf("a clean log should pass, got hits: %+v", rep.Hits)
	}
	if len(rep.Modules) != 1 {
		t.Errorf("modules = %d, want 1", len(rep.Modules))
	}
}

func TestEachErrorPatternIsCaught(t *testing.T) {
	for _, pattern := range DefaultErrorPatterns {
		t.Run(pattern, func(t *testing.T) {
			log := "SCRIPT (E): " + pattern + " something something\n"
			rep := scan(t, log, Options{})
			if rep.OK() {
				t.Fatalf("%q was not caught", pattern)
			}
			if len(rep.Hits) != 1 {
				t.Fatalf("hits = %d, want 1", len(rep.Hits))
			}
			if rep.Hits[0].Line != 1 {
				t.Errorf("line = %d, want 1", rep.Hits[0].Line)
			}
		})
	}
}

// These appear in the thousands on every boot from the local test missions and
// have nothing to do with any mod.
func TestNoiseIsIgnored(t *testing.T) {
	var log strings.Builder
	for i := 0; i < 100; i++ {
		log.WriteString("SCRIPT (E): Scripted variables corrupted\n")
		log.WriteString("SCRIPT (E): trying to set muzzle index\n")
	}
	rep := scan(t, log.String(), Options{})
	if !rep.OK() {
		t.Fatalf("noise should not fail the check, got %d hits", len(rep.Hits))
	}
}

// A noise line that happens to contain an error word is still noise.
func TestNoiseWinsOverErrors(t *testing.T) {
	log := "SCRIPT (E): Scripted variables corrupted - Bad type in something\n"
	rep := scan(t, log, Options{})
	if !rep.OK() {
		t.Errorf("noise should win over an error substring, got %+v", rep.Hits)
	}
}

func TestRealErrorSurvivesNoise(t *testing.T) {
	log := `SCRIPT (E): Scripted variables corrupted
SCRIPT (E): trying to set muzzle index
SCRIPT (E): Undefined function 'PWE_Gone'
SCRIPT (E): Scripted variables corrupted
`
	rep := scan(t, log, Options{})
	if rep.OK() {
		t.Fatal("a real error was lost among the noise")
	}
	if len(rep.Hits) != 1 {
		t.Fatalf("hits = %d, want 1: %+v", len(rep.Hits), rep.Hits)
	}
	if rep.Hits[0].Line != 3 {
		t.Errorf("line = %d, want 3", rep.Hits[0].Line)
	}
}

func TestMatchingIsCaseInsensitive(t *testing.T) {
	rep := scan(t, "SCRIPT (E): UNDEFINED FUNCTION 'x'\n", Options{})
	if rep.OK() {
		t.Error("matching should ignore case")
	}
}

func TestExtraPatterns(t *testing.T) {
	log := "SCRIPT (E): something project-specific went wrong\n"

	if rep := scan(t, log, Options{}); !rep.OK() {
		t.Fatal("this line should not match by default")
	}
	rep := scan(t, log, Options{ExtraErrorPatterns: []string{"project-specific"}})
	if rep.OK() {
		t.Error("an extra error pattern was not applied")
	}

	// And an extra noise pattern should silence a default error.
	noisy := "SCRIPT (E): Bad type in the vendor mod we do not control\n"
	rep = scan(t, noisy, Options{ExtraNoisePatterns: []string{"vendor mod"}})
	if !rep.OK() {
		t.Error("an extra noise pattern was not applied")
	}
}

// Module lines carry a defines list running to several hundred characters, and
// only the head of it is useful.
func TestModuleSummaryIsTrimmed(t *testing.T) {
	log := `SCRIPT       : Module: World; loaded 2262x files; 5978x classes; used 9649/33554 kB (28%); defines: "DAYZ_1_29,SERVER_FOR_WINDOWS,SERVER,PLATFORM_WINDOWS,RELEASE"
`
	rep := scan(t, log, Options{})
	if len(rep.Modules) != 1 {
		t.Fatalf("modules = %d, want 1", len(rep.Modules))
	}
	got := rep.Modules[0].Summary
	if !strings.Contains(got, "Module: World") || !strings.Contains(got, "2262x files") {
		t.Errorf("summary lost the useful part: %q", got)
	}
	if strings.Contains(got, "defines:") {
		t.Errorf("summary should drop the defines list: %q", got)
	}
}

// A module line should never read as an error, whatever it happens to contain.
func TestModuleLinesAreNotErrors(t *testing.T) {
	log := "SCRIPT       : Module: Game; loaded 1x files; Bad type\n"
	rep := scan(t, log, Options{})
	if !rep.OK() {
		t.Errorf("a module line should not count as an error: %+v", rep.Hits)
	}
}

func TestLongLinesAreHandled(t *testing.T) {
	// Well past bufio's default 64 KiB limit.
	long := "SCRIPT (E): Undefined function " + strings.Repeat("x", 200_000) + "\n"
	rep := scan(t, long, Options{})
	if rep.OK() {
		t.Error("a very long line should still be scanned")
	}
}

func TestEmptyLog(t *testing.T) {
	rep := scan(t, "", Options{})
	if !rep.OK() {
		t.Error("an empty log has no errors")
	}
	if rep.Lines != 0 {
		t.Errorf("lines = %d, want 0", rep.Lines)
	}
}

func TestWindowsLineEndings(t *testing.T) {
	rep := scan(t, "SCRIPT (E): Undefined function 'x'\r\n", Options{})
	if rep.OK() {
		t.Fatal("CRLF input was not scanned")
	}
	if strings.HasSuffix(rep.Hits[0].Text, "\r") {
		t.Error("the carriage return should be trimmed from the reported text")
	}
}

// --- log selection --------------------------------------------------------

func writeLog(t *testing.T, dir, name string, mod time.Time) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("SCRIPT: ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFindNewestPicksTheLatest(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	writeLog(t, dir, "script_2026-07-27_10-00-00.log", now.Add(-2*time.Hour))
	want := writeLog(t, dir, "script_2026-07-28_10-00-00.log", now)

	got, err := FindNewest(dir, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("FindNewest = %q, want %q", got, want)
	}
}

// The important one. If the server dies before writing a log, an unconditional
// "newest log" search picks up the PREVIOUS run's log and reports a pass. A
// false success on exactly the failure you most need to catch.
func TestFindNewestIgnoresLogsFromBeforeThisRun(t *testing.T) {
	dir := t.TempDir()
	writeLog(t, dir, "script_2026-07-27_10-00-00.log", time.Now().Add(-2*time.Hour))

	started := time.Now()
	_, err := FindNewest(dir, started)
	if err == nil {
		t.Fatal("a stale log must not be accepted as this run's result")
	}
	var noLog *ErrNoLog
	if !asErrNoLog(err, &noLog) {
		t.Fatalf("expected ErrNoLog, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "config validation") {
		t.Errorf("the error should explain the likely cause, got: %v", err)
	}
}

func TestFindNewestAcceptsAFreshLog(t *testing.T) {
	dir := t.TempDir()
	started := time.Now()
	want := writeLog(t, dir, "script_2026-07-28_10-00-00.log", started.Add(2*time.Second))

	got, err := FindNewest(dir, started)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("FindNewest = %q, want %q", got, want)
	}
}

func TestFindNewestIgnoresOtherFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"crash_2026.log", "DayZServer_x64.RPT", "notes.txt"} {
		writeLog(t, dir, name, time.Now())
	}
	if _, err := FindNewest(dir, time.Time{}); err == nil {
		t.Error("only script_*.log files should be considered")
	}
}

func TestFindNewestMissingDir(t *testing.T) {
	if _, err := FindNewest(filepath.Join(t.TempDir(), "nope"), time.Time{}); err == nil {
		t.Error("a missing profile directory should be an error")
	}
}

func asErrNoLog(err error, target **ErrNoLog) bool {
	if e, ok := err.(*ErrNoLog); ok {
		*target = e
		return true
	}
	return false
}
