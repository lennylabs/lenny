// SPDX-License-Identifier: MIT

package admin

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/environmentstore"
)

// EnvironmentUsagePayload is the §15.1 line 840 GET
// /v1/admin/environments/{name}/usage response: the reconciled token and
// compute usage rolled up across every session created in the
// environment. The fields mirror the per-session usage envelope so a
// client can present environment and session totals with the same shape.
type EnvironmentUsagePayload struct {
	Environment  string  `json:"environment"`
	TenantID     string  `json:"tenantId"`
	TokensInput  uint64  `json:"tokensInput"`
	TokensOutput uint64  `json:"tokensOutput"`
	PodMinutes   float64 `json:"podMinutes"`
	EventCount   int     `json:"eventCount"`
}

// handleEnvironmentUsage implements GET /v1/admin/environments/{name}/usage
// (§15.1 line 840): the environment billing rollup. It resolves the
// environment within the caller's authorized tenant (404 if absent), then
// sums the §11.2.1 billing ledger across every event stamped with the
// environment name, applying correction semantics. The environment id
// stamped on billing events is the §10.6 environment name, so no join
// against session rows is needed. An environment with no usage returns a
// zero-valued rollup rather than an error, mirroring the per-session
// usage endpoint. spec: §15.1 line 840; §10.6 line 663; §11.2.1. F-15.1.3.
func (r *Router) handleEnvironmentUsage(w http.ResponseWriter, req *http.Request) {
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

	usage := billingstore.SessionUsage{}
	if r.billing != nil {
		u, err := r.billing.EnvironmentTotals(req.Context(), env.TenantID, env.Name)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
			return
		}
		usage = u
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(EnvironmentUsagePayload{
		Environment:  env.Name,
		TenantID:     env.TenantID,
		TokensInput:  usage.TokensInput,
		TokensOutput: usage.TokensOutput,
		PodMinutes:   usage.PodMinutes,
		EventCount:   usage.EventCount,
	})
}
