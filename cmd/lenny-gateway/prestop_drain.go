// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/coordination/barrier"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/prestop"
)

// barrierCoordinatorDispatch adapts a *barrier.Coordinator to the
// prestop.BarrierDispatcher interface. It surfaces the pass's per-session
// Outcome set (not just the error) so the preStop hook can skip every
// barrier-acked session in its post-barrier per-session loop: under the
// §10.1.8 quiesce-and-hold contract the barrier already drives a
// full gateway-side checkpoint for every session it acks, so a loop that
// re-checkpointed those sessions would open a second manifest row and
// duplicate catalog rows for one (session, coordination_generation).
// spec: §10.1.
type barrierCoordinatorDispatch struct{ c *barrier.Coordinator }

func (d barrierCoordinatorDispatch) Dispatch(ctx context.Context) ([]barrier.Outcome, error) {
	sum, err := d.c.Dispatch(ctx)
	return sum.Outcomes, err
}

// parseTerminationGrace returns the §4.4 termination grace
// period the preStop hook uses to bound the staged drain. It reads
// LENNY_TERMINATION_GRACE_SECONDS first; the chart-default 240s
// applies when the env is unset or invalid.
//
// spec: §17.8.2 — terminationGracePeriodSeconds: 240 default.
func parseTerminationGrace() time.Duration {
	seconds := envInt("LENNY_TERMINATION_GRACE_SECONDS", prestop.DefaultTerminationGraceSeconds)
	if seconds <= 0 {
		seconds = prestop.DefaultTerminationGraceSeconds
	}
	return time.Duration(seconds) * time.Second
}
