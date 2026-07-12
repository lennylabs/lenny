// SPDX-License-Identifier: MIT

// Package interceptor is a §4.8 external RequestInterceptor gRPC stub
// that listens on a real loopback TCP port. The interceptor package's
// own unit tests dial an in-process bufconn fake; this stub instead
// stands in as a deployer-supplied interceptor service that the
// lenny-gateway binary reaches over a real network socket, so a tier-4
// test exercises the gateway's real dial, --external-interceptor spec
// parsing, and gRPC round-trip end to end.
//
// The stub records every InterceptRequest it receives so a test can
// assert the gateway forwarded the right phase, identity, and payload,
// and delegates the ALLOW / MODIFY / REJECT decision (or a deliberate
// hang) to a caller-supplied Handler.
//
//	stub := interceptor.Start(t, interceptor.Reject("blocked"))
//	gw := gateway.StartWith(t, "--dev-mode",
//	    "--external-interceptor=name=guard,endpoint="+stub.Addr()+",phase=PostAgentOutput")
//
// spec: §4.8 — "External interceptors are invoked via gRPC (like
// Kubernetes admission webhooks)."
package interceptor

import (
	"context"
	"net"
	"sync"
	"testing"

	"google.golang.org/grpc"

	interceptorv1 "github.com/lennylabs/lenny/pkg/proto/interceptor/v1"
)

// Handler decides the response for one Intercept call. It receives the
// request context (so a handler can block on ctx.Done to simulate a
// hang against the gateway's per-interceptor timeout) and the forwarded
// request. Returning a non-nil error surfaces to the gateway as a gRPC
// failure, which the gateway treats as an interceptor error subject to
// the registration's failPolicy.
type Handler func(context.Context, *interceptorv1.InterceptRequest) (*interceptorv1.InterceptResponse, error)

// Stub is a RequestInterceptor gRPC service on a real TCP port.
type Stub struct {
	addr string
	srv  *grpc.Server

	mu       sync.Mutex
	requests []*interceptorv1.InterceptRequest
	handler  Handler

	interceptorv1.UnimplementedRequestInterceptorServer
}

// Start serves the stub on a random loopback TCP port and registers a
// t.Cleanup that stops it. handler decides each response; a nil handler
// allows every request.
func Start(t testing.TB, handler Handler) *Stub {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("interceptor stub: listen: %v", err)
	}
	s := &Stub{addr: lis.Addr().String(), srv: grpc.NewServer(), handler: handler}
	interceptorv1.RegisterRequestInterceptorServer(s.srv, s)
	go func() { _ = s.srv.Serve(lis) }()
	t.Cleanup(s.srv.Stop)
	return s
}

// Intercept implements interceptorv1.RequestInterceptorServer. It records
// the request and delegates to the configured handler.
func (s *Stub) Intercept(ctx context.Context, req *interceptorv1.InterceptRequest) (*interceptorv1.InterceptResponse, error) {
	s.mu.Lock()
	s.requests = append(s.requests, req)
	h := s.handler
	s.mu.Unlock()
	if h == nil {
		return &interceptorv1.InterceptResponse{Action: interceptorv1.InterceptResponse_ALLOW}, nil
	}
	return h(ctx, req)
}

// Addr returns the host:port the stub is listening on, suitable for the
// gateway's --external-interceptor endpoint= field.
func (s *Stub) Addr() string { return s.addr }

// Requests returns a snapshot of every InterceptRequest the stub has
// received, in arrival order.
func (s *Stub) Requests() []*interceptorv1.InterceptRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*interceptorv1.InterceptRequest, len(s.requests))
	copy(out, s.requests)
	return out
}

// Allow returns a Handler that returns ALLOW for every request.
func Allow() Handler {
	return func(context.Context, *interceptorv1.InterceptRequest) (*interceptorv1.InterceptResponse, error) {
		return &interceptorv1.InterceptResponse{Action: interceptorv1.InterceptResponse_ALLOW}, nil
	}
}

// Reject returns a Handler that returns REJECT with reason for every
// request, short-circuiting the gateway's interceptor chain.
func Reject(reason string) Handler {
	return func(context.Context, *interceptorv1.InterceptRequest) (*interceptorv1.InterceptResponse, error) {
		return &interceptorv1.InterceptResponse{
			Action: interceptorv1.InterceptResponse_REJECT,
			Reason: reason,
		}, nil
	}
}

// Modify returns a Handler that returns MODIFY with modified as the
// replacement payload for every request. The gateway applies the phase's
// immutable-field enforcement before accepting the modified content, so
// modified must be a valid payload for the target phase.
func Modify(modified []byte) Handler {
	return func(context.Context, *interceptorv1.InterceptRequest) (*interceptorv1.InterceptResponse, error) {
		return &interceptorv1.InterceptResponse{
			Action:          interceptorv1.InterceptResponse_MODIFY,
			ModifiedContent: modified,
		}, nil
	}
}

// Hang returns a Handler that blocks until the request context is
// cancelled, so the gateway's per-interceptor timeout fires and the
// registration's failPolicy governs the outcome. It backs the
// fail-open/fail-closed timeout tests.
func Hang() Handler {
	return func(ctx context.Context, _ *interceptorv1.InterceptRequest) (*interceptorv1.InterceptResponse, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
}
