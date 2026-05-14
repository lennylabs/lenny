// SPDX-License-Identifier: MIT

package quota

import (
	"testing"

	"pgregory.net/rapid"
)

// TestPropertyCheckIsMonotonic asserts that Check is monotonic in
// used: as used grows toward limit, the returned State never moves
// from a more permissive level to a less permissive level then back.
// State ordering is: OK < Warn < Exceeded.
//
// spec: 11.2 (quota Check monotonicity)
// diagnosis: A non-monotonic check would let used → used + Δ
//
//	produce a state more permissive than used alone. This
//	property holds for every (used, limit) pair.
func TestPropertyCheckIsMonotonic(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		limit := rapid.Int64Range(1, 1<<40).Draw(rt, "limit")
		usedA := rapid.Int64Range(0, limit*2).Draw(rt, "usedA")
		delta := rapid.Int64Range(0, limit*2).Draw(rt, "delta")
		usedB := usedA + delta

		stateA := Check(usedA, limit)
		stateB := Check(usedB, limit)

		if stateRank(stateA) > stateRank(stateB) {
			rt.Errorf("monotonicity violated: Check(%d, %d) = %v but Check(%d, %d) = %v",
				usedA, limit, stateA, usedB, limit, stateB)
		}
	})
}

// TestPropertyFailOpenCeilingNonNegative asserts FailOpenCeiling
// always returns a non-negative result, even on degenerate inputs.
//
// spec: 11.2 (fail-open per-replica hard cap arithmetic)
// diagnosis: A negative result would let downstream callers loop
//
//	or panic on a budget computation. The function must
//	saturate at zero.
func TestPropertyFailOpenCeilingNonNegative(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		tenantLimit := rapid.Int64Range(0, 1<<40).Draw(rt, "tenantLimit")
		replicas := rapid.IntRange(0, 100).Draw(rt, "replicas")
		hardCap := rapid.Int64Range(0, 1<<40).Draw(rt, "hardCap")

		got := FailOpenCeiling(tenantLimit, replicas, hardCap)
		if got < 0 {
			rt.Errorf("FailOpenCeiling returned negative: %d (tenant=%d replicas=%d cap=%d)",
				got, tenantLimit, replicas, hardCap)
		}
	})
}

// TestPropertyFailOpenBoundedByHardCap asserts FailOpenCeiling never
// exceeds the per-replica hard cap when hardCap > 0.
//
// spec: 11.2 (fail-open hard cap bound)
// diagnosis: A return value > hardCap means the cap is not being
//
//	applied at the right place in the arithmetic.
func TestPropertyFailOpenBoundedByHardCap(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		tenantLimit := rapid.Int64Range(0, 1<<40).Draw(rt, "tenantLimit")
		replicas := rapid.IntRange(1, 100).Draw(rt, "replicas")
		hardCap := rapid.Int64Range(1, 1<<40).Draw(rt, "hardCap")

		got := FailOpenCeiling(tenantLimit, replicas, hardCap)
		if got > hardCap {
			rt.Errorf("FailOpenCeiling > hardCap: %d > %d (tenant=%d replicas=%d)",
				got, hardCap, tenantLimit, replicas)
		}
	})
}

// TestPropertyReconcileMaxIsMax asserts ReconcileMax always returns
// max(in-memory, postgres-checkpoint).
//
// spec: 11.2 MAX-rule reconciliation
// diagnosis: A return value below either input means the
//
//	reconciliation rule is wrong. Replicas pick the MAX
//	on recovery to avoid double-spending budget.
func TestPropertyReconcileMaxIsMax(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		a := rapid.Int64Range(0, 1<<40).Draw(rt, "in-memory")
		b := rapid.Int64Range(0, 1<<40).Draw(rt, "postgres-checkpoint")
		got := ReconcileMax(a, b)
		want := a
		if b > want {
			want = b
		}
		if got != want {
			rt.Errorf("ReconcileMax(%d, %d) = %d, want max = %d", a, b, got, want)
		}
	})
}
