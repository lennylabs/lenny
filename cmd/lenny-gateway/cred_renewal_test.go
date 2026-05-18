// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/credrenewal"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// spec: §4.9 Proactive Lease Renewal — the renewal worker issues a
// replacement lease before the original expires and pushes the rotated
// credential to the lease's pod via the §4.7 RotateCredentials RPC.

// rotateRecorder is a minimal Adapter gRPC server that captures the
// RotateCredentials request the renewal push sends.
type rotateRecorder struct {
	adapterv1.UnimplementedAdapterServer
	mu        sync.Mutex
	gotRotate *adapterv1.RotateCredentialsRequest
}

func (r *rotateRecorder) RotateCredentials(_ context.Context, req *adapterv1.RotateCredentialsRequest) (*adapterv1.RotateCredentialsResponse, error) {
	r.mu.Lock()
	r.gotRotate = req
	r.mu.Unlock()
	return &adapterv1.RotateCredentialsResponse{}, nil
}

func (r *rotateRecorder) last() *adapterv1.RotateCredentialsRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.gotRotate
}

// dialRecorder serves rec over an in-memory connection and returns an
// adapter client wired to it.
func dialRecorder(t *testing.T, rec *rotateRecorder) *adapterclient.Client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	adapterv1.RegisterAdapterServer(gs, rec)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	cl, err := adapterclient.Dial("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial recorder: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close() })
	return cl
}

// renewalProxyPool returns a proxy-mode §4.9 pool with one healthy
// credential, the assignment service input the renewal worker re-mints
// from.
func renewalProxyPool(name, credID string) credassign.Pool {
	return credassign.Pool{
		Name:         name,
		Provider:     credential.ProviderAnthropicDirect,
		DeliveryMode: credential.DeliveryProxy,
		Strategy:     credential.StrategyLeastLoaded,
		ProxyURL:     "https://gateway-internal:8443/llm-proxy",
		ProxyDialect: "anthropic",
		Credentials:  []credassign.PoolCredential{{ID: credID, APIKey: "sk-ant-real", Healthy: true}},
	}
}

// TestRenewalWorkerPushesRotationToPod is the renewal-worker-to-pod
// rotation push: a tracked lease whose renewBefore deadline has passed
// is renewed by the worker, and the replacement credential is pushed to
// the session's pod via the §4.7 RotateCredentials RPC.
func TestRenewalWorkerPushesRotationToPod(t *testing.T) {
	assign := credassign.New(credleasestore.New(), credcache.New())
	assign.RegisterPool(renewalProxyPool("claude-prod", "key-1"))

	// The session's pod is bound on this replica with a live adapter
	// connection to the rotation recorder.
	rec := &rotateRecorder{}
	registry := podsession.NewRegistry()
	registry.Put(&podsession.BindResult{SessionID: "run_a", Adapter: dialRecorder(t, rec)})

	wiring := newCredRenewalWiring(assign, registry)
	if wiring == nil {
		t.Fatal("newCredRenewalWiring returned nil for a wired credential service")
	}
	worker := credrenewal.New(wiring, credrenewal.Options{
		OnRenewed:   wiring.onRenewed,
		OnExhausted: wiring.onExhausted,
	})
	assign.OnAssigned(func(a credassign.LeaseAssignment) {
		wiring.track(worker, a.PoolName, string(a.Lease.Provider), a.Lease)
	})

	// Mint and assign the session's first lease. OnAssigned tracks it
	// for renewal.
	lease, err := assign.Assign("claude-prod", "run_a", "")
	if err != nil {
		t.Fatalf("Assign: %v", err)
	}
	if worker.Tracked() != 1 {
		t.Fatalf("renewal worker tracks %d leases after Assign, want 1", worker.Tracked())
	}

	// Sweep after the lease's renewBefore deadline. The worker renews
	// the lease and onRenewed pushes the replacement to the pod.
	renewed := worker.Tick(context.Background(), lease.RenewBefore.Add(time.Second))
	if renewed != 1 {
		t.Fatalf("renewal sweep renewed %d leases, want 1", renewed)
	}

	got := rec.last()
	if got == nil {
		t.Fatal("the pod's adapter received no RotateCredentials push from the renewal worker")
	}
	if got.GetSessionId().GetValue() != "run_a" {
		t.Errorf("RotateCredentials session id = %q, want run_a", got.GetSessionId().GetValue())
	}
	entry, ok := got.GetLeases()["anthropic_direct"]
	if !ok {
		t.Fatalf("RotateCredentials leases = %v, want an anthropic_direct entry", got.GetLeases())
	}
	// The push carries the replacement lease, not the original.
	if entry.GetLeaseId() == lease.LeaseID {
		t.Errorf("RotateCredentials pushed the original lease %s, want the renewed replacement", lease.LeaseID)
	}
	if entry.GetLeaseId() == "" {
		t.Error("the rotated lease carries no lease id")
	}
}

// TestRenewalWorkerSurvivesNilRegistry confirms a renewal that has no
// pod binding on this replica still renews the lease without panicking:
// the worker tracks the fresh lease, and there is simply no local pod
// to push the rotation to.
func TestRenewalWorkerSurvivesNoPodBinding(t *testing.T) {
	assign := credassign.New(credleasestore.New(), credcache.New())
	assign.RegisterPool(renewalProxyPool("claude-prod", "key-1"))

	// An empty registry: the session's pod is bound on another replica.
	wiring := newCredRenewalWiring(assign, podsession.NewRegistry())
	worker := credrenewal.New(wiring, credrenewal.Options{OnRenewed: wiring.onRenewed})
	assign.OnAssigned(func(a credassign.LeaseAssignment) {
		wiring.track(worker, a.PoolName, string(a.Lease.Provider), a.Lease)
	})

	lease, err := assign.Assign("claude-prod", "run_a", "")
	if err != nil {
		t.Fatalf("Assign: %v", err)
	}
	if renewed := worker.Tick(context.Background(), lease.RenewBefore.Add(time.Second)); renewed != 1 {
		t.Fatalf("renewal sweep renewed %d leases, want 1 even with no local pod", renewed)
	}
}

// TestNewCredRenewalWiringNilWithoutService confirms the wiring is nil
// when the gateway has no credential-assignment service, so a minimal
// gateway runs no renewal worker.
func TestNewCredRenewalWiringNilWithoutService(t *testing.T) {
	if w := newCredRenewalWiring(nil, podsession.NewRegistry()); w != nil {
		t.Errorf("newCredRenewalWiring(nil, ...) = %v, want nil", w)
	}
}

// TestRenewWithoutPoolBindingFails confirms the renewer rejects a lease
// it never recorded a pool binding for: without the originating pool it
// cannot re-mint, and the worker falls through to fault rotation.
func TestRenewWithoutPoolBindingFails(t *testing.T) {
	assign := credassign.New(credleasestore.New(), credcache.New())
	wiring := newCredRenewalWiring(assign, podsession.NewRegistry())

	_, err := wiring.Renew(context.Background(), credrenewal.Lease{LeaseID: "untracked"})
	if err == nil {
		t.Error("Renew of an untracked lease succeeded, want a no-pool-binding error")
	}
}

// TestRenewalWiringNilReceiverHooksAreNoops confirms the renewal hooks
// are safe on a nil wiring, the degraded mode a gateway with no
// credential pools runs in.
func TestRenewalWiringNilReceiverHooksAreNoops(t *testing.T) {
	var w *credRenewalWiring
	w.track(nil, "pool", "provider", credential.Lease{})
	w.onRenewed(credrenewal.Lease{})
	w.onExhausted(credrenewal.Lease{})
}
