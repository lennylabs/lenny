// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"log/slog"

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

// sessionScrubOutcome maps the per-slot teardown result to the §5.2/§4.7
// cleanup outcome the adapter reports. It derives the outcome from the same
// closeErr that sets ShutdownResponse.ExitedCleanly: a clean close (nil error,
// which includes the runtime finishing within the grace deadline) is
// `released`, and any close failure or grace-deadline overrun is `leaked` — the
// resource could not be reclaimed at the session release. The leaked outcome is
// the sole feeder of the gateway leak ledger, the persistent leaked count, and
// the drain chain, so a mis-classification here silently disables the whole
// leaked-pod liveness path. spec: §5.2 (per-slot cleanup outcome), §4.7
// (leaked determination). F-5.2.31.
func sessionScrubOutcome(closeErr error) gatewaycontrol.SessionScrubOutcome {
	if closeErr != nil {
		return gatewaycontrol.SessionScrubLeaked
	}
	return gatewaycontrol.SessionScrubReleased
}

// reportSessionScrub emits the §5.2 per-slot cleanup outcome to the gateway
// after a session release, keyed on the adapter's cached pod identity, the
// released session, and the released slot (empty on the base, non-concurrent
// recycle path). It is the sole production writer of sessions_served
// (IncrementSessionsServed) and the feeder of the leak ledger, so the
// maxSessionsPerPod retirement and the leaked-pod drain both depend on it.
//
// It is best-effort with respect to blocking the shutdown response: a nil
// reporter (the dev path with no gateway link) or a transport failure is logged
// and does not fail the release, because the gateway missing-report timeout and
// the recycle boundary are the backstops. It withholds the report when the
// cached podID is empty (a missing or misnamed Downward API POD_NAME env),
// because the gateway rejects an empty pod_id InvalidArgument, so reporting
// under an empty key would only add churn; the withheld report is logged so a
// broken pod-spec env is diagnosable.
// spec: §4.7 (ReportSessionScrub); §5.2 (maxSessionsPerPod). F-5.2.31.
func (s *Server) reportSessionScrub(ctx context.Context, sessionID, slotID string, closeErr error) {
	outcome := sessionScrubOutcome(closeErr)
	if s.SessionScrubReporter == nil {
		// The dev path has no gateway link; nothing to report through. The
		// recycle boundary and the missing-report timeout are the backstops.
		slog.Warn("adapter: no SessionScrubReporter wired; session scrub outcome not reported",
			"session", sessionID, "slot", slotID, "outcome", outcome)
		return
	}
	if s.podID == "" {
		// Fail-closed on a wiring gap: the gateway rejects an empty pod_id
		// InvalidArgument, so withhold the report rather than churn against an
		// unresolvable key. An absent env is a build defect, surfaced here.
		slog.Error("adapter: session scrub has no cached pod id; withholding report (POD_NAME env unset)",
			"session", sessionID, "slot", slotID, "outcome", outcome)
		return
	}
	if err := s.SessionScrubReporter.ReportSessionScrub(ctx, s.podID, sessionID, slotID, outcome); err != nil {
		// The gateway missing-report timeout and the next release's idempotent
		// re-report are the backstops; log and move on rather than fail the
		// release the caller has already completed.
		slog.Error("adapter: ReportSessionScrub failed",
			"pod", s.podID, "session", sessionID, "slot", slotID, "outcome", outcome, "err", err)
	}
}
