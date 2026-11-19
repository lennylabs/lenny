// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/core/session/budgetstate"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/slotreaper"
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
		Budgets:   w.meteringBudgets,
		Interval:  interval,
		BatchSize: slotreaper.DefaultBatchSize,
	})
}
