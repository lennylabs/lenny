// SPDX-License-Identifier: MIT

package runtimestore_test

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
)

// spec: §5.1 line 24 — sdkWarmBlockingPaths defaults to
// ["CLAUDE.md", ".claude/*"] when capabilities.preConnect is true and the
// runtime declares none; the field is ignored (no default) otherwise.
func TestApplyDefaultsSeedsSDKWarmBlockingPaths(t *testing.T) {
	cases := []struct {
		name string
		in   runtimestore.Runtime
		want []string
	}{
		{
			name: "preConnect true, no list seeds the default",
			in:   runtimestore.Runtime{Capabilities: &runtimestore.RuntimeCapabilities{PreConnect: true}},
			want: []string{"CLAUDE.md", ".claude/*"},
		},
		{
			name: "preConnect true keeps an explicit list",
			in: runtimestore.Runtime{
				Capabilities:         &runtimestore.RuntimeCapabilities{PreConnect: true},
				SDKWarmBlockingPaths: []string{"AGENTS.md"},
			},
			want: []string{"AGENTS.md"},
		},
		{
			name: "preConnect false seeds nothing",
			in:   runtimestore.Runtime{Capabilities: &runtimestore.RuntimeCapabilities{PreConnect: false}},
			want: nil,
		},
		{
			name: "no capabilities block seeds nothing",
			in:   runtimestore.Runtime{},
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt := tc.in
			runtimestore.ApplyDefaults(&rt, false)
			if !equalStrings(rt.SDKWarmBlockingPaths, tc.want) {
				t.Errorf("SDKWarmBlockingPaths = %v, want %v", rt.SDKWarmBlockingPaths, tc.want)
			}
		})
	}
}

// spec: §5.1 lines 22-24 — a derived runtime inherits the base's
// sdkWarmBlockingPaths, the companion of the inherited capabilities.preConnect.
func TestMergeInheritsSDKWarmBlockingPaths(t *testing.T) {
	base := runtimestore.Runtime{
		Name:                 "base",
		Capabilities:         &runtimestore.RuntimeCapabilities{PreConnect: true},
		SDKWarmBlockingPaths: []string{"CLAUDE.md", ".claude/*"},
	}
	derived := runtimestore.Runtime{
		Name: "derived", BaseRuntime: "base",
		// A derived runtime that tries to set its own list is overridden:
		// the field is inherited from the base.
		SDKWarmBlockingPaths: []string{"ignored.md"},
	}
	eff := runtimestore.Merge(base, derived)
	if !equalStrings(eff.SDKWarmBlockingPaths, []string{"CLAUDE.md", ".claude/*"}) {
		t.Errorf("derived must inherit base sdkWarmBlockingPaths: %v", eff.SDKWarmBlockingPaths)
	}
	if eff.Capabilities == nil || !eff.Capabilities.PreConnect {
		t.Errorf("derived must inherit capabilities.preConnect: %+v", eff.Capabilities)
	}
	// Mutating the merged slice must not alias the base input.
	eff.SDKWarmBlockingPaths[0] = "tampered"
	if base.SDKWarmBlockingPaths[0] != "CLAUDE.md" {
		t.Error("Merge aliased the base sdkWarmBlockingPaths slice")
	}
}

// spec: §5.1 line 195 — setupPolicy.timeoutSeconds is a Maximum merge in
// which zero ("no aggregate cap", §5.1 line 260) is the largest possible
// bound and wins over any finite value, so a base "no cap" floor survives.
func TestMergeSetupPolicyZeroIsNoCap(t *testing.T) {
	mk := func(sec int) *runtimestore.SetupPolicy {
		return &runtimestore.SetupPolicy{TimeoutSeconds: sec, OnTimeout: runtimestore.SetupTimeoutFail}
	}
	cases := []struct {
		name           string
		base, derived  *runtimestore.SetupPolicy
		wantTimeoutSec int
	}{
		{"base no-cap beats finite derived", mk(0), mk(120), 0},
		{"derived no-cap beats finite base", mk(300), mk(0), 0},
		{"both finite takes the max", mk(300), mk(120), 300},
		{"both no-cap stays no-cap", mk(0), mk(0), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := runtimestore.Runtime{Name: "base", SetupPolicy: tc.base}
			derived := runtimestore.Runtime{Name: "derived", BaseRuntime: "base", SetupPolicy: tc.derived}
			eff := runtimestore.Merge(base, derived)
			if eff.SetupPolicy == nil || eff.SetupPolicy.TimeoutSeconds != tc.wantTimeoutSec {
				t.Errorf("timeoutSeconds = %+v, want %d", eff.SetupPolicy, tc.wantTimeoutSec)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
