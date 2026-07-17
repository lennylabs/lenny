// SPDX-License-Identifier: MIT

// Package credentialstore is the §4.9 / §15.1 end-user credential
// registry. It backs the `/v1/credentials` REST endpoints: a user
// registers one credential per provider, the gateway stores the
// secret material out of band, and lease assignment draws on the
// registered credential.
//
// The store NEVER returns secret material. A registered credential
// is addressed by an opaque `credential_ref`; the secret is held in
// a separate field that is excluded from every wire payload per
// §15.1 ("no secret material returned").
//
// Credentials are scoped to (tenant_id, user_id, provider): the
// triple is unique, and re-registering the same triple replaces the
// secret per §15.1.
package credentialstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
)

// Status is the credential lifecycle state.
type Status string

const (
	// StatusActive — the credential is usable for lease assignment.
	StatusActive Status = "active"
	// StatusRevoked — the credential has been revoked; all leases
	// backed by it are invalidated per §15.1.
	StatusRevoked Status = "revoked"
)

// Credential is one registered user credential. The Secret field is
// the raw secret material; it is never marshalled to the wire — the
// admin/REST handlers project Credential into a secret-free payload.
type Credential struct {
	// Ref is the opaque credential_ref the user addresses the
	// credential by.
	Ref string

	// TenantID + UserID + Environment scope the credential per §4.3
	// line 202. Environment is the §10.6 environment name; the empty
	// string selects the no-environment scope (the default).
	TenantID    string
	UserID      string
	Environment string

	// Provider is the §4.9 credential provider.
	Provider credential.Provider

	// Secret is the raw secret material. NEVER serialised.
	Secret string

	// Status is the lifecycle state.
	Status Status

	// CreatedAt / RotatedAt / RevokedAt are the audit timestamps.
	CreatedAt time.Time
	RotatedAt time.Time
	RevokedAt time.Time

	// LastUsedAt is the last instant the credential was resolved for a
	// lease. It is the zero time until the credential is first used.
	// The §15.1 GET /v1/credentials response carries it.
	//
	// spec: §4.9 line 1349, 1365 — last_used_at is a /v1/credentials
	// response field.
	LastUsedAt time.Time
}

// Sentinel errors.
var (
	ErrNotFound = errors.New("credentialstore: credential not found")
)

// RevokedUserCredential identifies one revoked user credential for the
// §4.9 startup deny-list rebuild. The §4.9 deny list keys a user-backed
// entry on (tenantId, credentialRef).
//
// spec: §4.9 — the startup rebuild's user-credential term.
type RevokedUserCredential struct {
	// TenantID owns the credential.
	TenantID string
	// CredentialRef is the opaque credential_ref; it is the deny-list
	// credentialRef.
	CredentialRef string
}

// Store is the §4.9 user-credential registry contract.
//
// spec: §4.3 line 202 — credentials are scoped by (tenant, user,
// provider, environment). Register takes the environment as a
// distinct argument; the unique constraint widens to (tenant, user,
// provider, environment) so the same triple can hold one credential
// per environment.
type Store interface {
	// Register stores (or replaces) the credential for the
	// (tenant, user, provider, environment) four-tuple, returning the
	// credential_ref. Re-registering the same four-tuple replaces the
	// secret and refreshes RotatedAt per §15.1.
	Register(ctx context.Context, tenantID, userID string, provider credential.Provider, environment, secret string) (Credential, error)

	// Get returns the credential by ref, scoped to the tenant.
	Get(ctx context.Context, tenantID, ref string) (Credential, error)

	// Lookup resolves the credential for the §4.3 line 202
	// (tenant, user, provider, environment) four-tuple. It backs the §4.9
	// Pre-Authorized Credential Flow's session-creation resolution: the
	// gateway resolves a user-scoped credential by provider, not by ref.
	// Returns ErrNotFound when no credential is registered for the
	// four-tuple. A revoked credential is returned (Status reports it); the
	// resolution path treats a revoked credential as not-found per §4.9
	// line 1379.
	//
	// spec: §4.9 lines 1347-1351 (resolution at session creation).
	Lookup(ctx context.Context, tenantID, userID string, provider credential.Provider, environment string) (Credential, error)

	// List returns the user's registered credentials.
	List(ctx context.Context, tenantID, userID string) ([]Credential, error)

	// RevokedCredentials returns every revoked user credential across
	// all tenants, for the §4.9 startup deny-list rebuild. It is the
	// only method that is not tenant-scoped.
	//
	// spec: §4.9 — a newly started gateway replica rebuilds its deny
	// list from the stores' revoked entries so no revoked credential
	// silently becomes accepted on a replica that missed the original
	// pub/sub notification.
	RevokedCredentials(ctx context.Context) ([]RevokedUserCredential, error)

	// Rotate replaces the secret material for an existing credential.
	Rotate(ctx context.Context, tenantID, ref, newSecret string) (Credential, error)

	// Revoke marks the credential revoked.
	Revoke(ctx context.Context, tenantID, ref string) (Credential, error)

	// MarkUsed records at as the credential's last-used instant. The
	// §4.9 lease-resolution path calls it when it resolves a user
	// credential for a lease so the §15.1 GET /v1/credentials response
	// reports last_used_at. It is a no-op for an unknown ref.
	//
	// spec: §4.9 line 1349, 1365.
	MarkUsed(ctx context.Context, tenantID, ref string, at time.Time) error

	// Delete removes the credential row.
	Delete(ctx context.Context, tenantID, ref string) error

	// DeleteByUser implements the §12.1 mandatory-erasure primitive.
	// Removes every credential row owned by (tenantID, userID) — the
	// §4.9 user-credential registry is user-keyed, so DeleteByUser is
	// the load-bearing path for GDPR erasure of this store. Returns
	// the number of rows removed.
	//
	// spec: §12.1 line 5, §12.8 step `TokenStore`.
	DeleteByUser(ctx context.Context, tenantID, userID string) (int, error)

	// DeleteByTenant implements the §12.1 mandatory-erasure primitive.
	// Hard-deletes every credential row owned by tenantID.
	//
	// spec: §12.1 line 5, §12.8 Phase 4.
	DeleteByTenant(ctx context.Context, tenantID string) (int, error)
}

// Memory is the in-memory Store implementation.
type Memory struct {
	mu          sync.RWMutex
	byRef       map[string]Credential // key: tenantID + "/" + ref
	refByTriple map[string]string     // key: tenant/user/provider -> ref
	clock       func() time.Time
}

// NewMemory returns an empty Memory store. Pass nil for clock to
// default to time.Now.
func NewMemory(clock func() time.Time) *Memory {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Memory{
		byRef:       map[string]Credential{},
		refByTriple: map[string]string{},
		clock:       clock,
	}
}

func refKey(tenantID, ref string) string { return tenantID + "/" + ref }

// scopeKey is the in-memory map key for the §4.3 line 202
// (tenant, user, provider, environment) scope four-tuple.
func scopeKey(tenantID, userID string, p credential.Provider, environment string) string {
	return tenantID + "/" + userID + "/" + string(p) + "/" + environment
}

// Register implements Store.
// spec: §4.3 line 202.
func (m *Memory) Register(_ context.Context, tenantID, userID string, provider credential.Provider, environment, secret string) (Credential, error) {
	if !provider.IsValid() {
		return Credential{}, errors.New("credentialstore: unknown provider " + string(provider))
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.clock()
	tk := scopeKey(tenantID, userID, provider, environment)
	if existingRef, ok := m.refByTriple[tk]; ok {
		// Replace — re-register refreshes the secret + RotatedAt.
		c := m.byRef[refKey(tenantID, existingRef)]
		c.Secret = secret
		c.Status = StatusActive
		c.RotatedAt = now
		c.RevokedAt = time.Time{}
		m.byRef[refKey(tenantID, existingRef)] = c
		return c, nil
	}
	ref := "cred_" + randomHex(8)
	c := Credential{
		Ref:         ref,
		TenantID:    tenantID,
		UserID:      userID,
		Environment: environment,
		Provider:    provider,
		Secret:      secret,
		Status:      StatusActive,
		CreatedAt:   now,
	}
	m.byRef[refKey(tenantID, ref)] = c
	m.refByTriple[tk] = ref
	return c, nil
}

// Get implements Store.
func (m *Memory) Get(_ context.Context, tenantID, ref string) (Credential, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.byRef[refKey(tenantID, ref)]
	if !ok {
		return Credential{}, ErrNotFound
	}
	return c, nil
}

// Lookup implements Store. It resolves the (tenant, user, provider,
// environment) four-tuple through the scope index.
// spec: §4.9 lines 1347-1351.
func (m *Memory) Lookup(_ context.Context, tenantID, userID string, provider credential.Provider, environment string) (Credential, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ref, ok := m.refByTriple[scopeKey(tenantID, userID, provider, environment)]
	if !ok {
		return Credential{}, ErrNotFound
	}
	c, ok := m.byRef[refKey(tenantID, ref)]
	if !ok {
		return Credential{}, ErrNotFound
	}
	return c, nil
}

// List implements Store.
func (m *Memory) List(_ context.Context, tenantID, userID string) ([]Credential, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Credential, 0)
	for _, c := range m.byRef {
		if c.TenantID == tenantID && c.UserID == userID {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out, nil
}

// RevokedCredentials implements Store. It scans every tenant's stored
// credentials and returns each revoked one in a deterministic order.
//
// spec: §4.9 — the startup rebuild's user-credential term.
func (m *Memory) RevokedCredentials(_ context.Context) ([]RevokedUserCredential, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []RevokedUserCredential
	for _, c := range m.byRef {
		if c.Status == StatusRevoked {
			out = append(out, RevokedUserCredential{TenantID: c.TenantID, CredentialRef: c.Ref})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TenantID != out[j].TenantID {
			return out[i].TenantID < out[j].TenantID
		}
		return out[i].CredentialRef < out[j].CredentialRef
	})
	return out, nil
}

// Rotate implements Store.
func (m *Memory) Rotate(_ context.Context, tenantID, ref, newSecret string) (Credential, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := refKey(tenantID, ref)
	c, ok := m.byRef[k]
	if !ok {
		return Credential{}, ErrNotFound
	}
	c.Secret = newSecret
	c.RotatedAt = m.clock()
	c.Status = StatusActive
	c.RevokedAt = time.Time{}
	m.byRef[k] = c
	return c, nil
}

// Revoke implements Store.
func (m *Memory) Revoke(_ context.Context, tenantID, ref string) (Credential, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := refKey(tenantID, ref)
	c, ok := m.byRef[k]
	if !ok {
		return Credential{}, ErrNotFound
	}
	c.Status = StatusRevoked
	c.RevokedAt = m.clock()
	m.byRef[k] = c
	return c, nil
}

// MarkUsed implements Store.
// spec: §4.9 line 1349, 1365.
func (m *Memory) MarkUsed(_ context.Context, tenantID, ref string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := refKey(tenantID, ref)
	c, ok := m.byRef[k]
	if !ok {
		return ErrNotFound
	}
	c.LastUsedAt = at.UTC()
	m.byRef[k] = c
	return nil
}

// Delete implements Store.
func (m *Memory) Delete(_ context.Context, tenantID, ref string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := refKey(tenantID, ref)
	c, ok := m.byRef[k]
	if !ok {
		return ErrNotFound
	}
	delete(m.byRef, k)
	delete(m.refByTriple, scopeKey(tenantID, c.UserID, c.Provider, c.Environment))
	return nil
}

// DeleteByUser implements the §12.1 mandatory-erasure primitive.
// Removes every credential row owned by (tenantID, userID) — the §4.9
// user-credential registry is user-keyed, so DeleteByUser is the
// load-bearing path for GDPR erasure of this store. Returns the
// number of rows removed; idempotent (a second call returns 0, nil).
//
// spec: §12.1 line 5.
func (m *Memory) DeleteByUser(_ context.Context, tenantID, userID string) (int, error) {
	if tenantID == "" || userID == "" {
		return 0, errors.New("credentialstore: DeleteByUser requires non-empty tenant_id and user_id")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	deleted := 0
	for k, c := range m.byRef {
		if c.TenantID == tenantID && c.UserID == userID {
			delete(m.byRef, k)
			delete(m.refByTriple, scopeKey(tenantID, c.UserID, c.Provider, c.Environment))
			deleted++
		}
	}
	return deleted, nil
}

// DeleteByTenant implements the §12.1 mandatory-erasure primitive.
// Hard-deletes every credential row belonging to tenantID.
//
// spec: §12.1 line 5, §12.8 Phase 4.
func (m *Memory) DeleteByTenant(_ context.Context, tenantID string) (int, error) {
	if tenantID == "" {
		return 0, errors.New("credentialstore: DeleteByTenant requires a concrete tenant_id")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	deleted := 0
	for k, c := range m.byRef {
		if c.TenantID == tenantID {
			delete(m.byRef, k)
			delete(m.refByTriple, scopeKey(tenantID, c.UserID, c.Provider, c.Environment))
			deleted++
		}
	}
	return deleted, nil
}

// spec: §12.1 line 5 — compile-time satisfaction of the mandatory
// erasure-bearing Store interface.
var _ Store = (*Memory)(nil)

func randomHex(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
