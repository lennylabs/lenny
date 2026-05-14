// SPDX-License-Identifier: MIT

// Package tracing wraps go.opentelemetry.io/otel/trace with span naming
// and attribute conventions from spec §16.3.
//
// Span names come from the §16.3 catalog: session.create, session.claim_pod,
// session.upload, session.tool_call, delegation.spawn_child, etc. The
// catalog is exposed as typed SpanName constants so callers cannot mistype
// a span name and silently emit one off-spec.
//
// Every span started through this package carries the correlation attributes
// the spec mandates on tier-1 traces (trace_id and span_id are part of the
// OTel span; the rest — tenant_id, session_id, task_id, operation_id,
// agent_name, component, runtime_class, pool — are projected from the
// correlation.Fields value on the span's context).
//
// The structured error taxonomy from §16.3 (TRANSIENT, PERMANENT, POLICY,
// UPSTREAM) is mapped to OTel status codes by Span.RecordError.
package tracing

import (
	"context"
	"errors"

	"github.com/lennylabs/lenny/pkg/observability/correlation"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// SpanName enumerates the span boundaries instrumented per §16.3. Callers
// should pass a SpanName constant rather than a free-form string so the
// catalog stays the source of truth.
type SpanName string

// Span names from the §16.3 catalog. Add a constant here when the spec
// adds a row; the catalog is intentionally explicit so a typo cannot
// silently invent a span.
const (
	SpanSessionCreate           SpanName = "session.create"
	SpanSessionClaimPod         SpanName = "session.claim_pod"
	SpanSessionUpload           SpanName = "session.upload"
	SpanSessionFinalizeWorkspace SpanName = "session.finalize_workspace"
	SpanSessionRunSetup         SpanName = "session.run_setup"
	SpanSessionStart            SpanName = "session.start"
	SpanSessionPrompt           SpanName = "session.prompt"
	SpanSessionToolCall         SpanName = "session.tool_call"
	SpanSessionCheckpoint       SpanName = "session.checkpoint"
	SpanSessionSealAndExport    SpanName = "session.seal_and_export"
	SpanDelegationSpawnChild    SpanName = "delegation.spawn_child"
	SpanDelegationAwaitChild    SpanName = "delegation.await_child"
	SpanDelegationExportFiles   SpanName = "delegation.export_files"
	SpanDelegationBudgetReserve SpanName = "delegation.budget_reserve"
	SpanDelegationBudgetReturn  SpanName = "delegation.budget_return"
	SpanMCPExternalToolCall     SpanName = "mcp.external_tool_call"
	SpanMCPElicitation          SpanName = "mcp.elicitation"
	SpanCredentialAssign        SpanName = "credential.assign"
	SpanCredentialRotate        SpanName = "credential.rotate"
	SpanCredentialFallbackChain SpanName = "credential.fallback_chain"
	SpanCredentialProxyRequest  SpanName = "credential.proxy_request"
	SpanCoordinatorHandoff      SpanName = "coordinator.handoff"
)

// SpanNames returns the full catalog. Tests use it to assert exhaustive
// coverage.
func SpanNames() []SpanName {
	return []SpanName{
		SpanSessionCreate, SpanSessionClaimPod, SpanSessionUpload,
		SpanSessionFinalizeWorkspace, SpanSessionRunSetup, SpanSessionStart,
		SpanSessionPrompt, SpanSessionToolCall, SpanSessionCheckpoint,
		SpanSessionSealAndExport,
		SpanDelegationSpawnChild, SpanDelegationAwaitChild,
		SpanDelegationExportFiles, SpanDelegationBudgetReserve,
		SpanDelegationBudgetReturn,
		SpanMCPExternalToolCall, SpanMCPElicitation,
		SpanCredentialAssign, SpanCredentialRotate,
		SpanCredentialFallbackChain, SpanCredentialProxyRequest,
		SpanCoordinatorHandoff,
	}
}

// ErrorCategory enumerates the §16.3 structured error taxonomy.
type ErrorCategory string

const (
	CategoryTransient ErrorCategory = "TRANSIENT"
	CategoryPermanent ErrorCategory = "PERMANENT"
	CategoryPolicy    ErrorCategory = "POLICY"
	CategoryUpstream  ErrorCategory = "UPSTREAM"
)

// CategorizedError carries the §16.3 error taxonomy. Wrap a domain error
// in CategorizedError to give RecordError the category to attach as an
// attribute on the span.
type CategorizedError struct {
	Category ErrorCategory
	Err      error
}

func (e *CategorizedError) Error() string { return e.Err.Error() }
func (e *CategorizedError) Unwrap() error { return e.Err }

// AttrErrorCategory is the attribute key used to record §16.3 categories
// on OTel spans.
const AttrErrorCategory = "error.category"

// Tracer is the Lenny-flavored wrapper around trace.Tracer. Construct it
// with NewTracer.
type Tracer struct {
	inner trace.Tracer
}

// NewTracer returns a Tracer backed by the supplied otel.Tracer. Pass the
// process-wide tracer obtained from otel.GetTracerProvider().Tracer(name).
// In tests, the package can be exercised against any trace.Tracer including
// the otel global no-op tracer; spans recorded against the no-op tracer are
// observable through OTel SDK-supplied span recorders the test installs.
func NewTracer(inner trace.Tracer) *Tracer {
	if inner == nil {
		inner = otel.Tracer("github.com/lennylabs/lenny")
	}
	return &Tracer{inner: inner}
}

// Start opens a span named by SpanName and projects the correlation fields
// from ctx as span attributes. The returned context is the child context to
// pass to descendant code; the Span is the handle for AddEvent, SetAttributes,
// RecordError, and End calls.
func (t *Tracer) Start(ctx context.Context, name SpanName, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	childCtx, span := t.inner.Start(ctx, string(name), opts...)
	span.SetAttributes(correlationAttributes(correlation.From(ctx))...)
	return childCtx, span
}

// correlationAttributes returns the OTel attributes derived from a Fields
// value. Empty values are skipped.
func correlationAttributes(f correlation.Fields) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0, 8)
	if f.TenantID != "" {
		attrs = append(attrs, attribute.String("tenant_id", f.TenantID))
	}
	if f.SessionID != "" {
		attrs = append(attrs, attribute.String("session_id", f.SessionID))
	}
	if f.TaskID != "" {
		attrs = append(attrs, attribute.String("task_id", f.TaskID))
	}
	if f.OperationID != "" {
		attrs = append(attrs, attribute.String("operation_id", f.OperationID))
	}
	if f.AgentName != "" {
		attrs = append(attrs, attribute.String("agent_name", f.AgentName))
	}
	if f.Component != "" {
		attrs = append(attrs, attribute.String("component", f.Component))
	}
	if f.RuntimeClass != "" {
		attrs = append(attrs, attribute.String("runtime_class", f.RuntimeClass))
	}
	if f.Pool != "" {
		attrs = append(attrs, attribute.String("pool", f.Pool))
	}
	return attrs
}

// RecordError attaches err to the supplied span and maps its §16.3 category
// to an OTel status. A nil err is a no-op so callers can pass through error
// values from deferred functions without a guard.
//
// The status mapping follows §16.3:
//   - TRANSIENT, UPSTREAM:  codes.Error (caller may retry or escalate)
//   - PERMANENT:            codes.Error (caller MUST NOT retry)
//   - POLICY:               codes.Error (denied by policy engine)
//
// The category itself is recorded as a span attribute so dashboards can
// distinguish them; OTel's status code intentionally collapses to Error
// for any non-Ok value.
func RecordError(span trace.Span, err error) {
	if err == nil || span == nil {
		return
	}
	span.RecordError(err)

	var cat *CategorizedError
	if errors.As(err, &cat) {
		span.SetAttributes(attribute.String(AttrErrorCategory, string(cat.Category)))
	}
	span.SetStatus(codes.Error, err.Error())
}

// CategorizeError wraps err with the supplied §16.3 category. Returns nil
// when err is nil so callers can chain it inside `return CategorizeError(...)`
// without a guard.
func CategorizeError(err error, category ErrorCategory) error {
	if err == nil {
		return nil
	}
	return &CategorizedError{Category: category, Err: err}
}
