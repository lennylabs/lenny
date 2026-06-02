// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"net/http"

	"github.com/lennylabs/lenny/pkg/preflight"
)

// InfraPreflighter runs the §15.1 line 890 infrastructure-connectivity
// preflight (Postgres, Redis, MinIO connectivity and schema version)
// against the gateway's configured backends and returns one result per
// check. *infra.Run wired against the gateway's resolved DSNs satisfies
// it; tests inject a fake. spec: §15.1 line 890; §24.2.
type InfraPreflighter interface {
	Preflight(ctx context.Context) []preflight.CheckResult
}

// InfraPreflightFunc adapts a function to InfraPreflighter.
type InfraPreflightFunc func(ctx context.Context) []preflight.CheckResult

// Preflight calls f.
func (f InfraPreflightFunc) Preflight(ctx context.Context) []preflight.CheckResult {
	return f(ctx)
}

// WithPreflight wires the §15.1 line 890 infrastructure preflight onto
// the Router, registering `POST /v1/admin/preflight`. Without it the
// endpoint stays unregistered (a 404), so a gateway with no configured
// backends does not advertise a probe it cannot run.
func (r *Router) WithPreflight(p InfraPreflighter) *Router {
	r.preflighter = p
	return r
}

// PreflightCheckResult is one check in the §24.2 preflight report.
type PreflightCheckResult struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Reason string `json:"reason"`
}

// PreflightResponse is the `POST /v1/admin/preflight` response body. The
// CLI's API-backed mode renders the same report the standalone mode
// produces locally, and exits non-zero when Passed is false.
type PreflightResponse struct {
	Passed bool                   `json:"passed"`
	Checks []PreflightCheckResult `json:"checks"`
}

// handlePreflight implements POST /v1/admin/preflight. It runs the
// active outbound connectivity probes against the gateway's configured
// backends and returns the full report. A failed check yields
// Passed=false in the body with HTTP 200: the probe itself succeeded;
// the negative finding is the payload, not a processing error.
//
// spec: §15.1 line 890; §24.2.
func (r *Router) handlePreflight(w http.ResponseWriter, req *http.Request) {
	report := r.preflighter.Preflight(req.Context())
	resp := PreflightResponse{Passed: !preflight.Failed(report), Checks: make([]PreflightCheckResult, 0, len(report))}
	for _, c := range report {
		resp.Checks = append(resp.Checks, PreflightCheckResult{
			Name:   c.Name,
			Passed: c.Decision.Passed,
			Reason: c.Decision.Reason,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}
