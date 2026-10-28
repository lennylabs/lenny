// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/watchdog"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// handleStart implements POST /v1/sessions/{id}/start per §15.1: the
// explicit launch transition of the two-step create → finalize → start
// lifecycle. It moves a ready session through starting to running and, when
// the gateway is wired with a pod binder, launches the runtime on the pod
// prepared at /finalize using the §14 WorkspacePlan stored at create.
//
// Per the proposal §4.4, /start is launch-only: the §4.3 preparation barrier
// (staging, workspace materialization, setup commands, and credential-lease
// assignment) already ran at /finalize, so /start neither claims a pod nor
// re-runs any of that work. It reconnects to the prepared pod and runs only
// the §6.3 agent_session_start phase (StartSession for a pod-warm pod,
// ConfigureWorkspace for an SDK-warm one) through launchOnPod. The exception
// is a concurrent-workspace pool, whose reserved slot materializes and
// launches together at /start (§5.2); launchOnPod owns that distinction.
//
// The steps run in this order: the entry read, the §15.1 precondition, and
// the stored-plan parse; the ready → starting write, re-validated on the
// locked row, followed by status_change(starting); the coordination-lease
// acquire when the row names a pod; the launch and the binding publish; the
// setup-output persist; and the starting → running commit. The starting
// write precedes the acquire, so the locked row alone serializes concurrent
// starts and no ready row holds a lease, and the §6.2 starting watchdog
// bounds the launch rather than the ready watchdog. An exit before the
// starting write changes nothing. Every error exit after it fails the
// session through failStarting and releases the lease; a retry of the same
// session is then refused, and the client creates a new session.
//
// spec: §4.4, §4.6 (proposal); §15.1 (/start precondition); §6.1; §6.2
// (starting watchdog); §7.2 (Session state machine: ready → starting,
// starting → running, starting → failed); §10.1.1 (Primary:).
func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	tenantID := s.resolveTenant(r)
	id := r.PathValue("id")
	// spec: §5.2 (Slot assignment atomicity) — the launch bound starts before
	// the row is read, and the read goes to the primary, whose answer a
	// terminal write cannot precede unseen. A launch that read the row
	// non-terminal therefore ends within the bound of the terminal write that
	// would end it, which the stale-slot reaper's terminal age exceeds.
	launchCtx, cancelLaunch := s.launchContext(r.Context())
	defer cancelLaunch()
	row, err := s.readPrimary(launchCtx, tenantID, id)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if err := session.Validate(session.PreconditionRequest{
		Endpoint:     session.EndpointStart,
		CurrentState: row.State,
	}); err != nil {
		s.writePreconditionError(w, err)
		return
	}
	var plan workspaceplan.Plan
	if s.podBinder != nil {
		plan, err = storedWorkspacePlan(row)
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
				"stored workspace plan could not be parsed: "+err.Error(), nil)
			return
		}
	}
	if err := s.enterStarting(r.Context(), tenantID, id); err != nil {
		var pe *session.PreconditionError
		if errors.As(err, &pe) {
			s.writePreconditionError(w, err)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	updated, answer := s.launchStarted(r.Context(), launchCtx, row, plan)
	if answer != nil {
		s.abandonStart(r.Context(), w, tenantID, id, answer)
		return
	}
	s.writeSession(w, http.StatusOK, updated)
}

// startAnswer writes the response of a /start exit that follows the
// ready → starting write.
type startAnswer func(w http.ResponseWriter)

// enterStarting writes ready → starting. The mutator re-runs the §15.1 start
// precondition on the locked row, so of two concurrent starts on any replicas
// only the first write applies, and the other gets the precondition error
// carrying the locked row's state. An applied write publishes
// status_change(starting).
//
// spec: §7.2 (Session state machine: ready → starting), §15.1 (/start)
func (s *Server) enterStarting(ctx context.Context, tenantID, id string) error {
	updated, err := s.store.Update(ctx, tenantID, id, func(row *sessionstore.Session) error {
		if verr := session.Validate(session.PreconditionRequest{
			Endpoint:     session.EndpointStart,
			CurrentState: row.State,
		}); verr != nil {
			return verr
		}
		row.State = session.StateStarting
		return nil
	})
	if err != nil {
		return err
	}
	s.emitStatusChange(updated.TenantID, updated.ID, updated.State)
	return nil
}

// launchStarted runs the steps that follow the ready → starting write: the
// coordination-lease acquire, the launch, the binding publish, the setup
// output persist, and the starting → running commit. It returns the running
// row, or the answer of the exit that stopped it; the caller fails the
// session on any exit.
//
// spec: §15.1 (/start), §4.6.1 (coordinating replica holds the lease), §10.1
// (per-session coordination lease), §7.5 (setup output)
func (s *Server) launchStarted(ctx, launchCtx context.Context, row sessionstore.Session, plan workspaceplan.Plan) (sessionstore.Session, startAnswer) {
	if s.podBinder != nil {
		if answer := s.launchAndPublish(ctx, launchCtx, row, plan); answer != nil {
			return sessionstore.Session{}, answer
		}
	}
	updated, err := s.commitStart(ctx, row.TenantID, row.ID)
	if err != nil {
		var pe *session.PreconditionError
		if errors.As(err, &pe) {
			return sessionstore.Session{}, func(w http.ResponseWriter) { s.writePreconditionError(w, err) }
		}
		return sessionstore.Session{}, func(w http.ResponseWriter) {
			s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		}
	}
	return updated, nil
}

// launchAndPublish acquires the coordination lease when the row names a pod,
// launches the runtime, publishes the binding, and persists any setup output
// the launch captured. A row with no pod (a service-mode pool) acquires no
// lease, as the bound != nil acquire of the single-call start does.
//
// spec: §4.4, §4.6 (proposal) — /start is launch-only: launchOnPod reconnects
// to the pod prepared at /finalize and runs only the §6.3 agent_session_start
// phase, without re-staging, re-materializing, re-running setup, or
// re-assigning the credential lease. Only the concurrent-pool slot launch,
// which materializes at /start, resolves credential inputs.
func (s *Server) launchAndPublish(ctx, launchCtx context.Context, row sessionstore.Session, plan workspaceplan.Plan) startAnswer {
	if row.PodAssignment != "" {
		// spec: §4.6.1, §10.1 — on ErrHeld a live foreign holder still
		// coordinates this session, so launch nothing and fail the start
		// closed rather than double-bind.
		if err := s.acquireCoordinationLease(ctx, row.TenantID, row.ID); err != nil {
			return func(w http.ResponseWriter) {
				s.writeError(w, http.StatusServiceUnavailable, "STARTING_FAILED",
					"could not acquire the session's coordination lease: "+err.Error(), nil)
			}
		}
	}
	result, err := s.launchOnPod(launchCtx, row, plan)
	if err != nil {
		// spec: §7.1 / §6.2 — the launch failed on the explicit launch half of
		// the §7.1 atomic-creation unit; the binder already reclaimed the pod
		// it bound on a failure inside the binder.
		return podClaimAnswer(s, err)
	}
	// spec: §10.1.1, §10.1.5 — the binding carries the generation of the row
	// read at entry; registerBinding self-renews the lease acquired above.
	stampBindingGeneration(result, row.CoordinationGeneration)
	if err := s.registerBinding(ctx, result); err != nil {
		s.rollbackBinding(ctx, result)
		return podClaimAnswer(s, err)
	}
	s.persistStartSetupOutput(ctx, row.TenantID, row.ID, result)
	return nil
}

// podClaimAnswer answers a launch or bind error through the writePodClaimError
// mapping, with STARTING_FAILED as the fallback.
func podClaimAnswer(s *Server, err error) startAnswer {
	return func(w http.ResponseWriter) {
		s.writePodClaimError(w, err, "STARTING_FAILED", "could not place the session on a warm pod")
	}
}

// persistStartSetupOutput persists the per-command setup output a
// concurrent-workspace slot launch captured, so a later GET
// /v1/sessions/{id} can surface it. The exclusive-pool launch produces none,
// because its setup ran at /finalize. A store error is logged and the start
// continues, as the finalize persist does, because the §7.5 trail is
// best-effort.
//
// spec: §7.5 (setup output). F-7.5.4.
func (s *Server) persistStartSetupOutput(ctx context.Context, tenantID, id string, result *podsession.BindResult) {
	if result == nil || len(result.SetupOutputs) == 0 {
		return
	}
	outs := setupOutputsFromBind(result.SetupOutputs)
	if _, err := s.store.Update(ctx, tenantID, id, func(rr *sessionstore.Session) error {
		rr.SetupOutput = outs
		return nil
	}); err != nil {
		log.Printf("sessionserver: persist setup output at start of session %s: %v", id, err)
	}
}

// abandonStart ends a /start whose exit followed the ready → starting write.
// It fails the session through failStarting and then releases the
// coordination lease detached from the request, whatever failStarting
// returned. When failStarting refuses because another writer moved the
// locked row out of starting, the response is that refusal with the locked
// row's state; otherwise it is the exit's own answer.
//
// spec: §7.2 (Session state machine: starting → failed), §15.1 (/start),
// §10.1 (per-session coordination lease)
func (s *Server) abandonStart(ctx context.Context, w http.ResponseWriter, tenantID, id string, answer startAnswer) {
	ferr := s.failStarting(ctx, tenantID, id)
	s.releaseCoordinationLease(context.WithoutCancel(ctx), tenantID, id)
	var pe *session.PreconditionError
	if errors.As(ferr, &pe) {
		s.writePreconditionError(w, ferr)
		return
	}
	if ferr != nil {
		log.Printf("sessionserver: fail starting session %s: %v", id, ferr)
	}
	answer(w)
}

// errStartSuperseded is the not-applied sentinel of a running-commit whose
// locked row no longer admits the start: a terminal write, or a racing second
// start that committed first, landed after the entry read.
var errStartSuperseded = errors.New("sessionserver: start superseded on the locked row")

// commitStart commits a started session to running and delivers the messages
// held for it before running. It takes the session's delivery lock before the
// commit and releases it after the delivery. The commit applies
// startingPrecondition to the locked row and refuses with the §15.1
// precondition error carrying the locked row's state, so a terminal write
// that landed after the starting write stays terminal (the terminal binding
// release reclaims a binding published after it); a refused commit emits no
// status change and delivers nothing. A delivery error is logged, and the
// start still answers running.
//
// spec: §7.2 (Interactive Session Model; Message delivery routing — seven
// paths, path 6), §15.1 (REST API)
func (s *Server) commitStart(ctx context.Context, tenantID, id string) (sessionstore.Session, error) {
	unlock, err := s.deliveryLocks.Lock(ctx, id)
	if err != nil {
		return sessionstore.Session{}, err
	}
	defer unlock()
	updated, err := s.store.Update(ctx, tenantID, id, func(row *sessionstore.Session) error {
		if verr := startingPrecondition(row); verr != nil {
			return verr
		}
		transitionStart(row)
		return nil
	})
	if err != nil {
		return sessionstore.Session{}, err
	}
	// spec: §7.2 — surface the starting → running transition.
	s.emitStatusChange(updated.TenantID, updated.ID, updated.State)
	if derr := s.deliverBuffered(ctx, updated); derr != nil {
		log.Printf("sessionserver: deliver held messages after start of session %s: %v", id, derr)
	}
	return updated, nil
}

// launchContext derives the context a slot launch runs on: ctx bounded by
// the launch bound, MaxFinalizing or the watchdog's finalizing default when
// it is not positive. The compensating reclaims of a failed launch detach
// from it, so an expired bound still releases what the launch reserved.
// spec: §5.2 (Slot assignment atomicity).
func (s *Server) launchContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, s.launchBound())
}

// launchBound is the launch deadline in effect.
func (s *Server) launchBound() time.Duration {
	if s.maxFinalizing > 0 {
		return s.maxFinalizing
	}
	return time.Duration(watchdog.DefaultMaxFinalizingStateSeconds) * time.Second
}
