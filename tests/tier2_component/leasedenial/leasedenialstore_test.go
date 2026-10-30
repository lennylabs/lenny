//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §8.6 Postgres-backed
// leasecontrol.DenialStore (pkg/gateway/leasecontrol/denialpg) against a
// real container with the production migrations applied. Covers the
// durable Deny/Denied round-trip, the database-clock cool-off comparison
// (line 733), the in-flight atomic re-check that gates a Grant on a
// denied tree (line 732), the grant-counter increment on a not-denied
// tree, Clear, and cross-tenant isolation. F-8.6.5.
//
// This lives in its own package rather than tests/tier2_component/stores
// so it does not share that package's compilation unit.
package leasedenial_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol/denialpg"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// startDenialStore brings up a Postgres container with the production
// migrations and returns the denialpg store plus the raw handle.
func startDenialStore(t *testing.T) (*denialpg.Store, *containers.Postgres) {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	return denialpg.New(pg.Pool), pg
}

// freshTenant inserts a uniquely-named tenant and returns its id so each
// sub-test operates on an isolated tenant (the table FKs tenants(id)).
func freshTenant(t *testing.T, ctx context.Context, pg *containers.Postgres) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	// The §12.3 lenny_tenant_guard format check restricts ids to
	// [A-Za-z0-9_-]{1,128}; hex keeps the generated id inside it.
	id := "t-" + hex.EncodeToString(b[:])
	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, $2)`, id, []byte{0x01}); err != nil {
		t.Fatalf("seed tenant %q: %v", id, err)
	}
	return id
}

// extTokens reads the §8.6 token grant counter for a tree under
// its tenant's RLS context, so the test can prove a Grant did or did not
// increment it.
func extTokens(t *testing.T, ctx context.Context, pg *containers.Postgres, tenant, root string) int64 {
	t.Helper()
	tx, err := pg.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL app.current_tenant = '"+tenant+"'"); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	var tokens int64
	if err := tx.QueryRow(ctx,
		`SELECT ext_tokens FROM delegation_tree_budget WHERE tenant_id = $1 AND root_session_id = $2`,
		tenant, root).Scan(&tokens); err != nil {
		if err == pgx.ErrNoRows {
			return 0
		}
		t.Fatalf("read ext_tokens: %v", err)
	}
	return tokens
}

// noLimit is a clamp limit no test grant reaches.
var noLimit = leasecontrol.Dimensions{Tokens: 1 << 40, Seconds: 1 << 40}

// spec: §8.6 — the delegation-tree denial store records a
// Deny with a cool-off expiry, round-trips Denied, reports not-denied
// for an untouched tree, and isolates denials per tenant and root.
// diagnosis: a failure means the lease-denial store mis-records the
// cool-off window, leaks a denial across tenants or roots, or fails to
// fail-closed (reporting not-denied when a denial is in force).
func TestLeaseDenialStoreContract(t *testing.T) {
	t.Parallel()
	store, pg := startDenialStore(t)
	ctx := context.Background()

	t.Run("deny then denied round-trip", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		if err := store.Deny(ctx, tenant, "root-a", 300*time.Second); err != nil {
			t.Fatalf("Deny: %v", err)
		}
		denied, expiry, err := store.Denied(ctx, tenant, "root-a")
		if err != nil {
			t.Fatalf("Denied: %v", err)
		}
		if !denied {
			t.Fatalf("tree should be denied after Deny")
		}
		if expiry.Before(time.Now().Add(4 * time.Minute)) {
			t.Fatalf("cool-off expiry %v should be ~5m out", expiry)
		}
	})

	t.Run("no row reports not denied", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		denied, expiry, err := store.Denied(ctx, tenant, "never-touched")
		if err != nil {
			t.Fatalf("Denied: %v", err)
		}
		if denied || !expiry.IsZero() {
			t.Fatalf("absent row should be not-denied/zero, got denied=%v expiry=%v", denied, expiry)
		}
	})

	// spec: §8.6 — the cool-off comparison uses the database
	// clock, so an already-expired cool-off reports not denied even
	// though extension_denied is still set on the row.
	t.Run("expired cool-off reports not denied via db clock", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		if err := store.Deny(ctx, tenant, "root-exp", 0); err != nil {
			t.Fatalf("Deny: %v", err)
		}
		denied, _, err := store.Denied(ctx, tenant, "root-exp")
		if err != nil {
			t.Fatalf("Denied: %v", err)
		}
		if denied {
			t.Fatalf("a zero cool-off expires immediately; should not be denied")
		}
	})

	// spec: §8.6 — Grant on a not-denied tree increments the
	// durable counters and commits.
	t.Run("grant increments counters on not-denied tree", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		if _, err := store.Grant(ctx, tenant, "root-g", leasecontrol.Dimensions{Tokens: 50_000, Seconds: 60}, noLimit); err != nil {
			t.Fatalf("Grant: %v", err)
		}
		if got := extTokens(t, ctx, pg, tenant, "root-g"); got != 50_000 {
			t.Fatalf("ext_tokens = %d, want 50000", got)
		}
		// A second grant accumulates.
		if _, err := store.Grant(ctx, tenant, "root-g", leasecontrol.Dimensions{Tokens: 25_000}, noLimit); err != nil {
			t.Fatalf("Grant 2: %v", err)
		}
		if got := extTokens(t, ctx, pg, tenant, "root-g"); got != 75_000 {
			t.Fatalf("ext_tokens after 2 grants = %d, want 75000", got)
		}
	})

	// spec: §8.6 — the in-flight atomic re-check: a Grant against
	// a denied-and-in-cool-off tree returns ErrExtensionDenied and does
	// NOT increment the counters.
	t.Run("grant on denied tree returns ErrExtensionDenied and does not increment", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		if err := store.Deny(ctx, tenant, "root-d", 300*time.Second); err != nil {
			t.Fatalf("Deny: %v", err)
		}
		_, err := store.Grant(ctx, tenant, "root-d", leasecontrol.Dimensions{Tokens: 10_000}, noLimit)
		if err != leasecontrol.ErrExtensionDenied {
			t.Fatalf("Grant on denied tree err = %v, want ErrExtensionDenied", err)
		}
		if got := extTokens(t, ctx, pg, tenant, "root-d"); got != 0 {
			t.Fatalf("denied grant incremented counters: ext_tokens = %d, want 0", got)
		}
	})

	// spec: §8.6 — Clear lifts the denial so a subsequent Grant
	// proceeds.
	t.Run("clear lifts the denial", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		if err := store.Deny(ctx, tenant, "root-c", 300*time.Second); err != nil {
			t.Fatalf("Deny: %v", err)
		}
		if err := store.Clear(ctx, tenant, "root-c"); err != nil {
			t.Fatalf("Clear: %v", err)
		}
		denied, _, err := store.Denied(ctx, tenant, "root-c")
		if err != nil {
			t.Fatalf("Denied: %v", err)
		}
		if denied {
			t.Fatalf("tree should not be denied after Clear")
		}
		if _, err := store.Grant(ctx, tenant, "root-c", leasecontrol.Dimensions{Tokens: 1_000}, noLimit); err != nil {
			t.Fatalf("Grant after Clear: %v", err)
		}
	})

	// spec: §8.6 (Extension state) — Grant clamps the request at the
	// remaining headroom under the row lock and returns the applied grant
	// and the new cumulative, and a grant at no headroom applies zero.
	t.Run("grant clamps at the remaining headroom", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		limit := leasecontrol.Dimensions{Tokens: 100_000}
		res, err := store.Grant(ctx, tenant, "root-h", leasecontrol.Dimensions{Tokens: 70_000}, limit)
		if err != nil {
			t.Fatalf("Grant 1: %v", err)
		}
		if res.Applied.Tokens != 70_000 || res.Total.Tokens != 70_000 {
			t.Fatalf("grant 1 = %+v, want 70000 applied and total", res)
		}
		res, err = store.Grant(ctx, tenant, "root-h", leasecontrol.Dimensions{Tokens: 70_000}, limit)
		if err != nil {
			t.Fatalf("Grant 2: %v", err)
		}
		if res.Applied.Tokens != 30_000 || res.Total.Tokens != 100_000 {
			t.Fatalf("grant 2 = %+v, want 30000 applied and a 100000 total", res)
		}
		res, err = store.Grant(ctx, tenant, "root-h", leasecontrol.Dimensions{Tokens: 1}, limit)
		if err != nil {
			t.Fatalf("Grant 3: %v", err)
		}
		if res.Applied.Tokens != 0 {
			t.Fatalf("grant at no headroom applied %d, want 0", res.Applied.Tokens)
		}
		if got := extTokens(t, ctx, pg, tenant, "root-h"); got != 100_000 {
			t.Fatalf("ext_tokens = %d, want the 100000 limit", got)
		}
	})

	// spec: §8.6 (Extension state) — concurrent grants serialize on the
	// row lock, so together they never take the cumulative grant past the
	// limit and their applied amounts sum to it.
	t.Run("concurrent grants stay within the limit", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		limit := leasecontrol.Dimensions{Tokens: 50_000}
		var wg sync.WaitGroup
		var mu sync.Mutex
		var sum int64
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				res, err := store.Grant(ctx, tenant, "root-cc", leasecontrol.Dimensions{Tokens: 20_000}, limit)
				if err != nil {
					t.Errorf("Grant: %v", err)
					return
				}
				mu.Lock()
				sum += res.Applied.Tokens
				mu.Unlock()
			}()
		}
		wg.Wait()
		if got := extTokens(t, ctx, pg, tenant, "root-cc"); got != 50_000 || sum != 50_000 {
			t.Fatalf("ext_tokens = %d, applied sum = %d, want both 50000", got, sum)
		}
	})

	// spec: §8.6 (Extension state) — Grants reads the cumulative ext_*
	// columns, and zero for a tree with no row.
	t.Run("grants reads the cumulative and zero for a missing row", func(t *testing.T) {
		tenant := freshTenant(t, ctx, pg)
		got, err := store.Grants(ctx, tenant, "root-none")
		if err != nil || got != (leasecontrol.Dimensions{}) {
			t.Fatalf("Grants(missing) = %+v, %v, want zero", got, err)
		}
		if _, err := store.Grant(ctx, tenant, "root-gr", leasecontrol.Dimensions{Tokens: 5, Seconds: 7}, noLimit); err != nil {
			t.Fatalf("Grant: %v", err)
		}
		if _, err := store.Grant(ctx, tenant, "root-gr", leasecontrol.Dimensions{Tokens: 3}, noLimit); err != nil {
			t.Fatalf("Grant: %v", err)
		}
		got, err = store.Grants(ctx, tenant, "root-gr")
		if err != nil || got.Tokens != 8 || got.Seconds != 7 {
			t.Fatalf("Grants = %+v, %v, want 8 tokens and 7 seconds", got, err)
		}
	})

	// Cross-tenant isolation: a denial on tenant A's tree is invisible to
	// tenant B reading the same root id.
	t.Run("cross-tenant isolation", func(t *testing.T) {
		tenantA := freshTenant(t, ctx, pg)
		tenantB := freshTenant(t, ctx, pg)
		if err := store.Deny(ctx, tenantA, "shared-root", 300*time.Second); err != nil {
			t.Fatalf("Deny A: %v", err)
		}
		denied, _, err := store.Denied(ctx, tenantB, "shared-root")
		if err != nil {
			t.Fatalf("Denied B: %v", err)
		}
		if denied {
			t.Fatalf("tenant B must not see tenant A's denial")
		}
	})
}
