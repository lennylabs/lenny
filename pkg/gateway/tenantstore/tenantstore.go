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
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/experiment"
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

	// WorkspaceTier is the §12.9 data-classification tier. The
	// tenant-settable values per §15.1 are `T3` (the Confidential
	// default) and `T4` (Restricted). Empty defaults to T3 at write
	// time. `T1`/`T2` classify other data categories and are not
	// selectable as a tenant workspaceTier; ValidWorkspaceTier rejects
	// them, IsWorkspaceTierDowngrade ratchets T3↔T4.
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

	// TokenQuotaPerWindow is the §11.2 per-tenant LLM-token budget for
	// one reset-period window. The §4.8 QuotaEvaluator enforces it
	// hierarchically (global → tenant → user) against the §11.2 Redis
	// token-usage counter and rejects a session create with
	// QUOTA_EXCEEDED once a window's recorded usage reaches the limit.
	// Zero means the tenant has no token budget (unlimited).
	TokenQuotaPerWindow int64

	// QuotaResetPeriod is the §11.2 line 31 per-tenant token-quota reset
	// period: `hourly`, `daily`, `monthly`, or `rolling`. Empty means
	// the tenant inherits the platform-wide default period. The §4.8
	// QuotaEvaluator reads this to scope the tenant's token-usage window
	// independently of other tenants.
	QuotaResetPeriod string

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

	// BillingErasurePolicy is the §12.8 policy that decides how the
	// GDPR erasure job treats this tenant's billing events. Empty or
	// `pseudonymize` replaces an erased user's id with a salted hash;
	// `exempt` retains the original id under GDPR Article 17(3)(b) for
	// financial record-keeping.
	BillingErasurePolicy string

	// NoEnvironmentPolicy is the §10.6 tenant RBAC-config policy that
	// governs runtime access for an authenticated caller who is a
	// member of no environment. `deny-all` (the platform default,
	// also the value an empty field is treated as) denies all runtime
	// access; `allow-all` grants access to every runtime owned by the
	// tenant.
	NoEnvironmentPolicy string

	// RBACConfig is the rest of the §10.6 tenant RBAC configuration set
	// through PUT /v1/admin/tenants/{id}/rbac-config: the OIDC identity
	// provider, the token policy, the tenant-custom capability taxonomy,
	// and the per-tenant MCP-annotation capability-inference overrides.
	// The zero value means the tenant configured none of these.
	RBACConfig RBACConfig

	// ExperimentTargeting is the §10.7 experimentTargeting block: how
	// the tenant resolves mode:external experiment assignment. The zero
	// value means the tenant configures no external targeting and only
	// percentage-mode experiments route.
	ExperimentTargeting experiment.TargetingConfig

	// CredentialPolicy is the §4.9 tenant credentialPolicy: which
	// credential sources are available and how they are selected. The
	// §4.9 CredentialRouter intersects ProviderPools with a Runtime's
	// supportedProviders at session creation. The zero value means the
	// tenant configures no credential sourcing and the intersection is
	// empty (sessions assign no upstream LLM credentials).
	CredentialPolicy credential.CredentialPolicy

	// ErasureSalt is the §12.8 per-tenant billing-pseudonymization
	// secret (256-bit). It is non-nil only transiently, while an
	// erasure job pseudonymizes the tenant's billing events; the job
	// destroys it immediately afterward so the pseudonymized records
	// cannot be re-identified. Postgres persistence of this field is
	// deferred with the rest of the §12.8 Postgres billing-erasure
	// path: the salt must be KMS-envelope-encrypted, never stored
	// plaintext.
	ErasureSalt []byte

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

// §12.9 data-classification tier values a tenant (or a stricter
// environment override) may carry. T1/T2 classify other data categories
// and are not tenant-settable workspace tiers; the tenant-settable set
// per §15.1 is T3 (default) and T4. spec: §12.9 lines 1025-1033.
const (
	WorkspaceTierT3 = "T3"
	WorkspaceTierT4 = "T4"
)

// workspaceTierRank maps the §12.9 tenant-settable classification tier to
// a strictness ordinal. The empty string is the T3 default, so it shares
// T3's rank. Off-ladder values (T1, T2, or any unrecognized string) are
// absent from the map. spec: §12.9 lines 1033, 1048; §15.1 line 816.
var workspaceTierRank = map[string]int{
	"":              1,
	WorkspaceTierT3: 1,
	WorkspaceTierT4: 2,
}

// ValidWorkspaceTier reports whether s is a §12.9 tenant-settable
// data-classification tier: the empty string (the T3 default), T3, or T4.
// Per §15.1 the tenant-settable values are restricted to T3 and T4; a
// value like "T2", "T5", or "prod" is a misconfiguration that downstream
// consumers would silently treat as "not T4". spec: §12.9 line 1048.
func ValidWorkspaceTier(s string) bool {
	_, ok := workspaceTierRank[s]
	return ok
}

// WorkspaceTierRank returns the §12.9 strictness ordinal for tier and
// whether tier is on the ratchet ladder. A higher ordinal is stricter.
func WorkspaceTierRank(tier string) (rank int, onLadder bool) {
	r, ok := workspaceTierRank[tier]
	return r, ok
}

// IsWorkspaceTierDowngrade reports whether a transition from current to
// requested lowers the §12.9 classification tier. §15.1 states that
// workspaceTier is ratcheted stricter-only, exactly as the §11.7
// complianceProfile is. A transition that involves an off-ladder tier is
// not treated as a downgrade (the enum validator rejects those values
// before they reach the ratchet). spec: §12.9 line 1033; §15.1 line 816.
func IsWorkspaceTierDowngrade(current, requested string) bool {
	cur, curOnLadder := workspaceTierRank[current]
	req, reqOnLadder := workspaceTierRank[requested]
	if !curOnLadder || !reqOnLadder {
		return false
	}
	return req < cur
}

// IdentityProvider is the §10.6 tenant identity-provider configuration.
// v1 carries the OIDC provider type and the introspectionEnabled toggle
// the §10.6 line 661 real-time group check reads. spec: §10.6 line 661.
type IdentityProvider struct {
	// Type names the identity provider. The §10.6 identity model is
	// OIDC; an empty Type means the tenant inherits the platform OIDC
	// configuration.
	Type string `json:"type,omitempty"`

	// IntrospectionEnabled turns on the §10.6 line 661 real-time group
	// check (RFC 7662 token introspection) at the documented latency
	// cost. The default is off — JWT group claims alone carry group
	// identity.
	IntrospectionEnabled bool `json:"introspectionEnabled,omitempty"`
}

// RBACConfig is the §10.6 tenant RBAC configuration carried by
// PUT /v1/admin/tenants/{id}/rbac-config beyond the noEnvironmentPolicy
// column. spec: §10.6 line 665.
type RBACConfig struct {
	// IdentityProvider is the §10.6 OIDC identity-provider configuration.
	IdentityProvider IdentityProvider `json:"identityProvider,omitempty"`

	// TokenPolicy is the §10.6 tenant token policy. §10.6 line 665 names
	// the field but does not define its sub-fields; the gateway stores
	// it verbatim as an opaque JSON object and round-trips it without
	// interpreting fields the spec does not define. A nil or empty value
	// means the tenant set no token policy.
	TokenPolicy json.RawMessage `json:"tokenPolicy,omitempty"`

	// Capabilities is the §10.6 tenant-custom capability taxonomy: the
	// capability names this tenant adds on top of the platform defaults.
	Capabilities []string `json:"capabilities,omitempty"`

	// MCPAnnotationMapping is the §10.6 / §5.1 line 325 per-tenant
	// override of the MCP-annotation capability inference: a tool name
	// maps to the §5.3 capability set the gateway assigns it, overriding
	// the value inferred from the tool's MCP ToolAnnotations.
	MCPAnnotationMapping map[string][]string `json:"mcpAnnotationMapping,omitempty"`
}

// Clone deep-copies the RBACConfig so a stored value and a returned copy
// never share the TokenPolicy backing array, the Capabilities slice, or
// the MCPAnnotationMapping map.
func (c RBACConfig) Clone() RBACConfig {
	cp := c
	if c.TokenPolicy != nil {
		cp.TokenPolicy = append(json.RawMessage(nil), c.TokenPolicy...)
	}
	if c.Capabilities != nil {
		cp.Capabilities = append([]string(nil), c.Capabilities...)
	}
	if c.MCPAnnotationMapping != nil {
		cp.MCPAnnotationMapping = make(map[string][]string, len(c.MCPAnnotationMapping))
		for k, v := range c.MCPAnnotationMapping {
			cp.MCPAnnotationMapping[k] = append([]string(nil), v...)
		}
	}
	return cp
}

// §12.8 BillingErasurePolicy values.
const (
	// BillingErasurePseudonymize is the default policy: the erasure job
	// replaces an erased user's id with a salted hash. An empty
	// BillingErasurePolicy is treated as this value.
	BillingErasurePseudonymize = "pseudonymize"

	// BillingErasureExempt retains billing events with the original
	// user id under GDPR Article 17(3)(b).
	BillingErasureExempt = "exempt"
)

// §10.6 NoEnvironmentPolicy values. The platform default is deny-all;
// an empty NoEnvironmentPolicy is treated as deny-all.
const (
	// NoEnvPolicyDenyAll denies runtime access to a caller who is a
	// member of no environment.
	NoEnvPolicyDenyAll = "deny-all"
	// NoEnvPolicyAllowAll grants such a caller access to every runtime
	// owned by the tenant.
	NoEnvPolicyAllowAll = "allow-all"
)

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
	m.tenants[t.ID] = cloneTenant(t)
	return nil
}

// cloneTenant returns a deep copy of t so the in-memory store never
// shares the ExperimentTargeting sub-blocks or the ErasureSalt backing
// array with a caller — a mutation through a returned or stored Tenant
// cannot reach into the registry.
func cloneTenant(t Tenant) Tenant {
	cp := t
	if t.ErasureSalt != nil {
		cp.ErasureSalt = append([]byte(nil), t.ErasureSalt...)
	}
	cp.ExperimentTargeting = t.ExperimentTargeting.Clone()
	cp.CredentialPolicy = t.CredentialPolicy.Clone()
	cp.RBACConfig = t.RBACConfig.Clone()
	return cp
}

// Get implements Store.
func (m *Memory) Get(_ context.Context, id string) (Tenant, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	row, ok := m.tenants[id]
	if !ok {
		return Tenant{}, ErrNotFound
	}
	return cloneTenant(row), nil
}

// Update implements Store.
func (m *Memory) Update(_ context.Context, id string, mutate func(*Tenant) error) (Tenant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	stored, ok := m.tenants[id]
	if !ok {
		return Tenant{}, ErrNotFound
	}
	row := cloneTenant(stored)
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
	return cloneTenant(row), nil
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
		out = append(out, cloneTenant(row))
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
