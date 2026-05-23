// SPDX-License-Identifier: MIT

package loadgen

import (
	"math"
	"sort"
	"sync"
	"time"
)

// Histogram collects observation samples and produces percentiles.
// The implementation stores raw samples and sorts on Quantile;
// scenarios that produce more than a few hundred thousand samples
// should not use the default Histogram and may attach a streaming
// summary (e.g. t-digest) through Result.Custom.
type Histogram struct {
	mu      sync.Mutex
	samples []float64
	count   int64
	sum     float64
	min     float64
	max     float64
}

// NewHistogram returns an empty Histogram.
func NewHistogram() *Histogram {
	return &Histogram{
		samples: make([]float64, 0, 1024),
		min:     math.Inf(1),
		max:     math.Inf(-1),
	}
}

// Observe records v. Safe to call concurrently.
func (h *Histogram) Observe(v float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.samples = append(h.samples, v)
	h.count++
	h.sum += v
	if v < h.min {
		h.min = v
	}
	if v > h.max {
		h.max = v
	}
}

// ObserveDuration records d as seconds.
func (h *Histogram) ObserveDuration(d time.Duration) {
	h.Observe(d.Seconds())
}

// Count returns the total observation count.
func (h *Histogram) Count() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.count
}

// Quantile returns the q-quantile (0.0..1.0). Returns 0 when empty.
func (h *Histogram) Quantile(q float64) float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.samples) == 0 {
		return 0
	}
	sorted := make([]float64, len(h.samples))
	copy(sorted, h.samples)
	sort.Float64s(sorted)
	if q <= 0 {
		return sorted[0]
	}
	if q >= 1 {
		return sorted[len(sorted)-1]
	}
	idx := int(math.Ceil(q*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// Snapshot returns a frozen summary suitable for storage.
func (h *Histogram) Snapshot() HistogramSnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.samples) == 0 {
		return HistogramSnapshot{}
	}
	sorted := make([]float64, len(h.samples))
	copy(sorted, h.samples)
	sort.Float64s(sorted)
	avg := h.sum / float64(len(sorted))
	return HistogramSnapshot{
		Count: h.count,
		Min:   h.min,
		Max:   h.max,
		Avg:   avg,
		P50:   quantile(sorted, 0.50),
		P90:   quantile(sorted, 0.90),
		P95:   quantile(sorted, 0.95),
		P99:   quantile(sorted, 0.99),
		P999:  quantile(sorted, 0.999),
	}
}

func quantile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(q*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// HistogramSnapshot is the frozen summary form.
type HistogramSnapshot struct {
	Count int64   `json:"count"`
	Min   float64 `json:"min"`
	Max   float64 `json:"max"`
	Avg   float64 `json:"avg"`
	P50   float64 `json:"p50"`
	P90   float64 `json:"p90"`
	P95   float64 `json:"p95"`
	P99   float64 `json:"p99"`
	P999  float64 `json:"p999"`
}
