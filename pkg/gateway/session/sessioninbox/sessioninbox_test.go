// SPDX-License-Identifier: MIT

package sessioninbox

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func msg(id, sender string) Message {
	return Message{
		MessageID:       id,
		SenderSessionID: sender,
		Payload:         []byte(`{"content":"` + id + `"}`),
		EnqueuedAt:      time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC),
	}
}

func newRedisT(t *testing.T) redis.UniversalClient {
	t.Helper()
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// spec: §7.2 line 281 — the in-memory inbox preserves FIFO order and
// reports its buffered count.
func TestMemoryInbox_FIFOAndLen_spec_7_2_281(t *testing.T) {
	in := NewMemoryInbox(0) // default cap
	ctx := context.Background()
	for _, id := range []string{"m1", "m2", "m3"} {
		dropped, err := in.Enqueue(ctx, "acme", "sess-1", msg(id, "snd"))
		if err != nil || dropped != nil {
			t.Fatalf("enqueue %s: dropped=%v err=%v", id, dropped, err)
		}
	}
	if n, _ := in.Len(ctx, "acme", "sess-1"); n != 3 {
		t.Fatalf("len = %d, want 3", n)
	}
	out, err := in.Drain(ctx, "acme", "sess-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 || out[0].MessageID != "m1" || out[2].MessageID != "m3" {
		t.Fatalf("drain order = %+v, want m1,m2,m3", out)
	}
	if n, _ := in.Len(ctx, "acme", "sess-1"); n != 0 {
		t.Fatalf("len after drain = %d, want 0", n)
	}
}

// spec: §7.2 line 282 — on overflow the oldest buffered message is
// dropped and returned for the message_dropped(inbox_overflow) receipt.
func TestMemoryInbox_OverflowDropsOldest_spec_7_2_282(t *testing.T) {
	in := NewMemoryInbox(2)
	ctx := context.Background()
	_, _ = in.Enqueue(ctx, "acme", "s", msg("m1", "snd"))
	_, _ = in.Enqueue(ctx, "acme", "s", msg("m2", "snd"))
	dropped, err := in.Enqueue(ctx, "acme", "s", msg("m3", "snd"))
	if err != nil {
		t.Fatal(err)
	}
	if dropped == nil || dropped.MessageID != "m1" {
		t.Fatalf("dropped = %+v, want m1 evicted", dropped)
	}
	out, _ := in.Drain(ctx, "acme", "s")
	if len(out) != 2 || out[0].MessageID != "m2" || out[1].MessageID != "m3" {
		t.Fatalf("remaining = %+v, want m2,m3", out)
	}
}

// spec: §7.2 line 280 — the in-memory inbox is keyed per (tenant,
// session); a foreign tenant's queue is isolated.
func TestMemoryInbox_TenantIsolation_spec_7_2_280(t *testing.T) {
	in := NewMemoryInbox(10)
	ctx := context.Background()
	_, _ = in.Enqueue(ctx, "acme", "s", msg("m1", "snd"))
	if n, _ := in.Len(ctx, "globex", "s"); n != 0 {
		t.Fatalf("globex len = %d, want 0 (isolated from acme)", n)
	}
}

// spec: §7.2 line 293 — the durable inbox enforces maxInboxSize
// atomically (LLEN-before-RPUSH) and evicts the oldest on overflow.
func TestRedisInbox_OverflowDropsOldest_spec_7_2_293(t *testing.T) {
	in := NewRedisInbox(newRedisT(t), 2)
	ctx := context.Background()
	if _, err := in.Enqueue(ctx, "acme", "s", msg("m1", "snd")); err != nil {
		t.Fatal(err)
	}
	if _, err := in.Enqueue(ctx, "acme", "s", msg("m2", "snd")); err != nil {
		t.Fatal(err)
	}
	dropped, err := in.Enqueue(ctx, "acme", "s", msg("m3", "snd"))
	if err != nil {
		t.Fatal(err)
	}
	if dropped == nil || dropped.MessageID != "m1" {
		t.Fatalf("dropped = %+v, want m1", dropped)
	}
	if n, _ := in.Len(ctx, "acme", "s"); n != 2 {
		t.Fatalf("len = %d, want 2 (capped)", n)
	}
}

// spec: §7.2 lines 297, 305 — recovery reads the durable inbox in FIFO
// order; Drain returns and clears the list.
func TestRedisInbox_DrainFIFO_spec_7_2_297(t *testing.T) {
	in := NewRedisInbox(newRedisT(t), 10)
	ctx := context.Background()
	for _, id := range []string{"m1", "m2", "m3"} {
		if _, err := in.Enqueue(ctx, "acme", "s", msg(id, "snd")); err != nil {
			t.Fatal(err)
		}
	}
	out, err := in.Drain(ctx, "acme", "s")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 || out[0].MessageID != "m1" || out[2].MessageID != "m3" {
		t.Fatalf("drain = %+v, want m1,m2,m3", out)
	}
	if n, _ := in.Len(ctx, "acme", "s"); n != 0 {
		t.Fatalf("len after drain = %d, want 0", n)
	}
}

// spec: §7.2 line 305 — durable-inbox resume_pending handling applies an
// EXPIRE to the inbox key rather than draining to the DLQ.
func TestRedisInbox_Expire_spec_7_2_305(t *testing.T) {
	c := newRedisT(t)
	in := NewRedisInbox(c, 10)
	ctx := context.Background()
	if _, err := in.Enqueue(ctx, "acme", "s", msg("m1", "snd")); err != nil {
		t.Fatal(err)
	}
	if err := in.Expire(ctx, "acme", "s", 30*time.Second); err != nil {
		t.Fatal(err)
	}
	ttl, err := c.TTL(ctx, inboxKey("acme", "s")).Result()
	if err != nil {
		t.Fatal(err)
	}
	if ttl <= 0 || ttl > 30*time.Second {
		t.Fatalf("ttl = %v, want (0, 30s]", ttl)
	}
}
