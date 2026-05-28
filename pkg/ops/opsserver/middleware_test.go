// SPDX-License-Identifier: MIT

package opsserver

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/observability/correlation"
	"github.com/lennylabs/lenny/pkg/observability/logging"
)

// TestWithCorrelationStampsRequestContext_spec_25_4_2499 covers §25.4
// lines 2499-2509: the middleware reads the §25.2 correlation headers
// off the inbound request and stamps a correlation.Fields value onto the
// request context for downstream handlers.
func TestWithCorrelationStampsRequestContext_spec_25_4_2499(t *testing.T) {
	var captured correlation.Fields
	h := withCorrelation(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = correlation.From(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/ops/health", nil)
	req.Header.Set(correlation.HeaderOperationID, "op-abc-123")
	req.Header.Set(correlation.HeaderAgentName, "prod-watchdog")
	req.Header.Set(correlation.HeaderTraceParent, "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	req.Header.Set(correlation.HeaderTenantID, "acme")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if captured.OperationID != "op-abc-123" {
		t.Errorf("operation_id = %q, want %q", captured.OperationID, "op-abc-123")
	}
	if captured.AgentName != "prod-watchdog" {
		t.Errorf("agent_name = %q, want %q", captured.AgentName, "prod-watchdog")
	}
	if captured.TraceID != "0af7651916cd43dd8448eb211c80319c" {
		t.Errorf("trace_id = %q, want the traceparent trace-id segment", captured.TraceID)
	}
	if captured.TenantID != "acme" {
		t.Errorf("tenant_id = %q, want %q", captured.TenantID, "acme")
	}
	if captured.Component != "lenny-ops" {
		t.Errorf("component = %q, want %q (§16.4 binary label)", captured.Component, "lenny-ops")
	}
}

// TestWithCorrelationEmptyHeadersStillStampsComponent_spec_25_4_2499
// covers the §25.4 default-component invariant: every log line carries
// component=lenny-ops even when no correlation headers were supplied by
// the caller.
func TestWithCorrelationEmptyHeadersStillStampsComponent_spec_25_4_2499(t *testing.T) {
	var captured correlation.Fields
	h := withCorrelation(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = correlation.From(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if captured.Component != "lenny-ops" {
		t.Errorf("component = %q, want %q with no inbound correlation headers", captured.Component, "lenny-ops")
	}
	if captured.OperationID != "" {
		t.Errorf("operation_id = %q, want empty", captured.OperationID)
	}
}

// TestAccessLogEmitsStructuredLine_spec_25_4_2512 covers §25.4 lines
// 2512-2526: each request produces a JSON log line carrying ts, level,
// msg, component, operation_id, agent_name, and trace_id (when present).
// The slog handler chain is the one configured by main; here we install
// the same handler against a buffer and assert the projection.
func TestAccessLogEmitsStructuredLine_spec_25_4_2512(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	defer slog.SetDefault(prev)
	slog.SetDefault(slog.New(logging.NewJSONHandler(&buf, logging.Options{
		Level:            slog.LevelInfo,
		DefaultComponent: "lenny-ops",
	})))

	h := withCorrelation(withAccessLog(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})))
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/ops/health", nil)
	req.Header.Set(correlation.HeaderOperationID, "op-trace-1")
	req.Header.Set(correlation.HeaderAgentName, "prod-watchdog")
	req.Header.Set(correlation.HeaderTraceParent, "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatalf("expected one structured log line, got empty buffer")
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("log line is not JSON: %v\nline: %s", err, line)
	}
	for _, field := range []string{"time", "level", "msg", "component", "operation_id", "agent_name", "trace_id"} {
		if _, ok := got[field]; !ok {
			t.Errorf("log line missing %q field (§25.4 lines 2512-2526)\nline: %s", field, line)
		}
	}
	if got["component"] != "lenny-ops" {
		t.Errorf("component = %v, want %q", got["component"], "lenny-ops")
	}
	if got["operation_id"] != "op-trace-1" {
		t.Errorf("operation_id = %v, want %q", got["operation_id"], "op-trace-1")
	}
	if got["agent_name"] != "prod-watchdog" {
		t.Errorf("agent_name = %v, want %q", got["agent_name"], "prod-watchdog")
	}
	if got["trace_id"] != "0af7651916cd43dd8448eb211c80319c" {
		t.Errorf("trace_id = %v, want the traceparent trace-id segment", got["trace_id"])
	}
	if got["status"] == nil {
		t.Errorf("access log line missing status (response status code)")
	}
	if got["method"] != http.MethodGet {
		t.Errorf("method = %v, want GET", got["method"])
	}
}

// TestWithCorrelationMergesWithExistingContext_spec_25_4_2499 ensures
// the middleware merges its extracted Fields onto any existing context
// scope rather than replacing it, so component / runtime_class fields
// stamped by upstream wrappers survive.
func TestWithCorrelationMergesWithExistingContext_spec_25_4_2499(t *testing.T) {
	var captured correlation.Fields
	pre := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := correlation.With(r.Context(), correlation.Fields{RuntimeClass: "claude-code"})
		withCorrelation(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			captured = correlation.From(r.Context())
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(w, r.WithContext(ctx))
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(correlation.HeaderOperationID, "op-merge")
	rr := httptest.NewRecorder()
	pre.ServeHTTP(rr, req)

	if captured.OperationID != "op-merge" {
		t.Errorf("operation_id = %q, want merged value", captured.OperationID)
	}
	if captured.RuntimeClass != "claude-code" {
		t.Errorf("runtime_class = %q, want pre-existing scope to survive merge", captured.RuntimeClass)
	}
}

// TestContextWithComponentStampsBinaryLabel_spec_16_4 covers the §16.4
// helper used by background loops (outside the HTTP path) so log lines
// emitted from leader-elected reconcilers also carry the binary label.
func TestContextWithComponentStampsBinaryLabel_spec_16_4(t *testing.T) {
	ctx := ContextWithComponent(context.Background())
	got := correlation.From(ctx)
	if got.Component != "lenny-ops" {
		t.Errorf("component = %q, want %q", got.Component, "lenny-ops")
	}
}
