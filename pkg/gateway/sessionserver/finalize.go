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
// A no-body finalize (the pre-upload single-shot path) leaves WorkspacePlan
// nil. The §26.2 line 114 CLI submits the plan here, after the create →
// upload-archive steps have minted the session-scoped uploadRef the plan
// references: the create-time plan cannot name an uploadRef that does not
// exist yet, so the §7.1 decomposed flow binds the plan at finalize
// (step 11, FinalizeWorkspace).
type finalizeRequest struct {
	WorkspacePlan json.RawMessage `json:"workspacePlan,omitempty"`
}

// resolveFinalizePlan reads the optional §14 WorkspacePlan from a finalize
// request body and, when present, validates it for binding onto the
// session row. It returns the canonical JSON to persist, the parser's
// consumer-advisory warnings, whether a plan was supplied, and ok=false
// (with the error response already written) on any validation failure.
//
// The plan reaching finalize references blobs uploaded against this
// session (POST /v1/sessions/{id}/upload-archive), so it closes the
// §26.2↔§15.1 ordering gap: the upload mints a session-scoped uploadRef
// only after the session exists, and the immutable create-time plan
// cannot name it, so the CLI uploads first and binds the plan here.
//
// spec: §7.1 lines 35-37 (step 11 FinalizeWorkspace); §26.2 lines 95-114;
// §14 workspace-plan schema.
func (s *Server) resolveFinalizePlan(w http.ResponseWriter, r *http.Request, tenantID string, row sessionstore.Session) (
	storedJSON json.RawMessage, warnings []workspaceplan.Warning, hasPlan bool, ok bool,
) {
	raw, readOK := s.readFinalizePlanBody(w, r)
	if !readOK {
		return nil, nil, false, false
	}
	if len(raw) == 0 || isJSONNull(raw) {
		return nil, nil, false, true
	}

	// spec: §14 — a session created with a plan already has its workspace
	// sources fixed; finalize cannot silently replace it. Reject so a
	// client that submitted a create-time plan and a finalize plan learns
	// which one binds rather than getting a surprising merge.
	if len(row.WorkspacePlan) > 0 && !isJSONNull(row.WorkspacePlan) {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"session already has a workspace plan; finalize cannot replace it",
			map[string]any{"reason": "plan_already_set"})
		return nil, nil, false, false
	}

	parsed, storedJSON, warns, planOK := s.resolvePlanForCreate(w, r, raw)
	if !planOK {
		return nil, nil, false, false
	}
	// spec: §7.5 line 477 / §5.1 line 76 — the setup-command cap applies to
	// a finalize-bound plan exactly as it does at create.
	if !s.enforceSetupCommandPolicy(w, r, row.RuntimeRef, parsed) {
		return nil, nil, false, false
	}
	// spec: §12.5 line 295 / §13.4 — every uploadRef the finalize plan
	// references must be a blob staged against this very session. This
	// keeps the binding safe by construction: a client cannot finalize its
	// session against another tenant's or another session's staged blob.
	if !s.validateFinalizeUploadRefs(w, tenantID, row.ID, parsed) {
		return nil, nil, false, false
	}
	return storedJSON, warns, true, true
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

// validateFinalizeUploadRefs verifies that every uploadFile / uploadArchive
// source in the finalize plan references a blob staged against this exact
// session. The check parses each uploadRef as a §4.5 lenny-blob:// URI and
// requires its tenant and session segments to match the finalizing
// session. It writes the §15.1 error response and returns false on the
// first foreign or malformed ref.
//
// spec: §12.5 line 295 (tenant-scoped blob namespace); §13.4 (upload
// security).
func (s *Server) validateFinalizeUploadRefs(w http.ResponseWriter, tenantID, sessionID string, plan workspaceplan.Plan) bool {
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
		if uri.TenantID != tenantID || uri.SessionID != sessionID {
			// spec: §12.5 line 295 — the staged blob lives under the
			// session's own tenant+session prefix. A ref into another
			// session's prefix is rejected so finalize cannot bind a plan
			// to a blob the caller did not stage for this session.
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
// upload content into /workspace/staging (PrepareWorkspace), materializes
// /workspace/current with the §7.4 post-promotion symlink re-validation
// (FinalizeWorkspace), runs the plan's setup commands (RunSetup), and assigns
// the §4.9 credential lease (AssignCredentials), recording the §6.3
// workspace_materialization / setup_commands / credential_assignment phase
// timings.
//
// On any failure the create-time pod is reclaimed via the §6.2 pre-attached
// disposition so no pod leaks past a finalize-barrier failure (§4.3). A
// through-Prepare failure (workspace validation, setup command, or lease
// assignment) is reclaimed by the binder's lease-aware failPhase, which revokes
// the lease when AssignCredentials had already run. A pre-Prepare failure (pool
// resolution, or a credential source that was available at the create-time
// pre-check but gone by finalize) never engages the binder, so this function
// reclaims the claimed pod itself before returning; no lease is assigned yet, so
// the revoke is a no-op. A finalize-time credential availability miss is the
// §4.9 line 1220 check-to-assignment mismatch (the source vanished across the
// upload window), so it is remapped to CREDENTIAL_POOL_EXHAUSTED rather than the
// create-only USER_CREDENTIAL_NOT_FOUND (§7.6). The returned error is the
// corresponding workspace-validation, setup-command, or credential error for
// handleFinalize to surface (writePodClaimError).
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
// spec: §7.1 steps 11-13; §7.4 lines 434, 450, 459, 461; §4.9 (finalize lease
// assignment); §4.3, §4.6 (proposal); §6.3 lines 358, 372.
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
		// spec: §4.3 (proposal: finalize-failure reclaim) / §6.2 — a pre-Prepare
		// failure (pool resolution) never engages the binder, so its internal
		// failPhase reclaim cannot run. Reclaim the create-time pod here so a
		// pool that was deleted during the upload window does not leak the claim
		// past the §4.6.1 orphan-claim GC timeout.
		s.reclaimFinalizedPod(ctx, row.PodAssignment, row.ID)
		return nil, err
	}
	// spec: §5.2 — service mode is claimless and materializes no workspace;
	// a concurrent-workspace pool reserves a slot at create and materializes +
	// launches it together at /start (BindReservedSlot), so neither is prepared
	// at finalize. Both finalize by a plain state transition.
	if match.ExecutionMode == string(runtimestore.ExecutionModeService) || match.MaxConcurrentSessions > 1 {
		return nil, nil
	}
	// spec: §4.9 lines 1216-1218 / §7.1 step 12 — the credential lease is
	// assigned at finalize (the proposal's deviation from the literal step-6
	// ordering), so resolve the per-provider pool map here from the persisted
	// row. The create-time pre-check already confirmed availability; this
	// resolves the assignment inputs the prepare phase's AssignCredentials uses.
	credPools, userCredProviders, err := s.resolveCredentialPools(ctx, row)
	if err != nil {
		// spec: §4.3 (proposal: finalize-failure reclaim) / §6.2 — the
		// resolution ran before the binder engaged, so reclaim the create-time
		// pod here (no lease is assigned yet, so the revoke is a no-op).
		s.reclaimFinalizedPod(ctx, row.PodAssignment, row.ID)
		// spec: §7.6 line 153 (proposal) — a credential source that was
		// available at the create-time pre-check but is gone at finalize is the
		// check-to-assignment mismatch the proposal requires to surface as
		// CREDENTIAL_POOL_EXHAUSTED at /finalize, not the create-only
		// USER_CREDENTIAL_NOT_FOUND (404) or pre-claim envelope. The mismatch
		// window spans the whole create → upload → finalize window (§4.9), and
		// the lenny_credential_preclaim_mismatch_total counter records the same
		// pre-check-passes-then-assignment-fails event, now observed at finalize.
		return nil, mapFinalizeCredentialMismatch(err)
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
// availability miss into the §4.9 line 1220 check-to-assignment mismatch so
// writePodClaimError surfaces it as CREDENTIAL_POOL_EXHAUSTED (assignment_race)
// and increments lenny_credential_preclaim_mismatch_total, rather than the
// create-only USER_CREDENTIAL_NOT_FOUND (404) or pre-claim CREDENTIAL_POOL_EXHAUSTED
// envelopes.
//
// The §7.6 division is "check at create, assignment at finalize": a credential
// source present at the create-time pre-check that is gone by finalize is the
// race the proposal attributes to the upload window, not a create-time
// not-found. Both credrouter sentinels (ErrUserCredentialNotFound, which the
// without-fallback create-time pre-check already rejects before any pod is
// claimed, and ErrNoCredentialAvailable) collapse to the same finalize-time
// mismatch; any other error (a store read failure, a proxy-dialect mismatch)
// is returned unchanged so it keeps its own envelope.
//
// spec: §4.9 line 1220 (check-to-assignment race); §7.3 line 138, §7.6 line 153
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
// republishes the §7.4 line 459 strip-skip advisories on the §7.2 SSE stream.
// The persists mirror the /start launch path (registerBinding), moved to
// finalize because the prepare phase now runs there. They are best-effort: a
// store failure leaves the in-memory state authoritative and does not block
// the finalize from reaching ready.
//
// spec: §7.5 line 475 (setup output), §7.3 line 408 (workspace root), §7.4
// line 459 (strip-skip advisories); §4.3 (proposal).
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
	// spec: §7.3 line 408 — capture the adapter's negotiated workspace root so a
	// later Resume can assert the replacement pod's WorkspaceRoot matches.
	s.persistWorkspaceRoot(ctx, resultTenantID, resultSessionID, prep.WorkspaceRoot)
	// spec: §7.4 line 459 — republish each strip-components-skip advisory the
	// gateway and adapter raised during materialization on the per-session SSE
	// bus so a client can audit the skipped archive entries.
	s.publishWorkspacePlanWarnings(resultTenantID, resultSessionID, prep.WorkspacePlanWarnings)
}

// reclaimFinalizedPod releases the pod claimed at /create and revokes the §4.9
// credential lease the §4.3 prepare phase assigned, for a Gap-2 finalize
// failure that occurs AFTER AssignCredentials succeeded (a failed
// finalizing → ready transition or a failed single-use upload-token consume).
// ReclaimClaimed deletes the per-pod SandboxClaim and revokes the lease keyed
// by sessionID, so a post-assignment finalize failure does not leak either.
// Best-effort: the §4.6.1 orphan-claim GC backstops a release error.
// spec: §4.3 (Gap 2), §7.1 step 23 (lease release), §4.6.1 (orphan-claim GC).
func (s *Server) reclaimFinalizedPod(ctx context.Context, sandboxName, sessionID string) {
	if s.podBinder == nil || sandboxName == "" {
		return
	}
	if err := s.podBinder.ReclaimClaimed(ctx, sandboxName, sessionID); err != nil {
		log.Printf("sessionserver: reclaim finalized pod %s for session %s after Gap-2 finalize failure: %v",
			sandboxName, sessionID, err)
	}
}
