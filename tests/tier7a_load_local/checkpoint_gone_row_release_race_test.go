// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local concurrency coverage for the checkpointer's release of
// a binding whose session row is absent on the primary. Periodic sweeps and
// preStop eviction checkpoints race one another over a real session server;
// each tick that reads the row as absent calls ReleaseGone, and the session
// server's terminal binding release dedups the calls into one release with
// the failed disposition.
//
// spec: §7.1 (Seal-and-export invariant), §10.1.7 (preStop Hook Drain)

package tier7a_load_local_test

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/checkpoint"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointer"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// goneStreamAdapter counts every Checkpoint stream opened on it.
type goneStreamAdapter struct {
	adapterv1.UnimplementedAdapterServer
	opened atomic.Int64
}

func (a *goneStreamAdapter) Checkpoint(grpc.BidiStreamingServer[adapterv1.CheckpointRequest, adapterv1.CheckpointResponse]) error {
	a.opened.Add(1)
	return nil
}

// goneRowExecutor records each Release and removes the released binding,
// as the pod executor does.
type goneRowExecutor struct {
	reg      *podsession.Registry
	mu       sync.Mutex
	released []string
}

func (e *goneRowExecutor) Send(context.Context, string, []executor.Message) (executor.Response, error) {
	return executor.Response{}, nil
}

func (e *goneRowExecutor) Close(ctx context.Context, id string) error { return e.Release(ctx, id, "") }

func (e *goneRowExecutor) Release(_ context.Context, id string, d executor.Disposition) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.released = append(e.released, id+":"+string(d))
	e.reg.Remove(id)
	return nil
}

func (e *goneRowExecutor) releases() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.released...)
}

// spec: 7.1 (Seal-and-export invariant), 10.1.7 (preStop Hook Drain)
// diagnosis: periodic sweeps and eviction checkpoints of a bound session
// whose row is gone on the primary raced one another. A race-detector report
// means the gone-row release path shares state without ordering; an opened
// adapter stream means an attempt checkpointed a session with no row; zero
// releases means the absent row was not routed to ReleaseGone, so the binding
// would be held until the replica exits; more than one release, or one whose
// disposition is not failed, means the session server's terminal claim
// record did not dedup the calls or the seam synthesised the wrong state.
func TestCheckpointGoneRowReleaseRacesSweepsAndEvictions_spec_7_1(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	adapterSrv := &goneStreamAdapter{}
	dial := heldServe(t, adapterSrv)

	const iterations = 30
	for i := range iterations {
		id := fmt.Sprintf("gone-%d", i)
		reg := podsession.NewRegistry()
		reg.Put(&podsession.BindResult{SessionID: id, TenantID: "acme", CoordinationGeneration: 1, Adapter: dial()})
		exec := &goneRowExecutor{reg: reg}
		srv := sessionserver.New(memstore.New(), sessionserver.Options{DevTenantHeader: true, Executor: exec, PodRegistry: reg})
		cp := &checkpointer.Checkpointer{
			Sessions: memstore.New(),
			Registry: reg,
			ReleaseGone: func(tenantID, sessionID string) {
				srv.ReleaseTerminalBinding(sessionstore.Session{ID: sessionID, TenantID: tenantID, State: session.StateFailed})
			},
		}
		runGoneRowInterleaving(t, rand.New(rand.NewSource(rng.Int63())), cp, id)

		deadline := time.Now().Add(5 * time.Second)
		for len(exec.releases()) == 0 && time.Now().Before(deadline) {
			time.Sleep(2 * time.Millisecond)
		}
		srv.Close()
		want := id + ":" + string(executor.DispositionFailed)
		if got := exec.releases(); len(got) != 1 || got[0] != want {
			t.Errorf("%s: releases = %v, want exactly [%s]", id, got, want)
		}
		if _, bound := reg.Get(id); bound {
			t.Errorf("%s: binding still published after the release", id)
		}
	}
	if n := adapterSrv.opened.Load(); n != 0 {
		t.Errorf("adapter Checkpoint streams opened = %d, want 0", n)
	}
}

// runGoneRowInterleaving starts sweeps and eviction checkpoints in a random
// order, each after a random delay, and waits for every one.
func runGoneRowInterleaving(t *testing.T, rng *rand.Rand, cp *checkpointer.Checkpointer, id string) {
	t.Helper()
	acts := make([]func(), 0, 12)
	for range 6 {
		acts = append(
			acts,
			func() { cp.Sweep(context.Background()) },
			func() { _ = cp.CheckpointWithTrigger(context.Background(), "acme", id, checkpoint.TriggerEviction) },
		)
	}
	rng.Shuffle(len(acts), func(i, j int) { acts[i], acts[j] = acts[j], acts[i] })
	var wg sync.WaitGroup
	for _, act := range acts {
		delay := time.Duration(rng.Intn(2000)) * time.Microsecond
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(delay)
			act()
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s: checkpoint ticks did not finish", id)
	}
}
