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
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
)

// spec: §15.1 lines 1207-1224 — ETag-based optimistic concurrency for the
// runtimes admin resource.

func putRuntimeRaw(t *testing.T, h http.Handler, name, ifMatch string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPut, "/v1/admin/runtimes/"+name, bytes.NewReader(b)))
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func deleteRuntimeRaw(t *testing.T, h http.Handler, name, ifMatch string) *httptest.ResponseRecorder {
	t.Helper()
	req := withAdminPrincipal(httptest.NewRequest(http.MethodDelete, "/v1/admin/runtimes/"+name, nil))
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func seedEtagRuntime(t *testing.T, name string) (*admin.Router, runtimestore.Store) {
	t.Helper()
	router, store, _ := newRuntimeAdmin(t)
	if err := store.Create(context.Background(), runtimestore.Runtime{
		Name:             name,
		Type:             runtimestore.TypeAgent,
		Image:            "ghcr.io/acme/agent@sha256:abcdef",
		ExecutionMode:    runtimestore.ExecutionModeSession,
		IsolationProfile: "sandboxed",
		IntegrationLevel: runtimestore.IntegrationLevelFull,
	}); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	return router, store
}

// TestRuntimeETagOptimisticConcurrency_spec_15_1_1207 covers the §15.1 lines
// 1207-1224 ETag optimistic-concurrency contract for the runtimes resource,
// including the interaction with the §15.1 line 1140 ?dryRun=true branch.
func TestRuntimeETagOptimisticConcurrency_spec_15_1_1207(t *testing.T) {
	desc := func(s string) *string { return &s }

	t.Run("GetCarriesETag", func(t *testing.T) {
		router, _ := seedEtagRuntime(t, "rt_etag")
		code, etag := getETagHeader(t, router.Handler(), "/v1/admin/runtimes/rt_etag")
		if code != http.StatusOK || etag != `"1"` {
			t.Fatalf("get: code=%d etag=%q, want 200 / \"1\"", code, etag)
		}
		g := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/runtimes/rt_etag", nil))
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, g)
		var body admin.RuntimePayload
		_ = json.Unmarshal(rr.Body.Bytes(), &body)
		if body.ETag != `"1"` {
			t.Errorf("body etag: got %q, want \"1\"", body.ETag)
		}
	})

	t.Run("ListCarriesPerItemETag", func(t *testing.T) {
		router, _ := seedEtagRuntime(t, "rt_etag")
		g := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/runtimes", nil))
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, g)
		if rr.Code != http.StatusOK {
			t.Fatalf("list: %d body=%s", rr.Code, rr.Body.String())
		}
		var page struct {
			Items []admin.RuntimePayload `json:"items"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
			t.Fatalf("decode list: %v body=%s", err, rr.Body.String())
		}
		if len(page.Items) != 1 || page.Items[0].ETag != `"1"` {
			t.Fatalf("items: %+v", page.Items)
		}
	})

	t.Run("PutMissingIfMatchIs428", func(t *testing.T) {
		router, _ := seedEtagRuntime(t, "rt_etag")
		rr := putRuntimeRaw(t, router.Handler(), "rt_etag", "", admin.UpdateRuntimeRequest{Description: desc("x")})
		if rr.Code != http.StatusPreconditionRequired {
			t.Fatalf("status: got %d, want 428; body=%s", rr.Code, rr.Body.String())
		}
		assertErrorCode(t, rr, "ETAG_REQUIRED")
	})

	// spec: §15.1 lines 1140, 1202-1203 — ?dryRun=true combined with a
	// missing If-Match still returns 428 (the precondition is checked
	// before the dry-run branch).
	t.Run("DryRunMissingIfMatchIs428", func(t *testing.T) {
		router, _ := seedEtagRuntime(t, "rt_etag")
		b, _ := json.Marshal(admin.UpdateRuntimeRequest{Description: desc("x")})
		req := withAdminPrincipal(httptest.NewRequest(http.MethodPut, "/v1/admin/runtimes/rt_etag?dryRun=true", bytes.NewReader(b)))
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusPreconditionRequired {
			t.Fatalf("dryRun missing If-Match: got %d, want 428; body=%s", rr.Code, rr.Body.String())
		}
	})

	// spec: §15.1 lines 1202-1203 — ?dryRun=true with a stale If-Match
	// returns 412 without mutating the resource.
	t.Run("DryRunStaleIfMatchIs412", func(t *testing.T) {
		router, store := seedEtagRuntime(t, "rt_etag")
		b, _ := json.Marshal(admin.UpdateRuntimeRequest{Description: desc("x")})
		req := withAdminPrincipal(httptest.NewRequest(http.MethodPut, "/v1/admin/runtimes/rt_etag?dryRun=true", bytes.NewReader(b)))
		req.Header.Set("If-Match", `"999"`)
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusPreconditionFailed {
			t.Fatalf("dryRun stale If-Match: got %d, want 412; body=%s", rr.Code, rr.Body.String())
		}
		row, _ := store.Get(context.Background(), "rt_etag")
		if row.Version != 1 {
			t.Errorf("dry run mutated version: got %d, want 1", row.Version)
		}
	})

	t.Run("PutMalformedIfMatchIs400", func(t *testing.T) {
		for _, bad := range []string{"3", "abc", "W/\"3\"", "*", `"1.5"`} {
			router, _ := seedEtagRuntime(t, "rt_etag")
			rr := putRuntimeRaw(t, router.Handler(), "rt_etag", bad, admin.UpdateRuntimeRequest{Description: desc("x")})
			if rr.Code != http.StatusBadRequest {
				t.Errorf("If-Match %q: got %d, want 400; body=%s", bad, rr.Code, rr.Body.String())
			}
		}
	})

	t.Run("PutStaleIfMatchIs412", func(t *testing.T) {
		router, _ := seedEtagRuntime(t, "rt_etag")
		rr := putRuntimeRaw(t, router.Handler(), "rt_etag", `"999"`, admin.UpdateRuntimeRequest{Description: desc("x")})
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
		if env.Error.Code != "ETAG_MISMATCH" || env.Error.Details["currentEtag"] != `"1"` {
			t.Errorf("got code=%q currentEtag=%v", env.Error.Code, env.Error.Details["currentEtag"])
		}
	})

	t.Run("PutMatchingIfMatchBumpsETag", func(t *testing.T) {
		router, store := seedEtagRuntime(t, "rt_etag")
		rr := putRuntimeRaw(t, router.Handler(), "rt_etag", `"1"`, admin.UpdateRuntimeRequest{Description: desc("updated")})
		if rr.Code != http.StatusOK {
			t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
		}
		if got := rr.Header().Get("ETag"); got != `"2"` {
			t.Errorf("new ETag header: got %q, want \"2\"", got)
		}
		var body admin.RuntimePayload
		_ = json.Unmarshal(rr.Body.Bytes(), &body)
		if body.ETag != `"2"` {
			t.Errorf("body etag: got %q, want \"2\"", body.ETag)
		}
		row, _ := store.Get(context.Background(), "rt_etag")
		if row.Version != 2 || row.Description != "updated" {
			t.Errorf("stored: version=%d description=%q", row.Version, row.Description)
		}
		stale := putRuntimeRaw(t, router.Handler(), "rt_etag", `"1"`, admin.UpdateRuntimeRequest{Description: desc("again")})
		if stale.Code != http.StatusPreconditionFailed {
			t.Errorf("stale second PUT: got %d, want 412; body=%s", stale.Code, stale.Body.String())
		}
	})

	t.Run("DeleteStaleIfMatchIs412", func(t *testing.T) {
		router, store := seedEtagRuntime(t, "rt_del")
		rr := deleteRuntimeRaw(t, router.Handler(), "rt_del", `"999"`)
		if rr.Code != http.StatusPreconditionFailed {
			t.Fatalf("delete stale If-Match: got %d, want 412; body=%s", rr.Code, rr.Body.String())
		}
		assertErrorCode(t, rr, "ETAG_MISMATCH")
		row, err := store.Get(context.Background(), "rt_del")
		if err != nil || !row.IsActive() {
			t.Fatalf("runtime deleted despite 412: active=%v err=%v", row.IsActive(), err)
		}
	})

	t.Run("DeleteWithoutIfMatchProceeds", func(t *testing.T) {
		router, store := seedEtagRuntime(t, "rt_del")
		rr := deleteRuntimeRaw(t, router.Handler(), "rt_del", "")
		if rr.Code != http.StatusNoContent {
			t.Fatalf("delete without If-Match: got %d, want 204; body=%s", rr.Code, rr.Body.String())
		}
		row, _ := store.Get(context.Background(), "rt_del")
		if row.IsActive() {
			t.Error("runtime should be soft-deleted after delete")
		}
	})
}
