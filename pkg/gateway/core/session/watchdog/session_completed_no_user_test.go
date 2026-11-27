// SPDX-License-Identifier: MIT

package watchdog_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/billing/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/watchdog"
	"github.com/lennylabs/lenny/pkg/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/session/sessionstore/memstore"
)

// TestWatchdogFallbackSessionCompletedCarriesNoUserID pins the watchdog's
// fallback without a terminal hook: the session.completed billing event it
// writes for a session it forced terminal leaves user_id empty and keeps the
// tenant, the session id, the environment, and the labels, as the terminal
// pipeline's event does.
//
// spec: §11.2.1 (Billing Event Stream)
func TestWatchdogFallbackSessionCompletedCarriesNoUserID(t *testing.T) {
	store := memstore.New()
	billing := billingstore.NewMemory()
	stale := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_stuck", TenantID: "acme", UserID: "alice@acme.com", State: session.StateCreated,
		Environment: "env-prod", Labels: map[string]string{"team": "payments"},
		CreatedAt: stale, UpdatedAt: stale,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil).WithBilling(billing)

	if _, err := w.Tick(context.Background(), stale.Add(310*time.Second)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	w.WaitDispatched()

	events, err := billing.Since(context.Background(), "acme", 0, 0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if len(events) != 1 || events[0].EventType != billingstore.EventSessionCompleted {
		t.Fatalf("billing events = %+v, want one session.completed event", events)
	}
	e := events[0]
	if e.UserID != "" {
		t.Errorf("session.completed user_id = %q, want empty", e.UserID)
	}
	if e.TenantID != "acme" || e.SessionID != "sess_stuck" || e.EnvironmentID != "env-prod" || e.Labels["team"] != "payments" {
		t.Errorf("session.completed = %+v, want the tenant, session, environment, and labels kept", e)
	}
}
