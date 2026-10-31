// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/watchdog"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// TestWatchdogDrivesTheResumeDriver_spec_7_3 drives the resume driver through
// Watchdog.Tick with the session server as the ResumeDriver. A pre-claim
// failure leaves the row resume_pending with no attempt entry and writes
// nothing, and the next tick starts exactly one new attempt; no tick starts an
// attempt while one is in flight; the attempt's first bound is the window
// left; and a failure that recurs on every tick ends in
// awaiting_client_action when the window elapses, with the resume-attempts
// counter unchanged.
//
// spec: §7.3 (Resume driver), §6.2 (Pod State Machine)
func TestWatchdogDrivesTheResumeDriver_spec_7_3(t *testing.T) {
	f := newFunnelFixture(t, funnelOpts{noPool: true})
	var mu sync.Mutex
	var bounds []time.Duration
	f.srv.afterFunc = func(d time.Duration, fn func()) func() bool {
		mu.Lock()
		bounds = append(bounds, d)
		mu.Unlock()
		return time.AfterFunc(d, fn).Stop
	}
	counted := 0
	f.srv.incSessionResumeAttempt = func(string, string) { counted++ }
	if err := f.store.Create(context.Background(), sessionstore.Session{
		UserID: testOwner,
		ID:     "s", TenantID: "acme", RuntimeRef: "echo", State: session.StateResumePending,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	f.leases.holders[leaseK("acme", "s")] = "rep-1"
	row := f.state(t, "s")
	f.srv.clock = func() time.Time { return row.UpdatedAt.Add(time.Second) }
	w := watchdog.New(f.store, watchdog.StaticTenants{"acme"}, watchdog.Config{
		MaxResumePendingSeconds: 900, MaxIdleSeconds: 100000, MaxSessionAgeSeconds: 100000,
		MaxAwaitingClientActionSeconds: 100000,
	}, nil).WithTerminalHook(f.srv)

	tick := func(at time.Time) {
		t.Helper()
		if _, err := w.Tick(context.Background(), at); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		f.srv.waitAttempts(t)
	}

	tick(row.UpdatedAt.Add(time.Second))
	if got := f.state(t, "s"); got.State != session.StateResumePending || !got.UpdatedAt.Equal(row.UpdatedAt) {
		t.Fatalf("row after a pre-claim failure = %q (updated %v), want resume_pending unchanged", got.State, got.UpdatedAt)
	}
	if f.leases.acquires != 1 {
		t.Fatalf("attempts = %d, want 1", f.leases.acquires)
	}
	mu.Lock()
	if len(bounds) != 1 || bounds[0] != 899*time.Second {
		t.Errorf("first attempt bound = %v, want the window left, 899s", bounds)
	}
	mu.Unlock()

	tick(row.UpdatedAt.Add(2 * time.Second))
	if f.leases.acquires != 2 {
		t.Errorf("attempts after the second tick = %d, want exactly one more", f.leases.acquires)
	}

	if !f.srv.resumeAttempts.begin("s", func() {}) {
		t.Fatal("begin refused")
	}
	if _, err := w.Tick(context.Background(), row.UpdatedAt.Add(3*time.Second)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if f.leases.acquires != 2 {
		t.Errorf("a tick started an attempt while one was in flight (attempts = %d)", f.leases.acquires)
	}
	f.srv.resumeAttempts.end("s")

	tick(row.UpdatedAt.Add(901 * time.Second))
	if got := f.state(t, "s"); got.State != session.StateAwaitingClientAction {
		t.Errorf("state after the window = %q, want awaiting_client_action", got.State)
	}
	if counted != 0 {
		t.Errorf("resume-attempts counter = %d, want unchanged: no attempt wrote its entry", counted)
	}
}

// waitAttempts waits until no resume attempt is in flight on this replica.
func (s *Server) waitAttempts(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for s.resumeAttempts.size() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("attempts in flight = %d after 5s", s.resumeAttempts.size())
		}
		time.Sleep(2 * time.Millisecond)
	}
}
