// SPDX-License-Identifier: MIT

package storerouter

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// spec: §12.6 lines 556-558 — DefaultScatterConfig returns the documented
// scatter-gather bounds (16 / 10s / 120s).
func TestDefaultScatterConfig_spec_12_6_556(t *testing.T) {
	got := DefaultScatterConfig()
	if got.MaxConcurrency != 16 {
		t.Errorf("MaxConcurrency = %d, want 16", got.MaxConcurrency)
	}
	if got.PerShardTimeout != 10*time.Second {
		t.Errorf("PerShardTimeout = %s, want 10s", got.PerShardTimeout)
	}
	if got.AggregateTimeout != 120*time.Second {
		t.Errorf("AggregateTimeout = %s, want 120s", got.AggregateTimeout)
	}
}

// withDefaults fills zero fields so the zero ScatterConfig is spec-compliant.
func TestScatterConfigWithDefaults(t *testing.T) {
	got := ScatterConfig{}.withDefaults()
	if got != DefaultScatterConfig() {
		t.Errorf("zero config withDefaults = %+v, want %+v", got, DefaultScatterConfig())
	}
	custom := ScatterConfig{MaxConcurrency: 4}.withDefaults()
	if custom.MaxConcurrency != 4 || custom.PerShardTimeout != 10*time.Second {
		t.Errorf("partial config not filled correctly: %+v", custom)
	}
}

func shards(ids ...string) []ShardHandle {
	out := make([]ShardHandle, len(ids))
	for i, id := range ids {
		out[i] = ShardHandle{ID: ShardID(id)}
	}
	return out
}

// fakeScatterMetrics records the single ObserveScatterGather call a
// scatter helper emits per invocation.
type fakeScatterMetrics struct {
	calls int
	qt    QueryType
	count int
	secs  float64
}

func (f *fakeScatterMetrics) ObserveScatterGather(qt QueryType, count int, secs float64) {
	f.calls++
	f.qt = qt
	f.count = count
	f.secs = secs
}

// spec: §12.6 lines 554-560 — all shards complete, results aggregate, the
// read is not partial, and the metric is emitted once with the shard count.
func TestScatterRead_AllShardsSucceed_spec_12_6_554(t *testing.T) {
	m := &fakeScatterMetrics{}
	got, partial, err := ScatterRead(context.Background(), DefaultScatterConfig(), m, nil,
		QueryListSessions, shards("a", "b", "c"),
		func(_ context.Context, sh ShardHandle) (string, error) { return string(sh.ID), nil })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if partial {
		t.Errorf("partial = true, want false when every shard succeeds")
	}
	if len(got) != 3 {
		t.Errorf("results = %v, want 3 entries", got)
	}
	if m.calls != 1 || m.count != 3 || m.qt != QueryListSessions {
		t.Errorf("metrics = %+v, want one call with count 3 and query type list_sessions", m)
	}
}

// spec: §12.6 line 557 — a shard that exceeds the per-shard timeout is
// dropped and the read returns the remaining shards with partial=true.
func TestScatterRead_PerShardTimeoutYieldsPartial_spec_12_6_557(t *testing.T) {
	got, partial, err := ScatterRead(context.Background(), DefaultScatterConfig(), nil, nil,
		QueryListSessions, shards("ok", "slow"),
		func(_ context.Context, sh ShardHandle) (string, error) {
			if sh.ID == "slow" {
				return "", context.DeadlineExceeded
			}
			return string(sh.ID), nil
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !partial {
		t.Errorf("partial = false, want true when a shard times out")
	}
	if len(got) != 1 || got[0] != "ok" {
		t.Errorf("results = %v, want only [ok]", got)
	}
}

// spec: §12.6 line 557 — a non-timeout shard error is a real failure, not a
// slow shard, so it fails the whole read rather than producing a partial.
func TestScatterRead_NonTimeoutErrorFails_spec_12_6_557(t *testing.T) {
	boom := errors.New("query error")
	_, _, err := ScatterRead(context.Background(), DefaultScatterConfig(), nil, nil,
		QueryListSessions, shards("a", "b"),
		func(_ context.Context, sh ShardHandle) (string, error) {
			if sh.ID == "b" {
				return "", boom
			}
			return string(sh.ID), nil
		})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the query error", err)
	}
}

// spec: §12.6 line 557 — write paths retry a timed-out shard up to twice
// (three attempts total) before failing the operation.
func TestScatterWrite_RetriesTimeoutThenFails_spec_12_6_557(t *testing.T) {
	var attempts int64
	err := ScatterWrite(context.Background(), DefaultScatterConfig(), nil, nil,
		QueryGDPRErasure, shards("only"),
		func(_ context.Context, _ ShardHandle) error {
			atomic.AddInt64(&attempts, 1)
			return context.DeadlineExceeded
		})
	if err == nil {
		t.Fatalf("err = nil, want failure after exhausting retries")
	}
	if got := atomic.LoadInt64(&attempts); got != 3 {
		t.Errorf("attempts = %d, want 3 (initial + 2 retries)", got)
	}
}

// spec: §12.6 line 557 — a transient timeout that clears within the retry
// budget completes the write.
func TestScatterWrite_SucceedsAfterRetry_spec_12_6_557(t *testing.T) {
	var attempts int64
	err := ScatterWrite(context.Background(), DefaultScatterConfig(), nil, nil,
		QueryTenantDeletion, shards("only"),
		func(_ context.Context, _ ShardHandle) error {
			if atomic.AddInt64(&attempts, 1) == 1 {
				return context.DeadlineExceeded
			}
			return nil
		})
	if err != nil {
		t.Fatalf("err = %v, want nil after a successful retry", err)
	}
	if got := atomic.LoadInt64(&attempts); got != 2 {
		t.Errorf("attempts = %d, want 2", got)
	}
}

// spec: §12.6 line 557 — a non-timeout write error is not retried; it fails
// the operation immediately.
func TestScatterWrite_NonTimeoutErrorNotRetried_spec_12_6_557(t *testing.T) {
	var attempts int64
	boom := errors.New("constraint violation")
	err := ScatterWrite(context.Background(), DefaultScatterConfig(), nil, nil,
		QueryTenantDeletion, shards("only"),
		func(_ context.Context, _ ShardHandle) error {
			atomic.AddInt64(&attempts, 1)
			return boom
		})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the constraint violation", err)
	}
	if got := atomic.LoadInt64(&attempts); got != 1 {
		t.Errorf("attempts = %d, want 1 (no retry on a non-timeout error)", got)
	}
}

// spec: §12.6 line 558 — the aggregate timeout bounds a slow fan-out; an
// in-flight shard past the deadline becomes a partial-result miss.
func TestScatterRead_AggregateTimeout_spec_12_6_558(t *testing.T) {
	cfg := ScatterConfig{MaxConcurrency: 4, PerShardTimeout: time.Second, AggregateTimeout: 5 * time.Millisecond}
	got, partial, err := ScatterRead(context.Background(), cfg, nil, nil,
		QueryListSessions, shards("slow"),
		func(ctx context.Context, _ ShardHandle) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !partial || len(got) != 0 {
		t.Errorf("partial=%v results=%v, want partial with no results under the aggregate deadline", partial, got)
	}
}

// spec: §12.6 line 560 — the production metrics implementation emits the
// duration histogram and shard-count gauge labeled by query type.
func TestPromScatterMetrics_Emits_spec_12_6_560(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := NewScatterMetrics(reg)
	if err != nil {
		t.Fatalf("NewScatterMetrics: %v", err)
	}
	m.ObserveScatterGather(QueryGDPRErasure, 7, 1.5)

	if got := testutil.ToFloat64(m.shardCount.WithLabelValues("gdpr_erasure")); got != 7 {
		t.Errorf("shard_count gauge = %v, want 7", got)
	}
	if got := testutil.CollectAndCount(m.duration); got == 0 {
		t.Errorf("duration histogram emitted no series")
	}
}

// The router carries the scatter config and lets the gateway attach the
// metrics sink after the registerer is built. F-12.6.18.
func TestSingleShardRouterScatterAccessors(t *testing.T) {
	r := &SingleShardRouter{scatterCfg: DefaultScatterConfig()}
	if r.ScatterConfig() != DefaultScatterConfig() {
		t.Errorf("ScatterConfig() = %+v", r.ScatterConfig())
	}
	if r.ScatterMetrics() != nil {
		t.Errorf("ScatterMetrics() should be nil before SetScatterMetrics")
	}
	m := &fakeScatterMetrics{}
	r.SetScatterMetrics(m)
	if r.ScatterMetrics() != m {
		t.Errorf("SetScatterMetrics did not attach the sink")
	}
}
