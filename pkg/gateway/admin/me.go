// SPDX-License-Identifier: MIT

package admin

import (
	"encoding/json"
	"net/http"

	"github.com/lennylabs/lenny/pkg/auth"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/ops/me"
	"github.com/lennylabs/lenny/pkg/ops/operations"
)

// AuthorizedToolsPayload is the §25.4 GET /v1/admin/me/authorized-tools
// response — every admin tool the caller's roles grant access to.
type AuthorizedToolsPayload struct {
	Tools []me.AuthorizedTool `json:"tools"`
}

// meService caches the §4.0 caller-identity service over the in-process
// admin-tool catalog. The catalog is constructed lazily on first use so
// the package init order does not matter.
var meService = me.NewService(adminToolCatalog())

// handleMe implements GET /v1/admin/me — every authenticated caller
// can read it, no role gate.
func (r *Router) handleMe(w http.ResponseWriter, req *http.Request) {
	p, ok := authmw.FromContext(req.Context())
	if !ok {
		// spec: §15.1 line 986 — UNAUTHORIZED (401) is the canonical
		// "missing or invalid auth" code.
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED",
			"endpoint requires authentication",
			map[string]any{"reason": "auth_required"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(meService.Me(p))
}

// handleAuthorizedTools implements GET /v1/admin/me/authorized-tools.
// Returns every admin tool the caller's roles authorise.
func (r *Router) handleAuthorizedTools(w http.ResponseWriter, req *http.Request) {
	p, ok := authmw.FromContext(req.Context())
	if !ok {
		// spec: §15.1 line 986 — UNAUTHORIZED (401) is the canonical
		// "missing or invalid auth" code.
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED",
			"endpoint requires authentication",
			map[string]any{"reason": "auth_required"})
		return
	}
	tools := meService.Authorized(p)
	if tools == nil {
		tools = []me.AuthorizedTool{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(AuthorizedToolsPayload{Tools: tools})
}

// handleMeOperations implements GET /v1/admin/me/operations — the
// caller's own in-flight operations. It reuses the §4.0 / §25.4
// Operations Inventory, scoping the scatter-gather query to the
// caller's subject and to the non-terminal statuses (in_progress,
// paused, held, awaiting_flush) so the response is the operator's
// live work rather than the full platform history.
// spec: §25 line 4903 — `lenny-ctl me operations` → caller's in-flight
// operations; §24.15 line 180.
func (r *Router) handleMeOperations(w http.ResponseWriter, req *http.Request) {
	p, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "UNAUTHORIZED",
			"endpoint requires authentication",
			map[string]any{"reason": "auth_required"})
		return
	}
	filter := operations.Filter{
		Actor: p.Subject,
		Statuses: []operations.Status{
			operations.StatusInProgress, operations.StatusPaused,
			operations.StatusHeld, operations.StatusAwaitingFlush,
		},
	}
	page := r.operationsInventory.List(req.Context(), filter, 0)
	// The inventory's post-filter enforces the status narrowing but
	// delegates the actor narrowing to each Source (which MAY ignore it).
	// Enforce caller-ownership here so a permissive Source can never leak
	// another operator's operations through the unauthenticated-by-role
	// me endpoint. The owner field is StartedBy.
	kept := page.Operations[:0]
	for _, op := range page.Operations {
		if op.StartedBy == p.Subject {
			kept = append(kept, op)
		}
	}
	page.Operations = kept
	if page.Operations == nil {
		page.Operations = []operations.Operation{}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(page)
}

// adminToolCatalog returns the full set of admin tools the §13 MCP
// Management Server would generate from the OpenAPI document. The
// list is hand-maintained alongside the OpenAPI JSON so the two stay
// in sync; an OpenAPI extractor that reflects the live document
// against this catalog ships in a later commit.
func adminToolCatalog() []me.AuthorizedTool {
	return []me.AuthorizedTool{
		{Tool: "admin.create_tenant", Scope: "admin.tenants.write", Category: "tenant-management", MinRole: auth.RolePlatformAdmin, Description: "Create a tenant"},
		{Tool: "admin.list_tenants", Scope: "admin.tenants.read", Category: "tenant-management", MinRole: auth.RolePlatformAdmin, Description: "List tenants"},
		{Tool: "admin.get_tenant", Scope: "admin.tenants.read", Category: "tenant-management", MinRole: auth.RolePlatformAdmin, Description: "Get a tenant"},
		{Tool: "admin.update_tenant", Scope: "admin.tenants.write", Category: "tenant-management", MinRole: auth.RolePlatformAdmin, Description: "Update a tenant"},
		{Tool: "admin.soft_delete_tenant", Scope: "admin.tenants.write", Category: "tenant-management", MinRole: auth.RolePlatformAdmin, Description: "Soft-delete a tenant"},
		{Tool: "admin.create_runtime", Scope: "admin.runtimes.write", Category: "runtime-management", MinRole: auth.RolePlatformAdmin, Description: "Create a runtime"},
		{Tool: "admin.list_runtimes", Scope: "admin.runtimes.read", Category: "runtime-management", MinRole: auth.RolePlatformAdmin, Description: "List runtimes"},
		{Tool: "admin.get_runtime", Scope: "admin.runtimes.read", Category: "runtime-management", MinRole: auth.RolePlatformAdmin, Description: "Get a runtime"},
		{Tool: "admin.update_runtime", Scope: "admin.runtimes.write", Category: "runtime-management", MinRole: auth.RolePlatformAdmin, Description: "Update a runtime"},
		{Tool: "admin.soft_delete_runtime", Scope: "admin.runtimes.write", Category: "runtime-management", MinRole: auth.RolePlatformAdmin, Description: "Soft-delete a runtime"},
		// spec: §24.3 / §15.1:778-780 — runtime tenant-access management.
		{Tool: "admin.grant_runtime_tenant_access", Scope: "admin.runtimes.write", Category: "runtime-management", MinRole: auth.RolePlatformAdmin, Description: "Grant a tenant access to a runtime"},
		{Tool: "admin.list_runtime_tenant_access", Scope: "admin.runtimes.read", Category: "runtime-management", MinRole: auth.RolePlatformAdmin, Description: "List tenants with access to a runtime"},
		{Tool: "admin.revoke_runtime_tenant_access", Scope: "admin.runtimes.write", Category: "runtime-management", MinRole: auth.RolePlatformAdmin, Description: "Revoke a tenant's access to a runtime"},
		{Tool: "admin.create_user", Scope: "admin.users.write", Category: "user-management", MinRole: auth.RoleTenantAdmin, Description: "Create a user"},
		{Tool: "admin.list_users", Scope: "admin.users.read", Category: "user-management", MinRole: auth.RoleTenantAdmin, Description: "List users"},
		{Tool: "admin.get_user", Scope: "admin.users.read", Category: "user-management", MinRole: auth.RoleTenantAdmin, Description: "Get a user"},
		{Tool: "admin.update_user", Scope: "admin.users.write", Category: "user-management", MinRole: auth.RoleTenantAdmin, Description: "Update a user"},
		{Tool: "admin.soft_delete_user", Scope: "admin.users.write", Category: "user-management", MinRole: auth.RoleTenantAdmin, Description: "Soft-delete a user"},
		{Tool: "admin.create_pool", Scope: "admin.pools.write", Category: "pool-management", MinRole: auth.RolePlatformAdmin, Description: "Create a pool"},
		{Tool: "admin.list_pools", Scope: "admin.pools.read", Category: "pool-management", MinRole: auth.RolePlatformAdmin, Description: "List pools"},
		{Tool: "admin.get_pool", Scope: "admin.pools.read", Category: "pool-management", MinRole: auth.RolePlatformAdmin, Description: "Get a pool"},
		{Tool: "admin.update_pool", Scope: "admin.pools.write", Category: "pool-management", MinRole: auth.RolePlatformAdmin, Description: "Update a pool"},
		{Tool: "admin.soft_delete_pool", Scope: "admin.pools.write", Category: "pool-management", MinRole: auth.RolePlatformAdmin, Description: "Soft-delete a pool"},
		// spec: §24.4 / §15.1:798-800 — the §25.14 agent-discovery catalog
		// advertises every mounted pool action so an AI DevOps agent can
		// discover the warm-pool-exhaustion and PoolScalingAdmissionStuck
		// remediation surface. The upgrade/drain/circuit-breaker/
		// exit-bootstrap tools register here when their endpoints mount
		// (F-24.4.2); the catalog mirrors the OpenAPI document so a
		// discovered tool resolves to a real route.
		{Tool: "admin.set_pool_warm_count", Scope: "admin.pools.write", Category: "pool-management", MinRole: auth.RolePlatformAdmin, Description: "Override minWarm for emergency scaling"},
		{Tool: "admin.pool_sync_status", Scope: "admin.pools.read", Category: "pool-management", MinRole: auth.RolePlatformAdmin, Description: "Show a pool's CRD reconciliation state"},
		{Tool: "admin.resume_pool_reconciliation", Scope: "admin.pools.write", Category: "pool-management", MinRole: auth.RolePlatformAdmin, Description: "Clear a pool's admission-denial backoff"},
		// spec: §15.1:802 — pool tenant-access management (the §24.3
		// runtime commands have a pool sibling on the same join table).
		{Tool: "admin.grant_pool_tenant_access", Scope: "admin.pools.write", Category: "pool-management", MinRole: auth.RolePlatformAdmin, Description: "Grant a tenant access to a pool"},
		{Tool: "admin.list_pool_tenant_access", Scope: "admin.pools.read", Category: "pool-management", MinRole: auth.RolePlatformAdmin, Description: "List tenants with access to a pool"},
		{Tool: "admin.revoke_pool_tenant_access", Scope: "admin.pools.write", Category: "pool-management", MinRole: auth.RolePlatformAdmin, Description: "Revoke a tenant's access to a pool"},
		{Tool: "admin.bootstrap", Scope: "admin.bootstrap.write", Category: "platform-management", MinRole: auth.RolePlatformAdmin, Description: "Apply seed configuration (upsert)"},
		// spec: §24.5 / §15.1:805-812, 876-878 — credential-pool admin
		// operations. The CRUD and per-credential management ops are gated
		// on the §10.2 manage_credential_pools permission (held by
		// tenant-admin); re-enable is platform-admin per §15.1 line 811.
		// The catalog mirrors the OpenAPI x-lenny-mcp-tool declarations so a
		// §25.14 agent discovers every mounted credential-pool route.
		{Tool: "admin.create_credential_pool", Scope: "admin.credential-pools.write", Category: "policy-management", MinRole: auth.RoleTenantAdmin, Description: "Create a credential pool"},
		{Tool: "admin.list_credential_pools", Scope: "admin.credential-pools.read", Category: "policy-management", MinRole: auth.RoleTenantAdmin, Description: "List credential pools"},
		{Tool: "admin.get_credential_pool", Scope: "admin.credential-pools.read", Category: "policy-management", MinRole: auth.RoleTenantAdmin, Description: "Get a credential pool, including per-credential health and lease counts"},
		{Tool: "admin.update_credential_pool", Scope: "admin.credential-pools.write", Category: "policy-management", MinRole: auth.RoleTenantAdmin, Description: "Update a credential pool"},
		{Tool: "admin.delete_credential_pool", Scope: "admin.credential-pools.write", Category: "policy-management", MinRole: auth.RoleTenantAdmin, Description: "Delete a credential pool"},
		{Tool: "admin.add_credential_to_pool", Scope: "admin.credential-pools.write", Category: "policy-management", MinRole: auth.RoleTenantAdmin, Description: "Add a credential to a pool"},
		{Tool: "admin.update_pool_credential", Scope: "admin.credential-pools.write", Category: "policy-management", MinRole: auth.RoleTenantAdmin, Description: "Update a credential in a pool"},
		{Tool: "admin.remove_pool_credential", Scope: "admin.credential-pools.write", Category: "policy-management", MinRole: auth.RoleTenantAdmin, Description: "Remove a credential from a pool"},
		{Tool: "admin.revoke_pool_credential", Scope: "admin.credential-pools.write", Category: "policy-management", MinRole: auth.RoleTenantAdmin, Description: "Emergency-revoke a single pool credential"},
		{Tool: "admin.revoke_credential_pool", Scope: "admin.credential-pools.write", Category: "policy-management", MinRole: auth.RoleTenantAdmin, Description: "Emergency-revoke all credentials in a pool"},
		{Tool: "admin.re_enable_pool_credential", Scope: "admin.credential-pools.write", Category: "policy-management", MinRole: auth.RolePlatformAdmin, Description: "Re-enable a previously revoked pool credential"},
	}
}
