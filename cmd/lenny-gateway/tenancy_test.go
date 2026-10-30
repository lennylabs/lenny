// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// TestValidateTenancyConfigRefusesContradiction covers every pair of the
// two tenancy inputs: the agreeing pairs start, and a contradictory pair or
// an unknown mode is refused with an error naming the offending key.
//
// spec: §10.2 (`tenant_id` extraction), §4.9
// (tenancy.mode: multi), §10.3 (gateway startup configuration validation).
func TestValidateTenancyConfigRefusesContradiction(t *testing.T) {
	cases := []struct {
		mode        string
		multiTenant bool
		wantErr     string
	}{
		{mode: "single", multiTenant: false},
		{mode: "multi", multiTenant: true},
		{mode: "single", multiTenant: true, wantErr: "config_key=auth.multiTenant"},
		{mode: "multi", multiTenant: false, wantErr: "config_key=auth.multiTenant"},
		{mode: "", multiTenant: false, wantErr: "config_key=tenancy.mode"},
		{mode: "Multi", multiTenant: true, wantErr: "config_key=tenancy.mode"},
		{mode: "dev", multiTenant: false, wantErr: "config_key=tenancy.mode"},
	}
	for _, tc := range cases {
		err := validateTenancyConfig(tc.mode, tc.multiTenant)
		switch {
		case tc.wantErr == "" && err != nil:
			t.Errorf("validateTenancyConfig(%q, %t) = %v, want nil", tc.mode, tc.multiTenant, err)
		case tc.wantErr != "" && err == nil:
			t.Errorf("validateTenancyConfig(%q, %t) = nil, want an error naming %s", tc.mode, tc.multiTenant, tc.wantErr)
		case tc.wantErr != "" && !strings.Contains(err.Error(), tc.wantErr):
			t.Errorf("validateTenancyConfig(%q, %t) = %v, want it to name %s", tc.mode, tc.multiTenant, err, tc.wantErr)
		}
	}
}

// TestApplyTenantAuthOptionsWiring asserts the auth options each
// combination of --multi-tenant and --dev-mode produces: only a
// multi-tenant gateway carries a registry, and that registry is the
// tenantstore-backed one; the dev-header tenant exception is on only in
// single-tenant dev mode.
//
// spec: §10.2 (`tenant_id` extraction), §17.4 (dev mode).
func TestApplyTenantAuthOptionsWiring(t *testing.T) {
	store := tenantstore.NewMemory()
	for _, tc := range []struct {
		multiTenant, devMode, wantDevHeaderNamesTenant bool
	}{
		{false, false, false},
		{false, true, true},
		{true, false, false},
		{true, true, false},
	} {
		// Start from a populated Options so the helper must overwrite
		// every field it owns.
		opts := authmw.Options{MultiTenant: !tc.multiTenant, DevHeaderNamesTenant: !tc.wantDevHeaderNamesTenant, Registry: bearerTenantRegistry{}}
		applyTenantAuthOptions(&opts, tc.multiTenant, tc.devMode, store)
		if opts.MultiTenant != tc.multiTenant {
			t.Errorf("multi=%t dev=%t: MultiTenant = %t", tc.multiTenant, tc.devMode, opts.MultiTenant)
		}
		if opts.DevHeaderNamesTenant != tc.wantDevHeaderNamesTenant {
			t.Errorf("multi=%t dev=%t: DevHeaderNamesTenant = %t, want %t", tc.multiTenant, tc.devMode, opts.DevHeaderNamesTenant, tc.wantDevHeaderNamesTenant)
		}
		reg, isBearer := opts.Registry.(bearerTenantRegistry)
		switch {
		case tc.multiTenant && (!isBearer || reg.store != store):
			t.Errorf("multi=%t dev=%t: Registry = %#v, want the tenantstore-backed bearerTenantRegistry", tc.multiTenant, tc.devMode, opts.Registry)
		case !tc.multiTenant && opts.Registry != nil:
			t.Errorf("multi=%t dev=%t: Registry = %#v, want nil in single-tenant mode", tc.multiTenant, tc.devMode, opts.Registry)
		}
	}
}

// TestGatewayAuthTenantResolutionByMode wires the auth middleware the way
// the gateway does and drives Bearer tokens through it. A multi-tenant
// gateway admits a registered tenant, refuses an unregistered or
// soft-deleted one with 403 TENANT_NOT_FOUND, and refuses an absent claim
// with 401 TENANT_CLAIM_MISSING. A single-tenant gateway serves every
// Bearer as `default`, whatever tenant the claim names.
//
// spec: §10.2 (`tenant_id` extraction).
func TestGatewayAuthTenantResolutionByMode(t *testing.T) {
	ctx := context.Background()
	store := tenantstore.NewMemory()
	if err := store.Create(ctx, tenantstore.Tenant{ID: "acme", DisplayName: "Acme"}); err != nil {
		t.Fatalf("create acme: %v", err)
	}
	if err := store.Create(ctx, tenantstore.Tenant{ID: "initech", DisplayName: "Initech"}); err != nil {
		t.Fatalf("create initech: %v", err)
	}
	if err := store.SoftDelete(ctx, "initech", time.Now()); err != nil {
		t.Fatalf("delete initech: %v", err)
	}
	signer := jwt.NewHMACSigner("k", []byte("gateway-tenancy-secret"))
	mint := func(tenant string) string {
		tok, err := signer.Sign(jwt.Claims{Subject: "alice", TenantID: tenant, Expiry: time.Now().Add(time.Minute).Unix(), JWTID: "j-" + tenant})
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		return tok
	}
	serve := func(multiTenant bool, token string) (int, string, string) {
		var tenant string
		inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if p, ok := authmw.FromContext(r.Context()); ok {
				tenant = p.TenantID
			}
			w.WriteHeader(http.StatusNoContent)
		})
		opts := authmw.Options{Verifier: signer, AllowDevHeaders: true}
		applyTenantAuthOptions(&opts, multiTenant, false, store)
		req := httptest.NewRequest(http.MethodGet, "/v1/admin/me", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		authmw.Wrap(inner, opts).ServeHTTP(rr, req)
		return rr.Code, tenant, rr.Body.String()
	}

	cases := []struct {
		name        string
		multiTenant bool
		claim       string
		wantStatus  int
		wantTenant  string
		wantCode    string
	}{
		{"multi admits a registered tenant", true, "acme", http.StatusNoContent, "acme", ""},
		{"multi refuses an unregistered tenant", true, "totally-bogus-tenant-xyz", http.StatusForbidden, "", "TENANT_NOT_FOUND"},
		{"multi refuses a soft-deleted tenant", true, "initech", http.StatusForbidden, "", "TENANT_NOT_FOUND"},
		{"multi refuses an absent claim", true, "", http.StatusUnauthorized, "", "TENANT_CLAIM_MISSING"},
		{"multi admits the built-in default tenant", true, auth.DefaultTenantID, http.StatusNoContent, auth.DefaultTenantID, ""},
		{"single ignores an unregistered claim", false, "totally-bogus-tenant-xyz", http.StatusNoContent, auth.DefaultTenantID, ""},
		{"single ignores a registered claim", false, "acme", http.StatusNoContent, auth.DefaultTenantID, ""},
		{"single ignores an absent claim", false, "", http.StatusNoContent, auth.DefaultTenantID, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, tenant, body := serve(tc.multiTenant, mint(tc.claim))
			if status != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", status, tc.wantStatus, body)
			}
			if tenant != tc.wantTenant {
				t.Fatalf("tenant = %q, want %q", tenant, tc.wantTenant)
			}
			if tc.wantCode != "" && !strings.Contains(body, tc.wantCode) {
				t.Fatalf("body %s does not carry %s", body, tc.wantCode)
			}
		})
	}
}
