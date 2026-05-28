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

// idlePods is the §16.1 lenny_warmpool_idle_pods gauge: instantaneous
// count of warm pods in the idle (claimable) state, labeled by pool.
// spec: §17.8.2 line 1101 "First-week monitoring workflow" instructs
// operators to read this metric as a gauge ("if consistently near zero,
// minWarm is too low"). Multiple §16.5 alerting rules also join against
// it (WarmPoolExhausted, WarmPoolLow, PodClaimQueueBacklog). The
// WarmPoolController is the sole writer and refreshes the gauge on
// every reconcile; the §16.1 catalog declares it Gauge.
var idlePods = func() *prometheus.GaugeVec {
	g, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_warmpool_idle_pods",
		Help: "Warm pods available in the idle state (instantaneous, per pool).",
	}, []string{"pool"})
	if err != nil {
		panic(fmt.Sprintf("warmpool: build idle-pods gauge: %v", err))
	}
	metrics.MustRegister(ctrlmetrics.Registry, g)
	return g
}()

// setIdlePods publishes the pool's current idle-pod count to the §16.1
// lenny_warmpool_idle_pods gauge. Called once per reconcile so a
// controller restart re-establishes the series.
// spec: §17.8.2 line 1101.
func setIdlePods(pool string, count int) {
	idlePods.WithLabelValues(pool).Set(float64(count))
}

// forgetIdlePods clears a deleted pool's idle-pods gauge series so a
// removed pool does not leave a stale `lenny_warmpool_idle_pods` series
// behind.
func forgetIdlePods(pool string) {
	idlePods.DeleteLabelValues(pool)
}

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
