// SPDX-License-Identifier: MIT

package recommendations

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestGeneratedTotalCountsRecommendations_spec_25_3_618 asserts each
// emitted recommendation increments lenny_recommendations_generated_total
// keyed by the recommendation's own category and priority.
func TestGeneratedTotalCountsRecommendations_spec_25_3_618(t *testing.T) {
	m, err := NewMetrics(prometheus.NewRegistry())
	if err != nil {
		t.Fatalf("NewMetrics: %v", err)
	}
	store := NewWindowStore(7 * 24 * time.Hour)
	store.Record("lenny_credential_pool_utilization", nil, 0.90)
	svc := NewCapacityService(store).WithMetrics(m)

	resp, err := svc.GetRecommendations(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetRecommendations: %v", err)
	}
	if len(resp.Recommendations) == 0 {
		t.Fatal("expected at least one recommendation from a 90% utilized credential pool")
	}
	for _, rec := range resp.Recommendations {
		if got := testutil.ToFloat64(m.generated.WithLabelValues(rec.Category, rec.Priority)); got < 1 {
			t.Errorf("generated{%s,%s} = %v, want >= 1", rec.Category, rec.Priority, got)
		}
	}
}

// TestRingBufferBytesReflectsStore_spec_25_3_598 asserts the
// lenny_recommendations_ring_buffer_bytes gauge reports the store's
// current memory use: zero when empty, growing after Record.
func TestRingBufferBytesReflectsStore_spec_25_3_598(t *testing.T) {
	store := NewWindowStore(time.Hour)
	if got := store.ApproxBytes(); got != 0 {
		t.Fatalf("empty store ApproxBytes = %d, want 0", got)
	}

	reg := prometheus.NewRegistry()
	if err := RegisterRingBufferBytes(reg, store); err != nil {
		t.Fatalf("RegisterRingBufferBytes: %v", err)
	}
	if got := gaugeValue(t, reg, "lenny_recommendations_ring_buffer_bytes"); got != 0 {
		t.Fatalf("empty-store gauge = %v, want 0", got)
	}

	for i := 0; i < 100; i++ {
		store.Record("lenny_warmpool_claims_total", map[string]string{"pool": "p"}, float64(i))
	}
	if store.ApproxBytes() == 0 {
		t.Fatal("ApproxBytes still 0 after 100 records")
	}
	if got := gaugeValue(t, reg, "lenny_recommendations_ring_buffer_bytes"); got <= 0 {
		t.Fatalf("populated-store gauge = %v, want > 0", got)
	}
}

// gaugeValue gathers reg and returns the value of the named single-series
// gauge.
func gaugeValue(t *testing.T, reg *prometheus.Registry, name string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		if len(mf.GetMetric()) == 0 {
			t.Fatalf("metric %q has no series", name)
		}
		return mf.GetMetric()[0].GetGauge().GetValue()
	}
	t.Fatalf("metric %q not registered", name)
	return 0
}
