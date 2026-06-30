// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security test for the §10.2 own-tenant confinement on the
// §15.1 line 868 admin extension-denial clear (proposal 0019 finding
// ADM-4). The endpoint
//
//	DELETE /v1/admin/trees/{rootSessionId}/subtrees/{sessionId}/extension-denial
//
// is admitted to both platform-admin and tenant-admin by the §15.1 line
// 869 RBAC grant. The tree is keyed by an opaque session UUID, so the
// role gate alone lets a tenant-admin of one tenant clear another
// tenant's durable extension-denial row. §10.2 line 261 states a
// tenant-admin cannot access another tenant's data, so the handler must
// resolve the tree's owner tenant and reject a non-platform-admin caller
// whose tenant differs before the durable clear runs.
//
// The gate is exercised in-process against the genuine admin Router with
// injected Principals carrying distinct tenant ids, the same Principal
// the §10.2 auth middleware attaches after it validates the caller's JWT.
// Driving the real Router exercises the same authorization code path a
// Bearer-JWT caller exercises; the cross-tenant boundary is the property
// under test, independent of the JWT-parsing front door.
//
// spec: §10.2 (tenant-admin cannot access other tenants' data, line
// 261), §15.1 (extension-denial requires platform-admin or tenant-admin,
// line 869).

package tier9_security_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/leasecontrol"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// clearRecordingDenials records whether the durable clear ran, so a test
// can assert the gate rejected a cross-tenant caller before the victim
// row was touched.
type clearRecordingDenials struct {
	calls [][2]string
}

func (c *clearRecordingDenials) ClearSubtreeDenial(_ context.Context, root, sub string) (bool, error) {
	c.calls = append(c.calls, [2]string{root, sub})
	return true, nil
}

// clearTenantResolver maps a tree's root session id to its owning tenant,
// satisfying leasecontrol.TenantResolver. An unknown root reports
// ErrSessionNotFound.
type clearTenantResolver struct {
	owners map[string]string
}

func (r clearTenantResolver) TenantOf(_ context.Context, sessionID string) (string, error) {
	owner, ok := r.owners[sessionID]
	if !ok {
		return "", leasecontrol.ErrSessionNotFound
	}
	return owner, nil
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

func newClearRouter(denials admin.LeaseDenialClearer, resolver leasecontrol.TenantResolver) *admin.Router {
	r := admin.NewRouter(tenantstore.NewMemory(), admin.Options{
		Clock: func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
	}).WithLeaseDenials(denials)
	if resolver != nil {
		r = r.WithTenantResolver(resolver)
	}
	return r
}

// spec: 10.2, 15.1
// diagnosis: the §15.1 admin extension-denial clear leaks across
// tenants. A tenant-admin of acme cleared a tree owned by globex given
// the opaque root session UUID, because the durable clear ran before any
// caller-tenant check. A failure here means the §10.2 line 261 own-tenant
// confinement is missing or unwired, and any tenant-admin can wipe
// another tenant's extension-denial row. The owning tenant-admin must
// still succeed; the confinement narrows to the foreign caller only.
func TestClearExtensionDenial_CrossTenantRejected_ADM4(t *testing.T) {
	// globex owns the tree; acme's tenant-admin must not clear it.
	const root = "11111111-1111-1111-1111-111111111111"
	const sub = "22222222-2222-2222-2222-222222222222"
	path := "/v1/admin/trees/" + root + "/subtrees/" + sub + "/extension-denial"
	resolver := clearTenantResolver{owners: map[string]string{root: "globex"}}

	// 1. acme's tenant-admin is rejected 403 FORBIDDEN and the durable
	//    clear never runs, so globex's row is untouched.
	denials := &clearRecordingDenials{}
	router := newClearRouter(denials, resolver)
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, clearTenantAdminReq(
		httptest.NewRequest(http.MethodDelete, path, nil), "acme",
	))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("foreign tenant-admin: status %d, want 403 FORBIDDEN; body=%s", rr.Code, rr.Body.String())
	}
	if len(denials.calls) != 0 {
		t.Fatalf("durable clear ran on a cross-tenant rejection; calls=%v (the victim row was wiped — ADM-4 leak)", denials.calls)
	}

	// 2. The owning tenant-admin (globex) clears its own tree: 204, and
	//    the clear runs. The confinement does not over-reject the
	//    legitimate owner.
	denials = &clearRecordingDenials{}
	router = newClearRouter(denials, resolver)
	rr = httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, clearTenantAdminReq(
		httptest.NewRequest(http.MethodDelete, path, nil), "globex",
	))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("owning tenant-admin: status %d, want 204; body=%s", rr.Code, rr.Body.String())
	}
	if len(denials.calls) != 1 || denials.calls[0] != [2]string{root, sub} {
		t.Fatalf("owning tenant-admin clear calls = %v, want one (%s, %s)", denials.calls, root, sub)
	}

	// 3. A platform-admin clears across tenants regardless of owner: 204,
	//    the clear runs. The §10.2 confinement applies to non-platform
	//    callers only.
	denials = &clearRecordingDenials{}
	router = newClearRouter(denials, resolver)
	rr = httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, clearPlatformAdminReq(
		httptest.NewRequest(http.MethodDelete, path, nil),
	))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("platform-admin cross-tenant: status %d, want 204; body=%s", rr.Code, rr.Body.String())
	}
	if len(denials.calls) != 1 {
		t.Fatalf("platform-admin clear calls = %v, want one", denials.calls)
	}
}

// spec: 10.2, 15.1
// diagnosis: the §15.1 admin extension-denial clear failed open under
// misconfiguration. When the tenant resolver is unwired, a
// non-platform-admin caller still reached the durable clear, so the
// §10.2 cross-tenant confinement depended on a present resolver to hold.
// A failure here means a misconfigured gateway reopens the cross-tenant
// clear for any tenant-admin.
func TestClearExtensionDenial_NilResolverFailsClosed_ADM4(t *testing.T) {
	const root = "11111111-1111-1111-1111-111111111111"
	const sub = "22222222-2222-2222-2222-222222222222"
	path := "/v1/admin/trees/" + root + "/subtrees/" + sub + "/extension-denial"

	denials := &clearRecordingDenials{}
	router := newClearRouter(denials, nil) // resolver unwired
	rr := httptest.NewRecorder()
	router.Handler().ServeHTTP(rr, clearTenantAdminReq(
		httptest.NewRequest(http.MethodDelete, path, nil), "acme",
	))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("nil resolver tenant-admin: status %d, want 403 FORBIDDEN; body=%s", rr.Code, rr.Body.String())
	}
	if len(denials.calls) != 0 {
		t.Fatalf("durable clear ran with an unwired resolver; calls=%v (fail-open under misconfiguration)", denials.calls)
	}
}
