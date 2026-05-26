// SPDX-License-Identifier: MIT

package sessionserver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/storagequota"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
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

	// ContentHash is the §4.5 ll. 311 content-addressed identity of
	// the uploaded blob — hex-encoded SHA-256 of the streamed bytes.
	// Clients verify storage integrity by comparing this against
	// their locally-computed hash before finalize. Workspace
	// lineage queries also surface this hash via the §15.1 derive
	// response's `workspaceSnapshotContentHash` field.
	//
	// spec: §4.5 line 311.
	ContentHash string `json:"contentHash,omitempty"`

	// IsArchive is true when the blob was uploaded via the §18 line 234
	// POST /v1/sessions/{id}/upload-archive endpoint. The §7.4 archive
	// extraction pipeline (the §4.1 Upload Handler subsystem) inspects
	// this flag to decide whether to parse the body as a tar/tar.gz/zip
	// and apply the §13.4 archive ceilings; a plain /upload blob is
	// stored verbatim and finalized later via an `uploadArchive`
	// workspace-plan source. Extraction itself remains tracked under
	// F-7.4.1 / F-7.4.2 / F-7.4.11 (the in-gateway extraction pipeline
	// and the §13.4 ceiling enforcement).
	IsArchive bool `json:"isArchive,omitempty"`
}

// UploadKind distinguishes the §18 line 234 /upload and /upload-archive
// surfaces. The plain upload stores the body verbatim under
// ObjectTypeUpload; the archive variant tags the response so the §7.4
// extraction pipeline (Upload Handler subsystem) can recognise the
// blob as archive-shaped without inspecting bytes. Storage path and
// breaker accounting are identical in v1 — F-7.4.1 / F-7.4.2 ship the
// in-gateway extraction pipeline that actually parses the archive.
type UploadKind int

const (
	// UploadKindBlob is the plain /upload endpoint. Body is stored as
	// an opaque blob and referenced via a workspace plan `uploadFile`
	// or `uploadArchive` source on finalize.
	UploadKindBlob UploadKind = iota
	// UploadKindArchive is the /upload-archive endpoint. Body is stored
	// the same way as a blob in v1; the §7.4 extraction pipeline will
	// later parse the body in-gateway and apply the §13.4 ceilings.
	UploadKindArchive
)

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
//   - Per-§7.1 single-use invalidation at finalize time — the upload
//     handler does not consume the token; the finalize handler will
//     when it ships.
//   - Per-§7.4 mid-session upload (running state) — admitted when
//     the runtime capabilities flag is set; for now the minimal
//     gateway uses the §15.1 precondition table directly (which
//     admits `created` only without the capability lift).
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	s.runUpload(w, r, UploadKindBlob)
}

// handleUploadArchive implements POST /v1/sessions/{id}/upload-archive
// per §18 line 234. It accepts the same request shape as /upload but
// tags the resulting blob as archive-kind so the §7.4 extraction
// pipeline (F-7.4.1) can recognise it without sniffing bytes. v1
// stores the body unchanged; archive parsing, the §13.4 size /
// decompression-ratio / entry-count ceilings, and the zip-slip guard
// land with the extraction pipeline. spec: §18 line 234.
func (s *Server) handleUploadArchive(w http.ResponseWriter, r *http.Request) {
	s.runUpload(w, r, UploadKindArchive)
}

// runUpload is the shared §15.1 / §18 line 234 upload pipeline that
// backs both /upload and /upload-archive. The kind parameter only
// affects the response envelope's IsArchive flag; the storage path,
// breaker accounting, and §11.2 quota reservation are identical.
func (s *Server) runUpload(w http.ResponseWriter, r *http.Request, kind UploadKind) {
	if s.blobs == nil {
		s.writeError(w, http.StatusServiceUnavailable, "BLOBSTORE_UNAVAILABLE",
			"gateway has no blob store configured", nil)
		return
	}
	// §4.1 Upload Handler subsystem gate. The breaker may be open or
	// the limiter saturated, in which case the handler returns 503
	// SUBSYSTEM_UNAVAILABLE while the Stream Proxy and MCP Fabric keep
	// serving. When no subsystem is configured (tests, minimal
	// gateway), the call is a pass-through.
	var breakerErr error
	if s.uploadSubsystem != nil {
		release, ok := s.acquireUploadSlot(r)
		if !ok {
			s.writeError(w, http.StatusServiceUnavailable, "SUBSYSTEM_UNAVAILABLE",
				"upload handler is temporarily unavailable", nil)
			return
		}
		defer func() {
			release()
			s.recordUploadOutcome(breakerErr)
		}()
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

	// §11.2 storage quota: reserve the declared upload size against the
	// tenant's quota before any blob bytes are committed.
	reservation, ok := s.reserveStorageQuota(w, r, tenantID)
	if !ok {
		return
	}

	// §13.4 body cap: refuse uploads that exceed the per-entry
	// ceiling before any blob bytes are committed. http.MaxBytesReader
	// short-circuits Read once the cap is reached.
	body := http.MaxBytesReader(w, r.Body, UploadMaxBodyBytes)
	defer body.Close()

	// §11.2 hard stream cap: when a quota reservation is held, cap the
	// inbound stream one byte past the headroom so a client that
	// under-declared Content-Length cannot stream past its quota. The
	// extra byte makes an over-stream detectable after the read.
	var src io.Reader = body
	if reservation != nil {
		src = &io.LimitedReader{R: body, N: reservation.headroom + 1}
	}

	// spec: §12.5 ll. 295 — every URI carries the {object_type}
	// segment; the upload handler emits ObjectTypeUpload so the
	// resulting MinIO key sits under `/{tenant}/upload/{session}/{part}`,
	// the §12.5 prefix-scoped GC sweep recognises it as an uploaded
	// file, and the eviction-context prefix (`/{tenant}/eviction/...`)
	// stays distinct.
	uri := blobstore.URI{
		TenantID:   tenantID,
		ObjectType: blobstore.ObjectTypeUpload,
		SessionID:  row.ID,
		PartID:     blobstore.NewPartID(),
		TTL:        UploadDefaultTTL,
	}
	bytesRead := newCountingReader(src)
	ref, err := s.blobs.Put(uri, mimeType, bytesRead)
	if err != nil {
		if reservation != nil {
			// The upload failed: release the whole reservation.
			_ = s.storageQuota.Adjust(r.Context(), tenantID, -reservation.reserved)
		}
		// http.MaxBytesReader surfaces oversize as *http.MaxBytesError.
		// We cannot import that type directly without bringing in
		// net/http internals; the wrapper detects via interface
		// fallthrough.
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			// Client-side error: do NOT count against the breaker.
			s.writeError(w, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE",
				"upload exceeds the per-blob size cap",
				map[string]any{"maxBytes": UploadMaxBodyBytes})
			return
		}
		if errors.Is(err, blobstore.ErrConflict) {
			// part_id collision — exceptionally rare, but treated as
			// retryable. Client-retriable, not a breaker failure.
			s.writeError(w, http.StatusConflict, "RESOURCE_CONFLICT",
				"blob already exists; retry the upload", nil)
			return
		}
		// Downstream blob store failure — feed the §4.1 Upload Handler
		// subsystem breaker so repeated MinIO outages trip it open and
		// new uploads return 503 SUBSYSTEM_UNAVAILABLE.
		breakerErr = err
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	if reservation != nil {
		if bytesRead.n > reservation.headroom {
			// The client streamed past the quota headroom: abort and
			// release the reservation. The orphaned blob ages out on
			// its TTL.
			_ = s.storageQuota.Adjust(r.Context(), tenantID, -reservation.reserved)
			s.writeError(w, http.StatusTooManyRequests, "STORAGE_QUOTA_EXCEEDED",
				"the upload exceeded the tenant's storage quota", nil)
			return
		}
		// Reconcile the reservation to the bytes actually written.
		_ = s.storageQuota.Adjust(r.Context(), tenantID, bytesRead.n-reservation.reserved)
	}

	resp := UploadResponse{
		UploadRef:   ref,
		MimeType:    mimeType,
		Size:        bytesRead.n,
		ContentHash: bytesRead.ContentHash(),
		IsArchive:   kind == UploadKindArchive,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

// acquireUploadSlot reserves a slot in the §4.1 Upload Handler
// subsystem. It returns a release function and true when the breaker
// admitted and a limiter slot was free; otherwise it returns nil and
// false so the caller surfaces a 503 SUBSYSTEM_UNAVAILABLE.
//
// The release function is the only path that touches the breaker's
// outcome: callers track the in-handler error and pass it into the
// returned release closure via s.recordUploadOutcome — a successful
// handler leaves no error; a downstream error feeds the per-subsystem
// breaker. A saturated limiter does NOT count as a downstream
// failure: the limiter rejection is rolled back so the breaker's
// half-open probe slot is not burned by back-pressure.
func (s *Server) acquireUploadSlot(r *http.Request) (release func(), ok bool) {
	if s.uploadSubsystem == nil {
		return func() {}, true
	}
	if s.uploadSubsystem.Breaker != nil && !s.uploadSubsystem.Breaker.Allow() {
		return nil, false
	}
	if s.uploadSubsystem.Limiter == nil {
		// Breaker-only mode: no slot to release; the caller still
		// records the outcome on the breaker via recordUploadOutcome.
		return func() {}, true
	}
	slot, ok := s.uploadSubsystem.Limiter.TryAcquire()
	if !ok {
		// The breaker admitted but the limiter is saturated. Roll
		// the half-open probe back so the limiter rejection does not
		// burn the breaker's one probe slot.
		if s.uploadSubsystem.Breaker != nil {
			s.uploadSubsystem.Breaker.RecordSuccess()
		}
		return nil, false
	}
	return slot, true
}

// recordUploadOutcome feeds the §4.1 Upload Handler subsystem
// breaker with the in-handler outcome. A non-nil err counts as a
// failure; nil counts as a success. Pre-handler validation rejections
// (404 RESOURCE_NOT_FOUND, 412 PRECONDITION_FAILED, 422 unprocessable)
// pass nil so the breaker does not trip on client-side errors.
func (s *Server) recordUploadOutcome(err error) {
	if s.uploadSubsystem == nil || s.uploadSubsystem.Breaker == nil {
		return
	}
	if err != nil {
		s.uploadSubsystem.Breaker.RecordFailure()
		return
	}
	s.uploadSubsystem.Breaker.RecordSuccess()
}

// quotaReservation records a held §11.2 storage-quota reservation: the
// byte count reserved (the declared Content-Length) and the headroom
// the stream may not exceed (the tenant's quota minus prior usage).
type quotaReservation struct {
	reserved int64
	headroom int64
}

// reserveStorageQuota runs the §11.2 pre-upload reservation. It returns
// a non-nil reservation when the tenant has an active storage quota,
// (nil, true) when the tenant is unlimited or the quota is unwired,
// and (nil, false) — after writing the error response — when the
// upload must be rejected.
func (s *Server) reserveStorageQuota(w http.ResponseWriter, r *http.Request, tenantID string) (*quotaReservation, bool) {
	if s.storageQuota == nil || s.tenants == nil {
		return nil, true
	}
	tenant, err := s.tenants.Get(r.Context(), tenantID)
	if errors.Is(err, tenantstore.ErrNotFound) {
		return nil, true
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"storage-quota check failed: "+err.Error(), nil)
		return nil, false
	}
	if tenant.StorageQuotaBytes <= 0 {
		return nil, true // the tenant has no storage limit
	}
	incoming := r.ContentLength
	if incoming < 0 {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"a Content-Length header is required for uploads under a storage quota", nil)
		return nil, false
	}
	priorUsed, err := s.storageQuota.Reserve(r.Context(), tenantID, incoming, tenant.StorageQuotaBytes)
	if errors.Is(err, storagequota.ErrQuotaExceeded) {
		s.writeError(w, http.StatusTooManyRequests, "STORAGE_QUOTA_EXCEEDED",
			"the upload would exceed the tenant's storage quota",
			map[string]any{"currentBytes": priorUsed, "limitBytes": tenant.StorageQuotaBytes})
		return nil, false
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"storage-quota reservation failed: "+err.Error(), nil)
		return nil, false
	}
	return &quotaReservation{reserved: incoming, headroom: tenant.StorageQuotaBytes - priorUsed}, true
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

// countingReader counts bytes read off r and accumulates the SHA-256
// of the streamed body so the upload handler can echo the body
// length and the §4.5 ll. 311 content-addressed hash without
// buffering the whole body in memory.
//
// spec: §4.5 line 311 — "Each workspace snapshot is immutable and
// identified by a content-addressed hash (SHA-256 of the tar
// archive)".
type countingReader struct {
	r io.Reader
	n int64
	h hash.Hash
}

func newCountingReader(r io.Reader) *countingReader {
	return &countingReader{r: r, h: sha256.New()}
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.n += int64(n)
		// hash.Hash.Write never returns a non-nil error per the
		// standard-library contract.
		_, _ = c.h.Write(p[:n])
	}
	return n, err
}

// ContentHash returns the hex-encoded SHA-256 of the streamed bytes
// so far. The caller MUST drain the reader before calling.
func (c *countingReader) ContentHash() string {
	return hex.EncodeToString(c.h.Sum(nil))
}

func formatInt64(n int64) string {
	return strconv.FormatInt(n, 10)
}
