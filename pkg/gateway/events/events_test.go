// SPDX-License-Identifier: MIT

package events_test

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/events"
)

func ts() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

func TestPublishAssignsMonotonicSeq(t *testing.T) {
	b := events.NewBus(0)
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
	b := events.NewBus(0)
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
	b := events.NewBus(0)
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
	b := events.NewBus(0)
	b.Publish("sess_1", "e1", `{}`, ts())
	sub := b.Subscribe("sess_1", 1, 8)
	defer sub.Close()
	if len(sub.Backlog) != 0 {
		t.Errorf("caught-up subscriber should have empty backlog: %+v", sub.Backlog)
	}
}

func TestMultipleSubscribersAllReceive(t *testing.T) {
	b := events.NewBus(0)
	a := b.Subscribe("sess_1", 0, 8)
	defer a.Close()
	c := b.Subscribe("sess_1", 0, 8)
	defer c.Close()

	b.Publish("sess_1", "broadcast", `{}`, ts())
	for i, sub := range []*events.Subscription{a, c} {
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
	b := events.NewBus(0)
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
	b := events.NewBus(0)
	sub := b.Subscribe("sess_1", 0, 8)
	sub.Close()
	sub.Close() // must not panic
}

func TestHistoryBoundedByMaxHistory(t *testing.T) {
	b := events.NewBus(3)
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
	b := events.NewBus(0)
	for i := 0; i < 5; i++ {
		b.Publish("sess_1", "e", `{}`, ts())
	}
	hist := b.History("sess_1", 3)
	if len(hist) != 2 || hist[0].Seq != 4 {
		t.Errorf("cursor history: %+v", hist)
	}
}

func TestSlowSubscriberDoesNotBlockPublish(t *testing.T) {
	b := events.NewBus(0)
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
