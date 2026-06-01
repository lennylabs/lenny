// SPDX-License-Identifier: MIT

package admin

import (
	"encoding/json"
	"net/http"

	"github.com/lennylabs/lenny/pkg/auth"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/ops/me"
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
		// spec: §15.1:802 — pool tenant-access management (the §24.3
		// runtime commands have a pool sibling on the same join table).
		{Tool: "admin.grant_pool_tenant_access", Scope: "admin.pools.write", Category: "pool-management", MinRole: auth.RolePlatformAdmin, Description: "Grant a tenant access to a pool"},
		{Tool: "admin.list_pool_tenant_access", Scope: "admin.pools.read", Category: "pool-management", MinRole: auth.RolePlatformAdmin, Description: "List tenants with access to a pool"},
		{Tool: "admin.revoke_pool_tenant_access", Scope: "admin.pools.write", Category: "pool-management", MinRole: auth.RolePlatformAdmin, Description: "Revoke a tenant's access to a pool"},
		{Tool: "admin.bootstrap", Scope: "admin.bootstrap.write", Category: "platform-management", MinRole: auth.RolePlatformAdmin, Description: "Apply seed configuration (upsert)"},
	}
}
