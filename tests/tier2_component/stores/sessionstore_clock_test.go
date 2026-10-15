//go:build component

// SPDX-License-Identifier: MIT

package stores_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
)

// spec: 6.2 (maxSessionAge timer behavior across states), 7.3 (Retry and
// Resume)
// diagnosis: the Postgres session store stamped created_at or updated_at,
// or accrued the active session age, from the wall clock while a clock was
// injected. The resume_pending window and the active age are compared
// against those stamps by the gateway's timers, so a store that ignores the
// injected clock makes a stepped-clock scenario expire windows early.
func TestSessionStoreStampsFromTheInjectedClock_spec_6_2(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)

	var mu sync.Mutex
	now := time.Date(2031, 1, 2, 3, 4, 5, 0, time.UTC)
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	store := pgstore.New(pg.Pool, pgstore.WithClock(clock))
	id := newUUID(t)
	if err := store.Create(ctx, sessionstore.Session{ID: id, TenantID: tenant, State: session.StateRunning, RuntimeRef: "echo"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := store.Get(ctx, tenant, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.CreatedAt.Equal(now) || !got.UpdatedAt.Equal(now) {
		t.Fatalf("Create stamped created_at %v and updated_at %v, want the injected %v", got.CreatedAt, got.UpdatedAt, now)
	}

	mu.Lock()
	now = now.Add(90 * time.Second)
	mu.Unlock()
	updated, err := store.Update(ctx, tenant, id, func(r *sessionstore.Session) error {
		r.State = session.StateSuspended
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if !updated.UpdatedAt.Equal(clock()) {
		t.Errorf("Update stamped updated_at %v, want the injected %v", updated.UpdatedAt, clock())
	}
	if updated.AccumulatedSessionAgeSeconds != 90 {
		t.Errorf("active age after 90 injected seconds in running = %v, want 90", updated.AccumulatedSessionAgeSeconds)
	}
}
