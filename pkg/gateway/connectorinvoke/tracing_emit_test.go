// SPDX-License-Identifier: MIT

package connectorinvoke

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/lennylabs/lenny/pkg/gateway/connectorcredstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
)

// installSpanRecorder swaps the global OTel TracerProvider for an
// SDK-backed recorder so a test can read every span the function under
// test emitted, then restores the prior provider when the test ends.
// spec: §16.3 line 344.
func installSpanRecorder(t *testing.T) (*tracetest.SpanRecorder, func()) {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	return rec, func() { otel.SetTracerProvider(prev) }
}

func spanByName(spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	for _, s := range spans {
		if s.Name() == name {
			return s
		}
	}
	return nil
}

// TestInvokerNilTracerStillEmitsExternalToolCallSpan_spec_16_3 proves the
// production gap is closed: cmd/lenny-gateway constructs the invoker with a
// nil tracer, yet a tools/call must still open the mcp.external_tool_call
// span. NewInvoker defaults a nil tracer to the process-global tracer, so
// the span resolves against whatever provider InitProvider installed (here,
// the test recorder). The previous code left iv.tracer nil and skipped the
// span entirely in production.
func TestInvokerNilTracerStillEmitsExternalToolCallSpan_spec_16_3(t *testing.T) {
	rec, restore := installSpanRecorder(t)
	defer restore()

	connectors := connectorstore.NewMemory()
	seedConnector(t, connectors, connectorstore.Connector{
		TenantID: "acme", ID: "github", MCPServerURL: "https://mcp.github.example",
	})
	doer := &fakeDoer{
		responses: []*http.Response{
			jsonResp(200, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18"}}`, nil),
			jsonResp(202, ``, nil),
			jsonResp(200, `{"jsonrpc":"2.0","id":2,"result":{}}`, nil),
		},
	}
	// nil tracer — exactly how cmd/lenny-gateway wires the invoker.
	iv := NewInvoker(connectors, connectorcredstore.NewMemory(clock), New(doer), nil, nil)
	if _, err := iv.CallTool(context.Background(), "acme", "sess-1", "github", "alice", "", "ping", nil); err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	span := spanByName(rec.Ended(), "mcp.external_tool_call")
	if span == nil {
		t.Fatalf("mcp.external_tool_call span not emitted with a nil tracer; got %d spans", len(rec.Ended()))
	}
	var sawTool bool
	for _, a := range span.Attributes() {
		if string(a.Key) == "mcp.tool" && a.Value.AsString() == "ping" {
			sawTool = true
		}
	}
	if !sawTool {
		t.Errorf("span missing mcp.tool=ping attribute; attrs=%v", span.Attributes())
	}
}

// TestInvokerNilTracerListToolsEmitsSpan_spec_16_3 verifies the second
// existing span site (ListTools) also emits under the nil-tracer default.
func TestInvokerNilTracerListToolsEmitsSpan_spec_16_3(t *testing.T) {
	rec, restore := installSpanRecorder(t)
	defer restore()

	connectors := connectorstore.NewMemory()
	seedConnector(t, connectors, connectorstore.Connector{
		TenantID: "acme", ID: "github", MCPServerURL: "https://mcp.github.example",
	})
	doer := &fakeDoer{
		responses: []*http.Response{
			jsonResp(200, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18"}}`, nil),
			jsonResp(202, ``, nil),
			jsonResp(200, `{"jsonrpc":"2.0","id":2,"result":{"tools":[]}}`, nil),
		},
	}
	iv := NewInvoker(connectors, connectorcredstore.NewMemory(clock), New(doer), nil, nil)
	if _, err := iv.ListTools(context.Background(), "acme", "sess-1", "github", "alice", ""); err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	span := spanByName(rec.Ended(), "mcp.external_tool_call")
	if span == nil {
		t.Fatalf("ListTools did not emit mcp.external_tool_call span; got %d spans", len(rec.Ended()))
	}
	var sawMethod bool
	for _, a := range span.Attributes() {
		if string(a.Key) == "mcp.method" && a.Value.AsString() == "tools/list" {
			sawMethod = true
		}
	}
	if !sawMethod {
		t.Errorf("span missing mcp.method=tools/list attribute; attrs=%v", span.Attributes())
	}
}

// TestInvokerExternalToolCallSpanRecordsError_spec_16_3 covers the error
// path: when the outbound dial fails the span records the error status so a
// failed external tool call is visible in the trace.
func TestInvokerExternalToolCallSpanRecordsError_spec_16_3(t *testing.T) {
	rec, restore := installSpanRecorder(t)
	defer restore()

	connectors := connectorstore.NewMemory()
	seedConnector(t, connectors, connectorstore.Connector{
		TenantID: "acme", ID: "github", MCPServerURL: "https://mcp.github.example",
	})
	// The first outbound call (initialize) errors, so CallTool fails after
	// the span is opened.
	doer := &fakeDoer{errs: []error{errors.New("dial refused")}}
	iv := NewInvoker(connectors, connectorcredstore.NewMemory(clock), New(doer), nil, nil)
	if _, err := iv.CallTool(context.Background(), "acme", "sess-1", "github", "alice", "", "ping", nil); err == nil {
		t.Fatal("expected CallTool to fail when the outbound dial errors")
	}

	span := spanByName(rec.Ended(), "mcp.external_tool_call")
	if span == nil {
		t.Fatalf("mcp.external_tool_call span not emitted on the error path; got %d spans", len(rec.Ended()))
	}
	if got := span.Status().Code.String(); got != "Error" {
		t.Errorf("span status = %q, want Error on an outbound dial failure", got)
	}
}
