// SPDX-License-Identifier: MIT

package tracing

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/observability/correlation"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func newTestTracer(t *testing.T) (*Tracer, *tracetest.SpanRecorder) {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	return NewTracer(tp.Tracer("test")), recorder
}

func findAttr(attrs []attribute.KeyValue, key string) (attribute.KeyValue, bool) {
	for _, a := range attrs {
		if string(a.Key) == key {
			return a, true
		}
	}
	return attribute.KeyValue{}, false
}

func TestStartProjectsCorrelationFieldsAsAttributes(t *testing.T) {
	tracer, rec := newTestTracer(t)
	ctx := correlation.With(context.Background(), correlation.Fields{
		TenantID:     "acme",
		SessionID:    "sess_42",
		Component:    "gateway",
		RuntimeClass: "claude-code",
		Pool:         "default",
	})
	_, span := tracer.Start(ctx, SpanSessionCreate)
	span.End()

	spans := rec.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	got := spans[0]
	if got.Name() != string(SpanSessionCreate) {
		t.Errorf("span name: want %q, got %q", SpanSessionCreate, got.Name())
	}
	cases := map[string]string{
		"tenant_id":     "acme",
		"session_id":    "sess_42",
		"component":     "gateway",
		"runtime_class": "claude-code",
		"pool":          "default",
	}
	for k, want := range cases {
		a, ok := findAttr(got.Attributes(), k)
		if !ok {
			t.Errorf("attribute %q missing from span attributes %v", k, got.Attributes())
			continue
		}
		if a.Value.AsString() != want {
			t.Errorf("attribute %q: want %q, got %q", k, want, a.Value.AsString())
		}
	}
}

func TestStartOmitsEmptyCorrelationFields(t *testing.T) {
	tracer, rec := newTestTracer(t)
	ctx := correlation.With(context.Background(), correlation.Fields{TenantID: "acme"})
	_, span := tracer.Start(ctx, SpanSessionCreate)
	span.End()

	got := rec.Ended()[0]
	for _, missing := range []string{"session_id", "task_id", "operation_id", "agent_name", "runtime_class", "pool"} {
		if _, ok := findAttr(got.Attributes(), missing); ok {
			t.Errorf("attribute %q should be absent for empty Fields, attributes %v", missing, got.Attributes())
		}
	}
}

func TestRecordErrorAttachesCategoryAttribute(t *testing.T) {
	tracer, rec := newTestTracer(t)
	_, span := tracer.Start(context.Background(), SpanSessionCreate)
	RecordError(span, CategorizeError(errors.New("rate limited"), CategoryTransient))
	span.End()

	got := rec.Ended()[0]
	a, ok := findAttr(got.Attributes(), AttrErrorCategory)
	if !ok {
		t.Fatalf("error.category attribute missing, attributes %v", got.Attributes())
	}
	if a.Value.AsString() != string(CategoryTransient) {
		t.Errorf("error.category: want %q, got %q", CategoryTransient, a.Value.AsString())
	}
	if got.Status().Code != codes.Error {
		t.Errorf("status: want Error, got %v", got.Status().Code)
	}
}

func TestRecordErrorNilIsNoOp(t *testing.T) {
	tracer, rec := newTestTracer(t)
	_, span := tracer.Start(context.Background(), SpanSessionCreate)
	RecordError(span, nil)
	span.End()

	got := rec.Ended()[0]
	if _, ok := findAttr(got.Attributes(), AttrErrorCategory); ok {
		t.Errorf("nil error should not attach category attribute")
	}
	if got.Status().Code == codes.Error {
		t.Errorf("nil error should not change span status")
	}
}

func TestCategorizeErrorPreservesUnderlying(t *testing.T) {
	base := errors.New("boom")
	wrapped := CategorizeError(base, CategoryPolicy)
	if !errors.Is(wrapped, base) {
		t.Fatalf("CategorizeError should preserve errors.Is identity")
	}
	var cat *CategorizedError
	if !errors.As(wrapped, &cat) {
		t.Fatalf("CategorizeError should be retrievable via errors.As")
	}
	if cat.Category != CategoryPolicy {
		t.Errorf("category: want POLICY, got %q", cat.Category)
	}
}

func TestCategorizeErrorNilReturnsNil(t *testing.T) {
	if got := CategorizeError(nil, CategoryPolicy); got != nil {
		t.Errorf("CategorizeError(nil, ...) should return nil, got %v", got)
	}
}

// Catalog hygiene: SpanNames returns every constant.
func TestSpanNamesCatalogIsExhaustive(t *testing.T) {
	got := SpanNames()
	if len(got) < 20 {
		t.Errorf("SpanNames catalog suspiciously short (%d entries)", len(got))
	}
	seen := map[SpanName]bool{}
	for _, n := range got {
		if seen[n] {
			t.Errorf("duplicate span name %q in catalog", n)
		}
		seen[n] = true
		if n == "" {
			t.Errorf("empty span name in catalog")
		}
	}
}
