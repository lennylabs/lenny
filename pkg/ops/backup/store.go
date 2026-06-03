// SPDX-License-Identifier: MIT

package backup

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// ErrNotFound is returned by a Store when a backup, restore, or
// singleton row does not exist.
var ErrNotFound = errors.New("backup: record not found")

// Store is the §25.11 durable state of the backup pipeline: the
// ops_backups, ops_backup_schedule, ops_retention_policy, and
// ops_restore_state tables. The production implementation is
// Postgres-backed; MemStore is the in-memory implementation the unit
// tests and a Postgres-less local deployment use. The §25.11 degrade
// rule (Postgres down: backup creation, listing, and scheduling fail
// 503) is the caller's responsibility — a Store returning an error
// surfaces as BACKUP_STORAGE_UNREACHABLE.
type Store interface {
	// InsertBackup writes a new ops_backups row. It is the first step of
	// the §25.11 Insert-Before-Job creation sequence: the row exists
	// before any Kubernetes Job is created.
	InsertBackup(ctx context.Context, b Backup) error
	// UpdateBackup overwrites the ops_backups row identified by b.ID.
	UpdateBackup(ctx context.Context, b Backup) error
	// GetBackup reads one ops_backups row. It returns ErrNotFound when no
	// row of that ID exists.
	GetBackup(ctx context.Context, id string) (Backup, error)
	// ListBackups reads ops_backups rows matching filter, ordered
	// newest-first.
	ListBackups(ctx context.Context, filter BackupFilter) ([]Backup, error)

	// GetSchedule reads the ops_backup_schedule singleton.
	GetSchedule(ctx context.Context) (BackupSchedule, error)
	// PutSchedule overwrites the ops_backup_schedule singleton.
	PutSchedule(ctx context.Context, s BackupSchedule) error

	// GetPolicy reads the ops_retention_policy singleton.
	GetPolicy(ctx context.Context) (RetentionPolicy, error)
	// PutPolicy overwrites the ops_retention_policy singleton.
	PutPolicy(ctx context.Context, p RetentionPolicy) error

	// InsertRestore writes a new ops_restore_state row.
	InsertRestore(ctx context.Context, r RestoreState) error
	// UpdateRestore overwrites the ops_restore_state row identified by
	// r.ID.
	UpdateRestore(ctx context.Context, r RestoreState) error
	// GetRestore reads one ops_restore_state row. It returns ErrNotFound
	// when no row of that ID exists.
	GetRestore(ctx context.Context, id string) (RestoreState, error)
	// ListRestores reads ops_restore_state rows matching filter, ordered
	// newest-first by started_at. It backs the restore-completion
	// reconciler that polls running restores to completion.
	ListRestores(ctx context.Context, filter RestoreFilter) ([]RestoreState, error)
}

// RestoreFilter is the ListRestores query filter. A zero-value filter
// matches every restore.
type RestoreFilter struct {
	// Status, when non-empty, restricts the listing to one restore status.
	Status string
}

// MemStore is the in-memory §25.11 Store. It backs the unit tests and a
// Postgres-less local deployment. It is goroutine-safe.
type MemStore struct {
	mu       sync.Mutex
	backups  map[string]Backup
	restores map[string]RestoreState
	schedule BackupSchedule
	policy   RetentionPolicy
}

var _ Store = (*MemStore)(nil)

// NewMemStore returns a MemStore seeded with the §25.11 default
// schedule (full daily at 02:00 UTC, Postgres every 6 hours) and the
// Tier-2 default retention policy.
func NewMemStore() *MemStore {
	return &MemStore{
		backups:  make(map[string]Backup),
		restores: make(map[string]RestoreState),
		schedule: BackupSchedule{Full: "0 2 * * *", Postgres: "0 */6 * * *", Enabled: true},
		policy:   RetentionPolicy{RetainDays: 30, RetainCount: 10, RetainMinFull: 3},
	}
}

// InsertBackup implements Store.
func (m *MemStore) InsertBackup(_ context.Context, b Backup) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.backups[b.ID]; ok {
		return errors.New("backup: duplicate backup id " + b.ID)
	}
	m.backups[b.ID] = b
	return nil
}

// UpdateBackup implements Store.
func (m *MemStore) UpdateBackup(_ context.Context, b Backup) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.backups[b.ID]; !ok {
		return ErrNotFound
	}
	m.backups[b.ID] = b
	return nil
}

// GetBackup implements Store.
func (m *MemStore) GetBackup(_ context.Context, id string) (Backup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.backups[id]
	if !ok {
		return Backup{}, ErrNotFound
	}
	return b, nil
}

// ListBackups implements Store. The result is ordered newest-first by
// StartedAt, ties broken by ID descending so the cursor is stable.
func (m *MemStore) ListBackups(_ context.Context, filter BackupFilter) ([]Backup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Backup
	for _, b := range m.backups {
		if filter.Type != "" && b.Type != filter.Type {
			continue
		}
		if filter.Status != "" && b.Status != filter.Status {
			continue
		}
		if !filter.Since.IsZero() && b.StartedAt.Before(filter.Since) {
			continue
		}
		if !filter.Until.IsZero() && b.StartedAt.After(filter.Until) {
			continue
		}
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].StartedAt.After(out[j].StartedAt)
	})
	return out, nil
}

// GetSchedule implements Store.
func (m *MemStore) GetSchedule(context.Context) (BackupSchedule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.schedule, nil
}

// PutSchedule implements Store.
func (m *MemStore) PutSchedule(_ context.Context, s BackupSchedule) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.schedule = s
	return nil
}

// GetPolicy implements Store.
func (m *MemStore) GetPolicy(context.Context) (RetentionPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.policy, nil
}

// PutPolicy implements Store.
func (m *MemStore) PutPolicy(_ context.Context, p RetentionPolicy) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.policy = p
	return nil
}

// InsertRestore implements Store.
func (m *MemStore) InsertRestore(_ context.Context, r RestoreState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.restores[r.ID]; ok {
		return errors.New("backup: duplicate restore id " + r.ID)
	}
	m.restores[r.ID] = r
	return nil
}

// UpdateRestore implements Store.
func (m *MemStore) UpdateRestore(_ context.Context, r RestoreState) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.restores[r.ID]; !ok {
		return ErrNotFound
	}
	m.restores[r.ID] = r
	return nil
}

// GetRestore implements Store.
func (m *MemStore) GetRestore(_ context.Context, id string) (RestoreState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.restores[id]
	if !ok {
		return RestoreState{}, ErrNotFound
	}
	return r, nil
}

// ListRestores implements Store. The result is ordered newest-first by
// StartedAt, ties broken by ID descending so iteration is stable.
func (m *MemStore) ListRestores(_ context.Context, filter RestoreFilter) ([]RestoreState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []RestoreState
	for _, r := range m.restores {
		if filter.Status != "" && r.Status != filter.Status {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].ID > out[j].ID
		}
		return out[i].StartedAt.After(out[j].StartedAt)
	})
	return out, nil
}

// pendingOlderThan returns the IDs of ops_backups rows still in
// status:pending whose StartedAt is older than the cutoff. The §25.11
// reconciler marks these failed (JOB_CREATE_FAILED) — the Job creation
// never happened.
func (m *MemStore) pendingOlderThan(cutoff time.Time) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var ids []string
	for id, b := range m.backups {
		if b.Status == StatusPending && b.StartedAt.Before(cutoff) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}
