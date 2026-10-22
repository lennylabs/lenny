// SPDX-License-Identifier: MIT

package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
)

// setRegistry is a TenantRegistry over a fixed tenant set.
type setRegistry map[string]bool

func (s setRegistry) IsRegistered(id string) (bool, error) { return s[id], nil }

// tenantModeRequest drives one request through a middleware built from
// opts and returns the status and the tenant the inner handler saw ("" when
// the handler did not run).
func tenantModeRequest(t *testing.T, opts Options, prep func(*http.Request)) (int, string) {
	t.Helper()
	ran := false
	var tenant string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ran = true
		if p, ok := FromContext(r.Context()); ok {
			tenant = p.TenantID
		}
		w.WriteHeader(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	prep(req)
	rr := httptest.NewRecorder()
	Wrap(inner, opts).ServeHTTP(rr, req)
	if !ran {
		tenant = ""
	}
	return rr.Code, tenant
}

// TestTenantResolutionBySingleAndMultiTenantMode exercises the §10.2
// extraction table on both transports in both modes, including the
// DevHeaderNamesTenant exception. A single-tenant middleware must ignore
// the Bearer tenant claim even when the dev-header exception is on, and a
// multi-tenant middleware must check the dev header against the registry
// even when the exception is on.
//
// spec: §10.2 (tenant_id extraction from OIDC tokens).
func TestTenantResolutionBySingleAndMultiTenantMode(t *testing.T) {
	signer := jwt.NewHMACSigner("k", []byte("tenant-mode-secret"))
	bearer := func(tenant string) func(*http.Request) {
		return func(r *http.Request) {
			tok, err := signer.Sign(jwt.Claims{
				Subject:  "alice",
				TenantID: tenant,
				Expiry:   time.Now().Add(time.Minute).Unix(),
				JWTID:    "jti-" + tenant,
			})
			if err != nil {
				t.Fatalf("sign: %v", err)
			}
			r.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	devHeader := func(tenant string) func(*http.Request) {
		return func(r *http.Request) {
			if tenant != "" {
				r.Header.Set("X-Lenny-Tenant-ID", tenant)
			}
			r.Header.Set("X-Lenny-User-ID", "alice")
		}
	}
	registry := setRegistry{"acme": true}
	single := Options{Verifier: signer, AllowDevHeaders: true}
	singleDev := Options{Verifier: signer, AllowDevHeaders: true, DevHeaderNamesTenant: true}
	multi := Options{Verifier: signer, AllowDevHeaders: true, MultiTenant: true, Registry: registry}
	multiDev := Options{Verifier: signer, AllowDevHeaders: true, MultiTenant: true, Registry: registry, DevHeaderNamesTenant: true}

	cases := []struct {
		name       string
		opts       Options
		prep       func(*http.Request)
		wantStatus int
		wantTenant string
	}{
		{"single ignores an unregistered bearer claim", single, bearer("globex"), http.StatusNoContent, pkgauth.DefaultTenantID},
		{"single ignores a registered bearer claim", single, bearer("acme"), http.StatusNoContent, pkgauth.DefaultTenantID},
		{"single ignores a malformed bearer claim", single, bearer("bad/tenant"), http.StatusNoContent, pkgauth.DefaultTenantID},
		{"single ignores the bearer claim with the dev-header exception on", singleDev, bearer("globex"), http.StatusNoContent, pkgauth.DefaultTenantID},
		{"single resolves the dev header to default", single, devHeader("globex"), http.StatusNoContent, pkgauth.DefaultTenantID},
		{"single dev mode lets the dev header name the tenant", singleDev, devHeader("globex"), http.StatusNoContent, "globex"},
		{"single dev mode resolves an absent dev header to default", singleDev, devHeader(""), http.StatusNoContent, pkgauth.DefaultTenantID},
		{"single dev mode refuses a malformed dev header", singleDev, devHeader("bad/tenant"), http.StatusUnauthorized, ""},
		{"multi admits a registered bearer claim", multi, bearer("acme"), http.StatusNoContent, "acme"},
		{"multi refuses an unregistered bearer claim", multi, bearer("globex"), http.StatusForbidden, ""},
		{"multi refuses an absent bearer claim", multi, bearer(""), http.StatusUnauthorized, ""},
		{"multi refuses an unregistered dev header", multi, devHeader("globex"), http.StatusForbidden, ""},
		{"multi refuses an unregistered dev header with the exception on", multiDev, devHeader("globex"), http.StatusForbidden, ""},
		{"multi admits a registered dev header with the exception on", multiDev, devHeader("acme"), http.StatusNoContent, "acme"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, tenant := tenantModeRequest(t, tc.opts, tc.prep)
			if status != tc.wantStatus {
				t.Fatalf("status = %d, want %d", status, tc.wantStatus)
			}
			if tenant != tc.wantTenant {
				t.Fatalf("tenant = %q, want %q", tenant, tc.wantTenant)
			}
		})
	}
}
