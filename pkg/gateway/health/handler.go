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
// The HTTP status code mirrors the verdict: healthy/degraded → 200,
// unhealthy → 503, so liveness/readiness probes can consume the
// endpoint directly.
func Handler(agg *Aggregator) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/admin/health", func(w http.ResponseWriter, r *http.Request) {
		report := agg.Report(r.Context())
		writeReport(w, report)
	})
	mux.HandleFunc("GET /v1/admin/health/summary", func(w http.ResponseWriter, r *http.Request) {
		report := agg.Report(r.Context())
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode(report.Status))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":         report.Status,
			"componentCount": len(report.Components),
		})
	})
	mux.HandleFunc("GET /v1/admin/health/{component}", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("component")
		comp, ok := agg.Component(r.Context(), name)
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{
					"code":    "RESOURCE_NOT_FOUND",
					"message": "no health checker registered for component " + name,
				},
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode(comp.Status))
		_ = json.NewEncoder(w).Encode(comp)
	})
	return mux
}

func writeReport(w http.ResponseWriter, report Report) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode(report.Status))
	_ = json.NewEncoder(w).Encode(report)
}

// statusCode maps a §25.3 verdict to an HTTP status. healthy and
// degraded both return 200 (the platform is still serving);
// unhealthy returns 503 so a probe fails closed.
func statusCode(s Status) int {
	if s == StatusUnhealthy {
		return http.StatusServiceUnavailable
	}
	return http.StatusOK
}
