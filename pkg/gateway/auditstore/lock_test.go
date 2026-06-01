// SPDX-License-Identifier: MIT

package auditstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/prometheus/client_golang/prometheus"
)

// spec: §11.7 item 3 line 368 — acquireTimeoutMs/maxRetries/retryBaseMs
// defaults; a zero or negative field inherits the spec default.
func TestLockConfigWithDefaults_spec_11_7(t *testing.T) {
	d := DefaultLockConfig()
	if d.AcquireTimeoutMs != 5000 || d.MaxRetries != 3 || d.RetryBaseMs != 20 {
		t.Fatalf("spec defaults drifted: %+v", d)
	}
	got := LockConfig{}.withDefaults()
	if got != d {
		t.Errorf("zero config did not inherit defaults: %+v", got)
	}
	got = LockConfig{AcquireTimeoutMs: 1000, MaxRetries: -1, RetryBaseMs: 0}.withDefaults()
	if got.AcquireTimeoutMs != 1000 {
		t.Errorf("explicit timeout override lost: %+v", got)
	}
	if got.MaxRetries != 3 || got.RetryBaseMs != 20 {
		t.Errorf("negative/zero fields did not fall back to defaults: %+v", got)
	}
}

// spec: §11.7 item 3 line 368 — a statement_timeout firing on the
// advisory lock (57014) or a backend termination (57P01) or a context
// deadline is the AUDIT_CONCURRENCY_TIMEOUT signal; nothing else is.
func TestIsLockTimeout_spec_11_7(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"query_canceled", &pgconn.PgError{Code: "57014"}, true},
		{"admin_shutdown", &pgconn.PgError{Code: "57P01"}, true},
		{"context_deadline", context.DeadlineExceeded, true},
		{"unique_violation", &pgconn.PgError{Code: "23505"}, false},
		{"plain_error", errors.New("boom"), false},
		{"nil", nil, false},
		{"wrapped_57014", &ConcurrencyTimeoutError{Err: &pgconn.PgError{Code: "57014"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isLockTimeout(tc.err); got != tc.want {
				t.Errorf("isLockTimeout(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// spec: §11.7 item 3 line 368 — the typed error codes the gateway
// surfaces: AUDIT_CONCURRENCY_TIMEOUT to the caller, then 503
// audit_unavailable after the retry budget is spent.
func TestAuditLockErrorCodes_spec_11_7(t *testing.T) {
	inner := &pgconn.PgError{Code: "57014"}
	cte := &ConcurrencyTimeoutError{TenantID: "acme", Err: inner}
	if cte.Code() != "AUDIT_CONCURRENCY_TIMEOUT" {
		t.Errorf("concurrency code = %q", cte.Code())
	}
	if !errors.Is(cte, inner) {
		t.Errorf("ConcurrencyTimeoutError does not unwrap to its cause")
	}
	au := &AuditUnavailableError{TenantID: "acme", Attempts: 4, Err: cte}
	if au.Code() != "audit_unavailable" || au.HTTPStatus() != 503 {
		t.Errorf("unavailable code/status = %q/%d", au.Code(), au.HTTPStatus())
	}
	var asCTE *ConcurrencyTimeoutError
	if !errors.As(au, &asCTE) {
		t.Errorf("AuditUnavailableError does not unwrap to the timeout cause")
	}
}

// spec: §11.7 item 3 line 368 — the retry backoff must abandon promptly
// when the request context is cancelled (gateway shutdown), rather than
// blocking on the audit retry loop.
func TestSleepBackoffHonorsContextCancel_spec_11_7(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepBackoff(ctx, DefaultLockConfig(), 1); !errors.Is(err, context.Canceled) {
		t.Errorf("sleepBackoff with cancelled ctx = %v, want context.Canceled", err)
	}
	// A live context returns nil after the (small) interval elapses.
	cfg := LockConfig{RetryBaseMs: 1}.withDefaults()
	start := time.Now()
	if err := sleepBackoff(context.Background(), cfg, 1); err != nil {
		t.Errorf("sleepBackoff live ctx = %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("sleepBackoff for a 1ms base waited %v", elapsed)
	}
}

// spec: §11.7 item 3 — the lock metric surface registers under the
// §16.1.1 naming rules and is safe to observe through a nil receiver.
func TestNewLockMetrics_spec_11_7(t *testing.T) {
	reg := prometheus.NewRegistry()
	m, err := NewLockMetrics(reg)
	if err != nil {
		t.Fatalf("NewLockMetrics: %v", err)
	}
	m.observeAcquire(10 * time.Millisecond)
	m.incTimeout()

	var nilM *LockMetrics
	nilM.observeAcquire(time.Second) // must not panic
	nilM.incTimeout()

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	seen := map[string]bool{}
	for _, mf := range mfs {
		seen[mf.GetName()] = true
	}
	for _, name := range []string{"lenny_audit_lock_acquire_seconds", "lenny_audit_concurrency_timeout_total"} {
		if !seen[name] {
			t.Errorf("metric %q not registered", name)
		}
	}
}
