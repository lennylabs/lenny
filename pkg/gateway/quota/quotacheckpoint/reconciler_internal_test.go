// SPDX-License-Identifier: MIT

package quotacheckpoint

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/quota/quotastore"
	"github.com/lennylabs/lenny/pkg/quota"
)

var tickNow = time.Date(2026, 6, 2, 14, 30, 0, 0, time.UTC)

// recCounter records restore calls (the reconcile side) and returns a
// fixed live value so a checkpoint row triggers a restore.
type recCounter struct{ restores int }

func (c *recCounter) Usage(context.Context, string, string, quota.ResetPeriod, time.Time) (int64, error) {
	return 123, nil
}

func (c *recCounter) TenantRollupUsage(context.Context, string, quota.ResetPeriod, time.Time) (int64, error) {
	return 456, nil
}

func (c *recCounter) RestoreUserWindow(_ context.Context, _, _ string, _ quota.ResetPeriod, _ time.Time, v int64) (int64, error) {
	c.restores++
	return v, nil
}

func (c *recCounter) RestoreTenantRollupWindow(_ context.Context, _ string, _ quota.ResetPeriod, _ time.Time, v int64) (int64, error) {
	c.restores++
	return v, nil
}

// recStore records Write calls (the checkpoint side) and serves a fixed
// current-window checkpoint row (the reconcile side).
type recStore struct {
	writes int
	rows   []Row
}

func (s *recStore) Write(context.Context, []Row) error                        { s.writes++; return nil }
func (s *recStore) ListActive(context.Context) ([]Row, error)                 { return s.rows, nil }
func (s *recStore) ListByTenant(context.Context, string) ([]Row, error)       { return s.rows, nil }
func (s *recStore) DeleteByUser(context.Context, string, string) (int, error) { return 0, nil }
func (s *recStore) DeleteByTenant(context.Context, string) (int, error)       { return 0, nil }

type oneSubject struct{}

func (oneSubject) ListActiveSubjects(context.Context) ([]Subject, error) {
	return []Subject{{TenantID: "acme", UserID: "alice@acme.com"}}, nil
}

type hourlyPeriods struct{}

func (hourlyPeriods) ResolvePeriod(context.Context, string) (quota.ResetPeriod, error) {
	return quota.ResetHourly, nil
}

func newTickReconciler(t *testing.T) (*Reconciler, *recStore, *recCounter) {
	t.Helper()
	lbl, _ := quotastore.WindowLabel(quota.ResetHourly, tickNow)
	cnt := &recCounter{}
	store := &recStore{rows: []Row{
		{TenantID: "acme", Scope: ScopeUser, SubjectID: "alice@acme.com", Period: "hourly", WindowLabel: lbl, TokenTotal: 500},
	}}
	svc := &Service{
		Store:    store,
		Subjects: oneSubject{},
		Periods:  hourlyPeriods{},
		Reader:   cnt,
		Restorer: cnt,
		Now:      func() time.Time { return tickNow },
	}
	return &Reconciler{Probe: nil, Service: svc, Interval: time.Hour}, store, cnt
}

// spec: §11.2 lines 44, 48 — a down→up edge reconstructs before
// checkpointing; a steady-reachable tick only checkpoints; an unreachable
// tick does neither.
func TestReconcilerTickEdge_spec_11_2(t *testing.T) {
	t.Parallel()

	up := func(context.Context) bool { return true }
	down := func(context.Context) bool { return false }

	// Down → up edge: reconcile (restore) and checkpoint both run.
	r, store, cnt := newTickReconciler(t)
	r.Probe = up
	if got := r.tick(context.Background(), false); !got {
		t.Fatal("tick(reachable) returned false")
	}
	if cnt.restores == 0 {
		t.Error("down→up edge did not reconstruct")
	}
	if store.writes == 0 {
		t.Error("reachable tick did not checkpoint")
	}

	// Steady reachable: no further reconcile, checkpoint continues.
	r2, store2, cnt2 := newTickReconciler(t)
	r2.Probe = up
	r2.tick(context.Background(), true)
	if cnt2.restores != 0 {
		t.Error("steady-reachable tick reconstructed unexpectedly")
	}
	if store2.writes == 0 {
		t.Error("steady-reachable tick did not checkpoint")
	}

	// Unreachable: neither runs, and the tick reports down.
	r3, store3, cnt3 := newTickReconciler(t)
	r3.Probe = down
	if got := r3.tick(context.Background(), true); got {
		t.Fatal("tick(unreachable) returned true")
	}
	if cnt3.restores != 0 || store3.writes != 0 {
		t.Error("unreachable tick performed work")
	}
}

// A Reconciler missing a required seam is a no-op Run that returns at once.
func TestReconcilerRunNoopWhenUnwired(t *testing.T) {
	t.Parallel()
	(&Reconciler{}).Run(context.Background())                                                  // no Probe, no Service
	(&Reconciler{Probe: func(context.Context) bool { return true }}).Run(context.Background()) // no Service
}
