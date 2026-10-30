// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test for the §12.4 Redis-outage Postgres
// fallback behind the §5.2 intra-pod slot counter. It wires a real
// slotcounter.Counter against a real Redis container and a real Postgres
// container (the production migrations applied) as the FallbackSource, then
// injects a Redis outage by stopping the Redis container mid-flight. The test
// asserts the multi-service flow the proposal names: a Redis-only outage
// degrades intra-pod slot admission to the Postgres advisory-lock gate rather
// than rejecting all session dispatch, concurrent admissions during the outage
// serialize under the per-pod advisory lock so the bound is not overrun, and
// after the bounded fail-closed window the gate fails closed.
package tier4_integration_test

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"reflect"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/slotreaper"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	sessionpg "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// newSessionUUID returns a fresh random UUIDv4 string; the sessions.id column
// is typed UUID per §12.6.
func newSessionUUID(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// spec:
// §12.4 (durable fallback for every Redis-backed role), §12.4 (bounded
// fail-closed window).
// diagnosis: a failure means the §5.2 slot counter did not degrade to the
// Postgres advisory-lock fallback on a Redis outage. Either a Redis-only
// outage rejected all slot dispatch (an unguarded mandatory increment), the
// per-pod advisory lock did not serialize concurrent admissions so the bound
// was overrun, or the bounded fail-closed window did not eventually fail
// closed against a sustained outage.
func TestSlotCounterRedisOutagePostgresFallback_spec_12_4(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	pg := sharedDatabase(t)
	seedTenant(t, pg, "acme")
	store := sessionpg.New(pg.Pool)

	rd := containers.StartRedis(t, containers.RedisOptions{})

	// A test clock so the bounded fail-closed window is exercised without a
	// wall-clock sleep. The window is 60s; the clock advances past it on
	// demand.
	now := time.Now()
	clk := &advanceableClock{t: now}
	counter := slotcounter.New(rd.Client,
		slotcounter.WithSlotSource(store),
		slotcounter.WithFallbackSource(store),
		slotcounter.WithFallbackMaxWindow(60*time.Second),
		slotcounter.WithClockForTest(clk.now))

	const pod = "pod-outage-1"
	const maxConcurrent = 4

	// Fast path: Redis is healthy, so the reservation adds the session's
	// member to the pod's Redis slot set. The rehydrated flag is absent on a fresh counter, so the first
	// reservation seeds from Postgres (zero live sessions) and admits.
	if count, _, err := counter.Reserve(ctx, pod, "outage-fast", maxConcurrent); err != nil {
		t.Fatalf("Reserve on the Redis fast path: %v", err)
	} else if count != 1 {
		t.Fatalf("fast-path count = %d, want 1", count)
	}

	// Inject the Redis outage: stop the Redis container. Every subsequent
	// Reserve observes Redis as unreachable and routes to the Postgres gate.
	rd.Stop(t)

	// Seed two live sessions bound to the pod in Postgres so the fallback gate
	// reads a non-zero occupancy from the same source the rehydration path
	// reads. ReserveSlotUnderLock counts these live rows.
	mustSeedSlotSession(t, ctx, store, "acme", pod)
	mustSeedSlotSession(t, ctx, store, "acme", pod)

	// Degraded path: the Redis-only outage must not reject all dispatch. The
	// gate admits via Postgres (2 live + this admission = 3, below the bound).
	count, _, err := counter.Reserve(ctx, pod, "outage-degraded", maxConcurrent)
	if err != nil {
		t.Fatalf("Reserve during the Redis outage must degrade to the Postgres fallback, got: %v", err)
	}
	if count != 3 {
		t.Errorf("fallback count = %d, want 3 (2 live + this admission)", count)
	}

	// Concurrent dispatch during the outage on a pod with one free slot left:
	// the per-pod advisory lock must serialize the count-and-decide so at most
	// one of the racers wins the last slot. Each winner persists its session
	// row so the next gated count observes the committed occupancy.
	t.Run("concurrent dispatch serializes under the per-pod advisory lock", func(t *testing.T) {
		const racerPod = "pod-outage-race"
		// Three live sessions on a bound of 4 leave exactly one free slot.
		mustSeedSlotSession(t, ctx, store, "acme", racerPod)
		mustSeedSlotSession(t, ctx, store, "acme", racerPod)
		mustSeedSlotSession(t, ctx, store, "acme", racerPod)

		const racers = 8
		var admitted int32
		var wg sync.WaitGroup
		wg.Add(racers)
		for i := 0; i < racers; i++ {
			go func(i int) {
				defer wg.Done()
				c, _, rErr := counter.Reserve(ctx, racerPod, fmt.Sprintf("outage-racer-%d", i), maxConcurrent)
				if rErr != nil {
					return // ErrSlotsExhausted once the bound is reached.
				}
				if c >= 1 {
					mustSeedSlotSession(t, ctx, store, "acme", racerPod)
					atomic.AddInt32(&admitted, 1)
				}
			}(i)
		}
		wg.Wait()
		if got := atomic.LoadInt32(&admitted); got != 1 {
			t.Errorf("admitted = %d, want exactly 1; the per-pod advisory lock must serialize the count-and-decide so the bound is not overrun during the outage", got)
		}
	})

	// Bounded fail-closed window: a sustained Redis outage cannot keep gating
	// on Postgres latency forever. Advance the clock past the 60s window; the
	// next reservation fails closed.
	t.Run("fails closed after the bounded outage window", func(t *testing.T) {
		clk.advance(61 * time.Second)
		_, _, fErr := counter.Reserve(ctx, "pod-window", "outage-window", maxConcurrent)
		if fErr == nil {
			t.Fatal("Reserve after the bounded outage window must fail closed, got nil")
		}
	})
}

// mustSeedSlotSession persists a live (running) session row bound to pod so
// the Postgres fallback gate counts it. The non-terminal predicate in
// GetActiveSlotsByPod / ReserveSlotUnderLock matches the running state.
func mustSeedSlotSession(t *testing.T, ctx context.Context, store *sessionpg.Store, tenant, pod string) {
	t.Helper()
	if err := store.Create(ctx, sessionstore.Session{
		ID:            newSessionUUID(t),
		TenantID:      tenant,
		State:         session.StateRunning,
		RuntimeRef:    "echo",
		PodAssignment: pod,
	}); err != nil {
		t.Fatalf("seed slot session on %s: %v", pod, err)
	}
}

// advanceableClock is a test wall clock the §12.4 outage-window measurement
// uses so the bounded fail-closed window is exercised deterministically.
type advanceableClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *advanceableClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *advanceableClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// outageCap is the maxConcurrentSessions of the pod the outage-recovery cases
// fill: a live session, a leaked: member, an in-flight reservation, and two
// fallback admissions reach it exactly.
const outageCap = 5

// outageTopology is two gateway replicas' slot counters, A and B, on one
// Redis reached through a partitionable proxy and one Postgres session store,
// with one pod whose set holds, before the outage, a live session, a leaked:
// member, and a reservation B made for a session whose row does not yet name
// the pod.
type outageTopology struct {
	store *sessionpg.Store
	proxy *lifecycleharness.Proxy
	a, b  *slotcounter.Counter
	pod   string
	// live is the pre-outage session whose row names the pod; leaked the
	// session whose leaked release left leaked:<id>; inflight B's
	// reservation with no row; admitted the two sessions A's fallback
	// admits during the outage.
	live, leaked, inflight string
	admitted               [2]string
}

// outageCounter returns one replica's counter over the proxy, with timeouts
// short enough that a partition reads as an unreachable Redis.
func outageCounter(t *testing.T, addr string, store *sessionpg.Store) *slotcounter.Counter {
	t.Helper()
	rc := redis.NewClient(&redis.Options{
		Addr: addr, DialTimeout: 300 * time.Millisecond, ReadTimeout: 300 * time.Millisecond,
		WriteTimeout: 300 * time.Millisecond, MaxRetries: -1,
	})
	t.Cleanup(func() { _ = rc.Close() })
	return slotcounter.New(rc, slotcounter.WithSlotSource(store), slotcounter.WithFallbackSource(store),
		slotcounter.WithFallbackMaxWindow(time.Hour))
}

// newOutageTopology starts the stores and fills the pod's set with its
// pre-outage members.
func newOutageTopology(t *testing.T) *outageTopology {
	t.Helper()
	ctx := context.Background()
	pg := sharedDatabase(t)
	seedTenant(t, pg, "acme")
	store := sessionpg.New(pg.Pool)
	rd := containers.StartRedis(t, containers.RedisOptions{})
	proxy := lifecycleharness.NewProxy(t, rd.Addr)
	o := &outageTopology{
		store: store, proxy: proxy, pod: "pod-" + newSessionUUID(t)[:8],
		a: outageCounter(t, proxy.Addr(), store), b: outageCounter(t, proxy.Addr(), store),
		live: newSessionUUID(t), leaked: newSessionUUID(t), inflight: newSessionUUID(t),
		admitted: [2]string{newSessionUUID(t), newSessionUUID(t)},
	}
	o.seedRow(t, o.live, session.StateRunning)
	o.reserve(t, o.a, o.live)
	o.reserve(t, o.a, o.leaked)
	if _, _, _, err := o.a.Release(ctx, o.pod, o.leaked, true); err != nil {
		t.Fatalf("leaked release: %v", err)
	}
	o.reserve(t, o.b, o.inflight)
	o.requireMembers(t, o.live, slotcounter.LeakedMember(o.leaked), o.inflight)
	return o
}

// seedRow writes a concurrent-workspace session row in state naming the
// pod, with the per-slot scrub policy the slot-holder listing selects on.
func (o *outageTopology) seedRow(t *testing.T, id string, st session.State) {
	t.Helper()
	if err := o.store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", State: st, RuntimeRef: "echo", PodAssignment: o.pod,
		ScrubPolicy: sessionserver.ScrubPolicyPerSlot,
	}); err != nil {
		t.Fatalf("seed row %s: %v", id, err)
	}
}

// end moves a session row to completed and returns its terminated_at.
func (o *outageTopology) end(t *testing.T, id string) time.Time {
	t.Helper()
	row, err := o.store.Update(context.Background(), "acme", id, func(r *sessionstore.Session) error {
		from := r.State
		r.State = session.StateCompleted
		sessionstore.MarkTerminated(r, from, time.Now().UTC())
		return nil
	})
	if err != nil {
		t.Fatalf("end %s: %v", id, err)
	}
	return row.TerminatedAt
}

// reserve reserves id on the pod through c and fails on an error.
func (o *outageTopology) reserve(t *testing.T, c *slotcounter.Counter, id string) {
	t.Helper()
	if _, _, err := c.Reserve(context.Background(), o.pod, id, outageCap); err != nil {
		t.Fatalf("reserve %s: %v", id, err)
	}
}

// members reads the pod's set through a counter of its own, which runs no
// rebuild.
func (o *outageTopology) members(t *testing.T) []string {
	t.Helper()
	m, err := o.a.Members(context.Background(), o.pod)
	if err != nil {
		t.Fatalf("read the slot set: %v", err)
	}
	sort.Strings(m)
	return m
}

// requireMembers fails unless the pod's set holds exactly want.
func (o *outageTopology) requireMembers(t *testing.T, want ...string) {
	t.Helper()
	sort.Strings(want)
	if got := o.members(t); !reflect.DeepEqual(got, want) {
		t.Fatalf("slot set = %v, want %v", got, want)
	}
}

// outageAdmissions makes Redis unreachable with its data kept, dropping
// every request sent during the outage, and has A's fallback admit both
// sessions; the first one's row is written during the outage.
func (o *outageTopology) outageAdmissions(t *testing.T) {
	t.Helper()
	o.proxy.Drop()
	o.reserve(t, o.a, o.admitted[0])
	o.seedRow(t, o.admitted[0], session.StateRunning)
	o.reserve(t, o.a, o.admitted[1])
}

// rebuildOnA heals Redis and runs a call on A, which runs A's recovery
// rebuild first; the call itself touches another pod.
func (o *outageTopology) rebuildOnA(t *testing.T) {
	t.Helper()
	o.proxy.Heal()
	other := "pod-other-" + o.pod
	o.probeAnotherPod(t, other)
}

// probeAnotherPod reserves and releases a probe session on another pod through A.
func (o *outageTopology) probeAnotherPod(t *testing.T, pod string) {
	t.Helper()
	ctx := context.Background()
	probe := newSessionUUID(t)
	if _, _, err := o.a.Reserve(ctx, pod, probe, outageCap); err != nil {
		t.Fatalf("A's call after the outage: %v", err)
	}
	if _, _, _, err := o.a.Release(ctx, pod, probe, false); err != nil {
		t.Fatalf("A's probe release: %v", err)
	}
}

// requireRefused fails unless a reservation of a new session through c is
// refused at the cap.
func (o *outageTopology) requireRefused(t *testing.T, c *slotcounter.Counter, name string) {
	t.Helper()
	if _, _, err := c.Reserve(context.Background(), o.pod, newSessionUUID(t), outageCap); !errors.Is(err, slotcounter.ErrSlotsExhausted) {
		t.Errorf("a reservation over the cap on %s = %v, want ErrSlotsExhausted", name, err)
	}
}

// boundClaimer is a slot claimer over a cluster holding the pod's bound
// claim, so a release's occupancy-zero disposition is observable.
func (o *outageTopology) boundClaimer(t *testing.T, c *slotcounter.Counter) (*podclaim.SlotClaimer, client.Client) {
	t.Helper()
	s := k8sruntime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	claim := &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: podclaim.ClaimName(o.pod), Namespace: "lenny-agents"},
		Spec:       lennyv1.SandboxClaimSpec{SandboxRef: o.pod, TenantID: "acme"},
		Status:     lennyv1.SandboxClaimStatus{Phase: string(claimstate.Bound)},
	}
	kube := ctrlfake.NewClientBuilder().WithScheme(s).WithObjects(claim).WithStatusSubresource(claim).Build()
	return &podclaim.SlotClaimer{Client: kube, Namespace: "lenny-agents", Counter: c}, kube
}

// requireClaimBound fails unless the pod's claim is still bound.
func (o *outageTopology) requireClaimBound(t *testing.T, kube client.Client) {
	t.Helper()
	var claim lennyv1.SandboxClaim
	if err := kube.Get(context.Background(), client.ObjectKey{Namespace: "lenny-agents", Name: podclaim.ClaimName(o.pod)}, &claim); err != nil {
		t.Fatalf("the claim is gone: %v", err)
	}
	if claim.Status.Phase != string(claimstate.Bound) {
		t.Errorf("claim = %q, want bound", claim.Status.Phase)
	}
}

// spec: §12.4 (Redis failure behavior), §5.2 (Post-recovery rehydration
// atomicity)
// diagnosis: after a Redis outage that kept its data, the admitting replica's
// recovery rebuild lost a member: a fallback admission (whose row named the
// pod before or after the rebuild), the leaked: member, or the other
// replica's in-flight reservation is missing, so the pod admits past its cap;
// or the release of the pre-outage session ran the occupancy-zero
// disposition under the other members.
func TestOutageRebuildKeepsEveryMember_spec_12_4(t *testing.T) {
	o := newOutageTopology(t)
	o.outageAdmissions(t)
	o.rebuildOnA(t)
	o.seedRow(t, o.admitted[1], session.StateRunning)
	o.requireMembers(t, o.live, slotcounter.LeakedMember(o.leaked), o.inflight, o.admitted[0], o.admitted[1])
	o.requireRefused(t, o.a, "replica A")
	o.requireRefused(t, o.b, "replica B")

	claimer, kube := o.boundClaimer(t, o.a)
	if _, err := claimer.ReleaseSlot(context.Background(), o.pod, o.live, false, false); err != nil {
		t.Fatalf("release the pre-outage session: %v", err)
	}
	o.requireClaimBound(t, kube)
	o.requireMembers(t, slotcounter.LeakedMember(o.leaked), o.inflight, o.admitted[0], o.admitted[1])
}

// failedTerminalRelease has B end the pre-outage live session during the
// partition, so its release fails, and then discards B.
func (o *outageTopology) failedTerminalRelease(t *testing.T) time.Time {
	t.Helper()
	terminatedAt := o.end(t, o.live)
	if _, _, _, err := o.b.Release(context.Background(), o.pod, o.live, false); err == nil {
		t.Fatal("B's release during the partition succeeded, want an unreachable Redis")
	}
	o.b = nil
	return terminatedAt
}

// spec: §12.4 (Redis failure behavior), §5.2 (Post-recovery rehydration
// atomicity), §4.6.1 (Orphaned SandboxClaim detection)
// diagnosis: a member whose release failed during the outage, on a replica
// that stopped before Redis returned, was removed by the surviving replica's
// rebuild, so the pod admits one session over its cap while the slot may
// still be held; or the pod's last release after it ran the occupancy-zero
// disposition, or the slot-holder read still names a session for the pod,
// which keeps the orphan GC from draining it.
func TestOutageFailedReleaseStaysCounted_spec_12_4(t *testing.T) {
	ctx := context.Background()
	o := newOutageTopology(t)
	o.outageAdmissions(t)
	o.failedTerminalRelease(t)
	o.rebuildOnA(t)
	o.seedRow(t, o.admitted[1], session.StateRunning)
	o.requireMembers(t, o.live, slotcounter.LeakedMember(o.leaked), o.inflight, o.admitted[0], o.admitted[1])
	o.requireRefused(t, o.a, "replica A")

	claimer, kube := o.boundClaimer(t, o.a)
	for _, id := range []string{o.admitted[0], o.admitted[1], o.inflight} {
		if id != o.inflight {
			o.end(t, id)
		}
		if _, err := claimer.ReleaseSlot(ctx, o.pod, id, false, false); err != nil {
			t.Fatalf("release %s: %v", id, err)
		}
	}
	o.requireMembers(t, o.live, slotcounter.LeakedMember(o.leaked))
	o.requireClaimBound(t, kube)
	holders, err := o.store.GetActiveSlotsByPod(ctx, o.pod)
	if err != nil {
		t.Fatalf("GetActiveSlotsByPod: %v", err)
	}
	if len(holders) != 0 {
		t.Errorf("slot holders of the pod = %v, want none, so the orphan GC's precondition holds", holders)
	}
}

// spec: §5.2 (Slot assignment atomicity), §12.4 (Redis failure behavior)
// diagnosis: the stale-slot reaper did not release a member whose release
// failed during an outage once its session had been terminal longer than the
// terminal age, so the pod stays one slot short for its lifetime; or it
// released it early, or removed a live session's member.
func TestStaleSlotReaperReleasesTheOutageMember_spec_5_2(t *testing.T) {
	ctx := context.Background()
	o := newOutageTopology(t)
	o.outageAdmissions(t)
	terminatedAt := o.failedTerminalRelease(t)
	o.rebuildOnA(t)
	o.seedRow(t, o.admitted[1], session.StateRunning)
	o.requireRefused(t, o.a, "replica A")
	o.requireMembers(t, o.live, slotcounter.LeakedMember(o.leaked), o.inflight, o.admitted[0], o.admitted[1])

	const terminalAge = 10 * time.Second
	now := terminatedAt
	reaper, err := slotreaper.New(slotreaper.Config{
		Pods: o.store, Members: o.a, Sessions: o.store, Releaser: o.reapBinder(t),
		Rate: 10, Interval: time.Second, TerminalAge: terminalAge,
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("slotreaper.New: %v", err)
	}
	for _, at := range []time.Duration{0, terminalAge / 2, terminalAge - time.Second} {
		now = terminatedAt.Add(at)
		reaper.Pass(ctx)
		if m := o.members(t); !slices.Contains(m, o.live) {
			t.Fatalf("the pass at terminated_at+%s released the member early: %v", at, m)
		}
	}
	now = terminatedAt.Add(terminalAge + time.Second)
	if stats := reaper.Pass(ctx); stats.Released != 1 {
		t.Fatalf("the pass after the terminal age released %d members, want 1 (stats %+v)", stats.Released, stats)
	}
	o.requireMembers(t, slotcounter.LeakedMember(o.leaked), o.inflight, o.admitted[0], o.admitted[1])
	fresh := newSessionUUID(t)
	o.reserve(t, o.a, fresh)
	o.requireMembers(t, slotcounter.LeakedMember(o.leaked), o.inflight, o.admitted[0], o.admitted[1], fresh)
}

// reapBinder is the reaper's releaser: a binder whose cluster holds the pod's
// Sandbox and whose adapter dial reaches an adapter holding no session, which
// answers a Shutdown absent with a clean exit.
func (o *outageTopology) reapBinder(t *testing.T) *podsession.Binder {
	t.Helper()
	s := k8sruntime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	sb := &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: o.pod, Namespace: "lenny-agents"},
		Status:     lennyv1.SandboxStatus{Phase: "claimed", PodIP: "10.244.9.9"},
	}
	kube := ctrlfake.NewClientBuilder().WithScheme(s).WithObjects(sb).WithStatusSubresource(sb).Build()
	srv := adapter.New("reaper-target")
	srv.WorkspaceBase = t.TempDir()
	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	return &podsession.Binder{
		Client: kube, Namespace: "lenny-agents", AdapterPort: 50051,
		AcceptedVersions: []string{adapter.ProtocolVersionV1},
		SlotCounter:      o.a,
		DialAdapter: func(string) (*adapterclient.Client, error) {
			return adapterclient.Dial("passthrough:///bufnet",
				grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
				grpc.WithTransportCredentials(insecure.NewCredentials()))
		},
	}
}
