// SPDX-License-Identifier: MIT

// Package blobstore implements the §4.5 artifact / blob abstraction
// that backs `lenny-blob://` references in `OutputPart`, workspace
// snapshots, transcripts, and the upload pipeline.
//
// The blob-uri scheme is canonical: a blob is addressed by
// `(tenant_id, session_id, part_id)` and carries a per-blob TTL plus
// a fixed encryption marker. The package exposes:
//
//   - URI: parser + serialiser for `lenny-blob://` references
//   - Store: small CRUD interface (Put / Get / Stat)
//   - MemoryStore: in-memory implementation suitable for tests + the
//     minimal gateway
//
// Production replaces MemoryStore with a MinIO-backed implementation
// that maps the URI to the §4.5 object path
// `/{tenant_id}/{object_type}/{session_id}/{part_id}` and applies
// at-rest envelope encryption per §13.x. The wire surface is unchanged.
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

// URI is a parsed `lenny-blob://` reference.
type URI struct {
	TenantID  string
	SessionID string
	PartID    string
	TTL       time.Duration
	Encoding  string
}

// ParseURI decodes the §4.5 `lenny-blob://{tenant_id}/{session_id}/{part_id}`
// reference. URL-decoding is performed on each path segment. The
// `ttl` query parameter (seconds) is required; `enc` defaults to
// `aes256gcm`.
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
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if tenant == "" || len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return URI{}, fmt.Errorf("%w: expected lenny-blob://<tenant>/<session>/<part>", ErrInvalidURI)
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
		TenantID:  tenant,
		SessionID: parts[0],
		PartID:    parts[1],
		TTL:       time.Duration(ttlSecs) * time.Second,
		Encoding:  enc,
	}, nil
}

// String serialises u back to the §4.5 wire format.
func (u URI) String() string {
	enc := u.Encoding
	if enc == "" {
		enc = Encoding
	}
	return fmt.Sprintf(
		"%s://%s/%s/%s?ttl=%d&enc=%s",
		Scheme,
		url.PathEscape(u.TenantID),
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

func (s *MemoryStore) key(u URI) string {
	return fmt.Sprintf("%s/%s/%s", u.TenantID, u.SessionID, u.PartID)
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

// NewPartID returns a fresh §4.5 part identifier — 16 random bytes
// hex-encoded with a `part_` prefix.
func NewPartID() string {
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	return "part_" + hex.EncodeToString(buf[:])
}
