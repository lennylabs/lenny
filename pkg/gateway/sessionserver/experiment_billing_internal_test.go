// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// spec: §11.2 lines 87-88 — the session.created and session.completed
// billing events auto-populate experiment_id/variant_id from the
// session's experimentContext so per-experiment / per-variant cost
// attribution works without joining the session row. F-11.2.13.

func enrolledSession(state session.State) sessionstore.Session {
	return sessionstore.Session{
		ID: "sess_x", TenantID: "acme", UserID: "alice@acme.com",
		RuntimeRef: "claude-code", State: state,
		ExperimentContext: &sessionstore.ExperimentContext{
			ExperimentID: "exp-checkout", VariantID: "treatment",
		},
	}
}

func TestRecordSessionCreatedStampsExperimentVariant_spec_11_2_87(t *testing.T) {
	billing := billingstore.NewMemory()
	srv := New(memstore.New(), Options{Billing: billing})

	srv.recordSessionCreated(context.Background(), enrolledSession(session.StateCreated))

	events, err := billing.Since(context.Background(), "acme", 0, 0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("billing events: got %d, want 1", len(events))
	}
	if events[0].EventType != billingstore.EventSessionCreated {
		t.Fatalf("event type = %q, want %q", events[0].EventType, billingstore.EventSessionCreated)
	}
	if events[0].ExperimentID != "exp-checkout" || events[0].VariantID != "treatment" {
		t.Fatalf("created event must carry experiment_id/variant_id, got %+v", events[0])
	}
}

func TestRecordSessionCompletedStampsExperimentVariant_spec_11_2_87(t *testing.T) {
	billing := billingstore.NewMemory()
	srv := New(memstore.New(), Options{Billing: billing})

	srv.recordSessionCompleted(context.Background(), session.StateRunning, enrolledSession(session.StateCompleted))

	events, err := billing.Since(context.Background(), "acme", 0, 0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	var completed *billingstore.Event
	for i := range events {
		if events[i].EventType == billingstore.EventSessionCompleted {
			completed = &events[i]
		}
	}
	if completed == nil {
		t.Fatalf("no session.completed billing event emitted, got %+v", events)
	}
	if completed.ExperimentID != "exp-checkout" || completed.VariantID != "treatment" {
		t.Fatalf("completed event must carry experiment_id/variant_id, got %+v", *completed)
	}
}

// An unenrolled session must carry empty experiment_id/variant_id (no
// stamp), mirroring the nullable §11.2.1 contract. F-11.2.13.
func TestRecordSessionCreatedNoExperimentWhenUnenrolled_spec_11_2_87(t *testing.T) {
	billing := billingstore.NewMemory()
	srv := New(memstore.New(), Options{Billing: billing})

	srv.recordSessionCreated(context.Background(), sessionstore.Session{
		ID: "sess_y", TenantID: "acme", UserID: "alice@acme.com",
		RuntimeRef: "claude-code", State: session.StateCreated,
	})

	events, err := billing.Since(context.Background(), "acme", 0, 0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if len(events) != 1 || events[0].ExperimentID != "" || events[0].VariantID != "" {
		t.Fatalf("unenrolled session must carry empty experiment_id/variant_id, got %+v", events)
	}
}
