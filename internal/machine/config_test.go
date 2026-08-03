package machine

import (
	"strings"
	"testing"
)

// Rejecting unknown fields is what stops a typo leaving a tool quietly
// undiscovered. The cost is that one dead key from an older config stops
// everything, so the message has to be worth reading: yaml.v3's own wording
// names a Go type the reader has never heard of and gives no way forward.
func TestUnknownKeyIsExplained(t *testing.T) {
	cfg := `
version: 1
paths:
  mikero: C:\Mikero\DePboTools\bin
  inkscape: C:\Program Files\Inkscape\bin\inkscape.exe
`
	_, err := Parse([]byte(cfg))
	if err == nil {
		t.Fatal("expected an error for a key the schema has no home for")
	}

	msg := err.Error()
	for _, want := range []string{`"inkscape"`, "line 5", "Delete the line"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message should contain %q, got: %s", want, msg)
		}
	}
	// The Go type name is an implementation detail and means nothing to whoever
	// is reading their own config file.
	if strings.Contains(msg, "machine.Paths") {
		t.Errorf("message leaks the Go type name: %s", msg)
	}
}

// A config that is merely missing is a different problem, and `config init` is
// the right advice only for that one.
func TestMissingConfigIsNotAParseError(t *testing.T) {
	_, err := Load(`Z:\nowhere\dayzmod\config.yml`)
	if err == nil {
		t.Fatal("expected an error for a config that does not exist")
	}
	if !strings.Contains(err.Error(), "no configuration found") {
		t.Errorf("expected ErrNotConfigured, got: %v", err)
	}
}
