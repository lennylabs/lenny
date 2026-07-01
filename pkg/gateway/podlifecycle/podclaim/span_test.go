// SPDX-License-Identifier: MIT

package podclaim_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
)

// installSpanRecorder swaps the global OTel TracerProvider for an
// SDK-backed recorder so a test can read the spans Claim emitted.
// spec: §16.3 line 337.
func installSpanRecorder(t *testing.T) (*tracetest.SpanRecorder, func()) {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	return rec, func() { otel.SetTracerProvider(prev) }
}

func claimSpan(rec *tracetest.SpanRecorder) (sdktrace.ReadOnlySpan, bool) {
	for _, s := range rec.Ended() {
		if s.Name() == "session.claim_pod" {
			return s, true
		}
	}
	return nil, false
}

// TestClaimEmitsClaimPodSpan_spec_16_3 asserts a successful claim emits
// the §16.3 session.claim_pod span tagged with the pool.
func TestClaimEmitsClaimPodSpan_spec_16_3(t *testing.T) {
	rec, restore := installSpanRecorder(t)
	defer restore()

	claimer, _ := claimerFor(t, sandboxIn(testPool, "sbx-1", "idle"))
	if _, err := claimer.Claim(context.Background(), podclaim.ClaimRequest{
		Pool: testPool, SessionID: "sess-1", TenantID: "acme",
	}); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	span, ok := claimSpan(rec)
	if !ok {
		t.Fatal("no session.claim_pod span emitted")
	}
	var pool string
	for _, a := range span.Attributes() {
		if string(a.Key) == "pool" {
			pool = a.Value.AsString()
		}
	}
	if pool != testPool {
		t.Errorf("span pool attr = %q, want %q", pool, testPool)
	}
	if span.Status().Code == codes.Error {
		t.Error("successful claim recorded an error status")
	}
}

// TestClaimSpanRecordsNoIdlePodError_spec_16_3 asserts the span records
// the error status when the pool has no claimable idle Sandbox.
func TestClaimSpanRecordsNoIdlePodError_spec_16_3(t *testing.T) {
	rec, restore := installSpanRecorder(t)
	defer restore()

	claimer, _ := claimerFor(t)
	if _, err := claimer.Claim(context.Background(), podclaim.ClaimRequest{
		Pool: testPool, SessionID: "sess-2", TenantID: "acme",
	}); err == nil {
		t.Fatal("Claim against an empty pool should fail")
	}

	span, ok := claimSpan(rec)
	if !ok {
		t.Fatal("no session.claim_pod span emitted on the error path")
	}
	if span.Status().Code != codes.Error {
		t.Errorf("span status = %v, want Error", span.Status().Code)
	}
}
