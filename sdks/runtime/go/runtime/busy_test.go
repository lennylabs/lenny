// SPDX-License-Identifier: MIT

package runtime

import (
	"context"
	"errors"
	"testing"
	"time"
)

// heartbeatAckFor writes a heartbeat naming sessionID (none when empty) and
// returns the heartbeat_ack that answers it, skipping any frame written
// before it.
func (l *liveSDK) heartbeatAckFor(sessionID string) map[string]any {
	l.t.Helper()
	if sessionID == "" {
		l.send(`{"type":"heartbeat","ts":1}`)
	} else {
		l.send(`{"type":"heartbeat","ts":1,"sessionId":"` + sessionID + `"}`)
	}
	for {
		f := l.next(3 * time.Second)
		if f["type"] == "heartbeat_ack" {
			return f
		}
	}
}

// assertAck checks an ack's sessionId and busy members; an empty wantID
// requires the bare ack.
func assertAck(t *testing.T, ack map[string]any, wantID string, wantBusy bool) {
	t.Helper()
	if wantID == "" {
		if len(ack) != 1 {
			t.Fatalf("ack = %v, want the bare {\"type\":\"heartbeat_ack\"}", ack)
		}
		return
	}
	if ack["sessionId"] != wantID {
		t.Fatalf("ack sessionId = %v, want %q", ack["sessionId"], wantID)
	}
	busy, present := ack["busy"]
	if wantBusy && busy != true {
		t.Fatalf("ack = %v, want busy: true", ack)
	}
	if !wantBusy && present {
		t.Fatalf("ack = %v, want busy omitted", ack)
	}
}

// busyHandler is a Handler whose OnMessage behavior each case sets.
type busyHandler struct {
	recorder
	onMessage func(ctx context.Context, m Message) (Reply, error)
	onCreate  func(ctx context.Context, req CreateRequest) error
}

func (h *busyHandler) OnCreate(ctx context.Context, req CreateRequest) error {
	if h.onCreate != nil {
		return h.onCreate(ctx, req)
	}
	return nil
}

func (h *busyHandler) OnMessage(ctx context.Context, m Message) (Reply, error) {
	if h.onMessage != nil {
		return h.onMessage(ctx, m)
	}
	return Reply{Parts: m.Envelope.Input, Final: true}, nil
}

// spec: 28.5.3 (CH-MSGSOCK), 15.7 (Runtime Author SDKs)
//
// The SDK answers a heartbeat naming a held session with that sessionId and
// busy: true while the session has a message routed and not finished, or an
// unreleased busy hold, and omits busy otherwise; it answers an unaddressed
// heartbeat and one naming a session it does not hold with the bare ack.
func TestHeartbeatAckReportsBusy_spec_28_5_3(t *testing.T) {
	t.Run("unaddressed and unknown sessions get the bare ack", func(t *testing.T) {
		l := startLiveSDK(t, &busyHandler{})
		l.send(startFrame("sess_a", "st_a"))
		_ = l.next(3 * time.Second)
		assertAck(t, l.heartbeatAckFor(""), "", false)
		assertAck(t, l.heartbeatAckFor("sess_x"), "", false)
	})
	t.Run("a running handler and a queued message are busy; the response ends it", func(t *testing.T) {
		g := newGate()
		h := &busyHandler{onMessage: func(_ context.Context, m Message) (Reply, error) {
			g.wait(m.Envelope.ID)
			return TextReply("ok"), nil
		}}
		l := startLiveSDK(t, h)
		l.send(startFrame("sess_a", "st_a"))
		_ = l.next(3 * time.Second)
		assertAck(t, l.heartbeatAckFor("sess_a"), "sess_a", false)
		l.send(msgFrame("sess_a", "m1", "x"))
		l.send(msgFrame("sess_a", "m2", "y"))
		g.awaitEntry(t)
		assertAck(t, l.heartbeatAckFor("sess_a"), "sess_a", true)
		g.release <- struct{}{}
		g.awaitEntry(t)
		assertAck(t, l.heartbeatAckFor("sess_a"), "sess_a", true)
		g.release <- struct{}{}
		// The ack answers after the handler returned only once the second
		// response is written; poll until it reports idle.
		deadline := time.Now().Add(3 * time.Second)
		for {
			ack := l.heartbeatAckFor("sess_a")
			if _, busy := ack["busy"]; !busy {
				assertAck(t, ack, "sess_a", false)
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("the session stayed busy after both responses")
			}
		}
	})
	t.Run("an error response and a streaming non-final return end it", func(t *testing.T) {
		h := &busyHandler{onMessage: func(_ context.Context, m Message) (Reply, error) {
			if m.Envelope.ID == "err" {
				return Reply{}, errors.New("boom")
			}
			return Reply{Streaming: true}, nil
		}}
		l := startLiveSDK(t, h)
		l.send(startFrame("sess_a", "st_a"))
		_ = l.next(3 * time.Second)
		l.send(msgFrame("sess_a", "err", "x"))
		if f := l.next(3 * time.Second); errorCode(f) != "RUNTIME_ERROR" {
			t.Fatalf("frame = %v, want the error response", f)
		}
		// The message stops counting once its handling returned, which is
		// just after the error response is written; poll until idle.
		deadline := time.Now().Add(3 * time.Second)
		for {
			ack := l.heartbeatAckFor("sess_a")
			if _, busy := ack["busy"]; !busy {
				assertAck(t, ack, "sess_a", false)
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("an error response left the session busy")
			}
		}
		l.send(msgFrame("sess_a", "stream", "x"))
		deadline = time.Now().Add(3 * time.Second)
		for {
			ack := l.heartbeatAckFor("sess_a")
			if _, busy := ack["busy"]; !busy {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("a streaming non-final return left the session busy")
			}
		}
	})
	t.Run("a hold outlives the handler, and a second release does nothing", func(t *testing.T) {
		releases := make(chan func(), 1)
		h := &busyHandler{onMessage: func(ctx context.Context, m Message) (Reply, error) {
			releases <- HoldBusy(ctx)
			return TextReply("ok"), nil
		}}
		l := startLiveSDK(t, h)
		l.send(startFrame("sess_a", "st_a"))
		_ = l.next(3 * time.Second)
		l.send(msgFrame("sess_a", "m1", "x"))
		release := <-releases
		_ = l.next(3 * time.Second)
		assertAck(t, l.heartbeatAckFor("sess_a"), "sess_a", true)
		release()
		release()
		assertAck(t, l.heartbeatAckFor("sess_a"), "sess_a", false)
	})
	t.Run("a hold taken in OnCreate is busy before any message", func(t *testing.T) {
		releases := make(chan func(), 1)
		h := &busyHandler{onCreate: func(ctx context.Context, _ CreateRequest) error {
			releases <- HoldBusy(ctx)
			return nil
		}}
		l := startLiveSDK(t, h)
		l.send(startFrame("sess_a", "st_a"))
		_ = l.next(3 * time.Second)
		release := <-releases
		assertAck(t, l.heartbeatAckFor("sess_a"), "sess_a", true)
		release()
		assertAck(t, l.heartbeatAckFor("sess_a"), "sess_a", false)
	})
	t.Run("session_end zeroes the counts", func(t *testing.T) {
		releases := make(chan func(), 1)
		h := &busyHandler{onCreate: func(ctx context.Context, _ CreateRequest) error {
			releases <- HoldBusy(ctx)
			return nil
		}}
		l := startLiveSDK(t, h)
		l.send(startFrame("sess_a", "st_a"))
		_ = l.next(3 * time.Second)
		release := <-releases
		l.send(endFrame("sess_a"))
		l.send(startFrame("sess_a", "st_b"))
		_ = l.next(3 * time.Second)
		release()
		ack := l.heartbeatAckFor("sess_a")
		if ack["sessionId"] != "sess_a" {
			t.Fatalf("ack = %v, want the reopened session", ack)
		}
	})
	t.Run("a context naming no session returns a no-op release", func(t *testing.T) {
		HoldBusy(context.Background())()
	})
}
