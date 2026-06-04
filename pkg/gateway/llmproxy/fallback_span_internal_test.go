// SPDX-License-Identifier: MIT

package llmproxy

import (
	"net/http/httptest"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credfallback"
)

// recordSpans installs an SDK span recorder over the global provider so
// the test can read the spans driveFallback emitted. spec: §16.3 line 353.
func recordSpans(t *testing.T) (*tracetest.SpanRecorder, func()) {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	return rec, func() { otel.SetTracerProvider(prev) }
}

func fallbackSpan(rec *tracetest.SpanRecorder) (sdktrace.ReadOnlySpan, bool) {
	for _, s := range rec.Ended() {
		if s.Name() == "credential.fallback_chain" {
			return s, true
		}
	}
	return nil, false
}

func outcomeAttr(s sdktrace.ReadOnlySpan) string {
	for _, a := range s.Attributes() {
		if string(a.Key) == "outcome" {
			return a.Value.AsString()
		}
	}
	return ""
}

// TestDriveFallbackEmitsRotatedSpan_spec_16_3 asserts a fault that rotates
// to the next chain pool emits the credential.fallback_chain span with
// outcome=rotated and no error status.
func TestDriveFallbackEmitsRotatedSpan_spec_16_3(t *testing.T) {
	rec, restore := recordSpans(t)
	defer restore()

	ctrl := credfallback.NewController(3, time.Minute)
	ctrl.RegisterChain("s_1", credential.ProviderAnthropicDirect, []string{"pool-a", "pool-b"})
	h := &Handler{Fallback: ctrl}

	lease := credential.Lease{
		SessionID: "s_1", TenantID: "tenant-a",
		Provider: credential.ProviderAnthropicDirect, PoolID: "pool-a",
	}
	if exhausted := h.driveFallback(httptest.NewRecorder(), lease,
		credential.TriggerFaultAuthExpired, "AUTH_FAILED"); exhausted {
		t.Fatal("a chain with a free next pool must not report exhausted")
	}

	span, ok := fallbackSpan(rec)
	if !ok {
		t.Fatal("no credential.fallback_chain span emitted")
	}
	if got := outcomeAttr(span); got != "rotated" {
		t.Errorf("outcome = %q, want rotated", got)
	}
	if span.Status().Code == codes.Error {
		t.Error("a successful rotation recorded an error status")
	}
}

// TestDriveFallbackEmitsExhaustedSpan_spec_16_3 asserts an exhausted chain
// emits the span with outcome=exhausted and a POLICY-category error.
func TestDriveFallbackEmitsExhaustedSpan_spec_16_3(t *testing.T) {
	rec, restore := recordSpans(t)
	defer restore()

	ctrl := credfallback.NewController(3, time.Minute)
	// A single-pool chain: faulting its only pool leaves nothing to
	// select, so the chain exhausts.
	ctrl.RegisterChain("s_2", credential.ProviderAnthropicDirect, []string{"pool-a"})
	h := &Handler{Fallback: ctrl}

	lease := credential.Lease{
		SessionID: "s_2", TenantID: "tenant-a",
		Provider: credential.ProviderAnthropicDirect, PoolID: "pool-a",
	}
	if exhausted := h.driveFallback(httptest.NewRecorder(), lease,
		credential.TriggerFaultAuthExpired, "AUTH_FAILED"); !exhausted {
		t.Fatal("a single-pool chain must report exhausted after its pool faults")
	}

	span, ok := fallbackSpan(rec)
	if !ok {
		t.Fatal("no credential.fallback_chain span emitted")
	}
	if got := outcomeAttr(span); got != "exhausted" {
		t.Errorf("outcome = %q, want exhausted", got)
	}
	if span.Status().Code != codes.Error {
		t.Errorf("span status = %v, want Error", span.Status().Code)
	}
	if cat := errorCategoryAttr(span); cat != "POLICY" {
		t.Errorf("error.category = %q, want POLICY", cat)
	}
}

func errorCategoryAttr(s sdktrace.ReadOnlySpan) string {
	for _, a := range s.Attributes() {
		if string(a.Key) == "error.category" {
			return a.Value.AsString()
		}
	}
	return ""
}
