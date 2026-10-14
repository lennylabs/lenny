// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"google.golang.org/grpc/status"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/checkpoint"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// SuspensionCheckpointer takes the checkpoint a suspension release takes
// before it releases a suspended session's pod. The gateway wires the
// checkpoint service.
// spec: §6.2 (Pod State Machine), §4.4.3 (Checkpoint Quiescence, Timeout,
// and Abort Handling)
type SuspensionCheckpointer interface {
	CheckpointWithTrigger(ctx context.Context, tenantID, sessionID string, trigger checkpoint.Trigger) error
}

// suspensionCheckpointTimeout is the §4.4.3 checkpoint timeout the
// suspension release bounds its checkpoint by.
// spec: §4.4.3 (Checkpoint Quiescence, Timeout, and Abort Handling)
const suspensionCheckpointTimeout = 60 * time.Second

// Structured event names of the suspension release.
// spec: §6.2 (Pod State Machine)
const (
	eventPodReleasedDuringSuspension = "session.pod_released_during_suspension"
	eventSuspensionCheckpointFailed  = "session.suspension_checkpoint_failed"
)

// errSuspensionReleaseSuperseded is the sentinel the suspension release's
// clear returns when the locked row is no longer the suspension it listed:
// it left `suspended`, re-entered it with a new SuspendedAt, or names
// another pod. The write changes nothing and the release stops.
var errSuspensionReleaseSuperseded = errors.New("sessionserver: suspension release superseded on the locked row")

// errIdleWriteNotApplied is the sentinel the idle suspension's write returns
// for every outcome but idleWriteApply. The write changes nothing.
var errIdleWriteNotApplied = errors.New("sessionserver: idle suspension write not applied")

// idleWriteRetryBackoff spaces the repeats of an idle suspension write whose
// outcome a store error left unknown.
const idleWriteRetryBackoff = 250 * time.Millisecond

// ReleaseSuspendedPod is the watchdog's SuspendedPodReleaser hook: it
// releases the pod of an interrupt suspension whose hold window ended. It
// returns at once. It starts nothing when a pod binder is wired and this
// replica holds no binding for the session, when this replica does not hold
// the session's coordination lease or the lease read fails, when
// this replica holds an open terminal claim for the session, when a release
// or an idle suspension of the session is in flight on this replica, or
// within 60 s of the session's last failed suspension checkpoint.
//
// Otherwise it runs, off the caller and on the server-lifetime context: the
// checkpoint, the guarded clear of the pod from the row, the inbox
// migration, one backlog check, the release under slotAccountLocks, and the
// release event. It takes no delivery lock and makes no seal call.
// Invariant: a session row never names a released pod. A stop between the
// clear and the release leaves a podless row whose binding this replica
// still holds until a later resume attempt or the terminal binding release
// releases it; on an exclusive pool the orphan-claim GC can reclaim the claim
// first.
//
// spec: §6.2 (Pod State Machine), §10.1.4 (Coordinator-Loss Detection and
// Hold State)
func (s *Server) ReleaseSuspendedPod(ctx context.Context, row sessionstore.Session) {
	bind, ok := s.suspensionBinding(row)
	if !ok {
		return
	}
	if !s.coordinatesSession(ctx, row.TenantID, row.ID) {
		return
	}
	if !s.beginSuspension(row.ID) {
		return
	}
	go func() {
		defer s.suspensions.end(row.ID)
		s.runSuspensionRelease(s.lifetime(), row, bind)
	}()
}

// suspensionBinding returns the binding the suspension release acts on: the
// registry's binding when a pod binder is wired, which must exist, and
// otherwise whatever the registry holds, possibly nil. ok is false when the
// release starts nothing.
func (s *Server) suspensionBinding(row sessionstore.Session) (*podsession.BindResult, bool) {
	bind := s.snapshotBinding(row.ID)
	if s.podBinder != nil && bind == nil {
		return nil, false
	}
	return bind, true
}

// beginSuspension tests, in order, this replica's terminal claims, the
// suspension record, and the retry record, and inserts the suspension entry
// when all three admit the session. A caller that gets true removes the entry
// with s.suspensions.end on every return of the work it starts.
// spec: §6.2 (Pod State Machine)
func (s *Server) beginSuspension(sessionID string) bool {
	if s.terminalClaims.claimed(sessionID) {
		return false
	}
	if !s.suspensions.begin(sessionID) {
		return false
	}
	if s.suspensionRetries.waiting(sessionID, s.clock()) {
		s.suspensions.end(sessionID)
		return false
	}
	return true
}

// runSuspensionRelease runs the steps of an interrupt suspension's release
// after the gates admitted it.
func (s *Server) runSuspensionRelease(ctx context.Context, row sessionstore.Session, bind *podsession.BindResult) {
	if !s.checkpointForSuspension(ctx, row) {
		return
	}
	cleared, ok := s.clearSuspendedPod(ctx, row)
	if !ok {
		return
	}
	s.releaseSuspendedBinding(ctx, cleared, bind)
}

// checkpointForSuspension takes the checkpoint the release needs, bounded
// by the §4.4.3 60 s timeout. On a failure, including that deadline, it
// emits the suspension_checkpoint_failed event, increments its counter, and
// records the retry time, and the caller releases nothing.
// spec: §6.2 (Pod State Machine), §4.4.3 (Checkpoint Quiescence, Timeout,
// and Abort Handling)
func (s *Server) checkpointForSuspension(ctx context.Context, row sessionstore.Session) bool {
	if s.suspensionCheckpointer == nil {
		slog.Info("sessionserver: no checkpointer is wired; the suspended pod stays held", "session_id", row.ID)
		return false
	}
	cctx, cancel := context.WithTimeout(ctx, suspensionCheckpointTimeout)
	defer cancel()
	err := s.suspensionCheckpointer.CheckpointWithTrigger(cctx, row.TenantID, row.ID, checkpoint.TriggerPeriodic)
	if err == nil {
		return true
	}
	slog.Warn(eventSuspensionCheckpointFailed, "session_id", row.ID, "tenant_id", row.TenantID, "pool", row.PoolRef, "error", err)
	if s.incSuspensionCheckpointFailed != nil {
		s.incSuspensionCheckpointFailed(row.PoolRef, row.TenantID)
	}
	s.suspensionRetries.recordFailure(row.ID, s.clock())
	return false
}

// clearSuspendedPod clears the pod from the row with one guarded write, on
// the row still being the listed suspension (same SuspendedAt) naming the
// same pod. A guard miss or a store error stops the release, and the write
// is not repeated. It returns the stored podless row.
// spec: §6.2 (Pod State Machine)
func (s *Server) clearSuspendedPod(ctx context.Context, row sessionstore.Session) (sessionstore.Session, bool) {
	updated, err := s.store.Update(ctx, row.TenantID, row.ID, func(r *sessionstore.Session) error {
		if r.State != session.StateSuspended || !r.SuspendedAt.Equal(row.SuspendedAt) || r.PodAssignment != row.PodAssignment {
			return errSuspensionReleaseSuperseded
		}
		r.PodAssignment = ""
		return nil
	})
	if err != nil {
		if !errors.Is(err, errSuspensionReleaseSuperseded) {
			slog.Warn("sessionserver: clear the pod of a suspended session", "session_id", row.ID, "error", err)
		}
		return sessionstore.Session{}, false
	}
	return updated, true
}

// releaseSuspendedBinding runs the release tail over the stored podless
// suspended row and the binding the release read: the inbox migration and a
// terminal re-read, one backlog check, the release under slotAccountLocks
// with the pool's session-end disposition, the elicitation expiry, and the
// release event. The suspension release and the idle suspension share it.
//
// Step 4 releases only bind, through releaseReportedBinding's identity
// check, so it never releases a binding or a lease a resume attempt created:
// releaseEarlierBinding runs under the same lock before every attempt's
// claim, a restore claims a pod other than the released one, and the
// credential release is scoped by the binding's coordination generation.
//
// spec: §6.2 (Pod State Machine), §7.2 (Message delivery routing — seven
// paths, path 6), §9.2 (Elicitation Chain)
func (s *Server) releaseSuspendedBinding(ctx context.Context, row sessionstore.Session, bind *podsession.BindResult) {
	s.migrateInboxOnResumePending(ctx, row)
	terminal := false
	if reread, err := s.readPrimary(ctx, row.TenantID, row.ID); err != nil {
		slog.Warn("sessionserver: re-read a suspended session after clearing its pod", "session_id", row.ID, "error", err)
	} else if session.IsTerminal(reread.State) {
		// spec: §7.2 — a terminal write after the clear drains what the
		// migration moved, so no held message outlives the session.
		s.drainMessagingOnTerminal(ctx, reread)
		terminal = true
	}
	s.checkBacklogOnce(ctx, row)
	released, ok := s.releaseUnderSlotLock(ctx, row, bind, terminal)
	if !ok || !released {
		return
	}
	slog.Info(eventPodReleasedDuringSuspension, "session_id", row.ID, "tenant_id", row.TenantID, "pool", row.PoolRef)
	if s.incPodReleasedDuringSuspension != nil {
		s.incPodReleasedDuringSuspension(row.PoolRef, row.TenantID)
	}
}

// releaseUnderSlotLock is step 4 of the release. It takes slotAccountLocks,
// honoring ctx, and stops (ok false) when the wait ends with ctx, when this
// replica holds an open terminal claim for the session, or when the re-read
// found the row terminal: the terminal binding release or the writer's hook
// then releases the binding with the terminal disposition. Otherwise it
// releases bind with the completed disposition and, before it releases the
// lock, expires the elicitations the session raised, so the runtime on the
// released pod cannot raise again after the expiry and no resume attempt has
// claimed a pod yet.
// spec: §6.2 (Pod State Machine), §9.2 (Elicitation Chain)
func (s *Server) releaseUnderSlotLock(ctx context.Context, row sessionstore.Session, bind *podsession.BindResult, terminal bool) (released, ok bool) {
	unlock, err := s.slotAccountLocks.Lock(ctx, row.ID)
	if err != nil {
		return false, false
	}
	defer unlock()
	if terminal || s.terminalClaims.claimed(row.ID) {
		return false, false
	}
	released = s.releaseReportedBinding(ctx, row.ID, bind, executor.DispositionCompleted)
	s.expireRaisedElicitations(ctx, row)
	return released, true
}

// checkBacklogOnce evaluates §7.2 delivery path 6 Backlog resume. once for a
// row the release left podless: when a message is held for the session, it
// moves the row to resume_pending, and the resume driver restores it. Either
// error is logged and the release continues.
// spec: §7.2 (Message delivery routing — seven paths, path 6)
func (s *Server) checkBacklogOnce(ctx context.Context, row sessionstore.Session) {
	n, err := s.heldLen(ctx, row)
	if err != nil {
		slog.Warn("sessionserver: read the held messages of a suspended session", "session_id", row.ID, "error", err)
		return
	}
	if n == 0 {
		return
	}
	if _, err := s.enterResumePending(ctx, row); err != nil {
		slog.Info("sessionserver: resume a suspended session with held messages", "session_id", row.ID, "error", err)
	}
}

// heldLen counts the messages the backlog check of row resumes for: for an
// idle suspension every DLQ entry and the inbox, and for an interrupt
// suspension the DLQ entries accepted after SuspendedAt.
// spec: §7.2 (Message delivery routing — seven paths, path 6)
func (s *Server) heldLen(ctx context.Context, row sessionstore.Session) (int, error) {
	if row.SuspendedReason == sessionstore.SuspendedReasonIdle {
		return s.messaging.HeldLen(ctx, row.TenantID, row.ID, true, time.Time{})
	}
	return s.messaging.HeldLen(ctx, row.TenantID, row.ID, false, row.SuspendedAt)
}

// idleWriteOutcome classifies one try of the idle suspension's write on the
// locked row it saw.
type idleWriteOutcome int

const (
	// idleWriteApply writes running → suspended with reason Idle.
	idleWriteApply idleWriteOutcome = iota
	// idleWriteLanded is a row this attempt's own earlier try wrote.
	idleWriteLanded
	// idleWriteLift is a row still on the attempt's pod whose activity or
	// state moved: the attempt lifts the seal and stops.
	idleWriteLift
	// idleWriteStale is a row at another coordination generation: a
	// takeover or a resume overtook the attempt, which sends the pod nothing
	// further.
	idleWriteStale
	// idleWriteStop is a row no longer on the attempt's pod.
	idleWriteStop
)

// classifyIdleWrite decides one try of the idle suspension's write from the
// locked row seen, the listed row, and the coordination generation gen of
// the binding through which the gateway sealed the session. A row at another
// generation is Stale. At gen, a `running` row on the listed pod with the
// listed activity is Apply; a podless Idle suspension is Landed, because at
// gen only this incarnation writes that row; any other row on the listed pod
// is Lift; and anything else is Stop.
// spec: §6.2 (maxClientIdleSeconds clock behavior across states), §10.1.1
// (Stateless Replicas and Per-Session Coordination)
func classifyIdleWrite(seen, row sessionstore.Session, gen int64) idleWriteOutcome {
	switch {
	case seen.CoordinationGeneration != gen:
		return idleWriteStale
	case seen.State == session.StateRunning && seen.PodAssignment == row.PodAssignment &&
		seen.LastAgentActivityAt.Equal(row.LastAgentActivityAt):
		return idleWriteApply
	case seen.State == session.StateSuspended && seen.SuspendedReason == sessionstore.SuspendedReasonIdle &&
		seen.PodAssignment == "":
		return idleWriteLanded
	case seen.PodAssignment == row.PodAssignment:
		return idleWriteLift
	default:
		return idleWriteStop
	}
}

// SuspendIdle is the watchdog's IdleSuspender hook: it suspends an idle
// `running` session and releases its pod or slot in one sequence (§6.2 Idle
// suspension.). It returns at once. It writes nothing when this replica
// holds no binding for the session or the binding carries no adapter
// connection. It starts nothing when this replica does not hold the
// session's coordination lease, holds an open terminal claim for it,
// already runs a release or an idle suspension of it, or is within 60 s of
// its last failed suspension checkpoint.
//
// Otherwise it runs off the caller, on the server-lifetime context: the
// adapter seal, the checkpoint, the guarded running → suspended write at the
// sealing binding's coordination generation, and the release tail. No step
// takes the delivery lock. Invariant: no Idle row names a pod, an Idle write
// takes effect only at the generation of the binding that sealed the
// session, and no message reaches the runtime between the seal and the
// release, on any replica.
//
// spec: §6.2 (maxClientIdleSeconds clock behavior across states), §6.2 (Pod
// State Machine)
func (s *Server) SuspendIdle(ctx context.Context, row sessionstore.Session, idleSeconds int) {
	bind := s.snapshotBinding(row.ID)
	if bind == nil || s.sealerOf(bind) == nil {
		return
	}
	if !s.coordinatesSession(ctx, row.TenantID, row.ID) {
		return
	}
	if !s.beginSuspension(row.ID) {
		return
	}
	go func() {
		defer s.suspensions.end(row.ID)
		s.runIdleSuspension(s.lifetime(), row, bind, idleSeconds)
	}()
}

// runIdleSuspension runs the idle suspension's steps after the gates.
func (s *Server) runIdleSuspension(ctx context.Context, row sessionstore.Session, bind *podsession.BindResult, idleSeconds int) {
	if !s.sealForIdleSuspension(ctx, row, bind, idleSeconds) {
		return
	}
	// The seal precedes the checkpoint, so no delivered message changes the
	// workspace after it.
	if !s.checkpointForSuspension(ctx, row) {
		s.unsealOrLog(ctx, row, bind)
		return
	}
	stored, outcome := s.writeIdleSuspension(ctx, row, bind.CoordinationGeneration)
	switch outcome {
	case idleWriteApply, idleWriteLanded:
	case idleWriteLift:
		s.unsealOrLog(ctx, row, bind)
		return
	case idleWriteStale:
		// spec: §10.1.5 — the pod release is not generation-checked, so
		// only this check keeps a stale attempt from releasing a pod an
		// adopter serves; the attempt sends the pod nothing further.
		s.evictStaleBinding(row.ID, bind)
		slog.Warn("sessionserver: coordinator_preempted", "session_id", row.ID,
			"binding_generation", bind.CoordinationGeneration, "row_generation", stored.CoordinationGeneration)
		return
	default:
		return
	}
	s.emitStatusChange(stored.TenantID, stored.ID, stored.State)
	s.rearmBudgetKeys(ctx, stored)
	s.releaseSuspendedBinding(ctx, stored, bind)
}

// sealForIdleSuspension asks the adapter to seal the session and reports
// whether it is sealed. When the adapter answers that activity occurred, the
// gateway's idle clock moves to that activity; when it refuses or does not
// answer, the clock moves to now. In each case nothing else is written and
// the session stays `running`, so the clock fires again one window later.
// spec: §6.2 (maxClientIdleSeconds clock behavior across states)
func (s *Server) sealForIdleSuspension(ctx context.Context, row sessionstore.Session, bind *podsession.BindResult, idleSeconds int) bool {
	sealed, idleAge, err := s.sealSession(ctx, bind, idleSeconds)
	switch {
	case err != nil && sealRefusedUntapped(err):
		// spec: §6.2 (maxClientIdleSeconds clock behavior across states) —
		// an adapter that reads no runtime output refuses the seal.
		s.stampActivity(row, time.Time{})
		slog.Info("sessionserver: idle suspension not sealed", "session_id", row.ID, "reason", "activity_untapped")
		return false
	case err != nil:
		s.stampActivity(row, time.Time{})
		slog.Info("sessionserver: idle suspension not sealed", "session_id", row.ID, "reason", "seal_unavailable", "error", err)
		return false
	case !sealed:
		s.stampActivity(row, s.clock().Add(-idleAge))
		slog.Info("sessionserver: idle suspension not sealed", "session_id", row.ID, "reason", "activity_recent")
		return false
	}
	return true
}

// sealRefusedUntapped reports whether err is the adapter's refusal of a seal
// for a runtime whose output it does not read.
func sealRefusedUntapped(err error) bool {
	st, ok := status.FromError(err)
	return ok && strings.HasPrefix(st.Message(), "activity_untapped:")
}

// stampActivity moves the session's idle clock: to at when at is set, and to
// now otherwise.
func (s *Server) stampActivity(row sessionstore.Session, at time.Time) {
	if s.activityStamper == nil {
		return
	}
	if at.IsZero() {
		s.activityStamper.Stamp(row.TenantID, row.ID)
		return
	}
	s.activityStamper.StampAt(row.TenantID, row.ID, at)
}

// unsealOrLog lifts the seal and logs an error with the session ID.
func (s *Server) unsealOrLog(ctx context.Context, row sessionstore.Session, bind *podsession.BindResult) {
	if err := s.unsealSession(ctx, bind); err != nil {
		slog.Warn("sessionserver: lift the idle suspension's seal", "session_id", row.ID, "error", err)
	}
}

// writeIdleSuspension runs the idle suspension's running → suspended write.
// Each try's mutator classifies the locked row with classifyIdleWrite and
// applies only idleWriteApply: State suspended, SuspendedAt at,
// SuspendedReason Idle, and no pod. A store error leaves the outcome unknown,
// so the write repeats with the same inputs until it returns nil, the
// not-applied sentinel, or ErrNotFound, or ctx ends; a repeat classifies a
// row its own landed commit left as idleWriteLanded. It returns the stored
// row on Apply and the seen row otherwise. ErrNotFound and a context end are
// idleWriteStop.
// spec: §6.2 (maxClientIdleSeconds clock behavior across states), §10.1.1
// (Stateless Replicas and Per-Session Coordination)
func (s *Server) writeIdleSuspension(ctx context.Context, row sessionstore.Session, gen int64) (sessionstore.Session, idleWriteOutcome) {
	at := s.clock()
	for {
		var seen sessionstore.Session
		outcome := idleWriteStop
		updated, err := s.store.Update(ctx, row.TenantID, row.ID, func(r *sessionstore.Session) error {
			seen = *r
			outcome = classifyIdleWrite(seen, row, gen)
			if outcome != idleWriteApply {
				return errIdleWriteNotApplied
			}
			r.State = session.StateSuspended
			r.SuspendedAt = at
			r.SuspendedReason = sessionstore.SuspendedReasonIdle
			r.PodAssignment = ""
			return nil
		})
		switch {
		case err == nil:
			return updated, idleWriteApply
		case errors.Is(err, errIdleWriteNotApplied):
			return seen, outcome
		case errors.Is(err, sessionstore.ErrNotFound):
			return seen, idleWriteStop
		}
		slog.Warn("sessionserver: idle suspension write; repeating", "session_id", row.ID, "error", err)
		select {
		case <-ctx.Done():
			return seen, idleWriteStop
		case <-time.After(idleWriteRetryBackoff):
		}
	}
}
