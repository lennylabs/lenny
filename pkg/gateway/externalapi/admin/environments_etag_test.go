// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/environment/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
)

// spec: §15.1 lines 1207-1224 — ETag-based optimistic concurrency for the
// environments admin resource.

// putEnvironmentRaw issues a PUT carrying the given If-Match header
// verbatim as a tenant-admin of acme (the tenant resolves from the
// principal, so no tenantId query is needed). An empty ifMatch sends no
// header.
func putEnvironmentRaw(t *testing.T, h http.Handler, name, ifMatch string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := withTenantAdminPrincipal(httptest.NewRequest(http.MethodPut, "/v1/admin/environments/"+name, bytes.NewReader(b)))
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// deleteEnvironmentRaw issues a DELETE carrying the given If-Match header
// verbatim. An empty ifMatch sends no header.
func deleteEnvironmentRaw(t *testing.T, h http.Handler, name, ifMatch string) *httptest.ResponseRecorder {
	t.Helper()
	req := withTenantAdminPrincipal(httptest.NewRequest(http.MethodDelete, "/v1/admin/environments/"+name, nil))
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// seedEtagEnvironment wires the admin router and seeds one environment
// under acme.
func seedEtagEnvironment(t *testing.T, name string) (*admin.Router, environmentstore.Store) {
	t.Helper()
	router, store, _ := newEnvironmentAdmin(t)
	if err := store.Create(context.Background(), environmentstore.Environment{
		TenantID: "acme",
		Name:     name,
	}); err != nil {
		t.Fatalf("seed environment: %v", err)
	}
	return router, store
}

// TestEnvironmentETagOptimisticConcurrency_spec_15_1_1207 covers the §15.1
// lines 1207-1224 ETag optimistic-concurrency contract for the
// environments resource.
func TestEnvironmentETagOptimisticConcurrency_spec_15_1_1207(t *testing.T) {
	// spec: §15.1 line 1209 — single-item GET carries the ETag header and
	// the body carries the per-item etag field.
	t.Run("GetCarriesETag", func(t *testing.T) {
		router, _ := seedEtagEnvironment(t, "env_etag")
		g := withTenantAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/environments/env_etag", nil))
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, g)
		if rr.Code != http.StatusOK {
			t.Fatalf("get: %d body=%s", rr.Code, rr.Body.String())
		}
		if got := rr.Header().Get("ETag"); got != `"1"` {
			t.Errorf("ETag header: got %q, want %q", got, `"1"`)
		}
		var body admin.EnvironmentPayload
		_ = json.Unmarshal(rr.Body.Bytes(), &body)
		if body.ETag != `"1"` {
			t.Errorf("body etag: got %q, want %q", body.ETag, `"1"`)
		}
	})

	// spec: §15.1 line 1209 — list responses include a per-item ETag.
	t.Run("ListCarriesPerItemETag", func(t *testing.T) {
		router, _ := seedEtagEnvironment(t, "env_etag")
		g := withTenantAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/environments", nil))
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, g)
		if rr.Code != http.StatusOK {
			t.Fatalf("list: %d body=%s", rr.Code, rr.Body.String())
		}
		var page struct {
			Items []admin.EnvironmentPayload `json:"items"`
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
		router, _ := seedEtagEnvironment(t, "env_etag")
		rr := putEnvironmentRaw(t, router.Handler(), "env_etag", "", validEnvironmentPayload("env_etag"))
		if rr.Code != http.StatusPreconditionRequired {
			t.Fatalf("status: got %d, want 428; body=%s", rr.Code, rr.Body.String())
		}
		assertErrorCode(t, rr, "ETAG_REQUIRED")
	})

	// spec: §15.1 line 1210 — a malformed If-Match (weak validator, unquoted,
	// non-decimal, or `*`) is 400 VALIDATION_ERROR naming the header.
	t.Run("PutMalformedIfMatch", func(t *testing.T) {
		for _, bad := range []string{"3", "abc", "W/\"3\"", "*", `"1.5"`} {
			router, _ := seedEtagEnvironment(t, "env_etag")
			rr := putEnvironmentRaw(t, router.Handler(), "env_etag", bad, validEnvironmentPayload("env_etag"))
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
		router, _ := seedEtagEnvironment(t, "env_etag")
		rr := putEnvironmentRaw(t, router.Handler(), "env_etag", `"999"`, validEnvironmentPayload("env_etag"))
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

	// spec: §15.1 line 1211 — a matching If-Match succeeds and returns the
	// new (incremented) ETag; a retried PUT with the now-stale tag 412s. A
	// dry-run with a stale tag still 412s and persists nothing.
	t.Run("PutMatchingIfMatchBumpsETag", func(t *testing.T) {
		router, store := seedEtagEnvironment(t, "env_etag")
		changed := validEnvironmentPayload("env_etag")
		changed.Description = "renamed workspace"
		rr := putEnvironmentRaw(t, router.Handler(), "env_etag", `"1"`, changed)
		if rr.Code != http.StatusOK {
			t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
		}
		if got := rr.Header().Get("ETag"); got != `"2"` {
			t.Errorf("new ETag header: got %q, want %q", got, `"2"`)
		}
		var body admin.EnvironmentPayload
		_ = json.Unmarshal(rr.Body.Bytes(), &body)
		if body.ETag != `"2"` {
			t.Errorf("body etag: got %q, want %q", body.ETag, `"2"`)
		}
		row, _ := store.Get(context.Background(), "acme", "env_etag")
		if row.Version != 2 || row.Description != "renamed workspace" {
			t.Errorf("stored: version=%d description=%q", row.Version, row.Description)
		}
		stale := putEnvironmentRaw(t, router.Handler(), "env_etag", `"1"`, changed)
		if stale.Code != http.StatusPreconditionFailed {
			t.Errorf("stale second PUT: got %d, want 412; body=%s", stale.Code, stale.Body.String())
		}
	})

	// spec: §15.1 lines 1140 + 1210 — a dry-run with a stale If-Match still
	// fails the precondition before the no-persist preview.
	t.Run("DryRunStaleIfMatchIs412", func(t *testing.T) {
		router, _ := seedEtagEnvironment(t, "env_etag")
		req := withTenantAdminPrincipal(httptest.NewRequest(http.MethodPut,
			"/v1/admin/environments/env_etag?dryRun=true", mustJSON(validEnvironmentPayload("env_etag"))))
		req.Header.Set("If-Match", `"999"`)
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusPreconditionFailed {
			t.Fatalf("dry-run stale If-Match: got %d, want 412; body=%s", rr.Code, rr.Body.String())
		}
		assertErrorCode(t, rr, "ETAG_MISMATCH")
	})

	// spec: §15.1 line 1213 — DELETE honours If-Match only when present.
	t.Run("DeleteStaleIfMatchIs412", func(t *testing.T) {
		router, store := seedEtagEnvironment(t, "env_del")
		rr := deleteEnvironmentRaw(t, router.Handler(), "env_del", `"999"`)
		if rr.Code != http.StatusPreconditionFailed {
			t.Fatalf("delete stale If-Match: got %d, want 412; body=%s", rr.Code, rr.Body.String())
		}
		assertErrorCode(t, rr, "ETAG_MISMATCH")
		if _, err := store.Get(context.Background(), "acme", "env_del"); err != nil {
			t.Fatalf("environment removed despite 412: %v", err)
		}
	})

	t.Run("DeleteWithoutIfMatchProceeds", func(t *testing.T) {
		router, store := seedEtagEnvironment(t, "env_del")
		rr := deleteEnvironmentRaw(t, router.Handler(), "env_del", "")
		if rr.Code != http.StatusNoContent {
			t.Fatalf("delete without If-Match: got %d, want 204; body=%s", rr.Code, rr.Body.String())
		}
		if _, err := store.Get(context.Background(), "acme", "env_del"); err == nil {
			t.Error("environment must be gone after an unconditional DELETE")
		}
	})
}

// mustJSON marshals v to a bytes.Reader for request bodies.
func mustJSON(v any) *bytes.Reader {
	b, _ := json.Marshal(v)
	return bytes.NewReader(b)
}
