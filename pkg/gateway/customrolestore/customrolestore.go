// SPDX-License-Identifier: MIT

// Package customrolestore is the §10.2 custom-role registry. It backs
// the §15.1 `/v1/admin/tenants/{id}/roles` admin CRUD.
//
// Per §10.2 a custom role is a tenant-scoped, named subset of the RBAC
// permission matrix that "cannot exceed the permissions of
// `tenant-admin`". This store keys every role by (tenant_id, name) and
// enforces that subset invariant on every write.
package customrolestore

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
)

// CustomRole is the §10.2 tenant-scoped custom-role resource: a named
// set of permissions drawn from the §10.2 permission matrix.
type CustomRole struct {
	// TenantID is the owning tenant. A custom role belongs to exactly
	// one tenant (§10.2).
	TenantID string

	// Name is the role identifier, unique within the tenant and
	// distinct from every built-in role name.
	Name string

	// Permissions is the role's permission set. Every entry must be a
	// permission the §10.2 `tenant-admin` role holds.
	Permissions []auth.Permission

	// CreatedAt / UpdatedAt are the audit timestamps.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Store is the §10.2 custom-role registry contract. Every method is
// tenant-scoped.
type Store interface {
	Create(ctx context.Context, r CustomRole) error
	Get(ctx context.Context, tenantID, name string) (CustomRole, error)
	Update(ctx context.Context, tenantID, name string, mutate func(*CustomRole) error) (CustomRole, error)
	List(ctx context.Context, tenantID string) ([]CustomRole, error)
	Delete(ctx context.Context, tenantID, name string) error
}

// Sentinel errors.
var (
	// ErrNotFound — no role with the (tenant, name) key exists.
	ErrNotFound = errors.New("customrolestore: custom role not found")
	// ErrAlreadyExists — a role with this (tenant, name) is registered.
	ErrAlreadyExists = errors.New("customrolestore: custom role already exists")
)

// namePattern follows the §10.2 role-name shape — lowercase
// alphanumeric with hyphen and underscore separators.
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,127}$`)

// ValidateName reports whether name is a structurally valid custom-role
// name that does not collide with a built-in §10.2 role.
func ValidateName(name string) error {
	if name == "" {
		return errors.New("customrolestore: name is required")
	}
	if !namePattern.MatchString(name) {
		return errors.New(`customrolestore: name must match ^[a-z0-9][a-z0-9_-]{0,127}$`)
	}
	if auth.Role(name).IsValid() {
		return errors.New("customrolestore: name must not collide with a built-in role")
	}
	return nil
}

// Validate reports the §10.2 structural invariants of a custom role: a
// tenant id, a valid non-colliding name, and a permission set whose
// every entry is a permission the `tenant-admin` role holds — a custom
// role may restrict but never exceed `tenant-admin`.
func Validate(r CustomRole) error {
	if r.TenantID == "" {
		return errors.New("customrolestore: tenantId is required")
	}
	if err := ValidateName(r.Name); err != nil {
		return err
	}
	for _, p := range r.Permissions {
		if !p.IsValid() {
			return errors.New("customrolestore: unrecognised permission " + string(p))
		}
		if !auth.IsTenantAdminPermission(p) {
			return errors.New("customrolestore: permission " + string(p) +
				" exceeds the tenant-admin permission set; custom roles may not exceed tenant-admin")
		}
	}
	return nil
}

// Memory is the in-memory Store implementation.
type Memory struct {
	mu    sync.RWMutex
	roles map[string]map[string]CustomRole
}

// NewMemory returns an empty Memory store.
func NewMemory() *Memory {
	return &Memory{roles: map[string]map[string]CustomRole{}}
}

// Create implements Store.
func (m *Memory) Create(_ context.Context, r CustomRole) error {
	if err := Validate(r); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.roles[r.TenantID][r.Name]; exists {
		return ErrAlreadyExists
	}
	now := time.Now().UTC()
	if r.CreatedAt.IsZero() {
		r.CreatedAt = now
	}
	if r.UpdatedAt.IsZero() {
		r.UpdatedAt = r.CreatedAt
	}
	if m.roles[r.TenantID] == nil {
		m.roles[r.TenantID] = map[string]CustomRole{}
	}
	m.roles[r.TenantID][r.Name] = cloneRole(r)
	return nil
}

// Get implements Store.
func (m *Memory) Get(_ context.Context, tenantID, name string) (CustomRole, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	row, ok := m.roles[tenantID][name]
	if !ok {
		return CustomRole{}, ErrNotFound
	}
	return cloneRole(row), nil
}

// Update implements Store.
func (m *Memory) Update(_ context.Context, tenantID, name string, mutate func(*CustomRole) error) (CustomRole, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.roles[tenantID][name]
	if !ok {
		return CustomRole{}, ErrNotFound
	}
	row = cloneRole(row)
	prev := row.UpdatedAt
	if err := mutate(&row); err != nil {
		return CustomRole{}, err
	}
	if err := Validate(row); err != nil {
		return CustomRole{}, err
	}
	now := time.Now().UTC()
	if !now.After(prev) {
		now = prev.Add(time.Nanosecond)
	}
	row.UpdatedAt = now
	m.roles[tenantID][name] = cloneRole(row)
	return cloneRole(row), nil
}

// List implements Store, returning the tenant's roles in name order.
func (m *Memory) List(_ context.Context, tenantID string) ([]CustomRole, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]CustomRole, 0, len(m.roles[tenantID]))
	for _, row := range m.roles[tenantID] {
		out = append(out, cloneRole(row))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Delete implements Store.
func (m *Memory) Delete(_ context.Context, tenantID, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.roles[tenantID][name]; !ok {
		return ErrNotFound
	}
	delete(m.roles[tenantID], name)
	return nil
}

// cloneRole deep-copies the permission slice so a stored role and a
// returned copy never share mutable state.
func cloneRole(r CustomRole) CustomRole {
	r.Permissions = append([]auth.Permission(nil), r.Permissions...)
	return r
}

var _ Store = (*Memory)(nil)
