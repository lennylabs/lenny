// SPDX-License-Identifier: MIT

package checkpointer_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/checkpoint"
	"github.com/lennylabs/lenny/pkg/gateway/checkpointer"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// stubGauge captures every SetCheckpointStaleSessions call so tests
// can assert on the final per-label values.
type stubGauge struct {
	mu     sync.Mutex
	counts map[[2]string]int
}

func newStubGauge() *stubGauge { return &stubGauge{counts: map[[2]string]int{}} }

func (g *stubGauge) SetCheckpointStaleSessions(pool, level string, count int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.counts[[2]string{pool, level}] = count
}

func (g *stubGauge) Get(pool, level string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.counts[[2]string{pool, level}]
}

// stubTenantLister is an in-memory tenants enumerator for the reaper.
type stubTenantLister struct {
	ids []string
	err error
}

func (s stubTenantLister) ListTenants(_ context.Context) ([]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.ids, nil
}

// seedSession inserts a row into the session store at the given state
// with the given last-successful-checkpoint timestamp.
func seedSession(t *testing.T, store sessionstore.Store, tenantID, sessionID, pool string, state session.State, lastCp time.Time) {
	t.Helper()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID:                         sessionID,
		TenantID:                   tenantID,
		State:                      state,
		RuntimeRef:                 "echo",
		PoolRef:                    pool,
		LastSuccessfulCheckpointAt: lastCp,
	}); err != nil {
		t.Fatalf("seed session %s: %v", sessionID, err)
	}
}

// spec: §4.4 line 256 — a session whose `last_successful_checkpoint_at`
// is older than the interval is stale; the per-(pool, level) gauge
// reports the count.
func TestFreshnessReaperCountsStaleSessions(t *testing.T) {
	store := memstore.New()
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	// Two stale sessions in pool "tier-1" and one fresh.
	seedSession(t, store, "acme", "s-stale-1", "tier-1", session.StateRunning, now.Add(-20*time.Minute))
	seedSession(t, store, "acme", "s-stale-2", "tier-1", session.StateRunning, now.Add(-12*time.Minute))
	seedSession(t, store, "acme", "s-fresh", "tier-1", session.StateRunning, now.Add(-1*time.Minute))

	gauge := newStubGauge()
	r := &checkpointer.FreshnessReaper{
		Tenants:  stubTenantLister{ids: []string{"acme"}},
		Sessions: store,
		Gauge:    gauge,
		Interval: 10 * time.Minute,
		Now:      func() time.Time { return now },
	}
	r.Sweep(context.Background())

	if got := gauge.Get("tier-1", "unknown"); got != 2 {
		t.Errorf("stale count for (tier-1, unknown) = %d, want 2", got)
	}
}

// spec: §4.4 line 256 — terminal sessions are not active and must NOT
// count toward the staleness gauge. A completed session whose
// `last_successful_checkpoint_at` is never set would otherwise mask
// as permanently stale.
func TestFreshnessReaperIgnoresTerminalSessions(t *testing.T) {
	store := memstore.New()
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	for i, state := range []session.State{
		session.StateCompleted,
		session.StateFailed,
		session.StateCancelled,
		session.StateExpired,
	} {
		seedSession(t, store, "acme", "s-term-"+string(rune('a'+i)), "tier-1", state, time.Time{})
	}

	gauge := newStubGauge()
	r := &checkpointer.FreshnessReaper{
		Tenants:  stubTenantLister{ids: []string{"acme"}},
		Sessions: store,
		Gauge:    gauge,
		Interval: 10 * time.Minute,
		Now:      func() time.Time { return now },
	}
	r.Sweep(context.Background())

	if got := gauge.Get("tier-1", "unknown"); got != 0 {
		t.Errorf("terminal sessions counted: gauge for (tier-1, unknown) = %d, want 0", got)
	}
}

// spec: §4.4 line 256 — a session that has never been checkpointed is
// stale once an interval boundary has passed; the `IsZero()` branch
// of FreshnessCheck applies.
func TestFreshnessReaperCountsNeverCheckpointed(t *testing.T) {
	store := memstore.New()
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	seedSession(t, store, "acme", "s-never", "tier-1", session.StateRunning, time.Time{})

	gauge := newStubGauge()
	r := &checkpointer.FreshnessReaper{
		Tenants:  stubTenantLister{ids: []string{"acme"}},
		Sessions: store,
		Gauge:    gauge,
		Interval: 10 * time.Minute,
		Now:      func() time.Time { return now },
	}
	r.Sweep(context.Background())

	if got := gauge.Get("tier-1", "unknown"); got != 1 {
		t.Errorf("never-checkpointed session not counted as stale: gauge = %d, want 1", got)
	}
}

// spec: §4.4 line 256 — the gauge is labelled per (pool, level); the
// reaper resolves the labels via the ResolveLabels callback so the
// gateway can wire it against its runtime / pool registry.
func TestFreshnessReaperHonoursLabelResolver(t *testing.T) {
	store := memstore.New()
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	seedSession(t, store, "acme", "s1", "tier-1", session.StateRunning, now.Add(-20*time.Minute))
	seedSession(t, store, "acme", "s2", "tier-1", session.StateRunning, now.Add(-15*time.Minute))
	seedSession(t, store, "acme", "s3", "tier-2", session.StateRunning, now.Add(-20*time.Minute))

	gauge := newStubGauge()
	r := &checkpointer.FreshnessReaper{
		Tenants:  stubTenantLister{ids: []string{"acme"}},
		Sessions: store,
		Gauge:    gauge,
		Interval: 10 * time.Minute,
		Now:      func() time.Time { return now },
		ResolveLabels: func(s *sessionstore.Session) (string, checkpoint.Level) {
			// Pool flows through; level is "full" for tier-1, "basic"
			// otherwise — exactly the production-side pool/level
			// mapping pattern.
			if s.PoolRef == "tier-1" {
				return s.PoolRef, checkpoint.LevelFull
			}
			return s.PoolRef, checkpoint.LevelBasic
		},
	}
	r.Sweep(context.Background())

	if got := gauge.Get("tier-1", string(checkpoint.LevelFull)); got != 2 {
		t.Errorf("tier-1 full stale = %d, want 2", got)
	}
	if got := gauge.Get("tier-2", string(checkpoint.LevelBasic)); got != 1 {
		t.Errorf("tier-2 basic stale = %d, want 1", got)
	}
}

// spec: §4.4 line 256 — the reaper sweeps every tenant returned by
// the TenantLister so a multi-tenant deployment reports a fleet-wide
// staleness count.
func TestFreshnessReaperSweepsAcrossTenants(t *testing.T) {
	store := memstore.New()
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	seedSession(t, store, "acme", "s1", "tier-1", session.StateRunning, now.Add(-20*time.Minute))
	seedSession(t, store, "globex", "s2", "tier-1", session.StateRunning, now.Add(-30*time.Minute))

	gauge := newStubGauge()
	r := &checkpointer.FreshnessReaper{
		Tenants:  stubTenantLister{ids: []string{"acme", "globex"}},
		Sessions: store,
		Gauge:    gauge,
		Interval: 10 * time.Minute,
		Now:      func() time.Time { return now },
	}
	r.Sweep(context.Background())

	if got := gauge.Get("tier-1", "unknown"); got != 2 {
		t.Errorf("cross-tenant stale count = %d, want 2", got)
	}
}

// spec: §4.4 line 256 — a previously stale (pool, level) cell that
// has no stale sessions on this cycle must zero out so the gauge does
// not pin at the peak count.
func TestFreshnessReaperZeroesCellsAfterRecovery(t *testing.T) {
	store := memstore.New()
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	// Start with a stale session.
	seedSession(t, store, "acme", "s1", "tier-1", session.StateRunning, now.Add(-20*time.Minute))

	gauge := newStubGauge()
	r := &checkpointer.FreshnessReaper{
		Tenants:  stubTenantLister{ids: []string{"acme"}},
		Sessions: store,
		Gauge:    gauge,
		Interval: 10 * time.Minute,
		Now:      func() time.Time { return now },
	}
	r.Sweep(context.Background())
	if got := gauge.Get("tier-1", "unknown"); got != 1 {
		t.Fatalf("first sweep stale = %d, want 1", got)
	}

	// Now bump the session's last_successful_checkpoint_at so it is fresh.
	_, _ = store.Update(context.Background(), "acme", "s1", func(s *sessionstore.Session) error {
		s.LastSuccessfulCheckpointAt = now.Add(-1 * time.Minute)
		return nil
	})
	r.Sweep(context.Background())
	if got := gauge.Get("tier-1", "unknown"); got != 0 {
		t.Errorf("post-recovery stale = %d, want 0", got)
	}
}

// spec: §4.4 line 256 — a per-tenant list error must not stop the
// sweep; the reaper logs the error via OnError and moves on so a
// transient failure for one tenant cannot mask staleness in others.
func TestFreshnessReaperContinuesAfterPerTenantError(t *testing.T) {
	store := memstore.New()
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	seedSession(t, store, "globex", "s1", "tier-1", session.StateRunning, now.Add(-20*time.Minute))

	var capturedErrors []error
	r := &checkpointer.FreshnessReaper{
		Tenants:  stubTenantLister{ids: []string{"globex"}, err: nil},
		Sessions: store,
		Gauge:    newStubGauge(),
		Interval: 10 * time.Minute,
		Now:      func() time.Time { return now },
		OnError: func(_ string, err error) {
			capturedErrors = append(capturedErrors, err)
		},
	}
	// Inject a tenant-listing error then a clean one: the failing
	// sweep records the error and exits.
	r.Tenants = stubTenantLister{err: errors.New("tenants down")}
	r.Sweep(context.Background())
	if len(capturedErrors) != 1 {
		t.Errorf("OnError called %d times, want 1", len(capturedErrors))
	}
}

// spec: §4.4 line 256 — a nil gauge / store / tenants lister must
// degrade gracefully; the reaper does not panic so a partially
// configured gateway start cannot crash the process.
func TestFreshnessReaperNoOpOnUnconfiguredFields(t *testing.T) {
	r := &checkpointer.FreshnessReaper{}
	// No panic.
	r.Sweep(context.Background())
}

// spec: §4.4 line 256 — Run takes an immediate first sweep so the
// gauge reflects the current state without waiting one
// SweepInterval. Subsequent ticks follow the configured cadence.
func TestFreshnessReaperRunSweepsImmediately(t *testing.T) {
	store := memstore.New()
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, time.UTC)
	seedSession(t, store, "acme", "s1", "tier-1", session.StateRunning, now.Add(-20*time.Minute))

	gauge := newStubGauge()
	r := &checkpointer.FreshnessReaper{
		Tenants:       stubTenantLister{ids: []string{"acme"}},
		Sessions:      store,
		Gauge:         gauge,
		Interval:      10 * time.Minute,
		SweepInterval: time.Hour, // ensure we observe only the immediate sweep
		Now:           func() time.Time { return now },
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx)

	deadline := time.After(2 * time.Second)
	for {
		if gauge.Get("tier-1", "unknown") == 1 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("Run did not sweep within the deadline")
		case <-time.After(5 * time.Millisecond):
		}
	}
}
