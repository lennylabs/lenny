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
	// TenantID, ConnectorID, and UserID are the §9.3 triple the
	// credential is keyed by. ConnectorID is the connectorstore
	// registry id; UserID is the OAuth-completing user.
	TenantID    string
	ConnectorID string
	UserID      string

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
type Store interface {
	// Put stores (or replaces) the credential for the
	// (tenant, connector, user) triple. A second Put for the same
	// triple replaces the token material and advances UpdatedAt — the
	// path a re-authorization or a refreshed token takes.
	Put(ctx context.Context, cred ConnectorCredential) error

	// Get returns the credential for the triple, or ErrNotFound.
	Get(ctx context.Context, tenantID, connectorID, userID string) (ConnectorCredential, error)

	// Delete removes the credential for the triple. Deleting an absent
	// triple returns ErrNotFound.
	Delete(ctx context.Context, tenantID, connectorID, userID string) error

	// ListByConnector returns every stored credential for one connector
	// within a tenant, ordered by user id. Token material is populated;
	// callers that surface the result project it away.
	ListByConnector(ctx context.Context, tenantID, connectorID string) ([]ConnectorCredential, error)
}

// tripleKey is the in-memory map key for the (tenant, connector, user)
// triple.
func tripleKey(tenantID, connectorID, userID string) string {
	return tenantID + "\x00" + connectorID + "\x00" + userID
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
	k := tripleKey(cred.TenantID, cred.ConnectorID, cred.UserID)
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
func (m *Memory) Get(_ context.Context, tenantID, connectorID, userID string) (ConnectorCredential, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.creds[tripleKey(tenantID, connectorID, userID)]
	if !ok {
		return ConnectorCredential{}, ErrNotFound
	}
	return c, nil
}

// Delete implements Store.
func (m *Memory) Delete(_ context.Context, tenantID, connectorID, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := tripleKey(tenantID, connectorID, userID)
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
	sort.Slice(out, func(i, j int) bool { return out[i].UserID < out[j].UserID })
	return out, nil
}
