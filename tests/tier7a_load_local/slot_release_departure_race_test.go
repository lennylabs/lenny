// SPDX-License-Identifier: MIT

//go:build load_local

package tier7a_load_local_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/slotreaper"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
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

// reapFixture is a binder whose dial reaches a real adapter holding no
// session, so a Shutdown answers absent with a clean exit, serialized per
// session by the adapter, and a session store holding the rows the reaper
// reads.
type reapFixture struct {
	*slotRaceEnv
	binder *podsession.Binder
	store  *memstore.Store
}

func newReapFixture(t *testing.T) *reapFixture {
	t.Helper()
	e := newSlotRaceEnv(t, "claimed")
	srv := adapter.New("reap-race")
	srv.WorkspaceBase = t.TempDir()
	lis := bufconn.Listen(1 << 20)
	gs := adapter.NewGRPCServer(srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	return &reapFixture{
		slotRaceEnv: e,
		store:       memstore.New(),
		binder: &podsession.Binder{
			Client: e.kube, Namespace: slotRaceNS, AdapterPort: 50051,
			AcceptedVersions: []string{adapter.ProtocolVersionV1}, SlotCounter: e.counter(),
			DialAdapter: func(string) (*adapterclient.Client, error) {
				return adapterclient.Dial("passthrough:///bufnet",
					grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
					grpc.WithTransportCredentials(insecure.NewCredentials()))
			},
		},
	}
}

// seedRow writes a per-slot session row naming the pod, terminal at
// terminatedAt when that is non-zero.
func (f *reapFixture) seedRow(t *testing.T, id string, terminatedAt time.Time) {
	t.Helper()
	ctx := context.Background()
	if err := f.store.Create(ctx, sessionstore.Session{
		ID: id, TenantID: "acme", State: session.StateRunning, PodAssignment: slotRacePod, ScrubPolicy: "best-effort-per-slot",
	}); err != nil {
		t.Fatal(err)
	}
	if terminatedAt.IsZero() {
		return
	}
	if _, err := f.store.Update(ctx, "acme", id, func(r *sessionstore.Session) error {
		r.State = session.StateCompleted
		sessionstore.MarkTerminated(r, session.StateRunning, terminatedAt)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// spec: §5.2 (Slot assignment atomicity), §6.2 (leaked slot semantics)
// diagnosis: a stale-slot reaper pass raced a late release of the same
// terminal session on another replica and removed the member twice, lost the
// late leaked release's leaked: member, removed the live sibling's member,
// emptied the set, or disposed of the claim.
func TestStaleSlotReaperRacesALateRelease_spec_5_2(t *testing.T) {
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	for round := 0; round < slotRounds/2; round++ {
		f := newReapFixture(t)
		stale, live := fmt.Sprintf("00000000-0000-4000-8000-%012d", round), "00000000-0000-4000-8000-999999999999"
		f.seedBoundClaim(t, stale, live)
		f.seedRow(t, stale, time.Now().Add(-time.Hour))
		f.seedRow(t, live, time.Time{})
		reaper, err := slotreaper.New(slotreaper.Config{
			Pods: f.store, Members: f.counter(), Sessions: f.store, Releaser: f.binder,
			Rate: 100, Interval: time.Second, TerminalAge: time.Minute,
		})
		if err != nil {
			t.Fatal(err)
		}
		leaked := rng.Intn(2) == 0
		late := f.claimer(f.kube)
		raceJittered(rng,
			func() { reaper.Pass(context.Background()) },
			func() {
				if _, err := late.ReleaseSlot(context.Background(), slotRacePod, stale, false, leaked); err != nil {
					t.Errorf("round %d: late release: %v", round, err)
				}
			})
		set := f.members(t)
		if set[stale] || !set[live] {
			t.Fatalf("round %d: set %v, want the stale member gone and the live sibling counted", round, set)
		}
		if leaked != set[slotcounter.LeakedMember(stale)] {
			t.Fatalf("round %d: leaked late release %v, set %v", round, leaked, set)
		}
		if n := f.log.Removals(slotRacePod, stale); n != 1 {
			t.Fatalf("round %d: removals of the stale member = %d, want 1", round, n)
		}
		if got := f.claimPhase(t); got != string(claimstate.Bound) {
			t.Fatalf("round %d: claim = %q, want bound", round, got)
		}
	}
}
