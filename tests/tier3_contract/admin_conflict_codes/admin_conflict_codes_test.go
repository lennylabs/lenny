//go:build contract

// SPDX-License-Identifier: MIT

// Tier-3 contract test for the §15.1 error-code catalog over the admin REST
// wire. The HTTP error envelope is a wire contract: every 409 the admin
// surface returns must carry a code the §15.1 catalog enumerates, so a
// client (and the §25.2 errorclassify retry path) can branch on it. ADM-7
// renamed the uncataloged `RESOURCE_CONFLICT` string to the two cataloged
// codes the catalog defines for the two distinct 409 conditions: a
// duplicate-identifier conflict returns `RESOURCE_ALREADY_EXISTS` and a
// state conflict returns `INVALID_STATE_TRANSITION`. This drives the
// production admin handlers (router → store → JSON error encoder) over
// httptest so a code-string regression at any 409 emit site surfaces here,
// and confirms `errorclassify.ClassifyStatus` yields the same
// (PERMANENT, not-retryable) verdict the catalog assigns both codes.
//
// spec: §15.1 (RESOURCE_ALREADY_EXISTS line 983, INVALID_STATE_TRANSITION
// line 981), §25.2 (errorclassify category, 409 is PERMANENT not retryable).
package admin_conflict_codes_test

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
	"github.com/lennylabs/lenny/pkg/gateway/errorclassify"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/runtimeupgrade"
	"github.com/lennylabs/lenny/pkg/gateway/runtimeupgradestore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// fixedClock pins the router clock so the wire output is deterministic.
func fixedClock() time.Time { return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) }

// adminPrincipal attaches a platform-admin principal so the §15.1 admin
// handlers' role gate passes and the request reaches the store, which is
// where the duplicate and state conflicts originate.
func adminPrincipal(req *http.Request) *http.Request {
	return req.WithContext(authmw.WithPrincipal(req.Context(), authmw.Principal{
		TenantID: "platform",
		Subject:  "platform-admin@acme.com",
		Roles:    []pkgauth.Role{pkgauth.RolePlatformAdmin},
	}))
}

// serve runs one admin request and returns the recorder plus the decoded
// `error.code` so the test asserts on the wire code string directly.
func serve(t *testing.T, h http.Handler, method, path string, body any) (*httptest.ResponseRecorder, string) {
	t.Helper()
	var buf *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		buf = bytes.NewReader(b)
	} else {
		buf = bytes.NewReader(nil)
	}
	req := adminPrincipal(httptest.NewRequest(method, path, buf))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if rr.Body.Len() > 0 {
		_ = json.Unmarshal(rr.Body.Bytes(), &env)
	}
	return rr, env.Error.Code
}

// TestAdminDuplicateCreateReturnsResourceAlreadyExists pins the
// duplicate-identifier 409 to the cataloged `RESOURCE_ALREADY_EXISTS`
// (ADM-7). A second tenant create with the same id is the canonical
// duplicate-identifier conflict; the renamed emit sites across the admin
// surface share this code. The test also runs errorclassify over the wire
// code to confirm the §25.2 verdict is (PERMANENT, not retryable), so a
// retry loop will not re-issue the doomed create.
//
// diagnosis: a failure means an admin duplicate-create returns a 409 code
// the §15.1 catalog does not enumerate (the pre-ADM-7 `RESOURCE_CONFLICT`),
// so a client cannot branch on it and errorclassify falls through the
// status default rather than the catalog entry.
// spec: 15.1 (RESOURCE_ALREADY_EXISTS line 983), 25.2 (errorclassify 409)
func TestAdminDuplicateCreateReturnsResourceAlreadyExists(t *testing.T) {
	h := admin.NewRouter(tenantstore.NewMemory(), admin.Options{Clock: fixedClock}).Handler()

	if rr, _ := serve(t, h, http.MethodPost, "/v1/admin/tenants", admin.TenantPayload{ID: "acme"}); rr.Code != http.StatusCreated {
		t.Fatalf("first create: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}

	rr, code := serve(t, h, http.MethodPost, "/v1/admin/tenants", admin.TenantPayload{ID: "acme"})
	if rr.Code != http.StatusConflict {
		t.Fatalf("duplicate create: got %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	if code != "RESOURCE_ALREADY_EXISTS" {
		t.Fatalf("duplicate create code = %q, want RESOURCE_ALREADY_EXISTS; body=%s", code, rr.Body.String())
	}
	if cat, retryable := errorclassify.ClassifyStatus(code, rr.Code); cat != errorclassify.CategoryPermanent || retryable {
		t.Fatalf("errorclassify(%s, %d) = (%s, %v), want (PERMANENT, false)", code, rr.Code, cat, retryable)
	}
}

// TestAdminStateConflictReturnsInvalidStateTransition pins the
// state-conflict 409 to the cataloged `INVALID_STATE_TRANSITION` (ADM-7). A
// second pool-upgrade start while an upgrade is active is the
// ErrUpgradeActive state conflict (distinct from a duplicate identifier);
// the catalog assigns it INVALID_STATE_TRANSITION. The test confirms the
// errorclassify verdict is (PERMANENT, not retryable).
//
// diagnosis: a failure means an admin state-conflict 409 returns a code the
// §15.1 catalog does not enumerate (the pre-ADM-7 `RESOURCE_CONFLICT`), so
// a client cannot distinguish a state conflict from a duplicate-identifier
// conflict and branch its recovery accordingly.
// spec: 15.1 (INVALID_STATE_TRANSITION line 981), 25.2 (errorclassify 409)
func TestAdminStateConflictReturnsInvalidStateTransition(t *testing.T) {
	store := runtimeupgradestore.NewMemory().WithClock(fixedClock)
	pools := fakeUpgradePool{specs: map[string][]byte{"claude-worker": []byte(`{"minWarm":2}`)}}
	mgr := runtimeupgrade.NewManager(
		store,
		runtimeupgrade.WithPoolReader(pools),
		runtimeupgrade.WithClock(fixedClock),
	)
	h := admin.NewRouter(tenantstore.NewMemory(), admin.Options{Clock: fixedClock}).
		WithRuntimeUpgrade(mgr).Handler()
	base := "/v1/admin/pools/claude-worker/upgrade"

	if rr, _ := serve(t, h, http.MethodPost, base+"/start", admin.StartUpgradeRequest{NewImage: "v1"}); rr.Code != http.StatusOK {
		t.Fatalf("first start: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	rr, code := serve(t, h, http.MethodPost, base+"/start", admin.StartUpgradeRequest{NewImage: "v2"})
	if rr.Code != http.StatusConflict {
		t.Fatalf("second start: got %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	if code != "INVALID_STATE_TRANSITION" {
		t.Fatalf("second start code = %q, want INVALID_STATE_TRANSITION; body=%s", code, rr.Body.String())
	}
	if cat, retryable := errorclassify.ClassifyStatus(code, rr.Code); cat != errorclassify.CategoryPermanent || retryable {
		t.Fatalf("errorclassify(%s, %d) = (%s, %v), want (PERMANENT, false)", code, rr.Code, cat, retryable)
	}
}

// fakeUpgradePool is the minimal PoolReader the upgrade manager needs to
// resolve a pool spec for the start request.
type fakeUpgradePool struct{ specs map[string][]byte }

func (f fakeUpgradePool) PoolSpec(_ context.Context, pool string) ([]byte, bool, error) {
	spec, ok := f.specs[pool]
	return spec, ok, nil
}
