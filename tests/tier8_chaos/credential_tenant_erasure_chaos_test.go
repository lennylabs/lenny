// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos test for the tenant-deletion Phase 4 step that erases a
// tenant's credential pools and then its Lenny-written credential Secrets.
// Each case injects a failure into one half of the step, asserts the step
// fails so the deletion job stays at Phase 4, and asserts the retry
// converges: a Postgres backend killed during the row deletion leaves the
// rows and issues no delete-collection, and a gateway lost between the row
// deletion and the delete-collection leaves an orphan, plus any Secret a
// late-applied Put recreated, for the retried delete-collection to remove.
//
// spec: §12.8 (Compliance Interfaces), §4.9 (Credential Storage Topology:
// Credential values submitted through the admin API rule 5).

package tier8_chaos_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	credentialpoolpg "github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/credsecretfake"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

const (
	erasureChaosTenant = "acme-erase-chaos"
	erasureChaosTwin   = "globex-erase-chaos"
)

func erasureChaosSecret(tenant string) string {
	return credential.ManagedSecretName(tenant, "p1", "key-1")
}

// newErasureChaosRig starts a migrated Postgres holding one pool with a
// Lenny-written reference for each of the two tenants, and a namespace
// holding each tenant's Secret.
func newErasureChaosRig(t *testing.T) (*pgxpool.Pool, *credentialpoolpg.Store, *credsecretfake.Writer) {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	ctx := context.Background()
	store := credentialpoolpg.New(pg.Pool)
	ns := credsecretfake.New()
	for _, tenant := range []string{erasureChaosTenant, erasureChaosTwin} {
		if _, err := pg.Pool.Exec(ctx, `INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x01')`, tenant); err != nil {
			t.Fatalf("seed tenant: %v", err)
		}
		if err := store.Create(ctx, credentialpoolstore.CredentialPool{
			TenantID: tenant, Name: "p1", Provider: "anthropic_direct",
			Credentials: []credentialpoolstore.Credential{{
				ID: "key-1", SecretRef: credential.CanonicalSecretRef(residueNS, erasureChaosSecret(tenant)),
			}},
		}); err != nil {
			t.Fatalf("seed pool: %v", err)
		}
		ns.Seed(erasureChaosSecret(tenant), tenant)
	}
	return pg.Pool, store, ns
}

func erasureChaosRows(t *testing.T, store credentialpoolstore.Store, tenant string) int {
	t.Helper()
	rows, err := store.List(context.Background(), tenant, credentialpoolstore.ListFilter{IncludeDeleted: true})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return len(rows)
}

// killBlockedDelete holds an exclusive lock on credential_pools until the
// row deletion of the erasure blocks on it, then terminates that backend
// and releases the lock.
func killBlockedDelete(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (release func()) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin lock tx: %v", err)
	}
	if _, err := tx.Exec(ctx, `LOCK TABLE credential_pools IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatalf("lock credential_pools: %v", err)
	}
	return func() {
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			var killed int
			if err := pool.QueryRow(ctx, `SELECT count(pg_terminate_backend(pid)) FROM pg_stat_activity
				WHERE datname = current_database() AND wait_event_type = 'Lock'
				AND query ILIKE 'DELETE FROM credential_pools%'`).Scan(&killed); err != nil {
				t.Errorf("terminate blocked delete: %v", err)
				break
			}
			if killed > 0 {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		_ = tx.Rollback(ctx)
	}
}

// TestCredentialTenantErasureRecoversFromInjectedFaults_spec_12_8 runs the
// Phase 4 credential step under a killed Postgres backend and under a lost
// gateway, then retries it.
//
// diagnosis: a failure means a killed row deletion was followed by a
// delete-collection or reported success, a step lost before its
// delete-collection reported success so Phase 4 advanced with the tenant's
// Secrets in place, the retried delete-collection did not remove an orphan
// or a Secret a late Put recreated, or the erasure reached the twin tenant.
// spec: §12.8 (Compliance Interfaces), §4.9 (Credential Storage Topology:
// Credential values submitted through the admin API rule 5)
func TestCredentialTenantErasureRecoversFromInjectedFaults_spec_12_8(t *testing.T) {
	t.Run("backend killed during the row deletion", func(t *testing.T) {
		ctx := context.Background()
		pool, store, ns := newErasureChaosRig(t)
		erase := admin.TenantCredentialPoolEraser(store, ns, residueNS)

		release := killBlockedDelete(t, ctx, pool)
		done := make(chan error, 1)
		go func() {
			_, err := erase(ctx, erasureChaosTenant)
			done <- err
		}()
		release()
		if err := <-done; err == nil {
			t.Fatal("the step reported success after its row deletion backend was killed")
		}
		if calls := ns.Calls(); len(calls) != 0 {
			t.Fatalf("writer calls = %v, want none after a failed row deletion", calls)
		}
		if erasureChaosRows(t, store, erasureChaosTenant) != 1 {
			t.Fatal("the killed row deletion committed")
		}

		if _, err := erase(ctx, erasureChaosTenant); err != nil {
			t.Fatalf("retry: %v", err)
		}
		assertErasureConverged(t, store, ns)
	})

	t.Run("gateway lost before the delete-collection, then a late Put", func(t *testing.T) {
		ctx := context.Background()
		_, store, ns := newErasureChaosRig(t)
		erase := admin.TenantCredentialPoolEraser(store, ns, residueNS)

		lost := errors.New("gateway lost before the delete-collection reached the kube-apiserver")
		ns.BeforeDeleteTenant(func(context.Context, string) error { return lost })
		if _, err := erase(ctx, erasureChaosTenant); !errors.Is(err, lost) {
			t.Fatalf("err = %v, want the lost delete-collection", err)
		}
		if erasureChaosRows(t, store, erasureChaosTenant) != 0 {
			t.Fatal("the row deletion did not commit before the delete-collection")
		}
		if !ns.Has(erasureChaosSecret(erasureChaosTenant)) {
			t.Fatal("the lost delete-collection removed a Secret")
		}
		// A Put an earlier admin write issued, applied by the kube-apiserver
		// after the rows were gone, leaves an orphan no row names.
		late := credential.ManagedSecretName(erasureChaosTenant, "p1", "key-late")
		if err := ns.Put(ctx, late, admin.SecretOwner{TenantID: erasureChaosTenant, Pool: "p1", CredentialID: "key-late"}, map[string]string{"apiKey": "v"}); err != nil {
			t.Fatalf("late Put: %v", err)
		}

		ns.BeforeDeleteTenant(nil)
		if _, err := erase(ctx, erasureChaosTenant); err != nil {
			t.Fatalf("retry: %v", err)
		}
		if ns.Has(late) {
			t.Fatal("the retried delete-collection left the orphan a late Put created")
		}
		assertErasureConverged(t, store, ns)
	})
}

// assertErasureConverged asserts the tenant's rows and Secrets are gone and
// the twin tenant's are intact.
func assertErasureConverged(t *testing.T, store credentialpoolstore.Store, ns *credsecretfake.Writer) {
	t.Helper()
	if erasureChaosRows(t, store, erasureChaosTenant) != 0 || ns.Has(erasureChaosSecret(erasureChaosTenant)) {
		t.Fatalf("tenant rows or Secret left after convergence: Secrets %v", ns.Names())
	}
	if erasureChaosRows(t, store, erasureChaosTwin) != 1 || !ns.Has(erasureChaosSecret(erasureChaosTwin)) {
		t.Fatal("the twin tenant's row or Secret did not survive")
	}
}
