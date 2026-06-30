// SPDX-License-Identifier: MIT

package createdsweeper

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// spec: §7.1 line 58 — abandoned `created`-state rows past the
// `maxCreatedStateTimeoutSeconds` deadline are reclaimable by the
// sweep. A row whose CreatedAt is older than the timeout is dropped;
// a row inside the window survives.
func TestSweepDropsExpiredCreatedRows_spec_7_1_20(t *testing.T) {
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	store := memstore.New()
	mustCreate(t, store, sessionstore.Session{
		ID: "expired", TenantID: "acme",
		State: session.StateCreated, CreatedAt: now.Add(-10 * time.Minute),
	})
	mustCreate(t, store, sessionstore.Session{
		ID: "fresh", TenantID: "acme",
		State: session.StateCreated, CreatedAt: now.Add(-2 * time.Minute),
	})
	sw := New(store, StaticTenants{"acme"}, Options{Clock: func() time.Time { return now }})
	dropped, err := sw.Tick(context.Background(), now)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if dropped != 1 {
		t.Errorf("dropped = %d, want 1 (only the row past the §7.1 timeout)", dropped)
	}
	if _, err := store.Get(context.Background(), "acme", "expired"); err == nil {
		t.Errorf("expired row was not removed; sweep failed to enforce §7.1 line 58")
	}
	if _, err := store.Get(context.Background(), "acme", "fresh"); err != nil {
		t.Errorf("fresh row was swept: %v (sweep must not touch rows inside the window)", err)
	}
}

// spec: §7.1 — only `created`-state rows are eligible. A ready,
// running, suspended, etc. row is never swept even if its CreatedAt
// pre-dates the timeout.
func TestSweepIgnoresNonCreatedStateRows_spec_7_1_20(t *testing.T) {
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	store := memstore.New()
	for _, st := range []session.State{
		session.StateReady, session.StateRunning, session.StateSuspended,
		session.StateCompleted, session.StateFailed, session.StateCancelled,
	} {
		mustCreate(t, store, sessionstore.Session{
			ID: "row-" + string(st), TenantID: "acme",
			State: st, CreatedAt: now.Add(-time.Hour),
		})
	}
	sw := New(store, StaticTenants{"acme"}, Options{Clock: func() time.Time { return now }})
	dropped, _ := sw.Tick(context.Background(), now)
	if dropped != 0 {
		t.Errorf("dropped = %d, want 0 (non-created rows are not eligible)", dropped)
	}
}

// spec: §7.1 — a row with a zero CreatedAt timestamp (a malformed
// row that bypassed the gateway's auto-stamp; production stores stamp
// it on Create) is never swept. The deadline check requires a real
// timestamp so the sweep cannot fault-overwrite an unknown row by
// treating zero as "very old".
func TestSweepSkipsZeroCreatedAt_spec_7_1_20(t *testing.T) {
	now := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		row  sessionstore.Session
		want bool
	}{
		{"zero-created-at", sessionstore.Session{State: session.StateCreated}, false},
		{
			"created-fresh",
			sessionstore.Session{State: session.StateCreated, CreatedAt: now.Add(-time.Minute)},
			false,
		},
		{
			"created-stale",
			sessionstore.Session{State: session.StateCreated, CreatedAt: now.Add(-time.Hour)},
			true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := eligible(tc.row, DefaultCreatedStateTimeout, now)
			if got != tc.want {
				t.Errorf("eligible = %v, want %v", got, tc.want)
			}
		})
	}
}

// spec: §7.1 — the sweep operates on every tenant the TenantLister
// reports. Multi-tenant deployments do not leak abandoned rows across
// the tenant boundary.
func TestSweepMultiTenant_spec_7_1_20(t *testing.T) {
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	store := memstore.New()
	mustCreate(t, store, sessionstore.Session{
		ID: "a", TenantID: "acme",
		State: session.StateCreated, CreatedAt: now.Add(-10 * time.Minute),
	})
	mustCreate(t, store, sessionstore.Session{
		ID: "b", TenantID: "globex",
		State: session.StateCreated, CreatedAt: now.Add(-10 * time.Minute),
	})
	sw := New(store, StaticTenants{"acme", "globex"}, Options{Clock: func() time.Time { return now }})
	dropped, _ := sw.Tick(context.Background(), now)
	if dropped != 2 {
		t.Errorf("dropped = %d, want 2 (one per tenant)", dropped)
	}
}

// spec: §16 — the metrics sink records success outcomes and the
// per-sweep drop count, so the operator can graph `lenny_gc_runs_total`
// and friends. The error path increments the error outcome only.
func TestSweepMetricsSink_spec_7_1_20(t *testing.T) {
	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	store := memstore.New()
	mustCreate(t, store, sessionstore.Session{
		ID: "e", TenantID: "acme",
		State: session.StateCreated, CreatedAt: now.Add(-10 * time.Minute),
	})
	sink := &captureMetrics{}
	sw := New(store, StaticTenants{"acme"}, Options{
		Clock: func() time.Time { return now }, Metrics: sink,
	})
	if _, err := sw.Tick(context.Background(), now); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if sink.successes != 1 || sink.errors != 0 {
		t.Errorf("metrics success/error = %d/%d, want 1/0", sink.successes, sink.errors)
	}
	if sink.collected != 1 {
		t.Errorf("collected metric = %d, want 1", sink.collected)
	}
}

// spec: §7.1 — a tenant-listing failure aborts the sweep with the
// error and reports outcome=error so the operator sees the missed
// cycle through observability. The retention GC follows the same
// contract; this sweeper mirrors it.
func TestSweepReportsTenantListErrors_spec_7_1_20(t *testing.T) {
	sink := &captureMetrics{}
	sw := New(memstore.New(), &errorTenants{}, Options{Metrics: sink})
	if _, err := sw.Tick(context.Background(), time.Now()); err == nil {
		t.Fatalf("Tick: expected an error from the failing TenantLister")
	}
	if sink.errors != 1 || sink.successes != 0 {
		t.Errorf("metrics success/error = %d/%d, want 0/1", sink.successes, sink.errors)
	}
}

// spec: §7.1 — Run terminates promptly on ctx cancellation so the
// sweeper goroutine winds down with the rest of the process.
func TestSweepRunStopsOnContextCancel_spec_7_1_20(t *testing.T) {
	sw := New(memstore.New(), StaticTenants{"acme"}, Options{Interval: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { sw.Run(ctx, nil); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return within 2s of ctx cancel")
	}
}

// spec: §15.1 line 630 — a `created`-expiry release returns the pod claimed
// at /create to the pool and revokes any assigned lease before the row is
// retired. The sweep calls the injected Reclaimer with the row's
// PodAssignment, PoolRef, and ID exactly once per eligible row, ahead of the
// row Delete, so an abandoned create does not strand a pod.
func TestSweepReclaimsPodBeforeDelete_spec_15_1_630(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	store := memstore.New()
	mustCreate(t, store, sessionstore.Session{
		ID: "abandoned", TenantID: "acme",
		State: session.StateCreated, CreatedAt: now.Add(-10 * time.Minute),
		PodAssignment: "pod-abandoned", PoolRef: "pool-a",
	})
	rec := &captureReclaim{}
	sw := New(store, StaticTenants{"acme"}, Options{
		Clock: func() time.Time { return now }, Reclaim: rec.reclaim,
	})
	dropped, err := sw.Tick(context.Background(), now)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if dropped != 1 {
		t.Fatalf("dropped = %d, want 1", dropped)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("reclaim calls = %d, want 1 (the eligible row's pod must be released)", len(rec.calls))
	}
	got := rec.calls[0]
	if got.podName != "pod-abandoned" || got.poolRef != "pool-a" || got.sessionID != "abandoned" {
		t.Errorf("reclaim args = %+v, want pod-abandoned/pool-a/abandoned (row PodAssignment+PoolRef+ID)", got)
	}
	if _, err := store.Get(context.Background(), "acme", "abandoned"); err == nil {
		t.Errorf("row was not deleted after a successful reclaim")
	}
}

// spec: §15.1 line 630, §7.1 line 28 — a reclaim failure aborts the sweep with
// the error and leaves the row in place, so the next tick retries the release
// rather than dropping a row whose pod was never returned to the pool. The
// abort-on-error semantics that the per-row Delete failure already had extend
// to the reclaim step.
func TestSweepAbortsAndKeepsRowOnReclaimError_spec_15_1_630(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	store := memstore.New()
	mustCreate(t, store, sessionstore.Session{
		ID: "stuck", TenantID: "acme",
		State: session.StateCreated, CreatedAt: now.Add(-10 * time.Minute),
		PodAssignment: "pod-stuck", PoolRef: "pool-a",
	})
	sink := &captureMetrics{}
	sw := New(store, StaticTenants{"acme"}, Options{
		Clock: func() time.Time { return now }, Metrics: sink,
		Reclaim: func(context.Context, string, string, string) error {
			return errors.New("injected reclaim failure")
		},
	})
	dropped, err := sw.Tick(context.Background(), now)
	if err == nil {
		t.Fatalf("Tick: expected an error from the failing reclaim")
	}
	if dropped != 0 {
		t.Errorf("dropped = %d, want 0 (a reclaim failure must not drop the row)", dropped)
	}
	if sink.errors != 1 || sink.successes != 0 {
		t.Errorf("metrics success/error = %d/%d, want 0/1", sink.successes, sink.errors)
	}
	if _, err := store.Get(context.Background(), "acme", "stuck"); err != nil {
		t.Errorf("row was deleted despite the reclaim failure: %v", err)
	}
}

// spec: §15.1 line 630 — a `created` row with no persisted pod binding
// (PodAssignment empty, e.g. a row that failed before the at-create claim
// landed) is dropped without invoking the Reclaimer, so the sweep never
// deletes a phantom claim. The lease revoke is also skipped because no pod
// and therefore no lease is bound.
func TestSweepSkipsReclaimWhenNoPodBound_spec_15_1_630(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	store := memstore.New()
	mustCreate(t, store, sessionstore.Session{
		ID: "no-pod", TenantID: "acme",
		State: session.StateCreated, CreatedAt: now.Add(-10 * time.Minute),
	})
	rec := &captureReclaim{}
	sw := New(store, StaticTenants{"acme"}, Options{
		Clock: func() time.Time { return now }, Reclaim: rec.reclaim,
	})
	dropped, err := sw.Tick(context.Background(), now)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if dropped != 1 {
		t.Fatalf("dropped = %d, want 1", dropped)
	}
	if len(rec.calls) != 0 {
		t.Errorf("reclaim calls = %d, want 0 (a row with no PodAssignment has no claim to release)", len(rec.calls))
	}
	if _, err := store.Get(context.Background(), "acme", "no-pod"); err == nil {
		t.Errorf("row with no pod binding was not dropped")
	}
}

// spec: §15.1 line 630 — a sweep wired with no Reclaimer (the in-memory
// dev/test gateway) drops the row without attempting a pod release, so the
// nil-dependency path degrades to a plain row drop rather than panicking.
func TestSweepWithoutReclaimerDropsRow_spec_15_1_630(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	store := memstore.New()
	mustCreate(t, store, sessionstore.Session{
		ID: "abandoned", TenantID: "acme",
		State: session.StateCreated, CreatedAt: now.Add(-10 * time.Minute),
		PodAssignment: "pod-abandoned", PoolRef: "pool-a",
	})
	sw := New(store, StaticTenants{"acme"}, Options{Clock: func() time.Time { return now }})
	dropped, err := sw.Tick(context.Background(), now)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if dropped != 1 {
		t.Errorf("dropped = %d, want 1 (the nil-Reclaimer sweep still drops the row)", dropped)
	}
}

// --- test helpers ---------------------------------------------------

func mustCreate(t *testing.T, s sessionstore.Store, row sessionstore.Session) {
	t.Helper()
	if err := s.Create(context.Background(), row); err != nil {
		t.Fatalf("seed %s/%s: %v", row.TenantID, row.ID, err)
	}
}

type captureMetrics struct {
	successes int
	errors    int
	collected int
}

func (m *captureMetrics) IncSweep(outcome string) {
	switch outcome {
	case "success":
		m.successes++
	case "error":
		m.errors++
	}
}
func (m *captureMetrics) AddCollected(n int) { m.collected += n }

type errorTenants struct{}

func (e *errorTenants) ListTenants(_ context.Context) ([]string, error) {
	return nil, errors.New("injected tenant-list error")
}

// captureReclaim records each Reclaimer invocation so a test can assert the
// sweep released the right pod with the row's PodAssignment, PoolRef, and ID.
type captureReclaim struct {
	calls []reclaimCall
}

type reclaimCall struct {
	podName   string
	poolRef   string
	sessionID string
}

func (c *captureReclaim) reclaim(_ context.Context, podName, poolRef, sessionID string) error {
	c.calls = append(c.calls, reclaimCall{podName: podName, poolRef: poolRef, sessionID: sessionID})
	return nil
}
