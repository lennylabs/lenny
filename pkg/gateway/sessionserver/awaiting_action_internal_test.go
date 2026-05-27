// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/events"
	"github.com/lennylabs/lenny/pkg/gateway/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// spec: §7.3 line 427 — when a session transitions into
// awaiting_client_action the gateway fires session.awaiting_action via the
// §14 callbackUrl webhook stream so CI systems react without polling. The
// emitter helper publishes the §16.6 EventSessionAwaitingAction
// operational event, the §11.7 / §16.7 session.awaiting_action_entered
// audit row, and the §7.2 line 137 status_change(awaiting_client_action)
// SSE frame. F-7.3.13 / F-7.3.25.
func TestEmitAwaitingClientActionEnteredEmitsAllSurfaces_spec_7_3_13(t *testing.T) {
	bus := sessionevents.NewBus(64)
	sink := &captureLifecycleAudit{}
	emitter := events.NewEmitter(events.NewEventBuffer(0), "test")
	at := time.Date(2026, 5, 26, 10, 0, 0, 0, time.UTC)
	srv := New(memstore.New(), Options{
		Events:             bus,
		OpsEmitter:         emitter,
		LifecycleAuditSink: sink,
		Clock:              func() time.Time { return at },
	})

	srv.emitAwaitingClientActionEntered(context.Background(), sessionstore.Session{
		ID: "s_await", TenantID: "acme", UserID: "alice@acme.com",
		RuntimeRef: "claude-code", State: session.StateAwaitingClientAction,
	})

	// §16.6 operational event.
	if !eventTypeInBuffer(emitter.Buffer(), "dev.lenny.session_awaiting_action") {
		t.Errorf("F-7.3.13: expected dev.lenny.session_awaiting_action operational event")
	}

	// §11.7 audit row.
	if len(sink.events) != 1 || sink.events[0].EventType != auditSessionAwaitingActionEntered {
		t.Fatalf("F-7.3.25: audit events = %+v, want one session.awaiting_action_entered", sink.events)
	}
	ev := sink.events[0]
	if ev.TenantID != "acme" || ev.SessionID != "s_await" || ev.RuntimeRef != "claude-code" {
		t.Errorf("F-7.3.25: audit identity fields wrong: %+v", ev)
	}
	if ev.State != string(session.StateAwaitingClientAction) {
		t.Errorf("F-7.3.25: audit state = %q, want awaiting_client_action", ev.State)
	}
	if !ev.At.Equal(at) {
		t.Errorf("F-7.3.25: audit at = %v, want %v", ev.At, at)
	}

	// §7.2 line 137 status_change SSE frame.
	sc := sseEventsOfType(bus, "s_await", "status_change")
	if len(sc) != 1 {
		t.Fatalf("F-7.3.13: status_change count = %d, want 1", len(sc))
	}
	var payload struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal([]byte(sc[0].Data), &payload); err != nil {
		t.Fatalf("status_change data: %v", err)
	}
	if payload.State != string(session.StateAwaitingClientAction) {
		t.Errorf("F-7.3.13: status_change state = %q, want awaiting_client_action", payload.State)
	}
}

// spec: defensive — the helper no-ops when invoked with a state that
// disagrees with the awaiting-action contract so a concurrent state
// transition cannot fire the wrong audit row.
func TestEmitAwaitingClientActionEnteredIgnoresWrongState_spec_7_3_13(t *testing.T) {
	bus := sessionevents.NewBus(64)
	sink := &captureLifecycleAudit{}
	emitter := events.NewEmitter(events.NewEventBuffer(0), "test")
	srv := New(memstore.New(), Options{Events: bus, OpsEmitter: emitter, LifecycleAuditSink: sink})

	srv.emitAwaitingClientActionEntered(context.Background(), sessionstore.Session{
		ID: "s", TenantID: "acme", State: session.StateRunning,
	})
	if n := len(emitter.Buffer().Query(0, events.EventFilter{}, 100).Events); n != 0 {
		t.Errorf("F-7.3.13: wrong-state emit produced %d ops events, want 0", n)
	}
	if len(sink.events) != 0 {
		t.Errorf("F-7.3.13: wrong-state emit produced %d audit rows, want 0", len(sink.events))
	}
}

// spec: §7.3 line 423 — the awaiting_client_action → expired entry
// path writes the distinct §11.7 session.expired_in_awaiting_action audit
// row so SIEM/SOC dashboards can filter on the cause of expiry. F-7.3.25.
func TestEmitAwaitingClientActionExpiredEmitsAuditRow_spec_7_3_25(t *testing.T) {
	sink := &captureLifecycleAudit{}
	at := time.Date(2026, 5, 26, 11, 0, 0, 0, time.UTC)
	srv := New(memstore.New(), Options{
		LifecycleAuditSink: sink,
		Clock:              func() time.Time { return at },
	})

	srv.emitAwaitingClientActionExpired(context.Background(), sessionstore.Session{
		ID: "s_exp", TenantID: "acme", UserID: "alice@acme.com",
		RuntimeRef: "claude-code", State: session.StateExpired,
	})

	if len(sink.events) != 1 || sink.events[0].EventType != auditSessionExpiredInAwaitingAction {
		t.Fatalf("F-7.3.25: audit events = %+v, want one session.expired_in_awaiting_action", sink.events)
	}
	if !sink.events[0].At.Equal(at) {
		t.Errorf("F-7.3.25: audit at = %v, want %v", sink.events[0].At, at)
	}
}

// spec: nil sinks must never panic — every collaborator is optional.
func TestAwaitingActionEmittersNoSinkSafe_spec_7_3_13(t *testing.T) {
	srv := New(memstore.New(), Options{})
	srv.emitAwaitingClientActionEntered(context.Background(), sessionstore.Session{
		ID: "s", TenantID: "acme", State: session.StateAwaitingClientAction,
	})
	srv.emitAwaitingClientActionExpired(context.Background(), sessionstore.Session{
		ID: "s", TenantID: "acme", State: session.StateExpired,
	})
}

// spec: the §7.3 audit hook wires through to the watchdog terminal hook
// via the AwaitingClientActionExpiryNotifier optional interface, so
// Server satisfies it.
func TestServerImplementsAwaitingExpiryNotifier_spec_7_3_25(t *testing.T) {
	srv := New(memstore.New(), Options{})
	var _ interface {
		OnSessionExpiredFromAwaitingClientAction(ctx context.Context, sess sessionstore.Session)
	} = srv
	// Bonus: a method call with a nil sink is safe.
	srv.OnSessionExpiredFromAwaitingClientAction(context.Background(), sessionstore.Session{
		ID: "s", TenantID: "acme",
	})
}
