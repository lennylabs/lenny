// SPDX-License-Identifier: MIT

package metering

import (
	"context"
	"sort"
	"sync"
	"testing"

	"github.com/lennylabs/lenny/pkg/billing/usagestore"
	"github.com/lennylabs/lenny/pkg/gateway/meteringproxy/sessionbudget"
	"github.com/lennylabs/lenny/pkg/session/sessionstore/memstore"
)

// expiryRecorder records the sessions the enforcer expires.
type expiryRecorder struct {
	mu  sync.Mutex
	ids []string
}

func (e *expiryRecorder) ExpireSession(sessionID, _ string) {
	e.mu.Lock()
	e.ids = append(e.ids, sessionID)
	e.mu.Unlock()
}

func (e *expiryRecorder) expired() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.ids...)
}

// trackerStub reports a fixed set of tracked sessions.
type trackerStub []string

func (t trackerStub) TrackedSessions() []string { return t }

func sorted(ids []string) []string {
	out := append([]string(nil), ids...)
	sort.Strings(out)
	return out
}

func sameIDs(got, want []string) bool {
	got, want = sorted(got), sorted(want)
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// exhaust records enough tokens against a budget of 10 to exhaust the
// session on the enforcer, which then tracks it and denies its next request.
func exhaust(enf *sessionbudget.Enforcer, sessionID string) {
	ctx := context.Background()
	enf.Record(ctx, ctx, "acme", sessionID, 10, 10)
}

// TestBudgetsTrackTheUnionOfEnforcerRecorderAndAnomalySessions checks that
// TrackedBudgetSessions reads the enforcer alone before the recorder and the
// detector are attached, and the distinct union of all three after.
// spec: GWSEAM-TrackedBudgetSessions
func TestBudgetsTrackTheUnionOfEnforcerRecorderAndAnomalySessions(t *testing.T) {
	enf := sessionbudget.New(&expiryRecorder{}, nil, nil)
	b := NewBudgets(enf)
	if got := b.TrackedBudgetSessions(); len(got) != 0 {
		t.Fatalf("tracked with nothing recorded = %v, want none", got)
	}
	exhaust(enf, "s_enf")
	if got := b.TrackedBudgetSessions(); !sameIDs(got, []string{"s_enf"}) {
		t.Fatalf("tracked before attaching = %v, want [s_enf]", got)
	}

	rec := NewRecorder(usagestore.NewMemory(), memstore.New(), nil, nil, nil, enf)
	b.SetRecorder(rec)
	b.SetAnomalyTracker(trackerStub{"s_anom", "s_enf", ""})
	rec.RaiseBudget("s_rec", 5)
	if got := b.TrackedBudgetSessions(); !sameIDs(got, []string{"s_enf", "s_rec", "s_anom"}) {
		t.Fatalf("tracked after attaching = %v, want the distinct union [s_anom s_enf s_rec]", got)
	}
}

// TestBudgetsForgetDropsTheEnforcerAndRecorderState checks that ForgetBudget
// drops a session's enforcer counter and the recorder's granted delta, and
// that it reaches the enforcer alone when no recorder is attached.
// spec: GWSEAM-ForgetBudget
func TestBudgetsForgetDropsTheEnforcerAndRecorderState(t *testing.T) {
	enf := sessionbudget.New(&expiryRecorder{}, nil, nil)
	b := NewBudgets(enf)
	exhaust(enf, "s_1")
	b.ForgetBudget("s_1")
	if got := enf.TrackedSessions(); len(got) != 0 {
		t.Fatalf("enforcer tracks %v after ForgetBudget with no recorder, want none", got)
	}

	rec := NewRecorder(usagestore.NewMemory(), memstore.New(), nil, nil, nil, enf)
	anomaly := &recordAnomalyObserver{}
	rec.SetAnomalyObserver(anomaly)
	b.SetRecorder(rec)
	exhaust(enf, "s_2")
	rec.RaiseBudget("s_2", 5)
	b.ForgetBudget("s_2")
	if got := b.TrackedBudgetSessions(); len(got) != 0 {
		t.Fatalf("tracked after ForgetBudget = %v, want none", got)
	}
	if len(anomaly.forgotten) != 1 || anomaly.forgotten[0] != "s_2" {
		t.Fatalf("anomaly detector forgot %v, want [s_2]", anomaly.forgotten)
	}
}

// TestBudgetsRaiseThroughTheRecorderWhenOneIsAttached checks that
// RaiseBudget reaches the recorder, which keeps the granted delta, when one
// is attached, and the enforcer alone otherwise; either way the session's
// deny state clears.
// spec: GWSEAM-RaiseBudget
func TestBudgetsRaiseThroughTheRecorderWhenOneIsAttached(t *testing.T) {
	enf := sessionbudget.New(&expiryRecorder{}, func(_, _ context.Context, _, _ string, _, _ int64) sessionbudget.Outcome {
		return sessionbudget.Pending
	}, nil)
	b := NewBudgets(enf)
	exhaust(enf, "s_1")
	if enf.Allow("s_1") {
		t.Fatal("an exhausted session with a pending extension is admitted")
	}
	b.RaiseBudget("s_1", 5)
	if !enf.Allow("s_1") {
		t.Fatal("RaiseBudget with no recorder did not clear the enforcer's deny state")
	}

	rec := NewRecorder(usagestore.NewMemory(), memstore.New(), nil, nil, nil, enf)
	b.SetRecorder(rec)
	exhaust(enf, "s_2")
	b.RaiseBudget("s_2", 7)
	if !enf.Allow("s_2") {
		t.Fatal("RaiseBudget through the recorder did not clear the enforcer's deny state")
	}
	if got := rec.grantedDeltaFor("s_2"); got != 7 {
		t.Fatalf("recorder granted delta = %d, want 7", got)
	}
}

// TestBudgetsTerminateExpiresTheSessionAndDropsItsGrant checks that
// TerminateForBudget expires the session through the enforcer's expiry,
// with or without a recorder, and that the recorder drops the session's
// granted delta.
// spec: GWSEAM-TerminateForBudget
func TestBudgetsTerminateExpiresTheSessionAndDropsItsGrant(t *testing.T) {
	expiry := &expiryRecorder{}
	enf := sessionbudget.New(expiry, nil, nil)
	b := NewBudgets(enf)
	b.TerminateForBudget("s_1")

	rec := NewRecorder(usagestore.NewMemory(), memstore.New(), nil, nil, nil, enf)
	b.SetRecorder(rec)
	rec.RaiseBudget("s_2", 3)
	b.TerminateForBudget("s_2")
	if got := expiry.expired(); !sameIDs(got, []string{"s_1", "s_2"}) {
		t.Fatalf("expired %v, want [s_1 s_2]", got)
	}
	if got := rec.grantedDeltaFor("s_2"); got != 0 {
		t.Fatalf("recorder kept a granted delta of %d after TerminateForBudget, want 0", got)
	}
}

// TestBudgetsWithoutAnEnforcerHoldNoState checks that budgets built over a
// nil enforcer serve every operation without one.
// spec: GWSEAM-ForgetBudget, GWSEAM-TrackedBudgetSessions, GWSEAM-RaiseBudget, GWSEAM-TerminateForBudget
func TestBudgetsWithoutAnEnforcerHoldNoState(t *testing.T) {
	b := NewBudgets(nil)
	b.ForgetBudget("s_1")
	b.RaiseBudget("s_1", 1)
	b.TerminateForBudget("s_1")
	if got := b.TrackedBudgetSessions(); len(got) != 0 {
		t.Fatalf("tracked = %v, want none", got)
	}
}

// TestBudgetsAttachConcurrentlyWithCalls checks, under the race detector,
// that attaching the recorder and the detector while the session core and
// the platform tools seam call the budgets is safe.
// spec: GWSEAM-ForgetBudget, GWSEAM-TrackedBudgetSessions, GWSEAM-RaiseBudget, GWSEAM-TerminateForBudget
func TestBudgetsAttachConcurrentlyWithCalls(t *testing.T) {
	enf := sessionbudget.New(&expiryRecorder{}, nil, nil)
	b := NewBudgets(enf)
	rec := NewRecorder(usagestore.NewMemory(), memstore.New(), nil, nil, nil, enf)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				b.RaiseBudget("s_1", 1)
				_ = b.TrackedBudgetSessions()
				b.ForgetBudget("s_1")
			}
		}()
	}
	b.SetRecorder(rec)
	b.SetAnomalyTracker(trackerStub{"s_2"})
	wg.Wait()
	b.TerminateForBudget("s_1")
}
