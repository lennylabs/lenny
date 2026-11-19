// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/billing/usagestore"
	"github.com/lennylabs/lenny/pkg/gateway/meteringproxy/metering"
	"github.com/lennylabs/lenny/pkg/gateway/meteringproxy/sessionbudget"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/tokenanomaly"
)

type sweepNoopTerminator struct{}

func (sweepNoopTerminator) ExpireSession(string, string) {}

// spec: §11.2, §10.1.1, §8.6
//
// The wired sweep reads the union of the enforcer's counters, the usage
// recorder's granted deltas, and the anomaly detector's accumulators, and a
// pass forgets an ended session from all three through the composed forget
// while leaving a live session's state in place. Before the recorder and
// the detector are built, the tracked union is the enforcer's alone.
func TestBudgetStateSweepWiringForgetsAcrossTrackers_spec_11_2(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	ended, live := uuid.NewString(), uuid.NewString()
	for _, id := range []string{ended, live} {
		if err := store.Create(ctx, sessionstore.Session{ID: id, TenantID: "acme", State: session.StateRunning}); err != nil {
			t.Fatal(err)
		}
	}
	enf := sessionbudget.New(sweepNoopTerminator{}, nil, nil)
	w := &gatewayWiring{}
	w.sessions = store
	w.sessionBudgetEnforcer = enf
	w.meteringBudgets = metering.NewBudgets(enf)

	enf.Record(ctx, ctx, "acme", ended, 1000, 1)
	if got := w.meteringBudgets.TrackedBudgetSessions(); len(got) != 1 || got[0] != ended {
		t.Fatalf("tracked before the recorder is built = %v, want [%s]", got, ended)
	}

	det, err := tokenanomaly.New(prometheus.NewRegistry(), tokenanomaly.Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec := metering.NewRecorder(usagestore.NewMemory(), store, nil, nil, nil, enf)
	rec.SetAnomalyObserver(det)
	w.proxyUsageRec = rec
	w.meteringBudgets.SetRecorder(rec)
	w.meteringBudgets.SetAnomalyTracker(det)
	rec.RaiseBudget(ended, 10)
	det.Observe("acme", ended, 1, 1)
	enf.Record(ctx, ctx, "acme", live, 1000, 1)
	rec.RaiseBudget(live, 10)
	det.Observe("acme", live, 1, 1)

	want := []string{ended, live}
	sort.Strings(want)
	got := w.meteringBudgets.TrackedBudgetSessions()
	sort.Strings(got)
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("tracked = %v, want %v", got, want)
	}

	if _, err := store.Update(ctx, "acme", ended, func(r *sessionstore.Session) error {
		from := r.State
		r.State = session.StateCompleted
		sessionstore.MarkTerminated(r, from, time.Now().Add(-time.Minute))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sweep := w.buildBudgetStateSweep(time.Minute)
	if got := sweep.Pass(ctx); got != 1 {
		t.Fatalf("pass forgot %d, want 1", got)
	}
	for name, ids := range map[string][]string{
		"enforcer": enf.TrackedSessions(),
		"recorder": rec.TrackedSessions(),
		"detector": det.TrackedSessions(),
	} {
		for _, id := range ids {
			if id == ended {
				t.Fatalf("%s still tracks the ended session after the pass", name)
			}
		}
	}
	if !containsID(rec.TrackedSessions(), live) || !containsID(det.TrackedSessions(), live) || !containsID(enf.TrackedSessions(), live) {
		t.Fatalf("the live session lost state: enforcer=%v recorder=%v detector=%v",
			enf.TrackedSessions(), rec.TrackedSessions(), det.TrackedSessions())
	}
	var nilRec *metering.Recorder
	if got := nilRec.TrackedSessions(); got != nil {
		t.Fatalf("nil recorder TrackedSessions = %v, want nil", got)
	}
}

func containsID(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}
