// SPDX-License-Identifier: MIT

package recommendations_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/operability/recommendations"
	"github.com/lennylabs/lenny/pkg/ops/conventions"
)

func TestGetRecommendationsEmptyWhenNoData(t *testing.T) {
	// §25.3: with empty sliding windows, no rule triggers.
	svc := recommendations.NewCapacityService(recommendations.NewWindowStore(7 * 24 * time.Hour))
	resp, err := svc.GetRecommendations(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetRecommendations: %v", err)
	}
	if len(resp.Recommendations) != 0 {
		t.Errorf("no-data recommendations: got %+v, want empty", resp.Recommendations)
	}
}

func TestGetRecommendationsTriggersCredentialPool(t *testing.T) {
	store := recommendations.NewWindowStore(7 * 24 * time.Hour)
	store.Record("lenny_credential_pool_utilization", nil, 0.85)
	svc := recommendations.NewCapacityService(store)

	resp, err := svc.GetRecommendations(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetRecommendations: %v", err)
	}
	var found *recommendations.Recommendation
	for i := range resp.Recommendations {
		if resp.Recommendations[i].Rule == "CredentialPoolUndersized" {
			found = &resp.Recommendations[i]
		}
	}
	if found == nil {
		t.Fatalf("CredentialPoolUndersized must trigger at 0.85 utilisation: %+v", resp.Recommendations)
	}
	if found.Category != "credential_pool_sizing" || !found.DataAvailable || found.Value != 0.85 {
		t.Errorf("recommendation fields: %+v", found)
	}
	if found.Confidence <= 0 || found.Confidence > 1 {
		t.Errorf("confidence must be in (0,1]: %v", found.Confidence)
	}
}

func TestGetRecommendationsBelowThresholdDoesNotTrigger(t *testing.T) {
	store := recommendations.NewWindowStore(7 * 24 * time.Hour)
	store.Record("lenny_credential_pool_utilization", nil, 0.40)
	svc := recommendations.NewCapacityService(store)
	resp, _ := svc.GetRecommendations(context.Background(), nil)
	for _, r := range resp.Recommendations {
		if r.Rule == "CredentialPoolUndersized" {
			t.Errorf("CredentialPoolUndersized must not trigger at 0.40 utilisation")
		}
	}
}

func TestGetRecommendationsCategoryFilter(t *testing.T) {
	store := recommendations.NewWindowStore(7 * 24 * time.Hour)
	store.Record("lenny_credential_pool_utilization", nil, 0.85)
	store.Record("lenny_gateway_cpu_utilization_ratio", nil, 0.90)
	svc := recommendations.NewCapacityService(store)

	cat := "gateway_scaling"
	resp, err := svc.GetRecommendations(context.Background(), &cat)
	if err != nil {
		t.Fatalf("GetRecommendations: %v", err)
	}
	if len(resp.Recommendations) != 1 || resp.Recommendations[0].Category != "gateway_scaling" {
		t.Errorf("category filter: got %+v, want only gateway_scaling", resp.Recommendations)
	}
}

func hasRule(resp *recommendations.RecommendationsResponse, rule string) bool {
	for _, r := range resp.Recommendations {
		if r.Rule == rule {
			return true
		}
	}
	return false
}

func TestGetRecommendationsResourceLimits(t *testing.T) {
	// §25.3 resource_limits: OOM kills in the 24h window trigger a
	// memory-limit recommendation.
	store := recommendations.NewWindowStore(7 * 24 * time.Hour)
	store.Record("lenny_pod_oom_killed_total", nil, 0)
	store.Record("lenny_pod_oom_killed_total", nil, 2)
	svc := recommendations.NewCapacityService(store)
	resp, _ := svc.GetRecommendations(context.Background(), nil)
	if !hasRule(resp, "ResourceLimitsMemoryPressure") {
		t.Errorf("ResourceLimitsMemoryPressure must trigger on OOM kills: %+v", resp.Recommendations)
	}
}

func TestGetRecommendationsRetentionAndQuota(t *testing.T) {
	// §25.3 retention_tuning and quota_adjustment.
	store := recommendations.NewWindowStore(7 * 24 * time.Hour)
	store.Record("lenny_storage_utilization_ratio", nil, 0.92)
	store.Record("lenny_quota_rejection_ratio", nil, 0.12)
	svc := recommendations.NewCapacityService(store)
	resp, _ := svc.GetRecommendations(context.Background(), nil)
	if !hasRule(resp, "RetentionTuningStoragePressure") {
		t.Errorf("RetentionTuningStoragePressure must trigger above 80%% storage: %+v", resp.Recommendations)
	}
	if !hasRule(resp, "QuotaAdjustmentRejections") {
		t.Errorf("QuotaAdjustmentRejections must trigger above 5%% rejections: %+v", resp.Recommendations)
	}
}

func TestGetRecommendationsRetentionBelowThreshold(t *testing.T) {
	store := recommendations.NewWindowStore(7 * 24 * time.Hour)
	store.Record("lenny_storage_utilization_ratio", nil, 0.50)
	svc := recommendations.NewCapacityService(store)
	resp, _ := svc.GetRecommendations(context.Background(), nil)
	if hasRule(resp, "RetentionTuningStoragePressure") {
		t.Error("RetentionTuningStoragePressure must not trigger at 50 percent storage")
	}
}

func TestGetRecommendationsWarmPoolIncrease(t *testing.T) {
	// §25.3: WarmPoolUndersized triggers when the pool was exhausted
	// 3+ times across the 24h window.
	store := recommendations.NewWindowStore(7 * 24 * time.Hour)
	store.Record("lenny_warmpool_exhausted_total", nil, 10)
	store.Record("lenny_warmpool_exhausted_total", nil, 14) // +4 over the window
	svc := recommendations.NewCapacityService(store)

	resp, _ := svc.GetRecommendations(context.Background(), nil)
	triggered := false
	for _, r := range resp.Recommendations {
		if r.Rule == "WarmPoolUndersized" {
			triggered = true
			if r.Value < 3 {
				t.Errorf("WarmPoolUndersized value (the 24h increase) = %v, want >= 3", r.Value)
			}
		}
	}
	if !triggered {
		t.Errorf("WarmPoolUndersized must trigger on a 24h increase of 4: %+v", resp.Recommendations)
	}
}

// TestGetRecommendationsStampsCompiledInDefaults_spec_25_13_4848
// asserts the gateway always stamps thresholdSource=compiled-in-defaults
// on its in-process /v1/admin/recommendations response per §25.13 line
// 4848. lenny-ops layers operator-customized on top when it serves
// from the Prometheus rule set. F-25.13.5.
func TestGetRecommendationsStampsCompiledInDefaults_spec_25_13_4848(t *testing.T) {
	svc := recommendations.NewCapacityService(recommendations.NewWindowStore(time.Hour))
	resp, err := svc.GetRecommendations(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetRecommendations: %v", err)
	}
	if resp.Degradation == nil {
		t.Fatal("expected degradation envelope; got nil")
	}
	if resp.Degradation.ThresholdSource != conventions.ThresholdSourceCompiledInDefaults {
		t.Errorf("thresholdSource = %q, want %q",
			resp.Degradation.ThresholdSource,
			conventions.ThresholdSourceCompiledInDefaults)
	}
	if resp.Degradation.Level != conventions.DegradationHealthy {
		t.Errorf("degradation level = %q, want %q",
			resp.Degradation.Level, conventions.DegradationHealthy)
	}
}

// fakeReader is a deterministic MetricReader for the formula and config
// tests: it returns controlled gauge/rate/quantile values regardless of
// the wall clock, and records the window each WindowedRate call used so
// the windowOverrides knob can be observed.
type fakeReader struct {
	gauges      map[string]float64
	rates       map[string]float64
	quantiles   map[string]float64
	seenWindows map[string]time.Duration
	available   *bool // non-nil makes the reader an AvailabilityReporter
}

func (f *fakeReader) GaugeValue(name string, _ map[string]string) (float64, bool) {
	v, ok := f.gauges[name]
	return v, ok
}

func (f *fakeReader) CounterValue(name string, l map[string]string) (float64, bool) {
	return f.GaugeValue(name, l)
}

func (f *fakeReader) HistogramQuantile(name string, _ map[string]string, _ float64) (float64, bool) {
	v, ok := f.quantiles[name]
	return v, ok
}

func (f *fakeReader) WindowedRate(name string, _ map[string]string, window time.Duration) (float64, bool) {
	if f.seenWindows != nil {
		f.seenWindows[name] = window
	}
	v, ok := f.rates[name]
	return v, ok
}

type availableReader struct {
	*fakeReader
}

func (a availableReader) Available() bool { return *a.fakeReader.available }

// TestWarmPoolTargetMinWarmFormula_spec_25_3_582 asserts the warm-pool
// recommendation derives minWarm = ceil(peak_claim_rate *
// (startup_p99 + failover_seconds) * 1.3) with failover_seconds = 25,
// and exposes it as a machine-executable SCALE_WARM_POOL target.
func TestWarmPoolTargetMinWarmFormula_spec_25_3_582(t *testing.T) {
	r := &fakeReader{
		rates: map[string]float64{
			"lenny_warmpool_exhausted_total": 0.001, // increase over 24h >= 3
			"lenny_warmpool_claims_total":    2.0,   // peak_claim_rate
		},
		quantiles: map[string]float64{"lenny_warmpool_pod_startup_duration_seconds": 20}, // startup_p99
	}
	svc := recommendations.NewCapacityService(r)
	resp, err := svc.GetRecommendations(context.Background(), nil)
	if err != nil {
		t.Fatalf("GetRecommendations: %v", err)
	}
	var rec *recommendations.Recommendation
	for i := range resp.Recommendations {
		if resp.Recommendations[i].Rule == "WarmPoolUndersized" {
			rec = &resp.Recommendations[i]
		}
	}
	if rec == nil {
		t.Fatalf("WarmPoolUndersized must trigger: %+v", resp.Recommendations)
	}
	if rec.Target == nil || rec.Target.Action != "SCALE_WARM_POOL" {
		t.Fatalf("target: %+v, want SCALE_WARM_POOL", rec.Target)
	}
	// ceil(2.0 * (20 + 25) * 1.3) = ceil(117.0) = 117
	got, ok := rec.Target.Body["minWarm"].(int)
	if !ok || got != 117 {
		t.Errorf("minWarm = %v (%T), want 117", rec.Target.Body["minWarm"], rec.Target.Body["minWarm"])
	}
}

// TestWarmPoolTargetFallsBackToPodWarmBaseline_spec_25_3_582 asserts the
// formula substitutes the §17.8 10s pod-warm baseline for startup_p99
// when the startup-latency histogram has no samples.
func TestWarmPoolTargetFallsBackToPodWarmBaseline_spec_25_3_582(t *testing.T) {
	r := &fakeReader{
		rates: map[string]float64{
			"lenny_warmpool_exhausted_total": 0.001,
			"lenny_warmpool_claims_total":    2.0,
		},
		// no startup quantile -> baseline 10s
	}
	svc := recommendations.NewCapacityService(r)
	resp, _ := svc.GetRecommendations(context.Background(), nil)
	for _, rec := range resp.Recommendations {
		if rec.Rule != "WarmPoolUndersized" {
			continue
		}
		// ceil(2.0 * (10 + 25) * 1.3) = ceil(91.0) = 91
		if got, _ := rec.Target.Body["minWarm"].(int); got != 91 {
			t.Errorf("minWarm with baseline startup = %v, want 91", rec.Target.Body["minWarm"])
		}
		return
	}
	t.Fatal("WarmPoolUndersized did not trigger")
}

// TestCredentialTargetAddCredentialsFormula_spec_25_3_583 asserts the
// credential recommendation derives addCredentials to bring utilization
// below 60% from the current pool size.
func TestCredentialTargetAddCredentialsFormula_spec_25_3_583(t *testing.T) {
	r := &fakeReader{gauges: map[string]float64{
		"lenny_credential_pool_utilization":      0.90,
		"lenny_credential_pool_assignable_count": 10,
	}}
	svc := recommendations.NewCapacityService(r)
	resp, _ := svc.GetRecommendations(context.Background(), nil)
	for _, rec := range resp.Recommendations {
		if rec.Rule != "CredentialPoolUndersized" {
			continue
		}
		if rec.Target == nil || rec.Target.Action != "ADD_CREDENTIALS" {
			t.Fatalf("target: %+v, want ADD_CREDENTIALS", rec.Target)
		}
		// ceil(10 * (0.90/0.60) - 10) = ceil(15 - 10) = 5
		if got, _ := rec.Target.Body["addCredentials"].(int); got != 5 {
			t.Errorf("addCredentials = %v, want 5", rec.Target.Body["addCredentials"])
		}
		return
	}
	t.Fatal("CredentialPoolUndersized did not trigger")
}

// TestRecommendationCarriesPriority_spec_25_3_618 asserts every emitted
// recommendation carries a priority band derived from its confidence.
func TestRecommendationCarriesPriority_spec_25_3_618(t *testing.T) {
	r := &fakeReader{gauges: map[string]float64{
		"lenny_credential_pool_utilization":   0.90, // confidence 0.8 -> high
		"lenny_gateway_cpu_utilization_ratio": 0.90, // confidence 0.7 -> medium
	}}
	svc := recommendations.NewCapacityService(r)
	resp, _ := svc.GetRecommendations(context.Background(), nil)
	want := map[string]string{
		"CredentialPoolUndersized": "high",
		"GatewayScalingPressure":   "medium",
	}
	seen := map[string]bool{}
	for _, rec := range resp.Recommendations {
		if w, ok := want[rec.Rule]; ok {
			seen[rec.Rule] = true
			if rec.Priority != w {
				t.Errorf("%s priority = %q, want %q", rec.Rule, rec.Priority, w)
			}
		}
	}
	for rule := range want {
		if !seen[rule] {
			t.Errorf("rule %s did not trigger", rule)
		}
	}
}

// TestDisabledRulesAreSkipped_spec_25_3_604 asserts a rule named in the
// disabled set never runs even when its condition holds.
func TestDisabledRulesAreSkipped_spec_25_3_604(t *testing.T) {
	r := &fakeReader{gauges: map[string]float64{"lenny_credential_pool_utilization": 0.90}}
	svc := recommendations.NewCapacityServiceWithConfig(r, recommendations.Config{
		DisabledRules: []string{"CredentialPoolUndersized"},
	})
	resp, _ := svc.GetRecommendations(context.Background(), nil)
	for _, rec := range resp.Recommendations {
		if rec.Rule == "CredentialPoolUndersized" {
			t.Errorf("disabled rule must not appear: %+v", resp.Recommendations)
		}
	}
}

// TestWindowOverrideAppliesToRule_spec_25_3_596 asserts a per-category
// window override changes the sliding window the rule evaluates over.
func TestWindowOverrideAppliesToRule_spec_25_3_596(t *testing.T) {
	r := &fakeReader{
		rates:       map[string]float64{"lenny_warmpool_exhausted_total": 0.001},
		seenWindows: map[string]time.Duration{},
	}
	svc := recommendations.NewCapacityServiceWithConfig(r, recommendations.Config{
		WindowOverrides: map[string]time.Duration{"warm_pool_sizing": 12 * time.Hour},
	})
	if _, err := svc.GetRecommendations(context.Background(), nil); err != nil {
		t.Fatalf("GetRecommendations: %v", err)
	}
	if got := r.seenWindows["lenny_warmpool_exhausted_total"]; got != 12*time.Hour {
		t.Errorf("warm-pool window = %v, want 12h (override applied)", got)
	}
}

// TestUnknownCategoryRejected_spec_25_3_624 asserts a category filter
// outside the closed enum returns ErrUnknownCategory.
func TestUnknownCategoryRejected_spec_25_3_624(t *testing.T) {
	svc := recommendations.NewCapacityService(&fakeReader{})
	bogus := "scaling_things"
	if _, err := svc.GetRecommendations(context.Background(), &bogus); err != recommendations.ErrUnknownCategory {
		t.Errorf("err = %v, want ErrUnknownCategory", err)
	}
}

// TestRecommendationsUnavailableOnOutage_spec_25_3_625 asserts that with
// disableOnPrometheusOutage set, an unavailable reader yields
// ErrRecommendationsUnavailable, and an available one computes normally.
func TestRecommendationsUnavailableOnOutage_spec_25_3_625(t *testing.T) {
	down := false
	reader := availableReader{&fakeReader{
		gauges:    map[string]float64{"lenny_credential_pool_utilization": 0.90},
		available: &down,
	}}
	svc := recommendations.NewCapacityServiceWithConfig(reader, recommendations.Config{DisableOnPrometheusOutage: true})

	if _, err := svc.GetRecommendations(context.Background(), nil); err != recommendations.ErrRecommendationsUnavailable {
		t.Errorf("reader down: err = %v, want ErrRecommendationsUnavailable", err)
	}

	down = true // now available
	resp, err := svc.GetRecommendations(context.Background(), nil)
	if err != nil {
		t.Fatalf("reader up: %v", err)
	}
	if len(resp.Recommendations) == 0 {
		t.Error("reader up must compute recommendations")
	}

	// Without the opt-out, an unavailable reader still computes (fallback).
	down = false
	svc2 := recommendations.NewCapacityServiceWithConfig(reader, recommendations.Config{})
	if _, err := svc2.GetRecommendations(context.Background(), nil); err != nil {
		t.Errorf("no opt-out: err = %v, want nil (fallback compute)", err)
	}
}
