// SPDX-License-Identifier: MIT

package sessionserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/uploadtoken"
)

// UploadDefaultTTL is the §4.5 TTL the minimal gateway stamps on
// blobs produced by `POST /v1/sessions/{id}/upload`. Spec leaves
// the exact TTL deployer-configurable; the minimal gateway pins it
// at 7 days so the artifact survives the §7.1 default retention
// window of completed sessions.
const UploadDefaultTTL = 7 * 24 * time.Hour

// UploadMaxBodyBytes caps the request body the upload handler will
// admit. The cap matches the §13.4 per-entry ceiling so a single
// uploaded blob cannot exceed the largest archive entry the
// gateway would later accept under uploadArchive. Production
// deployments lower this via the configured tier policy; raising
// past the §13.4 normative cap is prohibited.
//
// The handler uses http.MaxBytesReader so requests that exceed the
// cap fail with `413 PAYLOAD_TOO_LARGE` before the gateway commits
// any blob.
const UploadMaxBodyBytes int64 = 64 * 1024 * 1024 // 64 MiB

// UploadResponse is the §15.1 POST /v1/sessions/{id}/upload reply.
type UploadResponse struct {
	// UploadRef is the §4.5 `lenny-blob://` URI clients pass as
	// `sources[<n>].uploadRef` in a subsequent finalize call.
	UploadRef string `json:"uploadRef"`

	// MimeType echoes the Content-Type the gateway stored alongside
	// the blob, so the client can verify the round-trip.
	MimeType string `json:"mimeType,omitempty"`

	// Size is the byte count the gateway received and stored.
	Size int64 `json:"size"`
}

// handleUpload implements POST /v1/sessions/{id}/upload per §15.1.
//
// The handler:
//
//  1. Looks up the session row in the active tenant.
//  2. Validates the §7.1 uploadToken (`X-Lenny-Upload-Token` header).
//  3. Validates the session is in a state that permits upload.
//  4. Streams the request body into the blob store keyed by a new
//     part_id under the session's tenant+session prefix.
//  5. Returns the resulting `lenny-blob://` URI as `uploadRef`.
//
// Spec gaps the minimal gateway does not yet cover:
//
//   - Per-§7.4 archive parsing (POST /v1/sessions/{id}/upload-archive)
//     — separate handler in a later commit.
//   - Per-§7.1 single-use invalidation at finalize time — the upload
//     handler does not consume the token; the finalize handler will
//     when it ships.
//   - Per-§7.4 mid-session upload (running state) — admitted when
//     the runtime capabilities flag is set; for now the minimal
//     gateway uses the §15.1 precondition table directly (which
//     admits `created` only without the capability lift).
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if s.blobs == nil {
		s.writeError(w, http.StatusServiceUnavailable, "BLOBSTORE_UNAVAILABLE",
			"gateway has no blob store configured", nil)
		return
	}
	tenantID := s.resolveTenant(r)
	id := r.PathValue("id")

	row, err := s.store.Get(r.Context(), tenantID, id)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	// §7.1 uploadToken: validate before any body is read.
	if err := s.verifyUploadToken(r, row.ID); err != nil {
		s.writeUploadTokenError(w, err)
		return
	}

	// §15.1 precondition: upload admitted only in created (and
	// running, when capabilities.midSessionUpload is true; the
	// minimal gateway leaves the capability flag off).
	if err := session.Validate(session.PreconditionRequest{
		Endpoint:     session.EndpointUpload,
		CurrentState: row.State,
	}); err != nil {
		s.writePreconditionError(w, err)
		return
	}

	mimeType := r.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	// §13.4 body cap: refuse uploads that exceed the per-entry
	// ceiling before any blob bytes are committed. http.MaxBytesReader
	// short-circuits Read once the cap is reached.
	body := http.MaxBytesReader(w, r.Body, UploadMaxBodyBytes)
	defer body.Close()

	uri := blobstore.URI{
		TenantID:  tenantID,
		SessionID: row.ID,
		PartID:    blobstore.NewPartID(),
		TTL:       UploadDefaultTTL,
	}
	bytesRead := &countingReader{r: body}
	ref, err := s.blobs.Put(uri, mimeType, bytesRead)
	if err != nil {
		// http.MaxBytesReader surfaces oversize as *http.MaxBytesError.
		// We cannot import that type directly without bringing in
		// net/http internals; the wrapper detects via interface
		// fallthrough.
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			s.writeError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE",
				"upload exceeds the per-blob size cap",
				map[string]any{"maxBytes": UploadMaxBodyBytes})
			return
		}
		if errors.Is(err, blobstore.ErrConflict) {
			// part_id collision — exceptionally rare, but treated as
			// retryable.
			s.writeError(w, http.StatusConflict, "RESOURCE_CONFLICT",
				"blob already exists; retry the upload", nil)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	resp := UploadResponse{
		UploadRef: ref,
		MimeType:  mimeType,
		Size:      bytesRead.n,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

// handleBlob implements GET /v1/blobs/{ref} per §15.1.
//
// The handler:
//
//  1. URL-decodes the path-encoded `lenny-blob://` reference.
//  2. Parses the URI into its tenant/session/part fields.
//  3. Verifies the caller's resolved tenant matches the URI tenant
//     (403 FORBIDDEN on mismatch; never leak existence).
//  4. Streams the blob back with its original Content-Type.
//  5. Returns 404 RESOURCE_NOT_FOUND when the blob is unknown or
//     has expired (the spec collapses the two cases).
func (s *Server) handleBlob(w http.ResponseWriter, r *http.Request) {
	if s.blobs == nil {
		s.writeError(w, http.StatusServiceUnavailable, "BLOBSTORE_UNAVAILABLE",
			"gateway has no blob store configured", nil)
		return
	}
	// The mux is set up to capture `{ref...}` so callers can include
	// the full lenny-blob://… in the path. `r.PathValue` returns the
	// already-URL-decoded segment.
	ref := r.PathValue("ref")
	if ref == "" {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"ref path segment is required", nil)
		return
	}

	uri, err := blobstore.ParseURI(ref)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"ref is not a valid lenny-blob:// URI", map[string]any{
				"reason": err.Error(),
			})
		return
	}

	callerTenant := s.resolveTenant(r)
	if uri.TenantID != callerTenant {
		s.writeError(w, http.StatusForbidden, "FORBIDDEN",
			"caller has no read access to this blob", nil)
		return
	}

	info, body, err := s.blobs.Get(uri)
	if err != nil {
		if errors.Is(err, blobstore.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND",
				"blob not found or expired", nil)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	defer body.Close()

	w.Header().Set("Content-Type", info.MimeType)
	w.Header().Set("X-Lenny-Blob-Size", formatInt64(info.Size))
	w.Header().Set("X-Lenny-Blob-Expires-At", info.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z07:00"))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, body)
}

// verifyUploadToken pulls the `X-Lenny-Upload-Token` header off the
// request and verifies it against the supplied session id. Returns
// nil on success or one of uploadtoken.{ErrInvalid, ErrExpired,
// ErrSessionMismatch, ErrConsumed}.
func (s *Server) verifyUploadToken(r *http.Request, sessionID string) error {
	if s.uploadVerifier == nil {
		// Disabled: tests + the minimal gateway can opt out by not
		// passing a verifier. Production always wires one.
		return nil
	}
	tok := r.Header.Get("X-Lenny-Upload-Token")
	if tok == "" {
		return uploadtoken.ErrInvalid
	}
	_, err := s.uploadVerifier.Verify(tok, sessionID)
	return err
}

func (s *Server) writeUploadTokenError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, uploadtoken.ErrExpired):
		s.writeError(w, http.StatusUnauthorized, "UPLOAD_TOKEN_EXPIRED", "upload token has expired", nil)
	case errors.Is(err, uploadtoken.ErrSessionMismatch):
		s.writeError(w, http.StatusForbidden, "UPLOAD_TOKEN_MISMATCH", "upload token does not match session id", nil)
	case errors.Is(err, uploadtoken.ErrConsumed):
		s.writeError(w, http.StatusGone, "UPLOAD_TOKEN_CONSUMED", "upload token already consumed", nil)
	default:
		s.writeError(w, http.StatusUnauthorized, "UPLOAD_TOKEN_INVALID", err.Error(), nil)
	}
}

// countingReader counts bytes read off r so the upload handler can
// echo the body length in the response without buffering the whole
// body in memory.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func formatInt64(n int64) string {
	return strconv.FormatInt(n, 10)
}
