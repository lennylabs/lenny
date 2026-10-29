// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 fault injection for the background sweeps that every gateway
// replica runs over the session rows: the session watchdog, orphan cleanup,
// and the orphan-session reconciler. A failure to list one tenant's rows or
// to write one row must not stop a pass for the other tenants and rows, and
// a terminal pipeline that blocks must hold its terminal claim open without
// delaying the next row or inheriting the pass's cancellation.
//
// spec: §10.1.1 (Primary:), §7.1 (Terminal pipeline context:)
package tier8_chaos_test

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/orphancleanup"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/watchdog"
	"github.com/lennylabs/lenny/pkg/gateway/session/orphansession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

var (
	// errSweepListDown is the injected listing failure of one tenant.
	errSweepListDown = errors.New("injected: list sessions unavailable")
	// errSweepWriteDown is the injected write failure of one row.
	errSweepWriteDown = errors.New("injected: session write unavailable")
)

// sweepFaultStore injects a listing error per tenant and a write error per
// session over an in-memory store.
type sweepFaultStore struct {
	sessionstore.Store
	mu        sync.Mutex
	listErr   map[string]error
	updateErr map[string]error
}

func newSweepFaultStore(inner sessionstore.Store) *sweepFaultStore {
	return &sweepFaultStore{Store: inner, listErr: map[string]error{}, updateErr: map[string]error{}}
}

func (s *sweepFaultStore) List(ctx context.Context, tenantID string, f sessionstore.ListFilter) ([]sessionstore.Session, error) {
	s.mu.Lock()
	err := s.listErr[tenantID]
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	rows, err := s.Store.List(ctx, tenantID, f)
	// Rows come back in ID order, so the failing row is written before the
	// rows after it and a pass that stops at the failure leaves them.
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, err
}

func (s *sweepFaultStore) Update(ctx context.Context, tenantID, id string, mut func(*sessionstore.Session) error) (sessionstore.Session, error) {
	s.mu.Lock()
	err := s.updateErr[id]
	s.mu.Unlock()
	if err != nil {
		return sessionstore.Session{}, err
	}
	return s.Store.Update(ctx, tenantID, id, mut)
}

// heal clears the listing fault of tenant and the write fault of id.
func (s *sweepFaultStore) heal(tenant, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.listErr, tenant)
	delete(s.updateErr, id)
}

// claimHook is a terminal hook for all three sweeps. It tracks each
// session's open terminal claims the way the gateway's shutdown wait does,
// counts terminal hooks, records the context each hook received, and can
// hold every hook until released.
type claimHook struct {
	mu       sync.Mutex
	open     map[string]int
	hooks    map[string]int
	returned map[string]bool
	ctxs     map[string]context.Context
	// earlyEnd counts claims that ended before their session's hook returned.
	earlyEnd int
	block    chan struct{}
	entered  chan string
}

func newClaimHook(blocking bool) *claimHook {
	h := &claimHook{
		open: map[string]int{}, hooks: map[string]int{}, returned: map[string]bool{},
		ctxs: map[string]context.Context{}, entered: make(chan string, 64),
	}
	if blocking {
		h.block = make(chan struct{})
	}
	return h
}

func (h *claimHook) BeginTerminal(id string) func() {
	h.mu.Lock()
	h.open[id]++
	h.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.open[id]--
			if h.hooks[id] > 0 && !h.returned[id] {
				h.earlyEnd++
			}
		})
	}
}

func (h *claimHook) OnSessionTerminal(ctx context.Context, _ session.State, sess sessionstore.Session) {
	h.mu.Lock()
	h.hooks[sess.ID]++
	h.ctxs[sess.ID] = ctx
	h.mu.Unlock()
	h.entered <- sess.ID
	if h.block != nil {
		<-h.block
	}
	h.mu.Lock()
	h.returned[sess.ID] = true
	h.mu.Unlock()
}

func (h *claimHook) openFor(id string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.open[id]
}

func (h *claimHook) hookCount(id string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.hooks[id]
}

func (h *claimHook) openTotal() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, c := range h.open {
		n += c
	}
	return n
}

// waitClaimsClosed is the shutdown wait: it polls until no claim is open and
// reports whether that happened within timeout.
func (h *claimHook) waitClaimsClosed(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if h.openTotal() == 0 {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return h.openTotal() == 0
}

// sweepFaultSubject is one sweep under test: seed writes an eligible row with
// the given id under tenant, and tick runs one pass at the subject's due time.
type sweepFaultSubject struct {
	name string
	seed func(t *testing.T, store sessionstore.Store, tenant, id string)
	tick func(ctx context.Context, store sessionstore.Store, hook *claimHook) error
}

// sweepBorn is the creation instant of every seeded row.
var sweepBorn = time.Date(2026, 4, 1, 9, 0, 0, 0, time.UTC)

// sweepDue is a pass instant past every subject's deadline.
var sweepDue = sweepBorn.Add(2 * time.Hour)

func sweepCreate(t *testing.T, store sessionstore.Store, row sessionstore.Session) {
	t.Helper()
	if row.CreatedAt.IsZero() {
		row.CreatedAt, row.UpdatedAt = sweepBorn, sweepBorn
	}
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed %s: %v", row.ID, err)
	}
}

// sweepTenants is the tenant set every subject sweeps.
var sweepTenants = []string{"acme", "globex"}

// orphanMirror reports every pod terminated with a fresh mirror.
type orphanMirror struct{}

func (orphanMirror) GetByPodID(context.Context, string) (orphansession.MirrorPod, bool, error) {
	return orphansession.MirrorPod{PoolID: "pool-a", Phase: "terminated"}, true, nil
}

func (orphanMirror) MirrorLagSeconds(context.Context, string) (float64, error) { return 0, nil }

type sweepTenantList []string

func (s sweepTenantList) ListTenants(context.Context) ([]string, error) { return s, nil }

// continuationSubjects are the three sweeps with a terminal write each.
func continuationSubjects() []sweepFaultSubject {
	return []sweepFaultSubject{
		{
			name: "watchdog",
			seed: func(t *testing.T, store sessionstore.Store, tenant, id string) {
				sweepCreate(t, store, sessionstore.Session{ID: id, TenantID: tenant, State: session.StateCreated})
			},
			tick: func(ctx context.Context, store sessionstore.Store, hook *claimHook) error {
				w := watchdog.New(store, watchdog.StaticTenants(sweepTenants), watchdog.Config{}, nil).WithTerminalHook(hook)
				_, err := w.Tick(ctx, sweepDue)
				return err
			},
		},
		{
			name: "orphan cleanup",
			seed: func(t *testing.T, store sessionstore.Store, tenant, id string) {
				root := tenant + "-root"
				if _, err := store.Get(context.Background(), tenant, root); err != nil {
					sweepCreate(t, store, sessionstore.Session{
						ID: root, TenantID: tenant, State: session.StateCompleted, TerminatedAt: sweepBorn,
					})
				}
				sweepCreate(t, store, sessionstore.Session{
					ID: id, TenantID: tenant, State: session.StateRunning, ParentSessionID: root,
				})
			},
			tick: func(ctx context.Context, store sessionstore.Store, hook *claimHook) error {
				sw := orphancleanup.New(store, orphancleanup.StaticTenants(sweepTenants), orphancleanup.Options{Terminal: hook})
				_, err := sw.Tick(ctx, sweepDue)
				return err
			},
		},
		{
			name: "orphan-session reconciler",
			seed: func(t *testing.T, store sessionstore.Store, tenant, id string) {
				sweepCreate(t, store, sessionstore.Session{
					ID: id, TenantID: tenant, State: session.StateRunning, PodAssignment: "pod-" + id, PoolRef: "pool-a",
				})
			},
			tick: func(ctx context.Context, store sessionstore.Store, hook *claimHook) error {
				r := orphansession.New(store, sweepTenantList(sweepTenants), orphanMirror{}, orphansession.Options{Terminal: hook})
				_, err := r.Tick(ctx)
				return err
			},
		},
	}
}

// spec: §10.1.1 (Primary:)
//
// diagnosis: a failure means one tenant's listing error or one row's write
// error stopped a sweep pass for the other tenants and rows, the pass hid the
// error instead of returning it, a missing row was reported as a failure or
// ran a hook, or a failed row was not ended by the next pass.
func TestSweepsContinuePastListAndWriteFaults_spec_10_1_1(t *testing.T) {
	for _, sub := range continuationSubjects() {
		t.Run(sub.name, func(t *testing.T) {
			mem := memstore.New()
			store := newSweepFaultStore(mem)
			for _, id := range []string{"bad", "good", "gone"} {
				sub.seed(t, mem, "acme", id)
			}
			sub.seed(t, mem, "globex", "skipped")
			store.listErr["globex"] = errSweepListDown
			store.updateErr["bad"] = errSweepWriteDown
			store.updateErr["gone"] = sessionstore.ErrNotFound
			hook := newClaimHook(false)

			err := sub.tick(context.Background(), store, hook)
			if !errors.Is(err, errSweepListDown) || !errors.Is(err, errSweepWriteDown) {
				t.Fatalf("pass error = %v, want both the listing fault and the write fault joined", err)
			}
			if errors.Is(err, sessionstore.ErrNotFound) {
				t.Errorf("pass error %v carries ErrNotFound, want a missing row treated as superseded", err)
			}
			for _, id := range []string{"bad", "gone"} {
				if n := hook.openFor(id); n != 0 {
					t.Errorf("%s has %d open claims when the pass returned, want its claim ended synchronously", id, n)
				}
				if n := hook.hookCount(id); n != 0 {
					t.Errorf("%s ran %d terminal hooks, want none", id, n)
				}
			}
			if !hook.waitClaimsClosed(5 * time.Second) {
				t.Fatal("terminal claims still open after the dispatched hooks should have returned")
			}
			assertSweepTerminal(t, mem, "acme", "good", true)
			assertSweepTerminal(t, mem, "acme", "bad", false)
			assertSweepTerminal(t, mem, "globex", "skipped", false)
			if n := hook.hookCount("good"); n != 1 {
				t.Errorf("good ran %d terminal hooks, want 1", n)
			}

			store.heal("globex", "bad")
			if err := sub.tick(context.Background(), store, hook); err != nil {
				t.Fatalf("second pass error = %v, want nil once the faults heal", err)
			}
			if !hook.waitClaimsClosed(5 * time.Second) {
				t.Fatal("terminal claims still open after the second pass")
			}
			assertSweepTerminal(t, mem, "acme", "bad", true)
			assertSweepTerminal(t, mem, "globex", "skipped", true)
			for _, id := range []string{"good", "bad", "skipped"} {
				if n := hook.hookCount(id); n != 1 {
					t.Errorf("%s ran %d terminal hooks across both passes, want 1", id, n)
				}
			}
		})
	}
}

func assertSweepTerminal(t *testing.T, store sessionstore.Store, tenant, id string, want bool) {
	t.Helper()
	row, err := store.Get(context.Background(), tenant, id)
	if err != nil {
		t.Fatalf("get %s: %v", id, err)
	}
	if session.IsTerminal(row.State) != want {
		t.Errorf("%s/%s state = %q, want terminal=%v", tenant, id, row.State, want)
	}
}

// blockingSubject is one terminal writer whose hook is held: seed writes two
// eligible rows, and tick runs one pass with ctx.
type blockingSubject struct {
	name string
	seed func(t *testing.T, store sessionstore.Store, id string)
	tick func(ctx context.Context, store sessionstore.Store, hook *claimHook) error
}

// sweepOff keeps a watchdog control out of the way of the subject under test.
const sweepOff = 100 * 24 * 3600

// watchdogSubject builds a blockingSubject that runs a watchdog with cfg.
func watchdogSubject(name string, cfg watchdog.Config, row func(id string) sessionstore.Session) blockingSubject {
	return blockingSubject{
		name: name,
		seed: func(t *testing.T, store sessionstore.Store, id string) { sweepCreate(t, store, row(id)) },
		tick: func(ctx context.Context, store sessionstore.Store, hook *claimHook) error {
			_, err := watchdog.New(store, watchdog.StaticTenants{"acme"}, cfg, nil).WithTerminalHook(hook).Tick(ctx, sweepDue)
			return err
		},
	}
}

func blockingSubjects() []blockingSubject {
	subs := []blockingSubject{
		watchdogSubject("watchdog idle expiry",
			watchdog.Config{MaxIdleSeconds: 600, MaxSessionAgeSeconds: sweepOff},
			func(id string) sessionstore.Session {
				return sessionstore.Session{ID: id, TenantID: "acme", State: session.StateRunning, LastAgentActivityAt: sweepBorn}
			}),
		watchdogSubject("watchdog suspended lifetime",
			watchdog.Config{MaxSuspendedSessionSeconds: 600, MaxSuspendedPodHoldSeconds: sweepOff},
			func(id string) sessionstore.Session {
				return sessionstore.Session{ID: id, TenantID: "acme", State: session.StateSuspended, SuspendedAt: sweepBorn}
			}),
		watchdogSubject("watchdog maxSessionAge",
			watchdog.Config{MaxSessionAgeSeconds: 600, MaxIdleSeconds: sweepOff},
			func(id string) sessionstore.Session {
				return sessionstore.Session{ID: id, TenantID: "acme", State: session.StateRunning, LastAgentActivityAt: sweepDue}
			}),
	}
	for _, sub := range continuationSubjects()[1:] {
		sub := sub
		subs = append(subs, blockingSubject{
			name: sub.name,
			seed: func(t *testing.T, store sessionstore.Store, id string) { sub.seed(t, store, "acme", id) },
			tick: sub.tick,
		})
	}
	return subs
}

// spec: §10.1.1 (Primary:), §7.1 (Terminal pipeline context:)
//
// diagnosis: a failure means a sweep ran a terminal pipeline inline, so a
// slow seal delayed the next row; the claim closed before the pipeline
// returned, so the gateway's shutdown wait would not observe it; or the
// pipeline inherited the pass's context and a cancelled pass cut it.
func TestBlockingTerminalHookHoldsItsClaimWithoutDelayingThePass_spec_7_1(t *testing.T) {
	for _, sub := range blockingSubjects() {
		t.Run(sub.name, func(t *testing.T) {
			store := memstore.New()
			ids := []string{"first", "second"}
			for _, id := range ids {
				sub.seed(t, store, id)
			}
			hook := newClaimHook(true)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			done := make(chan error, 1)
			go func() { done <- sub.tick(ctx, store, hook) }()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("pass error = %v", err)
				}
			case <-time.After(10 * time.Second):
				close(hook.block)
				t.Fatal("the pass waited on a blocked terminal hook")
			}
			for _, id := range ids {
				assertSweepTerminal(t, store, "acme", id, true)
			}
			for range ids {
				select {
				case <-hook.entered:
				case <-time.After(5 * time.Second):
					close(hook.block)
					t.Fatal("a dispatched terminal hook never started")
				}
			}
			if hook.waitClaimsClosed(50 * time.Millisecond) {
				t.Fatal("the shutdown wait saw no open claim while the terminal hooks were blocked")
			}
			for _, id := range ids {
				if n := hook.openFor(id); n != 1 {
					t.Errorf("%s has %d open claims while its hook blocks, want 1", id, n)
				}
			}

			cancel()
			hook.mu.Lock()
			for _, id := range ids {
				if err := hook.ctxs[id].Err(); err != nil {
					t.Errorf("%s hook context error after the pass was cancelled = %v, want none", id, err)
				}
			}
			hook.mu.Unlock()

			close(hook.block)
			if !hook.waitClaimsClosed(5 * time.Second) {
				t.Fatal("terminal claims still open after the hooks returned")
			}
			hook.mu.Lock()
			defer hook.mu.Unlock()
			if hook.earlyEnd != 0 {
				t.Errorf("%d claims ended before their hook returned, want none", hook.earlyEnd)
			}
		})
	}
}
