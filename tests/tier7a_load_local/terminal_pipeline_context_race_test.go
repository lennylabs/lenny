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
// than the budget, every session whose terminal write committed is released
// exactly once, a session whose write did not commit is neither sealed nor
// released, and no goroutine outlives the round. A terminal write runs on its
// writer's own context, so a writer cancelled before its write commits leaves
// the row non-terminal and owes no release.
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
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
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
// terminal pipeline budget, every session the store reads back as terminal
// is released once, and a session still non-terminal (its writer was
// cancelled before the terminal write committed) is neither sealed nor
// released. A seal cut
// by its writer's cancellation means the hook runs on the writer's context;
// a missing or late deadline means the hook is unbounded; a missing or
// double release of a terminal row means the detached pipeline lost or
// repeated its release; a seal or release of a non-terminal row means a
// post-terminal hook ran with no committed terminal write.
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
	// spec: §7.1 (Seal-and-export invariant) — the seal step seals only a
	// session this replica holds a binding for, so every session is bound,
	// at a generation above any row the writers produce.
	reg := podsession.NewRegistry()
	srv := sessionserver.New(store, sessionserver.Options{
		DevTenantHeader:            true,
		Sealer:                     sealer,
		PodRegistry:                reg,
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
		reg.Put(&podsession.BindResult{SessionID: ids[i], TenantID: "acme", CoordinationGeneration: 1 << 30})
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
	// spec: §7.1 (Terminal pipeline context) — OnSessionTerminal runs the
	// pipeline detached under its own claim.
	if err := srv.WaitTerminalWork(context.Background()); err != nil {
		t.Fatalf("round %d: WaitTerminalWork: %v", round, err)
	}
	assertCtxRaceRound(t, round, store, ids, sealer, exec)
}

func assertCtxRaceRound(t *testing.T, round int, store sessionstore.Store, ids []string, sealer *ctxRaceSealer, exec *ctxRaceExecutor) {
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
	for _, id := range ids {
		assertCtxRaceSession(t, round, store, id, sealer, exec)
	}
}

// assertCtxRaceSession checks one session's outcome against whether its
// terminal write committed. A terminal row is released exactly once; the
// release may land on the detached pipeline shortly after the writer
// returns, so the check polls up to a bound. A non-terminal row is never
// sealed or released.
func assertCtxRaceSession(t *testing.T, round int, store sessionstore.Store, id string, sealer *ctxRaceSealer, exec *ctxRaceExecutor) {
	t.Helper()
	row, err := store.Get(context.Background(), "acme", id)
	if err != nil {
		t.Fatalf("round %d: read %s: %v", round, id, err)
	}
	want := 0
	if session.IsTerminal(row.State) {
		want = 1
	}
	got := ctxRaceReleases(exec, id, want, 5*time.Second)
	if got != want {
		t.Fatalf("round %d: releases of %s (state %s) = %d, want %d", round, id, row.State, got, want)
	}
	sealer.mu.Lock()
	seals := sealer.seals[id]
	sealer.mu.Unlock()
	if seals != want {
		t.Fatalf("round %d: %s (state %s) was sealed %d times, want %d", round, id, row.State, seals, want)
	}
}

// ctxRaceReleases returns the release count of id once it reaches want, or
// the count at the bound when it never does.
func ctxRaceReleases(exec *ctxRaceExecutor, id string, want int, bound time.Duration) int {
	deadline := time.Now().Add(bound)
	for {
		exec.mu.Lock()
		got := exec.releases[id]
		exec.mu.Unlock()
		if got >= want || time.Now().After(deadline) {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
}
