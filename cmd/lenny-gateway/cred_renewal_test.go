// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/events"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credleasestore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credrenewal"
	"github.com/lennylabs/lenny/pkg/gateway/eventbuffer"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
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

	wiring := newCredRenewalWiring(assign, registry, nil, nil, nil)
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
	lease, err := assign.Assign("claude-prod", "run_a", "", "")
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
	wiring := newCredRenewalWiring(assign, podsession.NewRegistry(), nil, nil, nil)
	worker := credrenewal.New(wiring, credrenewal.Options{OnRenewed: wiring.onRenewed})
	assign.OnAssigned(func(a credassign.LeaseAssignment) {
		wiring.track(worker, a.PoolName, string(a.Lease.Provider), a.Lease)
	})

	lease, err := assign.Assign("claude-prod", "run_a", "", "")
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
	if w := newCredRenewalWiring(nil, podsession.NewRegistry(), nil, nil, nil); w != nil {
		t.Errorf("newCredRenewalWiring(nil, ...) = %v, want nil", w)
	}
}

// TestRenewWithoutPoolBindingFails confirms the renewer rejects a lease
// it never recorded a pool binding for: without the originating pool it
// cannot re-mint, and the worker falls through to fault rotation.
func TestRenewWithoutPoolBindingFails(t *testing.T) {
	assign := credassign.New(credleasestore.New(), credcache.New())
	wiring := newCredRenewalWiring(assign, podsession.NewRegistry(), nil, nil, nil)

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
	if err := w.onExtend(credrenewal.Lease{}, time.Time{}); err != nil {
		t.Errorf("onExtend on a nil wiring returned %v, want nil", err)
	}
	w.onExtensionCapReached(credrenewal.Lease{})
}

// spec §4.0, §16.6: credential pool manager emits credential_rotated on
// lease rotation and credential_pool_exhausted on pool exhaustion.
func TestRenewalEmitsCredentialRotatedOnSuccess(t *testing.T) {
	buf := eventbuffer.NewEventBuffer(0)
	em := eventbuffer.NewEmitter(buf, "test")

	assign := credassign.New(credleasestore.New(), credcache.New())
	assign.RegisterPool(renewalProxyPool("claude-prod", "key-1"))
	wiring := newCredRenewalWiring(assign, podsession.NewRegistry(), em, nil, nil)
	worker := credrenewal.New(wiring, credrenewal.Options{OnRenewed: wiring.onRenewed})
	assign.OnAssigned(func(a credassign.LeaseAssignment) {
		wiring.track(worker, a.PoolName, string(a.Lease.Provider), a.Lease)
	})

	lease, err := assign.Assign("claude-prod", "run_a", "", "")
	if err != nil {
		t.Fatalf("Assign: %v", err)
	}
	if renewed := worker.Tick(context.Background(), lease.RenewBefore.Add(time.Second)); renewed != 1 {
		t.Fatalf("renewal sweep renewed %d leases, want 1", renewed)
	}

	page := buf.Query(0, events.EventFilter{EventType: "credential_rotated"}, 100)
	if len(page.Events) != 1 {
		t.Fatalf("emitted %d credential_rotated events, want 1", len(page.Events))
	}
	ev := page.Events[0].Event
	if ev.Type != "dev.lenny.credential_rotated" {
		t.Errorf("event type = %q, want dev.lenny.credential_rotated", ev.Type)
	}
	if ev.Severity != "info" {
		t.Errorf("event severity = %q, want info", ev.Severity)
	}
	var data struct {
		Pool         string `json:"pool"`
		CredentialID string `json:"credentialId"`
		SessionID    string `json:"sessionId"`
		LeaseID      string `json:"leaseId"`
		Reason       string `json:"reason"`
	}
	if err := json.Unmarshal(ev.Data, &data); err != nil {
		t.Fatalf("event data: %v", err)
	}
	if data.Pool != "claude-prod" {
		t.Errorf("pool = %q, want claude-prod", data.Pool)
	}
	if data.SessionID != "run_a" {
		t.Errorf("sessionId = %q, want run_a", data.SessionID)
	}
	if data.Reason != string(credential.TriggerProactiveRenewal) {
		t.Errorf("reason = %q, want proactive_renewal", data.Reason)
	}
	if data.LeaseID == "" {
		t.Error("event leaseId is empty")
	}
}

// spec §4.0, §16.6: an exhausted lease — the §4.9 fall-through —
// emits credential_pool_exhausted with the lease's pool binding.
func TestRenewalEmitsCredentialPoolExhaustedOnExhaustion(t *testing.T) {
	buf := eventbuffer.NewEventBuffer(0)
	em := eventbuffer.NewEmitter(buf, "test")

	assign := credassign.New(credleasestore.New(), credcache.New())
	assign.RegisterPool(renewalProxyPool("claude-prod", "key-1"))
	wiring := newCredRenewalWiring(assign, podsession.NewRegistry(), em, nil, nil)
	worker := credrenewal.New(wiring, credrenewal.Options{OnExhausted: wiring.onExhausted})
	assign.OnAssigned(func(a credassign.LeaseAssignment) {
		wiring.track(worker, a.PoolName, string(a.Lease.Provider), a.Lease)
	})

	lease, err := assign.Assign("claude-prod", "run_a", "", "")
	if err != nil {
		t.Fatalf("Assign: %v", err)
	}
	// Sweep after the lease has already expired — the worker drops it
	// and onExhausted fires with the pool binding.
	worker.Tick(context.Background(), lease.ExpiresAt.Add(time.Second))

	page := buf.Query(0, events.EventFilter{EventType: "credential_pool_exhausted"}, 100)
	if len(page.Events) != 1 {
		t.Fatalf("emitted %d credential_pool_exhausted events, want 1", len(page.Events))
	}
	ev := page.Events[0].Event
	if ev.Severity != "warning" {
		t.Errorf("event severity = %q, want warning", ev.Severity)
	}
	var data struct {
		Pool      string `json:"pool"`
		SessionID string `json:"sessionId"`
		LeaseID   string `json:"leaseId"`
	}
	if err := json.Unmarshal(ev.Data, &data); err != nil {
		t.Fatalf("event data: %v", err)
	}
	if data.Pool != "claude-prod" {
		t.Errorf("pool = %q, want claude-prod", data.Pool)
	}
	if data.LeaseID != lease.LeaseID {
		t.Errorf("leaseId = %q, want %q", data.LeaseID, lease.LeaseID)
	}
}

// spec §4.0: a renewal wired without an emitter is a no-op for the
// event side; the rotation lifecycle continues normally.
func TestRenewalNoEmitterIsSilent(t *testing.T) {
	assign := credassign.New(credleasestore.New(), credcache.New())
	assign.RegisterPool(renewalProxyPool("claude-prod", "key-1"))
	wiring := newCredRenewalWiring(assign, podsession.NewRegistry(), nil, nil, nil)
	worker := credrenewal.New(wiring, credrenewal.Options{
		OnRenewed:   wiring.onRenewed,
		OnExhausted: wiring.onExhausted,
	})
	assign.OnAssigned(func(a credassign.LeaseAssignment) {
		wiring.track(worker, a.PoolName, string(a.Lease.Provider), a.Lease)
	})

	lease, err := assign.Assign("claude-prod", "run_a", "", "")
	if err != nil {
		t.Fatalf("Assign: %v", err)
	}
	if renewed := worker.Tick(context.Background(), lease.RenewBefore.Add(time.Second)); renewed != 1 {
		t.Fatalf("renewal sweep renewed %d leases, want 1", renewed)
	}
	// No panic; the rotation completed without an emitter.
}
