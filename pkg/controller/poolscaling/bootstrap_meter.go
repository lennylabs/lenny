// SPDX-License-Identifier: MIT

package poolscaling

import (
	"fmt"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// poolBootstrapMode mirrors each pool's §17.8.2 cold-start scaling mode
// into Prometheus: 1 while the pool is pinned to its bootstrapMinWarm
// override (status.scalingMode: bootstrap), 0 once the §17.8.2 step-4
// convergence criteria are met or no override is set
// (status.scalingMode: formula). The §16.5 PoolBootstrapMode alert reads
// `lenny_pool_bootstrap_mode == 1`. spec: §17.8.2 steps 2, 5.
var poolBootstrapMode = func() *prometheus.GaugeVec {
	g, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_pool_bootstrap_mode",
		Help: "Pool bootstrap mode flag (1 active, 0 converged), read by the §16.5 PoolBootstrapMode alert.",
	}, []string{"pool"})
	if err != nil {
		panic(fmt.Sprintf("poolscaling: build bootstrap-mode gauge: %v", err))
	}
	metrics.MustRegister(ctrlmetrics.Registry, g)
	return g
}()

// poolBootstrapOverride mirrors the operator-set bootstrapMinWarm
// override for a bootstrap-eligible pool. The §16.5
// PoolBootstrapUnderprovisioned alert reads it as the denominator of
// `lenny_pool_bootstrap_target_min_warm > 3 * lenny_pool_bootstrap_min_warm_override`.
// spec: §17.8.2 step 4, step 5.
var poolBootstrapOverride = func() *prometheus.GaugeVec {
	g, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_pool_bootstrap_min_warm_override",
		Help: "Configured bootstrap minWarm override, read by PoolBootstrapUnderprovisioned.",
	}, []string{"pool"})
	if err != nil {
		panic(fmt.Sprintf("poolscaling: build bootstrap-override gauge: %v", err))
	}
	metrics.MustRegister(ctrlmetrics.Registry, g)
	return g
}()

// poolBootstrapTarget mirrors the formula-computed target minWarm for a
// bootstrap-eligible pool with observed demand. The §16.5
// PoolBootstrapUnderprovisioned alert reads it as the numerator of the
// 3× comparison against the override. It is only published while a
// formula target exists (demand observed); otherwise the series is
// dropped so the alert does not compare against a stale target. spec:
// §17.8.2 step 4.
var poolBootstrapTarget = func() *prometheus.GaugeVec {
	g, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_pool_bootstrap_target_min_warm",
		Help: "Formula-computed target minWarm for a bootstrapping pool, read by PoolBootstrapUnderprovisioned.",
	}, []string{"pool"})
	if err != nil {
		panic(fmt.Sprintf("poolscaling: build bootstrap-target gauge: %v", err))
	}
	metrics.MustRegister(ctrlmetrics.Registry, g)
	return g
}()

// bootstrapSample is the per-pool §17.8.2 cold-start gauge snapshot the
// reconciler publishes each clean reconcile.
type bootstrapSample struct {
	// BootstrapActive is true while the pool is pinned to its override
	// (status.scalingMode: bootstrap) — the lenny_pool_bootstrap_mode
	// value (1 when true, 0 otherwise).
	BootstrapActive bool
	// HasOverride reports whether an operator-set bootstrapMinWarm
	// override is in force. Only then are the override / target gauges
	// published, since the PoolBootstrapUnderprovisioned ratio is
	// undefined without an override.
	HasOverride bool
	// OverrideMinWarm is the override value, published when HasOverride.
	OverrideMinWarm int
	// HasFormula reports whether a formula target exists (demand
	// observed). The target gauge is published only when both
	// HasOverride and HasFormula hold.
	HasFormula bool
	// FormulaTarget is the formula-computed target minWarm.
	FormulaTarget int
}

// bootstrapModeMeter publishes the §17.8.2 cold-start gauges and tracks
// which pools have a series so the reconciler can drop a pool's series
// when it leaves the source, matching reconciliationLagMeter.forgetNotIn.
type bootstrapModeMeter struct {
	mu    sync.Mutex
	pools map[string]struct{}
}

func newBootstrapModeMeter() *bootstrapModeMeter {
	return &bootstrapModeMeter{pools: map[string]struct{}{}}
}

// Set publishes the pool's §17.8.2 cold-start gauges. The bootstrap-mode
// gauge is always written (1 active, 0 converged). The override and
// target gauges are written only while their value is meaningful and
// their series is dropped otherwise so the
// PoolBootstrapUnderprovisioned alert never compares against a stale or
// missing operand. spec: §17.8.2 steps 4, 5.
func (m *bootstrapModeMeter) Set(poolName string, s bootstrapSample) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.pools[poolName] = struct{}{}
	m.mu.Unlock()

	mode := 0.0
	if s.BootstrapActive {
		mode = 1.0
	}
	poolBootstrapMode.WithLabelValues(poolName).Set(mode)

	if s.HasOverride {
		poolBootstrapOverride.WithLabelValues(poolName).Set(float64(s.OverrideMinWarm))
	} else {
		poolBootstrapOverride.DeleteLabelValues(poolName)
	}

	if s.HasOverride && s.HasFormula {
		poolBootstrapTarget.WithLabelValues(poolName).Set(float64(s.FormulaTarget))
	} else {
		poolBootstrapTarget.DeleteLabelValues(poolName)
	}
}

// forgetNotIn drops every §17.8.2 cold-start series for a pool no longer
// in desired so the PoolBootstrapMode / PoolBootstrapUnderprovisioned
// alerts auto-resolve once a pool is removed from the source.
func (m *bootstrapModeMeter) forgetNotIn(desired map[string]struct{}) {
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
		poolBootstrapMode.DeleteLabelValues(pool)
		poolBootstrapOverride.DeleteLabelValues(pool)
		poolBootstrapTarget.DeleteLabelValues(pool)
	}
}
