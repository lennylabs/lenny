// SPDX-License-Identifier: MIT

package connectorinvoke

import (
	"context"
	"net/http"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// spec: §16.3 line 330 ("Gateway → External MCP tools (HTTP headers)") — the
// outbound connector request carries a W3C traceparent injected from the
// gateway's current trace context, so the mcp.external_tool_call span and any
// tracing the external connector performs share one trace. F-16.3.3.
func TestOutboundRequestInjectsTraceparent_spec_16_3_330(t *testing.T) {
	prevTP := otel.GetTracerProvider()
	prevProp := otel.GetTextMapPropagator()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample())))
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})

	doer := &fakeDoer{
		responses: []*http.Response{
			jsonResp(200, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18"}}`, nil),
			jsonResp(202, ``, nil),
		},
	}
	c := New(doer)

	ctx, span := otel.Tracer("test").Start(context.Background(), "mcp.external_tool_call")
	if _, _, err := c.Initialize(ctx, "https://mcp.example.com", ""); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	span.End()

	if len(doer.reqs) == 0 {
		t.Fatal("no outbound request was issued")
	}
	tp := doer.reqs[0].Header.Get("traceparent")
	if tp == "" {
		t.Fatal("outbound request carried no traceparent header")
	}
	carrier := propagation.HeaderCarrier(doer.reqs[0].Header)
	sc := trace.SpanContextFromContext(propagation.TraceContext{}.Extract(context.Background(), carrier))
	if sc.TraceID() != span.SpanContext().TraceID() {
		t.Errorf("traceparent trace id = %q, want %q", sc.TraceID(), span.SpanContext().TraceID())
	}
}
