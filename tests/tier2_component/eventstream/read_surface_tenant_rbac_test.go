//go:build component

// SPDX-License-Identifier: MIT

// Tier-2 component test for the §25.5 tenant-isolation contract on the
// operational-event-stream read surface (GET /v1/admin/events and
// /v1/admin/events/stream).
//
// This test is currently a placeholder. Two coupled facts block it from
// running against the current binary:
//
//   - The read endpoints are declared with the platform-admin required
//     role (pkg/ops/opsserver/schema.go), so the served OpenAPI /
//     management-tool surface presents them as platform-admin-only, while
//     the §25.5 spec sentence below describes tenant-scoped callers
//     reaching them and receiving a tenant-filtered view. Whether the
//     endpoints should be reachable by a tenant-admin (and, if so, what
//     the required role becomes) is an undecided spec/RBAC question.
//   - The read handlers (pkg/ops/events.Service.HandlePoll /
//     HandleStream) accept no caller identity and apply no per-event
//     tenant filter, so there is no plumbing to determine a caller's
//     tenant or to gate no-tenant-label (platform-scoped) events on a
//     "permission for platform-scoped events" check. The delivery-time
//     tenant-filter mechanism itself is the separate unbuilt §25.5
//     tenant-isolation read path.
//
// Exercising the real path requires a spec/RBAC reconciliation that
// decides how a tenant-scoped caller reaches these read endpoints and
// where the platform-scoped-events permission check lives, then the
// caller-tenant plumbing and per-event tenant filter on HandlePoll /
// HandleStream. Until that lands the assertions below cannot run, so the
// test skips rather than bake in one of the two contested readings (the
// RBAC scope is wrong, or the tenant-scoped-caller sentence is
// unreachable). The interpretation-neutral core it pins is the tenant
// isolation guarantee: whichever caller reaches the endpoint, a
// tenant-scoped caller must observe only its own tenant's events and must
// not observe no-tenant-label events without platform-scoped permission.
package eventstream_test

import "testing"

// TestOpsEventStreamReadSurfaceTenantScopedCallerIsolation stands up a
// wired lenny-ops server with the OIDC role gate, publishes an
// acme-tenant-labeled event, a globex-tenant-labeled event, and a
// platform-scoped (no-tenant-label) event onto the read surface, then
// polls and streams as a tenant-scoped caller for acme. It asserts the
// caller receives the acme-labeled event, never the globex-labeled
// event, and never the platform-scoped event unless the caller carries
// permission for platform-scoped events; and that a caller lacking that
// permission never observes a no-tenant-label event.
//
// spec: 25.5 (Tenant Isolation) — "SSE and polling endpoints apply the
// same filter: tenant-scoped callers only see events matching their
// tenant or carrying no tenant label if the caller has permission for
// platform-scoped events (typically platform-admin only)."
// diagnosis: a failure means the §25.5 read surface leaks another
// tenant's operational events to a tenant-scoped caller, or exposes
// platform-scoped (no-tenant-label) events to a caller without
// platform-scoped-event permission — a cross-tenant data-isolation
// breach on the SSE/polling read path.
func TestOpsEventStreamReadSurfaceTenantScopedCallerIsolation(t *testing.T) {
	t.Skip("the §25.5 read endpoints are declared platform-admin-only while the spec describes a tenant-filtered view for tenant-scoped callers, and HandlePoll/HandleStream carry no caller identity or per-event tenant filter; a spec/RBAC reconciliation deciding how a tenant-scoped caller reaches these endpoints and where the platform-scoped-events permission check lives must land before this isolation assertion can run")
}
