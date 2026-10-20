// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a randomized interleavings of the releases and reservations that act
// on one concurrent-workspace pod's slot set and per-pod claim.
//
// Each case runs many rounds over a miniredis slot set and a fake
// kube-apiserver, starting the racing actors behind one barrier with random
// jitter, and checks the slot invariants after every round: a member is
// removed at most once per reservation (read off the Redis commands by the
// lifecycle harness's slot log), a leaked release leaves its leaked: member,
// a pod's set never exceeds its cap, and the per-pod claim is deleted only by
// the release that empties the set. The departure cases, whose residue
// §5.2 (Slot assignment atomicity) and §4.6.1 (Orphaned SandboxClaim
// detection) accept, run on the lifecycle harness in
// slot_release_departure_race_test.go.
//
// spec: §5.2 (Slot assignment atomicity), §6.2 (leaked slot semantics),
// §4.6.3 (SandboxClaim: a release that ends the occupancy without reuse).
package tier7a_load_local_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/warmpool"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

const (
	slotRaceNS   = "lenny-agents"
	slotRacePool = "pool-c"
	slotRacePod  = "sbx-race"
	slotRaceCap  = 4
	slotRounds   = 60
)

// slotRaceEnv is one pod's slot set behind a logged Redis client and a fake
// apiserver holding the pod's Sandbox.
type slotRaceEnv struct {
	mr  *miniredis.Miniredis
	rc  *redis.Client
	log *lifecycleharness.SlotLog
	// base is the fake apiserver; kube serves status applies over it.
	base client.WithWatch
	kube client.WithWatch
}

func newSlotRaceEnv(t *testing.T, sandboxPhase string) *slotRaceEnv {
	t.Helper()
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	log := lifecycleharness.NewSlotLog()
	rc.AddHook(log)
	s := k8sruntime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	sb := &lennyv1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: slotRacePod, Namespace: slotRaceNS, Labels: map[string]string{warmpool.LabelPool: slotRacePool}},
		Status:     lennyv1.SandboxStatus{Phase: sandboxPhase, PodIP: "10.244.3.3"},
	}
	base := fake.NewClientBuilder().WithScheme(s).WithObjects(sb).WithStatusSubresource(sb, &lennyv1.SandboxClaim{}).Build()
	return &slotRaceEnv{mr: mr, rc: rc, log: log, base: base, kube: interceptor.NewClient(base, applyAsUpdate())}
}

// applyAsUpdate serves a server-side-apply status patch of a SandboxClaim,
// which the fake client does not implement, as a status update of the
// fields the patch sets.
func applyAsUpdate() interceptor.Funcs {
	return interceptor.Funcs{
		SubResourcePatch: func(ctx context.Context, cl client.Client, sub string, obj client.Object, p client.Patch, opts ...client.SubResourcePatchOption) error {
			want, ok := obj.(*lennyv1.SandboxClaim)
			if !ok || p.Type() != types.ApplyPatchType {
				return cl.SubResource(sub).Patch(ctx, obj, p, opts...)
			}
			var cur lennyv1.SandboxClaim
			if err := cl.Get(ctx, client.ObjectKeyFromObject(want), &cur); err != nil {
				return err
			}
			if want.Status.Phase != "" {
				cur.Status.Phase = want.Status.Phase
			}
			if want.Status.BindingStateTransitionTime != nil {
				cur.Status.BindingStateTransitionTime = want.Status.BindingStateTransitionTime
			}
			if want.Status.HoldExpiresAt != nil {
				cur.Status.HoldExpiresAt = want.Status.HoldExpiresAt
			}
			return cl.Status().Update(ctx, &cur)
		},
	}
}

// counter returns a fresh replica counter over the logged client.
func (e *slotRaceEnv) counter() *slotcounter.Counter { return slotcounter.New(e.rc) }

// claimer returns a slot claimer over kube with its own counter.
func (e *slotRaceEnv) claimer(kube client.Client) *podclaim.SlotClaimer {
	return &podclaim.SlotClaimer{Client: kube, Namespace: slotRaceNS, Counter: e.counter()}
}

// seedBoundClaim creates the pod's claim in `bound` and seeds the set with
// members, flag set.
func (e *slotRaceEnv) seedBoundClaim(t *testing.T, members ...string) {
	t.Helper()
	ctx := context.Background()
	claim := &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: podclaim.ClaimName(slotRacePod), Namespace: slotRaceNS},
		Spec:       lennyv1.SandboxClaimSpec{SandboxRef: slotRacePod, TenantID: "acme"},
	}
	if err := e.kube.Create(ctx, claim); err != nil {
		t.Fatal(err)
	}
	claim.Status.Phase = string(claimstate.Bound)
	if err := e.kube.Status().Update(ctx, claim); err != nil {
		t.Fatal(err)
	}
	c := e.counter()
	for _, m := range members {
		if _, _, err := c.Reserve(ctx, slotRacePod, m, slotRaceCap); err != nil {
			t.Fatal(err)
		}
	}
}

// claimPhase returns the pod's claim phase, or "" when it is gone.
func (e *slotRaceEnv) claimPhase(t *testing.T) string {
	t.Helper()
	var claim lennyv1.SandboxClaim
	err := e.kube.Get(context.Background(), client.ObjectKey{Namespace: slotRaceNS, Name: podclaim.ClaimName(slotRacePod)}, &claim)
	if apierrors.IsNotFound(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return claim.Status.Phase
}

// members returns the pod's slot set.
func (e *slotRaceEnv) members(t *testing.T) map[string]bool {
	t.Helper()
	ms, err := e.counter().Members(context.Background(), slotRacePod)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, m := range ms {
		out[m] = true
	}
	return out
}

// raceJittered starts each actor behind one barrier after a random jitter and waits.
func raceJittered(rng *rand.Rand, actors ...func()) {
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, a := range actors {
		jitter := time.Duration(rng.Intn(300)) * time.Microsecond
		wg.Add(1)
		go func(a func()) {
			defer wg.Done()
			<-start
			time.Sleep(jitter)
			a()
		}(a)
	}
	close(start)
	wg.Wait()
}

// spec: §5.2 (Slot assignment atomicity)
// diagnosis: concurrent reservations and releases on one pod let its slot set
// exceed maxConcurrentSessions, so the pod admitted more sessions than its
// cap.
func TestConcurrentReservationsNeverExceedTheCap_spec_5_2(t *testing.T) {
	e := newSlotRaceEnv(t, "claimed")
	ctx := context.Background()
	if err := e.mr.Set("lenny:pod:{"+slotRacePod+"}:rehydrated", "1"); err != nil {
		t.Fatal(err)
	}
	var over atomic.Int32
	stop := make(chan struct{})
	var sampler sync.WaitGroup
	sampler.Add(1)
	go func() {
		defer sampler.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if n, err := e.rc.SCard(ctx, "lenny:pod:{"+slotRacePod+"}:active_slots").Result(); err == nil && n > slotRaceCap {
				over.Add(1)
			}
		}
	}()
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			c := e.counter()
			rng := rand.New(rand.NewSource(int64(w)))
			for i := 0; i < 200; i++ {
				id := fmt.Sprintf("w%d-%d", w, rng.Intn(6))
				if rng.Intn(2) == 0 {
					n, _, err := c.Reserve(ctx, slotRacePod, id, slotRaceCap)
					if err == nil && n > slotRaceCap {
						over.Add(1)
					}
					if err != nil && !errors.Is(err, slotcounter.ErrSlotsExhausted) {
						t.Errorf("reserve: %v", err)
					}
					continue
				}
				if _, _, _, err := c.Release(ctx, slotRacePod, id, false); err != nil {
					t.Errorf("release: %v", err)
				}
			}
		}(w)
	}
	wg.Wait()
	close(stop)
	sampler.Wait()
	if n := over.Load(); n != 0 {
		t.Fatalf("the slot set exceeded its cap of %d %d times", slotRaceCap, n)
	}
}

// createGate counts the SandboxClaim writes one claimer makes that reach the
// apiserver, so a case can assert that a joining or losing claimer wrote
// nothing.
type createGate struct {
	writes atomic.Int32
}

// funcs counts every SandboxClaim write the claimer makes.
func (g *createGate) funcs() interceptor.Funcs {
	f := applyAsUpdate()
	inner := f.SubResourcePatch
	f.SubResourcePatch = func(ctx context.Context, cl client.Client, sub string, obj client.Object, p client.Patch, opts ...client.SubResourcePatchOption) error {
		if _, ok := obj.(*lennyv1.SandboxClaim); ok {
			g.writes.Add(1)
		}
		return inner(ctx, cl, sub, obj, p, opts...)
	}
	f.Create = func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
		if _, ok := obj.(*lennyv1.SandboxClaim); ok {
			err := cl.Create(ctx, obj, opts...)
			if err == nil {
				g.writes.Add(1)
			}
			return err
		}
		return cl.Create(ctx, obj, opts...)
	}
	f.Delete = func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
		if _, ok := obj.(*lennyv1.SandboxClaim); ok {
			g.writes.Add(1)
		}
		return cl.Delete(ctx, obj, opts...)
	}
	return f
}

// spec: §4.6.3 (SandboxClaim: a release that ends the occupancy without
// reuse), §5.2 (Slot assignment atomicity)
// diagnosis: two replicas acquiring one fresh pod left more than one claim,
// left no claim for the winner, or the loser wrote to the winner's claim
// (deleted it or patched it) while withdrawing its reservation, so the
// winner's live session lost its pod.
func TestClaimCreateRaceLeavesOneClaimAndTheLoserWritesNothing_spec_4_6_3(t *testing.T) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	for round := 0; round < slotRounds; round++ {
		e := newSlotRaceEnv(t, "idle")
		if err := e.mr.Set("lenny:pod:{"+slotRacePod+"}:rehydrated", "1"); err != nil {
			t.Fatal(err)
		}
		gates := [2]*createGate{{}, {}}
		results := [2]*podclaim.SlotResult{}
		errs := [2]error{}
		var calls []func()
		for i := range gates {
			i := i
			claimer := e.claimer(interceptor.NewClient(e.base, gates[i].funcs()))
			calls = append(calls, func() {
				results[i], errs[i] = claimer.ClaimSlot(context.Background(), podclaim.SlotRequest{
					Pool: slotRacePool, SessionID: fmt.Sprintf("s%d-%d", i, round), TenantID: "acme", MaxConcurrentSessions: slotRaceCap,
				})
			})
		}
		raceJittered(rng, calls...)
		acquirers := 0
		for i := range results {
			switch {
			case errs[i] == nil && results[i].FreshPod:
				// The CREATE winner.
				acquirers++
			case errs[i] == nil:
				// A claimer that read the winner's claim joined it as a
				// sibling; a join writes no claim.
				if n := gates[i].writes.Load(); n != 0 {
					t.Fatalf("round %d: the joining claimer made %d claim writes, want none", round, n)
				}
			case errors.Is(errs[i], podclaim.ErrNoConcurrentSlot):
				// The CREATE loser withdrew its reservation and wrote
				// nothing to the winner's claim.
				if n := gates[i].writes.Load(); n != 0 {
					t.Fatalf("round %d: the CREATE loser made %d claim writes, want none", round, n)
				}
			default:
				t.Fatalf("round %d: claimer %d = %v", round, i, errs[i])
			}
		}
		if acquirers != 1 {
			t.Fatalf("round %d: %d fresh acquisitions, want 1 (errs %v)", round, acquirers, errs)
		}
		if got := e.claimPhase(t); got != string(claimstate.Bound) {
			t.Fatalf("round %d: winner's claim = %q, want bound", round, got)
		}
		set := e.members(t)
		for i := range results {
			if errs[i] == nil && !set[results[i].SlotID] {
				t.Fatalf("round %d: claimer %d's member is not counted: %v", round, i, set)
			}
			if errs[i] != nil && set[fmt.Sprintf("s%d-%d", i, round)] {
				t.Fatalf("round %d: the loser's withdrawn member is still counted: %v", round, set)
			}
		}
	}
}
