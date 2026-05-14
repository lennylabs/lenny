// SPDX-License-Identifier: MIT

package cycle

import (
	"errors"
	"testing"
)

func TestAllModesIsExhaustive(t *testing.T) {
	if got := len(AllModes()); got != 3 {
		t.Errorf("AllModes() returned %d, want 3 per §8.2", got)
	}
	if Mode("bogus").IsValid() {
		t.Errorf("unknown mode must not be IsValid")
	}
}

func TestIdentityEqualsRequiresBothFields(t *testing.T) {
	a := Identity{RuntimeName: "claude", PoolName: "default"}
	b := Identity{RuntimeName: "claude", PoolName: "default"}
	c := Identity{RuntimeName: "claude", PoolName: "premium"}
	if !a.Equals(b) {
		t.Errorf("identical identities must be equal")
	}
	if a.Equals(c) {
		t.Errorf("pool-differentiated identities must not be equal per §8.2")
	}
}

func TestLineageDepthAndContains(t *testing.T) {
	root := Identity{"root", "default"}
	a := Identity{"a", "default"}
	b := Identity{"b", "default"}
	l := Lineage{root, a, b}
	if got := l.Depth(); got != 3 {
		t.Errorf("Depth: want 3, got %d", got)
	}
	if !l.Contains(a) {
		t.Errorf("Contains must find a in the lineage")
	}
	if l.Contains(Identity{"c", "default"}) {
		t.Errorf("Contains must not match an unrelated identity")
	}
}

// Non-cyclic hops admit regardless of mode and layer settings.
func TestDecideNonCyclicHopAdmits(t *testing.T) {
	for _, mode := range AllModes() {
		l := Lineage{{"root", "default"}}
		settings := Settings{Mode: mode, PlatformAllowSelfRec: false, RuntimeAllowSelfRec: false, PolicyAllowSelfRec: false}
		d := Decide(l, Identity{"target", "default"}, settings)
		if d.Outcome != OutcomeAdmitted {
			t.Errorf("mode=%q: non-cyclic hop should be admitted, got %+v", mode, d)
		}
		if d.IsSelfRecursive {
			t.Errorf("mode=%q: non-cyclic hop must not flag IsSelfRecursive", mode)
		}
	}
}

// Permissive mode admits self-recursive hops with no recording.
func TestDecidePermissiveAdmitsSelfRecursion(t *testing.T) {
	target := Identity{"loop", "default"}
	l := Lineage{target}
	d := Decide(l, target, Settings{Mode: ModePermissive})
	if d.Outcome != OutcomeAdmitted {
		t.Errorf("permissive mode must admit, got %+v", d)
	}
	if !d.IsSelfRecursive {
		t.Errorf("self-recursive hop must flag IsSelfRecursive even under permissive")
	}
	if len(d.WouldHaveBlockedLayers) != 0 {
		t.Errorf("permissive mode must not record WouldHaveBlockedLayers")
	}
}

// Enforce mode + all three layers true → admit.
func TestDecideEnforceAdmitsWhenAllLayersTrue(t *testing.T) {
	target := Identity{"loop", "default"}
	l := Lineage{target}
	d := Decide(l, target, Settings{
		Mode:                 ModeEnforce,
		PlatformAllowSelfRec: true,
		RuntimeAllowSelfRec:  true,
		PolicyAllowSelfRec:   true,
	})
	if d.Outcome != OutcomeAdmitted {
		t.Errorf("enforce + all layers true must admit, got %+v", d)
	}
	if d.BlockedBy != "" {
		t.Errorf("BlockedBy must be empty on admit, got %q", d.BlockedBy)
	}
}

// Enforce mode + any false layer → reject; BlockedBy names the first
// false layer in canonical order.
func TestDecideEnforceRejectsAndAttributesBlocker(t *testing.T) {
	cases := []struct {
		name      string
		s         Settings
		wantLayer Layer
	}{
		{"platform false", Settings{Mode: ModeEnforce, PlatformAllowSelfRec: false, RuntimeAllowSelfRec: true, PolicyAllowSelfRec: true}, LayerPlatform},
		{"runtime false", Settings{Mode: ModeEnforce, PlatformAllowSelfRec: true, RuntimeAllowSelfRec: false, PolicyAllowSelfRec: true}, LayerRuntime},
		{"policy false", Settings{Mode: ModeEnforce, PlatformAllowSelfRec: true, RuntimeAllowSelfRec: true, PolicyAllowSelfRec: false}, LayerPolicy},
		// Canonical order: platform > runtime > policy. When multiple
		// layers are false, BlockedBy names the platform.
		{"platform and runtime false", Settings{Mode: ModeEnforce, PlatformAllowSelfRec: false, RuntimeAllowSelfRec: false, PolicyAllowSelfRec: true}, LayerPlatform},
		{"runtime and policy false", Settings{Mode: ModeEnforce, PlatformAllowSelfRec: true, RuntimeAllowSelfRec: false, PolicyAllowSelfRec: false}, LayerRuntime},
		{"all false", Settings{Mode: ModeEnforce, PlatformAllowSelfRec: false, RuntimeAllowSelfRec: false, PolicyAllowSelfRec: false}, LayerPlatform},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			target := Identity{"loop", "default"}
			l := Lineage{target}
			d := Decide(l, target, c.s)
			if d.Outcome != OutcomeRejected {
				t.Errorf("expected rejection, got %+v", d)
			}
			if d.BlockedBy != c.wantLayer {
				t.Errorf("BlockedBy: want %q, got %q", c.wantLayer, d.BlockedBy)
			}
			if len(d.WouldHaveBlockedLayers) == 0 {
				t.Errorf("WouldHaveBlockedLayers must list every false layer")
			}
		})
	}
}

// Warn mode + any false layer → admit with WouldHaveBlocked outcome.
func TestDecideWarnAdmitsAndRecordsBlocker(t *testing.T) {
	target := Identity{"loop", "default"}
	l := Lineage{target}
	d := Decide(l, target, Settings{
		Mode:                 ModeWarn,
		PlatformAllowSelfRec: true,
		RuntimeAllowSelfRec:  false,
		PolicyAllowSelfRec:   false,
	})
	if d.Outcome != OutcomeWouldHaveBlocked {
		t.Errorf("warn + false layer should record WouldHaveBlocked, got %+v", d)
	}
	if d.BlockedBy != LayerRuntime {
		t.Errorf("BlockedBy on warn: want runtime, got %q", d.BlockedBy)
	}
	if len(d.WouldHaveBlockedLayers) != 2 {
		t.Errorf("WouldHaveBlockedLayers: want [runtime,policy], got %v", d.WouldHaveBlockedLayers)
	}
}

func TestDecideWarnAdmitsCleanlyWhenAllLayersTrue(t *testing.T) {
	target := Identity{"loop", "default"}
	l := Lineage{target}
	d := Decide(l, target, Settings{
		Mode:                 ModeWarn,
		PlatformAllowSelfRec: true,
		RuntimeAllowSelfRec:  true,
		PolicyAllowSelfRec:   true,
	})
	if d.Outcome != OutcomeAdmitted {
		t.Errorf("warn + all layers true must admit cleanly, got %+v", d)
	}
}

// Cycle detection MUST distinguish pool-differentiated calls per §8.2.
// A/pool1 → B → A/pool2 is NOT a cycle.
func TestDecidePoolDifferentiatedIsNotCycle(t *testing.T) {
	a1 := Identity{"a", "pool1"}
	a2 := Identity{"a", "pool2"}
	b := Identity{"b", "default"}
	l := Lineage{a1, b}
	d := Decide(l, a2, Settings{
		Mode:                 ModeEnforce,
		PlatformAllowSelfRec: false,
		RuntimeAllowSelfRec:  false,
		PolicyAllowSelfRec:   false,
	})
	if d.Outcome != OutcomeAdmitted {
		t.Errorf("pool-differentiated hop must not be treated as a cycle, got %+v", d)
	}
}

func TestToErrorMapsRejectionToTypedError(t *testing.T) {
	target := Identity{"loop", "default"}
	l := Lineage{target}
	d := Decide(l, target, Settings{Mode: ModeEnforce})
	err := ToError(d, target)
	if err == nil {
		t.Fatal("ToError should produce an error for a rejected decision")
	}
	var rej *Rejection
	if !errors.As(err, &rej) {
		t.Fatalf("expected *Rejection, got %T", err)
	}
	if rej.CycleRuntimeName != "loop" {
		t.Errorf("CycleRuntimeName: %q", rej.CycleRuntimeName)
	}
	if rej.BlockedBy == "" {
		t.Errorf("BlockedBy must be set on a rejection")
	}
}

func TestToErrorReturnsNilForAdmittedDecision(t *testing.T) {
	target := Identity{"loop", "default"}
	l := Lineage{} // empty — no cycle
	d := Decide(l, target, Settings{Mode: ModeEnforce})
	if got := ToError(d, target); got != nil {
		t.Errorf("admitted decision must not produce an error, got %v", got)
	}
}
