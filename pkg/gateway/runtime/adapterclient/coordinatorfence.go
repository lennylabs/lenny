// SPDX-License-Identifier: MIT

package adapterclient

import (
	"context"
	"time"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// CoordinatorFenceTimeout is the §11.3 line 209 hard-coded per-call
// timeout the gateway bounds every CoordinatorFence RPC by. The fence
// is a fast control-plane handshake (the adapter records the generation
// and returns); a pod that does not answer within the budget is treated
// as a failed fence so the coordinator can retry or relinquish rather
// than block the resume path indefinitely.
//
// spec: §11.3 line 209 — "CoordinatorFence RPC: 5s hard-coded timeout".
const CoordinatorFenceTimeout = 5 * time.Second

// CoordinatorFenceResult mirrors the §10.1 / §4.7 line 632
// CoordinatorFenceResponse: whether the pod accepted the announced
// coordination_generation, the pod's last fenced generation (so a
// rejecting caller knows how far ahead the pod is), and whether the
// fence skipped one or more generations (the §10.1 line 36 gap path).
type CoordinatorFenceResult struct {
	Accepted             bool
	LastFencedGeneration int64
	GapDetected          bool
}

// CoordinatorFence announces a new coordination_generation to the pod's
// adapter (§4.7 line 632 / §10.1 lines 33-37). The pod records the
// generation and, from that point, rejects any RPC carrying a strictly
// older generation with FailedPrecondition + a `coordinator_handoff_stale`
// detail. The first fence on a pod's lifetime is always accepted; a
// later fence whose generation is not strictly greater than the pod's
// last fenced value returns codes.FailedPrecondition, which the caller
// records as a generation-stale handoff and which drives the §11.3
// retry/relinquish decision.
//
// The call is bounded by the §11.3 line 209 hard-coded
// CoordinatorFenceTimeout; the caller's ctx still applies, so an earlier
// caller deadline wins.
//
// spec: §10.1 lines 33-37, §11.3 line 209.
func (c *Client) CoordinatorFence(ctx context.Context, sessionID string, coordinationGeneration int64) (CoordinatorFenceResult, error) {
	ctx, cancel := context.WithTimeout(ctx, CoordinatorFenceTimeout)
	defer cancel()
	resp, err := c.rpc.CoordinatorFence(ctx, &adapterv1.CoordinatorFenceRequest{
		SessionId:              &adapterv1.SessionId{Value: sessionID},
		CoordinationGeneration: coordinationGeneration,
	})
	if err != nil {
		return CoordinatorFenceResult{}, err
	}
	return CoordinatorFenceResult{
		Accepted:             resp.GetAccepted(),
		LastFencedGeneration: resp.GetLastFencedGeneration(),
		GapDetected:          resp.GetGapDetected(),
	}, nil
}
