// SPDX-License-Identifier: MIT

package recommendations

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// TestSamplerRecordsGaugeFromRegistry_spec_25_3_588 verifies the §25.3
// sampler reads a gauge out of the in-process registry and records it
// into the WindowStore under the nil-label aggregate the evaluators read.
func TestSamplerRecordsGaugeFromRegistry_spec_25_3_588(t *testing.T) {
	reg := prometheus.NewRegistry()
	g := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "lenny_credential_pool_utilization"}, []string{"pool"})
	reg.MustRegister(g)
	g.WithLabelValues("default").Set(0.91)

	store := NewWindowStore(time.Hour)
	if err := NewSampler(reg, store).Sample(); err != nil {
		t.Fatalf("Sample: %v", err)
	}

	got, ok := store.GaugeValue("lenny_credential_pool_utilization", nil)
	if !ok {
		t.Fatal("expected a recorded sample, got none")
	}
	if got != 0.91 {
		t.Fatalf("GaugeValue = %v, want 0.91", got)
	}
}

// TestSamplerMaxAggregatesGaugeAcrossLabels_spec_25_3_588 verifies the
// ratio gauges aggregate with aggMax so a single hot pool trips the
// recommendation (matching the §16.5 "any pool" alert framing).
func TestSamplerMaxAggregatesGaugeAcrossLabels_spec_25_3_588(t *testing.T) {
	reg := prometheus.NewRegistry()
	g := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "lenny_credential_pool_utilization"}, []string{"pool"})
	reg.MustRegister(g)
	g.WithLabelValues("cool").Set(0.10)
	g.WithLabelValues("hot").Set(0.88)

	store := NewWindowStore(time.Hour)
	if err := NewSampler(reg, store).Sample(); err != nil {
		t.Fatalf("Sample: %v", err)
	}

	got, _ := store.GaugeValue("lenny_credential_pool_utilization", nil)
	if got != 0.88 {
		t.Fatalf("aggMax GaugeValue = %v, want 0.88 (worst pool)", got)
	}
}

// TestSamplerSumAggregatesCounterAcrossLabels_spec_25_3_588 verifies
// counters aggregate with aggSum so the platform-wide rate is the sum of
// the per-pool counters.
func TestSamplerSumAggregatesCounterAcrossLabels_spec_25_3_588(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "lenny_warmpool_claims_total"}, []string{"pool"})
	reg.MustRegister(c)
	c.WithLabelValues("a").Add(3)
	c.WithLabelValues("b").Add(4)

	store := NewWindowStore(time.Hour)
	if err := NewSampler(reg, store).Sample(); err != nil {
		t.Fatalf("Sample: %v", err)
	}

	got, _ := store.CounterValue("lenny_warmpool_claims_total", nil)
	if got != 7 {
		t.Fatalf("aggSum CounterValue = %v, want 7", got)
	}
}

// TestSamplerSkipsUnregisteredSeries_spec_25_3_597 verifies a metric not
// present in the registry (it originates in another process) leaves its
// window empty — the §25.3 insufficient-data path — rather than erroring.
func TestSamplerSkipsUnregisteredSeries_spec_25_3_597(t *testing.T) {
	reg := prometheus.NewRegistry()
	store := NewWindowStore(time.Hour)
	if err := NewSampler(reg, store).Sample(); err != nil {
		t.Fatalf("Sample over empty registry: %v", err)
	}
	if _, ok := store.GaugeValue("lenny_quota_rejection_ratio", nil); ok {
		t.Fatal("expected no sample for an unregistered series")
	}
}

// TestSamplerFeedsRecommendationEvaluator_spec_25_3_588 is the end-to-end
// check: after the sampler records a hot credential-pool utilization, the
// CapacityService over the same store emits the CredentialPoolUndersized
// recommendation. Before the sampler runs the store is empty and the rule
// does not trigger.
func TestSamplerFeedsRecommendationEvaluator_spec_25_3_588(t *testing.T) {
	reg := prometheus.NewRegistry()
	g := prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "lenny_credential_pool_utilization"}, []string{"pool"})
	reg.MustRegister(g)
	g.WithLabelValues("default").Set(0.85)

	store := NewWindowStore(time.Hour)
	svc := NewCapacityService(store)

	before, err := svc.GetRecommendations(t.Context(), nil)
	if err != nil {
		t.Fatalf("GetRecommendations (empty): %v", err)
	}
	if len(before.Recommendations) != 0 {
		t.Fatalf("expected no recommendations before sampling, got %d", len(before.Recommendations))
	}

	if err := NewSampler(reg, store).Sample(); err != nil {
		t.Fatalf("Sample: %v", err)
	}

	after, err := svc.GetRecommendations(t.Context(), nil)
	if err != nil {
		t.Fatalf("GetRecommendations (sampled): %v", err)
	}
	found := false
	for _, r := range after.Recommendations {
		if r.Rule == "CredentialPoolUndersized" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected CredentialPoolUndersized after sampling 0.85 utilization, got %+v", after.Recommendations)
	}
}

// TestSamplerNilSafe_spec_25_3_588 verifies a Sampler built without a
// gatherer or store is a no-op rather than a panic, so a gateway started
// without recommendations is unaffected.
func TestSamplerNilSafe_spec_25_3_588(t *testing.T) {
	if err := NewSampler(nil, nil).Sample(); err != nil {
		t.Fatalf("nil Sample: %v", err)
	}
}
