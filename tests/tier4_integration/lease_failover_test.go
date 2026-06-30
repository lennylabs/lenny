// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test for the §12.4 Postgres advisory-lock fallback
// behind the §12.2 LeaseStore (pkg/gateway/leasestore/pgstore) and the
// §12.1 interactionstore.DeleteByTenant erasure primitive lifted onto
// the production pgstore. Both run against a real Postgres container with
// the production migrations applied.
package tier4_integration_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/interactionstore"
	interactionpg "github.com/lennylabs/lenny/pkg/gateway/interactionstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
	leasepg "github.com/lennylabs/lenny/pkg/gateway/storage/leasestore/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: §12.4 line 206 — "Distributed session leases | Fall back to
// Postgres advisory locks (higher latency)".
// diagnosis: a failure means the Postgres advisory-lock lease fallback
// does not provide mutual exclusion, so two holders could acquire the
// same session lease when Redis is unavailable.
func TestLeasePgstoreAdvisoryLockFallback_spec_12_4(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	seedTenant(t, pg, "acme")

	store := leasepg.New(pg.Pool)
	const sess = "sess-1"
	ttl := time.Minute

	t.Run("acquire then get reports the holder", func(t *testing.T) {
		lease, err := store.Acquire(ctx, "acme", sess, "replica-1", ttl)
		if err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		if lease.Holder != "replica-1" {
			t.Fatalf("holder = %q, want replica-1", lease.Holder)
		}
		got, err := store.Get(ctx, "acme", sess)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Holder != "replica-1" {
			t.Fatalf("Get holder = %q, want replica-1", got.Holder)
		}
	})

	t.Run("a different replica cannot steal a live lease", func(t *testing.T) {
		_, err := store.Acquire(ctx, "acme", sess, "replica-2", ttl)
		if err != leasestore.ErrHeld {
			t.Fatalf("Acquire by replica-2 = %v, want ErrHeld", err)
		}
	})

	t.Run("the holder renews idempotently; a non-holder cannot", func(t *testing.T) {
		if _, err := store.Renew(ctx, "acme", sess, "replica-1", ttl); err != nil {
			t.Fatalf("Renew by holder: %v", err)
		}
		if _, err := store.Renew(ctx, "acme", sess, "replica-2", ttl); err != leasestore.ErrNotHeld {
			t.Fatalf("Renew by non-holder = %v, want ErrNotHeld", err)
		}
	})

	t.Run("release by a non-holder is a no-op; the holder releases", func(t *testing.T) {
		if err := store.Release(ctx, "acme", sess, "replica-2"); err != nil {
			t.Fatalf("Release by non-holder: %v", err)
		}
		if _, err := store.Get(ctx, "acme", sess); err != nil {
			t.Fatalf("lease must survive a non-holder release: %v", err)
		}
		if err := store.Release(ctx, "acme", sess, "replica-1"); err != nil {
			t.Fatalf("Release by holder: %v", err)
		}
		if _, err := store.Get(ctx, "acme", sess); err != leasestore.ErrNotFound {
			t.Fatalf("Get after release = %v, want ErrNotFound", err)
		}
	})

	t.Run("an expired lease is treated as absent and can be re-acquired", func(t *testing.T) {
		if _, err := store.Acquire(ctx, "acme", sess, "replica-1", ttl); err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		// Force the row's expiry into the past without going through the
		// store, so the test does not depend on wall-clock sleeps. The
		// tenant guard fires for the superuser too, so the GUC must be set.
		tx, err := pg.Pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin expire: %v", err)
		}
		if _, err := tx.Exec(ctx, "SET LOCAL app.current_tenant = 'acme'"); err != nil {
			t.Fatalf("set tenant: %v", err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE session_leases SET expires_at = now() - interval '1 hour'
			 WHERE tenant_id = 'acme' AND session_id = $1`, sess); err != nil {
			t.Fatalf("expire row: %v", err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit expire: %v", err)
		}
		// Get treats the lapsed lease as absent.
		if _, err := store.Get(ctx, "acme", sess); err != leasestore.ErrNotFound {
			t.Fatalf("Get of expired lease = %v, want ErrNotFound", err)
		}
		// A different replica may now claim it.
		lease, err := store.Acquire(ctx, "acme", sess, "replica-2", ttl)
		if err != nil {
			t.Fatalf("Acquire after expiry: %v", err)
		}
		if lease.Holder != "replica-2" {
			t.Fatalf("post-expiry holder = %q, want replica-2", lease.Holder)
		}
		_ = store.Release(ctx, "acme", sess, "replica-2")
	})

	t.Run("concurrent acquires serialize via the advisory lock", func(t *testing.T) {
		const contested = "sess-contended"
		const racers = 8
		var wg sync.WaitGroup
		results := make([]error, racers)
		wg.Add(racers)
		for i := 0; i < racers; i++ {
			go func(i int) {
				defer wg.Done()
				holder := "racer-" + string(rune('a'+i))
				_, err := store.Acquire(ctx, "acme", contested, holder, ttl)
				results[i] = err
			}(i)
		}
		wg.Wait()
		wins, held := 0, 0
		for _, err := range results {
			switch err {
			case nil:
				wins++
			case leasestore.ErrHeld:
				held++
			default:
				t.Fatalf("unexpected concurrent Acquire error: %v", err)
			}
		}
		if wins != 1 {
			t.Fatalf("%d racers won the lease, want exactly 1 (advisory lock did not serialize)", wins)
		}
		if held != racers-1 {
			t.Fatalf("%d racers saw ErrHeld, want %d", held, racers-1)
		}
	})

	t.Run("DeleteByTenant clears every lease; DeleteByUser is a guarded no-op", func(t *testing.T) {
		for _, s := range []string{"d1", "d2", "d3"} {
			if _, err := store.Acquire(ctx, "acme", s, "replica-1", ttl); err != nil {
				t.Fatalf("Acquire %s: %v", s, err)
			}
		}
		if n, err := store.DeleteByUser(ctx, "acme", "alice"); err != nil || n != 0 {
			t.Fatalf("DeleteByUser = (%d, %v), want (0, nil) — leases are session-keyed", n, err)
		}
		n, err := store.DeleteByTenant(ctx, "acme")
		if err != nil {
			t.Fatalf("DeleteByTenant: %v", err)
		}
		if n < 3 {
			t.Fatalf("DeleteByTenant removed %d, want at least the 3 seeded leases", n)
		}
		if _, err := store.DeleteByUser(ctx, "", "alice"); err != leasestore.ErrEmptyScope {
			t.Fatalf("DeleteByUser with empty tenant = %v, want ErrEmptyScope", err)
		}
	})
}

// spec: §12.1 line 5 / §12.8 Phase 4 — interactionstore.DeleteByTenant is
// mandatory on the production interface and erases one tenant's rows.
// diagnosis: a failure means interactionstore.DeleteByTenant does not
// erase a tenant's rows, breaching the §12.1 mandatory tenant-erasure
// primitive.
func TestInteractionPgstoreDeleteByTenant_spec_12_1(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	seedTenant(t, pg, "acme")
	seedTenant(t, pg, "globex")

	store := interactionpg.New(pg.Pool)
	put := func(tenant, id, user string) {
		t.Helper()
		if err := store.Put(ctx, interactionstore.Interaction{
			ID: id, Kind: interactionstore.KindToolUse, SessionID: "sess-" + id,
			TenantID: tenant, UserID: user,
		}); err != nil {
			t.Fatalf("Put %s/%s: %v", tenant, id, err)
		}
	}
	put("acme", "a1", "alice")
	put("acme", "a2", "bob")
	put("globex", "g1", "carol")

	n, err := store.DeleteByTenant(ctx, "acme")
	if err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	if n != 2 {
		t.Fatalf("DeleteByTenant removed %d, want 2", n)
	}
	// globex's row survives.
	if _, err := store.Get(ctx, "globex", "sess-g1", "carol", "g1"); err != nil {
		t.Fatalf("globex interaction must survive acme deletion: %v", err)
	}
	// Idempotent.
	if n, err := store.DeleteByTenant(ctx, "acme"); err != nil || n != 0 {
		t.Fatalf("repeat DeleteByTenant = (%d, %v), want (0, nil)", n, err)
	}
}

// seedTenant inserts a tenant registry row (no guard trigger on the
// platform-global tenants table, so no SET LOCAL is needed).
func seedTenant(t *testing.T, pg *containers.Postgres, id string) {
	t.Helper()
	if _, err := pg.Pool.Exec(context.Background(),
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00')
		 ON CONFLICT (id) DO NOTHING`, id); err != nil {
		t.Fatalf("seed tenant %q: %v", id, err)
	}
}
