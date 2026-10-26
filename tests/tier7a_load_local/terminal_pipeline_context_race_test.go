// SPDX-License-Identifier: MIT

//go:build load_local

// Tier-7a load_local concurrency coverage for the terminal pipeline context.
//
// Every post-terminal hook runs on a context the gateway owns: the writer's
// context detached from its cancellation and bounded by what remains of the
// terminal pipeline budget. These cases end many sessions at once through
// the REST writers, the failure funnel, and the OnSessionTerminal and
// AfterTerminalWrite entries, and cancel each writer's context at a random
// offset while its seal runs, under the race detector. They assert the
// invariants a writer-tied or unbounded context would break: no seal context
// ends while the seal runs, every seal context carries a deadline no later
// than the budget, every session is released exactly once, and no goroutine
// outlives the round.
//
// spec: §7.1 (Normal Flow), §7.1 (Terminal pipeline context), §7.2
// (Interactive Session Model).
package tier7a_load_local_test

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// ctxRaceSealer holds each seal for a short random time and records any
// seal whose context ended while it ran or carried no deadline, or a
// deadline past the budget.
type ctxRaceSealer struct {
	budget time.Duration

	mu      sync.Mutex
	rng     *rand.Rand
	cut     []string
	unbound []string
	seals   map[string]int
}

func (s *ctxRaceSealer) hold() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Duration(s.rng.Intn(2000)) * time.Microsecond
}

func (s *ctxRaceSealer) Seal(ctx context.Context, _, sessionID string) error {
	entered := time.Now()
	dl, ok := ctx.Deadline()
	time.Sleep(s.hold())
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seals[sessionID]++
	if !ok || dl.After(entered.Add(s.budget+time.Second)) {
		s.unbound = append(s.unbound, sessionID)
	}
	if ctx.Err() != nil {
		s.cut = append(s.cut, sessionID)
	}
	return nil
}

// ctxRaceExecutor counts releases per session.
type ctxRaceExecutor struct {
	mu       sync.Mutex
	releases map[string]int
}

func (e *ctxRaceExecutor) Send(context.Context, string, []executor.Message) (executor.Response, error) {
	return executor.Response{}, nil
}

func (e *ctxRaceExecutor) Close(ctx context.Context, sessionID string) error {
	return e.Release(ctx, sessionID, "")
}

func (e *ctxRaceExecutor) Release(_ context.Context, sessionID string, _ executor.Disposition) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.releases[sessionID]++
	return nil
}

// ctxRaceWriter ends session id on ctx through one terminal writer path.
type ctxRaceWriter func(ctx context.Context, srv *sessionserver.Server, store sessionstore.Store, id string)

func ctxRaceREST(method, suffix string) ctxRaceWriter {
	return func(ctx context.Context, srv *sessionserver.Server, _ sessionstore.Store, id string) {
		req := httptest.NewRequest(method, "/v1/sessions/"+id+suffix, nil).WithContext(ctx)
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		srv.Handler().ServeHTTP(httptest.NewRecorder(), req)
	}
}

func ctxRaceFunnel(ctx context.Context, srv *sessionserver.Server, _ sessionstore.Store, id string) {
	_, _ = srv.ReportSessionFailure(ctx, sessionserver.FailureReport{
		TenantID: "acme", SessionID: id, Reason: string(session.FailureWorkspaceValidationFailed),
	})
}

// ctxRaceEntry writes the row terminal as an outside writer does and runs
// the hook through OnSessionTerminal, or AfterTerminalWrite when after is set.
func ctxRaceEntry(after bool) ctxRaceWriter {
	return func(ctx context.Context, srv *sessionserver.Server, store sessionstore.Store, id string) {
		var from session.State
		updated, err := store.Update(ctx, "acme", id, func(r *sessionstore.Session) error {
			from = r.State
			r.State = session.StateExpired
			sessionstore.MarkTerminated(r, from, srv.Now())
			return nil
		})
		if err != nil {
			return
		}
		if after {
			srv.AfterTerminalWrite(ctx, from, updated, nil)
			return
		}
		srv.OnSessionTerminal(ctx, from, updated)
	}
}

var ctxRaceWriters = []ctxRaceWriter{
	ctxRaceREST(http.MethodDelete, ""),
	ctxRaceREST(http.MethodPost, "/terminate"),
	ctxRaceFunnel,
	ctxRaceEntry(false),
	ctxRaceEntry(true),
}

// spec: §7.1 (Normal Flow), §7.1 (Terminal pipeline context), §7.2
// (Interactive Session Model)
// diagnosis: many sessions end at once through every terminal writer path,
// and each writer's context is cancelled at a random offset while its seal
// runs. Every seal keeps a live context with a deadline no later than the
// terminal pipeline budget, and every session is released once. A seal cut
// by its writer's cancellation means the hook runs on the writer's context;
// a missing or late deadline means the hook is unbounded; a missing or
// double release means the detached pipeline lost or repeated its release.
func TestTerminalPipelineContextSurvivesWriterCancellation_spec_7_1(t *testing.T) {
	seed := time.Now().UnixNano()
	t.Logf("seed %d", seed)
	rng := rand.New(rand.NewSource(seed))
	baseline := runtime.NumGoroutine()
	for round := 0; round < 20; round++ {
		ctxRaceRound(t, round, rand.New(rand.NewSource(rng.Int63())))
	}
	waitGoroutines(t, baseline, 5)
}

func ctxRaceRound(t *testing.T, round int, rng *rand.Rand) {
	t.Helper()
	const sessions = 40
	store := memstore.New()
	sealer := &ctxRaceSealer{rng: rand.New(rand.NewSource(rng.Int63())), seals: map[string]int{}}
	exec := &ctxRaceExecutor{releases: map[string]int{}}
	srv := sessionserver.New(store, sessionserver.Options{
		DevTenantHeader:            true,
		Sealer:                     sealer,
		Executor:                   exec,
		WorkspaceSealMaxDuration:   300 * time.Second,
		StreamFailureReportTimeout: 30 * time.Second,
	})
	defer srv.Close()
	sealer.budget = srv.TerminalPipelineBudget()

	ids := make([]string, sessions)
	for i := range ids {
		ids[i] = fmt.Sprintf("sess-ctx-%d-%d", round, i)
		if err := store.Create(context.Background(), sessionstore.Session{
			ID: ids[i], TenantID: "acme", RuntimeRef: "echo", State: session.StateRunning,
		}); err != nil {
			t.Fatalf("round %d: seed %s: %v", round, ids[i], err)
		}
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, id := range ids {
		writer := ctxRaceWriters[rng.Intn(len(ctxRaceWriters))]
		cancelAfter := time.Duration(rng.Intn(1500)) * time.Microsecond
		wg.Add(2)
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			defer wg.Done()
			<-start
			time.Sleep(cancelAfter)
			cancel()
		}()
		go func(id string) {
			defer wg.Done()
			defer cancel()
			<-start
			writer(ctx, srv, store, id)
		}(id)
	}
	close(start)
	wg.Wait()
	assertCtxRaceRound(t, round, ids, sealer, exec)
}

func assertCtxRaceRound(t *testing.T, round int, ids []string, sealer *ctxRaceSealer, exec *ctxRaceExecutor) {
	t.Helper()
	sealer.mu.Lock()
	cut, unbound := append([]string(nil), sealer.cut...), append([]string(nil), sealer.unbound...)
	sealer.mu.Unlock()
	if len(cut) != 0 {
		t.Fatalf("round %d: seals cut by their writer's cancellation: %v", round, cut)
	}
	if len(unbound) != 0 {
		t.Fatalf("round %d: seals with no deadline or a deadline past the budget: %v", round, unbound)
	}
	exec.mu.Lock()
	defer exec.mu.Unlock()
	for _, id := range ids {
		if got := exec.releases[id]; got != 1 {
			t.Fatalf("round %d: releases of %s = %d, want 1", round, id, got)
		}
	}
}
