// SPDX-License-Identifier: MIT

// Package runtimestore is the §5.1 Runtime registry. It backs the
// platform-global `Runtime` records the gateway uses to resolve
// `runtimeRef` on session creation, and the §15.1 admin runtime CRUD
// endpoints.
//
// Per §5.1 / §10.6 runtimes are platform-global: no tenant_id, no
// RLS. The §15.1 admin handlers enforce per-tenant visibility via
// the `runtime_tenant_access` join table (out of scope for v1
// in-memory; production wires Postgres-backed access).
package runtimestore

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// Runtime captures the §5.1 Runtime CRD shape. Only the v1 essential
// fields are modelled; extension fields (capabilities, providers,
// setup policy) are admitted but not strictly validated by this
// store — the admin handler runs the cross-field checks.
type Runtime struct {
	// Name is the §5.1 registry key. Required.
	Name string

	// Type discriminates between agent runtimes (the v1 default) and
	// `mcp` runtimes that speak MCP directly per §12b.
	Type RuntimeType

	// Image is the §5.1 image reference. v1 requires digest-pinned
	// references; the admin handler enforces the digest check.
	Image string

	// ExecutionMode is the §5.2 mode: `session`, `task`, or
	// `concurrent`.
	ExecutionMode ExecutionMode

	// IsolationProfile is the §5.3 profile this runtime defaults to.
	IsolationProfile isolation.Profile

	// IntegrationLevel is the §15.4.3 conformance level: `basic`,
	// `standard`, or `full`.
	IntegrationLevel IntegrationLevel

	// Description is an admin-facing description.
	Description string

	// DelegationPolicyRef names the §8.3 DelegationPolicy that scopes
	// delegations originating from this runtime. Empty when the
	// runtime has no runtime-level delegation policy.
	DelegationPolicyRef string

	// Labels is the §5.1 label set. §5.1 requires labels from v1 as the
	// primary mechanism for environment runtimeSelector matching
	// (§10.6). A nil or empty map means the runtime carries no labels.
	Labels map[string]string

	// CreatedAt / UpdatedAt are the audit timestamps.
	CreatedAt time.Time
	UpdatedAt time.Time

	// DeletedAt is the §15.1 soft-delete timestamp. The minimal
	// implementation refuses to register pods against soft-deleted
	// runtimes but keeps the row for audit.
	DeletedAt time.Time
}

// IsActive reports whether the runtime has not been soft-deleted.
func (r Runtime) IsActive() bool { return r.DeletedAt.IsZero() }

// RuntimeType is the §5.1 type discriminator.
type RuntimeType string

const (
	TypeAgent RuntimeType = "agent"
	TypeMCP   RuntimeType = "mcp"
)

// AllRuntimeTypes returns the closed enum.
func AllRuntimeTypes() []RuntimeType { return []RuntimeType{TypeAgent, TypeMCP} }

// IsValid reports whether t is a known runtime type.
func (t RuntimeType) IsValid() bool {
	for _, v := range AllRuntimeTypes() {
		if t == v {
			return true
		}
	}
	return false
}

// ExecutionMode is the §5.2 enum.
type ExecutionMode string

const (
	ExecutionModeSession    ExecutionMode = "session"
	ExecutionModeTask       ExecutionMode = "task"
	ExecutionModeConcurrent ExecutionMode = "concurrent"
)

// AllExecutionModes returns the closed enum.
func AllExecutionModes() []ExecutionMode {
	return []ExecutionMode{ExecutionModeSession, ExecutionModeTask, ExecutionModeConcurrent}
}

// IsValid reports whether m is a known execution mode.
func (m ExecutionMode) IsValid() bool {
	for _, v := range AllExecutionModes() {
		if m == v {
			return true
		}
	}
	return false
}

// IntegrationLevel is the §15.4.3 conformance enum.
type IntegrationLevel string

const (
	IntegrationLevelBasic    IntegrationLevel = "basic"
	IntegrationLevelStandard IntegrationLevel = "standard"
	IntegrationLevelFull     IntegrationLevel = "full"
)

// AllIntegrationLevels returns the closed enum.
func AllIntegrationLevels() []IntegrationLevel {
	return []IntegrationLevel{IntegrationLevelBasic, IntegrationLevelStandard, IntegrationLevelFull}
}

// IsValid reports whether l is a known integration level.
func (l IntegrationLevel) IsValid() bool {
	for _, v := range AllIntegrationLevels() {
		if l == v {
			return true
		}
	}
	return false
}

// Store is the §5.1 Runtime registry contract.
type Store interface {
	Create(ctx context.Context, r Runtime) error
	Get(ctx context.Context, name string) (Runtime, error)
	Update(ctx context.Context, name string, mutate func(*Runtime) error) (Runtime, error)
	List(ctx context.Context, filter ListFilter) ([]Runtime, error)
	SoftDelete(ctx context.Context, name string, at time.Time) error
}

// ListFilter narrows List results.
type ListFilter struct {
	// IncludeDeleted, when true, returns soft-deleted rows.
	IncludeDeleted bool

	// Type filters by RuntimeType. Empty returns every type.
	Type RuntimeType
}

// Sentinel errors.
var (
	ErrNotFound      = errors.New("runtimestore: runtime not found")
	ErrAlreadyExists = errors.New("runtimestore: runtime already exists")
)

// namePattern is the §5.1 registry-name format: DNS-label-like with
// dashes, underscores, and lowercase alphanumerics. Bounded at 128
// to match the §10.2 tenant-id format.
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,127}$`)

// ValidateName reports whether name satisfies the §5.1 pattern.
func ValidateName(name string) error {
	if name == "" {
		return errors.New("runtimestore: name is required")
	}
	if !namePattern.MatchString(name) {
		return errors.New(`runtimestore: name must match ^[a-z0-9][a-z0-9_-]{0,127}$`)
	}
	return nil
}

// ApplyDefaults fills in the §5.1 default values for unset Runtime
// fields: type defaults to agent, execution mode to session, and
// isolation profile to the platform default. Integration level
// defaults to basic for agent runtimes only — §5.1 specifies that
// integrationLevel is meaningful solely on type: agent runtimes, so
// an mcp runtime keeps an empty integration level.
//
// Registration handlers call this at the admin-API boundary; the
// stores persist whatever they are given.
func ApplyDefaults(r *Runtime) {
	if r.Type == "" {
		r.Type = TypeAgent
	}
	if r.ExecutionMode == "" {
		r.ExecutionMode = ExecutionModeSession
	}
	if r.IsolationProfile == "" {
		r.IsolationProfile = isolation.Default()
	}
	if r.Type == TypeAgent && r.IntegrationLevel == "" {
		r.IntegrationLevel = IntegrationLevelBasic
	}
}

// cloneRuntime returns a deep copy of r. The Labels map is copied so
// the store never shares mutable state with a caller.
func cloneRuntime(r Runtime) Runtime {
	if r.Labels != nil {
		labels := make(map[string]string, len(r.Labels))
		for k, v := range r.Labels {
			labels[k] = v
		}
		r.Labels = labels
	}
	return r
}

// Memory is the in-memory Store implementation.
type Memory struct {
	mu       sync.RWMutex
	runtimes map[string]Runtime
}

// NewMemory returns an empty Memory store.
func NewMemory() *Memory { return &Memory{runtimes: map[string]Runtime{}} }

// Create implements Store.
func (m *Memory) Create(_ context.Context, r Runtime) error {
	if err := ValidateName(r.Name); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.runtimes[r.Name]; exists {
		return ErrAlreadyExists
	}
	now := time.Now().UTC()
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	if r.UpdatedAt.IsZero() {
		r.UpdatedAt = r.CreatedAt
	}
	m.runtimes[r.Name] = cloneRuntime(r)
	return nil
}

// Get implements Store.
func (m *Memory) Get(_ context.Context, name string) (Runtime, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	row, ok := m.runtimes[name]
	if !ok {
		return Runtime{}, ErrNotFound
	}
	return cloneRuntime(row), nil
}

// Update implements Store.
func (m *Memory) Update(_ context.Context, name string, mutate func(*Runtime) error) (Runtime, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	stored, ok := m.runtimes[name]
	if !ok {
		return Runtime{}, ErrNotFound
	}
	row := cloneRuntime(stored)
	prev := row.UpdatedAt
	if err := mutate(&row); err != nil {
		return Runtime{}, err
	}
	now := time.Now().UTC()
	if !now.After(prev) {
		now = prev.Add(time.Nanosecond)
	}
	row.UpdatedAt = now
	m.runtimes[name] = cloneRuntime(row)
	return cloneRuntime(row), nil
}

// List implements Store.
func (m *Memory) List(_ context.Context, filter ListFilter) ([]Runtime, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Runtime, 0, len(m.runtimes))
	for _, row := range m.runtimes {
		if !filter.IncludeDeleted && !row.IsActive() {
			continue
		}
		if filter.Type != "" && row.Type != filter.Type {
			continue
		}
		out = append(out, cloneRuntime(row))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// SoftDelete implements Store.
func (m *Memory) SoftDelete(_ context.Context, name string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.runtimes[name]
	if !ok {
		return ErrNotFound
	}
	if !row.DeletedAt.IsZero() {
		return nil // idempotent
	}
	row.DeletedAt = at
	row.UpdatedAt = at
	m.runtimes[name] = row
	return nil
}
