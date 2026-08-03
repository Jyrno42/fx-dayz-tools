package packer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const ppExe = `C:\Mikero\DePboTools\bin\pboProject.exe`

// defaults mirror what the schema resolves an unset channel to.
func defaultOpts() Options {
	return Options{
		Engine:            "dayz",
		Exclude:           []string{"*.md", "*.tga"},
		Compress:          true,
		Noisy:             true,
		Warnings:          false,
		AutomakeStale:     true,
		CleanTemp:         true,
		NoPrefix:          false,
		BinariseCpp:       false,
		DeletePng:         false,
		ConvertOgg:        false,
		ShrinkP3D:         false,
		DisablePngConvert: false,
		RenameCfgPatches:  false,
	}
}

func job(obfuscate bool) Job {
	return Job{
		AddonName:  "PWE_Core",
		SourceDir:  `P:\projects\project-with-everything\mod\PWE_Core`,
		OutDir:     `P:\_tmp\mod-release\@project-with-everything\Addons`,
		Prefix:     `projects\project-with-everything\mod\PWE_Core`,
		Obfuscate:  obfuscate,
		SignKey:    `P:\keys\project-with-everything.biprivatekey`,
		Excludes:   []string{"*.md", "*.tga"},
		PboProject: defaultOpts(),
	}
}

// Every option that affects output needs an explicit polarity. An omitted flag
// gets inherited from the GUI registry, which is how a build ends up silently
// obfuscating something it should not have.
func TestArgvPolarisesEveryOption(t *testing.T) {
	p := &PboProject{Exe: ppExe}

	argv, err := p.Argv(job(true))
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(argv, " ")

	// Only these get emitted. On 3.91 anything beyond the proven set makes
	// pboProject reject the command line, blank its settings and fall back to its
	// GUI. H and @ are a deliberate addition on top of that set: both change what
	// the engine sees, so inheriting them from the registry is the worse risk.
	for _, letter := range []string{"N", "C", "Z", "J", "B", "O", "H", "@"} {
		hasPlus := contains(argv, "+"+letter)
		hasMinus := contains(argv, "-"+letter)
		if !hasPlus && !hasMinus {
			t.Errorf("option %s is not stated; it would be inherited from the registry\n%s", letter, joined)
		}
		if hasPlus && hasMinus {
			t.Errorf("option %s is stated twice with both polarities\n%s", letter, joined)
		}
	}

	if !contains(argv, "-P") {
		t.Error("-P (do not pause) missing; the packer would block waiting for a keypress")
	}
	// Flags outside the emitted set are deliberately absent, because passing them
	// makes 3.91 reject the whole command line.
	for _, letter := range []string{"R", "W", "D", "G", "T", "$"} {
		if contains(argv, "+"+letter) || contains(argv, "-"+letter) {
			t.Errorf("option %s is emitted, but it is outside the vector known to work", letter)
		}
	}
}

// +$ means "ship with no prefix" on 4.31, the opposite of what the 3.91
// documentation said the same letter did. pboProject refuses to combine it with
// obfuscation or a rename, so the packer refuses first, where the error is
// visible. Getting this wrong ships a PBO the engine cannot address.
func TestArgvRefusesNoPrefixCombinations(t *testing.T) {
	p := &PboProject{Exe: ppExe}

	j := job(true)
	j.PboProject.NoPrefix = true
	if _, err := p.Argv(j); err == nil {
		t.Error("expected an error for no_prefix together with obfuscation")
	}

	j = job(false)
	j.PboProject.NoPrefix = true
	j.Label = "PinkTurtles"
	if _, err := p.Argv(j); err == nil {
		t.Error("expected an error for no_prefix together with a label")
	}

	// On its own it is merely unusual, not refused.
	j = job(false)
	j.PboProject.NoPrefix = true
	if _, err := p.Argv(j); err != nil {
		t.Errorf("no_prefix alone should be allowed: %v", err)
	}
}

// The extensionless $PBOPREFIX$ is deprecated in 4.31, and a deprecation notice
// is a failed pack once warnings are errors.
func TestPreflightWritesTheExtendedPrefixName(t *testing.T) {
	if prefixFileName != "$PBOPREFIX$.txt" {
		t.Fatalf("prefixFileName = %q, want the .txt spelling", prefixFileName)
	}

	src := t.TempDir()
	p := &PboProject{Exe: ppExe}
	j := job(false)
	j.SourceDir = src
	j.OutDir = filepath.Join(t.TempDir(), "Addons")
	j.WritePrefixFile = true

	cleanup, err := p.Preflight(t.Context(), j)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	if _, err := os.Stat(filepath.Join(src, "$PBOPREFIX$.txt")); err != nil {
		t.Errorf("$PBOPREFIX$.txt was not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(src, "$PBOPREFIX$")); !os.IsNotExist(err) {
		t.Error("the deprecated extensionless $PBOPREFIX$ was written")
	}
}

// A repo that committed the old extensionless name keeps it, and does not end up
// with a second prefix file next to it for pboProject to choose between.
func TestPreflightLeavesALegacyPrefixFile(t *testing.T) {
	src := t.TempDir()
	committed := []byte("prefix=upstream\\thing\r\n")
	if err := os.WriteFile(filepath.Join(src, legacyPrefixFileName), committed, 0o644); err != nil {
		t.Fatal(err)
	}

	p := &PboProject{Exe: ppExe}
	j := job(false)
	j.SourceDir = src
	j.OutDir = filepath.Join(t.TempDir(), "Addons")
	j.WritePrefixFile = true

	cleanup, err := p.Preflight(t.Context(), j)
	if err != nil {
		t.Fatal(err)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(src, prefixFileName)); !os.IsNotExist(err) {
		t.Error("a second prefix file was written alongside the committed one")
	}
	got, err := os.ReadFile(filepath.Join(src, legacyPrefixFileName))
	if err != nil || string(got) != string(committed) {
		t.Errorf("the committed prefix file was disturbed: %q, %v", got, err)
	}
}

// The single most important assertion in the package. An addon that should not
// be obfuscated gets an explicit -O, not merely the absence of +O.
func TestArgvEmitsNegativeObfuscate(t *testing.T) {
	p := &PboProject{Exe: ppExe}

	off, err := p.Argv(job(false))
	if err != nil {
		t.Fatal(err)
	}
	if !contains(off, "-O") {
		t.Fatalf("expected an explicit -O for an addon that must not be obfuscated: %v", off)
	}
	if contains(off, "+O") {
		t.Fatal("+O present on an addon that must not be obfuscated")
	}

	on, err := p.Argv(job(true))
	if err != nil {
		t.Fatal(err)
	}
	if !contains(on, "+O") || contains(on, "-O") {
		t.Fatalf("expected +O and no -O: %v", on)
	}

	// The two command lines should differ in exactly that one flag.
	diff := 0
	for i := range on {
		if on[i] != off[i] {
			diff++
		}
	}
	if diff != 1 {
		t.Errorf("obfuscation should change exactly one argument, %d differ:\n on:  %v\n off: %v", diff, on, off)
	}
}

func TestArgvShape(t *testing.T) {
	p := &PboProject{Exe: ppExe}
	argv, err := p.Argv(job(true))
	if err != nil {
		t.Fatal(err)
	}

	if argv[0] != ppExe {
		t.Errorf("argv[0] = %q", argv[0])
	}
	if !contains(argv, "-e=dayz") {
		t.Errorf("engine missing: %v", argv)
	}
	// -M= is the MOD folder and pboProject creates Addons/ inside it, so the
	// Addons directory the pipeline asks for gets stepped up one level.
	if !contains(argv, `-M=P:\_tmp\mod-release\@project-with-everything`) {
		t.Errorf("-M should be the mod folder, not its Addons dir: %v", argv)
	}
	if !contains(argv, `+K=P:\keys\project-with-everything.biprivatekey`) {
		t.Errorf("signing key missing: %v", argv)
	}
	// No embedded quotes. The argument goes straight across without a shell, so
	// quote characters would end up as part of the exclude pattern.
	if !contains(argv, "-X=*.md,*.tga") {
		t.Errorf("exclude list missing or wrongly quoted: %v", argv)
	}
	for _, a := range argv {
		if strings.HasPrefix(a, "-X=") && strings.Contains(a, `"`) {
			t.Errorf("exclude list carries literal quotes, which become part of the value: %q", a)
		}
	}
	// The source folder is positional and immediately follows -P, matching the
	// invocation known to work.
	found := false
	for i, a := range argv {
		if a == "-P" {
			if i+1 >= len(argv) || argv[i+1] != `P:\projects\project-with-everything\mod\PWE_Core` {
				t.Errorf("the source folder must follow -P: %v", argv)
			}
			found = true
			break
		}
	}
	if !found {
		t.Errorf("-P missing: %v", argv)
	}
}

// Not signing has to be stated, or a key lingering in the GUI gets used.
func TestArgvUnsignedIsExplicit(t *testing.T) {
	p := &PboProject{Exe: ppExe}
	j := job(false)
	j.SignKey = ""

	argv, err := p.Argv(j)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(argv, "-K") {
		t.Errorf("expected an explicit -K when not signing: %v", argv)
	}
	for _, a := range argv {
		if strings.HasPrefix(a, "+K") {
			t.Errorf("a signing key was passed for an unsigned pack: %v", argv)
		}
	}
}

func TestPboPathAndModDir(t *testing.T) {
	p := &PboProject{Exe: ppExe}
	j := job(false)

	want := filepath.Join(`P:\_tmp\mod-release\@project-with-everything\Addons`, "PWE_Core.pbo")
	if got := p.PboPath(j); got != want {
		t.Errorf("PboPath = %q, want %q", got, want)
	}
	if got := modDir(j.OutDir); got != `P:\_tmp\mod-release\@project-with-everything` {
		t.Errorf("modDir = %q", got)
	}
	// A directory that is not called Addons is used as-is.
	if got := modDir(`P:\out\somewhere`); got != `P:\out\somewhere` {
		t.Errorf("modDir = %q", got)
	}
}

func TestCaps(t *testing.T) {
	c := (&PboProject{}).Caps()
	if !c.CanObfuscate {
		t.Error("pboProject can obfuscate")
	}
	if !c.CanSignInline {
		t.Error("pboProject signs during packing, so the release must not sign again")
	}
	if !c.StickyOptions {
		t.Error("sticky options must be advertised; it is why the flag vector is complete")
	}
}

// $PBOPREFIX$ gets written for the pack and removed afterwards, leaving the
// source tree the way we found it.
func TestPreflightWritesAndRemovesPrefixFile(t *testing.T) {
	src := t.TempDir()
	out := filepath.Join(t.TempDir(), "Addons")

	p := &PboProject{Exe: ppExe}
	j := job(false)
	j.SourceDir = src
	j.OutDir = out
	j.WritePrefixFile = true
	j.NoScramble = []string{"init.c"}

	cleanup, err := p.Preflight(t.Context(), j)
	if err != nil {
		t.Fatal(err)
	}

	prefixPath := filepath.Join(src, prefixFileName)
	body, err := os.ReadFile(prefixPath)
	if err != nil {
		t.Fatalf("$PBOPREFIX$ was not written: %v", err)
	}
	if !strings.Contains(string(body), j.Prefix) {
		t.Errorf("$PBOPREFIX$ = %q, want it to contain %q", body, j.Prefix)
	}
	if _, err := os.Stat(filepath.Join(src, noScrambleFile)); err != nil {
		t.Errorf("noscramble.lst was not written: %v", err)
	}

	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(prefixPath); !os.IsNotExist(err) {
		t.Error("$PBOPREFIX$ survived cleanup; it would be committed by mistake")
	}
	if _, err := os.Stat(filepath.Join(src, noScrambleFile)); !os.IsNotExist(err) {
		t.Error("noscramble.lst survived cleanup")
	}
	// Cleanup has to be safe to run twice.
	if err := cleanup(); err != nil {
		t.Errorf("second cleanup failed: %v", err)
	}
}

// A repo that commits its own $PBOPREFIX$ keeps it.
func TestPreflightLeavesACommittedPrefixFile(t *testing.T) {
	src := t.TempDir()
	committed := []byte("prefix=upstream\\thing\r\n")
	if err := os.WriteFile(filepath.Join(src, prefixFileName), committed, 0o644); err != nil {
		t.Fatal(err)
	}

	p := &PboProject{Exe: ppExe}
	j := job(false)
	j.SourceDir = src
	j.OutDir = filepath.Join(t.TempDir(), "Addons")
	j.WritePrefixFile = true

	cleanup, err := p.Preflight(t.Context(), j)
	if err != nil {
		t.Fatal(err)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(filepath.Join(src, prefixFileName))
	if err != nil {
		t.Fatalf("the committed $PBOPREFIX$ was removed: %v", err)
	}
	if string(got) != string(committed) {
		t.Errorf("the committed $PBOPREFIX$ was rewritten: %q", got)
	}
}

func TestArgvRejectsIncomplete(t *testing.T) {
	if _, err := (&PboProject{}).Argv(job(false)); err == nil {
		t.Error("expected an error when pboProject is not configured")
	}
	p := &PboProject{Exe: ppExe}
	j := job(false)
	j.SourceDir = ""
	if _, err := p.Argv(j); err == nil {
		t.Error("expected an error with no source directory")
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
