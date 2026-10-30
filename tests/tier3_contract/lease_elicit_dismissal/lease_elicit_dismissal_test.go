// SPDX-License-Identifier: MIT

//go:build contract

// Package lease_elicit_dismissal_test is the Tier 3 contract suite for the
// client-facing records of a §8.6 lease-extension elicitation whose
// requesting session ends before the user decides: the §7.2
// `elicitation_request` payload the client receives, the §9.2 interaction
// record the elicitor dismisses, and the answer a client gets when it
// responds to the dismissed prompt.
package lease_elicit_dismissal_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol/leaseelicit"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

const (
	tenant        = "acme"
	sessionID     = "sess_lease_elicit"
	user          = "alice@acme.com"
	elicitationID = "lease-elicit-contract"
)

// capturedEvent is one §7.2 event the elicitor published.
type capturedEvent struct {
	eventType string
	data      string
}

// TestLeaseElicitRequesterTerminalReason_spec_8_6 pins the dismissal
// reason value LEASE_EXTENSION_REQUESTER_TERMINAL on the §9.2 interaction
// record the client's elicitation resolves against, the
// `elicitation_request` payload keys the client receives, and the
// INTERACTION_ALREADY_RESOLVED refusal a client gets for answering the
// dismissed prompt.
//
// diagnosis: a different reason string means the client cannot tell a
// requester-ended dismissal from a timeout or a user dismissal; a changed
// event payload breaks clients that render the budget prompt; a resolved
// respond means a client answer is accepted for a prompt whose requester
// can receive no grant.
//
// spec: §8.6 (Elicitation mode flow), §9.2 (Elicitation chain), §7.2
func TestLeaseElicitRequesterTerminalReason_spec_8_6(t *testing.T) {
	ctx := context.Background()
	sessions := memstore.New()
	if err := sessions.Create(ctx, sessionstore.Session{
		ID: sessionID, TenantID: tenant, UserID: user, State: session.StateRunning,
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	interactions := interactionstore.NewMemory()
	var mu sync.Mutex
	var events []capturedEvent
	el := leaseelicit.New(leaseelicit.Config{
		Sessions:     sessions,
		Interactions: interactions,
		Publish: func(_, eventType, data string, _ time.Time) {
			mu.Lock()
			events = append(events, capturedEvent{eventType, data})
			mu.Unlock()
		},
		Clock:   func() time.Time { return time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC) },
		IDGen:   func() string { return elicitationID },
		Timeout: 10 * time.Second,
		Poll:    5 * time.Millisecond,
	})

	done := make(chan error, 1)
	go func() {
		_, err := el.Elicit(ctx, tenant, sessionID)
		done <- err
	}()
	waitPending(t, interactions)
	if _, err := sessions.Update(ctx, tenant, sessionID, func(s *sessionstore.Session) error {
		s.State = session.StateCancelled
		return nil
	}); err != nil {
		t.Fatalf("terminal write: %v", err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, leasecontrol.ErrRequesterEnded) {
			t.Fatalf("Elicit err = %v, want ErrRequesterEnded", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Elicit did not return after the requester ended")
	}

	if leaseelicit.ReasonRequesterTerminal != "LEASE_EXTENSION_REQUESTER_TERMINAL" {
		t.Errorf("ReasonRequesterTerminal = %q, want the §8.6 value", leaseelicit.ReasonRequesterTerminal)
	}
	rec, err := interactions.Get(ctx, tenant, sessionID, user, elicitationID)
	if err != nil {
		t.Fatalf("Get interaction: %v", err)
	}
	if rec.Kind != interactionstore.KindElicitation || rec.Phase != interactionstore.PhaseDismissed ||
		rec.Reason != "LEASE_EXTENSION_REQUESTER_TERMINAL" {
		t.Errorf("interaction = %s/%s/%q, want elicitation/dismissed/LEASE_EXTENSION_REQUESTER_TERMINAL",
			rec.Kind, rec.Phase, rec.Reason)
	}

	assertRequestPayload(t, events)

	_, err = mcptools.ResolveElicitation(ctx, interactions, tenant, sessionID, user, elicitationID,
		interactionstore.PhaseResponded, map[string]any{"action": "accept"}, "")
	if !errors.Is(err, interactionstore.ErrAlreadyResolved) {
		t.Errorf("respond to dismissed prompt err = %v, want ErrAlreadyResolved (INTERACTION_ALREADY_RESOLVED)", err)
	}
}

// assertRequestPayload checks that exactly one `elicitation_request` was
// published and that its payload carries exactly the elicitationId,
// message, and reason keys.
func assertRequestPayload(t *testing.T, events []capturedEvent) {
	t.Helper()
	if len(events) != 1 || events[0].eventType != "elicitation_request" {
		t.Fatalf("events = %+v, want one elicitation_request", events)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(events[0].data), &payload); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	if len(payload) != 3 || payload["elicitationId"] != elicitationID || payload["reason"] != "lease_extension" {
		t.Errorf("payload = %v, want elicitationId, message, and reason=lease_extension", payload)
	}
	if _, ok := payload["message"].(string); !ok {
		t.Errorf("payload message = %v, want a string", payload["message"])
	}
}

func waitPending(t *testing.T, store interactionstore.Store) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if p, err := store.ListPending(context.Background(), tenant, sessionID); err == nil && len(p) == 1 {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("lease elicitation was never stored pending")
}
