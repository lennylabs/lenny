// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 security tests for §12.9.7 RBAC. The e2e gateway runs in dev
// mode (global.devMode: true), which enables the dev-header auth path
// with AllowDevRoles: a request carries its §10.2 RBAC roles in the
// X-Lenny-Roles header and the gateway's role-gate middleware
// (requireAdmin, requirePermission, requireAuditReader) enforces the
// §10.2 permission matrix on them exactly as it would for roles parsed
// from a Bearer JWT.
//
// The two tests below drive the live gateway admin API with distinct
// role headers:
//
//   - TestRBACRolePositiveAccess asserts each documented role reaches
//     an endpoint the §10.2 matrix grants it (platform-admin →
//     platform-scoped CRUD, tenant-admin → tenant-resource admin and
//     audit query, any authenticated caller → self-introspection).
//   - TestRBACEscalationDenied asserts every escalation attempt is
//     rejected with 403 FORBIDDEN: tenant-admin cannot reach a
//     platform-admin-only endpoint, a user cannot reach a tenant-admin
//     endpoint, and a viewer cannot create a session.
//
// The dev-header role path is the genuine §10.2 RBAC code path under
// test: parseRolesHeader feeds the same auth.Role values into the same
// gate as the Bearer path. The OIDC-verifier half (the gateway mapping
// an identity-provider token's groups claim to roles) is not reachable
// in dev mode — there is no OIDC provider in the e2e cluster — and is
// not exercised here.

package tier9_security_test

import (
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/kind"
)

// rbacGrant is one §10.2 positive-access assertion: a role that the
// permission matrix grants access to an endpoint, the HTTP method and
// path, and the status the gate must allow through (any non-403,
// non-401 — the endpoint itself may 200, 404, or 400, but the RBAC
// gate must not reject it).
type rbacGrant struct {
	name   string
	role   gwRole
	method string
	path   string
	body   string
}

// rbacDenial is one §12.9.7 escalation assertion: a role that the
// permission matrix does NOT grant access to an endpoint. The gate
// must reject the request with 403 FORBIDDEN.
type rbacDenial struct {
	name   string
	role   gwRole
	method string
	path   string
	body   string
}

// spec: 12.9.7
// diagnosis: §12.9.7 RBAC positive access did not hold — the §10.2
// role-gate middleware rejected a role the permission matrix grants.
// The test drives the live gateway admin API with each documented
// role against an endpoint the matrix permits it (platform-admin →
// tenant CRUD, tenant-admin → environments + audit query, any
// authenticated caller → /v1/admin/me) and asserts the gate admits the
// request (no 401/403). A 403 here means the gate is over-restrictive.
func TestRBACRolePositiveAccess(t *testing.T) {
	c := kind.InstallLenny(t)
	if !deploymentReadyT9(t, c, gatewayDeploymentName) {
		t.Skipf("precondition not met: %s is not Ready; the RBAC gate is gateway middleware", gatewayDeploymentName)
	}
	probe := "t9-rbac-pos-probe"
	gatewayIP := startGatewayProbe(t, c, probe)

	grants := []rbacGrant{
		{
			// §10.2: platform-admin holds every permission; tenant CRUD is
			// the platform-admin-only requireAdmin gate.
			name:   "platform-admin/list-tenants",
			role:   gwRole{tenant: "platform", roles: "platform-admin", user: "alice"},
			method: "GET",
			path:   "/v1/admin/tenants",
		},
		{
			// §10.2: tenant-admin holds manage_environments; the
			// environments admin route runs the requirePermission gate.
			name:   "tenant-admin/list-environments",
			role:   gwRole{tenant: "t9-rbac-acme", roles: "tenant-admin", user: "carol"},
			method: "GET",
			path:   "/v1/admin/environments",
		},
		{
			// §10.2: the §25.9 audit-query API admits platform-admin or
			// tenant-admin; a tenant-admin reads its own tenant's chain.
			name:   "tenant-admin/verify-own-audit-chain",
			role:   gwRole{tenant: "t9-rbac-acme", roles: "tenant-admin", user: "carol"},
			method: "GET",
			path:   "/v1/admin/audit-events/verify",
		},
		{
			// §25.4 self-introspection is available to any authenticated
			// caller with no role gate; a plain `user` must reach it.
			name:   "user/self-introspection",
			role:   gwRole{tenant: "t9-rbac-acme", roles: "user", user: "bob"},
			method: "GET",
			path:   "/v1/admin/me",
		},
		{
			// §10.2: tenant-viewer holds view_usage and the session-read
			// permissions; /v1/admin/me carries no role gate, so a viewer
			// reaches it — the positive control for the viewer role.
			name:   "tenant-viewer/self-introspection",
			role:   gwRole{tenant: "t9-rbac-acme", roles: "tenant-viewer", user: "erin"},
			method: "GET",
			path:   "/v1/admin/me",
		},
	}

	for _, g := range grants {
		t.Run(g.name, func(t *testing.T) {
			res := gatewayRequestRetry(t, c, probe, gatewayIP, g.method, g.path, g.role, g.body)
			if res.curlExit != 0 {
				t.Fatalf("request to %s %s did not complete (curl exit %d, body %q)",
					g.method, g.path, res.curlExit, res.body)
			}
			if res.statusCode == 401 || res.statusCode == 403 {
				t.Fatalf("§12.9.7 violation: role %q was rejected (status %d) by %s %s, but the §10.2 "+
					"permission matrix grants it access; the RBAC gate is over-restrictive (body %q)",
					g.role.roles, res.statusCode, g.method, g.path, res.body)
			}
			t.Logf("§12.9.7 positive: role %q admitted to %s %s (status %d)",
				g.role.roles, g.method, g.path, res.statusCode)
		})
	}
}

// spec: 12.9.7
// diagnosis: §12.9.7 RBAC escalation rejection did not hold — the
// §10.2 role-gate middleware admitted a role the permission matrix
// does not grant. The test drives the live gateway admin API with each
// escalation vector (tenant-admin → platform-admin-only tenant CRUD,
// user → tenant-admin environments, billing-viewer → tenant CRUD,
// tenant-viewer → audit query, viewer → session create) and asserts
// each is rejected with 403 FORBIDDEN. A non-403 means the gate leaks
// a privilege boundary.
func TestRBACEscalationDenied(t *testing.T) {
	c := kind.InstallLenny(t)
	if !deploymentReadyT9(t, c, gatewayDeploymentName) {
		t.Skipf("precondition not met: %s is not Ready; the RBAC gate is gateway middleware", gatewayDeploymentName)
	}
	probe := "t9-rbac-esc-probe"
	gatewayIP := startGatewayProbe(t, c, probe)

	denials := []rbacDenial{
		{
			// §10.2: tenant CRUD is reserved to platform-admin; tenant-admin
			// hitting it is the canonical "tenant-admin cannot access
			// platform-admin endpoints" escalation.
			name:   "tenant-admin-to-platform-tenant-crud",
			role:   gwRole{tenant: "t9-rbac-esc", roles: "tenant-admin", user: "carol"},
			method: "GET",
			path:   "/v1/admin/tenants",
		},
		{
			// §10.2: manage_environments is held by tenant-admin, not by
			// `user`; a user hitting the environments admin route is the
			// "user cannot access tenant-admin endpoints" escalation.
			name:   "user-to-tenant-admin-environments",
			role:   gwRole{tenant: "t9-rbac-esc", roles: "user", user: "bob"},
			method: "GET",
			path:   "/v1/admin/environments",
		},
		{
			// §10.2: billing-viewer holds only view_usage; tenant CRUD is
			// far outside its ceiling.
			name:   "billing-viewer-to-platform-tenant-crud",
			role:   gwRole{tenant: "t9-rbac-esc", roles: "billing-viewer", user: "dave"},
			method: "GET",
			path:   "/v1/admin/tenants",
		},
		{
			// §10.2: the §25.9 audit-query API admits only platform-admin
			// or tenant-admin; tenant-viewer is denied.
			name:   "tenant-viewer-to-audit-query",
			role:   gwRole{tenant: "t9-rbac-esc", roles: "tenant-viewer", user: "erin"},
			method: "GET",
			path:   "/v1/admin/audit-events/verify",
		},
		{
			// §10.2: "viewer cannot create sessions" — manage_own_sessions
			// is not held by tenant-viewer, so the session-create endpoint
			// rejects it. POST /v1/sessions is the §15 session surface.
			name:   "tenant-viewer-to-session-create",
			role:   gwRole{tenant: "t9-rbac-esc", roles: "tenant-viewer", user: "erin"},
			method: "POST",
			path:   "/v1/sessions",
			body:   `{"runtime":"echo"}`,
		},
	}

	for _, d := range denials {
		t.Run(d.name, func(t *testing.T) {
			res := gatewayRequestRetry(t, c, probe, gatewayIP, d.method, d.path, d.role, d.body)
			if res.curlExit != 0 {
				t.Fatalf("request to %s %s did not complete (curl exit %d, body %q)",
					d.method, d.path, res.curlExit, res.body)
			}
			if res.statusCode != 403 {
				t.Errorf("§12.9.7 violation: escalation vector %q (role %q → %s %s) returned status %d, "+
					"expected 403 FORBIDDEN; the §10.2 role gate leaked a privilege boundary (body %q)",
					d.name, d.role.roles, d.method, d.path, res.statusCode, res.body)
				return
			}
			if code := res.errorCode(); code != "FORBIDDEN" {
				t.Errorf("§12.9.7: escalation %q was rejected with status 403 but error code %q, "+
					"expected \"FORBIDDEN\" (body %q)", d.name, code, res.body)
				return
			}
			t.Logf("§12.9.7 escalation denied: role %q rejected by %s %s with 403 FORBIDDEN",
				d.role.roles, d.method, d.path)
		})
	}
}
