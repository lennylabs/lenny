// SPDX-License-Identifier: MIT

package recommendations

import (
	"testing"
	"time"
)

// newTestStore returns a store with a controllable clock.
func newTestStore(window time.Duration) (*WindowStore, *time.Time) {
	w := NewWindowStore(window)
	clock := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	now := &clock
	w.now = func() time.Time { return *now }
	return w, now
}

func TestGaugeAndCounterValueReturnLatest(t *testing.T) {
	w, now := newTestStore(time.Hour)
	if _, ok := w.GaugeValue("g", nil); ok {
		t.Error("an empty series must report no value")
	}
	w.Record("g", nil, 1)
	*now = now.Add(time.Minute)
	w.Record("g", nil, 5)
	if v, ok := w.GaugeValue("g", nil); !ok || v != 5 {
		t.Errorf("GaugeValue = %v, %v; want 5, true", v, ok)
	}
	if v, ok := w.CounterValue("g", nil); !ok || v != 5 {
		t.Errorf("CounterValue = %v, %v; want 5, true", v, ok)
	}
}

func TestWindowedRateComputesIncreasePerSecond(t *testing.T) {
	w, now := newTestStore(time.Hour)
	w.Record("c", nil, 100)
	*now = now.Add(10 * time.Minute)
	w.Record("c", nil, 700)
	// Increase of 600 over a 10-minute (600s) window => 1.0/s.
	rate, ok := w.WindowedRate("c", nil, 10*time.Minute)
	if !ok || rate != 1.0 {
		t.Errorf("WindowedRate = %v, %v; want 1.0, true", rate, ok)
	}
	if _, ok := w.WindowedRate("c", nil, 0); ok {
		t.Error("a non-positive window must report no rate")
	}
}

func TestWindowedRateRespectsTrailingWindow(t *testing.T) {
	w, now := newTestStore(time.Hour)
	w.Record("c", nil, 0) // t=0, outside a 5-minute trailing window later
	*now = now.Add(20 * time.Minute)
	w.Record("c", nil, 200) // t=20m
	*now = now.Add(5 * time.Minute)
	w.Record("c", nil, 500) // t=25m
	// A 5-minute window sees only the t=20m and t=25m samples: 300 / 300s.
	rate, ok := w.WindowedRate("c", nil, 5*time.Minute)
	if !ok || rate != 1.0 {
		t.Errorf("trailing-window rate = %v, %v; want 1.0, true", rate, ok)
	}
}

func TestHistogramQuantile(t *testing.T) {
	w, _ := newTestStore(time.Hour)
	for _, v := range []float64{10, 20, 30, 40, 50, 60, 70, 80, 90, 100} {
		w.Record("h", nil, v)
	}
	p50, ok := w.HistogramQuantile("h", nil, 0.5)
	if !ok || p50 != 60 {
		t.Errorf("p50 = %v, %v; want 60, true", p50, ok)
	}
	p99, _ := w.HistogramQuantile("h", nil, 0.99)
	if p99 != 100 {
		t.Errorf("p99 = %v, want 100", p99)
	}
	if _, ok := w.HistogramQuantile("h", nil, 1.5); ok {
		t.Error("an out-of-range quantile must report no value")
	}
	if _, ok := w.HistogramQuantile("missing", nil, 0.5); ok {
		t.Error("an empty series must report no quantile")
	}
}

func TestRetentionWindowEvictsOldSamples(t *testing.T) {
	w, now := newTestStore(10 * time.Minute)
	w.Record("g", nil, 1)
	*now = now.Add(20 * time.Minute) // push the first sample past retention
	w.Record("g", nil, 2)
	// Only the second sample is retained.
	w.mu.Lock()
	got := len(w.series[seriesKey("g", nil)])
	w.mu.Unlock()
	if got != 1 {
		t.Errorf("retained samples = %d, want 1 (old sample evicted)", got)
	}
}

func TestSeriesKeyIsLabelOrderIndependent(t *testing.T) {
	w, _ := newTestStore(time.Hour)
	w.Record("m", map[string]string{"a": "1", "b": "2"}, 42)
	// The same labels in a different declaration order address one series.
	if v, ok := w.GaugeValue("m", map[string]string{"b": "2", "a": "1"}); !ok || v != 42 {
		t.Errorf("label-order-independent lookup = %v, %v; want 42, true", v, ok)
	}
	if _, ok := w.GaugeValue("m", map[string]string{"a": "1"}); ok {
		t.Error("a different label set must be a distinct series")
	}
}

func TestMaxSamplesPerSeriesCapsBuffer(t *testing.T) {
	w, _ := newTestStore(time.Hour)
	w.maxLen = 5
	for i := 0; i < 20; i++ {
		w.Record("g", nil, float64(i))
	}
	w.mu.Lock()
	got := len(w.series[seriesKey("g", nil)])
	w.mu.Unlock()
	if got != 5 {
		t.Errorf("buffer length = %d, want 5 (capped)", got)
	}
	// The cap keeps the most recent samples.
	if v, _ := w.GaugeValue("g", nil); v != 19 {
		t.Errorf("latest after cap = %v, want 19", v)
	}
}
