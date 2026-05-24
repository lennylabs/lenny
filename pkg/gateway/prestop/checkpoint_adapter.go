// SPDX-License-Identifier: MIT

package prestop

import (
	"context"
	"time"
)

// CheckpointTrigger is the per-session checkpoint dispatcher the
// gateway wires into the preStop hook. The implementation invokes
// the in-process checkpointer to take an eviction checkpoint under
// the supplied budget.
type CheckpointTrigger interface {
	Checkpoint(ctx context.Context, tenantID, sessionID string) error
}

// CheckpointFnFor adapts a CheckpointTrigger (typically
// *checkpointer.Checkpointer) to the CheckpointFn signature the
// Hook expects. The budget is wrapped around the inner Checkpoint
// call via context.WithTimeout so a stuck adapter does not exceed
// the cap.
func CheckpointFnFor(trigger CheckpointTrigger) CheckpointFn {
	return func(ctx context.Context, tenantID, sessionID string, budget time.Duration) error {
		if trigger == nil {
			return nil
		}
		callCtx, cancel := context.WithTimeout(ctx, budget)
		defer cancel()
		return trigger.Checkpoint(callCtx, tenantID, sessionID)
	}
}
