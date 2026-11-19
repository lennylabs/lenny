// SPDX-License-Identifier: MIT

package main

import (
	"net/http"
	"strings"

	"github.com/lennylabs/lenny/pkg/gateway/replica/gatewaymetrics"
)

// memoryStoreObserver adapts the gatewaymetrics emitters into the §9.4
// MemoryStore Observer contract so the in-memory and Postgres
// backends route their per-operation metrics through one bound
// `backend` label. spec: §9.4 / §16.1. F-9.4.1.
type memoryStoreObserver struct {
	metrics *gatewaymetrics.Metrics
	backend string
}

func (o memoryStoreObserver) ObserveOperation(op string, seconds float64) {
	o.metrics.ObserveMemoryStoreOperation(op, o.backend, seconds)
}

func (o memoryStoreObserver) IncError(op, errorType string) {
	o.metrics.IncMemoryStoreError(op, o.backend, errorType)
}

func (o memoryStoreObserver) SetRecordCount(tenantID string, count int) {
	o.metrics.SetMemoryStoreRecordCount(tenantID, count)
}

func (o memoryStoreObserver) IncUserOverThreshold(tenantID string) {
	o.metrics.IncMemoryStoreUserOverThreshold(tenantID, o.backend)
}

// routeTemplate collapses a request path to a stable §16.1.1
// low-cardinality route label so the request metric does not
// explode into one series per session id / blob ref.
func routeTemplate(r *http.Request) string {
	p := r.URL.Path
	switch {
	case p == "/healthz", p == "/metrics", p == "/v1/sessions",
		p == "/v1/sessions/start", p == "/v1/chat/completions",
		p == "/v1/responses", p == "/mcp", p == "/openapi.yaml",
		p == "/openapi.json", p == "/v1/openapi.json":
		return p
	case strings.HasPrefix(p, "/v1/sessions/"):
		return "/v1/sessions/{id}/*"
	case strings.HasPrefix(p, "/v1/blobs/"):
		return "/v1/blobs/{ref}"
	case strings.HasPrefix(p, "/v1/responses/"):
		return "/v1/responses/{id}"
	case strings.HasPrefix(p, "/v1/admin/"):
		return "/v1/admin/*"
	case strings.HasPrefix(p, "/v1/oauth/"):
		return "/v1/oauth/*"
	default:
		return "other"
	}
}
