// SPDX-License-Identifier: MIT

// Package partialmanifeststore persists the §10.1 checkpoint_manifest
// row the gateway writes intent-row-first at the start of every
// checkpoint attempt and updates as chunks commit. The row records the
// per-attempt upload state: the gateway-minted checkpoint_id, the
// (session_id, slot_id) scoping key, the coordination and recovery
// generations, the chunk_object_key_prefix the chunks live under, the
// running chunk_count / workspace_bytes_uploaded counters, the §11.2
// storage reservation and its exactly-once release guard, and the
// terminal disposition (partial, manifest_reason). The §7.2 resume path
// reads the row to reassemble a partial workspace; the §4.4 cleanup path
// and the §12.5 backstop sweep read it to release the chunk objects and
// reservation and soft-delete the row.
//
// spec: §10.1 lines 141-151.
package partialmanifeststore

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ChunkEncoding is the §10.1 closed enum of chunk wire encodings the
// adapter may write. The resume-time decode pipeline reads this off
// the manifest row rather than inferring from the object-key suffix.
type ChunkEncoding string

const (
	ChunkEncodingTar   ChunkEncoding = "tar"
	ChunkEncodingTarGz ChunkEncoding = "tar.gz"
)

// IsValid reports whether e is one of the spec-defined chunk
// encodings.
func (e ChunkEncoding) IsValid() bool {
	return e == ChunkEncodingTar || e == ChunkEncodingTarGz
}

// The §10.1 line 141 closed enum of manifest_reason dispositions. A row
// carries ReasonInProgress from intent-row INSERT until a terminal arm
// overwrites it.
const (
	ReasonInProgress      = "in_progress"
	ReasonComplete        = "complete"
	ReasonTimeout         = "timeout"
	ReasonStreamTruncated = "stream_truncated"
	ReasonSuperseded      = "superseded"
	ReasonQuotaExceeded   = "quota_exceeded"
)

// IsValidReason reports whether r is one of the §10.1 line 141
// manifest_reason enum values.
func IsValidReason(r string) bool {
	switch r {
	case ReasonInProgress, ReasonComplete, ReasonTimeout,
		ReasonStreamTruncated, ReasonSuperseded, ReasonQuotaExceeded:
		return true
	default:
		return false
	}
}

// SlotDefault is the sentinel slot_id for pools with
// maxConcurrentSessions: 1, so the (session_id, slot_id) scoping key is
// well-defined for every session (§10.1 line 147).
const SlotDefault = "default"

// Record is one checkpoint_manifest row.
type Record struct {
	// TenantID and CheckpointID are the composite primary key. The
	// gateway mints CheckpointID (a UUID) at intent-row INSERT.
	TenantID     string
	CheckpointID string
	// SessionID and SlotID are the §10.1 line 147 scoping key the
	// partial_manifest_active_uniq index and the supersede predicate
	// key on. SlotID defaults to SlotDefault.
	SessionID string
	SlotID    string
	// CoordinationGeneration is the coordinator's fenced generation at
	// intent-row INSERT. The resume path selects the row at
	// MAX(coordination_generation) so a late-committed older-generation
	// row cannot win against a fenced newer writer under split-brain,
	// and the supersede predicate fences on it (§10.1 lines 141, 155).
	CoordinationGeneration int64
	// RecoveryGeneration is the session's recovery counter at the time
	// the row was written, captured for audit reconstruction.
	RecoveryGeneration int64
	// Partial is the §10.1 line 141 disposition flag. It is true from
	// intent-row INSERT and stays true on every terminal arm except a
	// complete checkpoint, which finalises Partial = false.
	Partial bool
	// ManifestReason is the §10.1 line 141 manifest_reason enum value.
	ManifestReason string
	// ChunkObjectKeyPrefix is the
	// /{tenant_id}/checkpoints/{session_id}/{checkpoint_id}/ prefix
	// under which the committed chunks live. Captured at intent-row
	// INSERT and never modified after.
	ChunkObjectKeyPrefix string
	// ChunkSizeBytes is the partialChunkSizeBytes value in effect for
	// this manifest, captured at intent-row INSERT.
	ChunkSizeBytes int64
	// ChunkEncoding is the wire encoding the adapter wrote chunks with.
	ChunkEncoding ChunkEncoding
	// ChunkCount is the number of chunks committed (highest {n} + 1),
	// initialised to 0 at intent-row INSERT and updated monotonically by
	// ConfirmChunk as chunks commit.
	ChunkCount int
	// WorkspaceBytesUploaded is the sum of chunk sizes committed to
	// object storage, initialised to 0 and updated monotonically by
	// ConfirmChunk.
	WorkspaceBytesUploaded int64
	// ReservedBytes is the §11.2 storage reservation taken from the
	// adapter's probe at intent-row INSERT, so any party that finalises
	// or sweeps the row can release it.
	ReservedBytes int64
	// ReservationReleasedAt is the exactly-once release guard for that
	// reservation across the in-process finalisation arms and the §12.5
	// backstop. Zero while the reservation is outstanding; set once by
	// ReleaseReservation.
	ReservationReleasedAt time.Time
	// BaselineFullCheckpointBytes is the session's
	// last_checkpoint_workspace_bytes at intent-row INSERT, frozen for
	// the life of the row. Nil is load-bearing: it denotes a session
	// with no prior successful full checkpoint (§10.1 line 155).
	BaselineFullCheckpointBytes *int64
	// CheckpointStartedAt and CheckpointTimeoutAt bound the attempt. The
	// §12.5 backstop treats a row past CheckpointTimeoutAt as reclaimable
	// even while ManifestReason is still in_progress.
	CheckpointStartedAt time.Time
	CheckpointTimeoutAt time.Time
	// CreatedAt is set on intent-row INSERT and immutable thereafter.
	CreatedAt time.Time
	// DeletedAt is the §12.5 soft-delete tombstone. Zero while the row
	// is active; set by SoftDelete and never unset.
	DeletedAt time.Time
}

// ErrNotFound is returned by Get / LatestActive when no row matches the
// supplied key.
var ErrNotFound = errors.New("partialmanifeststore: row not found")

// ErrStaleGeneration is returned by Put when an active partial manifest
// at a strictly higher coordination_generation already exists for the
// same (session, slot). The write is a fenced stale-coordinator attempt:
// honoring it would downgrade the active manifest below the highest
// fenced generation, which the §10.1 line 155 MAX(coordination_generation)
// resume selector forbids. The losing writer rolls back and re-reads.
//
// spec: §10.1 lines 137, 155.
var ErrStaleGeneration = errors.New("partialmanifeststore: a higher-generation active manifest already exists")

// ErrAlreadyExists is returned by Put when a row already carries the
// incoming checkpoint_id. The intent row is written once per attempt;
// a duplicate checkpoint_id is a minting error.
var ErrAlreadyExists = errors.New("partialmanifeststore: a row with this checkpoint_id already exists")

// Store persists checkpoint_manifest rows. The in-memory MemoryStore
// backs unit tests and the developer-mode deployment; the
// Postgres-backed implementation lives in
// pkg/gateway/checkpoint/partialmanifeststore/pgstore.
//
// The DeleteByUser / DeleteByTenant methods carry the §12.8 GDPR
// erasure contract.
type Store interface {
	// Put writes the §10.1 intent row for a fresh checkpoint attempt.
	// In one transaction it fences on coordination_generation and
	// supersedes prior attempts for the same (session, slot): a write
	// whose generation is below an already-active strictly-higher
	// generation is rejected with ErrStaleGeneration; every active
	// partial row at or below the incoming generation is soft-deleted
	// (manifest_reason = 'superseded') before the INSERT, so the
	// partial_manifest_active_uniq index never sees two active partial
	// rows for one (session, slot). A duplicate checkpoint_id is
	// rejected with ErrAlreadyExists. Returns an error when a required
	// field is empty or invalid.
	//
	// spec: §10.1 lines 137, 143-151, 155.
	Put(ctx context.Context, r Record) error

	// Get returns the row for (tenantID, checkpointID) or ErrNotFound.
	// A soft-deleted row is still returned (DeletedAt is non-zero).
	Get(ctx context.Context, tenantID, checkpointID string) (Record, error)

	// LatestActive returns the highest-coordination_generation active
	// partial row for (tenantID, sessionID) — the §4.4 cleanup selector.
	// It carries the `partial = TRUE` predicate so a completed checkpoint
	// is never destroyed by the resume-time cleaner. Returns ErrNotFound
	// when no active partial row exists.
	//
	// spec: §10.1 line 157 — the resume-time cleaner selects partial rows.
	LatestActive(ctx context.Context, tenantID, sessionID string) (Record, error)

	// LatestActiveAny returns the highest-coordination_generation active row
	// (`deleted_at IS NULL`) for (tenantID, sessionID) regardless of
	// `partial`, taking the most recently created row among rows at that
	// generation — the §10.1 line 154 resume-reassembly selector. It resolves
	// the checkpoint the resume restores from: a newer partial = true drain
	// row at or above a completed checkpoint's generation wins, so the
	// partialRecoveryThresholdFraction gate can arbitrate. Returns ErrNotFound
	// when no active row exists.
	//
	// spec: §10.1 line 154 — select the active row at
	// MAX(coordination_generation) regardless of partial.
	LatestActiveAny(ctx context.Context, tenantID, sessionID string) (Record, error)

	// HasActivePartialManifest reports whether an active partial row
	// exists for (tenantID, sessionID) — the §7.2 resume-classification
	// input. It is LatestActive collapsed to a boolean: true when a
	// `partial = TRUE AND deleted_at IS NULL` row is present, false when
	// none is. A store error is surfaced so the resume path can degrade to
	// ResumeFull rather than block on a transient outage.
	//
	// spec: §10.1 partial-manifest path — `session.resumed` carries
	// `resumeMode: "partial_workspace"` when an active partial manifest is
	// present.
	HasActivePartialManifest(ctx context.Context, tenantID, sessionID string) (bool, error)

	// LatestFull returns the most-recently-created active full checkpoint
	// row (`partial = FALSE AND deleted_at IS NULL`) for
	// (tenantID, sessionID) — the §10.1 line 155 "last successful full
	// checkpoint" the resume path falls back to when reassembly of the
	// selected partial manifest fails its contiguity check. Returns
	// ErrNotFound when the session has no surviving full checkpoint.
	//
	// spec: §10.1 line 155 — fallback to the last successful full checkpoint.
	LatestFull(ctx context.Context, tenantID, sessionID string) (Record, error)

	// LatestActiveForSlot returns the active partial row for the single
	// (tenantID, sessionID, slotID) the supersede path scopes on. The
	// partial_manifest_active_uniq index admits at most one active partial
	// row per (session, slot), so the result is that row when present. It
	// carries the `partial = TRUE` predicate. Returns ErrNotFound when no
	// active partial row exists for the slot. Unlike LatestActive, which is
	// session-wide and returns an arbitrary slot's row on tied generations,
	// this selector matches exactly the set Put supersedes, so a multi-slot
	// session's supersede release never misses the target slot's prior row.
	//
	// spec: §10.1 line 137 — supersede is scoped to
	// (tenant_id, session_id, slot_id).
	LatestActiveForSlot(ctx context.Context, tenantID, sessionID, slotID string) (Record, error)

	// ConfirmChunk advances the monotonic chunk_count / workspace_bytes
	// counters as chunk n commits. It applies the §10.1 line 131
	// conditional UPDATE (`chunk_count < n + 1` guard) so out-of-order
	// acknowledgements cannot decrement the counter, under the
	// `deleted_at IS NULL` guard. A no-op (returns nil) when the row is
	// missing, soft-deleted, or already at or beyond n + 1.
	//
	// spec: §10.1 line 131.
	ConfirmChunk(ctx context.Context, tenantID, checkpointID string, n int, workspaceBytesUploaded int64) error

	// Finalise stamps the terminal disposition on the row under the
	// `deleted_at IS NULL` guard: a complete checkpoint finalises
	// partial = false, manifest_reason = 'complete'; every other arm
	// finalises partial = true with the reason that names it. When the row
	// carries chunk_count == 0 at finalisation time the same statement also
	// soft-deletes it, so an empty manifest (an adapter crash, a stream
	// truncation, or a quota refusal before the first chunk confirmed) is
	// never left active for the resume path or a downstream reclaimer to
	// pick up. A no-op when the row is missing or soft-deleted.
	//
	// spec: §10.1 lines 132, 141 — the zero-chunk finalisation soft-deletes
	// the row in the same transaction.
	Finalise(ctx context.Context, tenantID, checkpointID string, partial bool, manifestReason string) error

	// SoftDelete sets `deleted_at = now()` on the row under the
	// `deleted_at IS NULL` predicate. Returns nil when the row is
	// already soft-deleted or missing — the cleanup path is idempotent
	// so a stale-leader retry, a crash-resumed resume path, or the §12.5
	// backstop that races the primary cleanup all converge to a single
	// state mutation.
	//
	// spec: §12.5 GC rule 6.
	SoftDelete(ctx context.Context, tenantID, checkpointID string) error

	// ListSoftDeletedBefore returns every row whose DeletedAt is older
	// than cutoff across every tenant. The §12.5 hard-prune sweep walks
	// the result to remove tombstoned rows.
	ListSoftDeletedBefore(ctx context.Context, cutoff time.Time) ([]Record, error)

	// ListReclaimable returns every abandoned active row the §12.5
	// backstop must sweep across every tenant. The predicate is
	// `partial = true AND (manifest_reason != 'in_progress' OR
	// now() > checkpoint_timeout_at) AND (terminal_state OR
	// created_at < now() - maxResumeWindow) AND deleted_at IS NULL`, so
	// a live seal-checkpoint attempt holding an in_progress intent row
	// inside its timeout is never swept out from under it.
	//
	// spec: §12.5 GC rule 6.
	ListReclaimable(ctx context.Context, maxResumeWindow time.Duration) ([]Record, error)

	// HardDelete removes the row from the table entirely. The §12.5
	// hard-prune sweep calls this on rows whose DeletedAt is older than
	// the tombstone retention window.
	HardDelete(ctx context.Context, tenantID, checkpointID string) error

	// ReleaseReservation stamps `reservation_released_at = now()` on the
	// row under the `reservation_released_at IS NULL` guard and reports
	// the rows affected (1 on the first release, 0 on every retry). The
	// guard makes the §11.2 reservation decrement exactly-once across the
	// in-process finalisation arms and the §12.5 backstop.
	//
	// spec: §11.2 reservation release, §12.5 GC rule 4.
	ReleaseReservation(ctx context.Context, tenantID, checkpointID string) (int64, error)

	// SumOutstandingReservations returns the sum of
	// (reserved_bytes - workspace_bytes_uploaded) over the tenant's rows
	// that are active and whose reservation has not been released. The
	// §11.2 reservation-aware storage-counter rebuild folds it into the
	// absolute value it sets so the relative decrement is rebuild-safe.
	//
	// spec: §11.2 reservation-aware LiveBytesSource.
	SumOutstandingReservations(ctx context.Context, tenantID string) (int64, error)

	// DeleteByUser removes every row in tenantID whose session id
	// is in sessionIDs. Called by the §12.8 erasure orchestrator.
	DeleteByUser(ctx context.Context, tenantID, userID string, sessionIDs []string) error

	// DeleteByTenant removes every row scoped to tenantID. Idempotent.
	DeleteByTenant(ctx context.Context, tenantID string) error
}

// MemoryStore is an in-memory Store backing tests and the
// developer-mode deployment. It has no session-state view, so
// ListReclaimable evaluates the resume-window branch of the §12.5
// predicate only; the terminal-state widening is a Postgres-backend
// refinement that joins the sessions table.
type MemoryStore struct {
	mu   sync.Mutex
	rows map[recordKey]Record
	now  func() time.Time
}

// NewMemoryStore returns an empty MemoryStore. now selects the
// timestamp source; a nil now uses time.Now.
func NewMemoryStore(now func() time.Time) *MemoryStore {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &MemoryStore{rows: map[recordKey]Record{}, now: now}
}

var _ Store = (*MemoryStore)(nil)

// recordKey is the in-memory composite primary key.
type recordKey struct {
	tenantID     string
	checkpointID string
}

// normalise applies the intent-row defaults and validates the required
// fields.
func normalise(r *Record) error {
	if r.TenantID == "" || r.SessionID == "" {
		return fmt.Errorf("partialmanifeststore: tenant and session ids are required")
	}
	if r.CheckpointID == "" {
		return fmt.Errorf("partialmanifeststore: checkpoint_id is required")
	}
	if r.ChunkObjectKeyPrefix == "" {
		return fmt.Errorf("partialmanifeststore: chunk_object_key_prefix is required")
	}
	if r.SlotID == "" {
		r.SlotID = SlotDefault
	}
	if r.ChunkEncoding == "" {
		r.ChunkEncoding = ChunkEncodingTar
	}
	if !r.ChunkEncoding.IsValid() {
		return fmt.Errorf("partialmanifeststore: invalid chunk_encoding %q", r.ChunkEncoding)
	}
	if r.ManifestReason == "" {
		r.ManifestReason = ReasonInProgress
	}
	if !IsValidReason(r.ManifestReason) {
		return fmt.Errorf("partialmanifeststore: invalid manifest_reason %q", r.ManifestReason)
	}
	// The §10.1 step-1 intent row is always partial; only Finalise flips
	// it to false when a checkpoint completes with every byte confirmed.
	r.Partial = true
	return nil
}

// Put writes the §10.1 intent row, fencing on and superseding prior
// attempts for the same (session, slot).
func (m *MemoryStore) Put(_ context.Context, r Record) error {
	if err := normalise(&r); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	if _, exists := m.rows[recordKey{r.TenantID, r.CheckpointID}]; exists {
		return ErrAlreadyExists
	}
	// §10.1 line 155 fencing: reject a write whose generation sits below
	// an already-active strictly-higher generation before mutating
	// anything, so a stale-coordinator write never downgrades the active
	// manifest.
	for _, row := range m.rows {
		if row.TenantID != r.TenantID || row.SessionID != r.SessionID || row.SlotID != r.SlotID {
			continue
		}
		if row.Partial && row.DeletedAt.IsZero() && row.CoordinationGeneration > r.CoordinationGeneration {
			return ErrStaleGeneration
		}
	}
	// §10.1 line 137 supersede-on-write: soft-delete every active partial
	// row at or below the incoming generation so at most one active
	// partial row survives for this (session, slot).
	for key, row := range m.rows {
		if row.TenantID != r.TenantID || row.SessionID != r.SessionID || row.SlotID != r.SlotID {
			continue
		}
		if row.CheckpointID == r.CheckpointID {
			continue
		}
		if row.Partial && row.DeletedAt.IsZero() && row.CoordinationGeneration <= r.CoordinationGeneration {
			row.DeletedAt = now
			row.ManifestReason = ReasonSuperseded
			m.rows[key] = row
		}
	}
	r.CreatedAt = now
	m.rows[recordKey{r.TenantID, r.CheckpointID}] = r
	return nil
}

// Get returns the row or ErrNotFound.
func (m *MemoryStore) Get(_ context.Context, tenantID, checkpointID string) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.rows[recordKey{tenantID, checkpointID}]
	if !ok {
		return Record{}, ErrNotFound
	}
	return row, nil
}

// LatestActive returns the highest-generation active partial row for
// (tenantID, sessionID).
func (m *MemoryStore) LatestActive(_ context.Context, tenantID, sessionID string) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var best Record
	found := false
	for _, row := range m.rows {
		if row.TenantID != tenantID || row.SessionID != sessionID {
			continue
		}
		if !row.Partial || !row.DeletedAt.IsZero() {
			continue
		}
		if !found || row.CoordinationGeneration > best.CoordinationGeneration {
			best = row
			found = true
		}
	}
	if !found {
		return Record{}, ErrNotFound
	}
	return best, nil
}

// LatestActiveAny returns the highest-generation active row for
// (tenantID, sessionID) regardless of partial — the §10.1 line 154 resume
// selector. Ties on coordination_generation break to the most recently
// created row.
func (m *MemoryStore) LatestActiveAny(_ context.Context, tenantID, sessionID string) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var best Record
	found := false
	for _, row := range m.rows {
		if row.TenantID != tenantID || row.SessionID != sessionID {
			continue
		}
		if !row.DeletedAt.IsZero() {
			continue
		}
		if !found ||
			row.CoordinationGeneration > best.CoordinationGeneration ||
			(row.CoordinationGeneration == best.CoordinationGeneration && row.CreatedAt.After(best.CreatedAt)) {
			best = row
			found = true
		}
	}
	if !found {
		return Record{}, ErrNotFound
	}
	return best, nil
}

// HasActivePartialManifest reports whether an active partial row exists
// for (tenantID, sessionID).
func (m *MemoryStore) HasActivePartialManifest(ctx context.Context, tenantID, sessionID string) (bool, error) {
	_, err := m.LatestActive(ctx, tenantID, sessionID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// LatestFull returns the most-recently-created active full checkpoint row
// for (tenantID, sessionID), or ErrNotFound when none survives.
func (m *MemoryStore) LatestFull(_ context.Context, tenantID, sessionID string) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var best Record
	found := false
	for _, row := range m.rows {
		if row.TenantID != tenantID || row.SessionID != sessionID {
			continue
		}
		if row.Partial || !row.DeletedAt.IsZero() {
			continue
		}
		if !found || row.CreatedAt.After(best.CreatedAt) {
			best = row
			found = true
		}
	}
	if !found {
		return Record{}, ErrNotFound
	}
	return best, nil
}

// LatestActiveForSlot returns the active partial row for
// (tenantID, sessionID, slotID), the single slot the supersede path scopes
// on.
func (m *MemoryStore) LatestActiveForSlot(_ context.Context, tenantID, sessionID, slotID string) (Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var best Record
	found := false
	for _, row := range m.rows {
		if row.TenantID != tenantID || row.SessionID != sessionID || row.SlotID != slotID {
			continue
		}
		if !row.Partial || !row.DeletedAt.IsZero() {
			continue
		}
		if !found || row.CoordinationGeneration > best.CoordinationGeneration {
			best = row
			found = true
		}
	}
	if !found {
		return Record{}, ErrNotFound
	}
	return best, nil
}

// ConfirmChunk applies the monotonic chunk_count / workspace_bytes
// update under the `deleted_at IS NULL` and `chunk_count < n + 1` guards.
func (m *MemoryStore) ConfirmChunk(_ context.Context, tenantID, checkpointID string, n int, workspaceBytesUploaded int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := recordKey{tenantID, checkpointID}
	row, ok := m.rows[key]
	if !ok || !row.DeletedAt.IsZero() {
		return nil
	}
	if row.ChunkCount >= n+1 {
		return nil
	}
	row.ChunkCount = n + 1
	row.WorkspaceBytesUploaded = workspaceBytesUploaded
	m.rows[key] = row
	return nil
}

// Finalise stamps the terminal disposition under the `deleted_at IS NULL`
// guard, soft-deleting a zero-chunk row in the same mutation.
func (m *MemoryStore) Finalise(_ context.Context, tenantID, checkpointID string, partial bool, manifestReason string) error {
	if !IsValidReason(manifestReason) {
		return fmt.Errorf("partialmanifeststore: invalid manifest_reason %q", manifestReason)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := recordKey{tenantID, checkpointID}
	row, ok := m.rows[key]
	if !ok || !row.DeletedAt.IsZero() {
		return nil
	}
	row.Partial = partial
	row.ManifestReason = manifestReason
	// spec: §10.1 line 132 — a terminal arm that finalises with no chunks
	// committed soft-deletes the row in the same transaction, so an empty
	// manifest never occupies the partial_manifest_active_uniq slot waiting
	// on a later supersede or the §12.5 backstop to reclaim it.
	if row.ChunkCount == 0 {
		row.DeletedAt = m.now()
	}
	m.rows[key] = row
	return nil
}

// SoftDelete sets the tombstone under the `deleted_at IS NULL`
// idempotency guard.
func (m *MemoryStore) SoftDelete(_ context.Context, tenantID, checkpointID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := recordKey{tenantID, checkpointID}
	row, ok := m.rows[key]
	if !ok || !row.DeletedAt.IsZero() {
		return nil
	}
	row.DeletedAt = m.now()
	m.rows[key] = row
	return nil
}

// ListSoftDeletedBefore returns every soft-deleted row whose DeletedAt
// is older than cutoff.
func (m *MemoryStore) ListSoftDeletedBefore(_ context.Context, cutoff time.Time) ([]Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Record{}
	for _, row := range m.rows {
		if row.DeletedAt.IsZero() {
			continue
		}
		if row.DeletedAt.Before(cutoff) {
			out = append(out, row)
		}
	}
	return out, nil
}

// ListReclaimable returns every abandoned active row whose resume window
// has expired. The MemoryStore has no session-state view, so it
// evaluates only the resume-window branch of the §12.5 predicate.
func (m *MemoryStore) ListReclaimable(_ context.Context, maxResumeWindow time.Duration) ([]Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	windowStart := now.Add(-maxResumeWindow)
	out := []Record{}
	for _, row := range m.rows {
		if !row.Partial || !row.DeletedAt.IsZero() {
			continue
		}
		finalisedOrTimedOut := row.ManifestReason != ReasonInProgress || now.After(row.CheckpointTimeoutAt)
		if !finalisedOrTimedOut {
			continue
		}
		if !row.CreatedAt.Before(windowStart) {
			continue
		}
		out = append(out, row)
	}
	return out, nil
}

// HardDelete removes the row entirely.
func (m *MemoryStore) HardDelete(_ context.Context, tenantID, checkpointID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rows, recordKey{tenantID, checkpointID})
	return nil
}

// ReleaseReservation stamps reservation_released_at under the
// `reservation_released_at IS NULL` guard and reports the rows affected.
func (m *MemoryStore) ReleaseReservation(_ context.Context, tenantID, checkpointID string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := recordKey{tenantID, checkpointID}
	row, ok := m.rows[key]
	if !ok || !row.ReservationReleasedAt.IsZero() {
		return 0, nil
	}
	row.ReservationReleasedAt = m.now()
	m.rows[key] = row
	return 1, nil
}

// SumOutstandingReservations sums the tenant's unreleased reservation
// headroom over active rows.
func (m *MemoryStore) SumOutstandingReservations(_ context.Context, tenantID string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var sum int64
	for _, row := range m.rows {
		if row.TenantID != tenantID {
			continue
		}
		if !row.DeletedAt.IsZero() || !row.ReservationReleasedAt.IsZero() {
			continue
		}
		outstanding := row.ReservedBytes - row.WorkspaceBytesUploaded
		if outstanding > 0 {
			sum += outstanding
		}
	}
	return sum, nil
}

// DeleteByUser removes every row in tenantID whose session_id is in
// the supplied slice. The orchestrator owns the session-id lookup
// because checkpoint_manifest does not carry a user_id column.
func (m *MemoryStore) DeleteByUser(_ context.Context, tenantID, _ string, sessionIDs []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, row := range m.rows {
		if row.TenantID != tenantID {
			continue
		}
		for _, sessID := range sessionIDs {
			if row.SessionID == sessID {
				delete(m.rows, k)
			}
		}
	}
	return nil
}

// DeleteByTenant removes every row scoped to tenantID. Idempotent.
func (m *MemoryStore) DeleteByTenant(_ context.Context, tenantID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, row := range m.rows {
		if row.TenantID == tenantID {
			delete(m.rows, k)
		}
	}
	return nil
}
