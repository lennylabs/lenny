// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
	"github.com/lennylabs/lenny/pkg/kms/rekey"
)

// fakeRekeyer is a CredentialRekeyer test double. Run reports a fixed
// summary; Verify reports a fixed stale count (and ErrRekeyIncomplete
// when non-zero), or a transport error when verifyErr is set.
type fakeRekeyer struct {
	summary   rekey.Summary
	verifyN   int
	verifyErr error
	ranTenant string
}

func (f *fakeRekeyer) Run(_ context.Context, tenantID string) (rekey.Summary, error) {
	f.ranTenant = tenantID
	s := f.summary
	s.TenantID = tenantID
	return s, nil
}

func (f *fakeRekeyer) Verify(_ context.Context, _ string) (int, error) {
	if f.verifyErr != nil {
		return 0, f.verifyErr
	}
	if f.verifyN > 0 {
		return f.verifyN, rekey.ErrRekeyIncomplete
	}
	return 0, nil
}

func rekeyRouter(rk admin.CredentialRekeyer) *admin.Router {
	return admin.NewRouter(tenantstore.NewMemory(), admin.Options{}).WithCredentialRekey(rk)
}

// spec: §4.9.1 lines 1718-1724 — POST runs the re-encryption loop for the
// path tenant and returns the per-store counts plus the verification
// result.
func TestCredentialRekey_RunReturnsSummary(t *testing.T) {
	rk := &fakeRekeyer{summary: rekey.Summary{
		Rekeyed:  7,
		Stale:    0,
		Verified: true,
		Results: []rekey.Result{
			{Store: "credentials", Rekeyed: 5, Stale: 0},
			{Store: "connector_credentials", Rekeyed: 2, Stale: 0},
		},
	}}
	rr := doAdminReq(t, rekeyRouter(rk).Handler(), http.MethodPost,
		"/v1/admin/tenants/acme/credential-rekey", nil, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if rk.ranTenant != "acme" {
		t.Fatalf("ran for tenant %q, want acme", rk.ranTenant)
	}
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["rekeyed"].(float64) != 7 || got["verified"].(bool) != true {
		t.Fatalf("body = %v, want rekeyed 7 verified true", got)
	}
	if results, ok := got["results"].([]any); !ok || len(results) != 2 {
		t.Fatalf("results = %v, want 2 entries", got["results"])
	}
}

// spec: §4.9.1 lines 1723-1724 — GET runs the verification query; a
// non-zero stale count is the expected "incomplete" signal and returns
// 200 with verified:false, not a 5xx.
func TestCredentialRekey_StatusIncompleteIsNotError(t *testing.T) {
	rk := &fakeRekeyer{verifyN: 3}
	rr := doAdminReq(t, rekeyRouter(rk).Handler(), http.MethodGet,
		"/v1/admin/tenants/acme/credential-rekey", nil, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var got map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got["stale"].(float64) != 3 || got["verified"].(bool) != false {
		t.Fatalf("body = %v, want stale 3 verified false", got)
	}
}

// spec: §4.9.1 line 1723 — GET reports verified once no row remains
// below the current KEK version.
func TestCredentialRekey_StatusVerified(t *testing.T) {
	rk := &fakeRekeyer{verifyN: 0}
	rr := doAdminReq(t, rekeyRouter(rk).Handler(), http.MethodGet,
		"/v1/admin/tenants/acme/credential-rekey", nil, withAdminPrincipal)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var got map[string]any
	_ = json.Unmarshal(rr.Body.Bytes(), &got)
	if got["verified"].(bool) != true {
		t.Fatalf("verified = %v, want true", got["verified"])
	}
}

// The re-key admin surface is platform-admin gated: a plain user is
// rejected before the handler runs.
func TestCredentialRekey_RequiresAdmin(t *testing.T) {
	rk := &fakeRekeyer{}
	rr := doAdminReq(t, rekeyRouter(rk).Handler(), http.MethodPost,
		"/v1/admin/tenants/acme/credential-rekey", nil, withUserPrincipal)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
	if rk.ranTenant != "" {
		t.Fatal("handler ran despite forbidden principal")
	}
}

// Without a wired rekeyer the routes are absent (404), keeping the
// dev-mode in-memory posture free of a KEK-rotation surface.
func TestCredentialRekey_AbsentWithoutRekeyer(t *testing.T) {
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{})
	rr := doAdminReq(t, router.Handler(), http.MethodPost,
		"/v1/admin/tenants/acme/credential-rekey", nil, withAdminPrincipal)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}
