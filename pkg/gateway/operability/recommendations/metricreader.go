// SPDX-License-Identifier: MIT

// Package recommendations implements the §25.3 Capacity Recommendations
// service. This file holds the MetricReader the rules engine reads
// through and the in-memory windowed metric store that backs it on the
// gateway.
package recommendations

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// MetricReader is the §25.3 read interface the capacity-recommendation
// rules evaluate against. The gateway backs it with the in-process
// WindowStore; lenny-ops backs it with a Prometheus-backed reader.
type MetricReader interface {
	GaugeValue(name string, labels map[string]string) (float64, bool)
	CounterValue(name string, labels map[string]string) (float64, bool)
	HistogramQuantile(name string, labels map[string]string, quantile float64) (float64, bool)
	WindowedRate(name string, labels map[string]string, window time.Duration) (float64, bool)
}

// DefaultMaxSamplesPerSeries bounds the ring buffer of one series so a
// high-emission metric cannot grow without limit (§25.3 memory budget).
const DefaultMaxSamplesPerSeries = 16384

// sample is one recorded metric observation.
type sample struct {
	at    time.Time
	value float64
}

// WindowStore is the §25.3 in-memory sliding-window metric store. It
// keeps a bounded ring buffer of samples per (metric, label-set) series
// and answers the MetricReader queries from those buffers. It holds no
// Postgres or Redis state; after a gateway restart the windows are
// empty and a rule reading the store has no data.
type WindowStore struct {
	mu     sync.Mutex
	now    func() time.Time
	window time.Duration
	maxLen int
	series map[string][]sample
}

var _ MetricReader = (*WindowStore)(nil)

// NewWindowStore returns an empty store that retains samples for the
// given window. A non-positive window falls back to 24h.
func NewWindowStore(window time.Duration) *WindowStore {
	if window <= 0 {
		window = 24 * time.Hour
	}
	return &WindowStore{
		now:    time.Now,
		window: window,
		maxLen: DefaultMaxSamplesPerSeries,
		series: map[string][]sample{},
	}
}

// seriesKey composes a stable key for a (metric, label-set) series so
// two callers with the same labels in any order address one buffer.
func seriesKey(name string, labels map[string]string) string {
	if len(labels) == 0 {
		return name
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(name)
	for _, k := range keys {
		b.WriteByte('\x00')
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(labels[k])
	}
	return b.String()
}

// Record appends a sample for the (name, labels) series at the current
// time, evicting samples older than the retention window and capping
// the buffer length.
func (w *WindowStore) Record(name string, labels map[string]string, value float64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	key := seriesKey(name, labels)
	s := append(w.series[key], sample{at: now, value: value})
	w.series[key] = w.evict(s, now)
}

// evict drops samples older than the retention window and caps the
// buffer to maxLen, copying to a fresh slice only when it must trim.
func (w *WindowStore) evict(s []sample, now time.Time) []sample {
	cutoff := now.Add(-w.window)
	start := 0
	for start < len(s) && s[start].at.Before(cutoff) {
		start++
	}
	if len(s)-start > w.maxLen {
		start = len(s) - w.maxLen
	}
	if start == 0 {
		return s
	}
	return append([]sample(nil), s[start:]...)
}

// windowed returns the samples of a series within the trailing window.
// A non-positive window returns the whole retained series.
func (w *WindowStore) windowed(key string, window time.Duration) []sample {
	s := w.series[key]
	if window <= 0 {
		return s
	}
	cutoff := w.now().Add(-window)
	start := 0
	for start < len(s) && s[start].at.Before(cutoff) {
		start++
	}
	return s[start:]
}

// approxSampleBytes is the per-sample memory estimate for the ring-buffer
// gauge: a time.Time plus a float64 plus slice overhead. It backs the
// §25.3 lenny_recommendations_ring_buffer_bytes gauge, which only needs
// to be close enough to drive the >100 MB alert. spec: §25.3 line 598.
const approxSampleBytes = 32

// ApproxBytes estimates the store's current memory use: the retained
// samples across every series plus the series-key strings. It backs the
// §25.3 lenny_recommendations_ring_buffer_bytes gauge. spec: §25.3
// line 598.
func (w *WindowStore) ApproxBytes() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	total := 0
	for key, s := range w.series {
		total += len(key) + len(s)*approxSampleBytes
	}
	return total
}

// GaugeValue returns the most recent sample of a gauge series.
func (w *WindowStore) GaugeValue(name string, labels map[string]string) (float64, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := w.series[seriesKey(name, labels)]
	if len(s) == 0 {
		return 0, false
	}
	return s[len(s)-1].value, true
}

// CounterValue returns the most recent sample of a counter series. A
// counter is cumulative, so its latest sample is its current total.
func (w *WindowStore) CounterValue(name string, labels map[string]string) (float64, bool) {
	return w.GaugeValue(name, labels)
}

// WindowedRate returns the per-second rate of a counter series over the
// trailing window: the increase across the window divided by the
// window in seconds. It needs at least two samples in the window.
func (w *WindowStore) WindowedRate(name string, labels map[string]string, window time.Duration) (float64, bool) {
	if window <= 0 {
		return 0, false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	s := w.windowed(seriesKey(name, labels), window)
	if len(s) < 2 {
		return 0, false
	}
	delta := s[len(s)-1].value - s[0].value
	return delta / window.Seconds(), true
}

// HistogramQuantile returns the requested quantile of the recorded
// sample values of a series. The in-process store keeps raw
// observations, so the quantile is computed over the retained samples
// using nearest-rank interpolation.
func (w *WindowStore) HistogramQuantile(name string, labels map[string]string, quantile float64) (float64, bool) {
	if quantile < 0 || quantile > 1 {
		return 0, false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	s := w.series[seriesKey(name, labels)]
	if len(s) == 0 {
		return 0, false
	}
	vals := make([]float64, len(s))
	for i, x := range s {
		vals[i] = x.value
	}
	sort.Float64s(vals)
	idx := int(quantile*float64(len(vals)-1) + 0.5)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(vals) {
		idx = len(vals) - 1
	}
	return vals[idx], true
}
