// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test for the §4.9 admin-time Secret probe under the
// pool row lock. The probe runs inside the credential-pool Update
// transaction, after SELECT ... FOR UPDATE, so a Token Service that never
// answers would hold the row lock and a Postgres connection for as long as
// the call lasts. The test drives the production handler, the production
// prober, and the Postgres pool store against a Token Service that accepts
// the call and stays silent.
//
// This test lives in package main because the production prober is
// unexported.
//
// spec: §4.9 (Credential Storage Topology: admin-time Secret probe rule 2),
// §15.1 (CREDENTIAL_PROBE_UNAVAILABLE).
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	credentialpoolpg "github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// diagnosis: a failure means a silent Token Service keeps the pool row locked
// past the probe deadline, or the write it refuses still changes the row, so
// emergency revocation of that pool would queue behind the hung probe.
// spec: §4.9 (Credential Storage Topology: admin-time Secret probe rule 2)
func TestSecretProbe_SilentTokenServiceReleasesPoolRowLock(t *testing.T) {
	const tenant, pool = "acme-probe-lock", "p1"
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
	ctx := context.Background()
	if _, err := pg.Pool.Exec(ctx, `INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x01')`, tenant); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	store := credentialpoolpg.New(pg.Pool)
	if err := store.Create(ctx, credentialpoolstore.CredentialPool{
		TenantID: tenant, Name: pool, Provider: "anthropic_direct",
		Credentials: []credentialpoolstore.Credential{{ID: "key-1", SecretRef: testCredNS + "/existing"}},
	}); err != nil {
		t.Fatalf("seed pool: %v", err)
	}

	hung, stub := dialHungTokenService(t)
	prober := newTokenServiceSecretProber(stub, testCredNS)
	prober.timeout = hungCallTimeout
	h := admin.NewRouter(tenantstore.NewMemory(), admin.Options{CredentialsNamespace: testCredNS}).
		WithCredentialPools(store).WithSecretAccessProber(prober).Handler()

	body, err := json.Marshal(admin.CredentialEntryPayload{ID: "key-2", SecretRef: "gh-token"})
	if err != nil {
		t.Fatalf("marshal credential: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/admin/credential-pools/"+pool+"/credentials?tenantId="+tenant, strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		TenantID: "platform", Subject: "alice@acme.com", Roles: []pkgauth.Role{pkgauth.RolePlatformAdmin},
	}))
	rec := withinHungCallLimit(t, func() *httptest.ResponseRecorder {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, req)
		return r
	})
	if rec.Code != http.StatusServiceUnavailable || errorCode(t, rec) != "CREDENTIAL_PROBE_UNAVAILABLE" {
		t.Fatalf("status %d body %s, want 503 CREDENTIAL_PROBE_UNAVAILABLE", rec.Code, rec.Body)
	}
	if hung.calls.Load() != 1 {
		t.Fatalf("RPC calls = %d, want 1: the write never reached the probe", hung.calls.Load())
	}

	lockCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	updated, err := store.Update(lockCtx, tenant, pool, func(*credentialpoolstore.CredentialPool) error { return nil })
	if err != nil {
		t.Fatalf("a later Update on the row after the refused write: %v", err)
	}
	if len(updated.Credentials) != 1 {
		t.Fatalf("credentials = %+v, want the refused entry absent", updated.Credentials)
	}
}
