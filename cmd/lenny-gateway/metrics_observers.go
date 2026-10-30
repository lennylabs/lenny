// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"net/http"
	"strings"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/metrics/gatewaymetrics"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
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

// treeCycleEmitter increments the
// `lenny_delegation_tree_cycle_detected_total` counter when a §8.9
// tree walker hits a cycle in the §8.2 ParentSessionID lineage. Each
// tree-walker surface (REST `/v1/sessions/{id}/tree`, MCP
// `lenny/get_task_tree`) wraps this emitter in a per-package adapter
// that matches the package's TreeCycleObserver interface; both
// adapters fan into the same metric so the corruption surfaces
// regardless of which transport walked the tree. The audit-row half
// of the §8.9 finding (a `delegation.tree_cycle_detected` row) is
// not yet emitted: §16.7 is a closed catalog of spec-listed events
// and the new event type requires a spec change. spec: §8.9; F-8.9.10 (metric half closed, audit half deferred to a
// future spec addition).
type treeCycleEmitter struct {
	metrics *gatewaymetrics.Metrics
}

func (e treeCycleEmitter) emit(_ context.Context, tenantID, _, _, source string) {
	if e.metrics != nil {
		e.metrics.IncDelegationTreeCycleDetected(tenantID, source)
	}
	// The cycle-detected metric is the operator-visible signal in v1.
	// A `delegation.tree_cycle_detected` audit row is the cleaner long-
	// run answer but lands with the §16.7 catalog extension.
}

// sessionserverTreeCycleObserver adapts treeCycleEmitter to
// sessionserver.TreeCycleObserver for the REST /v1/sessions/{id}/tree
// walker. spec: §8.9; F-8.9.10.
type sessionserverTreeCycleObserver struct {
	emitter treeCycleEmitter
}

func (o sessionserverTreeCycleObserver) OnTreeCycle(ctx context.Context, ev sessionserver.TreeCycleEvent) {
	o.emitter.emit(ctx, ev.TenantID, ev.RootSessionID, ev.CycleNodeID, ev.Source)
}

// mcpToolsTreeCycleObserver adapts treeCycleEmitter to
// mcptools.TreeCycleObserver for the lenny/get_task_tree platform-
// tool walker. spec: §8.9; F-8.9.10.
type mcpToolsTreeCycleObserver struct {
	emitter treeCycleEmitter
}

func (o mcpToolsTreeCycleObserver) OnTreeCycle(ctx context.Context, ev mcptools.TreeCycleEvent) {
	o.emitter.emit(ctx, ev.TenantID, ev.RootSessionID, ev.CycleNodeID, ev.Source)
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
