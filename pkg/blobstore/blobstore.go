// SPDX-License-Identifier: MIT

// Package blobstore implements the §4.5 artifact / blob abstraction
// that backs `lenny-blob://` references in `MessagePart`, workspace
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

// DerivedSnapshotTTL is the §4.5 ll. 311 TTL stamped on a derived
// session's workspace-snapshot copy. The derive handler reuses the
// 7-day default that the §7.1 retention window applies to a
// completed session: the derived session owns its workspace
// independent of the parent, so 7 days lines up with the §7.1
// `extend-retention` adjustable budget.
//
// spec: §4.5 ll. 311; §7.1 retention.
const DerivedSnapshotTTL = 7 * 24 * time.Hour

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

	// ErrClassificationControlViolation is the §12.5 line 303 / §12.9
	// line 1046 fail-closed write sentinel: a T4 tenant whose per-tenant
	// KMS key is unavailable at write time. Every §4.5 ArtifactStore
	// backend (MinIO, S3, Azure, GCS) returns it — wrapped with the
	// offending tenant id — so a Put never silently downgrades a T4
	// tenant to a deployment-wide or bucket-default key. The §15.1 error
	// catalog maps it to `CLASSIFICATION_CONTROL_VIOLATION` and the
	// gateway counts each rejection in
	// lenny_checkpoint_storage_failure_total{reason="kms_unavailable"}.
	//
	// spec: §12.5 line 303; §12.9 line 1046.
	ErrClassificationControlViolation = errors.New("blobstore: T4 tenant KMS key unavailable; CLASSIFICATION_CONTROL_VIOLATION")

	// ErrTierStoreMismatch is the §12.9 line 1048
	// `details.reason="tier_store_mismatch"` sentinel: a tenant whose
	// `workspaceTier` requires envelope encryption at rest (T4) wrote to
	// a backend that is not configured for it (the in-memory store or the
	// §17.4 local-filesystem store). The spec's worked example —
	// "writing T4 data to a store not configured for envelope encryption"
	// — is exactly this case. It is distinct from
	// ErrClassificationControlViolation's KMS-unavailable case, where the
	// store IS envelope-capable but the per-tenant key is unreachable.
	// The error wraps ErrClassificationControlViolation so the §15.1
	// mapping still resolves the CLASSIFICATION_CONTROL_VIOLATION family
	// while `details.reason` distinguishes the two.
	//
	// spec: §12.9 line 1048; §15.1 line 1078.
	ErrTierStoreMismatch = errors.New("blobstore: tier_store_mismatch — store not configured for envelope encryption")
)

// TierGuardFunc reports whether the writing tenant's §12.9
// data-classification tier requires envelope encryption at rest (true
// for T4). A non-envelope-capable backend (the in-memory store, the
// §17.4 local-filesystem store) calls it on every write so a T4 tenant's
// Put / Copy is rejected at the storage boundary with
// CLASSIFICATION_CONTROL_VIOLATION rather than silently persisting
// restricted data without envelope encryption. An envelope-capable
// backend (MinIO, S3, GCS, Azure with an SSE-KMS resolver) enforces the
// T4 contract through its own key resolver and does not install a guard.
//
// spec: §12.9 line 1048 — "Each store method receives the applicable
// tier as context ... Tier mismatches ... are rejected at write time".
type TierGuardFunc func(tenantID string) (requireEnvelope bool, err error)

// checkTierStoreMismatch returns the §12.9 line 1048
// CLASSIFICATION_CONTROL_VIOLATION / tier_store_mismatch error when guard
// reports the writing tenant requires envelope encryption. A nil guard
// (the production envelope-capable path, or a dev deployment with no
// tenant tier source) is a no-op. A guard lookup error is treated as
// indeterminate and passes through: the guard only fires on a confirmed
// T4 tenant so a flaky tenant-store lookup cannot wedge the in-memory or
// filesystem dev path, and the envelope-capable backends carry their own
// fail-closed posture independent of this guard.
//
// spec: §12.9 line 1048.
func checkTierStoreMismatch(guard TierGuardFunc, backend, tenantID string, onMismatch func(string)) error {
	if guard == nil {
		return nil
	}
	requireEnvelope, err := guard(tenantID)
	if err != nil || !requireEnvelope {
		return nil
	}
	if onMismatch != nil {
		onMismatch(tenantID)
	}
	return fmt.Errorf("%w: tenant=%s: %s artifact store is not configured for envelope encryption (T4 requires it): %w",
		ErrClassificationControlViolation, tenantID, backend, ErrTierStoreMismatch)
}

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
	// checkpoints per §4.4 line 234, §12.5 ll. 311-313. The key segment
	// is `checkpoints` (plural) so the object key matches the §10.1
	// chunked-checkpoint prefix `/{tenant_id}/checkpoints/{session_id}/`.
	ObjectTypeCheckpoint ObjectType = "checkpoints"
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
	switch {
	case len(parts) >= 3:
		// §12.5 ll. 295 canonical form
		// (`/tenant/object_type/session/part...`). Everything after the
		// session segment is the part id, so a §10.1 nested checkpoint
		// key (`{checkpoint_id}/chunk-{n}.{enc}`) round-trips instead of
		// being rejected as a 5-segment path.
		objType = ObjectType(parts[0])
		session = parts[1]
		part = strings.Join(parts[2:], "/")
		if !IsValidObjectType(objType) {
			return URI{}, fmt.Errorf("%w: unknown object_type %q", ErrInvalidURI, parts[0])
		}
	case len(parts) == 2:
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
		escapePartID(u.PartID),
		int(u.TTL.Seconds()),
		enc,
	)
}

// escapePartID percent-escapes each slash-separated segment of a part
// id while preserving the separators, so a §10.1 nested checkpoint key
// (`{checkpoint_id}/chunk-{n}.{enc}`) survives a String → ParseURI
// round-trip. Escaping the whole part id in one call would collapse its
// slashes into `%2F` and the parser would then read it as one segment.
func escapePartID(part string) string {
	segs := strings.Split(part, "/")
	for i, seg := range segs {
		segs[i] = url.PathEscape(seg)
	}
	return strings.Join(segs, "/")
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

// Presigner extends Store with §13.2 capability minting. The returned
// Grant is a single-key, single-method, short-expiry capability the
// gateway hands to an agent pod; the pod issues the object-store call,
// the ArtifactStore does not. A PresignPut grant additionally binds an
// exact Content-Length; a PresignGet grant is read-only and is minted
// only for keys the gateway resolved from a manifest row it owns.
//
// The gateway signs the tenant prefix, the HTTP method, and the object
// key into every grant, so a pod that alters any of them produces a
// signature mismatch and the object store rejects the request before a
// byte is written or read. On the SigV4 backends (MinIO, AWS S3) the
// tenant's SSE-KMS headers and the Content-Length are folded into the
// signature as well; the gateway returns those signed name→value pairs
// in Grant.Headers for the grantee to replay verbatim. GCS V4 signed
// URLs and Azure account-key SAS carry no signed headers, so their T4
// encryption posture rests on a bucket-default CMEK (GCS) or a
// container default encryption scope (Azure) rather than on the grant.
//
// spec: §13.2 (capability model); §12.5 (per-provider T4 mint
// invariants).
type Presigner interface {
	// PresignPut mints a single-key PUT capability for u bound to the
	// exact contentLength, expiring after ttl.
	PresignPut(u URI, contentLength int64, ttl time.Duration) (Grant, error)
	// PresignGet mints a single-key read-only GET capability for u,
	// expiring after ttl.
	PresignGet(u URI, ttl time.Duration) (Grant, error)
}

// Grant is a §13.2 object-store capability. Headers carries the exact
// name→value pairs the backend folded into the signature (the SSE-KMS
// header set and Content-Length on a T4 PresignPut for the SigV4
// backends), which the grantee MUST replay verbatim on the request or
// the object store rejects it with SignatureDoesNotMatch. Headers is
// empty when the backend signs no request headers.
//
// spec: §13.2 (capability model).
type Grant struct {
	URL       string
	Headers   map[string]string
	ExpiresAt time.Time
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
	// physically removed. HardPrune is the out-of-band ghost sweep: it
	// lists the bucket and removes objects whose tombstone tag has
	// aged out. The routine §12.5 hard-prune is catalog-driven
	// (HardDeleteObject below) so it does not pay the bucket-wide list
	// per cycle; HardPrune remains as a defense-in-depth path for
	// objects whose catalog row was lost.
	HardPrune(now time.Time, retention time.Duration) int

	// HardDeleteObject physically removes the single object named by
	// its URI, regardless of tombstone tag or retention window.
	// Deleting an absent object is a no-op (idempotent). The §12.5
	// catalog-driven hard-prune sweep calls it for each object whose
	// catalog row is past its tombstone deadline, so the sweep cost
	// scales with the Postgres-supplied set rather than with the
	// bucket-wide object count.
	//
	// spec: §12.5 line 320 — the GC job queries Postgres for artifacts
	// past their TTL and deletes exactly that set.
	HardDeleteObject(u URI) error

	// StatIncludingTombstones returns the blob's metadata and its
	// lifecycle State (Active, SoftDeleted, NotFound). Unlike Stat,
	// which surfaces a soft-deleted blob as ErrNotFound to mirror
	// the §12.5 contract, StatIncludingTombstones lets the GC sweep
	// distinguish "soft-deleted, awaiting hard-prune" from
	// "physically absent". Returns ErrNotFound only when the blob
	// has never been written or has already been hard-pruned.
	//
	// spec: §12.5 ll. 333-339 — soft-delete vs hard-prune state
	// surface.
	StatIncludingTombstones(u URI) (BlobInfo, BlobState, error)
}

// TenantPrefixDeleter extends the blob store with the §12.5 ll. 295
// prefix-scoped bulk delete the §12.8 Phase 4 tenant-deletion
// orchestrator runs. DeleteByTenant removes every object under the
// `/{tenant_id}/` prefix in a single prefix-scoped sweep instead of
// the O(#sessions) per-session enumeration DeleteBySession requires;
// it returns the count removed and is idempotent — a tenant with no
// objects is a no-op returning (0, nil).
//
// Backends that hold the bytes (Memory, MinIO, S3, GCS, Azure)
// implement it directly; the cataloging decorator forwards to the
// inner store and reconciles the matching catalog rows. The
// TenantScoped wrapper restricts DeleteByTenant to the bound tenant
// so a scoped handle cannot erase a foreign tenant's prefix.
//
// spec: §12.5 ll. 295 — "DeleteByTenant(tenant_id) performs a
// prefix-scoped bulk delete on /{tenant_id}/*."
type TenantPrefixDeleter interface {
	DeleteByTenant(ctx context.Context, tenantID string) (int, error)
}

// Eraser is the §12.1 mandatory-erasure interface for the §12.2
// ArtifactStore role. Every blob backend exposes both primitives so a
// substitute backend that omits either cannot compile into the gateway
// binary.
//
// DeleteByTenant is the §12.5 / §12.8 Phase-4 prefix-scoped purge
// (same method as TenantPrefixDeleter). DeleteByUser is a no-op on the
// artifact store: blobs are keyed by (tenant, object_type, session,
// part) with no user dimension, so the §12.8 step-7 per-user artifact
// erasure runs per session via DeleteBySession over the user's
// sessions, not through a whole-user call. The method is present to
// satisfy the §12.1 compile-time contract.
//
// spec: §12.1 line 5 — every store role interface MUST expose
// DeleteByUser and DeleteByTenant; §12.8 step 7 (artifact erasure is
// session-scoped).
type Eraser interface {
	DeleteByUser(ctx context.Context, tenantID, userID string) (int, error)
	DeleteByTenant(ctx context.Context, tenantID string) (int, error)
}

// BlobState is the §12.5 lifecycle classification surfaced by
// StatIncludingTombstones. The GC sweep and observability paths use
// it to distinguish a soft-deleted blob (still discoverable for the
// retention window) from one that has been physically removed.
//
// spec: §12.5 ll. 333-339.
type BlobState string

const (
	// BlobStateActive is the live state: the blob is readable.
	BlobStateActive BlobState = "active"
	// BlobStateSoftDeleted is the §12.5 tombstoned state: bytes are
	// removed but the row / metadata stays for the retention window.
	BlobStateSoftDeleted BlobState = "soft_deleted"
	// BlobStateNotFound is the post-hard-prune (or never-existed)
	// state. StatIncludingTombstones pairs this with ErrNotFound.
	BlobStateNotFound BlobState = "not_found"
)

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

	// tierGuard, when set, enforces the §12.9 line 1048 storage-boundary
	// classification check: a T4 tenant's write is rejected because the
	// in-memory store cannot envelope-encrypt at rest. onTierMismatch is
	// the optional metrics/log hook the gateway wires to the §12.5
	// `lenny_checkpoint_storage_failure_total{reason="tier_store_mismatch"}`
	// counter.
	tierGuard      TierGuardFunc
	onTierMismatch func(tenantID string)
}

// SetTierGuard installs the §12.9 line 1048 storage-boundary tier check.
// The in-memory store is not envelope-capable, so a tenant whose
// `workspaceTier` requires envelope encryption (T4) is rejected at Put /
// Copy with CLASSIFICATION_CONTROL_VIOLATION / tier_store_mismatch
// instead of silently persisting restricted data in the clear.
//
// spec: §12.9 line 1048.
func (s *MemoryStore) SetTierGuard(g TierGuardFunc) { s.tierGuard = g }

// SetOnTierStoreMismatch registers the hook fired on every
// tier_store_mismatch rejection (the gateway wires it to the §12.5
// checkpoint-storage-failure counter).
func (s *MemoryStore) SetOnTierStoreMismatch(fn func(tenantID string)) { s.onTierMismatch = fn }

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
	// spec: §12.9 line 1048 — reject a T4 write before reading the body
	// because the in-memory store cannot envelope-encrypt at rest.
	if err := checkTierStoreMismatch(s.tierGuard, "in-memory", u.TenantID, s.onTierMismatch); err != nil {
		return "", err
	}
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

// HardDeleteObject implements Tombstoner. It physically removes the
// single object named by u, whether live or tombstoned. A missing
// object is a no-op. spec: §12.5 line 320.
func (s *MemoryStore) HardDeleteObject(u URI) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.blobs, s.key(u))
	return nil
}

// StatIncludingTombstones implements Tombstoner. It returns the blob's
// metadata along with the §12.5 lifecycle state: Active for a live
// blob, SoftDeleted for one carrying a tombstone, NotFound when the
// blob has never been written or has been hard-pruned. Unlike Stat,
// this method does not collapse SoftDeleted into ErrNotFound — callers
// that need to distinguish the two (the GC sweep, observability)
// use this surface.
//
// spec: §12.5 ll. 333-339.
func (s *MemoryStore) StatIncludingTombstones(u URI) (BlobInfo, BlobState, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.blobs[s.key(u)]
	if !ok {
		return BlobInfo{}, BlobStateNotFound, ErrNotFound
	}
	if !b.deletedAt.IsZero() {
		return b.info, BlobStateSoftDeleted, nil
	}
	if s.clock().After(b.info.ExpiresAt) {
		return BlobInfo{}, BlobStateNotFound, ErrNotFound
	}
	return b.info, BlobStateActive, nil
}

// Keys returns every URI the store currently holds, including
// tombstoned rows. The slice is unordered. Used by tests to assert
// per-session GC and §7.4 staging-rollback invariants.
func (s *MemoryStore) Keys() []URI {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]URI, 0, len(s.blobs))
	for _, b := range s.blobs {
		out = append(out, b.info.URI)
	}
	return out
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

// DeleteByUser implements the §12.1 Eraser primitive. Blobs carry no
// user dimension (they are keyed by session), so per-user artifact
// erasure runs per session via DeleteBySession over the user's
// sessions (§12.8 step 7); this whole-user call is a no-op returning
// (0, nil).
//
// spec: §12.1 line 5 (mandatory primitive); §12.8 step 7 (session-
// scoped artifact erasure).
func (s *MemoryStore) DeleteByUser(_ context.Context, _, _ string) (int, error) {
	return 0, nil
}

// DeleteByTenant implements TenantPrefixDeleter — the §12.8 Phase 4
// tenant-erasure adapter. It drops every blob under the tenant's
// prefix (live or tombstoned) in a single pass and returns the count
// removed; a tenant with no blobs is a no-op returning (0, nil). An
// empty tenantID matches nothing so a mis-scoped call cannot wipe the
// whole store.
//
// spec: §12.5 ll. 295.
func (s *MemoryStore) DeleteByTenant(_ context.Context, tenantID string) (int, error) {
	if tenantID == "" {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	deleted := 0
	for k, b := range s.blobs {
		if b.info.URI.TenantID == tenantID {
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
	// spec: §12.9 line 1048 — the derive byte-copy is a write; a T4
	// destination is rejected for the same reason a Put is.
	if err := checkTierStoreMismatch(s.tierGuard, "in-memory", dst.TenantID, s.onTierMismatch); err != nil {
		return err
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

// PresignPut implements Presigner with the §12.5 ll. 295 tenant-prefix
// invariant: the caller-tenant gate runs at mint time so a scoped
// handle cannot sign a capability for a foreign tenant's key.
//
// spec: §13.2 (capability model); §12.5 ll. 295.
func (s *TenantScoped) PresignPut(u URI, contentLength int64, ttl time.Duration) (Grant, error) {
	if err := s.check(u); err != nil {
		return Grant{}, err
	}
	p, ok := s.inner.(Presigner)
	if !ok {
		return Grant{}, fmt.Errorf("blobstore: underlying store %T does not implement Presigner", s.inner)
	}
	return p.PresignPut(u, contentLength, ttl)
}

// PresignGet implements Presigner with the §12.5 ll. 295 tenant-prefix
// invariant, mirroring PresignPut on the read path.
//
// spec: §13.2 (capability model); §12.5 ll. 295.
func (s *TenantScoped) PresignGet(u URI, ttl time.Duration) (Grant, error) {
	if err := s.check(u); err != nil {
		return Grant{}, err
	}
	p, ok := s.inner.(Presigner)
	if !ok {
		return Grant{}, fmt.Errorf("blobstore: underlying store %T does not implement Presigner", s.inner)
	}
	return p.PresignGet(u, ttl)
}

// DeleteByTenant implements TenantPrefixDeleter with the §12.5 ll. 295
// tenant-prefix invariant: a store bound to tenant T only permits
// DeleteByTenant(T); a request to bulk-delete a different tenant
// returns ErrCrossTenant before any object is touched. An unscoped
// wrapper (empty tenant) forwards any tenant through to the inner
// deleter for the §12.8 Phase 4 orchestrator that legitimately spans
// tenants.
func (s *TenantScoped) DeleteByTenant(ctx context.Context, tenantID string) (int, error) {
	if s.tenant != "" && tenantID != s.tenant {
		return 0, ErrCrossTenant
	}
	del, ok := s.inner.(TenantPrefixDeleter)
	if !ok {
		return 0, fmt.Errorf("blobstore: underlying store %T does not implement TenantPrefixDeleter", s.inner)
	}
	return del.DeleteByTenant(ctx, tenantID)
}

// DeleteByUser implements the §12.1 Eraser primitive. Artifact erasure
// is session-scoped (§12.8 step 7), so this whole-user call is a no-op
// returning (0, nil), as it is on every blob backend.
func (s *TenantScoped) DeleteByUser(_ context.Context, _, _ string) (int, error) {
	return 0, nil
}

var (
	_ Eraser    = (*MemoryStore)(nil)
	_ Eraser    = (*TenantScoped)(nil)
	_ Presigner = (*TenantScoped)(nil)
)
