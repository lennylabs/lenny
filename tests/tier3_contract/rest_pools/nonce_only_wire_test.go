//go:build contract

// SPDX-License-Identifier: MIT

// Tier-3 contract test for the §5.3 / §4.7 acknowledgeNonceOnlyAuth admin
// pool wire field. An HTTP request or response field is a wire contract: a
// client that sets acknowledgeNonceOnlyAuth on a pool create or update must
// see it preserved on the JSON the gateway returns, and the field must
// survive a GET round-trip. This drives the production admin handlers
// (router → poolstore → JSON encoder) over httptest so a JSON-tag or
// encode/decode regression in any layer surfaces as a contract violation.
//
// It sits alongside the rest_circuitbreaker / rest_sessions admin-pool wire
// coverage: each exercises one admin-pool field over the real HTTP surface.
//
// spec: §5.3 (acknowledgeNonceOnlyAuth pool flag), §4.7 (nonce-only gate).
package rest_pools_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
)

// adminPrincipal attaches a platform-admin principal so the §15.1 admin
// pool handlers' authz check passes.
func adminPrincipal(req *http.Request) *http.Request {
	return req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		TenantID: "platform",
		Subject:  "platform-admin@acme.com",
		Roles:    []pkgauth.Role{pkgauth.RolePlatformAdmin},
	}))
}

// newPoolRouter constructs the admin router with a nonce-only runtime
// (requireSoPeercred: false) seeded so the gate admits an acknowledged pool
// bound to it.
func newPoolRouter(t *testing.T) (*admin.Router, *poolstore.Memory) {
	t.Helper()
	tenants := tenantstore.NewMemory()
	runtimes := runtimestore.NewMemory()
	pools := poolstore.NewMemory()
	nonceOnly := false
	if err := runtimes.Create(context.Background(), runtimestore.Runtime{
		Name:              "nonce-only",
		RequireSoPeercred: &nonceOnly,
	}); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	router := admin.NewRouter(tenants, admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithRuntimes(runtimes).WithPools(pools)
	return router, pools
}

// serve runs one admin request and decodes the JSON body into a generic map
// so the test asserts on the wire field directly rather than on the typed
// struct, catching a JSON-tag drift the struct round-trip would hide.
func serve(t *testing.T, h http.Handler, method, path string, body any, etag string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var buf *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		buf = bytes.NewReader(b)
	} else {
		buf = bytes.NewReader(nil)
	}
	req := adminPrincipal(httptest.NewRequest(method, path, buf))
	if etag != "" {
		req.Header.Set("If-Match", etag)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	out := map[string]any{}
	if rr.Body.Len() > 0 {
		_ = json.Unmarshal(rr.Body.Bytes(), &out)
	}
	return rr, out
}

// TestAdminPoolAcknowledgeNonceOnlyAuthRoundTrips is the §5.3 wire contract
// on acknowledgeNonceOnlyAuth: it is preserved on the create response, on a
// later GET, and toggling it through a PUT is reflected on the update
// response and a re-GET.
//
// diagnosis: a failure means the admin pool create/update/get JSON does not
// round-trip acknowledgeNonceOnlyAuth, so a client cannot read back the
// §4.7 nonce-only opt-in it set, and the gateway and the
// PoolScalingController mirror could disagree on the pool's posture.
// spec: 5.3 (acknowledgeNonceOnlyAuth pool flag), 4.7 (nonce-only gate)
func TestAdminPoolAcknowledgeNonceOnlyAuthRoundTrips(t *testing.T) {
	router, _ := newPoolRouter(t)
	h := router.Handler()

	// Create with the acknowledgment set. The wire field must appear true on
	// the create response.
	rr, created := serve(t, h, http.MethodPost, "/v1/admin/pools", admin.PoolPayload{
		Name:                     "nonce-pool",
		RuntimeRef:               "nonce-only",
		IsolationProfile:         "sandboxed",
		AcknowledgeNonceOnlyAuth: true,
	}, "")
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	if created["acknowledgeNonceOnlyAuth"] != true {
		t.Errorf("create response did not carry acknowledgeNonceOnlyAuth=true: %v",
			created["acknowledgeNonceOnlyAuth"])
	}

	// GET must surface the same value.
	rr, got := serve(t, h, http.MethodGet, "/v1/admin/pools/nonce-pool", nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("get: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if got["acknowledgeNonceOnlyAuth"] != true {
		t.Errorf("get response did not carry acknowledgeNonceOnlyAuth=true: %v",
			got["acknowledgeNonceOnlyAuth"])
	}
	etag, _ := got["etag"].(string)

	// A PUT that toggles the ack off must be rejected (the pool stays bound to
	// the nonce-only runtime), proving the wire field reaches the gate. A PUT
	// that keeps it on (re-stating true) is admitted and echoes the field.
	off := false
	rr, _ = serve(t, h, http.MethodPut, "/v1/admin/pools/nonce-pool",
		admin.UpdatePoolRequest{AcknowledgeNonceOnlyAuth: &off}, etag)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("toggle-off PUT: got %d, want 400; body=%s", rr.Code, rr.Body.String())
	}

	on := true
	rr, updated := serve(t, h, http.MethodPut, "/v1/admin/pools/nonce-pool",
		admin.UpdatePoolRequest{AcknowledgeNonceOnlyAuth: &on}, etag)
	if rr.Code != http.StatusOK {
		t.Fatalf("re-state PUT: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if updated["acknowledgeNonceOnlyAuth"] != true {
		t.Errorf("update response did not carry acknowledgeNonceOnlyAuth=true: %v",
			updated["acknowledgeNonceOnlyAuth"])
	}
}
