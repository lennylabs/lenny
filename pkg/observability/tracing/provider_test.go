// SPDX-License-Identifier: MIT

package tracing

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// spec: §16.3 line 359 — with no OTLP endpoint, InitProvider installs the
// stdout exporter (the `make run` / dev posture). F-16.3.8.
func TestNewExporterStdoutWhenNoEndpoint_spec_16_3(t *testing.T) {
	exp, err := newExporter(context.Background(), ProviderConfig{ServiceName: "t"})
	if err != nil {
		t.Fatalf("newExporter: %v", err)
	}
	if got := fmt.Sprintf("%T", exp); !strings.Contains(got, "stdouttrace") {
		t.Fatalf("empty endpoint: want stdouttrace exporter, got %s", got)
	}
}

// spec: §16.3 line 359 — dev mode forces the stdout exporter even when an
// OTLP endpoint is configured, so a local developer sees spans on stdout
// instead of failing to reach a Collector. F-16.3.8.
func TestNewExporterDevModeForcesStdout_spec_16_3(t *testing.T) {
	exp, err := newExporter(context.Background(), ProviderConfig{
		ServiceName:  "t",
		OTLPEndpoint: "http://collector.example:4318",
		DevMode:      true,
	})
	if err != nil {
		t.Fatalf("newExporter: %v", err)
	}
	if got := fmt.Sprintf("%T", exp); !strings.Contains(got, "stdouttrace") {
		t.Fatalf("dev mode: want stdouttrace exporter, got %s", got)
	}
}

// spec: §16.3 line 359 — a configured OTLP endpoint selects the OTLP/HTTP
// exporter. F-16.3.2. Construction does not dial, so no Collector is needed.
func TestNewExporterOTLPWhenEndpointSet_spec_16_3(t *testing.T) {
	exp, err := newExporter(context.Background(), ProviderConfig{
		ServiceName:  "t",
		OTLPEndpoint: "http://collector.example:4318",
	})
	if err != nil {
		t.Fatalf("newExporter: %v", err)
	}
	if got := fmt.Sprintf("%T", exp); !strings.Contains(got, "otlptrace") {
		t.Fatalf("endpoint set: want otlptrace exporter, got %s", got)
	}
}

// spec: §16.3 — InitProvider installs a real SDK TracerProvider (not the
// global no-op) plus the W3C trace-context propagator, so spans started
// through Tracer.Start are actually exported and traceparent crosses
// process boundaries. F-16.3.2.
func TestInitProviderInstallsSDKProviderAndPropagator_spec_16_3(t *testing.T) {
	prevTP := otel.GetTracerProvider()
	prevProp := otel.GetTextMapPropagator()
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})

	shutdown, err := InitProvider(context.Background(), ProviderConfig{ServiceName: "lenny-test"})
	if err != nil {
		t.Fatalf("InitProvider: %v", err)
	}
	if shutdown == nil {
		t.Fatal("InitProvider returned a nil shutdown func")
	}

	if _, ok := otel.GetTracerProvider().(*sdktrace.TracerProvider); !ok {
		t.Fatalf("global provider is %T, want *sdktrace.TracerProvider (no-op not replaced)",
			otel.GetTracerProvider())
	}

	var sawTraceparent bool
	for _, f := range otel.GetTextMapPropagator().Fields() {
		if f == "traceparent" {
			sawTraceparent = true
		}
	}
	if !sawTraceparent {
		t.Fatalf("propagator fields %v lack traceparent (W3C propagation not installed)",
			otel.GetTextMapPropagator().Fields())
	}

	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

// spec: §16.3 lines 347-348, 357 — the budget and coordinator-handoff span
// attribute keys are declared as constants so call sites cannot drift from
// the spec's attribute names. F-16.3.5.
func TestBudgetAndHandoffAttributeKeys_spec_16_3(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{AttrOutcome, "outcome"},
		{AttrRootSessionID, "root_session_id"},
		{AttrLuaQueueWaitMs, "lua_queue_wait_ms"},
		{AttrGeneration, "generation"},
	} {
		if c.got != c.want {
			t.Errorf("attribute key = %q, want %q", c.got, c.want)
		}
	}
}
