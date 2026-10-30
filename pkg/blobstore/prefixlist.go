// SPDX-License-Identifier: MIT

package blobstore

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// PrefixLister lists every live object under a key prefix inside one
// (tenant, object_type, session) scope.
//
// The prefix is the object key of u: the canonical
// `{tenant_id}/{object_type}/{session_id}/` session prefix followed by
// u.PartID (for a checkpoint, `{checkpoint_id}/`). The listing never
// leaves that session prefix, so it cannot return another tenant's or
// another session's keys. The returned BlobInfos carry the full object
// URI (u's tenant, object type, and session, with PartID set to the key's
// suffix after the session prefix), Size, and StoredAt, in ascending
// lexicographic key order, which is the native order of every
// S3-compatible, GCS, and Azure listing. A soft-deleted (§12.5 tombstoned)
// object is excluded, so a released chunk is never offered for
// reassembly. A listing that spans several provider pages is returned
// whole; a provider error on any page fails the call rather than
// returning a truncated set, because the §10.1.7 contiguity check must
// see every chunk under the prefix. An empty prefix returns an empty
// slice and a nil error. A URI whose tenant or session segment is empty
// or contains a slash returns ErrInvalidURI, since such a URI cannot name
// one session prefix.
//
// spec: §10.1.7 (Reassembly on resume: list objects under
// chunk_object_key_prefix via ListObjectsV2); §4.4 (Checkpoint abort
// cleanup sweeps the prefix by ListObjectsV2); §12.5 (tenant-prefixed
// object keys).
type PrefixLister interface {
	ListByPrefix(ctx context.Context, u URI) ([]BlobInfo, error)
}

// CheckpointStore is the object-store surface the §10.1.7 chunked
// checkpoint model requires of a backend that serves pod-based sessions.
// The checkpoint driver mints single-key PUT capabilities (Presigner),
// the resume path lists the chunk prefix and mints single-key GET
// capabilities (PrefixLister, Presigner), and the abort, supersede, and
// backstop cleanup arms list the prefix and delete each key
// (PrefixLister, Tombstoner). Every production backend (MinIO, S3, GCS,
// and Azure Blob) asserts it at compile time, and the gateway refuses to
// start a pod-based deployment on a store that does not satisfy it, so a
// backend cannot silently lack a method and leave resume restoring
// nothing.
//
// spec: §10.1.7; §13.2 (capability model); §4.4.
type CheckpointStore interface {
	Store
	Presigner
	PrefixLister
	Tombstoner
}

// PrefixListKeys returns the object-key prefix to list for u and the
// session prefix every listed key starts with. Both use the canonical
// §12.5 layout `{tenant_id}/{object_type}/{session_id}/{part_id}` that
// the S3, GCS, Azure, and MinIO backends share; an empty ObjectType is
// normalised to ObjectTypeUpload, as each backend's objectKey does. A
// tenant or session segment that is empty or contains a slash returns
// ErrInvalidURI, because such a segment would let the prefix match keys
// outside the caller's tenant or session.
//
// spec: §12.5 (tenant-prefixed object keys).
func PrefixListKeys(u URI) (keyPrefix, sessionPrefix string, err error) {
	if err := validatePrefixSegment("tenant", u.TenantID); err != nil {
		return "", "", err
	}
	if err := validatePrefixSegment("session", u.SessionID); err != nil {
		return "", "", err
	}
	objType := u.ObjectType
	if objType == "" {
		objType = ObjectTypeUpload
	}
	sessionPrefix = u.TenantID + "/" + string(objType) + "/" + u.SessionID + "/"
	return sessionPrefix + u.PartID, sessionPrefix, nil
}

func validatePrefixSegment(name, v string) error {
	if v == "" {
		return fmt.Errorf("%w: empty %s segment in prefix listing", ErrInvalidURI, name)
	}
	if strings.Contains(v, "/") {
		return fmt.Errorf("%w: %s segment %q contains a slash", ErrInvalidURI, name, v)
	}
	return nil
}

// PrefixListEntry builds the BlobInfo a PrefixLister returns for one
// listed key: u's tenant, object type, and session, with PartID set to
// the key's suffix after sessionPrefix.
func PrefixListEntry(u URI, sessionPrefix, key string, size int64, storedAt time.Time) BlobInfo {
	objType := u.ObjectType
	if objType == "" {
		objType = ObjectTypeUpload
	}
	return BlobInfo{
		URI: URI{
			TenantID:   u.TenantID,
			ObjectType: objType,
			SessionID:  u.SessionID,
			PartID:     strings.TrimPrefix(key, sessionPrefix),
		},
		Size:     size,
		StoredAt: storedAt,
	}
}

// SortByPartID orders a listing by ascending PartID, which within one
// session prefix is ascending lexicographic key order. Backends whose
// provider already lists in key order call it anyway, so the PrefixLister
// ordering contract does not rest on a provider guarantee or on a test
// double.
func SortByPartID(infos []BlobInfo) {
	sort.SliceStable(infos, func(i, j int) bool { return infos[i].URI.PartID < infos[j].URI.PartID })
}

// ListByPrefix implements PrefixLister over the in-memory map. A
// tombstoned or expired blob is excluded, matching Stat.
//
// spec: §10.1.7; §12.5.
func (s *MemoryStore) ListByPrefix(_ context.Context, u URI) ([]BlobInfo, error) {
	keyPrefix, sessPrefix, err := PrefixListKeys(u)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	now := s.clock()
	out := []BlobInfo{}
	for key, b := range s.blobs {
		if !strings.HasPrefix(key, keyPrefix) || !b.deletedAt.IsZero() || now.After(b.info.ExpiresAt) {
			continue
		}
		out = append(out, PrefixListEntry(u, sessPrefix, key, b.info.Size, b.info.StoredAt))
	}
	SortByPartID(out)
	return out, nil
}

// ListByPrefix implements PrefixLister over the §17.4 filesystem layout.
// The filesystem store escapes PartID into one directory segment, so the
// walk is scoped to the session directory and each blob's persisted
// PartID is matched against u.PartID as a string prefix. A tombstoned
// blob is excluded.
//
// spec: §10.1.7; §12.5.
func (s *FilesystemStore) ListByPrefix(_ context.Context, u URI) ([]BlobInfo, error) {
	keyPrefix, sessPrefix, err := PrefixListKeys(u)
	if err != nil {
		return nil, err
	}
	sessDir, err := s.blobDir(URI{TenantID: u.TenantID, ObjectType: u.ObjectType, SessionID: u.SessionID, PartID: "_"})
	if err != nil {
		return nil, err
	}
	sessDir = filepath.Dir(sessDir)
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []BlobInfo{}
	werr := s.walkBlobDirsUnder(sessDir, func(_ string, m fsMeta) {
		key := sessPrefix + m.PartID
		if !m.DeletedAt.IsZero() || !strings.HasPrefix(key, keyPrefix) {
			return
		}
		out = append(out, PrefixListEntry(u, sessPrefix, key, m.Size, m.StoredAt))
	})
	if werr != nil {
		return nil, werr
	}
	SortByPartID(out)
	return out, nil
}

var (
	_ PrefixLister = (*MemoryStore)(nil)
	_ PrefixLister = (*FilesystemStore)(nil)
)
