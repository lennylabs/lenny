// SPDX-License-Identifier: MIT

// Package coordfixture provides a real in-process §4.7 adapter that models a
// still-running pod, plus a coordination.Readopter that drives the genuine
// §10.1 CoordinatorFence over it and a concurrency-safe
// coordination.BindingRegistry. A two-replica coordination test wires the
// survivor as a real Sweeper and models the crashed coordinator directly
// through its lease and binding, both over a shared Redis lease store and a
// shared session store, and uses this fixture so the cross-replica coordinator
// handoff exercises the real generation fence — a stale coordinator's
// session-mutating RPC is rejected by the pod rather than by an in-memory stub
// — which is what distinguishes the integration and chaos coverage from the
// tier-1/tier-2 fakes.
//
// spec: §10.1 (coordinator handoff; CoordinatorFence generation fence; no
// operational RPC before the fence acknowledges), §4.6.1 (coordinating
// replica holds the lease), §4.7 (single content consumer / Attach content
// stream).
package coordfixture

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/gateway/coordination/coordination"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
)

// Tenants is a coordination.TenantLister over a fixed set of tenant ids.
type Tenants []string

// ListTenants returns the fixed tenant set.
func (t Tenants) ListTenants(context.Context) ([]string, error) { return []string(t), nil }

// fakeRuntime is the minimal RuntimeProcess the adapter's StartSession needs.
// It runs no real process; the fixture exercises the coordinator generation
// fence, not the runtime.
type fakeRuntime struct{}

func (fakeRuntime) Start(context.Context, string) error           { return nil }
func (fakeRuntime) WriteEnvelope(string, []byte) error            { return nil }
func (fakeRuntime) Interrupt(context.Context, string, bool) error { return nil }
func (fakeRuntime) Close(context.Context, string) error           { return nil }
func (fakeRuntime) Output(context.Context, string) (<-chan []byte, error) {
	ch := make(chan []byte)
	close(ch)
	return ch, nil
}

// Pod is a real in-process adapter modeling a still-running pod, with a dialed
// client the harness uses to drive the §10.1 CoordinatorFence and to probe the
// generation fence with a session-mutating RPC.
type Pod struct {
	Server    *adapter.Server
	Client    *adapterclient.Client
	SessionID string
}

// StartPod boots an in-process adapter over a bufconn, starts the named
// session on it, and returns the pod with a dialed client. The pod is not yet
// fenced; the first CoordinatorFence a coordinator drives records the pod's
// initial generation.
func StartPod(t testing.TB, sessionID string) *Pod {
	t.Helper()
	srv := adapter.New("coordfixture")
	srv.WorkspaceRoot = t.TempDir()
	srv.ManifestDir = t.TempDir()
	srv.Runtime = fakeRuntime{}

	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	cl, err := adapterclient.Dial("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("coordfixture: dial adapter: %v", err)
	}
	t.Cleanup(func() { _ = cl.Close() })

	if err := cl.StartSession(context.Background(), adapterclient.StartSessionParams{
		SessionID: sessionID, Runtime: "claude-code",
	}); err != nil {
		t.Fatalf("coordfixture: StartSession: %v", err)
	}
	return &Pod{Server: srv, Client: cl, SessionID: sessionID}
}

// Fence drives a real CoordinatorFence to gen and reports whether the pod
// accepted it. The first fence on a pod is always accepted; a later fence
// whose generation is not strictly greater is rejected with FailedPrecondition.
func (p *Pod) Fence(ctx context.Context, gen int64) (bool, error) {
	res, err := p.Client.CoordinatorFence(ctx, p.SessionID, gen)
	return res.Accepted, err
}

// LastFenced returns the generation the pod is currently fenced to.
func (p *Pod) LastFenced() int64 { return p.Server.LastFencedGeneration() }

// StaleRPCRejected reports whether a session-mutating RPC (CheckpointBarrier)
// carrying gen is rejected by the pod's §10.1 generation fence. It is the
// split-brain probe: after a handoff advances the coordination generation, the
// previous coordinator's RPC at the pre-handoff generation must be rejected.
// spec: §10.1 (generation fence; a stale coordinator's RPC is rejected).
func (p *Pod) StaleRPCRejected(ctx context.Context, gen int64) bool {
	_, err := p.Client.CheckpointBarrier(ctx, p.SessionID, gen, "coordfixture-split-brain-probe")
	return status.Code(err) == codes.FailedPrecondition
}

// Bindings is a concurrency-safe coordination.BindingRegistry standing in for a
// replica's per-replica podsession registry. A takeover publish flips the
// session bound (modeling the production podRegistry.Put that holds the
// re-established connection open); KillConn models a dead held gateway-to-pod
// channel so a test can exercise the dead-connection eviction path.
type Bindings struct {
	mu      sync.Mutex
	bound   map[string]bool
	dead    map[string]bool
	evicted map[string]int
}

// NewBindings returns an empty binding registry.
func NewBindings() *Bindings {
	return &Bindings{bound: map[string]bool{}, dead: map[string]bool{}, evicted: map[string]int{}}
}

// Bound reports whether this replica holds a live binding for the session.
func (b *Bindings) Bound(id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.bound[id]
}

// ConnAlive reports whether the bound session's held channel is live. A
// KillConn'd session reports dead so the Sweeper surfaces the lease for
// re-adoption.
func (b *Bindings) ConnAlive(id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return !b.dead[id]
}

// EvictBinding drops the session's binding and (in production) the executor's
// cached Attach stream, recording the eviction for assertions.
func (b *Bindings) EvictBinding(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.bound, id)
	delete(b.dead, id)
	b.evicted[id]++
}

// Publish marks the session bound with a live channel, modeling the
// post-fence podRegistry.Put.
func (b *Bindings) Publish(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.bound[id] = true
	b.dead[id] = false
}

// KillConn marks the held channel of a bound session dead.
func (b *Bindings) KillConn(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.dead[id] = true
}

// Evicted reports how many times the session was evicted.
func (b *Bindings) Evicted(id string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.evicted[id]
}

// FenceReadopter is a coordination.Readopter that drives a genuine
// CoordinatorFence on the pod and, on acknowledgement, publishes the binding on
// the replica's registry so the pod stays continuously coordinated. A session
// listed in Fail models the coordfence terminal relinquish: it releases the
// real lease and returns an error, so the Sweeper records an adoption backoff
// and publishes no binding.
//
// spec: §10.1 (coordinator handoff re-adopts the still-running pod;
// CoordinatorFence is the first RPC; no operational RPC before the fence
// acknowledges; relinquish-and-backoff), §4.7.
type FenceReadopter struct {
	Pod       *Pod
	Bindings  *Bindings
	Leases    leasestore.LeaseStore
	ReplicaID string
	TenantID  string
	Fail      map[string]bool

	mu       sync.Mutex
	calls    int
	gens     []int64
	sessions []string
}

// ReadoptAndFence fences the pod to the post-handoff generation and returns a
// publish callback the Sweeper invokes only after the fence acknowledges. On a
// Fail session it relinquishes the lease and returns an error.
func (r *FenceReadopter) ReadoptAndFence(ctx context.Context, tenantID, sessionID string, generation int64) (func(), error) {
	r.mu.Lock()
	r.calls++
	r.gens = append(r.gens, generation)
	r.sessions = append(r.sessions, sessionID)
	r.mu.Unlock()

	if r.Fail[sessionID] {
		_ = r.Leases.Release(ctx, tenantID, sessionID, r.ReplicaID)
		return nil, fmt.Errorf("coordfixture: fence relinquished for session %s", sessionID)
	}
	accepted, err := r.Pod.Fence(ctx, generation)
	if err != nil {
		_ = r.Leases.Release(ctx, tenantID, sessionID, r.ReplicaID)
		return nil, fmt.Errorf("coordfixture: fence session %s to generation %d: %w", sessionID, generation, err)
	}
	if !accepted {
		_ = r.Leases.Release(ctx, tenantID, sessionID, r.ReplicaID)
		return nil, fmt.Errorf("coordfixture: pod rejected fence for session %s at generation %d", sessionID, generation)
	}
	return func() { r.Bindings.Publish(sessionID) }, nil
}

// Calls reports how many times ReadoptAndFence ran.
func (r *FenceReadopter) Calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// Generations returns the generation passed to each ReadoptAndFence call, in
// order, so a test can assert the pod was fenced to the post-handoff value.
func (r *FenceReadopter) Generations() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]int64, len(r.gens))
	copy(out, r.gens)
	return out
}

// CalledFor reports how many times ReadoptAndFence ran for the session. It
// lets a test assert a terminal session was never adopted (re-fenced) by the
// survivor's takeover sweep, so the §10.1 terminal-skip gate is pinned directly
// rather than inferred from a relinquished lease.
// spec: §10.1 (a terminal session is no longer coordinated by anyone).
func (r *FenceReadopter) CalledFor(sessionID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, s := range r.sessions {
		if s == sessionID {
			n++
		}
	}
	return n
}

// Replica is a real gateway coordination replica: the production Sweeper wired
// over a shared session store and lease store, its own per-replica binding
// registry, and its own fence readopter over a shared pod. Standing up two
// Replicas over one Redis lease store and one Postgres session store makes a
// coordinator handoff a genuine cross-replica lease race and generation fence
// driven by the production Sweeper on both sides, rather than a directly modeled
// lease write. The coordinating replica binds the session (holds the lease at
// the at-bind generation through its own sweep); the survivor's Sweeper adopts
// the lapsed lease once the coordinator crashes.
// spec: §4.6.1 (coordinating replica holds the lease), §10.1 (coordinator
// handoff; generation fence).
type Replica struct {
	ID        string
	Sweeper   *coordination.Sweeper
	Bindings  *Bindings
	Readopter *FenceReadopter
}

// NewReplica builds a real coordination Replica for tenantID over the shared
// session store, lease store, and pod. The sessions named in bind are published
// on the replica's binding registry so a coordinating replica holds the binding
// the §4.6.1 co-location invariant requires, and its sweep renews (rather than
// adopts) those leases. ttl is the lease lifetime; a short ttl on a coordinating
// replica lets a test inject a crash as a genuine Redis lease lapse once the
// replica stops sweeping.
func NewReplica(id, tenantID string, pod *Pod, sessions sessionstore.Store, leases leasestore.LeaseStore, ttl time.Duration, bind ...string) *Replica {
	bindings := NewBindings()
	for _, s := range bind {
		bindings.Publish(s)
	}
	readopter := &FenceReadopter{Pod: pod, Bindings: bindings, Leases: leases, ReplicaID: id, TenantID: tenantID}
	sw := coordination.NewSweeper(Tenants{tenantID}, sessions, leases, coordination.Options{
		ReplicaID: id,
		TTL:       ttl,
		Interval:  time.Hour,
		Bindings:  bindings,
		Readopter: readopter,
	})
	return &Replica{ID: id, Sweeper: sw, Bindings: bindings, Readopter: readopter}
}
