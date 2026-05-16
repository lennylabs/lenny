// SPDX-License-Identifier: MIT

// Package tenantstore is the platform tenant registry. It backs the
// §10.2 tenant-claim extractor (via auth.TenantRegistry) and the
// §15.1 admin tenant CRUD endpoints.
//
// The in-memory implementation is the minimal-gateway backend; the
// Postgres-backed implementation that ships in a later phase swaps
// in behind the same Store interface.
package tenantstore

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
)

// Tenant captures the platform-scoped registry row per §12.5 and
// §12.8.
type Tenant struct {
	// ID is the tenant identifier — must satisfy the §10.2 format
	// (`^[a-zA-Z0-9_-]{1,128}$`).
	ID string

	// DisplayName is a human-readable label. Optional.
	DisplayName string

	// ComplianceProfile is the §12.8 profile applied to this tenant
	// (`none`, `soc2`, `hipaa`, `gdpr`, `fedramp`). Empty when the
	// tenant has no compliance profile assigned.
	ComplianceProfile string

	// DataResidencyRegion pins the tenant's storage routing to a
	// specific region per §12.8. Empty for unscoped tenants.
	DataResidencyRegion string

	// WorkspaceTier is the §12.9 storage tier (`T1`, `T2`, `T3`,
	// `T4`). Empty defaults to platform default at write time.
	WorkspaceTier string

	// MaxConcurrentSessions is the §11.2 per-tenant concurrent-session
	// quota: the gateway rejects a session create once the tenant
	// holds this many non-terminal sessions. Zero means unlimited.
	MaxConcurrentSessions int

	// StorageQuotaBytes is the §11.2 per-tenant storage quota: the
	// gateway rejects an upload once the tenant's reserved-plus-
	// committed artifact bytes would exceed this value. Zero means
	// unlimited.
	StorageQuotaBytes int64

	// ElicitationContentIntegrity is the §9.2 tenant-stored elicitation
	// content-integrity mode (`off`, `detect-only`, or `enforce`). The
	// gateway clamps it against the platform floor at use time. Empty
	// means the tenant has set no override and the platform floor
	// applies.
	ElicitationContentIntegrity string

	// MinIsolationProfile is the §5.3 tenant isolation floor: the
	// weakest §5.3 profile (`standard`, `sandboxed`, `microvm`) the
	// tenant's sessions may run at. Empty means no tenant floor — the
	// platform default applies.
	MinIsolationProfile string

	// CreatedAt is the UTC instant the tenant row was committed.
	CreatedAt time.Time

	// UpdatedAt is the UTC instant of the last admin mutation.
	UpdatedAt time.Time

	// DeletedAt is the UTC instant the tenant was soft-deleted per
	// §12.8 tenant lifecycle. Nil when active.
	DeletedAt time.Time
}

// IsActive reports whether the tenant has not been soft-deleted.
func (t Tenant) IsActive() bool { return t.DeletedAt.IsZero() }

// Store is the §12.8 platform-tenant registry contract.
type Store interface {
	// Create inserts a new tenant row. Returns ErrAlreadyExists when
	// the ID is taken.
	Create(ctx context.Context, t Tenant) error

	// Get returns the tenant row keyed by id. Returns ErrNotFound
	// when the row is missing — soft-deleted rows ARE returned
	// (callers consult Tenant.IsActive() to filter).
	Get(ctx context.Context, id string) (Tenant, error)

	// Update applies mutate to the row and persists the result.
	// Returns ErrNotFound when the row is missing. The store does
	// not validate the mutation — callers run any admin-side
	// validation first.
	Update(ctx context.Context, id string, mutate func(*Tenant) error) (Tenant, error)

	// List returns every tenant row in created-at-descending order
	// after applying the filter. IncludeDeleted false drops
	// soft-deleted rows.
	List(ctx context.Context, filter ListFilter) ([]Tenant, error)

	// SoftDelete sets DeletedAt on the row per §12.8 tenant
	// lifecycle. The row remains queryable for audit; the operator
	// later invokes the tenant-deletion controller to fully erase.
	SoftDelete(ctx context.Context, id string, at time.Time) error
}

// ListFilter narrows List results. Zero value returns every active
// tenant.
type ListFilter struct {
	// IncludeDeleted, when true, returns soft-deleted rows as well.
	IncludeDeleted bool
}

// Sentinel errors.
var (
	// ErrNotFound — the tenant id is not in the registry.
	ErrNotFound = errors.New("tenantstore: tenant not found")

	// ErrAlreadyExists — a tenant with this id is already
	// registered.
	ErrAlreadyExists = errors.New("tenantstore: tenant already exists")
)

// Memory is the in-memory Store backing tests and the minimal
// gateway.
type Memory struct {
	mu      sync.RWMutex
	tenants map[string]Tenant
}

// NewMemory returns an empty Memory store.
func NewMemory() *Memory { return &Memory{tenants: map[string]Tenant{}} }

// Create implements Store.
func (m *Memory) Create(_ context.Context, t Tenant) error {
	if err := auth.ValidateTenantID(t.ID); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.tenants[t.ID]; exists {
		return ErrAlreadyExists
	}
	now := time.Now().UTC()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	if t.UpdatedAt.IsZero() {
		t.UpdatedAt = t.CreatedAt
	}
	m.tenants[t.ID] = t
	return nil
}

// Get implements Store.
func (m *Memory) Get(_ context.Context, id string) (Tenant, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	row, ok := m.tenants[id]
	if !ok {
		return Tenant{}, ErrNotFound
	}
	return row, nil
}

// Update implements Store.
func (m *Memory) Update(_ context.Context, id string, mutate func(*Tenant) error) (Tenant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.tenants[id]
	if !ok {
		return Tenant{}, ErrNotFound
	}
	prev := row.UpdatedAt
	if err := mutate(&row); err != nil {
		return Tenant{}, err
	}
	now := time.Now().UTC()
	if !now.After(prev) {
		now = prev.Add(time.Nanosecond)
	}
	row.UpdatedAt = now
	m.tenants[id] = row
	return row, nil
}

// List implements Store.
func (m *Memory) List(_ context.Context, filter ListFilter) ([]Tenant, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Tenant, 0, len(m.tenants))
	for _, row := range m.tenants {
		if !filter.IncludeDeleted && !row.IsActive() {
			continue
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// SoftDelete implements Store.
func (m *Memory) SoftDelete(_ context.Context, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.tenants[id]
	if !ok {
		return ErrNotFound
	}
	if !row.DeletedAt.IsZero() {
		// Idempotent — already soft-deleted.
		return nil
	}
	row.DeletedAt = at
	row.UpdatedAt = at
	m.tenants[id] = row
	return nil
}

// IsRegistered implements auth.TenantRegistry. Returns false for
// soft-deleted tenants so the §10.2 tenant-claim extractor rejects
// requests against deleted tenants with TENANT_NOT_FOUND.
func (m *Memory) IsRegistered(id string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	row, ok := m.tenants[id]
	if !ok {
		return false, nil
	}
	return row.IsActive(), nil
}

// Ensure Memory satisfies auth.TenantRegistry at compile time.
var _ auth.TenantRegistry = (*Memory)(nil)
