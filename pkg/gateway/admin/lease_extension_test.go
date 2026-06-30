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
	"github.com/lennylabs/lenny/pkg/gateway/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
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

// fakeTenantResolver resolves a root session id to its owning tenant,
// satisfying leasecontrol.TenantResolver so the ADM-4 cross-tenant
// confinement can be exercised. An empty owner reports the session as
// unknown so the not-found branch can be asserted too.
type fakeTenantResolver struct {
	owners map[string]string
}

func (f fakeTenantResolver) TenantOf(_ context.Context, sessionID string) (string, error) {
	owner, ok := f.owners[sessionID]
	if !ok {
		return "", leasecontrol.ErrSessionNotFound
	}
	return owner, nil
}

// errTenantResolver always fails, exercising the handler's resolver-error
// 500 fail-closed branch.
type errTenantResolver struct{ err error }

func (e errTenantResolver) TenantOf(context.Context, string) (string, error) {
	return "", e.err
}

func newLeaseDenialAdmin(t *testing.T, clearer admin.LeaseDenialClearer) (*admin.Router, *recordingAudit) {
	t.Helper()
	audit := &recordingAudit{}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Audit: audit,
	}).WithLeaseDenials(clearer).
		WithTenantResolver(fakeTenantResolver{owners: map[string]string{"root-1": "acme"}})
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

// spec: §10.2 line 261; §15.1 line 869 (ADM-4) — a tenant-admin of one
// tenant cannot clear a tree owned by another tenant. The resolver maps
// root-1 to acme, so the globex tenant-admin is rejected 403 FORBIDDEN
// and the durable clearer is never called. Fails pre-fix, where the
// clear runs before any caller-tenant check and the victim row is wiped.
//
// diagnosis: the extension-denial clear leaks across tenants: a
// tenant-admin cleared another tenant's denial row given an opaque
// session UUID. The §10.2 own-tenant confinement is missing or unwired.
func TestClearExtensionDenial_ForeignTenantForbidden_ADM4(t *testing.T) {
	clearer := &fakeLeaseDenials{found: true}
	router, audit := newLeaseDenialAdmin(t, clearer)
	req := withForeignTenantAdminPrincipal(httptest.NewRequest(http.MethodDelete, denialPath, nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("foreign tenant-admin status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
	if len(clearer.calls) != 0 {
		t.Fatalf("clear must not run on a cross-tenant rejection; calls=%v", clearer.calls)
	}
	if len(audit.snapshot()) != 0 {
		t.Fatalf("no audit row expected for a rejected cross-tenant clear; got %+v", audit.snapshot())
	}
}

// spec: §10.2 line 261; §15.1 line 869 (ADM-4) — the owning tenant-admin
// (acme owns root-1) still clears its own tree: 204, the clearer runs,
// and the audit row is emitted. This pins that the confinement does not
// over-reject the legitimate own-tenant caller.
//
// diagnosis: the §10.2 own-tenant confinement on the extension-denial
// clear rejected the legitimate owning tenant-admin.
func TestClearExtensionDenial_OwningTenantAllowed_ADM4(t *testing.T) {
	clearer := &fakeLeaseDenials{found: true}
	router, audit := newLeaseDenialAdmin(t, clearer)
	req := withTenantAdminPrincipal(httptest.NewRequest(http.MethodDelete, denialPath, nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("owning tenant-admin status = %d, want 204; body=%s", rr.Code, rr.Body.String())
	}
	if len(clearer.calls) != 1 {
		t.Fatalf("owning tenant-admin clear calls = %v, want one", clearer.calls)
	}
	if len(audit.snapshot()) != 1 {
		t.Fatalf("owning tenant-admin clear must emit one audit row; got %+v", audit.snapshot())
	}
}

// spec: §10.2 line 261 (ADM-4) — fail closed when the tenant resolver is
// unwired: a non-platform-admin caller is rejected 403 FORBIDDEN so a
// misconfigured gateway cannot reopen the cross-tenant clear, while the
// clearer is never reached.
//
// diagnosis: the extension-denial clear ran for a non-platform-admin
// caller despite an unwired tenant resolver, so the cross-tenant
// confinement failed open under misconfiguration.
func TestClearExtensionDenial_NilResolverFailsClosed_ADM4(t *testing.T) {
	clearer := &fakeLeaseDenials{found: true}
	audit := &recordingAudit{}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Audit: audit,
	}).WithLeaseDenials(clearer) // no WithTenantResolver
	req := withTenantAdminPrincipal(httptest.NewRequest(http.MethodDelete, denialPath, nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("nil-resolver tenant-admin status = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
	if len(clearer.calls) != 0 {
		t.Fatalf("clear must not run when the resolver is unwired; calls=%v", clearer.calls)
	}
}

// spec: §10.2 line 261 (ADM-4) — a platform-admin still clears across
// tenants even when the tree is owned by another tenant; the resolver
// confinement applies only to non-platform-admin callers. root-1 is
// owned by acme, but the platform-admin (tenant=platform) succeeds.
//
// diagnosis: the §10.2 confinement over-rejected a platform-admin
// cross-tenant clear, which §10.2 permits.
func TestClearExtensionDenial_PlatformAdminCrossTenant_ADM4(t *testing.T) {
	clearer := &fakeLeaseDenials{found: true}
	router, _ := newLeaseDenialAdmin(t, clearer)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodDelete, denialPath, nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("platform-admin cross-tenant status = %d, want 204; body=%s", rr.Code, rr.Body.String())
	}
	if len(clearer.calls) != 1 {
		t.Fatalf("platform-admin clear calls = %v, want one", clearer.calls)
	}
}

// spec: §10.2 line 261; §15.1 line 868 (ADM-4) — a non-platform-admin
// caller naming a tree the resolver does not know gets 404
// RESOURCE_NOT_FOUND before the clear, so an unknown tree is reported
// rather than confirming or denying ownership. The clearer is never
// reached.
func TestClearExtensionDenial_ResolverUnknownTree404_ADM4(t *testing.T) {
	clearer := &fakeLeaseDenials{found: true}
	audit := &recordingAudit{}
	// The resolver knows no trees, so root-1 resolves to ErrSessionNotFound.
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Audit: audit,
	}).WithLeaseDenials(clearer).
		WithTenantResolver(fakeTenantResolver{owners: map[string]string{}})
	req := withTenantAdminPrincipal(httptest.NewRequest(http.MethodDelete, denialPath, nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown-tree resolver status = %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
	if len(clearer.calls) != 0 {
		t.Fatalf("clear must not run when the tree is unknown to the resolver; calls=%v", clearer.calls)
	}
}

// spec: §10.2 line 261 (ADM-4) — a resolver storage error on the
// confinement lookup surfaces as 500 and the clear does not run, so a
// transient resolver outage fails closed rather than skipping the
// cross-tenant check.
func TestClearExtensionDenial_ResolverError500_ADM4(t *testing.T) {
	clearer := &fakeLeaseDenials{found: true}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithLeaseDenials(clearer).
		WithTenantResolver(errTenantResolver{err: errors.New("session store down")})
	req := withTenantAdminPrincipal(httptest.NewRequest(http.MethodDelete, denialPath, nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("resolver error status = %d, want 500; body=%s", rr.Code, rr.Body.String())
	}
	if len(clearer.calls) != 0 {
		t.Fatalf("clear must not run when the resolver errors; calls=%v", clearer.calls)
	}
}
