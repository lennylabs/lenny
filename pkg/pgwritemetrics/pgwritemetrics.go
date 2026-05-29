// SPDX-License-Identifier: MIT

// Package pgwritemetrics implements the §12.3 lines 115-125 sustained
// Postgres write-IOPS sampler. Each gateway replica periodically reads a
// monotonic cumulative write counter (production wires the
// pg_stat_database row-write total), differentiates successive reads
// into a per-second rate, and publishes it through the
// `lenny_postgres_write_iops` gauge. The §16.5 PostgresWriteSaturation
// alert evaluates `lenny_postgres_write_iops /
// scalar(lenny_postgres_write_ceiling_iops) > 0.80`, so without this
// sampler the alert numerator is always absent and the alert can never
// fire.
//
// The package is decoupled from Postgres: tests inject a deterministic
// CounterFunc and Clock; production wires a pg_stat_database query. This
// keeps the rate arithmetic — including counter-reset and zero-interval
// handling — unit-testable without a live database.
//
// spec: §12.3 lines 115-125, §16.5 PostgresWriteSaturation. F-12.3.7.
package pgwritemetrics

import (
	"context"
	"sync"
	"time"
)

// MetricEmitter publishes the write-IOPS gauge to /metrics. The gateway
// implementation in pkg/gateway/gatewaymetrics satisfies it.
type MetricEmitter interface {
	// SetPostgresWriteIops updates the lenny_postgres_write_iops gauge
	// with the latest sampled sustained write rate (operations/second).
	SetPostgresWriteIops(iops float64)
}

// CounterFunc returns the current cumulative Postgres write count, a
// value that increases monotonically over the life of the server (it
// resets only on pg_stat_reset() or a server restart). The Sampler
// differentiates successive reads to derive the write rate.
type CounterFunc func(ctx context.Context) (uint64, error)

// Clock returns the current time. Production passes time.Now; tests
// inject a deterministic clock.
type Clock func() time.Time

// Sampler differentiates a monotonic write counter into a per-second
// rate and publishes it on a fixed cadence.
type Sampler struct {
	read    CounterFunc
	emitter MetricEmitter
	now     Clock

	mu       sync.Mutex
	have     bool
	last     uint64
	lastTime time.Time
}

// New constructs a Sampler. read is required; emitter may be nil (the
// rate is computed but not published); now defaults to time.Now when
// nil.
func New(read CounterFunc, emitter MetricEmitter, now Clock) *Sampler {
	if now == nil {
		now = time.Now
	}
	return &Sampler{read: read, emitter: emitter, now: now}
}

// Sample reads the cumulative counter, computes the per-second write
// rate since the previous sample, publishes it on the gauge, and
// records the new baseline. It returns the computed IOPS and whether a
// value was published.
//
// The first sample establishes the baseline only — there is no prior
// point to differentiate, so nothing is published (ok=false). A counter
// that decreased since the last read (a pg_stat_reset() or server
// restart) re-baselines without publishing a spurious negative rate. A
// non-positive elapsed interval is skipped to avoid a divide-by-zero.
func (s *Sampler) Sample(ctx context.Context) (float64, bool, error) {
	if s == nil || s.read == nil {
		return 0, false, nil
	}
	cur, err := s.read(ctx)
	if err != nil {
		return 0, false, err
	}
	now := s.now()

	s.mu.Lock()
	defer s.mu.Unlock()
	prevHave, prevCount, prevTime := s.have, s.last, s.lastTime
	s.have, s.last, s.lastTime = true, cur, now

	if !prevHave {
		return 0, false, nil
	}
	if cur < prevCount {
		// Counter reset (pg_stat_reset / server restart): re-baseline.
		return 0, false, nil
	}
	dt := now.Sub(prevTime).Seconds()
	if dt <= 0 {
		return 0, false, nil
	}
	iops := float64(cur-prevCount) / dt
	if s.emitter != nil {
		s.emitter.SetPostgresWriteIops(iops)
	}
	return iops, true, nil
}

// Start runs Sample on the given interval until ctx is cancelled. The
// first tick only establishes the baseline; the gauge populates on the
// second tick onward.
func (s *Sampler) Start(ctx context.Context, interval time.Duration) {
	if s == nil || interval <= 0 {
		return
	}
	// Establish the baseline immediately so the first published rate
	// reflects one interval rather than the time since process start.
	_, _, _ = s.Sample(ctx)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_, _, _ = s.Sample(ctx)
		}
	}
}
