// SPDX-License-Identifier: MIT

//go:build component

// Tier-2 component test for the §4.3 gateway-side Token Service
// client. The companion file tokenservice_test.go exercises the
// server-side GRPCServer; this file drives the gateway-side
// credassign.Client over the same in-process bufconn link so the
// full cutover code path (gateway → mTLS-shaped gRPC → Token Service
// → in-process credassign.Service → credential.Lease → wire encode
// → bufconn → gateway-side decode → local credcache + credleasestore
// mirror) is exercised end-to-end without a separate process.

package controllers_test

import (
	"context"
	"errors"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credleasestore"
	"github.com/lennylabs/lenny/pkg/tokenservice"
	tokensv1 "github.com/lennylabs/lenny/pkg/proto/tokenservice/v1"
)

// gatewayClientHarness wires both halves of the §4.3 boundary into one
// in-process harness: a real GRPCServer plus credassign.Service on the
// Token Service side, and a real credassign.Client on the gateway
// side. Tests can drive the gateway-side surface and assert on both
// the gateway-local mirror state and the Token Service's authoritative
// state.
type gatewayClientHarness struct {
	client       *credassign.Client
	clientLeases credleasestore.LeaseStore
	clientCreds  *credcache.Cache
	serverLeases credleasestore.LeaseStore
	serverCreds  *credcache.Cache
	stop         func()
}

func newGatewayClientHarness(t *testing.T) *gatewayClientHarness {
	t.Helper()
	serverLeases := credleasestore.New()
	serverCreds := credcache.New()
	assign := credassign.New(serverLeases, serverCreds)
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
	srv := tokenservice.NewGRPCServer(assign, serverLeases)

	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	tokensv1.RegisterTokenServiceServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()

	conn, err := grpc.NewClient(
		"passthrough://bufnet",
		grpc.WithContextDialer(func(_ context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(context.Background())
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial bufconn: %v", err)
	}

	clientLeases := credleasestore.New()
	clientCreds := credcache.New()
	client := credassign.NewClient(credassign.ClientOptions{
		Stub:     tokensv1.NewTokenServiceClient(conn),
		Leases:   clientLeases,
		Creds:    clientCreds,
		TenantID: "acme",
	})
	return &gatewayClientHarness{
		client:       client,
		clientLeases: clientLeases,
		clientCreds:  clientCreds,
		serverLeases: serverLeases,
		serverCreds:  serverCreds,
		stop: func() {
			_ = conn.Close()
			gs.Stop()
			_ = lis.Close()
		},
	}
}

// spec: 4.3 / 4.9
// diagnosis: a §4.7 binder calling AssignProto through the Client gets
// the adapter wire form for proxy-mode delivery, the gateway-local
// credcache mirrors the materialized upstream secret the Token Service
// returned, and the gateway-local credleasestore mirrors the lease so
// the §4.9 LLM proxy can resolve the bearer without another round-trip.
func TestTokenServiceClientCutoverEndToEnd(t *testing.T) {
	t.Parallel()
	h := newGatewayClientHarness(t)
	defer h.stop()

	wire, err := h.client.AssignProto("claude-prod", "s_1", "spiffe://lenny.test/agent/claude-prod/pod-1")
	if err != nil {
		t.Fatalf("AssignProto: %v", err)
	}
	if wire.GetProvider() != string(credential.ProviderAnthropicDirect) {
		t.Errorf("wire provider = %q, want %q", wire.GetProvider(), credential.ProviderAnthropicDirect)
	}

	if _, ok := h.clientLeases.GetByID(wire.GetLeaseId()); !ok {
		t.Errorf("gateway-local lease store missing lease %q", wire.GetLeaseId())
	}
	if _, ok := h.serverLeases.GetByID(wire.GetLeaseId()); !ok {
		t.Errorf("Token Service lease store missing lease %q", wire.GetLeaseId())
	}

	// Decode the lease from the gateway-local store to derive its
	// CredentialKey for the credcache lookup. The §4.9 proxy uses the
	// same key on its hot path.
	lease, ok := h.clientLeases.GetByID(wire.GetLeaseId())
	if !ok {
		t.Fatalf("expected gateway-local lease after AssignProto")
	}
	cached, ok := h.clientCreds.UpstreamCredential(lease)
	if !ok || cached != "sk-ant-real" {
		t.Errorf("gateway-local credcache for lease = (%q, %v), want (sk-ant-real, true)", cached, ok)
	}
}

// spec: 4.3 / 4.9
// diagnosis: the §4.9 Proactive Lease Renewal worker depends on
// re-minting from the same pool; the gateway-side Client records the
// pool binding for each lease and exposes PoolForLease so the worker
// rotates against the right §4.9 pool after the cutover.
func TestTokenServiceClientPoolBindingForRenewal(t *testing.T) {
	t.Parallel()
	h := newGatewayClientHarness(t)
	defer h.stop()

	lease, err := h.client.Assign("claude-prod", "s_renew", "")
	if err != nil {
		t.Fatalf("Assign: %v", err)
	}
	pool, ok := h.client.PoolForLease(lease.LeaseID)
	if !ok || pool != "claude-prod" {
		t.Errorf("PoolForLease = (%q, %v), want (claude-prod, true)", pool, ok)
	}
}

// spec: 4.3 / 4.9
// diagnosis: Release on the gateway-side Client issues
// RevokeCredentials over the wire and drops the gateway-local lease,
// so the §4.9 proxy stops resolving the bearer. The Token Service's
// authoritative store also drops the lease so a rotated credential
// cannot resurrect through it.
func TestTokenServiceClientReleaseReleasesBothSides(t *testing.T) {
	t.Parallel()
	h := newGatewayClientHarness(t)
	defer h.stop()

	lease, err := h.client.Assign("claude-prod", "s_rel", "")
	if err != nil {
		t.Fatalf("Assign: %v", err)
	}
	h.client.Release(lease.LeaseID)
	if _, ok := h.clientLeases.GetByID(lease.LeaseID); ok {
		t.Errorf("gateway-local lease %q still present after Release", lease.LeaseID)
	}
	if _, ok := h.serverLeases.GetByID(lease.LeaseID); ok {
		t.Errorf("Token Service lease %q still present after Release", lease.LeaseID)
	}
}

// spec: 4.3
// diagnosis: the gateway-side Client maps the §4.9 ErrPoolNotFound and
// ErrPoolExhausted sentinels back from gRPC status codes so existing
// call sites that branch on these errors keep working after the
// cutover.
func TestTokenServiceClientErrorMapping(t *testing.T) {
	t.Parallel()
	h := newGatewayClientHarness(t)
	defer h.stop()

	if _, err := h.client.Assign("missing", "s_err", ""); !errors.Is(err, credassign.ErrPoolNotFound) {
		t.Errorf("unknown pool err = %v, want ErrPoolNotFound", err)
	}
}
