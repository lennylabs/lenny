// SPDX-License-Identifier: MIT

package main

import (
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/core/session/budgetstate"
	"github.com/lennylabs/lenny/pkg/gateway/core/session/slotreaper"
)

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
