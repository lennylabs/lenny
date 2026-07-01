// SPDX-License-Identifier: MIT

package connectorcredstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorcredstore"
)

// seedCred stores one credential and fails the test on error.
func seedCred(t *testing.T, store *connectorcredstore.Memory, tenant, connector, user, env string) {
	t.Helper()
	err := store.Put(context.Background(), connectorcredstore.ConnectorCredential{
		TenantID:    tenant,
		ConnectorID: connector,
		UserID:      user,
		Environment: env,
		AccessToken: "at-" + user,
	})
	if err != nil {
		t.Fatalf("seed Put(%s/%s/%s/%s): %v", tenant, connector, user, env, err)
	}
}

// spec: §12.1 line 5 — DeleteByUser removes every credential keyed to
// (tenant, user) across connectors and environments; the TokenStore
// role is the §12.9 T4 highest-risk class the §12.8 user-erasure path
// must purge.
func TestMemoryDeleteByUser_spec_12_1(t *testing.T) {
	store := connectorcredstore.NewMemory(fixedClock(time.Unix(0, 0).UTC()))
	// alice has two connectors and two environments; bob is a control.
	seedCred(t, store, "acme", "github", "alice@acme.com", "")
	seedCred(t, store, "acme", "jira", "alice@acme.com", "staging")
	seedCred(t, store, "acme", "github", "bob@acme.com", "")

	n, err := store.DeleteByUser(context.Background(), "acme", "alice@acme.com")
	if err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	if n != 2 {
		t.Fatalf("DeleteByUser removed %d rows, want 2", n)
	}
	// bob's credential survives.
	if _, err := store.Get(context.Background(), "acme", "github", "bob@acme.com", ""); err != nil {
		t.Fatalf("bob's credential should survive alice's erasure: %v", err)
	}
	// alice's credentials are gone.
	if _, err := store.Get(context.Background(), "acme", "github", "alice@acme.com", ""); err == nil {
		t.Fatal("alice's credential should be erased")
	}
}

// spec: §12.1 line 5 — DeleteByUser is idempotent: a second call finds
// nothing and returns (0, nil).
func TestMemoryDeleteByUserIdempotent_spec_12_1(t *testing.T) {
	store := connectorcredstore.NewMemory(fixedClock(time.Unix(0, 0).UTC()))
	seedCred(t, store, "acme", "github", "alice@acme.com", "")
	if _, err := store.DeleteByUser(context.Background(), "acme", "alice@acme.com"); err != nil {
		t.Fatalf("first DeleteByUser: %v", err)
	}
	n, err := store.DeleteByUser(context.Background(), "acme", "alice@acme.com")
	if err != nil {
		t.Fatalf("second DeleteByUser: %v", err)
	}
	if n != 0 {
		t.Fatalf("second DeleteByUser removed %d rows, want 0", n)
	}
}

// spec: §12.1 line 5 — empty scope ids are rejected so a malformed
// erasure cannot silently widen to the whole store.
func TestMemoryDeleteByUserEmptyScopeRejected_spec_12_1(t *testing.T) {
	store := connectorcredstore.NewMemory(fixedClock(time.Unix(0, 0).UTC()))
	if _, err := store.DeleteByUser(context.Background(), "", "alice@acme.com"); err == nil {
		t.Fatal("DeleteByUser with empty tenant id should error")
	}
	if _, err := store.DeleteByUser(context.Background(), "acme", ""); err == nil {
		t.Fatal("DeleteByUser with empty user id should error")
	}
}

// spec: §12.1 line 5, §12.8 Phase 4 — DeleteByTenant removes every
// credential the tenant owns and leaves other tenants untouched.
func TestMemoryDeleteByTenant_spec_12_1(t *testing.T) {
	store := connectorcredstore.NewMemory(fixedClock(time.Unix(0, 0).UTC()))
	seedCred(t, store, "acme", "github", "alice@acme.com", "")
	seedCred(t, store, "acme", "jira", "bob@acme.com", "")
	seedCred(t, store, "globex", "github", "carol@globex.com", "")

	n, err := store.DeleteByTenant(context.Background(), "acme")
	if err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	if n != 2 {
		t.Fatalf("DeleteByTenant removed %d rows, want 2", n)
	}
	if _, err := store.Get(context.Background(), "globex", "github", "carol@globex.com", ""); err != nil {
		t.Fatalf("globex credential should survive acme teardown: %v", err)
	}
}

// spec: §12.1 line 5 — DeleteByTenant rejects the empty tenant id; an
// unscoped tenant deletion must never run.
func TestMemoryDeleteByTenantEmptyRejected_spec_12_1(t *testing.T) {
	store := connectorcredstore.NewMemory(fixedClock(time.Unix(0, 0).UTC()))
	if _, err := store.DeleteByTenant(context.Background(), ""); err == nil {
		t.Fatal("DeleteByTenant with empty tenant id should error")
	}
}
