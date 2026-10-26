// SPDX-License-Identifier: MIT

package sessionstore_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

var windowNow = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

// TestInPostTerminalWindowTable_spec_7_1 pins the §7.1 post-terminal window
// predicate over state, PodAssignment, PodReleasedAt, TerminatedAt (zero,
// inside, at, and past the cap), and the cap (zero and positive), and the
// pod-holder predicate built on it.
//
// spec: §7.1 (Post-terminal window)
func TestInPostTerminalWindowTable_spec_7_1(t *testing.T) {
	const capD = 5 * time.Minute
	inside := windowNow.Add(-time.Minute)
	at := windowNow.Add(-capD)
	pastCap := windowNow.Add(-capD - time.Second)
	for _, tc := range []struct {
		name     string
		row      sessionstore.Session
		cap      time.Duration
		inWindow bool
		holds    bool
	}{
		{"terminal, pod, no release, inside", sessionstore.Session{State: session.StateCompleted, PodAssignment: "p", TerminatedAt: inside}, capD, true, true},
		{"each terminal state counts", sessionstore.Session{State: session.StateExpired, PodAssignment: "p", TerminatedAt: inside}, capD, true, true},
		{"at the cap is outside", sessionstore.Session{State: session.StateFailed, PodAssignment: "p", TerminatedAt: at}, capD, false, false},
		{"past the cap is outside", sessionstore.Session{State: session.StateCancelled, PodAssignment: "p", TerminatedAt: pastCap}, capD, false, false},
		{"zero TerminatedAt is outside", sessionstore.Session{State: session.StateCompleted, PodAssignment: "p"}, capD, false, false},
		{"release recorded is outside", sessionstore.Session{State: session.StateCompleted, PodAssignment: "p", TerminatedAt: inside, PodReleasedAt: inside}, capD, false, false},
		{"no pod is outside", sessionstore.Session{State: session.StateCompleted, TerminatedAt: inside}, capD, false, false},
		{"zero cap puts no row in the window", sessionstore.Session{State: session.StateCompleted, PodAssignment: "p", TerminatedAt: inside}, 0, false, false},
		{"negative cap puts no row in the window", sessionstore.Session{State: session.StateCompleted, PodAssignment: "p", TerminatedAt: inside}, -time.Minute, false, false},
		{"non-terminal is not in a window but holds", sessionstore.Session{State: session.StateRunning, PodAssignment: "p"}, capD, false, true},
		{"non-terminal with no pod holds nothing", sessionstore.Session{State: session.StateRunning}, capD, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sessionstore.InPostTerminalWindow(tc.row, windowNow, tc.cap); got != tc.inWindow {
				t.Errorf("InPostTerminalWindow = %v, want %v", got, tc.inWindow)
			}
			if got := sessionstore.HoldsPod(tc.row, windowNow, tc.cap); got != tc.holds {
				t.Errorf("HoldsPod = %v, want %v", got, tc.holds)
			}
		})
	}
}

// TestMarkPodReleasedSetsTheMarkerOnce_spec_7_1 pins the release-marker
// mutator: it sets PodReleasedAt on a terminal row with no marker, and
// returns ErrSuperseded, changing nothing, on a non-terminal row and on a
// row that already records a release.
//
// spec: §7.1 (Post-terminal window)
func TestMarkPodReleasedSetsTheMarkerOnce_spec_7_1(t *testing.T) {
	first := windowNow
	row := sessionstore.Session{State: session.StateCompleted}
	if err := sessionstore.MarkPodReleased(first)(&row); err != nil || !row.PodReleasedAt.Equal(first) {
		t.Fatalf("first mark = %v, PodReleasedAt %v; want nil and %v", err, row.PodReleasedAt, first)
	}
	if err := sessionstore.MarkPodReleased(first.Add(time.Minute))(&row); !errors.Is(err, sessionstore.ErrSuperseded) || !row.PodReleasedAt.Equal(first) {
		t.Errorf("second mark = %v, PodReleasedAt %v; want ErrSuperseded and the first instant", err, row.PodReleasedAt)
	}
	live := sessionstore.Session{State: session.StateRunning}
	if err := sessionstore.MarkPodReleased(first)(&live); !errors.Is(err, sessionstore.ErrSuperseded) || !live.PodReleasedAt.IsZero() {
		t.Errorf("mark on a running row = %v, PodReleasedAt %v; want ErrSuperseded and zero", err, live.PodReleasedAt)
	}
}

// failingUpdater is a RowUpdater whose Update fails with err and records
// whether its context had ended and carried a deadline during the call.
type failingUpdater struct {
	err         error
	ctxErr      error
	hasDeadline bool
}

func (f *failingUpdater) Update(ctx context.Context, _, _ string, _ func(*sessionstore.Session) error) (sessionstore.Session, error) {
	f.ctxErr = ctx.Err()
	_, f.hasDeadline = ctx.Deadline()
	return sessionstore.Session{}, f.err
}

// TestRecordPodReleaseOutcomes_spec_7_1 pins RecordPodRelease: it records the
// marker on a terminal row once, writes nothing on a running row, treats a
// missing row and a second record as no error, wraps any other store error,
// and writes on a context that the caller's cancellation does not end and
// that carries a deadline.
//
// spec: §7.1 (Post-terminal window)
func TestRecordPodReleaseOutcomes_spec_7_1(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	for _, r := range []sessionstore.Session{
		{ID: "ended", TenantID: "acme", State: session.StateCompleted, PodAssignment: "p", TerminatedAt: windowNow},
		{ID: "live", TenantID: "acme", State: session.StateRunning, PodAssignment: "p"},
	} {
		if err := store.Create(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := sessionstore.RecordPodRelease(cancelled, store, "acme", "ended", windowNow); err != nil {
		t.Fatalf("record on a cancelled caller context = %v, want nil", err)
	}
	if err := sessionstore.RecordPodRelease(ctx, store, "acme", "ended", windowNow.Add(time.Minute)); err != nil {
		t.Errorf("second record = %v, want nil", err)
	}
	got, _ := store.Get(ctx, "acme", "ended")
	if !got.PodReleasedAt.Equal(windowNow) {
		t.Errorf("PodReleasedAt = %v, want the first record %v", got.PodReleasedAt, windowNow)
	}
	if err := sessionstore.RecordPodRelease(ctx, store, "acme", "live", windowNow); err != nil {
		t.Errorf("record on a running row = %v, want nil", err)
	}
	if live, _ := store.Get(ctx, "acme", "live"); !live.PodReleasedAt.IsZero() {
		t.Errorf("running row PodReleasedAt = %v, want zero", live.PodReleasedAt)
	}
	if err := sessionstore.RecordPodRelease(ctx, store, "acme", "gone", windowNow); err != nil {
		t.Errorf("record on a missing row = %v, want nil", err)
	}
	down := errors.New("primary unreachable")
	f := &failingUpdater{err: down}
	if err := sessionstore.RecordPodRelease(cancelled, f, "acme", "ended", windowNow); !errors.Is(err, down) {
		t.Errorf("record on a failing store = %v, want it to wrap %v", err, down)
	}
	if f.ctxErr != nil {
		t.Errorf("write context ended with the caller's: %v", f.ctxErr)
	}
	if !f.hasDeadline {
		t.Error("write context carries no deadline")
	}
}
