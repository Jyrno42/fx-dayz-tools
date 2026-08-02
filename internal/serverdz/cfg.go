// Package serverdz reads and patches the dedicated server config.
package serverdz

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// instanceIDRe matches `instanceId = 1;` with any spacing.
var instanceIDRe = regexp.MustCompile(`(?im)^\s*instanceId\s*=\s*(-?\d+)\s*;`)

// battlEyeRe matches `BattlEye = 1;` with any spacing and casing.
var battlEyeRe = regexp.MustCompile(`(?im)^(\s*BattlEye\s*=\s*)(\d+)(\s*;)`)

// HasInstanceID reports whether the config sets a usable instanceId.
//
// Without a valid 32-bit instanceId the server does a graceful
// config-validation termination about ten seconds in, BEFORE mission init.
// Nothing in the logs points at the cause, and the whole thing looks exactly
// like a mod failing to load.
func HasInstanceID(cfg string) bool {
	for _, m := range instanceIDRe.FindAllStringSubmatch(StripComments(cfg), -1) {
		if v, err := strconv.ParseInt(m[1], 10, 64); err == nil && v >= 0 && v <= 0xFFFFFFFF {
			return true
		}
	}
	return false
}

// SetBattlEye rewrites the BattlEye setting, returning the new config and
// whether anything changed.
//
// A diag client can only join a server with BattlEye off, which is why this
// exists at all. BattlEye is configured per repo as its own setting instead of
// being implied by diag mode.
func SetBattlEye(cfg string, on bool) (string, bool) {
	want := "0"
	if on {
		want = "1"
	}

	changed := false
	out := battlEyeRe.ReplaceAllStringFunc(cfg, func(m string) string {
		parts := battlEyeRe.FindStringSubmatch(m)
		if parts[2] == want {
			return m
		}
		changed = true
		return parts[1] + want + parts[3]
	})
	return out, changed
}

// BattlEyeEnabled reports the config's current BattlEye setting. Absent counts
// as enabled, matching the server's own default.
func BattlEyeEnabled(cfg string) bool {
	m := battlEyeRe.FindStringSubmatch(StripComments(cfg))
	if m == nil {
		return true
	}
	return m[2] != "0"
}

// StripComments removes // line comments so a commented-out setting does not
// read as a live one.
func StripComments(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// Deploy copies the repo's server config into the server installation,
// applying the BattlEye setting on the way.
//
// The patch only touches the deployed copy. We never modify the tracked file in
// the repo.
func Deploy(srcPath, dstPath string, battlEye bool) error {
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("serverdz: reading %s: %w", srcPath, err)
	}
	cfg := string(data)

	if !HasInstanceID(cfg) {
		return fmt.Errorf("serverdz: %s sets no valid instanceId; the server would terminate during config validation, before mission init", srcPath)
	}

	patched, _ := SetBattlEye(cfg, battlEye)

	if err := os.WriteFile(dstPath, []byte(patched), 0o644); err != nil {
		return fmt.Errorf("serverdz: writing %s: %w", dstPath, err)
	}
	return nil
}
