// SPDX-License-Identifier: MIT

package sessionevents_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
)

func ts() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

func TestPublishAssignsMonotonicSeq(t *testing.T) {
	b := sessionevents.NewBus(0)
	e1 := b.Publish("sess_1", "message", `{}`, ts())
	e2 := b.Publish("sess_1", "response", `{}`, ts())
	if e1.Seq != 1 || e2.Seq != 2 {
		t.Errorf("seq: %d, %d", e1.Seq, e2.Seq)
	}
	// Per-session sequencing.
	eb := b.Publish("sess_2", "message", `{}`, ts())
	if eb.Seq != 1 {
		t.Errorf("sess_2 seq should restart at 1, got %d", eb.Seq)
	}
}

// spec: §10.1 line 45 — Broadcast pushes a platform-level event to every
// session that has a live subscriber, assigning each its own per-session
// Seq, and returns the number of sessions reached. Sessions with no live
// subscriber are skipped.
func TestBroadcastReachesActiveSessions_spec_10_1(t *testing.T) {
	b := sessionevents.NewBus(0)
	// Two sessions with live subscribers; a third has only history.
	subA, err := b.SubscribeForTenant("acme", "sess_a", 0, 8)
	if err != nil {
		t.Fatalf("subscribe a: %v", err)
	}
	defer subA.Close()
	subB, err := b.SubscribeForTenant("acme", "sess_b", 0, 8)
	if err != nil {
		t.Fatalf("subscribe b: %v", err)
	}
	defer subB.Close()
	// sess_c has an event in history but no live subscriber.
	b.PublishForTenant("acme", "sess_c", "message", `{}`, ts())

	reached := b.Broadcast("PLATFORM_DEGRADED", `{"reason":"dual_store_unavailable","retry_after":10}`, ts())
	if reached != 2 {
		t.Fatalf("Broadcast reached %d sessions, want 2 (only those with live subscribers)", reached)
	}
	for _, tc := range []struct {
		name string
		sub  *sessionevents.Subscription
	}{{"sess_a", subA}, {"sess_b", subB}} {
		select {
		case ev := <-tc.sub.Events():
			if ev.Type != "PLATFORM_DEGRADED" {
				t.Errorf("%s: type=%q, want PLATFORM_DEGRADED", tc.name, ev.Type)
			}
			if ev.Seq != 1 {
				t.Errorf("%s: seq=%d, want 1", tc.name, ev.Seq)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s: did not receive PLATFORM_DEGRADED broadcast", tc.name)
		}
	}
}

// spec: §10.1 — Broadcast on a bus with no live subscribers is a no-op
// that reaches zero sessions.
func TestBroadcastNoSubscribersReachesZero_spec_10_1(t *testing.T) {
	b := sessionevents.NewBus(0)
	b.PublishForTenant("acme", "sess_a", "message", `{}`, ts())
	if reached := b.Broadcast("PLATFORM_DEGRADED", `{}`, ts()); reached != 0 {
		t.Fatalf("Broadcast reached %d, want 0 with no live subscribers", reached)
	}
}

func TestSubscribeReceivesLiveEvents(t *testing.T) {
	b := sessionevents.NewBus(0)
	sub := b.Subscribe("sess_1", 0, 8)
	defer sub.Close()

	b.Publish("sess_1", "message", `{"n":1}`, ts())
	select {
	case ev := <-sub.Events():
		if ev.Type != "message" || ev.Seq != 1 {
			t.Errorf("event: %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("did not receive published event")
	}
}

func TestSubscribeBacklogReplaysHistory(t *testing.T) {
	b := sessionevents.NewBus(0)
	b.Publish("sess_1", "e1", `{}`, ts())
	b.Publish("sess_1", "e2", `{}`, ts())
	b.Publish("sess_1", "e3", `{}`, ts())

	// Reconnect with cursor afterSeq=1 → backlog has e2, e3.
	sub := b.Subscribe("sess_1", 1, 8)
	defer sub.Close()
	if len(sub.Backlog) != 2 {
		t.Fatalf("backlog: got %d, want 2", len(sub.Backlog))
	}
	if sub.Backlog[0].Seq != 2 || sub.Backlog[1].Seq != 3 {
		t.Errorf("backlog seqs: %+v", sub.Backlog)
	}
}

func TestSubscribeNoBacklogWhenCaughtUp(t *testing.T) {
	b := sessionevents.NewBus(0)
	b.Publish("sess_1", "e1", `{}`, ts())
	sub := b.Subscribe("sess_1", 1, 8)
	defer sub.Close()
	if len(sub.Backlog) != 0 {
		t.Errorf("caught-up subscriber should have empty backlog: %+v", sub.Backlog)
	}
}

func TestMultipleSubscribersAllReceive(t *testing.T) {
	b := sessionevents.NewBus(0)
	a := b.Subscribe("sess_1", 0, 8)
	defer a.Close()
	c := b.Subscribe("sess_1", 0, 8)
	defer c.Close()

	b.Publish("sess_1", "broadcast", `{}`, ts())
	for i, sub := range []*sessionevents.Subscription{a, c} {
		select {
		case ev := <-sub.Events():
			if ev.Type != "broadcast" {
				t.Errorf("subscriber %d: %+v", i, ev)
			}
		case <-time.After(time.Second):
			t.Errorf("subscriber %d did not receive", i)
		}
	}
}

func TestCloseStopsDelivery(t *testing.T) {
	b := sessionevents.NewBus(0)
	sub := b.Subscribe("sess_1", 0, 8)
	sub.Close()
	// Channel should be closed.
	if _, open := <-sub.Events(); open {
		t.Error("Events channel should be closed after Close")
	}
	// Publishing after Close must not panic.
	b.Publish("sess_1", "after-close", `{}`, ts())
}

func TestCloseIsIdempotent(t *testing.T) {
	b := sessionevents.NewBus(0)
	sub := b.Subscribe("sess_1", 0, 8)
	sub.Close()
	sub.Close() // must not panic
}

func TestHistoryBoundedByMaxHistory(t *testing.T) {
	b := sessionevents.NewBus(3)
	for i := 0; i < 10; i++ {
		b.Publish("sess_1", "e", `{}`, ts())
	}
	hist := b.History("sess_1", 0)
	if len(hist) != 3 {
		t.Fatalf("history should be bounded to 3, got %d", len(hist))
	}
	// The retained window is the most-recent 3 (seq 8,9,10).
	if hist[0].Seq != 8 || hist[2].Seq != 10 {
		t.Errorf("retained window: %+v", hist)
	}
}

func TestHistoryCursor(t *testing.T) {
	b := sessionevents.NewBus(0)
	for i := 0; i < 5; i++ {
		b.Publish("sess_1", "e", `{}`, ts())
	}
	hist := b.History("sess_1", 3)
	if len(hist) != 2 || hist[0].Seq != 4 {
		t.Errorf("cursor history: %+v", hist)
	}
}

func TestSlowSubscriberDoesNotBlockPublish(t *testing.T) {
	b := sessionevents.NewBus(0)
	// Buffer size 1 — fill it, then publish more.
	sub := b.Subscribe("sess_1", 0, 1)
	defer sub.Close()
	for i := 0; i < 10; i++ {
		// Must not block even though the subscriber never drains.
		b.Publish("sess_1", "e", `{}`, ts())
	}
	// History still has all 10 for a reconnect-with-cursor.
	if len(b.History("sess_1", 0)) != 10 {
		t.Error("all events should be retained in history")
	}
}

// spec: §7.2 tenant isolation — once a session id is published under a
// tenant, a SubscribeForTenant from a different tenant must be rejected
// so a future call site that drops the store.Get precheck cannot
// silently deliver cross-tenant events.
func TestSubscribeForTenantRejectsMismatch_spec_7_2(t *testing.T) {
	b := sessionevents.NewBus(0)
	b.PublishForTenant("acme", "sess_1", "message", `{}`, ts())

	sub, err := b.SubscribeForTenant("globex", "sess_1", 0, 8)
	if err == nil {
		sub.Close()
		t.Fatal("SubscribeForTenant with foreign tenant must fail")
	}
	if err != sessionevents.ErrTenantMismatch {
		t.Errorf("err = %v, want ErrTenantMismatch", err)
	}
}

// spec: §7.2 — the legitimate tenant can subscribe and receive events.
func TestSubscribeForTenantMatchingTenantSucceeds_spec_7_2(t *testing.T) {
	b := sessionevents.NewBus(0)
	b.PublishForTenant("acme", "sess_1", "message", `{}`, ts())

	sub, err := b.SubscribeForTenant("acme", "sess_1", 0, 8)
	if err != nil {
		t.Fatalf("SubscribeForTenant: %v", err)
	}
	defer sub.Close()
	if len(sub.Backlog) != 1 {
		t.Errorf("backlog len = %d, want 1", len(sub.Backlog))
	}
}

// A tenant binding once set is frozen: a later PublishForTenant under
// a different tenant is dropped (defensive against a buggy caller).
func TestPublishForTenantFrozenAfterFirstPublish_spec_7_2(t *testing.T) {
	b := sessionevents.NewBus(0)
	b.PublishForTenant("acme", "sess_1", "e", `{}`, ts())
	// Globex tries to publish on the same session id; the bus drops
	// the event (returns zero-value).
	ev := b.PublishForTenant("globex", "sess_1", "e", `{}`, ts())
	if ev.Seq != 0 {
		t.Errorf("foreign-tenant publish should be dropped, got Seq=%d", ev.Seq)
	}
	// History must contain only the acme event.
	hist := b.History("sess_1", 0)
	if len(hist) != 1 {
		t.Errorf("history len = %d, want 1 (the dropped publish leaked)", len(hist))
	}
}

// Untenanted Publish/Subscribe still works (legacy entry points kept
// for tests and back-compat).
func TestSubscribeUntenantedStillWorks(t *testing.T) {
	b := sessionevents.NewBus(0)
	b.Publish("sess_1", "e", `{}`, ts())
	sub := b.Subscribe("sess_1", 0, 8)
	defer sub.Close()
	if len(sub.Backlog) != 1 {
		t.Errorf("untenanted subscribe backlog len = %d, want 1", len(sub.Backlog))
	}
}

// When no tenant has ever been registered (only untenanted Publish), a
// SubscribeForTenant is permissive — the bus has no binding to enforce.
// This matches the defense-in-depth design: enforcement triggers only
// after a tenant binding exists.
func TestSubscribeForTenantPermissiveWhenNoBinding(t *testing.T) {
	b := sessionevents.NewBus(0)
	b.Publish("sess_1", "e", `{}`, ts())
	sub, err := b.SubscribeForTenant("acme", "sess_1", 0, 8)
	if err != nil {
		t.Errorf("SubscribeForTenant without prior binding should be permissive, got %v", err)
	}
	if sub != nil {
		sub.Close()
	}
}

// spec: §7.2 line 143 — OldestRetainedSeq reports the smallest Seq in
// the buffer so the SSE handler can detect a cursor-eviction gap and
// emit gap_detected / checkpoint_boundary before replaying the backlog.
func TestOldestRetainedSeqAdvancesWithEviction_spec_7_2(t *testing.T) {
	b := sessionevents.NewBus(3)
	// Empty session: ok=false, value=0.
	if seq, ok := b.OldestRetainedSeq("sess_1"); ok || seq != 0 {
		t.Errorf("empty session: got (%d, %v), want (0, false)", seq, ok)
	}
	b.Publish("sess_1", "e", `{}`, ts())
	if seq, ok := b.OldestRetainedSeq("sess_1"); !ok || seq != 1 {
		t.Errorf("single event: got (%d, %v), want (1, true)", seq, ok)
	}
	for i := 0; i < 5; i++ {
		b.Publish("sess_1", "e", `{}`, ts())
	}
	// maxHistory=3 → kept 4,5,6 (the most-recent three after eviction).
	if seq, ok := b.OldestRetainedSeq("sess_1"); !ok || seq != 4 {
		t.Errorf("after eviction: got (%d, %v), want (4, true)", seq, ok)
	}
}

// spec: §7.2 line 143 — OldestRetainedSeq does not surface evictions
// from a different session (per-session bookkeeping).
func TestOldestRetainedSeqIsPerSession_spec_7_2(t *testing.T) {
	b := sessionevents.NewBus(0)
	b.Publish("sess_1", "e", `{}`, ts())
	b.Publish("sess_1", "e", `{}`, ts())
	if seq, ok := b.OldestRetainedSeq("sess_2"); ok || seq != 0 {
		t.Errorf("foreign session: got (%d, %v), want (0, false)", seq, ok)
	}
}

// fakeLastSeqStore implements both LastSeqPersister and LastSeqLoader
// for the §7.3 line 397 durable counter wiring tests. F-7.3.3.
type fakeLastSeqStore struct {
	mu        sync.Mutex
	persisted map[string]int64
	loadErr   error
	loadCount int
	advCount  int
}

func newFakeLastSeqStore() *fakeLastSeqStore {
	return &fakeLastSeqStore{persisted: map[string]int64{}}
}

func (f *fakeLastSeqStore) seed(tenantID, sessionID string, seq int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.persisted[tenantID+"/"+sessionID] = seq
}

func (f *fakeLastSeqStore) LoadLastSeq(_ context.Context, tenantID, sessionID string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loadCount++
	if f.loadErr != nil {
		return 0, f.loadErr
	}
	return f.persisted[tenantID+"/"+sessionID], nil
}

func (f *fakeLastSeqStore) AdvanceLastSeq(_ context.Context, tenantID, sessionID string, seq int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.advCount++
	k := tenantID + "/" + sessionID
	if seq > f.persisted[k] {
		f.persisted[k] = seq
	}
	return nil
}

func (f *fakeLastSeqStore) snapshot(tenantID, sessionID string) int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.persisted[tenantID+"/"+sessionID]
}

func (f *fakeLastSeqStore) loads() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.loadCount
}

func (f *fakeLastSeqStore) advances() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.advCount
}

// TestPublishAdvancesPersistedLastSeq_spec_7_3 covers F-7.3.3 / §7.3
// line 397: every Publish must durably advance the per-session
// monotonic counter so the value survives replica restart and
// coordinator handoff. The Bus calls AdvanceLastSeq asynchronously so
// the test polls until the writer drains.
func TestPublishAdvancesPersistedLastSeq_spec_7_3(t *testing.T) {
	store := newFakeLastSeqStore()
	b := sessionevents.NewBus(0).
		WithLastSeqPersister(store).
		WithLastSeqLoader(store)
	for i := 0; i < 3; i++ {
		b.PublishForTenant("tenant_a", "sess_1", "event", `{}`, ts())
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if store.snapshot("tenant_a", "sess_1") == 3 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if got := store.snapshot("tenant_a", "sess_1"); got != 3 {
		t.Errorf("persisted last_seq = %d, want 3", got)
	}
}

// TestPublishSeedsFromPersistedLastSeq_spec_7_3 covers F-7.3.3 / §7.3
// line 397 + §10.4 coordinator-handoff: a fresh replica picking up a
// session whose persisted last_seq is 5 must continue with Seq=6 on
// its next publish (never rewinding).
func TestPublishSeedsFromPersistedLastSeq_spec_7_3(t *testing.T) {
	store := newFakeLastSeqStore()
	store.seed("tenant_a", "sess_1", 5)
	b := sessionevents.NewBus(0).
		WithLastSeqPersister(store).
		WithLastSeqLoader(store)
	ev := b.PublishForTenant("tenant_a", "sess_1", "event", `{}`, ts())
	if ev.Seq != 6 {
		t.Errorf("first publish after seed: Seq = %d, want 6", ev.Seq)
	}
}

// TestSeedLookupRunsAtMostOncePerSession_spec_7_3 verifies the seed
// loader is consulted only on the first publish for a session — later
// publishes must use the in-memory counter so a fresh Postgres lookup
// does not run on every event.
func TestSeedLookupRunsAtMostOncePerSession_spec_7_3(t *testing.T) {
	store := newFakeLastSeqStore()
	b := sessionevents.NewBus(0).
		WithLastSeqPersister(store).
		WithLastSeqLoader(store)
	for i := 0; i < 4; i++ {
		b.PublishForTenant("tenant_a", "sess_1", "event", `{}`, ts())
	}
	if got := store.loads(); got != 1 {
		t.Errorf("LoadLastSeq invocations = %d, want 1", got)
	}
}

// TestSeedLoadErrorLeavesCounterAtZero_spec_7_3 covers the
// "best-effort, degrade to local" posture: a loader failure leaves the
// Bus on its local counter starting at 1 so a Postgres outage cannot
// stall publishes.
func TestSeedLoadErrorLeavesCounterAtZero_spec_7_3(t *testing.T) {
	store := newFakeLastSeqStore()
	store.loadErr = errLoadFailed
	b := sessionevents.NewBus(0).
		WithLastSeqLoader(store)
	ev := b.PublishForTenant("tenant_a", "sess_1", "event", `{}`, ts())
	if ev.Seq != 1 {
		t.Errorf("first publish after load error: Seq = %d, want 1 (loader degrades to local)", ev.Seq)
	}
}

// TestPersisterAndLoaderAreNoopWhenUnwired_spec_7_3 ensures the
// single-replica default path keeps working without either hook.
func TestPersisterAndLoaderAreNoopWhenUnwired_spec_7_3(t *testing.T) {
	b := sessionevents.NewBus(0)
	ev := b.PublishForTenant("tenant_a", "sess_1", "event", `{}`, ts())
	if ev.Seq != 1 {
		t.Errorf("unwired Bus Seq = %d, want 1", ev.Seq)
	}
}

// TestUntenantedPublishSkipsPersistence_spec_7_3 covers the
// tenant-isolation contract: the durable persister is only invoked for
// tenant-scoped publishes (the legacy untenanted entry point is kept
// for internal bus self-tests only).
func TestUntenantedPublishSkipsPersistence_spec_7_3(t *testing.T) {
	store := newFakeLastSeqStore()
	b := sessionevents.NewBus(0).WithLastSeqPersister(store)
	b.Publish("sess_1", "event", `{}`, ts())
	// Give the goroutine pool a chance to run (if it were going to).
	time.Sleep(10 * time.Millisecond)
	if got := store.advances(); got != 0 {
		t.Errorf("AdvanceLastSeq invoked for untenanted publish: %d", got)
	}
}

var errLoadFailed = errors.New("sessionevents_test: load failed")

// spec: §10.4 line 389 / §16 catalog lenny_event_bus_replay_buffer_utilization.
// F-10.4.11.
func TestMaxReplayBufferUtilization_Empty(t *testing.T) {
	b := sessionevents.NewBus(64)
	if got := b.MaxReplayBufferUtilization(); got != 0 {
		t.Errorf("empty bus utilization: got %v want 0", got)
	}
}

func TestMaxReplayBufferUtilization_PartialFill_spec_10_4(t *testing.T) {
	b := sessionevents.NewBus(8)
	for i := 0; i < 2; i++ {
		b.Publish("sess_a", "e", `{}`, ts())
	}
	// 2/8 = 0.25
	if got := b.MaxReplayBufferUtilization(); got != 0.25 {
		t.Errorf("partial-fill utilization: got %v want 0.25", got)
	}
}

func TestMaxReplayBufferUtilization_FullBuffer_spec_10_4(t *testing.T) {
	b := sessionevents.NewBus(4)
	for i := 0; i < 10; i++ {
		b.Publish("sess_a", "e", `{}`, ts())
	}
	// Bounded at 4; ratio is 1.0 once full.
	if got := b.MaxReplayBufferUtilization(); got != 1.0 {
		t.Errorf("full-buffer utilization: got %v want 1.0", got)
	}
}

func TestMaxReplayBufferUtilization_WorstAcrossSessions_spec_10_4(t *testing.T) {
	b := sessionevents.NewBus(8)
	for i := 0; i < 2; i++ {
		b.Publish("sess_quiet", "e", `{}`, ts())
	}
	for i := 0; i < 6; i++ {
		b.Publish("sess_busy", "e", `{}`, ts())
	}
	// Worst is sess_busy at 6/8 = 0.75.
	if got := b.MaxReplayBufferUtilization(); got != 0.75 {
		t.Errorf("worst-across-sessions utilization: got %v want 0.75", got)
	}
}
