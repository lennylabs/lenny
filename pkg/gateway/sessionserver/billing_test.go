// SPDX-License-Identifier: MIT

package sessionserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionserver"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// createSessionID creates a session and returns its id.
func createSessionID(t *testing.T, h http.Handler) string {
	t.Helper()
	rr := createRequest(t, h, sessionserver.CreateSessionRequest{RuntimeRef: "claude-code"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rr.Code, rr.Body.String())
	}
	var created sessionserver.CreateSessionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	return created.ID
}

// sessionRequest drives a bodyless request against a session endpoint.
func sessionRequest(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("X-Lenny-Tenant-ID", "acme")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// billingEventTypes returns the event types in tenant acme's ledger.
func billingEventTypes(t *testing.T, billing *billingstore.Memory) []billingstore.EventType {
	t.Helper()
	events, err := billing.Since(context.Background(), "acme", 0, 0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	out := make([]billingstore.EventType, 0, len(events))
	for _, e := range events {
		out = append(out, e.EventType)
	}
	return out
}

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

func TestTerminateEmitsSessionCompletedBillingEvent(t *testing.T) {
	billing := billingstore.NewMemory()
	srv := sessionserver.New(memstore.New(), sessionserver.Options{Billing: billing})
	h := srv.Handler()

	id := createSessionID(t, h)
	rr := sessionRequest(t, h, http.MethodPost, "/v1/sessions/"+id+"/terminate")
	if rr.Code != http.StatusOK {
		t.Fatalf("terminate: status %d, body %s", rr.Code, rr.Body.String())
	}

	got := billingEventTypes(t, billing)
	want := []billingstore.EventType{
		billingstore.EventSessionCreated, billingstore.EventSessionCompleted,
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("billing events after terminate: got %v, want %v", got, want)
	}
}

func TestDeleteEmitsSessionCompletedBillingEvent(t *testing.T) {
	billing := billingstore.NewMemory()
	srv := sessionserver.New(memstore.New(), sessionserver.Options{Billing: billing})
	h := srv.Handler()

	id := createSessionID(t, h)
	rr := sessionRequest(t, h, http.MethodDelete, "/v1/sessions/"+id)
	if rr.Code != http.StatusOK {
		t.Fatalf("delete: status %d, body %s", rr.Code, rr.Body.String())
	}

	got := billingEventTypes(t, billing)
	if len(got) != 2 || got[1] != billingstore.EventSessionCompleted {
		t.Errorf("billing events after delete: got %v, want [session.created session.completed]", got)
	}
}

func TestNonTerminalTransitionEmitsNoCompletedEvent(t *testing.T) {
	billing := billingstore.NewMemory()
	srv := sessionserver.New(memstore.New(), sessionserver.Options{Billing: billing})
	h := srv.Handler()

	id := createSessionID(t, h)
	// finalize moves the session to ready, a non-terminal state.
	rr := sessionRequest(t, h, http.MethodPost, "/v1/sessions/"+id+"/finalize")
	if rr.Code != http.StatusOK {
		t.Fatalf("finalize: status %d, body %s", rr.Code, rr.Body.String())
	}

	got := billingEventTypes(t, billing)
	if len(got) != 1 || got[0] != billingstore.EventSessionCreated {
		t.Errorf("a non-terminal transition must emit no session.completed event: got %v", got)
	}
}
