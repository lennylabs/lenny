// SPDX-License-Identifier: MIT

package poolscaling

import (
	"fmt"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// alertWarmupBaselineDefaultSeconds is the per-pool warm-up baseline the
// §16.5 WarmPoolReplenishmentSlow alert assumes for a pool that does not
// set scalingPolicy.podWarmupSecondsBaseline. spec:
// spec/16_observability.md line 488 — "2× the pool's
// scalingPolicy.podWarmupSecondsBaseline (default: 30s)". 2×30 = 60s
// reproduces the alert's prior fixed threshold for an unconfigured pool
// while a pool that sets an explicit baseline gets a per-pool 2×
// threshold. This default is distinct from defaultPodWarmupSeconds (10s,
// the §4.6.2 scaling-formula pod-warm fallback): the formula sizes
// optimistically for pod-warm pools, while the alert defaults to the
// field's nominal 30s so an under-provisioned pool still pages.
const alertWarmupBaselineDefaultSeconds = 30.0

// warmupBaselineForAlert resolves the per-pool warm-up baseline the
// §16.5 WarmPoolReplenishmentSlow alert compares P95 startup against. It
// mirrors the operator-configured scalingPolicy.podWarmupSecondsBaseline
// when set, falling back to alertWarmupBaselineDefaultSeconds. spec:
// §16.5 line 488.
func warmupBaselineForAlert(cfg PoolConfig) float64 {
	if cfg.ScalePolicy != nil && cfg.ScalePolicy.PodWarmupSecondsBaseline > 0 {
		return float64(cfg.ScalePolicy.PodWarmupSecondsBaseline)
	}
	return alertWarmupBaselineDefaultSeconds
}

// poolWarmupBaselineSeconds mirrors each pool's
// scalingPolicy.podWarmupSecondsBaseline into Prometheus as the per-pool
// scalar the §16.5 WarmPoolReplenishmentSlow alert reads. The alert
// fires when P95 of lenny_warmpool_pod_startup_duration_seconds exceeds
// 2× this gauge, so the threshold tracks the operator-configured
// per-pool baseline rather than a single hard-coded value. The
// PoolScalingController owns the SandboxWarmPool spec, so it is the
// emit point that keeps the alert threshold in lock-step with the
// configured baseline. spec: spec/16_observability.md line 488.
var poolWarmupBaselineSeconds = func() *prometheus.GaugeVec {
	g, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_pool_warmup_seconds_baseline",
		Help: "Per-pool scalingPolicy.podWarmupSecondsBaseline mirrored for the §16.5 WarmPoolReplenishmentSlow alert, which fires at 2× this value.",
	}, []string{"pool"})
	if err != nil {
		panic(fmt.Sprintf("poolscaling: build warmup-baseline gauge: %v", err))
	}
	metrics.MustRegister(ctrlmetrics.Registry, g)
	return g
}()

// warmupBaselineMeter mirrors the per-pool warm-up baseline to the
// poolWarmupBaselineSeconds gauge and tracks which pools have a series
// so the reconciler can drop a pool's series when it leaves the Postgres
// source (mirroring reconciliationLagMeter.forgetNotIn). The gauge value
// is a static mirror, refreshed on every clean reconciliation.
type warmupBaselineMeter struct {
	mu    sync.Mutex
	pools map[string]struct{}
}

func newWarmupBaselineMeter() *warmupBaselineMeter {
	return &warmupBaselineMeter{pools: map[string]struct{}{}}
}

// Set publishes the pool's warm-up baseline (seconds) to the gauge so
// the §16.5 WarmPoolReplenishmentSlow alert evaluates 2× the
// operator-configured value. spec: §16.5 line 488.
func (m *warmupBaselineMeter) Set(poolName string, seconds float64) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.pools[poolName] = struct{}{}
	m.mu.Unlock()
	poolWarmupBaselineSeconds.WithLabelValues(poolName).Set(seconds)
}

// forgetNotIn drops the series for any pool no longer in desired so the
// WarmPoolReplenishmentSlow alert auto-resolves once a pool is removed
// from the Postgres source, matching the reconciliation-lag gauge.
func (m *warmupBaselineMeter) forgetNotIn(desired map[string]struct{}) {
	if m == nil {
		return
	}
	m.mu.Lock()
	stale := make([]string, 0)
	for pool := range m.pools {
		if _, kept := desired[pool]; !kept {
			stale = append(stale, pool)
		}
	}
	for _, pool := range stale {
		delete(m.pools, pool)
	}
	m.mu.Unlock()
	for _, pool := range stale {
		poolWarmupBaselineSeconds.DeleteLabelValues(pool)
	}
}
