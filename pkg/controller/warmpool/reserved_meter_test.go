// SPDX-License-Identifier: MIT

package warmpool

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// spec: §16.1 (lenny_warmpool_reserved_pods gauge), §4.6.2 (reserved pods
// count as occupied). setReservedPods must publish the pool's current
// reserved-pod count to the §16.1 gauge on every reconcile so a restart
// re-establishes the series.
func TestSetReservedPodsPublishesGauge_spec_16_1(t *testing.T) {
	const pool = "pool-reserved-gauge"
	t.Cleanup(func() { forgetReservedPods(pool) })
	setReservedPods(pool, 3)
	if got := testutil.ToFloat64(reservedPods.WithLabelValues(pool)); got != 3 {
		t.Errorf("reservedPods gauge for %q = %v, want 3", pool, got)
	}
	// A later reconcile that observes no reserved pods must refresh the
	// gauge to zero rather than leaving the stale high-water value.
	setReservedPods(pool, 0)
	if got := testutil.ToFloat64(reservedPods.WithLabelValues(pool)); got != 0 {
		t.Errorf("reservedPods gauge for %q after refresh = %v, want 0", pool, got)
	}
}

// spec: §16.1 — a removed pool must not leave a stale gauge series
// behind. forgetReservedPods deletes the labeled series.
func TestForgetReservedPodsClearsSeries_spec_16_1(t *testing.T) {
	const pool = "pool-reserved-forget"
	setReservedPods(pool, 5)
	forgetReservedPods(pool)
	// Re-reading the labeled series via Set initializes a fresh zero
	// baseline; the previous value of 5 must not survive the forget.
	if got := testutil.ToFloat64(reservedPods.WithLabelValues(pool)); got != 0 {
		t.Errorf("reservedPods gauge for %q survived forget at %v, want 0", pool, got)
	}
	forgetReservedPods(pool)
}
