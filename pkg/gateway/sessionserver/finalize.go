// SPDX-License-Identifier: MIT

package sessionserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy/credrouter"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// finalizePlanMaxBytes caps the JSON body POST /v1/sessions/{id}/finalize
// will read for the workspace-plan binding. The plan references already
// uploaded blobs by uploadRef and carries no file content, so it stays
// small; the cap protects against a malformed oversize body.
const finalizePlanMaxBytes int64 = 1 << 20 // 1 MiB

// finalizeRequest is the optional body of POST /v1/sessions/{id}/finalize.
// A no-body finalize leaves WorkspacePlan nil. A session created without a
// plan binds the plan carried here.
//
// spec: §14.1 WorkspacePlan Schema Versioning (Plan binding); §7.1 Normal
// Flow.
type finalizeRequest struct {
	WorkspacePlan json.RawMessage `json:"workspacePlan,omitempty"`
}

// resolveFinalizePlan reads the optional §14 WorkspacePlan from a finalize
// request body and resolves the plan this finalize binds. It returns the
// canonical JSON to persist, the parser's consumer-advisory warnings,
// whether there is a plan to persist, and ok=false (with the error response
// already written) on any rejection. Every rejection precedes the
// created → finalizing write, so a rejected finalize binds nothing.
//
//   - A row that already has a plan keeps it: a body carrying a plan is
//     rejected with plan_already_set, and a body without one persists
//     nothing, so finalize materializes the stored plan.
//   - Otherwise the body's plan, or the empty plan when the body carries
//     none, is validated as a submitted plan bound to this session (its
//     uploadRef values are checked against row.ID), checked against the
//     runtime's setupCommandPolicy, and composed with the runtime's
//     workspaceDefaults. hasPlan is false only when that yields no plan
//     JSON: no client plan and no defaults.
//
// spec: §14.1 WorkspacePlan Schema Versioning (Plan binding; Runtime
// workspace defaults); §7.1 Normal Flow; §7.5 Setup Commands.
func (s *Server) resolveFinalizePlan(w http.ResponseWriter, r *http.Request, tenantID string, row sessionstore.Session) (
	storedJSON json.RawMessage, warnings []workspaceplan.Warning, hasPlan bool, ok bool,
) {
	raw, readOK := s.readFinalizePlanBody(w, r)
	if !readOK {
		return nil, nil, false, false
	}
	if len(row.WorkspacePlan) > 0 && !isJSONNull(row.WorkspacePlan) {
		// spec: §14.1 (Plan binding) — a session binds at most one plan, so
		// finalize cannot replace or merge with the plan bound at creation.
		if len(raw) > 0 && !isJSONNull(raw) {
			s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
				"session already has a workspace plan; finalize cannot replace it",
				map[string]any{"reason": "plan_already_set"})
			return nil, nil, false, false
		}
		return nil, nil, false, true
	}

	parsed, storedJSON, warns, planOK := s.resolveSubmittedPlan(w, r, raw, tenantID, row.ID)
	if !planOK {
		return nil, nil, false, false
	}
	// spec: §7.5 / §5.1 — the setup-command cap applies to
	// a finalize-bound plan exactly as it does at create.
	if !s.enforceSetupCommandPolicy(w, r, row.RuntimeRef, parsed) {
		return nil, nil, false, false
	}
	if _, storedJSON, planOK = s.composeRuntimeDefaults(w, r, row.RuntimeRef, parsed, storedJSON); !planOK {
		return nil, nil, false, false
	}
	return storedJSON, warns, len(storedJSON) > 0, true
}

// readFinalizePlanBody reads the finalize request body and extracts the
// optional workspacePlan. An empty body is the no-plan finalize and
// returns (nil, true). A malformed body writes a 400 and returns ok=false.
func (s *Server) readFinalizePlanBody(w http.ResponseWriter, r *http.Request) (json.RawMessage, bool) {
	if r.Body == nil {
		return nil, true
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, finalizePlanMaxBytes))
	if err != nil {
		s.writeError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE",
			"finalize request body exceeds the size cap", nil)
		return nil, false
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, true
	}
	var req finalizeRequest
	if err := json.Unmarshal(body, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"finalize request body is not valid JSON", nil)
		return nil, false
	}
	return req.WorkspacePlan, true
}

// validatePlanUploadRefs verifies that every uploadFile and uploadArchive
// source in a client-submitted plan references content stored for the
// session the plan binds to. The gateway reads its own lenny-blob://
// encoding of the uploadRef, while clients treat the value as opaque. A
// value the gateway cannot decode writes 400 VALIDATION_ERROR with
// reason invalid_upload_ref. A value stored for another tenant or
// session, or any upload source when bindSessionID is empty (a plan bound
// at creation), writes 400 VALIDATION_ERROR with reason
// upload_ref_foreign_session. It returns false on the first rejected
// source.
//
// spec: §14 Workspace Plan Schema (uploadRef field note); §14.1
// WorkspacePlan Schema Versioning (Plan binding); §12.5.
func (s *Server) validatePlanUploadRefs(w http.ResponseWriter, tenantID, bindSessionID string, plan workspaceplan.Plan) bool {
	for i, src := range plan.Sources {
		var ref string
		switch v := src.Variant.(type) {
		case workspaceplan.UploadFile:
			ref = v.UploadRef
		case workspaceplan.UploadArchive:
			ref = v.UploadRef
		default:
			continue
		}
		uri, err := blobstore.ParseURI(ref)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
				"uploadRef is not a valid lenny-blob:// reference",
				map[string]any{"field": sourceUploadRefField(i, src.Type), "reason": "invalid_upload_ref"})
			return false
		}
		// A plan bound at creation has no session whose uploads it could
		// name, so the empty bindSessionID rejects every upload source
		// even if the decoder ever admits an empty session segment.
		if bindSessionID == "" || uri.TenantID != tenantID || uri.SessionID != bindSessionID {
			s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
				"uploadRef must reference a blob uploaded to this session",
				map[string]any{"field": sourceUploadRefField(i, src.Type), "reason": "upload_ref_foreign_session"})
			return false
		}
	}
	return true
}

// sourceUploadRefField returns the §15.1 details.field path for a source's
// uploadRef so the error report points at the offending plan entry.
func sourceUploadRefField(i int, _ string) string {
	return "sources[" + strconv.Itoa(i) + "].uploadRef"
}

// prepareAtFinalize runs the §4.3 finalize-time preparation barrier against
// the pod claimed at /create. It reconnects to the pod named in the row's
// persisted §4.6 binding (PodAssignment + PoolRef), streams the buffered
// upload content into the session's staging tree (PrepareWorkspace),
// materializes its current tree with the §7.4 post-promotion symlink re-validation
// (FinalizeWorkspace), runs the plan's setup commands (RunSetup), and assigns
// the §4.9 credential lease (AssignCredentials), recording the §6.3
// workspace_materialization / setup_commands / credential_assignment phase
// timings.
//
// No failure exit of this function releases the create-time pod or slot
// itself. A through-Prepare failure (workspace validation, setup command, or
// lease assignment) is reclaimed by the binder's lease-aware failPhase, which
// revokes the lease when AssignCredentials had already run. A pre-Prepare
// failure (pool resolution, or a credential source that was available at the
// create-time pre-check but gone by finalize) never engages the binder; the
// caller's failure write, when it applies, runs the post-terminal hook with
// fromState finalizing, and the hook's pre-running reclaim releases the pod
// on an exclusive pool and the session's slot member on a concurrent-workspace
// pool. When that write loses or reports a store error, the terminal writer
// or the finalizing watchdog owns the release. A finalize-time credential availability miss is the
// §4.9 check-to-assignment mismatch (the source vanished across the
// upload window), so it is remapped to CREDENTIAL_POOL_EXHAUSTED rather than the
// create-only USER_CREDENTIAL_NOT_FOUND (§4.9). handleFinalize answers the
// returned error through finalizePrepareFailed. When a terminal writer
// overtook the call, the response is 409 INVALID_STATE_TRANSITION with the
// terminal state in place of the returned error (§15.1). Otherwise the error
// surfaces through writePodClaimError, where a workspace-materialization
// failure takes the retryable SESSION_CREATION_FAILED fallback unless it is a
// §13.4 archive validator violation (UPLOAD_ARCHIVE_LIMIT_EXCEEDED).
//
// It returns (nil, nil) for the dispositions that perform no finalize-time
// preparation:
//
//   - the binder is not wired (the minimal gateway, which finalizes by a plain
//     state transition);
//   - a service-mode pool, which is claimless and materializes no workspace
//     (§5.2);
//   - a concurrent-workspace pool (maxConcurrentSessions>1), whose reserved
//     slot is materialized and launched together at /start via
//     BindReservedSlot rather than decomposed across finalize and start;
//   - a row that carries no live create-time binding (PodAssignment empty or a
//     recovery-state row), so there is no claimed pod to prepare against.
//
// spec: §7.1 steps 11-13; §7.4; §4.9 (finalize lease
// assignment); §4.3, §4.6 (proposal); §6.3.
func (s *Server) prepareAtFinalize(ctx context.Context, row sessionstore.Session, plan workspaceplan.Plan) (*podsession.PrepareResult, error) {
	if s.podBinder == nil {
		return nil, nil
	}
	// No claimed pod on the row means nothing to prepare: a service-mode
	// session (claimless), or a row whose binding is not a live create-time
	// claim (empty PodAssignment, or a recovery-state row carrying a stale
	// dead-pod name). Fail closed against re-claiming here; the launch path
	// owns the recovery-state rebuild.
	if row.PodAssignment == "" || session.IsRecovery(row.State) {
		return nil, nil
	}
	// spec: §7.1 / §14.1 — constrain resolution to the client-pinned pool
	// (row.Pool) the create persisted; empty resolves by runtime + §5.3
	// profile. F-CS2 (0018).
	match, err := podsession.ResolvePool(ctx, s.podBinder.Client, s.poolPolicyReader(), s.agentNamespace,
		row.RuntimeRef, string(row.IsolationProfile), row.Pool)
	if err != nil {
		// spec: §15.1 (finalize row), §6.2 (finalize timeout), §7.2 (terminal
		// states) — a pre-Prepare failure (pool resolution) releases nothing
		// here. This is the only pre-Prepare exit a concurrent-workspace pool
		// reaches, and a by-name claim delete would end every live sibling on
		// its shared claim. The applied failure write's hook releases the pod
		// or the slot by the row's pool kind.
		return nil, err
	}
	// spec: §5.2 — service mode is claimless and materializes no workspace;
	// a concurrent-workspace pool reserves a slot at create and materializes +
	// launches it together at /start (BindReservedSlot), so neither is prepared
	// at finalize. Both finalize by a plain state transition.
	if match.ExecutionMode == string(runtimestore.ExecutionModeService) || match.MaxConcurrentSessions > 1 {
		return nil, nil
	}
	// spec: §4.9 / §7.1 step 12 — the credential lease is
	// assigned at finalize (the proposal's deviation from the literal step-6
	// ordering), so resolve the per-provider pool map here from the persisted
	// row. The create-time pre-check already confirmed availability; this
	// resolves the assignment inputs the prepare phase's AssignCredentials uses.
	credPools, poolDeliveryModes, userCredProviders, err := s.resolveCredentialPools(ctx, row)
	if err != nil {
		// spec: §15.1 (finalize row), §6.2 (finalize timeout) — the resolution
		// ran before the binder engaged; the applied failure write's hook
		// releases the create-time pod.
		// spec: §4.9 — a credential source that was
		// available at the create-time pre-check but is gone at finalize is the
		// check-to-assignment mismatch the proposal requires to surface as
		// CREDENTIAL_POOL_EXHAUSTED at /finalize, not the create-only
		// USER_CREDENTIAL_NOT_FOUND (404) or pre-claim envelope. The mismatch
		// window spans the whole create → upload → finalize window (§4.9), and
		// the lenny_credential_preclaim_mismatch_total counter records the same
		// pre-check-passes-then-assignment-fails event, now observed at finalize.
		return nil, mapFinalizeCredentialMismatch(err)
	}
	// spec: §4.9 — the session-start credential-delivery gate on the two-step
	// finalize seam. Evaluate each resolved CredentialPool's effective
	// deliveryMode against the bound pod's isolationProfile/spiffeBinding
	// before the prepare barrier assigns the lease, so a forbidden combination
	// the pool-definition copy hid is rejected in multi-tenant mode before any
	// lease is minted. Like the two pre-Prepare exits above, the refusal
	// releases nothing here; the applied failure write's hook releases the
	// create-time pod.
	if derr := s.checkCredentialDeliveryIsolation(match, poolDeliveryModes); derr != nil {
		return nil, derr
	}
	agentInterface, minPlatformVersion := s.runtimeManifestFields(ctx, row.RuntimeRef)
	bindReq := s.exclusiveBindRequest(ctx, row, match, plan, credPools, userCredProviders, agentInterface, minPlatformVersion)
	bindReq.SandboxName = row.PodAssignment
	if row.PoolRef != "" {
		bindReq.Pool = row.PoolRef
	}
	prep, err := s.podBinder.Prepare(ctx, bindReq)
	if err != nil {
		return nil, err
	}
	// spec: §6.3 / §5 (proposal) — record the workspace_materialization,
	// setup_commands, and credential_assignment phase timings at /finalize,
	// their new boundary in the decomposed lifecycle. The pod_claim phase was
	// recorded at /create and the agent_session_start phase is recorded at
	// /start, so each phase is observed once per logical start.
	s.recordStartupPhases(match, prep.Timings)
	return prep, nil
}

// mapFinalizeCredentialMismatch translates a finalize-time credential
// availability miss into the §4.9 check-to-assignment mismatch so
// writePodClaimError surfaces it as CREDENTIAL_POOL_EXHAUSTED (assignment_race)
// and increments lenny_credential_preclaim_mismatch_total, rather than the
// create-only USER_CREDENTIAL_NOT_FOUND (404) or pre-claim CREDENTIAL_POOL_EXHAUSTED
// envelopes.
//
// The §4.9 division is "check at create, assignment at finalize": a credential
// source present at the create-time pre-check that is gone by finalize is the
// race the proposal attributes to the upload window, not a create-time
// not-found. Both credrouter sentinels (ErrUserCredentialNotFound, which the
// without-fallback create-time pre-check already rejects before any pod is
// claimed, and ErrNoCredentialAvailable) collapse to the same finalize-time
// mismatch; any other error (a store read failure, a proxy-dialect mismatch)
// is returned unchanged so it keeps its own envelope.
//
// spec: §4.9; §7.3
// (proposal: USER_CREDENTIAL_NOT_FOUND is not a finalize trigger; the mismatch
// surfaces as CREDENTIAL_POOL_EXHAUSTED at /finalize).
func mapFinalizeCredentialMismatch(err error) error {
	if errors.Is(err, credrouter.ErrUserCredentialNotFound) || errors.Is(err, credrouter.ErrNoCredentialAvailable) {
		// Wrap the message rather than the sentinel itself: writePodClaimError
		// routes ErrUserCredentialNotFound to a create-only 404 before it reaches
		// the CredentialAssignmentError case, so a CredentialAssignmentError that
		// unwrapped to the sentinel would still surface as USER_CREDENTIAL_NOT_FOUND.
		// Keeping the sentinel out of the unwrap chain forces the assignment-race
		// (CREDENTIAL_POOL_EXHAUSTED) envelope the proposal requires at finalize.
		return &podsession.CredentialAssignmentError{Err: errors.New(err.Error())}
	}
	return err
}

// storedWorkspacePlanForFinalize returns the §14 WorkspacePlan the finalize
// barrier materializes. When the finalize body carried a plan (hasPlan), that
// is the plan to materialize, parsed from its canonical JSON; otherwise the
// plan recorded on the row at create is re-parsed via ParseStored (which
// accepts the gateway-written resolvedCommitSha the create path pinned). A
// finalize without either is the empty workspace.
func storedWorkspacePlanForFinalize(row sessionstore.Session, hasPlan bool, planJSON []byte) (workspaceplan.Plan, error) {
	if hasPlan {
		if len(planJSON) == 0 || isJSONNull(planJSON) {
			return workspaceplan.Plan{}, nil
		}
		plan, _, err := workspaceplan.ParseStored(planJSON)
		return plan, err
	}
	return storedWorkspacePlan(row)
}

// applyFinalizePrepareResult persists the §7.5 setup-command trail and the
// §7.3 negotiated workspace root the §4.3 prepare phase produced, and
// republishes the §7.4 strip-skip advisories on the §7.2 SSE stream.
// The persists mirror the /start launch path (registerBinding), moved to
// finalize because the prepare phase now runs there. They are best-effort: a
// store failure leaves the in-memory state authoritative and does not block
// the finalize from reaching ready.
//
// spec: §7.5, §7.3, §7.4; §4.3 (proposal).
func (s *Server) applyFinalizePrepareResult(ctx context.Context, tenantID, id, resultTenantID, resultSessionID string, prep *podsession.PrepareResult) {
	if len(prep.SetupOutputs) > 0 {
		if _, err := s.store.Update(ctx, tenantID, id, func(r *sessionstore.Session) error {
			r.SetupOutput = setupOutputsFromBind(prep.SetupOutputs)
			return nil
		}); err != nil {
			// Best-effort: the §7.5 trail is non-fatal to reaching ready.
			log.Printf("sessionserver: persist setup output for session %s: %v", id, err)
		}
	}
	// spec: §7.3; §6.4 — capture the workspace base the adapter reported so
	// the persist derives the session's slot root and a later Resume can
	// assert the replacement pod's resolved root matches.
	s.persistWorkspaceRoot(ctx, resultTenantID, resultSessionID, prep.WorkspaceBase)
	// spec: §7.4 — republish each strip-components-skip advisory the
	// gateway and adapter raised during materialization on the per-session SSE
	// bus so a client can audit the skipped archive entries.
	s.publishWorkspacePlanWarnings(resultTenantID, resultSessionID, prep.WorkspacePlanWarnings)
}

// finalizePlanParseFailed answers a finalize call whose stored WorkspacePlan
// could not be parsed. The parse failed before the prepare phase engaged the
// binder, so the binder's own reclaim cannot run. A failure write that
// applied runs the post-terminal hook, whose pre-running reclaim releases the
// pod claimed at /create by the row's pool kind. After a failure write that
// returned a store error no hook ran, so the handler reclaims through the same
// slot-aware call, keyed by scrubPolicy: on a concurrent-workspace pool it
// removes only the session's slot member, and on an exclusive pool it deletes
// the claim by name. No ready write runs on this exit, so the reclaim cannot
// end a committed `ready` session, and a later release by the finalizing
// watchdog's hook is a keyed no-op. When a terminal writer overtook the call,
// the failure write loses, the terminal writer's reclaim owns the pod, and the
// handler revokes any lease under the session and answers 409 with the
// terminal state without releasing the pod.
// spec: §15.1 (finalize row), §6.2 (finalize timeout), §7.2 (terminal states),
// §6.1 (Per-session credential lease lifecycle), §7.1 step 23 (lease release)
func (s *Server) finalizePlanParseFailed(
	w http.ResponseWriter, r *http.Request, tenantID, id, podAssignment, scrubPolicy string, perr error,
) {
	ferr := s.failFinalizing(r.Context(), tenantID, id)
	if isPreconditionError(ferr) {
		s.revokeFinalizeLease(id)
		s.writePreconditionError(w, ferr)
		return
	}
	if ferr != nil {
		log.Printf("sessionserver: fail finalizing session %s after plan parse failure: %v", id, ferr)
		s.reclaimFinalizedPod(r.Context(), podAssignment, id, scrubPolicy == ScrubPolicyPerSlot)
	}
	s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
		"stored workspace plan could not be parsed: "+perr.Error(), nil)
}

// finalizePrepareFailed answers a finalize call whose prepare phase failed.
// The handler writes `failed` only while the row is still `finalizing`. A
// failure write that applied runs the post-terminal hook, whose pre-running
// reclaim releases the pod or the slot by the row's pool kind; after a
// through-Prepare failure the binder's failPhase has already reclaimed the
// exclusive pod, and the hook's release repeats it. The handler revokes the
// session's leases on every outcome, because a partial credential assignment
// can leave a recorded lease that the binder's attempt-scoped release omits,
// and because a failure write that commits and still reports a store error
// runs no hook, which leaves this revoke as the only lease return. A lost failure write means
// a terminal writer overtook the call: the failure is still recorded, and the
// response is 409 with the terminal state in place of the prepare-phase
// error. Otherwise the failure surfaces through writePodClaimError, and a
// workspace-materialization failure takes the retryable
// SESSION_CREATION_FAILED fallback unless it is a §13.4 archive validator
// violation.
// spec: §15.1 (finalize row), §6.2 (finalize timeout), §7.2 (terminal states),
// §6.1 (Per-session credential lease lifecycle), §7.1 step 23 (lease release)
func (s *Server) finalizePrepareFailed(w http.ResponseWriter, r *http.Request, tenantID, id string, err error) {
	ferr := s.failFinalizing(r.Context(), tenantID, id)
	s.revokeFinalizeLease(id)
	if isPreconditionError(ferr) {
		s.recordPodClaimFailure(err)
		s.writePreconditionError(w, ferr)
		return
	}
	if ferr != nil {
		log.Printf("sessionserver: fail finalizing session %s after prepare failure: %v", id, ferr)
	}
	s.writePodClaimError(w, err, "SESSION_CREATION_FAILED", "workspace finalization failed")
}

// finalizeReadyWriteFailed answers a finalize call whose finalizing → ready
// write did not commit cleanly. A lost ready write means a terminal writer
// overtook the call: the handler revokes any lease under the session, deletes
// no claim, and answers 409 with the terminal state, skipping the upload
// channel close, the SSE status change, and the audit row. Any other store
// error is ambiguous, so the handler attempts the guarded failure write and
// branches on its outcome:
//
//   - The failure write commits: its post-terminal hook releases the pod or
//     the slot by the row's pool kind, and the handler answers 500.
//   - The failure write fails with a store error: the ready write may have
//     committed, so the handler releases nothing and answers 500. When the
//     row is still `finalizing`, the finalizing watchdog's terminal write and
//     its hook release the pod.
//   - The failure write loses to a terminal state: a terminal writer ended the
//     session and owns the pod, so the handler revokes any lease and answers
//     409 with that state.
//   - The failure write loses to a non-terminal state: the ready write
//     committed despite its error, and the session holds its pod and lease
//     legitimately, so the handler releases nothing and answers 500 with the
//     ready-write error.
//
// spec: §15.1 (finalize row), §6.2 (finalize timeout), §7.2 (terminal states),
// §6.1 (Per-session credential lease lifecycle), §7.1 step 23 (lease release)
func (s *Server) finalizeReadyWriteFailed(w http.ResponseWriter, r *http.Request, tenantID, id string, err error) {
	if isPreconditionError(err) {
		s.revokeFinalizeLease(id)
		s.writePreconditionError(w, err)
		return
	}
	ferr := s.failFinalizing(r.Context(), tenantID, id)
	var pe *session.PreconditionError
	if errors.As(ferr, &pe) {
		if session.IsTerminal(pe.CurrentState) {
			s.revokeFinalizeLease(id)
			s.writePreconditionError(w, ferr)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if ferr != nil {
		log.Printf("sessionserver: fail finalizing session %s after ready write failure: %v", id, ferr)
	}
	s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
}

// isPreconditionError reports whether err carries the §15.1 precondition
// refusal, which a guarded finalize write returns when it loses to another
// writer's state.
func isPreconditionError(err error) bool {
	var pe *session.PreconditionError
	return errors.As(err, &pe)
}

// reclaimFinalizedPod releases the pod or slot claimed at /create and revokes
// the §4.9 credential lease keyed by sessionID. Its only caller is
// finalizePlanParseFailed, on a failure write that returned a store error,
// where no post-terminal hook ran and no ready write can have committed.
// perSlot is the row's frozen scrubPolicy discriminator: on a
// concurrent-workspace pool ReclaimClaimed removes the session's slot member
// and deletes the shared claim only when that release empties the set, and on
// an exclusive pool it deletes the claim by name. Best-effort: the §4.6.1
// orphan-claim GC backstops a release error.
// spec: §15.1 (finalize row), §6.2 (finalize timeout), §4.6.3 (SandboxClaim: a
// release that ends the occupancy without reuse), §5.2 (Slot assignment
// atomicity), §6.1 (Per-session credential lease lifecycle), §4.6.1
// (orphan-claim GC).
func (s *Server) reclaimFinalizedPod(ctx context.Context, sandboxName, sessionID string, perSlot bool) {
	if s.podBinder == nil || sandboxName == "" {
		return
	}
	if err := s.podBinder.ReclaimClaimed(ctx, sandboxName, sessionID, perSlot); err != nil {
		log.Printf("sessionserver: reclaim finalized pod %s for session %s after finalize failure: %v",
			sandboxName, sessionID, err)
	}
}

// revokeFinalizeLease releases the §4.9 credential leases recorded under the
// session. It is keyed by the session identifier and deletes no claim,
// because podclaim.DeleteClaim has no owner check, and a by-name delete after
// a terminal writer has returned the pod to its pool could remove a successor
// session's claim on that pod. ReleaseSession does nothing when the session
// holds no lease, so a second release after a terminal writer's reclaim is
// harmless.
// spec: §7.1 step 23 (lease release), §4.9
func (s *Server) revokeFinalizeLease(sessionID string) {
	if s.podBinder == nil || s.podBinder.Credentials == nil {
		return
	}
	if err := s.podBinder.Credentials.ReleaseSession(sessionID); err != nil {
		log.Printf("sessionserver: release credential leases for session %s after finalize failure: %v", sessionID, err)
	}
}
