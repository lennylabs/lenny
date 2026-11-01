// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 tenant-isolation check for credential-pool secretRefs. A
// credential Secret is identified only by its name in the shared
// credentials namespace, so a tenant that could write another tenant's
// ref into its own pool would lease that tenant's credential. §4.9 closes
// this where refs enter a pool: introducing a ref needs platform-admin,
// and a ref with the reserved lenny-cred- prefix is accepted only on the
// credential id that already carries it.
//
// The checks run in-process, as admin_scope_enforce_test.go does, by
// driving the production admin Router with an injected Principal. The
// role and prefix decisions are made in the gateway before any store
// write or Token Service call, so no cluster is needed to reach them.
//
// spec: §4.9 (Credential Storage Topology), §10.2.

package tier9_security_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/customrolestore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

const crossTenantCredNS = "lenny-credentials"

// recordingCrossTenantProber answers ALLOWED for every ref and records
// each call, so a refusal that happened after a probe would be visible.
type recordingCrossTenantProber struct{ calls []string }

func (p *recordingCrossTenantProber) ProbeSecretAccess(_ context.Context, ref string) (admin.SecretProbeVerdict, error) {
	p.calls = append(p.calls, ref)
	return admin.SecretProbeAllowed, nil
}

func crossTenantPrincipal(tenant string, roles ...pkgauth.Role) func(*http.Request) *http.Request {
	return func(req *http.Request) *http.Request {
		return req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
			TenantID: tenant, Subject: "carol@" + tenant, Roles: roles,
		}))
	}
}

// TestCredentialSecretRefCrossTenantReference pins that no caller can
// point tenant acme's pool at a Secret tenant globex's pool uses: a
// tenant-admin and a custom role holding manage_credential_pools get 403
// for globex's operator ref, and a platform-admin gets 400 for globex's
// Lenny-written ref. None of the refusals probes the Token Service or
// changes acme's pool.
//
// diagnosis: a failure means a caller added another tenant's credential
// Secret to its own pool, so its sessions would lease the other tenant's
// credential (cross-tenant credential use), or the refusal ran after a
// Token Service probe that reveals whether the other tenant's Secret
// exists.
// spec: §4.9 (Credential Storage Topology: Secret references, Registering a
// Secret by reference), §10.2 (Manage credential pools)
func TestCredentialSecretRefCrossTenantReference(t *testing.T) {
	globexOperatorRef := crossTenantCredNS + "/globex-anthropic"
	globexWrittenName := credential.ManagedSecretName("globex", "shared", "key-1")

	cases := []struct {
		name   string
		as     func(*http.Request) *http.Request
		ref    string
		status int
		code   string
	}{
		{"tenant-admin, globex operator ref", crossTenantPrincipal("acme", pkgauth.RoleTenantAdmin), "globex-anthropic", http.StatusForbidden, "FORBIDDEN"},
		{"custom role with manage_credential_pools, globex operator ref", crossTenantPrincipal("acme", "cred-admin"), globexOperatorRef, http.StatusForbidden, "FORBIDDEN"},
		{"platform-admin, globex Lenny-written ref", crossTenantPrincipal("platform", pkgauth.RolePlatformAdmin), globexWrittenName, http.StatusBadRequest, "VALIDATION_ERROR"},
		{"platform-admin, globex Lenny-written ref qualified", crossTenantPrincipal("platform", pkgauth.RolePlatformAdmin), crossTenantCredNS + "/" + globexWrittenName, http.StatusBadRequest, "VALIDATION_ERROR"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			store := credentialpoolstore.NewMemory()
			for _, p := range []credentialpoolstore.CredentialPool{
				{
					TenantID: "acme", Name: "p1", Provider: "anthropic_direct", MaxConcurrentSessions: 10,
					Credentials: []credentialpoolstore.Credential{{ID: "key-1", SecretRef: crossTenantCredNS + "/acme-anthropic"}},
				},
				{
					TenantID: "globex", Name: "shared", Provider: "anthropic_direct", MaxConcurrentSessions: 10,
					Credentials: []credentialpoolstore.Credential{
						{ID: "key-1", SecretRef: crossTenantCredNS + "/" + globexWrittenName},
						{ID: "key-2", SecretRef: globexOperatorRef},
					},
				},
			} {
				if err := store.Create(ctx, p); err != nil {
					t.Fatalf("seed %s/%s: %v", p.TenantID, p.Name, err)
				}
			}
			roles := customrolestore.NewMemory()
			if err := roles.Create(ctx, customrolestore.CustomRole{
				TenantID: "acme", Name: "cred-admin",
				Permissions: []pkgauth.Permission{pkgauth.PermManageCredentialPools},
			}); err != nil {
				t.Fatalf("seed custom role: %v", err)
			}
			prober := &recordingCrossTenantProber{}
			h := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
				Clock:                func() time.Time { return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) },
				CredentialsNamespace: crossTenantCredNS,
			}).WithCredentialPools(store).WithSecretAccessProber(prober).WithCustomRoles(roles).Handler()

			before, _ := store.Get(ctx, "acme", "p1")
			// The credential id key-1 matches the id globex's written ref
			// sits on, so a per-id check that ignored the pool would pass.
			for _, req := range []struct{ method, path string }{
				{http.MethodPost, "/v1/admin/credential-pools/p1/credentials?tenantId=acme"},
				{http.MethodPut, "/v1/admin/credential-pools/p1/credentials/key-1?tenantId=acme"},
			} {
				id := "key-9"
				if req.method == http.MethodPut {
					id = "key-1"
				}
				buf, _ := json.Marshal(admin.CredentialEntryPayload{ID: id, SecretRef: tc.ref})
				r := tc.as(httptest.NewRequest(req.method, req.path, bytes.NewReader(buf)))
				rr := httptest.NewRecorder()
				h.ServeHTTP(rr, r)
				if rr.Code != tc.status {
					t.Fatalf("%s %s: status = %d, want %d; body %s", req.method, req.path, rr.Code, tc.status, rr.Body.String())
				}
				var env struct {
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				_ = json.Unmarshal(rr.Body.Bytes(), &env)
				if env.Error.Code != tc.code {
					t.Fatalf("%s %s: code = %q, want %q", req.method, req.path, env.Error.Code, tc.code)
				}
			}
			if len(prober.calls) != 0 {
				t.Fatalf("a refused cross-tenant ref was probed: %v", prober.calls)
			}
			after, _ := store.Get(ctx, "acme", "p1")
			if after.Version != before.Version || !reflect.DeepEqual(after.Credentials, before.Credentials) {
				t.Fatalf("acme's pool changed: before %+v after %+v", before.Credentials, after.Credentials)
			}
		})
	}
}
