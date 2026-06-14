// SPDX-License-Identifier: MIT

package credassign_test

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credleasestore"
	tokensv1 "github.com/lennylabs/lenny/pkg/proto/tokenservice/v1"
	tokenservice "github.com/lennylabs/lenny/pkg/tokenservice"
)

// clientHarness wires a §4.3 Token Service gRPC server backed by a real
// credassign.Service over an in-memory bufconn so a Client can talk to
// it without setting up TLS. The harness gives the test:
//
//   - The Client under test (gateway-side credential assigner).
//   - The Client's own lease store and credential cache so the test can
//     assert the §4.9 proxy hot-path state is mirrored locally.
//   - The server-side lease store so the test can assert the Token
//     Service's view stays in sync with the gateway's view.
type clientHarness struct {
	client       *credassign.Client
	clientLeases credleasestore.LeaseStore
	clientCreds  *credcache.Cache
	serverLeases credleasestore.LeaseStore
	closer       func()
}

func newClientHarness(t *testing.T, pool credassign.Pool) *clientHarness {
	t.Helper()

	// Server side: a real credassign.Service plus pool, exposed by the
	// GRPCServer the gateway is meant to call. The server's credcache
	// holds the upstream secret; the gateway's mirror is empty until
	// the Client invokes AssignCredentials.
	serverLeases := credleasestore.New()
	serverCreds := credcache.New()
	svc := credassign.New(serverLeases, serverCreds)
	svc.RegisterPool(pool)
	grpcSrv := tokenservice.NewGRPCServer(svc, serverLeases)

	listener := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	tokensv1.RegisterTokenServiceServer(srv, grpcSrv)
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

	clientLeases := credleasestore.New()
	clientCreds := credcache.New()
	client := credassign.NewClient(credassign.ClientOptions{
		Stub:     tokensv1.NewTokenServiceClient(conn),
		Leases:   clientLeases,
		Creds:    clientCreds,
		TenantID: "acme",
		Timeout:  2 * time.Second,
	})

	return &clientHarness{
		client:       client,
		clientLeases: clientLeases,
		clientCreds:  clientCreds,
		serverLeases: serverLeases,
		closer: func() {
			_ = conn.Close()
			srv.Stop()
			_ = listener.Close()
		},
	}
}

func clientProxyPool(name, credID, apiKey string) credassign.Pool {
	return credassign.Pool{
		Name:         name,
		Provider:     credential.ProviderAnthropicDirect,
		DeliveryMode: credential.DeliveryProxy,
		Strategy:     credential.StrategyLeastLoaded,
		ProxyURL:     "https://gateway-internal:8443/llm-proxy",
		ProxyDialect: "anthropic",
		Credentials:  []credassign.PoolCredential{{ID: credID, APIKey: apiKey, Healthy: true}},
	}
}

// spec: 4.3 / 4.9
// diagnosis: Client.Assign returns the materialized lease, records it in
// the gateway-local lease store, and caches the upstream credential so
// the §4.9 LLM reverse proxy can resolve and inject it without a second
// Token Service round-trip.
func TestClientAssignMirrorsLocally(t *testing.T) {
	h := newClientHarness(t, clientProxyPool("claude-prod", "key-1", "sk-ant-real"))
	defer h.closer()

	lease, err := h.client.Assign("claude-prod", "s_1", "spiffe://lenny.test/agent/claude-prod/pod-1", "")
	if err != nil {
		t.Fatalf("Assign: %v", err)
	}

	if lease.PoolID != "claude-prod" || lease.CredentialID != "key-1" {
		t.Errorf("lease identity = %s/%s, want claude-prod/key-1", lease.PoolID, lease.CredentialID)
	}
	if lease.DeliveryMode != credential.DeliveryProxy {
		t.Errorf("delivery = %q, want proxy", lease.DeliveryMode)
	}
	if lease.Proxy == nil || lease.Proxy.LeaseToken == "" {
		t.Errorf("proxy-mode lease missing lease token: %+v", lease)
	}

	if _, ok := h.clientLeases.GetByID(lease.LeaseID); !ok {
		t.Errorf("lease %q not mirrored to client lease store", lease.LeaseID)
	}
	if _, ok := h.serverLeases.GetByID(lease.LeaseID); !ok {
		t.Errorf("lease %q not recorded on the server", lease.LeaseID)
	}

	cached, ok := h.clientCreds.UpstreamCredential(lease)
	if !ok {
		t.Fatalf("client credcache has no entry for lease %s", lease.LeaseID)
	}
	if cached != "sk-ant-real" {
		t.Errorf("cached upstream credential = %q, want %q", cached, "sk-ant-real")
	}
}

// spec: 4.3
// diagnosis: AssignProto returns the adapter wire form for proxy-mode
// leases. The mirrored credcache entry remains in place because the wire
// form passes through ProtoLease and is independent of the upstream
// credential delivery channel.
func TestClientAssignProtoConvertsToAdapterWire(t *testing.T) {
	h := newClientHarness(t, clientProxyPool("claude-prod", "key-1", "sk-ant-real"))
	defer h.closer()

	wire, err := h.client.AssignProto("claude-prod", "s_1", "spiffe://lenny.test/agent/claude-prod/pod-1", "")
	if err != nil {
		t.Fatalf("AssignProto: %v", err)
	}
	if wire.GetProvider() != string(credential.ProviderAnthropicDirect) {
		t.Errorf("adapter wire provider = %q, want %q", wire.GetProvider(), credential.ProviderAnthropicDirect)
	}
	if wire.GetLeaseId() == "" {
		t.Errorf("adapter wire missing lease id")
	}
	if len(wire.GetPayload()) == 0 {
		t.Errorf("adapter wire missing payload")
	}
}

// spec: 4.3
// diagnosis: Assign for an unregistered pool maps the gRPC NotFound
// status back to credassign.ErrPoolNotFound so call sites that already
// branch on the in-process sentinel keep working after the cutover.
func TestClientAssignUnknownPoolReturnsErrPoolNotFound(t *testing.T) {
	h := newClientHarness(t, clientProxyPool("claude-prod", "key-1", "sk-ant-real"))
	defer h.closer()

	_, err := h.client.Assign("missing", "s_1", "", "")
	if !errors.Is(err, credassign.ErrPoolNotFound) {
		t.Errorf("err = %v, want credassign.ErrPoolNotFound", err)
	}
}

// spec: 4.9
// diagnosis: an exhausted pool maps to credential.ErrPoolExhausted, the
// same sentinel the in-process service uses, so the §4.9 fault-rotation
// fallback path triggers without branching on transport.
func TestClientAssignExhaustedPoolReturnsErrPoolExhausted(t *testing.T) {
	pool := clientProxyPool("claude-prod", "key-1", "sk-ant-real")
	pool.Credentials[0].Healthy = false
	h := newClientHarness(t, pool)
	defer h.closer()

	_, err := h.client.Assign("claude-prod", "s_1", "", "")
	if !errors.Is(err, credential.ErrPoolExhausted) {
		t.Errorf("err = %v, want credential.ErrPoolExhausted", err)
	}
}

// spec: 4.7 / 4.9
// diagnosis: Release issues RevokeCredentials and removes the lease
// from the gateway-local store so a subsequent proxy request cannot
// resolve the bearer token. The server-side lease record is dropped too
// because the Token Service is authoritative.
func TestClientReleaseRemovesLeaseEndToEnd(t *testing.T) {
	h := newClientHarness(t, clientProxyPool("claude-prod", "key-1", "sk-ant-real"))
	defer h.closer()

	lease, err := h.client.Assign("claude-prod", "s_1", "", "")
	if err != nil {
		t.Fatalf("Assign: %v", err)
	}
	if _, ok := h.serverLeases.GetByID(lease.LeaseID); !ok {
		t.Fatalf("lease not on server before Release")
	}

	h.client.Release(lease.LeaseID)

	if _, ok := h.clientLeases.GetByID(lease.LeaseID); ok {
		t.Errorf("lease %q still in client store after Release", lease.LeaseID)
	}
	if _, ok := h.serverLeases.GetByID(lease.LeaseID); ok {
		t.Errorf("lease %q still on server after Release", lease.LeaseID)
	}
}

// spec: 4.7
// diagnosis: ProtoLeaseByID reads the gateway-local lease store so the
// §4.9 renewal worker can convert a tracked lease to the adapter wire
// form without an extra Token Service round-trip.
func TestClientProtoLeaseByIDFromLocalStore(t *testing.T) {
	h := newClientHarness(t, clientProxyPool("claude-prod", "key-1", "sk-ant-real"))
	defer h.closer()

	lease, err := h.client.Assign("claude-prod", "s_1", "", "")
	if err != nil {
		t.Fatalf("Assign: %v", err)
	}
	wire, err := h.client.ProtoLeaseByID(lease.LeaseID)
	if err != nil {
		t.Fatalf("ProtoLeaseByID: %v", err)
	}
	if wire.GetLeaseId() != lease.LeaseID {
		t.Errorf("wire lease id = %q, want %q", wire.GetLeaseId(), lease.LeaseID)
	}
}

// spec: 4.9
// diagnosis: OnAssigned fires the §4.9 Proactive Lease Renewal worker
// hook once per successful Assign, carrying the pool name so the worker
// can re-mint a replacement from the same pool.
func TestClientOnAssignedObserverFires(t *testing.T) {
	h := newClientHarness(t, clientProxyPool("claude-prod", "key-1", "sk-ant-real"))
	defer h.closer()

	var got credassign.LeaseAssignment
	h.client.OnAssigned(func(a credassign.LeaseAssignment) { got = a })

	lease, err := h.client.Assign("claude-prod", "s_1", "", "")
	if err != nil {
		t.Fatalf("Assign: %v", err)
	}
	if got.PoolName != "claude-prod" {
		t.Errorf("observer pool = %q, want claude-prod", got.PoolName)
	}
	if got.Lease.LeaseID != lease.LeaseID {
		t.Errorf("observer lease id = %q, want %q", got.Lease.LeaseID, lease.LeaseID)
	}
}

// spec: 4.3
// diagnosis: PoolForLease returns the pool the renewal worker uses to
// re-mint a replacement after rotation. The binding is recorded on
// Assign and dropped on Release.
func TestClientPoolForLeaseLifecycle(t *testing.T) {
	h := newClientHarness(t, clientProxyPool("claude-prod", "key-1", "sk-ant-real"))
	defer h.closer()

	lease, err := h.client.Assign("claude-prod", "s_1", "", "")
	if err != nil {
		t.Fatalf("Assign: %v", err)
	}
	got, ok := h.client.PoolForLease(lease.LeaseID)
	if !ok || got != "claude-prod" {
		t.Errorf("PoolForLease = (%q, %v), want (claude-prod, true)", got, ok)
	}

	h.client.Release(lease.LeaseID)
	if _, ok := h.client.PoolForLease(lease.LeaseID); ok {
		t.Errorf("PoolForLease still resolves after Release")
	}
}

// clientDirectPool returns a direct-mode pool whose single credential
// carries a full per-provider materializedConfig bundle.
func clientDirectPool(name string, p credential.Provider, mc credential.MaterializedConfig) credassign.Pool {
	return credassign.Pool{
		Name:         name,
		Provider:     p,
		DeliveryMode: credential.DeliveryDirect,
		Strategy:     credential.StrategyLeastLoaded,
		Credentials:  []credassign.PoolCredential{{ID: "key-1", Healthy: true, Materialized: mc}},
	}
}

// spec: §4.9 lines 1246-1298
// diagnosis: a direct-mode lease's materializedConfig survives the full
// gateway↔Token-Service round trip: leaseToProto carries it in
// materialized_config, credentialLeaseFromProto reconstructs lease.Direct,
// and AssignProto renders the per-provider payload the adapter writes.
func TestClientAssignDirectModeReconstructsMaterializedConfig(t *testing.T) {
	mc := credential.MaterializedConfig{
		"accessKeyId": "AKIA", "secretAccessKey": "shh", "sessionToken": "tok",
		"region": "us-east-1", "expiresAt": "2099-01-01T00:00:00Z",
	}
	h := newClientHarness(t, clientDirectPool("bedrock-prod", credential.ProviderAWSBedrock, mc))
	defer h.closer()

	lease, err := h.client.Assign("bedrock-prod", "s_1", "", "")
	if err != nil {
		t.Fatalf("Assign: %v", err)
	}
	if lease.DeliveryMode != credential.DeliveryDirect {
		t.Errorf("delivery = %q, want direct", lease.DeliveryMode)
	}
	if lease.Direct["accessKeyId"] != "AKIA" || lease.Direct["region"] != "us-east-1" {
		t.Errorf("reconstructed Direct = %+v, want the STS bundle", lease.Direct)
	}

	proto, err := h.client.AssignProto("bedrock-prod", "s_2", "", "")
	if err != nil {
		t.Fatalf("AssignProto: %v", err)
	}
	if !strings.Contains(string(proto.GetPayload()), "\"deliveryMode\":\"direct\"") ||
		!strings.Contains(string(proto.GetPayload()), "sessionToken") {
		t.Errorf("adapter payload = %s, want a direct materializedConfig", proto.GetPayload())
	}
}
