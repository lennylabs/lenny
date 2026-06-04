// SPDX-License-Identifier: MIT

package treebudget_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/lennylabs/lenny/pkg/gateway/treebudget"
	"github.com/lennylabs/lenny/pkg/observability/correlation"
)

// installSpanRecorder swaps the global OTel TracerProvider for an
// SDK-backed recorder so a test can read every span the function under
// test emitted, then restores the prior provider when the test ends.
// spec: §16.3 lines 347-348 (F-16.3.1).
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

// stringAttr returns the string value of key on attrs, or ("", false).
func stringAttr(attrs []attribute.KeyValue, key string) (string, bool) {
	for _, a := range attrs {
		if string(a.Key) == key && a.Value.Type() == attribute.STRING {
			return a.Value.AsString(), true
		}
	}
	return "", false
}

// hasIntAttr reports whether attrs carries an int64 value at key.
func hasIntAttr(attrs []attribute.KeyValue, key string) bool {
	for _, a := range attrs {
		if string(a.Key) == key && a.Value.Type() == attribute.INT64 {
			return true
		}
	}
	return false
}

// TestReserveEmitsBudgetReserveSpan_spec_16_3 asserts the §16.3 line 347
// `delegation.budget_reserve` span is emitted on a successful reserve
// with the four mandated attributes (outcome, tenant_id, root_session_id,
// lua_queue_wait_ms). tenant_id is projected from the correlation context
// by Start; the other three are set explicitly at the call site. The
// prior gap left SpanDelegationBudgetReserve a catalog-only constant with
// no tracer.Start call site (F-16.3.1).
func TestReserveEmitsBudgetReserveSpan_spec_16_3(t *testing.T) {
	rec, restore := installSpanRecorder(t)
	defer restore()

	r, _ := newReserver(t)
	ctx := correlation.With(context.Background(), correlation.Fields{TenantID: "acme"})
	if _, err := r.Reserve(ctx, treebudget.Reservation{
		RootSessionID:   "root_span_ok",
		ParentSessionID: "root_span_ok",
		TreeSizeCap:     10,
		TreeSizeDelta:   1,
	}); err != nil {
		t.Fatalf("Reserve: %v", err)
	}

	span := findSpan(rec.Ended(), "delegation.budget_reserve")
	if span == nil {
		t.Fatalf("delegation.budget_reserve span not recorded; got %d spans", len(rec.Ended()))
	}
	attrs := span.Attributes()
	if got, _ := stringAttr(attrs, "outcome"); got != "reserved" {
		t.Errorf("outcome = %q, want reserved", got)
	}
	if got, _ := stringAttr(attrs, "tenant_id"); got != "acme" {
		t.Errorf("tenant_id = %q, want acme (projected from correlation context)", got)
	}
	if got, _ := stringAttr(attrs, "root_session_id"); got != "root_span_ok" {
		t.Errorf("root_session_id = %q, want root_span_ok", got)
	}
	if !hasIntAttr(attrs, "lua_queue_wait_ms") {
		t.Error("lua_queue_wait_ms attribute missing; the Lua-call duration must be recorded")
	}
	if status := span.Status(); status.Code != codes.Unset {
		t.Errorf("clean-pass span status = %v, want Unset", status.Code)
	}
}

// TestReserveRejectedSetsOutcomeAndRecordsError_spec_16_3 asserts a
// budget-exceeded reserve sets outcome=rejected and records the error on
// the `delegation.budget_reserve` span. §8.2 line 127.
func TestReserveRejectedSetsOutcomeAndRecordsError_spec_16_3(t *testing.T) {
	rec, restore := installSpanRecorder(t)
	defer restore()

	r, _ := newReserver(t)
	ctx := context.Background()
	res := treebudget.Reservation{
		RootSessionID:   "root_span_reject",
		ParentSessionID: "root_span_reject",
		TreeSizeCap:     1,
		TreeSizeDelta:   1,
	}
	// First reserve fills the cap; the second breaches it and is rejected.
	if _, err := r.Reserve(ctx, res); err != nil {
		t.Fatalf("first Reserve: %v", err)
	}
	if _, err := r.Reserve(ctx, res); err == nil {
		t.Fatal("second Reserve should breach maxTreeSize")
	}

	var rejected sdktrace.ReadOnlySpan
	for _, s := range rec.Ended() {
		if s.Name() != "delegation.budget_reserve" {
			continue
		}
		if got, _ := stringAttr(s.Attributes(), "outcome"); got == "rejected" {
			rejected = s
		}
	}
	if rejected == nil {
		t.Fatal("no delegation.budget_reserve span with outcome=rejected recorded")
	}
	if rejected.Status().Code != codes.Error {
		t.Errorf("rejected span status = %v, want codes.Error", rejected.Status().Code)
	}
	if got, _ := stringAttr(rejected.Attributes(), "root_session_id"); got != "root_span_reject" {
		t.Errorf("root_session_id = %q, want root_span_reject", got)
	}
	if !hasIntAttr(rejected.Attributes(), "lua_queue_wait_ms") {
		t.Error("lua_queue_wait_ms attribute missing on the rejected span")
	}
}

// TestReturnEmitsBudgetReturnSpan_spec_16_3 asserts the §16.3 line 348
// `delegation.budget_return` span is emitted on a successful return with
// outcome=returned and the mandated attributes.
func TestReturnEmitsBudgetReturnSpan_spec_16_3(t *testing.T) {
	rec, restore := installSpanRecorder(t)
	defer restore()

	r, _ := newReserver(t)
	ctx := correlation.With(context.Background(), correlation.Fields{TenantID: "acme"})
	res := treebudget.Reservation{
		RootSessionID:         "root_span_ret",
		ParentSessionID:       "root_span_ret",
		ParallelChildrenDelta: 1,
	}
	if _, err := r.Reserve(ctx, treebudget.Reservation{
		RootSessionID:         "root_span_ret",
		ParentSessionID:       "root_span_ret",
		ParallelChildrenCap:   5,
		ParallelChildrenDelta: 1,
	}); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := r.Return(ctx, res); err != nil {
		t.Fatalf("Return: %v", err)
	}

	span := findSpan(rec.Ended(), "delegation.budget_return")
	if span == nil {
		t.Fatalf("delegation.budget_return span not recorded; got %d spans", len(rec.Ended()))
	}
	attrs := span.Attributes()
	if got, _ := stringAttr(attrs, "outcome"); got != "returned" {
		t.Errorf("outcome = %q, want returned", got)
	}
	if got, _ := stringAttr(attrs, "tenant_id"); got != "acme" {
		t.Errorf("tenant_id = %q, want acme (projected from correlation context)", got)
	}
	if got, _ := stringAttr(attrs, "root_session_id"); got != "root_span_ret" {
		t.Errorf("root_session_id = %q, want root_span_ret", got)
	}
	if !hasIntAttr(attrs, "lua_queue_wait_ms") {
		t.Error("lua_queue_wait_ms attribute missing on the return span")
	}
	if status := span.Status(); status.Code != codes.Unset {
		t.Errorf("clean-return span status = %v, want Unset", status.Code)
	}
}
