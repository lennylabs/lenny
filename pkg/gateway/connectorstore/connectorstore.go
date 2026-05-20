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
	// ID is the §9.3 registry key.
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

// Store is the §9.3 connector registry contract.
type Store interface {
	Create(ctx context.Context, c Connector) error
	Get(ctx context.Context, id string) (Connector, error)
	Update(ctx context.Context, id string, mutate func(*Connector) error) (Connector, error)
	List(ctx context.Context, filter ListFilter) ([]Connector, error)
	SoftDelete(ctx context.Context, id string, at time.Time) error
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
func (c Connector) Validate() error {
	if err := ValidateID(c.ID); err != nil {
		return err
	}
	if c.Transport != "" && c.Transport != "streamable_http" {
		return errors.New("connectorstore: v1 transport must be streamable_http")
	}
	if c.MCPServerURL != "" {
		if err := requireHTTPS(c.MCPServerURL, "mcpServerUrl"); err != nil {
			return err
		}
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

// Memory is the in-memory Store implementation.
type Memory struct {
	mu         sync.RWMutex
	connectors map[string]Connector
}

// NewMemory returns an empty Memory store.
func NewMemory() *Memory { return &Memory{connectors: map[string]Connector{}} }

// Create implements Store.
func (m *Memory) Create(_ context.Context, c Connector) error {
	if err := c.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.connectors[c.ID]; exists {
		return ErrAlreadyExists
	}
	now := time.Now().UTC()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	if c.UpdatedAt.IsZero() {
		c.UpdatedAt = c.CreatedAt
	}
	m.connectors[c.ID] = c
	return nil
}

// Get implements Store.
func (m *Memory) Get(_ context.Context, id string) (Connector, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	row, ok := m.connectors[id]
	if !ok {
		return Connector{}, ErrNotFound
	}
	return row, nil
}

// Update implements Store.
func (m *Memory) Update(_ context.Context, id string, mutate func(*Connector) error) (Connector, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.connectors[id]
	if !ok {
		return Connector{}, ErrNotFound
	}
	prev := row.UpdatedAt
	if err := mutate(&row); err != nil {
		return Connector{}, err
	}
	if err := row.Validate(); err != nil {
		return Connector{}, err
	}
	now := time.Now().UTC()
	if !now.After(prev) {
		now = prev.Add(time.Nanosecond)
	}
	row.UpdatedAt = now
	m.connectors[id] = row
	return row, nil
}

// List implements Store.
func (m *Memory) List(_ context.Context, filter ListFilter) ([]Connector, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Connector, 0, len(m.connectors))
	for _, row := range m.connectors {
		if !filter.IncludeDeleted && !row.IsActive() {
			continue
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// SoftDelete implements Store.
func (m *Memory) SoftDelete(_ context.Context, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.connectors[id]
	if !ok {
		return ErrNotFound
	}
	if !row.DeletedAt.IsZero() {
		return nil
	}
	row.DeletedAt = at
	row.UpdatedAt = at
	m.connectors[id] = row
	return nil
}

// DeleteByUser implements the §12.2.1 mandatory-erasure interface.
// Connectors are platform-scoped resources keyed by id; the
// Visibility field discriminates platform vs tenant exposure but
// the Connector struct carries no tenant id, so per-user erasure
// has nothing to scope on at this layer. The orchestrator skips
// connectorstore on user-scoped runs.
func (m *Memory) DeleteByUser(_ context.Context, _, _ string) (int, error) {
	return 0, nil
}

// DeleteByTenant implements the §12.2.1 mandatory-erasure interface.
// A platform-visibility connector survives tenant erasure; tenant-
// visibility connectors carry no tenant_id today, so the per-tenant
// sweep is also a no-op at this layer. A follow-on Connector.TenantID
// migration enables real per-tenant cleanup (see BUILD-GAPS.md).
func (m *Memory) DeleteByTenant(_ context.Context, _ string) (int, error) {
	return 0, nil
}
