// SPDX-License-Identifier: MIT

package sessioninbox

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/redisconn/slotguard"
)

// heldCoordinator builds a Coordinator over miniredis with the given inbox
// mode and returns it with the miniredis server.
func heldCoordinator(t *testing.T, durable bool) (*Coordinator, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	c := slotguard.Install(redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	t.Cleanup(func() { _ = c.Close() })
	var in Inbox = NewMemoryInbox(10)
	if durable {
		in = NewRedisInbox(c, 10)
	}
	return NewCoordinator(Config{Inbox: in, DLQ: NewDLQ(c, 10), Durable: durable}), mr
}

func acceptedAt(id string, at time.Time) Message {
	m := msg(id, "")
	m.EnqueuedAt = at
	return m
}

// spec: §7.2 (Interactive Session Model)
// HeldLen counts the DLQ alone, adds the inbox when asked, and counts only
// the DLQ entries accepted after a given time when one is passed.
func TestHeldLenCountsTheBacklog_spec_7_2(t *testing.T) {
	ctx := context.Background()
	co, _ := heldCoordinator(t, false)
	base := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	for i, id := range []string{"d1", "d2", "d3"} {
		if _, err := co.EnqueueDLQ(ctx, "acme", "s", acceptedAt(id, base.Add(time.Duration(i)*time.Minute)), 0); err != nil {
			t.Fatalf("EnqueueDLQ %s: %v", id, err)
		}
	}
	if _, err := co.EnqueueInbox(ctx, "acme", "s", acceptedAt("i1", base)); err != nil {
		t.Fatalf("EnqueueInbox: %v", err)
	}
	cases := []struct {
		name      string
		withInbox bool
		after     time.Time
		want      int
	}{
		{"DLQ alone", false, time.Time{}, 3},
		{"DLQ and inbox", true, time.Time{}, 4},
		{"accepted after the first", false, base.Add(time.Second), 2},
		{"accepted after the second, with the inbox", true, base.Add(time.Minute + time.Second), 2},
		{"accepted after the last", false, base.Add(2*time.Minute + time.Second), 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := co.HeldLen(ctx, "acme", "s", c.withInbox, c.after)
			if err != nil || got != c.want {
				t.Fatalf("HeldLen = %d, %v; want %d", got, err, c.want)
			}
		})
	}
	if got, err := (*Coordinator)(nil).HeldLen(ctx, "acme", "s", true, time.Time{}); got != 0 || err != nil {
		t.Fatalf("nil Coordinator HeldLen = %d, %v; want 0", got, err)
	}
}

// spec: §7.2 (Message delivery routing — seven paths, path 6)
// An interrupt suspension stamps SuspendedAt partway through a millisecond,
// and a message held later in that same millisecond was accepted after the
// session entered suspended. HeldLen must count it, or no backlog check ever
// resumes the podless session for it.
func TestHeldLenCountsAMessageHeldInTheSuspensionMillisecond_spec_7_2(t *testing.T) {
	ctx := context.Background()
	co, _ := heldCoordinator(t, false)
	ms := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	suspendedAt := ms.Add(400 * time.Microsecond)
	if _, err := co.EnqueueDLQ(ctx, "acme", "s", acceptedAt("before", ms.Add(-time.Millisecond)), 0); err != nil {
		t.Fatalf("EnqueueDLQ before: %v", err)
	}
	if _, err := co.EnqueueDLQ(ctx, "acme", "s", acceptedAt("held", ms.Add(900*time.Microsecond)), 0); err != nil {
		t.Fatalf("EnqueueDLQ held: %v", err)
	}
	got, err := co.HeldLen(ctx, "acme", "s", false, suspendedAt)
	if err != nil || got != 1 {
		t.Fatalf("HeldLen(acceptedAfter=SuspendedAt) = %d, %v; want 1, the message held in the suspension millisecond", got, err)
	}
}

// spec: §7.2 (Interactive Session Model)
// A DLQ read error reaches the caller wrapped, so a backlog check that
// cannot read the count skips its row rather than reading zero.
func TestHeldLenReturnsAReadError_spec_7_2(t *testing.T) {
	co, mr := heldCoordinator(t, false)
	mr.Close()
	_, err := co.HeldLen(context.Background(), "acme", "s", true, time.Time{})
	if err == nil || !strings.Contains(err.Error(), "count held DLQ entries") {
		t.Fatalf("HeldLen on a closed Redis = %v, want a wrapped error", err)
	}
}

// spec: §12.8 (Compliance Interfaces)
// Session erasure deletes the session's DLQ and inbox in durable mode and
// clears them in non-durable mode, leaves other sessions' and tenants' keys,
// returns the count and 0 on a repeat, and wraps a server error.
func TestEraseSessionDeletesOnlyThatSessionsQueues_spec_12_8(t *testing.T) {
	ctx := context.Background()
	for _, durable := range []bool{true, false} {
		name := "memory inbox"
		if durable {
			name = "durable inbox"
		}
		t.Run(name, func(t *testing.T) {
			co, mr := heldCoordinator(t, durable)
			at := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
			for _, key := range [][2]string{{"acme", "s"}, {"acme", "other"}, {"globex", "s"}} {
				if _, err := co.EnqueueDLQ(ctx, key[0], key[1], acceptedAt("d", at), 0); err != nil {
					t.Fatalf("EnqueueDLQ: %v", err)
				}
				if _, err := co.EnqueueInbox(ctx, key[0], key[1], acceptedAt("i", at)); err != nil {
					t.Fatalf("EnqueueInbox: %v", err)
				}
			}
			n, err := co.EraseSession(ctx, "acme", "s")
			if err != nil || n != 2 {
				t.Fatalf("EraseSession = %d, %v; want 2", n, err)
			}
			if n, err := co.EraseSession(ctx, "acme", "s"); err != nil || n != 0 {
				t.Fatalf("repeat EraseSession = %d, %v; want 0", n, err)
			}
			for _, key := range [][2]string{{"acme", "other"}, {"globex", "s"}} {
				if got, _ := co.HeldLen(ctx, key[0], key[1], true, time.Time{}); got != 2 {
					t.Fatalf("%s/%s held %d after another session's erasure, want 2", key[0], key[1], got)
				}
			}
			if durable && mr.Exists(InboxKey("acme", "s")) {
				t.Fatal("the durable inbox key survived the erasure")
			}
			if mr.Exists(DLQKey("acme", "s")) {
				t.Fatal("the DLQ key survived the erasure")
			}
			mr.SetError("boom")
			if _, err := co.EraseSession(ctx, "acme", "other"); err == nil || !strings.Contains(err.Error(), "erase DLQ") {
				t.Fatalf("EraseSession on a failing server = %v, want a wrapped error", err)
			}
			mr.SetError("")
		})
	}
	if n, err := (*Coordinator)(nil).EraseSession(ctx, "acme", "s"); n != 0 || err != nil {
		t.Fatalf("nil Coordinator EraseSession = %d, %v", n, err)
	}
}
