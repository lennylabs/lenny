// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
)

// putPoolRaw issues a PUT carrying the given If-Match header verbatim,
// bypassing poolReq's auto-fetch so the §15.1 ETag preconditions can be
// exercised directly. An empty ifMatch sends no header.
func putPoolRaw(t *testing.T, h http.Handler, name, ifMatch string, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodPut, "/v1/admin/pools/"+name, bytes.NewReader(b)))
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func assertErrorCode(t *testing.T, rr *httptest.ResponseRecorder, want string) {
	t.Helper()
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &env)
	if env.Error.Code != want {
		t.Errorf("error code: got %q, want %q; body=%s", env.Error.Code, want, rr.Body.String())
	}
}

func seedEtagPool(t *testing.T) (*admin.Router, *poolstore.Memory) {
	t.Helper()
	router, pools, runtimes, _ := newPoolAdmin(t)
	if err := runtimes.Create(context.Background(), runtimestore.Runtime{Name: "echo"}); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	if err := pools.Create(context.Background(), poolstore.Pool{Name: "p", RuntimeRef: "echo"}); err != nil {
		t.Fatalf("seed pool: %v", err)
	}
	return router, pools
}

// spec: §15.1 line 1209 — GET on an admin resource carries the ETag
// header set to the resource's current version (the pool_config_generation).
func TestPoolGetCarriesETag_spec_15_1_1209(t *testing.T) {
	router, _ := seedEtagPool(t)
	g := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/pools/p", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, g)
	if rr.Code != http.StatusOK {
		t.Fatalf("get: %d body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("ETag"); got != `"1"` {
		t.Errorf("ETag header: got %q, want %q", got, `"1"`)
	}
	var body admin.PoolPayload
	_ = json.Unmarshal(rr.Body.Bytes(), &body)
	if body.ETag != `"1"` {
		t.Errorf("body etag: got %q, want %q", body.ETag, `"1"`)
	}
}

// spec: §15.1 line 1209 — list responses include a per-item ETag.
func TestPoolListCarriesPerItemETag_spec_15_1_1209(t *testing.T) {
	router, _ := seedEtagPool(t)
	g := withAdminPrincipal(httptest.NewRequest(http.MethodGet, "/v1/admin/pools", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, g)
	if rr.Code != http.StatusOK {
		t.Fatalf("list: %d body=%s", rr.Code, rr.Body.String())
	}
	var page struct {
		Pools []admin.PoolPayload `json:"items"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode list: %v body=%s", err, rr.Body.String())
	}
	if len(page.Pools) != 1 {
		t.Fatalf("pools: %d", len(page.Pools))
	}
	if page.Pools[0].ETag != `"1"` {
		t.Errorf("per-item etag: got %q, want %q", page.Pools[0].ETag, `"1"`)
	}
}

// spec: §15.1 line 1210 — a PUT with no If-Match returns 428 ETAG_REQUIRED.
func TestPoolUpdateMissingIfMatch_spec_15_1_1210(t *testing.T) {
	router, _ := seedEtagPool(t)
	rr := putPoolRaw(t, router.Handler(), "p", "", admin.UpdatePoolRequest{})
	if rr.Code != http.StatusPreconditionRequired {
		t.Fatalf("status: got %d, want 428; body=%s", rr.Code, rr.Body.String())
	}
	assertErrorCode(t, rr, "ETAG_REQUIRED")
}

// spec: §15.1 line 1210 — a malformed If-Match (weak validator, unquoted,
// non-decimal, or `*`) is 400 VALIDATION_ERROR naming the header.
func TestPoolUpdateMalformedIfMatch_spec_15_1_1210(t *testing.T) {
	for _, bad := range []string{"1", "abc", "W/\"1\"", "*", `"1.5"`, `""`} {
		router, _ := seedEtagPool(t)
		rr := putPoolRaw(t, router.Handler(), "p", bad, admin.UpdatePoolRequest{})
		if rr.Code != http.StatusBadRequest {
			t.Errorf("If-Match %q: got %d, want 400; body=%s", bad, rr.Code, rr.Body.String())
			continue
		}
		assertErrorCode(t, rr, "VALIDATION_ERROR")
	}
}

// spec: §15.1 line 1210 — a stale If-Match is 412 ETAG_MISMATCH and
// carries details.currentEtag.
func TestPoolUpdateStaleIfMatch_spec_15_1_1210(t *testing.T) {
	router, _ := seedEtagPool(t)
	rr := putPoolRaw(t, router.Handler(), "p", `"99"`, admin.UpdatePoolRequest{})
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
}

// spec: §15.1 line 1210 — a matching If-Match succeeds and the response
// returns the new (incremented) ETag.
func TestPoolUpdateMatchingIfMatchBumpsETag_spec_15_1_1210(t *testing.T) {
	router, store := seedEtagPool(t)
	rr := putPoolRaw(t, router.Handler(), "p", `"1"`, admin.UpdatePoolRequest{ResourceClass: ptr("large")})
	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if got := rr.Header().Get("ETag"); got != `"2"` {
		t.Errorf("new ETag: got %q, want %q", got, `"2"`)
	}
	row, _ := store.Get(context.Background(), "p")
	if row.Generation != 2 || row.ResourceClass != "large" {
		t.Errorf("stored: gen=%d class=%s", row.Generation, row.ResourceClass)
	}
	// A second PUT reusing the now-stale "1" must lose the race with 412.
	stale := putPoolRaw(t, router.Handler(), "p", `"1"`, admin.UpdatePoolRequest{ResourceClass: ptr("small")})
	if stale.Code != http.StatusPreconditionFailed {
		t.Errorf("stale second PUT: got %d, want 412; body=%s", stale.Code, stale.Body.String())
	}
}

// spec: §15.1 line 1210 — the not-found check precedes the precondition
// so a stale-handle client cannot probe pool existence via the ETag path.
func TestPoolUpdateMissingPoolIsNotFoundBeforeETag(t *testing.T) {
	router, _ := seedEtagPool(t)
	rr := putPoolRaw(t, router.Handler(), "absent", "", admin.UpdatePoolRequest{})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
}
