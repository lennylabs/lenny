// SPDX-License-Identifier: MIT

// Package tenantaccessstore is the §4 runtime/pool tenant-access
// registry. Runtimes and pools are platform-global resources with no
// tenant_id; per-tenant visibility is granted through the
// `runtime_tenant_access` and `pool_tenant_access` join tables. This
// store backs the §15.1 `/v1/admin/{runtimes,pools}/{name}/tenant-access`
// grant, list, and revoke endpoints.
package tenantaccessstore

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

// ResourceKind identifies the platform-global resource a grant scopes.
type ResourceKind string

const (
	// KindRuntime is the `runtime_tenant_access` join table.
	KindRuntime ResourceKind = "runtime"
	// KindPool is the `pool_tenant_access` join table.
	KindPool ResourceKind = "pool"
)

// IsValid reports whether k is a known resource kind.
func (k ResourceKind) IsValid() bool { return k == KindRuntime || k == KindPool }

// Grant is one §4 tenant-access join-table entry: a tenant that may
// see and use a given platform-global runtime or pool.
type Grant struct {
	// TenantID is the tenant granted access.
	TenantID string
	// GrantedBy is the subject of the platform-admin who granted it.
	GrantedBy string
	// GrantedAt is the UTC instant the grant was recorded.
	GrantedAt time.Time
}

// Sentinel errors.
var (
	// ErrNotFound — no grant exists for the (kind, resource, tenant).
	ErrNotFound = errors.New("tenantaccessstore: grant not found")
	// ErrInvalidArg — Grant or Revoke was called with an empty or
	// unrecognised argument.
	ErrInvalidArg = errors.New("tenantaccessstore: kind, resource, and tenantId are required")
)

// Store is the §4 tenant-access registry contract.
type Store interface {
	// Grant records tenant access to a resource. It is idempotent:
	// created reports whether a new grant was written (true) or one
	// already existed (false).
	Grant(ctx context.Context, kind ResourceKind, resource, tenantID, grantedBy string, at time.Time) (created bool, err error)
	// Revoke removes a grant. Returns ErrNotFound when no grant exists.
	Revoke(ctx context.Context, kind ResourceKind, resource, tenantID string) error
	// List returns a resource's grants, tenant-id ascending.
	List(ctx context.Context, kind ResourceKind, resource string) ([]Grant, error)
	// ListForTenant returns the names of every resource of the given
	// kind the tenant has been granted, name ascending. It is the
	// inverse of List, used to filter a tenant-admin's runtime/pool
	// reads to the resources they may see (§4).
	ListForTenant(ctx context.Context, kind ResourceKind, tenantID string) ([]string, error)
}

// Memory is the in-memory Store implementation.
type Memory struct {
	mu     sync.RWMutex
	grants map[string]map[string]Grant
}

// NewMemory returns an empty Memory store.
func NewMemory() *Memory { return &Memory{grants: map[string]map[string]Grant{}} }

// key composes the join-table key for a (kind, resource) pair.
func key(kind ResourceKind, resource string) string {
	return string(kind) + "/" + resource
}

// Grant implements Store.
func (m *Memory) Grant(_ context.Context, kind ResourceKind, resource, tenantID, grantedBy string, at time.Time) (bool, error) {
	if !kind.IsValid() || resource == "" || tenantID == "" {
		return false, ErrInvalidArg
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(kind, resource)
	if _, exists := m.grants[k][tenantID]; exists {
		return false, nil
	}
	if m.grants[k] == nil {
		m.grants[k] = map[string]Grant{}
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	m.grants[k][tenantID] = Grant{TenantID: tenantID, GrantedBy: grantedBy, GrantedAt: at.UTC()}
	return true, nil
}

// Revoke implements Store.
func (m *Memory) Revoke(_ context.Context, kind ResourceKind, resource, tenantID string) error {
	if !kind.IsValid() || resource == "" || tenantID == "" {
		return ErrInvalidArg
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(kind, resource)
	if _, exists := m.grants[k][tenantID]; !exists {
		return ErrNotFound
	}
	delete(m.grants[k], tenantID)
	return nil
}

// List implements Store.
func (m *Memory) List(_ context.Context, kind ResourceKind, resource string) ([]Grant, error) {
	if !kind.IsValid() || resource == "" {
		return nil, ErrInvalidArg
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Grant, 0, len(m.grants[key(kind, resource)]))
	for _, g := range m.grants[key(kind, resource)] {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TenantID < out[j].TenantID })
	return out, nil
}

// ListForTenant implements Store.
func (m *Memory) ListForTenant(_ context.Context, kind ResourceKind, tenantID string) ([]string, error) {
	if !kind.IsValid() || tenantID == "" {
		return nil, ErrInvalidArg
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	prefix := string(kind) + "/"
	var out []string
	for k, grants := range m.grants {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		if _, ok := grants[tenantID]; ok {
			out = append(out, strings.TrimPrefix(k, prefix))
		}
	}
	sort.Strings(out)
	return out, nil
}

var _ Store = (*Memory)(nil)
