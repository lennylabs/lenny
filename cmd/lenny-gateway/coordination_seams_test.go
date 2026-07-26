// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// dialSeamsAdapter opens a live *adapterclient.Client to an in-process adapter
// over bufconn so ConnAlive and the re-adopt publish path exercise a real gRPC
// channel.
func dialSeamsAdapter(t *testing.T) *adapterclient.Client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(adapter.New("seams-test-build"))
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	cl, err := adapterclient.Dial("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial adapter: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close() })
	return cl
}

// recordingEvicter is a streamEvicter that records the sessions whose cached
// Attach stream the dead-connection eviction dropped.
type recordingEvicter struct {
	evicted []string
}

func (r *recordingEvicter) EvictStream(sessionID string) { r.evicted = append(r.evicted, sessionID) }

// Send/Close satisfy executor.Executor so a *recordingEvicter can stand in for
// w.exec; the coordination eviction only exercises EvictStream.
func (r *recordingEvicter) Send(context.Context, string, []executor.Message) (executor.Response, error) {
	return executor.Response{}, nil
}

func (r *recordingEvicter) Close(context.Context, string) error { return nil }

// TestCoordinationBindingsBoundAndConnAlive pins the co-location predicates the
// Sweeper reads: a session this replica binds over a live channel reports bound
// and alive, so the Sweeper renews its lease rather than evicting it.
//
// spec: 4.6.1 (coordinating replica holds the lease), 10.1 (per-session
// coordination lease)
func TestCoordinationBindingsBoundAndConnAlive(t *testing.T) {
	reg := podsession.NewRegistry()
	b := coordinationBindings{w: &gatewayWiring{gatewayWiringFields: gatewayWiringFields{podRegistry: reg}}}

	if b.Bound("s1") {
		t.Fatal("Bound reported true for an unbound session")
	}
	if b.ConnAlive("s1") {
		t.Fatal("ConnAlive reported true for an unbound session")
	}

	reg.Put(&podsession.BindResult{SessionID: "s1", TenantID: "acme", Adapter: dialSeamsAdapter(t)})
	if !b.Bound("s1") {
		t.Fatal("Bound reported false for a session this replica binds")
	}
	if !b.ConnAlive("s1") {
		t.Fatal("ConnAlive reported false for a live held channel; the Sweeper would evict a healthy binding")
	}
}

// TestCoordinationBindingsConnAliveDeadChannel pins the corrective predicate: a
// bound session whose held channel has died reports not-alive, so the Sweeper
// evicts the binding and releases the lease instead of pinning it to a replica
// that can no longer reach the pod. A nil-registry adapter reports not-bound.
//
// spec: 10.1 (hold state on connection loss; TTL-lapse recovery), 4.6.1
// (coordinating replica holds the lease)
func TestCoordinationBindingsConnAliveDeadChannel(t *testing.T) {
	reg := podsession.NewRegistry()
	b := coordinationBindings{w: &gatewayWiring{gatewayWiringFields: gatewayWiringFields{podRegistry: reg}}}

	dead := dialSeamsAdapter(t)
	_ = dead.Close()
	reg.Put(&podsession.BindResult{SessionID: "s1", TenantID: "acme", Adapter: dead})
	if !b.Bound("s1") {
		t.Fatal("Bound reported false for a session with a (dead) binding")
	}
	if b.ConnAlive("s1") {
		t.Fatal("ConnAlive reported true for a shut-down held channel; the dead-connection lease would be renewed forever")
	}

	// A binding with no adapter reports not-alive (fail toward re-adoption).
	reg.Put(&podsession.BindResult{SessionID: "s2", TenantID: "acme"})
	if b.ConnAlive("s2") {
		t.Fatal("ConnAlive reported true for a binding with no adapter")
	}

	// A nil registry (no bindings) reports neither bound nor alive.
	nilB := coordinationBindings{w: &gatewayWiring{}}
	if nilB.Bound("s1") || nilB.ConnAlive("s1") {
		t.Fatal("nil registry reported a binding")
	}
}

// TestCoordinationBindingsEvictBinding pins that a dead-connection eviction
// drops both the registry entry and the executor's cached Attach stream in one
// call. Dropping the cached stream is what lets a same-replica re-adopt serve
// over the freshly published binding rather than the stale dead cached stream.
//
// spec: 4.7 (single content consumer per session / Attach content stream),
// 10.1 (per-session coordination lease)
func TestCoordinationBindingsEvictBinding(t *testing.T) {
	reg := podsession.NewRegistry()
	ev := &recordingEvicter{}
	b := coordinationBindings{w: &gatewayWiring{gatewayWiringFields: gatewayWiringFields{podRegistry: reg, exec: ev}}}

	reg.Put(&podsession.BindResult{SessionID: "s1", TenantID: "acme", Adapter: dialSeamsAdapter(t)})
	b.EvictBinding("s1")

	if _, ok := reg.Get("s1"); ok {
		t.Fatal("EvictBinding left the registry entry in place")
	}
	if len(ev.evicted) != 1 || ev.evicted[0] != "s1" {
		t.Fatalf("EvictBinding did not drop the executor's cached stream: evicted=%v", ev.evicted)
	}
}

// fakeReadoptDialer resolves a fixed pod and returns a live adapter connection.
type fakeReadoptDialer struct {
	podIP        string
	adapter      *adapterclient.Client
	err          error
	gotSandbox   string
	dialAttempts int
}

func (d *fakeReadoptDialer) ReadoptConnect(_ context.Context, sandboxName string) (*lennyv1.Sandbox, *adapterclient.Client, error) {
	d.dialAttempts++
	d.gotSandbox = sandboxName
	if d.err != nil {
		return nil, nil, d.err
	}
	return &lennyv1.Sandbox{Status: lennyv1.SandboxStatus{PodIP: d.podIP}}, d.adapter, nil
}

// fakeReadoptFencer records the fence call and returns a scripted outcome.
type fakeReadoptFencer struct {
	relinquished bool
	err          error
	gotSession   string
}

func (f *fakeReadoptFencer) Fence(_ context.Context, _ *adapterclient.Client, _, sessionID string) (bool, error) {
	f.gotSession = sessionID
	return f.relinquished, f.err
}

type fakeSandboxReader struct {
	row sessionstore.Session
	err error
}

func (r fakeSandboxReader) Get(_ context.Context, _, _ string) (sessionstore.Session, error) {
	return r.row, r.err
}

type recordingPublisher struct{ published []*podsession.BindResult }

func (p *recordingPublisher) Put(b *podsession.BindResult) { p.published = append(p.published, b) }

// releaseCall records one coordination-lease release the re-adopt issued.
type releaseCall struct{ tenantID, sessionID, holder string }

// recordingReleaser records the lease releases the re-adopt issues so a test
// can assert a not-relinquished fence failure releases the still-held lease.
type recordingReleaser struct {
	released []releaseCall
	err      error
}

func (r *recordingReleaser) Release(_ context.Context, tenantID, sessionID, holder string) error {
	r.released = append(r.released, releaseCall{tenantID, sessionID, holder})
	return r.err
}

// TestReadoptAndFencePublishesOnFenceAck pins the crash-takeover happy path:
// the re-adopt dials the still-running pod from its persisted SandboxName,
// fences it, and returns a publish callback that places the re-established
// serving binding into the registry only after the fence acknowledges.
//
// spec: 10.1 (coordinator handoff re-adopts the still-running pod;
// CoordinatorFence is the first RPC; no operational RPC before the fence
// acknowledges), 4.6.1 (coordinating replica holds the lease)
func TestReadoptAndFencePublishesOnFenceAck(t *testing.T) {
	adapterConn := dialSeamsAdapter(t)
	dialer := &fakeReadoptDialer{podIP: "10.0.0.7", adapter: adapterConn}
	fencer := &fakeReadoptFencer{}
	pub := &recordingPublisher{}
	rel := &recordingReleaser{}
	sessions := fakeSandboxReader{row: sessionstore.Session{ID: "s1", TenantID: "acme", PodAssignment: "sbx-1"}}

	publish, err := readoptAndFence(context.Background(), dialer, fencer, pub, sessions, rel, "acme", "s1", "replica-A")
	if err != nil {
		t.Fatalf("readoptAndFence returned error on fence ack: %v", err)
	}
	if len(rel.released) != 0 {
		t.Fatalf("readoptAndFence released the lease on a successful fence: releases=%v", rel.released)
	}
	if dialer.gotSandbox != "sbx-1" {
		t.Fatalf("dialed sandbox %q, want the persisted PodAssignment sbx-1", dialer.gotSandbox)
	}
	if len(pub.published) != 0 {
		t.Fatal("binding published before the Sweeper invoked the publish callback (violates the no-RPC-before-fence precondition)")
	}
	publish()
	if len(pub.published) != 1 {
		t.Fatalf("publish callback did not publish the binding: %d bindings", len(pub.published))
	}
	got := pub.published[0]
	if got.SessionID != "s1" || got.TenantID != "acme" || got.SandboxName != "sbx-1" || got.PodIP != "10.0.0.7" || got.Adapter != adapterConn {
		t.Fatalf("published binding = %+v, want the re-established serving binding for s1", got)
	}
}

// TestReadoptAndFenceClosesConnAndReturnsErrorOnFenceFailure pins the corrective
// failure path: when the fence relinquishes (terminal failure), the re-adopt
// closes the dialed connection, publishes no binding, and returns the error, so
// the Sweeper records an adoption backoff rather than leaving a serving binding
// for a session whose fence never landed.
//
// spec: 10.1 (fence retry budget and relinquish-and-backoff; no binding on a
// terminal fence failure), 4.6.1 (coordinating replica holds the lease)
func TestReadoptAndFenceClosesConnAndReturnsErrorOnFenceFailure(t *testing.T) {
	adapterConn := dialSeamsAdapter(t)
	dialer := &fakeReadoptDialer{podIP: "10.0.0.7", adapter: adapterConn}
	fencer := &fakeReadoptFencer{relinquished: true, err: errors.New("relinquished")}
	pub := &recordingPublisher{}
	rel := &recordingReleaser{}
	sessions := fakeSandboxReader{row: sessionstore.Session{ID: "s1", TenantID: "acme", PodAssignment: "sbx-1"}}

	publish, err := readoptAndFence(context.Background(), dialer, fencer, pub, sessions, rel, "acme", "s1", "replica-A")
	if err == nil {
		t.Fatal("readoptAndFence returned nil error on a terminal fence failure")
	}
	if publish != nil {
		t.Fatal("readoptAndFence returned a publish callback on a fence failure")
	}
	if len(pub.published) != 0 {
		t.Fatal("a binding was published despite the fence failure")
	}
	if adapterConn.Alive() {
		t.Fatal("the dialed connection was not closed after the fence failure")
	}
	// The Fencer relinquished (released the lease itself), so the re-adopt must
	// not release it a second time.
	if len(rel.released) != 0 {
		t.Fatalf("re-adopt released the lease a relinquished fence already released: releases=%v", rel.released)
	}
}

// TestReadoptAndFenceReleasesLeaseOnNonRelinquishFenceFailure pins the
// corrective recovery path for a best-effort fence failure the Fencer did not
// relinquish (a coordination_generation read error or a context cancellation
// leaves the lease held). The re-adopt must release the still-held lease itself
// so its lapse surfaces for a subsequent sweep to re-adopt the still-running
// pod. Without the release the lease pins to a replica that never established
// the serving binding, the next sweep renews it forever, and the
// fenced-by-nothing pod self-terminates in its hold state at 120s. Pre-fix code
// discarded the relinquished flag and treated this failure as an
// already-relinquished terminal failure, so this test fails against it.
//
// spec: 10.1 (relinquish-and-backoff; hold state on connection loss), 4.6.1
// (coordinating replica holds the lease)
func TestReadoptAndFenceReleasesLeaseOnNonRelinquishFenceFailure(t *testing.T) {
	adapterConn := dialSeamsAdapter(t)
	dialer := &fakeReadoptDialer{podIP: "10.0.0.7", adapter: adapterConn}
	fencer := &fakeReadoptFencer{relinquished: false, err: errors.New("read coordination_generation")}
	pub := &recordingPublisher{}
	rel := &recordingReleaser{}
	sessions := fakeSandboxReader{row: sessionstore.Session{ID: "s1", TenantID: "acme", PodAssignment: "sbx-1"}}

	publish, err := readoptAndFence(context.Background(), dialer, fencer, pub, sessions, rel, "acme", "s1", "replica-A")
	if err == nil {
		t.Fatal("readoptAndFence returned nil error on a non-relinquish fence failure")
	}
	if publish != nil {
		t.Fatal("readoptAndFence returned a publish callback on a fence failure")
	}
	if len(pub.published) != 0 {
		t.Fatal("a binding was published despite the fence failure")
	}
	if adapterConn.Alive() {
		t.Fatal("the dialed connection was not closed after the fence failure")
	}
	if len(rel.released) != 1 {
		t.Fatalf("the still-held lease was not released after a non-relinquish fence failure: releases=%d; the lease would pin to a replica that never bound and the pod would self-terminate in hold state", len(rel.released))
	}
	if got := rel.released[0]; got.tenantID != "acme" || got.sessionID != "s1" || got.holder != "replica-A" {
		t.Fatalf("released lease = %+v, want acme/s1 held by replica-A", got)
	}
}

// TestReadoptAndFenceSurfacesReleaseFailureOnNonRelinquishFence pins that when
// the still-held lease cannot be released after a best-effort fence failure the
// re-adopt surfaces the release fault to the Sweeper rather than swallowing it,
// so the fail-closed lapse is not lost silently.
//
// spec: 10.1 (relinquish-and-backoff), 4.6.1 (coordinating replica holds the
// lease)
func TestReadoptAndFenceSurfacesReleaseFailureOnNonRelinquishFence(t *testing.T) {
	adapterConn := dialSeamsAdapter(t)
	dialer := &fakeReadoptDialer{podIP: "10.0.0.7", adapter: adapterConn}
	fencer := &fakeReadoptFencer{relinquished: false, err: errors.New("read coordination_generation")}
	pub := &recordingPublisher{}
	rel := &recordingReleaser{err: errors.New("redis unavailable")}
	sessions := fakeSandboxReader{row: sessionstore.Session{ID: "s1", TenantID: "acme", PodAssignment: "sbx-1"}}

	publish, err := readoptAndFence(context.Background(), dialer, fencer, pub, sessions, rel, "acme", "s1", "replica-A")
	if err == nil {
		t.Fatal("readoptAndFence returned nil error when the lease release failed")
	}
	if publish != nil {
		t.Fatal("readoptAndFence returned a publish callback on a fence failure")
	}
	if len(rel.released) != 1 {
		t.Fatalf("the re-adopt did not attempt the lease release: releases=%d", len(rel.released))
	}
}

// TestReadoptAndFenceReleasesLeaseOnDialFailure pins the corrective recovery
// path for a re-adopt whose pod dial fails before the fence. The dial fault
// leaves the lease the Sweeper acquired held by this replica; the re-adopt must
// release it itself so its lapse surfaces for a subsequent sweep to re-adopt
// the still-running pod. Without the release the lease pins to a replica that
// never fenced the pod, the next sweep renews it forever (the takeover
// predicate never fires again), and the fenced-by-nothing pod self-terminates
// in its hold state at 120s. Pre-fix code returned the dial error without
// releasing the lease, so this test fails against it.
//
// spec: 10.1 (relinquish-and-backoff; hold state on connection loss), 4.6.1
// (coordinating replica holds the lease)
func TestReadoptAndFenceReleasesLeaseOnDialFailure(t *testing.T) {
	dialer := &fakeReadoptDialer{err: errors.New("reconnect dial failed")}
	fencer := &fakeReadoptFencer{}
	pub := &recordingPublisher{}
	rel := &recordingReleaser{}
	sessions := fakeSandboxReader{row: sessionstore.Session{ID: "s1", TenantID: "acme", PodAssignment: "sbx-1"}}

	publish, err := readoptAndFence(context.Background(), dialer, fencer, pub, sessions, rel, "acme", "s1", "replica-A")
	if err == nil {
		t.Fatal("readoptAndFence returned nil error on a dial failure")
	}
	if publish != nil {
		t.Fatal("readoptAndFence returned a publish callback on a dial failure")
	}
	if fencer.gotSession != "" {
		t.Fatal("the pod was fenced despite the dial failure (fence must not run without a connection)")
	}
	if len(pub.published) != 0 {
		t.Fatal("a binding was published despite the dial failure")
	}
	if len(rel.released) != 1 {
		t.Fatalf("the still-held lease was not released after a dial failure: releases=%d; the lease would pin to a replica that never bound and the pod would self-terminate in hold state", len(rel.released))
	}
	if got := rel.released[0]; got.tenantID != "acme" || got.sessionID != "s1" || got.holder != "replica-A" {
		t.Fatalf("released lease = %+v, want acme/s1 held by replica-A", got)
	}
}

// TestReadoptAndFenceReleasesLeaseOnSessionReadFailure pins the corrective
// recovery path for a re-adopt whose session-row read fails before the dial and
// the fence. Like the dial failure, this pre-fence fault leaves the lease held,
// so the re-adopt must release it so its lapse surfaces for a subsequent sweep.
// Pre-fix code returned the read error without releasing the lease, so this
// test fails against it.
//
// spec: 10.1 (relinquish-and-backoff; hold state on connection loss), 4.6.1
// (coordinating replica holds the lease)
func TestReadoptAndFenceReleasesLeaseOnSessionReadFailure(t *testing.T) {
	dialer := &fakeReadoptDialer{}
	fencer := &fakeReadoptFencer{}
	pub := &recordingPublisher{}
	rel := &recordingReleaser{}
	sessions := fakeSandboxReader{err: errors.New("session row read failed")}

	publish, err := readoptAndFence(context.Background(), dialer, fencer, pub, sessions, rel, "acme", "s1", "replica-A")
	if err == nil {
		t.Fatal("readoptAndFence returned nil error on a session-row read failure")
	}
	if publish != nil {
		t.Fatal("readoptAndFence returned a publish callback on a session-row read failure")
	}
	if dialer.dialAttempts != 0 {
		t.Fatal("the pod was dialed despite the session-row read failure")
	}
	if len(rel.released) != 1 {
		t.Fatalf("the still-held lease was not released after a session-row read failure: releases=%d", len(rel.released))
	}
	if got := rel.released[0]; got.tenantID != "acme" || got.sessionID != "s1" || got.holder != "replica-A" {
		t.Fatalf("released lease = %+v, want acme/s1 held by replica-A", got)
	}
}

// TestReadoptAndFenceReturnsErrorWhenSeamsUnwired pins the fail-closed guard:
// with the re-adopt collaborators absent the Sweeper is told the re-adopt could
// not run (so it publishes no binding it cannot back) rather than silently
// succeeding.
//
// spec: 10.1 (coordinator handoff re-adopts the still-running pod), 4.6.1
// (coordinating replica holds the lease)
func TestReadoptAndFenceReturnsErrorWhenSeamsUnwired(t *testing.T) {
	r := coordinationReadopter{w: &gatewayWiring{}}
	publish, err := r.ReadoptAndFence(context.Background(), "acme", "s1", 3)
	if err == nil {
		t.Fatal("ReadoptAndFence returned nil error with no re-adopt seams wired")
	}
	if publish != nil {
		t.Fatal("ReadoptAndFence returned a publish callback with no seams wired")
	}
}
