// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/environment/customrolestore"
)

// spec: §15.1 lines 1207-1224 — ETag-based optimistic concurrency for the
// custom-roles admin resource.

// putCustomRoleRaw issues a PUT carrying the given If-Match header
// verbatim, bypassing doAdminReq's auto-fetch so the §15.1 ETag
// preconditions can be exercised directly. An empty ifMatch sends no
// header.
func putCustomRoleRaw(t *testing.T, h http.Handler, name, ifMatch string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPut, "/v1/admin/tenants/acme/roles/"+name, bytes.NewReader(b)))
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// deleteCustomRoleRaw issues a DELETE carrying the given If-Match header
// verbatim. An empty ifMatch sends no header.
func deleteCustomRoleRaw(t *testing.T, h http.Handler, name, ifMatch string) *httptest.ResponseRecorder {
	t.Helper()
	req := withAdminPrincipal(httptest.NewRequest(http.MethodDelete, "/v1/admin/tenants/acme/roles/"+name, nil))
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// seedEtagCustomRole wires the admin router and seeds one custom role
// under the `acme` tenant.
func seedEtagCustomRole(t *testing.T, name string) (*admin.Router, *customrolestore.Memory) {
	t.Helper()
	router, store := newCustomRoleAdmin(t)
	if err := store.Create(context.Background(), customrolestore.CustomRole{
		TenantID:    "acme",
		Name:        name,
		Permissions: []auth.Permission{auth.PermViewUsage},
	}); err != nil {
		t.Fatalf("seed custom role: %v", err)
	}
	return router, store
}

// TestCustomRoleETagOptimisticConcurrency_spec_15_1_1207 covers the §15.1
// lines 1207-1224 ETag optimistic-concurrency contract for the custom-roles
// resource.
func TestCustomRoleETagOptimisticConcurrency_spec_15_1_1207(t *testing.T) {
	// spec: §15.1 line 1209 — single-item GET carries the ETag header and
	// the body carries the per-item etag field.
	t.Run("GetCarriesETag", func(t *testing.T) {
		router, _ := seedEtagCustomRole(t, "cr_etag")
		g := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/tenants/acme/roles/cr_etag", nil))
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, g)
		if rr.Code != http.StatusOK {
			t.Fatalf("get: %d body=%s", rr.Code, rr.Body.String())
		}
		if got := rr.Header().Get("ETag"); got != `"1"` {
			t.Errorf("ETag header: got %q, want %q", got, `"1"`)
		}
		var body admin.CustomRolePayload
		_ = json.Unmarshal(rr.Body.Bytes(), &body)
		if body.ETag != `"1"` {
			t.Errorf("body etag: got %q, want %q", body.ETag, `"1"`)
		}
	})

	// spec: §15.1 line 1209 — list responses include a per-item ETag.
	t.Run("ListCarriesPerItemETag", func(t *testing.T) {
		router, _ := seedEtagCustomRole(t, "cr_etag")
		g := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/tenants/acme/roles", nil))
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, g)
		if rr.Code != http.StatusOK {
			t.Fatalf("list: %d body=%s", rr.Code, rr.Body.String())
		}
		var page struct {
			Items []admin.CustomRolePayload `json:"items"`
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
		router, _ := seedEtagCustomRole(t, "cr_etag")
		rr := putCustomRoleRaw(t, router.Handler(), "cr_etag", "", admin.UpdateCustomRoleRequest{
			Permissions: []auth.Permission{auth.PermViewUsage},
		})
		if rr.Code != http.StatusPreconditionRequired {
			t.Fatalf("status: got %d, want 428; body=%s", rr.Code, rr.Body.String())
		}
		assertErrorCode(t, rr, "ETAG_REQUIRED")
	})

	// spec: §15.1 line 1210 — a malformed If-Match (weak validator, unquoted,
	// non-decimal, or `*`) is 400 VALIDATION_ERROR naming the header.
	t.Run("PutMalformedIfMatch", func(t *testing.T) {
		for _, bad := range []string{"3", "abc", "W/\"3\"", "*", `"1.5"`} {
			router, _ := seedEtagCustomRole(t, "cr_etag")
			rr := putCustomRoleRaw(t, router.Handler(), "cr_etag", bad, admin.UpdateCustomRoleRequest{
				Permissions: []auth.Permission{auth.PermViewUsage},
			})
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
		router, _ := seedEtagCustomRole(t, "cr_etag")
		rr := putCustomRoleRaw(t, router.Handler(), "cr_etag", `"999"`, admin.UpdateCustomRoleRequest{
			Permissions: []auth.Permission{auth.PermViewUsage},
		})
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
		router, store := seedEtagCustomRole(t, "cr_etag")
		changed := admin.UpdateCustomRoleRequest{
			Permissions: []auth.Permission{auth.PermManageRuntimes, auth.PermManagePools},
		}
		rr := putCustomRoleRaw(t, router.Handler(), "cr_etag", `"1"`, changed)
		if rr.Code != http.StatusOK {
			t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
		}
		if got := rr.Header().Get("ETag"); got != `"2"` {
			t.Errorf("new ETag header: got %q, want %q", got, `"2"`)
		}
		var body admin.CustomRolePayload
		_ = json.Unmarshal(rr.Body.Bytes(), &body)
		if body.ETag != `"2"` {
			t.Errorf("body etag: got %q, want %q", body.ETag, `"2"`)
		}
		row, _ := store.Get(context.Background(), "acme", "cr_etag")
		if row.Version != 2 || len(row.Permissions) != 2 {
			t.Errorf("stored: version=%d permissions=%+v", row.Version, row.Permissions)
		}
		stale := putCustomRoleRaw(t, router.Handler(), "cr_etag", `"1"`, changed)
		if stale.Code != http.StatusPreconditionFailed {
			t.Errorf("stale second PUT: got %d, want 412; body=%s", stale.Code, stale.Body.String())
		}
	})

	// spec: §15.1 line 1213 — DELETE honours If-Match only when present: a
	// stale tag returns 412, an absent header proceeds.
	t.Run("DeleteStaleIfMatchIs412", func(t *testing.T) {
		router, store := seedEtagCustomRole(t, "cr_del")
		rr := deleteCustomRoleRaw(t, router.Handler(), "cr_del", `"999"`)
		if rr.Code != http.StatusPreconditionFailed {
			t.Fatalf("delete stale If-Match: got %d, want 412; body=%s", rr.Code, rr.Body.String())
		}
		assertErrorCode(t, rr, "ETAG_MISMATCH")
		if _, err := store.Get(context.Background(), "acme", "cr_del"); err != nil {
			t.Fatalf("role removed despite 412: %v", err)
		}
	})

	t.Run("DeleteWithoutIfMatchProceeds", func(t *testing.T) {
		router, store := seedEtagCustomRole(t, "cr_del")
		rr := deleteCustomRoleRaw(t, router.Handler(), "cr_del", "")
		if rr.Code != http.StatusNoContent {
			t.Fatalf("delete without If-Match: got %d, want 204; body=%s", rr.Code, rr.Body.String())
		}
		if _, err := store.Get(context.Background(), "acme", "cr_del"); err == nil {
			t.Error("role should be gone after delete")
		}
	})
}
