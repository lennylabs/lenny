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

	"github.com/lennylabs/lenny/pkg/credential"
)

// CredentialStatus is the §4.9 per-credential lifecycle state within a
// pool. An empty status reads as active, so a credential persisted
// before the revocation fields existed is treated as usable.
type CredentialStatus string

const (
	// CredentialActive — the credential is assignable.
	CredentialActive CredentialStatus = "active"

	// CredentialRevoked — the credential was emergency-revoked (§4.9
	// Emergency Credential Revocation). It is held off assignment and
	// its identity is on the §4.9 deny list.
	CredentialRevoked CredentialStatus = "revoked"
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

	// Status is the §4.9 lifecycle state of this credential within the
	// pool. Empty reads as active. Emergency revocation
	// (POST .../credentials/{credId}/revoke) sets it to revoked; the
	// /re-enable path returns it to active. spec: §4.9 line 1640 step 1.
	Status CredentialStatus

	// RevokedAt / RevokedBy / RevocationReason record the §4.9 emergency
	// revocation (spec line 1640 step 1). They are zero for an active
	// credential.
	RevokedAt        time.Time
	RevokedBy        string
	RevocationReason string
}

// IsRevoked reports whether the credential has been emergency-revoked.
// An empty Status reads as active. spec: §4.9 line 1640 step 1.
func (c Credential) IsRevoked() bool { return c.Status == CredentialRevoked }

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

	// CachePolicy is the §4.9 optional semantic-cache configuration for
	// the pool (strategy, ttl, similarityThreshold, backend). It is nil
	// when the pool declares no cachePolicy; §4.9 caching is disabled by
	// default and opt-in per pool, so a nil policy (or one with Enabled
	// false) leaves the LLM proxy path uncached. spec: §4.9 lines 1542-1556.
	CachePolicy *CachePolicy

	// CreatedAt / UpdatedAt / DeletedAt are the audit timestamps.
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt time.Time

	// Version is the §15.1 optimistic-concurrency counter: it starts at
	// 1 and increments on every successful write (Update or SoftDelete).
	// The quoted decimal version is the resource's strong ETag, enforced
	// on the admin PUT via the If-Match precondition and exposed on
	// GET/list responses. spec: §15.1 lines 1207-1213.
	Version int64
}

// IsActive reports whether the pool has not been soft-deleted.
func (p CredentialPool) IsActive() bool { return p.DeletedAt.IsZero() }

// CachePolicy is the §4.9 semantic-cache configuration on a pool. The
// spec example (lines 1542-1547) carries strategy, ttl, similarityThreshold,
// and backend; Enabled is the per-pool opt-in (§4.9 line 1549 — caching is
// disabled by default and opt-in per pool). The zero value (every field
// empty, Enabled false) is the disabled state.
//
// spec: spec/04_system-components.md lines 1542-1556.
type CachePolicy struct {
	// Enabled is the per-pool opt-in. A policy with Enabled false leaves
	// the LLM proxy path uncached even when the other fields are set.
	Enabled bool `json:"enabled"`

	// Strategy is the §4.9 cache strategy. The launch strategy is
	// `semantic`; empty selects it.
	Strategy string `json:"strategy,omitempty"`

	// TTLSeconds is the §4.9 cache entry TTL. Zero selects the
	// semanticcache default (300s) at consumption time.
	TTLSeconds int `json:"ttlSeconds,omitempty"`

	// SimilarityThreshold is the §4.9 cosine-similarity hit threshold in
	// [0, 1]. Zero selects the semanticcache default (0.92) at
	// consumption time.
	SimilarityThreshold float64 `json:"similarityThreshold,omitempty"`

	// Backend is the §4.9 cache backend (`redis` or `memory`). Empty
	// selects the deployment default (redis) at consumption time.
	Backend string `json:"backend,omitempty"`
}

// validCacheStrategy reports whether s is an accepted §4.9 cache
// strategy. Empty is accepted and selects `semantic` at consumption
// time; `semantic` is the only launch strategy.
func validCacheStrategy(s string) bool {
	switch s {
	case "", "semantic":
		return true
	default:
		return false
	}
}

// validCacheBackend reports whether b is an accepted §4.9 cache backend.
// Empty is accepted and selects the deployment default. `redis` is the
// §4.9 default implementation; `memory` is the in-process backend.
func validCacheBackend(b string) bool {
	switch b {
	case "", "redis", "memory":
		return true
	default:
		return false
	}
}

// validateCachePolicy reports the §4.9 structural invariants of a pool's
// CachePolicy: a recognized strategy and backend, a non-negative ttl,
// and a similarityThreshold in [0, 1]. A nil policy is valid (caching is
// off). spec: §4.9 lines 1542-1556.
func validateCachePolicy(c *CachePolicy) error {
	if c == nil {
		return nil
	}
	if !validCacheStrategy(c.Strategy) {
		return errors.New("credentialpoolstore: cachePolicy.strategy must be semantic")
	}
	if !validCacheBackend(c.Backend) {
		return errors.New("credentialpoolstore: cachePolicy.backend must be redis or memory")
	}
	if c.TTLSeconds < 0 {
		return errors.New("credentialpoolstore: cachePolicy.ttl must be >= 0")
	}
	if c.SimilarityThreshold < 0 || c.SimilarityThreshold > 1 {
		return errors.New("credentialpoolstore: cachePolicy.similarityThreshold must be in [0, 1]")
	}
	return nil
}

// RevokedCredential identifies one revoked pool credential for the §4.9
// startup deny-list rebuild. The §4.9 deny list keys a pool-backed
// entry on (poolId, credentialId), and a lease's PoolID is the pool
// name (§4.9), so PoolName is the deny-list poolId.
type RevokedCredential struct {
	// TenantID owns the pool.
	TenantID string
	// PoolName is the pool the credential belongs to; it is the
	// deny-list poolId.
	PoolName string
	// CredentialID is the revoked credential's per-pool id.
	CredentialID string
}

// Store is the §4.9 CredentialPool registry contract. Every method is
// tenant-scoped except RevokedCredentials, which scans all tenants for
// the startup deny-list rebuild.
type Store interface {
	Create(ctx context.Context, p CredentialPool) error
	Get(ctx context.Context, tenantID, name string) (CredentialPool, error)
	Update(ctx context.Context, tenantID, name string, mutate func(*CredentialPool) error) (CredentialPool, error)
	List(ctx context.Context, tenantID string, filter ListFilter) ([]CredentialPool, error)
	SoftDelete(ctx context.Context, tenantID, name string, at time.Time) error

	// RevokedCredentials returns every revoked credential across all
	// tenants' active (not soft-deleted) pools, for the §4.9 startup
	// deny-list rebuild. Soft-deleted pools are skipped: their leases
	// are gone, so their credentials cannot be presented.
	//
	// spec: §4.9 lines 1668-1673 — a newly started gateway replica
	// rebuilds its deny list from the stores' revoked entries so no
	// revoked credential silently becomes accepted on a replica that
	// missed the original pub/sub notification.
	RevokedCredentials(ctx context.Context) ([]RevokedCredential, error)

	// DeleteByUser implements the §12.1 mandatory-erasure primitive.
	// Credential pools are tenant-scoped configuration keyed by
	// (tenant, name); they carry no user_id column, so a user erasure
	// removes no pool definition. The §12.8 user-erasure path releases
	// the user's per-session credential leases through the LeaseStore,
	// not through this store. DeleteByUser is therefore a no-op that
	// returns (0, nil); the method is mandatory at the interface level
	// so the §12.1 compile-time contract holds for every backend, and
	// the no-op mirrors the documented pattern for non-user-scoped
	// stores.
	//
	// spec: §12.1 line 5.
	DeleteByUser(ctx context.Context, tenantID, userID string) (int, error)

	// DeleteByTenant implements the §12.1 mandatory-erasure primitive.
	// Hard-deletes every credential pool owned by tenantID — the §12.8
	// Phase 4 tenant-teardown path for the CredentialPoolStore role.
	// Returns the number of pools removed.
	//
	// spec: §12.1 line 5, §12.8 Phase 4.
	DeleteByTenant(ctx context.Context, tenantID string) (int, error)
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

// validProxyDialect reports whether d is an accepted §4.9 / §26 proxy
// dialect. Empty is accepted (a direct-mode pool declares no dialect).
// The §4.9 launch dialects are `openai` and `anthropic`; §26 extends the
// set with `google` (codex / langgraph / mastra) and `cursor` (cursor-cli).
// spec: §4.9 lines 1473-1476; §26.6 line 297; §26.5/§26.8/§26.9.
func validProxyDialect(d string) bool {
	if d == "" {
		return true
	}
	return credential.ProxyDialect(d).IsValid()
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
	if err := validateCachePolicy(p.CachePolicy); err != nil {
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
		switch c.Status {
		case "", CredentialActive, CredentialRevoked:
		default:
			return errors.New("credentialpoolstore: credential status must be active or revoked")
		}
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
	// spec: §15.1 line 1207 — every admin resource version starts at 1.
	if p.Version == 0 {
		p.Version = 1
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
	// spec: §15.1 line 1207 — bump the optimistic-concurrency version on
	// every successful Update so the next If-Match compares against it.
	row.Version++
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
	// spec: §15.1 line 1213 — a soft-delete is a write; advance the
	// version so a stale If-Match on a later operation is caught.
	row.Version++
	m.pools[tenantID][name] = row
	return nil
}

// RevokedCredentials implements Store. It scans every tenant's active
// pools and returns each revoked credential in a deterministic order.
func (m *Memory) RevokedCredentials(_ context.Context) ([]RevokedCredential, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []RevokedCredential
	for tenantID, byName := range m.pools {
		for name, p := range byName {
			if !p.IsActive() {
				continue
			}
			for _, c := range p.Credentials {
				if c.IsRevoked() {
					out = append(out, RevokedCredential{TenantID: tenantID, PoolName: name, CredentialID: c.ID})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TenantID != out[j].TenantID {
			return out[i].TenantID < out[j].TenantID
		}
		if out[i].PoolName != out[j].PoolName {
			return out[i].PoolName < out[j].PoolName
		}
		return out[i].CredentialID < out[j].CredentialID
	})
	return out, nil
}

// clonePool deep-copies the credential and host-pattern slices so a
// stored pool and a returned copy never share mutable state.
func clonePool(p CredentialPool) CredentialPool {
	p.Credentials = append([]Credential(nil), p.Credentials...)
	p.HostPatterns = append([]string(nil), p.HostPatterns...)
	return p
}

// DeleteByUser implements Store. Credential pools are tenant-scoped, so
// a user erasure removes no pool; it returns (0, nil).
//
// spec: §12.1 line 5.
func (m *Memory) DeleteByUser(_ context.Context, tenantID, userID string) (int, error) {
	if tenantID == "" || userID == "" {
		return 0, errors.New("credentialpoolstore: DeleteByUser requires non-empty tenant_id and user_id")
	}
	return 0, nil
}

// DeleteByTenant implements Store. It removes every credential pool the
// tenant owns and returns the count.
//
// spec: §12.1 line 5, §12.8 Phase 4.
func (m *Memory) DeleteByTenant(_ context.Context, tenantID string) (int, error) {
	if tenantID == "" {
		return 0, errors.New("credentialpoolstore: DeleteByTenant requires a concrete tenant_id")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	n := len(m.pools[tenantID])
	delete(m.pools, tenantID)
	return n, nil
}

var _ Store = (*Memory)(nil)
