// SPDX-License-Identifier: MIT

package coordination_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/coordination"
)

// fakeClock is a coordination.ClockReader returning a fixed time (or a
// fixed error) so a test can inject a known Postgres-Redis offset.
type fakeClock struct {
	t   time.Time
	err error
}

func (f fakeClock) ServerTime(context.Context) (time.Time, error) {
	if f.err != nil {
		return time.Time{}, f.err
	}
	return f.t, nil
}

// recordingSetter captures the last SetClockSkew call so a test can
// assert the published gauge value and label.
type recordingSetter struct {
	pair    string
	seconds float64
	calls   int
}

func (r *recordingSetter) SetClockSkew(pair string, seconds float64) {
	r.pair = pair
	r.seconds = seconds
	r.calls++
}

// TestClockSkewSamplerPublishesInjectedOffset_spec_25_4 injects a known
// 12s Postgres-Redis offset and asserts the sampler publishes that skew
// on the postgres-redis pair rather than 0. This is the F-SH-1
// regression: pre-fix nothing called SetClockSkew so the gauge was
// permanently 0 and the OpsClockSkewExceeded alert could never fire. The
// sample value is direction-agnostic (absolute), so a Redis-ahead and a
// Postgres-ahead offset of the same magnitude both publish the same skew.
//
// spec: 25.4 (Postgres-Redis skew monitoring and >10s alert, line 2280)
func TestClockSkewSamplerPublishesInjectedOffset_spec_25_4(t *testing.T) {
	base := time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		pgTime   time.Time
		redis    time.Time
		wantSkew float64
	}{
		{"redis ahead 12s", base, base.Add(12 * time.Second), 12},
		{"postgres ahead 12s", base.Add(12 * time.Second), base, 12},
		{"within tolerance", base, base.Add(3 * time.Second), 3},
		{"zero skew", base, base, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setter := &recordingSetter{}
			s := coordination.NewClockSkewSampler(fakeClock{t: tc.pgTime}, fakeClock{t: tc.redis}, setter)
			got, err := s.Sample(context.Background())
			if err != nil {
				t.Fatalf("Sample: unexpected error: %v", err)
			}
			if setter.calls != 1 {
				t.Fatalf("SetClockSkew called %d times, want 1", setter.calls)
			}
			if setter.pair != coordination.ClockSkewPair {
				t.Errorf("pair = %q, want %q", setter.pair, coordination.ClockSkewPair)
			}
			if setter.seconds != tc.wantSkew {
				t.Errorf("published skew = %v, want %v", setter.seconds, tc.wantSkew)
			}
			if got != tc.wantSkew {
				t.Errorf("returned skew = %v, want %v", got, tc.wantSkew)
			}
		})
	}
}

// TestClockSkewSamplerReadErrorLeavesGaugeUnchanged_spec_25_4 asserts a
// dependency-clock read failure returns an error wrapped with the failing
// tier and does not publish a skew, so a Postgres or Redis outage leaves
// the last good gauge in place rather than reporting a spurious skew.
//
// spec: 25.4 (Postgres-Redis skew monitoring, line 2280)
func TestClockSkewSamplerReadErrorLeavesGaugeUnchanged_spec_25_4(t *testing.T) {
	readErr := errors.New("connection refused")
	t.Run("postgres read fails", func(t *testing.T) {
		setter := &recordingSetter{}
		s := coordination.NewClockSkewSampler(fakeClock{err: readErr}, fakeClock{t: time.Now()}, setter)
		if _, err := s.Sample(context.Background()); err == nil {
			t.Fatal("Sample: want error on postgres read failure, got nil")
		}
		if setter.calls != 0 {
			t.Errorf("SetClockSkew called %d times on read failure, want 0", setter.calls)
		}
	})
	t.Run("redis read fails", func(t *testing.T) {
		setter := &recordingSetter{}
		s := coordination.NewClockSkewSampler(fakeClock{t: time.Now()}, fakeClock{err: readErr}, setter)
		if _, err := s.Sample(context.Background()); err == nil {
			t.Fatal("Sample: want error on redis read failure, got nil")
		}
		if setter.calls != 0 {
			t.Errorf("SetClockSkew called %d times on read failure, want 0", setter.calls)
		}
	})
}

// TestClockSkewSamplerNoOpWhenDependencyAbsent_spec_25_4 asserts the
// sampler is a no-op (returns 0, no error, no publish) when a reader or
// the metrics setter is unwired, so a single-process degraded deployment
// without Postgres or Redis skips the monitor rather than panicking or
// publishing a meaningless gauge.
//
// spec: 25.4 (Postgres-Redis skew monitoring, line 2280)
func TestClockSkewSamplerNoOpWhenDependencyAbsent_spec_25_4(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name   string
		pg     coordination.ClockReader
		redis  coordination.ClockReader
		setter coordination.SkewSetter
	}{
		{"nil postgres", nil, fakeClock{t: now}, &recordingSetter{}},
		{"nil redis", fakeClock{t: now}, nil, &recordingSetter{}},
		{"nil setter", fakeClock{t: now}, fakeClock{t: now}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := coordination.NewClockSkewSampler(tc.pg, tc.redis, tc.setter)
			got, err := s.Sample(context.Background())
			if err != nil {
				t.Fatalf("Sample: unexpected error: %v", err)
			}
			if got != 0 {
				t.Errorf("returned skew = %v, want 0 for an unwired dependency", got)
			}
		})
	}
}
