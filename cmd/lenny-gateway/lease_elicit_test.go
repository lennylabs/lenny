// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/session/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// fakeSessionLookup resolves a single session's owning user, the narrow
// dependency leaseElicitor needs.
type fakeSessionLookup struct {
	userID string
	err    error
}

func (f fakeSessionLookup) Get(_ context.Context, tenantID, id string) (sessionstore.Session, error) {
	if f.err != nil {
		return sessionstore.Session{}, f.err
	}
	return sessionstore.Session{ID: id, TenantID: tenantID, UserID: f.userID}, nil
}

func newTestElicitor(t *testing.T, store interactionstore.Store) (*leaseElicitor, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var events []string
	el := &leaseElicitor{
		sessions:     fakeSessionLookup{userID: "alice"},
		interactions: store,
		publish: func(_, eventType, _ string, _ time.Time) {
			mu.Lock()
			events = append(events, eventType)
			mu.Unlock()
		},
		clock:   func() time.Time { return time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC) },
		idgen:   func() string { return "lease-elicit-test" },
		timeout: 2 * time.Second,
		poll:    5 * time.Millisecond,
	}
	return el, &events
}

// resolveWhenPending waits for the elicitation to be recorded pending,
// then applies mutate to resolve it, so the blocking Elicit call
// observes the resolution on its next poll.
func resolveWhenPending(t *testing.T, store interactionstore.Store, mutate func(*interactionstore.Interaction)) {
	t.Helper()
	go func() {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			pending, err := store.ListPending(context.Background(), "acme", "sess-1")
			if err == nil && len(pending) == 1 {
				_, _ = store.Resolve(context.Background(), "acme", "sess-1", "alice", pending[0].ID, func(i *interactionstore.Interaction) error {
					mutate(i)
					return nil
				})
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()
}

// TestLeaseElicitorApproved_spec_8_6_line_720: a PhaseApproved resolution
// (the §7.2 approve endpoint) returns approved=true and publishes the
// elicitation_request event. F-8.6.2.
func TestLeaseElicitorApproved_spec_8_6_line_720(t *testing.T) {
	store := interactionstore.NewMemory()
	el, events := newTestElicitor(t, store)
	resolveWhenPending(t, store, func(i *interactionstore.Interaction) { i.Phase = interactionstore.PhaseApproved })

	approved, err := el.Elicit(context.Background(), "acme", "sess-1")
	if err != nil {
		t.Fatalf("Elicit: %v", err)
	}
	if !approved {
		t.Errorf("approved = false, want true")
	}
	if len(*events) != 1 || (*events)[0] != "elicitation_request" {
		t.Errorf("events = %v, want one elicitation_request", *events)
	}
}

// TestLeaseElicitorDenied_spec_8_6_line_729: a PhaseDenied resolution (the
// §7.2 deny endpoint) returns approved=false with no error, so the caller
// marks the subtree denied. F-8.6.2.
func TestLeaseElicitorDenied_spec_8_6_line_729(t *testing.T) {
	store := interactionstore.NewMemory()
	el, _ := newTestElicitor(t, store)
	resolveWhenPending(t, store, func(i *interactionstore.Interaction) { i.Phase = interactionstore.PhaseDenied })

	approved, err := el.Elicit(context.Background(), "acme", "sess-1")
	if err != nil {
		t.Fatalf("Elicit: %v", err)
	}
	if approved {
		t.Errorf("approved = true, want false (user denied)")
	}
}

// TestLeaseElicitorRespondedDecline_spec_8_6_line_727: a PhaseResponded
// answer whose action declines is a rejection. F-8.6.2.
func TestLeaseElicitorRespondedDecline_spec_8_6_line_727(t *testing.T) {
	store := interactionstore.NewMemory()
	el, _ := newTestElicitor(t, store)
	resolveWhenPending(t, store, func(i *interactionstore.Interaction) {
		i.Phase = interactionstore.PhaseResponded
		i.Response = map[string]any{"action": "decline"}
	})

	approved, err := el.Elicit(context.Background(), "acme", "sess-1")
	if err != nil {
		t.Fatalf("Elicit: %v", err)
	}
	if approved {
		t.Errorf("approved = true, want false (response declined)")
	}
}

// TestLeaseElicitorTimeout_spec_8_6_line_727: when no resolution arrives,
// Elicit times out, returns an error (a non-decision), and dismisses the
// pending interaction so it does not linger. F-8.6.2.
func TestLeaseElicitorTimeout_spec_8_6_line_727(t *testing.T) {
	store := interactionstore.NewMemory()
	el, _ := newTestElicitor(t, store)
	el.timeout = 30 * time.Millisecond

	approved, err := el.Elicit(context.Background(), "acme", "sess-1")
	if err == nil {
		t.Fatalf("Elicit err = nil, want a timeout error")
	}
	if approved {
		t.Errorf("approved = true, want false on timeout")
	}
	cur, err := store.Get(context.Background(), "acme", "sess-1", "alice", "lease-elicit-test")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if cur.Phase != interactionstore.PhaseDismissed {
		t.Errorf("phase = %q, want dismissed after timeout", cur.Phase)
	}
}

// TestElicitationDeclinedInterpretation covers the response-decoding
// helper across the §9.2 elicitation answer shapes the gateway accepts.
func TestElicitationDeclinedInterpretation(t *testing.T) {
	cases := []struct {
		name string
		resp any
		want bool
	}{
		{"bool true approves", true, false},
		{"bool false declines", false, true},
		{"string accept approves", "accept", false},
		{"string decline declines", "decline", true},
		{"action accept approves", map[string]any{"action": "accept"}, false},
		{"action cancel declines", map[string]any{"action": "cancel"}, true},
		{"approved=false declines", map[string]any{"approved": false}, true},
		{"nil approves", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := elicitationDeclined(tc.resp); got != tc.want {
				t.Errorf("elicitationDeclined(%v) = %v, want %v", tc.resp, got, tc.want)
			}
		})
	}
}
