// SPDX-License-Identifier: MIT

package gatewaycontrol

import (
	"context"
	"fmt"

	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// SessionScrubOutcome is the §5.2 result of the per-slot cleanup the
// adapter runs on every session release, lifted from the proto enum so the
// adapter reports a typed value rather than passing a proto constant.
// spec: §5.2 scrub model.
type SessionScrubOutcome int

const (
	// SessionScrubUnspecified is the zero value; a report should never
	// carry it. It maps to the proto unspecified, which the gateway
	// rejects fail-closed.
	SessionScrubUnspecified SessionScrubOutcome = iota
	// SessionScrubReleased — the slot's runtime, credential timers, and
	// per-slot directory tree were torn down cleanly and the slot was
	// released.
	SessionScrubReleased
	// SessionScrubLeaked — a resource could not be reclaimed at the
	// session release. The gateway feeds the outcome into the
	// unhealthy-threshold ledger.
	SessionScrubLeaked
)

func (o SessionScrubOutcome) proto() adapterv1.SessionScrubOutcome {
	switch o {
	case SessionScrubReleased:
		return adapterv1.SessionScrubOutcome_SESSION_SCRUB_OUTCOME_RELEASED
	case SessionScrubLeaked:
		return adapterv1.SessionScrubOutcome_SESSION_SCRUB_OUTCOME_LEAKED
	default:
		return adapterv1.SessionScrubOutcome_SESSION_SCRUB_OUTCOME_UNSPECIFIED
	}
}

// PodScrubOutcome is the binary §5.2 result of the whole-pod scrub the
// adapter runs at the occupancy-zero recycle boundary, lifted from the
// proto enum. spec: §5.2 scrub model; §3.4 recycle disposition.
type PodScrubOutcome int

const (
	// PodScrubUnspecified is the zero value; a report should never carry
	// it.
	PodScrubUnspecified PodScrubOutcome = iota
	// PodScrubSucceeded — the §5.2 whole-pod scrub sequence completed.
	PodScrubSucceeded
	// PodScrubFailed — the whole-pod scrub did not complete. The gateway
	// increments scrubFailureCount and computes the recycle disposition.
	PodScrubFailed
)

func (o PodScrubOutcome) proto() adapterv1.PodScrubOutcome {
	switch o {
	case PodScrubSucceeded:
		return adapterv1.PodScrubOutcome_POD_SCRUB_OUTCOME_SUCCEEDED
	case PodScrubFailed:
		return adapterv1.PodScrubOutcome_POD_SCRUB_OUTCOME_FAILED
	default:
		return adapterv1.PodScrubOutcome_POD_SCRUB_OUTCOME_UNSPECIFIED
	}
}

// ReportSessionScrub reports the §5.2 per-slot cleanup outcome to the
// gateway on every session release (the `maxConcurrentSessions > 1` and
// recycling cases alike). podID is the agent_pod_state row key, sessionID
// the released session, and slotID the released slot when the pod runs
// concurrent sessions (`maxConcurrentSessions > 1`); slotID is empty
// otherwise. The gateway increments sessionsServed on the pod's row and
// feeds a leaked outcome into the unhealthy-threshold ledger. A transport
// or gateway failure is returned as a wrapped error.
// spec: §4.7 (Adapter → Gateway RPCs); §5.2.
func (c *Client) ReportSessionScrub(ctx context.Context, podID, sessionID, slotID string, outcome SessionScrubOutcome) error {
	req := &adapterv1.ReportSessionScrubRequest{
		PodId:     podID,
		SessionId: &adapterv1.SessionId{Value: sessionID},
		Outcome:   outcome.proto(),
	}
	if slotID != "" {
		req.SlotId = &adapterv1.SlotId{Value: slotID}
	}
	if _, err := c.rpc.ReportSessionScrub(ctx, req); err != nil {
		return fmt.Errorf("gatewaycontrol: ReportSessionScrub for pod %s session %s: %w", podID, sessionID, err)
	}
	return nil
}

// ReportPodScrub reports the binary §5.2 whole-pod scrub outcome to the
// gateway when occupancy reaches zero on a recycling pod and the gateway
// has patched the pod's SandboxClaim to recycling. podID is the
// agent_pod_state row key the gateway resolves the claim from; there is
// no session because occupancy is zero at the recycle boundary. detail
// carries an optional failure description for the audit trail on a failed
// outcome. A transport or gateway failure is returned as a wrapped error.
// spec: §4.7 (Adapter → Gateway RPCs); §5.2; §3.4.
func (c *Client) ReportPodScrub(ctx context.Context, podID string, outcome PodScrubOutcome, detail string) error {
	req := &adapterv1.ReportPodScrubRequest{
		PodId:   podID,
		Outcome: outcome.proto(),
		Detail:  detail,
	}
	if _, err := c.rpc.ReportPodScrub(ctx, req); err != nil {
		return fmt.Errorf("gatewaycontrol: ReportPodScrub for pod %s: %w", podID, err)
	}
	return nil
}
