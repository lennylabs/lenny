// SPDX-License-Identifier: MIT

package gcpause_test

import (
	"context"
	"runtime/debug"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/gcpause"
)

// fakeSource serves the GCStats values the test prepares.
type fakeSource struct {
	mu    sync.Mutex
	stats debug.GCStats
}

func (f *fakeSource) set(pauses []time.Duration, ends []time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stats = debug.GCStats{
		Pause:    append([]time.Duration(nil), pauses...),
		PauseEnd: append([]time.Time(nil), ends...),
	}
}

func (f *fakeSource) ReadGCStats(stats *debug.GCStats) {
	f.mu.Lock()
	defer f.mu.Unlock()
	stats.Pause = append(stats.Pause[:0], f.stats.Pause...)
	stats.PauseEnd = append(stats.PauseEnd[:0], f.stats.PauseEnd...)
}

// recordingGauge captures every value the collector emits.
type recordingGauge struct {
	mu     sync.Mutex
	values []float64
}

func (g *recordingGauge) SetGCPauseP99Ms(value float64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.values = append(g.values, value)
}

func (g *recordingGauge) last() float64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.values) == 0 {
		return -1
	}
	return g.values[len(g.values)-1]
}

// fakeClock supplies a controllable time.Now substitute.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// spec: §4.1 — ReadGCStats with no pauses yields a 0 ms gauge value.
func TestCollectorEmptyStatsYieldsZero(t *testing.T) {
	src := &fakeSource{}
	gauge := &recordingGauge{}
	c := &gcpause.Collector{
		Source: src,
		Gauge:  gauge,
	}
	c.Sample()
	if got := gauge.last(); got != 0 {
		t.Fatalf("p99 with empty stats = %v ms, want 0", got)
	}
}

// spec: §4.1 — a single pause across a single sample produces p99 in
// milliseconds.
func TestCollectorOnePauseEmitsValueInMs(t *testing.T) {
	src := &fakeSource{}
	gauge := &recordingGauge{}
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	c := &gcpause.Collector{
		Source: src,
		Gauge:  gauge,
		Now:    clk.now,
	}
	src.set(
		[]time.Duration{75 * time.Millisecond},
		[]time.Time{clk.now()},
	)
	c.Sample()
	if got := gauge.last(); got != 75 {
		t.Fatalf("p99 = %v ms, want 75", got)
	}
}

// spec: §4.1 — the sliding window drops samples older than the
// configured Window so a fresh value reflects recent GC behaviour.
func TestCollectorSlidingWindowAgesOutOldSamples(t *testing.T) {
	src := &fakeSource{}
	gauge := &recordingGauge{}
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	c := &gcpause.Collector{
		Source: src,
		Gauge:  gauge,
		Now:    clk.now,
		Window: 1 * time.Minute,
	}
	// First sample: a 500ms pause two minutes ago. It is *already*
	// outside the 1-minute window, so it is dropped on the first
	// sample.
	src.set(
		[]time.Duration{500 * time.Millisecond},
		[]time.Time{clk.now().Add(-2 * time.Minute)},
	)
	c.Sample()
	if got := gauge.last(); got != 0 {
		t.Fatalf("p99 after window-drop = %v ms, want 0", got)
	}
	if got := c.SampleCount(); got != 0 {
		t.Fatalf("SampleCount after drop = %d, want 0", got)
	}
}

// spec: §4.1 — Sample appends only pauses observed since the last
// tick so a repeated ReadGCStats call (the runtime returns the
// trailing window every time) does not double-count.
func TestCollectorAppendsOnlyNewPauses(t *testing.T) {
	src := &fakeSource{}
	gauge := &recordingGauge{}
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	c := &gcpause.Collector{
		Source: src,
		Gauge:  gauge,
		Now:    clk.now,
		Window: 1 * time.Hour,
	}
	// First tick: one pause at t0.
	src.set(
		[]time.Duration{30 * time.Millisecond},
		[]time.Time{clk.now()},
	)
	c.Sample()
	if got := c.SampleCount(); got != 1 {
		t.Fatalf("SampleCount after first tick = %d, want 1", got)
	}

	// Advance the clock; ReadGCStats returns the SAME pause plus a
	// newer one (the runtime returns the trailing window).
	clk.advance(10 * time.Second)
	src.set(
		[]time.Duration{60 * time.Millisecond, 30 * time.Millisecond},
		[]time.Time{clk.now(), clk.now().Add(-10 * time.Second)},
	)
	c.Sample()
	if got := c.SampleCount(); got != 2 {
		t.Fatalf("SampleCount after second tick = %d, want 2 (no double count)", got)
	}
	// p99 across [30ms, 60ms] via nearest-rank ⌈0.99*2⌉-1 = 1 → 60ms.
	if got := gauge.last(); got != 60 {
		t.Fatalf("p99 = %v ms, want 60", got)
	}
}

// spec: §4.1 — p99 across many samples uses nearest-rank: the
// largest pause dominates so a long pause surfaces immediately.
func TestCollectorP99NearestRank(t *testing.T) {
	src := &fakeSource{}
	gauge := &recordingGauge{}
	clk := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	c := &gcpause.Collector{
		Source: src,
		Gauge:  gauge,
		Now:    clk.now,
		Window: 1 * time.Hour,
	}
	pauses := make([]time.Duration, 100)
	ends := make([]time.Time, 100)
	for i := 0; i < 100; i++ {
		pauses[i] = time.Duration(i+1) * time.Millisecond
		ends[i] = clk.now().Add(time.Duration(-i) * time.Second)
	}
	src.set(pauses, ends)
	c.Sample()
	// Sorted: 1..100; nearest-rank ⌈0.99*100⌉-1 = 98 → 99ms.
	if got := gauge.last(); got != 99 {
		t.Fatalf("p99 across 100 samples = %v ms, want 99", got)
	}
}

// spec: §4.1 — Run honours ctx.Done and exits without leaking.
func TestCollectorRunExitsOnCancel(t *testing.T) {
	src := &fakeSource{}
	gauge := &recordingGauge{}
	c := &gcpause.Collector{
		Source:   src,
		Gauge:    gauge,
		Interval: 5 * time.Millisecond,
	}
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		c.Run(ctx)
		close(done)
	}()
	// Let it sample at least once.
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not exit after cancel")
	}
}
