// SPDX-License-Identifier: MIT

package sessioninbox

import (
	"context"
	"testing"
	"time"
)

// recordingEmitter captures emitted events for assertions.
type recordingEmitter struct {
	expired []struct {
		tenant, sender string
		ev             MessageExpiredEvent
	}
	cleared []InboxClearedEvent
}

func (r *recordingEmitter) MessageExpired(tenant, sender string, ev MessageExpiredEvent) {
	r.expired = append(r.expired, struct {
		tenant, sender string
		ev             MessageExpiredEvent
	}{tenant, sender, ev})
}

func (r *recordingEmitter) InboxCleared(_, _ string, ev InboxClearedEvent) {
	r.cleared = append(r.cleared, ev)
}

func fixedClock() func() time.Time {
	return func() time.Time { return time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC) }
}

// spec: §7.2 lines 305-311 — the in-memory inbox is drained to the DLQ on
// resume_pending; the count is returned for messagesPreservedInDLQ.
func TestCoordinator_MigrateInboxToDLQ_InMemory_spec_7_2_305(t *testing.T) {
	c := NewCoordinator(Config{
		Inbox: NewMemoryInbox(10), DLQ: NewDLQ(newRedisT(t), 10),
		Now: fixedClock(),
	})
	ctx := context.Background()
	_, _ = c.inbox.Enqueue(ctx, "acme", "s", msg("m1", "snd"))
	_, _ = c.inbox.Enqueue(ctx, "acme", "s", msg("m2", "snd"))
	preserved, err := c.MigrateInboxToDLQ(ctx, "acme", "s", "pool-a")
	if err != nil {
		t.Fatal(err)
	}
	if preserved != 2 {
		t.Fatalf("preserved = %d, want 2", preserved)
	}
	if n, _ := c.inbox.Len(ctx, "acme", "s"); n != 0 {
		t.Fatalf("inbox len = %d, want 0 after drain", n)
	}
	if n, _ := c.dlq.Len(ctx, "acme", "s"); n != 2 {
		t.Fatalf("dlq len = %d, want 2", n)
	}
}

// spec: §7.2 line 305 — in durable mode the Redis inbox is left in place
// with an EXPIRE rather than drained to the DLQ.
func TestCoordinator_MigrateInboxToDLQ_Durable_spec_7_2_305(t *testing.T) {
	rc := newRedisT(t)
	c := NewCoordinator(Config{
		Inbox: NewRedisInbox(rc, 10), DLQ: NewDLQ(rc, 10),
		Now: fixedClock(), Durable: true,
	})
	ctx := context.Background()
	_, _ = c.inbox.Enqueue(ctx, "acme", "s", msg("m1", "snd"))
	preserved, err := c.MigrateInboxToDLQ(ctx, "acme", "s", "pool-a")
	if err != nil {
		t.Fatal(err)
	}
	if preserved != 0 {
		t.Fatalf("preserved = %d, want 0 (durable leaves inbox in place)", preserved)
	}
	if n, _ := c.inbox.Len(ctx, "acme", "s"); n != 1 {
		t.Fatalf("inbox len = %d, want 1 (retained)", n)
	}
	if n, _ := c.dlq.Len(ctx, "acme", "s"); n != 0 {
		t.Fatalf("dlq len = %d, want 0 (no drain in durable mode)", n)
	}
	if ttl, _ := rc.TTL(ctx, inboxKey("acme", "s")).Result(); ttl <= 0 {
		t.Fatalf("durable inbox key TTL = %v, want > 0 (EXPIRE applied)", ttl)
	}
}

// spec: §7.2 line 343 / §7.3 line 425 — terminal drain emits one
// message_expired(target_terminated) per buffered message that carries a
// sender, drawing from both the inbox and the DLQ; the DLQ key is
// deleted.
func TestCoordinator_DrainOnTerminal_spec_7_2_343(t *testing.T) {
	em := &recordingEmitter{}
	c := NewCoordinator(Config{
		Inbox: NewMemoryInbox(10), DLQ: NewDLQ(newRedisT(t), 10),
		Emitter: em, Now: fixedClock(),
	})
	ctx := context.Background()
	_, _ = c.inbox.Enqueue(ctx, "acme", "s", msg("im1", "snd-a"))
	_, _ = c.dlq.Enqueue(ctx, "acme", "s", msg("dm1", "snd-b"), time.Hour)
	drained, err := c.DrainOnTerminal(ctx, "acme", "s")
	if err != nil {
		t.Fatal(err)
	}
	if drained != 2 {
		t.Fatalf("drained = %d, want 2", drained)
	}
	if len(em.expired) != 2 {
		t.Fatalf("emitted %d events, want 2", len(em.expired))
	}
	for _, e := range em.expired {
		if e.ev.Reason != ReasonTargetTerminated {
			t.Fatalf("reason = %q, want target_terminated", e.ev.Reason)
		}
		if e.ev.TargetSessionID != "s" || e.ev.Type != EventMessageExpired {
			t.Fatalf("event = %+v, want target s / message_expired", e.ev)
		}
	}
	if n, _ := c.dlq.Len(ctx, "acme", "s"); n != 0 {
		t.Fatalf("dlq len = %d, want 0 (key deleted)", n)
	}
}

// spec: §7.2 line 343 — a buffered message with no sender session (an
// external-client source) is drained without a message_expired event,
// since there is no sender stream to notify.
func TestCoordinator_DrainOnTerminal_SkipsEmptySender_spec_7_2_343(t *testing.T) {
	em := &recordingEmitter{}
	c := NewCoordinator(Config{
		Inbox: NewMemoryInbox(10), DLQ: NewDLQ(newRedisT(t), 10),
		Emitter: em, Now: fixedClock(),
	})
	ctx := context.Background()
	_, _ = c.inbox.Enqueue(ctx, "acme", "s", msg("im1", "")) // no sender
	_, _ = c.inbox.Enqueue(ctx, "acme", "s", msg("im2", "snd"))
	if _, err := c.DrainOnTerminal(ctx, "acme", "s"); err != nil {
		t.Fatal(err)
	}
	if len(em.expired) != 1 || em.expired[0].ev.MessageID != "im2" {
		t.Fatalf("emitted = %+v, want only im2", em.expired)
	}
}

// spec: §7.2 line 341 / §15.4.1 — SweepExpired emits
// message_expired(dlq_ttl_expired) for each expired DLQ entry.
func TestCoordinator_SweepExpired_spec_7_2_341(t *testing.T) {
	em := &recordingEmitter{}
	clk := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	c := NewCoordinator(Config{
		Inbox: NewMemoryInbox(10), DLQ: NewDLQ(newRedisT(t), 10),
		Emitter: em, Now: func() time.Time { return clk.Add(time.Hour) },
	})
	ctx := context.Background()
	m := msg("m1", "snd")
	m.EnqueuedAt = clk
	_, _ = c.dlq.Enqueue(ctx, "acme", "s", m, time.Second) // expires at clk+1s
	expired, err := c.SweepExpired(ctx, "acme", "s")
	if err != nil {
		t.Fatal(err)
	}
	if expired != 1 || len(em.expired) != 1 {
		t.Fatalf("expired=%d emitted=%d, want 1/1", expired, len(em.expired))
	}
	if em.expired[0].ev.Reason != ReasonDLQTTLExpired {
		t.Fatalf("reason = %q, want dlq_ttl_expired", em.expired[0].ev.Reason)
	}
}

// A nil Coordinator no-ops every operation so a gateway without messaging
// wired never panics on a state transition.
func TestCoordinator_Nil_NoOp(t *testing.T) {
	var c *Coordinator
	ctx := context.Background()
	if p, err := c.MigrateInboxToDLQ(ctx, "acme", "s", "p"); p != 0 || err != nil {
		t.Fatalf("MigrateInboxToDLQ on nil = %d,%v", p, err)
	}
	if d, err := c.DrainOnTerminal(ctx, "acme", "s"); d != 0 || err != nil {
		t.Fatalf("DrainOnTerminal on nil = %d,%v", d, err)
	}
	if e, err := c.SweepExpired(ctx, "acme", "s"); e != 0 || err != nil {
		t.Fatalf("SweepExpired on nil = %d,%v", e, err)
	}
}

// NewCoordinator returns nil when the inbox or DLQ is missing so callers
// treat "messaging not wired" and "no-op coordinator" uniformly.
func TestNewCoordinator_NilWhenIncomplete(t *testing.T) {
	if NewCoordinator(Config{DLQ: NewDLQ(newRedisT(t), 10)}) != nil {
		t.Fatal("want nil coordinator when Inbox missing")
	}
	if NewCoordinator(Config{Inbox: NewMemoryInbox(10)}) != nil {
		t.Fatal("want nil coordinator when DLQ missing")
	}
}
