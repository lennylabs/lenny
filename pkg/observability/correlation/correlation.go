// SPDX-License-Identifier: MIT

// Package correlation carries the correlation fields the observability spec
// mandates on every log line, span attribute, and inter-service request.
//
// Spec references:
//   - §16.3 Distributed Tracing: trace_id, span_id propagated through every
//     gateway↔pod↔external-tool hop.
//   - §16.4 Logging: every log line MUST include ts, level, msg, component
//     and — when present on the originating request — operation_id and
//     agent_name (carried in the X-Lenny-Operation-ID and X-Lenny-Agent-Name
//     headers, §25.4 agent-operability).
//
// The package exposes a Fields value, helpers to read or write it on a
// context.Context, and helpers to inject and extract the fields across
// HTTP headers and gRPC metadata. It does not depend on slog, OTel, or any
// transport; the logging, tracing, and gRPC layers consume Fields and
// project the subset they need.
package correlation

import (
	"context"
	"net/http"
	"strings"
)

// HTTP header names the spec assigns to correlation fields that travel on
// inbound and outbound requests. The values match §25.4 (operation_id and
// agent_name) and the W3C Trace Context recommendation (traceparent).
const (
	HeaderOperationID = "X-Lenny-Operation-ID"
	HeaderAgentName   = "X-Lenny-Agent-Name"
	HeaderTenantID    = "X-Lenny-Tenant-ID"
	HeaderSessionID   = "X-Lenny-Session-ID"
	HeaderTraceParent = "traceparent"
)

// Fields holds the correlation values for a single execution scope.
// Zero values mean "not set on this scope"; callers MUST NOT use the empty
// string to mean a value of the same name. The String method renders only
// the populated fields.
type Fields struct {
	// TraceID is the OpenTelemetry trace ID for the current trace. Lower-case
	// hex, 32 characters. Empty means the scope is not under an active trace.
	TraceID string

	// SpanID is the OpenTelemetry span ID for the current span. Lower-case
	// hex, 16 characters. Empty means the scope is between spans (the trace
	// is established but no span is currently active).
	SpanID string

	// SessionID is the Lenny session this scope is acting on. Empty for
	// session-creation paths that have not yet allocated an ID.
	SessionID string

	// TaskID is the Lenny task this scope is acting on. Empty when the scope
	// is not driven by a TaskRecord.
	TaskID string

	// TenantID is the tenant that owns the request or resource. Required on
	// every audit and policy decision path.
	TenantID string

	// RequestID is the gateway-assigned per-request identifier. Useful for
	// stitching log lines emitted by independent goroutines that serve the
	// same request.
	RequestID string

	// OperationID is the agent-operability correlation token (X-Lenny-
	// Operation-ID, §25.4). It allows joining a single orchestrated agent
	// task across the gateway, lenny-ops, and any controller invocations
	// the task touched.
	OperationID string

	// AgentName is the agent that originated the request (X-Lenny-Agent-Name,
	// §25.4). Used by lenny-ops to attribute platform-side work to a
	// specific operator agent.
	AgentName string

	// Component identifies the binary emitting the log line — "gateway",
	// "lenny-ops", "warm-pool-controller", etc. Required on every log line
	// by §16.4.
	Component string

	// RuntimeClass identifies the runtime executing under this scope (e.g.,
	// "claude-code", "chat"). Empty on platform-only paths (admin API,
	// controllers reconciling without a session).
	RuntimeClass string

	// Pool identifies the warm pool the scope is acting on. Empty on paths
	// outside pool lifecycle.
	Pool string
}

// IsEmpty reports whether the Fields value carries no correlation values.
func (f Fields) IsEmpty() bool {
	return f.TraceID == "" && f.SpanID == "" && f.SessionID == "" && f.TaskID == "" &&
		f.TenantID == "" && f.RequestID == "" && f.OperationID == "" && f.AgentName == "" &&
		f.Component == "" && f.RuntimeClass == "" && f.Pool == ""
}

// Merge returns a Fields where each value from other overrides the same
// value in f when other's field is non-empty. The receiver is not mutated.
// This is the helper inbound transports use after extracting a partial
// Fields from headers: they merge the extracted values onto whatever scope
// they already had.
func (f Fields) Merge(other Fields) Fields {
	if other.TraceID != "" {
		f.TraceID = other.TraceID
	}
	if other.SpanID != "" {
		f.SpanID = other.SpanID
	}
	if other.SessionID != "" {
		f.SessionID = other.SessionID
	}
	if other.TaskID != "" {
		f.TaskID = other.TaskID
	}
	if other.TenantID != "" {
		f.TenantID = other.TenantID
	}
	if other.RequestID != "" {
		f.RequestID = other.RequestID
	}
	if other.OperationID != "" {
		f.OperationID = other.OperationID
	}
	if other.AgentName != "" {
		f.AgentName = other.AgentName
	}
	if other.Component != "" {
		f.Component = other.Component
	}
	if other.RuntimeClass != "" {
		f.RuntimeClass = other.RuntimeClass
	}
	if other.Pool != "" {
		f.Pool = other.Pool
	}
	return f
}

type ctxKey struct{}

// With returns a child context carrying fields. Subsequent From calls on
// the returned context resolve to the same Fields. If the parent already
// carries a Fields, the new value replaces it; merge before calling if
// merge semantics are desired.
func With(parent context.Context, fields Fields) context.Context {
	return context.WithValue(parent, ctxKey{}, fields)
}

// From returns the Fields attached to ctx. When no Fields have been
// attached, the zero value is returned.
func From(ctx context.Context) Fields {
	if ctx == nil {
		return Fields{}
	}
	if v, ok := ctx.Value(ctxKey{}).(Fields); ok {
		return v
	}
	return Fields{}
}

// WithComponent attaches (or replaces) the component value on ctx without
// touching other fields. Convenience for binary mains and goroutines that
// only own the component label.
func WithComponent(parent context.Context, component string) context.Context {
	f := From(parent)
	f.Component = component
	return With(parent, f)
}

// FromHTTPHeader extracts the spec-defined correlation headers from h.
// The returned Fields is suitable for Merge against an existing scope.
// Headers not present in h are returned as empty values.
func FromHTTPHeader(h http.Header) Fields {
	if h == nil {
		return Fields{}
	}
	traceID, spanID := parseTraceparent(h.Get(HeaderTraceParent))
	return Fields{
		TraceID:     traceID,
		SpanID:      spanID,
		SessionID:   h.Get(HeaderSessionID),
		TenantID:    h.Get(HeaderTenantID),
		OperationID: h.Get(HeaderOperationID),
		AgentName:   h.Get(HeaderAgentName),
	}
}

// InjectHTTPHeader writes the populated correlation values in f onto h.
// Fields that are empty on f are left untouched on h (the inbound value,
// if any, survives).
func (f Fields) InjectHTTPHeader(h http.Header) {
	if h == nil {
		return
	}
	if f.TraceID != "" {
		h.Set(HeaderTraceParent, formatTraceparent(f.TraceID, f.SpanID))
	}
	if f.SessionID != "" {
		h.Set(HeaderSessionID, f.SessionID)
	}
	if f.TenantID != "" {
		h.Set(HeaderTenantID, f.TenantID)
	}
	if f.OperationID != "" {
		h.Set(HeaderOperationID, f.OperationID)
	}
	if f.AgentName != "" {
		h.Set(HeaderAgentName, f.AgentName)
	}
}

// FromGRPCMetadata extracts correlation fields from a metadata map shaped
// the same way google.golang.org/grpc/metadata stores values (lower-case
// keys, slice values, first element is the canonical value). It accepts
// any map type with that contract so the package can stay free of the
// gRPC dependency in v1.
func FromGRPCMetadata(md map[string][]string) Fields {
	if md == nil {
		return Fields{}
	}
	first := func(key string) string {
		v, ok := md[strings.ToLower(key)]
		if !ok || len(v) == 0 {
			return ""
		}
		return v[0]
	}
	traceID, spanID := parseTraceparent(first(HeaderTraceParent))
	return Fields{
		TraceID:     traceID,
		SpanID:      spanID,
		SessionID:   first(HeaderSessionID),
		TenantID:    first(HeaderTenantID),
		OperationID: first(HeaderOperationID),
		AgentName:   first(HeaderAgentName),
	}
}

// parseTraceparent returns the trace-id and parent-id from a W3C
// traceparent header value of the form "00-<trace-id>-<span-id>-<flags>".
// Malformed values yield two empty strings so callers can treat the
// header as absent.
func parseTraceparent(v string) (traceID, spanID string) {
	if v == "" {
		return "", ""
	}
	parts := strings.Split(v, "-")
	if len(parts) != 4 || parts[0] != "00" || len(parts[1]) != 32 || len(parts[2]) != 16 {
		return "", ""
	}
	return parts[1], parts[2]
}

// formatTraceparent renders the W3C traceparent header value. The sampled
// flag is always set to "01" because Lenny's collector applies tail-based
// sampling per §16.3 — the sampled bit at the source is not load-bearing.
func formatTraceparent(traceID, spanID string) string {
	if spanID == "" {
		spanID = "0000000000000000"
	}
	return "00-" + traceID + "-" + spanID + "-01"
}
