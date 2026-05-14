// SPDX-License-Identifier: MIT

// Package logging wraps log/slog with a Handler that auto-attaches the
// correlation fields the spec requires on every log line.
//
// Spec §16.4: every log line MUST include ts (RFC 3339 UTC), level, msg,
// component, and — when present on the originating request — operation_id
// and agent_name. The Handler reads these from a correlation.Fields value
// stored on the log record's context and emits them as top-level structured
// attributes. The slog API itself enforces ts, level, and msg; this handler
// adds the Lenny-specific fields.
package logging

import (
	"context"
	"io"
	"log/slog"

	"github.com/lennylabs/lenny/pkg/observability/correlation"
)

// Options configures a Handler.
type Options struct {
	// Level is the minimum level the handler emits. Records below this
	// level are dropped. Zero value is slog.LevelInfo.
	Level slog.Level

	// AddSource controls whether the slog source location attribute is
	// included on each record. Off by default; production deployments
	// turn it on only for diagnostic builds because of cost.
	AddSource bool

	// DefaultComponent is the value emitted as the "component" field when
	// no Fields value on the context overrides it. Should be set to the
	// binary name in main() — "gateway", "lenny-ops", etc. Empty values
	// produce a log line without a component attribute, which violates
	// §16.4; callers SHOULD set this in main.
	DefaultComponent string
}

// NewJSONHandler returns a slog.Handler that emits structured JSON to w
// and decorates every record with the correlation fields visible on the
// record's context. Use this in production: the spec requires JSON logs
// from gateway, token service, pool controller, runtime adapter, and
// lenny-ops (§16.4).
func NewJSONHandler(w io.Writer, opts Options) slog.Handler {
	inner := slog.NewJSONHandler(w, &slog.HandlerOptions{
		AddSource: opts.AddSource,
		Level:     opts.Level,
	})
	return &handler{inner: inner, defaultComponent: opts.DefaultComponent}
}

// NewTextHandler is the human-readable companion to NewJSONHandler. Use it
// in local development and tests.
func NewTextHandler(w io.Writer, opts Options) slog.Handler {
	inner := slog.NewTextHandler(w, &slog.HandlerOptions{
		AddSource: opts.AddSource,
		Level:     opts.Level,
	})
	return &handler{inner: inner, defaultComponent: opts.DefaultComponent}
}

// Wrap decorates an existing slog.Handler with correlation-field
// extraction. Useful when callers want a custom encoder (test sinks, OTel
// log bridges) but still want the spec-mandated fields injected.
func Wrap(inner slog.Handler, defaultComponent string) slog.Handler {
	return &handler{inner: inner, defaultComponent: defaultComponent}
}

type handler struct {
	inner            slog.Handler
	defaultComponent string
}

func (h *handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

// Handle pulls the correlation Fields from the record's context, projects
// the non-empty values as structured attributes on the record, and
// delegates to the wrapped handler.
func (h *handler) Handle(ctx context.Context, r slog.Record) error {
	f := correlation.From(ctx)
	attrs := make([]slog.Attr, 0, 10)

	component := f.Component
	if component == "" {
		component = h.defaultComponent
	}
	if component != "" {
		attrs = append(attrs, slog.String("component", component))
	}
	if f.TraceID != "" {
		attrs = append(attrs, slog.String("trace_id", f.TraceID))
	}
	if f.SpanID != "" {
		attrs = append(attrs, slog.String("span_id", f.SpanID))
	}
	if f.SessionID != "" {
		attrs = append(attrs, slog.String("session_id", f.SessionID))
	}
	if f.TaskID != "" {
		attrs = append(attrs, slog.String("task_id", f.TaskID))
	}
	if f.TenantID != "" {
		attrs = append(attrs, slog.String("tenant_id", f.TenantID))
	}
	if f.RequestID != "" {
		attrs = append(attrs, slog.String("request_id", f.RequestID))
	}
	if f.OperationID != "" {
		attrs = append(attrs, slog.String("operation_id", f.OperationID))
	}
	if f.AgentName != "" {
		attrs = append(attrs, slog.String("agent_name", f.AgentName))
	}
	if f.RuntimeClass != "" {
		attrs = append(attrs, slog.String("runtime_class", f.RuntimeClass))
	}
	if f.Pool != "" {
		attrs = append(attrs, slog.String("pool", f.Pool))
	}

	if len(attrs) > 0 {
		r.AddAttrs(attrs...)
	}
	return h.inner.Handle(ctx, r)
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &handler{inner: h.inner.WithAttrs(attrs), defaultComponent: h.defaultComponent}
}

func (h *handler) WithGroup(name string) slog.Handler {
	return &handler{inner: h.inner.WithGroup(name), defaultComponent: h.defaultComponent}
}
