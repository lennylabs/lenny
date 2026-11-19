// SPDX-License-Identifier: MIT

package admin

import (
	"encoding/json"
	"net/http"
	"sort"
)

// AccessReportIdentity is one §10.6 member identity in the access
// report — a user or an OIDC group.
type AccessReportIdentity struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

// AccessCell is one identity's role in one environment.
type AccessCell struct {
	Environment string `json:"environment"`
	Role        string `json:"role"`
}

// AccessReportEntry is the access an identity holds across the tenant's
// environments — one row of the cross-environment matrix.
type AccessReportEntry struct {
	Identity AccessReportIdentity `json:"identity"`
	Access   []AccessCell         `json:"access"`
}

// TenantAccessReportPayload is the §15.1 GET
// /v1/admin/tenants/{id}/access-report response: the cross-environment
// access matrix for a tenant. `environments` lists the matrix columns;
// each entry is one identity's row.
type TenantAccessReportPayload struct {
	TenantID     string              `json:"tenantId"`
	Environments []string            `json:"environments"`
	Entries      []AccessReportEntry `json:"entries"`
}

// handleTenantAccessReport implements GET
// /v1/admin/tenants/{id}/access-report. It aggregates the §10.6 member
// lists of every environment in the tenant into a cross-environment
// access matrix keyed by identity. Identities are reported as declared
// (a member identity may be an individual user or an OIDC group);
// OIDC-group expansion to individual users is a separate concern of
// the group-resolution path.
func (r *Router) handleTenantAccessReport(w http.ResponseWriter, req *http.Request) {
	tenant, err := authorizeTenantPath(req, req.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	envs, err := r.environments.List(req.Context(), tenant)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	out := TenantAccessReportPayload{
		TenantID:     tenant,
		Environments: make([]string, 0, len(envs)),
		Entries:      []AccessReportEntry{},
	}
	// byIdentity preserves insertion order through the index map so the
	// access cells stay environment-name-ordered (List is name-sorted).
	type identityKey struct{ typ, value string }
	index := map[identityKey]int{}
	var entries []AccessReportEntry
	for _, env := range envs {
		out.Environments = append(out.Environments, env.Name)
		for _, m := range env.Members {
			k := identityKey{m.Identity.Type, m.Identity.Value}
			cell := AccessCell{Environment: env.Name, Role: string(m.Role)}
			if i, ok := index[k]; ok {
				entries[i].Access = append(entries[i].Access, cell)
				continue
			}
			index[k] = len(entries)
			entries = append(entries, AccessReportEntry{
				Identity: AccessReportIdentity{Type: m.Identity.Type, Value: m.Identity.Value},
				Access:   []AccessCell{cell},
			})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Identity.Type != entries[j].Identity.Type {
			return entries[i].Identity.Type < entries[j].Identity.Type
		}
		return entries[i].Identity.Value < entries[j].Identity.Value
	})
	out.Entries = append(out.Entries, entries...)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
