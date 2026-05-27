// SPDX-License-Identifier: MIT

package sessionevents_test

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/sessionevents"
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
