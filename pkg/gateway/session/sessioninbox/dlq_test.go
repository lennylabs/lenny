// SPDX-License-Identifier: MIT

package sessioninbox

import (
	"context"
	"testing"
	"time"
)

// spec: §7.2 — DLQ entries are delivered in FIFO order; DrainAll
// reads ascending-score (earliest-enqueued first) and clears the key.
func TestDLQ_EnqueueDrainFIFO_spec_7_2_341(t *testing.T) {
	d := NewDLQ(newRedisT(t), 10)
	ctx := context.Background()
	base := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	for i, id := range []string{"m1", "m2", "m3"} {
		m := msg(id, "snd")
		m.EnqueuedAt = base.Add(time.Duration(i) * time.Second)
		if _, err := d.Enqueue(ctx, "acme", "s", m, time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	out, err := d.DrainAll(ctx, "acme", "s")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 || out[0].MessageID != "m1" || out[2].MessageID != "m3" {
		t.Fatalf("drain = %+v, want m1,m2,m3 by score", out)
	}
	if n, _ := d.Len(ctx, "acme", "s"); n != 0 {
		t.Fatalf("len after DrainAll = %d, want 0", n)
	}
}

// spec: §7.2 — on maxDLQSize overflow the oldest (lowest-score)
// entry is evicted and returned for the message_dropped(dlq_overflow)
// receipt.
func TestDLQ_OverflowDropsOldest_spec_7_2_341(t *testing.T) {
	d := NewDLQ(newRedisT(t), 2)
	ctx := context.Background()
	base := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	enq := func(id string, off int) *Message {
		m := msg(id, "snd")
		m.EnqueuedAt = base.Add(time.Duration(off) * time.Second)
		dropped, err := d.Enqueue(ctx, "acme", "s", m, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		return dropped
	}
	enq("m1", 0)
	enq("m2", 1)
	dropped := enq("m3", 2)
	if dropped == nil || dropped.MessageID != "m1" {
		t.Fatalf("dropped = %+v, want m1 (lowest score)", dropped)
	}
	if n, _ := d.Len(ctx, "acme", "s"); n != 2 {
		t.Fatalf("len = %d, want 2 (capped)", n)
	}
}

// spec: §7.2 — SweepExpired removes entries whose absolute
// expiry has passed and returns them for the message_expired event;
// non-expired entries remain.
func TestDLQ_SweepExpired_spec_7_2_341(t *testing.T) {
	d := NewDLQ(newRedisT(t), 10)
	ctx := context.Background()
	base := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	// m1 expires at base+1s, m2 at base+1h.
	m1 := msg("m1", "snd")
	m1.EnqueuedAt = base
	if _, err := d.Enqueue(ctx, "acme", "s", m1, time.Second); err != nil {
		t.Fatal(err)
	}
	m2 := msg("m2", "snd")
	m2.EnqueuedAt = base
	if _, err := d.Enqueue(ctx, "acme", "s", m2, time.Hour); err != nil {
		t.Fatal(err)
	}
	expired, err := d.SweepExpired(ctx, "acme", "s", base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 1 || expired[0].MessageID != "m1" {
		t.Fatalf("expired = %+v, want only m1", expired)
	}
	if n, _ := d.Len(ctx, "acme", "s"); n != 1 {
		t.Fatalf("len after sweep = %d, want 1 (m2 survives)", n)
	}
}

// spec: §7.2 — a sweep before any entry expires removes nothing.
func TestDLQ_SweepExpired_NoneExpired_spec_7_2_341(t *testing.T) {
	d := NewDLQ(newRedisT(t), 10)
	ctx := context.Background()
	base := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	m := msg("m1", "snd")
	m.EnqueuedAt = base
	if _, err := d.Enqueue(ctx, "acme", "s", m, time.Hour); err != nil {
		t.Fatal(err)
	}
	expired, err := d.SweepExpired(ctx, "acme", "s", base.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 0 {
		t.Fatalf("expired = %+v, want none", expired)
	}
}

// TestDLQSameMillisecondEntriesKeepEnqueueOrder_spec_7_2 enqueues messages
// accepted in the same millisecond, under message IDs whose byte order is
// the reverse of their enqueue order, and checks that PeekOldest and
// DrainAll return them in enqueue order, that an entry enqueued after the
// head of a tie was removed still sorts last, and that CountAcceptedAfter
// counts every tied entry from its own millisecond onward and none from the
// next millisecond.
//
// spec: §7.2 (Message delivery routing — seven paths, path 6)
func TestDLQSameMillisecondEntriesKeepEnqueueOrder_spec_7_2(t *testing.T) {
	d := NewDLQ(newRedisT(t), 10)
	ctx := context.Background()
	at := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	enq := func(id string) {
		t.Helper()
		m := msg(id, "snd")
		m.EnqueuedAt = at
		if _, err := d.Enqueue(ctx, "acme", "s", m, 0); err != nil {
			t.Fatal(err)
		}
	}
	enq("m-z")
	enq("m-y")
	enq("m-x")
	head, member, ok, err := d.PeekOldest(ctx, "acme", "s")
	if err != nil || !ok || head.MessageID != "m-z" {
		t.Fatalf("PeekOldest = %q ok=%v err=%v, want m-z, the first enqueued", head.MessageID, ok, err)
	}
	if _, err := d.Remove(ctx, "acme", "s", member); err != nil {
		t.Fatal(err)
	}
	enq("m-a")
	if n, err := d.CountAcceptedAfter(ctx, "acme", "s", at); err != nil || n != 3 {
		t.Fatalf("CountAcceptedAfter(the shared millisecond) = %d err=%v, want 3", n, err)
	}
	if n, err := d.CountAcceptedAfter(ctx, "acme", "s", at.Add(time.Millisecond)); err != nil || n != 0 {
		t.Fatalf("CountAcceptedAfter(the millisecond after) = %d err=%v, want 0", n, err)
	}
	if n, err := d.CountAcceptedAfter(ctx, "acme", "s", at.Add(-time.Millisecond)); err != nil || n != 3 {
		t.Fatalf("CountAcceptedAfter(the millisecond before) = %d err=%v, want 3", n, err)
	}
	out, err := d.DrainAll(ctx, "acme", "s")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range out {
		got = append(got, m.MessageID)
	}
	if len(got) != 3 || got[0] != "m-y" || got[1] != "m-x" || got[2] != "m-a" {
		t.Fatalf("drain = %v, want [m-y m-x m-a], the enqueue order", got)
	}
}
