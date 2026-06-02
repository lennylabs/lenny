// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	pkgauth "github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/derivelock"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
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

	// UserID overrides the derived session's user. Optional — when
	// blank, the derived session inherits the source's userId.
	UserID string `json:"userId,omitempty"`

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
	// spec: §4.5 line 311.
	WorkspaceSnapshotContentHash string `json:"workspaceSnapshotContentHash,omitempty"`

	// ParentSessionID echoes the source session id for client-side
	// audit / lineage display.
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
// acquisition (§7.1 line 92), the source-session lookup, the
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

	// §7.1 line 92 — acquire the per-source-session advisory lock
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

	var req DeriveRequest
	if r.ContentLength > 0 {
		body := jsonReader(w, r)
		defer body.Close()
		if err := json.NewDecoder(body).Decode(&req); err != nil {
			s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
			return
		}
	}

	// §15.1 line 622-624 precondition table for /derive: terminal source
	// admitted by default; non-terminal source admitted only when the
	// caller passes `allowStale: true` AND the state is one of
	// {running, suspended, resume_pending, awaiting_client_action}. The
	// table-driven validator centralises that rule. When the rejection is
	// specifically "non-terminal-without-allowStale", §15.1 line 1052
	// upgrades the wire code from the generic INVALID_STATE_TRANSITION to
	// the dedicated DERIVE_ON_LIVE_SESSION so SDK clients can distinguish
	// "retry with allowStale" from "the source state is not derivable at
	// all" (e.g., created/finalizing/ready/starting/input_required).
	// spec: §15.1 line 622-624; spec: §15.1 line 1052.
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

	// Resolve the derived session's runtime / userId. Inherit the
	// source's values when the request omits them — same default
	// shape as `replayMode: prompt_history`.
	runtimeRef := req.RuntimeRef
	if runtimeRef == "" {
		runtimeRef = source.RuntimeRef
	}
	userID := req.UserID
	if userID == "" {
		userID = source.UserID
	}
	pool := req.TargetPool
	if pool == "" {
		pool = source.PoolRef
	}

	now := s.clock()
	derivedID := s.idFn()
	derivedRef := derivedSnapshotRef(tenantID, derivedID)
	// spec: §4.5 ll. 311 — derive copies the parent's workspace
	// snapshot bytes into a new object scoped to the derived
	// session's prefix so the derived session owns its workspace
	// independent of the parent's GC. When the blob store
	// implements Copier (production MinIO/S3/GCS), the copy runs
	// natively; otherwise we keep the previous ref-rewrite
	// behavior (tests + the minimal gateway).
	if _, ok := s.blobs.(blobstore.Copier); ok && source.WorkspaceSnapshot != nil && source.WorkspaceSnapshot.Ref != "" {
		srcURI, dstURI, ok := parseDeriveURIs(source.WorkspaceSnapshot.Ref, derivedRef, tenantID, source.ID, derivedID)
		if ok {
			// spec: §12.5 ll. 295 — run the snapshot copy through the
			// tenant-scoped boundary so both the parent (src) and derived
			// (dst) URIs are validated against the caller tenant at the
			// store interface rather than trusted from the constructed URI.
			scoped := blobstore.NewTenantScoped(tenantID, s.blobs)
			if err := scoped.Copy(srcURI, dstURI); err != nil && !errors.Is(err, blobstore.ErrConflict) {
				// spec: §7.1 derive rule 2 — the copy was attempted, so
				// under the opt-in this failure persists a terminal
				// derive_failure audit row before the error is returned.
				// F-15.1.14.
				if errors.Is(err, blobstore.ErrNotFound) {
					s.persistDeriveFailure(r.Context(), source, derivedID, target, "derive_snapshot_unavailable")
					s.writeError(w, http.StatusServiceUnavailable, "DERIVE_SNAPSHOT_UNAVAILABLE",
						"parent workspace snapshot object is absent in storage",
						map[string]any{"snapshotRef": source.WorkspaceSnapshot.Ref})
					return
				}
				s.persistDeriveFailure(r.Context(), source, derivedID, target, "workspace_copy_failed")
				s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
					"workspace snapshot copy failed: "+err.Error(), nil)
				return
			}
		}
	}
	// spec: §7.1 line 75 — resolve the derived session's pool-derived
	// isolation level once so executionMode + scrubPolicy are persisted
	// alongside isolationProfile (same path as the plain create flow).
	// GET /v1/sessions/{id} on a derived row therefore returns the same
	// rich envelope as a freshly-created row.
	derivedLevel := s.resolveIsolationLevel(r.Context(), runtimeRef, target)
	derived := sessionstore.Session{
		ID:                 derivedID,
		TenantID:           tenantID,
		UserID:             userID,
		State:              session.StateCreated,
		CreatedAt:          now,
		UpdatedAt:          now,
		RuntimeRef:         runtimeRef,
		PoolRef:            pool,
		IsolationProfile:   target,
		ExecutionMode:      derivedLevel.ExecutionMode,
		ScrubPolicy:        derivedLevel.ScrubPolicy,
		WorkspaceSnapshot:  copySnapshotRef(source.WorkspaceSnapshot, derivedRef),
		ParentSessionID:    source.ID,
		ParentWorkspaceRef: source.WorkspaceSnapshot.Ref,
	}
	if err := s.store.Create(r.Context(), derived); err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	resp := DeriveResponse{
		SessionResponse:              toResponse(derived),
		WorkspaceSnapshotSource:      string(derived.WorkspaceSnapshot.Source),
		WorkspaceSnapshotTimestamp:   derived.WorkspaceSnapshot.Timestamp.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		WorkspaceSnapshotContentHash: derived.WorkspaceSnapshot.ContentHash,
		ParentSessionID:              derived.ParentSessionID,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

// copySnapshotRef returns a §7.1 derived-snapshot reference at the
// derived session's path, preserving the §4.5 ll. 311
// content-addressed hash so the derived session can identify the
// inherited workspace bytes. The minimal gateway elides the MinIO
// copy and simply rewrites the ref; production wires the §4.5 ll.
// 311 byte-copy via blobstore.Copier (see Server.deriveCopier).
//
// spec: §4.5 ll. 311; §7.1 derive copy semantics.
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

// derivedSnapshotRef computes the §4.5 MinIO path for a derived
// snapshot under the derived session's own object prefix.
func derivedSnapshotRef(tenantID, sourceSessionID string) string {
	return fmt.Sprintf("/%s/workspace/derived-from-%s", tenantID, sourceSessionID)
}

// parseDeriveURIs builds the blobstore.URI pair the §4.5 ll. 311
// Copy call needs. The parent's `Ref` may carry either a stored
// `lenny-blob://...` URI or the historical MinIO path; this helper
// resolves whichever form is present. Returns ok=false when the
// reference can't be normalised to a workspace URI (an in-memory
// gateway with no real object storage uses the path form and falls
// through to the legacy ref-rewrite behavior).
//
// spec: §4.5 ll. 311; §7.1 derive copy semantics.
func parseDeriveURIs(parentRef, derivedRef, tenantID, sourceSessionID, derivedSessionID string) (src, dst blobstore.URI, ok bool) {
	if strings.HasPrefix(parentRef, blobstore.Scheme+"://") {
		parsed, err := blobstore.ParseURI(parentRef)
		if err != nil {
			return blobstore.URI{}, blobstore.URI{}, false
		}
		src = parsed
	} else {
		// Path-style reference (e.g. "/acme/workspace/sess/snap.tar").
		// Strip the leading slash if present and pull out the part_id.
		trimmed := strings.TrimPrefix(parentRef, "/")
		parts := strings.SplitN(trimmed, "/", 4)
		if len(parts) < 4 {
			return blobstore.URI{}, blobstore.URI{}, false
		}
		src = blobstore.URI{
			TenantID:   parts[0],
			ObjectType: blobstore.ObjectType(parts[1]),
			SessionID:  parts[2],
			PartID:     parts[3],
			TTL:        blobstore.DerivedSnapshotTTL,
		}
	}
	if src.TenantID != tenantID {
		return blobstore.URI{}, blobstore.URI{}, false
	}
	dst = src
	dst.SessionID = derivedSessionID
	dst.PartID = derivedSnapshotPart(sourceSessionID)
	if dst.TTL == 0 {
		dst.TTL = blobstore.DerivedSnapshotTTL
	}
	return src, dst, true
}

// derivedSnapshotPart is the §4.5 part-id stamp on the derived
// workspace object — a stable identifier so the same source +
// derived pair always names the same object, making the §7.1
// derive idempotent under retry.
func derivedSnapshotPart(sourceSessionID string) string {
	return "derived-from-" + sourceSessionID
}

// persistDeriveFailure writes the §7.1 derive rule 2 opt-in
// derive_failure audit row. It is a no-op unless
// `gateway.persistDeriveFailureRows` is enabled. The row is created
// directly in the terminal `failed` state (no intermediate visible
// `created` state) carrying `failureClass = derive_failure`,
// `parentSessionId = source.ID`, and the intended target isolation
// profile; no workspace snapshot, pod, or credential lease is recorded
// because none ever existed.
//
// The write is guarded by a §10.1 coordinator-handoff CAS fence: the
// source's `coordination_generation` is re-read and compared against the
// stamp captured at derive admission (the generation on the `source` row
// the handler already holds). If a replacement coordinator has
// incremented it (replica crash or Postgres failover mid-copy), the
// stale replica skips the write so no orphan `failed` row becomes
// visible. spec: §7.1 derive rule 2 (CAS-fenced INSERT); §15.1 lines
// 647-663 (reachability). F-15.1.14.
func (s *Server) persistDeriveFailure(ctx context.Context, source sessionstore.Session, derivedID string, target isolation.Profile, reason string) {
	if !s.persistDeriveFailureRows {
		return
	}
	// CAS fence: re-read the source and confirm the coordinator that
	// admitted this derive is still authoritative. A changed generation
	// means a handoff occurred; the stale replica must not write.
	cur, err := s.store.Get(ctx, source.TenantID, source.ID)
	if err != nil || cur.CoordinationGeneration != source.CoordinationGeneration {
		s.recordDeriveFailureAudit("fenced")
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
		ParentSessionID:  source.ID,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := s.store.Create(ctx, row); err != nil {
		s.recordDeriveFailureAudit("error")
		return
	}
	s.recordDeriveFailureAudit("persisted")
}

// recordDeriveFailureAudit bumps the §16.1
// lenny_session_derive_failure_audit_total{outcome} counter when wired.
// F-15.1.14.
func (s *Server) recordDeriveFailureAudit(outcome string) {
	if s.incDeriveFailureAudit != nil {
		s.incDeriveFailureAudit(outcome)
	}
}
