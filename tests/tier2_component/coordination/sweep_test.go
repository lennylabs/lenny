//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §10.1 lease coordination sweeper
// (pkg/gateway/coordination), exercised against a real Redis lease
// store with in-memory session and tenant stores. It confirms a
// sweep acquires the coordination lease for every non-terminal
// session, leaves terminal sessions and other replicas' sessions
// alone, and is idempotent across passes.
package coordination_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/coordination"
	"github.com/lennylabs/lenny/pkg/gateway/leasestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// staticLister is a coordination.TenantLister over a fixed slice.
type staticLister []string

func (s staticLister) ListTenants(context.Context) ([]string, error) {
	return []string(s), nil
}

func uniq(t *testing.T) string {
	t.Helper()
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return hex.EncodeToString(b[:])
}

func seedSession(t *testing.T, store *memstore.Store, tenant, id string, state session.State) {
	t.Helper()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: tenant, State: state, RuntimeRef: "echo",
	}); err != nil {
		t.Fatalf("seed session %s: %v", id, err)
	}
}

// spec: 10.1
// diagnosis: the lease coordination sweeper in
// pkg/gateway/coordination did not behave as specified. A sweep must
// acquire the coordination lease for every non-terminal session, leave
// terminal sessions and other replicas' sessions alone, and be
// idempotent across passes.
func TestSweeperContract(t *testing.T) {
	t.Parallel()
	rd := containers.StartRedis(t, containers.RedisOptions{})
	leases := leasestore.New(rd.Client)
	ctx := context.Background()

	newSweeper := func(sessions *memstore.Store, tenants []string, replica string) *coordination.Sweeper {
		return coordination.NewSweeper(staticLister(tenants), sessions, leases, coordination.Options{
			ReplicaID: replica,
			TTL:       30 * time.Second,
			Interval:  time.Hour,
		})
	}

	t.Run("sweep acquires leases for non-terminal sessions only", func(t *testing.T) {
		run := uniq(t)
		sessions := memstore.New()
		live := []string{run + "-a", run + "-b", run + "-c"}
		seedSession(t, sessions, "acme", live[0], session.StateRunning)
		seedSession(t, sessions, "acme", live[1], session.StateCreated)
		seedSession(t, sessions, "acme", live[2], session.StateAwaitingClientAction)
		done := run + "-done"
		seedSession(t, sessions, "acme", done, session.StateCompleted)
		seedSession(t, sessions, "acme", run+"-failed", session.StateFailed)

		held, err := newSweeper(sessions, []string{"acme"}, "replica-1").Sweep(ctx)
		if err != nil {
			t.Fatalf("Sweep: %v", err)
		}
		if held != 3 {
			t.Errorf("held = %d, want 3 (the non-terminal sessions)", held)
		}
		for _, id := range live {
			lease, err := leases.Get(ctx, "acme", id)
			if err != nil {
				t.Errorf("non-terminal session %s has no lease: %v", id, err)
				continue
			}
			if lease.Holder != "replica-1" {
				t.Errorf("session %s lease holder = %q, want replica-1", id, lease.Holder)
			}
		}
		if _, err := leases.Get(ctx, "acme", done); err == nil {
			t.Error("a completed session must not hold a coordination lease")
		}
	})

	t.Run("sweep is idempotent across passes", func(t *testing.T) {
		run := uniq(t)
		sessions := memstore.New()
		seedSession(t, sessions, "acme", run+"-x", session.StateRunning)
		sw := newSweeper(sessions, []string{"acme"}, "replica-1")
		first, err := sw.Sweep(ctx)
		if err != nil || first != 1 {
			t.Fatalf("first Sweep: held=%d err=%v", first, err)
		}
		second, err := sw.Sweep(ctx)
		if err != nil || second != 1 {
			t.Fatalf("second Sweep: held=%d err=%v", second, err)
		}
		if lease, err := leases.Get(ctx, "acme", run+"-x"); err != nil || lease.Holder != "replica-1" {
			t.Errorf("lease after re-sweep: %+v err=%v", lease, err)
		}
	})

	t.Run("sweep leaves another replica's sessions alone", func(t *testing.T) {
		run := uniq(t)
		sessions := memstore.New()
		mine := run + "-mine"
		theirs := run + "-theirs"
		seedSession(t, sessions, "acme", mine, session.StateRunning)
		seedSession(t, sessions, "acme", theirs, session.StateRunning)
		// Another replica already holds the lease for `theirs`.
		if _, err := leases.Acquire(ctx, "acme", theirs, "replica-other", 30*time.Second); err != nil {
			t.Fatalf("pre-acquire: %v", err)
		}
		held, err := newSweeper(sessions, []string{"acme"}, "replica-1").Sweep(ctx)
		if err != nil {
			t.Fatalf("Sweep: %v", err)
		}
		if held != 1 {
			t.Errorf("held = %d, want 1 (only the session this replica owns)", held)
		}
		if lease, _ := leases.Get(ctx, "acme", theirs); lease.Holder != "replica-other" {
			t.Errorf("other replica's lease holder = %q, want replica-other", lease.Holder)
		}
		if lease, _ := leases.Get(ctx, "acme", mine); lease.Holder != "replica-1" {
			t.Errorf("own session lease holder = %q, want replica-1", lease.Holder)
		}
	})

	t.Run("sweep with no sessions holds nothing", func(t *testing.T) {
		held, err := newSweeper(memstore.New(), []string{"acme", "globex"}, "replica-1").Sweep(ctx)
		if err != nil || held != 0 {
			t.Errorf("empty Sweep: held=%d err=%v, want 0/nil", held, err)
		}
	})

	// spec: §4.2 line 156 — coordination_generation is incremented on
	// coordinator handoff across gateway replicas. The sweeper observes
	// the prior holder before its Acquire; when the prior holder is a
	// different replica, the sweeper records the handoff. The no-bump
	// cases are: lease unheld (priorHolder == ""), self-renew
	// (priorHolder == replicaID), and held-by-other (Acquire returns
	// ErrHeld so no handoff occurred).
	t.Run("RecordHandoff increments coordination_generation monotonically", func(t *testing.T) {
		run := uniq(t)
		sessions := memstore.New()
		sessID := run + "-handoff"
		seedSession(t, sessions, "acme", sessID, session.StateRunning)

		// Confirm baseline counter is zero.
		got, _ := sessions.Get(ctx, "acme", sessID)
		if got.CoordinationGeneration != 0 {
			t.Fatalf("baseline CoordinationGeneration = %d, want 0", got.CoordinationGeneration)
		}

		sw := newSweeper(sessions, []string{"acme"}, "replica-B")
		// One observed handoff bumps the counter by exactly one.
		sw.RecordHandoff(ctx, "acme", sessID)
		got, _ = sessions.Get(ctx, "acme", sessID)
		if got.CoordinationGeneration != 1 {
			t.Errorf("after one handoff, CoordinationGeneration = %d, want 1",
				got.CoordinationGeneration)
		}

		// Successive handoffs continue to advance.
		sw.RecordHandoff(ctx, "acme", sessID)
		sw.RecordHandoff(ctx, "acme", sessID)
		got, _ = sessions.Get(ctx, "acme", sessID)
		if got.CoordinationGeneration != 3 {
			t.Errorf("after three handoffs, CoordinationGeneration = %d, want 3",
				got.CoordinationGeneration)
		}
	})

	// spec: §4.2 line 156 — the sweeper does NOT bump
	// coordination_generation on a self-renew (the prior holder is
	// this same replica) or on a fresh acquisition of an unheld lease
	// (no prior holder).
	t.Run("Sweep self-renew does not bump counter", func(t *testing.T) {
		run := uniq(t)
		sessions := memstore.New()
		sessID := run + "-self-renew"
		seedSession(t, sessions, "acme", sessID, session.StateRunning)
		sw := newSweeper(sessions, []string{"acme"}, "replica-1")

		// First sweep: lease unheld, fresh acquisition by replica-1.
		// No handoff (priorHolder is empty).
		if _, err := sw.Sweep(ctx); err != nil {
			t.Fatalf("first Sweep: %v", err)
		}
		got, _ := sessions.Get(ctx, "acme", sessID)
		if got.CoordinationGeneration != 0 {
			t.Errorf("after fresh acquisition Sweep, CoordinationGeneration = %d, want 0",
				got.CoordinationGeneration)
		}

		// Second sweep: lease held by replica-1, renewed by replica-1.
		// No handoff (priorHolder == replicaID).
		if _, err := sw.Sweep(ctx); err != nil {
			t.Fatalf("second Sweep (renew): %v", err)
		}
		got, _ = sessions.Get(ctx, "acme", sessID)
		if got.CoordinationGeneration != 0 {
			t.Errorf("after self-renew Sweep, CoordinationGeneration = %d, want 0",
				got.CoordinationGeneration)
		}
	})

	// spec: §4.2 line 156 — when the lease is held by another replica
	// the sweeper's Acquire fails with ErrHeld; no handoff occurred so
	// the counter must not bump.
	t.Run("Sweep held-by-other does not bump counter", func(t *testing.T) {
		run := uniq(t)
		sessions := memstore.New()
		sessID := run + "-held-by-other"
		seedSession(t, sessions, "acme", sessID, session.StateRunning)

		// Replica-A owns the lease.
		if _, err := leases.Acquire(ctx, "acme", sessID, "replica-A", 30*time.Second); err != nil {
			t.Fatalf("acquire replica-A: %v", err)
		}

		// Replica-B's sweep: Get sees replica-A as prior holder, but
		// Acquire returns ErrHeld (replica-A still owns). No handoff.
		swB := newSweeper(sessions, []string{"acme"}, "replica-B")
		if _, err := swB.Sweep(ctx); err != nil {
			t.Fatalf("held-by-other Sweep: %v", err)
		}
		got, _ := sessions.Get(ctx, "acme", sessID)
		if got.CoordinationGeneration != 0 {
			t.Errorf("after held-by-other Sweep, CoordinationGeneration = %d, want 0 "+
				"(Acquire returned ErrHeld; no handoff observed)",
				got.CoordinationGeneration)
		}
	})
}
