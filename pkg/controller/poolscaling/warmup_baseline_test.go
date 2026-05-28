// SPDX-License-Identifier: MIT

package poolscaling

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
)

// gaugeValueForPool reads the lenny_pool_warmup_seconds_baseline value
// for one pool, reporting whether a series exists. It collects the
// GaugeVec directly so a deleted series reads as absent rather than as a
// recreated zero.
func gaugeValueForPool(t *testing.T, pool string) (float64, bool) {
	t.Helper()
	ch := make(chan prometheus.Metric, 64)
	poolWarmupBaselineSeconds.Collect(ch)
	close(ch)
	for m := range ch {
		var dm dto.Metric
		if err := m.Write(&dm); err != nil {
			t.Fatalf("write metric: %v", err)
		}
		for _, lp := range dm.Label {
			if lp.GetName() == "pool" && lp.GetValue() == pool {
				return dm.Gauge.GetValue(), true
			}
		}
	}
	return 0, false
}

// TestWarmupBaselineForAlertUsesExplicitBaseline verifies the alert
// threshold source mirrors the operator-configured per-pool baseline.
// spec: §16.5 line 488.
func TestWarmupBaselineForAlertUsesExplicitBaseline_spec_16_5_488(t *testing.T) {
	for _, baseline := range []int64{5, 30, 60, 90} {
		cfg := PoolConfig{ScalePolicy: &lennyv1.ScalePolicy{PodWarmupSecondsBaseline: baseline}}
		if got := warmupBaselineForAlert(cfg); got != float64(baseline) {
			t.Errorf("baseline %d: warmupBaselineForAlert = %v, want %v", baseline, got, float64(baseline))
		}
	}
}

// TestWarmupBaselineForAlertDefaultsTo30WhenUnset verifies an
// unconfigured pool falls back to the spec's 30s default so the alert
// fires at the prior fixed 60s threshold (2×30). spec: §16.5 line 488.
func TestWarmupBaselineForAlertDefaultsTo30WhenUnset_spec_16_5_488(t *testing.T) {
	cases := []struct {
		name string
		cfg  PoolConfig
	}{
		{"nil scale policy", PoolConfig{}},
		{"zero baseline", PoolConfig{ScalePolicy: &lennyv1.ScalePolicy{PodWarmupSecondsBaseline: 0}}},
		{"negative baseline", PoolConfig{ScalePolicy: &lennyv1.ScalePolicy{PodWarmupSecondsBaseline: -1}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := warmupBaselineForAlert(tc.cfg); got != alertWarmupBaselineDefaultSeconds {
				t.Errorf("warmupBaselineForAlert = %v, want %v", got, alertWarmupBaselineDefaultSeconds)
			}
		})
	}
}

// TestWarmupBaselineMeterSetAndForget verifies the meter publishes a
// per-pool series and drops the series for pools removed from the
// source. spec: §16.5 line 488.
func TestWarmupBaselineMeterSetAndForget_spec_16_5_488(t *testing.T) {
	const poolA, poolB = "warmup-meter-a", "warmup-meter-b"
	m := newWarmupBaselineMeter()
	t.Cleanup(func() {
		poolWarmupBaselineSeconds.DeleteLabelValues(poolA)
		poolWarmupBaselineSeconds.DeleteLabelValues(poolB)
	})

	m.Set(poolA, 45)
	m.Set(poolB, 30)
	if v, ok := gaugeValueForPool(t, poolA); !ok || v != 45 {
		t.Errorf("pool A gauge = (%v, %v), want (45, true)", v, ok)
	}
	if v, ok := gaugeValueForPool(t, poolB); !ok || v != 30 {
		t.Errorf("pool B gauge = (%v, %v), want (30, true)", v, ok)
	}

	// Pool B leaves the source; its series must be dropped while pool A
	// is retained so the alert auto-resolves only for the removed pool.
	m.forgetNotIn(map[string]struct{}{poolA: {}})
	if _, ok := gaugeValueForPool(t, poolB); ok {
		t.Error("pool B series should be forgotten after forgetNotIn")
	}
	if v, ok := gaugeValueForPool(t, poolA); !ok || v != 45 {
		t.Errorf("pool A gauge after forget = (%v, %v), want (45, true)", v, ok)
	}
}

// warmupFakeSource is a minimal PoolConfigSource for the Sync
// integration test.
type warmupFakeSource struct{ configs []PoolConfig }

func (f *warmupFakeSource) ListPoolConfigs(context.Context) ([]PoolConfig, error) {
	return f.configs, nil
}

// TestSyncEmitsWarmupBaselineGauge verifies a full reconcile pass
// mirrors each pool's baseline into the gauge and clears it when the
// pool is removed from the Postgres source. spec: §16.5 line 488.
func TestSyncEmitsWarmupBaselineGauge_spec_16_5_488(t *testing.T) {
	const pool = "warmup-sync-pool"
	t.Cleanup(func() { poolWarmupBaselineSeconds.DeleteLabelValues(pool) })

	s := runtime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	c := fake.NewClientBuilder().WithScheme(s).Build()
	cfg := PoolConfig{
		Name:      pool,
		Namespace: "lenny-agents",
		Template: lennyv1.SandboxTemplateSpec{
			RuntimeRef:       "claude-code",
			IsolationProfile: "sandboxed",
			ResourceClass:    "medium",
			ExecutionMode:    "session",
		},
		MinWarm:     1,
		MaxWarm:     2,
		ScalePolicy: &lennyv1.ScalePolicy{PodWarmupSecondsBaseline: 45},
	}
	src := &warmupFakeSource{configs: []PoolConfig{cfg}}
	r := &Reconciler{Client: c, Source: src}

	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if v, ok := gaugeValueForPool(t, pool); !ok || v != 45 {
		t.Errorf("gauge after sync = (%v, %v), want (45, true)", v, ok)
	}

	// Pool removed from the source — the next pass must drop the series.
	src.configs = nil
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("Sync (empty): %v", err)
	}
	if _, ok := gaugeValueForPool(t, pool); ok {
		t.Error("gauge series should be cleared after the pool leaves the source")
	}
}
