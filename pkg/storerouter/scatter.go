// SPDX-License-Identifier: MIT

package storerouter

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// QueryType labels a scatter-gather invocation for the §12.6 line 560
// metrics. The set is closed; a caller passes the value that matches the
// fan-out it is running.
//
// spec: §12.6 line 560 — query_type is one of list_sessions,
// gdpr_erasure, tenant_deletion, delegation_budget_purge.
type QueryType string

const (
	QueryListSessions          QueryType = "list_sessions"
	QueryGDPRErasure           QueryType = "gdpr_erasure"
	QueryTenantDeletion        QueryType = "tenant_deletion"
	QueryDelegationBudgetPurge QueryType = "delegation_budget_purge"
	// QueryAuditEvents labels the §25.9 platform-admin cross-tenant audit
	// query fan-out across AllAuditShards.
	QueryAuditEvents QueryType = "audit_events"
)

// ScatterConfig pins the §12.6 lines 556-558 scatter-gather execution
// bounds. In v1 AllSessionShards / AllAuditShards return a single shard so
// the bounds are trivially satisfied; they become load-bearing the first
// time a multi-shard StoreRouter is deployed.
type ScatterConfig struct {
	// MaxConcurrency caps how many shards are queried in parallel
	// (storeRouter.maxScatterGatherConcurrency, default 16). Remaining
	// shards run in subsequent batches.
	MaxConcurrency int
	// PerShardTimeout bounds each shard query
	// (storeRouter.scatterGatherPerShardTimeoutSeconds, default 10s). A
	// shard that exceeds it is a partial-result miss for reads and a retry
	// candidate for writes.
	PerShardTimeout time.Duration
	// AggregateTimeout bounds the whole fan-out
	// (storeRouter.scatterGatherAggregateTimeoutSeconds, default 120s).
	AggregateTimeout time.Duration
}

// scatter-gather defaults per §12.6 lines 556-558.
const (
	defaultScatterMaxConcurrency   = 16
	defaultScatterPerShardTimeout  = 10 * time.Second
	defaultScatterAggregateTimeout = 120 * time.Second
)

// DefaultScatterConfig returns the §12.6 documented defaults.
func DefaultScatterConfig() ScatterConfig {
	return ScatterConfig{
		MaxConcurrency:   defaultScatterMaxConcurrency,
		PerShardTimeout:  defaultScatterPerShardTimeout,
		AggregateTimeout: defaultScatterAggregateTimeout,
	}
}

// withDefaults fills any zero/negative field from DefaultScatterConfig so
// a caller can pass a partially-populated config (or the zero value) and
// still get spec-compliant bounds.
func (c ScatterConfig) withDefaults() ScatterConfig {
	def := DefaultScatterConfig()
	if c.MaxConcurrency <= 0 {
		c.MaxConcurrency = def.MaxConcurrency
	}
	if c.PerShardTimeout <= 0 {
		c.PerShardTimeout = def.PerShardTimeout
	}
	if c.AggregateTimeout <= 0 {
		c.AggregateTimeout = def.AggregateTimeout
	}
	return c
}

// ScatterMetrics receives the two §12.6 line 560 scatter-gather metrics.
// A nil ScatterMetrics disables emission. PromScatterMetrics is the
// production implementation; tests may pass a fake.
type ScatterMetrics interface {
	// ObserveScatterGather records one scatter-gather invocation: its
	// wall-clock duration (lenny_store_router_scatter_gather_duration_seconds)
	// and the number of shards it fanned out across
	// (lenny_store_router_scatter_gather_shard_count), both labeled by
	// query type.
	ObserveScatterGather(queryType QueryType, shardCount int, seconds float64)
}

// PromScatterMetrics is the Prometheus-backed ScatterMetrics. Construct it
// with NewScatterMetrics and register it with the gateway's registerer so
// the §16 ScatterGatherSlowQuery alert has a series behind it.
type PromScatterMetrics struct {
	duration   *prometheus.HistogramVec
	shardCount *prometheus.GaugeVec
}

// NewScatterMetrics builds and registers the §12.6 line 560 scatter-gather
// collectors against reg. A nil reg uses the default registerer.
func NewScatterMetrics(reg prometheus.Registerer) (*PromScatterMetrics, error) {
	dur, err := metrics.NewHistogram(prometheus.HistogramOpts{
		Name: "lenny_store_router_scatter_gather_duration_seconds",
		Help: "Scatter-gather operation duration by query type (§12.6 line 560).",
	}, []string{"query_type"})
	if err != nil {
		return nil, err
	}
	sc, err := metrics.NewGauge(prometheus.GaugeOpts{
		Name: "lenny_store_router_scatter_gather_shard_count",
		Help: "Shards queried per scatter-gather invocation (§12.6 line 560).",
	}, []string{"query_type"})
	if err != nil {
		return nil, err
	}
	metrics.MustRegister(reg, dur)
	metrics.MustRegister(reg, sc)
	return &PromScatterMetrics{duration: dur, shardCount: sc}, nil
}

// ObserveScatterGather records the duration histogram sample and sets the
// shard-count gauge for queryType.
func (m *PromScatterMetrics) ObserveScatterGather(queryType QueryType, shardCount int, seconds float64) {
	if m == nil {
		return
	}
	m.duration.WithLabelValues(string(queryType)).Observe(seconds)
	m.shardCount.WithLabelValues(string(queryType)).Set(float64(shardCount))
}

// shardResult carries one shard's outcome out of the worker goroutine.
type shardResult[T any] struct {
	index   int
	value   T
	timeout bool
	err     error
}

// isTimeout reports whether err is a deadline/cancellation that the
// scatter bounds produced (a slow shard), as opposed to a real query
// error. The per-shard and aggregate contexts both surface as
// context.DeadlineExceeded; a cancelled parent surfaces as
// context.Canceled.
func isTimeout(err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

// ScatterRead runs fn against every shard under the §12.6 lines 556-558
// bounds and aggregates the per-shard results. It is the read-path helper
// (GET /v1/sessions and other fan-out queries): a shard that exceeds
// PerShardTimeout is logged as a scatter_gather_shard_timeout structured
// event and dropped, and the call returns the shards that did complete
// with partial=true. A shard that fails for a non-timeout reason fails the
// whole read (a query error is not a slow shard). The two §12.6 line 560
// metrics are emitted once per invocation via m.
//
// spec: §12.6 lines 554-560.
func ScatterRead[T any](
	ctx context.Context,
	cfg ScatterConfig,
	m ScatterMetrics,
	logger *slog.Logger,
	queryType QueryType,
	shards []ShardHandle,
	fn func(context.Context, ShardHandle) (T, error),
) (results []T, partial bool, err error) {
	cfg = cfg.withDefaults()
	if logger == nil {
		logger = slog.Default()
	}
	start := time.Now()
	defer func() {
		if m != nil {
			m.ObserveScatterGather(queryType, len(shards), time.Since(start).Seconds())
		}
	}()

	aggCtx, cancel := context.WithTimeout(ctx, cfg.AggregateTimeout)
	defer cancel()

	out := make([]shardResult[T], len(shards))
	sem := make(chan struct{}, cfg.MaxConcurrency)
	var wg sync.WaitGroup
	for i := range shards {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, sh ShardHandle) {
			defer wg.Done()
			defer func() { <-sem }()
			shardCtx, c := context.WithTimeout(aggCtx, cfg.PerShardTimeout)
			defer c()
			v, ferr := fn(shardCtx, sh)
			res := shardResult[T]{index: idx, value: v, err: ferr}
			if ferr != nil && isTimeout(ferr) || shardCtx.Err() != nil {
				res.timeout = true
			}
			out[idx] = res
		}(i, shards[i])
	}
	wg.Wait()

	results = make([]T, 0, len(shards))
	for _, r := range out {
		switch {
		case r.timeout:
			partial = true
			logger.Warn("scatter_gather_shard_timeout",
				slog.String("shard_id", string(shards[r.index].ID)),
				slog.String("query_type", string(queryType)),
				slog.Duration("per_shard_timeout", cfg.PerShardTimeout))
		case r.err != nil:
			return nil, false, r.err
		default:
			results = append(results, r.value)
		}
	}
	return results, partial, nil
}

// ScatterWrite runs fn against every shard under the §12.6 lines 556-558
// bounds. It is the write-path helper (GDPR erasure, tenant deletion): a
// shard that exceeds PerShardTimeout is retried up to scatterWriteRetries
// times before the whole operation fails, because a write cannot return a
// partial result. A non-timeout error fails the operation immediately.
//
// spec: §12.6 line 557 — "retries the timed-out shard up to 2 times for
// write paths (GDPR erasure, tenant deletion) before failing the
// operation."
func ScatterWrite(
	ctx context.Context,
	cfg ScatterConfig,
	m ScatterMetrics,
	logger *slog.Logger,
	queryType QueryType,
	shards []ShardHandle,
	fn func(context.Context, ShardHandle) error,
) error {
	cfg = cfg.withDefaults()
	if logger == nil {
		logger = slog.Default()
	}
	start := time.Now()
	defer func() {
		if m != nil {
			m.ObserveScatterGather(queryType, len(shards), time.Since(start).Seconds())
		}
	}()

	aggCtx, cancel := context.WithTimeout(ctx, cfg.AggregateTimeout)
	defer cancel()

	errs := make([]error, len(shards))
	sem := make(chan struct{}, cfg.MaxConcurrency)
	var wg sync.WaitGroup
	for i := range shards {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, sh ShardHandle) {
			defer wg.Done()
			defer func() { <-sem }()
			errs[idx] = scatterWriteShard(aggCtx, cfg, logger, queryType, sh, fn)
		}(i, shards[i])
	}
	wg.Wait()
	return errors.Join(errs...)
}

// scatterWriteRetries is the §12.6 line 557 write-path retry budget: a
// timed-out shard is retried up to twice (three attempts total) before the
// operation fails.
const scatterWriteRetries = 2

// scatterWriteShard runs fn against one shard, retrying a per-shard
// timeout up to scatterWriteRetries times. A non-timeout error returns
// immediately.
func scatterWriteShard(
	ctx context.Context,
	cfg ScatterConfig,
	logger *slog.Logger,
	queryType QueryType,
	sh ShardHandle,
	fn func(context.Context, ShardHandle) error,
) error {
	var lastErr error
	for attempt := 0; attempt <= scatterWriteRetries; attempt++ {
		shardCtx, c := context.WithTimeout(ctx, cfg.PerShardTimeout)
		err := fn(shardCtx, sh)
		c()
		if err == nil {
			return nil
		}
		if !isTimeout(err) {
			return err
		}
		lastErr = err
		logger.Warn("scatter_gather_shard_timeout",
			slog.String("shard_id", string(sh.ID)),
			slog.String("query_type", string(queryType)),
			slog.Int("attempt", attempt+1),
			slog.Duration("per_shard_timeout", cfg.PerShardTimeout))
		if ctx.Err() != nil {
			break
		}
	}
	return lastErr
}
