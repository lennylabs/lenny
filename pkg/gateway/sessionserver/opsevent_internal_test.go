// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/opsevents"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// eventTypeInBuffer reports whether the buffer holds an event of the
// given CloudEvents type.
func eventTypeInBuffer(buf *opsevents.EventBuffer, fullType string) bool {
	for _, e := range buf.Query(0, opsevents.EventFilter{}, 100).Events {
		if e.Event.Type == fullType {
			return true
		}
	}
	return false
}

// hasSessionFailed reports whether the buffer holds a session_failed
// operational event.
func hasSessionFailed(buf *opsevents.EventBuffer) bool {
	return eventTypeInBuffer(buf, "dev.lenny.session_failed")
}

func TestRecordSessionCompletedEmitsSessionFailed(t *testing.T) {
	// §16.6: a session reaching the failed state emits session_failed.
	emitter := opsevents.NewEmitter(opsevents.NewEventBuffer(0), "test")
	srv := New(memstore.New(), Options{OpsEmitter: emitter})

	srv.recordSessionCompleted(context.Background(), sessionstore.Session{
		ID: "s1", TenantID: "acme", RuntimeRef: "echo",
		State: session.StateFailed, FailureClass: "runtime_crash",
	})
	if !hasSessionFailed(emitter.Buffer()) {
		t.Error("a failed session must emit a session_failed operational event")
	}
	// The failed event carries the failure class.
	events := emitter.Buffer().Query(0, opsevents.EventFilter{}, 100).Events
	var data struct {
		FailureClass string `json:"failureClass"`
	}
	if err := json.Unmarshal(events[0].Event.Data, &data); err != nil {
		t.Fatalf("event data: %v", err)
	}
	if data.FailureClass != "runtime_crash" {
		t.Errorf("failureClass = %q, want runtime_crash", data.FailureClass)
	}
}

func TestRecordSessionCompletedEmitsTerminalLifecycleEvents(t *testing.T) {
	// §16.6: every terminal state emits its matching session lifecycle
	// operational event.
	cases := []struct {
		state     session.State
		eventType string
	}{
		{session.StateCompleted, "dev.lenny.session_completed"},
		{session.StateCancelled, "dev.lenny.session_cancelled"},
		{session.StateExpired, "dev.lenny.session_expired"},
	}
	for _, tc := range cases {
		emitter := opsevents.NewEmitter(opsevents.NewEventBuffer(0), "test")
		srv := New(memstore.New(), Options{OpsEmitter: emitter})
		srv.recordSessionCompleted(context.Background(), sessionstore.Session{
			ID: "s", TenantID: "acme", RuntimeRef: "echo", State: tc.state,
		})
		if !eventTypeInBuffer(emitter.Buffer(), tc.eventType) {
			t.Errorf("state %q must emit %q", tc.state, tc.eventType)
		}
		// A non-failed terminal event does not emit session_failed.
		if hasSessionFailed(emitter.Buffer()) {
			t.Errorf("state %q must not emit session_failed", tc.state)
		}
	}
}

func TestRecordSessionCompletedNoEmitForNonTerminal(t *testing.T) {
	// A non-terminal state reaching recordSessionCompleted emits no
	// lifecycle event.
	emitter := opsevents.NewEmitter(opsevents.NewEventBuffer(0), "test")
	srv := New(memstore.New(), Options{OpsEmitter: emitter})

	srv.recordSessionCompleted(context.Background(), sessionstore.Session{
		ID: "s2", TenantID: "acme", State: session.StateRunning,
	})
	if n := len(emitter.Buffer().Query(0, opsevents.EventFilter{}, 100).Events); n != 0 {
		t.Errorf("a non-terminal state emitted %d events, want 0", n)
	}
}

func TestRecordSessionCompletedNoEmitterIsSafe(t *testing.T) {
	// The emit is best-effort: a nil OpsEmitter must not panic.
	srv := New(memstore.New(), Options{})
	srv.recordSessionCompleted(context.Background(), sessionstore.Session{
		ID: "s3", TenantID: "acme", State: session.StateFailed,
	})
}

func TestTerminalSessionEventRejectsNonTerminal(t *testing.T) {
	for _, st := range []session.State{
		session.StateCreated, session.StateRunning, session.StateAwaitingClientAction,
	} {
		if _, _, ok := terminalSessionEvent(st); ok {
			t.Errorf("terminalSessionEvent(%q) reported terminal, want non-terminal", st)
		}
	}
}
