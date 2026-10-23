// SPDX-License-Identifier: MIT

package podclaim_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
)

// activeSlots reads the cardinality of the pod's §5.2 slot set directly, so
// a case can assert the occupancy without perturbing it.
func activeSlots(t *testing.T, mr *miniredis.Miniredis, sandboxName string) string {
	t.Helper()
	return strconv.Itoa(len(slotMembers(t, mr, sandboxName)))
}

// slotMembers returns the sorted members of the pod's §5.2 slot set.
func slotMembers(t *testing.T, mr *miniredis.Miniredis, sandboxName string) []string {
	t.Helper()
	key := "lenny:pod:{" + sandboxName + "}:active_slots"
	if !mr.Exists(key) {
		return nil
	}
	v, err := mr.Members(key)
	if err != nil {
		t.Fatalf("read %s: %v", key, err)
	}
	sort.Strings(v)
	return v
}

// reserveOnPodFixture boots the envtest harness with one concurrent-mode
// Sandbox, its backing agent Pod, and a miniredis-backed slot counter, and
// returns a claimer whose kube client is wrapped by patchFail. patchFail is
// consulted on every Pod patch and may return an error to stand in for a
// transient apiserver fault; nil lets the patch through.
func reserveOnPodFixture(t *testing.T, patchFail func(body string) error) (*podclaim.SlotClaimer, client.WithWatch, *miniredis.Miniredis) {
	t.Helper()
	base := newEnvtestClient(t, concurrentSandbox("sbx-1", "claimed", "acme"))
	mustCreate(t, base, agentPod("sbx-1", ""))

	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	counter := slotcounter.New(rc)

	c := base
	if patchFail != nil {
		c = interceptor.NewClient(base, interceptor.Funcs{
			Patch: func(ctx context.Context, w client.WithWatch, obj client.Object, p client.Patch, opts ...client.PatchOption) error {
				data, err := p.Data(obj)
				if err != nil {
					return err
				}
				if perr := patchFail(string(data)); perr != nil {
					return perr
				}
				return w.Patch(ctx, obj, p, opts...)
			},
		})
	}
	return &podclaim.SlotClaimer{Client: c, Namespace: testNS, Counter: counter}, base, mr
}

// seedPerPodClaim creates the live per-pod SandboxClaim the resume's connect
// leaves behind, in binding state `bound`, without reserving any slot.
func seedPerPodClaim(t *testing.T, c client.Client, sandboxName, tenantID string) *lennyv1.SandboxClaim {
	t.Helper()
	ctx := context.Background()
	claim := &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "claim-" + sandboxName, Namespace: testNS},
		Spec:       lennyv1.SandboxClaimSpec{SandboxRef: sandboxName, TenantID: tenantID},
	}
	if err := c.Create(ctx, claim); err != nil {
		t.Fatalf("seed per-pod claim for %s: %v", sandboxName, err)
	}
	if err := podclaim.WriteBoundStatus(ctx, c, testNS, claim.Name); err != nil {
		t.Fatalf("seed bound status for %s: %v", sandboxName, err)
	}
	return claim
}

// spec: 5.2 (atomic slot reservation on a chosen pod), 7.1 (resume onto a replacement pod)
// diagnosis: ReserveSlotOnPod did not reserve on the pod it was named.
// The resume's pod is fixed by the whole-pod claim its connect created, so a
// reservation that scans the pool would increment a different pod's counter
// and the compensating release would have no way to name the pod the scan
// chose.
func TestReserveSlotOnPodReservesOnTheNamedPod_spec_5_2(t *testing.T) {
	claimer, c, mr := reserveOnPodFixture(t, nil)
	seedPerPodClaim(t, c, "sbx-1", "acme")

	res, err := claimer.ReserveSlotOnPod(context.Background(), "sbx-1", slotReq("sess-1", "acme", 4))
	if err != nil {
		t.Fatalf("ReserveSlotOnPod: %v", err)
	}
	if res.SandboxName != "sbx-1" {
		t.Errorf("reserved on %q, want sbx-1", res.SandboxName)
	}
	if res.SlotID != "sess-1" {
		t.Errorf("SlotID = %q, want the session identifier", res.SlotID)
	}
	if res.FreshPod {
		t.Error("FreshPod = true; the resume reserves against the claim connect already created")
	}
	if got := activeSlots(t, mr, "sbx-1"); got != "1" {
		t.Errorf("active_slots = %s, want 1", got)
	}
	if !podClaimExists(t, c, "sbx-1") {
		t.Error("the per-pod claim connect created was disposed by the reservation")
	}
}

// spec: 4.6.3 (SandboxClaim: a release that ends the occupancy without reuse), 5.2 (Slot assignment atomicity)
// diagnosis: a reservation whose tenant or uptime stamp failed did not roll
// its member back through the keyed release, or the rollback's disposition
// was wrong. The rollback runs on a detached context after the attempt's
// context is cancelled, removes the session's member, deletes the per-pod
// claim only when that removal empties the set, and keeps the claim when a
// sibling's member remains.
func TestReserveSlotOnPodRollbackDisposesAtZeroOnly_spec_4_6_3(t *testing.T) {
	for _, stamp := range []string{"tenant-id", "max-pod-uptime-seconds"} {
		for _, sibling := range []bool{false, true} {
			name := stamp + "/alone"
			if sibling {
				name = stamp + "/with sibling"
			}
			t.Run(name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				claimer, c, mr := reserveOnPodFixture(t, func(body string) error {
					if strings.Contains(body, stamp) {
						cancel()
						return errors.New("apiserver unreachable")
					}
					return nil
				})
				seedPerPodClaim(t, c, "sbx-1", "acme")
				if sibling {
					if _, _, err := claimer.Counter.Reserve(context.Background(), "sbx-1", "sib", 4); err != nil {
						t.Fatalf("seed sibling: %v", err)
					}
				}
				req := slotReq("sess-1", "acme", 4)
				req.MaxPodUptimeSeconds = 3600
				if _, err := claimer.ReserveSlotOnPod(ctx, "sbx-1", req); err == nil {
					t.Fatal("ReserveSlotOnPod err = nil, want the stamp failure surfaced")
				}
				want := "[]"
				if sibling {
					want = "[sib]"
				}
				if got := fmt.Sprint(slotMembers(t, mr, "sbx-1")); got != want {
					t.Errorf("slot members = %s, want %s (the rollback removes only the session's member)", got, want)
				}
				if exists := podClaimExists(t, c, "sbx-1"); exists != sibling {
					t.Errorf("per-pod claim exists = %v, want %v: delete only when the rollback empties the set", exists, sibling)
				}
			})
		}
	}
}

// spec: 5.2 (the per-pod maxConcurrentSessions bound)
// diagnosis: a pod already at its bound admitted another slot, or reported
// the exhaustion as a nil result with a nil error. ReserveSlotOnPod has no
// next candidate to move to, so the conflict is the ErrNoConcurrentSlot
// sentinel; dropping it would run the resumed session on a pod that counts
// one fewer occupant than it holds.
func TestReserveSlotOnPodReturnsErrNoConcurrentSlotAtTheBound_spec_5_2(t *testing.T) {
	claimer, c, mr := reserveOnPodFixture(t, nil)
	seedPerPodClaim(t, c, "sbx-1", "acme")
	// Fill the pod to its bound of 2 with co-tenant occupancy.
	for i := 0; i < 2; i++ {
		if _, _, err := claimer.Counter.Reserve(context.Background(), "sbx-1", fmt.Sprintf("co-%d", i), 2); err != nil {
			t.Fatalf("seed occupancy %d: %v", i, err)
		}
	}

	res, err := claimer.ReserveSlotOnPod(context.Background(), "sbx-1", slotReq("sess-1", "acme", 2))
	if !errors.Is(err, podclaim.ErrNoConcurrentSlot) {
		t.Fatalf("err = %v (res %+v), want ErrNoConcurrentSlot", err, res)
	}
	if got := activeSlots(t, mr, "sbx-1"); got != "2" {
		t.Errorf("active_slots = %s, want the pod left at its bound of 2", got)
	}
}

// spec: 4.6.1 (the per-pod claim is the acquisition guard), 5.2
// diagnosis: ReserveSlotOnPod reserved against a pod whose per-pod claim is
// absent or terminal. A concurrent orphan-GC reclaim can remove or retire
// the claim in the window between the resume's connect and its reservation;
// reserving there holds an increment against a claim that will never be
// reconciled, on a pod the WarmPoolController is retiring.
func TestReserveSlotOnPodFailsClosedWhenThePerPodClaimIsGone_spec_4_6_1(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		claimer, _, mr := reserveOnPodFixture(t, nil)
		// No per-pod claim is seeded: the reclaim already removed it.
		if _, err := claimer.ReserveSlotOnPod(context.Background(), "sbx-1",
			slotReq("sess-1", "acme", 4)); err == nil {
			t.Fatal("ReserveSlotOnPod err = nil, want a refusal with no live per-pod claim")
		}
		if got := activeSlots(t, mr, "sbx-1"); got != "0" {
			t.Errorf("active_slots = %s, want 0 (the counter must not be touched)", got)
		}
	})

	for _, phase := range []string{"released", "failed"} {
		t.Run("terminal_"+phase, func(t *testing.T) {
			claimer, c, mr := reserveOnPodFixture(t, nil)
			claim := seedPerPodClaim(t, c, "sbx-1", "acme")
			var stored lennyv1.SandboxClaim
			if err := c.Get(context.Background(),
				client.ObjectKey{Namespace: testNS, Name: claim.Name}, &stored); err != nil {
				t.Fatalf("get claim: %v", err)
			}
			stored.Status.Phase = phase
			if err := c.Status().Update(context.Background(), &stored); err != nil {
				t.Fatalf("patch claim phase to %s: %v", phase, err)
			}

			if _, err := claimer.ReserveSlotOnPod(context.Background(), "sbx-1",
				slotReq("sess-1", "acme", 4)); err == nil {
				t.Fatalf("ReserveSlotOnPod err = nil, want a refusal on a %s claim", phase)
			}
			if got := activeSlots(t, mr, "sbx-1"); got != "0" {
				t.Errorf("active_slots = %s, want 0 (the counter must not be touched)", got)
			}
			if !podClaimExists(t, c, "sbx-1") {
				t.Error("the refusal disposed the claim; it must leave the pod untouched")
			}
		})
	}
}

// spec: 12.4 (the counter is the intra-pod capacity gate), 5.2
// diagnosis: ReserveSlotOnPod was entered without the refusals ClaimSlot
// states in its own body. The gateway constructs no counter when no Redis
// client is configured, and a concurrent-mode pool on such a deployment
// reaches this entry point; reserveSlot dereferences the counter at its
// first Redis call, so a missing guard panics the gateway where every other
// counter-touching entry point fails closed.
func TestReserveSlotOnPodFailsClosedWithNoSlotCounter_spec_12_4(t *testing.T) {
	t.Run("nil counter", func(t *testing.T) {
		c := newEnvtestClient(t, concurrentSandbox("sbx-1", "claimed", "acme"))
		mustCreate(t, c, agentPod("sbx-1", ""))
		seedPerPodClaim(t, c, "sbx-1", "acme")
		claimer := &podclaim.SlotClaimer{Client: c, Namespace: testNS}

		if _, err := claimer.ReserveSlotOnPod(context.Background(), "sbx-1",
			slotReq("sess-1", "acme", 4)); err == nil {
			t.Fatal("ReserveSlotOnPod err = nil on a nil counter, want a refusal")
		}
		if !podClaimExists(t, c, "sbx-1") {
			t.Error("the refusal disposed the per-pod claim")
		}
		pod := getPod(t, c, "sbx-1")
		if _, ok := pod.Labels[podclaim.LabelTenant]; ok {
			t.Error("the refusal stamped the agent pod; it must fail before any write")
		}
	})

	t.Run("bound below one", func(t *testing.T) {
		claimer, c, mr := reserveOnPodFixture(t, nil)
		seedPerPodClaim(t, c, "sbx-1", "acme")

		if _, err := claimer.ReserveSlotOnPod(context.Background(), "sbx-1",
			slotReq("sess-1", "acme", 0)); err == nil {
			t.Fatal("ReserveSlotOnPod err = nil on maxConcurrentSessions 0, want a refusal")
		}
		if got := activeSlots(t, mr, "sbx-1"); got != "0" {
			t.Errorf("active_slots = %s, want 0", got)
		}
	})
}
