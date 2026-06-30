// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
)

// spec: §15.1 lines 1207-1224 — ETag-based optimistic concurrency for the
// tenants admin resource. The tenant row's version is also the entity tag
// of its §10.6 rbac-config and §9.2 elicitation-content-integrity
// sub-resources, which are covered by their own batteries below.

// putTenantRaw issues a PUT carrying the given If-Match header verbatim so
// the §15.1 preconditions can be exercised directly. An empty ifMatch
// sends no header.
func putTenantRaw(t *testing.T, h http.Handler, id, ifMatch string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPut, "/v1/admin/tenants/"+id, bytes.NewReader(b)))
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// deleteTenantRaw issues a DELETE carrying the given If-Match header
// verbatim. An empty ifMatch sends no header.
func deleteTenantRaw(t *testing.T, h http.Handler, id, ifMatch string) *httptest.ResponseRecorder {
	t.Helper()
	req := withAdminPrincipal(httptest.NewRequest(http.MethodDelete, "/v1/admin/tenants/"+id, nil))
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// seedEtagTenant wires the admin router and seeds one active tenant.
func seedEtagTenant(t *testing.T, id string) (*admin.Router, *tenantstore.Memory) {
	t.Helper()
	router, store := newAdminServer(t)
	if err := store.Create(context.Background(), tenantstore.Tenant{ID: id, DisplayName: "Seed"}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	return router, store
}

// TestTenantETagOptimisticConcurrency_spec_15_1_1207 covers the §15.1 lines
// 1207-1224 ETag optimistic-concurrency contract for the tenants resource.
func TestTenantETagOptimisticConcurrency_spec_15_1_1207(t *testing.T) {
	displayName := func(s string) *string { return &s }

	// spec: §15.1 line 1209 — single-item GET carries the ETag header and
	// the body carries the per-item etag field.
	t.Run("GetCarriesETag", func(t *testing.T) {
		router, _ := seedEtagTenant(t, "acme")
		g := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/tenants/acme", nil))
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, g)
		if rr.Code != http.StatusOK {
			t.Fatalf("get: %d body=%s", rr.Code, rr.Body.String())
		}
		if got := rr.Header().Get("ETag"); got != `"1"` {
			t.Errorf("ETag header: got %q, want %q", got, `"1"`)
		}
		var body admin.TenantPayload
		_ = json.Unmarshal(rr.Body.Bytes(), &body)
		if body.ETag != `"1"` {
			t.Errorf("body etag: got %q, want %q", body.ETag, `"1"`)
		}
	})

	// spec: §15.1 line 1209 — list responses include a per-item ETag.
	t.Run("ListCarriesPerItemETag", func(t *testing.T) {
		router, _ := seedEtagTenant(t, "acme")
		g := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/tenants", nil))
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, g)
		if rr.Code != http.StatusOK {
			t.Fatalf("list: %d body=%s", rr.Code, rr.Body.String())
		}
		var page struct {
			Items []admin.TenantPayload `json:"items"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
			t.Fatalf("decode list: %v body=%s", err, rr.Body.String())
		}
		if len(page.Items) != 1 {
			t.Fatalf("items: %d", len(page.Items))
		}
		if page.Items[0].ETag != `"1"` {
			t.Errorf("per-item etag: got %q, want %q", page.Items[0].ETag, `"1"`)
		}
	})

	// spec: §15.1 line 1210 — a PUT with no If-Match returns 428 ETAG_REQUIRED.
	t.Run("PutMissingIfMatch", func(t *testing.T) {
		router, _ := seedEtagTenant(t, "acme")
		rr := putTenantRaw(t, router.Handler(), "acme", "", admin.UpdateTenantRequest{DisplayName: displayName("x")})
		if rr.Code != http.StatusPreconditionRequired {
			t.Fatalf("status: got %d, want 428; body=%s", rr.Code, rr.Body.String())
		}
		assertErrorCode(t, rr, "ETAG_REQUIRED")
	})

	// spec: §15.1 line 1210 — a malformed If-Match (weak validator, unquoted,
	// non-decimal, or `*`) is 400 VALIDATION_ERROR naming the header.
	t.Run("PutMalformedIfMatch", func(t *testing.T) {
		for _, bad := range []string{"3", "abc", "W/\"3\"", "*", `"1.5"`} {
			router, _ := seedEtagTenant(t, "acme")
			rr := putTenantRaw(t, router.Handler(), "acme", bad, admin.UpdateTenantRequest{DisplayName: displayName("x")})
			if rr.Code != http.StatusBadRequest {
				t.Errorf("If-Match %q: got %d, want 400; body=%s", bad, rr.Code, rr.Body.String())
				continue
			}
			var env struct {
				Error struct {
					Code    string         `json:"code"`
					Details map[string]any `json:"details"`
				} `json:"error"`
			}
			_ = json.Unmarshal(rr.Body.Bytes(), &env)
			if env.Error.Code != "VALIDATION_ERROR" {
				t.Errorf("If-Match %q: code got %q, want VALIDATION_ERROR", bad, env.Error.Code)
			}
			fields, _ := env.Error.Details["fields"].([]any)
			if len(fields) != 1 || fields[0] != "If-Match" {
				t.Errorf("If-Match %q: details.fields got %v, want [If-Match]", bad, env.Error.Details["fields"])
			}
		}
	})

	// spec: §15.1 line 1210 — a stale If-Match is 412 ETAG_MISMATCH and
	// carries details.currentEtag set to the live ETag.
	t.Run("PutStaleIfMatch", func(t *testing.T) {
		router, _ := seedEtagTenant(t, "acme")
		rr := putTenantRaw(t, router.Handler(), "acme", `"999"`, admin.UpdateTenantRequest{DisplayName: displayName("x")})
		if rr.Code != http.StatusPreconditionFailed {
			t.Fatalf("status: got %d, want 412; body=%s", rr.Code, rr.Body.String())
		}
		var env struct {
			Error struct {
				Code    string         `json:"code"`
				Details map[string]any `json:"details"`
			} `json:"error"`
		}
		_ = json.Unmarshal(rr.Body.Bytes(), &env)
		if env.Error.Code != "ETAG_MISMATCH" {
			t.Errorf("code: got %q, want ETAG_MISMATCH", env.Error.Code)
		}
		if env.Error.Details["currentEtag"] != `"1"` {
			t.Errorf("details.currentEtag: got %v, want %q", env.Error.Details["currentEtag"], `"1"`)
		}
	})

	// spec: §15.1 line 1211 — a matching If-Match succeeds and the response
	// returns the new (incremented) ETag; a retried PUT with the now-stale
	// tag loses the race with 412.
	t.Run("PutMatchingIfMatchBumpsETag", func(t *testing.T) {
		router, store := seedEtagTenant(t, "acme")
		rr := putTenantRaw(t, router.Handler(), "acme", `"1"`, admin.UpdateTenantRequest{DisplayName: displayName("Renamed")})
		if rr.Code != http.StatusOK {
			t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
		}
		if got := rr.Header().Get("ETag"); got != `"2"` {
			t.Errorf("new ETag header: got %q, want %q", got, `"2"`)
		}
		var body admin.TenantPayload
		_ = json.Unmarshal(rr.Body.Bytes(), &body)
		if body.ETag != `"2"` {
			t.Errorf("body etag: got %q, want %q", body.ETag, `"2"`)
		}
		row, _ := store.Get(context.Background(), "acme")
		if row.Version != 2 || row.DisplayName != "Renamed" {
			t.Errorf("stored: version=%d displayName=%q", row.Version, row.DisplayName)
		}
		stale := putTenantRaw(t, router.Handler(), "acme", `"1"`, admin.UpdateTenantRequest{DisplayName: displayName("Again")})
		if stale.Code != http.StatusPreconditionFailed {
			t.Errorf("stale second PUT: got %d, want 412; body=%s", stale.Code, stale.Body.String())
		}
	})

	// spec: §15.1 line 1213 — DELETE honours If-Match only when present: a
	// stale tag returns 412 and the tenant lifecycle is untouched.
	t.Run("DeleteStaleIfMatchIs412", func(t *testing.T) {
		router, store := seedEtagTenant(t, "acme")
		rr := deleteTenantRaw(t, router.Handler(), "acme", `"999"`)
		if rr.Code != http.StatusPreconditionFailed {
			t.Fatalf("delete stale If-Match: got %d, want 412; body=%s", rr.Code, rr.Body.String())
		}
		assertErrorCode(t, rr, "ETAG_MISMATCH")
		row, err := store.Get(context.Background(), "acme")
		if err != nil || !row.AcceptsNewWork() {
			t.Fatalf("tenant lifecycle advanced despite 412: state=%q err=%v", row.State, err)
		}
	})

	// spec: §15.1 line 1213 — an absent If-Match lets the DELETE proceed and
	// the tenant enters the §12.8 disabling lifecycle (202 Accepted).
	t.Run("DeleteWithoutIfMatchProceeds", func(t *testing.T) {
		router, store := seedEtagTenant(t, "acme")
		rr := deleteTenantRaw(t, router.Handler(), "acme", "")
		if rr.Code != http.StatusAccepted {
			t.Fatalf("delete without If-Match: got %d, want 202; body=%s", rr.Code, rr.Body.String())
		}
		row, _ := store.Get(context.Background(), "acme")
		if row.State != tenantstore.TenantStateDisabling {
			t.Errorf("state after delete: got %q, want disabling", row.State)
		}
	})
}
