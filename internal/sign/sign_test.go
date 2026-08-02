package sign

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const dsExe = `C:\DayZ Tools\Bin\DsUtils\DSSignFile.exe`

func TestArgv(t *testing.T) {
	s := &Signer{Exe: dsExe}

	got, err := s.Argv(`P:\out\PWE_Core.pbo`, Options{PrivateKey: `P:\keys\k.biprivatekey`})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{dsExe, `P:\keys\k.biprivatekey`, `P:\out\PWE_Core.pbo`}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("argv[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	got, _ = s.Argv(`P:\out\x.pbo`, Options{PrivateKey: `k`, V2: true})
	if got[len(got)-1] != "-v2" {
		t.Errorf("v2 flag missing: %v", got)
	}
}

func TestArgvRejectsIncomplete(t *testing.T) {
	if _, err := (&Signer{}).Argv("x.pbo", Options{PrivateKey: "k"}); err == nil {
		t.Error("expected an error when DSSignFile is not configured")
	}
	if _, err := (&Signer{Exe: dsExe}).Argv("x.pbo", Options{}); err == nil {
		t.Error("expected an error when no key is given")
	}
}

func TestPBOsIn(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"b.pbo", "a.pbo", "notes.txt", "a.pbo.k.bisign"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := PBOsIn(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("found %d PBOs, want 2: %v", len(got), got)
	}
	if filepath.Base(got[0]) != "a.pbo" {
		t.Errorf("results should be sorted, got %v", got)
	}
}

// Publishing a private mod's public key lets anyone whitelist a repack of it,
// so this is a hard check and not a warning.
func TestAssertNoPublicKeys(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.pbo"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AssertNoPublicKeys(dir); err != nil {
		t.Fatalf("a payload with no keys should pass, got %v", err)
	}

	keys := filepath.Join(dir, "keys")
	if err := os.MkdirAll(keys, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keys, "leaked.bikey"), []byte("k"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := AssertNoPublicKeys(dir)
	if err == nil {
		t.Fatal("a stray .bikey must be caught")
	}
	if !strings.Contains(err.Error(), "leaked.bikey") {
		t.Errorf("the error should name the offending file, got: %v", err)
	}
}

// pboProject writes keys/ unconditionally when signing, so a private mod's
// release has to undo it.
func TestRemoveKeysDir(t *testing.T) {
	dir := t.TempDir()
	keys := filepath.Join(dir, "keys")
	if err := os.MkdirAll(keys, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keys, "k.bikey"), []byte("k"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RemoveKeysDir(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(keys); !os.IsNotExist(err) {
		t.Error("keys/ should be gone")
	}
	// Idempotent, so having nothing to remove is not an error.
	if err := RemoveKeysDir(dir); err != nil {
		t.Errorf("removing an absent keys/ should succeed, got %v", err)
	}
}

func TestDistributeKey(t *testing.T) {
	dir := t.TempDir()
	pub := filepath.Join(dir, "signing-key.bikey")
	if err := os.WriteFile(pub, []byte("public"), 0o644); err != nil {
		t.Fatal(err)
	}
	mod := filepath.Join(dir, "@mod")

	dst, err := DistributeKey(pub, mod)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dst) != "signing-key.bikey" {
		t.Errorf("dst = %q", dst)
	}
	if _, err := os.Stat(filepath.Join(mod, "keys", "signing-key.bikey")); err != nil {
		t.Errorf("the key should land in keys/: %v", err)
	}

	if _, err := DistributeKey("", mod); err == nil {
		t.Error("a keyring with no public key cannot be distributed")
	}
}
