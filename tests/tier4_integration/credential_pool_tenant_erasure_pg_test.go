// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test for the tenant-deletion Phase 4 step that erases
// a tenant's credential pools and Lenny-written credential Secrets over the
// Postgres-backed CredentialPoolStore. The step deletes the pool rows, then
// issues one delete-collection by the tenant-hash label; with no writer it
// reads every pool of the tenant, soft-deleted ones included, and fails
// while one carries a Lenny-written reference. The soft-deleted read is the
// pgstore List deleted_at filter, which the in-memory store does not share.
//
// spec: §12.8 (Compliance Interfaces), §4.9 (Credential Storage Topology:
// Credential values submitted through the admin API rules 3, 5, and 6).

package tier4_integration_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	credentialpoolpg "github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/tests/testinfra/credsecretfake"
)

// erasurePGRig is a migrated database holding two tenants' pools.
type erasurePGRig struct {
	store *credentialpoolpg.Store
}

func newErasurePGRig(t *testing.T, tenants ...string) *erasurePGRig {
	t.Helper()
	pg := sharedDatabase(t)
	seedErasureTenants(t, pg.Pool, tenants...)
	return &erasurePGRig{store: credentialpoolpg.New(pg.Pool)}
}

func seedErasureTenants(t *testing.T, pool *pgxpool.Pool, tenants ...string) {
	t.Helper()
	for _, id := range tenants {
		if _, err := pool.Exec(context.Background(), `INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x01')`, id); err != nil {
			t.Fatalf("seed tenant %s: %v", id, err)
		}
	}
}

func erasureRef(tenant, pool string) string {
	return credential.CanonicalSecretRef(pgSecretNS, credential.ManagedSecretName(tenant, pool, "key-1"))
}

// seed creates pool name for tenant with one credential carrying ref, and
// soft-deletes it when deleted is set.
func (r *erasurePGRig) seed(t *testing.T, tenant, name, ref string, deleted bool) {
	t.Helper()
	ctx := context.Background()
	if err := r.store.Create(ctx, credentialpoolstore.CredentialPool{
		TenantID: tenant, Name: name, Provider: "anthropic_direct",
		Credentials: []credentialpoolstore.Credential{{ID: "key-1", SecretRef: ref}},
	}); err != nil {
		t.Fatalf("seed pool %s/%s: %v", tenant, name, err)
	}
	if deleted {
		if err := r.store.SoftDelete(ctx, tenant, name, time.Now()); err != nil {
			t.Fatalf("soft-delete %s/%s: %v", tenant, name, err)
		}
	}
}

func (r *erasurePGRig) rows(t *testing.T, tenant string) int {
	t.Helper()
	rows, err := r.store.List(context.Background(), tenant, credentialpoolstore.ListFilter{IncludeDeleted: true})
	if err != nil {
		t.Fatalf("List %s: %v", tenant, err)
	}
	return len(rows)
}

// TestCredentialPoolTenantErasurePostgres pins the Phase 4 credential step
// on Postgres: the delete-collection follows the committed row deletion and
// reaches an orphan, another tenant is untouched, a failed delete-collection
// fails the step and its retry completes, and with no writer a soft-deleted
// pool's Lenny-written reference fails the step with the rows kept.
//
// diagnosis: a failure means the step issues the delete-collection before
// the Postgres row deletion commits, deletes Secrets by stored reference
// rather than by tenant label, reaches another tenant, swallows a failed
// delete-collection, or, with no writer, misses a soft-deleted pool's
// Lenny-written reference and deletes the only rows that name the Secret.
// spec: §12.8 (Compliance Interfaces), §4.9 (Credential Storage Topology:
// Credential values submitted through the admin API rules 3, 5, and 6)
func TestCredentialPoolTenantErasurePostgres(t *testing.T) {
	ctx := context.Background()
	const acme, globex = "acme-erase", "globex-erase"

	t.Run("writer wired", func(t *testing.T) {
		r := newErasurePGRig(t, acme, globex)
		r.seed(t, acme, "p1", erasureRef(acme, "p1"), false)
		r.seed(t, acme, "p2", erasureRef(acme, "p2"), true)
		r.seed(t, globex, "p1", erasureRef(globex, "p1"), false)

		ns := credsecretfake.New()
		orphan := credential.ManagedSecretName(acme, "gone", "key-1")
		for _, s := range [][2]string{
			{credential.ManagedSecretName(acme, "p1", "key-1"), acme},
			{credential.ManagedSecretName(acme, "p2", "key-1"), acme},
			{orphan, acme},
			{credential.ManagedSecretName(globex, "p1", "key-1"), globex},
			{"operator-made", ""},
		} {
			ns.Seed(s[0], s[1])
		}
		apiDown := errors.New("kube-apiserver unavailable")
		var rowsSeen []int
		fail := true
		ns.BeforeDeleteTenant(func(context.Context, string) error {
			rowsSeen = append(rowsSeen, r.rows(t, acme))
			if fail {
				fail = false
				return apiDown
			}
			return nil
		})
		erase := admin.TenantCredentialPoolEraser(r.store, ns, pgSecretNS)

		if _, err := erase(ctx, acme); !errors.Is(err, apiDown) {
			t.Fatalf("first pass err = %v, want the delete-collection failure", err)
		}
		if !ns.Has(orphan) {
			t.Fatal("a failed delete-collection deleted a Secret")
		}
		n, err := erase(ctx, acme)
		if err != nil || n != 0 {
			t.Fatalf("retry = (%d, %v), want (0, nil): the rows went on the first pass", n, err)
		}
		if !reflect.DeepEqual(rowsSeen, []int{0, 0}) {
			t.Fatalf("committed acme rows at each delete-collection = %v, want [0 0]", rowsSeen)
		}
		want := []string{credential.ManagedSecretName(globex, "p1", "key-1"), "operator-made"}
		if got := ns.Names(); !reflect.DeepEqual(got, want) {
			t.Fatalf("Secrets left = %v, want %v", got, want)
		}
		if r.rows(t, globex) != 1 {
			t.Fatal("globex pool row did not survive acme's erasure")
		}
	})

	t.Run("no writer and a soft-deleted written reference", func(t *testing.T) {
		r := newErasurePGRig(t, acme, globex)
		r.seed(t, acme, "live", pgSecretNS+"/operator-made", false)
		r.seed(t, acme, "dead", erasureRef(acme, "dead"), true)
		r.seed(t, globex, "p1", pgSecretNS+"/operator-made", false)
		erase := admin.TenantCredentialPoolEraser(r.store, nil, pgSecretNS)

		if _, err := erase(ctx, acme); !errors.Is(err, admin.ErrLennyWrittenRefWithoutWriter) {
			t.Fatalf("err = %v, want ErrLennyWrittenRefWithoutWriter", err)
		}
		if got := r.rows(t, acme); got != 2 {
			t.Fatalf("acme rows after refusal = %d, want 2 kept", got)
		}
		n, err := erase(ctx, globex)
		if err != nil || n != 1 || r.rows(t, globex) != 0 {
			t.Fatalf("operator-only tenant erase = (%d, %v), want its row deleted", n, err)
		}
	})
}
