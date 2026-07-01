// SPDX-License-Identifier: MIT

// Package drainreadiness serves the §12.5 GET /internal/drain-readiness
// endpoint. The lenny-drain-readiness ValidatingAdmissionWebhook
// queries this endpoint before admitting a node-drain pod eviction: it
// runs a MinIO liveness probe so a planned drain cannot evict agent
// pods into a degraded artifact store and lose un-checkpointed
// workspace state.
//
// The package also serves the §12.5 line 291 POST
// /internal/audit/node-drain-forced endpoint, the durable hop the
// webhook uses to commit the §16.7 `node.drain.forced` audit event into
// the gateway's per-tenant §11.7 audit hash chain.
package drainreadiness

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	obsaudit "github.com/lennylabs/lenny/pkg/observability/audit"
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

// AuditAppender is the §11.7 per-tenant audit hash-chain surface the
// node.drain.forced handler commits to. Both the Postgres-backed
// pkg/gateway/auditstore.Store and an adapter over the in-memory
// audit.ChainSet satisfy it, so the gateway swaps the audit backend
// without changing the handler.
//
// spec: §12.5 line 291; §16.7 node.drain.forced.
type AuditAppender interface {
	Append(ctx context.Context, tenantID, eventType string, payload json.RawMessage, at time.Time) (audit.Row, error)
}

// MetricsSink emits the §12.5 line 291 forced-drain audit-write
// outcome counter. The webhook's metric (when wired) records every
// admission decision; this gateway-side counter records the durable
// audit-write step so a chronic chain-failure mode is visible apart
// from the admission-decision distribution.
//
// spec: §12.5 line 291.
type MetricsSink interface {
	IncDrainReadinessCheck(outcome string)
}

// ForcedDrainAuditRequest is the body the webhook POSTs on a forced
// admission. The tenant field defaults to the platform tenant — the
// drain-force event is a cluster-wide operator action, not a tenant-
// scoped one, but §11.7 routes audit rows to a hash chain keyed by
// tenant; the platform tenant is the catch-all sink for operator
// events.
type ForcedDrainAuditRequest struct {
	Tenant       string `json:"tenant"`
	PodNamespace string `json:"podNamespace"`
	PodName      string `json:"podName"`
	NodeName     string `json:"nodeName"`
	OperatorSub  string `json:"operatorSub,omitempty"`
	EvictedAt    string `json:"evictedAt,omitempty"`
}

// ForcedDrainHandler serves the §12.5 line 291 POST
// /internal/audit/node-drain-forced endpoint. The lenny-drain-readiness
// webhook posts here after admitting an eviction under a
// drain-force override; the handler appends a `node.drain.forced`
// row to the appender's per-tenant §11.7 hash chain.
type ForcedDrainHandler struct {
	// Appender is the §11.7 audit chain. Required.
	Appender AuditAppender
	// Metrics, when set, observes the audit-write outcome on the
	// `lenny_drain_readiness_checks_total{outcome="forced_audited"}`
	// or `{outcome="audit_failed"}` label.
	Metrics MetricsSink
	// PlatformTenant is the §11.7 catch-all tenant for operator-scope
	// events. A zero value selects "platform".
	PlatformTenant string
	// Clock overrides time.Now for tests.
	Clock func() time.Time
}

// ServeHTTP appends the §16.7 node.drain.forced audit event to the
// gateway's chain. The request body MUST be a ForcedDrainAuditRequest.
// On a chain-write failure the handler returns HTTP 503 so the webhook
// can deny the eviction fail-closed.
//
// spec: §12.5 line 291; §16.7 node.drain.forced.
func (h *ForcedDrainHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "node.drain.forced audit endpoint accepts POST", http.StatusMethodNotAllowed)
		return
	}
	if h.Appender == nil {
		http.Error(w, "audit chain not configured", http.StatusServiceUnavailable)
		return
	}
	var req ForcedDrainAuditRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.PodName == "" || req.NodeName == "" {
		http.Error(w, "podName and nodeName are required", http.StatusBadRequest)
		return
	}
	tenant := req.Tenant
	if tenant == "" {
		tenant = h.PlatformTenant
	}
	if tenant == "" {
		tenant = "platform"
	}
	now := time.Now
	if h.Clock != nil {
		now = h.Clock
	}
	if req.EvictedAt == "" {
		req.EvictedAt = now().UTC().Format(time.RFC3339)
	}
	payload, err := json.Marshal(map[string]any{
		"pod_namespace": req.PodNamespace,
		"pod_name":      req.PodName,
		"node_name":     req.NodeName,
		"operator_sub":  req.OperatorSub,
		"evicted_at":    req.EvictedAt,
	})
	if err != nil {
		http.Error(w, "marshal payload: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := h.Appender.Append(r.Context(), tenant, string(obsaudit.EventNodeDrainForced), payload, now().UTC()); err != nil {
		if h.Metrics != nil {
			h.Metrics.IncDrainReadinessCheck("audit_failed")
		}
		http.Error(w, "audit append: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	if h.Metrics != nil {
		h.Metrics.IncDrainReadinessCheck("forced_audited")
	}
	w.WriteHeader(http.StatusNoContent)
}

// ErrAuditAppenderUnset is returned by callers that construct a
// ForcedDrainHandler without an Appender.
var ErrAuditAppenderUnset = errors.New("drainreadiness: AuditAppender is required")
