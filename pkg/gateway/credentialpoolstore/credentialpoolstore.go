// SPDX-License-Identifier: MIT

// Package credentialpoolstore is the §4.9 CredentialPool registry. It
// backs the §15.1 `/v1/admin/credential-pools` admin CRUD and the
// credential-leasing path that resolves a provider's pool at session
// creation.
//
// Per §4.9 a CredentialPool is tenant-scoped: each pool belongs to
// exactly one tenant, and the store keys every pool by (tenant_id,
// name). This store is the source of truth for the pool definition —
// the provider, the credential set, the assignment strategy, and the
// lease limits. Per-credential runtime health (cooldown timers,
// rate-limit history, revocation status) is managed by the
// credential-leasing machinery, not by this registry.
package credentialpoolstore

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Credential is one §4.9 credential entry in a pool. A key-based
// provider entry carries SecretRef (a Kubernetes Secret reference); an
// `aws_bedrock` entry carries RoleArn and Region instead.
type Credential struct {
	// ID is the §4.9 per-pool credential identifier.
	ID string

	// SecretRef references the Kubernetes Secret holding the
	// credential material, for key-based providers.
	SecretRef string

	// RoleArn is the IAM role ARN for an `aws_bedrock` credential.
	RoleArn string

	// Region is the provider region for an `aws_bedrock` credential.
	Region string
}

// CredentialPool is the §4.9 tenant-scoped credential pool resource.
type CredentialPool struct {
	// TenantID is the owning tenant. A pool belongs to exactly one
	// tenant (§4.9).
	TenantID string

	// Name is the §4.9 pool identifier, unique within the tenant.
	Name string

	// Provider names the credential provider (`anthropic_direct`,
	// `aws_bedrock`, `github`, `vertex_ai`, `azure_openai`,
	// `vault_transit`, or a custom provider).
	Provider string

	// Credentials is the set of credentials the pool draws from.
	Credentials []Credential

	// AssignmentStrategy is the §4.9 strategy (`least-loaded`,
	// `round-robin`, or `sticky-until-failure`). Empty selects the
	// admin-handler default.
	AssignmentStrategy string

	// MaxConcurrentSessions caps the active leases per credential.
	MaxConcurrentSessions int

	// CooldownOnRateLimitSeconds is how long a rate-limited credential
	// is held out of assignment.
	CooldownOnRateLimitSeconds int

	// LeaseTTLSeconds optionally overrides the provider-default lease
	// TTL. Zero selects the provider default.
	LeaseTTLSeconds int

	// RenewBeforeBufferSeconds is the §4.9 proactive-renewal lead time.
	// Zero selects the 300-second default.
	RenewBeforeBufferSeconds int

	// HostPatterns are the §4.9 VCS-pool host matchers used to route a
	// `gitClone.url` to the pool. Required for VCS providers, ignored
	// otherwise.
	HostPatterns []string

	// DeliveryMode is the §4.9 credential delivery mode for the pool
	// (`proxy` or `direct`). Empty selects the deployment default
	// (proxy for multi-tenant, direct for single-tenant), resolved by
	// the leasing path. The closed value set is validated in Validate.
	DeliveryMode string

	// ProxyDialect is the §4.9 wire dialect a proxy-mode pool's lease
	// exposes (`openai` or `anthropic`). It must match a dialect the
	// bound Runtime declares in credentialCapabilities.proxyDialect
	// (§5.1); that cross-runtime check is enforced where a runtime and
	// pool join. Empty is permitted for a direct-mode pool. The closed
	// value set is validated in Validate.
	ProxyDialect string

	// ProxyEndpoint is the §4.9 HTTPS endpoint of the LLM reverse proxy
	// a proxy-mode pool's lease points the runtime SDK at. It must use
	// the `https://` scheme; an `http://` endpoint is rejected with
	// ErrInvalidProxyEndpointScheme so a lease token is never sent in
	// plaintext on the cluster network. Empty inherits the gateway's
	// configured proxy endpoint.
	ProxyEndpoint string

	// CacheScope is the §4.9 semantic-cache cacheScope for the pool
	// (`per-user`, `per-session`, `tenant`). Empty selects the
	// per-user default. `tenant` is the deployer opt-in to cross-user
	// cache sharing within the tenant; the admin layer rejects it for
	// a tenant with a regulated complianceProfile.
	CacheScope string

	// CreatedAt / UpdatedAt / DeletedAt are the audit timestamps.
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt time.Time
}

// IsActive reports whether the pool has not been soft-deleted.
func (p CredentialPool) IsActive() bool { return p.DeletedAt.IsZero() }

// Store is the §4.9 CredentialPool registry contract. Every method is
// tenant-scoped.
type Store interface {
	Create(ctx context.Context, p CredentialPool) error
	Get(ctx context.Context, tenantID, name string) (CredentialPool, error)
	Update(ctx context.Context, tenantID, name string, mutate func(*CredentialPool) error) (CredentialPool, error)
	List(ctx context.Context, tenantID string, filter ListFilter) ([]CredentialPool, error)
	SoftDelete(ctx context.Context, tenantID, name string, at time.Time) error
}

// ListFilter narrows the List result.
type ListFilter struct {
	// IncludeDeleted, when true, returns soft-deleted pools too.
	IncludeDeleted bool
}

// Sentinel errors.
var (
	// ErrNotFound — no pool with the (tenant, name) key exists.
	ErrNotFound = errors.New("credentialpoolstore: credential pool not found")

	// ErrAlreadyExists — a pool with this (tenant, name) is registered.
	ErrAlreadyExists = errors.New("credentialpoolstore: credential pool already exists")

	// ErrInvalidProxyEndpointScheme — proxyEndpoint does not use the
	// `https://` scheme. The §4.9 proxy-mode guarantee (the real API
	// key never leaves the gateway) requires the lease token to travel
	// encrypted, so a plaintext `http://` endpoint is rejected. spec:
	// §4.9 line 1513 (InvalidProxyEndpointScheme).
	ErrInvalidProxyEndpointScheme = errors.New("credentialpoolstore: proxyEndpoint must use the https:// scheme")
)

// namePattern follows the §4.9 pool-name shape — the same identifier
// pattern used by runtimes and pools.
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,127}$`)

// ValidateName reports whether name satisfies the §4.9 pattern.
func ValidateName(name string) error {
	if name == "" {
		return errors.New("credentialpoolstore: name is required")
	}
	if !namePattern.MatchString(name) {
		return errors.New(`credentialpoolstore: name must match ^[a-z0-9][a-z0-9_-]{0,127}$`)
	}
	return nil
}

// validCacheScope reports whether s is an accepted §4.9 cacheScope.
// Empty is accepted and selects the per-user default at consumption
// time. The values mirror semanticcache.CacheScope; they are checked
// here as plain strings to keep the store free of a cache dependency.
func validCacheScope(s string) bool {
	switch s {
	case "", "per-user", "per-session", "tenant":
		return true
	default:
		return false
	}
}

// validDeliveryMode reports whether m is an accepted §4.9 delivery
// mode. Empty is accepted and selects the deployment default at
// consumption time.
func validDeliveryMode(m string) bool {
	switch m {
	case "", "proxy", "direct":
		return true
	default:
		return false
	}
}

// validateProxyEndpoint rejects a proxyEndpoint that does not use the
// `https://` scheme. An empty endpoint is accepted (the pool inherits
// the gateway's configured endpoint). An `http://` endpoint, or any
// other non-HTTPS scheme, yields ErrInvalidProxyEndpointScheme.
//
// spec: §4.9 line 1513 — the proxy endpoint must use TLS so the lease
// token is always encrypted in transit; the controller rejects an
// `http://` endpoint (InvalidProxyEndpointScheme).
func validateProxyEndpoint(endpoint string) error {
	if endpoint == "" {
		return nil
	}
	if !strings.HasPrefix(strings.ToLower(endpoint), "https://") {
		return ErrInvalidProxyEndpointScheme
	}
	return nil
}

// validProxyDialect reports whether d is an accepted §4.9 proxy
// dialect. Empty is accepted (a direct-mode pool declares no dialect).
// The two launch dialects are `openai` and `anthropic` (spec line 1481).
func validProxyDialect(d string) bool {
	switch d {
	case "", "openai", "anthropic":
		return true
	default:
		return false
	}
}

// Validate reports the §4.9 structural invariants of a pool: a tenant
// id, a valid name, a provider, non-negative limits, and a credential
// set whose entries each carry a unique non-empty id.
func Validate(p CredentialPool) error {
	if p.TenantID == "" {
		return errors.New("credentialpoolstore: tenantId is required")
	}
	if err := ValidateName(p.Name); err != nil {
		return err
	}
	if p.Provider == "" {
		return errors.New("credentialpoolstore: provider is required")
	}
	if p.MaxConcurrentSessions < 0 {
		return errors.New("credentialpoolstore: maxConcurrentSessions must be >= 0")
	}
	if p.CooldownOnRateLimitSeconds < 0 {
		return errors.New("credentialpoolstore: cooldownOnRateLimitSeconds must be >= 0")
	}
	if p.LeaseTTLSeconds < 0 {
		return errors.New("credentialpoolstore: leaseTTLSeconds must be >= 0")
	}
	if p.RenewBeforeBufferSeconds < 0 {
		return errors.New("credentialpoolstore: renewBeforeBufferSeconds must be >= 0")
	}
	if !validCacheScope(p.CacheScope) {
		return errors.New("credentialpoolstore: cacheScope must be per-user, per-session, or tenant")
	}
	if !validDeliveryMode(p.DeliveryMode) {
		return errors.New("credentialpoolstore: deliveryMode must be proxy or direct")
	}
	if !validProxyDialect(p.ProxyDialect) {
		return errors.New("credentialpoolstore: proxyDialect must be openai or anthropic")
	}
	if err := validateProxyEndpoint(p.ProxyEndpoint); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, c := range p.Credentials {
		if c.ID == "" {
			return errors.New("credentialpoolstore: every credential requires an id")
		}
		if seen[c.ID] {
			return errors.New("credentialpoolstore: credential ids must be unique within a pool")
		}
		seen[c.ID] = true
	}
	return nil
}

// Memory is the in-memory Store implementation backing tests and the
// minimal gateway. Pools are held per tenant.
type Memory struct {
	mu    sync.RWMutex
	pools map[string]map[string]CredentialPool
}

// NewMemory returns an empty Memory store.
func NewMemory() *Memory {
	return &Memory{pools: map[string]map[string]CredentialPool{}}
}

// Create implements Store.
func (m *Memory) Create(_ context.Context, p CredentialPool) error {
	if err := Validate(p); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.pools[p.TenantID][p.Name]; exists {
		return ErrAlreadyExists
	}
	now := time.Now().UTC()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = p.CreatedAt
	}
	if m.pools[p.TenantID] == nil {
		m.pools[p.TenantID] = map[string]CredentialPool{}
	}
	m.pools[p.TenantID][p.Name] = clonePool(p)
	return nil
}

// Get implements Store. Soft-deleted pools are returned; callers
// consult CredentialPool.IsActive to filter.
func (m *Memory) Get(_ context.Context, tenantID, name string) (CredentialPool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	row, ok := m.pools[tenantID][name]
	if !ok {
		return CredentialPool{}, ErrNotFound
	}
	return clonePool(row), nil
}

// Update implements Store.
func (m *Memory) Update(_ context.Context, tenantID, name string, mutate func(*CredentialPool) error) (CredentialPool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.pools[tenantID][name]
	if !ok {
		return CredentialPool{}, ErrNotFound
	}
	row = clonePool(row)
	prev := row.UpdatedAt
	if err := mutate(&row); err != nil {
		return CredentialPool{}, err
	}
	if err := Validate(row); err != nil {
		return CredentialPool{}, err
	}
	now := time.Now().UTC()
	if !now.After(prev) {
		now = prev.Add(time.Nanosecond)
	}
	row.UpdatedAt = now
	m.pools[tenantID][name] = clonePool(row)
	return clonePool(row), nil
}

// List implements Store, returning the tenant's pools in name order.
func (m *Memory) List(_ context.Context, tenantID string, filter ListFilter) ([]CredentialPool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]CredentialPool, 0, len(m.pools[tenantID]))
	for _, row := range m.pools[tenantID] {
		if !filter.IncludeDeleted && !row.IsActive() {
			continue
		}
		out = append(out, clonePool(row))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// SoftDelete implements Store. A second SoftDelete of the same pool is
// a no-op.
func (m *Memory) SoftDelete(_ context.Context, tenantID, name string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.pools[tenantID][name]
	if !ok {
		return ErrNotFound
	}
	if !row.DeletedAt.IsZero() {
		return nil
	}
	row.DeletedAt = at
	row.UpdatedAt = at
	m.pools[tenantID][name] = row
	return nil
}

// clonePool deep-copies the credential and host-pattern slices so a
// stored pool and a returned copy never share mutable state.
func clonePool(p CredentialPool) CredentialPool {
	p.Credentials = append([]Credential(nil), p.Credentials...)
	p.HostPatterns = append([]string(nil), p.HostPatterns...)
	return p
}

var _ Store = (*Memory)(nil)
