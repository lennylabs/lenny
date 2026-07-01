// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorinvoke"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/policy/ratelimit"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/capabilityinference"
)

// connectorRefreshRateLimit caps the §9.3 capability refresh at the same
// 10/connector/minute bound as the live test (§15.1 line 1180): the
// refresh dials the external endpoint, so it must not be abusable as a
// network probe.
const connectorRefreshRateLimit = 10

// ConnectorCapabilityRefresher runs the §9.3 line 136 / §5.1 capability
// inference for a connector on the sanctioned outbound path: it reads the
// connector's tools/list and persists the inferred capability metadata.
// The production implementation is *connectorinvoke.Invoker; tests inject
// a fake.
type ConnectorCapabilityRefresher interface {
	RefreshCapabilities(ctx context.Context, tenantID, connectorID, userID, environment string) (connectorinvoke.CapabilityRefreshResult, error)
}

// WithConnectorRefresh wires the §9.3 line 136
// `POST /v1/admin/connectors/{name}/refresh` capability-inference
// endpoint. A nil refresher leaves the route unregistered. limiter
// enforces the per-connector 10/min cap on the outbound dial (matching
// the §15.1 line 1180 live-test cap); a nil limiter leaves the refresh
// unthrottled.
func (r *Router) WithConnectorRefresh(refresher ConnectorCapabilityRefresher, limiter ratelimit.Counter) *Router {
	r.connectorRefresher = refresher
	r.connectorRefreshLimiter = limiter
	return r
}

// connectorCapabilityResponse is the JSON body of a successful
// capability refresh. The fields mirror the §5.1 inference result.
type connectorCapabilityResponse struct {
	Connector               string                                      `json:"connector"`
	CapabilityInferenceMode capabilityinference.Mode                    `json:"capabilityInferenceMode"`
	Capabilities            []capabilityinference.Capability            `json:"capabilities"`
	ToolCapabilities        map[string][]capabilityinference.Capability `json:"toolCapabilities"`
	UnannotatedAdminTools   []string                                    `json:"unannotatedAdminTools,omitempty"`
}

// handleRefreshConnectorCapabilities implements §9.3 line 136:
// `POST /v1/admin/connectors/{name}/refresh`. It re-reads the connector's
// MCP tools/list on the sanctioned outbound path and persists the
// inferred §5.1 capability metadata. The synchronous registration path
// makes no outbound call (§15.1 line 1144), so this refresh is the path
// by which a connector's capability metadata is populated and kept
// current.
//
// spec: §9.3 line 136; §5.1 lines 312-329.
func (r *Router) handleRefreshConnectorCapabilities(w http.ResponseWriter, req *http.Request) {
	id := req.PathValue("name")
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	tenantID := listTenantScope(principal, req)
	conn, err := r.connectors.Get(req.Context(), tenantID, id)
	if err != nil {
		if errors.Is(err, connectorstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "connector not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if !conn.IsActive() {
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "connector not found", nil)
		return
	}

	if r.connectorRefreshLimiter != nil {
		key := "connector-refresh:" + conn.TenantID + ":" + conn.ID
		count, lerr := r.connectorRefreshLimiter.Incr(req.Context(), key, r.clock())
		if lerr == nil && count > connectorRefreshRateLimit {
			w.Header().Set("Retry-After", "60")
			writeError(w, http.StatusTooManyRequests, "RATE_LIMITED",
				"connector capability refresh is limited to 10 requests per connector per minute", nil)
			return
		}
	}

	// The refresh is scoped to the connector's owning tenant and the
	// calling principal's identity (for the credential lookup); the admin
	// refresh is not run inside an environment-scoped session, so the
	// no-environment credential scope is used.
	result, err := r.connectorRefresher.RefreshCapabilities(req.Context(), conn.TenantID, conn.ID, principal.Subject, "")
	if err != nil {
		writeError(w, http.StatusBadGateway, "CONNECTOR_UNREACHABLE", err.Error(), nil)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(connectorCapabilityResponse{
		Connector:               conn.ID,
		CapabilityInferenceMode: result.Mode,
		Capabilities:            result.Capabilities,
		ToolCapabilities:        result.ToolCapabilities,
		UnannotatedAdminTools:   result.UnannotatedAdminTools,
	})
}
