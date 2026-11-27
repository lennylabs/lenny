// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth/authmw"
	"github.com/lennylabs/lenny/pkg/credential/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/idempotency"
	"github.com/lennylabs/lenny/pkg/idempotency/idempotencymw"
	"github.com/lennylabs/lenny/pkg/storage/erasure"
)

// TestIdempotencyPrincipalReadsTheAuthenticatedPrincipal checks that the
// gateway's record-scope resolver returns the tenant, subject, and bound
// session of the principal on the request context, and ignores the dev
// identity headers, so a request without a principal resolves to an empty
// scope that the middleware and the MCP hook refuse.
//
// spec: §11.5 (Idempotency)
func TestIdempotencyPrincipalReadsTheAuthenticatedPrincipal(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "globex")
	req.Header.Set("X-Lenny-User-ID", "mallory")
	if tenant, subject, session := idempotencyPrincipal(req); tenant != "" || subject != "" || session != "" {
		t.Fatalf("no principal: scope = (%q, %q, %q), want empty", tenant, subject, session)
	}

	for _, p := range []authmw.Principal{
		{TenantID: "acme", Subject: "alice"},
		{TenantID: "acme", Subject: "alice", SessionID: "s1"},
		{TenantID: "acme", SessionID: "s1"},
	} {
		r := req.WithContext(authmw.WithPrincipal(req.Context(), p))
		tenant, subject, session := idempotencyPrincipal(r)
		if tenant != p.TenantID || subject != p.Subject || session != p.SessionID {
			t.Errorf("principal %+v: scope = (%q, %q, %q)", p, tenant, subject, session)
		}
	}
}

// seedIdempotencyRecords files one record under each key in store.
func seedIdempotencyRecords(t *testing.T, store idempotencymw.Store, keys ...idempotency.Key) {
	t.Helper()
	for _, k := range keys {
		rec := idempotency.Record{Key: k, BodyHash: "h", Response: idempotency.Response{StatusCode: 201}, StoredAt: time.Now().UTC()}
		if err := store.Put(context.Background(), rec); err != nil {
			t.Fatalf("seed %+v: %v", k, err)
		}
	}
}

// TestUserErasureDeletesTheUsersIdempotencyRecordsFromTheServedStore runs
// user erasure over the gateway's user-path builders and checks that the
// erased user's records under every bound session are gone from the store
// the HTTP surface serves, that another user's records and another tenant's
// remain, and that the receipt counts the deleted records.
//
// spec: §12.8 (Compliance Interfaces)
func TestUserErasureDeletesTheUsersIdempotencyRecordsFromTheServedStore(t *testing.T) {
	w := inMemoryEraserWiring()
	store := w.idempotencyStore()
	alice := idempotency.Key{TenantID: "acme", Subject: "alice", Value: "k1"}
	aliceS1 := idempotency.Key{TenantID: "acme", Subject: "alice", SessionID: "s1", Value: "child-1"}
	bob := idempotency.Key{TenantID: "acme", Subject: "bob", Value: "k1"}
	globexAlice := idempotency.Key{TenantID: "globex", Subject: "alice", Value: "k1"}
	seedIdempotencyRecords(t, store, alice, aliceS1, bob, globexAlice)

	cfg := erasure.Config{
		Sessions:      func(context.Context, string, string) ([]string, error) { return nil, nil },
		SessionScoped: w.buildSessionScopedErasers(),
		UserScoped:    w.buildUserErasers(nil, nil, credentialpoolstore.NewMemory(), nil),
	}
	if err := erasure.ValidateOrder(cfg); err != nil {
		t.Fatalf("ValidateOrder: %v", err)
	}
	res, err := erasure.New(cfg).DeleteByUser(context.Background(), "acme", "alice")
	if err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	if got := res.Deleted[idempotencyRecordName]; got != 2 {
		t.Errorf("receipt counts %d idempotency records, want 2", got)
	}
	ctx := context.Background()
	for _, gone := range []idempotency.Key{alice, aliceS1} {
		if _, found, _ := store.Get(ctx, gone); found {
			t.Errorf("record %+v survived the erasure", gone)
		}
	}
	for _, kept := range []idempotency.Key{bob, globexAlice} {
		if _, found, _ := store.Get(ctx, kept); !found {
			t.Errorf("record %+v was deleted, want it kept", kept)
		}
	}
}

// TestTenantDeletionDeletesTheTenantsIdempotencyRecords runs the gateway's
// Phase 4 idempotency entry and checks that it deletes the tenant's
// records and leaves another tenant's.
//
// spec: §12.8 (Compliance Interfaces)
func TestTenantDeletionDeletesTheTenantsIdempotencyRecords(t *testing.T) {
	w := inMemoryEraserWiring()
	store := w.idempotencyStore()
	acme := []idempotency.Key{
		{TenantID: "acme", Subject: "alice", Value: "k1"},
		{TenantID: "acme", Subject: "bob", SessionID: "s1", Value: "k1"},
	}
	globex := idempotency.Key{TenantID: "globex", Subject: "carol", Value: "k1"}
	seedIdempotencyRecords(t, store, append(acme, globex)...)

	var entry namedTenantEraser
	for _, e := range w.buildTenantErasers(nil, nil, credentialpoolstore.NewMemory(), nil) {
		if e.name == idempotencyRecordName {
			entry = e
		}
	}
	if entry.fn == nil {
		t.Fatalf("Phase 4 has no %s entry with a function", idempotencyRecordName)
	}
	n, err := entry.fn(context.Background(), "acme")
	if err != nil || n != len(acme) {
		t.Fatalf("Phase 4 entry deleted %d (err %v), want %d", n, err, len(acme))
	}
	for _, k := range acme {
		if _, found, _ := store.Get(context.Background(), k); found {
			t.Errorf("record %+v survived tenant deletion", k)
		}
	}
	if _, found, _ := store.Get(context.Background(), globex); !found {
		t.Error("tenant deletion of acme deleted globex's record")
	}
}

// TestTheHTTPSurfaceAndTheErasersShareOneIdempotencyStore checks that the
// store is built once, so the erasers delete from the store the
// middleware and the MCP hook write.
//
// spec: §12.8 (Compliance Interfaces)
func TestTheHTTPSurfaceAndTheErasersShareOneIdempotencyStore(t *testing.T) {
	w := &gatewayWiring{}
	first := w.idempotencyStore()
	if first == nil || w.idempotencyStore() != first {
		t.Fatal("idempotencyStore built a second store")
	}
	if _, ok := first.(*idempotencymw.MemoryStore); !ok {
		t.Errorf("without Postgres the store is %T, want *idempotencymw.MemoryStore", first)
	}
}
