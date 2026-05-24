// SPDX-License-Identifier: MIT

package main

import "testing"

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
