// SPDX-License-Identifier: MIT

package adapter

import (
	"context"

	"github.com/lennylabs/lenny/pkg/adapter/gatewaycontrol"
)

// SessionScrubReporter emits the §5.2 per-slot cleanup outcome to the gateway
// on every session release. The slot-release and base-recycle paths report
// through it after the per-slot teardown, keyed on the cached pod identity, the
// released session, and the released slot (empty for a base, non-concurrent
// pod). The gateway advances sessions_served on the pod's row (feeding the
// maxSessionsPerPod retirement) and feeds a leaked outcome into the
// unhealthy-threshold ledger. It is a small consumer-side seam so the release
// path stays testable without a live gateway. *gatewaycontrol.Client satisfies
// it, and ConnectGateway wires the dialed client onto the Server.
// spec: §4.7 (ReportSessionScrub); §5.2 (maxSessionsPerPod). F-5.2.31.
type SessionScrubReporter interface {
	// ReportSessionScrub reports the per-slot cleanup outcome for the released
	// session. slotID names the released slot on a concurrent
	// (maxConcurrentSessions > 1) pod and is empty otherwise. A transport or
	// gateway failure is returned as a wrapped error.
	ReportSessionScrub(ctx context.Context, podID, sessionID, slotID string, outcome gatewaycontrol.SessionScrubOutcome) error
}
