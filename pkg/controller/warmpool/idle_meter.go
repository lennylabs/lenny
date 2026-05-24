// SPDX-License-Identifier: MIT

package warmpool

import (
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// defaultIdleMeterInterval is the period the WarmPoolController
// re-reconciles a pool to advance the idle-pod-minutes integral when no
// pod event has fired. spec: §4.6.1 "Idle cost visibility and
// scale-to-zero" — the metric tracks cumulative idle pod-minutes, so
// the meter must keep accruing for pools whose idle pods sit unchanged.
const defaultIdleMeterInterval = 60 * time.Second

// idlePodMinutes is the §4.6.1 lenny_warmpool_idle_pod_minutes counter:
// cumulative warm-pool idle pod-minutes, labeled by pool and resource
// class, letting deployers estimate warm pool cost from their
// monitoring stack. It is registered against the controller-runtime
// metrics registry so it is exposed on the controller's existing
// /metrics endpoint. A duplicate registration (two reconcilers in one
// process) is tolerated by metrics.MustRegister.
var idlePodMinutes = func() *prometheus.CounterVec {
	c, err := metrics.NewCounter(prometheus.CounterOpts{
		Name: "lenny_warmpool_idle_pod_minutes",
		Help: "Cumulative warm pool idle pod-minutes.",
	}, []string{"pool", "resource_class"})
	if err != nil {
		panic(fmt.Sprintf("warmpool: build idle-pod-minutes counter: %v", err))
	}
	metrics.MustRegister(ctrlmetrics.Registry, c)
	return c
}()

// idleSample records the idle pod count observed for a pool at a point
// in time. The meter integrates idle pods over wall-clock time using
// the count observed at the start of each interval (a left-Riemann
// integral), so the cumulative counter approximates ∫ idle(t) dt.
type idleSample struct {
	at   time.Time
	idle int
}

// idleMeter accumulates idle-pod-minutes per pool across reconciles. It
// is safe for concurrent use; the controller-runtime queue serializes
// reconciles per object, but distinct pools reconcile in parallel.
type idleMeter struct {
	mu      sync.Mutex
	samples map[string]idleSample
	now     func() time.Time
}

// observe records idleNow idle pods for the pool keyed by key (the
// namespace/name) at the current time, and increments the
// idle-pod-minutes counter by the idle count observed over the elapsed
// interval. The first observation of a pool only sets the baseline; no
// time has elapsed yet so nothing is accrued.
func (m *idleMeter) observe(key, pool, resourceClass string, idleNow int) {
	now := time.Now
	if m.now != nil {
		now = m.now
	}
	at := now()

	m.mu.Lock()
	if m.samples == nil {
		m.samples = make(map[string]idleSample)
	}
	prev, seen := m.samples[key]
	m.samples[key] = idleSample{at: at, idle: idleNow}
	m.mu.Unlock()

	if !seen {
		return
	}
	elapsed := at.Sub(prev.at)
	if elapsed <= 0 || prev.idle <= 0 {
		// A clock that did not advance, or an interval with no idle pods,
		// accrues nothing. Guarding against a non-positive elapsed keeps
		// the monotonic counter from moving backward on clock skew.
		return
	}
	idlePodMinutes.
		WithLabelValues(pool, resourceClass).
		Add(float64(prev.idle) * elapsed.Minutes())
}

// forget drops a pool's accrual state. The next observe re-baselines
// without back-filling the idle gap across the deletion, which is the
// intended behavior: a deleted-then-recreated pool starts a fresh
// integral.
func (m *idleMeter) forget(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.samples, key)
}
