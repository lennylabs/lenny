// SPDX-License-Identifier: MIT

package credassign_test

import (
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/lennylabs/lenny/pkg/credential"
)

// installSpanRecorder swaps the global OTel TracerProvider for an
// SDK-backed recorder so a test can read every span the function under
// test emitted. spec: §16.3 line 351.
func installSpanRecorder(t *testing.T) (*tracetest.SpanRecorder, func()) {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	return rec, func() { otel.SetTracerProvider(prev) }
}

func spanByName(rec *tracetest.SpanRecorder, name string) (sdktrace.ReadOnlySpan, bool) {
	for _, s := range rec.Ended() {
		if s.Name() == name {
			return s, true
		}
	}
	return nil, false
}

func stringAttr(s sdktrace.ReadOnlySpan, key string) (string, bool) {
	for _, a := range s.Attributes() {
		if string(a.Key) == key {
			return a.Value.AsString(), true
		}
	}
	return "", false
}

// TestAssignEmitsCredentialAssignSpan_spec_16_3 asserts the §16.3
// credential.assign span is emitted on a successful lease, carrying the
// tenant/session/pool attributes and no credential material.
func TestAssignEmitsCredentialAssignSpan_spec_16_3(t *testing.T) {
	rec, restore := installSpanRecorder(t)
	defer restore()

	svc, _, _ := newService(t)
	svc.RegisterPool(proxyPool("claude-prod", credential.StrategyLeastLoaded,
		healthyCred("key-1", "sk-ant-real")))

	if _, err := svc.Assign("claude-prod", "s_1", "", "tenant-a"); err != nil {
		t.Fatalf("Assign: %v", err)
	}

	span, ok := spanByName(rec, "credential.assign")
	if !ok {
		t.Fatal("no credential.assign span emitted")
	}
	if got, _ := stringAttr(span, "tenant_id"); got != "tenant-a" {
		t.Errorf("tenant_id = %q, want tenant-a", got)
	}
	if got, _ := stringAttr(span, "session_id"); got != "s_1" {
		t.Errorf("session_id = %q, want s_1", got)
	}
	if got, _ := stringAttr(span, "credential.pool"); got != "claude-prod" {
		t.Errorf("credential.pool = %q, want claude-prod", got)
	}
	for _, a := range span.Attributes() {
		if a.Value.Type() == attribute.STRING && a.Value.AsString() == "sk-ant-real" {
			t.Errorf("span attribute %q leaked the upstream credential", a.Key)
		}
	}
	if span.Status().Code == codes.Error {
		t.Error("successful assign recorded an error status")
	}
}

// TestAssignSpanRecordsUnknownPoolError_spec_16_3 asserts the assign span
// records the error status when the pool is unknown.
func TestAssignSpanRecordsUnknownPoolError_spec_16_3(t *testing.T) {
	rec, restore := installSpanRecorder(t)
	defer restore()

	svc, _, _ := newService(t)
	if _, err := svc.Assign("missing", "s_2", "", "tenant-a"); err == nil {
		t.Fatal("Assign against an unknown pool should fail")
	}

	span, ok := spanByName(rec, "credential.assign")
	if !ok {
		t.Fatal("no credential.assign span emitted on the error path")
	}
	if span.Status().Code != codes.Error {
		t.Errorf("span status = %v, want Error", span.Status().Code)
	}
}
