// SPDX-License-Identifier: MIT

// Package gcpause emits the §4.1 lenny_gateway_gc_pause_p99_ms gauge.
//
// The §4.1 shared-process GC pressure signal is the additional
// indicator that the combined gateway binary is becoming a bottleneck
// regardless of per-subsystem queue depth: a sustained p99 GC pause
// >50 ms gates the §16.5 Tier3GCPressureHigh alert and the Tier 3
// promotion criterion. This package owns the periodic
// runtime/debug.ReadGCStats sweep, a sliding-window p99 calculator,
// and the gauge emission.
//
// The collector is goroutine-safe and clock-injectable for tests.
package gcpause

import (
	"context"
	"runtime/debug"
	"sort"
	"sync"
	"time"
)

// DefaultInterval is the §4.1 collector sampling cadence. Five
// seconds is fast enough to surface a GC-pause spike within one
// Prometheus scrape interval but slow enough to keep the
// runtime/debug.ReadGCStats overhead negligible.
const DefaultInterval = 5 * time.Second

// DefaultWindow is the §4.1 sliding-window length. Ten minutes
// matches the §16.5 Tier3GCPressureHigh alert's "sustained" period
// and absorbs GC-pause variance from short-lived bursts.
const DefaultWindow = 10 * time.Minute

// GCStatsSource is the runtime/debug.ReadGCStats indirection. The
// production collector uses runtime/debug; unit tests substitute a
// fake that returns deterministic samples.
type GCStatsSource interface {
	ReadGCStats(stats *debug.GCStats)
}

// DefaultGCStatsSource is the production source backed by
// runtime/debug.ReadGCStats.
type DefaultGCStatsSource struct{}

// ReadGCStats forwards to runtime/debug.ReadGCStats.
func (DefaultGCStatsSource) ReadGCStats(stats *debug.GCStats) {
	debug.ReadGCStats(stats)
}

// Gauge accepts the p99 millisecond value the collector computes. It
// is satisfied by *gatewaymetrics.Metrics SetGCPauseP99Ms.
type Gauge interface {
	SetGCPauseP99Ms(value float64)
}

// Collector is the §4.1 periodic GC-pause sampler.
//
// On every tick the collector calls runtime/debug.ReadGCStats, appends
// any pauses that arrived since the previous tick to a sliding window
// bounded by Window, drops samples older than the window, computes the
// p99 across remaining samples, and pushes the value (in milliseconds)
// to the configured Gauge. When no pauses have been recorded yet, p99
// is 0 — the gateway has not stalled, so the gauge should not lie.
//
// spec: §4.1 lines 130-132
type Collector struct {
	// Source is the runtime/debug.ReadGCStats indirection. nil
	// selects DefaultGCStatsSource.
	Source GCStatsSource

	// Gauge receives the computed p99 millisecond value on every
	// tick. Required.
	Gauge Gauge

	// Interval is the tick cadence. Zero selects DefaultInterval.
	Interval time.Duration

	// Window is the sliding-window length. Zero selects DefaultWindow.
	Window time.Duration

	// Now overrides time.Now. Tests inject a controllable clock; the
	// production path leaves this nil.
	Now func() time.Time

	mu          sync.Mutex
	samples     []sample
	lastSeen    time.Time
	initialized bool
}

// sample is one observed pause with the wall-clock time it occurred.
type sample struct {
	at       time.Time
	pauseSec float64
}

// Sample collects one observation and updates the gauge. It is the
// unit of work each Run tick executes, exposed for tests so they can
// step through deterministic clock ticks.
func (c *Collector) Sample() {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	stats := debug.GCStats{}
	// Ask the runtime for every pause it knows about. The Go runtime
	// keeps the last 256 pause records by default; that bound is
	// well above what our 5s tick / 10 min window observes.
	stats.PauseQuantiles = nil
	c.source().ReadGCStats(&stats)

	// Append any newly observed pauses. PauseEnd[i] is the end-time
	// of pause i and Pause[i] its duration. Both slices are ordered
	// newest-first by the runtime.
	for i := 0; i < len(stats.Pause); i++ {
		if i >= len(stats.PauseEnd) {
			break
		}
		end := stats.PauseEnd[i]
		// On first tick, lastSeen is the zero value so every pause
		// the runtime reports counts. Subsequent ticks skip any
		// pause whose end-time is ≤ lastSeen (already accounted).
		if c.initialized && !end.After(c.lastSeen) {
			break
		}
		c.samples = append(c.samples, sample{at: end, pauseSec: stats.Pause[i].Seconds()})
	}
	c.initialized = true
	c.lastSeen = now

	// Drop samples older than the sliding window.
	cutoff := now.Add(-c.window())
	keep := c.samples[:0]
	for _, s := range c.samples {
		if !s.at.Before(cutoff) {
			keep = append(keep, s)
		}
	}
	c.samples = keep

	c.Gauge.SetGCPauseP99Ms(percentile99Ms(c.samples))
}

// Run drives Sample on the configured interval until ctx is cancelled.
// The first tick fires immediately so the gauge has a non-stale value
// at /metrics startup, then subsequent ticks fire on c.Interval.
func (c *Collector) Run(ctx context.Context) {
	if c.Gauge == nil {
		return
	}
	c.Sample()
	ticker := time.NewTicker(c.interval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.Sample()
		}
	}
}

// SampleCount returns the current number of observations in the
// sliding window; useful in tests that verify window aging.
func (c *Collector) SampleCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.samples)
}

func (c *Collector) source() GCStatsSource {
	if c.Source != nil {
		return c.Source
	}
	return DefaultGCStatsSource{}
}

func (c *Collector) interval() time.Duration {
	if c.Interval > 0 {
		return c.Interval
	}
	return DefaultInterval
}

func (c *Collector) window() time.Duration {
	if c.Window > 0 {
		return c.Window
	}
	return DefaultWindow
}

func (c *Collector) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// percentile99Ms returns the 99th-percentile pause across the supplied
// samples, in milliseconds. The percentile is computed via the
// nearest-rank method so a small-N window degrades gracefully toward
// the maximum sample.
func percentile99Ms(samples []sample) float64 {
	if len(samples) == 0 {
		return 0
	}
	pauseSecs := make([]float64, len(samples))
	for i, s := range samples {
		pauseSecs[i] = s.pauseSec
	}
	sort.Float64s(pauseSecs)
	// nearest-rank: rank = ceil(0.99 * N); 0-indexed slot = rank - 1.
	// math.Ceil avoids the off-by-one int(0.99*100)==99 case that
	// returns the wrong slot for an N that is a multiple of 100.
	n := len(pauseSecs)
	rank := int(ceilFloat(0.99 * float64(n)))
	if rank < 1 {
		rank = 1
	}
	if rank > n {
		rank = n
	}
	return pauseSecs[rank-1] * 1000
}

// ceilFloat avoids importing math for a single ceiling.
func ceilFloat(v float64) float64 {
	iv := int64(v)
	if float64(iv) < v {
		return float64(iv + 1)
	}
	return float64(iv)
}
