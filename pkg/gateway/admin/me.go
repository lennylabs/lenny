// SPDX-License-Identifier: MIT

package admin

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/lennylabs/lenny/pkg/auth"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// MePayload is the §25.4 GET /v1/admin/me response shape.
type MePayload struct {
	Subject    string      `json:"subject"`
	TenantID   string      `json:"tenantId"`
	SessionID  string      `json:"sessionId,omitempty"`
	CallerType string      `json:"callerType,omitempty"`
	Typ        string      `json:"typ,omitempty"`
	Roles      []auth.Role `json:"roles"`
}

// AuthorizedToolsPayload is the §25.4 GET /v1/admin/me/authorized-tools
// response — every admin tool the caller's roles grant access to. The
// list is derived from the in-process route table and gated by the
// same role checks the live handlers apply.
type AuthorizedToolsPayload struct {
	Tools []AuthorizedTool `json:"tools"`
}

// AuthorizedTool captures one §25.4 tool entry.
type AuthorizedTool struct {
	Tool        string `json:"tool"`
	Scope       string `json:"scope"`
	Category    string `json:"category"`
	Description string `json:"description,omitempty"`
}

// handleMe implements GET /v1/admin/me — every authenticated caller
// can read it, no role gate.
func (r *Router) handleMe(w http.ResponseWriter, req *http.Request) {
	p, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "AUTH_REQUIRED",
			"endpoint requires authentication", nil)
		return
	}
	resp := MePayload{
		Subject:    p.Subject,
		TenantID:   p.TenantID,
		SessionID:  p.SessionID,
		CallerType: p.CallerType,
		Typ:        string(p.Typ),
		Roles:      append([]auth.Role(nil), p.Roles...),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// handleAuthorizedTools implements GET /v1/admin/me/authorized-tools.
// Returns every admin tool the caller's roles authorise.
func (r *Router) handleAuthorizedTools(w http.ResponseWriter, req *http.Request) {
	p, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "AUTH_REQUIRED",
			"endpoint requires authentication", nil)
		return
	}
	all := adminToolCatalog()
	out := make([]AuthorizedTool, 0, len(all))
	for _, t := range all {
		if !p.HasRole(t.MinRole) {
			continue
		}
		out = append(out, AuthorizedTool{
			Tool:        t.Tool,
			Scope:       t.Scope,
			Category:    t.Category,
			Description: t.Description,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tool < out[j].Tool })
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(AuthorizedToolsPayload{Tools: out})
}

// adminTool is the in-process catalog entry. It mirrors the
// `x-lenny-*` extensions on each OpenAPI admin endpoint.
type adminTool struct {
	Tool        string
	Scope       string
	Category    string
	MinRole     auth.Role
	Description string
}

// adminToolCatalog returns the full set of admin tools the §13 MCP
// Management Server would generate from the OpenAPI document. The
// list is hand-maintained alongside the OpenAPI JSON so the two stay
// in sync; an OpenAPI extractor that reflects the live document
// against this catalog ships in a later commit.
func adminToolCatalog() []adminTool {
	return []adminTool{
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
		{Tool: "admin.bootstrap", Scope: "admin.bootstrap.write", Category: "platform-management", MinRole: auth.RolePlatformAdmin, Description: "Apply seed configuration (upsert)"},
	}
}
