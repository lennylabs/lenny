// SPDX-License-Identifier: MIT

package leasecontrol_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
)

// passThrough is the unary handler the interceptor wraps; it records
// whether it ran so a test can assert the call reached the handler.
func passThrough(ran *bool) grpc.UnaryHandler {
	return func(ctx context.Context, req any) (any, error) {
		*ran = true
		return "ok", nil
	}
}

// tlsPeerCtx builds a context carrying a gRPC peer with TLS auth info.
// verified controls whether the peer's VerifiedChains is non-empty, the
// signal the §4.7/§15.3 mTLS gate keys on.
func tlsPeerCtx(verified bool) context.Context {
	state := tls.ConnectionState{}
	if verified {
		state.VerifiedChains = [][]*x509.Certificate{{&x509.Certificate{}}}
	}
	return peer.NewContext(context.Background(), &peer.Peer{
		AuthInfo: credentials.TLSInfo{State: state},
	})
}

// spec: §4.7 line 616; §15.3 — when mTLS verification is active the
// GatewayControl interceptor admits only peers with a verified client
// certificate chain and rejects everything else with Unauthenticated.
func TestRequireVerifiedPeerInterceptor_Spec4_7(t *testing.T) {
	cases := []struct {
		name     string
		enabled  bool
		ctx      context.Context
		wantRan  bool
		wantCode codes.Code
	}{
		{
			name:    "disabled passes through with no peer (dev plaintext)",
			enabled: false,
			ctx:     context.Background(),
			wantRan: true,
		},
		{
			name:    "disabled passes through even without verified chain",
			enabled: false,
			ctx:     tlsPeerCtx(false),
			wantRan: true,
		},
		{
			name:     "enabled rejects a call with no peer",
			enabled:  true,
			ctx:      context.Background(),
			wantRan:  false,
			wantCode: codes.Unauthenticated,
		},
		{
			name:     "enabled rejects a TLS peer with no verified chain",
			enabled:  true,
			ctx:      tlsPeerCtx(false),
			wantRan:  false,
			wantCode: codes.Unauthenticated,
		},
		{
			name:    "enabled admits a peer with a verified chain",
			enabled: true,
			ctx:     tlsPeerCtx(true),
			wantRan: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ran := false
			interceptor := leasecontrol.RequireVerifiedPeerInterceptor(tc.enabled)
			_, err := interceptor(tc.ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/ExtendLease"}, passThrough(&ran))
			if ran != tc.wantRan {
				t.Fatalf("handler ran = %v, want %v", ran, tc.wantRan)
			}
			if tc.wantCode != codes.OK {
				if status.Code(err) != tc.wantCode {
					t.Fatalf("status code = %v, want %v (err=%v)", status.Code(err), tc.wantCode, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// spec: §4.7 line 616 — a peer whose AuthInfo is not TLS (e.g. an
// insecure in-cluster dial that should never reach a verified listener)
// is rejected when verification is active.
func TestRequireVerifiedPeerInterceptor_NonTLSPeerRejected(t *testing.T) {
	ctx := peer.NewContext(context.Background(), &peer.Peer{AuthInfo: nil})
	ran := false
	interceptor := leasecontrol.RequireVerifiedPeerInterceptor(true)
	_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{}, passThrough(&ran))
	if ran {
		t.Fatal("handler ran on a non-TLS peer; want rejection")
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("status code = %v, want Unauthenticated", status.Code(err))
	}
}

// spec: §8.6 line 735; §15.1 line 868 — ClearSubtreeDenial clears the
// extension-denied flag for a known tree and reports found, and reports
// not-found (without error) for an unknown tree so the admin endpoint
// answers 404.
func TestMemoryBudgetSource_ClearSubtreeDenial_Spec8_6(t *testing.T) {
	const root = "root-1"
	const tenant = "acme"
	budgets := leasecontrol.NewMemoryBudgetSource()
	budgets.RegisterTree(root, leasecontrol.TreeConfig{
		TenantID:           tenant,
		CurrentTokenBudget: 100,
		DeploymentBase:     1000,
		DeploymentMax:      2000,
	})
	budgets.MarkDenied(root)

	// Confirm the denial is in force before clearing.
	tb, err := budgets.TreeBudget(context.Background(), tenant, root)
	if err != nil {
		t.Fatalf("TreeBudget: %v", err)
	}
	if !tb.ExtensionDenied {
		t.Fatal("expected the tree to be extension-denied after MarkDenied")
	}

	found, err := budgets.ClearSubtreeDenial(context.Background(), root, root)
	if err != nil {
		t.Fatalf("ClearSubtreeDenial: %v", err)
	}
	if !found {
		t.Fatal("ClearSubtreeDenial found=false for a known tree")
	}
	tb, err = budgets.TreeBudget(context.Background(), tenant, root)
	if err != nil {
		t.Fatalf("TreeBudget after clear: %v", err)
	}
	if tb.ExtensionDenied {
		t.Fatal("extension-denied flag still set after ClearSubtreeDenial")
	}

	// Unknown tree → found=false, no error.
	found, err = budgets.ClearSubtreeDenial(context.Background(), "missing-root", "missing-sub")
	if err != nil {
		t.Fatalf("ClearSubtreeDenial(unknown): unexpected error %v", err)
	}
	if found {
		t.Fatal("ClearSubtreeDenial found=true for an unknown tree")
	}
}
