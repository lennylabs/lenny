// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// fakeLeaseDenials records ClearSubtreeDenial calls and returns scripted
// results so the §15.1 handler's found/error branches can be exercised.
type fakeLeaseDenials struct {
	calls [][2]string
	found bool
	err   error
}

func (f *fakeLeaseDenials) ClearSubtreeDenial(_ context.Context, root, sub string) (bool, error) {
	f.calls = append(f.calls, [2]string{root, sub})
	return f.found, f.err
}

func newLeaseDenialAdmin(t *testing.T, clearer admin.LeaseDenialClearer) (*admin.Router, *recordingAudit) {
	t.Helper()
	audit := &recordingAudit{}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Audit: audit,
	}).WithLeaseDenials(clearer)
	return router, audit
}

const denialPath = "/v1/admin/trees/root-1/subtrees/sub-9/extension-denial"

// spec: §8.6 line 735; §15.1 line 868 — platform-admin clears a subtree
// denial; the handler returns 204, forwards both path ids to the
// clearer, and records the §10.6 admin audit row.
func TestClearExtensionDenial_PlatformAdmin_Spec15_1(t *testing.T) {
	clearer := &fakeLeaseDenials{found: true}
	router, audit := newLeaseDenialAdmin(t, clearer)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodDelete, denialPath, nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rr.Code, rr.Body.String())
	}
	if len(clearer.calls) != 1 || clearer.calls[0] != [2]string{"root-1", "sub-9"} {
		t.Fatalf("clearer calls = %v, want one (root-1, sub-9)", clearer.calls)
	}
	ev := audit.snapshot()
	if len(ev) != 1 || ev[0].Type != "admin.delegation.extension_denial_cleared" {
		t.Fatalf("audit = %+v, want one extension_denial_cleared event", ev)
	}
	if ev[0].Detail["root_session_id"] != "root-1" || ev[0].Detail["session_id"] != "sub-9" {
		t.Fatalf("audit detail = %+v", ev[0].Detail)
	}
}

// spec: §15.1 line 868 — the endpoint admits tenant-admin too.
func TestClearExtensionDenial_TenantAdminAllowed_Spec15_1(t *testing.T) {
	clearer := &fakeLeaseDenials{found: true}
	router, _ := newLeaseDenialAdmin(t, clearer)
	req := withTenantAdminPrincipal(httptest.NewRequest(http.MethodDelete, denialPath, nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("tenant-admin status = %d, want 204; body=%s", rr.Code, rr.Body.String())
	}
}

// spec: §15.1 line 868 — a plain user (no admin role) is rejected with
// 403 before the clearer is touched.
func TestClearExtensionDenial_NonAdminForbidden_Spec15_1(t *testing.T) {
	clearer := &fakeLeaseDenials{found: true}
	router, _ := newLeaseDenialAdmin(t, clearer)
	req := withUserPrincipal(httptest.NewRequest(http.MethodDelete, denialPath, nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("plain user status = %d, want 403", rr.Code)
	}
	if len(clearer.calls) != 0 {
		t.Fatalf("clearer must not be called on an RBAC rejection; calls=%v", clearer.calls)
	}
}

// spec: §15.1 line 868 — an unknown tree returns 404 (found=false), and
// no audit row is emitted for a no-op clear.
func TestClearExtensionDenial_UnknownTree404_Spec15_1(t *testing.T) {
	clearer := &fakeLeaseDenials{found: false}
	router, audit := newLeaseDenialAdmin(t, clearer)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodDelete, denialPath, nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown tree status = %d, want 404", rr.Code)
	}
	if len(audit.snapshot()) != 0 {
		t.Fatalf("no audit row expected for a not-found clear; got %+v", audit.snapshot())
	}
}

// spec: §15.1 line 868 — a storage failure surfaces as 500.
func TestClearExtensionDenial_StorageError500_Spec15_1(t *testing.T) {
	clearer := &fakeLeaseDenials{err: errors.New("postgres down")}
	router, _ := newLeaseDenialAdmin(t, clearer)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodDelete, denialPath, nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("storage error status = %d, want 500", rr.Code)
	}
}

// spec: §15.1 line 868 — when no LeaseDenialClearer is wired the route is
// not registered, so the mux answers 404 (the gateway runs no
// GatewayControl lease-extension control plane).
func TestClearExtensionDenial_Unwired404(t *testing.T) {
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	})
	req := withAdminPrincipal(httptest.NewRequest(http.MethodDelete, denialPath, nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unwired endpoint status = %d, want 404", rr.Code)
	}
}
