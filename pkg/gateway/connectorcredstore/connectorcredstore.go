// SPDX-License-Identifier: MIT

// Package connectorcredstore is the §9.3 connector-credential registry.
// After a connector OAuth flow completes, the gateway stores the
// resulting access token and refresh token here, keyed by the
// (tenant, connector, user) triple. Future tool calls from pods
// authorized for that connector draw on the stored credential — §9.3
// step 6, "Future calls from pods authorized for that connector use
// gateway-held connector state".
//
// This is distinct from credentialstore: credentialstore.Credential is
// keyed by the fixed credential.Provider LLM-provider enum
// (anthropic_direct, aws_bedrock, ...) and backs LLM credential leases.
// A §9.3 connector is addressed by an arbitrary registered connector
// id ("github", "jira", ...) which is not a credential.Provider, so a
// connector OAuth token needs its own store.
//
// §9.3 invariant — "Tokens never transit through pods. The gateway
// owns all downstream credential state." The store therefore never
// returns token material on the wire; the admin/REST handlers project
// a ConnectorCredential into a token-free status payload.
//
// Encryption at rest. §9.3 stores connector tokens "encrypted"; §12.9
// classifies an OAuth refresh token as T4 Restricted, which requires
// AES-256-GCM envelope encryption with a KMS-wrapped per-record DEK.
// The Postgres-backed implementation (subpackage pgstore) envelope-
// encrypts both tokens through pkg/kms/envelope. The in-memory Memory
// implementation here holds tokens in process for tests and the
// minimal gateway.
package connectorcredstore

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

// Sentinel errors. Callers compare with errors.Is.
var (
	// ErrNotFound is returned when no credential exists for the
	// requested (tenant, connector, user) triple.
	ErrNotFound = errors.New("connectorcredstore: connector credential not found")
)

// ConnectorCredential is one stored §9.3 connector OAuth credential.
// AccessToken and RefreshToken are raw token material; they are never
// marshalled to the wire — handlers project ConnectorCredential into a
// token-free payload.
type ConnectorCredential struct {
	// TenantID, ConnectorID, UserID, and Environment are the §4.3
	// line 202 four-tuple the credential is keyed by. ConnectorID is
	// the connectorstore registry id; UserID is the OAuth-completing
	// user; Environment is the §10.6 environment name (empty string
	// scopes the row to "no environment" / the default access path).
	// spec: §4.3 line 202.
	TenantID    string
	ConnectorID string
	UserID      string
	Environment string

	// AccessToken is the OAuth access token. NEVER serialised.
	AccessToken string

	// RefreshToken is the OAuth refresh token, empty when the provider
	// issued none. NEVER serialised.
	RefreshToken string

	// TokenType is the RFC 6749 token_type, typically "Bearer".
	TokenType string

	// Scopes is the granted scope set.
	Scopes []string

	// ExpiresAt is the absolute access-token expiry, zero when the
	// provider returned no expires_in.
	ExpiresAt time.Time

	// CreatedAt is the instant the credential was first stored;
	// UpdatedAt advances on every re-store (a re-authorization or a
	// refresh).
	CreatedAt time.Time
	UpdatedAt time.Time
}

// HasToken reports whether the credential carries access-token
// material. A zero ConnectorCredential reports false.
func (c ConnectorCredential) HasToken() bool { return c.AccessToken != "" }

// Store is the §9.3 connector-credential registry contract.
//
// spec: §4.3 line 202 — credentials are scoped by (tenant, connector,
// user, environment). The Environment argument carries the §10.6
// environment name; the empty string selects the no-environment scope.
type Store interface {
	// Put stores (or replaces) the credential for the four-tuple
	// (tenant, connector, user, environment). A second Put for the
	// same four-tuple replaces the token material and advances
	// UpdatedAt — the path a re-authorization or a refreshed token
	// takes.
	Put(ctx context.Context, cred ConnectorCredential) error

	// Get returns the credential for the four-tuple, or ErrNotFound.
	Get(ctx context.Context, tenantID, connectorID, userID, environment string) (ConnectorCredential, error)

	// Delete removes the credential for the four-tuple. Deleting an
	// absent four-tuple returns ErrNotFound.
	Delete(ctx context.Context, tenantID, connectorID, userID, environment string) error

	// ListByConnector returns every stored credential for one connector
	// within a tenant, ordered by user id. Token material is populated;
	// callers that surface the result project it away.
	ListByConnector(ctx context.Context, tenantID, connectorID string) ([]ConnectorCredential, error)

	// RotateAccessToken records the result of an RFC 6749 §6
	// refresh-token grant: the new access token replaces the stored
	// blob, the rotated_at column stamps the wall-clock time of the
	// rotation, and a non-empty RefreshToken on rot replaces the
	// stored refresh token (providers that rotate the refresh token
	// emit a new one; providers that do not leave RefreshToken empty
	// and the previously stored refresh token survives). UpdatedAt
	// advances. The four-tuple (tenant, connector, user, environment)
	// is the lookup key; ErrNotFound is returned when the row is
	// absent.
	//
	// spec: §4.3 line 200 (refresh tokens stored encrypted),
	// §4.3 line 202 (environment-scoped credentials),
	// §9.3 lines 152–155 (connector token lifecycle).
	RotateAccessToken(ctx context.Context, rot RotationRecord) error
}

// RotationRecord carries the new token material produced by an RFC 6749
// §6 refresh-token grant. The four-tuple (tenant, connector, user,
// environment) is the lookup key.
// spec: §4.3 line 202.
type RotationRecord struct {
	// TenantID, ConnectorID, UserID, and Environment identify the row
	// to rotate. Environment is the §10.6 environment name; empty
	// scopes to "no environment".
	TenantID    string
	ConnectorID string
	UserID      string
	Environment string

	// AccessToken is the freshly obtained access token. Required.
	AccessToken string

	// RefreshToken is the rotated refresh token. Some providers
	// rotate it on every refresh and others do not; pass empty to
	// leave the previously stored refresh token in place.
	RefreshToken string

	// TokenType is the RFC 6749 token_type (typically "Bearer").
	// Empty leaves the previously stored TokenType unchanged.
	TokenType string

	// Scopes is the granted scope set. Empty leaves the previously
	// stored Scopes unchanged.
	Scopes []string

	// ExpiresAt is the absolute expiry of the new access token.
	// Zero leaves the previously stored ExpiresAt unchanged.
	ExpiresAt time.Time
}

// fourKey is the in-memory map key for the (tenant, connector, user,
// environment) four-tuple. spec: §4.3 line 202.
func fourKey(tenantID, connectorID, userID, environment string) string {
	return tenantID + "\x00" + connectorID + "\x00" + userID + "\x00" + environment
}

// Memory is the in-memory Store implementation. It is safe for
// concurrent use.
type Memory struct {
	mu    sync.RWMutex
	creds map[string]ConnectorCredential
	clock func() time.Time
}

// NewMemory returns an empty Memory store. Pass nil for clock to
// default to time.Now in UTC.
func NewMemory(clock func() time.Time) *Memory {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Memory{creds: map[string]ConnectorCredential{}, clock: clock}
}

var _ Store = (*Memory)(nil)

// Put implements Store.
func (m *Memory) Put(_ context.Context, cred ConnectorCredential) error {
	switch {
	case cred.TenantID == "":
		return errors.New("connectorcredstore: tenant id is required")
	case cred.ConnectorID == "":
		return errors.New("connectorcredstore: connector id is required")
	case cred.UserID == "":
		return errors.New("connectorcredstore: user id is required")
	case cred.AccessToken == "":
		return errors.New("connectorcredstore: access token is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clock()
	k := fourKey(cred.TenantID, cred.ConnectorID, cred.UserID, cred.Environment)
	if prev, ok := m.creds[k]; ok {
		cred.CreatedAt = prev.CreatedAt
	} else if cred.CreatedAt.IsZero() {
		cred.CreatedAt = now
	}
	cred.UpdatedAt = now
	m.creds[k] = cred
	return nil
}

// Get implements Store.
func (m *Memory) Get(_ context.Context, tenantID, connectorID, userID, environment string) (ConnectorCredential, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.creds[fourKey(tenantID, connectorID, userID, environment)]
	if !ok {
		return ConnectorCredential{}, ErrNotFound
	}
	return c, nil
}

// Delete implements Store.
func (m *Memory) Delete(_ context.Context, tenantID, connectorID, userID, environment string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := fourKey(tenantID, connectorID, userID, environment)
	if _, ok := m.creds[k]; !ok {
		return ErrNotFound
	}
	delete(m.creds, k)
	return nil
}

// ListByConnector implements Store.
func (m *Memory) ListByConnector(_ context.Context, tenantID, connectorID string) ([]ConnectorCredential, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ConnectorCredential, 0)
	for _, c := range m.creds {
		if c.TenantID == tenantID && c.ConnectorID == connectorID {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UserID != out[j].UserID {
			return out[i].UserID < out[j].UserID
		}
		return out[i].Environment < out[j].Environment
	})
	return out, nil
}

// RotateAccessToken implements Store. spec: §4.3 line 200, §4.3 line
// 202, §9.3.
func (m *Memory) RotateAccessToken(_ context.Context, rot RotationRecord) error {
	switch {
	case rot.TenantID == "":
		return errors.New("connectorcredstore: tenant id is required")
	case rot.ConnectorID == "":
		return errors.New("connectorcredstore: connector id is required")
	case rot.UserID == "":
		return errors.New("connectorcredstore: user id is required")
	case rot.AccessToken == "":
		return errors.New("connectorcredstore: access token is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	k := fourKey(rot.TenantID, rot.ConnectorID, rot.UserID, rot.Environment)
	cred, ok := m.creds[k]
	if !ok {
		return ErrNotFound
	}
	now := m.clock()
	cred.AccessToken = rot.AccessToken
	if rot.RefreshToken != "" {
		cred.RefreshToken = rot.RefreshToken
	}
	if rot.TokenType != "" {
		cred.TokenType = rot.TokenType
	}
	if len(rot.Scopes) > 0 {
		cred.Scopes = append([]string(nil), rot.Scopes...)
	}
	if !rot.ExpiresAt.IsZero() {
		cred.ExpiresAt = rot.ExpiresAt
	}
	cred.UpdatedAt = now
	m.creds[k] = cred
	return nil
}
