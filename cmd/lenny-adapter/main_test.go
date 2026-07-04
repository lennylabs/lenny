// SPDX-License-Identifier: MIT

package main

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter/scrub"
)

// TestNewScrubOpsWiresRealScrub_spec_5_2 pins the production wiring of the §5.2
// whole-pod scrub. Before the fix, cmd/lenny-adapter never assigned
// adapterSrv.ScrubOps, so a session-mode recycle ran scrub.Run with nil Ops,
// reported PodScrubFailed, and the default warn policy reused the pod for the
// next session without any scrub having run (a between-session isolation
// regression). newScrubOps now backs the driver with the real DefaultOps. This
// asserts the production build supplies a non-nil scrub.Ops, so a recycle runs
// the real scrub rather than falling into the fail-open report path.
//
// spec: 5.2 (whole-pod scrub), 4.7 (reportpodscrub)
func TestNewScrubOpsWiresRealScrub_spec_5_2(t *testing.T) {
	var ops scrub.Ops = newScrubOps()
	if ops == nil {
		t.Fatal("newScrubOps returned nil; a production recycle would report PodScrubFailed and be reused without a scrub")
	}
	if _, isDefault := ops.(scrub.DefaultOps); !isDefault {
		t.Fatalf("newScrubOps returned %T, want scrub.DefaultOps (the real host operations)", ops)
	}
}

// TestResolveRuntimeUID_spec_4_7 covers the §4.7/§13 SO_PEERCRED peer-UID
// resolution (spec/04_system-components.md lines 866-868): the flag wins,
// a zero flag falls back to LENNY_RUNTIME_UID, and an unparseable or
// missing value leaves the check disabled (UID 0).
func TestResolveRuntimeUID_spec_4_7(t *testing.T) {
	tests := []struct {
		name    string
		flagUID uint
		env     string
		setEnv  bool
		want    uint32
	}{
		{name: "flag takes precedence over env", flagUID: 1001, env: "2002", setEnv: true, want: 1001},
		{name: "zero flag falls back to env", flagUID: 0, env: "1001", setEnv: true, want: 1001},
		{name: "zero flag and no env disables check", flagUID: 0, setEnv: false, want: 0},
		{name: "unparseable env disables check", flagUID: 0, env: "not-a-number", setEnv: true, want: 0},
		{name: "empty env disables check", flagUID: 0, env: "", setEnv: true, want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setEnv {
				t.Setenv("LENNY_RUNTIME_UID", tc.env)
			} else {
				// Setenv with empty then Unsetenv-like: ensure no inherited value.
				t.Setenv("LENNY_RUNTIME_UID", "")
			}
			if got := resolveRuntimeUID(tc.flagUID); got != tc.want {
				t.Fatalf("resolveRuntimeUID(%d) = %d, want %d", tc.flagUID, got, tc.want)
			}
		})
	}
}

// TestEnvIntOr_spec_11_3 covers the helper that backs the §11.3 keepalive
// flag defaults: a present-and-valid env wins, anything else falls back
// to the default. F-11.3.12.
func TestEnvIntOr_spec_11_3(t *testing.T) {
	tests := []struct {
		name   string
		setEnv bool
		val    string
		def    int
		want   int
	}{
		{name: "valid env wins", setEnv: true, val: "12345", def: 10_000, want: 12_345},
		{name: "empty env returns default", setEnv: true, val: "", def: 10_000, want: 10_000},
		{name: "unparseable env returns default", setEnv: true, val: "not-a-number", def: 5_000, want: 5_000},
		{name: "unset env returns default", setEnv: false, def: 5_000, want: 5_000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.setEnv {
				t.Setenv("LENNY_KEEPALIVE_TEST", tc.val)
			}
			got := envIntOr("LENNY_KEEPALIVE_TEST", tc.def)
			if got != tc.want {
				t.Errorf("envIntOr() = %d, want %d", got, tc.want)
			}
		})
	}
}
