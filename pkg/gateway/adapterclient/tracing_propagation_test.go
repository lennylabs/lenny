// SPDX-License-Identifier: MIT

package adapterclient_test

import (
	"context"
	"net"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/gateway/adapterclient"
)

// TestGRPCTraceContextPropagatesGatewayToPod exercises the full §16.3 line 327
// hop ("Gateway → Pod (gRPC metadata)"): a client span opened on the gateway
// side must reach the pod adapter as the parent of the server-side RPC span.
// Both the adapterclient.Dial client stats handler and the
// adapter.NewGRPCServer server stats handler must be wired for the server span
// to inherit the client's trace id. F-16.3.3.
func TestGRPCTraceContextPropagatesGatewayToPod_spec_16_3_327(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithSpanProcessor(recorder),
	)
	prevTP := otel.GetTracerProvider()
	prevProp := otel.GetTextMapPropagator()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})

	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(adapter.New("adapter-trace-test"))
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	cl, err := adapterclient.Dial("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial adapter: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close() })

	ctx, clientSpan := otel.Tracer("test").Start(context.Background(), "gateway.call")
	if _, err := cl.NegotiateVersion(ctx, []string{adapter.ProtocolVersionV1}); err != nil {
		t.Fatalf("NegotiateVersion: %v", err)
	}
	clientSpan.End()

	wantTrace := clientSpan.SpanContext().TraceID()
	var serverSpan sdktrace.ReadOnlySpan
	for _, s := range recorder.Ended() {
		if s.SpanKind() == trace.SpanKindServer {
			serverSpan = s
			break
		}
	}
	if serverSpan == nil {
		t.Fatal("no server-side RPC span was recorded; the server stats handler is not wired")
	}
	if serverSpan.SpanContext().TraceID() != wantTrace {
		t.Errorf("server span trace id = %q, want %q (client trace not propagated through gRPC metadata)",
			serverSpan.SpanContext().TraceID(), wantTrace)
	}
	if !serverSpan.Parent().IsValid() {
		t.Error("server span has no parent; the inbound traceparent was not extracted")
	}
}
