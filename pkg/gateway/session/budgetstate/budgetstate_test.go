// SPDX-License-Identifier: MIT

package budgetstate_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/budgetstate"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionbudget"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// fakeClock is the sweep's injected clock.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// noopTerminator satisfies the enforcer's terminator; these tests never
// exhaust a budget.
type noopTerminator struct{}

func (noopTerminator) TerminateSession(string, string) {}

// replica is one gateway replica's per-session budget state: an enforcer
// and the sweep over the shared session store.
type replica struct {
	enforcer *sessionbudget.Enforcer
	sweep    *budgetstate.Sweep
}

func newReplica(sessions budgetstate.Sessions, clock *fakeClock, logger *slog.Logger) *replica {
	enf := sessionbudget.New(noopTerminator{}, nil, nil)
	return &replica{
		enforcer: enf,
		sweep: budgetstate.New(budgetstate.Config{
			Sessions: sessions,
			Tracked:  enf.TrackedSessions,
			Forget:   enf.Forget,
			Interval: time.Minute,
			Now:      clock.Now,
			Logger:   logger,
		}),
	}
}

// record settles one proxied request on the replica, which creates the
// session's counter.
func (r *replica) record(t *testing.T, sessionID string) {
	t.Helper()
	ctx := context.Background()
	if exhausted, _ := r.enforcer.Record(ctx, ctx, "acme", sessionID, 1_000_000, 10); exhausted {
		t.Fatalf("Record(%s) exhausted a 1,000,000-token budget", sessionID)
	}
}

func (r *replica) tracked() []string {
	ids := r.enforcer.TrackedSessions()
	sort.Strings(ids)
	return ids
}

func createRunning(t *testing.T, store *memstore.Store, tenant string) string {
	t.Helper()
	id := uuid.NewString()
	if err := store.Create(context.Background(), sessionstore.Session{ID: id, TenantID: tenant, State: session.StateRunning}); err != nil {
		t.Fatalf("create %s: %v", id, err)
	}
	return id
}

func endSession(t *testing.T, store *memstore.Store, tenant, id string, at time.Time) {
	t.Helper()
	if _, err := store.Update(context.Background(), tenant, id, func(r *sessionstore.Session) error {
		from := r.State
		r.State = session.StateCancelled
		sessionstore.MarkTerminated(r, from, at)
		return nil
	}); err != nil {
		t.Fatalf("terminal write of %s: %v", id, err)
	}
}

func contains(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// spec: §11.2 (budget enforcement), §10.1.1 (replica-local state)
//
// A session that ends on replica A, whose terminal pipeline forgets it only
// on A, is forgotten on replica B by B's next sweep pass, while a session
// that is still running keeps its counter on B. Without the sweep, B's
// counter for the ended session stays for the life of the process.
func TestSweepForgetsSessionEndedOnAnotherReplicaWithinOneTick_spec_11_2(t *testing.T) {
	store := memstore.New()
	clock := &fakeClock{now: time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)}
	a := newReplica(store, clock, nil)
	b := newReplica(store, clock, nil)

	ended := createRunning(t, store, "acme")
	live := createRunning(t, store, "acme")
	for _, r := range []*replica{a, b} {
		r.record(t, ended)
		r.record(t, live)
	}

	// Replica A runs the terminal pipeline: the terminal write and the
	// hook-body forget, which reaches only A's own state.
	endSession(t, store, "acme", ended, clock.Now())
	a.enforcer.Forget(ended)
	clock.advance(time.Minute)

	if got := b.sweep.Pass(context.Background()); got != 1 {
		t.Fatalf("replica B pass forgot %d sessions, want 1", got)
	}
	if got := b.tracked(); len(got) != 1 || got[0] != live {
		t.Fatalf("replica B tracks %v after one pass, want only the live session %s", got, live)
	}
	if got := a.sweep.Pass(context.Background()); got != 0 {
		t.Fatalf("replica A pass forgot %d sessions, want 0: A already forgot the ended one and the other is live", got)
	}
	if !contains(a.tracked(), live) {
		t.Fatalf("replica A dropped the live session %s", live)
	}
}

// spec: §11.2, §12.8 (Compliance Interfaces)
//
// A session whose row was erased has no row at all, so it is neither
// terminal nor live. The sweep resolves it through ListAbsentSessions and
// forgets it, across tenants.
func TestSweepForgetsErasedSession_spec_11_2(t *testing.T) {
	store := memstore.New()
	clock := &fakeClock{now: time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)}
	r := newReplica(store, clock, nil)

	erased := createRunning(t, store, "globex")
	live := createRunning(t, store, "acme")
	r.record(t, erased)
	r.record(t, live)
	if err := store.Delete(context.Background(), "globex", erased); err != nil {
		t.Fatalf("delete %s: %v", erased, err)
	}

	if got := r.sweep.Pass(context.Background()); got != 1 {
		t.Fatalf("pass forgot %d sessions, want 1", got)
	}
	if got := r.tracked(); len(got) != 1 || got[0] != live {
		t.Fatalf("tracked %v, want only %s", got, live)
	}
}

// spec: §11.2, §8.6 (Extension state)
//
// A late request on a forgotten terminal session re-creates its counter.
// The sweep does not refuse the request; it removes the re-created entry on
// its next pass.
func TestSweepRemovesEntryReCreatedByLateRequest_spec_11_2(t *testing.T) {
	store := memstore.New()
	clock := &fakeClock{now: time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)}
	r := newReplica(store, clock, nil)

	id := createRunning(t, store, "acme")
	r.record(t, id)
	endSession(t, store, "acme", id, clock.Now())
	clock.advance(time.Minute)
	if got := r.sweep.Pass(context.Background()); got != 1 {
		t.Fatalf("first pass forgot %d, want 1", got)
	}

	r.record(t, id) // the late request
	if !contains(r.tracked(), id) {
		t.Fatalf("late request did not re-create the counter for %s", id)
	}
	clock.advance(time.Minute)
	if got := r.sweep.Pass(context.Background()); got != 1 {
		t.Fatalf("second pass forgot %d, want 1", got)
	}
	if got := r.tracked(); len(got) != 0 {
		t.Fatalf("tracked %v after the second pass, want none", got)
	}
}

// recordingSessions wraps a store and records the size of each batch it is
// asked to resolve; err, when set, fails every read.
type recordingSessions struct {
	inner budgetstate.Sessions

	mu         sync.Mutex
	staleSizes []int
	absentSize []int
	err        error
	failBatch  func(ids []string) bool
	foreign    string
}

func (s *recordingSessions) ListStaleTerminalSessions(ctx context.Context, ids []string, before time.Time) ([]string, error) {
	s.mu.Lock()
	s.staleSizes = append(s.staleSizes, len(ids))
	s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	return s.inner.ListStaleTerminalSessions(ctx, ids, before)
}

func (s *recordingSessions) ListAbsentSessions(ctx context.Context, ids []string) ([]string, error) {
	s.mu.Lock()
	s.absentSize = append(s.absentSize, len(ids))
	s.mu.Unlock()
	if s.failBatch != nil && s.failBatch(ids) {
		return nil, errors.New("postgres: connection reset")
	}
	out, err := s.inner.ListAbsentSessions(ctx, ids)
	if s.foreign != "" {
		out = append(out, s.foreign)
	}
	return out, err
}

// spec: §11.2, §10.1.1
//
// More tracked ids than one batch holds are resolved in batches of at most
// DefaultBatchSize, every batch is read, and every ended session across the
// batches is forgotten.
func TestSweepResolvesMoreThanOneBatch_spec_11_2(t *testing.T) {
	store := memstore.New()
	clock := &fakeClock{now: time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)}
	rec := &recordingSessions{inner: store}
	r := newReplica(rec, clock, nil)

	const n = 2*budgetstate.DefaultBatchSize + 37
	var live []string
	for i := 0; i < n; i++ {
		id := createRunning(t, store, "acme")
		r.record(t, id)
		if i%2 == 0 {
			endSession(t, store, "acme", id, clock.Now())
		} else {
			live = append(live, id)
		}
	}
	clock.advance(time.Minute)

	if got, want := r.sweep.Pass(context.Background()), n-len(live); got != want {
		t.Fatalf("pass forgot %d, want %d", got, want)
	}
	want := []int{budgetstate.DefaultBatchSize, budgetstate.DefaultBatchSize, 37}
	if !equalInts(rec.staleSizes, want) || !equalInts(rec.absentSize, want) {
		t.Fatalf("batch sizes stale=%v absent=%v, want %v for both", rec.staleSizes, rec.absentSize, want)
	}
	sort.Strings(live)
	if got := r.tracked(); !equalStrings(got, live) {
		t.Fatalf("tracked %d ids after the pass, want the %d live ids", len(got), len(live))
	}
}

// spec: §11.2, §10.1.1
//
// A store error keeps every entry of the batch it fails and logs at warn,
// while a batch whose reads succeed is still swept in the same pass. An
// error from the terminal read keeps everything.
func TestSweepStoreErrorRetainsEveryEntryOfTheBatch_spec_11_2(t *testing.T) {
	store := memstore.New()
	clock := &fakeClock{now: time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)}
	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))

	rec := &recordingSessions{inner: store, err: errors.New("postgres: connection refused")}
	r := newReplica(rec, clock, logger)
	for i := 0; i < 3; i++ {
		id := createRunning(t, store, "acme")
		r.record(t, id)
		endSession(t, store, "acme", id, clock.Now())
	}
	clock.advance(time.Minute)
	if got := r.sweep.Pass(context.Background()); got != 0 {
		t.Fatalf("pass forgot %d with a failing store, want 0", got)
	}
	if got := r.tracked(); len(got) != 3 {
		t.Fatalf("tracked %v after a failed pass, want all 3 retained", got)
	}
	if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), "connection refused") {
		t.Fatalf("failed batch did not log at warn: %q", logs.String())
	}

	// The absent read fails only for the batch holding the first id; with a
	// batch size of one, the other two are swept in the same pass.
	first := r.tracked()[0]
	rec.err = nil
	rec.failBatch = func(batch []string) bool { return contains(batch, first) }
	r.sweep = budgetstate.New(budgetstate.Config{
		Sessions: rec, Tracked: r.enforcer.TrackedSessions, Forget: r.enforcer.Forget,
		Interval: time.Minute, BatchSize: 1, Now: clock.Now, Logger: logger,
	})
	if got := r.sweep.Pass(context.Background()); got != 2 {
		t.Fatalf("pass forgot %d, want 2 (the failing batch kept)", got)
	}
	if got := r.tracked(); len(got) != 1 || got[0] != first {
		t.Fatalf("tracked %v, want only the failing batch's %s", got, first)
	}
}

// spec: §11.2
//
// An id the store answers with that was not in the batch is not forgotten,
// so a misbehaving store cannot reclaim another session's state.
func TestSweepForgetsOnlyIdsOfTheBatch_spec_11_2(t *testing.T) {
	store := memstore.New()
	clock := &fakeClock{now: time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)}
	live := createRunning(t, store, "acme")
	other := createRunning(t, store, "acme")
	rec := &recordingSessions{inner: store, foreign: other}

	var forgotten []string
	sweep := budgetstate.New(budgetstate.Config{
		Sessions: rec,
		Tracked:  func() []string { return []string{live} },
		Forget:   func(id string) { forgotten = append(forgotten, id) },
		Now:      clock.Now,
	})
	if got := sweep.Pass(context.Background()); got != 0 || len(forgotten) != 0 {
		t.Fatalf("pass forgot %v (count %d), want nothing", forgotten, got)
	}
}

// spec: §11.2, §10.1.1
//
// A pass with nothing tracked issues no store read, Union drops empty and
// duplicate ids across trackers, and a cancelled context stops a pass
// before its first batch.
func TestSweepEmptyTrackedAndUnion_spec_11_2(t *testing.T) {
	rec := &recordingSessions{inner: memstore.New()}
	sweep := budgetstate.New(budgetstate.Config{
		Sessions: rec,
		Tracked:  func() []string { return nil },
		Forget:   func(string) { t.Fatal("forget called with nothing tracked") },
	})
	if got := sweep.Pass(context.Background()); got != 0 || len(rec.staleSizes) != 0 {
		t.Fatalf("empty pass = %d with %d reads, want 0 and none", got, len(rec.staleSizes))
	}

	got := budgetstate.Union([]string{"b", "", "a"}, nil, []string{"a", "c"})
	if !equalStrings(got, []string{"a", "b", "c"}) {
		t.Fatalf("Union = %v, want [a b c]", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sweep = budgetstate.New(budgetstate.Config{
		Sessions: rec,
		Tracked:  func() []string { return []string{uuid.NewString()} },
		Forget:   func(string) { t.Fatal("forget called on a cancelled pass") },
	})
	if got := sweep.Pass(ctx); got != 0 || len(rec.staleSizes) != 0 {
		t.Fatalf("cancelled pass = %d with %d reads, want 0 and none", got, len(rec.staleSizes))
	}
}

// spec: §11.2, §10.1.1
//
// Run sweeps on its interval and returns when its context ends; a nil sweep
// and a non-positive interval return at once.
func TestSweepRunTicksAndStops_spec_11_2(t *testing.T) {
	store := memstore.New()
	id := uuid.NewString() // no row: absent
	forgot := make(chan string, 16)
	sweep := budgetstate.New(budgetstate.Config{
		Sessions: store,
		Tracked:  func() []string { return []string{id} },
		Forget:   func(s string) { forgot <- s },
		Interval: 5 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { sweep.Run(ctx); close(done) }()
	select {
	case got := <-forgot:
		if got != id {
			t.Fatalf("Run forgot %s, want %s", got, id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not sweep within 5s")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context ended")
	}

	var nilSweep *budgetstate.Sweep
	nilSweep.Run(context.Background())
	budgetstate.New(budgetstate.Config{Sessions: store, Tracked: func() []string { return nil }, Forget: func(string) {}}).Run(context.Background())
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
