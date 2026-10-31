// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 credential-handling checks for credential values submitted
// through the admin API. A value sent as secretData is written to a
// Kubernetes Secret and is never returned: no response body, response
// header, error body, or audit event of any admin credential-pool write
// carries it, whether the write commits, is previewed, or is refused. A
// platform-admin's write to another tenant's pool lands in a Secret owned
// by that pool's tenant, and a tenant-admin cannot reach another tenant's
// pool at all.
//
// The checks run in-process by driving the production admin Router with
// an injected Principal and a recording Secret writer, as
// credential_secret_ref_cross_tenant_test.go does.
//
// spec: §4.9 (Credential Storage Topology), §16.4.

package tier9_security_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
)

// valueCanary is the submitted value every case searches for.
const valueCanary = "sk-ant-CANARY-7f3e9a1c-do-not-echo"

type valueAudit struct {
	mu     sync.Mutex
	events []admin.AuditEvent
}

func (a *valueAudit) EmitAdminEvent(_ context.Context, ev admin.AuditEvent) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, ev)
}

// valueWriter records each Put and fails it when fail is set, with an
// error whose text carries the value, so an error path that forwards the
// writer error to the client is caught.
type valueWriter struct {
	mu   sync.Mutex
	puts []admin.SecretOwner
	name []string
	fail bool
}

func (w *valueWriter) Put(_ context.Context, name string, meta admin.SecretOwner, data map[string]string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.puts = append(w.puts, meta)
	w.name = append(w.name, name)
	if w.fail {
		return fmt.Errorf("admission webhook rejected data %v", data)
	}
	return nil
}
func (w *valueWriter) Delete(context.Context, string) error       { return nil }
func (w *valueWriter) DeleteTenant(context.Context, string) error { return nil }

type valueRig struct {
	h      http.Handler
	audit  *valueAudit
	writer *valueWriter
}

func newValueRig(t *testing.T, withWriter, failPut bool) *valueRig {
	t.Helper()
	store := credentialpoolstore.NewMemory()
	for _, tenant := range []string{"acme", "globex"} {
		if err := store.Create(context.Background(), credentialpoolstore.CredentialPool{
			TenantID: tenant, Name: "shared", Provider: "anthropic_direct",
			Credentials: []credentialpoolstore.Credential{{ID: "key-1", SecretRef: crossTenantCredNS + "/" + tenant + "-op"}},
		}); err != nil {
			t.Fatalf("seed %s: %v", tenant, err)
		}
	}
	rig := &valueRig{audit: &valueAudit{}}
	r := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock:                func() time.Time { return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) },
		CredentialsNamespace: crossTenantCredNS,
		Audit:                rig.audit,
	}).WithCredentialPools(store).WithSecretAccessProber(&recordingCrossTenantProber{})
	if withWriter {
		rig.writer = &valueWriter{fail: failPut}
		r = r.WithCredentialSecretWriter(rig.writer)
	}
	rig.h = r.Handler()
	return rig
}

func (rig *valueRig) send(t *testing.T, method, path string, body any, as func(*http.Request) *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := as(httptest.NewRequest(method, path, bytes.NewReader(buf)))
	req.Header.Set("If-Match", `"1"`)
	rr := httptest.NewRecorder()
	rig.h.ServeHTTP(rr, req)
	return rr
}

// TestCredentialSecretDataValueNeverReturned pins that a submitted
// secretData value appears in no response and no audit event on every
// admin credential-pool write that can carry it: a committed credential
// addition and update, their dryRun previews, a refusal for a missing
// Token Service, a failed Secret write whose error names the value, a
// secretData check refusal, the pool create and update refusals, and the
// bootstrap seed refusal.
//
// diagnosis: a failure means an admin response, error body, header, or
// audit event leaks a credential value submitted as secretData, so anyone
// with read access to API responses or the audit trail can recover the
// credential.
// spec: §4.9 (Credential Storage Topology: Credential values submitted
// through the admin API rules 1, 5, 6, and 7), §16.4
func TestCredentialSecretDataValueNeverReturned(t *testing.T) {
	data := map[string]string{"apiKey": valueCanary}
	entry := func(id string) map[string]any { return map[string]any{"id": id, "secretData": data} }
	pool := map[string]any{"tenantId": "acme", "name": "p2", "provider": "anthropic_direct", "credentials": []any{entry("key-1")}}
	tenantAdmin := crossTenantPrincipal("acme", pkgauth.RoleTenantAdmin)
	platformAdmin := crossTenantPrincipal("platform", pkgauth.RolePlatformAdmin)
	cases := []struct {
		name               string
		withWriter, fail   bool
		method, path       string
		body               any
		as                 func(*http.Request) *http.Request
		wantStatusAtLeast  int
		wantStatusLessThan int
	}{
		{"credential add", true, false, http.MethodPost, "/v1/admin/credential-pools/shared/credentials?tenantId=acme", entry("key-2"), tenantAdmin, 200, 300},
		{"credential add dryRun", true, false, http.MethodPost, "/v1/admin/credential-pools/shared/credentials?tenantId=acme&dryRun=true", entry("key-2"), tenantAdmin, 200, 300},
		{"credential update", true, false, http.MethodPut, "/v1/admin/credential-pools/shared/credentials/key-1?tenantId=acme", map[string]any{"secretData": data}, tenantAdmin, 200, 300},
		{"credential update dryRun", true, false, http.MethodPut, "/v1/admin/credential-pools/shared/credentials/key-1?tenantId=acme&dryRun=true", map[string]any{"secretData": data}, tenantAdmin, 200, 300},
		{"no Token Service", false, false, http.MethodPost, "/v1/admin/credential-pools/shared/credentials?tenantId=acme", entry("key-2"), tenantAdmin, 422, 423},
		{"failed Secret write", true, true, http.MethodPost, "/v1/admin/credential-pools/shared/credentials?tenantId=acme", entry("key-2"), tenantAdmin, 503, 504},
		{"two value fields", true, false, http.MethodPost, "/v1/admin/credential-pools/shared/credentials?tenantId=acme", map[string]any{"id": "key-2", "secretRef": "x", "secretData": data}, tenantAdmin, 400, 401},
		{"invalid key", true, false, http.MethodPost, "/v1/admin/credential-pools/shared/credentials?tenantId=acme", map[string]any{"id": "key-2", "secretData": map[string]string{"bad key": valueCanary}}, tenantAdmin, 400, 401},
		{"pool create", true, false, http.MethodPost, "/v1/admin/credential-pools", pool, platformAdmin, 400, 401},
		{"pool update", true, false, http.MethodPut, "/v1/admin/credential-pools/shared?tenantId=acme", map[string]any{"tenantId": "acme", "name": "shared", "provider": "anthropic_direct", "credentials": []any{entry("key-1")}}, platformAdmin, 400, 401},
		{"bootstrap seed", true, false, http.MethodPost, "/v1/admin/bootstrap", map[string]any{"credentialPools": []any{pool}}, platformAdmin, 200, 300},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := newValueRig(t, tc.withWriter, tc.fail)
			rr := rig.send(t, tc.method, tc.path, tc.body, tc.as)
			if rr.Code < tc.wantStatusAtLeast || rr.Code >= tc.wantStatusLessThan {
				t.Fatalf("status = %d, want in [%d, %d); body %s", rr.Code, tc.wantStatusAtLeast, tc.wantStatusLessThan, rr.Body.String())
			}
			if strings.Contains(rr.Body.String(), valueCanary) {
				t.Fatalf("response body carries the submitted value: %s", rr.Body.String())
			}
			for k, v := range rr.Header() {
				if strings.Contains(strings.Join(v, ","), valueCanary) {
					t.Fatalf("response header %s carries the submitted value", k)
				}
			}
			rig.audit.mu.Lock()
			defer rig.audit.mu.Unlock()
			for _, ev := range rig.audit.events {
				if strings.Contains(fmt.Sprintf("%+v", ev), valueCanary) {
					t.Fatalf("audit event %s carries the submitted value", ev.Type)
				}
			}
		})
	}
}

// TestCredentialSecretDataWrittenUnderPoolTenant pins that a platform-admin
// adding a value to tenant globex's pool writes the Secret §4.9 names for
// globex, owned by globex, so tenant erasure of globex reaches it and no
// other tenant's pool can reference it, and that acme's tenant-admin
// cannot write a value into globex's pool.
//
// diagnosis: a failure means a credential value written into one tenant's
// pool lands in a Secret named or owned for another tenant (the caller's),
// so the owning tenant's erasure misses it or another tenant can reference
// it, or a tenant-admin wrote a credential into another tenant's pool.
// spec: §4.9 (Credential Storage Topology: Credential values submitted
// through the admin API rule 2), §10.2 (Manage credential pools)
func TestCredentialSecretDataWrittenUnderPoolTenant(t *testing.T) {
	rig := newValueRig(t, true, false)
	body := map[string]any{"id": "key-2", "secretData": map[string]string{"apiKey": valueCanary}}
	rr := rig.send(t, http.MethodPost, "/v1/admin/credential-pools/shared/credentials?tenantId=globex", body,
		crossTenantPrincipal("platform", pkgauth.RolePlatformAdmin))
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body %s", rr.Code, rr.Body.String())
	}
	if len(rig.writer.puts) != 1 {
		t.Fatalf("puts = %+v, want one", rig.writer.puts)
	}
	wantOwner := admin.SecretOwner{TenantID: "globex", Pool: "shared", CredentialID: "key-2"}
	if rig.writer.puts[0] != wantOwner {
		t.Fatalf("owner = %+v, want %+v", rig.writer.puts[0], wantOwner)
	}
	if want := credential.ManagedSecretName("globex", "shared", "key-2"); rig.writer.name[0] != want {
		t.Fatalf("Secret name = %s, want %s", rig.writer.name[0], want)
	}

	rr = rig.send(t, http.MethodPost, "/v1/admin/credential-pools/shared/credentials?tenantId=globex", body,
		crossTenantPrincipal("acme", pkgauth.RoleTenantAdmin))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("acme tenant-admin into globex: status = %d, want 403", rr.Code)
	}
	if len(rig.writer.puts) != 1 {
		t.Fatalf("a refused cross-tenant write reached the writer: %+v", rig.writer.puts)
	}
}
