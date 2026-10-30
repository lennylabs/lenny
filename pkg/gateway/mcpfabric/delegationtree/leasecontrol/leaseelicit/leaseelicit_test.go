// SPDX-License-Identifier: MIT

package leaseelicit

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/leasecontrol"
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

func newTestElicitor(t *testing.T, store interactionstore.Store) (*Elicitor, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var events []string
	el := &Elicitor{
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

// scriptedSessionLookup answers Get from a sequence of responses, one per
// call, repeating the last once the sequence is exhausted.
type scriptedSessionLookup struct {
	mu    sync.Mutex
	resps []sessionLookupResp
	calls int
}

type sessionLookupResp struct {
	state session.State
	err   error
}

func (s *scriptedSessionLookup) Get(_ context.Context, tenantID, id string) (sessionstore.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.resps[min(s.calls, len(s.resps)-1)]
	s.calls++
	if r.err != nil {
		return sessionstore.Session{}, r.err
	}
	return sessionstore.Session{ID: id, TenantID: tenantID, UserID: "alice", State: r.state}, nil
}

func (s *scriptedSessionLookup) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// TestLeaseElicitorEndedRequesterBeforePut_spec_8_6: a terminal or missing
// requester returns ErrRequesterEnded and writes no interaction, and any
// other session-store read error is returned with no interaction written.
//
// spec: §8.6 (Extension state), §8.6 (Elicitation mode flow)
// diagnosis: a stored interaction means the elicitor presents a prompt for
// a session that can receive no grant.
func TestLeaseElicitorEndedRequesterBeforePut_spec_8_6(t *testing.T) {
	readErr := errors.New("postgres unreachable")
	cases := []struct {
		name      string
		resp      sessionLookupResp
		wantEnded bool
		wantErr   error
	}{
		{"terminal", sessionLookupResp{state: session.StateCompleted}, true, nil},
		{"not found", sessionLookupResp{err: sessionstore.ErrNotFound}, true, nil},
		{"read error", sessionLookupResp{err: readErr}, false, readErr},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := interactionstore.NewMemory()
			el, events := newTestElicitor(t, store)
			el.sessions = &scriptedSessionLookup{resps: []sessionLookupResp{tc.resp}}

			approved, err := el.Elicit(context.Background(), "acme", "sess-1")
			if approved || err == nil {
				t.Fatalf("Elicit = %v, %v; want an error", approved, err)
			}
			if got := errors.Is(err, leasecontrol.ErrRequesterEnded); got != tc.wantEnded {
				t.Errorf("errors.Is(err, ErrRequesterEnded) = %v, want %v (err %v)", got, tc.wantEnded, err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Errorf("err = %v, want it to wrap %v", err, tc.wantErr)
			}
			if _, gerr := store.Get(context.Background(), "acme", "sess-1", "alice", "lease-elicit-test"); !errors.Is(gerr, interactionstore.ErrNotFound) {
				t.Errorf("interaction lookup err = %v, want ErrNotFound (none written)", gerr)
			}
			if len(*events) != 0 {
				t.Errorf("events = %v, want none", *events)
			}
		})
	}
}

// TestLeaseElicitorRequesterEndsWhilePolling_spec_9_2: a requester that is
// terminal or missing on a poll tick dismisses the interaction with
// LEASE_EXTENSION_REQUESTER_TERMINAL and returns ErrRequesterEnded.
//
// spec: §9.2 (Elicitation chain), §8.6 (Elicitation mode flow)
// diagnosis: a pending interaction after return, or a timeout error, means
// the poll loop does not re-read the requester, so the prompt outlives its
// session.
func TestLeaseElicitorRequesterEndsWhilePolling_spec_9_2(t *testing.T) {
	for name, ended := range map[string]sessionLookupResp{
		"terminal":  {state: session.StateCancelled},
		"not found": {err: sessionstore.ErrNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			store := interactionstore.NewMemory()
			el, _ := newTestElicitor(t, store)
			el.sessions = &scriptedSessionLookup{resps: []sessionLookupResp{{state: session.StateRunning}, ended}}

			approved, err := el.Elicit(context.Background(), "acme", "sess-1")
			if approved || !errors.Is(err, leasecontrol.ErrRequesterEnded) {
				t.Fatalf("Elicit = %v, %v; want ErrRequesterEnded", approved, err)
			}
			cur, gerr := store.Get(context.Background(), "acme", "sess-1", "alice", "lease-elicit-test")
			if gerr != nil {
				t.Fatalf("Get: %v", gerr)
			}
			if cur.Phase != interactionstore.PhaseDismissed || cur.Reason != ReasonRequesterTerminal {
				t.Errorf("interaction = %s/%q, want dismissed/%s", cur.Phase, cur.Reason, ReasonRequesterTerminal)
			}
		})
	}
}

// TestLeaseElicitorTickReadErrorKeepsPolling_spec_8_6: a session-store
// read error on a poll tick leaves the interaction pending and polling
// continues, so a later approval is still honored.
//
// spec: §8.6 (Elicitation mode flow)
// diagnosis: a dismissed interaction or an error means a transient store
// failure is treated as the requester ending.
func TestLeaseElicitorTickReadErrorKeepsPolling_spec_8_6(t *testing.T) {
	store := interactionstore.NewMemory()
	el, _ := newTestElicitor(t, store)
	lookup := &scriptedSessionLookup{resps: []sessionLookupResp{
		{state: session.StateRunning},
		{err: errors.New("postgres unreachable")},
	}}
	el.sessions = lookup
	go func() {
		deadline := time.Now().Add(2 * time.Second)
		for lookup.callCount() < 4 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		cur, err := store.Get(context.Background(), "acme", "sess-1", "alice", "lease-elicit-test")
		if err != nil || cur.Phase != interactionstore.PhasePending {
			return
		}
		_, _ = store.Resolve(context.Background(), "acme", "sess-1", "alice", "lease-elicit-test", func(i *interactionstore.Interaction) error {
			i.Phase = interactionstore.PhaseApproved
			return nil
		})
	}()

	approved, err := el.Elicit(context.Background(), "acme", "sess-1")
	if err != nil || !approved {
		t.Fatalf("Elicit = %v, %v; want approved after tick read errors", approved, err)
	}
	if lookup.callCount() < 3 {
		t.Errorf("session reads = %d, want at least 3 (polling continued)", lookup.callCount())
	}
}

// TestLeaseDecisionDismissedRequesterTerminal: a stored dismissal carrying
// the requester-terminal reason classifies as ErrRequesterEnded, and any
// other dismissal does not.
//
// spec: §8.6 (Elicitation mode flow), §9.2
func TestLeaseDecisionDismissedRequesterTerminal(t *testing.T) {
	_, decided, err := leaseDecision(interactionstore.Interaction{ID: "x", Phase: interactionstore.PhaseDismissed, Reason: ReasonRequesterTerminal})
	if !decided || !errors.Is(err, leasecontrol.ErrRequesterEnded) {
		t.Errorf("requester-terminal dismissal = %v, %v; want decided with ErrRequesterEnded", decided, err)
	}
	_, decided, err = leaseDecision(interactionstore.Interaction{ID: "x", Phase: interactionstore.PhaseDismissed, Reason: "USER_DISMISSED"})
	if !decided || err == nil || errors.Is(err, leasecontrol.ErrRequesterEnded) {
		t.Errorf("user dismissal = %v, %v; want decided with a non-requester error", decided, err)
	}
	if _, decided, _ = leaseDecision(interactionstore.Interaction{Phase: interactionstore.PhasePending}); decided {
		t.Error("pending interaction classified as decided")
	}
}
