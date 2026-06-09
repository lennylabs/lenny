// SPDX-License-Identifier: MIT

package sessionserver

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
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
