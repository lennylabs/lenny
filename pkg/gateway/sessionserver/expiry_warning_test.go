// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
)

// spec: §11.3 line 240 — OnSessionExpiringSoon performs both halves: it emits
// the session_expiring_soon SSE event to the client AND dispatches the
// DEADLINE_APPROACHING signal to the running pod over the lifecycle channel.
// F-11.3.5.
func TestOnSessionExpiringSoonEmitsEventAndSignalsPod_spec_11_3_240(t *testing.T) {
	store := memstore.New()
	bus := sessionevents.NewBus(0)
	now := time.Now()
	sess := sessionstore.Session{
		ID: "sess_warn", TenantID: "acme", RuntimeRef: "echo",
		State:     session.StateRunning,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.Create(context.Background(), sess); err != nil {
		t.Fatalf("seed: %v", err)
	}
	stub := &stubAdapterServer{}
	adapter := dialStubAdapter(t, stub)
	reg := podsession.NewRegistry()
	reg.Put(&podsession.BindResult{SessionID: "sess_warn", TenantID: "acme", Adapter: adapter})

	srv := sessionserver.New(store, sessionserver.Options{Events: bus, PodRegistry: reg})

	sub, err := bus.SubscribeForTenant("acme", "sess_warn", 0, 8)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Close()

	srv.OnSessionExpiringSoon(context.Background(), sess, 7200, 299)

	// SSE half: a session_expiring_soon event with the documented payload.
	select {
	case ev := <-sub.Events():
		if ev.Type != "session_expiring_soon" {
			t.Fatalf("event type = %q, want session_expiring_soon", ev.Type)
		}
		var payload struct {
			MaxSessionAge    int `json:"maxSessionAge"`
			RemainingSeconds int `json:"remainingSeconds"`
		}
		if err := json.Unmarshal([]byte(ev.Data), &payload); err != nil {
			t.Fatalf("payload unmarshal: %v (data=%s)", err, ev.Data)
		}
		if payload.MaxSessionAge != 7200 || payload.RemainingSeconds != 299 {
			t.Errorf("payload = %+v, want {7200 299}", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no session_expiring_soon event published")
	}

	// Adapter half: the DEADLINE_APPROACHING signal reached the pod with the
	// remaining-ms and session_age trigger.
	waitFor(t, func() bool { return stub.deadlineCalled.Load() == 1 })
	req := stub.lastDeadlineReq.Load()
	if req == nil {
		t.Fatal("adapter received no SignalDeadline request")
	}
	if req.GetSessionId().GetValue() != "sess_warn" {
		t.Errorf("signal session id = %q, want sess_warn", req.GetSessionId().GetValue())
	}
	if req.GetRemainingMs() != 299000 {
		t.Errorf("signal remaining = %d ms, want 299000", req.GetRemainingMs())
	}
	if req.GetTrigger() != "session_age" {
		t.Errorf("signal trigger = %q, want session_age", req.GetTrigger())
	}
}

// The SSE half fires even when the session has no live pod binding (so a
// client watching a session whose pod has not re-bound still learns of the
// impending expiry).
func TestOnSessionExpiringSoonEmitsEventWithoutBinding_spec_11_3_240(t *testing.T) {
	store := memstore.New()
	bus := sessionevents.NewBus(0)
	now := time.Now()
	sess := sessionstore.Session{
		ID: "sess_nb", TenantID: "acme", State: session.StateRunning,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.Create(context.Background(), sess); err != nil {
		t.Fatalf("seed: %v", err)
	}
	srv := sessionserver.New(store, sessionserver.Options{Events: bus, PodRegistry: podsession.NewRegistry()})

	sub, err := bus.SubscribeForTenant("acme", "sess_nb", 0, 8)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Close()

	srv.OnSessionExpiringSoon(context.Background(), sess, 7200, 120)

	select {
	case ev := <-sub.Events():
		if ev.Type != "session_expiring_soon" {
			t.Errorf("event type = %q, want session_expiring_soon", ev.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no session_expiring_soon event published without a binding")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within deadline")
}
