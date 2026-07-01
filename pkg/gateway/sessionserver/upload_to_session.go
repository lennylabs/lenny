// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// UploadToSessionRequest is the body of POST /v1/sessions/{id}/upload-to-session,
// the §7.4 line 433 mid-session upload surface (`upload_to_session`). It
// carries the files to overlay onto the running session's workspace. The
// request uses the caller's normal session-scoped bearer credential; per
// §7.4 no uploadToken is reissued for the mid-session path. spec: §7.4 line
// 433 — F-7.4.6.
type UploadToSessionRequest struct {
	// Files are the files to write into /workspace/current. Each entry is
	// overlaid onto the existing workspace, preserving the agent's other
	// files.
	Files []UploadToSessionFile `json:"files"`
}

// UploadToSessionFile is one file in a mid-session upload.
type UploadToSessionFile struct {
	// Path is the workspace-relative destination. Absolute paths and `..`
	// traversal are rejected (the adapter re-validates containment).
	Path string `json:"path"`
	// Content is the file's bytes, base64-encoded so binary uploads survive
	// the JSON envelope.
	Content string `json:"content"`
	// Mode is the optional octal file mode (e.g. "0644"). Empty defaults to
	// 0644 at the adapter.
	Mode string `json:"mode,omitempty"`
}

// UploadToSessionResponse is the success envelope for a mid-session upload.
type UploadToSessionResponse struct {
	// Status is always "filesUpdated" on success — the files were promoted
	// into /workspace/current and the runtime was signaled.
	Status string `json:"status"`
	// Files is the number of files promoted.
	Files int `json:"files"`
}

// uploadToSessionMaxTotalBytes caps the total decoded payload a single
// mid-session upload may carry. It matches the §13.4 per-entry ceiling the
// pre-start /upload handler enforces (UploadMaxBodyBytes) so a mid-session
// upload cannot exceed the largest entry the gateway accepts pre-start.
const uploadToSessionMaxTotalBytes = UploadMaxBodyBytes

// handleUploadToSession implements POST /v1/sessions/{id}/upload-to-session,
// the §7.4 line 433 mid-session upload operation. It admits an upload into
// an already-running session only when the bound runtime declares
// capabilities.midSessionUpload and the deployer policy
// (MidSessionUploadEnabled) permits it, then pushes the files to the
// session's pod over the existing adapter binding: PrepareWorkspace streams
// the bytes into /workspace/staging and FinalizeWorkspace(midSession)
// overlays them onto /workspace/current and signals the runtime once
// promotion completes, so the agent never sees partially-written files.
// spec: §7.4 line 433 — F-7.4.6.
func (s *Server) handleUploadToSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := s.resolveTenant(r)
	id := r.PathValue("id")

	row, err := s.store.Get(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	// §7.4 line 433: the precondition admits an upload from `running` only
	// when capabilities.midSessionUpload is set AND the deployer policy
	// enables the mid-session surface. The capability is resolved from the
	// bound runtime; an absent runtime registry or a runtime that does not
	// declare the flag leaves the capability off, so the precondition
	// table rejects the running-state upload with INVALID_STATE_TRANSITION.
	caps := s.midSessionUploadCapabilities(ctx, row.RuntimeRef)
	if err := session.Validate(session.PreconditionRequest{
		Endpoint:     session.EndpointUpload,
		CurrentState: row.State,
		Capabilities: caps,
	}); err != nil {
		s.writePreconditionError(w, err)
		return
	}

	plan, uploads, ok := s.parseUploadToSession(w, r)
	if !ok {
		return
	}

	// §7.4 line 433: the upload is pushed to the session's live pod over the
	// adapter binding the coordinating replica holds. A session with no live
	// binding on this replica (single-replica without a pod, or a coordinator
	// handoff that has not re-bound) cannot receive the overlay; surface
	// TARGET_NOT_READY so the client retries against the coordinating replica.
	var bind *podsession.BindResult
	if s.podRegistry != nil {
		if b, okBind := s.podRegistry.Get(row.ID); okBind {
			bind = b
		}
	}
	if bind == nil || bind.Adapter == nil {
		s.writeError(w, http.StatusConflict, "TARGET_NOT_READY",
			"session has no live pod binding on this gateway replica; retry after the session is attached",
			map[string]any{"currentState": string(row.State)})
		return
	}

	// Stream the staged bytes into the pod's /workspace/staging, then overlay
	// them onto /workspace/current. A PrepareWorkspace or FinalizeWorkspace
	// failure leaves the live workspace untouched (the adapter aborts before
	// promotion) — surface it as a transient upstream error.
	if _, err := bind.Adapter.PrepareWorkspace(ctx, row.ID, uploads); err != nil {
		s.emitMidSessionUploadAudit(ctx, row, uploadOutcomeRejected, "stage_failed", "")
		s.writeError(w, http.StatusBadGateway, "UPSTREAM_ERROR",
			"failed to stage mid-session upload onto the pod: "+err.Error(), nil)
		return
	}
	if _, err := bind.Adapter.FinalizeWorkspace(ctx, row.ID, plan, nil, true); err != nil {
		s.emitMidSessionUploadAudit(ctx, row, uploadOutcomeRejected, "materialize_failed", "")
		s.writeError(w, http.StatusBadGateway, "UPSTREAM_ERROR",
			"failed to materialize mid-session upload into the workspace: "+err.Error(), nil)
		return
	}

	s.emitMidSessionUploadAudit(ctx, row, uploadOutcomeAccepted, "", strconv.Itoa(len(plan.GetSources())))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(UploadToSessionResponse{
		Status: "filesUpdated",
		Files:  len(plan.GetSources()),
	})
}

// midSessionUploadCapabilities resolves the §7.4 capability gate for the
// runtime bound to a session: it returns a Capabilities map with
// CapabilityMidSessionUpload set only when the deployer policy is enabled
// AND the runtime declares capabilities.midSessionUpload. Any other case
// (policy off, registry unwired, runtime missing or without the flag)
// returns an empty map, so the precondition table admits only the
// pre-running `created` state. spec: §7.4 line 433 — F-7.4.6.
func (s *Server) midSessionUploadCapabilities(ctx context.Context, runtimeRef string) map[session.Capability]bool {
	caps := map[session.Capability]bool{}
	if !s.midSessionUploadEnabled || s.runtimes == nil || runtimeRef == "" {
		return caps
	}
	rt, err := s.runtimes.Get(ctx, runtimeRef)
	if err != nil {
		return caps
	}
	if rt.Capabilities != nil && rt.Capabilities.MidSessionUpload {
		caps[session.CapabilityMidSessionUpload] = true
	}
	return caps
}

// parseUploadToSession decodes the request body into an adapter WorkspacePlan
// of uploadFile sources and the matching staged-content map keyed by a
// per-file upload ref. It writes the error response and returns ok=false on
// any validation failure (empty body, bad base64, traversal path, oversize).
// spec: §7.4 line 433 / "Enforcement rules" — F-7.4.6.
func (s *Server) parseUploadToSession(w http.ResponseWriter, r *http.Request) (*adapterv1.WorkspacePlan, map[string][]byte, bool) {
	var req UploadToSessionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, uploadToSessionMaxTotalBytes+1024)).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid request body: "+err.Error(), nil)
		return nil, nil, false
	}
	if len(req.Files) == 0 {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "at least one file is required", nil)
		return nil, nil, false
	}

	plan := &adapterv1.WorkspacePlan{SchemaVersion: 1}
	uploads := make(map[string][]byte, len(req.Files))
	var total int64
	for i, f := range req.Files {
		path := strings.TrimSpace(f.Path)
		if path == "" {
			s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
				"file path is required", map[string]any{"index": i})
			return nil, nil, false
		}
		// §7.4 "All paths relative to workspace root; reject `..`, absolute
		// paths, path traversal." The adapter re-validates, but a clean 400
		// here saves a pod round-trip.
		if !validRelWorkspacePath(path) {
			s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
				"file path must be workspace-relative and must not traverse outside the workspace",
				map[string]any{"index": i, "path": path})
			return nil, nil, false
		}
		content, derr := base64.StdEncoding.DecodeString(f.Content)
		if derr != nil {
			s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
				"file content must be base64-encoded", map[string]any{"index": i, "path": path})
			return nil, nil, false
		}
		total += int64(len(content))
		if total > uploadToSessionMaxTotalBytes {
			s.writeError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE",
				"mid-session upload exceeds the total size cap",
				map[string]any{"maxBytes": uploadToSessionMaxTotalBytes})
			return nil, nil, false
		}
		ref := "midupload-" + strconv.Itoa(i)
		uploads[ref] = content
		plan.Sources = append(plan.Sources, &adapterv1.WorkspaceSource{
			Type:      "uploadFile",
			Path:      path,
			UploadRef: ref,
			Mode:      strings.TrimSpace(f.Mode),
		})
	}
	return plan, uploads, true
}

// validRelWorkspacePath reports whether p is a workspace-relative path with
// no leading slash and no `..` traversal segment. It is the gateway-side
// pre-check mirroring the adapter's resolvePath containment guard. spec:
// §7.4 "Enforcement rules" — F-7.4.6.
func validRelWorkspacePath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return false
		}
	}
	return true
}

// emitMidSessionUploadAudit writes a §16.6 session.upload audit row for a
// mid-session upload, mirroring the pre-start /upload audit so SOC tooling
// sees one event class for both surfaces. Best-effort and non-blocking.
// spec: §16.6 line 338; §11.7 — F-7.4.6 / F-7.4.17.
func (s *Server) emitMidSessionUploadAudit(ctx context.Context, row sessionstore.Session, outcome, reason, detail string) {
	if s.lifecycleAudit == nil {
		return
	}
	s.lifecycleAudit.EmitSessionLifecycle(ctx, SessionLifecycleEvent{
		EventType:  auditSessionUpload,
		TenantID:   row.TenantID,
		SessionID:  row.ID,
		UserID:     row.UserID,
		RuntimeRef: row.RuntimeRef,
		State:      string(row.State),
		Outcome:    outcome,
		Reason:     reason,
		Detail:     detail,
		At:         s.clock(),
	})
}
