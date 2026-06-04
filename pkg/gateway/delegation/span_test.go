// SPDX-License-Identifier: MIT

package delegation_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/lennylabs/lenny/pkg/gateway/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// installSpanRecorder swaps the global OTel TracerProvider for an
// SDK-backed recorder so a test can read every span the function under
// test emitted, then restores the prior provider when the test ends.
// spec: §16.3 line 343 (F-16.3.1).
func installSpanRecorder(t *testing.T) (*tracetest.SpanRecorder, func()) {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	return rec, func() { otel.SetTracerProvider(prev) }
}

// findSpan returns the first recorded span with the given name, or nil.
func findSpan(spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	for _, s := range spans {
		if s.Name() == name {
			return s
		}
	}
	return nil
}

// TestDelegateEmitsSpawnChildSpan_spec_16_3 asserts the §16.3 line 343
// `delegation.spawn_child` span is emitted on the gateway spawn-child
// path for a successful delegation. The prior gap left
// SpanDelegationSpawnChild a catalog-only constant with no tracer.Start
// call site (F-16.3.1).
func TestDelegateEmitsSpawnChildSpan_spec_16_3(t *testing.T) {
	rec, restore := installSpanRecorder(t)
	defer restore()

	store := memstore.New()
	seedEnrolledParent(t, store, "exp_1")
	svc := delegation.NewService(store, delegation.Options{
		IDFunc: func() string { return "sess_child" },
	})
	if _, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "gemini",
		PoolRef:          "pool-b",
		IsolationProfile: isolation.ProfileSandboxed,
	}); err != nil {
		t.Fatalf("Delegate: %v", err)
	}

	span := findSpan(rec.Ended(), "delegation.spawn_child")
	if span == nil {
		t.Fatalf("delegation.spawn_child span not recorded; got %d spans", len(rec.Ended()))
	}
	if status := span.Status(); status.Code != codes.Unset {
		t.Errorf("clean-pass span status = %v, want Unset", status.Code)
	}
}

// TestDelegateSpawnChildSpanRecordsError_spec_16_3 asserts the
// `delegation.spawn_child` span records the error on a rejected
// delegation. A missing parent rejects with ErrParentNotFound, which
// returns through the named retErr the deferred RecordError stamps.
func TestDelegateSpawnChildSpanRecordsError_spec_16_3(t *testing.T) {
	rec, restore := installSpanRecorder(t)
	defer restore()

	store := memstore.New()
	svc := delegation.NewService(store, delegation.Options{
		IDFunc: func() string { return "sess_child" },
	})
	if _, err := svc.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID: "sess_missing",
		RuntimeRef:      "gemini",
		PoolRef:         "pool-b",
	}); err == nil {
		t.Fatal("Delegate against a missing parent should fail")
	}

	span := findSpan(rec.Ended(), "delegation.spawn_child")
	if span == nil {
		t.Fatalf("delegation.spawn_child span not recorded for the error path; got %d spans", len(rec.Ended()))
	}
	if span.Status().Code != codes.Error {
		t.Errorf("span status = %v, want codes.Error when the delegation is rejected", span.Status().Code)
	}
}
