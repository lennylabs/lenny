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
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/core/subsystem"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	tokensv1 "github.com/lennylabs/lenny/pkg/proto/tokenservice/v1"
)

// countingTokenService is a switchable TokenService stub: while healthy
// it mints a single direct-mode lease carrying an upstream credential;
// once down is set every AssignCredentials returns Unavailable. calls
// counts every AssignCredentials the gateway actually put on the wire,
// so a test can prove a resolution served no Token Service round-trip.
type countingTokenService struct {
	tokensv1.UnimplementedTokenServiceServer
	down       atomic.Bool
	calls      atomic.Int32
	expiresAt  time.Time
	upstream   string
	credential string
}

func (s *countingTokenService) AssignCredentials(_ context.Context, req *tokensv1.AssignCredentialsRequest) (*tokensv1.AssignCredentialsResponse, error) {
	s.calls.Add(1)
	if s.down.Load() {
		return nil, status.Error(codes.Unavailable, "token service down")
	}
	pool := ""
	if len(req.GetPoolIds()) > 0 {
		pool = req.GetPoolIds()[0]
	}
	return &tokensv1.AssignCredentialsResponse{
		Leases: map[string]*tokensv1.CredentialLease{
			string(credential.ProviderAnthropicDirect): {
				LeaseId:            "lease_" + req.GetSessionId(),
				SessionId:          req.GetSessionId(),
				Provider:           string(credential.ProviderAnthropicDirect),
				Source:             string(credential.SourcePool),
				PoolId:             pool,
				CredentialId:       s.credential,
				TenantId:           req.GetTenantId(),
				DeliveryMode:       string(credential.DeliveryDirect),
				IssuedAt:           timestamppb.New(time.Now()),
				ExpiresAt:          timestamppb.New(s.expiresAt),
				UpstreamCredential: s.upstream,
				MaterializedConfig: map[string]string{"apiKey": s.upstream},
			},
		},
	}, nil
}

func (s *countingTokenService) RevokeCredentials(_ context.Context, _ *tokensv1.RevokeCredentialsRequest) (*tokensv1.RevokeCredentialsResponse, error) {
	return &tokensv1.RevokeCredentialsResponse{}, nil
}

// newDegradedModeClient wires a Client to a countingTokenService over a
// bufconn, returning the client, the stub, and the same in-memory lease
// store and credential cache the client mirrors into. Retaining the
// stores lets the test resolve an already-leased credential exactly as
// the §4.9 LLM proxy does, without a Token Service round-trip.
func newDegradedModeClient(t *testing.T, sub *subsystem.Subsystem) (*credassign.Client, *countingTokenService, *credleasestore.Store, *credcache.Cache) {
	t.Helper()
	stub := &countingTokenService{
		expiresAt:  time.Now().Add(time.Hour),
		upstream:   "sk-upstream-leased",
		credential: "key-leased",
	}
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
	leases := credleasestore.New()
	creds := credcache.New()
	client := credassign.NewClient(credassign.ClientOptions{
		Stub:      tokensv1.NewTokenServiceClient(conn),
		Leases:    leases,
		Creds:     creds,
		TenantID:  "acme",
		Timeout:   500 * time.Millisecond,
		Subsystem: sub,
	})
	t.Cleanup(func() {
		_ = conn.Close()
		srv.Stop()
		_ = listener.Close()
	})
	return client, stub, leases, creds
}

// TestTokenServiceOutageDifferentiatedDegradation pins the §4.3
// "High availability and failure handling" degraded-mode matrix at the
// gateway credential-assignment layer where it is observable: with the
// Token Service unreachable and the per-subsystem breaker open, a
// session that already holds a lease keeps resolving its upstream
// credential from the in-memory cache with no Token Service round-trip
// (the leased session continues; the cached lease survives until its
// TTL), while a new session that requires a credential fails fast with
// the retryable ErrTokenServiceUnavailable. The third class — a session
// that needs no LLM credential — never reaches this layer at all; the
// §4.7 binder skips assignment entirely for a request that names no
// credential pools, covered by the podsession binder tests.
//
// spec: §4.3 "Sessions that already hold credential leases continue
// operating — leases are self-contained and do not require the Token
// Service until renewal."; "New sessions that require LLM or OAuth
// credentials cannot start and fail with a retryable error"; "the
// gateway caches active credential leases in memory. Token Service
// unavailability does not affect already-leased credentials until the
// lease expires."
func TestTokenServiceOutageDifferentiatedDegradation(t *testing.T) {
	sub := &subsystem.Subsystem{
		Name:    "token_service",
		Breaker: &subsystem.Breaker{FailureThreshold: 3, Cooldown: time.Hour},
	}
	client, stub, _, creds := newDegradedModeClient(t, sub)

	// A session mints a lease while the Token Service is healthy. The
	// Client mirrors the materialized upstream credential into the local
	// credcache so the LLM proxy resolves it without another round-trip.
	leased, err := client.Assign("claude-prod", "s_leased", "", "")
	if err != nil {
		t.Fatalf("initial Assign for the leased session: %v", err)
	}
	if apiKey, ok := creds.UpstreamCredential(leased); !ok || apiKey != stub.upstream {
		t.Fatalf("cached upstream credential after Assign = (%q, %v), want (%q, true)", apiKey, ok, stub.upstream)
	}

	// Injection: the Token Service goes down. Drive the breaker open with
	// consecutive new-session assignments so the outage is fully
	// realized before the leased-session assertions.
	stub.down.Store(true)
	for i := 0; i < 3; i++ {
		if _, err := client.Assign("claude-new", "s_new", "", ""); err == nil {
			t.Fatalf("new-session Assign %d succeeded against a downed Token Service", i)
		}
	}
	if state := sub.State(); state != subsystem.StateOpen {
		t.Fatalf("breaker state after three consecutive Unavailable failures = %s, want open", state)
	}

	// Class: a new session that requires a credential fails fast with the
	// retryable sentinel and never reaches the wire (breaker short-circuit).
	callsBeforeNew := stub.calls.Load()
	_, err = client.Assign("claude-new", "s_new2", "", "")
	if !errors.Is(err, credassign.ErrTokenServiceUnavailable) {
		t.Fatalf("new credentialed session during outage: got %v, want ErrTokenServiceUnavailable", err)
	}
	if got := stub.calls.Load(); got != callsBeforeNew {
		t.Errorf("open breaker issued %d Token Service calls for a new session, want 0 (short-circuit)", got-callsBeforeNew)
	}

	// Class: the already-leased session continues. Resolving its upstream
	// credential and its lease record during the outage must succeed and
	// must not put a single call on the wire — leases are self-contained.
	callsBeforeResolve := stub.calls.Load()
	if apiKey, ok := creds.UpstreamCredential(leased); !ok || apiKey != stub.upstream {
		t.Fatalf("leased-session credential resolve during outage = (%q, %v), want (%q, true)", apiKey, ok, stub.upstream)
	}
	if _, err := client.ProtoLeaseByID(leased.LeaseID); err != nil {
		t.Fatalf("leased-session lease resolve during outage: %v", err)
	}
	if got := stub.calls.Load(); got != callsBeforeResolve {
		t.Errorf("resolving an already-held lease issued %d Token Service calls, want 0", got-callsBeforeResolve)
	}

	// Class: the cached lease survives until its TTL, not beyond. The
	// grace period the spec promises is proportional to the lease TTL:
	// the lease is live now and lapses exactly at ExpiresAt.
	if leased.Expired(time.Now()) {
		t.Error("lease reported expired during its grace window; the cached lease must survive the outage until its TTL")
	}
	if !leased.Expired(leased.ExpiresAt) {
		t.Error("lease reported live at its ExpiresAt boundary; the grace window must end at the lease TTL")
	}
}
