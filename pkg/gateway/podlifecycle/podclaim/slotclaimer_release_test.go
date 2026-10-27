// SPDX-License-Identifier: MIT

package podclaim_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
)

// claimWrites counts the SandboxClaim writes and deletes an interceptor saw
// while armed.
type claimWrites struct {
	armed  atomic.Bool
	writes atomic.Int32
}

func (w *claimWrites) note(obj client.Object) {
	if _, ok := obj.(*lennyv1.SandboxClaim); ok && w.armed.Load() {
		w.writes.Add(1)
	}
}

// countingFuncs returns interceptor funcs that count claim writes and
// deletes into w, and run create for every Create.
func countingFuncs(w *claimWrites, create func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error) interceptor.Funcs {
	return interceptor.Funcs{
		Create: create,
		Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			w.note(obj)
			return c.Delete(ctx, obj, opts...)
		},
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			w.note(obj)
			return c.Update(ctx, obj, opts...)
		},
		Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, p client.Patch, opts ...client.PatchOption) error {
			w.note(obj)
			return c.Patch(ctx, obj, p, opts...)
		},
		SubResourcePatch: func(ctx context.Context, c client.Client, sub string, obj client.Object, p client.Patch, opts ...client.SubResourcePatchOption) error {
			w.note(obj)
			return c.SubResource(sub).Patch(ctx, obj, p, opts...)
		},
	}
}

// setClaimPhase writes phase onto the named claim's status.
func setClaimPhase(t *testing.T, c client.Client, name, phase string) {
	t.Helper()
	ns := testNamespace(t)
	ctx := context.Background()
	var stored lennyv1.SandboxClaim
	if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &stored); err != nil {
		t.Fatalf("get claim %s: %v", name, err)
	}
	stored.Status.Phase = phase
	if err := c.Status().Update(ctx, &stored); err != nil {
		t.Fatalf("set claim %s phase %s: %v", name, phase, err)
	}
}

// counterOn returns a counter on a fresh miniredis and the miniredis.
func counterOn(t *testing.T) (*slotcounter.Counter, *miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = rc.Close() })
	return slotcounter.New(rc), mr, rc
}

// spec: 5.2 (Slot assignment atomicity), 4.6.3 (SandboxClaim: a release that ends the occupancy without reuse)
// diagnosis: a duplicate release of a session whose member an earlier
// release already removed ran the occupancy-zero disposition and deleted the
// claim a later occupancy of the pod holds. The disposition runs only on the
// release that removed the member.
func TestReleaseSlotDuplicateLeavesLaterClaim_spec_5_2(t *testing.T) {
	ns := testNamespace(t)
	claimer, c, counter := slotClaimerFor(t, concurrentSandbox(ns, "sbx-1", "claimed", "acme"))
	seedOccupiedPod(t, c, counter, "sbx-1", "acme", 1)
	ctx := context.Background()
	if _, err := claimer.ReleaseSlot(ctx, "sbx-1", "occ-0", false, false); err != nil {
		t.Fatalf("first release: %v", err)
	}
	if podClaimExists(t, c, "sbx-1") {
		t.Fatal("precondition: the first release ends the occupancy")
	}
	// A later occupancy acquires the pod with its own claim.
	seedPerPodClaim(t, c, "sbx-1", "acme")
	recycled, err := claimer.ReleaseSlot(ctx, "sbx-1", "occ-0", false, false)
	if err != nil || recycled {
		t.Fatalf("duplicate release = (%v, %v), want (false, nil)", recycled, err)
	}
	if !podClaimExists(t, c, "sbx-1") {
		t.Fatal("a duplicate release deleted the claim of a later occupancy")
	}
}

// spec: 5.2 (Slot assignment atomicity)
// diagnosis: a slot-set release failure was not marked with
// ErrSlotCounterRelease, so the binder could not tell a repeatable counter
// failure from a disposition failure; or a disposition failure carried the
// sentinel and would be repeated after the member was gone.
func TestReleaseSlotErrorClassification_spec_5_2(t *testing.T) {
	t.Run("counter failure carries the sentinel", func(t *testing.T) {
		ns := testNamespace(t)
		c := newEnvtestClient(t, concurrentSandbox(ns, "sbx-1", "claimed", "acme"))
		counter, mr, _ := counterOn(t)
		mr.Close()
		claimer := &podclaim.SlotClaimer{Client: c, Namespace: ns, Counter: counter}
		_, err := claimer.ReleaseSlot(context.Background(), "sbx-1", "s1", false, false)
		if !errors.Is(err, podclaim.ErrSlotCounterRelease) {
			t.Fatalf("err = %v, want ErrSlotCounterRelease", err)
		}
	})
	t.Run("disposition failure carries no sentinel", func(t *testing.T) {
		ns := testNamespace(t)
		base := newEnvtestClient(t, concurrentSandbox(ns, "sbx-1", "claimed", "acme"))
		counter, mr, _ := counterOn(t)
		seedOccupiedPod(t, base, counter, "sbx-1", "acme", 1)
		c := interceptor.NewClient(base, interceptor.Funcs{
			Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
				return errors.New("apiserver unreachable")
			},
		})
		claimer := &podclaim.SlotClaimer{Client: c, Namespace: ns, Counter: counter}
		_, err := claimer.ReleaseSlot(context.Background(), "sbx-1", "occ-0", false, false)
		if err == nil || errors.Is(err, podclaim.ErrSlotCounterRelease) {
			t.Fatalf("err = %v, want a disposition error without ErrSlotCounterRelease", err)
		}
		if got := activeSlots(t, mr, "sbx-1"); got != "0" {
			t.Fatalf("active slots = %s, want 0: the member is removed before the disposition", got)
		}
	})
}

// spec: 5.2 (Post-recovery rehydration atomicity)
// diagnosis: a release that rehydrated the pod's set did not record the
// rehydration event, or recorded it under a pool other than the Sandbox's
// warm-pool label, which is the label a reservation records for the pod.
func TestReleaseSlotRecordsRehydrationWithPoolLabel_spec_5_2(t *testing.T) {
	ns := testNamespace(t)
	c := newEnvtestClient(t, concurrentSandbox(ns, "sbx-1", "claimed", "acme"))
	counter, _, _ := counterOn(t)
	var got []string
	claimer := &podclaim.SlotClaimer{
		Client: c, Namespace: ns, Counter: counter,
		OnRehydrate: func(pod, pool string) { got = append(got, pod+"/"+pool) },
	}
	if _, err := claimer.ReleaseSlot(context.Background(), "sbx-1", "s1", false, false); err != nil {
		t.Fatalf("ReleaseSlot: %v", err)
	}
	// A counter with no source does not report a rehydration event.
	if len(got) != 0 {
		t.Fatalf("rehydration events = %v, want none without a seed source", got)
	}
	src := &staticSource{}
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	claimer.Counter = slotcounter.New(rc, slotcounter.WithSlotSource(src))
	if _, err := claimer.ReleaseSlot(context.Background(), "sbx-1", "s1", false, false); err != nil {
		t.Fatalf("ReleaseSlot: %v", err)
	}
	if fmt.Sprint(got) != "[sbx-1/"+testPool+"]" {
		t.Fatalf("rehydration events = %v, want [sbx-1/%s]", got, testPool)
	}
}

// staticSource is a SlotSource with no slot holders.
type staticSource struct{}

func (staticSource) GetActiveSlotsByPod(context.Context, string) ([]string, error) { return nil, nil }

func (staticSource) ListPodsWithActiveSlots(context.Context) ([]string, error) { return nil, nil }

// spec: 4.6.3 (SandboxClaim: a release that ends the occupancy without reuse), 5.2 (Slot assignment atomicity)
// diagnosis: the rollback after a fresh claim's `bound` write failed
// deleted the fresh claim unconditionally, ending the occupancy of a sibling
// that reserved onto the claim in the window, or ran on the cancelled
// attempt context and left the member counted. The rollback removes the
// session's member on a detached context and deletes the claim only when the
// set is then empty.
func TestFreshClaimBoundWriteRollback_spec_4_6_3(t *testing.T) {
	for _, sibling := range []bool{false, true} {
		name := "alone"
		if sibling {
			name = "sibling joined"
		}
		t.Run(name, func(t *testing.T) {
			ns := testNamespace(t)
			base := newEnvtestClient(t, concurrentSandbox(ns, "sbx-1", "idle", ""))
			counter, mr, _ := counterOn(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c := interceptor.NewClient(base, interceptor.Funcs{
				SubResourcePatch: func(pctx context.Context, cl client.Client, sub string, obj client.Object, p client.Patch, opts ...client.SubResourcePatchOption) error {
					if _, ok := obj.(*lennyv1.SandboxClaim); ok {
						if sibling {
							if _, _, err := counter.Reserve(context.Background(), "sbx-1", "sib", 4); err != nil {
								t.Errorf("sibling reserve: %v", err)
							}
						}
						cancel()
						return errors.New("apiserver unreachable")
					}
					return cl.SubResource(sub).Patch(pctx, obj, p, opts...)
				},
			})
			claimer := &podclaim.SlotClaimer{Client: c, Namespace: ns, Counter: counter}
			if _, err := claimer.ClaimSlot(ctx, slotReq("sess-1", "acme", 4)); err == nil {
				t.Fatal("ClaimSlot err = nil, want the bound-write failure")
			}
			want := "[]"
			if sibling {
				want = "[sib]"
			}
			if got := fmt.Sprint(slotMembers(t, mr, "sbx-1")); got != want {
				t.Fatalf("slot members = %s, want %s", got, want)
			}
			if exists := podClaimExists(t, base, "sbx-1"); exists != sibling {
				t.Fatalf("fresh claim exists = %v, want %v", exists, sibling)
			}
		})
	}
}

// spec: 4.6.3 (SandboxClaim: a release that ends the occupancy without reuse), 5.2 (Slot assignment atomicity)
// diagnosis: the withdrawal after a failed claim CREATE ran the
// occupancy-zero disposition and wrote or deleted the winner's claim. The
// withdrawal holds no occupancy of the pod, so it removes the session's
// member with a raw keyed release and makes no claim write or delete, even
// when it empties the set.
func TestCreateLoserWithdrawalLeavesWinnerClaim_spec_4_6_3(t *testing.T) {
	createErrs := map[string]func() error{
		"AlreadyExists": func() error {
			return apierrors.NewAlreadyExists(schema.GroupResource{Group: "lenny.dev", Resource: "sandboxclaims"}, "claim-sbx-1")
		},
		"Forbidden": func() error {
			return apierrors.NewForbidden(schema.GroupResource{Group: "lenny.dev", Resource: "sandboxclaims"}, "claim-sbx-1", errors.New("guard"))
		},
		"other": func() error { return errors.New("apiserver unreachable") },
	}
	for errName, mkErr := range createErrs {
		for _, phase := range []string{"bound", "recycling", "reserved"} {
			t.Run(errName+"/"+phase, func(t *testing.T) {
				ns := testNamespace(t)
				base := newEnvtestClient(t, concurrentSandbox(ns, "sbx-1", "idle", ""))
				counter, mr, _ := counterOn(t)
				w := &claimWrites{}
				var once sync.Once
				c := interceptor.NewClient(base, countingFuncs(w, func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
					claim, ok := obj.(*lennyv1.SandboxClaim)
					if !ok {
						return cl.Create(ctx, obj, opts...)
					}
					var err error
					once.Do(func() {
						// The winner's CREATE lands first.
						winner := claim.DeepCopy()
						winner.Spec.TenantID = "acme"
						if cerr := cl.Create(ctx, winner); cerr != nil {
							t.Errorf("winner create: %v", cerr)
						}
						setClaimPhase(t, cl, winner.Name, phase)
						w.armed.Store(true)
						err = mkErr()
					})
					return err
				}))
				claimer := &podclaim.SlotClaimer{Client: c, Namespace: ns, Counter: counter}
				_, _ = claimer.ClaimSlot(context.Background(), slotReq("sess-1", "acme", 4))
				if n := w.writes.Load(); n != 0 {
					t.Fatalf("claim writes after the CREATE failure = %d, want 0", n)
				}
				if got := slotMembers(t, mr, "sbx-1"); len(got) != 0 {
					t.Fatalf("slot members = %v, want none (the withdrawal removed the member)", got)
				}
				stored := getClaim(t, base, "claim-sbx-1")
				if stored.Status.Phase != phase {
					t.Fatalf("winner claim phase = %q, want %q", stored.Status.Phase, phase)
				}
			})
		}
	}
}

// spec: 5.2 (Slot assignment atomicity), 6.2 (leaked slot semantics)
// diagnosis: a rollback whose counter call failed either deleted the claim
// or replaced the reservation's own error with the rollback's. The member
// stays counted, which fails closed, the claim stays, and the caller sees the
// stamp failure.
func TestRollbackCounterFailureKeepsMember_spec_5_2(t *testing.T) {
	ns := testNamespace(t)
	base := newEnvtestClient(t, concurrentSandbox(ns, "sbx-1", "claimed", "acme"))
	mustCreate(t, base, agentPod(ns, "sbx-1", ""))
	seedPerPodClaim(t, base, "sbx-1", "acme")
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = rc.Close() })
	block := &blockScripts{}
	rc.AddHook(block)
	counter := slotcounter.New(rc)
	c := interceptor.NewClient(base, interceptor.Funcs{
		Patch: func(ctx context.Context, cl client.WithWatch, obj client.Object, p client.Patch, opts ...client.PatchOption) error {
			data, _ := p.Data(obj)
			if strings.Contains(string(data), "tenant-id") {
				block.on.Store(true)
				return errors.New("stamp refused")
			}
			return cl.Patch(ctx, obj, p, opts...)
		},
	})
	claimer := &podclaim.SlotClaimer{Client: c, Namespace: ns, Counter: counter}
	_, err := claimer.ReserveSlotOnPod(context.Background(), "sbx-1", slotReq("sess-1", "acme", 4))
	if err == nil || !strings.Contains(err.Error(), "stamp refused") {
		t.Fatalf("err = %v, want the stamp failure", err)
	}
	block.on.Store(false)
	if got := fmt.Sprint(slotMembers(t, mr, "sbx-1")); got != "[sess-1]" {
		t.Fatalf("slot members = %s, want [sess-1] (the failed rollback keeps the member)", got)
	}
	if !podClaimExists(t, base, "sbx-1") {
		t.Fatal("a rollback whose counter call failed deleted the claim")
	}
}

// blockScripts fails every script call while on is set.
type blockScripts struct{ on atomic.Bool }

func (b *blockScripts) DialHook(next redis.DialHook) redis.DialHook { return next }

func (b *blockScripts) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}

func (b *blockScripts) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		name := strings.ToLower(cmd.Name())
		if b.on.Load() && (name == "evalsha" || name == "eval") {
			err := errors.New("dial tcp: connect: connection refused")
			cmd.SetErr(err)
			return err
		}
		return next(ctx, cmd)
	}
}
