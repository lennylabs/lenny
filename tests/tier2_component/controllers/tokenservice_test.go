//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §4.3 / §12.2.4 Token Service gRPC controller.
// Drives pkg/tokenservice.GRPCServer over an in-process bufconn link
// the same way the gateway will reach the deployed Token Service over
// mTLS, and asserts the AssignCredentials, RotateCredentials, and
// RevokeCredentials contracts against a registered §4.9 credential
// pool.
package controllers_test

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credleasestore"
	tokensv1 "github.com/lennylabs/lenny/pkg/proto/tokenservice/v1"
	"github.com/lennylabs/lenny/pkg/tokenservice"
)

// startTokenService brings up the §4.3 Token Service gRPC server on an
// in-process bufconn listener with one registered proxy-mode pool and
// returns a connected client.
func startTokenService(t *testing.T) (tokensv1.TokenServiceClient, *credleasestore.Store) {
	t.Helper()
	leases := credleasestore.New()
	creds := credcache.New()
	assign := credassign.New(leases, creds)
	assign.RegisterPool(credassign.Pool{
		Name:         "claude-prod",
		Provider:     credential.ProviderAnthropicDirect,
		DeliveryMode: credential.DeliveryProxy,
		Strategy:     credential.StrategyLeastLoaded,
		ProxyURL:     "https://gateway-internal:8443/llm-proxy",
		ProxyDialect: "anthropic",
		Credentials: []credassign.PoolCredential{
			{ID: "key-1", APIKey: "sk-ant-real", Healthy: true},
		},
	})
	srv := tokenservice.NewGRPCServer(assign, leases)

	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	tokensv1.RegisterTokenServiceServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return tokensv1.NewTokenServiceClient(conn), leases
}

// spec: 4.3 / 12.2.4
// diagnosis: AssignCredentials materializes one §4.9 lease per
// requested pool, the lease is recorded in the credential-lease store
// for proxy-side resolution, and the wire response carries the proxy
// dialect plus the lease bearer token.
func TestTokenServiceController(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("AssignCredentials returns proxy-mode lease", func(t *testing.T) {
		client, leases := startTokenService(t)
		resp, err := client.AssignCredentials(ctx, &tokensv1.AssignCredentialsRequest{
			TenantId:     "acme",
			SessionId:    "s_assign_1",
			PodSpiffeUri: "spiffe://lenny.test/agent/claude-prod/pod-1",
			PoolIds:      []string{"claude-prod"},
		})
		if err != nil {
			t.Fatalf("AssignCredentials: %v", err)
		}
		got, ok := resp.Leases["anthropic_direct"]
		if !ok {
			t.Fatalf("response has no anthropic_direct lease: %+v", resp.Leases)
		}
		if got.PoolId != "claude-prod" || got.CredentialId != "key-1" {
			t.Errorf("lease identity = %s/%s, want claude-prod/key-1", got.PoolId, got.CredentialId)
		}
		if got.DeliveryMode != string(credential.DeliveryProxy) {
			t.Errorf("delivery_mode = %q, want proxy", got.DeliveryMode)
		}
		if got.ProxyDialect != "anthropic" || got.ProxyUrl == "" || got.LeaseToken == "" {
			t.Errorf("proxy materializedConfig incomplete: %+v", got)
		}
		if _, found := leases.GetByID(got.LeaseId); !found {
			t.Errorf("lease %q not recorded in lease store", got.LeaseId)
		}
	})

	t.Run("AssignCredentials rejects unknown pool", func(t *testing.T) {
		client, _ := startTokenService(t)
		_, err := client.AssignCredentials(ctx, &tokensv1.AssignCredentialsRequest{
			TenantId:  "acme",
			SessionId: "s_assign_2",
			PoolIds:   []string{"missing"},
		})
		if status.Code(err) != codes.NotFound {
			t.Errorf("err = %v, want NotFound for unknown pool", err)
		}
	})

	t.Run("RotateCredentials reissues fresh lease", func(t *testing.T) {
		client, leases := startTokenService(t)
		resp, err := client.AssignCredentials(ctx, &tokensv1.AssignCredentialsRequest{
			TenantId:  "acme",
			SessionId: "s_rotate_1",
			PoolIds:   []string{"claude-prod"},
		})
		if err != nil {
			t.Fatalf("Assign: %v", err)
		}
		old := resp.Leases["anthropic_direct"]
		rotated, err := client.RotateCredentials(ctx, &tokensv1.RotateCredentialsRequest{
			TenantId: "acme",
			LeaseId:  old.LeaseId,
		})
		if err != nil {
			t.Fatalf("RotateCredentials: %v", err)
		}
		if rotated.Lease.LeaseId == old.LeaseId {
			t.Errorf("rotated lease reused id %q", old.LeaseId)
		}
		if _, stillThere := leases.GetByID(old.LeaseId); stillThere {
			t.Errorf("previous lease %q was not released", old.LeaseId)
		}
		if _, ok := leases.GetByID(rotated.Lease.LeaseId); !ok {
			t.Errorf("rotated lease %q missing from store", rotated.Lease.LeaseId)
		}
	})

	t.Run("RevokeCredentials drops the lease", func(t *testing.T) {
		client, leases := startTokenService(t)
		resp, err := client.AssignCredentials(ctx, &tokensv1.AssignCredentialsRequest{
			TenantId:  "acme",
			SessionId: "s_revoke_1",
			PoolIds:   []string{"claude-prod"},
		})
		if err != nil {
			t.Fatalf("Assign: %v", err)
		}
		got := resp.Leases["anthropic_direct"]
		if _, ok := client.RevokeCredentials(ctx, &tokensv1.RevokeCredentialsRequest{
			TenantId: "acme",
			LeaseId:  got.LeaseId,
			Reason:   "test",
		}); ok != nil {
			t.Fatalf("RevokeCredentials: %v", ok)
		}
		if _, stillThere := leases.GetByID(got.LeaseId); stillThere {
			t.Errorf("lease %q still resolvable after revoke", got.LeaseId)
		}
	})
}
