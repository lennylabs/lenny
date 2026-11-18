// SPDX-License-Identifier: MIT

package metering

import (
	"sync"

	"github.com/lennylabs/lenny/pkg/gateway/meteringproxy/sessionbudget"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
)

// SessionTracker reports the sessions a per-session tracker holds state
// for, such as the direct-mode token-usage anomaly detector.
type SessionTracker interface {
	TrackedSessions() []string
}

// Budgets is the metering proxy's side of seam.BudgetTracking and
// seam.BudgetResolutions: the
// per-session budget state the proxy holds in its budget enforcer, in its
// usage recorder's granted deltas, and in the direct-mode anomaly detector
// the recorder feeds. The session core forgets a settled session's state
// and sweeps the sessions it tracks through it, and the platform tools seam
// raises or ends a session's budget through it when a budget extension
// resolves.
//
// The composition root builds Budgets with the enforcer, before the session
// core that calls it, and attaches the recorder and the detector when it
// builds the proxy. Every operation reads them at call time, so a call that
// arrives before they are attached reaches the enforcer alone. Every method
// is safe for concurrent use.
type Budgets struct {
	enforcer *sessionbudget.Enforcer

	mu       sync.RWMutex
	recorder *Recorder
	anomaly  SessionTracker
}

var (
	_ seam.BudgetTracking    = (*Budgets)(nil)
	_ seam.BudgetResolutions = (*Budgets)(nil)
)

// NewBudgets returns the budgets over the enforcer. A nil enforcer holds no
// budget state of its own.
func NewBudgets(enforcer *sessionbudget.Enforcer) *Budgets {
	return &Budgets{enforcer: enforcer}
}

// SetRecorder attaches the usage recorder. A nil recorder, which the
// composition root holds when no usage store is configured, leaves the
// enforcer to serve every operation.
func (b *Budgets) SetRecorder(r *Recorder) {
	b.mu.Lock()
	b.recorder = r
	b.mu.Unlock()
}

// SetAnomalyTracker attaches the direct-mode anomaly detector, whose
// sessions TrackedBudgetSessions reports. The recorder forgets the
// detector's state for a session in ForgetBudget.
func (b *Budgets) SetAnomalyTracker(t SessionTracker) {
	b.mu.Lock()
	b.anomaly = t
	b.mu.Unlock()
}

func (b *Budgets) attached() (*Recorder, SessionTracker) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.recorder, b.anomaly
}

// ForgetBudget is GWSEAM-ForgetBudget: it drops a settled session's
// enforcer counter, the recorder's granted delta, and, through the
// recorder, the anomaly detector's accumulator.
// spec: GWSEAM-ForgetBudget
func (b *Budgets) ForgetBudget(sessionID string) {
	if b.enforcer != nil {
		b.enforcer.Forget(sessionID)
	}
	rec, _ := b.attached()
	rec.Forget(sessionID)
}

// TrackedBudgetSessions is GWSEAM-TrackedBudgetSessions: the distinct ids of
// the sessions the enforcer, the recorder's granted deltas, and the anomaly
// detector hold state for, in no particular order.
// spec: GWSEAM-TrackedBudgetSessions
func (b *Budgets) TrackedBudgetSessions() []string {
	rec, anomaly := b.attached()
	var sets [][]string
	if b.enforcer != nil {
		sets = append(sets, b.enforcer.TrackedSessions())
	}
	sets = append(sets, rec.TrackedSessions())
	if anomaly != nil {
		sets = append(sets, anomaly.TrackedSessions())
	}
	seen := map[string]bool{}
	var out []string
	for _, set := range sets {
		for _, id := range set {
			if id != "" && !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}

// RaiseBudget is GWSEAM-RaiseBudget. With a recorder attached the recorder
// raises the enforcer's budget and accumulates the granted delta, so the
// raise survives the recorder's next settlement; without one the enforcer
// alone raises it.
// spec: GWSEAM-RaiseBudget
func (b *Budgets) RaiseBudget(sessionID string, delta int64) {
	if rec, _ := b.attached(); rec != nil {
		rec.RaiseBudget(sessionID, delta)
		return
	}
	if b.enforcer != nil {
		b.enforcer.RaiseBudget(sessionID, delta)
	}
}

// TerminateForBudget is GWSEAM-TerminateForBudget: it ends a session whose
// budget extension was refused. With a recorder attached the recorder also
// drops the session's granted delta.
// spec: GWSEAM-TerminateForBudget
func (b *Budgets) TerminateForBudget(sessionID string) {
	if rec, _ := b.attached(); rec != nil {
		rec.TerminateSession(sessionID)
		return
	}
	if b.enforcer != nil {
		b.enforcer.TerminateSession(sessionID)
	}
}
