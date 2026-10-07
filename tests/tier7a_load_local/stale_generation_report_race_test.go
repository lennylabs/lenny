// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local concurrency coverage for the stream-failure report on a
// replica whose session a peer has taken over.
//
// When the session row's coordination_generation has moved past the
// generation this replica's binding was published at, a stream-failure report
// does not fail the session. It removes the stale binding and evicts its
// stream, as the coordination Sweeper's binding eviction does, and makes no
// binder call, so the pod the new coordinator serves is not drained. In
// production that eviction can run at the same moment as the Sweeper's own
// eviction of the same session and as a delivery that reopens a stream over
// the stale binding. This case drives the three together under the race
// detector and asserts the invariants a race would break: no binding and no
// cached stream survive, no release reaches the binder, the row is unchanged,
// and no stream goroutine outlives the race.
//
// spec: §10.1.5 (Stale Replica Behavior), §10.1.1 (Stateless Replicas and
// Per-Session Coordination), §28.5.1 (Gateway-to-pod, CH-ATTACH).
package tier7a_load_local_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// releaseCountingExecutor is the pod executor with every Release and Close
// counted, so the case can assert that the stale-generation eviction never
// reached the binder.
type releaseCountingExecutor struct {
	*executor.PodExecutor
	releases atomic.Int64
}

func (e *releaseCountingExecutor) Release(ctx context.Context, sessionID string, d executor.Disposition) error {
	e.releases.Add(1)
	return e.PodExecutor.Release(ctx, sessionID, d)
}

func (e *releaseCountingExecutor) Close(ctx context.Context, sessionID string) error {
	e.releases.Add(1)
	return e.PodExecutor.Close(ctx, sessionID)
}

// spec: 10.1.5 (Stale Replica Behavior), 10.1.1 (Stateless Replicas and
// Per-Session Coordination), 28.5.1 (Gateway-to-pod)
// diagnosis: the stale-generation eviction of a stream-failure report, the
// coordination Sweeper's binding eviction, and a delivery that reopens a
// stream over the stale binding raced. A race-detector report means the
// registry or the stream cache is touched outside its lock; a binding or a
// cached stream that survives means an eviction lost to the reopen, so the
// stale replica keeps a stream to a pod it no longer coordinates; a release
// means the stale replica drained the new coordinator's pod; a changed row
// means the stale replica wrote the failure transition.
func TestStaleGenerationReportRacesSweeperEviction_spec_10_1_5(t *testing.T) {
	dial := heldServe(t, &heldEchoAdapter{})
	cl := dial()
	reg := podsession.NewRegistry()
	pe := &releaseCountingExecutor{PodExecutor: executor.NewPodExecutor(reg, nil)}
	store := memstore.New()
	srv := sessionserver.New(store, sessionserver.Options{Executor: pe, PodRegistry: reg})
	pe.SetStreamFailureHandler(srv.ReportAttachStreamFailure)
	ctx := context.Background()

	for attempt := 0; attempt < raceAttempts; attempt++ {
		id := fmt.Sprintf("sess-stale-%d", attempt)
		sandbox := "sbx-" + id
		if err := store.Create(ctx, sessionstore.Session{
			ID: id, TenantID: "acme", RuntimeRef: "echo", State: session.StateRunning,
			CoordinationGeneration: 2, PodAssignment: sandbox,
		}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
		reg.Put(&podsession.BindResult{SessionID: id, TenantID: "acme", SandboxName: sandbox, Adapter: cl, CoordinationGeneration: 1})
		if err := heldSend(t, ctx, pe.PodExecutor, id, "open"); err != nil {
			t.Fatalf("open the held stream of %s: %v", id, err)
		}

		start := newRaceStart(3)
		var wg sync.WaitGroup
		wg.Add(3)
		go func() {
			defer wg.Done()
			start.arrive()
			srv.ReportAttachStreamFailure("acme", id, sandbox, status.Error(codes.DeadlineExceeded, "runtime missed heartbeat ack deadline"))
		}()
		go func() {
			defer wg.Done()
			start.arrive()
			// The Sweeper's binding eviction: remove the registry entry,
			// then evict the stream.
			reg.Remove(id)
			pe.EvictStream(id)
		}()
		go func() {
			defer wg.Done()
			start.arrive()
			_ = heldSend(t, ctx, pe.PodExecutor, id, "racing")
		}()
		start.release(t)
		wg.Wait()

		if _, ok := reg.Get(id); ok {
			t.Fatalf("attempt %d: the stale binding survived both evictions", attempt)
		}
		if _, err := pe.Send(ctx, id, []executor.Message{{Role: "user", Content: "after"}}); err == nil {
			t.Fatalf("attempt %d: a Send after both evictions was delivered over a cached stream", attempt)
		}
		row, err := store.Get(ctx, "acme", id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		if row.State != session.StateRunning || row.RetryCount != 0 || row.CoordinationGeneration != 2 {
			t.Fatalf("attempt %d: row = %s/%d/%d, want running/0/2 unchanged",
				attempt, row.State, row.RetryCount, row.CoordinationGeneration)
		}
	}
	if n := pe.releases.Load(); n != 0 {
		t.Fatalf("binder releases = %d, want none", n)
	}
	heldAssertNoStreamGoroutines(t)
}
