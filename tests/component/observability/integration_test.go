// SPDX-License-Identifier: MIT

//go:build component

// Tier-2 component test that wires the observability primitives together
// against the in-process OTLP collector helper. The test acts as the
// "first emitter" smoke for the §16.3/§16.4 correlation flow: a fake
// gateway handler creates a session and the test verifies that the span
// carries the correlation attributes and the log line carries the same
// fields.
//
// Component-level tests for real binaries land with those binaries.

package observability_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/lennylabs/lenny/pkg/observability/correlation"
	"github.com/lennylabs/lenny/pkg/observability/logging"
	"github.com/lennylabs/lenny/pkg/observability/tracing"
	"github.com/lennylabs/lenny/tests/testinfra/mocks/otelcollector"
)

// spec: 13.4 (observability primitives compose: correlation + logging + tracing)
// diagnosis: A correlated log/span did not carry the tenant id or
//
//	component label end-to-end. Inspect correlation.With,
//	the JSONHandler attribute pipeline, and the otelcollector
//	span recorder.
func TestObservabilityPrimitivesWireTogether(t *testing.T) {
	collector := otelcollector.New(t)
	tr := tracing.NewTracer(collector.Tracer("gateway"))

	var logBuf bytes.Buffer
	logger := slog.New(logging.NewJSONHandler(&logBuf, logging.Options{DefaultComponent: "gateway"}))

	ctx := correlation.With(context.Background(), correlation.Fields{
		TenantID:     "acme",
		SessionID:    "sess_42",
		Component:    "gateway",
		RuntimeClass: "claude-code",
		Pool:         "default",
	})

	ctx, span := tr.Start(ctx, tracing.SpanSessionCreate)
	logger.InfoContext(ctx, "session created")
	span.End()

	spans := collector.Spans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	want := map[string]string{
		"tenant_id":     "acme",
		"session_id":    "sess_42",
		"component":     "gateway",
		"runtime_class": "claude-code",
		"pool":          "default",
	}
	got := map[string]string{}
	for _, kv := range spans[0].Attributes() {
		got[string(kv.Key)] = kv.Value.AsString()
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("span attribute %q: want %q, got %q", k, v, got[k])
		}
	}

	var rec map[string]any
	if err := json.Unmarshal(logBuf.Bytes(), &rec); err != nil {
		t.Fatalf("log line is not valid JSON: %v\nbuf: %s", err, logBuf.String())
	}
	for k, v := range want {
		if rec[k] != v {
			t.Errorf("log attribute %q: want %q, got %v", k, v, rec[k])
		}
	}
}
