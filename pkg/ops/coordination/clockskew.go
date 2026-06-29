// SPDX-License-Identifier: MIT

package coordination

import (
	"context"
	"fmt"
	"math"
	"time"
)

// ClockReader reads a dependency's server clock. The two production
// implementations are the Postgres pool (SELECT now()) and the Redis
// store (the TIME command), both authored as the single per-tier clock
// the §25.4 lease-expiry computation assumes.
//
// spec: §25.4 line 2280 (Postgres-Redis skew monitoring).
type ClockReader interface {
	ServerTime(ctx context.Context) (time.Time, error)
}

// SkewSetter receives the measured Postgres-Redis skew. The production
// implementation is the lenny-ops lock-metrics adapter's SetClockSkew,
// which publishes lenny_ops_clock_skew_seconds{pair}. Defined at the
// consumer so a unit test can substitute a recorder.
//
// spec: §25.4 line 2336 (lenny_ops_clock_skew_seconds gauge).
type SkewSetter interface {
	SetClockSkew(pair string, seconds float64)
}

// ClockSkewSampler reads the Postgres and Redis dependency clocks, takes
// the absolute difference, and publishes it on the
// lenny_ops_clock_skew_seconds{pair="postgres-redis"} gauge. §25.4
// bounds Postgres-Redis skew by NTP and alerts (OpsClockSkewExceeded)
// when it exceeds 10s; this sampler is the producer that gauge needs so
// the alert is not permanently 0.
//
// The two reads are sequential and the difference includes the read
// round-trip latency, so a sub-second measurement floor is expected; the
// 10s alert threshold sits well above that floor. The sampler is not the
// authoritative skew oracle, it is a monitoring estimate of the same
// clock divergence the lease-expiry path is exposed to.
//
// spec: §25.4 line 2280 (Postgres-Redis skew monitoring and >10s alert).
type ClockSkewSampler struct {
	postgres ClockReader
	redis    ClockReader
	metrics  SkewSetter
}

// ClockSkewPair is the gauge pair label the OpsClockSkewExceeded alert
// expression selects on (lenny_ops_clock_skew_seconds{pair="postgres-redis"}).
const ClockSkewPair = "postgres-redis"

// NewClockSkewSampler returns a sampler over the two dependency clocks
// publishing onto metrics. postgres and redis are the ClockReaders for
// the two tiers; metrics receives the absolute skew. A nil reader or a
// nil metrics setter makes Sample a no-op error so a single-process
// degraded deployment (no Postgres or no Redis) does not panic or
// publish a meaningless gauge.
func NewClockSkewSampler(postgres, redis ClockReader, metrics SkewSetter) *ClockSkewSampler {
	return &ClockSkewSampler{postgres: postgres, redis: redis, metrics: metrics}
}

// Sample reads both dependency clocks, computes the absolute skew in
// seconds, and publishes it on the gauge. It returns the measured skew
// and an error. When either reader or the metrics setter is unwired, it
// returns 0 with no error and publishes nothing, so a deployment missing
// one tier skips the monitor rather than reporting a spurious skew. A
// read failure returns the error wrapped with the failing tier and does
// not update the gauge, leaving the last good sample in place.
//
// spec: §25.4 line 2280 (server computes expiresAt from a single clock;
// monitor Postgres-Redis skew). The skew is abs(redis - postgres) so the
// gauge is direction-agnostic, matching the alert expression's `> 10`.
func (s *ClockSkewSampler) Sample(ctx context.Context) (float64, error) {
	if s == nil || s.postgres == nil || s.redis == nil || s.metrics == nil {
		return 0, nil
	}
	pgTime, err := s.postgres.ServerTime(ctx)
	if err != nil {
		return 0, fmt.Errorf("read postgres clock: %w", err)
	}
	redisTime, err := s.redis.ServerTime(ctx)
	if err != nil {
		return 0, fmt.Errorf("read redis clock: %w", err)
	}
	skew := math.Abs(redisTime.Sub(pgTime).Seconds())
	s.metrics.SetClockSkew(ClockSkewPair, skew)
	return skew, nil
}
