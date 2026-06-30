// SPDX-License-Identifier: MIT

package tenantstore_test

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
)

// spec: §12.9 line 1043 — the per-tier retention default: T2 90 days, T4
// 24 hours (fixed by the spec), T1 indefinite (zero, fixed), and T3 / the
// empty default deployer-configured (not fixed). An unrecognized tier is
// not fixed so the caller keeps its own configured window.
func TestTierRetentionDefault_spec_12_9_1043(t *testing.T) {
	cases := []struct {
		tier      string
		wantDur   time.Duration
		wantFixed bool
	}{
		{"T1", 0, true},
		{"T2", 90 * 24 * time.Hour, true},
		{"T3", 0, false},
		{"", 0, false},
		{"T4", 24 * time.Hour, true},
		{"T5", 0, false},
		{"prod", 0, false},
	}
	for _, c := range cases {
		gotDur, gotFixed := tenantstore.TierRetentionDefault(c.tier)
		if gotDur != c.wantDur || gotFixed != c.wantFixed {
			t.Errorf("TierRetentionDefault(%q) = (%v, %v), want (%v, %v)",
				c.tier, gotDur, gotFixed, c.wantDur, c.wantFixed)
		}
	}
}

// spec: §12.9 line 1048; §15.1 line 816 — workspaceTier is a closed
// tenant-settable enum: empty (the T3 default), T3, or T4. T1/T2 and any
// other string are rejected.
func TestValidWorkspaceTier(t *testing.T) {
	cases := []struct {
		tier string
		want bool
	}{
		{"", true},
		{"T3", true},
		{"T4", true},
		{"T1", false},
		{"T2", false},
		{"T5", false},
		{"prod", false},
		{"t4", false}, // case-sensitive; the lowercase form is the pod label, not the tier
	}
	for _, c := range cases {
		if got := tenantstore.ValidWorkspaceTier(c.tier); got != c.want {
			t.Errorf("ValidWorkspaceTier(%q) = %v, want %v", c.tier, got, c.want)
		}
	}
}

// spec: §12.9 line 1033; §15.1 line 816 — workspaceTier is ratcheted
// stricter-only; T4→T3 is a downgrade, T3→T4 is not, and an off-ladder
// tier never counts as a downgrade (the enum validator rejects it first).
func TestIsWorkspaceTierDowngrade(t *testing.T) {
	cases := []struct {
		current, requested string
		want               bool
	}{
		{"T4", "T3", true},
		{"T4", "", true}, // empty ranks with T3, so T4→default is a downgrade
		{"T3", "T4", false},
		{"T3", "T3", false},
		{"T4", "T4", false},
		{"", "T4", false},
		{"", "T3", false},
		{"T2", "T3", false}, // off-ladder current: not treated as a downgrade
		{"T3", "T2", false}, // off-ladder requested: not treated as a downgrade
	}
	for _, c := range cases {
		if got := tenantstore.IsWorkspaceTierDowngrade(c.current, c.requested); got != c.want {
			t.Errorf("IsWorkspaceTierDowngrade(%q, %q) = %v, want %v", c.current, c.requested, got, c.want)
		}
	}
}

// WorkspaceTierRank reports a strictness ordinal and ladder membership.
func TestWorkspaceTierRank(t *testing.T) {
	if r, ok := tenantstore.WorkspaceTierRank("T4"); !ok || r != 2 {
		t.Errorf("rank(T4) = (%d, %v), want (2, true)", r, ok)
	}
	if r3, _ := tenantstore.WorkspaceTierRank("T3"); r3 != 1 {
		t.Errorf("rank(T3) = %d, want 1", r3)
	}
	if rEmpty, _ := tenantstore.WorkspaceTierRank(""); rEmpty != 1 {
		t.Errorf("rank(\"\") = %d, want 1 (T3 default)", rEmpty)
	}
	if _, ok := tenantstore.WorkspaceTierRank("T2"); ok {
		t.Error("rank(T2) reported on-ladder, want off-ladder")
	}
	// T4 must outrank T3 so the stricter-only environment override admits a
	// stricter tier and rejects a looser one.
	r4, _ := tenantstore.WorkspaceTierRank("T4")
	r3, _ := tenantstore.WorkspaceTierRank("T3")
	if !(r4 > r3) {
		t.Errorf("T4 rank %d must exceed T3 rank %d", r4, r3)
	}
}
