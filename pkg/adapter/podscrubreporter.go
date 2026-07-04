// SPDX-License-Identifier: MIT

package adapter

import (
	"context"

	"github.com/lennylabs/lenny/pkg/adapter/gatewaycontrol"
)

// PodScrubReporter emits the binary §5.2 whole-pod scrub outcome to the
// gateway on the recycle boundary. The adapter-side scrub driver reports
// through it after running the whole-pod scrub, keyed on the pod identity the
// recycle-trigger Shutdown carried. It is a small consumer-side seam so the
// driver stays testable without a live gateway. *gatewaycontrol.Client
// satisfies it, and ConnectGateway wires the dialed client onto the Server.
// spec: §4.7 (ReportPodScrub); §5.2 (whole-pod scrub). F-5.2.15.
type PodScrubReporter interface {
	// ReportPodScrub reports the whole-pod scrub outcome for podID. detail
	// carries an optional failure description for the audit trail on a failed
	// outcome. A transport or gateway failure is returned as a wrapped error.
	ReportPodScrub(ctx context.Context, podID string, outcome gatewaycontrol.PodScrubOutcome, detail string) error
}
