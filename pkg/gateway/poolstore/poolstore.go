// SPDX-License-Identifier: MIT

// Package poolstore is the §5.2 SandboxWarmPool registry. It backs
// the pool admission used by session creation (`runtimeRef` →
// `SandboxWarmPool` lookup), the §15.1 admin pool CRUD endpoints,
// and the §4.6.2 PoolScalingController.
//
// Per §5.1 pools are platform-global (no tenant_id, no RLS). The
// §10.6 `runtime_tenant_access` / `pool_tenant_access` join tables
// enforce per-tenant visibility at the admin handler layer; this
// store is the source of truth for the pool record itself.
package poolstore

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// Pool captures the §5.2 SandboxWarmPool CRD shape. v1 models the
// essential fields; extension fields (taskPolicy, credentialPolicy,
// env, image overrides) attach to the row but are not strictly
// validated by this store — the admin handler owns the cross-field
// validation per §5.2.
type Pool struct {
	// Name is the §5.2 pool identifier.
	Name string

	// RuntimeRef names the runtime this pool warms.
	RuntimeRef string

	// IsolationProfile overrides the runtime's default §5.3 profile.
	IsolationProfile isolation.Profile

	// ExecutionMode is the §5.2 mode (session, task, concurrent).
	ExecutionMode runtimestore.ExecutionMode

	// ResourceClass is the §5.2 size bucket (`small`, `medium`,
	// `large`); free-form per pool admin.
	ResourceClass string

	// WarmCount is the §5.2 desired warm replica count. Mode-adjusted
	// per §4.6.2.
	WarmCount int

	// MaxSessionAgeSeconds is the §5.2 per-session lifetime cap.
	MaxSessionAgeSeconds int

	// AllowStandardIsolation gates §5.3 `standard` profile admission.
	// Pools whose IsolationProfile is `standard` require this flag
	// per §5.3 security note.
	AllowStandardIsolation bool

	// CreatedAt / UpdatedAt / DeletedAt are the audit timestamps.
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt time.Time
}

// IsActive reports whether the pool has not been soft-deleted.
func (p Pool) IsActive() bool { return p.DeletedAt.IsZero() }

// Store is the §5.2 pool registry contract.
type Store interface {
	Create(ctx context.Context, p Pool) error
	Get(ctx context.Context, name string) (Pool, error)
	Update(ctx context.Context, name string, mutate func(*Pool) error) (Pool, error)
	List(ctx context.Context, filter ListFilter) ([]Pool, error)
	SoftDelete(ctx context.Context, name string, at time.Time) error
}

// ListFilter narrows the List result.
type ListFilter struct {
	IncludeDeleted bool
	RuntimeRef     string
}

// Sentinel errors.
var (
	ErrNotFound      = errors.New("poolstore: pool not found")
	ErrAlreadyExists = errors.New("poolstore: pool already exists")
)

// namePattern follows the §5.2 pool-name shape — same as
// runtimestore.ValidateName.
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,127}$`)

// ValidateName reports whether name satisfies the §5.2 pattern.
func ValidateName(name string) error {
	if name == "" {
		return errors.New("poolstore: name is required")
	}
	if !namePattern.MatchString(name) {
		return errors.New(`poolstore: name must match ^[a-z0-9][a-z0-9_-]{0,127}$`)
	}
	return nil
}

// Memory is the in-memory Store implementation.
type Memory struct {
	mu    sync.RWMutex
	pools map[string]Pool
}

// NewMemory returns an empty Memory store.
func NewMemory() *Memory { return &Memory{pools: map[string]Pool{}} }

// Create implements Store.
func (m *Memory) Create(_ context.Context, p Pool) error {
	if err := ValidateName(p.Name); err != nil {
		return err
	}
	if p.WarmCount < 0 {
		return errors.New("poolstore: warmCount must be >= 0")
	}
	if p.MaxSessionAgeSeconds < 0 {
		return errors.New("poolstore: maxSessionAgeSeconds must be >= 0")
	}
	if p.IsolationProfile == isolation.ProfileStandard && !p.AllowStandardIsolation {
		return errors.New("poolstore: isolationProfile=standard requires allowStandardIsolation=true (§5.3)")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.pools[p.Name]; exists {
		return ErrAlreadyExists
	}
	now := time.Now().UTC()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = p.CreatedAt
	}
	m.pools[p.Name] = p
	return nil
}

// Get implements Store.
func (m *Memory) Get(_ context.Context, name string) (Pool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	row, ok := m.pools[name]
	if !ok {
		return Pool{}, ErrNotFound
	}
	return row, nil
}

// Update implements Store.
func (m *Memory) Update(_ context.Context, name string, mutate func(*Pool) error) (Pool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.pools[name]
	if !ok {
		return Pool{}, ErrNotFound
	}
	prev := row.UpdatedAt
	if err := mutate(&row); err != nil {
		return Pool{}, err
	}
	if row.WarmCount < 0 {
		return Pool{}, errors.New("poolstore: warmCount must be >= 0")
	}
	if row.MaxSessionAgeSeconds < 0 {
		return Pool{}, errors.New("poolstore: maxSessionAgeSeconds must be >= 0")
	}
	if row.IsolationProfile == isolation.ProfileStandard && !row.AllowStandardIsolation {
		return Pool{}, errors.New("poolstore: isolationProfile=standard requires allowStandardIsolation=true (§5.3)")
	}
	now := time.Now().UTC()
	if !now.After(prev) {
		now = prev.Add(time.Nanosecond)
	}
	row.UpdatedAt = now
	m.pools[name] = row
	return row, nil
}

// List implements Store.
func (m *Memory) List(_ context.Context, filter ListFilter) ([]Pool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Pool, 0, len(m.pools))
	for _, row := range m.pools {
		if !filter.IncludeDeleted && !row.IsActive() {
			continue
		}
		if filter.RuntimeRef != "" && row.RuntimeRef != filter.RuntimeRef {
			continue
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// SoftDelete implements Store.
func (m *Memory) SoftDelete(_ context.Context, name string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.pools[name]
	if !ok {
		return ErrNotFound
	}
	if !row.DeletedAt.IsZero() {
		return nil
	}
	row.DeletedAt = at
	row.UpdatedAt = at
	m.pools[name] = row
	return nil
}
