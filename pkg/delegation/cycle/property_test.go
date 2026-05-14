// SPDX-License-Identifier: MIT

package cycle

import (
	"testing"

	"pgregory.net/rapid"
)

// genIdentity draws a (runtime, pool) identity.
func genIdentity(rt *rapid.T, label string) Identity {
	return Identity{
		PoolName:    rapid.StringMatching(`[a-z]{1,8}`).Draw(rt, label+".pool"),
		RuntimeName: rapid.StringMatching(`[a-z]{1,8}`).Draw(rt, label+".runtime"),
	}
}

// TestPropertyContainsImpliesDepthPositive — Lineage.Contains(x) is
// only true when Depth > 0. The lineage is empty iff Depth == 0.
//
// spec: 8.2 (lineage contracts)
// diagnosis: Contains returning true on an empty lineage means the
//
//	walk skipped the length check.
func TestPropertyContainsImpliesDepthPositive(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(0, 8).Draw(rt, "lineage length")
		var lineage Lineage
		for i := 0; i < n; i++ {
			lineage = append(lineage, genIdentity(rt, "lineage"))
		}
		probe := genIdentity(rt, "probe")
		if lineage.Contains(probe) && lineage.Depth() == 0 {
			rt.Errorf("Contains true on empty lineage")
		}
	})
}

// TestPropertyContainsFindsKnownMember — when a known identity is
// inserted at index i, Contains(x) is always true.
//
// spec: 8.2 (lineage.Contains correctness)
// diagnosis: Contains missing a known member is a fundamental
//
//	correctness bug — the cycle detector would let a real
//	cycle through.
func TestPropertyContainsFindsKnownMember(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(1, 8).Draw(rt, "lineage length")
		var lineage Lineage
		for i := 0; i < n; i++ {
			lineage = append(lineage, genIdentity(rt, "ancestor"))
		}
		idx := rapid.IntRange(0, n-1).Draw(rt, "idx")
		target := lineage[idx]
		if !lineage.Contains(target) {
			rt.Errorf("Contains returned false on inserted member at idx %d", idx)
		}
	})
}

// TestPropertyDepthEqualsLength — Depth() is exactly len(lineage).
//
// spec: 8.2 (Depth correctness)
// diagnosis: Depth drifting from len(lineage) means the lineage
//
//	abstraction has internal state out of sync with the
//	underlying slice — a bug class to rule out for good.
func TestPropertyDepthEqualsLength(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(0, 32).Draw(rt, "lineage length")
		var lineage Lineage
		for i := 0; i < n; i++ {
			lineage = append(lineage, Identity{RuntimeName: "r", PoolName: "p"})
		}
		if lineage.Depth() != n {
			rt.Errorf("Depth=%d, want %d", lineage.Depth(), n)
		}
	})
}
