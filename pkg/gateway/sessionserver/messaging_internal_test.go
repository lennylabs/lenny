// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// recordingExpiry captures message_expired emissions for assertion.
type recordingExpiry struct {
	events []sessioninbox.MessageExpiredEvent
}

func (r *recordingExpiry) MessageExpired(_, _ string, ev sessioninbox.MessageExpiredEvent) {
	r.events = append(r.events, ev)
}
func (r *recordingExpiry) InboxCleared(string, string, sessioninbox.InboxClearedEvent) {}

var msgTestClock = func() time.Time { return time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC) }

// testMessaging builds a Coordinator whose inbox and DLQ are returned so
// a test can seed them directly before driving a Server transition.
func testMessaging(t *testing.T, em sessioninbox.Emitter) (*sessioninbox.Coordinator, *sessioninbox.MemoryInbox, *sessioninbox.DLQ) {
	t.Helper()
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	inbox := sessioninbox.NewMemoryInbox(10)
	dlq := sessioninbox.NewDLQ(rc, 10)
	coord := sessioninbox.NewCoordinator(sessioninbox.Config{
		Inbox: inbox, DLQ: dlq, Emitter: em, Now: msgTestClock,
	})
	return coord, inbox, dlq
}

func seedMsg(id, sender string) sessioninbox.Message {
	return sessioninbox.Message{
		MessageID: id, SenderSessionID: sender,
		Payload: []byte(`{}`), EnqueuedAt: msgTestClock(),
	}
}

// spec: §7.2 line 343 / §7.3 line 425 — a terminal transition drains the
// session's DLQ and emits message_expired(target_terminated) to each
// registered sender. Exercises emitTerminalLifecycle →
// drainMessagingOnTerminal → Coordinator.DrainOnTerminal. F-7.3.12.
func TestEmitTerminalLifecycleDrainsDLQ_spec_7_3_12(t *testing.T) {
	em := &recordingExpiry{}
	coord, _, dlq := testMessaging(t, em)
	ctx := context.Background()
	if _, err := dlq.Enqueue(ctx, "acme", "sess-t", seedMsg("m1", "snd-x"), time.Hour); err != nil {
		t.Fatal(err)
	}

	srv := New(memstore.New(), Options{Messaging: coord, Clock: msgTestClock})
	srv.emitTerminalLifecycle(ctx, sessionstore.Session{
		ID: "sess-t", TenantID: "acme", State: session.StateCompleted,
	})

	if len(em.events) != 1 {
		t.Fatalf("message_expired emissions = %d, want 1", len(em.events))
	}
	if em.events[0].Reason != sessioninbox.ReasonTargetTerminated {
		t.Fatalf("reason = %q, want target_terminated", em.events[0].Reason)
	}
	if em.events[0].TargetSessionID != "sess-t" || em.events[0].MessageID != "m1" {
		t.Fatalf("event = %+v, want target sess-t / msg m1", em.events[0])
	}
	if n, _ := dlq.Len(ctx, "acme", "sess-t"); n != 0 {
		t.Fatalf("dlq len after terminal drain = %d, want 0", n)
	}
}

// spec: §7.2 lines 305-311 — entering resume_pending drains the in-memory
// inbox to the DLQ. Exercises migrateInboxOnResumePending. F-7.2.4.
func TestMigrateInboxOnResumePendingWiring_spec_7_2_305(t *testing.T) {
	coord, inbox, dlq := testMessaging(t, &recordingExpiry{})
	ctx := context.Background()
	if _, err := inbox.Enqueue(ctx, "acme", "sess-r", seedMsg("m1", "snd")); err != nil {
		t.Fatal(err)
	}

	srv := New(memstore.New(), Options{Messaging: coord, Clock: msgTestClock})
	srv.migrateInboxOnResumePending(ctx, sessionstore.Session{
		ID: "sess-r", TenantID: "acme", State: session.StateResumePending, PoolRef: "pool-a",
	})

	if n, _ := inbox.Len(ctx, "acme", "sess-r"); n != 0 {
		t.Fatalf("inbox len after migration = %d, want 0", n)
	}
	if n, _ := dlq.Len(ctx, "acme", "sess-r"); n != 1 {
		t.Fatalf("dlq len after migration = %d, want 1 (inbox message preserved)", n)
	}
}

// A terminal transition with no messaging coordinator wired must not
// panic (the dev / no-Redis posture).
func TestEmitTerminalLifecycleNoMessagingIsSafe(t *testing.T) {
	srv := New(memstore.New(), Options{Clock: func() time.Time { return time.Unix(0, 0) }})
	srv.emitTerminalLifecycle(context.Background(), sessionstore.Session{
		ID: "s", TenantID: "acme", State: session.StateCompleted,
	})
	srv.migrateInboxOnResumePending(context.Background(), sessionstore.Session{
		ID: "s", TenantID: "acme", State: session.StateResumePending,
	})
}

// spec: §15.4.1 — NewBusEmitter publishes message_expired onto the
// sender session's SSE stream so a reconnecting sender replays it.
func TestBusEmitterPublishesMessageExpired_spec_15_4_1(t *testing.T) {
	bus := sessionevents.NewBus(16)
	em := NewBusEmitter(bus, msgTestClock)
	sub, err := bus.SubscribeForTenant("acme", "snd", 0, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	em.MessageExpired("acme", "snd",
		sessioninbox.NewMessageExpiredEvent("msg_1", "tgt", sessioninbox.ReasonTargetTerminated, msgTestClock()))
	select {
	case ev := <-sub.Events():
		if ev.Type != sessioninbox.EventMessageExpired {
			t.Fatalf("event type = %q, want message_expired", ev.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("no event published to sender stream")
	}
}
