// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sessionrecord"
)

// heldExecutor records each delivered message and can fail or block a Send.
type heldExecutor struct {
	mu       sync.Mutex
	got      []executor.Message
	failNext int
	onSend   func(ctx context.Context, msg executor.Message)
}

func (e *heldExecutor) Send(ctx context.Context, _ string, msgs []executor.Message) (executor.Response, error) {
	e.mu.Lock()
	if e.failNext > 0 {
		e.failNext--
		e.mu.Unlock()
		return executor.Response{}, errors.New("runtime unreachable")
	}
	e.got = append(e.got, msgs...)
	hook := e.onSend
	e.mu.Unlock()
	if hook != nil {
		hook(ctx, msgs[0])
	}
	return executor.Response{Parts: []executor.MessagePart{{Type: "text", Text: "ok"}}}, nil
}

func (e *heldExecutor) Close(context.Context, string) error { return nil }

func (e *heldExecutor) ids() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, 0, len(e.got))
	for _, m := range e.got {
		out = append(out, m.ID)
	}
	return out
}

// heldFixture is a server with a messaging coordinator over a memory or
// Redis inbox and a Redis DLQ, a recording executor, and one running session.
type heldFixture struct {
	srv   *Server
	store *memstore.Store
	exec  *heldExecutor
	coord *sessioninbox.Coordinator
	inbox sessioninbox.Inbox
	dlq   *sessioninbox.DLQ
	rc    *redis.Client
}

func newHeldFixture(t *testing.T, durable bool) *heldFixture {
	t.Helper()
	mr := miniredis.RunT(t)
	rc := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rc.Close() })
	var inbox sessioninbox.Inbox = sessioninbox.NewMemoryInbox(10)
	if durable {
		inbox = sessioninbox.NewRedisInbox(rc, 10)
	}
	dlq := sessioninbox.NewDLQ(rc, 10)
	coord := sessioninbox.NewCoordinator(sessioninbox.Config{Inbox: inbox, DLQ: dlq})
	store := memstore.New()
	if err := store.Create(context.Background(), sessionstore.Session{
		UserID: testOwner,
		ID:     "s", TenantID: "acme", RuntimeRef: "echo", State: session.StateRunning,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	exec := &heldExecutor{}
	srv := New(store, Options{DevTenantHeader: true, Executor: exec, Messaging: coord})
	t.Cleanup(srv.Close)
	return &heldFixture{srv: srv, store: store, exec: exec, coord: coord, inbox: inbox, dlq: dlq, rc: rc}
}

func heldPayload(t *testing.T, id, role, text string) []byte {
	t.Helper()
	b, err := json.Marshal(MessagePayload{ID: id, Role: role, Content: sessionrecord.MessageContentFromText(text)})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func (f *heldFixture) holdInbox(t *testing.T, id string, at time.Time, payload []byte) {
	t.Helper()
	if _, err := f.coord.EnqueueInbox(context.Background(), "acme", "s", sessioninbox.Message{
		MessageID: id, Payload: payload, EnqueuedAt: at,
	}); err != nil {
		t.Fatalf("hold inbox %s: %v", id, err)
	}
}

func (f *heldFixture) holdDLQ(t *testing.T, id, sender string, at time.Time, payload []byte, ttl time.Duration) {
	t.Helper()
	if _, err := f.coord.EnqueueDLQ(context.Background(), "acme", "s", sessioninbox.Message{
		MessageID: id, SenderSessionID: sender, Payload: payload, EnqueuedAt: at,
	}, ttl); err != nil {
		t.Fatalf("hold DLQ %s: %v", id, err)
	}
}

func (f *heldFixture) row(t *testing.T) sessionstore.Session {
	t.Helper()
	r, err := f.store.Get(context.Background(), "acme", "s")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	return r
}

func (f *heldFixture) remaining(t *testing.T) int {
	t.Helper()
	in, err := f.inbox.Len(context.Background(), "acme", "s")
	if err != nil {
		t.Fatalf("inbox len: %v", err)
	}
	dl, err := f.dlq.Len(context.Background(), "acme", "s")
	if err != nil {
		t.Fatalf("dlq len: %v", err)
	}
	return in + dl
}

// TestDeliverBufferedDeliversHeldEntriesInOrder_spec_7_2 asserts that
// deliverBuffered delivers held inbox and DLQ entries in EnqueuedAt order in
// both inbox modes, delivers a DLQ entry whose expiry score has passed,
// leaves both stores empty, decodes a payload without a role as a user
// message with its ID and text, and stamps an entry's from as the sending
// agent when the entry names a sender.
//
// spec: §7.2 (Message delivery routing — seven paths, path 6)
func TestDeliverBufferedDeliversHeldEntriesInOrder_spec_7_2(t *testing.T) {
	for _, durable := range []bool{false, true} {
		t.Run(map[bool]string{false: "memory inbox", true: "durable inbox"}[durable], func(t *testing.T) {
			f := newHeldFixture(t, durable)
			// Every DLQ enqueue scores EnqueuedAt plus the default TTL. m1
			// was enqueued more than one TTL ago, so its expiry score has
			// passed and the delivery still takes it.
			base := time.Now().Add(-2 * sessioninbox.DefaultDLQTTL)
			f.holdDLQ(t, "m1", "", base, heldPayload(t, "m1", "", "first"), 0)
			f.holdInbox(t, "m2", base.Add(time.Second), heldPayload(t, "m2", "user", "second"))
			f.holdDLQ(t, "m3", "sess-sender", base.Add(2*time.Second), heldPayload(t, "m3", "", "third"), 0)
			f.holdInbox(t, "m4", base.Add(3*time.Second), heldPayload(t, "m4", "", "fourth"))

			if err := f.srv.deliverBuffered(context.Background(), f.row(t)); err != nil {
				t.Fatalf("deliverBuffered: %v", err)
			}
			if got := f.exec.ids(); len(got) != 4 || got[0] != "m1" || got[1] != "m2" || got[2] != "m3" || got[3] != "m4" {
				t.Errorf("delivered %v, want [m1 m2 m3 m4]", got)
			}
			if n := f.remaining(t); n != 0 {
				t.Errorf("held entries left = %d, want 0", n)
			}
			f.exec.mu.Lock()
			first, third := f.exec.got[0], f.exec.got[2]
			f.exec.mu.Unlock()
			if first.Role != "user" || first.Content != "first" || first.From != (executor.MessageFrom{}) {
				t.Errorf("first = %+v, want a user message with text and no sender", first)
			}
			if third.From != (executor.MessageFrom{Kind: "agent", ID: "sess-sender"}) {
				t.Errorf("third.From = %+v, want the sending agent", third.From)
			}
		})
	}
}

// TestDeliverBufferedKeepsDeliveryExactlyOnce_spec_7_2 asserts the failure
// behavior of deliverBuffered: a failed Send leaves that entry and the later
// ones in place for a later call; a head whose payload does not decode is
// removed; a DLQ head whose envelope does not decode stops the loop and stays
// in place; a missing messaging coordinator is a no-op; a caller cancelled mid-turn lets the turn
// complete and removes only that entry; and a row that reads terminal before
// an entry stops the loop with that entry in place.
//
// spec: §7.2 (Message delivery routing — seven paths, path 6)
func TestDeliverBufferedKeepsDeliveryExactlyOnce_spec_7_2(t *testing.T) {
	base := time.Now().Add(-time.Minute)
	t.Run("failed send keeps the entry at the head", func(t *testing.T) {
		f := newHeldFixture(t, false)
		f.holdInbox(t, "m1", base, heldPayload(t, "m1", "", "a"))
		f.holdInbox(t, "m2", base.Add(time.Second), heldPayload(t, "m2", "", "b"))
		f.exec.failNext = 1
		if err := f.srv.deliverBuffered(context.Background(), f.row(t)); err == nil {
			t.Fatal("deliverBuffered = nil, want the Send error")
		}
		if n := f.remaining(t); n != 2 {
			t.Fatalf("held entries = %d, want both left", n)
		}
		if err := f.srv.deliverBuffered(context.Background(), f.row(t)); err != nil {
			t.Fatalf("second deliverBuffered: %v", err)
		}
		if got := f.exec.ids(); len(got) != 2 || got[0] != "m1" || got[1] != "m2" {
			t.Errorf("delivered %v, want [m1 m2] once each", got)
		}
	})
	t.Run("undecodable head is removed", func(t *testing.T) {
		f := newHeldFixture(t, false)
		f.holdInbox(t, "bad", base, []byte("{not json"))
		f.holdInbox(t, "m2", base.Add(time.Second), heldPayload(t, "m2", "", "b"))
		if err := f.srv.deliverBuffered(context.Background(), f.row(t)); err != nil {
			t.Fatalf("deliverBuffered: %v", err)
		}
		if got := f.exec.ids(); len(got) != 1 || got[0] != "m2" {
			t.Errorf("delivered %v, want only m2", got)
		}
		if n := f.remaining(t); n != 0 {
			t.Errorf("held entries = %d, want 0", n)
		}
	})
	t.Run("undecodable DLQ head stops the loop and stays", func(t *testing.T) {
		// A DLQ member whose envelope does not decode is a failed peek: the
		// loop stops with nothing removed, as for an inbox Peek error.
		f := newHeldFixture(t, false)
		if err := f.rc.ZAdd(context.Background(), sessioninbox.DLQKey("acme", "s"),
			redis.Z{Score: 1, Member: "{not an envelope"}).Err(); err != nil {
			t.Fatalf("seed undecodable DLQ member: %v", err)
		}
		f.holdInbox(t, "m2", base, heldPayload(t, "m2", "", "b"))
		if err := f.srv.deliverBuffered(context.Background(), f.row(t)); err != nil {
			t.Fatalf("deliverBuffered = %v, want nil after a failed peek", err)
		}
		if got := f.exec.ids(); len(got) != 0 {
			t.Errorf("delivered %v, want nothing after a failed DLQ peek", got)
		}
		if n, err := f.dlq.Len(context.Background(), "acme", "s"); err != nil || n != 1 {
			t.Errorf("DLQ len = %d %v, want the undecodable member left in place", n, err)
		}
		if n := f.remaining(t); n != 2 {
			t.Errorf("held entries = %d, want both left", n)
		}
	})
	t.Run("no messaging coordinator", func(t *testing.T) {
		srv := New(memstore.New(), Options{DevTenantHeader: true, Executor: &heldExecutor{}})
		defer srv.Close()
		if err := srv.deliverBuffered(context.Background(), sessionstore.Session{UserID: testOwner, TenantID: "acme", ID: "s"}); err != nil {
			t.Errorf("deliverBuffered = %v, want nil", err)
		}
	})
	t.Run("caller cancelled mid-turn", func(t *testing.T) {
		f := newHeldFixture(t, false)
		f.holdInbox(t, "m1", base, heldPayload(t, "m1", "", "a"))
		f.holdInbox(t, "m2", base.Add(time.Second), heldPayload(t, "m2", "", "b"))
		ctx, cancel := context.WithCancel(context.Background())
		var turnCtxErr error
		f.exec.onSend = func(sendCtx context.Context, _ executor.Message) {
			cancel()
			turnCtxErr = sendCtx.Err()
		}
		err := f.srv.deliverBuffered(ctx, f.row(t))
		if !errors.Is(err, context.Canceled) {
			t.Errorf("deliverBuffered = %v, want the caller's cancellation", err)
		}
		if turnCtxErr != nil {
			t.Errorf("the turn ran on a context that ended with the caller: %v", turnCtxErr)
		}
		if got := f.exec.ids(); len(got) != 1 || got[0] != "m1" {
			t.Errorf("delivered %v, want only m1", got)
		}
		if n := f.remaining(t); n != 1 {
			t.Errorf("held entries = %d, want m2 left", n)
		}
	})
	t.Run("terminal row stops the loop", func(t *testing.T) {
		f := newHeldFixture(t, false)
		f.holdInbox(t, "m1", base, heldPayload(t, "m1", "", "a"))
		f.holdInbox(t, "m2", base.Add(time.Second), heldPayload(t, "m2", "", "b"))
		f.exec.onSend = func(context.Context, executor.Message) {
			_, _ = f.store.Update(context.Background(), "acme", "s", func(r *sessionstore.Session) error {
				r.State = session.StateCancelled
				return nil
			})
		}
		if err := f.srv.deliverBuffered(context.Background(), f.row(t)); err != nil {
			t.Fatalf("deliverBuffered: %v", err)
		}
		if got := f.exec.ids(); len(got) != 1 {
			t.Errorf("delivered %v, want only the entry before the terminal write", got)
		}
		if n := f.remaining(t); n != 1 {
			t.Errorf("held entries = %d, want m2 left in place", n)
		}
	})
}

// TestHoldMessageReReadsTheRowFromThePrimary_spec_7_2 asserts that
// holdMessage removes its own entries and answers HoldTerminal when its
// primary re-read finds the session terminal, removes them and returns the
// error when the re-read fails, and leaves them when the re-read finds the
// session running.
//
// spec: §7.2 (Message delivery routing — seven paths, path 6)
func TestHoldMessageReReadsTheRowFromThePrimary_spec_7_2(t *testing.T) {
	msg := func(id string) []sessioninbox.Message {
		return []sessioninbox.Message{{MessageID: id, Payload: []byte(`{}`), EnqueuedAt: time.Now()}}
	}
	t.Run("running keeps the entry", func(t *testing.T) {
		f := newHeldFixture(t, false)
		out, st, err := f.srv.holdMessage(context.Background(), "acme", "s", msg("m1"), time.Hour)
		if err != nil || out != HoldQueued || st != session.StateRunning {
			t.Fatalf("holdMessage = %v %q %v, want queued running", out, st, err)
		}
		if n := f.remaining(t); n != 1 {
			t.Errorf("held = %d, want 1", n)
		}
	})
	t.Run("terminal removes the entry", func(t *testing.T) {
		f := newHeldFixture(t, false)
		_, _ = f.store.Update(context.Background(), "acme", "s", func(r *sessionstore.Session) error {
			r.State = session.StateCancelled
			return nil
		})
		out, st, err := f.srv.holdMessage(context.Background(), "acme", "s", msg("m1"), time.Hour)
		if err != nil || out != HoldTerminal || st != session.StateCancelled {
			t.Fatalf("holdMessage = %v %q %v, want HoldTerminal cancelled", out, st, err)
		}
		if n := f.remaining(t); n != 0 {
			t.Errorf("held = %d, want 0", n)
		}
	})
	t.Run("re-read error removes the entry", func(t *testing.T) {
		f := newHeldFixture(t, false)
		f.srv.store = &flakyPrimary{Store: f.store}
		f.srv.store.(*flakyPrimary).fails.Store(1)
		if _, _, err := f.srv.holdMessage(context.Background(), "acme", "s", msg("m1"), time.Hour); err == nil {
			t.Fatal("holdMessage = nil error, want the re-read error")
		}
		if n := f.remaining(t); n != 0 {
			t.Errorf("held = %d, want 0", n)
		}
	})
}

// TestDeliverHeldAfterTakeoverDeliversOnlyAtTheTakeoverGeneration_spec_7_2
// asserts that DeliverHeldAfterTakeover delivers the held entries once for a
// running row at the passed generation, nothing for another generation or a
// row that is not running, and returns at once while the delivery lock is
// held, delivering after its release.
//
// spec: §7.2 (Message delivery routing — seven paths, path 6), §10.1.2
// (Coordinator Handoff Protocol)
func TestDeliverHeldAfterTakeoverDeliversOnlyAtTheTakeoverGeneration_spec_7_2(t *testing.T) {
	t.Parallel()
	wait := func(f *heldFixture, n int) bool {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if len(f.exec.ids()) >= n {
				return true
			}
			time.Sleep(2 * time.Millisecond)
		}
		return false
	}
	for _, tc := range []struct {
		name    string
		state   session.State
		genDiff int64
		want    int
	}{
		{"running at the generation", session.StateRunning, 0, 1},
		{"another generation", session.StateRunning, 1, 0},
		{"input_required", session.StateInputRequired, 0, 0},
		{"suspended", session.StateSuspended, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newHeldFixture(t, false)
			row, _ := f.store.Update(context.Background(), "acme", "s", func(r *sessionstore.Session) error {
				r.State = tc.state
				return nil
			})
			f.holdInbox(t, "m1", time.Now(), heldPayload(t, "m1", "", "a"))
			f.srv.DeliverHeldAfterTakeover("acme", "s", row.CoordinationGeneration+tc.genDiff)
			got := wait(f, 1)
			time.Sleep(20 * time.Millisecond)
			if n := len(f.exec.ids()); n != tc.want || got != (tc.want == 1) {
				t.Errorf("delivered %d, want %d", n, tc.want)
			}
		})
	}
	t.Run("held delivery lock", func(t *testing.T) {
		t.Parallel()
		f := newHeldFixture(t, false)
		row := f.row(t)
		f.holdInbox(t, "m1", time.Now(), heldPayload(t, "m1", "", "a"))
		unlock, err := f.srv.deliveryLocks.Lock(context.Background(), "s")
		if err != nil {
			t.Fatalf("lock: %v", err)
		}
		done := make(chan struct{})
		go func() {
			f.srv.DeliverHeldAfterTakeover("acme", "s", row.CoordinationGeneration)
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("DeliverHeldAfterTakeover blocked on the held delivery lock")
		}
		time.Sleep(20 * time.Millisecond)
		if n := len(f.exec.ids()); n != 0 {
			t.Fatalf("delivered %d while the lock was held, want 0", n)
		}
		unlock()
		if !wait(f, 1) {
			t.Error("nothing delivered after the lock's release")
		}
	})
}
