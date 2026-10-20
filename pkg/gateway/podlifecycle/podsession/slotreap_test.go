// SPDX-License-Identifier: MIT

package podsession_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// reapAdapter answers the stale-slot reaper's unconditional Shutdown with a
// configured outcome and exit report per session, and records every
// Shutdown it served.
type reapAdapter struct {
	adapterv1.UnimplementedAdapterServer

	mu        sync.Mutex
	answers   map[string]*adapterv1.ShutdownResponse
	errs      map[string]error
	shutdowns []string
}

func (a *reapAdapter) NegotiateVersion(context.Context, *adapterv1.NegotiateVersionRequest) (*adapterv1.NegotiateVersionResponse, error) {
	return &adapterv1.NegotiateVersionResponse{SelectedProtocolVersion: adapter.ProtocolVersionV1}, nil
}

func (a *reapAdapter) Shutdown(_ context.Context, req *adapterv1.ShutdownRequest) (*adapterv1.ShutdownResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id := req.GetSessionId().GetValue()
	a.shutdowns = append(a.shutdowns, id)
	if !req.GetUnconditionalTeardown() {
		return nil, status.Error(codes.InvalidArgument, "the reaper sends the unconditional form")
	}
	if err := a.errs[id]; err != nil {
		return nil, err
	}
	if resp, ok := a.answers[id]; ok {
		return resp, nil
	}
	return &adapterv1.ShutdownResponse{
		ExitedCleanly: true,
		SlotReclaim:   adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_ABSENT,
	}, nil
}

// reapFixture is a binder over a fake Kubernetes client holding one idle
// Sandbox, a miniredis-backed slot counter, and a reap adapter.
type reapFixture struct {
	binder *podsession.Binder
	adp    *reapAdapter
	redis  *miniredis.Miniredis
	dials  int
}

const reapPod = "sbx-reap"

func newReapFixture(t *testing.T, members ...string) *reapFixture {
	t.Helper()
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	counter := slotcounter.New(rc)
	for _, id := range members {
		if _, _, err := counter.Reserve(context.Background(), reapPod, id, 8); err != nil {
			t.Fatalf("reserve %s: %v", id, err)
		}
	}
	f := &reapFixture{
		adp:   &reapAdapter{answers: map[string]*adapterv1.ShutdownResponse{}, errs: map[string]error{}},
		redis: mr,
	}
	kube := fake.NewClientBuilder().WithScheme(newScheme(t)).
		WithObjects(concurrentIdleSandbox(reapPod, "10.244.3.9")).Build()
	dial := podAdapterDialer(t, f.adp)
	f.binder = newBinder(kube, func(addr string) (*adapterclient.Client, error) {
		f.dials++
		return dial(addr)
	})
	f.binder.SlotCounter = counter
	return f
}

// podAdapterDialer serves an adapter fake over an in-memory connection.
func podAdapterDialer(t *testing.T, srv adapterv1.AdapterServer) func(string) (*adapterclient.Client, error) {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	adapterv1.RegisterAdapterServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	return bufconnDialer(lis)
}

func (f *reapFixture) isMember(t *testing.T, id string) bool {
	t.Helper()
	ok, err := f.redis.SIsMember("lenny:pod:{"+reapPod+"}:active_slots", id)
	if err != nil {
		t.Fatalf("read the slot set: %v", err)
	}
	return ok
}

// countingWait is the reaper's limiter wait, counted.
type countingWait struct {
	mu sync.Mutex
	n  int
}

func (w *countingWait) wait(context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.n++
	return nil
}

// spec: §5.2 (Slot assignment atomicity)
//
// The reaper removes a stale member only after the pod's adapter tore its
// slot down cleanly: a clean absent answer and a clean reclaimed answer each
// remove it. An unclean answer, a Shutdown error, a dial failure, and a
// missing Sandbox each keep it, and no Reap follows them. wait runs once per
// identifier.
func TestReapStaleSlotsRemovesOnlyAfterACleanTeardown_spec_5_2(t *testing.T) {
	ctx := context.Background()
	// "keep" is the pod's other member, so Reap never sees a last member.
	const keep = "s-live"

	t.Run("clean absent and clean reclaimed answers remove the member", func(t *testing.T) {
		f := newReapFixture(t, keep, "s-absent", "s-reclaimed")
		f.adp.answers["s-reclaimed"] = &adapterv1.ShutdownResponse{
			ExitedCleanly: true,
			SlotReclaim:   adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_RECLAIMED,
		}
		w := &countingWait{}
		released, err := f.binder.ReapStaleSlots(ctx, reapPod, []string{"s-absent", "s-reclaimed"}, w.wait)
		if err != nil || released != 2 {
			t.Fatalf("ReapStaleSlots = %d, %v, want 2 released", released, err)
		}
		if f.isMember(t, "s-absent") || f.isMember(t, "s-reclaimed") {
			t.Error("a cleanly torn-down member is still in the set")
		}
		if w.n != 2 {
			t.Errorf("wait ran %d times, want once per identifier", w.n)
		}
		if f.dials != 1 {
			t.Errorf("dials = %d, want one per call", f.dials)
		}
	})

	t.Run("an unclean answer keeps the member", func(t *testing.T) {
		f := newReapFixture(t, keep, "s1")
		f.adp.answers["s1"] = &adapterv1.ShutdownResponse{
			SlotReclaim: adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_ABSENT,
		}
		released, err := f.binder.ReapStaleSlots(ctx, reapPod, []string{"s1"}, (&countingWait{}).wait)
		if err != nil || released != 0 {
			t.Fatalf("ReapStaleSlots = %d, %v, want 0 released and no error", released, err)
		}
		if !f.isMember(t, "s1") {
			t.Error("an unclean teardown's member was removed")
		}
	})

	t.Run("a Shutdown error keeps the member and the next one is still tried", func(t *testing.T) {
		f := newReapFixture(t, keep, "s1", "s2")
		f.adp.errs["s1"] = status.Error(codes.Unavailable, "adapter busy")
		w := &countingWait{}
		released, err := f.binder.ReapStaleSlots(ctx, reapPod, []string{"s1", "s2"}, w.wait)
		if err == nil || released != 1 {
			t.Fatalf("ReapStaleSlots = %d, %v, want 1 released and the first error", released, err)
		}
		if !f.isMember(t, "s1") || f.isMember(t, "s2") {
			t.Error("want s1 kept and s2 removed")
		}
		if w.n != 2 {
			t.Errorf("wait ran %d times, want 2", w.n)
		}
	})

	t.Run("a dial failure keeps every member and sends nothing", func(t *testing.T) {
		f := newReapFixture(t, keep, "s1")
		f.binder.DialAdapter = func(string) (*adapterclient.Client, error) { return nil, errors.New("dial refused") }
		if released, err := f.binder.ReapStaleSlots(ctx, reapPod, []string{"s1"}, (&countingWait{}).wait); err == nil || released != 0 {
			t.Fatalf("ReapStaleSlots = %d, %v, want an error", released, err)
		}
		if !f.isMember(t, "s1") || len(f.adp.shutdowns) != 0 {
			t.Errorf("member kept = %v, shutdowns = %v, want kept and none", f.isMember(t, "s1"), f.adp.shutdowns)
		}
	})

	t.Run("a missing Sandbox keeps every member and dials nothing", func(t *testing.T) {
		f := newReapFixture(t, keep, "s1")
		if released, err := f.binder.ReapStaleSlots(ctx, "sbx-gone", []string{"s1"}, (&countingWait{}).wait); err == nil || released != 0 {
			t.Fatalf("ReapStaleSlots = %d, %v, want an error", released, err)
		}
		if !f.isMember(t, "s1") || f.dials != 0 {
			t.Errorf("member kept = %v, dials = %d, want kept and no dial", f.isMember(t, "s1"), f.dials)
		}
	})

	t.Run("a wait error ends the call before the next Shutdown", func(t *testing.T) {
		f := newReapFixture(t, keep, "s1")
		stop := errors.New("limiter cancelled")
		released, err := f.binder.ReapStaleSlots(ctx, reapPod, []string{"s1"}, func(context.Context) error { return stop })
		if !errors.Is(err, stop) || released != 0 {
			t.Fatalf("ReapStaleSlots = %d, %v, want the wait error", released, err)
		}
		if len(f.adp.shutdowns) != 0 || !f.isMember(t, "s1") {
			t.Errorf("shutdowns = %v, want none and the member kept", f.adp.shutdowns)
		}
	})

	t.Run("no slot counter is an error", func(t *testing.T) {
		f := newReapFixture(t)
		f.binder.SlotCounter = nil
		if _, err := f.binder.ReapStaleSlots(ctx, reapPod, []string{"s1"}, (&countingWait{}).wait); err == nil {
			t.Fatal("ReapStaleSlots with no counter returned no error")
		}
	})
}
