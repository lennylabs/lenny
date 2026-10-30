// SPDX-License-Identifier: MIT

package admin_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// fakeLeaseDenials records ClearSubtreeDenial calls and returns a
// scripted error so the §15.1 handler's error branch can be exercised.
type fakeLeaseDenials struct {
	calls [][3]string
	err   error
}

func (f *fakeLeaseDenials) ClearSubtreeDenial(_ context.Context, tenantID, root, sub string) error {
	f.calls = append(f.calls, [3]string{tenantID, root, sub})
	return f.err
}

// failingSessions wraps a session store whose reads fail, exercising the
// handler's root-read 500 branch.
type failingSessions struct {
	sessionstore.Store
	err error
}

func (f failingSessions) Get(context.Context, string, string) (sessionstore.Session, error) {
	return sessionstore.Session{}, f.err
}

func (f failingSessions) GetByID(context.Context, string) (sessionstore.Session, error) {
	return sessionstore.Session{}, f.err
}

// newLeaseDenialAdmin wires the clear route over a session store holding
// root-1 owned by acme.
func newLeaseDenialAdmin(t *testing.T, clearer admin.LeaseDenialClearer) (*admin.Router, *recordingAudit) {
	t.Helper()
	sessions := memstore.New()
	seedSession(t, sessions, sessionstore.Session{ID: "root-1", TenantID: "acme", UserID: "alice@acme.com"})
	audit := &recordingAudit{}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		Audit: audit,
	}).WithLeaseDenials(clearer).WithSessions(sessions)
	return router, audit
}

const denialPath = "/v1/admin/trees/root-1/subtrees/sub-9/extension-denial"

// spec: §8.6; §15.1; §24.11 — platform-admin clears a subtree denial;
// the handler returns 204, forwards both path ids to the clearer under the
// root row's tenant rather than the principal's, and records the §10.6
// admin audit row.
func TestClearExtensionDenial_PlatformAdmin_Spec15_1(t *testing.T) {
	clearer := &fakeLeaseDenials{}
	router, audit := newLeaseDenialAdmin(t, clearer)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodDelete, denialPath, nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rr.Code, rr.Body.String())
	}
	if len(clearer.calls) != 1 || clearer.calls[0] != [3]string{"acme", "root-1", "sub-9"} {
		t.Fatalf("clearer calls = %v, want one (acme, root-1, sub-9)", clearer.calls)
	}
	ev := audit.snapshot()
	if len(ev) != 1 || ev[0].Type != "admin.delegation.extension_denial_cleared" {
		t.Fatalf("audit = %+v, want one extension_denial_cleared event", ev)
	}
	if ev[0].Detail["root_session_id"] != "root-1" || ev[0].Detail["session_id"] != "sub-9" {
		t.Fatalf("audit detail = %+v", ev[0].Detail)
	}
}

// spec: §15.1 — the endpoint admits tenant-admin too.
func TestClearExtensionDenial_TenantAdminAllowed_Spec15_1(t *testing.T) {
	clearer := &fakeLeaseDenials{}
	router, _ := newLeaseDenialAdmin(t, clearer)
	req := withTenantAdminPrincipal(httptest.NewRequest(http.MethodDelete, denialPath, nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("tenant-admin status = %d, want 204; body=%s", rr.Code, rr.Body.String())
	}
}

// spec: §15.1 — a plain user (no admin role) is rejected with
// 403 before the clearer is touched.
func TestClearExtensionDenial_NonAdminForbidden_Spec15_1(t *testing.T) {
	clearer := &fakeLeaseDenials{}
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

// spec: §15.1 — an unknown root returns 404 before the clear, and no
// audit row is emitted.
func TestClearExtensionDenial_UnknownTree404_Spec15_1(t *testing.T) {
	clearer := &fakeLeaseDenials{}
	router, audit := newLeaseDenialAdmin(t, clearer)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodDelete, "/v1/admin/trees/root-404/subtrees/sub-9/extension-denial", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown tree status = %d, want 404", rr.Code)
	}
	if len(audit.snapshot()) != 0 {
		t.Fatalf("no audit row expected for a not-found clear; got %+v", audit.snapshot())
	}
	if len(clearer.calls) != 0 {
		t.Fatalf("clear must not run for an unknown root; calls=%v", clearer.calls)
	}
}

// spec: §15.1 — a clear failure surfaces as 500.
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

// spec: §15.1 — when no LeaseDenialClearer is wired the route is not
// registered, so the mux answers 404.
func TestClearExtensionDenial_Unwired404(t *testing.T) {
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithSessions(memstore.New())
	req := withAdminPrincipal(httptest.NewRequest(http.MethodDelete, denialPath, nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unwired endpoint status = %d, want 404", rr.Code)
	}
}

// spec: §15.1 — the handler resolves the root row before the clear, so
// a router with a clearer but no session store leaves the route
// unregistered and the clear never runs.
func TestClearExtensionDenial_UnwiredSessionsUnregistered_Spec15_1(t *testing.T) {
	clearer := &fakeLeaseDenials{}
	router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithLeaseDenials(clearer)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodDelete, denialPath, nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("route without a session store status = %d, want 404", rr.Code)
	}
	if len(clearer.calls) != 0 {
		t.Fatalf("clear must not run without a session store; calls=%v", clearer.calls)
	}
}

// spec: §10.2; §15.1 (ADM-4) — a tenant-admin of one tenant cannot clear
// a tree owned by another tenant. root-1 is owned by acme, and the globex
// tenant-admin reads it under globex, so the read misses, the handler
// answers 404, and the clearer is never called.
//
// diagnosis: the extension-denial clear leaks across tenants: a
// tenant-admin cleared another tenant's denial row given an opaque
// session UUID. The §10.2 own-tenant confinement is missing or unwired.
func TestClearExtensionDenial_ForeignTenantForbidden_ADM4(t *testing.T) {
	clearer := &fakeLeaseDenials{}
	router, audit := newLeaseDenialAdmin(t, clearer)
	req := withForeignTenantAdminPrincipal(httptest.NewRequest(http.MethodDelete, denialPath, nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("foreign tenant-admin status = %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
	if len(clearer.calls) != 0 {
		t.Fatalf("clear must not run on a cross-tenant rejection; calls=%v", clearer.calls)
	}
	if len(audit.snapshot()) != 0 {
		t.Fatalf("no audit row expected for a rejected cross-tenant clear; got %+v", audit.snapshot())
	}
}

// spec: §10.2; §15.1 (ADM-4) — the owning tenant-admin
// (acme owns root-1) still clears its own tree: 204, the clearer runs,
// and the audit row is emitted. This pins that the confinement does not
// over-reject the legitimate own-tenant caller.
//
// diagnosis: the §10.2 own-tenant confinement on the extension-denial
// clear rejected the legitimate owning tenant-admin.
func TestClearExtensionDenial_OwningTenantAllowed_ADM4(t *testing.T) {
	clearer := &fakeLeaseDenials{}
	router, audit := newLeaseDenialAdmin(t, clearer)
	req := withTenantAdminPrincipal(httptest.NewRequest(http.MethodDelete, denialPath, nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("owning tenant-admin status = %d, want 204; body=%s", rr.Code, rr.Body.String())
	}
	if len(clearer.calls) != 1 || clearer.calls[0][0] != "acme" {
		t.Fatalf("owning tenant-admin clear calls = %v, want one under acme", clearer.calls)
	}
	if len(audit.snapshot()) != 1 {
		t.Fatalf("owning tenant-admin clear must emit one audit row; got %+v", audit.snapshot())
	}
}

// spec: §10.2 (ADM-4) — a platform-admin still clears across
// tenants: root-1 is owned by acme, the platform-admin (tenant=platform)
// reads it by id, and the clear runs under acme.
//
// diagnosis: the §10.2 confinement over-rejected a platform-admin
// cross-tenant clear, which §10.2 permits.
func TestClearExtensionDenial_PlatformAdminCrossTenant_ADM4(t *testing.T) {
	clearer := &fakeLeaseDenials{}
	router, _ := newLeaseDenialAdmin(t, clearer)
	req := withAdminPrincipal(httptest.NewRequest(http.MethodDelete, denialPath, nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("platform-admin cross-tenant status = %d, want 204; body=%s", rr.Code, rr.Body.String())
	}
	if len(clearer.calls) != 1 || clearer.calls[0][0] != "acme" {
		t.Fatalf("platform-admin clear calls = %v, want one under the row's tenant acme", clearer.calls)
	}
}

// spec: §10.2; §15.1 — a tenant-admin naming a root that does not exist
// gets 404 RESOURCE_NOT_FOUND before the clear.
func TestClearExtensionDenial_TenantAdminUnknownTree404_Spec15_1(t *testing.T) {
	clearer := &fakeLeaseDenials{}
	router, _ := newLeaseDenialAdmin(t, clearer)
	req := withTenantAdminPrincipal(httptest.NewRequest(http.MethodDelete, "/v1/admin/trees/root-404/subtrees/sub-9/extension-denial", nil))
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown root status = %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
	if len(clearer.calls) != 0 {
		t.Fatalf("clear must not run for an unknown root; calls=%v", clearer.calls)
	}
}

// spec: §15.1 — a session-store read error on the root lookup answers 500
// and the clear does not run, for both the tenant-admin and the
// platform-admin read.
func TestClearExtensionDenial_RootReadError500_Spec15_1(t *testing.T) {
	for name, principal := range map[string]func(*http.Request) *http.Request{
		"tenant-admin":   withTenantAdminPrincipal,
		"platform-admin": withAdminPrincipal,
	} {
		t.Run(name, func(t *testing.T) {
			clearer := &fakeLeaseDenials{}
			router := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
				Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
			}).WithLeaseDenials(clearer).
				WithSessions(failingSessions{Store: memstore.New(), err: errors.New("session store down")})
			req := principal(httptest.NewRequest(http.MethodDelete, denialPath, nil))
			rr := httptest.NewRecorder()
			router.Handler().ServeHTTP(rr, req)
			if rr.Code != http.StatusInternalServerError {
				t.Fatalf("root read error status = %d, want 500; body=%s", rr.Code, rr.Body.String())
			}
			if len(clearer.calls) != 0 {
				t.Fatalf("clear must not run when the root read errors; calls=%v", clearer.calls)
			}
		})
	}
}
