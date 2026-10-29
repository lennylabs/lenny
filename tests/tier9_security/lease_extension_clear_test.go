// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for the tenant confinement on the §15.1 admin
// extension-denial clear. The endpoint
//
//	DELETE /v1/admin/trees/{rootSessionId}/subtrees/{sessionId}/extension-denial
//
// is admitted to both platform-admin and tenant-admin by the §15.1 RBAC
// grant. The tree is keyed by an opaque session UUID, so the handler
// resolves the root row before the clear: a tenant-admin reads it under its
// own tenant, so another tenant's tree is not found, and a platform-admin
// reads it by id alone, which §24.11 permits on a platform-admin-gated
// path. The clear runs under the row's tenant, so any replica clears a tree
// another replica created.
//
// The gate is exercised in-process against the genuine admin Router with
// injected Principals carrying distinct tenant ids, the same Principal the
// §10.2 auth middleware attaches after it validates the caller's JWT.
//
// spec: §10.2, §15.1, §24.11, §8.6.

package tier9_security_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// clearRecordingDenials records every durable clear with the tenant it
// ran under, so a test can assert the gate rejected a cross-tenant caller
// before the victim row was touched and that the clear used the row's
// tenant.
type clearRecordingDenials struct {
	calls [][3]string
}

func (c *clearRecordingDenials) ClearSubtreeDenial(_ context.Context, tenantID, root, sub string) error {
	c.calls = append(c.calls, [3]string{tenantID, root, sub})
	return nil
}

// failingClearSessions is a session store whose reads fail.
type failingClearSessions struct {
	sessionstore.Store
}

func (failingClearSessions) Get(context.Context, string, string) (sessionstore.Session, error) {
	return sessionstore.Session{}, errors.New("session store down")
}

func (failingClearSessions) GetByID(context.Context, string) (sessionstore.Session, error) {
	return sessionstore.Session{}, errors.New("session store down")
}

// clearTenantAdminReq attaches a §10.2 tenant-admin Principal for the
// given tenant, the Principal the auth middleware builds from a validated
// tenant-admin JWT.
func clearTenantAdminReq(req *http.Request, tenant string) *http.Request {
	ctx := authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject:  "admin@" + tenant + ".example",
		TenantID: tenant,
		Roles:    []pkgauth.Role{pkgauth.RoleTenantAdmin},
	})
	return req.WithContext(ctx)
}

// clearPlatformAdminReq attaches a §10.2 platform-admin Principal, which
// the §10.2 RBAC matrix permits to act across tenants.
func clearPlatformAdminReq(req *http.Request) *http.Request {
	ctx := authmw.WithPrincipal(req.Context(), authmw.Principal{
		Subject:  "ops@platform.example",
		TenantID: "platform",
		Roles:    []pkgauth.Role{pkgauth.RolePlatformAdmin},
	})
	return req.WithContext(ctx)
}

// clearTreeRoot and clearSub name the acme tree the tests clear.
const (
	clearTreeRoot = "11111111-1111-1111-1111-111111111111"
	clearSub      = "22222222-2222-2222-2222-222222222222"
	clearPath     = "/v1/admin/trees/" + clearTreeRoot + "/subtrees/" + clearSub + "/extension-denial"
)

// acmeTreeSessions returns a session store holding the acme tree's root
// row, standing in for the shared store a row another replica created is
// read from.
func acmeTreeSessions(t *testing.T) sessionstore.Store {
	t.Helper()
	sessions := memstore.New()
	if err := sessions.Create(context.Background(), sessionstore.Session{
		ID: clearTreeRoot, TenantID: "acme", UserID: "alice@acme.com",
	}); err != nil {
		t.Fatalf("seed root row: %v", err)
	}
	return sessions
}

func newClearRouter(denials admin.LeaseDenialClearer, sessions sessionstore.Store) *admin.Router {
	r := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithLeaseDenials(denials)
	if sessions != nil {
		r = r.WithSessions(sessions)
	}
	return r
}

func serveClear(router *admin.Router, req *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, req)
	return rr
}

// spec: 8.6, 15.1, 24.11
// diagnosis: the §15.1 extension-denial clear did not run under the root
// row's tenant. A platform-admin clear of a tree created on another
// replica must read the row by id and clear it under the row's tenant; a
// clear under the principal's tenant writes nothing, so the denial a user
// asked an operator to lift stays in force.
func TestClearExtensionDenial_PlatformAdminUsesRowTenant_spec_15_1(t *testing.T) {
	denials := &clearRecordingDenials{}
	rr := serveClear(newClearRouter(denials, acmeTreeSessions(t)),
		clearPlatformAdminReq(httptest.NewRequest(http.MethodDelete, clearPath, nil)))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("platform-admin: status %d, want 204; body=%s", rr.Code, rr.Body.String())
	}
	if len(denials.calls) != 1 || denials.calls[0] != [3]string{"acme", clearTreeRoot, clearSub} {
		t.Fatalf("clear calls = %v, want one under the row's tenant acme, not the principal's platform", denials.calls)
	}
}

// spec: 10.2, 15.1
// diagnosis: the §15.1 extension-denial clear leaks across tenants. A
// tenant-admin of globex reached the clear for a tree acme owns given the
// opaque root session UUID. A failure here means the root read is not
// confined to the caller's tenant and any tenant-admin can wipe another
// tenant's extension-denial row. The owning tenant-admin still clears its
// own tree under its own tenant.
func TestClearExtensionDenial_CrossTenantNotFound_spec_10_2(t *testing.T) {
	denials := &clearRecordingDenials{}
	router := newClearRouter(denials, acmeTreeSessions(t))

	rr := serveClear(router, clearTenantAdminReq(httptest.NewRequest(http.MethodDelete, clearPath, nil), "globex"))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("foreign tenant-admin: status %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
	if len(denials.calls) != 0 {
		t.Fatalf("durable clear ran for a cross-tenant caller; calls=%v", denials.calls)
	}

	rr = serveClear(router, clearTenantAdminReq(httptest.NewRequest(http.MethodDelete, clearPath, nil), "acme"))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("owning tenant-admin: status %d, want 204; body=%s", rr.Code, rr.Body.String())
	}
	if len(denials.calls) != 1 || denials.calls[0] != [3]string{"acme", clearTreeRoot, clearSub} {
		t.Fatalf("owning tenant-admin clear calls = %v, want one under acme", denials.calls)
	}
}

// spec: 15.1
// diagnosis: the §15.1 extension-denial clear ran without resolving the
// root. An unknown root must answer 404, and a session-store read error
// must answer 500, before the clear; a router with no session store must
// leave the route unregistered. A failure here means the clear can run
// for a tree whose owner was never established.
func TestClearExtensionDenial_RootReadFailsClosed_spec_15_1(t *testing.T) {
	unknown := "/v1/admin/trees/33333333-3333-3333-3333-333333333333/subtrees/" + clearSub + "/extension-denial"
	cases := []struct {
		name     string
		sessions func(t *testing.T) sessionstore.Store
		path     string
		want     int
	}{
		{"unknown root", acmeTreeSessions, unknown, http.StatusNotFound},
		{"session store read error", func(*testing.T) sessionstore.Store { return failingClearSessions{Store: memstore.New()} }, clearPath, http.StatusInternalServerError},
		{"unwired session store", func(*testing.T) sessionstore.Store { return nil }, clearPath, http.StatusNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, req := range []*http.Request{
				clearPlatformAdminReq(httptest.NewRequest(http.MethodDelete, c.path, nil)),
				clearTenantAdminReq(httptest.NewRequest(http.MethodDelete, c.path, nil), "acme"),
			} {
				denials := &clearRecordingDenials{}
				rr := serveClear(newClearRouter(denials, c.sessions(t)), req)
				if rr.Code != c.want {
					t.Fatalf("status %d, want %d; body=%s", rr.Code, c.want, rr.Body.String())
				}
				if len(denials.calls) != 0 {
					t.Fatalf("durable clear ran; calls=%v", denials.calls)
				}
			}
		})
	}
}
