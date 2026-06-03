// SPDX-License-Identifier: MIT

package operations

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Baseline is one row of the §25.2 ops_operation_baselines table: the
// p50/p90 completion durations for an operation kind on this deployment
// and the number of completed operations that fed them.
//
// spec: §25.2 line 393 (ops_operation_baselines (kind, p50_duration_ms,
// p90_duration_ms, sample_size, last_updated)).
type Baseline struct {
	P50         time.Duration
	P90         time.Duration
	SampleSize  int
	LastUpdated time.Time
}

// BaselineStore persists and serves the §25.2 historical baselines. A
// completion is recorded on every operation completion (line 393); a
// lookup feeds the historical_p50 ETA method (line 394).
type BaselineStore interface {
	// RecordCompletion folds one completed operation's wall-clock
	// duration into the kind's baseline and updates last_updated.
	RecordCompletion(ctx context.Context, kind Kind, dur time.Duration) error
	// Lookup returns the kind's current baseline. The bool is false when
	// no completion of the kind has been recorded.
	Lookup(ctx context.Context, kind Kind) (Baseline, bool, error)
}

// baselineSampleCap bounds the per-kind in-memory sample window the
// percentile computation reads. The persisted sample_size still reflects
// every completion; only the percentile window is bounded so the store
// does not grow without limit on a long-lived process.
const baselineSampleCap = 512

// MemoryBaselineStore is the in-process BaselineStore. It computes exact
// nearest-rank percentiles over a bounded recent-sample window per kind.
// It is the test and single-process implementation; the Postgres-backed
// store mirrors its aggregate to the §25.2 table for durability.
type MemoryBaselineStore struct {
	mu  sync.Mutex
	now func() time.Time
	by  map[Kind]*kindSamples
}

type kindSamples struct {
	samplesMS   []float64
	total       int
	lastUpdated time.Time
}

// NewMemoryBaselineStore returns an empty in-memory baseline store.
func NewMemoryBaselineStore() *MemoryBaselineStore {
	return &MemoryBaselineStore{now: time.Now, by: map[Kind]*kindSamples{}}
}

// RecordCompletion folds dur into kind's sample window.
func (m *MemoryBaselineStore) RecordCompletion(_ context.Context, kind Kind, dur time.Duration) error {
	if dur < 0 {
		dur = 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ks := m.by[kind]
	if ks == nil {
		ks = &kindSamples{}
		m.by[kind] = ks
	}
	ks.samplesMS = append(ks.samplesMS, float64(dur.Milliseconds()))
	if len(ks.samplesMS) > baselineSampleCap {
		ks.samplesMS = ks.samplesMS[len(ks.samplesMS)-baselineSampleCap:]
	}
	ks.total++
	ks.lastUpdated = m.now().UTC()
	return nil
}

// Lookup returns kind's current baseline.
func (m *MemoryBaselineStore) Lookup(_ context.Context, kind Kind) (Baseline, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ks := m.by[kind]
	if ks == nil || ks.total == 0 {
		return Baseline{}, false, nil
	}
	p50, p90 := percentiles(ks.samplesMS)
	return Baseline{
		P50:         p50,
		P90:         p90,
		SampleSize:  ks.total,
		LastUpdated: ks.lastUpdated,
	}, true, nil
}

// percentiles computes the nearest-rank p50 and p90 over the duration
// samples (in milliseconds). It returns zero durations for an empty
// input.
func percentiles(samplesMS []float64) (p50, p90 time.Duration) {
	n := len(samplesMS)
	if n == 0 {
		return 0, 0
	}
	sorted := append([]float64(nil), samplesMS...)
	sort.Float64s(sorted)
	return nearestRank(sorted, 0.5), nearestRank(sorted, 0.9)
}

// nearestRank returns the §25.2 nearest-rank percentile q (0..1) of a
// sorted millisecond slice as a Duration. rank = ceil(q*n), clamped to
// [1, n].
func nearestRank(sorted []float64, q float64) time.Duration {
	n := len(sorted)
	rank := int(q*float64(n) + 0.999999)
	if rank < 1 {
		rank = 1
	}
	if rank > n {
		rank = n
	}
	return time.Duration(sorted[rank-1]) * time.Millisecond
}
