// SPDX-License-Identifier: MIT

package blobstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FilesystemStore is the §17.4 local-filesystem artifact backend. It
// implements the same blobstore.Store contract as MinIO/S3 so the §17.4
// Embedded and Source modes persist uploaded files, workspace
// snapshots, and checkpoints across a restart instead of losing them
// with the in-memory store (the §17.4 line 186 "lenny down without
// --purge preserves state" guarantee).
//
// On-disk layout mirrors the §12.5 line 295 object key
// `{tenant_id}/{object_type}/{session_id}/{part_id}`: each blob is a
// directory `root/<tenant>/<object_type>/<session>/<part>/` holding a
// `body` file and a `meta.json` sidecar. Mirroring the key hierarchy
// lets DeleteByTenant and DeleteBySession run as prefix-scoped directory
// removals, matching the MinIO prefix-delete semantics. Every path
// segment is URL-escaped so a tenant or session id cannot traverse
// outside root.
//
// FilesystemStore is goroutine-safe via a single mutex. It targets the
// single-host embedded deployment, where the coarse lock is not a
// throughput concern.
//
// spec: §17.4 line 165 (object storage: local filesystem, same
// artifact-store interface as MinIO/S3); §12.5 line 295 (object key
// format). F-17.4.8.
type FilesystemStore struct {
	root  string
	mu    sync.RWMutex
	clock func() time.Time

	// tierGuard / onTierMismatch enforce the §12.9 line 1048
	// storage-boundary classification check: the local-filesystem store
	// writes plaintext bytes, so a T4 tenant's write is rejected with
	// CLASSIFICATION_CONTROL_VIOLATION / tier_store_mismatch.
	tierGuard      TierGuardFunc
	onTierMismatch func(tenantID string)
}

// SetTierGuard installs the §12.9 line 1048 storage-boundary tier check.
// The §17.4 local-filesystem store persists plaintext, so a T4 tenant is
// rejected at Put / Copy rather than written in the clear.
//
// spec: §12.9 line 1048.
func (s *FilesystemStore) SetTierGuard(g TierGuardFunc) { s.tierGuard = g }

// SetOnTierStoreMismatch registers the per-rejection hook (wired by the
// gateway to the §12.5 checkpoint-storage-failure counter).
func (s *FilesystemStore) SetOnTierStoreMismatch(fn func(tenantID string)) { s.onTierMismatch = fn }

// fsMeta is the persisted sidecar metadata for one blob.
type fsMeta struct {
	TenantID   string    `json:"tenant_id"`
	ObjectType string    `json:"object_type"`
	SessionID  string    `json:"session_id"`
	PartID     string    `json:"part_id"`
	TTLSeconds int       `json:"ttl_seconds"`
	Encoding   string    `json:"encoding"`
	MimeType   string    `json:"mime_type"`
	Size       int64     `json:"size"`
	StoredAt   time.Time `json:"stored_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	// DeletedAt is the §12.5 soft-delete tombstone. Zero means live.
	DeletedAt time.Time `json:"deleted_at,omitempty"`
}

const (
	fsBodyName = "body"
	fsMetaName = "meta.json"
)

// NewFilesystemStore returns a FilesystemStore rooted at root, creating
// the directory (0700) if absent. Pass nil for clock to default to
// time.Now (UTC).
func NewFilesystemStore(root string, clock func() time.Time) (*FilesystemStore, error) {
	if root == "" {
		return nil, errors.New("blobstore: filesystem store root is empty")
	}
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("blobstore: create filesystem root %q: %w", root, err)
	}
	return &FilesystemStore{root: root, clock: clock}, nil
}

// escapeSegment URL-escapes one path segment and rejects the traversal
// values "." and ".." so a hostile tenant/session id cannot escape root.
// url.PathEscape turns "/" and "\\" into percent-codes, so the only
// remaining traversal risk is a literal dot segment, rejected here.
func escapeSegment(s string) (string, error) {
	if s == "" {
		return "", fmt.Errorf("%w: empty path segment", ErrInvalidURI)
	}
	esc := url.PathEscape(s)
	if esc == "." || esc == ".." {
		return "", fmt.Errorf("%w: unsafe path segment %q", ErrInvalidURI, s)
	}
	return esc, nil
}

// blobDir returns the directory holding u's body + metadata. An empty
// ObjectType is normalised to ObjectTypeUpload to match the §12.5 line
// 295 key normalisation MemoryStore applies.
func (s *FilesystemStore) blobDir(u URI) (string, error) {
	objType := u.ObjectType
	if objType == "" {
		objType = ObjectTypeUpload
	}
	tenant, err := escapeSegment(u.TenantID)
	if err != nil {
		return "", err
	}
	ot, err := escapeSegment(string(objType))
	if err != nil {
		return "", err
	}
	session, err := escapeSegment(u.SessionID)
	if err != nil {
		return "", err
	}
	part, err := escapeSegment(u.PartID)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.root, tenant, ot, session, part), nil
}

func (s *FilesystemStore) readMeta(dir string) (fsMeta, error) {
	raw, err := os.ReadFile(filepath.Join(dir, fsMetaName))
	if err != nil {
		return fsMeta{}, err
	}
	var m fsMeta
	if err := json.Unmarshal(raw, &m); err != nil {
		return fsMeta{}, fmt.Errorf("blobstore: decode meta %q: %w", dir, err)
	}
	return m, nil
}

func (s *FilesystemStore) writeMeta(dir string, m fsMeta) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("blobstore: encode meta: %w", err)
	}
	tmp := filepath.Join(dir, fsMetaName+".tmp")
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("blobstore: write meta: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, fsMetaName)); err != nil {
		return fmt.Errorf("blobstore: commit meta: %w", err)
	}
	return nil
}

// info reconstructs the caller-facing BlobInfo from persisted metadata.
func (m fsMeta) info() BlobInfo {
	return BlobInfo{
		URI: URI{
			TenantID:   m.TenantID,
			ObjectType: ObjectType(m.ObjectType),
			SessionID:  m.SessionID,
			PartID:     m.PartID,
			TTL:        time.Duration(m.TTLSeconds) * time.Second,
			Encoding:   m.Encoding,
		},
		MimeType:  m.MimeType,
		Size:      m.Size,
		StoredAt:  m.StoredAt,
		ExpiresAt: m.ExpiresAt,
	}
}

// Put implements Store. Blobs are write-once: an existing key (live or
// tombstoned) returns ErrConflict, matching the §4.5 immutability
// guarantee MemoryStore enforces.
func (s *FilesystemStore) Put(u URI, mimeType string, data io.Reader) (string, error) {
	// spec: §12.9 line 1048 — the filesystem store cannot envelope-encrypt
	// at rest; reject a T4 write before touching the body.
	if err := checkTierStoreMismatch(s.tierGuard, "filesystem", u.TenantID, s.onTierMismatch); err != nil {
		return "", err
	}
	body, err := io.ReadAll(data)
	if err != nil {
		return "", err
	}
	dir, err := s.blobDir(u)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(filepath.Join(dir, fsMetaName)); err == nil {
		return "", ErrConflict
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("blobstore: stat %q: %w", dir, err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("blobstore: create blob dir %q: %w", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, fsBodyName), body, 0o600); err != nil {
		return "", fmt.Errorf("blobstore: write body: %w", err)
	}
	now := s.clock()
	objType := u.ObjectType
	if objType == "" {
		objType = ObjectTypeUpload
	}
	enc := u.Encoding
	if enc == "" {
		enc = Encoding
	}
	m := fsMeta{
		TenantID:   u.TenantID,
		ObjectType: string(objType),
		SessionID:  u.SessionID,
		PartID:     u.PartID,
		TTLSeconds: int(u.TTL.Seconds()),
		Encoding:   enc,
		MimeType:   mimeType,
		Size:       int64(len(body)),
		StoredAt:   now,
		ExpiresAt:  now.Add(u.TTL),
	}
	if err := s.writeMeta(dir, m); err != nil {
		return "", err
	}
	return u.String(), nil
}

// Get implements Store. A tombstoned or expired blob reads as
// ErrNotFound, mirroring the §12.5 soft-delete contract.
func (s *FilesystemStore) Get(u URI) (BlobInfo, io.ReadCloser, error) {
	dir, err := s.blobDir(u)
	if err != nil {
		return BlobInfo{}, nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, err := s.readMeta(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return BlobInfo{}, nil, ErrNotFound
		}
		return BlobInfo{}, nil, err
	}
	if !m.DeletedAt.IsZero() || s.clock().After(m.ExpiresAt) {
		return BlobInfo{}, nil, ErrNotFound
	}
	body, err := os.ReadFile(filepath.Join(dir, fsBodyName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return BlobInfo{}, nil, ErrNotFound
		}
		return BlobInfo{}, nil, err
	}
	return m.info(), io.NopCloser(bytes.NewReader(body)), nil
}

// Stat implements Store.
func (s *FilesystemStore) Stat(u URI) (BlobInfo, error) {
	dir, err := s.blobDir(u)
	if err != nil {
		return BlobInfo{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, err := s.readMeta(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return BlobInfo{}, ErrNotFound
		}
		return BlobInfo{}, err
	}
	if !m.DeletedAt.IsZero() || s.clock().After(m.ExpiresAt) {
		return BlobInfo{}, ErrNotFound
	}
	return m.info(), nil
}

// SoftDelete implements Tombstoner. It stamps the tombstone and removes
// the body file, leaving meta.json for the retention window so the GC
// hard-prune sweep can find it. Idempotent.
func (s *FilesystemStore) SoftDelete(u URI) error {
	dir, err := s.blobDir(u)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := s.readMeta(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if !m.DeletedAt.IsZero() {
		return nil
	}
	m.DeletedAt = s.clock()
	if err := s.writeMeta(dir, m); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(dir, fsBodyName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("blobstore: remove tombstoned body: %w", err)
	}
	return nil
}

// HardPrune implements Tombstoner. It walks every blob directory and
// removes those whose tombstone is at or before now-retention. Returns
// the count physically removed.
func (s *FilesystemStore) HardPrune(now time.Time, retention time.Duration) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := now.Add(-retention)
	removed := 0
	s.walkBlobDirs(func(dir string, m fsMeta) {
		if m.DeletedAt.IsZero() || m.DeletedAt.After(cutoff) {
			return
		}
		if err := os.RemoveAll(dir); err == nil {
			removed++
		}
	})
	return removed
}

// HardDeleteObject implements Tombstoner. It physically removes the
// single object directory named by u, live or tombstoned. Idempotent.
func (s *FilesystemStore) HardDeleteObject(u URI) error {
	dir, err := s.blobDir(u)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return os.RemoveAll(dir)
}

// StatIncludingTombstones implements Tombstoner, distinguishing a
// soft-deleted blob from a physically absent one.
func (s *FilesystemStore) StatIncludingTombstones(u URI) (BlobInfo, BlobState, error) {
	dir, err := s.blobDir(u)
	if err != nil {
		return BlobInfo{}, BlobStateNotFound, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, err := s.readMeta(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return BlobInfo{}, BlobStateNotFound, ErrNotFound
		}
		return BlobInfo{}, BlobStateNotFound, err
	}
	if !m.DeletedAt.IsZero() {
		return m.info(), BlobStateSoftDeleted, nil
	}
	if s.clock().After(m.ExpiresAt) {
		return BlobInfo{}, BlobStateNotFound, ErrNotFound
	}
	return m.info(), BlobStateActive, nil
}

// Copy implements Copier. It duplicates a live src into a new dst,
// rejecting a cross-tenant copy, a missing/tombstoned src, and a dst
// that already names a live blob.
func (s *FilesystemStore) Copy(src, dst URI) error {
	if src.TenantID != dst.TenantID {
		return ErrCrossTenant
	}
	// spec: §12.9 line 1048 — the derive byte-copy is a write to dst.
	if err := checkTierStoreMismatch(s.tierGuard, "filesystem", dst.TenantID, s.onTierMismatch); err != nil {
		return err
	}
	srcDir, err := s.blobDir(src)
	if err != nil {
		return err
	}
	dstDir, err := s.blobDir(dst)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	srcMeta, err := s.readMeta(srcDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		return err
	}
	if !srcMeta.DeletedAt.IsZero() || s.clock().After(srcMeta.ExpiresAt) {
		return ErrNotFound
	}
	if dm, derr := s.readMeta(dstDir); derr == nil && dm.DeletedAt.IsZero() {
		return ErrConflict
	} else if derr != nil && !errors.Is(derr, os.ErrNotExist) {
		return derr
	}
	body, err := os.ReadFile(filepath.Join(srcDir, fsBodyName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		return err
	}
	if err := os.MkdirAll(dstDir, 0o700); err != nil {
		return fmt.Errorf("blobstore: create copy dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dstDir, fsBodyName), body, 0o600); err != nil {
		return fmt.Errorf("blobstore: write copy body: %w", err)
	}
	now := s.clock()
	objType := dst.ObjectType
	if objType == "" {
		objType = ObjectTypeUpload
	}
	enc := dst.Encoding
	if enc == "" {
		enc = srcMeta.Encoding
	}
	return s.writeMeta(dstDir, fsMeta{
		TenantID:   dst.TenantID,
		ObjectType: string(objType),
		SessionID:  dst.SessionID,
		PartID:     dst.PartID,
		TTLSeconds: int(dst.TTL.Seconds()),
		Encoding:   enc,
		MimeType:   srcMeta.MimeType,
		Size:       int64(len(body)),
		StoredAt:   now,
		ExpiresAt:  now.Add(dst.TTL),
	})
}

// DeleteBySession drops every blob staged for the session within the
// tenant and returns the count dropped — the §12.8 GDPR-erasure per-store
// adapter for the blob store. A session with no blobs is a no-op.
func (s *FilesystemStore) DeleteBySession(_ context.Context, tenantID, sessionID string) (int, error) {
	tenant, err := escapeSegment(tenantID)
	if err != nil {
		return 0, err
	}
	session, err := escapeSegment(sessionID)
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Session directories live one object-type level below the tenant:
	// root/<tenant>/<object_type>/<session>/.
	matches, err := filepath.Glob(filepath.Join(s.root, tenant, "*", session))
	if err != nil {
		return 0, fmt.Errorf("blobstore: glob session dirs: %w", err)
	}
	deleted := 0
	for _, sessDir := range matches {
		parts, err := os.ReadDir(sessDir)
		if err != nil {
			continue
		}
		n := len(parts)
		if err := os.RemoveAll(sessDir); err == nil {
			deleted += n
		}
	}
	return deleted, nil
}

// DeleteByUser implements the §12.1 Eraser primitive. Blobs carry no
// user dimension, so per-user artifact erasure runs per session
// (§12.8 step 7); this whole-user call is a no-op returning (0, nil).
func (s *FilesystemStore) DeleteByUser(_ context.Context, _, _ string) (int, error) {
	return 0, nil
}

// DeleteByTenant implements TenantPrefixDeleter / Eraser — the §12.8
// Phase 4 prefix-scoped purge. It removes every object under the
// tenant's directory in a single pass and returns the count removed. An
// empty tenantID matches nothing so a mis-scoped call cannot wipe the
// store.
func (s *FilesystemStore) DeleteByTenant(_ context.Context, tenantID string) (int, error) {
	if tenantID == "" {
		return 0, nil
	}
	tenant, err := escapeSegment(tenantID)
	if err != nil {
		return 0, err
	}
	tenantDir := filepath.Join(s.root, tenant)
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	if err := s.walkBlobDirsUnder(tenantDir, func(string, fsMeta) { count++ }); err != nil {
		return 0, err
	}
	if count == 0 {
		return 0, nil
	}
	if err := os.RemoveAll(tenantDir); err != nil {
		return 0, fmt.Errorf("blobstore: remove tenant prefix: %w", err)
	}
	return count, nil
}

// Sweep drops every blob whose ExpiresAt is at or before now and returns
// the count dropped. Embedded callers run it periodically to bound disk;
// production MinIO does its own lifecycle management.
func (s *FilesystemStore) Sweep(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	dropped := 0
	s.walkBlobDirs(func(dir string, m fsMeta) {
		if m.ExpiresAt.After(now) {
			return
		}
		if err := os.RemoveAll(dir); err == nil {
			dropped++
		}
	})
	return dropped
}

// walkBlobDirs visits every blob directory under root, calling fn with
// the directory and its decoded metadata. Directories without a
// readable meta.json are skipped. The caller holds s.mu.
func (s *FilesystemStore) walkBlobDirs(fn func(dir string, m fsMeta)) {
	_ = s.walkBlobDirsUnder(s.root, fn)
}

// walkBlobDirsUnder walks the blob directories under base. A base that
// does not exist is not an error (it yields no calls).
func (s *FilesystemStore) walkBlobDirsUnder(base string, fn func(dir string, m fsMeta)) error {
	err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		if d.Name() != fsMetaName {
			return nil
		}
		dir := filepath.Dir(path)
		m, rerr := s.readMeta(dir)
		if rerr != nil {
			return nil
		}
		fn(dir, m)
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("blobstore: walk %q: %w", base, err)
	}
	return nil
}

// Compile-time assertions that FilesystemStore satisfies the optional
// blob-store surfaces the gateway type-asserts for.
var (
	_ Store               = (*FilesystemStore)(nil)
	_ Copier              = (*FilesystemStore)(nil)
	_ Tombstoner          = (*FilesystemStore)(nil)
	_ TenantPrefixDeleter = (*FilesystemStore)(nil)
	_ Eraser              = (*FilesystemStore)(nil)
)
