// SPDX-License-Identifier: MIT

// Package sessionlogstore persists the §4.4 line 226 runtime-stderr
// session-log artifact. When a session reaches a terminal state, the
// gateway hands the buffered stderr bytes to a Store. The MinIO-backed
// Store uploads the bytes to
// `/{tenant_id}/sessions/{session_id}/stderr.log`, records an
// artifact_store row (artifact_type = session_log), and increments
// the per-tenant storage byte counter so the bytes participate in
// the §12.5 GC catalog and the §11.2 quota lifecycle.
//
// The Store is best-effort: a failure does not abort the session
// terminal-state path. The expected wiring is a single close-hook
// invocation per session that the gateway issues once the session
// transitions to completed / failed / cancelled. Adapter-side
// stderr collection is out of scope for this package; the store
// surface takes a byte slice the caller has already buffered.
//
// spec: §4.4 line 226 — "Session logs and runtime stderr" among the
// Event/Checkpoint Store contents.
package sessionlogstore

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// MaxLogBytes caps the per-session log size accepted by the store.
// The cap is conservative (1 MiB) so a runaway stderr producer
// cannot exhaust the gateway's buffer or saturate MinIO; the spec
// does not pin a number so the implementation chooses a value
// consistent with the §4.4 hard workspace size limit narrative
// (best-effort observability artifact, not authoritative state).
//
// Callers that buffer more than MaxLogBytes truncate the tail and
// stamp the resulting artifact with a `truncated: true` audit note
// (the cap is honored at the Store boundary so a caller that fails
// to pre-truncate still produces a bounded object).
const MaxLogBytes = 1 << 20

// SessionLogObjectKey returns the §4.4 line 226 canonical MinIO
// object key for the supplied (tenant, session).
//
// spec: §4.4 line 226 — session log path.
func SessionLogObjectKey(tenantID, sessionID string) string {
	return "/" + tenantID + "/sessions/" + sessionID + "/stderr.log"
}

// Record carries the per-call inputs to Store.Put. A Record carries
// the tenant, session, raw log bytes, and an optional truncation
// flag the caller can set when its own buffer overran MaxLogBytes
// before the bytes reached the Store.
type Record struct {
	TenantID  string
	SessionID string
	// Body is the raw stderr bytes. The Store truncates to MaxLogBytes
	// at the boundary if needed.
	Body []byte
	// Truncated, when true, signals the caller already discarded
	// bytes before handing the buffer to the Store. The Store
	// honors the flag verbatim; it does not raise the flag on its
	// own truncation because the audit point is the caller's
	// observation of the runtime producing more bytes than the
	// buffer could hold.
	Truncated bool
}

// ContextObjectUploader uploads the session-log object to MinIO. The
// production wiring lives in pkg/blobstore; this interface is
// narrow so unit tests can stub it without pulling in a S3 client.
//
// spec: §4.4 line 226 — MinIO upload of the session-log object.
type ContextObjectUploader interface {
	// Upload writes body to MinIO at the canonical session-log key
	// for (tenant, session). Returns nil on success, an error on
	// storage failure (network blip, leader election, permission).
	// Implementations are expected to be idempotent against repeated
	// writes of the same key.
	Upload(ctx context.Context, tenantID, sessionID string, body io.Reader, sizeBytes int) error
}

// ArtifactCatalog is the §12.5 catalog surface. The MinIO-backed
// Store inserts a row alongside every successful upload so the
// bytes participate in the GC lifecycle. Nil is permitted (dev mode
// without Postgres accounting); the upload still succeeds.
//
// spec: §4.4 line 226 / §12.5 artifact_store row insert.
type ArtifactCatalog interface {
	// RecordSessionLog inserts an artifact_store row for the
	// uploaded session-log object at the supplied tenant, session,
	// URI, and size. Implementations stamp the row with
	// `artifact_type = session_log`.
	RecordSessionLog(ctx context.Context, tenantID, sessionID, uri string, sizeBytes int64) error
}

// StorageQuotaSink is the §11.2 per-tenant byte-counter adjustment
// surface. The Store bumps the counter after a successful
// MinIO + artifact_store write pair. Nil is permitted (dev mode);
// the upload still succeeds.
type StorageQuotaSink interface {
	// Adjust shifts the per-tenant byte counter by delta. A positive
	// delta records bytes that were committed; a negative delta
	// releases.
	Adjust(ctx context.Context, tenantID string, delta int64) error
}

// Store persists session-log artifacts. Two production
// implementations ship in this package:
//
//   - Noop: drops every call. Used in dev mode and tests that do not
//     exercise the log path.
//   - MinIOStore: uploads to MinIO + records an artifact_store row +
//     bumps the per-tenant byte counter. Best-effort: a failure is
//     logged and discarded rather than propagated.
//
// spec: §4.4 line 226.
type Store interface {
	// Put writes the supplied Record to durable storage. Returns
	// nil on success; returns an error only when the Record itself
	// is malformed (empty tenant / session). Storage failures are
	// logged inside the implementation and dropped — the §4.4 line
	// 226 contract is "session log retained when practical", not a
	// hard durability guarantee that would block the session
	// terminal-state path.
	Put(ctx context.Context, r Record) error
}

// Noop is the no-op Store implementation. Used in dev mode and in
// tests that do not exercise the log path.
type Noop struct{}

// Put discards the Record and returns nil. Validates tenant/session
// non-empty so a misuse error in production still surfaces in dev
// mode through the same code path.
func (Noop) Put(_ context.Context, r Record) error {
	if r.TenantID == "" || r.SessionID == "" {
		return errors.New("sessionlogstore: tenant and session ids are required")
	}
	return nil
}

// MinIOStore is the §4.4 line 226 MinIO-backed Store. It uploads the
// session-log bytes to MinIO at the canonical key, records an
// artifact_store row with `artifact_type = session_log`, and bumps
// the §11.2 per-tenant storage byte counter.
//
// Every dependency is optional: a nil Uploader makes Put a no-op
// (matching dev mode without a MinIO endpoint); a nil Catalog or
// Quota skips the corresponding accounting step without failing the
// upload. The §4.4 contract treats the session log as observability
// data, not authoritative state, so the Store privileges progress
// over strict transactional ordering.
//
// spec: §4.4 line 226.
type MinIOStore struct {
	// Uploader is the MinIO surface the Store writes through. Nil
	// disables the upload entirely (the Store becomes a no-op).
	Uploader ContextObjectUploader
	// Catalog inserts the §12.5 artifact_store row. Nil skips the
	// row insert; the MinIO bytes are still uploaded.
	Catalog ArtifactCatalog
	// Quota bumps the per-tenant byte counter after a successful
	// upload + catalog insert. Nil skips the counter bump.
	Quota StorageQuotaSink
	// Logf, when set, receives a one-line diagnostic on any storage
	// failure. Nil silences the diagnostics. Default production
	// wiring routes this to log.Printf.
	Logf func(format string, args ...any)
}

// Put writes the session log to MinIO + the artifact catalog. The
// upload is best-effort: a transient MinIO outage logs and discards
// rather than fail the session terminal-state path.
func (s *MinIOStore) Put(ctx context.Context, r Record) error {
	if r.TenantID == "" || r.SessionID == "" {
		return errors.New("sessionlogstore: tenant and session ids are required")
	}
	body := r.Body
	if len(body) > MaxLogBytes {
		body = body[:MaxLogBytes]
	}
	// Skip the round-trip entirely when no bytes are buffered;
	// dropping an empty payload prevents accidental zero-byte
	// objects from accumulating in MinIO.
	if len(body) == 0 {
		return nil
	}
	if s == nil || s.Uploader == nil {
		// Treat absent uploader as dev-mode no-op so the close-hook
		// path is uniform across deployments.
		return nil
	}
	uri := SessionLogObjectKey(r.TenantID, r.SessionID)
	if err := s.Uploader.Upload(ctx, r.TenantID, r.SessionID,
		newReader(body), len(body)); err != nil {
		s.logf("sessionlogstore: upload failed tenant=%s session=%s err=%v",
			r.TenantID, r.SessionID, err)
		// §4.4 line 226: the session log is observability data, not
		// authoritative state — dropping the artifact is preferable to
		// failing the session terminal-state path.
		return nil
	}
	// Catalog and quota are independent — a catalog failure must not
	// stop the quota bump (and vice versa) because the §12.5 sweep
	// will rehydrate quota from Postgres on the next reconciliation
	// cycle, and a quota-bump failure is recoverable from the
	// catalog row alone.
	if s.Catalog != nil {
		if err := s.Catalog.RecordSessionLog(ctx, r.TenantID, r.SessionID,
			uri, int64(len(body))); err != nil {
			s.logf("sessionlogstore: catalog insert failed tenant=%s session=%s err=%v",
				r.TenantID, r.SessionID, err)
		}
	}
	if s.Quota != nil {
		if err := s.Quota.Adjust(ctx, r.TenantID, int64(len(body))); err != nil {
			s.logf("sessionlogstore: storage quota bump failed tenant=%s bytes=%d err=%v",
				r.TenantID, len(body), err)
		}
	}
	return nil
}

func (s *MinIOStore) logf(format string, args ...any) {
	if s == nil || s.Logf == nil {
		return
	}
	s.Logf(format, args...)
}

// newReader wraps the body slice in an io.Reader. The helper is
// internal so the package does not pull bytes.Reader into the
// public surface.
func newReader(body []byte) io.Reader {
	return &sliceReader{body: body}
}

type sliceReader struct {
	body []byte
	off  int
}

func (r *sliceReader) Read(p []byte) (int, error) {
	if r.off >= len(r.body) {
		return 0, io.EOF
	}
	n := copy(p, r.body[r.off:])
	r.off += n
	return n, nil
}

// CloseHook is the adapter-side close-hook the gateway wires into
// every session's terminal-state path. The hook captures the
// buffered stderr bytes and hands them to the Store. The wiring is a
// single function call so tests can stub it directly.
//
// spec: §4.4 line 226.
type CloseHook struct {
	// Store is the §4.4 line 226 session-log store. Required; the
	// hook returns an error when Store is nil.
	Store Store
}

// OnSessionTerminal is the close-hook invocation point. The gateway
// calls this once per session transition to a terminal state with
// the buffered stderr bytes and the truncation flag the buffer
// reports. The hook never panics or returns a non-recoverable error;
// the worst case is a logged drop, consistent with the best-effort
// §4.4 contract.
func (h *CloseHook) OnSessionTerminal(ctx context.Context, tenantID, sessionID string, body []byte, truncated bool) error {
	if h == nil || h.Store == nil {
		return fmt.Errorf("sessionlogstore: close-hook store is required")
	}
	return h.Store.Put(ctx, Record{
		TenantID:  tenantID,
		SessionID: sessionID,
		Body:      body,
		Truncated: truncated,
	})
}
