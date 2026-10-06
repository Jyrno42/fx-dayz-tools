package packer

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeTestPBO(t *testing.T, props []string, files map[string]string, order []string) string {
	t.Helper()
	var b bytes.Buffer
	entry := func(name string, mime, size uint32) {
		b.WriteString(name)
		b.WriteByte(0)
		binary.Write(&b, binary.LittleEndian, [5]uint32{mime, size, 0, 0, size})
	}
	entry("", pboVersionMime, 0)
	for _, p := range props {
		b.WriteString(p)
		b.WriteByte(0)
	}
	b.WriteByte(0)
	for _, name := range order {
		entry(name, 0, uint32(len(files[name])))
	}
	entry("", 0, 0)
	for _, name := range order {
		b.WriteString(files[name])
	}
	path := filepath.Join(t.TempDir(), "test.pbo")
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPboEntries(t *testing.T) {
	files := map[string]string{
		`config.cpp`:               "class CfgPatches {};",
		`Scripts\4_World\Cannon.c`: "class Cannon {};",
		`data\fx_cannon_01_co.paa`: "\x00\x01binary",
	}
	order := []string{`config.cpp`, `Scripts\4_World\Cannon.c`, `data\fx_cannon_01_co.paa`}
	// An empty property value must not end the property list early.
	path := writeTestPBO(t, []string{"prefix", "fx_cannon_01", "version", "", "product", "dayz ugc"}, files, order)

	got, err := pboEntries(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, order) {
		t.Fatalf("pboEntries = %q, want %q", got, order)
	}
}

func TestPboEntriesTruncated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.pbo")
	if err := os.WriteFile(path, []byte("config.cpp\x00\x01\x02"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := pboEntries(path); err == nil {
		t.Fatal("expected an error for a truncated header")
	}
}
