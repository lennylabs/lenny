// SPDX-License-Identifier: MIT

package health

import (
	"encoding/json"
	"net/http"
)

// Handler returns the §25.3 Platform Health API http.Handler over
// the supplied Aggregator. It mounts:
//
//	GET /v1/admin/health             — full report
//	GET /v1/admin/health/summary     — status + component count only
//	GET /v1/admin/health/{component} — single-component health
//
// The endpoints are unauthenticated-readable per §25.3 (health is
// not sensitive) but the gateway mounts them behind the admin path
// prefix so deployers can gate them with NetworkPolicy if desired.
//
// The health endpoint itself never returns 5xx — a verdict of
// `unhealthy` still returns 200 with the observed state in the body, so
// an agent reading the endpoint can distinguish "the platform is
// unhealthy" (a 200 carrying status: unhealthy) from "the request to
// the health endpoint failed" (a transport error). spec: §25.3 line
// 530 — "The health endpoint itself never returns 5xx — it reports what
// it can observe." Liveness and readiness probes therefore must not key
// on the health endpoint's HTTP status; they use the dedicated
// /healthz / /readyz probes instead.
func Handler(agg *Aggregator) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/admin/health", func(w http.ResponseWriter, r *http.Request) {
		report := agg.Report(r.Context())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(report)
	})
	mux.HandleFunc("GET /v1/admin/health/summary", func(w http.ResponseWriter, r *http.Request) {
		report := agg.Report(r.Context())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":         report.Status,
			"componentCount": len(report.Components),
		})
	})
	mux.HandleFunc("GET /v1/admin/health/{component}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("component")
		comp, ok := agg.Component(r.Context(), name)
		if !ok {
			// spec: §25.3 line 547 — UNKNOWN_HEALTH_COMPONENT (404) is
			// the only error code the health surface returns. A missing
			// component is a 4xx client error, not the forbidden 5xx.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{
					"code":    "UNKNOWN_HEALTH_COMPONENT",
					"message": "no health checker registered for component " + name,
				},
			})
			return
		}
		// spec: §25.3 line 530 — a degraded or unhealthy single
		// component still returns 200; the verdict rides in the body.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(comp)
	})
	return mux
}
