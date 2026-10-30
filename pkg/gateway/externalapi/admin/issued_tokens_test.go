// SPDX-License-Identifier: MIT

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
)

// spec: §13.3 operator-initiated token revocation.

type fakeTokenRevoker struct {
	calls                        int
	gotTenant, gotJTI, gotReason string
	err                          error
}

func (f *fakeTokenRevoker) Revoke(_ context.Context, tenantID, jti, reason string, _ time.Time) error {
	f.calls++
	f.gotTenant, f.gotJTI, f.gotReason = tenantID, jti, reason
	return f.err
}

type fakeRevCache struct{ revoked []string }

func (f *fakeRevCache) Revoke(jti string) { f.revoked = append(f.revoked, jti) }

func revokeRouter(revoker admin.IssuedTokenRevoker, cache admin.RevocationCache) *admin.Router {
	return admin.NewRouter(tenantstore.NewMemory(), admin.Options{}).
		WithIssuedTokens(revoker, cache)
}

func revokeReq(t *testing.T, h http.Handler, jti string, body any, asAdmin bool) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost,
		"/v1/admin/issued-tokens/"+jti+"/revoke", bytes.NewReader(b))
	if asAdmin {
		req = withAdminPrincipal(req)
	} else {
		req = withTenantAdminPrincipal(req)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func TestRevokeTokenHappyPath(t *testing.T) {
	revoker := &fakeTokenRevoker{}
	cache := &fakeRevCache{}
	rr := revokeReq(t, revokeRouter(revoker, cache).Handler(), "jti-123",
		admin.RevokeTokenRequest{TenantID: "acme", Reason: "compromised"}, true)

	if rr.Code != http.StatusOK {
		t.Fatalf("revoke: status %d, body %s", rr.Code, rr.Body.String())
	}
	if revoker.calls != 1 || revoker.gotJTI != "jti-123" ||
		revoker.gotTenant != "acme" || revoker.gotReason != "compromised" {
		t.Errorf("revoker call: %+v", revoker)
	}
	if len(cache.revoked) != 1 || cache.revoked[0] != "jti-123" {
		t.Errorf("cache not updated: %v", cache.revoked)
	}
}

func TestRevokeTokenNotFound(t *testing.T) {
	revoker := &fakeTokenRevoker{err: issuedtokenstore.ErrNotFound}
	cache := &fakeRevCache{}
	rr := revokeReq(t, revokeRouter(revoker, cache).Handler(), "jti-absent",
		admin.RevokeTokenRequest{TenantID: "acme"}, true)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown jti: status %d, want 404", rr.Code)
	}
	if len(cache.revoked) != 0 {
		t.Error("the cache must not be updated when the token was not found")
	}
}

func TestRevokeTokenRequiresTenantID(t *testing.T) {
	revoker := &fakeTokenRevoker{}
	rr := revokeReq(t, revokeRouter(revoker, &fakeRevCache{}).Handler(), "jti-1",
		admin.RevokeTokenRequest{Reason: "x"}, true)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("missing tenantId: status %d, want 400", rr.Code)
	}
	if revoker.calls != 0 {
		t.Error("the revoker must not be called without a tenantId")
	}
}

func TestRevokeTokenRequiresPlatformAdmin(t *testing.T) {
	revoker := &fakeTokenRevoker{}
	rr := revokeReq(t, revokeRouter(revoker, &fakeRevCache{}).Handler(), "jti-1",
		admin.RevokeTokenRequest{TenantID: "acme"}, false)

	if rr.Code != http.StatusForbidden {
		t.Errorf("tenant-admin revoke: status %d, want 403", rr.Code)
	}
	if revoker.calls != 0 {
		t.Error("the revoker must not be called for a non-platform-admin")
	}
}
