// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/resumechunks"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/derivelock"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// DeriveRequest is the §7.1 / §15.1 POST /v1/sessions/{id}/derive body.
type DeriveRequest struct {
	// RuntimeRef overrides the derived session's runtime. Optional —
	// when blank, the derived session inherits the source's
	// `RuntimeRef`. Production validates this against the
	// ExternalAdapterRegistry per §15.4.
	RuntimeRef string `json:"runtimeRef,omitempty"`

	// TargetPool identifies the `SandboxWarmPool` the derived session
	// runs against. Optional — when blank, the derived session
	// inherits the source's pool. Production resolves this through
	// the registry per §5.3.
	TargetPool string `json:"targetPool,omitempty"`

	// TargetIsolationProfile is the §5.3 isolation profile the derived
	// session is bound to. The minimal gateway accepts this directly
	// from the request body so monotonicity tests can drive the check
	// without a live pool registry; production resolves this from the
	// target pool's `sessionIsolationLevel.isolationProfile`.
	TargetIsolationProfile isolation.Profile `json:"targetIsolationProfile,omitempty"`

	// AllowStale lifts the §7.1 derive rule 1 precondition for
	// non-terminal source sessions. Without this flag, derives from
	// `running`, `suspended`, `resume_pending`, or
	// `awaiting_client_action` are rejected with
	// `409 DERIVE_ON_LIVE_SESSION`.
	AllowStale bool `json:"allowStale,omitempty"`

	// AllowIsolationDowngrade overrides §7.1 derive rule 5 (SEC-001)
	// when the target pool's isolation profile is weaker than the
	// source session's. Requires the caller to hold the
	// `platform-admin` role; non-admin callers carrying this flag
	// receive `403 FORBIDDEN`.
	AllowIsolationDowngrade bool `json:"allowIsolationDowngrade,omitempty"`

	// TicketID is the optional free-text justification echoed into
	// the `derive.isolation_downgrade` audit event per §7.1 derive
	// rule 5.
	TicketID string `json:"ticketId,omitempty"`
}

// DeriveResponse is the §15.1 derive envelope. The caller-visible
// fields are the regular session envelope plus the
// `workspaceSnapshot*` echo per §7.1.
type DeriveResponse struct {
	SessionResponse

	// UploadToken is the §7.1 uploadToken the client supplies on every
	// upload into the derived session until it is finalized, as on a
	// `POST /v1/sessions` response. Treat as a secret per §7.1.
	UploadToken string `json:"uploadToken"`

	// WorkspaceSnapshotSource echoes the §7.1 derive response field.
	WorkspaceSnapshotSource string `json:"workspaceSnapshotSource,omitempty"`

	// WorkspaceSnapshotTimestamp is the moment the snapshot was
	// originally committed to object storage, RFC3339Nano.
	WorkspaceSnapshotTimestamp string `json:"workspaceSnapshotTimestamp,omitempty"`

	// WorkspaceSnapshotContentHash is the §4.5 ll. 311
	// content-addressed identity of the snapshot — SHA-256 of the
	// tar archive, hex-encoded. Empty when the snapshot predates
	// the hash column or no archive content was available at the
	// originating write. Clients verify the derived session owns
	// the same parent bytes by comparing this hash with the
	// parent session's published hash.
	//
	// spec: §4.5.
	WorkspaceSnapshotContentHash string `json:"workspaceSnapshotContentHash,omitempty"`

	// ParentSessionID echoes the source session id for client-side
	// audit / lineage display. The derived row itself carries no
	// delegation parent: it is a new root (§12.6 StoreRouter).
	ParentSessionID string `json:"parentSessionId,omitempty"`
}

// DeriveAuditSink is the §7.1 derive rule 5 audit hook. The gateway
// emits a `derive.isolation_downgrade` event when an
// `allowIsolationDowngrade: true` override is exercised; this
// interface lets the server compose with the audit subsystem without
// importing it directly.
type DeriveAuditSink interface {
	// EmitDeriveIsolationDowngrade records the per-§7.1 derive rule 5
	// admin override. The implementation must be non-blocking — the
	// derive handler does not wait for delivery.
	EmitDeriveIsolationDowngrade(ctx context.Context, event DeriveIsolationDowngradeEvent)
}

// DeriveIsolationDowngradeEvent is the §7.1 derive rule 5 audit
// payload. Field names match the spec for downstream OCSF mapping.
type DeriveIsolationDowngradeEvent struct {
	SourceSessionID        string
	SourceIsolationProfile isolation.Profile
	TargetPool             string
	TargetIsolationProfile isolation.Profile
	AuthorizingUserSubject string
	TicketID               string
	TenantID               string
}

// handleDerive implements POST /v1/sessions/{id}/derive per §7.1 and
// §15.1. The handler runs the per-source-session advisory lock
// acquisition (§7.1), the source-session lookup, the
// precondition state check, the workspace-snapshot resolution, the
// SEC-001 monotonicity check (with platform-admin override), the
// snapshot copy, and the atomic INSERT of the derived session row.
//
// When DeriveLock is wired (production: derivelock.NewRedis), concurrent
// derives on the same source session serialize across replicas; a
// caller that does not acquire within the §7.1 5-second budget
// receives 429 DERIVE_LOCK_CONTENTION. When DeriveLock is nil the
// in-memory store mutex serializes within the running process — the
// single-replica fallback. The CAS-fenced `coordination_generation`
// write for opt-in derive-failure rows
// (`gateway.persistDeriveFailureRows: true`) is tracked under
// F-7.1.14.
func (s *Server) handleDerive(w http.ResponseWriter, r *http.Request) {
	if !s.requireActiveUser(w, r) {
		return
	}
	tenantID := s.resolveTenant(r)
	if !s.requireSessionQuota(w, r, tenantID) {
		return
	}
	if !s.requirePolicyChain(w, r, tenantID) {
		return
	}
	sourceID := r.PathValue("id")

	// §7.1 — acquire the per-source-session advisory lock
	// before reading the workspace snapshot reference. Concurrent
	// derives on the same source session serialize across replicas so
	// no caller observes a torn read while a checkpoint is mid-update.
	// The lock auto-expires after derivelock.DefaultTTL (30s) so a
	// crashed holder cannot block subsequent derives. We release it as
	// soon as the snapshot ref has been read; the copy itself runs
	// without the lock per the spec's "release before the copy is
	// safe" guarantee (each checkpoint write produces a new MinIO
	// object).
	if s.deriveLock != nil {
		release, err := s.deriveLock.Acquire(r.Context(), sourceID)
		if err != nil {
			if errors.Is(err, derivelock.ErrContended) {
				s.writeError(w, http.StatusTooManyRequests, "DERIVE_LOCK_CONTENTION",
					"another derive on this source session is in progress; retry shortly",
					map[string]any{"sourceSessionId": sourceID})
				return
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				s.writeError(w, http.StatusRequestTimeout, "REQUEST_TIMEOUT",
					"derive lock acquisition cancelled before the wait budget elapsed",
					map[string]any{"sourceSessionId": sourceID})
				return
			}
			s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
				"derive lock acquisition failed: "+err.Error(), nil)
			return
		}
		defer release()
	}

	source, err := s.store.Get(r.Context(), tenantID, sourceID)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// spec: §12.8 (Processing restriction during erasure (GDPR Article
	// 18)) — the source session's owner, when restricted, refuses the
	// derive before the isolation-downgrade audit event, the reservation,
	// and the copy, whatever the new owner's and the tenant's markers.
	if !s.requireSourceOwnerNotRestricted(w, r, tenantID, source.UserID) {
		return
	}

	var req DeriveRequest
	if r.ContentLength > 0 {
		body := jsonReader(w, r)
		defer body.Close()
		if err := json.NewDecoder(body).Decode(&req); err != nil {
			s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
			return
		}
	}

	// §15.1 precondition table for /derive: terminal source
	// admitted by default; non-terminal source admitted only when the
	// caller passes `allowStale: true` AND the state is one of
	// {running, suspended, resume_pending, awaiting_client_action}. The
	// table-driven validator centralises that rule. When the rejection is
	// specifically "non-terminal-without-allowStale", §15.1
	// upgrades the wire code from the generic INVALID_STATE_TRANSITION to
	// the dedicated DERIVE_ON_LIVE_SESSION so SDK clients can distinguish
	// "retry with allowStale" from "the source state is not derivable at
	// all" (e.g., created/finalizing/ready/starting/input_required).
	// spec: §15.1; spec: §15.1.
	capabilities := map[session.Capability]bool{}
	if req.AllowStale {
		capabilities[session.CapabilityAllowStaleDerive] = true
	}
	if err := session.Validate(session.PreconditionRequest{
		Endpoint:     session.EndpointDerive,
		CurrentState: source.State,
		Capabilities: capabilities,
	}); err != nil {
		var pe *session.PreconditionError
		if errors.As(err, &pe) {
			if !session.IsTerminal(source.State) && !req.AllowStale {
				s.writeError(w, http.StatusConflict, "DERIVE_ON_LIVE_SESSION",
					"source session is non-terminal; set allowStale: true to derive from the most recent checkpoint",
					map[string]any{"currentState": string(source.State)})
				return
			}
			allowed := make([]string, 0, len(pe.AllowedStates))
			for _, st := range pe.AllowedStates {
				allowed = append(allowed, string(st))
			}
			s.writeError(w, pe.Code(), pe.ErrorCode(), pe.Error(), map[string]any{
				"currentState":  string(source.State),
				"allowedStates": allowed,
			})
			return
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	// §7.1 derive rule 1: source must have a resolvable workspace snapshot.
	if source.WorkspaceSnapshot == nil || source.WorkspaceSnapshot.Ref == "" {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"source session has no resolvable workspace snapshot",
			map[string]any{"fields": []map[string]string{{"field": "sourceSessionId"}}})
		return
	}

	// §7.1 derive rule 5 (SEC-001): isolation monotonicity. The
	// minimal gateway accepts the target profile from the request
	// body; production resolves it from the target pool.
	target := req.TargetIsolationProfile
	if target == "" {
		target = source.IsolationProfile
	}
	if !isolation.IsValid(target) {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			fmt.Sprintf("targetIsolationProfile %q is not a recognised §5.3 profile", target),
			map[string]any{"fields": []map[string]string{{"field": "targetIsolationProfile"}}})
		return
	}

	// Resolve the derived session's runtime. Inherit the source's value
	// when the request omits it, the same default as
	// `replayMode: prompt_history`.
	runtimeRef := req.RuntimeRef
	if runtimeRef == "" {
		runtimeRef = source.RuntimeRef
	}
	// spec: §7.1 (Derive session semantics) derive rule 2, §14.1
	// (CreateSessionRequest.pool), §4 (pool_tenant_access) — the derived row
	// starts in the initial state of a fresh CreateSession and its claim is
	// pinned to targetPool, so a named targetPool passes the same selection
	// check as a create-body pool: 400 VALIDATION_ERROR for a pool that does
	// not exist, is not backed by the runtime, or does not satisfy the target
	// profile, and 403 FORBIDDEN for a pool the tenant holds no grant on. The
	// check runs before the downgrade audit event, the reservation, and the
	// copy, so a rejected pin leaves no trace. A derive that omits targetPool
	// inherits the source's pool and is not re-validated.
	if !s.requirePoolSelectable(w, r, tenantID, runtimeRef, string(target), req.TargetPool, "targetPool") {
		return
	}

	principal, _ := authmw.FromContext(r.Context())
	if !isolation.AtLeastAsRestrictive(target, source.IsolationProfile) {
		// Downgrade requested. Only platform-admin may override.
		if !req.AllowIsolationDowngrade {
			s.writeError(w, http.StatusUnprocessableEntity, "ISOLATION_MONOTONICITY_VIOLATED",
				"target pool's isolation profile is weaker than the source session's",
				map[string]any{
					"sourceIsolationProfile": string(source.IsolationProfile),
					"targetIsolationProfile": string(target),
					"targetPool":             req.TargetPool,
				})
			return
		}
		if !principal.HasRole(pkgauth.RolePlatformAdmin) {
			s.writeError(w, http.StatusForbidden, "FORBIDDEN",
				"allowIsolationDowngrade=true requires the platform-admin role",
				map[string]any{
					"sourceIsolationProfile": string(source.IsolationProfile),
					"targetIsolationProfile": string(target),
				})
			return
		}
		if s.deriveAuditSink != nil {
			s.deriveAuditSink.EmitDeriveIsolationDowngrade(r.Context(), DeriveIsolationDowngradeEvent{
				SourceSessionID:        source.ID,
				SourceIsolationProfile: source.IsolationProfile,
				TargetPool:             req.TargetPool,
				TargetIsolationProfile: target,
				AuthorizingUserSubject: principal.Subject,
				TicketID:               req.TicketID,
				TenantID:               tenantID,
			})
		}
	}

	// spec: §10.2 (Session ownership) rule 2 — a derived session takes its
	// source session's owner. No request field sets it.
	userID := source.UserID
	pool := req.TargetPool
	if pool == "" {
		pool = source.PoolRef
	}

	now := s.clock()
	derivedID := s.idFn()
	// spec: §12.8 (Lifecycle fence and drain) — reserve derivedID for its
	// owner before any object is copied under it. The reservation runs the
	// creation fence over the tenant and the new owner. Every error return
	// below that precedes the insert undoes the copy and releases it.
	if !s.reserveDerivedSession(w, r, tenantID, userID, derivedID) {
		return
	}
	// spec: §7.1 (Derive copy semantics) — derive always copies the
	// parent's snapshot into the derived session's own prefix, and the
	// stored ref names the copied object. A copy that cannot run fails the
	// derive rather than storing a ref with no object behind it. Under the
	// opt-in, a failed copy persists the derive rule 2 derive_failure row
	// (F-15.1.14).
	derivedRef, cerr := s.copyWorkspaceSnapshot(r.Context(), source, derivedID, now)
	if cerr != nil {
		s.undoDerivedWrites(r.Context(), tenantID, derivedID)
		s.persistDeriveFailure(r.Context(), source, derivedID, target, snapshotCopyFailureReason(cerr))
		s.writeSnapshotCopyError(w, cerr, source.WorkspaceSnapshot.Ref)
		return
	}
	// spec: §7.1 — resolve the derived session's pool-derived
	// isolation level once so executionMode + scrubPolicy are persisted
	// alongside isolationProfile (same path as the plain create flow).
	// GET /v1/sessions/{id} on a derived row therefore returns the same
	// rich envelope as a freshly-created row.
	// spec: §7.1 / §14.1 — derive the level from the resolved target pool
	// (req.TargetPool, or the source's pool when omitted) so the derived
	// row's persisted level reflects the pool it binds to. F-CS2 (0018).
	derivedLevel := s.resolveIsolationLevel(r.Context(), runtimeRef, target, pool)
	derived := sessionstore.Session{
		ID:                     derivedID,
		TenantID:               tenantID,
		UserID:                 userID,
		State:                  session.StateCreated,
		CreatedAt:              now,
		UpdatedAt:              now,
		RuntimeRef:             runtimeRef,
		PoolRef:                pool,
		Pool:                   req.TargetPool,
		IsolationProfile:       target,
		ExecutionMode:          derivedLevel.ExecutionMode,
		ScrubPolicy:            derivedLevel.ScrubPolicy,
		ConversationContinuity: derivedLevel.ConversationContinuity,
		WorkspaceSnapshot:      copySnapshotRef(source.WorkspaceSnapshot, derivedRef),
		// spec: §12.6 (StoreRouter), §15.1 (Session Replay Semantics),
		// §4.5 (Workspace lineage) — a derived session is a new root. It
		// carries no delegation parent, so the store makes it its own
		// root_session_id, and its lineage to the source is the
		// parent_workspace_ref below.
		ParentWorkspaceRef: source.WorkspaceSnapshot.Ref,
	}
	// spec: §7.1 (Derive session semantics) derive rule 2, §7.1 (Atomicity
	// of session creation (steps 2–8)) — the derived row is inserted in the
	// same initial state as a fresh CreateSession, so it claims its warm pod
	// (or reserves its slot) before the insert, as POST /v1/sessions does. A
	// claim failure undoes the copy and the reservation and answers the
	// create path's claim-failure envelope with no session_id. The claim
	// runs after the copy so a failed copy holds no pod.
	createClaim, ok := s.claimDerivedRow(w, r, &derived)
	if !ok {
		s.undoDerivedWrites(r.Context(), tenantID, derivedID)
		return
	}
	// spec: §7.1 (Normal Flow) step 8, §7.4 (Upload Safety) — the derived
	// session goes through the standard upload → finalize → start lifecycle,
	// so it carries an uploadToken minted and stored in the same atomic unit
	// as the insert. A mint failure releases the claim, undoes the copy and
	// the reservation, and answers with no session_id.
	uploadToken, ok := s.mintUploadToken(w, r, &derived, createClaim, trace.SpanFromContext(r.Context()))
	if !ok {
		s.undoDerivedWrites(r.Context(), tenantID, derivedID)
		return
	}
	if err := s.store.CreateReserved(r.Context(), derived); err != nil {
		// spec: §7.1 (Atomicity of session creation (steps 2–8)) — every
		// insert failure releases the create-time claim, as the create path
		// does, so no pod leaks past a response that carries no session_id.
		s.rollbackClaim(r.Context(), createClaim, derivedID)
		// spec: §12.8 (Lifecycle fence and drain) — a fence refusal, or a
		// reservation the owner's erasure ended, undoes the copy, releases
		// the reservation, and answers 403. Any other insert error keeps
		// the objects and the reservation, because such an error can
		// follow a committed insert.
		var ref *sessionstore.CreationRefusal
		if errors.As(err, &ref) {
			s.undoDerivedWrites(r.Context(), tenantID, derivedID)
			s.writeCreationRefusal(w, ref)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	resp := DeriveResponse{
		SessionResponse:              toResponse(derived),
		UploadToken:                  uploadToken,
		WorkspaceSnapshotSource:      string(derived.WorkspaceSnapshot.Source),
		WorkspaceSnapshotTimestamp:   derived.WorkspaceSnapshot.Timestamp.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		WorkspaceSnapshotContentHash: derived.WorkspaceSnapshot.ContentHash,
		ParentSessionID:              source.ID,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

// claimDerivedRow runs the create-time claim of claimForNewRow for a row
// that derive or replay is about to insert. A derived or replayed row binds
// no workspace plan at create (its plan binds at finalize, and its workspace
// comes from the copied snapshot at the first bind), so the claim carries an
// empty plan. The response renders the level from the row, on which the
// claim stamps the resolved pool's level, so the returned level is unused.
// On failure it has written the claim-failure envelope and returns ok=false.
// spec: §7.1 (Derive session semantics) derive rule 2; §7.1 (Derive restore
// semantics); §15.1 (Session Replay Semantics).
func (s *Server) claimDerivedRow(w http.ResponseWriter, r *http.Request, row *sessionstore.Session) (*podsession.ClaimResult, bool) {
	_, claim, ok := s.claimForNewRow(w, r, row, workspaceplan.Plan{}, SessionIsolationLevel{}, trace.SpanFromContext(r.Context()))
	return claim, ok
}

// deriveChunkedCheckpoint copies every chunk of the parent's §10.1
// chunked checkpoint into the derived session's own prefix and writes a
// derived manifest row, returning the derived session's
// WorkspaceSnapshot.Ref (the four-segment object-path form
// /{tenant}/checkpoints/{derived_session}/{derived_checkpoint_id} the
// snapshot parsers accept, whose last segment is the derived checkpoint_id).
// The caller has checked that every dependency is wired. A parent ref with
// no manifest row, or a row with no chunk, returns
// errSnapshotRefUnresolvable; a missing chunk object returns the store's
// blobstore.ErrNotFound; any other manifest read, copy, or manifest write
// failure returns that error.
//
// The parent's ref is resolved by its trailing checkpoint_id segment, so
// both the bare-checkpoint_id and four-segment ref forms name the same
// manifest row. The derived checkpoint owns a fresh checkpoint_id and a
// distinct object prefix, so no chunk object is shared with the parent: the
// derived session survives the parent's GC. The manifest is recorded
// complete via the intent-row model (Put → ConfirmChunk →
// Finalise(partial=false)).
//
// spec: §10.1; §7.1 (Derive copy semantics); §7.1 (Derive session
// semantics) derive rule 2; §4.5.
func (s *Server) deriveChunkedCheckpoint(ctx context.Context, source sessionstore.Session, derivedID string, now time.Time) (string, error) {
	parentCheckpointID := checkpointIDFromSnapshotRef(source.WorkspaceSnapshot.Ref)
	parentRec, gerr := s.checkpointManifests.Get(ctx, source.TenantID, parentCheckpointID)
	if errors.Is(gerr, partialmanifeststore.ErrNotFound) {
		return "", errSnapshotRefUnresolvable
	}
	if gerr != nil {
		return "", fmt.Errorf("read manifest of checkpoint %s: %w", parentCheckpointID, gerr)
	}
	if parentRec.ChunkCount <= 0 {
		return "", errSnapshotRefUnresolvable
	}
	encoding := parentRec.ChunkEncoding
	if encoding == "" {
		encoding = partialmanifeststore.ChunkEncodingTarGz
	}
	derivedCheckpointID := s.idFn()
	derivedRef := checkpointSnapshotRef(source.TenantID, derivedID, derivedCheckpointID)

	// spec: §12.5 ll. 295 — copy each chunk through the tenant-scoped
	// boundary so both source and destination URIs are validated against
	// the caller tenant at the store interface. A copy of an already-copied
	// chunk (an idempotent retry) is tolerated via ErrConflict.
	scoped := blobstore.NewTenantScoped(source.TenantID, s.blobs)
	for n := 0; n < parentRec.ChunkCount; n++ {
		srcURI := resumechunks.ChunkObjectURI(source.TenantID, source.ID, parentCheckpointID, uint32(n), encoding)
		dstURI := resumechunks.ChunkObjectURI(source.TenantID, derivedID, derivedCheckpointID, uint32(n), encoding)
		if cerr := scoped.Copy(srcURI, dstURI); cerr != nil && !errors.Is(cerr, blobstore.ErrConflict) {
			return "", cerr
		}
	}

	// Record the derived manifest complete: Put writes the (always partial)
	// intent row, ConfirmChunk advances chunk_count to the copied count
	// (which also keeps the row out of the zero-chunk soft-delete arm), and
	// Finalise stamps partial=false / complete.
	rec := partialmanifeststore.Record{
		TenantID:                    source.TenantID,
		CheckpointID:                derivedCheckpointID,
		SessionID:                   derivedID,
		ManifestReason:              partialmanifeststore.ReasonInProgress,
		ChunkObjectKeyPrefix:        derivedChunkPrefix(source.TenantID, derivedID, derivedCheckpointID),
		ChunkSizeBytes:              parentRec.ChunkSizeBytes,
		ChunkEncoding:               encoding,
		BaselineFullCheckpointBytes: parentRec.BaselineFullCheckpointBytes,
		CheckpointStartedAt:         now,
		CheckpointTimeoutAt:         now,
		CreatedAt:                   now,
	}
	if perr := s.checkpointManifestWriter.Put(ctx, rec); perr != nil {
		return "", perr
	}
	if cerr := s.checkpointManifestWriter.ConfirmChunk(ctx, source.TenantID, derivedCheckpointID,
		parentRec.ChunkCount-1, parentRec.WorkspaceBytesUploaded); cerr != nil {
		return "", cerr
	}
	if ferr := s.checkpointManifestWriter.Finalise(ctx, source.TenantID, derivedCheckpointID,
		false, partialmanifeststore.ReasonComplete); ferr != nil {
		return "", ferr
	}
	return derivedRef, nil
}

// checkpointSnapshotRef builds the four-segment object-path form the
// snapshot-ref parsers accept for a §10.1 chunked checkpoint:
// /{tenant}/checkpoints/{session}/{checkpoint_id}. Its last segment is the
// checkpoint_id the manifest row is keyed by, so a derived session's ref
// round-trips through checkpointIDFromSnapshotRef on the next download or
// re-derive. spec: §10.1; §4.5.
func checkpointSnapshotRef(tenantID, sessionID, checkpointID string) string {
	return fmt.Sprintf("/%s/%s/%s/%s", tenantID, blobstore.ObjectTypeCheckpoint, sessionID, checkpointID)
}

// derivedChunkPrefix is the object-key prefix under which the derived
// session's copied chunks live:
// /{tenant_id}/checkpoints/{derived_session_id}/{derived_checkpoint_id}/.
func derivedChunkPrefix(tenantID, derivedSessionID, derivedCheckpointID string) string {
	return fmt.Sprintf("/%s/%s/%s/%s/", tenantID, blobstore.ObjectTypeCheckpoint, derivedSessionID, derivedCheckpointID)
}

// copySnapshotRef returns a §7.1 derived-snapshot reference at the
// derived session's path, preserving the §4.5 content-addressed hash so
// the derived session can identify the inherited workspace bytes. newRef
// is the ref copyWorkspaceSnapshot returned, which names the object the
// copy wrote.
//
// spec: §4.5 (Artifact Store); §7.1 (Derive copy semantics).
func copySnapshotRef(src *sessionstore.WorkspaceSnapshot, newRef string) *sessionstore.WorkspaceSnapshot {
	if src == nil {
		return nil
	}
	return &sessionstore.WorkspaceSnapshot{
		Ref:         newRef,
		Source:      src.Source,
		Timestamp:   src.Timestamp,
		ContentHash: src.ContentHash,
	}
}

// persistDeriveFailure writes the §7.1 derive rule 2 opt-in
// derive_failure audit row. It is a no-op unless
// `gateway.persistDeriveFailureRows` is enabled. The row is created
// directly in the terminal `failed` state (no intermediate visible
// `created` state) carrying `failureClass = derive_failure` and the
// intended target isolation profile. The row has no delegation parent: it is
// not a node in the source's delegation tree (§15.1), so a ListByRoot of the
// source's tree does not return it. When the source has a workspace snapshot,
// the row records that snapshot's ref as `parent_workspace_ref`, which is
// where its lineage lives (§4.5). No workspace snapshot of its own, pod, or
// credential lease is recorded because none ever existed.
//
// The write is guarded by a §10.1 coordinator-handoff CAS fence inside the
// INSERT transaction (CreateIfGeneration): the store reads the source's
// `coordination_generation` under a shared lock and compares it against the
// stamp captured at derive admission (the generation on the `source` row the
// handler already holds). The fence skips the write whenever the source's
// coordination_generation has advanced since admission: a replacement
// coordinator's handoff (replica crash or Postgres failover mid-copy), and a
// resume_pending → resuming write that started a new resuming incarnation of
// the source. Because the read and the INSERT share one transaction, a bump
// racing the insert cannot leave a row at a stale generation. The write runs
// on a context detached from the request's cancellation, so a client that
// disconnects after the copy failed does not cut the audit row. The row
// records the Terminated fact in its own Create. spec: §7.1 (Derive session
// semantics) derive rule 2 (CAS-fenced INSERT); §15.1. F-15.1.14.
func (s *Server) persistDeriveFailure(ctx context.Context, source sessionstore.Session, derivedID string, target isolation.Profile, reason string) {
	if !s.persistDeriveFailureRows {
		return
	}
	now := s.clock()
	row := sessionstore.Session{
		ID:               derivedID,
		TenantID:         source.TenantID,
		UserID:           source.UserID,
		State:            session.StateFailed,
		FailureClass:     session.FailureClassDeriveFailure,
		FailureReason:    reason,
		RuntimeRef:       source.RuntimeRef,
		IsolationProfile: target,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	// spec: §15.1 (Session Replay Semantics), §4.5 (Workspace lineage) — the
	// audit row is no node in the source's delegation tree ("no tree node is
	// materialised"), so it carries no delegation parent and keeps lineage in
	// parent_workspace_ref when the source has a snapshot.
	if source.WorkspaceSnapshot != nil {
		row.ParentWorkspaceRef = source.WorkspaceSnapshot.Ref
	}
	sessionstore.MarkTerminated(&row, "", now)
	// spec: §7.1 (Derive session semantics) — the CAS fence runs in the
	// INSERT transaction, so a stale replica, or an admission an entry
	// write overtook, persists no row.
	err := s.store.CreateIfGeneration(context.WithoutCancel(ctx), row,
		source.TenantID, source.ID, source.CoordinationGeneration)
	switch {
	case err == nil:
		s.recordDeriveFailureAudit("persisted")
	case errors.Is(err, sessionstore.ErrGenerationAdvanced), errors.Is(err, sessionstore.ErrNotFound):
		s.recordDeriveFailureAudit("fenced")
	default:
		s.recordDeriveFailureAudit("error")
	}
}

// recordDeriveFailureAudit bumps the §16.1
// lenny_session_derive_failure_audit_total{outcome} counter when wired.
// F-15.1.14.
func (s *Server) recordDeriveFailureAudit(outcome string) {
	if s.incDeriveFailureAudit != nil {
		s.incDeriveFailureAudit(outcome)
	}
}
