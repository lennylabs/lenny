// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/slotreaper"
	"github.com/lennylabs/lenny/pkg/gateway/session/budgetstate"
)

// validateBudgetStateSweepInterval refuses a non-positive
// --budget-state-sweep-interval, which would stop every replica from
// reclaiming the per-session state of sessions that ended elsewhere.
// spec: §11.2.
func validateBudgetStateSweepInterval(d time.Duration) error {
	if d <= 0 {
		return fmt.Errorf("--budget-state-sweep-interval must be positive, got %s (§11.2)", d)
	}
	return nil
}

// buildBudgetStateSweep constructs this replica's per-session budget state
// sweep over the session store, the union of the ids the enforcer, the
// usage recorder's granted-delta map, and the anomaly detector track, and
// the composed forget the terminal pipeline also calls. It resolves ids in
// slotreaper.DefaultBatchSize batches. startBackgroundWorkers runs it.
// spec: §11.2, §10.1.1, §8.6.
func (w *gatewayWiring) buildBudgetStateSweep(interval time.Duration) *budgetstate.Sweep {
	return budgetstate.New(budgetstate.Config{
		Sessions:  w.sessions,
		Tracked:   w.trackedBudgetSessions,
		Forget:    w.forgetBudgetState,
		Interval:  interval,
		BatchSize: slotreaper.DefaultBatchSize,
	})
}

// forgetBudgetState is the composed per-session budget forget: the §11.2
// enforcer counter, the §8.6 recorder's accumulated grant delta, and, through
// the recorder, the §11.2 direct-mode anomaly detector's accumulator. The
// terminal pipeline and the budget state sweep both call it. The recorder is
// constructed in a later build step than the session server, so
// w.proxyUsageRec is read at call time.
// spec: §11.2, §8.6.
func (w *gatewayWiring) forgetBudgetState(sessionID string) {
	w.sessionBudgetEnforcer.Forget(sessionID)
	w.proxyUsageRec.forget(sessionID)
}

// trackedBudgetSessions returns the union of the session ids the enforcer,
// the recorder's granted-delta map, and the anomaly detector hold. Each source
// is read at call time and is nil-safe, because the recorder and the detector
// are built after the session server.
// spec: §11.2, §10.1.1.
func (w *gatewayWiring) trackedBudgetSessions() []string {
	return budgetstate.Union(
		w.sessionBudgetEnforcer.TrackedSessions(),
		w.proxyUsageRec.TrackedSessions(),
		w.tokenAnomaly.TrackedSessions(),
	)
}
