// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

const reclaimPod = "pod-a"

// reclaimAdapter serves only Shutdown, answering clean unless fail is set,
// and records the recycle flag of each request.
type reclaimAdapter struct {
	adapterv1.UnimplementedAdapterServer
	fail bool

	mu       sync.Mutex
	recycles []bool
}

func (a *reclaimAdapter) Shutdown(_ context.Context, req *adapterv1.ShutdownRequest) (*adapterv1.ShutdownResponse, error) {
	a.mu.Lock()
	a.recycles = append(a.recycles, req.GetRecycle() != nil)
	a.mu.Unlock()
	if a.fail {
		return nil, errors.New("adapter unreachable")
	}
	return &adapterv1.ShutdownResponse{ExitedCleanly: true}, nil
}

func (a *reclaimAdapter) sent() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return fmt.Sprint(a.recycles)
}

func dialReclaimAdapter(t *testing.T, a *reclaimAdapter) *adapterclient.Client {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	adapterv1.RegisterAdapterServer(gs, a)
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
	return cl
}

// recyclingArmer records the missing-report timeouts the binder arms.
type recyclingArmer struct{ armed []string }

func (r *recyclingArmer) OnRecycling(pod string) { r.armed = append(r.armed, pod) }

// preRunningFixture is a funnel Server whose binder runs over a fake cluster
// holding pod-a's Sandbox and per-pod claim, a miniredis slot set, a
// recording assigner, and a recording recycle armer. Every claim delete,
// claim status patch, and Pod patch (the drain-request stamp) is logged.
type preRunningFixture struct {
	*funnelFixture
	c      client.Client
	mr     *miniredis.Miniredis
	creds  *reclaimRecordingAssigner
	armer  *recyclingArmer
	writes *funnelLog
}

func newPreRunningFixture(t *testing.T) *preRunningFixture {
	t.Helper()
	f := newFunnelFixture(t, funnelOpts{})
	writes := &funnelLog{}
	scheme := claimAtCreateScheme(t)
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme corev1: %v", err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		&lennyv1.Sandbox{ObjectMeta: metav1.ObjectMeta{Name: reclaimPod, Namespace: funnelNS}},
		&lennyv1.SandboxClaim{ObjectMeta: metav1.ObjectMeta{Name: podclaim.ClaimName(reclaimPod), Namespace: funnelNS}},
	).WithInterceptorFuncs(interceptor.Funcs{
		Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			if _, ok := obj.(*lennyv1.SandboxClaim); ok {
				writes.add("delete-claim")
			}
			return cl.Delete(ctx, obj, opts...)
		},
		SubResourcePatch: func(ctx context.Context, cl client.Client, sub string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
			if _, ok := obj.(*lennyv1.SandboxClaim); ok {
				writes.add("patch-claim-status")
			}
			return cl.SubResource(sub).Patch(ctx, obj, patch, opts...)
		},
		Patch: func(ctx context.Context, cl client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			if _, ok := obj.(*corev1.Pod); ok {
				writes.add("drain-request")
			}
			return cl.Patch(ctx, obj, patch, opts...)
		},
	}).Build()
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	creds := &reclaimRecordingAssigner{}
	armer := &recyclingArmer{}
	f.srv.podBinder = &podsession.Binder{
		Client: c, Namespace: funnelNS, Credentials: creds,
		SlotCounter: slotcounter.New(rc), RecycleBoundary: armer,
	}
	return &preRunningFixture{funnelFixture: f, c: c, mr: mr, creds: creds, armer: armer, writes: writes}
}

func (p *preRunningFixture) reserve(t *testing.T, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if _, _, err := p.srv.podBinder.SlotCounter.Reserve(context.Background(), reclaimPod, id, 4); err != nil {
			t.Fatalf("reserve %s: %v", id, err)
		}
	}
}

func (p *preRunningFixture) members(t *testing.T) string {
	t.Helper()
	key := "lenny:pod:{" + reclaimPod + "}:active_slots"
	if !p.mr.Exists(key) {
		return "[]"
	}
	m, err := p.mr.Members(key)
	if err != nil {
		t.Fatalf("members: %v", err)
	}
	sort.Strings(m)
	return fmt.Sprint(m)
}

func (p *preRunningFixture) claimPresent(t *testing.T) bool {
	t.Helper()
	return claimExistsIn(t, p.c, funnelNS, reclaimPod)
}

// recreateClaim seeds pod-a's claim again, as a later occupancy's
// reservation does after the last release deleted it.
func (p *preRunningFixture) recreateClaim(t *testing.T) {
	t.Helper()
	if err := p.c.Create(context.Background(), &lennyv1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: podclaim.ClaimName(reclaimPod), Namespace: funnelNS},
	}); err != nil {
		t.Fatalf("recreate claim: %v", err)
	}
}

// terminal seeds the session's terminal row and runs the post-terminal hook
// with the state the terminal write applied from.
func (p *preRunningFixture) terminal(t *testing.T, id string, from, to session.State, scrub string) {
	t.Helper()
	row := sessionstore.Session{
		ID: id, TenantID: "acme", RuntimeRef: "echo", State: to,
		PodAssignment: reclaimPod, ScrubPolicy: scrub,
	}
	if err := p.store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed terminal row %s: %v", id, err)
	}
	p.srv.AfterTerminalWrite(context.Background(), from, row, nil)
}

func claimExistsIn(t *testing.T, c client.Client, ns, pod string) bool {
	t.Helper()
	err := c.Get(context.Background(), client.ObjectKey{Namespace: ns, Name: podclaim.ClaimName(pod)}, &lennyv1.SandboxClaim{})
	if err == nil {
		return true
	}
	if client.IgnoreNotFound(err) == nil {
		return false
	}
	t.Fatalf("get claim: %v", err)
	return false
}

// TestPreRunningTerminalWritersReleaseByPoolKind_spec_4_6_3 drives the
// post-terminal hook with each pre-running terminal writer's applied-from
// state over each pool kind. Every writer reaches the same pre-running
// reclaim, keyed by the row's frozen scrubPolicy. On an exclusive pool, on
// both recycle settings, the claim is deleted by name once with no
// `recycling` patch. On a concurrent-workspace pool exactly the session's
// member is removed; alone, the claim is deleted; with a live sibling, the
// shared claim stays and no drain is requested. Every cell releases the
// session's leases once and makes no executor release.
//
// spec: §4.6.3 (SandboxClaim: a release that ends the occupancy without
// reuse), §5.2 (Slot assignment atomicity), §6.2 (pre-attached
// disposition), §7.1 step 23 (lease release)
func TestPreRunningTerminalWritersReleaseByPoolKind_spec_4_6_3(t *testing.T) {
	writers := []struct {
		name     string
		from, to session.State
	}{
		{"DELETE", session.StateReady, session.StateCancelled},
		{"terminate", session.StateFinalizing, session.StateCancelled},
		{"cancel_child", session.StateCreated, session.StateCancelled},
		{"watchdog created timeout", session.StateCreated, session.StateFailed},
		{"watchdog finalizing timeout", session.StateFinalizing, session.StateFailed},
		{"watchdog ready timeout", session.StateReady, session.StateExpired},
	}
	pools := []struct {
		name        string
		scrub       string
		reserve     []string
		wantClaim   bool
		wantMembers string
	}{
		{name: "exclusive", scrub: "", wantClaim: false, wantMembers: "[]"},
		{name: "exclusive recycling", scrub: "best-effort", wantClaim: false, wantMembers: "[]"},
		{name: "concurrent, alone", scrub: ScrubPolicyPerSlot, reserve: []string{"sess-1"}, wantClaim: false, wantMembers: "[]"},
		{name: "concurrent, live sibling", scrub: ScrubPolicyPerSlot, reserve: []string{"sess-1", "sib"}, wantClaim: true, wantMembers: "[sib]"},
	}
	for _, w := range writers {
		for _, pk := range pools {
			t.Run(w.name+"/"+pk.name, func(t *testing.T) {
				p := newPreRunningFixture(t)
				p.reserve(t, pk.reserve...)
				p.terminal(t, "sess-1", w.from, w.to, pk.scrub)

				if got := p.claimPresent(t); got != pk.wantClaim {
					t.Errorf("claim present = %v, want %v", got, pk.wantClaim)
				}
				wantDeletes := 0
				if !pk.wantClaim {
					wantDeletes = 1
				}
				if n := p.writes.count("delete-claim"); n != wantDeletes {
					t.Errorf("claim deletes = %d, want %d", n, wantDeletes)
				}
				if n := p.writes.count("patch-claim-status"); n != 0 {
					t.Errorf("claim status patches = %d, want 0 (no `recycling` patch for a pre-running session)", n)
				}
				if n := p.writes.count("drain-request"); n != 0 {
					t.Errorf("drain requests = %d, want 0", n)
				}
				if got := p.members(t); got != pk.wantMembers {
					t.Errorf("slot members = %s, want %s", got, pk.wantMembers)
				}
				if fmt.Sprint(p.creds.released) != "[sess-1]" {
					t.Errorf("ReleaseSession calls = %v, want [sess-1]", p.creds.released)
				}
				if n := p.log.count("release:"); n != 0 {
					t.Errorf("executor releases = %d, want 0 (the pre-running reclaim owns the release)", n)
				}
			})
		}
	}
}

// TestPreRunningSlotReleaseIsOncePerSession_spec_5_2 pins the keyed slot
// release across the paths that release one pre-running slot twice: a failed
// /start whose BindReservedSlot released the reservation followed by DELETE,
// and the hook's reclaim followed by a peer's terminal-binding release. The
// second release removes nothing, so a sibling's member and the shared
// claim survive, and a duplicate never deletes the claim of a later
// occupancy. A clean hook release followed by a leaked release keeps the
// slot counted through its leaked member.
//
// spec: §5.2 (Slot assignment atomicity), §6.2 (leaked slot semantics),
// §4.6.3 (SandboxClaim: a release that ends the occupancy without reuse)
func TestPreRunningSlotReleaseIsOncePerSession_spec_5_2(t *testing.T) {
	ctx := context.Background()
	deleteTerminal := func(t *testing.T, p *preRunningFixture) {
		p.terminal(t, "sess-1", session.StateReady, session.StateCancelled, ScrubPolicyPerSlot)
	}
	peerRelease := func(t *testing.T, p *preRunningFixture, a *reclaimAdapter) {
		t.Helper()
		res := &podsession.BindResult{SessionID: "sess-1", TenantID: "acme", SandboxName: reclaimPod, SlotID: "sess-1"}
		if a != nil {
			res.Adapter = dialReclaimAdapter(t, a)
		}
		if err := p.srv.podBinder.ReleaseSlot(ctx, res); err != nil {
			t.Fatalf("peer release: %v", err)
		}
	}

	t.Run("failed start then DELETE, live sibling", func(t *testing.T) {
		p := newPreRunningFixture(t)
		p.reserve(t, "sess-1", "sib")
		if err := p.srv.podBinder.ReleaseSlotReservation(ctx, reclaimPod, "sess-1", false); err != nil {
			t.Fatalf("start failure release: %v", err)
		}
		deleteTerminal(t, p)
		if got := p.members(t); got != "[sib]" {
			t.Errorf("members = %s, want [sib]", got)
		}
		if !p.claimPresent(t) || p.writes.count("delete-claim") != 0 {
			t.Errorf("claim present = %v after %d deletes, want present with none", p.claimPresent(t), p.writes.count("delete-claim"))
		}
	})
	t.Run("failed start then DELETE, a later occupancy holds the pod", func(t *testing.T) {
		p := newPreRunningFixture(t)
		p.reserve(t, "sess-1")
		if err := p.srv.podBinder.ReleaseSlotReservation(ctx, reclaimPod, "sess-1", false); err != nil {
			t.Fatalf("start failure release: %v", err)
		}
		if p.claimPresent(t) {
			t.Fatalf("claim present after the last release, want deleted")
		}
		p.recreateClaim(t)
		p.reserve(t, "next")
		deleteTerminal(t, p)
		if got := p.members(t); got != "[next]" {
			t.Errorf("members = %s, want [next]", got)
		}
		if !p.claimPresent(t) {
			t.Errorf("the duplicate release deleted the later occupancy's claim")
		}
	})
	t.Run("hook reclaim then a peer's terminal-binding release", func(t *testing.T) {
		p := newPreRunningFixture(t)
		p.reserve(t, "sess-1", "sib")
		deleteTerminal(t, p)
		peerRelease(t, p, nil)
		if got := p.members(t); got != "[sib]" {
			t.Errorf("members = %s, want [sib]", got)
		}
		if !p.claimPresent(t) || p.writes.count("delete-claim") != 0 {
			t.Errorf("claim present = %v after %d deletes, want present with none", p.claimPresent(t), p.writes.count("delete-claim"))
		}
	})
	t.Run("hook clean release then a leaked release", func(t *testing.T) {
		p := newPreRunningFixture(t)
		p.reserve(t, "sess-1", "sib")
		deleteTerminal(t, p)
		peerRelease(t, p, &reclaimAdapter{fail: true})
		if got := p.members(t); got != "[leaked:sess-1 sib]" {
			t.Errorf("members = %s, want [leaked:sess-1 sib] (the leaked slot stays counted)", got)
		}
		if !p.claimPresent(t) {
			t.Errorf("claim deleted while a sibling lives")
		}
	})
}

// TestRollbackClaimReleasesByClaimKind_spec_7_1 pins the create-time
// rollback through the pre-running reclaim: an exclusive claim is deleted by
// name; a slot reservation removes the session's member and deletes the
// shared claim only when the set empties. A nil claim releases nothing.
//
// spec: §7.1 (atomic-creation rollback), §4.6.3 (SandboxClaim: a release
// that ends the occupancy without reuse), §5.2 (Slot assignment atomicity)
func TestRollbackClaimReleasesByClaimKind_spec_7_1(t *testing.T) {
	for _, tc := range []struct {
		name        string
		claim       *podsession.ClaimResult
		reserve     []string
		wantClaim   bool
		wantMembers string
		wantLease   string
	}{
		{name: "nil claim", wantClaim: true, wantMembers: "[]", wantLease: "[]"},
		{
			name: "exclusive claim", claim: &podsession.ClaimResult{SandboxName: reclaimPod},
			wantClaim: false, wantMembers: "[]", wantLease: "[sess-1]",
		},
		{
			name: "slot reservation, alone", claim: &podsession.ClaimResult{SandboxName: reclaimPod, SlotID: "sess-1"},
			reserve: []string{"sess-1"}, wantClaim: false, wantMembers: "[]", wantLease: "[sess-1]",
		},
		{
			name: "slot reservation, live sibling", claim: &podsession.ClaimResult{SandboxName: reclaimPod, SlotID: "sess-1"},
			reserve: []string{"sess-1", "sib"}, wantClaim: true, wantMembers: "[sib]", wantLease: "[sess-1]",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newPreRunningFixture(t)
			p.reserve(t, tc.reserve...)
			p.srv.rollbackClaim(context.Background(), tc.claim, "sess-1")
			if got := p.claimPresent(t); got != tc.wantClaim {
				t.Errorf("claim present = %v, want %v", got, tc.wantClaim)
			}
			if got := p.members(t); got != tc.wantMembers {
				t.Errorf("members = %s, want %s", got, tc.wantMembers)
			}
			if got := fmt.Sprint(p.creds.released); got != tc.wantLease {
				t.Errorf("ReleaseSession calls = %s, want %s", got, tc.wantLease)
			}
		})
	}
}

// TestRollbackBindingEndsOccupancyWithoutReuse_spec_4_6_3 pins the release
// of a binding that was never registered, on a recycle.enabled pool. An
// exclusive binding retires its pod after a plain Shutdown. A concurrent
// binding releases its slot with recycling disabled: alone, the claim is
// deleted with no `recycling` patch, no armed timeout, and no recycle
// Shutdown; with a live sibling, the claim stays; with a leaked Shutdown,
// the slot stays counted and the claim stays.
//
// spec: §4.6.3 (SandboxClaim: a release that ends the occupancy without
// reuse), §5.2 (Recycle lifecycle), §7.1 (atomic-creation rollback)
func TestRollbackBindingEndsOccupancyWithoutReuse_spec_4_6_3(t *testing.T) {
	for _, tc := range []struct {
		name        string
		slot        bool
		reserve     []string
		failStop    bool
		wantClaim   bool
		wantMembers string
		wantSent    string
	}{
		{name: "exclusive", wantClaim: false, wantMembers: "[]", wantSent: "[false]"},
		{name: "concurrent, alone", slot: true, reserve: []string{"sess-1"}, wantClaim: false, wantMembers: "[]", wantSent: "[false]"},
		{name: "concurrent, live sibling", slot: true, reserve: []string{"sess-1", "sib"}, wantClaim: true, wantMembers: "[sib]", wantSent: "[false]"},
		{name: "concurrent, leaked Shutdown", slot: true, reserve: []string{"sess-1"}, failStop: true, wantClaim: true, wantMembers: "[leaked:sess-1]", wantSent: "[false]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newPreRunningFixture(t)
			p.reserve(t, tc.reserve...)
			a := &reclaimAdapter{fail: tc.failStop}
			res := &podsession.BindResult{
				SessionID: "sess-1", TenantID: "acme", SandboxName: reclaimPod,
				Adapter: dialReclaimAdapter(t, a), Recycle: true,
			}
			if tc.slot {
				res.SlotID = "sess-1"
			}
			p.srv.rollbackBinding(context.Background(), res)

			if got := p.claimPresent(t); got != tc.wantClaim {
				t.Errorf("claim present = %v, want %v", got, tc.wantClaim)
			}
			if got := p.members(t); got != tc.wantMembers {
				t.Errorf("members = %s, want %s", got, tc.wantMembers)
			}
			if n := p.writes.count("patch-claim-status"); n != 0 {
				t.Errorf("claim status patches = %d, want 0 (no `recycling` patch for an unpublished binding)", n)
			}
			if len(p.armer.armed) != 0 {
				t.Errorf("armed missing-report timeouts = %v, want none", p.armer.armed)
			}
			if got := a.sent(); got != tc.wantSent {
				t.Errorf("Shutdown requests (recycle flag each) = %s, want %s (no whole-pod recycle Shutdown)", got, tc.wantSent)
			}
		})
	}
}
