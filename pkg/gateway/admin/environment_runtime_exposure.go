// SPDX-License-Identifier: MIT

package admin

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/environment"
	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/environmentstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
)

// ExposedRuntime is one runtime the environment's §10.6 runtimeSelector
// admits, in the §15.1 runtime-exposure response.
type ExposedRuntime struct {
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
}

// ExposedConnector is one connector the environment's §10.6
// connectorSelector admits.
type ExposedConnector struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName,omitempty"`
}

// RuntimeExposurePayload is the §15.1 GET
// /v1/admin/environments/{name}/runtime-exposure response: the runtimes
// and connectors the environment's §10.6 selectors bring into scope.
type RuntimeExposurePayload struct {
	Environment string             `json:"environment"`
	TenantID    string             `json:"tenantId"`
	Runtimes    []ExposedRuntime   `json:"runtimes"`
	Connectors  []ExposedConnector `json:"connectors"`
}

// handleEnvironmentRuntimeExposure implements GET
// /v1/admin/environments/{name}/runtime-exposure. It evaluates the
// environment's §10.6 runtimeSelector against the runtime registry and
// its connectorSelector against the connector registry, returning the
// resources in scope. Selector evaluation reuses pkg/environment.
// Capability filtering (mcpRuntimeFilters, connector capabilities)
// governs what an admitted resource may do, not whether it is in
// scope, so it does not affect this membership view.
func (r *Router) handleEnvironmentRuntimeExposure(w http.ResponseWriter, req *http.Request) {
	tenant, _, err := r.authorizedTenantForUser(req, req.URL.Query().Get("tenantId"))
	if err != nil {
		writeError(w, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
		return
	}
	env, err := r.environments.Get(req.Context(), tenant, req.PathValue("name"))
	if err != nil {
		if errors.Is(err, environmentstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "environment not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	out := RuntimeExposurePayload{
		Environment: env.Name,
		TenantID:    env.TenantID,
		Runtimes:    []ExposedRuntime{},
		Connectors:  []ExposedConnector{},
	}

	if r.runtimes != nil {
		runtimes, err := r.runtimes.List(req.Context(), runtimestore.ListFilter{})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
			return
		}
		for _, rt := range runtimes {
			if env.RuntimeSelector.Matches(environment.Candidate{
				Name: rt.Name, Type: string(rt.Type), Labels: rt.Labels,
			}) {
				out.Runtimes = append(out.Runtimes, ExposedRuntime{
					Name: rt.Name, Type: string(rt.Type),
				})
			}
		}
	}
	if r.connectors != nil {
		connectors, err := r.connectors.List(req.Context(), connectorstore.ListFilter{})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
			return
		}
		for _, c := range connectors {
			if env.ConnectorSelector.Matches(environment.Candidate{
				Name: c.ID, Labels: c.Labels,
			}) {
				out.Connectors = append(out.Connectors, ExposedConnector{
					ID: c.ID, DisplayName: c.DisplayName,
				})
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
