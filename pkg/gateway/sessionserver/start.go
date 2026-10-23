// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/watchdog"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// handleStart implements POST /v1/sessions/{id}/start per §15.1: the
// explicit launch transition of the two-step create → finalize → start
// lifecycle. It transitions a ready session to running and, when the gateway
// is wired with a pod binder, launches the runtime on the pod prepared at
// /finalize using the §14 WorkspacePlan stored at create.
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
// handleStart is a dedicated handler rather than a generic handleTransition
// because the start transition carries the extra launch step — the same
// reason handleFinalize is dedicated for the finalize transition. The launch
// runs before the row transitions: a launch failure leaves the row ready so
// the client can retry POST /start.
//
// spec: §4.4, §4.6 (proposal); §15.1 (/start precondition); §6.1.
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

	if s.podBinder != nil {
		plan, err := storedWorkspacePlan(row)
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
				"stored workspace plan could not be parsed: "+err.Error(), nil)
			return
		}
		// spec: §4.4, §4.6 (proposal) — /start is launch-only: launchOnPod
		// reconnects to the pod prepared at /finalize and runs only the §6.3
		// agent_session_start phase (StartSession or ConfigureWorkspace),
		// without re-staging, re-materializing, re-running setup, or
		// re-assigning the credential lease, and without any §4.9 credential
		// resolution. The exclusive-pool launch needs no credential inputs (the
		// lease was assigned at /finalize); only the concurrent-pool slot
		// launch, which materializes at /start, resolves them.
		result, err := s.launchOnPod(launchCtx, row, plan)
		if err != nil {
			// spec: §7.1 / §6.2 — `POST /v1/sessions/{id}/start`
			// returns `STARTING_FAILED` when the §7.1 atomic-creation unit
			// fails on the explicit launch half. The row stays `ready` so the
			// client can retry; the binder already reclaimed the prepared pod
			// (and the lease assigned at /finalize) on the launch failure path.
			s.writePodClaimError(w, err, "STARTING_FAILED", "could not place the session on a warm pod")
			return
		}
		// spec: §4.6.1 (coordinating replica holds the lease), §10.1
		// (per-session coordination lease) — the row is still `ready` here
		// (the running-commit follows below), so the at-bind acquire inside
		// registerBinding precedes the running-commit. On ErrHeld a live
		// foreign holder still coordinates this session, so publish nothing,
		// release this replica's freshly launched pod, and fail the start
		// closed rather than double-bind. The binding carries the generation
		// of the row read above (spec: §10.1.1, §10.1.5).
		stampBindingGeneration(result, row.CoordinationGeneration)
		if err := s.registerBinding(r.Context(), result); err != nil {
			s.rollbackBinding(r.Context(), result)
			s.writePodClaimError(w, err, "STARTING_FAILED", "could not place the session on a warm pod")
			return
		}
		// spec: §7.5 — persist the per-command setup output a
		// concurrent-workspace slot launch captured so a subsequent GET
		// /v1/sessions/{id} can surface it. The exclusive-pool launch-only path
		// produces no setup output (setup ran at /finalize, persisted there by
		// applyFinalizePrepareResult), so this fires only for the concurrent
		// slot launch that materializes at /start. F-7.5.4.
		if result != nil && len(result.SetupOutputs) > 0 {
			outs := setupOutputsFromBind(result.SetupOutputs)
			if _, uerr := s.store.Update(r.Context(), tenantID, id, func(rr *sessionstore.Session) error {
				rr.SetupOutput = outs
				return nil
			}); uerr != nil {
				// Persistence failure is non-fatal; the §7.5 trail is
				// best-effort. Log via the diagnostics path.
				s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", uerr.Error(), nil)
				return
			}
		}
	}

	updated, err := s.commitStart(r.Context(), tenantID, id)
	if err != nil {
		s.writePreconditionError(w, err)
		return
	}
	s.writeSession(w, http.StatusOK, updated)
}

// errStartSuperseded is the not-applied sentinel of a running-commit whose
// locked row no longer admits the start: a terminal write, or a racing second
// start that committed first, landed after the entry read.
var errStartSuperseded = errors.New("sessionserver: start superseded on the locked row")

// commitStart commits a started session to running and delivers the messages
// held for it before running. It takes the session's delivery lock before the
// commit and releases it after the delivery. The commit re-runs the start
// precondition on the locked row and refuses with the §15.1 precondition
// error carrying the locked row's state, so a terminal write that landed after
// the binding was registered stays terminal (the terminal binding release
// reclaims that binding) and a second start racing one that committed gets
// the answer a sequential second start gets; a refused commit emits no status
// change and delivers nothing. A delivery error is logged, and the start
// still answers running.
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
		if verr := session.Validate(session.PreconditionRequest{
			Endpoint:     session.EndpointStart,
			CurrentState: row.State,
		}); verr != nil {
			return verr
		}
		transitionStart(row)
		return nil
	})
	if err != nil {
		return sessionstore.Session{}, err
	}
	// spec: §7.2 — surface the ready → running transition.
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
