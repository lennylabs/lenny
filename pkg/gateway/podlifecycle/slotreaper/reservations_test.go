// SPDX-License-Identifier: MIT

package slotreaper_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/slotreaper"
)

// fakeReclaimer records each ReclaimSlotReservations cutoff and returns a
// configured count or error.
type fakeReclaimer struct {
	cutoffs []time.Time
	n       int
	err     error
}

func (f *fakeReclaimer) ReclaimSlotReservations(_ context.Context, cutoff time.Time) (int, error) {
	f.cutoffs = append(f.cutoffs, cutoff)
	return f.n, f.err
}

// spec: §12.4 (Redis failure behavior), §4.6.1 (Orphaned SandboxClaim
// detection).
//
// Each pass reclaims the abandoned slot reservation records once, with the
// cutoff the terminal age (claimOrphanTimeout) before the pass's clock, and
// reports the count. A reclaim failure is logged and does not stop the
// pass's member releases.
func TestReaperReclaimsAbandonedReservations_spec_12_4(t *testing.T) {
	for _, tc := range []struct {
		name    string
		n       int
		err     error
		wantN   int
		wantLog string
	}{
		{name: "reclaims", n: 3, wantN: 3},
		{name: "nothing to reclaim", n: 0, wantN: 0},
		{name: "reclaim fails", err: errors.New("postgres unavailable"), wantN: 0, wantLog: "reclaim slot reservations failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newWorld()
			w.seedStale("pod-a", "s1")
			rec := &fakeReclaimer{n: tc.n, err: tc.err}
			var logs bytes.Buffer
			clk := &fakeClock{now: t0.Add(terminalAge + time.Second)}
			r, err := slotreaper.New(slotreaper.Config{
				Pods: w, Members: w, Sessions: w, Releaser: w, Reservations: rec,
				Rate: 50, Interval: time.Minute, TerminalAge: terminalAge,
				Limiter: &countingLimiter{}, Now: clk.Now,
				Logger: slog.New(slog.NewTextHandler(&logs, nil)),
			})
			if err != nil {
				t.Fatal(err)
			}
			st := r.Pass(context.Background())
			if st.Reservations != tc.wantN {
				t.Errorf("Reservations = %d, want %d", st.Reservations, tc.wantN)
			}
			if len(rec.cutoffs) != 1 || !rec.cutoffs[0].Equal(clk.Now().Add(-terminalAge)) {
				t.Errorf("cutoffs = %v, want one at now minus the terminal age", rec.cutoffs)
			}
			if st.Released != 1 {
				t.Errorf("Released = %d, want 1: the reclaim does not stop member releases", st.Released)
			}
			if tc.wantLog != "" && !strings.Contains(logs.String(), tc.wantLog) {
				t.Errorf("logs = %q, want %q", logs.String(), tc.wantLog)
			}
		})
	}
}

// spec: §12.4 (Redis failure behavior).
//
// A reaper with no reclaimer configured runs its member releases alone.
func TestReaperWithoutAReclaimer_spec_12_4(t *testing.T) {
	w := newWorld()
	r, err := slotreaper.New(slotreaper.Config{
		Pods: w, Members: w, Sessions: w, Releaser: w,
		Rate: 1, Interval: time.Minute, TerminalAge: terminalAge,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if st := r.Pass(context.Background()); st.Reservations != 0 {
		t.Errorf("Reservations = %d, want 0", st.Reservations)
	}
}
