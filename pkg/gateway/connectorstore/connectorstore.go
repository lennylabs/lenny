// SPDX-License-Identifier: MIT

// Package connectorstore is the §9.3 ConnectorDefinition registry.
// It backs the §15.1 admin connector CRUD endpoints and the §9.1
// adapter-manifest connector list.
//
// Per §9.3 all connectors must be registered before they can be
// used — unregistered external endpoints cannot be called from
// inside a pod. The registry is therefore the SSRF allowlist for
// external MCP traffic: the gateway only dials hosts that resolve
// to a registered connector.
//
// spec: §4.2 line 173 — connectors are tenant-scoped. Each row
// carries a `tenant_id` column and is filtered by the standard
// lenny_tenant_isolation RLS policy. The Store API threads the
// owning tenant through every CRUD call; platform-admin reads pass
// `pgtenant.AllTenantsSentinel` to bypass the per-tenant predicate.
package connectorstore

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Connector captures the §9.3 ConnectorDefinition. v1 supports the
// MCP (Streamable HTTP) transport only; A2A / Agent Protocol
// transports are reserved post-v1.
type Connector struct {
	// TenantID is the §4.2 line 173 tenant-scoping column. Each
	// connector belongs to exactly one tenant; an in-memory Store
	// indexes by (TenantID, ID).
	TenantID string

	// ID is the §9.3 registry key, unique within a tenant.
	ID string

	// DisplayName is the human-facing label.
	DisplayName string

	// MCPServerURL is the §9.3 connector MCP endpoint. Must be HTTPS.
	MCPServerURL string

	// Transport is the §9.3 transport protocol. v1 accepts only
	// `streamable_http`.
	Transport string

	// Auth carries the OAuth2 configuration. Optional — public
	// connectors omit it.
	Auth *ConnectorAuth

	// Visibility scopes the connector: `tenant` (default) or
	// `platform`.
	Visibility string

	// Labels is the §9.3 environment-selector label map.
	Labels map[string]string

	// CreatedAt / UpdatedAt / DeletedAt audit timestamps.
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt time.Time
}

// IsActive reports whether the connector has not been soft-deleted.
func (c Connector) IsActive() bool { return c.DeletedAt.IsZero() }

// ConnectorAuth is the §9.3 OAuth2 auth block. The client secret is
// always a reference (`namespace/secret-name`); the raw secret never
// enters the registry per §9.3 ("encrypted, never in pods").
type ConnectorAuth struct {
	Type                  string   `json:"type"`
	AuthorizationEndpoint string   `json:"authorizationEndpoint,omitempty"`
	TokenEndpoint         string   `json:"tokenEndpoint,omitempty"`
	ClientID              string   `json:"clientId,omitempty"`
	ClientSecretRef       string   `json:"clientSecretRef,omitempty"`
	Scopes                []string `json:"scopes,omitempty"`
}

// Store is the §9.3 connector registry contract. Every method takes
// the §4.2 line 173 tenant context: a concrete tenant id scopes the
// operation to that tenant, and the AllTenantsSentinel value (`__all__`)
// lets a platform-admin code path span tenants on reads.
//
// spec: §12.1 line 5 — DeleteByUser and DeleteByTenant are the
// mandatory erasure primitives every storage role exposes at the
// interface level. Connectors are tenant-scoped resources with no
// user attribution, so DeleteByUser is a no-op that returns 0 erased
// rows; DeleteByTenant hard-deletes every connector owned by the
// tenant.
type Store interface {
	Create(ctx context.Context, c Connector) error
	Get(ctx context.Context, tenantID, id string) (Connector, error)
	Update(ctx context.Context, tenantID, id string, mutate func(*Connector) error) (Connector, error)
	List(ctx context.Context, tenantID string, filter ListFilter) ([]Connector, error)
	SoftDelete(ctx context.Context, tenantID, id string, at time.Time) error

	// DeleteByUser implements the §12.1 mandatory-erasure primitive.
	// Connectors are tenant-scoped resources keyed by (tenant_id, id)
	// and carry no user attribution; per-user erasure has nothing to
	// scope on at this layer, so the method returns 0 erased rows.
	DeleteByUser(ctx context.Context, tenantID, userID string) (int, error)

	// DeleteByTenant implements the §12.1 mandatory-erasure primitive.
	// Hard-deletes every connector owned by tenantID, returning the
	// number of rows removed.
	DeleteByTenant(ctx context.Context, tenantID string) (int, error)
}

// ListFilter narrows List results.
type ListFilter struct {
	IncludeDeleted bool
}

// Sentinel errors.
var (
	ErrNotFound      = errors.New("connectorstore: connector not found")
	ErrAlreadyExists = errors.New("connectorstore: connector already exists")
)

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,127}$`)

// ValidateID reports whether id satisfies the §9.3 connector-id
// format.
func ValidateID(id string) error {
	if id == "" {
		return errors.New("connectorstore: id is required")
	}
	if !idPattern.MatchString(id) {
		return errors.New(`connectorstore: id must match ^[a-z0-9][a-z0-9_-]{0,127}$`)
	}
	return nil
}

// Validate runs the §9.3 cross-field rules on a Connector. Used at
// the admin Create / Update boundary. Returns nil when admissible.
//
// Security: every URL must be HTTPS so a registered connector
// cannot become an http:// SSRF pivot, and the §9.3 v1 transport
// constraint (`streamable_http` only) is enforced.
//
// spec: §9.3 line 116-130 — the connector YAML example treats
// `mcpServerUrl` and the oauth2 endpoint+clientId triple as
// mandatory. The validator rejects malformed connectors at
// registration time so the §15.1 admin API matches the documented
// syntactic-validation contract instead of failing late at OAuth
// authorize time with CONNECTOR_OAUTH_INCOMPLETE. F-9.3.6, F-9.3.10.
func (c Connector) Validate() error {
	if c.TenantID == "" {
		return errors.New("connectorstore: tenant_id is required")
	}
	if err := ValidateID(c.ID); err != nil {
		return err
	}
	if c.Transport != "" && c.Transport != "streamable_http" {
		return errors.New("connectorstore: v1 transport must be streamable_http")
	}
	// spec: §9.3 line 140 — every connector must be registered before
	// it can be used. The mcpServerUrl is the endpoint the registry
	// authorizes; an empty value admits a connector that cannot be
	// dialed and would later fail at adapter-manifest time. F-9.3.10.
	if c.MCPServerURL == "" {
		return errors.New("connectorstore: mcpServerUrl is required")
	}
	if err := requireHTTPS(c.MCPServerURL, "mcpServerUrl"); err != nil {
		return err
	}
	switch c.Visibility {
	case "", "tenant", "platform":
		// ok
	default:
		return errors.New("connectorstore: visibility must be tenant or platform")
	}
	if c.Auth != nil {
		if c.Auth.Type != "" && c.Auth.Type != "oauth2" {
			return errors.New("connectorstore: auth.type must be oauth2")
		}
		// spec: §9.3 line 116-130 — an oauth2 auth block must carry
		// authorizationEndpoint, tokenEndpoint, and clientId. Without
		// these the OAuth authorize endpoint fails late at
		// `handleAuthorizeConnector` with CONNECTOR_OAUTH_INCOMPLETE
		// instead of rejecting at registration time. F-9.3.6.
		if c.Auth.Type == "oauth2" {
			if c.Auth.AuthorizationEndpoint == "" {
				return errors.New("connectorstore: auth.authorizationEndpoint is required when auth.type is oauth2")
			}
			if c.Auth.TokenEndpoint == "" {
				return errors.New("connectorstore: auth.tokenEndpoint is required when auth.type is oauth2")
			}
			if c.Auth.ClientID == "" {
				return errors.New("connectorstore: auth.clientId is required when auth.type is oauth2")
			}
		}
		for _, pair := range []struct{ field, value string }{
			{"auth.authorizationEndpoint", c.Auth.AuthorizationEndpoint},
			{"auth.tokenEndpoint", c.Auth.TokenEndpoint},
		} {
			if pair.value != "" {
				if err := requireHTTPS(pair.value, pair.field); err != nil {
					return err
				}
			}
		}
		// clientSecretRef must be a `namespace/name` reference, never
		// an inline secret value.
		if c.Auth.ClientSecretRef != "" && !strings.Contains(c.Auth.ClientSecretRef, "/") {
			return errors.New("connectorstore: auth.clientSecretRef must be a namespace/name reference")
		}
	}
	return nil
}

func requireHTTPS(raw, field string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return errors.New("connectorstore: " + field + " is not a valid URL")
	}
	if strings.ToLower(u.Scheme) != "https" {
		return errors.New("connectorstore: " + field + " must use https")
	}
	return nil
}

// AllTenantsSentinel is the §4.2 platform-admin cross-tenant value
// accepted by every Store read. Writes (Create/Update/SoftDelete) must
// always carry a concrete tenant_id — the sentinel is reads-only.
const AllTenantsSentinel = "__all__"

// Memory is the in-memory Store implementation. It keys connectors
// by (tenant_id, id) so two tenants may register a connector under
// the same logical id without collision.
type Memory struct {
	mu sync.RWMutex
	// connectors[tenant_id][id] = Connector
	connectors map[string]map[string]Connector
}

// NewMemory returns an empty Memory store.
func NewMemory() *Memory {
	return &Memory{connectors: map[string]map[string]Connector{}}
}

// spec: §12.1 line 5 — compile-time satisfaction of the mandatory
// erasure-bearing Store interface.
var _ Store = (*Memory)(nil)

// Create implements Store. The tenant_id is read from c.TenantID;
// Validate rejects empty values.
func (m *Memory) Create(_ context.Context, c Connector) error {
	if err := c.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tenant, ok := m.connectors[c.TenantID]
	if !ok {
		tenant = map[string]Connector{}
		m.connectors[c.TenantID] = tenant
	}
	if _, exists := tenant[c.ID]; exists {
		return ErrAlreadyExists
	}
	now := time.Now().UTC()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	if c.UpdatedAt.IsZero() {
		c.UpdatedAt = c.CreatedAt
	}
	tenant[c.ID] = c
	return nil
}

// Get implements Store. tenantID may be a concrete tenant id or the
// AllTenantsSentinel for a platform-admin read that does not know
// which tenant owns the connector.
func (m *Memory) Get(_ context.Context, tenantID, id string) (Connector, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if tenantID == AllTenantsSentinel {
		for _, tenant := range m.connectors {
			if row, ok := tenant[id]; ok {
				return row, nil
			}
		}
		return Connector{}, ErrNotFound
	}
	tenant, ok := m.connectors[tenantID]
	if !ok {
		return Connector{}, ErrNotFound
	}
	row, ok := tenant[id]
	if !ok {
		return Connector{}, ErrNotFound
	}
	return row, nil
}

// Update implements Store. tenantID must be a concrete tenant id (the
// AllTenantsSentinel is reads-only).
func (m *Memory) Update(_ context.Context, tenantID, id string, mutate func(*Connector) error) (Connector, error) {
	if tenantID == "" || tenantID == AllTenantsSentinel {
		return Connector{}, errors.New("connectorstore: Update requires a concrete tenant_id")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tenant, ok := m.connectors[tenantID]
	if !ok {
		return Connector{}, ErrNotFound
	}
	row, ok := tenant[id]
	if !ok {
		return Connector{}, ErrNotFound
	}
	prev := row.UpdatedAt
	if err := mutate(&row); err != nil {
		return Connector{}, err
	}
	// Lock the tenant id to the row's owner; a mutate that rewrites
	// it would silently re-parent the row.
	row.TenantID = tenantID
	if err := row.Validate(); err != nil {
		return Connector{}, err
	}
	now := time.Now().UTC()
	if !now.After(prev) {
		now = prev.Add(time.Nanosecond)
	}
	row.UpdatedAt = now
	tenant[id] = row
	return row, nil
}

// List implements Store. tenantID is either a concrete tenant id
// (returns that tenant's connectors) or the AllTenantsSentinel
// (returns every tenant's connectors, sorted by (tenant_id, id)).
func (m *Memory) List(_ context.Context, tenantID string, filter ListFilter) ([]Connector, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []Connector
	emit := func(tenant map[string]Connector) {
		for _, row := range tenant {
			if !filter.IncludeDeleted && !row.IsActive() {
				continue
			}
			out = append(out, row)
		}
	}
	if tenantID == AllTenantsSentinel {
		for _, tenant := range m.connectors {
			emit(tenant)
		}
	} else if tenant, ok := m.connectors[tenantID]; ok {
		emit(tenant)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TenantID != out[j].TenantID {
			return out[i].TenantID < out[j].TenantID
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// SoftDelete implements Store. tenantID must be a concrete tenant id.
func (m *Memory) SoftDelete(_ context.Context, tenantID, id string, at time.Time) error {
	if tenantID == "" || tenantID == AllTenantsSentinel {
		return errors.New("connectorstore: SoftDelete requires a concrete tenant_id")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tenant, ok := m.connectors[tenantID]
	if !ok {
		return ErrNotFound
	}
	row, ok := tenant[id]
	if !ok {
		return ErrNotFound
	}
	if !row.DeletedAt.IsZero() {
		return nil
	}
	row.DeletedAt = at
	row.UpdatedAt = at
	tenant[id] = row
	return nil
}

// DeleteByUser implements the §12.1 mandatory-erasure interface.
// Connectors are tenant-scoped resources keyed by (tenant_id, id) and
// carry no user attribution; per-user erasure has nothing to scope on
// at this layer. The orchestrator skips connectorstore on user-scoped
// runs.
func (m *Memory) DeleteByUser(_ context.Context, _, _ string) (int, error) {
	return 0, nil
}

// DeleteByTenant implements the §12.1 mandatory-erasure interface.
// Hard-deletes every connector owned by tenantID, returning the number
// of rows removed. The erasure orchestrator invokes this after
// soft-deletes have propagated through downstream consumers.
//
// spec: §4.2 line 173 — connectors are tenant-scoped, so a tenant
// erasure can purge their definitions cleanly.
func (m *Memory) DeleteByTenant(_ context.Context, tenantID string) (int, error) {
	if tenantID == "" || tenantID == AllTenantsSentinel {
		return 0, errors.New("connectorstore: DeleteByTenant requires a concrete tenant_id")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	tenant, ok := m.connectors[tenantID]
	if !ok {
		return 0, nil
	}
	n := len(tenant)
	delete(m.connectors, tenantID)
	return n, nil
}
