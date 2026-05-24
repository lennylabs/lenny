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

// defaultInitialFillGracePeriod is the §4.6.1 "Cold-start pool fill"
// window during which the WarmPoolExhausted and WarmPoolLow alerts are
// suppressed to avoid false positives while a fresh or re-activated
// pool fills toward minWarm. spec: §4.6.1 — "--initial-fill-grace-period,
// default: 120s from pool creation or controller startup".
const defaultInitialFillGracePeriod = 120 * time.Second

// fillDurationSeconds is the §4.6.1 lenny_warmpool_fill_duration_seconds
// histogram: time from pool creation (or a minWarm 0→positive
// re-activation) to reaching minWarm ready pods, letting operators set
// cold-start recovery SLO targets. spec: §4.6.1 "Cold-start pool fill".
var fillDurationSeconds = func() *prometheus.HistogramVec {
	h, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name:    "lenny_warmpool_fill_duration_seconds",
		Help:    "Time from pool creation to reaching minWarm ready pods.",
		Buckets: prometheus.ExponentialBuckets(1, 2, 10),
	}, []string{"pool"})
	if err != nil {
		panic(fmt.Sprintf("warmpool: build fill-duration histogram: %v", err))
	}
	metrics.MustRegister(ctrlmetrics.Registry, h)
	return h
}()

// fillGraceActive is the alert-support gauge the §16.5 WarmPoolExhausted
// and WarmPoolLow rules join against to suppress false positives during
// the §4.6.1 initial-fill grace period. It is 1 for a pool inside its
// grace window and 0 (or absent) otherwise. spec: §4.6.1 "Cold-start
// pool fill" and "Re-activation grace period".
var fillGraceActive = func() *prometheus.GaugeVec {
	g, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_warmpool_fill_grace_active",
		Help: "1 while a warm pool is inside its initial-fill grace period, 0 otherwise.",
	}, []string{"pool"})
	if err != nil {
		panic(fmt.Sprintf("warmpool: build fill-grace gauge: %v", err))
	}
	metrics.MustRegister(ctrlmetrics.Registry, g)
	return g
}()

// fillState tracks one pool's cold-start fill progress across
// reconciles.
type fillState struct {
	// startedAt is when the current fill window opened: the pool's first
	// observation by this controller, or the most recent minWarm
	// 0→positive transition.
	startedAt time.Time
	// recorded is true once the fill duration for the current window has
	// been observed into the histogram, so a flapping ready count does
	// not double-count a single fill.
	recorded bool
	// prevMinWarm is the minWarm observed on the previous reconcile, used
	// to detect the §4.6.1 re-activation transition.
	prevMinWarm int
}

// fillMeter records §4.6.1 cold-start fill durations and maintains the
// per-pool grace-active gauge. It is safe for concurrent use across the
// per-pool reconciles the controller-runtime queue runs in parallel.
type fillMeter struct {
	mu     sync.Mutex
	pools  map[string]*fillState
	now    func() time.Time
	period time.Duration
}

// observe records the pool's fill progress for one reconcile. key is
// the namespace/name; minWarm and ready are the pool's target and its
// current ready (idle) pod count. It opens a fresh fill window on the
// first observation and on every minWarm 0→positive transition, records
// the fill duration once ready first reaches minWarm, and sets the
// grace-active gauge for the pool.
func (m *fillMeter) observe(key, pool string, minWarm, ready int) {
	now := time.Now
	if m.now != nil {
		now = m.now
	}
	at := now()
	grace := m.period
	if grace <= 0 {
		grace = defaultInitialFillGracePeriod
	}

	m.mu.Lock()
	if m.pools == nil {
		m.pools = make(map[string]*fillState)
	}
	st, seen := m.pools[key]
	if !seen {
		st = &fillState{startedAt: at, prevMinWarm: minWarm}
		m.pools[key] = st
	} else if st.prevMinWarm == 0 && minWarm > 0 {
		// §4.6.1 re-activation grace period: a minWarm 0→positive
		// transition reopens the fill window.
		st.startedAt = at
		st.recorded = false
	}
	st.prevMinWarm = minWarm

	started := st.startedAt
	record := minWarm > 0 && !st.recorded && ready >= minWarm
	if record {
		st.recorded = true
	}
	m.mu.Unlock()

	if record {
		elapsed := at.Sub(started).Seconds()
		if elapsed < 0 {
			elapsed = 0
		}
		fillDurationSeconds.WithLabelValues(pool).Observe(elapsed)
	}
	m.setGrace(pool, minWarm, at, started, grace)
}

// setGrace publishes the grace-active gauge for a pool: 1 when minWarm
// is positive and now is within the grace window from the fill start, 0
// otherwise.
func (m *fillMeter) setGrace(pool string, minWarm int, now, startedAt time.Time, grace time.Duration) {
	active := 0.0
	if minWarm > 0 && now.Before(startedAt.Add(grace)) {
		active = 1.0
	}
	fillGraceActive.WithLabelValues(pool).Set(active)
}

// forget drops a pool's fill state and clears its grace gauge series. A
// deleted-then-recreated pool re-baselines its fill window.
func (m *fillMeter) forget(key, pool string) {
	m.mu.Lock()
	delete(m.pools, key)
	m.mu.Unlock()
	fillGraceActive.DeleteLabelValues(pool)
}
