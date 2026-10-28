// SPDX-License-Identifier: MIT

package watchdog_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/watchdog"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// perChildMaxAgeSeconds is the slice every child in these tests is granted.
const perChildMaxAgeSeconds = 3600

// leaseCfg isolates the perChildMaxAge leg: the maxSessionAge cap is
// capSeconds and every other expiry clock, the suspended ones included, is
// set far past anything these tests reach.
func leaseCfg(capSeconds int) watchdog.Config {
	cfg := ageCfg(capSeconds)
	cfg.MaxSuspendedSessionSeconds = idleCapDisabled
	return cfg
}

// seedParentAndChild seeds a running root "root" and its delegated child
// "child" built by mutate from a child created at born.
func seedParentAndChild(t *testing.T, store sessionstore.Store, born time.Time, mutate func(*sessionstore.Session)) {
	t.Helper()
	mustCreate(t, store, sessionstore.Session{
		ID: "root", TenantID: "acme", State: session.StateRunning,
		CreatedAt: born, UpdatedAt: born, LastAgentActivityAt: born,
	})
	child := sessionstore.Session{
		ID: "child", TenantID: "acme", State: session.StateRunning,
		ParentSessionID: "root", DelegationDepth: 1,
		CreatedAt: born, UpdatedAt: born, LastAgentActivityAt: born,
		DelegationLease: &sessionstore.DelegationLease{PerChildMaxAge: perChildMaxAgeSeconds},
	}
	if mutate != nil {
		mutate(&child)
	}
	if child.State == session.StateSuspended {
		child.SuspendedAt = born
	}
	mustCreate(t, store, child)
}

// spec: §8.2 (LeaseSlice perChildMaxAge), §7.2 (Session State Machine)
//
// A delegated child that has lived past the perChildMaxAge its stored slice
// grants, counted in wall-clock time from its creation, is expired with
// `expired:lease` in every non-terminal state, suspended included, even
// though its active session age is far under the maxSessionAge cap. Before
// the lease leg existed no sweep enforced perChildMaxAge, so each such child
// lived on. The expiry runs the terminal hook once and fires no §16.1.1
// max_session_age notification, because perChildMaxAge is not a platform
// clock.
func TestSweepMaxAgeExpiresChildPastPerChildMaxAge_spec_8_2(t *testing.T) {
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := born.Add((perChildMaxAgeSeconds + 1) * time.Second)
	for _, st := range []session.State{
		session.StateRunning,
		session.StateSuspended,
		session.StateResumePending,
		session.StateAwaitingClientAction,
	} {
		t.Run(string(st), func(t *testing.T) {
			store := memstore.New()
			seedParentAndChild(t, store, born, func(c *sessionstore.Session) {
				c.State = st
				c.LastAgentActivityAt = now
			})
			hook := &expiryCapture{}
			w := watchdog.New(store, watchdog.StaticTenants{"acme"}, leaseCfg(idleCapDisabled), nil).WithTerminalHook(hook)
			res, err := w.Tick(context.Background(), now)
			if err != nil {
				t.Fatalf("Tick: %v", err)
			}
			w.WaitDispatched()
			row := mustGet(t, store, "child")
			if row.State != session.StateExpired || row.FailureReason != string(session.FailureExpiredLease) {
				t.Fatalf("child = %q reason %q, want expired with %q", row.State, row.FailureReason, session.FailureExpiredLease)
			}
			if res.Expirations != 1 || len(hook.terminal) != 1 || hook.terminal[0].ID != "child" {
				t.Fatalf("Expirations %d, terminal %+v; want the child's terminal hook once", res.Expirations, hook.terminal)
			}
			if len(hook.expiries) != 0 {
				t.Fatalf("expiry notifications = %v, want none for a perChildMaxAge expiry", hook.reasons())
			}
			if parent := mustGet(t, store, "root"); parent.State != session.StateRunning {
				t.Fatalf("parent state = %q, want running", parent.State)
			}
		})
	}
}

// spec: §8.2 (LeaseSlice perChildMaxAge)
//
// The lease leg applies only to a delegated child whose stored slice carries
// a positive perChildMaxAge, and only once the child is strictly older than
// it. A child one second short and a child exactly at the bound survive; a
// root session carrying a DelegationLease is not a child and survives; a
// child whose slice carries zero and a child with no stored slice survive.
func TestSweepMaxAgeLeavesRowsWithinOrWithoutPerChildMaxAge_spec_8_2(t *testing.T) {
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		at     time.Duration
		mutate func(*sessionstore.Session)
	}{
		{"child one second short", (perChildMaxAgeSeconds - 1) * time.Second, nil},
		{"child exactly at the bound", perChildMaxAgeSeconds * time.Second, nil},
		{"root carrying a lease", 2 * perChildMaxAgeSeconds * time.Second, func(c *sessionstore.Session) {
			c.ParentSessionID = ""
			c.DelegationDepth = 0
		}},
		{"child with a zero perChildMaxAge", 2 * perChildMaxAgeSeconds * time.Second, func(c *sessionstore.Session) {
			c.DelegationLease = &sessionstore.DelegationLease{MaxChildrenTotal: 3}
		}},
		{"child with no stored slice", 2 * perChildMaxAgeSeconds * time.Second, func(c *sessionstore.Session) {
			c.DelegationLease = nil
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			now := born.Add(tc.at)
			store := memstore.New()
			seedParentAndChild(t, store, born, func(c *sessionstore.Session) {
				c.LastAgentActivityAt = now
				if tc.mutate != nil {
					tc.mutate(c)
				}
			})
			hook := &expiryCapture{}
			w := watchdog.New(store, watchdog.StaticTenants{"acme"}, leaseCfg(idleCapDisabled), nil).WithTerminalHook(hook)
			res, err := w.Tick(context.Background(), now)
			if err != nil {
				t.Fatalf("Tick: %v", err)
			}
			if row := mustGet(t, store, "child"); row.State != session.StateRunning {
				t.Fatalf("child = %q reason %q, want running", row.State, row.FailureReason)
			}
			if res.Expirations != 0 || len(hook.terminal) != 0 || len(hook.expiries) != 0 {
				t.Fatalf("Expirations %d, terminal %d, expiries %d; want no side effect", res.Expirations, len(hook.terminal), len(hook.expiries))
			}
		})
	}
}

// spec: §8.2 (LeaseSlice perChildMaxAge), §6.2 (maxSessionAge timer behavior
// across states), §16.1.1 (reason vocabulary)
//
// A child past both its perChildMaxAge and its maxSessionAge cap is expired
// once with `expired:deadline`, and the max_session_age expiry notification
// fires for it, because the age leg holds.
func TestSweepMaxAgeChildPastBothBoundsTakesDeadline_spec_8_2(t *testing.T) {
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := born.Add(2 * perChildMaxAgeSeconds * time.Second)
	store := memstore.New()
	seedParentAndChild(t, store, born, func(c *sessionstore.Session) {
		c.LastAgentActivityAt = now
		c.AccumulatedSessionAgeSeconds = 2 * perChildMaxAgeSeconds
	})
	hook := &expiryCapture{}
	// The cap expires the child but not its younger-by-active-time parent.
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, leaseCfg(perChildMaxAgeSeconds*3), nil).WithTerminalHook(hook)
	res, err := w.Tick(context.Background(), now)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	row := mustGet(t, store, "child")
	if row.State != session.StateExpired || row.FailureReason != string(session.FailureExpiredDeadline) {
		t.Fatalf("child = %q reason %q, want expired with %q", row.State, row.FailureReason, session.FailureExpiredDeadline)
	}
	if res.Expirations != 1 {
		t.Fatalf("Expirations = %d, want 1", res.Expirations)
	}
	if got := hook.reasons(); len(got) != 1 || got[0] != watchdog.ExpiryReasonMaxSessionAge {
		t.Fatalf("expiry reasons = %v, want exactly [%s]", got, watchdog.ExpiryReasonMaxSessionAge)
	}
}

// spec: §8.2 (LeaseSlice perChildMaxAge), §10.1.1 (Primary:)
//
// The lease leg is re-checked on the locked row. A listing that shows a
// child past its perChildMaxAge selects the row, but when the locked row is
// already terminal, or its stored slice does not put it past the bound, the
// mutator returns the superseded sentinel: the row is not rewritten, no hook
// runs, and the sweep goes on to expire the next listed child.
func TestSweepMaxAgeRechecksPerChildMaxAgeOnLockedRow_spec_8_2(t *testing.T) {
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := born.Add((perChildMaxAgeSeconds + 1) * time.Second)
	cases := []struct {
		name   string
		stored func(sessionstore.Session) sessionstore.Session
	}{
		{"locked row terminal", func(r sessionstore.Session) sessionstore.Session {
			r.State = session.StateCompleted
			return r
		}},
		{"locked row within its slice", func(r sessionstore.Session) sessionstore.Session {
			r.DelegationLease = &sessionstore.DelegationLease{PerChildMaxAge: 2 * perChildMaxAgeSeconds}
			return r
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mem := memstore.New()
			seedParentAndChild(t, mem, born, func(c *sessionstore.Session) { c.LastAgentActivityAt = now })
			listedChild := mustGet(t, mem, "child")
			mem2 := memstore.New()
			mustCreate(t, mem2, mustGet(t, mem, "root"))
			stored := tc.stored(listedChild)
			mustCreate(t, mem2, stored)
			next := listedChild
			next.ID = "child2"
			mustCreate(t, mem2, next)
			store := &staleListStore{Store: mem2, listed: []sessionstore.Session{listedChild, next}}
			hook := &expiryCapture{}
			w := watchdog.New(store, watchdog.StaticTenants{"acme"}, leaseCfg(idleCapDisabled), nil).WithTerminalHook(hook)

			res, err := w.Tick(context.Background(), now)
			if err != nil {
				t.Fatalf("Tick: %v", err)
			}
			w.WaitDispatched()
			got := mustGet(t, mem2, "child")
			if got.State != stored.State || !got.UpdatedAt.Equal(stored.UpdatedAt) {
				t.Fatalf("superseded row was written: state %q UpdatedAt %v, want %q %v", got.State, got.UpdatedAt, stored.State, stored.UpdatedAt)
			}
			if res.Expirations != 1 || len(hook.terminal) != 1 || hook.terminal[0].ID != "child2" {
				t.Fatalf("Expirations %d, terminal %+v; want only child2 expired", res.Expirations, hook.terminal)
			}
			if row := mustGet(t, mem2, "child2"); row.FailureReason != string(session.FailureExpiredLease) {
				t.Fatalf("child2 reason = %q, want %q", row.FailureReason, session.FailureExpiredLease)
			}
		})
	}
}
