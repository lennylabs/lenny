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
	"github.com/lennylabs/lenny/pkg/gateway/credentialpoolstore"
)

// spec: §15.1 lines 1207-1224 — ETag-based optimistic concurrency for the
// credential-pools admin resource.

// putCredPoolRaw issues a PUT carrying the given If-Match header verbatim,
// bypassing doAdminReq's auto-fetch so the §15.1 ETag preconditions can be
// exercised directly. The body carries TenantID acme so the handler targets
// the fixture tenant.
func putCredPoolRaw(t *testing.T, h http.Handler, name, ifMatch string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPut, "/v1/admin/credential-pools/"+name, bytes.NewReader(b)))
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func deleteCredPoolRaw(t *testing.T, h http.Handler, name, ifMatch string) *httptest.ResponseRecorder {
	t.Helper()
	req := withAdminPrincipal(httptest.NewRequest(http.MethodDelete, "/v1/admin/credential-pools/"+name+"?tenantId=acme", nil))
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func seedEtagCredPool(t *testing.T, name string) (*admin.Router, *credentialpoolstore.Memory) {
	t.Helper()
	router, store := newCredentialPoolAdmin(t)
	rr := doAdminReq(t, router.Handler(), http.MethodPost, "/v1/admin/credential-pools",
		validCredentialPool("acme", name), withAdminPrincipal)
	if rr.Code != http.StatusCreated {
		t.Fatalf("seed pool: %d body=%s", rr.Code, rr.Body.String())
	}
	return router, store
}

func changedCredPool(name string) admin.CredentialPoolPayload {
	p := validCredentialPool("acme", name)
	p.MaxConcurrentSessions = 20
	return p
}

func TestCredentialPoolETagOptimisticConcurrency_spec_15_1_1207(t *testing.T) {
	// spec: §15.1 line 1209 — GET carries the ETag header and per-item etag.
	t.Run("GetCarriesETag", func(t *testing.T) {
		router, _ := seedEtagCredPool(t, "claude")
		g := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/credential-pools/claude?tenantId=acme", nil))
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, g)
		if rr.Code != http.StatusOK {
			t.Fatalf("get: %d body=%s", rr.Code, rr.Body.String())
		}
		if got := rr.Header().Get("ETag"); got != `"1"` {
			t.Errorf("ETag header: got %q, want %q", got, `"1"`)
		}
		var body admin.CredentialPoolPayload
		_ = json.Unmarshal(rr.Body.Bytes(), &body)
		if body.ETag != `"1"` {
			t.Errorf("body etag: got %q, want %q", body.ETag, `"1"`)
		}
	})

	// spec: §15.1 line 1209 — list responses include a per-item ETag.
	t.Run("ListCarriesPerItemETag", func(t *testing.T) {
		router, _ := seedEtagCredPool(t, "claude")
		g := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/credential-pools?tenantId=acme", nil))
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, g)
		if rr.Code != http.StatusOK {
			t.Fatalf("list: %d body=%s", rr.Code, rr.Body.String())
		}
		var page struct {
			Items []admin.CredentialPoolPayload `json:"items"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
			t.Fatalf("decode list: %v body=%s", err, rr.Body.String())
		}
		if len(page.Items) != 1 || page.Items[0].ETag != `"1"` {
			t.Fatalf("items: %+v", page.Items)
		}
	})

	// spec: §15.1 line 1210 — a PUT with no If-Match returns 428 ETAG_REQUIRED.
	t.Run("PutMissingIfMatch", func(t *testing.T) {
		router, _ := seedEtagCredPool(t, "claude")
		rr := putCredPoolRaw(t, router.Handler(), "claude", "", changedCredPool("claude"))
		if rr.Code != http.StatusPreconditionRequired {
			t.Fatalf("status: got %d, want 428; body=%s", rr.Code, rr.Body.String())
		}
		assertErrorCode(t, rr, "ETAG_REQUIRED")
	})

	// spec: §15.1 line 1210 — a malformed If-Match is 400 VALIDATION_ERROR.
	t.Run("PutMalformedIfMatch", func(t *testing.T) {
		for _, bad := range []string{"3", "abc", "W/\"3\"", "*", `"1.5"`} {
			router, _ := seedEtagCredPool(t, "claude")
			rr := putCredPoolRaw(t, router.Handler(), "claude", bad, changedCredPool("claude"))
			if rr.Code != http.StatusBadRequest {
				t.Errorf("If-Match %q: got %d, want 400; body=%s", bad, rr.Code, rr.Body.String())
				continue
			}
			assertErrorCode(t, rr, "VALIDATION_ERROR")
		}
	})

	// spec: §15.1 line 1210 — a stale If-Match is 412 with details.currentEtag.
	t.Run("PutStaleIfMatch", func(t *testing.T) {
		router, _ := seedEtagCredPool(t, "claude")
		rr := putCredPoolRaw(t, router.Handler(), "claude", `"999"`, changedCredPool("claude"))
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
			t.Errorf("code=%q currentEtag=%v", env.Error.Code, env.Error.Details["currentEtag"])
		}
	})

	// spec: §15.1 line 1211 — a matching If-Match succeeds and returns the
	// bumped ETag; a retried PUT with the now-stale tag loses with 412.
	t.Run("PutMatchingIfMatchBumpsETag", func(t *testing.T) {
		router, store := seedEtagCredPool(t, "claude")
		rr := putCredPoolRaw(t, router.Handler(), "claude", `"1"`, changedCredPool("claude"))
		if rr.Code != http.StatusOK {
			t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
		}
		if got := rr.Header().Get("ETag"); got != `"2"` {
			t.Errorf("new ETag header: got %q, want %q", got, `"2"`)
		}
		row, _ := store.Get(context.Background(), "acme", "claude")
		if row.Version != 2 || row.MaxConcurrentSessions != 20 {
			t.Errorf("stored: version=%d maxConcurrent=%d", row.Version, row.MaxConcurrentSessions)
		}
		stale := putCredPoolRaw(t, router.Handler(), "claude", `"1"`, changedCredPool("claude"))
		if stale.Code != http.StatusPreconditionFailed {
			t.Errorf("stale second PUT: got %d, want 412; body=%s", stale.Code, stale.Body.String())
		}
	})

	// spec: §15.1 line 1213 — DELETE honours If-Match only when present.
	t.Run("DeleteStaleIfMatchIs412", func(t *testing.T) {
		router, store := seedEtagCredPool(t, "claude")
		rr := deleteCredPoolRaw(t, router.Handler(), "claude", `"999"`)
		if rr.Code != http.StatusPreconditionFailed {
			t.Fatalf("delete stale If-Match: got %d, want 412; body=%s", rr.Code, rr.Body.String())
		}
		assertErrorCode(t, rr, "ETAG_MISMATCH")
		row, err := store.Get(context.Background(), "acme", "claude")
		if err != nil || !row.IsActive() {
			t.Errorf("pool soft-deleted despite 412: err=%v active=%v", err, row.IsActive())
		}
	})

	t.Run("DeleteWithoutIfMatchProceeds", func(t *testing.T) {
		router, store := seedEtagCredPool(t, "claude")
		rr := deleteCredPoolRaw(t, router.Handler(), "claude", "")
		if rr.Code != http.StatusNoContent {
			t.Fatalf("delete without If-Match: got %d, want 204; body=%s", rr.Code, rr.Body.String())
		}
		row, _ := store.Get(context.Background(), "acme", "claude")
		if row.IsActive() {
			t.Error("pool still active after delete")
		}
	})
}
