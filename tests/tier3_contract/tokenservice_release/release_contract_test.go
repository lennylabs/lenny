// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract tests for the gateway ↔ Token Service lease release
// and breaker contract. Each test runs the real gateway-side
// credassign.Client against the real pkg/tokenservice GRPCServer over an
// in-process gRPC link, so the RevokeCredentials and AssignCredentials
// wire contract is exercised on both sides.
package tokenservice_release_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/core/subsystem"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	tokensv1 "github.com/lennylabs/lenny/pkg/proto/tokenservice/v1"
	"github.com/lennylabs/lenny/pkg/tokenservice"
)

// link is one gateway client wired to one real Token Service.
type link struct {
	client       *credassign.Client
	clientLeases credleasestore.LeaseStore
	serverLeases credleasestore.LeaseStore
	stub         tokensv1.TokenServiceClient
	sub          *subsystem.Subsystem
}

// newLink starts a real Token Service with a healthy pool `healthy` and a
// pool `exhausted` whose only credential is unhealthy, and connects a
// gateway client with replica tenant replicaTenant and a breaker that
// opens on the first counted failure.
func newLink(t *testing.T, replicaTenant string) *link {
	t.Helper()
	serverLeases := credleasestore.New()
	assign := credassign.New(serverLeases, credcache.New())
	for _, p := range []struct {
		name    string
		healthy bool
	}{{"healthy", true}, {"exhausted", false}} {
		assign.RegisterPool(credassign.Pool{
			Name:         p.name,
			Provider:     credential.ProviderAnthropicDirect,
			DeliveryMode: credential.DeliveryProxy,
			Strategy:     credential.StrategyLeastLoaded,
			ProxyURL:     "https://gateway-internal:8443/llm-proxy",
			ProxyDialect: "anthropic",
			Credentials:  []credassign.PoolCredential{{ID: "key-1", APIKey: "sk-test", Healthy: p.healthy}},
		})
	}
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	tokensv1.RegisterTokenServiceServer(gs, tokenservice.NewGRPCServer(assign, serverLeases))
	go func() { _ = gs.Serve(lis) }()
	conn, err := grpc.NewClient(
		"passthrough://bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return lis.DialContext(context.Background())
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		gs.Stop()
		_ = lis.Close()
	})
	stub := tokensv1.NewTokenServiceClient(conn)
	sub := &subsystem.Subsystem{Name: "token_service", Breaker: &subsystem.Breaker{FailureThreshold: 1, Cooldown: time.Hour}}
	clientLeases := credleasestore.New()
	return &link{
		client: credassign.NewClient(credassign.ClientOptions{
			Stub: stub, Leases: clientLeases, Creds: credcache.New(),
			TenantID: replicaTenant, Timeout: 2 * time.Second, Subsystem: sub,
		}),
		clientLeases: clientLeases,
		serverLeases: serverLeases,
		stub:         stub,
		sub:          sub,
	}
}

// diagnosis: a failure means a terminal lease release no longer frees the
// Token Service lease for the session's tenant. Under the chart's
// single-tenant configuration (replica tenant `default`) or a
// multi-tenant one (no replica tenant), the release must carry a tenant
// the real Token Service accepts, so the authoritative lease is removed.
//
// spec: §7.1 (Normal Flow, step 23), §4.3 (Token Service), §4.2 (Session
// Manager, tenant_id claim extraction).
func TestReleaseFreesTokenServiceLeaseEveryTenancyConfig_spec_7_1(t *testing.T) {
	cases := []struct {
		name          string
		replicaTenant string
		sessionTenant string
	}{
		{name: "single tenant, chart default", replicaTenant: "default", sessionTenant: "default"},
		{name: "multi tenant, no replica tenant", replicaTenant: "", sessionTenant: "acme"},
		{name: "multi tenant, explicit replica tenant differs", replicaTenant: "default", sessionTenant: "globex"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := newLink(t, tc.replicaTenant)
			lease, err := l.client.Assign("healthy", "s_1", "", tc.sessionTenant)
			if err != nil {
				t.Fatalf("Assign: %v", err)
			}
			if _, ok := l.serverLeases.GetByID(lease.LeaseID); !ok {
				t.Fatal("Token Service holds no lease after Assign")
			}
			if err := l.client.ReleaseSession("s_1"); err != nil {
				t.Fatalf("ReleaseSession: %v", err)
			}
			if _, ok := l.serverLeases.GetByID(lease.LeaseID); ok {
				t.Fatal("Token Service still holds the lease after release; the pool slot was not freed")
			}
			if _, ok := l.clientLeases.GetByID(lease.LeaseID); ok {
				t.Fatal("gateway still holds the lease after release")
			}
		})
	}
}

// diagnosis: a failure means the Token Service stopped rejecting an
// empty tenant on RevokeCredentials, or the gateway client started
// sending one. The client must refuse to send an empty tenant and must
// surface the failure, leaving the authoritative lease visible as
// still held rather than reporting a silent success.
//
// spec: §4.3 (Token Service), §7.1 (Normal Flow, step 23).
func TestReleaseWithoutTenantIsRejectedAndSurfaced_spec_4_3(t *testing.T) {
	l := newLink(t, "")
	// Mint a lease for acme; the client's local copy is stripped of its
	// tenant below so the client holds a tenantless lease.
	lease, err := l.client.Assign("healthy", "s_1", "", "acme")
	if err != nil {
		t.Fatalf("Assign: %v", err)
	}
	// The server contract: an empty tenant is rejected.
	_, rerr := l.stub.RevokeCredentials(context.Background(), &tokensv1.RevokeCredentialsRequest{LeaseId: lease.LeaseID})
	if status.Code(rerr) != codes.InvalidArgument {
		t.Fatalf("RevokeCredentials with empty tenant = %v, want InvalidArgument", rerr)
	}
	// The client contract: a lease with no recorded tenant and no replica
	// tenant is not sent, and the failure is returned.
	stripped := lease
	stripped.TenantID = ""
	if err := l.clientLeases.Put(stripped); err != nil {
		t.Fatalf("rewrite local lease: %v", err)
	}
	if err := l.client.Release(lease.LeaseID); !errors.Is(err, credassign.ErrReleaseTenantUnknown) {
		t.Fatalf("Release = %v, want ErrReleaseTenantUnknown", err)
	}
	if _, ok := l.serverLeases.GetByID(lease.LeaseID); !ok {
		t.Fatal("server lease vanished although no revoke was sent")
	}
}

// diagnosis: a failure means an exhausted credential pool, answered by
// the real Token Service as ResourceExhausted, counts against the §4.3
// breaker again, so one exhausted pool would block assignment from every
// healthy pool on the replica.
//
// spec: §4.3 (Token Service, High availability and failure handling),
// §4.9 (Credential Leasing Service).
func TestExhaustedPoolDoesNotOpenBreaker_spec_4_3(t *testing.T) {
	l := newLink(t, "default")
	for i := 0; i < 5; i++ {
		_, err := l.client.Assign("exhausted", "s_x", "", "default")
		if !errors.Is(err, credential.ErrPoolExhausted) {
			t.Fatalf("call %d: Assign from exhausted pool = %v, want ErrPoolExhausted", i, err)
		}
	}
	if st := l.sub.State(); st != subsystem.StateClosed {
		t.Fatalf("breaker state = %s after exhausted-pool answers, want closed", st)
	}
	if _, err := l.client.Assign("healthy", "s_y", "", "default"); err != nil {
		t.Fatalf("Assign from a healthy pool after exhausted answers: %v", err)
	}
	// An unregistered pool (NotFound) is a healthy answer as well.
	if _, err := l.client.Assign("missing", "s_z", "", "default"); !errors.Is(err, credassign.ErrPoolNotFound) {
		t.Fatalf("Assign from a missing pool = %v, want ErrPoolNotFound", err)
	}
	if st := l.sub.State(); st != subsystem.StateClosed {
		t.Fatalf("breaker state = %s after NotFound, want closed", st)
	}
}
