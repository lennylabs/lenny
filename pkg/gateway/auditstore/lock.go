// SPDX-License-Identifier: MIT

package auditstore

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/observability/metrics"
)

// LockConfig carries the §11.7 item 3 "Lock acquisition SLO and retry"
// tunables. The spec names the defaults (audit.lock.acquireTimeoutMs
// 5000, audit.lock.maxRetries 3, audit.lock.retryBaseMs 20); a zero or
// negative field falls back to the spec default via withDefaults so an
// operator overrides only the knobs it cares about.
// spec: §11.7 item 3 line 368.
type LockConfig struct {
	// AcquireTimeoutMs is set as statement_timeout on the
	// pg_advisory_xact_lock call (audit.lock.acquireTimeoutMs).
	AcquireTimeoutMs int
	// MaxRetries is the same-replica retry budget after a lock-
	// acquisition timeout (audit.lock.maxRetries).
	MaxRetries int
	// RetryBaseMs is the exponential-backoff base, doubling per attempt
	// and jittered ±20% (audit.lock.retryBaseMs).
	RetryBaseMs int
}

// DefaultLockConfig returns the §11.7 spec defaults.
func DefaultLockConfig() LockConfig {
	return LockConfig{AcquireTimeoutMs: 5000, MaxRetries: 3, RetryBaseMs: 20}
}

func (c LockConfig) withDefaults() LockConfig {
	d := DefaultLockConfig()
	if c.AcquireTimeoutMs <= 0 {
		c.AcquireTimeoutMs = d.AcquireTimeoutMs
	}
	if c.MaxRetries <= 0 {
		c.MaxRetries = d.MaxRetries
	}
	if c.RetryBaseMs <= 0 {
		c.RetryBaseMs = d.RetryBaseMs
	}
	return c
}

// LockMetrics holds the §11.7 lock-acquisition Prometheus collectors. A
// nil *LockMetrics is safe and discards every observation, so callers
// that do not wire metrics (the embedded AppendInTx default path) pass
// nil.
type LockMetrics struct {
	acquireSeconds      prometheus.Histogram
	concurrencyTimeouts prometheus.Counter
}

// NewLockMetrics registers the §11.7 lock-acquisition metric surface
// against reg: the lenny_audit_lock_acquire_seconds histogram (P99 SLO
// 50ms per item 3) and the lenny_audit_concurrency_timeout_total
// counter. Pass nil reg to register against the default registerer.
// spec: §11.7 item 3 line 368.
func NewLockMetrics(reg prometheus.Registerer) (*LockMetrics, error) {
	const acquireName = "lenny_audit_lock_acquire_seconds"
	const timeoutName = "lenny_audit_concurrency_timeout_total"
	if err := metrics.Validate(acquireName, nil); err != nil {
		return nil, err
	}
	if err := metrics.Validate(timeoutName, nil); err != nil {
		return nil, err
	}
	h := prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: acquireName,
		Help: "Per-tenant audit advisory-lock acquisition latency (§11.7 item 3; P99 SLO 50ms).",
		// Buckets straddle the 50ms P99 SLO and reach the 5s default
		// acquireTimeoutMs so a contended/timing-out tail is visible.
		Buckets: []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
	})
	c := prometheus.NewCounter(prometheus.CounterOpts{
		Name: timeoutName,
		Help: "Audit write advisory-lock acquisition timeouts (§11.7 AUDIT_CONCURRENCY_TIMEOUT).",
	})
	metrics.MustRegister(reg, h)
	metrics.MustRegister(reg, c)
	return &LockMetrics{acquireSeconds: h, concurrencyTimeouts: c}, nil
}

func (m *LockMetrics) observeAcquire(d time.Duration) {
	if m != nil {
		m.acquireSeconds.Observe(d.Seconds())
	}
}

func (m *LockMetrics) incTimeout() {
	if m != nil {
		m.concurrencyTimeouts.Inc()
	}
}

// ConcurrencyTimeoutError is returned when an audit write fails to
// acquire the per-tenant advisory lock within AcquireTimeoutMs. Code()
// is the §11.7 AUDIT_CONCURRENCY_TIMEOUT error code the caller surfaces;
// the gateway retries on the same replica before giving up.
// spec: §11.7 item 3 line 368.
type ConcurrencyTimeoutError struct {
	TenantID string
	Err      error
}

// Code returns the §11.7 typed error code.
func (e *ConcurrencyTimeoutError) Code() string { return "AUDIT_CONCURRENCY_TIMEOUT" }

func (e *ConcurrencyTimeoutError) Error() string {
	return fmt.Sprintf("auditstore: tenant %q audit advisory-lock acquisition timed out (AUDIT_CONCURRENCY_TIMEOUT): %v", e.TenantID, e.Err)
}

func (e *ConcurrencyTimeoutError) Unwrap() error { return e.Err }

// AuditUnavailableError is returned by Store.Append after exhausting the
// configured MaxRetries. The top-level request maps it to HTTP 503
// audit_unavailable and the AuditLockContention alert fires.
// spec: §11.7 item 3 line 368.
type AuditUnavailableError struct {
	TenantID string
	Attempts int
	Err      error
}

// Code returns the §11.7 503 response code.
func (e *AuditUnavailableError) Code() string { return "audit_unavailable" }

// HTTPStatus is the §11.7 "503 audit_unavailable" status.
func (e *AuditUnavailableError) HTTPStatus() int { return 503 }

func (e *AuditUnavailableError) Error() string {
	return fmt.Sprintf("auditstore: tenant %q audit write unavailable after %d attempts (503 audit_unavailable): %v", e.TenantID, e.Attempts, e.Err)
}

func (e *AuditUnavailableError) Unwrap() error { return e.Err }

// isLockTimeout reports whether err is the Postgres signal that the
// statement_timeout fired on the advisory-lock acquisition (57014
// query_canceled) or that the backend was terminated mid-wait (57P01
// admin_shutdown), or that the caller's context deadline elapsed first.
func isLockTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "57014", "57P01":
			return true
		}
	}
	return false
}

// acquireAuditLock takes the §11.7 per-tenant audit advisory lock under a
// bounded statement_timeout and records the acquisition latency. It is
// the first data statement of the audit-write transaction (after the
// SET LOCAL that scopes the timeout), taken before the tail-row SELECT
// that reads prev_hash and sequence_number. A timeout returns a
// *ConcurrencyTimeoutError; the lock budget is lifted once the lock is
// held so the seal+insert run under the connection's default timeout.
// spec: §11.7 item 3 lines 367-368.
func acquireAuditLock(ctx context.Context, tx pgx.Tx, tenantID string, cfg LockConfig, m *LockMetrics) error {
	cfg = cfg.withDefaults()
	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL statement_timeout = %d", cfg.AcquireTimeoutMs)); err != nil {
		return err
	}
	start := time.Now()
	_, lockErr := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "audit:"+tenantID)
	m.observeAcquire(time.Since(start))
	if lockErr != nil {
		if isLockTimeout(lockErr) {
			m.incTimeout()
			return &ConcurrencyTimeoutError{TenantID: tenantID, Err: lockErr}
		}
		return lockErr
	}
	// The acquireTimeoutMs budget bounds only the lock acquisition. Lift
	// it before the tail SELECT + INSERT so a large payload write is not
	// charged against the lock SLO.
	if _, err := tx.Exec(ctx, `SET LOCAL statement_timeout = 0`); err != nil {
		return err
	}
	return nil
}

// sleepBackoff waits the exponential-backoff interval for retry attempt
// (1-based): RetryBaseMs * 2^(attempt-1), jittered ±20%. It returns the
// context error when ctx is cancelled mid-wait so a shutdown does not
// block on the audit retry loop.
func sleepBackoff(ctx context.Context, cfg LockConfig, attempt int) error {
	base := time.Duration(cfg.RetryBaseMs) * time.Millisecond
	backoff := base << (attempt - 1)
	jitter := 1.0 + (rand.Float64()*0.4 - 0.2) // ±20%
	d := time.Duration(float64(backoff) * jitter)
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
