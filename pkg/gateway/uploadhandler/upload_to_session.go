// SPDX-License-Identifier: MIT

package uploadhandler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
)

// UploadToSessionRequest is the body of POST /v1/sessions/{id}/upload-to-session,
// the §7.4 mid-session upload surface (`upload_to_session`). It
// carries the files to overlay onto the running session's workspace. The
// request uses the caller's bearer; per
// §7.4 no uploadToken is reissued for the mid-session path. spec: §7.4 — F-7.4.6.
type UploadToSessionRequest struct {
	// Files are the files to write into the session's workspace root. Each entry is
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
	// into the session's workspace root and the runtime was signaled.
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
// the §7.4 mid-session upload operation. It admits an upload into
// an already-running session only when the bound runtime declares
// capabilities.midSessionUpload and the deployer policy
// (MidSessionUploadEnabled) permits it, then writes the files into the
// session's workspace through GWSEAM-WriteWorkspace, which pushes them to
// the session's pod over the existing adapter binding so the agent never
// sees partially-written files. A replica that does not coordinate the
// session forwards the request to the coordinating replica before it reads
// the body.
// spec: §7.4 — F-7.4.6; §29.3 (Interactive message send) Forwarding.
func (h *Handler) handleUploadToSession(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tenantID := h.identity.RequestTenant(r)
	id := r.PathValue("id")

	row, err := h.sessions.LookupSession(ctx, tenantID, id)
	if err != nil {
		if errors.Is(err, seam.ErrSessionNotFound) {
			h.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
			return
		}
		h.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	// §7.4: the precondition admits an upload from `running` only
	// when capabilities.midSessionUpload is set AND the deployer policy
	// enables the mid-session surface. The capability is resolved from the
	// bound runtime; an absent runtime registry or a runtime that does not
	// declare the flag leaves the capability off, so the precondition
	// table rejects the running-state upload with INVALID_STATE_TRANSITION.
	caps := h.midSessionUploadCapabilities(ctx, row.RuntimeRef)
	if err := session.Validate(session.PreconditionRequest{
		Endpoint:     session.EndpointUpload,
		CurrentState: session.State(row.State),
		Capabilities: caps,
	}); err != nil {
		h.writePreconditionError(w, err)
		return
	}
	// spec: §29.3 (Interactive message send) Forwarding rule 1 — a replica
	// that does not coordinate the running session forwards the upload, its
	// body unread, so the coordinator pushes the files to the pod.
	if h.forwarding != nil && h.forwarding.ForwardRequest(w, r, row, seam.ForwardUpload, r.Body) != seam.ForwardLocal {
		return
	}

	files, ok := h.parseUploadToSession(w, r)
	if !ok {
		return
	}
	h.writeMidSessionUpload(w, r, row, files)
}

// writeMidSessionUpload writes a parsed mid-session upload into the
// session's workspace through GWSEAM-WriteWorkspace and answers the client.
// A session with no live binding on this replica (single-replica without a
// pod, or a coordinator handoff that has not re-bound) cannot receive the
// overlay, so it answers TARGET_NOT_READY and the client retries against
// the coordinating replica. A staging or promotion failure leaves the live
// workspace untouched, so it answers a transient upstream error and audits
// a rejected upload naming the phase that failed.
// spec: §7.4 — F-7.4.6.
func (h *Handler) writeMidSessionUpload(w http.ResponseWriter, r *http.Request, row seam.Session, files []seam.WorkspaceFile) {
	ctx := r.Context()
	written, err := 0, error(seam.ErrNoLiveBinding)
	if h.workspaces != nil {
		written, err = h.workspaces.WriteWorkspace(ctx, row.ID, files)
	}
	var wwe *seam.WorkspaceWriteError
	switch {
	case errors.Is(err, seam.ErrNoLiveBinding):
		h.writeError(w, http.StatusConflict, "TARGET_NOT_READY",
			"session has no live pod binding on this gateway replica; retry after the session is attached",
			map[string]any{"currentState": string(row.State)})
		return
	case errors.As(err, &wwe) && wwe.Phase == seam.WorkspacePromotion:
		h.emitMidSessionUploadAudit(ctx, row, seam.UploadRejected, "materialize_failed", "")
		h.writeError(w, http.StatusBadGateway, "UPSTREAM_ERROR",
			"failed to materialize mid-session upload into the workspace: "+wwe.Err.Error(), nil)
		return
	case err != nil:
		// A staging failure, and any other failure before promotion,
		// leaves the live workspace untouched.
		cause := err
		if errors.As(err, &wwe) {
			cause = wwe.Err
		}
		h.emitMidSessionUploadAudit(ctx, row, seam.UploadRejected, "stage_failed", "")
		h.writeError(w, http.StatusBadGateway, "UPSTREAM_ERROR",
			"failed to stage mid-session upload onto the pod: "+cause.Error(), nil)
		return
	}

	h.emitMidSessionUploadAudit(ctx, row, seam.UploadAccepted, "", strconv.Itoa(written))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(UploadToSessionResponse{
		Status: "filesUpdated",
		Files:  written,
	})
}

// midSessionUploadCapabilities resolves the §7.4 capability gate for the
// runtime bound to a session: it returns a Capabilities map with
// CapabilityMidSessionUpload set only when the deployer policy is enabled
// AND the runtime declares capabilities.midSessionUpload. Any other case
// (policy off, registry unwired, runtime missing or without the flag)
// returns an empty map, so the precondition table admits only the
// pre-running `created` state. spec: §7.4 — F-7.4.6.
func (h *Handler) midSessionUploadCapabilities(ctx context.Context, runtimeRef string) map[session.Capability]bool {
	caps := map[session.Capability]bool{}
	if !h.midSessionUploadEnabled || h.runtimes == nil || runtimeRef == "" {
		return caps
	}
	rt, err := h.runtimes.Get(ctx, runtimeRef)
	if err != nil {
		return caps
	}
	if rt.Capabilities != nil && rt.Capabilities.MidSessionUpload {
		caps[session.CapabilityMidSessionUpload] = true
	}
	return caps
}

// parseUploadToSession decodes the request body into the files of a
// workspace write, each with its decoded content and trimmed mode. It writes
// the error response and returns ok=false on any validation failure (empty
// body, bad base64, traversal path, oversize).
// spec: §7.4 / "Enforcement rules" — F-7.4.6.
func (h *Handler) parseUploadToSession(w http.ResponseWriter, r *http.Request) ([]seam.WorkspaceFile, bool) {
	var req UploadToSessionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, uploadToSessionMaxTotalBytes+1024)).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid request body: "+err.Error(), nil)
		return nil, false
	}
	if len(req.Files) == 0 {
		h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "at least one file is required", nil)
		return nil, false
	}

	files := make([]seam.WorkspaceFile, 0, len(req.Files))
	var total int64
	for i, f := range req.Files {
		path := strings.TrimSpace(f.Path)
		if path == "" {
			h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
				"file path is required", map[string]any{"index": i})
			return nil, false
		}
		// §7.4 "All paths relative to workspace root; reject `..`, absolute
		// paths, path traversal." The adapter re-validates, but a clean 400
		// here saves a pod round-trip.
		if !validRelWorkspacePath(path) {
			h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
				"file path must be workspace-relative and must not traverse outside the workspace",
				map[string]any{"index": i, "path": path})
			return nil, false
		}
		content, derr := base64.StdEncoding.DecodeString(f.Content)
		if derr != nil {
			h.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
				"file content must be base64-encoded", map[string]any{"index": i, "path": path})
			return nil, false
		}
		total += int64(len(content))
		if total > uploadToSessionMaxTotalBytes {
			h.writeError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE",
				"mid-session upload exceeds the total size cap",
				map[string]any{"maxBytes": uploadToSessionMaxTotalBytes})
			return nil, false
		}
		files = append(files, seam.WorkspaceFile{
			Path:    path,
			Content: content,
			Mode:    strings.TrimSpace(f.Mode),
		})
	}
	return files, true
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
// spec: §16.6; §11.7 — F-7.4.6 / F-7.4.17.
func (h *Handler) emitMidSessionUploadAudit(ctx context.Context, row seam.Session, outcome, reason, detail string) {
	if h.audit == nil {
		return
	}
	h.audit.AuditUpload(ctx, seam.UploadAuditEvent{
		Session: row,
		Outcome: outcome,
		Reason:  reason,
		Detail:  detail,
	})
}
