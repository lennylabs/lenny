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
	"github.com/lennylabs/lenny/pkg/sandbox/egress"
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

	// ConcurrencyStyle is the §5.2 concurrent-mode sub-variant
	// (`workspace`, `stateless`). It is meaningful only when
	// ExecutionMode is `concurrent`, where ValidateConcurrentConfig
	// requires it.
	ConcurrencyStyle ConcurrencyStyle

	// MaxConcurrent is the §5.2 per-pod slot bound for concurrent mode.
	// A concurrent-mode pod hosts at most this many slots
	// simultaneously. It must be >= 1 on a concurrent-mode pool.
	MaxConcurrent int

	// AcknowledgeProcessLevelIsolation records the §5.2 deployer
	// acknowledgment that concurrent-workspace slots share the pod
	// process namespace, /tmp, cgroup memory, network stack, and
	// credential group-read access. A concurrent-workspace pool is
	// rejected without it.
	AcknowledgeProcessLevelIsolation bool

	// CleanupTimeoutSeconds bounds per-slot cleanup on a
	// concurrent-workspace pool. The §5.2 rule requires
	// CleanupTimeoutSeconds >= MaxConcurrent * 5 so each slot's cleanup
	// budget clears the 5-second floor.
	CleanupTimeoutSeconds int

	// AllowCrossTenantReuse mirrors the §5.2 task-mode field. Concurrent
	// modes have no cross-tenant isolation boundary, so
	// ValidateConcurrentConfig rejects a concurrent-mode pool that sets
	// it.
	AllowCrossTenantReuse bool

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

	// EgressProfile is the §13.2 per-pool egress profile (`restricted`,
	// `provider-direct`, `internet`). Empty resolves to the §13.2
	// default (`restricted`) at admission. The store rejects an
	// `internet` profile on a `standard` (runc) pool per the §13.2
	// cross-control.
	EgressProfile egress.Profile

	// CreatedAt / UpdatedAt / DeletedAt are the audit timestamps.
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt time.Time
}

// IsActive reports whether the pool has not been soft-deleted.
func (p Pool) IsActive() bool { return p.DeletedAt.IsZero() }

// ConcurrencyStyle is the §5.2 concurrent-mode sub-variant.
type ConcurrencyStyle string

const (
	// ConcurrencyStyleWorkspace is `concurrencyStyle: workspace` —
	// workspace-concurrent. Each slot gets its own per-slot workspace
	// tree and the pod's /workspace/shared/ is shared read-only across
	// the pod's slots (§6.4).
	ConcurrencyStyleWorkspace ConcurrencyStyle = "workspace"

	// ConcurrencyStyleStateless is `concurrencyStyle: stateless` —
	// stateless-concurrent. No workspace is materialized and the pod
	// holds no Lenny-managed per-slot session state (§5.2).
	ConcurrencyStyleStateless ConcurrencyStyle = "stateless"
)

// AllConcurrencyStyles returns the closed §5.2 enum.
func AllConcurrencyStyles() []ConcurrencyStyle {
	return []ConcurrencyStyle{ConcurrencyStyleWorkspace, ConcurrencyStyleStateless}
}

// IsValid reports whether s is a known concurrency style.
func (s ConcurrencyStyle) IsValid() bool {
	for _, v := range AllConcurrencyStyles() {
		if s == v {
			return true
		}
	}
	return false
}

// ValidateEgressIsolation enforces the §13.2 cross-control that pairs a
// pool's egress profile with its isolation profile. An empty egress
// profile is treated as the §13.2 default (`restricted`), which is
// compatible with any isolation profile, so the check fires only when a
// pool explicitly opts into a broader egress profile. The current rule
// rejects the `internet` egress profile on a `standard` (runc) pool: a
// runc pod with broad internet egress is the high-blast-radius
// configuration the §5.3 security note targets and §13.2 forbids.
//
// A non-empty but unrecognised egress profile is rejected so a mistyped
// value fails closed rather than being silently ignored.
//
// spec: §13.2 — "the `internet` profile requires a sandboxed isolation
// profile (`sandboxed` or `microvm`) ... The warm pool controller
// rejects pool configurations that combine `standard` isolation with
// `internet` egress at validation time."
func ValidateEgressIsolation(p Pool) error {
	if p.EgressProfile == "" {
		return nil
	}
	if !egress.IsValid(p.EgressProfile) {
		return errors.New("poolstore: egressProfile is not a recognised §13.2 profile (restricted, provider-direct, internet)")
	}
	// Resolve the effective isolation profile the way the admission path
	// does: an empty profile defaults to the §5.3 production default,
	// which always satisfies the cross-control, so only an explicit
	// `standard` profile can trip it.
	iso := p.IsolationProfile
	if iso == "" {
		iso = isolation.Default()
	}
	if !egress.AllowsIsolation(p.EgressProfile, iso) {
		return errors.New("poolstore: egressProfile=internet requires isolationProfile sandboxed or microvm; standard (runc) is forbidden (§13.2)")
	}
	return nil
}

// ValidateConcurrentConfig enforces the §5.2 / §13.1 admission rules for
// a pool's concurrent-mode configuration. It is the pool-side half of
// the Phase 12c pod-level isolation enforcement: a `concurrent`-mode
// pool cannot be created without the §5.2 deployer acknowledgment, and
// it cannot weaken the cross-tenant boundary that §5.2 reserves to
// task-mode microvm pools.
//
// The rules:
//
//   - A non-concurrent pool must not set concurrent-only fields
//     (concurrencyStyle, maxConcurrent), so a stray field on a
//     session-mode or task-mode pool is rejected rather than silently
//     ignored.
//   - A concurrent pool must name a valid concurrencyStyle (`workspace`
//     or `stateless`).
//   - A concurrent pool must set maxConcurrent >= 1 — the §5.2 per-pod
//     slot bound.
//   - A concurrent pool must never set allowCrossTenantReuse: §5.2
//     gives simultaneous process-level co-tenancy no isolation boundary,
//     so cross-tenant slot sharing is categorically rejected (unlike
//     task mode's microvm option).
//   - A concurrent-workspace pool must set
//     acknowledgeProcessLevelIsolation: §5.2 requires the deployer to
//     accept the shared process namespace, /tmp, cgroup memory, network
//     stack, and credential group-read access between simultaneous
//     slots before the mode is enabled.
//   - A concurrent-workspace pool that sets cleanupTimeoutSeconds must
//     satisfy cleanupTimeoutSeconds >= maxConcurrent * 5 so each slot's
//     per-slot cleanup budget clears the §5.2 5-second floor.
//
// It returns nil for a session-mode or task-mode pool. Callers invoke
// it at the admin-API boundary and surface the error as a §15.1
// VALIDATION_ERROR.
func ValidateConcurrentConfig(p Pool) error {
	if p.ExecutionMode != runtimestore.ExecutionModeConcurrent {
		if p.ConcurrencyStyle != "" {
			return errors.New("poolstore: concurrencyStyle is valid only when executionMode is concurrent (§5.2)")
		}
		if p.MaxConcurrent != 0 {
			return errors.New("poolstore: maxConcurrent is valid only when executionMode is concurrent (§5.2)")
		}
		return nil
	}

	if !p.ConcurrencyStyle.IsValid() {
		return errors.New("poolstore: concurrent-mode pool requires concurrencyStyle to be workspace or stateless (§5.2)")
	}
	if p.MaxConcurrent < 1 {
		return errors.New("poolstore: concurrent-mode pool requires maxConcurrent >= 1 (§5.2)")
	}
	if p.AllowCrossTenantReuse {
		return errors.New("poolstore: allowCrossTenantReuse is not permitted for concurrent-mode pools; " +
			"cross-tenant slot sharing has no isolation boundary in concurrent mode (§5.2)")
	}
	if p.ConcurrencyStyle == ConcurrencyStyleWorkspace {
		if !p.AcknowledgeProcessLevelIsolation {
			return errors.New("poolstore: concurrent-workspace pool requires acknowledgeProcessLevelIsolation=true; " +
				"concurrent slots share the pod process namespace, /tmp, cgroup memory, network stack, " +
				"and credential group-read access (§5.2)")
		}
		if p.CleanupTimeoutSeconds != 0 && p.CleanupTimeoutSeconds < p.MaxConcurrent*5 {
			return errors.New("poolstore: cleanupTimeoutSeconds / maxConcurrent would produce a per-slot " +
				"cleanup timeout below the 5s minimum; set cleanupTimeoutSeconds >= maxConcurrent * 5 (§5.2)")
		}
	}
	return nil
}

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
	if err := ValidateEgressIsolation(p); err != nil {
		return err
	}
	if err := ValidateConcurrentConfig(p); err != nil {
		return err
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
	if err := ValidateEgressIsolation(row); err != nil {
		return Pool{}, err
	}
	if err := ValidateConcurrentConfig(row); err != nil {
		return Pool{}, err
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
