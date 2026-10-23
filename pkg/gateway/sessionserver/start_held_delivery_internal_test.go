// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionevents"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// readyHeldFixture is a held-message fixture whose session is ready, with
// two messages held in acceptance order.
func readyHeldFixture(t *testing.T) *heldFixture {
	t.Helper()
	f := newHeldFixture(t, false)
	if _, err := f.store.Update(context.Background(), "acme", "s", func(r *sessionstore.Session) error {
		r.State = session.StateReady
		return nil
	}); err != nil {
		t.Fatalf("ready: %v", err)
	}
	base := time.Now().Add(-time.Minute)
	f.holdDLQ(t, "m1", "", base, heldPayload(t, "m1", "", "a"), 0)
	f.holdDLQ(t, "m2", "", base.Add(time.Second), heldPayload(t, "m2", "", "b"), 0)
	return f
}

// TestStartDeliversHeldMessagesAfterItsCommit_spec_7_2 asserts that POST
// /start delivers the messages held before running after its commit, in
// acceptance order, and that a failed held Send still answers 200 with running
// and leaves the entry at the head for a later delivery.
//
// spec: §7.2 (Message delivery routing — seven paths, path 6)
func TestStartDeliversHeldMessagesAfterItsCommit_spec_7_2(t *testing.T) {
	t.Run("delivered in acceptance order", func(t *testing.T) {
		f := readyHeldFixture(t)
		code, ans := postStep(t, f.srv, "/v1/sessions/s/start")
		if code != http.StatusOK || ans.State != string(session.StateRunning) {
			t.Fatalf("start = %d %q, want 200 running", code, ans.State)
		}
		if got := f.exec.ids(); len(got) != 2 || got[0] != "m1" || got[1] != "m2" {
			t.Errorf("delivered %v, want [m1 m2]", got)
		}
		if n := f.remaining(t); n != 0 {
			t.Errorf("held = %d, want 0", n)
		}
	})
	t.Run("failed send", func(t *testing.T) {
		f := readyHeldFixture(t)
		f.exec.failNext = 1
		code, ans := postStep(t, f.srv, "/v1/sessions/s/start")
		if code != http.StatusOK || ans.State != string(session.StateRunning) {
			t.Fatalf("start = %d %q, want 200 running", code, ans.State)
		}
		if n := f.remaining(t); n != 2 {
			t.Errorf("held = %d, want both entries left for a later delivery", n)
		}
	})
}

// TestStartCommitIsGuardedOnTheLockedRow_spec_7_2 asserts that the POST
// /start commit is guarded: a peer's cancelled write before the commit
// answers the precondition error and leaves the row cancelled, and a racing
// second start that committed first answers the precondition error with
// running and runs no status event or held delivery.
//
// spec: §7.2 (Interactive Session Model)
func TestStartCommitIsGuardedOnTheLockedRow_spec_7_2(t *testing.T) {
	for _, tc := range []struct {
		name    string
		reaches session.State
	}{
		{"peer cancelled", session.StateCancelled},
		{"second start committed first", session.StateRunning},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := readyHeldFixture(t)
			bus := sessionevents.NewBus(64)
			f.srv.events = bus
			f.srv.store = &movingStore{Store: f.store, move: func(r *sessionstore.Session) { r.State = tc.reaches }}
			code, ans := postStep(t, f.srv, "/v1/sessions/s/start")
			if code != http.StatusConflict {
				t.Fatalf("start = %d (%s), want 409", code, ans.Error.Code)
			}
			if got := f.row(t); got.State != tc.reaches {
				t.Errorf("state = %q, want %q kept", got.State, tc.reaches)
			}
			if n := eventCount(bus, "s", "status_change"); n != 0 {
				t.Errorf("status_change events = %d, want 0", n)
			}
			if got := f.exec.ids(); len(got) != 0 {
				t.Errorf("delivered %v, want nothing", got)
			}
		})
	}
}

func postStep(t *testing.T, srv *Server, path string) (int, resumeAnswer) {
	t.Helper()
	return postPath(t, srv, path)
}
