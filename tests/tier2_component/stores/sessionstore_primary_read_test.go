//go:build component

// SPDX-License-Identifier: MIT

package stores_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// spec: §12.3 (Postgres HA Requirements)
// diagnosis: a failure means the Postgres primary read writes the row it
// reads (advancing UpdatedAt or the accumulated session age), leaks another
// tenant's row, or takes the row lock, so a one-shot routing decision on the
// primary blocks behind, or is blocked by, a concurrent Update.
func TestSessionStorePrimaryReadIsLockFree_spec_12_3(t *testing.T) {
	t.Parallel()
	store, pg := startStore(t)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)
	other := freshTenant(t, ctx, pg)
	id := newUUID(t)
	if err := store.Create(ctx, sessionstore.Session{
		ID: id, TenantID: tenant, UserID: "alice", RuntimeRef: "echo", State: session.StateRunning,
		CoordinationGeneration: 3,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	before, err := store.Get(ctx, tenant, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	t.Run("returns the stored row and writes nothing", func(t *testing.T) {
		time.Sleep(5 * time.Millisecond)
		got, err := sessionstore.ReadPrimary(ctx, store, 0, tenant, id)
		if err != nil {
			t.Fatalf("ReadPrimary: %v", err)
		}
		if got.State != session.StateRunning || got.CoordinationGeneration != 3 {
			t.Errorf("row = %q gen %d, want running gen 3", got.State, got.CoordinationGeneration)
		}
		after, _ := store.Get(ctx, tenant, id)
		if !after.UpdatedAt.Equal(before.UpdatedAt) ||
			after.AccumulatedSessionAgeSeconds != before.AccumulatedSessionAgeSeconds {
			t.Errorf("row written by the read: updated %v age %v, want %v / %v",
				after.UpdatedAt, after.AccumulatedSessionAgeSeconds, before.UpdatedAt, before.AccumulatedSessionAgeSeconds)
		}
	})

	t.Run("missing and cross-tenant rows are not found", func(t *testing.T) {
		if _, err := sessionstore.ReadPrimary(ctx, store, 0, tenant, newUUID(t)); !errors.Is(err, sessionstore.ErrNotFound) {
			t.Errorf("missing row: err = %v, want ErrNotFound", err)
		}
		if _, err := sessionstore.ReadPrimary(ctx, store, 0, other, id); !errors.Is(err, sessionstore.ErrNotFound) {
			t.Errorf("other tenant's row: err = %v, want ErrNotFound", err)
		}
	})

	t.Run("does not block behind a held row lock", func(t *testing.T) {
		locked := make(chan struct{})
		release := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			_, err := store.Update(ctx, tenant, id, func(r *sessionstore.Session) error {
				close(locked)
				<-release
				return nil
			})
			done <- err
		}()
		<-locked
		begin := time.Now()
		_, err := sessionstore.ReadPrimary(ctx, store, 2*time.Second, tenant, id)
		elapsed := time.Since(begin)
		close(release)
		if uerr := <-done; uerr != nil {
			t.Fatalf("Update: %v", uerr)
		}
		if err != nil {
			t.Fatalf("ReadPrimary while the row is locked: %v", err)
		}
		if elapsed > time.Second {
			t.Errorf("ReadPrimary took %v while an Update held the row lock; it must not wait for the lock", elapsed)
		}
	})
}
