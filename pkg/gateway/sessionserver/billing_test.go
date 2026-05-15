// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// spec: §11.2.1 — a session.created billing event is emitted on every
// session create.

func TestCreateEmitsSessionCreatedBillingEvent(t *testing.T) {
	billing := billingstore.NewMemory()
	srv := sessionserver.New(memstore.New(), sessionserver.Options{Billing: billing})

	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
		UserID:     "alice@acme.com",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rr.Code, rr.Body.String())
	}

	events, err := billing.Since(context.Background(), "acme", 0, 0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("billing events after create: got %d, want 1", len(events))
	}
	e := events[0]
	if e.EventType != billingstore.EventSessionCreated {
		t.Errorf("event type: got %q, want session.created", e.EventType)
	}
	if e.UserID != "alice@acme.com" {
		t.Errorf("user id: got %q, want alice@acme.com", e.UserID)
	}
	if e.SessionID == "" {
		t.Error("the billing event must carry the session id")
	}
	if e.SequenceNumber != 1 {
		t.Errorf("sequence number: got %d, want 1", e.SequenceNumber)
	}
}

func TestCreateAndStartEmitsSessionCreatedBillingEvent(t *testing.T) {
	billing := billingstore.NewMemory()
	srv := sessionserver.New(memstore.New(), sessionserver.Options{Billing: billing})

	rr := postSession(t, srv.Handler(), "/v1/sessions/start", "bob@acme.com", "acme")
	if rr.Code != http.StatusCreated {
		t.Fatalf("create-and-start: status %d, body %s", rr.Code, rr.Body.String())
	}

	events, err := billing.Since(context.Background(), "acme", 0, 0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if len(events) != 1 || events[0].EventType != billingstore.EventSessionCreated {
		t.Fatalf("POST /v1/sessions/start must emit one session.created event, got %+v", events)
	}
}

func TestCreateWithoutBillingStoreDoesNotFail(t *testing.T) {
	// Billing is nil: the create path must still succeed.
	srv := sessionserver.New(memstore.New(), sessionserver.Options{})
	rr := createRequest(t, srv.Handler(), sessionserver.CreateSessionRequest{
		RuntimeRef: "claude-code",
	})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create with no billing store: status %d, body %s", rr.Code, rr.Body.String())
	}
}
