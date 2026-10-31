// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 cross-tenant isolation of the tenant-deletion Phase 4 step that
// erases a tenant's credential pools and Lenny-written credential Secrets.
// The delete-collection selects by the lenny.dev/tenant-hash label, so
// deleting one tenant must never reach a Secret another tenant owns, among
// them tenants whose ids extend or truncate the deleted tenant's id, and the
// no-writer refusal must read only the deleted tenant's pools.
//
// spec: §12.8 (Compliance Interfaces), §4.9 (Credential Storage Topology:
// Credential values submitted through the admin API rules 2, 3, and 6).

package tier9_security_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	credentialpoolpg "github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/credsecretfake"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

const erasureIsoNS = "lenny-credentials"

// TestCredentialTenantErasureNeverReachesAnotherTenant_spec_12_8 erases
// tenant acme over Postgres beside twins whose ids share its prefix and
// asserts every twin keeps its rows, its referenced Secrets, and its
// orphans, and that with no writer acme's erasure is not refused because a
// twin carries a Lenny-written reference.
//
// diagnosis: a twin Secret deleted means the delete-collection selects by a
// label value another tenant shares, or the step passes a tenant other than
// the one being deleted; a twin row deleted means the row deletion is not
// tenant-scoped; a refusal for acme with no writer means the rule 6 read
// crosses tenants.
// spec: §12.8 (Compliance Interfaces), §4.9 (Credential Storage Topology:
// Credential values submitted through the admin API rules 2, 3, and 6)
func TestCredentialTenantErasureNeverReachesAnotherTenant_spec_12_8(t *testing.T) {
	ctx := context.Background()
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: schematest.RepoRoot(t) + "/migrations"})
	store := credentialpoolpg.New(pg.Pool)
	const deleted = "acme"
	twins := []string{"acme-b", "acm", "globex"}
	ns := credsecretfake.New()

	for _, tenant := range append([]string{deleted}, twins...) {
		if _, err := pg.Pool.Exec(ctx, `INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x01')`, tenant); err != nil {
			t.Fatalf("seed tenant %s: %v", tenant, err)
		}
		ref := credential.CanonicalSecretRef(erasureIsoNS, "operator-"+tenant)
		if tenant != deleted {
			ref = credential.CanonicalSecretRef(erasureIsoNS, credential.ManagedSecretName(tenant, "p1", "key-1"))
			ns.Seed(credential.ManagedSecretName(tenant, "p1", "key-1"), tenant)
			ns.Seed(credential.ManagedSecretName(tenant, "orphan", "key-1"), tenant)
		}
		if err := store.Create(ctx, credentialpoolstore.CredentialPool{
			TenantID: tenant, Name: "p1", Provider: "anthropic_direct",
			Credentials: []credentialpoolstore.Credential{{ID: "key-1", SecretRef: ref}},
		}); err != nil {
			t.Fatalf("seed pool %s: %v", tenant, err)
		}
	}
	ns.Seed(credential.ManagedSecretName(deleted, "orphan", "key-1"), deleted)
	before := len(ns.Names())

	// With no writer, acme carries operator references only, so its rows go
	// although every twin carries a Lenny-written reference.
	if _, err := admin.TenantCredentialPoolEraser(store, nil, erasureIsoNS)(ctx, deleted); err != nil {
		t.Fatalf("no-writer erase of %s refused by another tenant's reference: %v", deleted, err)
	}
	if got := len(ns.Names()); got != before {
		t.Fatalf("no-writer erase changed the namespace: %d Secrets, want %d", got, before)
	}

	if _, err := admin.TenantCredentialPoolEraser(store, ns, erasureIsoNS)(ctx, deleted); err != nil {
		t.Fatalf("erase %s: %v", deleted, err)
	}
	if ns.Has(credential.ManagedSecretName(deleted, "orphan", "key-1")) {
		t.Fatal("the deleted tenant's orphan survived")
	}
	for _, twin := range twins {
		for _, name := range []string{credential.ManagedSecretName(twin, "p1", "key-1"), credential.ManagedSecretName(twin, "orphan", "key-1")} {
			if !ns.Has(name) {
				t.Fatalf("twin %s lost Secret %s to %s's erasure", twin, name, deleted)
			}
		}
		rows, err := store.List(ctx, twin, credentialpoolstore.ListFilter{IncludeDeleted: true})
		if err != nil || len(rows) != 1 {
			t.Fatalf("twin %s rows = (%d, %v), want 1", twin, len(rows), err)
		}
	}
	for _, call := range ns.Calls() {
		if call != "deleteTenant "+deleted {
			t.Fatalf("writer call %q, want only the deleted tenant's delete-collection", call)
		}
	}
}
