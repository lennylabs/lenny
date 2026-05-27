// SPDX-License-Identifier: MIT

// Package environmentstore is the §10.6 Environment registry: the
// per-tenant Environment resource behind the /v1/admin/environments
// admin API.
//
// An Environment is a named, RBAC-governed project context that
// groups runtimes and connectors for a team. The pkg/environment
// package supplies the §10.6 primitives this resource composes: the
// Role enum and the tag-based Selector evaluator.
package environmentstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/environment"
)

// Identity names a §10.6 environment member: an OIDC group or other
// identity principal.
type Identity struct {
	// Type is the §10.6 identity type, e.g. "oidc-group".
	Type string
	// Value is the identity itself, e.g. the OIDC group name.
	Value string
}

// Member binds an identity to a §10.6 role within the environment.
type Member struct {
	Identity Identity
	Role     environment.Role
}

// MCPRuntimeFilter is a §10.6 capability-based tool filter applied to
// the `type: mcp` runtimes the RuntimeSelector admits.
type MCPRuntimeFilter struct {
	RuntimeSelector     environment.Selector
	AllowedCapabilities []string
	DeniedCapabilities  []string
}

// CrossEnvRule is one §10.6 bilateral cross-environment-delegation
// declaration. For an outbound rule Environment is the target; for an
// inbound rule it is the permitted source ("*" matches any source).
type CrossEnvRule struct {
	Environment string
	Runtimes    environment.Selector
}

// Environment is the §10.6 Environment resource.
type Environment struct {
	// Name is the §10.6 identifier and the per-tenant key.
	Name string
	// TenantID scopes the environment.
	TenantID string
	// Description is the human-facing label.
	Description string
	// Members is the §10.6 RBAC member list.
	Members []Member
	// RuntimeSelector is the §10.6 tag-based runtime selection.
	RuntimeSelector environment.Selector
	// MCPRuntimeFilters are the §10.6 capability filters for mcp runtimes.
	MCPRuntimeFilters []MCPRuntimeFilter
	// ConnectorSelector is the §10.6 internal connector selection.
	ConnectorSelector environment.Selector
	// DefaultDelegationPolicy names the DelegationPolicy applied to
	// sessions created in this environment.
	DefaultDelegationPolicy string
	// CrossEnvOutbound and CrossEnvInbound are the §10.6 bilateral
	// cross-environment-delegation declarations.
	CrossEnvOutbound []CrossEnvRule
	CrossEnvInbound  []CrossEnvRule
	// CreatedAt and UpdatedAt are audit timestamps.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Validate reports the §10.6 admission errors for an Environment.
func (e Environment) Validate() error {
	var v []string
	if e.Name == "" {
		v = append(v, "name is required")
	}
	if e.TenantID == "" {
		v = append(v, "tenantId is required")
	}
	seen := map[string]bool{}
	for i, m := range e.Members {
		if m.Identity.Value == "" {
			v = append(v, fmt.Sprintf("members[%d].identity.value is required", i))
		}
		if !m.Role.IsValid() {
			v = append(v, fmt.Sprintf("members[%d].role %q is not a valid §10.6 role", i, m.Role))
		}
		k := m.Identity.Type + "/" + m.Identity.Value
		if m.Identity.Value != "" && seen[k] {
			v = append(v, fmt.Sprintf("members[%d] duplicates identity %q", i, m.Identity.Value))
		}
		seen[k] = true
	}
	if err := e.RuntimeSelector.Validate(); err != nil {
		v = append(v, "runtimeSelector: "+err.Error())
	}
	if err := e.ConnectorSelector.Validate(); err != nil {
		v = append(v, "connectorSelector: "+err.Error())
	}
	for i, f := range e.MCPRuntimeFilters {
		if err := f.RuntimeSelector.Validate(); err != nil {
			v = append(v, fmt.Sprintf("mcpRuntimeFilters[%d].runtimeSelector: %v", i, err))
		}
	}
	for i, r := range e.CrossEnvOutbound {
		// spec: §10.6 lines 613-625 — the outbound declaration names a
		// literal target environment. The "*" wildcard is described
		// only for inbound rules; admitting it outbound would create a
		// silent no-op (the envaccess outboundPermits matcher compares
		// rule.Environment to a literal target environment name and
		// would never hit). Reject it at admission so admins reading
		// the inbound wildcard example and generalising to outbound
		// fail loudly rather than write a rule that never matches.
		// F-10.6.13.
		if r.Environment == "*" {
			v = append(v, fmt.Sprintf("crossEnvironmentDelegation.outbound[%d].targetEnvironment: %q wildcard is not supported (use literal target environment names)", i, r.Environment))
		}
		if strings.TrimSpace(r.Environment) == "" {
			v = append(v, fmt.Sprintf("crossEnvironmentDelegation.outbound[%d].targetEnvironment is required", i))
		}
		if err := r.Runtimes.Validate(); err != nil {
			v = append(v, fmt.Sprintf("crossEnvironmentDelegation.outbound[%d].runtimes: %v", i, err))
		}
	}
	for i, r := range e.CrossEnvInbound {
		// spec: §10.6 line 619 — inbound rules accept "*" wildcard
		// for sourceEnvironment. Any other empty value is a
		// malformed rule. F-10.6.13.
		if strings.TrimSpace(r.Environment) == "" {
			v = append(v, fmt.Sprintf("crossEnvironmentDelegation.inbound[%d].sourceEnvironment is required (use %q for any source)", i, "*"))
		}
		if err := r.Runtimes.Validate(); err != nil {
			v = append(v, fmt.Sprintf("crossEnvironmentDelegation.inbound[%d].runtimes: %v", i, err))
		}
	}
	if len(v) == 0 {
		return nil
	}
	return fmt.Errorf("environmentstore: %s: %s", e.Name, strings.Join(v, "; "))
}

// Sentinel errors.
var (
	// ErrNotFound — no environment with the requested (tenant, name).
	ErrNotFound = errors.New("environmentstore: environment not found")
	// ErrAlreadyExists — a (tenant, name) environment already exists.
	ErrAlreadyExists = errors.New("environmentstore: environment already exists")
)

// Store is the §10.6 Environment registry contract. Every method is
// goroutine-safe and tenant-scoped.
type Store interface {
	// Create persists a fresh environment. Returns ErrAlreadyExists
	// when the (tenant, name) pair is taken, or a validation error.
	Create(ctx context.Context, e Environment) error
	// Get returns the environment keyed by (tenantID, name). Returns
	// ErrNotFound when no matching row exists.
	Get(ctx context.Context, tenantID, name string) (Environment, error)
	// Update applies mutate, re-validates, and advances UpdatedAt.
	// Returns ErrNotFound when the row is missing.
	Update(ctx context.Context, tenantID, name string, mutate func(*Environment) error) (Environment, error)
	// List returns the tenant's environments, name-ascending.
	List(ctx context.Context, tenantID string) ([]Environment, error)
	// Delete removes the environment. Returns ErrNotFound when missing.
	Delete(ctx context.Context, tenantID, name string) error
}

// Memory is the in-memory Store implementation.
type Memory struct {
	mu           sync.RWMutex
	environments map[string]Environment // keyed tenantID + "/" + name
}

// NewMemory returns an empty Memory store.
func NewMemory() *Memory { return &Memory{environments: map[string]Environment{}} }

func key(tenantID, name string) string { return tenantID + "/" + name }

// Create implements Store.
func (m *Memory) Create(_ context.Context, e Environment) error {
	if err := e.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(e.TenantID, e.Name)
	if _, ok := m.environments[k]; ok {
		return ErrAlreadyExists
	}
	now := time.Now().UTC()
	if e.CreatedAt.IsZero() {
		e.CreatedAt = now
	}
	if e.UpdatedAt.IsZero() {
		e.UpdatedAt = e.CreatedAt
	}
	m.environments[k] = clone(e)
	return nil
}

// Get implements Store.
func (m *Memory) Get(_ context.Context, tenantID, name string) (Environment, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.environments[key(tenantID, name)]
	if !ok {
		return Environment{}, ErrNotFound
	}
	return clone(e), nil
}

// Update implements Store.
func (m *Memory) Update(_ context.Context, tenantID, name string, mutate func(*Environment) error) (Environment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(tenantID, name)
	e, ok := m.environments[k]
	if !ok {
		return Environment{}, ErrNotFound
	}
	e = clone(e)
	if err := mutate(&e); err != nil {
		return Environment{}, err
	}
	if err := e.Validate(); err != nil {
		return Environment{}, err
	}
	e.UpdatedAt = time.Now().UTC()
	m.environments[k] = clone(e)
	return clone(e), nil
}

// List implements Store.
func (m *Memory) List(_ context.Context, tenantID string) ([]Environment, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []Environment
	for _, e := range m.environments {
		if e.TenantID == tenantID {
			out = append(out, clone(e))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Delete implements Store.
func (m *Memory) Delete(_ context.Context, tenantID, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := key(tenantID, name)
	if _, ok := m.environments[k]; !ok {
		return ErrNotFound
	}
	delete(m.environments, k)
	return nil
}

// DeleteByUser implements the §12.2.1 mandatory-erasure interface.
// Environments are tenant-scoped and do not record user-owned rows;
// DeleteByUser is therefore a no-op that returns 0 erased rows.
func (m *Memory) DeleteByUser(_ context.Context, _, _ string) (int, error) {
	return 0, nil
}

// DeleteByTenant implements the §12.2.1 mandatory-erasure interface.
// Removes every environment belonging to tenantID.
func (m *Memory) DeleteByTenant(_ context.Context, tenantID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prefix := tenantID + "/"
	n := 0
	for k := range m.environments {
		if strings.HasPrefix(k, prefix) {
			delete(m.environments, k)
			n++
		}
	}
	return n, nil
}

// clone deep-copies an Environment so a stored value and a returned
// copy never share the nested member, selector, or cross-environment
// slices and maps.
func clone(e Environment) Environment {
	b, err := json.Marshal(e)
	if err != nil {
		return e
	}
	var c Environment
	if err := json.Unmarshal(b, &c); err != nil {
		return e
	}
	return c
}
