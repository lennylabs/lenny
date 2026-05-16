// SPDX-License-Identifier: MIT

// Package drainreadiness serves the §12.5 GET /internal/drain-readiness
// endpoint. The lenny-drain-readiness ValidatingAdmissionWebhook
// queries this endpoint before admitting a node-drain pod eviction: it
// runs a MinIO liveness probe so a planned drain cannot evict agent
// pods into a degraded artifact store and lose un-checkpointed
// workspace state.
package drainreadiness

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// probeTimeout bounds the §12.5 MinIO liveness probe so a hung artifact
// store cannot stall the drain-readiness response.
const probeTimeout = 2 * time.Second

// Prober runs the §12.5 pre-drain artifact-store liveness probe — an S3
// HeadBucket on the checkpoints bucket. A nil error reports the store
// healthy.
type Prober interface {
	Probe(ctx context.Context) error
}

// ProberFunc adapts a function to the Prober interface.
type ProberFunc func(ctx context.Context) error

// Probe calls f.
func (f ProberFunc) Probe(ctx context.Context) error { return f(ctx) }

// Handler serves the §12.5 GET /internal/drain-readiness endpoint.
type Handler struct {
	// Prober runs the artifact-store liveness probe. Required.
	Prober Prober
	// Timeout bounds one probe. A non-positive value selects the §12.5
	// default of two seconds.
	Timeout time.Duration
}

// readyResponse is the §12.5 HTTP 200 body.
type readyResponse struct {
	Status string `json:"status"`
	MinIO  string `json:"minio"`
}

// notReadyResponse is the §12.5 HTTP 503 body.
type notReadyResponse struct {
	Status string `json:"status"`
	MinIO  string `json:"minio"`
	Reason string `json:"reason"`
}

// ServeHTTP runs the MinIO liveness probe and reports drain readiness:
// HTTP 200 {"status":"ready","minio":"healthy"} when the probe
// succeeds, HTTP 503 {"status":"not_ready","minio":"unhealthy",
// "reason":"..."} when it fails.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "drain-readiness endpoint accepts GET", http.StatusMethodNotAllowed)
		return
	}
	timeout := h.Timeout
	if timeout <= 0 {
		timeout = probeTimeout
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	w.Header().Set("Content-Type", "application/json")
	if err := h.Prober.Probe(ctx); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(notReadyResponse{
			Status: "not_ready",
			MinIO:  "unhealthy",
			Reason: err.Error(),
		})
		return
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(readyResponse{Status: "ready", MinIO: "healthy"})
}
