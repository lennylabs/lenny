// SPDX-License-Identifier: MIT

package orphansession_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/orphansession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// mutatingMirror answers the terminated phase for pod-1 and, on its lookup,
// rewrites the listed row through mutate: the write lands after the
// reconciler listed the row and before its guarded Update.
type mutatingMirror struct {
	store  *memstore.Store
	id     string
	mutate func(*sessionstore.Session)
}

func (m *mutatingMirror) GetByPodID(ctx context.Context, podID string) (orphansession.MirrorPod, bool, error) {
	if m.mutate != nil {
		_, _ = m.store.Update(ctx, "acme", m.id, func(s *sessionstore.Session) error {
			m.mutate(s)
			return nil
		})
	}
	return orphansession.MirrorPod{PoolID: "pool-a", Phase: "terminated"}, true, nil
}

func (*mutatingMirror) MirrorLagSeconds(context.Context, string) (float64, error) { return 0, nil }

// TestFailOrphanLockedRowGuard_spec_10_1_4 pins the guard on the locked row:
// a listed suspended row whose suspension released the pod after the listing
// stays suspended with no counter or hook; a row another writer already
// failed is neither counted nor hooked; a row moved to a new pod is left; and
// a row that still names the terminated pod fails.
//
// spec: 6.2 (Pod State Machine), 10.1.4 (Coordinator-Loss Detection and Hold
// State)
func TestFailOrphanLockedRowGuard_spec_10_1_4(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(*sessionstore.Session)
		wantState session.State
		wantFail  bool
	}{
		{
			name:      "pod_released_after_listing",
			mutate:    func(s *sessionstore.Session) { s.PodAssignment = "" },
			wantState: session.StateSuspended,
		},
		{
			name: "already_failed_elsewhere",
			mutate: func(s *sessionstore.Session) {
				s.State = session.StateFailed
			},
			wantState: session.StateFailed,
		},
		{
			name:      "moved_to_another_pod",
			mutate:    func(s *sessionstore.Session) { s.PodAssignment = "pod-2" },
			wantState: session.StateSuspended,
		},
		{
			name:      "still_names_terminated_pod",
			wantState: session.StateFailed,
			wantFail:  true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := memstore.New()
			mkSession(t, store, "acme", "s1", session.StateSuspended, "pod-1", "pool-a")
			term := &fakeTerminal{}
			met := &fakeMetrics{}
			mirror := &mutatingMirror{store: store, id: "s1", mutate: tc.mutate}
			r := orphansession.New(store, fakeTenants{"acme"}, mirror, orphansession.Options{Terminal: term, Metrics: met})

			n, err := r.Tick(context.Background())
			if err != nil {
				t.Fatalf("Tick: %v", err)
			}
			want := 0
			if tc.wantFail {
				want = 1
			}
			if n != want || met.reconciliations != want || len(term.called) != want {
				t.Errorf("failed %d, counted %d, hooked %d; want %d each", n, met.reconciliations, len(term.called), want)
			}
			if got := getState(t, store, "acme", "s1").State; got != tc.wantState {
				t.Errorf("state = %q, want %q", got, tc.wantState)
			}
		})
	}
}
