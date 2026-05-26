// SPDX-License-Identifier: MIT

package credassign_test

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/subsystem"
	tokensv1 "github.com/lennylabs/lenny/pkg/proto/tokenservice/v1"
)

// stubTokenServiceServer is a controllable TokenService implementation
// the breaker tests use to drive the Client into Unavailable / NotFound
// outcomes deterministically. unavailableCount tracks how many times
// AssignCredentials was invoked.
type stubTokenServiceServer struct {
	tokensv1.UnimplementedTokenServiceServer
	respond          func(req *tokensv1.AssignCredentialsRequest) (*tokensv1.AssignCredentialsResponse, error)
	unavailableCount atomic.Int32
}

func (s *stubTokenServiceServer) AssignCredentials(ctx context.Context, req *tokensv1.AssignCredentialsRequest) (*tokensv1.AssignCredentialsResponse, error) {
	if s.respond != nil {
		return s.respond(req)
	}
	return nil, status.Error(codes.Unimplemented, "no responder")
}

func (s *stubTokenServiceServer) RevokeCredentials(ctx context.Context, req *tokensv1.RevokeCredentialsRequest) (*tokensv1.RevokeCredentialsResponse, error) {
	return &tokensv1.RevokeCredentialsResponse{}, nil
}

func newStubClient(t *testing.T, respond func(req *tokensv1.AssignCredentialsRequest) (*tokensv1.AssignCredentialsResponse, error), sub *subsystem.Subsystem) (*credassign.Client, *stubTokenServiceServer, func()) {
	t.Helper()
	stub := &stubTokenServiceServer{respond: respond}
	listener := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	tokensv1.RegisterTokenServiceServer(srv, stub)
	go func() { _ = srv.Serve(listener) }()
	conn, err := grpc.NewClient(
		"passthrough://bufnet",
		grpc.WithContextDialer(func(_ context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(context.Background())
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	client := credassign.NewClient(credassign.ClientOptions{
		Stub:      tokensv1.NewTokenServiceClient(conn),
		Leases:    credleasestore.New(),
		Creds:     credcache.New(),
		TenantID:  "acme",
		Timeout:   500 * time.Millisecond,
		Subsystem: sub,
	})
	closer := func() {
		_ = conn.Close()
		srv.Stop()
		_ = listener.Close()
	}
	return client, stub, closer
}

// spec: §4.3 line 211 — consecutive Unavailable failures trip the
// breaker, and subsequent Assign calls fail fast with
// ErrTokenServiceUnavailable rather than re-issuing the gRPC.
func TestClientBreakerOpensOnConsecutiveUnavailable(t *testing.T) {
	sub := &subsystem.Subsystem{
		Name:    "token_service",
		Breaker: &subsystem.Breaker{FailureThreshold: 3, Cooldown: time.Hour},
	}
	client, stub, closer := newStubClient(t,
		func(req *tokensv1.AssignCredentialsRequest) (*tokensv1.AssignCredentialsResponse, error) {
			stub := req // shadow the outer; not needed
			_ = stub
			return nil, status.Error(codes.Unavailable, "token service down")
		},
		sub,
	)
	t.Cleanup(closer)
	_ = stub

	// First three calls: gRPC reaches the server, which returns
	// Unavailable. The breaker counts each failure.
	for i := 0; i < 3; i++ {
		_, err := client.Assign("any-pool", "s_1", "", "")
		if err == nil {
			t.Fatalf("call %d: Assign succeeded against an Unavailable server", i)
		}
		if errors.Is(err, credassign.ErrTokenServiceUnavailable) {
			t.Fatalf("call %d: breaker should not have opened yet, got %v", i, err)
		}
	}
	if state := sub.State(); state != subsystem.StateOpen {
		t.Fatalf("after 3 consecutive failures: breaker state = %s, want open", state)
	}

	// Fourth call: breaker is open; the Client must short-circuit with
	// ErrTokenServiceUnavailable and never reach the stub server.
	_, err := client.Assign("any-pool", "s_1", "", "")
	if !errors.Is(err, credassign.ErrTokenServiceUnavailable) {
		t.Fatalf("breaker-open call: got %v, want ErrTokenServiceUnavailable", err)
	}
}

// spec: §4.3 line 211 — NotFound (user-classified) errors must not
// count against the breaker.
func TestClientBreakerSkipsNotFound(t *testing.T) {
	sub := &subsystem.Subsystem{
		Name:    "token_service",
		Breaker: &subsystem.Breaker{FailureThreshold: 2, Cooldown: time.Hour},
	}
	client, _, closer := newStubClient(t,
		func(req *tokensv1.AssignCredentialsRequest) (*tokensv1.AssignCredentialsResponse, error) {
			return nil, status.Error(codes.NotFound, "pool not registered")
		},
		sub,
	)
	t.Cleanup(closer)

	for i := 0; i < 5; i++ {
		_, err := client.Assign("missing", "s_1", "", "")
		if !errors.Is(err, credassign.ErrPoolNotFound) {
			t.Fatalf("call %d: expected ErrPoolNotFound, got %v", i, err)
		}
	}
	if state := sub.State(); state != subsystem.StateClosed {
		t.Fatalf("after 5 NotFound errors: breaker state = %s, want closed (user errors must not count)", state)
	}
}

// spec: §4.3 line 211 — a successful response resets consecutive
// failure count so a transient blip does not cumulatively trip the
// breaker.
func TestClientBreakerResetsOnSuccess(t *testing.T) {
	sub := &subsystem.Subsystem{
		Name:    "token_service",
		Breaker: &subsystem.Breaker{FailureThreshold: 3, Cooldown: time.Hour},
	}
	var failing atomic.Bool
	failing.Store(true)
	client, _, closer := newStubClient(t,
		func(req *tokensv1.AssignCredentialsRequest) (*tokensv1.AssignCredentialsResponse, error) {
			if failing.Load() {
				return nil, status.Error(codes.Unavailable, "blip")
			}
			// Synthesize a successful response with a single lease in the
			// map keyed by provider id. Proxy-mode leases require
			// proxy_url, proxy_dialect, and lease_token; otherwise the
			// in-process credential.NewLease validator rejects the wire
			// form on the gateway side.
			return &tokensv1.AssignCredentialsResponse{
				Leases: map[string]*tokensv1.CredentialLease{
					"anthropic_direct": {
						LeaseId:      "lease_1",
						SessionId:    "s_1",
						Provider:     string(credential.ProviderAnthropicDirect),
						Source:       string(credential.SourcePool),
						PoolId:       "pool",
						CredentialId: "key-1",
						TenantId:     "acme",
						DeliveryMode: string(credential.DeliveryDirect),
					},
				},
			}, nil
		},
		sub,
	)
	t.Cleanup(closer)

	// Two consecutive failures, just under threshold.
	for i := 0; i < 2; i++ {
		if _, err := client.Assign("pool", "s_1", "", ""); err == nil {
			t.Fatalf("call %d: expected failure, got nil", i)
		}
	}
	failing.Store(false)
	// Success resets the consecutive-fail counter.
	if _, err := client.Assign("pool", "s_1", "", ""); err != nil {
		t.Fatalf("post-blip Assign: %v", err)
	}
	if state := sub.State(); state != subsystem.StateClosed {
		t.Fatalf("after success: breaker state = %s, want closed", state)
	}

	// Two more failures: still under threshold because the success
	// reset the counter.
	failing.Store(true)
	for i := 0; i < 2; i++ {
		_, _ = client.Assign("pool", "s_1", "", "")
	}
	if state := sub.State(); state != subsystem.StateClosed {
		t.Fatalf("after success+2 failures: breaker state = %s, want closed", state)
	}
}

// spec: §4.3 line 211 — nil breaker is a no-op: tests/dev mode without
// the breaker continue to receive raw gRPC errors.
func TestClientWithoutBreakerPassesRawErrors(t *testing.T) {
	client, _, closer := newStubClient(t,
		func(req *tokensv1.AssignCredentialsRequest) (*tokensv1.AssignCredentialsResponse, error) {
			return nil, status.Error(codes.Unavailable, "down")
		},
		nil,
	)
	t.Cleanup(closer)

	_, err := client.Assign("pool", "s_1", "", "")
	if err == nil {
		t.Fatal("expected an error against an Unavailable server")
	}
	if errors.Is(err, credassign.ErrTokenServiceUnavailable) {
		t.Fatalf("nil-breaker call surfaced ErrTokenServiceUnavailable; want raw mapped error: %v", err)
	}
}
