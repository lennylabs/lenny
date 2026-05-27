// SPDX-License-Identifier: MIT

package runtimestore_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
)

// spec: §7.5 lines 481-490 — F-7.5.1.

// TestSetupCommandPolicyPermitsCommandAllowlist exercises the §7.5 prefix
// match in allowlist mode: a command matches when its raw text equals the
// prefix exactly or begins with `prefix + space`.
func TestSetupCommandPolicyPermitsCommandAllowlist(t *testing.T) {
	policy := &runtimestore.SetupCommandPolicy{
		Mode:      runtimestore.SetupCommandModeAllowlist,
		Allowlist: []string{"npm", "make", "pip install"},
	}
	cases := []struct {
		cmd  string
		want bool
	}{
		{"npm", true},
		{"npm ci", true},
		{"npm install -g foo", true},
		{"make", true},
		{"make test", true},
		{"pip install -r requirements.txt", true},
		// Not in the allowlist.
		{"curl http://...", false},
		{"rm -rf /", false},
		// Prefix collision: `npm-other` is NOT a prefix-match for `npm`.
		{"npm-other foo", false},
		// Empty command is rejected.
		{"", false},
	}
	for _, tc := range cases {
		if got := policy.PermitsCommand(tc.cmd); got != tc.want {
			t.Errorf("PermitsCommand(%q) allowlist = %v, want %v", tc.cmd, got, tc.want)
		}
	}
}

// TestSetupCommandPolicyPermitsCommandBlocklist exercises the §7.5 prefix
// match in blocklist mode: a command is rejected when it matches any
// blocklist prefix and admitted otherwise.
func TestSetupCommandPolicyPermitsCommandBlocklist(t *testing.T) {
	policy := &runtimestore.SetupCommandPolicy{
		Mode:      runtimestore.SetupCommandModeBlocklist,
		Blocklist: []string{"curl", "rm -rf"},
	}
	cases := []struct {
		cmd  string
		want bool
	}{
		// Blocklist hits — must be rejected.
		{"curl http://...", false},
		{"curl", false},
		{"rm -rf /", false},
		{"rm -rf foo bar", false},
		// Everything else admits.
		{"npm ci", true},
		{"make test", true},
		// Empty command admits (the blocklist did not match).
		{"", true},
		// Prefix collision: `curl-other` is NOT a prefix-match for `curl`.
		{"curl-other", true},
	}
	for _, tc := range cases {
		if got := policy.PermitsCommand(tc.cmd); got != tc.want {
			t.Errorf("PermitsCommand(%q) blocklist = %v, want %v", tc.cmd, got, tc.want)
		}
	}
}

// TestSetupCommandPolicyPermitsCommandNilOrEmptyMode admits every command:
// no policy means no gate, an empty mode means the prefix gate is disabled.
func TestSetupCommandPolicyPermitsCommandNilOrEmptyMode(t *testing.T) {
	if !(*runtimestore.SetupCommandPolicy)(nil).PermitsCommand("anything") {
		t.Error("nil SetupCommandPolicy must admit every command")
	}
	policy := &runtimestore.SetupCommandPolicy{
		// No Mode declared: the allow/block list is not consulted.
		Allowlist: []string{"foo"},
	}
	if !policy.PermitsCommand("anything") {
		t.Error("empty Mode must admit every command (prefix gate disabled)")
	}
}

// TestSetupCommandPolicyPermitsAllowlistEmptyListDeniesAll covers the §7.5
// deny-by-default invariant: allowlist mode with no Allowlist denies every
// command.
func TestSetupCommandPolicyPermitsAllowlistEmptyListDeniesAll(t *testing.T) {
	policy := &runtimestore.SetupCommandPolicy{Mode: runtimestore.SetupCommandModeAllowlist}
	if policy.PermitsCommand("npm ci") {
		t.Error("allowlist mode with an empty Allowlist must deny every command")
	}
}

// TestSetupCommandModesEnumIsAllowlistAndBlocklist locks the §7.5 closed
// enum so a stray `shell` literal (the prior enum value, F-7.5.1) cannot
// re-enter the codebase.
func TestSetupCommandModesEnumIsAllowlistAndBlocklist(t *testing.T) {
	modes := runtimestore.AllSetupCommandModes()
	if len(modes) != 2 {
		t.Fatalf("AllSetupCommandModes() len = %d, want 2; got %v", len(modes), modes)
	}
	seen := map[runtimestore.SetupCommandMode]bool{}
	for _, m := range modes {
		seen[m] = true
	}
	for _, want := range []runtimestore.SetupCommandMode{
		runtimestore.SetupCommandModeAllowlist,
		runtimestore.SetupCommandModeBlocklist,
	} {
		if !seen[want] {
			t.Errorf("AllSetupCommandModes() missing %q", want)
		}
	}
	// The retired `shell` mode must not validate.
	if (runtimestore.SetupCommandMode("shell")).IsValid() {
		t.Error("legacy `shell` mode must no longer be valid; spec §7.5 closed enum is allowlist|blocklist")
	}
}

// TestSetupCommandPolicyClonePreservesBlocklist guards the deep-copy of
// the Blocklist slice so the store never aliases a caller's input.
func TestSetupCommandPolicyClonePreservesBlocklist(t *testing.T) {
	in := &runtimestore.SetupCommandPolicy{
		Mode:      runtimestore.SetupCommandModeBlocklist,
		Blocklist: []string{"curl"},
	}
	out := in.Clone()
	if out == nil || len(out.Blocklist) != 1 || out.Blocklist[0] != "curl" {
		t.Fatalf("Clone() = %+v, want a copy with Blocklist=[curl]", out)
	}
	// Mutate the source — the clone must not see the change.
	in.Blocklist[0] = "tampered"
	if out.Blocklist[0] != "curl" {
		t.Errorf("Clone() Blocklist aliases the source slice: %v", out.Blocklist)
	}
}

// TestArchivePolicyClone covers F-7.4.4: the §13.4 archivePolicy block
// is deep-copied so the store never aliases a caller's input. The Clone
// receiver also handles nil and the round-trip yields an independent
// value.
func TestArchivePolicyClone(t *testing.T) {
	if got := (*runtimestore.ArchivePolicy)(nil).Clone(); got != nil {
		t.Errorf("nil ArchivePolicy clone = %+v, want nil", got)
	}
	in := &runtimestore.ArchivePolicy{AllowSymlinks: true}
	out := in.Clone()
	if out == nil || !out.AllowSymlinks {
		t.Fatalf("Clone() = %+v, want a copy with AllowSymlinks=true", out)
	}
	in.AllowSymlinks = false
	if !out.AllowSymlinks {
		t.Errorf("Clone() AllowSymlinks aliased to source mutation")
	}
}
