// SPDX-License-Identifier: MIT

package sessioninbox

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// TestInboxPeekAndAckRemoveOnlyTheAcknowledgedEntry_spec_7_2 asserts, for the
// memory and the Redis inbox, that Peek returns the head without removing it,
// that Ack removes exactly the entry its handle names, that an Ack of a handle
// already removed finds nothing, and that a Peek of an empty inbox reports no
// entry.
//
// spec: §7.2 (Message delivery routing — seven paths, path 6)
func TestInboxPeekAndAckRemoveOnlyTheAcknowledgedEntry_spec_7_2(t *testing.T) {
	ctx := context.Background()
	for name, mk := range map[string]func(t *testing.T) Inbox{
		"memory": func(*testing.T) Inbox { return NewMemoryInbox(10) },
		"redis":  func(t *testing.T) Inbox { return NewRedisInbox(newRedisT(t), 10) },
	} {
		t.Run(name, func(t *testing.T) {
			in := mk(t)
			if _, _, ok, err := in.Peek(ctx, "acme", "s"); ok || err != nil {
				t.Fatalf("Peek(empty) = ok %v err %v, want nothing", ok, err)
			}
			for _, id := range []string{"m1", "m2"} {
				if _, err := in.Enqueue(ctx, "acme", "s", msg(id, "")); err != nil {
					t.Fatalf("Enqueue %s: %v", id, err)
				}
			}
			head, handle, ok, err := in.Peek(ctx, "acme", "s")
			if err != nil || !ok || head.MessageID != "m1" {
				t.Fatalf("Peek = %+v ok %v err %v, want m1", head, ok, err)
			}
			if n, _ := in.Len(ctx, "acme", "s"); n != 2 {
				t.Fatalf("Len after Peek = %d, want 2: Peek removes nothing", n)
			}
			if found, err := in.Ack(ctx, "acme", "s", handle); err != nil || !found {
				t.Fatalf("Ack = %v %v, want the head removed", found, err)
			}
			if found, err := in.Ack(ctx, "acme", "s", handle); err != nil || found {
				t.Errorf("second Ack = %v %v, want nothing found", found, err)
			}
			next, _, ok, _ := in.Peek(ctx, "acme", "s")
			if !ok || next.MessageID != "m2" {
				t.Errorf("Peek after Ack = %+v ok %v, want m2", next, ok)
			}
		})
	}
}

// TestDLQPeekOldestAndRemoveActOnOneEntry_spec_7_2 asserts that PeekOldest
// returns the lowest-scored entry, including one whose expiry score has
// passed, without removing it, and that Remove removes exactly the member it
// names.
//
// spec: §7.2 (Message delivery routing — seven paths, path 6)
func TestDLQPeekOldestAndRemoveActOnOneEntry_spec_7_2(t *testing.T) {
	ctx := context.Background()
	d := NewDLQ(newRedisT(t), 10)
	if _, _, ok, err := d.PeekOldest(ctx, "acme", "s"); ok || err != nil {
		t.Fatalf("PeekOldest(empty) = ok %v err %v, want nothing", ok, err)
	}
	old := msg("m-old", "")
	old.EnqueuedAt = time.Now().Add(-2 * DefaultDLQTTL)
	young := msg("m-young", "")
	young.EnqueuedAt = time.Now()
	for _, m := range []Message{young, old} {
		if _, err := d.Enqueue(ctx, "acme", "s", m, 0); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}
	got, member, ok, err := d.PeekOldest(ctx, "acme", "s")
	if err != nil || !ok || got.MessageID != "m-old" {
		t.Fatalf("PeekOldest = %+v ok %v err %v, want the expired-score m-old", got, ok, err)
	}
	if n, _ := d.Len(ctx, "acme", "s"); n != 2 {
		t.Fatalf("Len after PeekOldest = %d, want 2", n)
	}
	if found, err := d.Remove(ctx, "acme", "s", member); err != nil || !found {
		t.Fatalf("Remove = %v %v, want m-old removed", found, err)
	}
	if found, _ := d.Remove(ctx, "acme", "s", member); found {
		t.Error("a second Remove found the removed member")
	}
	if next, _, _, _ := d.PeekOldest(ctx, "acme", "s"); next.MessageID != "m-young" {
		t.Errorf("PeekOldest after Remove = %q, want m-young", next.MessageID)
	}
	if m, err := DLQMember(young); err != nil || m == "" {
		t.Errorf("DLQMember = %q %v", m, err)
	}
}

// TestDLQPeekOldestUndecodableHeadIsAFailedPeek_spec_7_2 asserts that a DLQ
// head member whose envelope does not decode is returned as an error with ok
// false and no member, and that the member stays in the DLQ, so a caller
// cannot treat the failed peek as an entry and remove it.
//
// spec: §7.2 (Message delivery routing — seven paths, path 6)
func TestDLQPeekOldestUndecodableHeadIsAFailedPeek_spec_7_2(t *testing.T) {
	ctx := context.Background()
	rc := newRedisT(t)
	d := NewDLQ(rc, 10)
	if err := rc.ZAdd(ctx, DLQKey("acme", "s"), redis.Z{Score: 1, Member: "{not an envelope"}).Err(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, member, ok, err := d.PeekOldest(ctx, "acme", "s")
	if err == nil || ok || member != "" || got.MessageID != "" {
		t.Fatalf("PeekOldest = %+v %q ok %v err %v, want a failed peek with no entry", got, member, ok, err)
	}
	if n, _ := d.Len(ctx, "acme", "s"); n != 1 {
		t.Errorf("Len = %d, want the undecodable member left in place", n)
	}
}

// TestCoordinatorHeldAccessForwardsAndToleratesNil_spec_7_2 asserts that the
// coordinator forwards Peek, Ack, PeekOldest, and Remove to its inbox and DLQ,
// and that a nil coordinator answers each with nothing and no error.
//
// spec: §7.2 (Message delivery routing — seven paths, path 6)
func TestCoordinatorHeldAccessForwardsAndToleratesNil_spec_7_2(t *testing.T) {
	ctx := context.Background()
	var nilc *Coordinator
	if _, _, ok, err := nilc.Peek(ctx, "acme", "s"); ok || err != nil {
		t.Error("nil Peek returned an entry or an error")
	}
	if ok, err := nilc.Ack(ctx, "acme", "s", "h"); ok || err != nil {
		t.Error("nil Ack returned found or an error")
	}
	if _, _, ok, err := nilc.PeekOldest(ctx, "acme", "s"); ok || err != nil {
		t.Error("nil PeekOldest returned an entry or an error")
	}
	if ok, err := nilc.Remove(ctx, "acme", "s", "m"); ok || err != nil {
		t.Error("nil Remove returned found or an error")
	}
	c := NewCoordinator(Config{Inbox: NewMemoryInbox(10), DLQ: NewDLQ(newRedisT(t), 10)})
	if _, err := c.EnqueueInbox(ctx, "acme", "s", msg("i1", "")); err != nil {
		t.Fatalf("EnqueueInbox: %v", err)
	}
	if _, err := c.EnqueueDLQ(ctx, "acme", "s", msg("d1", ""), time.Hour); err != nil {
		t.Fatalf("EnqueueDLQ: %v", err)
	}
	_, h, ok, err := c.Peek(ctx, "acme", "s")
	if !ok || err != nil {
		t.Fatalf("Peek: %v %v", ok, err)
	}
	if found, err := c.Ack(ctx, "acme", "s", h); !found || err != nil {
		t.Errorf("Ack: %v %v", found, err)
	}
	_, m, ok, err := c.PeekOldest(ctx, "acme", "s")
	if !ok || err != nil {
		t.Fatalf("PeekOldest: %v %v", ok, err)
	}
	if found, err := c.Remove(ctx, "acme", "s", m); !found || err != nil {
		t.Errorf("Remove: %v %v", found, err)
	}
}
