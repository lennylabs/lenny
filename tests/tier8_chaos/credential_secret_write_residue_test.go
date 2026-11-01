// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos test for the residue a lost row commit leaves after a
// Lenny-written credential Secret operation. The admin credential-pool
// write performs its Put or Delete under the pool row lock as the last step
// before the commit, and takes no compensating action. When the Postgres
// backend holding the transaction dies after the Secret operation, the
// write is refused with 503 SERVICE_UNAVAILABLE, the row does not change,
// and the Secret and the row disagree in one of the ways §4.9 lists. Each
// case kills the backend from the writer's hook, asserts the 503 and the
// state, then issues the later write §4.9 names and asserts it converges.
//
// spec: §4.9 (Credential Storage Topology: Credential values submitted
// through the admin API rule 5).

package tier8_chaos_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	credentialpoolpg "github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

const residueNS = "lenny-credentials"

// killingSecretWriter holds the Secrets it was asked to write. While kill
// is set, each successful Put or Delete terminates every other backend of
// the database that is idle in a transaction, which is the admin write's
// transaction waiting on the writer, so its row commit is lost.
type killingSecretWriter struct {
	mu      sync.Mutex
	pool    *pgxpool.Pool
	secrets map[string]map[string]string
	kill    bool
}

func (w *killingSecretWriter) Put(ctx context.Context, name string, _ admin.SecretOwner, data map[string]string) error {
	w.mu.Lock()
	w.secrets[name] = data
	w.mu.Unlock()
	return w.terminate(ctx)
}

func (w *killingSecretWriter) Delete(ctx context.Context, name string) error {
	w.mu.Lock()
	delete(w.secrets, name)
	w.mu.Unlock()
	return w.terminate(ctx)
}

func (w *killingSecretWriter) DeleteTenant(context.Context, string) error { return nil }

func (w *killingSecretWriter) terminate(ctx context.Context) error {
	if !w.kill {
		return nil
	}
	w.kill = false
	_, err := w.pool.Exec(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity
		WHERE datname = current_database() AND pid <> pg_backend_pid() AND state = 'idle in transaction'`)
	return err
}

func (w *killingSecretWriter) value(name string) (map[string]string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	v, ok := w.secrets[name]
	return v, ok
}

type residueRig struct {
	store  *credentialpoolpg.Store
	writer *killingSecretWriter
	h      http.Handler
}

const residueTenant = "acme-residue"

func newResidueRig(t *testing.T) *residueRig {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	ctx := context.Background()
	if _, err := pg.Pool.Exec(ctx, `INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x01')`, residueTenant); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	store := credentialpoolpg.New(pg.Pool)
	writer := &killingSecretWriter{pool: pg.Pool, secrets: map[string]map[string]string{
		residueName("key-2"): {"apiKey": "v1"},
	}}
	if err := store.Create(ctx, credentialpoolstore.CredentialPool{
		TenantID: residueTenant, Name: "p1", Provider: "anthropic_direct",
		Credentials: []credentialpoolstore.Credential{
			{ID: "key-1", SecretRef: residueNS + "/operator-made"},
			{ID: "key-2", SecretRef: residueRef("key-2")},
		},
	}); err != nil {
		t.Fatalf("seed pool: %v", err)
	}
	h := admin.NewRouter(tenantstore.NewMemory(), admin.Options{CredentialsNamespace: residueNS}).
		WithCredentialPools(store).WithCredentialSecretWriter(writer).Handler()
	return &residueRig{store: store, writer: writer, h: h}
}

func residueName(id string) string { return credential.ManagedSecretName(residueTenant, "p1", id) }

func residueRef(id string) string { return credential.CanonicalSecretRef(residueNS, residueName(id)) }

func (r *residueRig) send(t *testing.T, method, path string, body any) int {
	t.Helper()
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path+"?tenantId="+residueTenant, bytes.NewReader(buf))
	req = req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		TenantID: residueTenant, Subject: "alice@acme.com", Roles: []pkgauth.Role{pkgauth.RoleTenantAdmin},
	}))
	rr := httptest.NewRecorder()
	r.h.ServeHTTP(rr, req)
	return rr.Code
}

func (r *residueRig) entryRef(t *testing.T, id string) (string, bool) {
	t.Helper()
	p, err := r.store.Get(context.Background(), residueTenant, "p1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	for _, c := range p.Credentials {
		if c.ID == id {
			return c.SecretRef, true
		}
	}
	return "", false
}

// TestCredentialSecretResidueConverges_spec_4_9 runs one case per state row
// of the §4.9 rule 5 residue table.
//
// diagnosis: a failure means a lost row commit after a credential Secret
// operation is reported as success, changes the row, or leaves a residue
// state that the later write §4.9 names does not converge: an orphan a
// retried Put does not adopt, a reference to an absent Secret that a
// retried removal cannot drop, or a stale value a later Put does not
// overwrite.
// spec: §4.9 (Credential Storage Topology: Credential values submitted
// through the admin API rule 5)
func TestCredentialSecretResidueConverges_spec_4_9(t *testing.T) {
	t.Run("orphan", func(t *testing.T) {
		rig := newResidueRig(t)
		rig.writer.kill = true
		body := map[string]any{"id": "key-3", "secretData": map[string]string{"apiKey": "v3"}}
		if code := rig.send(t, http.MethodPost, "/v1/admin/credential-pools/p1/credentials", body); code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", code)
		}
		if _, ok := rig.entryRef(t, "key-3"); ok {
			t.Fatal("the lost commit added the entry")
		}
		if _, ok := rig.writer.value(residueName("key-3")); !ok {
			t.Fatal("the Put did not stand as an orphan")
		}
		if code := rig.send(t, http.MethodPost, "/v1/admin/credential-pools/p1/credentials", body); code != http.StatusCreated {
			t.Fatalf("retried add status = %d, want 201", code)
		}
		if ref, ok := rig.entryRef(t, "key-3"); !ok || ref != residueRef("key-3") {
			t.Fatalf("key-3 ref = %q, want the orphan adopted", ref)
		}
	})
	t.Run("entry references an absent Secret", func(t *testing.T) {
		rig := newResidueRig(t)
		rig.writer.kill = true
		if code := rig.send(t, http.MethodDelete, "/v1/admin/credential-pools/p1/credentials/key-2", nil); code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", code)
		}
		if ref, ok := rig.entryRef(t, "key-2"); !ok || ref != residueRef("key-2") {
			t.Fatal("the lost commit dropped the entry")
		}
		if _, ok := rig.writer.value(residueName("key-2")); ok {
			t.Fatal("the Delete did not stand")
		}
		if code := rig.send(t, http.MethodDelete, "/v1/admin/credential-pools/p1/credentials/key-2", nil); code != http.StatusOK {
			t.Fatalf("retried removal status = %d, want 200", code)
		}
		if _, ok := rig.entryRef(t, "key-2"); ok {
			t.Fatal("the retried removal did not drop the reference")
		}
	})
	t.Run("Secret holds an uncommitted value", func(t *testing.T) {
		rig := newResidueRig(t)
		rig.writer.kill = true
		v2 := map[string]any{"secretData": map[string]string{"apiKey": "v2"}}
		if code := rig.send(t, http.MethodPut, "/v1/admin/credential-pools/p1/credentials/key-2", v2); code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", code)
		}
		if got, _ := rig.writer.value(residueName("key-2")); !reflect.DeepEqual(got, map[string]string{"apiKey": "v2"}) {
			t.Fatalf("Secret = %v, want the uncommitted v2", got)
		}
		v3 := map[string]any{"secretData": map[string]string{"apiKey": "v3"}}
		if code := rig.send(t, http.MethodPut, "/v1/admin/credential-pools/p1/credentials/key-2", v3); code != http.StatusOK {
			t.Fatalf("later update status = %d, want 200", code)
		}
		if got, _ := rig.writer.value(residueName("key-2")); !reflect.DeepEqual(got, map[string]string{"apiKey": "v3"}) {
			t.Fatalf("Secret = %v, want v3", got)
		}
	})
}
