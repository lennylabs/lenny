// SPDX-License-Identifier: MIT

package sessionserver

import (
	"encoding/json"
	"net/http"

	"github.com/lennylabs/lenny/pkg/gateway/envaccess"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
)

// RuntimeDiscoveryEntry is one runtime in the §9.1 GET /v1/runtimes
// discovery response. v1 reports the §5.1 registry fields; the §9.1
// agentInterface, mcpEndpoint, and adapterCapabilities blocks are not
// yet modelled on the runtime record.
type RuntimeDiscoveryEntry struct {
	Name             string            `json:"name"`
	Type             string            `json:"type,omitempty"`
	IntegrationLevel string            `json:"integrationLevel,omitempty"`
	Description      string            `json:"description,omitempty"`
	Labels           map[string]string `json:"labels,omitempty"`
}

// handleListRuntimes implements GET /v1/runtimes — the §9.1 REST
// runtime-discovery surface. Results are identity-filtered by §10.6
// environment access: a caller sees only the runtimes its environment
// membership authorizes. A not-authorized runtime is simply absent
// from the list, so the response does not enable enumeration.
func (s *Server) handleListRuntimes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.runtimes == nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"runtimes": []RuntimeDiscoveryEntry{}})
		return
	}
	rows, err := s.runtimes.List(r.Context(), runtimestore.ListFilter{})
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	rows = s.filterRuntimesByEnvironment(r, rows)

	out := make([]RuntimeDiscoveryEntry, 0, len(rows))
	for _, rt := range rows {
		out = append(out, RuntimeDiscoveryEntry{
			Name:             rt.Name,
			Type:             string(rt.Type),
			IntegrationLevel: string(rt.IntegrationLevel),
			Description:      rt.Description,
			Labels:           rt.Labels,
		})
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"runtimes": out})
}

// filterRuntimesByEnvironment narrows a runtime list to the §10.6
// environment access the request principal holds. When the
// environment or tenant registry is not wired, or the request carries
// no principal, the list is returned unchanged — the transparent
// filter is not in effect.
func (s *Server) filterRuntimesByEnvironment(r *http.Request, runtimes []runtimestore.Runtime) []runtimestore.Runtime {
	if s.environments == nil || s.tenants == nil {
		return runtimes
	}
	principal, ok := getPrincipal(r)
	if !ok || principal.TenantID == "" {
		return runtimes
	}
	envs, err := s.environments.List(r.Context(), principal.TenantID)
	if err != nil {
		return runtimes
	}
	policy := s.defaultNoEnvPolicy
	if tenant, err := s.tenants.Get(r.Context(), principal.TenantID); err == nil && tenant.NoEnvironmentPolicy != "" {
		policy = tenant.NoEnvironmentPolicy
	}
	caller := envaccess.Caller{Subject: principal.Subject, Groups: principal.Groups}
	return envaccess.AuthorizedRuntimes(caller, envs, runtimes, policy)
}
