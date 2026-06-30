// SPDX-License-Identifier: MIT

package eventbus

import (
	"context"
	"fmt"
	"time"
)

// RetranscribeRow is one audit row the §12.3.7 retranscribe worker
// re-publishes. The worker re-serializes a byte-identical CloudEvents
// envelope from the canonical Postgres tuple — same id, source, time,
// and OCSF data payload — so downstream de-duplication by CloudEvents
// id continues to work.
type RetranscribeRow struct {
	// TenantID scopes the row.
	TenantID string

	// Seq is the per-tenant sequence number.
	Seq uint64

	// Topic is the source EventTopic.
	Topic EventTopic

	// Event is the byte-identical CloudEvents envelope rebuilt from the
	// canonical tuple.
	Event Event

	// RetryCount is the row's current retry_count.
	RetryCount int
}

// RetranscribeStore is the §12.3.7 surface the retranscribe worker
// sweeps. It reads rows in (failed | retry_pending) state whose
// retry_count is below the limit and writes back their publish state.
type RetranscribeStore interface {
	// PendingRepublish returns up to limit rows in
	// (failed | retry_pending) state with retry_count < maxRetryAttempts,
	// ordered by created_at ASC (FIFO).
	PendingRepublish(ctx context.Context, maxRetryAttempts, limit int) ([]RetranscribeRow, error)

	// SetPublishState transitions a row's eventbus_publish_state and
	// sets retry_count.
	SetPublishState(ctx context.Context, tenantID string, seq uint64,
		state PublishState, retryCount int) error
}

// RetranscribePublisher is the narrow publish surface the worker drives —
// the §12.6 RedisEventBus satisfies it (a subset of the EventBus
// interface). It is kept separate from EventBus so the worker depends only
// on the method it uses.
type RetranscribePublisher interface {
	Publish(ctx context.Context, tenantID TenantID, topic EventTopic, event Event) error
}

// RetranscribeMetrics is the §12.3.7 retranscribe-worker metric
// surface. A nil value is a valid no-op.
type RetranscribeMetrics interface {
	// Attempt counts a retranscribe attempt, labeled by outcome
	// ("success" | "failure") and, for a failure, the error_type.
	Attempt(outcome string, errType PublishErrorType)

	// FinalFailure counts a row that exhausted maxRetryAttempts.
	// Drives the §16.5 EventBusPublishFinalFailure alert.
	FinalFailure(topic EventTopic)
}

// RetranscribeConfig pins the §12.3.7 retranscribe-worker parameters.
type RetranscribeConfig struct {
	// RetryInterval is eventBus.retryInterval (default 60s): the sweep
	// cadence.
	RetryInterval time.Duration

	// MaxRetryAttempts is eventBus.maxRetryAttempts: a row whose
	// retry_count reaches this stops being swept and needs a manual
	// republish.
	MaxRetryAttempts int

	// BatchSize bounds how many rows one sweep processes per tenant
	// shard.
	BatchSize int
}

// DefaultRetranscribeConfig returns the §12.3.7 defaults.
func DefaultRetranscribeConfig() RetranscribeConfig {
	return RetranscribeConfig{
		RetryInterval:    60 * time.Second,
		MaxRetryAttempts: 5,
		BatchSize:        256,
	}
}

// Retranscriber is the §12.3.7 EventBus retranscribe worker. It is the
// correctness layer that guarantees every `failed` audit row is
// eventually re-published even when the in-memory replay buffer is
// lost. Production runs it leader-elected (lock key
// lenny.gateway.eventbus.retranscribe.leader); the sweep itself is
// leader-agnostic.
type Retranscriber struct {
	store     RetranscribeStore
	publisher RetranscribePublisher
	cfg       RetranscribeConfig
	metrics   RetranscribeMetrics
}

// NewRetranscriber returns a Retranscriber. cfg's zero fields are
// filled from DefaultRetranscribeConfig; metrics may be nil.
func NewRetranscriber(store RetranscribeStore, publisher RetranscribePublisher,
	cfg RetranscribeConfig, metrics RetranscribeMetrics,
) *Retranscriber {
	def := DefaultRetranscribeConfig()
	if cfg.RetryInterval <= 0 {
		cfg.RetryInterval = def.RetryInterval
	}
	if cfg.MaxRetryAttempts <= 0 {
		cfg.MaxRetryAttempts = def.MaxRetryAttempts
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = def.BatchSize
	}
	return &Retranscriber{store: store, publisher: publisher, cfg: cfg, metrics: metrics}
}

// SweepResult reports what one Sweep pass did.
type SweepResult struct {
	// Republished is the count of rows that published successfully.
	Republished int

	// RetryScheduled is the count of rows that failed and were written
	// back as retry_pending.
	RetryScheduled int

	// TerminalFailures is the count of rows that exhausted
	// MaxRetryAttempts this sweep.
	TerminalFailures int
}

// Sweep runs one §12.3.7 retranscribe pass. For each eligible row it
// re-invokes Publish with the byte-identical envelope and applies the
// state transition:
//
//   - Success → eventbus_publish_state = published; retry_count is
//     retained for forensics.
//   - Failure → eventbus_publish_state = retry_pending;
//     retry_count = retry_count + 1.
//   - Terminal failure (retry_count would reach MaxRetryAttempts) →
//     eventbus_publish_state = failed; retry_count = MaxRetryAttempts —
//     the row stops being swept and needs a manual republish.
func (r *Retranscriber) Sweep(ctx context.Context) (SweepResult, error) {
	rows, err := r.store.PendingRepublish(ctx, r.cfg.MaxRetryAttempts, r.cfg.BatchSize)
	if err != nil {
		return SweepResult{}, fmt.Errorf("eventbus: read pending republish: %w", err)
	}
	var res SweepResult
	for _, row := range rows {
		perr := r.publisher.Publish(ctx, TenantID(row.TenantID), row.Topic, row.Event)
		if perr == nil {
			if e := r.store.SetPublishState(ctx, row.TenantID, row.Seq,
				PublishPublished, row.RetryCount); e != nil {
				return res, fmt.Errorf("eventbus: mark published: %w", e)
			}
			if r.metrics != nil {
				r.metrics.Attempt("success", "")
			}
			res.Republished++
			continue
		}
		errType := classifyError(perr)
		attempts := row.RetryCount + 1
		if attempts >= r.cfg.MaxRetryAttempts {
			// Terminal: the row stops being eligible for future sweeps.
			if e := r.store.SetPublishState(ctx, row.TenantID, row.Seq,
				PublishFailed, r.cfg.MaxRetryAttempts); e != nil {
				return res, fmt.Errorf("eventbus: mark terminal failed: %w", e)
			}
			if r.metrics != nil {
				r.metrics.Attempt("failure", errType)
				r.metrics.FinalFailure(row.Topic)
			}
			res.TerminalFailures++
			continue
		}
		if e := r.store.SetPublishState(ctx, row.TenantID, row.Seq,
			PublishRetryPending, attempts); e != nil {
			return res, fmt.Errorf("eventbus: mark retry_pending: %w", e)
		}
		if r.metrics != nil {
			r.metrics.Attempt("failure", errType)
		}
		res.RetryScheduled++
	}
	return res, nil
}

// Run is the §12.3.7 retranscribe sweep loop. It drives Sweep every
// RetryInterval until ctx is cancelled. Callers run it in a goroutine,
// guarded by leader election in production.
func (r *Retranscriber) Run(ctx context.Context) {
	ticker := time.NewTicker(r.cfg.RetryInterval)
	defer ticker.Stop()
	_, _ = r.Sweep(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_, _ = r.Sweep(ctx)
		}
	}
}

// classifyError maps a publish error to its §12.3.7 error_type. A
// *PublishError carries the type directly; any other error is treated
// as backend_unavailable.
func classifyError(err error) PublishErrorType {
	var pe *PublishError
	if e, ok := err.(*PublishError); ok {
		pe = e
	}
	if pe != nil {
		return pe.Type
	}
	return ErrBackendUnavailable
}
