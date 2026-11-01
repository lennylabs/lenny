// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test for the Lenny-written credential Secret path of
// the admin credential-pool writes over the Postgres-backed
// CredentialPoolStore. The pool DELETE runs its If-Match compare, its
// soft-deleted guard, the soft delete, and the Delete of each Lenny-written
// Secret as one operation under the pool row lock, and the store's Update
// read does not filter deleted_at, so the credential addition's
// soft-deleted guard is the only thing keeping a write off a deleted pool.
// Both are properties of the real row lock, which the in-memory store's
// process mutex does not exercise.
//
// spec: §4.9 (Credential Storage Topology: Credential values submitted
// through the admin API rules 3, 4, and 5), §15.1.

package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	credentialpoolpg "github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

const pgSecretNS = "lenny-credentials"

// pgSecretWriter is a fault-injecting credential Secret writer. onDelete
// runs inside each Delete, while the admin write holds the pool row lock.
type pgSecretWriter struct {
	mu       sync.Mutex
	puts     []string
	deletes  []string
	failDel  error
	onDelete func()
}

func (w *pgSecretWriter) Put(_ context.Context, name string, _ admin.SecretOwner, _ map[string]string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.puts = append(w.puts, name)
	return nil
}

func (w *pgSecretWriter) Delete(_ context.Context, name string) error {
	if w.onDelete != nil {
		w.onDelete()
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.deletes = append(w.deletes, name)
	return w.failDel
}

func (w *pgSecretWriter) DeleteTenant(context.Context, string) error { return nil }

type pgSecretRig struct {
	store  *credentialpoolpg.Store
	writer *pgSecretWriter
	h      http.Handler
	tenant string
}

func newPGSecretRig(t *testing.T) *pgSecretRig {
	t.Helper()
	pg := sharedDatabase(t)
	ctx := context.Background()
	const tenant = "acme-secretwrite"
	if _, err := pg.Pool.Exec(ctx, `INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x01')`, tenant); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	store := credentialpoolpg.New(pg.Pool)
	if err := store.Create(ctx, credentialpoolstore.CredentialPool{
		TenantID: tenant, Name: "p1", Provider: "anthropic_direct",
		Credentials: []credentialpoolstore.Credential{
			{ID: "key-1", SecretRef: pgSecretNS + "/operator-made"},
			{ID: "key-2", SecretRef: credential.CanonicalSecretRef(pgSecretNS, credential.ManagedSecretName(tenant, "p1", "key-2"))},
		},
	}); err != nil {
		t.Fatalf("seed pool: %v", err)
	}
	writer := &pgSecretWriter{}
	h := admin.NewRouter(tenantstore.NewMemory(), admin.Options{CredentialsNamespace: pgSecretNS}).
		WithCredentialPools(store).WithCredentialSecretWriter(writer).Handler()
	return &pgSecretRig{store: store, writer: writer, h: h, tenant: tenant}
}

func (r *pgSecretRig) send(t *testing.T, method, path string, body any, ifMatch string) *httptest.ResponseRecorder {
	t.Helper()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(buf))
	req = req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		TenantID: r.tenant, Subject: "alice@acme.com", Roles: []pkgauth.Role{pkgauth.RoleTenantAdmin},
	}))
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	rr := httptest.NewRecorder()
	r.h.ServeHTTP(rr, req)
	return rr
}

func (r *pgSecretRig) row(t *testing.T) credentialpoolstore.CredentialPool {
	t.Helper()
	p, err := r.store.Get(context.Background(), r.tenant, "p1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	return p
}

// TestCredentialPoolDeleteUnderRowLockPostgres pins the pool DELETE on
// Postgres: it soft-deletes the pool and Deletes its Lenny-written Secret
// while holding the row lock, so a concurrent row write blocks for the
// whole Secret operation; a repeat DELETE is 204 with no Secret operation
// and no row change; and a failed Delete leaves the pool active.
//
// diagnosis: a failure means the Postgres pool DELETE performs its Secret
// Delete outside the row lock (a concurrent write can add a Lenny-written
// ref that the Delete misses), re-runs the deletion on a pool that is
// already deleted, or commits the soft delete after its Secret Delete
// failed.
// spec: §4.9 (Credential Storage Topology: Credential values submitted
// through the admin API rules 3, 4, and 5), §15.1
func TestCredentialPoolDeleteUnderRowLockPostgres(t *testing.T) {
	t.Run("delete holds the row lock", func(t *testing.T) {
		rig := newPGSecretRig(t)
		var concurrentErr error
		rig.writer.onDelete = func() {
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			_, concurrentErr = rig.store.Update(ctx, rig.tenant, "p1", func(p *credentialpoolstore.CredentialPool) error {
				p.MaxConcurrentSessions = 99
				return nil
			})
		}
		rr := rig.send(t, http.MethodDelete, "/v1/admin/credential-pools/p1?tenantId="+rig.tenant, nil, "")
		if rr.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204; body %s", rr.Code, rr.Body.String())
		}
		if concurrentErr == nil {
			t.Fatal("a concurrent row write committed while the DELETE ran its Secret operation")
		}
		row := rig.row(t)
		if row.IsActive() || row.MaxConcurrentSessions == 99 {
			t.Fatalf("row = %+v, want soft-deleted and untouched by the blocked write", row)
		}
		want := credential.ManagedSecretName(rig.tenant, "p1", "key-2")
		if len(rig.writer.deletes) != 1 || rig.writer.deletes[0] != want {
			t.Fatalf("deletes = %v, want [%s]", rig.writer.deletes, want)
		}

		rig.writer.onDelete = nil
		before := rig.row(t)
		rr = rig.send(t, http.MethodDelete, "/v1/admin/credential-pools/p1?tenantId="+rig.tenant, nil, "")
		if rr.Code != http.StatusNoContent {
			t.Fatalf("repeat status = %d, want 204", rr.Code)
		}
		if after := rig.row(t); after.Version != before.Version || len(rig.writer.deletes) != 1 {
			t.Fatalf("a repeat DELETE changed the row or deleted again: version %d -> %d, deletes %v", before.Version, after.Version, rig.writer.deletes)
		}
	})
	t.Run("stale If-Match", func(t *testing.T) {
		rig := newPGSecretRig(t)
		rr := rig.send(t, http.MethodDelete, "/v1/admin/credential-pools/p1?tenantId="+rig.tenant, nil, `"9"`)
		if rr.Code != http.StatusPreconditionFailed || len(rig.writer.deletes) != 0 || !rig.row(t).IsActive() {
			t.Fatalf("status = %d deletes = %v, want 412, none, and an active pool", rr.Code, rig.writer.deletes)
		}
	})
	t.Run("failed Delete leaves the pool active", func(t *testing.T) {
		rig := newPGSecretRig(t)
		rig.writer.failDel = errors.New("apiserver unavailable")
		before := rig.row(t)
		rr := rig.send(t, http.MethodDelete, "/v1/admin/credential-pools/p1?tenantId="+rig.tenant, nil, "")
		if rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", rr.Code)
		}
		if after := rig.row(t); !after.IsActive() || after.Version != before.Version {
			t.Fatalf("row = %+v, want unchanged and active", after)
		}
	})
}

// TestCredentialAddOnSoftDeletedPoolPostgres pins the credential addition
// row guard on Postgres, whose Update read selects soft-deleted rows: a
// secretData addition to a soft-deleted pool is 404 with no Put and no row
// change, and the same addition to an active pool Puts the Secret and
// commits its reference.
//
// diagnosis: a failure means a credential value can be written into a
// soft-deleted pool on Postgres (a Secret no live pool uses, and a
// reference the deleted row carries), or the active-pool addition no longer
// writes its Secret and reference.
// spec: §4.9 (Credential Storage Topology: Credential values submitted
// through the admin API rules 2 and 4)
func TestCredentialAddOnSoftDeletedPoolPostgres(t *testing.T) {
	body := map[string]any{"id": "key-3", "secretData": map[string]string{"apiKey": "v"}}
	t.Run("soft-deleted", func(t *testing.T) {
		rig := newPGSecretRig(t)
		if err := rig.store.SoftDelete(context.Background(), rig.tenant, "p1", time.Now().UTC()); err != nil {
			t.Fatalf("soft delete: %v", err)
		}
		before := rig.row(t)
		rr := rig.send(t, http.MethodPost, "/v1/admin/credential-pools/p1/credentials?tenantId="+rig.tenant, body, "")
		if rr.Code != http.StatusNotFound || len(rig.writer.puts) != 0 {
			t.Fatalf("status = %d puts = %v, want 404 and no Put", rr.Code, rig.writer.puts)
		}
		if after := rig.row(t); after.Version != before.Version || len(after.Credentials) != 2 {
			t.Fatalf("row changed: %+v", after)
		}
	})
	t.Run("active", func(t *testing.T) {
		rig := newPGSecretRig(t)
		rr := rig.send(t, http.MethodPost, "/v1/admin/credential-pools/p1/credentials?tenantId="+rig.tenant, body, "")
		if rr.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201; body %s", rr.Code, rr.Body.String())
		}
		name := credential.ManagedSecretName(rig.tenant, "p1", "key-3")
		if len(rig.writer.puts) != 1 || rig.writer.puts[0] != name {
			t.Fatalf("puts = %v, want [%s]", rig.writer.puts, name)
		}
		row := rig.row(t)
		if got := row.Credentials[len(row.Credentials)-1]; got.SecretRef != credential.CanonicalSecretRef(pgSecretNS, name) {
			t.Fatalf("stored ref = %q", got.SecretRef)
		}
	})
}
