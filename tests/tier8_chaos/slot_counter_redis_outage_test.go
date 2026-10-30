// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos test for the §12.4 Redis-outage Postgres
// fallback behind the §5.2 intra-pod slot counter. It is the failure/recovery
// path the proposal names: a Redis outage injected mid-flight under concurrent
// dispatch, then a Redis recovery. The Kind e2e overlay's store-outage tests
// (store_failure_test.go::TestRedisClusterDegraded) assert the gateway process
// survives a Redis outage and the §25.3 health report flags Redis; this test
// drives the slot counter directly against real Redis and Postgres containers
// so the §12.4 fail-closed window and the post-recovery rehydration are
// exercised with an injected clock, the fault-injection layer the scaffolds
// note the Kind overlay does not provide.
package tier8_chaos_test

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	sessionpg "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// spec: 5.2 (Slot assignment atomicity), 12.4 (Redis HA and failure modes,
// bounded fail-closed window; durable fallback for every Redis-backed role).
// diagnosis: a failure means the §5.2 slot counter did not survive a Redis
// outage per §12.4. Either a Redis-only outage rejected all slot dispatch
// instead of degrading to the Postgres advisory-lock gate, concurrent
// admissions during the outage overran the per-pod bound, the bounded
// fail-closed window did not eventually fail closed against a sustained
// outage, or the counter did not resume the Redis fast path after Redis
// recovered (rehydrating its count from Postgres).
func TestSlotCounterSurvivesRedisOutageAndRecovers(t *testing.T) {
	ctx := context.Background()

	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	seedChaosTenant(t, pg, "acme")
	store := sessionpg.New(pg.Pool)

	rd := containers.StartRedis(t, containers.RedisOptions{})

	clk := &chaosClock{t: time.Now()}
	counter := slotcounter.New(rd.Client,
		slotcounter.WithSlotSource(store),
		slotcounter.WithFallbackSource(store),
		slotcounter.WithFallbackMaxWindow(60*time.Second),
		slotcounter.WithClockForTest(clk.now))

	const pod = "pod-chaos-1"
	const maxConcurrent = 4

	// Healthy: the first reservation seeds the counter from Postgres (zero
	// live sessions) and admits via Redis.
	if c, _, err := counter.Reserve(ctx, pod, "chaos-healthy", maxConcurrent); err != nil || c != 1 {
		t.Fatalf("healthy Reserve = (%d, %v), want (1, nil)", c, err)
	}

	// Inject: stop Redis. Every subsequent Reserve observes Redis unreachable.
	rd.Stop(t)

	// Two live slots persisted in Postgres on the pod so the fallback gate
	// reads a real occupancy; the gate admits the third (below the bound of 4).
	mustSeedChaosSlot(t, ctx, store, "acme", pod)
	mustSeedChaosSlot(t, ctx, store, "acme", pod)
	if c, _, err := counter.Reserve(ctx, pod, "chaos-outage", maxConcurrent); err != nil {
		t.Fatalf("Reserve during the Redis outage must degrade to Postgres, got: %v", err)
	} else if c != 3 {
		t.Errorf("outage fallback count = %d, want 3 (2 live + this admission)", c)
	}

	// Concurrent dispatch during the outage on a pod with one free slot: the
	// per-pod advisory lock serializes the count-and-decide so only one racer
	// wins the last slot.
	t.Run("concurrent dispatch under the outage does not overrun the bound", func(t *testing.T) {
		const racePod = "pod-chaos-race"
		mustSeedChaosSlot(t, ctx, store, "acme", racePod)
		mustSeedChaosSlot(t, ctx, store, "acme", racePod)
		mustSeedChaosSlot(t, ctx, store, "acme", racePod)
		const racers = 12
		var admitted int32
		var wg sync.WaitGroup
		wg.Add(racers)
		for i := 0; i < racers; i++ {
			go func(i int) {
				defer wg.Done()
				c, _, err := counter.Reserve(ctx, racePod, fmt.Sprintf("chaos-racer-%d", i), maxConcurrent)
				if err != nil {
					return
				}
				if c >= 1 {
					mustSeedChaosSlot(t, ctx, store, "acme", racePod)
					atomic.AddInt32(&admitted, 1)
				}
			}(i)
		}
		wg.Wait()
		if got := atomic.LoadInt32(&admitted); got != 1 {
			t.Errorf("admitted = %d during the outage, want exactly 1 (the advisory lock must serialize the gate)", got)
		}
	})

	// Bounded fail-closed window: advance the clock past 60s with Redis still
	// down; the gate fails closed.
	t.Run("fails closed after the bounded outage window", func(t *testing.T) {
		clk.advance(61 * time.Second)
		if _, _, err := counter.Reserve(ctx, "pod-chaos-window", "chaos-window", maxConcurrent); err == nil {
			t.Fatal("Reserve after the bounded outage window must fail closed, got nil")
		}
	})

	// Recover: bring Redis back. The counter resumes the Redis fast path,
	// rehydrating the pod's count from Postgres on the first post-recovery
	// reservation so the stale-zero counter does not over-admit. The
	// recovered pod (`pod-recovered`) has one live session seeded in Postgres,
	// so the first post-recovery Reserve seeds the counter to 1 and admits
	// slot 2.
	rd2 := containers.StartRedis(t, containers.RedisOptions{})
	counter2 := slotcounter.New(rd2.Client,
		slotcounter.WithSlotSource(store),
		slotcounter.WithFallbackSource(store),
		slotcounter.WithClockForTest(clk.now))

	const recoveredPod = "pod-recovered"
	mustSeedChaosSlot(t, ctx, store, "acme", recoveredPod)
	c, rehydrated, err := counter2.Reserve(ctx, recoveredPod, "chaos-recovered-1", maxConcurrent)
	if err != nil {
		t.Fatalf("post-recovery Reserve: %v", err)
	}
	if !rehydrated {
		t.Error("the first post-recovery reservation must rehydrate the counter from Postgres")
	}
	if c != 2 {
		t.Errorf("post-recovery count = %d, want 2 (1 live seeded in Postgres + this admission)", c)
	}
	// A further admission past the bound on the recovered fast path is
	// rejected with ErrSlotsExhausted, confirming the Redis cap enforcement
	// resumed.
	mustSeedChaosSlot(t, ctx, store, "acme", recoveredPod) // now 2 live + 1 reserved.
	if _, _, err := counter2.Reserve(ctx, recoveredPod, "chaos-recovered-2", 2); !errors.Is(err, slotcounter.ErrSlotsExhausted) {
		t.Errorf("post-recovery over-bound Reserve = %v, want ErrSlotsExhausted (Redis cap enforcement resumed)", err)
	}
}

// seedChaosTenant inserts a tenant registry row for the chaos slot test.
func seedChaosTenant(t *testing.T, pg *containers.Postgres, id string) {
	t.Helper()
	if _, err := pg.Pool.Exec(context.Background(),
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00')
		 ON CONFLICT (id) DO NOTHING`, id); err != nil {
		t.Fatalf("seed tenant %q: %v", id, err)
	}
}

// mustSeedChaosSlot persists a live (running) session row bound to pod so the
// Postgres fallback / rehydration count observes it.
func mustSeedChaosSlot(t *testing.T, ctx context.Context, store *sessionpg.Store, tenant, pod string) {
	t.Helper()
	if err := store.Create(ctx, sessionstore.Session{
		ID:            newChaosUUID(t),
		TenantID:      tenant,
		State:         session.StateRunning,
		RuntimeRef:    "echo",
		PodAssignment: pod,
	}); err != nil {
		t.Fatalf("seed slot session on %s: %v", pod, err)
	}
}

// newChaosUUID returns a fresh random UUIDv4 string for a session ID.
func newChaosUUID(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// chaosClock is a test wall clock the §12.4 outage-window measurement uses so
// the bounded fail-closed window is exercised without a wall-clock sleep.
type chaosClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *chaosClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *chaosClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// slotChaos is the topology the restart and outage cases share: a Postgres
// session store, one Redis container reached through a partitionable proxy,
// and a fake apiserver holding each pod's claim, so a release's occupancy-zero
// disposition is observable.
type slotChaos struct {
	store *sessionpg.Store
	rd    *containers.Redis
	proxy *lifecycleharness.Proxy
	kube  client.WithWatch
}

func newSlotChaos(t *testing.T) *slotChaos {
	t.Helper()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	seedChaosTenant(t, pg, "acme")
	rd := containers.StartRedis(t, containers.RedisOptions{})
	s := k8sruntime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	base := ctrlfake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&lennyv1.SandboxClaim{}).Build()
	return &slotChaos{
		// spec: §7.1 (Post-terminal window) — the store counts a terminal
		// session with no recorded release as holding its pod for the
		// gateway's default terminal pipeline budget, as production wires it.
		store: sessionpg.New(pg.Pool, sessionpg.WithPostTerminalCap(sessionserver.TerminalPipelineBudgetFor(
			sessionserver.DefaultWorkspaceSealMaxDuration, sessionserver.DefaultStreamFailureReportTimeout,
		))),
		rd: rd, proxy: lifecycleharness.NewProxy(t, rd.Addr),
		kube: interceptor.NewClient(base, interceptor.Funcs{SubResourcePatch: chaosApplyAsUpdate}),
	}
}

// chaosApplyAsUpdate serves a server-side-apply status patch of a
// SandboxClaim, which the fake client does not implement, as a status update
// of the phase and transition time the patch sets.
func chaosApplyAsUpdate(ctx context.Context, cl client.Client, sub string, obj client.Object, p client.Patch, opts ...client.SubResourcePatchOption) error {
	want, ok := obj.(*lennyv1.SandboxClaim)
	if !ok || p.Type() != types.ApplyPatchType {
		return cl.SubResource(sub).Patch(ctx, obj, p, opts...)
	}
	var cur lennyv1.SandboxClaim
	if err := cl.Get(ctx, client.ObjectKeyFromObject(want), &cur); err != nil {
		return err
	}
	cur.Status.Phase = want.Status.Phase
	cur.Status.BindingStateTransitionTime = want.Status.BindingStateTransitionTime
	if want.Status.HoldExpiresAt != nil {
		cur.Status.HoldExpiresAt = want.Status.HoldExpiresAt
	}
	return cl.Status().Update(ctx, &cur)
}

// counter returns one replica's counter over the proxy, with timeouts short
// enough that a dropped request reads as an unreachable Redis, and hooks on
// its client.
func (c *slotChaos) counter(t *testing.T, hooks ...redis.Hook) *slotcounter.Counter {
	t.Helper()
	rc := redis.NewClient(&redis.Options{
		Addr: c.proxy.Addr(), DialTimeout: 300 * time.Millisecond, ReadTimeout: 300 * time.Millisecond,
		WriteTimeout: 300 * time.Millisecond, MaxRetries: -1,
	})
	for _, h := range hooks {
		rc.AddHook(h)
	}
	t.Cleanup(func() { _ = rc.Close() })
	return slotcounter.New(rc, slotcounter.WithSlotSource(c.store), slotcounter.WithFallbackSource(c.store),
		slotcounter.WithFallbackMaxWindow(time.Hour), slotcounter.WithRehydrationTimeout(500*time.Millisecond))
}

// pod returns a fresh pod name with a bound claim in phase.
func (c *slotChaos) pod(t *testing.T, phase claimstate.State) string {
	t.Helper()
	pod := "pod-" + newChaosUUID(t)[:8]
	claim := &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: podclaim.ClaimName(pod), Namespace: agentNamespace},
		Spec:       lennyv1.SandboxClaimSpec{SandboxRef: pod, TenantID: "acme"},
	}
	ctx := context.Background()
	if err := c.kube.Create(ctx, claim); err != nil {
		t.Fatal(err)
	}
	claim.Status.Phase = string(phase)
	if err := c.kube.Status().Update(ctx, claim); err != nil {
		t.Fatal(err)
	}
	return pod
}

// claimPhase returns the pod's claim phase, or "" when it is gone.
func (c *slotChaos) claimPhase(t *testing.T, pod string) string {
	t.Helper()
	var claim lennyv1.SandboxClaim
	err := c.kube.Get(context.Background(), client.ObjectKey{Namespace: agentNamespace, Name: podclaim.ClaimName(pod)}, &claim)
	if apierrors.IsNotFound(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return claim.Status.Phase
}

// row writes a per-slot session row naming pod.
func (c *slotChaos) row(t *testing.T, id, pod string, st session.State) {
	t.Helper()
	if err := c.store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", State: st, RuntimeRef: "echo", PodAssignment: pod, ScrubPolicy: "best-effort-per-slot",
	}); err != nil {
		t.Fatal(err)
	}
}

// update rewrites a row's state and pod.
func (c *slotChaos) update(t *testing.T, id string, st session.State, pod string) {
	t.Helper()
	if _, err := c.store.Update(context.Background(), "acme", id, func(r *sessionstore.Session) error {
		from := r.State
		r.State, r.PodAssignment = st, pod
		sessionstore.MarkTerminated(r, from, time.Now().UTC())
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// members returns the pod's set read straight from Redis.
func (c *slotChaos) members(t *testing.T, pod string) map[string]bool {
	t.Helper()
	ms, err := c.rd.Client.SMembers(context.Background(), "lenny:pod:{"+pod+"}:active_slots").Result()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, m := range ms {
		out[m] = true
	}
	return out
}

// restartLosingData empties Redis, which is what a restart without
// persistence leaves.
func (c *slotChaos) restartLosingData(t *testing.T) {
	t.Helper()
	if err := c.rd.Client.FlushAll(context.Background()).Err(); err != nil {
		t.Fatal(err)
	}
}

// claimer returns a slot claimer over the fake apiserver and counter.
func (c *slotChaos) claimer(counter *slotcounter.Counter) *podclaim.SlotClaimer {
	return &podclaim.SlotClaimer{Client: c.kube, Namespace: agentNamespace, Counter: counter}
}

// mustReserveChaos reserves id on pod and fails on an error.
func mustReserveChaos(t *testing.T, c *slotcounter.Counter, pod, id string, cap int32) {
	t.Helper()
	if _, _, err := c.Reserve(context.Background(), pod, id, cap); err != nil {
		t.Fatalf("reserve %s on %s: %v", id, pod, err)
	}
}

// spec: §5.2 (Post-recovery rehydration atomicity), §12.4 (Redis failure
// behavior), §6.2 (leaked slot semantics)
// diagnosis: after a Redis restart that lost the slot sets, a pod's set was
// not rehydrated from the session rows before a release or reservation acted
// on it, or the accepted residue of a lost set changed: the pod's last release
// ran no occupancy-zero disposition (the claim waits for the GC), a leaked:
// member survived the restart, a create's lost reservation stopped ending the
// create on the pod-failure path, or a duplicate release after the restart
// stopped re-running the recycle disposition on a held pod.
func TestRedisRestartLosingTheSlotSets_spec_5_2(t *testing.T) {
	ctx := context.Background()
	c := newSlotChaos(t)

	t.Run("the last release after a restart disposes of the claim", func(t *testing.T) {
		counter := c.counter(t)
		pod, s := c.pod(t, claimstate.Bound), newChaosUUID(t)
		c.row(t, s, pod, session.StateRunning)
		mustReserveChaos(t, counter, pod, s, 4)
		c.restartLosingData(t)
		c.update(t, s, session.StateCompleted, pod)
		if _, err := c.claimer(counter).ReleaseSlot(ctx, pod, s, false, false); err != nil {
			t.Fatalf("release: %v", err)
		}
		if got := c.claimPhase(t, pod); got != "" {
			t.Errorf("claim = %q, want deleted by the release's own disposition", got)
		}
		if m := c.members(t, pod); len(m) != 0 {
			t.Errorf("set = %v, want empty", m)
		}
	})

	t.Run("a restart drops the leaked members", func(t *testing.T) {
		counter := c.counter(t)
		pod, live, leaked, next := c.pod(t, claimstate.Bound), newChaosUUID(t), newChaosUUID(t), newChaosUUID(t)
		c.row(t, live, pod, session.StateRunning)
		mustReserveChaos(t, counter, pod, live, 4)
		mustReserveChaos(t, counter, pod, leaked, 4)
		if _, _, _, err := counter.Release(ctx, pod, leaked, true); err != nil {
			t.Fatal(err)
		}
		c.restartLosingData(t)
		mustReserveChaos(t, counter, pod, next, 4)
		if m := c.members(t, pod); m[slotcounter.LeakedMember(leaked)] || !m[live] || !m[next] {
			t.Errorf("set after the restart = %v, want the live and new sessions and no leaked member", m)
		}
	})

	t.Run("a create's lost reservation ends the create on the pod-failure path", func(t *testing.T) {
		counter := c.counter(t)
		pod, sibling, created := c.pod(t, claimstate.Bound), newChaosUUID(t), newChaosUUID(t)
		c.row(t, sibling, pod, session.StateRunning)
		mustReserveChaos(t, counter, pod, sibling, 4)
		mustReserveChaos(t, counter, pod, created, 4)
		c.restartLosingData(t)
		c.update(t, sibling, session.StateCompleted, pod)
		if _, err := c.claimer(counter).ReleaseSlot(ctx, pod, sibling, false, false); err != nil {
			t.Fatal(err)
		}
		if got := c.claimPhase(t, pod); got != "" {
			t.Errorf("claim = %q, want deleted, which ends the binding create on the pod-failure path", got)
		}
	})

	t.Run("a create's lost reservation admits one sibling over the cap until it ends", func(t *testing.T) {
		counter := c.counter(t)
		pod, live, created, joiner := c.pod(t, claimstate.Bound), newChaosUUID(t), newChaosUUID(t), newChaosUUID(t)
		c.row(t, live, pod, session.StateRunning)
		mustReserveChaos(t, counter, pod, live, 2)
		mustReserveChaos(t, counter, pod, created, 2)
		c.restartLosingData(t)
		mustReserveChaos(t, counter, pod, joiner, 2)
		if removed, _, _, err := counter.Release(ctx, pod, created, false); err != nil || removed {
			t.Fatalf("the lost create's release = removed %v, %v; want nothing to remove", removed, err)
		}
		if _, _, err := counter.Reserve(ctx, pod, newChaosUUID(t), 2); !errors.Is(err, slotcounter.ErrSlotsExhausted) {
			t.Errorf("the next reservation at the cap = %v, want ErrSlotsExhausted", err)
		}
	})

	// spec: §7.1 (Post-terminal window), §5.2 (Post-recovery rehydration
	// atomicity) — a terminal session whose release is not recorded keeps
	// its slot through a set loss: the seed counts it and a reservation at
	// the cap is refused, on the Redis path and on the outage gate. Once the
	// release is recorded the next set loss frees the slot.
	t.Run("the seed after a set loss counts a session in its post-terminal window", func(t *testing.T) {
		counter := c.counter(t)
		pod, s := c.pod(t, claimstate.Bound), newChaosUUID(t)
		c.row(t, s, pod, session.StateRunning)
		mustReserveChaos(t, counter, pod, s, 1)
		c.update(t, s, session.StateCompleted, pod)
		c.restartLosingData(t)
		if _, _, err := counter.Reserve(ctx, pod, newChaosUUID(t), 1); !errors.Is(err, slotcounter.ErrSlotsExhausted) {
			t.Fatalf("reservation at the cap after the set loss = %v, want ErrSlotsExhausted", err)
		}
		if m := c.members(t, pod); !m[s] {
			t.Errorf("seeded set = %v, want the sealing session", m)
		}
		if _, admitted, err := c.store.ReserveSlotUnderLock(ctx, pod, 1); err != nil || admitted {
			t.Errorf("outage gate at the cap = (admitted %v, %v), want refused", admitted, err)
		}
		if err := sessionstore.RecordPodRelease(ctx, c.store, "acme", s, time.Now()); err != nil {
			t.Fatalf("record the release: %v", err)
		}
		c.restartLosingData(t)
		if _, _, err := counter.Reserve(ctx, pod, newChaosUUID(t), 1); err != nil {
			t.Errorf("reservation after the recorded release = %v, want admitted", err)
		}
		if _, admitted, err := c.store.ReserveSlotUnderLock(ctx, pod, 1); err != nil || !admitted {
			t.Errorf("outage gate after the recorded release = (admitted %v, %v), want admitted", admitted, err)
		}
	})

	t.Run("a duplicate release after a restart re-runs the recycle disposition", func(t *testing.T) {
		counter := c.counter(t)
		pod, s := c.pod(t, claimstate.Bound), newChaosUUID(t)
		c.row(t, s, pod, session.StateRunning)
		mustReserveChaos(t, counter, pod, s, 4)
		c.update(t, s, session.StateCompleted, pod)
		if recycled, err := c.claimer(counter).ReleaseSlot(ctx, pod, s, true, false); err != nil || !recycled {
			t.Fatalf("first release = %v, %v; want the recycle edge", recycled, err)
		}
		if _, err := podclaim.WriteReservedStatus(ctx, c.kube, agentNamespace, podclaim.ClaimName(pod), time.Minute, time.Now); err != nil {
			t.Fatalf("hold the scrubbed pod: %v", err)
		}
		c.restartLosingData(t)
		if recycled, err := c.claimer(counter).ReleaseSlot(ctx, pod, s, true, false); err != nil || !recycled {
			t.Fatalf("duplicate release = %v, %v; want the recycle edge again", recycled, err)
		}
		if got := c.claimPhase(t, pod); got != string(claimstate.Recycling) {
			t.Errorf("held claim after the duplicate release = %q, want recycling", got)
		}
	})
}

// dropOn is a Redis hook that drops the path, through the proxy, right after
// the first command matching match completes (after true) or right before it
// is sent (after false), so a case can end an outage's start at a precise
// step of a rehydration or a rebuild.
type dropOn struct {
	proxy *lifecycleharness.Proxy
	match func(args []any) bool
	after bool
	fired atomic.Bool
}

func (d *dropOn) DialHook(next redis.DialHook) redis.DialHook { return next }

func (d *dropOn) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func (d *dropOn) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		hit := !d.fired.Load() && d.match(cmd.Args())
		if hit && !d.after {
			d.fired.Store(true)
			d.proxy.Drop()
		}
		err := next(ctx, cmd)
		if hit && d.after {
			d.fired.Store(true)
			d.proxy.Drop()
		}
		return err
	}
}

// isRehydratingLock matches the SET NX of a pod's rehydrating lock.
func isRehydratingLock(args []any) bool {
	return len(args) > 1 && strings.EqualFold(fmt.Sprint(args[0]), "set") && strings.HasSuffix(fmt.Sprint(args[1]), ":rehydrating")
}

// isRebuildScript matches the recovery rebuild's script, the only slot
// script whose first argument after its keys is the listing flag and which
// deletes the rehydrated flag.
func isRebuildScript(args []any) bool {
	if len(args) < 6 {
		return false
	}
	name := strings.ToLower(fmt.Sprint(args[0]))
	if name == "eval" {
		return strings.Contains(fmt.Sprint(args[1]), "redis.call('DEL', KEYS[2])")
	}
	if name != "evalsha" {
		return false
	}
	flag := fmt.Sprint(args[5])
	return fmt.Sprint(args[2]) == "2" && (flag == "0" || flag == "1") && !strings.HasPrefix(fmt.Sprint(args[5]), "leaked:")
}

// spec: §12.4 (Redis failure behavior), §5.2 (Post-recovery rehydration
// atomicity)
// diagnosis: a Redis outage that began during a pod's rehydration, or during
// a recovery rebuild, lost a member the set held before the outage, a
// non-terminal session naming the pod, or a session the fallback admitted, or
// admitted a reservation over the cap, or ran an occupancy-zero disposition
// under a live session; or a member whose release failed during the outage
// was removed by a rebuild, on the replica that kept running, on a replica
// whose counter was replaced, or after another replica re-reserved it.
func TestRedisOutageMidRehydrationAndMidRebuild_spec_12_4(t *testing.T) {
	ctx := context.Background()
	c := newSlotChaos(t)
	const cap = 6

	// seedPod gives the pod a live session row and member and a leaked:
	// member, then deletes the rehydrated flag, as a rebuild does.
	seedPod := func(t *testing.T, counter *slotcounter.Counter) (pod, live, leaked string) {
		pod, live, leaked = c.pod(t, claimstate.Bound), newChaosUUID(t), newChaosUUID(t)
		c.row(t, live, pod, session.StateRunning)
		mustReserveChaos(t, counter, pod, live, cap)
		mustReserveChaos(t, counter, pod, leaked, cap)
		if _, _, _, err := counter.Release(ctx, pod, leaked, true); err != nil {
			t.Fatal(err)
		}
		return pod, live, slotcounter.LeakedMember(leaked)
	}

	t.Run("an outage that begins after the rehydrating lock", func(t *testing.T) {
		drop := &dropOn{proxy: c.proxy, match: isRehydratingLock, after: true}
		setup := c.counter(t)
		pod, live, leaked := seedPod(t, setup)
		other := newChaosUUID(t)
		c.row(t, other, pod, session.StateRunning)
		if err := c.rd.Client.Del(ctx, "lenny:pod:{"+pod+"}:rehydrated").Err(); err != nil {
			t.Fatal(err)
		}
		a := c.counter(t, drop)
		admitted := newChaosUUID(t)
		if _, _, err := a.Reserve(ctx, pod, admitted, cap); err != nil {
			t.Fatalf("a reservation during the outage = %v, want the fallback's admission", err)
		}
		if !drop.fired.Load() {
			t.Fatal("the outage never began")
		}
		c.row(t, admitted, pod, session.StateRunning)
		c.proxy.Heal()
		after := newChaosUUID(t)
		mustReserveChaos(t, a, pod, after, cap)
		m := c.members(t, pod)
		for _, want := range []string{live, leaked, other, admitted, after} {
			if !m[want] {
				t.Errorf("set = %v, missing %s", m, want)
			}
		}
		if len(m) > cap {
			t.Errorf("set holds %d members, over the cap of %d", len(m), cap)
		}
		if got := c.claimPhase(t, pod); got != string(claimstate.Bound) {
			t.Errorf("claim = %q, want bound", got)
		}
	})

	t.Run("an outage during the rebuild's scripts keeps the ledger", func(t *testing.T) {
		setup := c.counter(t)
		pod, live, leaked := seedPod(t, setup)
		drop := &dropOn{proxy: c.proxy, match: isRebuildScript}
		a := c.counter(t, drop)
		c.proxy.Drop()
		admitted := newChaosUUID(t)
		if _, _, err := a.Reserve(ctx, pod, admitted, cap); err != nil {
			t.Fatalf("fallback admission: %v", err)
		}
		c.proxy.Heal()
		// The next call's rebuild drops the path before its first script.
		_, _, _ = a.Reserve(ctx, "pod-elsewhere-"+pod, newChaosUUID(t), cap)
		if !drop.fired.Load() {
			t.Fatal("the rebuild never ran a script")
		}
		if m := c.members(t, pod); m[admitted] {
			t.Fatalf("the admission was counted by a rebuild that failed: %v", m)
		}
		c.proxy.Heal()
		mustReserveChaos(t, a, "pod-elsewhere-"+pod, newChaosUUID(t), cap)
		m := c.members(t, pod)
		for _, want := range []string{live, leaked, admitted} {
			if !m[want] {
				t.Errorf("set after the completed rebuild = %v, missing %s", m, want)
			}
		}
	})

	// failedRelease ends session s and fails its release on replica r during
	// a dropped-path outage.
	failedRelease := func(t *testing.T, r *slotcounter.Counter, pod, s string) {
		c.update(t, s, session.StateCompleted, pod)
		c.proxy.Drop()
		if _, _, _, err := r.Release(ctx, pod, s, false); err == nil {
			t.Fatal("the release during the outage succeeded")
		}
	}

	t.Run("a failed release on a replica that keeps running stays counted", func(t *testing.T) {
		a := c.counter(t)
		pod, live, leaked := seedPod(t, a)
		sibling := newChaosUUID(t)
		c.row(t, sibling, pod, session.StateRunning)
		mustReserveChaos(t, a, pod, sibling, cap)
		failedRelease(t, a, pod, live)
		c.proxy.Heal()
		mustReserveChaos(t, a, "pod-elsewhere-"+pod, newChaosUUID(t), cap)
		if m := c.members(t, pod); !m[live] || !m[sibling] || !m[leaked] {
			t.Errorf("set after the rebuild = %v, want the failed release's member, the sibling, and the leaked member", m)
		}
	})

	t.Run("a failed release on a replaced counter stays counted", func(t *testing.T) {
		setup := c.counter(t)
		pod, live, leaked := seedPod(t, setup)
		observer := c.counter(t)
		failedRelease(t, c.counter(t), pod, live)
		if _, _, err := observer.Reserve(ctx, pod, newChaosUUID(t), cap); err != nil {
			t.Fatalf("the observer's fallback admission: %v", err)
		}
		c.proxy.Heal()
		mustReserveChaos(t, observer, "pod-elsewhere-"+pod, newChaosUUID(t), cap)
		if m := c.members(t, pod); !m[live] || !m[leaked] {
			t.Errorf("set after the observer's rebuild = %v, want the failed release's member and the leaked member", m)
		}
	})

	t.Run("a failed funnel release re-reserved by a peer stays counted", func(t *testing.T) {
		a, b := c.counter(t), c.counter(t)
		pod, live, _ := seedPod(t, a)
		// The failure funnel's edge write clears the pod, then its release
		// fails during the outage.
		if _, err := c.store.Update(ctx, "acme", live, func(r *sessionstore.Session) error {
			r.State, r.PodAssignment = session.StateResumePending, ""
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		c.proxy.Drop()
		if _, _, _, err := a.Release(ctx, pod, live, false); err == nil {
			t.Fatal("the funnel release during the outage succeeded")
		}
		if _, _, err := b.Reserve(ctx, pod, live, cap); err != nil {
			t.Fatalf("the peer's re-reservation during the outage: %v", err)
		}
		c.proxy.Heal()
		mustReserveChaos(t, a, "pod-elsewhere-"+pod, newChaosUUID(t), cap)
		if m := c.members(t, pod); !m[live] {
			t.Errorf("set after A's rebuild = %v, want the re-reserved session's member", m)
		}
	})
}

// failScripts fails every EVAL and EVALSHA while armed and counts the
// failures.
type failScripts struct {
	remaining atomic.Int32
	failed    atomic.Int32
}

func (f *failScripts) DialHook(next redis.DialHook) redis.DialHook { return next }

func (f *failScripts) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func (f *failScripts) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		name := strings.ToLower(fmt.Sprint(cmd.Args()[0]))
		if (name == "eval" || name == "evalsha") && f.remaining.Load() != 0 {
			f.remaining.Add(-1)
			f.failed.Add(1)
			err := errors.New("injected slot script failure")
			cmd.SetErr(err)
			return err
		}
		return next(ctx, cmd)
	}
}

// spec: §5.2 (Slot assignment atomicity), §12.4 (Redis failure behavior)
// diagnosis: a slot release whose counter call failed was not retried within
// its bound, so a transient failure left a stopped session counted; or a
// release whose counter call kept failing returned without error, or removed
// the member anyway, which fails open.
func TestReleaseCounterFailuresRetryOrStayCounted_spec_5_2(t *testing.T) {
	ctx := context.Background()
	c := newSlotChaos(t)
	for _, tc := range []struct {
		name      string
		failures  int32
		wantError bool
	}{
		{"recovers within the bound", 2, false},
		{"fails beyond the bound", -1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hook := &failScripts{}
			counter := c.counter(t, hook)
			pod, s, sibling := c.pod(t, claimstate.Bound), newChaosUUID(t), newChaosUUID(t)
			mustReserveChaos(t, counter, pod, s, 4)
			mustReserveChaos(t, counter, pod, sibling, 4)
			hook.remaining.Store(tc.failures)
			binder := &podsession.Binder{Client: c.kube, Namespace: agentNamespace, SlotCounter: counter}
			err := binder.ReleaseSlotReservation(ctx, pod, s, false)
			hook.remaining.Store(0)
			if (err != nil) != tc.wantError {
				t.Fatalf("ReleaseSlotReservation = %v, want error %v (failed %d calls)", err, tc.wantError, hook.failed.Load())
			}
			if m := c.members(t, pod); m[s] == !tc.wantError {
				t.Errorf("set = %v; the member is counted %v, want %v", m, m[s], tc.wantError)
			}
			if got := c.claimPhase(t, pod); got != string(claimstate.Bound) {
				t.Errorf("claim = %q, want bound while the sibling lives", got)
			}
		})
	}
}
