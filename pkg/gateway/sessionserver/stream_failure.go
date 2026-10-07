// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"log/slog"
	"time"

	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// DefaultStreamFailureReportTimeout is the default for
// Options.StreamFailureReportTimeout: the time a CH-ATTACH stream-failure
// report has, beyond the workspace seal window, for the binding release, the
// parent notification, and the cascade. It is not fixed by the spec and is
// operator-tunable through the gateway's --stream-failure-report-timeout flag.
const DefaultStreamFailureReportTimeout = 30 * time.Second

// streamEvicter is the executor seam that drops a session's cached Attach
// stream without touching the pod. *executor.PodExecutor satisfies it.
type streamEvicter interface {
	EvictStream(sessionID string)
}

// ReportAttachStreamFailure reports a CH-ATTACH stream that ended with a
// stream-failure status as the session's runtime_crash. The pod executor
// calls it on the stream's reader goroutine, after the ended stream has left
// its cache and before a delivery waiting on that stream returns.
//
// The report runs on a context detached from any request, bounded by the
// report timeout plus the seal window, so a failed edge whose seal never
// returns still leaves time for the release and the cascade. It is dropped
// when the registry no longer names the stream's sandbox for the session, so
// the end of a superseded binding cannot act on a newer one. It is also
// dropped when a peer has taken the session over (reportSupersededByPeer).
// A failed report is logged and not retried: the next delivery re-detects
// the failure, and the session watchdog bounds an idle session.
//
// spec: §28.5.1 (CH-ATTACH Degradation.); §7.3 (resume flow after pod
// failure); §10.1.1; §10.1.5.
func (s *Server) ReportAttachStreamFailure(tenantID, sessionID, sandboxName string, cause error) {
	ctx, cancel := context.WithTimeout(context.Background(), s.streamFailureReportTimeout+s.sealMaxDuration)
	defer cancel()
	log := slog.With("tenant_id", tenantID, "session_id", sessionID, "sandbox", sandboxName,
		"status_code", status.Code(cause).String())

	bind := s.reportedStreamBinding(sessionID, sandboxName)
	if bind == nil {
		log.Info("sessionserver: attach stream failure belongs to a superseded binding; not reported")
		return
	}
	row, err := s.store.Get(ctx, tenantID, sessionID)
	if err != nil {
		log.Warn("sessionserver: read session for attach stream failure; not reported", "error", err)
		return
	}
	if reportSupersededByPeer(row, bind) {
		s.evictStaleBinding(sessionID, bind)
		log.Warn("sessionserver: attach stream failure on a binding a peer has taken over; evicted without a drain",
			"binding_generation", bind.CoordinationGeneration, "row_generation", row.CoordinationGeneration)
		return
	}
	disp, err := s.ReportSessionFailure(ctx, FailureReport{
		TenantID:      tenantID,
		SessionID:     sessionID,
		Reason:        string(session.FailureRuntimeCrash),
		PodAssignment: sandboxName,
	})
	if err != nil {
		log.Warn("sessionserver: report attach stream failure", "error", err)
		return
	}
	log.Info("sessionserver: reported attach stream failure",
		"classification", disp.Classification.String(), "from", string(disp.From), "to", string(disp.To),
		"retry_count", disp.RetryCount)
}

// reportedStreamBinding returns the session's current binding when it names
// sandboxName, and nil when the server has no registry, the session is
// unbound, or the binding names another sandbox.
func (s *Server) reportedStreamBinding(sessionID, sandboxName string) *podsession.BindResult {
	if s.podRegistry == nil {
		return nil
	}
	bind, ok := s.podRegistry.Get(sessionID)
	if !ok || bind.SandboxName != sandboxName {
		return nil
	}
	return bind
}

// reportSupersededByPeer reports whether a peer replica has taken the session
// over since this replica published bind. RecordHandoff bumps the row's
// coordination_generation on every takeover, so a row generation above the
// binding's means another replica coordinates the session; a zero binding
// generation counts as superseded, so a publish site that never stamped its
// binding fails closed.
//
// Only the states ReportSessionFailure acts on are checked. In any other
// state the report is a no-op there, and the binding stays: handleResume
// bumps the generation on its failure exit after it may have published a
// binding, and leaves the row in awaiting_client_action or failed, where that
// binding waits for the next resume's release or the terminal release.
//
// spec: §10.1.1 (one coordinating replica per session); §10.1.5 (a replica
// that is no longer the coordinator stops its RPCs).
func reportSupersededByPeer(row sessionstore.Session, bind *podsession.BindResult) bool {
	switch row.State {
	case session.StateRunning, session.StateInputRequired, session.StateSuspended, session.StateResuming:
		return bind.CoordinationGeneration == 0 || row.CoordinationGeneration > bind.CoordinationGeneration
	default:
		return false
	}
}

// evictStaleBinding drops a binding a peer has taken over, as the
// coordination Sweeper's EvictBinding does: it removes the registry entry and
// evicts the cached stream, which also ends any stream a concurrent delivery
// reopened over the binding. It runs only while the registry still returns
// bind, so a binding published since is left alone. It makes no binder call,
// so the pod the new coordinator serves is not drained, and it writes no row,
// lease, or generation, because this replica does not hold the lease.
// spec: §10.1.5.
func (s *Server) evictStaleBinding(sessionID string, bind *podsession.BindResult) {
	if cur, ok := s.podRegistry.Get(sessionID); !ok || cur != bind {
		return
	}
	s.podRegistry.Remove(sessionID)
	if se, ok := s.executor.(streamEvicter); ok {
		se.EvictStream(sessionID)
	}
}
