// SPDX-License-Identifier: MIT

package correlation

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// withTraceContextPropagator installs the W3C trace-context propagator for
// the duration of a test and restores the prior global propagator after.
// In production tracing.InitProvider installs this propagator; the tests
// install it locally so otel.GetTextMapPropagator is not the no-op default.
func withTraceContextPropagator(t *testing.T) {
	t.Helper()
	prev := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTextMapPropagator(prev) })
}

// spec: §16.3 lines 320, 326 ("Client → Gateway (HTTP headers)") — an inbound
// W3C traceparent is extracted into the OTel context so a downstream span
// continues the client's trace rather than starting a detached root. The
// regression the finding names ("a traceparent received from a client is
// discarded") is closed when the extracted remote SpanContext carries the
// inbound trace id. F-16.3.3.
func TestWrapExtractsInboundTraceparentIntoOTelContext_spec_16_3_326(t *testing.T) {
	withTraceContextPropagator(t)

	const wantTrace = "0af7651916cd43dd8448eb211c80319c"
	const wantSpan = "b7ad6b7169203331"

	var got trace.SpanContext
	inner := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = trace.SpanContextFromContext(r.Context())
	})
	h := Wrap(inner, Options{})

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	req.Header.Set("traceparent", "00-"+wantTrace+"-"+wantSpan+"-01")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if !got.IsValid() {
		t.Fatal("downstream context carried no remote span context")
	}
	if got.TraceID().String() != wantTrace {
		t.Errorf("extracted trace id = %q, want %q", got.TraceID().String(), wantTrace)
	}
	if got.SpanID().String() != wantSpan {
		t.Errorf("extracted parent span id = %q, want %q", got.SpanID().String(), wantSpan)
	}
	if !got.IsRemote() {
		t.Error("extracted span context is not marked remote")
	}
}

// spec: §16.3 line 326 — a request with no traceparent yields no remote span
// context, so a downstream span starts a fresh root rather than inheriting a
// bogus parent. F-16.3.3.
func TestWrapWithoutTraceparentLeavesContextRootable_spec_16_3_326(t *testing.T) {
	withTraceContextPropagator(t)

	var got trace.SpanContext
	h := Wrap(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = trace.SpanContextFromContext(r.Context())
	}), Options{})

	req := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if got.IsValid() {
		t.Errorf("expected no remote span context, got trace id %q", got.TraceID().String())
	}
}
