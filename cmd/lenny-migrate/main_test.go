// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"strings"
	"testing"
)

// runMigrate drives the binary's run() with no LENNY_POSTGRES_DSN set,
// so a command that reaches the DSN check stops there. The break-glass
// guard runs before the DSN check, letting these tests exercise the
// fence without a database.
func runMigrate(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	t.Setenv("LENNY_POSTGRES_DSN", "")
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// TestMigrateDownRequiresBreakGlass_spec_24_13 verifies that `down`
// without --break-glass --confirm is rejected before any DB connection,
// and the message points at the audited admin-API path.
func TestMigrateDownRequiresBreakGlass_spec_24_13(t *testing.T) {
	code, _, stderr := runMigrate(t, "down")
	if code != 2 {
		t.Fatalf("exit code: got %d, want 2", code)
	}
	if !strings.Contains(stderr, "--break-glass --confirm") {
		t.Errorf("stderr should name the guard flags: %q", stderr)
	}
	if !strings.Contains(stderr, "lenny-ctl migrate down") {
		t.Errorf("stderr should point at the audited path: %q", stderr)
	}
	// The DSN-required message must NOT appear: the guard fires first.
	if strings.Contains(stderr, "--postgres-dsn is required") {
		t.Errorf("guard should reject before the DSN check: %q", stderr)
	}
}

// TestMigrateGotoRequiresBreakGlass_spec_24_13 verifies the same fence
// applies to goto.
func TestMigrateGotoRequiresBreakGlass_spec_24_13(t *testing.T) {
	code, _, stderr := runMigrate(t, "goto", "5")
	if code != 2 {
		t.Fatalf("exit code: got %d, want 2", code)
	}
	if !strings.Contains(stderr, "--break-glass --confirm") {
		t.Errorf("stderr should name the guard flags: %q", stderr)
	}
}

// TestMigratePartialGuardRejected_spec_24_13 verifies that one of the
// two flags is insufficient.
func TestMigratePartialGuardRejected_spec_24_13(t *testing.T) {
	for _, args := range [][]string{
		{"--break-glass", "down"},
		{"--confirm", "down"},
	} {
		code, _, stderr := runMigrate(t, args...)
		if code != 2 {
			t.Fatalf("args %v: exit code %d, want 2", args, code)
		}
		if !strings.Contains(stderr, "--break-glass --confirm") {
			t.Errorf("args %v: stderr should name the guard: %q", args, stderr)
		}
	}
}

// TestMigrateGuardSatisfiedReachesDSNCheck_spec_24_13 verifies that with
// both guard flags the command clears the fence and proceeds to the DSN
// check (the next gate), proving the guard passed.
func TestMigrateGuardSatisfiedReachesDSNCheck_spec_24_13(t *testing.T) {
	code, _, stderr := runMigrate(t, "--break-glass", "--confirm", "down")
	if code != 2 {
		t.Fatalf("exit code: got %d, want 2", code)
	}
	if !strings.Contains(stderr, "--postgres-dsn is required") {
		t.Errorf("guard satisfied should fall through to the DSN check: %q", stderr)
	}
	if strings.Contains(stderr, "bypasses the audited") {
		t.Errorf("guard should not fire when both flags are set: %q", stderr)
	}
}

// TestMigrateUpNotGuarded_spec_24_13 verifies the deploy-Job `up` path
// is not fenced: it falls straight through to the DSN check.
func TestMigrateUpNotGuarded_spec_24_13(t *testing.T) {
	code, _, stderr := runMigrate(t, "up")
	if code != 2 {
		t.Fatalf("exit code: got %d, want 2", code)
	}
	if !strings.Contains(stderr, "--postgres-dsn is required") {
		t.Errorf("up should reach the DSN check unguarded: %q", stderr)
	}
	if strings.Contains(stderr, "break-glass") {
		t.Errorf("up must not be break-glass-fenced: %q", stderr)
	}
}

// TestMigrateUnknownCommand_spec_24_13 verifies an unrecognized command
// is a usage error and is never break-glass-fenced (only down/goto are).
func TestMigrateUnknownCommand_spec_24_13(t *testing.T) {
	code, _, stderr := runMigrate(t, "frobnicate")
	if code != 2 {
		t.Fatalf("exit code: got %d, want 2", code)
	}
	if strings.Contains(stderr, "break-glass") {
		t.Errorf("only down/goto are fenced, not %q: %q", "frobnicate", stderr)
	}
}
