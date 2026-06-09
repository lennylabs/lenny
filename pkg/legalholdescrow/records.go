// SPDX-License-Identifier: MIT

package legalholdescrow

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Record is the durable §12.8 sub-step 4 escrow ledger row: the marker
// Phase 4's DeleteByTenant skip logic reads to leave an escrowed object in
// place, and the index the escrow-GC release path queries when a hold is
// cleared on a tombstoned tenant. One record per migrated resource,
// identified by its escrow object key.
//
// spec: §12.8 lines 884-885.
type Record struct {
	TenantID        string
	ResourceType    string
	ResourceID      string
	EscrowObjectKey string
	EscrowRegion    string
	EscrowKEKID     string
	TenantDeleteJob string
	// SessionID is the owning session of an escrowed artifact, so clearing
	// the session hold releases every artifact escrowed under it. Empty for
	// a non-session-scoped resource.
	SessionID string
	// ArtifactURI is the raw artifact URI an artifact record was escrowed
	// from, so clearing the artifact's own hold releases exactly it. Empty
	// for a non-artifact resource.
	ArtifactURI     string
	OriginalHoldSet time.Time
	MigratedAt      time.Time
	// Released and ReleasedAt/ReleasedBy record the escrow-GC release. A
	// released record is excluded from the active-set queries so a re-cleared
	// hold does not re-delete (idempotent).
	Released   bool
	ReleasedAt time.Time
	ReleasedBy string
}

// RecordStore persists the §12.8 escrow ledger records and serves the
// release-path lookups. The store outlives the tenant tombstone (it is
// platform-scoped), so a hold cleared after Phase 4 still resolves the
// escrow objects to delete.
//
// spec: §12.8 lines 884-885.
type RecordStore interface {
	// Save records one migrated resource. It is idempotent on
	// (tenant_id, escrow_object_key): a re-entered Phase 3.5 overwrites the
	// same row rather than duplicating it.
	Save(ctx context.Context, rec Record) error
	// ActiveForSession returns the tenant's not-yet-released escrow records
	// owned by sessionID (the artifacts escrowed under a held session).
	ActiveForSession(ctx context.Context, tenantID, sessionID string) ([]Record, error)
	// ActiveForArtifact returns the tenant's not-yet-released escrow records
	// for the artifact at artifactURI.
	ActiveForArtifact(ctx context.Context, tenantID, artifactURI string) ([]Record, error)
	// MarkReleased flips the record at (tenant_id, escrow_object_key) to
	// released, recording who cleared the hold and when.
	MarkReleased(ctx context.Context, tenantID, escrowObjectKey, by string, at time.Time) error
}

// MemRecordStore is the in-memory RecordStore. The zero value is not
// usable; construct with NewMemRecordStore.
type MemRecordStore struct {
	mu   sync.Mutex
	recs map[string]Record // keyed by tenantID + "\x00" + escrowObjectKey
}

// NewMemRecordStore returns an empty in-memory escrow record store.
func NewMemRecordStore() *MemRecordStore {
	return &MemRecordStore{recs: map[string]Record{}}
}

func memKey(tenantID, escrowObjectKey string) string {
	return tenantID + "\x00" + escrowObjectKey
}

// Save implements RecordStore.
func (m *MemRecordStore) Save(_ context.Context, rec Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recs[memKey(rec.TenantID, rec.EscrowObjectKey)] = rec
	return nil
}

func (m *MemRecordStore) active(tenantID string, match func(Record) bool) []Record {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Record
	for _, r := range m.recs {
		if r.TenantID == tenantID && !r.Released && match(r) {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EscrowObjectKey < out[j].EscrowObjectKey })
	return out
}

// ActiveForSession implements RecordStore.
func (m *MemRecordStore) ActiveForSession(_ context.Context, tenantID, sessionID string) ([]Record, error) {
	return m.active(tenantID, func(r Record) bool { return r.SessionID == sessionID }), nil
}

// ActiveForArtifact implements RecordStore.
func (m *MemRecordStore) ActiveForArtifact(_ context.Context, tenantID, artifactURI string) ([]Record, error) {
	return m.active(tenantID, func(r Record) bool { return r.ArtifactURI == artifactURI }), nil
}

// MarkReleased implements RecordStore.
func (m *MemRecordStore) MarkReleased(_ context.Context, tenantID, escrowObjectKey, by string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := memKey(tenantID, escrowObjectKey)
	rec, ok := m.recs[k]
	if !ok {
		return nil
	}
	rec.Released = true
	rec.ReleasedAt = at.UTC()
	rec.ReleasedBy = by
	m.recs[k] = rec
	return nil
}

var _ RecordStore = (*MemRecordStore)(nil)
