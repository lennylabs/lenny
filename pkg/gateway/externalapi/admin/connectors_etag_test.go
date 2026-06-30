// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
)

// spec: §15.1 lines 1207-1224 — ETag-based optimistic concurrency for the
// connectors admin resource.

// putConnectorRaw issues a PUT carrying the given If-Match header verbatim,
// bypassing connReq's auto-fetch so the §15.1 ETag preconditions can be
// exercised directly. An empty ifMatch sends no header. The path carries
// tenant_id=acme so the platform-admin handler targets the fixture tenant.
func putConnectorRaw(t *testing.T, h http.Handler, id, ifMatch string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPut, "/v1/admin/connectors/"+id+"?tenant_id=acme", bytes.NewReader(b)))
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// deleteConnectorRaw issues a DELETE carrying the given If-Match header
// verbatim. An empty ifMatch sends no header.
func deleteConnectorRaw(t *testing.T, h http.Handler, id, ifMatch string) *httptest.ResponseRecorder {
	t.Helper()
	req := withAdminPrincipal(httptest.NewRequest(http.MethodDelete, "/v1/admin/connectors/"+id+"?tenant_id=acme", nil))
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func seedEtagConnector(t *testing.T, id string) (*admin.Router, *connectorstore.Memory) {
	t.Helper()
	router, store, _ := newConnectorAdmin(t)
	if err := store.Create(context.Background(), connectorstore.Connector{
		TenantID:     "acme",
		ID:           id,
		DisplayName:  "GitHub",
		MCPServerURL: "https://mcp.github.com",
		Transport:    "streamable_http",
		Visibility:   "tenant",
	}); err != nil {
		t.Fatalf("seed connector: %v", err)
	}
	return router, store
}

func validConnectorUpdate() admin.ConnectorPayload {
	return admin.ConnectorPayload{DisplayName: "GitHub Renamed"}
}

func TestConnectorETagOptimisticConcurrency_spec_15_1_1207(t *testing.T) {
	// spec: §15.1 line 1209 — single-item GET carries the ETag header and the
	// body carries the per-item etag field.
	t.Run("GetCarriesETag", func(t *testing.T) {
		router, _ := seedEtagConnector(t, "github")
		g := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/connectors/github?tenant_id=acme", nil))
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, g)
		if rr.Code != http.StatusOK {
			t.Fatalf("get: %d body=%s", rr.Code, rr.Body.String())
		}
		if got := rr.Header().Get("ETag"); got != `"1"` {
			t.Errorf("ETag header: got %q, want %q", got, `"1"`)
		}
		var body admin.ConnectorPayload
		_ = json.Unmarshal(rr.Body.Bytes(), &body)
		if body.ETag != `"1"` {
			t.Errorf("body etag: got %q, want %q", body.ETag, `"1"`)
		}
	})

	// spec: §15.1 line 1209 — list responses include a per-item ETag.
	t.Run("ListCarriesPerItemETag", func(t *testing.T) {
		router, _ := seedEtagConnector(t, "github")
		g := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/connectors?tenant_id=acme", nil))
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, g)
		if rr.Code != http.StatusOK {
			t.Fatalf("list: %d body=%s", rr.Code, rr.Body.String())
		}
		var page struct {
			Items []admin.ConnectorPayload `json:"items"`
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
		router, _ := seedEtagConnector(t, "github")
		rr := putConnectorRaw(t, router.Handler(), "github", "", validConnectorUpdate())
		if rr.Code != http.StatusPreconditionRequired {
			t.Fatalf("status: got %d, want 428; body=%s", rr.Code, rr.Body.String())
		}
		assertErrorCode(t, rr, "ETAG_REQUIRED")
	})

	// spec: §15.1 line 1210 — a malformed If-Match is 400 VALIDATION_ERROR.
	t.Run("PutMalformedIfMatch", func(t *testing.T) {
		for _, bad := range []string{"3", "abc", "W/\"3\"", "*", `"1.5"`} {
			router, _ := seedEtagConnector(t, "github")
			rr := putConnectorRaw(t, router.Handler(), "github", bad, validConnectorUpdate())
			if rr.Code != http.StatusBadRequest {
				t.Errorf("If-Match %q: got %d, want 400; body=%s", bad, rr.Code, rr.Body.String())
				continue
			}
			assertErrorCode(t, rr, "VALIDATION_ERROR")
		}
	})

	// spec: §15.1 line 1210 — a stale If-Match is 412 ETAG_MISMATCH and carries
	// details.currentEtag set to the live ETag.
	t.Run("PutStaleIfMatch", func(t *testing.T) {
		router, _ := seedEtagConnector(t, "github")
		rr := putConnectorRaw(t, router.Handler(), "github", `"999"`, validConnectorUpdate())
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

	// spec: §15.1 line 1211 — a matching If-Match succeeds and returns the new
	// (incremented) ETag; a retried PUT with the now-stale tag loses with 412.
	t.Run("PutMatchingIfMatchBumpsETag", func(t *testing.T) {
		router, store := seedEtagConnector(t, "github")
		rr := putConnectorRaw(t, router.Handler(), "github", `"1"`, validConnectorUpdate())
		if rr.Code != http.StatusOK {
			t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
		}
		if got := rr.Header().Get("ETag"); got != `"2"` {
			t.Errorf("new ETag header: got %q, want %q", got, `"2"`)
		}
		var body admin.ConnectorPayload
		_ = json.Unmarshal(rr.Body.Bytes(), &body)
		if body.ETag != `"2"` || body.DisplayName != "GitHub Renamed" {
			t.Errorf("body: etag=%q displayName=%q", body.ETag, body.DisplayName)
		}
		row, _ := store.Get(context.Background(), "acme", "github")
		if row.Version != 2 {
			t.Errorf("stored version=%d, want 2", row.Version)
		}
		stale := putConnectorRaw(t, router.Handler(), "github", `"1"`, validConnectorUpdate())
		if stale.Code != http.StatusPreconditionFailed {
			t.Errorf("stale second PUT: got %d, want 412; body=%s", stale.Code, stale.Body.String())
		}
	})

	// spec: §15.1 line 1140 + 1207 — dryRun=true combined with a stale If-Match
	// still fails the precondition before the preview.
	t.Run("DryRunStaleIfMatchIs412", func(t *testing.T) {
		router, _ := seedEtagConnector(t, "github")
		b, _ := json.Marshal(validConnectorUpdate())
		req := withAdminPrincipal(httptest.NewRequest(http.MethodPut, "/v1/admin/connectors/github?dryRun=true&tenant_id=acme", bytes.NewReader(b)))
		req.Header.Set("If-Match", `"999"`)
		rr := httptest.NewRecorder()
		router.Handler().ServeHTTP(rr, req)
		if rr.Code != http.StatusPreconditionFailed {
			t.Fatalf("dryRun stale If-Match: got %d, want 412; body=%s", rr.Code, rr.Body.String())
		}
	})

	// spec: §15.1 line 1213 — DELETE honours If-Match only when present: a
	// stale tag returns 412, an absent header proceeds.
	t.Run("DeleteStaleIfMatchIs412", func(t *testing.T) {
		router, store := seedEtagConnector(t, "github")
		rr := deleteConnectorRaw(t, router.Handler(), "github", `"999"`)
		if rr.Code != http.StatusPreconditionFailed {
			t.Fatalf("delete stale If-Match: got %d, want 412; body=%s", rr.Code, rr.Body.String())
		}
		assertErrorCode(t, rr, "ETAG_MISMATCH")
		row, err := store.Get(context.Background(), "acme", "github")
		if err != nil || !row.IsActive() {
			t.Errorf("connector soft-deleted despite 412: err=%v active=%v", err, row.IsActive())
		}
	})

	t.Run("DeleteWithoutIfMatchProceeds", func(t *testing.T) {
		router, store := seedEtagConnector(t, "github")
		rr := deleteConnectorRaw(t, router.Handler(), "github", "")
		if rr.Code != http.StatusNoContent {
			t.Fatalf("delete without If-Match: got %d, want 204; body=%s", rr.Code, rr.Body.String())
		}
		row, _ := store.Get(context.Background(), "acme", "github")
		if row.IsActive() {
			t.Error("connector still active after delete")
		}
	})
}
