// SPDX-License-Identifier: MIT

//go:build load_local

package tier7a_load_local_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	claimstate "github.com/lennylabs/lenny/pkg/sandboxclaim/state"
)

// spec: §5.2 (Slot assignment atomicity), §4.6.3 (SandboxClaim: a release
// that ends the occupancy without reuse)
// diagnosis: a reservation racing the release that empties the set ended
// outside the accepted departure: the claim survived an empty set, the
// refused reservation left its member counted, or a member was removed
// twice. The accepted outcomes are a refusal, a join onto the live claim, or
// a join onto a pod whose claim the release deleted, which ends that session
// on the pod-failure path.
func TestEmptyingReleaseRacesAReservation_spec_5_2(t *testing.T) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	outcomes := map[string]int{}
	for round := 0; round < slotRounds; round++ {
		e := newSlotRaceEnv(t, "claimed")
		e.seedBoundClaim(t, "sess-last")
		releaser, joiner := e.claimer(e.kube), e.claimer(e.kube)
		joinID := fmt.Sprintf("sess-join-%d", round)
		var joinErr error
		raceJittered(rng,
			func() {
				if _, err := releaser.ReleaseSlot(context.Background(), slotRacePod, "sess-last", false, false); err != nil {
					t.Errorf("round %d: release: %v", round, err)
				}
			},
			func() {
				_, joinErr = joiner.ClaimSlot(context.Background(), podclaim.SlotRequest{
					Pool: slotRacePool, SessionID: joinID, TenantID: "acme", MaxConcurrentSessions: slotRaceCap,
				})
			})
		set, phase := e.members(t), e.claimPhase(t)
		switch {
		case errors.Is(joinErr, podclaim.ErrNoConcurrentSlot) && phase == "" && len(set) == 0:
			outcomes["refused"]++
		case joinErr == nil && phase == string(claimstate.Bound) && len(set) == 1 && set[joinID]:
			outcomes["joined the live claim"]++
		case joinErr == nil && phase == "" && len(set) == 1 && set[joinID]:
			outcomes["joined a deleted claim (pod-failure path)"]++
		default:
			t.Fatalf("round %d: join err %v, claim %q, set %v: outside the accepted outcomes", round, joinErr, phase, set)
		}
		if over := e.log.OverRemoved(); len(over) != 0 {
			t.Fatalf("round %d: removed more than once: %v", round, over)
		}
	}
	t.Logf("outcomes: %v", outcomes)
}

// spec: §4.6.1 (Orphaned SandboxClaim detection), §5.2 (Slot assignment
// atomicity)
// diagnosis: the by-name delete a resume's claim makes after its bound
// status write fails raced a same-tenant slot join and ended outside the
// accepted departure: the joined sibling kept a member on a pod with a live
// claim it does not hold, two claims existed, or a refusal left its member
// counted. A sibling that joined the claim before the delete is on the
// pod-failure path, which fails closed.
func TestResumeClaimDeleteRacesASlotJoin_spec_5_2(t *testing.T) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	outcomes := map[string]int{}
	for round := 0; round < slotRounds; round++ {
		e := newSlotRaceEnv(t, "idle")
		if err := e.mr.Set("lenny:pod:{"+slotRacePod+"}:rehydrated", "1"); err != nil {
			t.Fatal(err)
		}
		delay := time.Duration(rng.Intn(400)) * time.Microsecond
		failing := applyAsUpdate()
		failing.SubResourcePatch = func(ctx context.Context, cl client.Client, sub string, obj client.Object, p client.Patch, opts ...client.SubResourcePatchOption) error {
			if _, ok := obj.(*lennyv1.SandboxClaim); ok {
				time.Sleep(delay)
				return errors.New("injected bound status failure")
			}
			return cl.SubResource(sub).Patch(ctx, obj, p, opts...)
		}
		resumer := &podclaim.Claimer{Client: interceptor.NewClient(e.base, failing), Namespace: slotRaceNS}
		joiner := e.claimer(e.kube)
		joinID := fmt.Sprintf("sess-join-%d", round)
		var res *podclaim.SlotResult
		var joinErr, claimErr error
		raceJittered(rng,
			func() {
				_, claimErr = resumer.Claim(context.Background(), podclaim.ClaimRequest{
					Pool: slotRacePool, SessionID: "sess-resume", TenantID: "acme", KeepsRuntime: true,
				})
			},
			func() {
				res, joinErr = joiner.ClaimSlot(context.Background(), podclaim.SlotRequest{
					Pool: slotRacePool, SessionID: joinID, TenantID: "acme", MaxConcurrentSessions: slotRaceCap,
				})
			})
		if claimErr == nil {
			t.Fatalf("round %d: the resume's claim succeeded through a failing status write", round)
		}
		set, phase := e.members(t), e.claimPhase(t)
		switch {
		case joinErr == nil && res.FreshPod && phase == string(claimstate.Bound) && set[joinID]:
			outcomes["the join acquired the pod"]++
		case joinErr == nil && !res.FreshPod && phase == "" && set[joinID]:
			outcomes["joined the deleted claim (pod-failure path)"]++
		case joinErr != nil && len(set) == 0:
			outcomes["refused"]++
		default:
			t.Fatalf("round %d: join %+v err %v, claim %q, set %v: outside the accepted outcomes", round, res, joinErr, phase, set)
		}
	}
	t.Logf("outcomes: %v", outcomes)
}

// spec: §5.2 (Recycle lifecycle), §4.6.3 (SandboxClaim: a release that ends
// the occupancy without reuse)
// diagnosis: on an exclusive recycling pod, a peer's recycle and a
// successor's rebind raced a writer's by-name claim delete and left the
// successor holding a claim after the delete. The accepted outcome is that
// the writer's delete always ends the claim, so a successor that rebound
// first is on its own pod-failure path, which fails closed.
func TestRecycleRebindRacesAByNameDelete_spec_5_2(t *testing.T) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	outcomes := map[string]int{}
	for round := 0; round < slotRounds; round++ {
		e := newSlotRaceEnv(t, "claimed")
		e.seedBoundClaim(t)
		ctx := context.Background()
		name := podclaim.ClaimName(slotRacePod)
		rebound := false
		// The writer's delete lands anywhere from before the recycle to
		// after the rebind.
		deleteAfter := time.Duration(rng.Intn(3000)) * time.Microsecond
		raceJittered(rng,
			func() {
				if err := podclaim.WriteRecyclingStatus(ctx, e.kube, slotRaceNS, name, time.Now); err != nil {
					return
				}
				_, _ = podclaim.WriteReservedStatus(ctx, e.kube, slotRaceNS, name, time.Minute, time.Now)
			},
			func() {
				for i := 0; i < 200; i++ {
					switch e.claimPhaseOrGone(ctx) {
					case "":
						return
					case string(claimstate.Reserved):
						rebound = podclaim.WriteRebindStatus(ctx, e.kube, slotRaceNS, name, time.Now) == nil
						return
					}
					time.Sleep(20 * time.Microsecond)
				}
			},
			func() {
				time.Sleep(deleteAfter)
				if err := podclaim.DeleteClaim(ctx, e.kube, slotRaceNS, slotRacePod); err != nil {
					t.Errorf("round %d: by-name delete: %v", round, err)
				}
			})
		if phase := e.claimPhase(t); phase != "" {
			t.Fatalf("round %d: claim = %q after the by-name delete, want it gone", round, phase)
		}
		if rebound {
			outcomes["successor rebound, then lost the claim (pod-failure path)"]++
		} else {
			outcomes["successor never rebound"]++
		}
	}
	t.Logf("outcomes: %v", outcomes)
}

// claimPhaseOrGone returns the claim phase without failing the test.
func (e *slotRaceEnv) claimPhaseOrGone(ctx context.Context) string {
	var claim lennyv1.SandboxClaim
	if err := e.kube.Get(ctx, client.ObjectKey{Namespace: slotRaceNS, Name: podclaim.ClaimName(slotRacePod)}, &claim); err != nil {
		return ""
	}
	return claim.Status.Phase
}
