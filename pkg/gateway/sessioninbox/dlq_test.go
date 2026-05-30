// SPDX-License-Identifier: MIT

package sessioninbox

import (
	"context"
	"testing"
	"time"
)

// spec: §7.2 line 341 — DLQ entries are delivered in FIFO order; DrainAll
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

// spec: §7.2 line 341 — on maxDLQSize overflow the oldest (lowest-score)
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

// spec: §7.2 line 341 — SweepExpired removes entries whose absolute
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

// spec: §7.2 line 341 — a sweep before any entry expires removes nothing.
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
