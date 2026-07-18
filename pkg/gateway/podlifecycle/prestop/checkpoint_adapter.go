// SPDX-License-Identifier: MIT

package prestop

import (
	"context"
	"time"

	"github.com/lennylabs/lenny/pkg/checkpoint"
)

// CheckpointTrigger is the per-session checkpoint dispatcher the
// gateway wires into the preStop hook. The implementation invokes
// the in-process checkpointer to take a checkpoint under the supplied
// trigger and budget. *checkpointer.Checkpointer satisfies it via
// CheckpointWithTrigger.
type CheckpointTrigger interface {
	CheckpointWithTrigger(ctx context.Context, tenantID, sessionID string, trigger checkpoint.Trigger) error
}

// CheckpointFnFor adapts a CheckpointTrigger (typically
// *checkpointer.Checkpointer) to the CheckpointFn signature the
// Hook expects. The per-session drain checkpoint is stamped
// checkpoint.TriggerEviction so the §10.1 line 172 finalisation and the
// §16.1 `lenny_checkpoint_partial_total{trigger="eviction"}` domain both
// see the eviction trigger on the post-barrier loop, symmetric with the
// barrier-window driver. The budget is wrapped around the inner call via
// context.WithTimeout so a stuck adapter does not exceed the cap.
//
// spec: §10.1 line 172 (the preStop drain driver stamps the eviction
// trigger on the Stage 2 tier-cap finalisations).
func CheckpointFnFor(trigger CheckpointTrigger) CheckpointFn {
	return func(ctx context.Context, tenantID, sessionID string, budget time.Duration) error {
		if trigger == nil {
			return nil
		}
		callCtx, cancel := context.WithTimeout(ctx, budget)
		defer cancel()
		return trigger.CheckpointWithTrigger(callCtx, tenantID, sessionID, checkpoint.TriggerEviction)
	}
}
