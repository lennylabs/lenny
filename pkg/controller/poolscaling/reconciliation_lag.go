// SPDX-License-Identifier: MIT

package poolscaling

import (
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// reconciliationLagSeconds is the §4.6.2 line 557
// lenny_pool_config_reconciliation_lag_seconds gauge labeled by pool.
// The controller resets it to zero on every successful reconciliation
// cycle (item 1) and the PoolConfigDrift alert in §16.5 fires when it
// stays above 60s, so the gauge is the join point between the §4.6.2
// drift design and the §16.5 alert. spec:
// spec/04_system-components.md lines 557-560.
var reconciliationLagSeconds = func() *prometheus.GaugeVec {
	g, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_pool_config_reconciliation_lag_seconds",
		Help: "Time since the last successful CRD reconciliation for the pool.",
	}, []string{"pool"})
	if err != nil {
		panic(fmt.Sprintf("poolscaling: build reconciliation-lag gauge: %v", err))
	}
	metrics.MustRegister(ctrlmetrics.Registry, g)
	return g
}()

// reconciliationLagMeter tracks the last successful reconciliation
// instant per pool so the gauge reports the wall-clock age of that
// observation. The reconciler resets the recorded instant on a clean
// apply; the gauge value is sampled at scrape time via a lightweight
// periodic refresher so the reading is current.
type reconciliationLagMeter struct {
	mu    sync.Mutex
	last  map[string]time.Time
	clock func() time.Time
}

func newReconciliationLagMeter(clock func() time.Time) *reconciliationLagMeter {
	if clock == nil {
		clock = time.Now
	}
	return &reconciliationLagMeter{last: map[string]time.Time{}, clock: clock}
}

// MarkSynced records a clean reconciliation for poolName and resets
// the gauge to zero. spec: §4.6.2 line 557 — "On every successful
// reconciliation cycle, the controller resets the gauge to zero".
func (m *reconciliationLagMeter) MarkSynced(poolName string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.last[poolName] = m.clock()
	m.mu.Unlock()
	reconciliationLagSeconds.WithLabelValues(poolName).Set(0)
}

// Forget removes a pool's series (e.g., on soft delete). The series
// removal lets the §16.5 PoolConfigDrift alert auto-resolve once the
// pool is gone.
func (m *reconciliationLagMeter) Forget(poolName string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	delete(m.last, poolName)
	m.mu.Unlock()
	reconciliationLagSeconds.DeleteLabelValues(poolName)
}

// forgetNotIn drops every tracked pool whose name is not in desired,
// removing each gauge series so the §16.5 PoolConfigDrift alert
// auto-resolves once a pool is deleted from Postgres.
func (m *reconciliationLagMeter) forgetNotIn(desired map[string]struct{}) {
	if m == nil {
		return
	}
	m.mu.Lock()
	stale := make([]string, 0)
	for pool := range m.last {
		if _, kept := desired[pool]; !kept {
			stale = append(stale, pool)
		}
	}
	for _, pool := range stale {
		delete(m.last, pool)
	}
	m.mu.Unlock()
	for _, pool := range stale {
		reconciliationLagSeconds.DeleteLabelValues(pool)
	}
}

// Sample refreshes every pool's gauge to the wall-clock age of its
// last successful reconciliation. Call it from the reconcile loop tick
// (the controller's Sync is already invoked periodically by Runnable),
// so the gauge advances even on quiet pools without writing into the
// store. A pool that has never synced is skipped (the spec's design
// has the gauge "grow monotonically" only after a successful
// reconciliation has been recorded).
func (m *reconciliationLagMeter) Sample() {
	if m == nil {
		return
	}
	now := m.clock()
	m.mu.Lock()
	defer m.mu.Unlock()
	for pool, last := range m.last {
		if last.IsZero() {
			continue
		}
		age := now.Sub(last).Seconds()
		if age < 0 {
			age = 0
		}
		reconciliationLagSeconds.WithLabelValues(pool).Set(age)
	}
}
