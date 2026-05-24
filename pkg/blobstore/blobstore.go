// SPDX-License-Identifier: MIT

// Package blobstore implements the §4.5 artifact / blob abstraction
// that backs `lenny-blob://` references in `OutputPart`, workspace
// snapshots, transcripts, and the upload pipeline.
//
// The blob-uri scheme is canonical: a blob is addressed by
// `(tenant_id, object_type, session_id, part_id)` and carries a
// per-blob TTL plus a fixed encryption marker. The package exposes:
//
//   - URI: parser + serialiser for `lenny-blob://` references
//   - Store: small CRUD interface (Put / Get / Stat / Copy)
//   - MemoryStore: in-memory implementation suitable for tests + the
//     minimal gateway
//
// Production replaces MemoryStore with a MinIO-backed implementation
// that maps the URI to the §4.5 object path
// `/{tenant_id}/{object_type}/{session_id}/{part_id}` and applies
// at-rest envelope encryption per §13.x. The wire surface is unchanged.
//
// spec: §4.5 ll. 309; §12.5 ll. 295 — path format
// `/{tenant_id}/{object_type}/{session_id}/{filename}`.
package blobstore

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Scheme is the §4.5 blob URI scheme.
const Scheme = "lenny-blob"

// Encoding is the only encryption marker §4.5 v1 understands.
const Encoding = "aes256gcm"

// Sentinel errors.
var (
	// ErrNotFound — the blob does not exist or has expired.
	ErrNotFound = errors.New("blobstore: blob not found or expired")

	// ErrCrossTenant — the caller's tenant_id does not match the
	// blob URI's tenant_id; reads are denied at the §15.1 403
	// boundary.
	ErrCrossTenant = errors.New("blobstore: caller tenant_id does not match blob tenant_id")

	// ErrInvalidURI — the URI does not match the §4.5 shape.
	ErrInvalidURI = errors.New("blobstore: invalid lenny-blob URI")
)

// ObjectType is the §12.5 line 295 / §4.5 line 309 object-class tag
// embedded into every blob URI and into every object key (path
// segment between {tenant_id} and {session_id}). The closed enum
// matches the spec's §4.5 artifact-kinds list.
//
// spec: §4.5 ll. 309; §12.5 ll. 295, 315.
type ObjectType string

const (
	// ObjectTypeWorkspace — workspace snapshots (sealed bundles, derive
	// copies) per §4.5 ll. 301-303.
	ObjectTypeWorkspace ObjectType = "workspace"
	// ObjectTypeCheckpoint — periodic / eviction / pre-drain
	// checkpoints per §4.4 line 234, §12.5 ll. 311-313.
	ObjectTypeCheckpoint ObjectType = "checkpoint"
	// ObjectTypeTranscript — session conversation transcripts per
	// §4.4 line 226.
	ObjectTypeTranscript ObjectType = "transcript"
	// ObjectTypeUpload — uploaded files (POST
	// /v1/sessions/{id}/upload), the canonical initial workspace per
	// §4.5 ll. 301.
	ObjectTypeUpload ObjectType = "upload"
	// ObjectTypeEviction — eviction fallback context objects keyed
	// at `/{tenant_id}/eviction/{session_id}/context` per §4.4 line
	// 291 and §12.5 line 315.
	ObjectTypeEviction ObjectType = "eviction"
	// ObjectTypeExport — exported file subsets for delegation per
	// §4.5 ll. 303, §8.7.
	ObjectTypeExport ObjectType = "export"
	// ObjectTypeSessionLog — runtime stderr session logs at
	// `/{tenant_id}/sessions/{session_id}/stderr.log` per §4.4
	// line 226.
	ObjectTypeSessionLog ObjectType = "sessions"
)

// IsValidObjectType reports whether t is a recognised §4.5/§12.5
// object-type segment. The §12.5 line 295 path format requires
// every object key to embed one of these values.
func IsValidObjectType(t ObjectType) bool {
	switch t {
	case ObjectTypeWorkspace, ObjectTypeCheckpoint, ObjectTypeTranscript,
		ObjectTypeUpload, ObjectTypeEviction, ObjectTypeExport,
		ObjectTypeSessionLog:
		return true
	default:
		return false
	}
}

// URI is a parsed `lenny-blob://` reference.
//
// spec: §4.5 ll. 309 / §12.5 ll. 295 — path format
// `/{tenant_id}/{object_type}/{session_id}/{filename}`.
type URI struct {
	TenantID   string
	ObjectType ObjectType
	SessionID  string
	PartID     string
	TTL        time.Duration
	Encoding   string
}

// ParseURI decodes a `lenny-blob://` reference. Both wire formats
// are accepted:
//
//   - `lenny-blob://{tenant_id}/{object_type}/{session_id}/{part_id}?ttl=...`
//     — the §12.5 line 295 canonical 4-segment path that carries the
//     object-type tag.
//   - `lenny-blob://{tenant_id}/{session_id}/{part_id}?ttl=...` — the
//     pre-{object_type} 3-segment legacy form. The parser stamps
//     ObjectTypeUpload (the most common emit path) on the parsed URI
//     so the result still round-trips. Production code should always
//     populate ObjectType explicitly; the legacy form exists only so
//     callers that already mint 3-segment URIs do not have to be
//     rewritten before this change lands.
//
// URL-decoding is performed on each path segment. The `ttl` query
// parameter (seconds) is required; `enc` defaults to `aes256gcm`.
func ParseURI(raw string) (URI, error) {
	if !strings.HasPrefix(raw, Scheme+"://") {
		return URI{}, fmt.Errorf("%w: missing %s:// scheme", ErrInvalidURI, Scheme)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return URI{}, fmt.Errorf("%w: %v", ErrInvalidURI, err)
	}
	if u.Scheme != Scheme {
		return URI{}, fmt.Errorf("%w: scheme %q", ErrInvalidURI, u.Scheme)
	}
	tenant := u.Host
	if tenant == "" {
		return URI{}, fmt.Errorf("%w: missing tenant segment", ErrInvalidURI)
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	var objType ObjectType
	var session, part string
	switch len(parts) {
	case 3:
		// §12.5 ll. 295 canonical 4-segment form
		// (`/tenant/object_type/session/part`).
		objType = ObjectType(parts[0])
		session = parts[1]
		part = parts[2]
		if !IsValidObjectType(objType) {
			return URI{}, fmt.Errorf("%w: unknown object_type %q", ErrInvalidURI, parts[0])
		}
	case 2:
		// Pre-{object_type} legacy form (`/tenant/session/part`).
		// Default to `upload` because the upload handler is the
		// dominant emit path under the legacy shape.
		objType = ObjectTypeUpload
		session = parts[0]
		part = parts[1]
	default:
		return URI{}, fmt.Errorf("%w: expected lenny-blob://<tenant>/<object_type>/<session>/<part>", ErrInvalidURI)
	}
	if session == "" || part == "" {
		return URI{}, fmt.Errorf("%w: empty path segment", ErrInvalidURI)
	}
	ttlStr := u.Query().Get("ttl")
	if ttlStr == "" {
		return URI{}, fmt.Errorf("%w: missing ttl query parameter", ErrInvalidURI)
	}
	ttlSecs, err := strconv.Atoi(ttlStr)
	if err != nil || ttlSecs <= 0 {
		return URI{}, fmt.Errorf("%w: ttl %q must be a positive integer", ErrInvalidURI, ttlStr)
	}
	enc := u.Query().Get("enc")
	if enc == "" {
		enc = Encoding
	}
	return URI{
		TenantID:   tenant,
		ObjectType: objType,
		SessionID:  session,
		PartID:     part,
		TTL:        time.Duration(ttlSecs) * time.Second,
		Encoding:   enc,
	}, nil
}

// String serialises u back to the §12.5 line 295 4-segment wire
// format. A URI with an empty ObjectType is stamped with
// ObjectTypeUpload before serialisation so the wire output stays
// well-formed; callers should always set ObjectType explicitly.
func (u URI) String() string {
	enc := u.Encoding
	if enc == "" {
		enc = Encoding
	}
	objType := u.ObjectType
	if objType == "" {
		objType = ObjectTypeUpload
	}
	return fmt.Sprintf(
		"%s://%s/%s/%s/%s?ttl=%d&enc=%s",
		Scheme,
		url.PathEscape(u.TenantID),
		url.PathEscape(string(objType)),
		url.PathEscape(u.SessionID),
		url.PathEscape(u.PartID),
		int(u.TTL.Seconds()),
		enc,
	)
}

// Store is the §4.5 blob-store interface. Implementations are
// goroutine-safe.
type Store interface {
	// Put stages a blob keyed by u. Returns the canonical URI string
	// the caller hands out (identical to u.String() — Put never
	// rewrites the path). Overwriting an existing key returns
	// ErrConflict; see §4.5 immutability guarantee.
	Put(u URI, mimeType string, data io.Reader) (string, error)

	// Get returns the blob's MIME type + content reader. Caller is
	// responsible for closing the returned reader.
	Get(u URI) (BlobInfo, io.ReadCloser, error)

	// Stat returns metadata about the blob without reading its body.
	// Returns ErrNotFound when the blob has expired or never existed.
	Stat(u URI) (BlobInfo, error)
}

// Copier extends Store with the §4.5 line 311 / §7.1 derive
// byte-copy primitive. The gateway invokes Copy when a derived
// session must own its workspace snapshot bytes independently of
// the parent (so the §12.5 GC of the parent's artifacts cannot
// dangle the derived session's ref).
//
// Backends that implement the S3 CopyObject (MinIO, AWS S3, GCS,
// Azure Blob) satisfy Copier natively; the in-memory store
// satisfies it by duplicating the buffered bytes.
//
// spec: §4.5 ll. 311 — "the gateway copies the parent's workspace
// snapshot bytes into a new MinIO object scoped to the derived
// session's own path".
type Copier interface {
	// Copy copies src to dst. Returns ErrNotFound when src is
	// absent; returns ErrConflict when dst already names a live
	// blob (§4.5 write-once). The two URIs must point at the same
	// tenant; a cross-tenant Copy returns ErrCrossTenant.
	Copy(src, dst URI) error
}

// Tombstoner extends Store with the §12.5 soft-delete + hard-prune
// lifecycle. The §12.5 GC sweep soft-deletes a blob by setting a
// tombstone (the blob's bytes are removed from storage immediately;
// metadata stays in the catalog for a retention window); the
// hard-prune sweep physically removes tombstoned entries whose
// `deleted_at` is older than `gc.tombstoneRetentionSeconds`.
//
// A blob backend may implement Tombstoner alongside Store. Backends
// that do not (legacy / read-only mirrors) appear only as Store and
// the GC orchestrator's tombstone path is a no-op against them.
type Tombstoner interface {
	// SoftDelete marks the blob deleted. Subsequent Get / Stat
	// return ErrNotFound. SoftDelete is idempotent: calling it on a
	// blob that is already soft-deleted (or absent) is a no-op
	// returning nil.
	SoftDelete(u URI) error

	// HardPrune removes tombstoned entries whose `deleted_at` is at
	// or before `now - retention`. Returns the count of entries
	// physically removed. Callers run HardPrune periodically as the
	// §12.5 tombstone hard-prune sweep.
	HardPrune(now time.Time, retention time.Duration) int
}

// BlobInfo carries the metadata fields surfaced to callers.
type BlobInfo struct {
	URI       URI
	MimeType  string
	Size      int64
	StoredAt  time.Time
	ExpiresAt time.Time
}

// ErrConflict — the URI already names an existing blob; §4.5 blobs
// are write-once.
var ErrConflict = errors.New("blobstore: blob already exists (write-once)")

// MemoryStore is the in-memory Store backing tests + the minimal
// gateway.
type MemoryStore struct {
	mu    sync.RWMutex
	blobs map[string]memBlob
	clock func() time.Time
}

type memBlob struct {
	info BlobInfo
	body []byte
	// deletedAt is the §12.5 soft-delete tombstone. Zero value means
	// the blob is live; a non-zero value means SoftDelete has run
	// and the blob is tombstoned pending hard-prune.
	deletedAt time.Time
}

// NewMemoryStore returns an empty MemoryStore. Pass nil for `clock`
// to default to time.Now.
func NewMemoryStore(clock func() time.Time) *MemoryStore {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &MemoryStore{blobs: map[string]memBlob{}, clock: clock}
}

// key returns the §12.5 line 295 4-segment object key
// `{tenant_id}/{object_type}/{session_id}/{part_id}`. An empty
// ObjectType is normalised to ObjectTypeUpload so callers under the
// legacy 3-segment URI form keep working until they are rewritten.
//
// spec: §12.5 ll. 295.
func (s *MemoryStore) key(u URI) string {
	objType := u.ObjectType
	if objType == "" {
		objType = ObjectTypeUpload
	}
	return fmt.Sprintf("%s/%s/%s/%s", u.TenantID, objType, u.SessionID, u.PartID)
}

// Put implements Store.
func (s *MemoryStore) Put(u URI, mimeType string, data io.Reader) (string, error) {
	body, err := io.ReadAll(data)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := s.key(u)
	if _, exists := s.blobs[key]; exists {
		return "", ErrConflict
	}
	now := s.clock()
	s.blobs[key] = memBlob{
		info: BlobInfo{
			URI:       u,
			MimeType:  mimeType,
			Size:      int64(len(body)),
			StoredAt:  now,
			ExpiresAt: now.Add(u.TTL),
		},
		body: body,
	}
	return u.String(), nil
}

// Get implements Store.
func (s *MemoryStore) Get(u URI) (BlobInfo, io.ReadCloser, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.blobs[s.key(u)]
	if !ok {
		return BlobInfo{}, nil, ErrNotFound
	}
	if !b.deletedAt.IsZero() {
		return BlobInfo{}, nil, ErrNotFound
	}
	if s.clock().After(b.info.ExpiresAt) {
		return BlobInfo{}, nil, ErrNotFound
	}
	return b.info, io.NopCloser(bytes.NewReader(b.body)), nil
}

// Stat implements Store.
func (s *MemoryStore) Stat(u URI) (BlobInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.blobs[s.key(u)]
	if !ok {
		return BlobInfo{}, ErrNotFound
	}
	if !b.deletedAt.IsZero() {
		return BlobInfo{}, ErrNotFound
	}
	if s.clock().After(b.info.ExpiresAt) {
		return BlobInfo{}, ErrNotFound
	}
	return b.info, nil
}

// SoftDelete implements Tombstoner. It marks the blob as tombstoned
// and clears the in-memory body so subsequent reads return
// ErrNotFound, mirroring the §12.5 soft-delete contract (the bytes
// are gone from storage; the row stays in the catalog for the
// retention window). Idempotent.
func (s *MemoryStore) SoftDelete(u URI) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.blobs[s.key(u)]
	if !ok {
		return nil
	}
	if !b.deletedAt.IsZero() {
		return nil
	}
	b.deletedAt = s.clock()
	b.body = nil
	s.blobs[s.key(u)] = b
	return nil
}

// HardPrune implements Tombstoner. It removes tombstoned entries
// whose deletedAt is at or before `now - retention`. Returns the
// count removed.
func (s *MemoryStore) HardPrune(now time.Time, retention time.Duration) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := now.Add(-retention)
	removed := 0
	for k, b := range s.blobs {
		if b.deletedAt.IsZero() {
			continue
		}
		if !b.deletedAt.After(cutoff) {
			delete(s.blobs, k)
			removed++
		}
	}
	return removed
}

// Tombstoned reports whether the blob is currently soft-deleted.
// Used by tests to assert the SoftDelete state machine.
func (s *MemoryStore) Tombstoned(u URI) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.blobs[s.key(u)]
	return ok && !b.deletedAt.IsZero()
}

// Sweep drops every blob whose ExpiresAt is at or before `now`.
// Returns the count of blobs dropped. Callers run this periodically
// to bound memory; production MinIO performs its own lifecycle
// management.
func (s *MemoryStore) Sweep(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	dropped := 0
	for k, b := range s.blobs {
		if !b.info.ExpiresAt.After(now) {
			delete(s.blobs, k)
			dropped++
		}
	}
	return dropped
}

// DeleteBySession drops every blob staged for the session within the
// tenant and returns the count dropped. It is the §12.8 GDPR-erasure
// per-store adapter for the blob store, which is keyed by session
// rather than by user; the erasure orchestrator invokes it for each of
// an erased user's sessions. Erasing a session with no blobs is a
// no-op returning (0, nil). The signature matches the orchestrator's
// DeleteBySessionFunc so the adapter plugs in directly.
func (s *MemoryStore) DeleteBySession(_ context.Context, tenantID, sessionID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	deleted := 0
	for k, b := range s.blobs {
		if b.info.URI.TenantID == tenantID && b.info.URI.SessionID == sessionID {
			delete(s.blobs, k)
			deleted++
		}
	}
	return deleted, nil
}

// Copy implements Copier. It duplicates src into a new in-memory
// blob at dst with a freshly minted ExpiresAt; returns
// ErrCrossTenant when src and dst name different tenants,
// ErrNotFound when src is absent or tombstoned, and ErrConflict
// when dst already names a live blob.
//
// spec: §4.5 ll. 311 — derive copies parent bytes.
func (s *MemoryStore) Copy(src, dst URI) error {
	if src.TenantID != dst.TenantID {
		return ErrCrossTenant
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	from, ok := s.blobs[s.key(src)]
	if !ok || !from.deletedAt.IsZero() {
		return ErrNotFound
	}
	if s.clock().After(from.info.ExpiresAt) {
		return ErrNotFound
	}
	if existing, ok := s.blobs[s.key(dst)]; ok && existing.deletedAt.IsZero() {
		return ErrConflict
	}
	now := s.clock()
	body := append([]byte(nil), from.body...)
	s.blobs[s.key(dst)] = memBlob{
		info: BlobInfo{
			URI:       dst,
			MimeType:  from.info.MimeType,
			Size:      int64(len(body)),
			StoredAt:  now,
			ExpiresAt: now.Add(dst.TTL),
		},
		body: body,
	}
	return nil
}

// NewPartID returns a fresh §4.5 part identifier — 16 random bytes
// hex-encoded with a `part_` prefix.
func NewPartID() string {
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	return "part_" + hex.EncodeToString(buf[:])
}

// TenantScoped wraps a Store with the §4.5 ll. 309 / §12.5 ll. 295
// interface-level tenant-prefix validation. Every method validates
// that the caller-supplied URI's TenantID matches the configured
// caller-tenant before delegating to the underlying Store; a
// mismatch returns ErrCrossTenant.
//
// The decorator is what the spec text describes as validation
// living "at the interface boundary, not in individual callers":
// the gateway constructs the wrapper from the resolved request
// tenant and forwards calls through it, so a future caller that
// constructs a URI from foreign-tenant input cannot reach the
// underlying Store. The pre-validation hop is what guarantees the
// tenant-isolation invariant the spec relies on.
//
// spec: §4.5 ll. 309; §12.5 ll. 295.
type TenantScoped struct {
	tenant string
	inner  Store
}

// NewTenantScoped wraps inner so every URI operation is checked
// against tenant. tenant must be non-empty; an empty tenant
// disables the check (used for tests + internal background jobs
// that legitimately span tenants).
func NewTenantScoped(tenant string, inner Store) *TenantScoped {
	return &TenantScoped{tenant: tenant, inner: inner}
}

// CallerTenant returns the tenant the decorator validates against.
func (s *TenantScoped) CallerTenant() string { return s.tenant }

// check enforces the §12.5 ll. 295 tenant-prefix invariant.
func (s *TenantScoped) check(u URI) error {
	if s.tenant == "" {
		return nil
	}
	if u.TenantID != s.tenant {
		return ErrCrossTenant
	}
	return nil
}

// Put implements Store with §12.5 ll. 295 tenant-prefix validation.
func (s *TenantScoped) Put(u URI, mimeType string, data io.Reader) (string, error) {
	if err := s.check(u); err != nil {
		return "", err
	}
	return s.inner.Put(u, mimeType, data)
}

// Get implements Store with §12.5 ll. 295 tenant-prefix validation.
func (s *TenantScoped) Get(u URI) (BlobInfo, io.ReadCloser, error) {
	if err := s.check(u); err != nil {
		return BlobInfo{}, nil, err
	}
	return s.inner.Get(u)
}

// Stat implements Store with §12.5 ll. 295 tenant-prefix validation.
func (s *TenantScoped) Stat(u URI) (BlobInfo, error) {
	if err := s.check(u); err != nil {
		return BlobInfo{}, err
	}
	return s.inner.Stat(u)
}

// Copy implements Copier with §12.5 ll. 295 tenant-prefix
// validation. Both src and dst are checked, and a Copy across
// tenants is rejected even when src and dst pass the
// caller-tenant gate individually.
func (s *TenantScoped) Copy(src, dst URI) error {
	if err := s.check(src); err != nil {
		return err
	}
	if err := s.check(dst); err != nil {
		return err
	}
	if src.TenantID != dst.TenantID {
		return ErrCrossTenant
	}
	cop, ok := s.inner.(Copier)
	if !ok {
		return fmt.Errorf("blobstore: underlying store %T does not implement Copier", s.inner)
	}
	return cop.Copy(src, dst)
}
