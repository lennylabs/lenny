// SPDX-License-Identifier: MIT

// Package usercreds is the §4.9 Pre-Authorized Credential Flow's
// user-source delivery path. It resolves a user-registered credential
// (the §4.9 `/v1/credentials` registry) into a proxy-mode
// CredentialLease at session creation and serves the §4.9 LLM reverse
// proxy from it, exactly as a pool lease does.
//
// User credentials are delivered in proxy mode: the user's secret stays
// gateway-side in the credential cache and only an opaque lease token
// reaches the agent pod, mirroring the §4.9 design point that a runtime
// "never receives the pool's root secret or long-lived key". This keeps
// the user's key off the pod and makes rotation and revocation
// gateway-local — a rotation re-caches the new secret under the same
// lease key (running sessions pick it up on their next upstream request,
// the lease token unchanged) and a revocation deny-lists the lease key
// (cross-replica, via the credential-lease revocation propagator) and
// drops the cached secret. Neither path needs a per-pod RotateCredentials
// RPC, because the pod never held the secret.
//
// Delivery is scoped to the providers whose native API maps to a single
// built-in proxy dialect (credential.UserProxyDialect): anthropic_direct,
// azure_openai, vertex_ai, cursor_direct. A user credential for a
// provider outside that set (multi-dialect aws_bedrock, or the non-LLM
// github / vault_transit) is not deliverable through the LLM proxy in v1
// and is reported unavailable, so the §4.9 router falls through to pool
// per the fallback configuration.
//
// spec: §4.9 lines 1340-1381 (Pre-Authorized Credential Flow), 1246-1262
// (proxy-mode delivery), 1640-1652 (deny list).
package usercreds

import (
	"context"
	"errors"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialstore"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// LeaseStore is the slice of the §4.9 credential-lease store the
// user-source materializer needs: record a minted lease, enumerate the
// leases backed by a credential, and drop a lease by id. The materializer
// shares the store the §4.9 credassign service and the LLM proxy use, so
// a user lease it mints resolves on the proxy hot path and is released by
// the session-teardown path alongside the session's pool leases. Both the
// in-memory credleasestore.Store and the Postgres-backed pgstore.Store
// satisfy it.
type LeaseStore interface {
	Put(lease credential.Lease) error
	LeasesByCredential(key credential.CredentialKey) []credential.Lease
	Remove(leaseID string)
}

// CredCache is the slice of the §4.9 upstream-credential cache the
// materializer writes. The LLM proxy reads the same cache to resolve a
// lease's upstream secret. *credcache.Cache satisfies it.
type CredCache interface {
	Put(key credential.CredentialKey, apiKey string)
	Remove(key credential.CredentialKey)
}

// Revoker propagates a user-credential revocation onto every replica's
// §4.9 deny list. *credrenewalprop.Propagator satisfies it: it revokes
// locally and publishes the key on the cross-replica pub/sub channel so a
// peer that still holds a lease for the credential rejects it on the next
// upstream request. A nil Revoker degrades RevokeUser to a local-replica
// drop (single-replica or development deployments).
//
// spec: §4.9 lines 1640-1652.
type Revoker interface {
	Revoke(key credential.CredentialKey)
}

// Materializer is the §4.9 user-source credential delivery service. It is
// goroutine-safe; the underlying store, lease store, and cache are each
// goroutine-safe.
type Materializer struct {
	store    credentialstore.Store
	leases   LeaseStore
	creds    CredCache
	proxyURL string
	ttl      int
	now      func() time.Time
	revoker  Revoker
}

// Config configures a Materializer.
type Config struct {
	// Store is the §4.9 user-credential registry.
	Store credentialstore.Store
	// Leases is the shared §4.9 credential-lease store.
	Leases LeaseStore
	// Creds is the shared §4.9 upstream-credential cache.
	Creds CredCache
	// ProxyURL is the public HTTPS URL of the §4.9 LLM reverse proxy the
	// agent pod dials. When empty the materializer reports every provider
	// unavailable: a user credential cannot be delivered without the proxy
	// endpoint, so the §4.9 router falls through to pool.
	ProxyURL string
	// LeaseTTLSeconds overrides the §4.9 user-source lease TTL; 0 selects
	// the provider default (credential.DefaultLeaseTTLSeconds).
	LeaseTTLSeconds int
	// Now overrides the clock; nil selects time.Now.
	Now func() time.Time
}

// New returns a user-source credential materializer.
func New(cfg Config) *Materializer {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Materializer{
		store:    cfg.Store,
		leases:   cfg.Leases,
		creds:    cfg.Creds,
		proxyURL: cfg.ProxyURL,
		ttl:      cfg.LeaseTTLSeconds,
		now:      now,
	}
}

// SetRevoker wires the cross-replica deny-list propagator. It is set once
// during gateway start-up, before the materializer serves requests.
func (m *Materializer) SetRevoker(r Revoker) { m.revoker = r }

// Available reports whether a usable user-scoped credential exists for
// (tenant, user, provider) and can be delivered through the LLM proxy. It
// is the §4.9 router's userCredChecker. It reports false when the LLM
// proxy is not configured, the provider has no canonical proxy dialect,
// no credential is registered for the four-tuple, or the registered
// credential is revoked (treated as not-found per §4.9 line 1379). The
// runtime-dialect compatibility check (§4.9 line 1476) is applied
// downstream at the session's pre-claim provider intersection.
//
// spec: §4.9 lines 1347-1351, 1368-1372, 1379.
func (m *Materializer) Available(ctx context.Context, tenantID, userID, provider string) bool {
	if m.proxyURL == "" {
		return false
	}
	p := credential.Provider(provider)
	if _, ok := credential.UserProxyDialect(p); !ok {
		return false
	}
	cred, err := m.store.Lookup(ctx, tenantID, userID, p, "")
	if err != nil {
		return false
	}
	return cred.Status == credentialstore.StatusActive
}

// ErrProxyUnavailable reports that the materializer cannot deliver a user
// credential because the LLM proxy URL is unconfigured. It is an internal
// guard: Available gates user-source resolution on the same condition, so
// the binder should never reach a MintProto with no proxy URL.
var ErrProxyUnavailable = errors.New("usercreds: LLM proxy URL is not configured")

// MintProto resolves the user credential for (tenant, user, provider)
// into a proxy-mode CredentialLease, records it in the lease store,
// caches the upstream secret for the §4.9 LLM proxy, marks the credential
// used, and returns the adapter wire form the §4.7 binder pushes to the
// pod. It is the user-source analogue of credassign.AssignProto.
//
// The minted lease is SPIFFE-bound to the issuing pod (spiffeURI) for the
// §4.9 proxy-mode binding check, exactly as a pool proxy lease is.
//
// spec: §4.9 lines 1347-1351 (resolution), 1246-1262 (proxy delivery).
func (m *Materializer) MintProto(ctx context.Context, tenantID, userID, sessionID, spiffeURI, provider string) (*adapterv1.CredentialLease, error) {
	if m.proxyURL == "" {
		return nil, ErrProxyUnavailable
	}
	p := credential.Provider(provider)
	dialect, ok := credential.UserProxyDialect(p)
	if !ok {
		return nil, errors.New("usercreds: provider " + provider + " has no canonical proxy dialect")
	}
	cred, err := m.store.Lookup(ctx, tenantID, userID, p, "")
	if err != nil {
		return nil, err
	}
	if cred.Status != credentialstore.StatusActive {
		// A revoked credential is treated as not-found per §4.9 line 1379;
		// it must not produce a deliverable lease.
		return nil, credentialstore.ErrNotFound
	}
	lease, err := credential.MintLease(credential.MintRequest{
		SessionID:       sessionID,
		Provider:        p,
		Source:          credential.SourceUser,
		TenantID:        tenantID,
		CredentialRef:   cred.Ref,
		DeliveryMode:    credential.DeliveryProxy,
		PoolTTLSeconds:  m.ttl,
		FallbackAllowed: true,
		Now:             m.now(),
		SpiffeURI:       spiffeURI,
		ProxyURL:        m.proxyURL,
		ProxyDialect:    string(dialect),
	})
	if err != nil {
		return nil, err
	}
	if err := m.leases.Put(lease); err != nil {
		return nil, err
	}
	// The upstream secret stays gateway-side: the proxy resolves it from
	// the cache by the lease's source-aware key. The agent pod receives
	// only the proxy materializedConfig (proxyUrl, proxyDialect,
	// leaseToken) in the wire lease below.
	m.creds.Put(lease.CredentialKey(), cred.Secret)
	// spec: §4.9 lines 1349, 1365 — last_used_at is updated on each
	// successful resolution.
	_ = m.store.MarkUsed(ctx, tenantID, cred.Ref, m.now())
	return credassign.ProtoLease(lease)
}

// RotateUser re-caches the credential's current secret for every active
// lease backed by (tenant, credentialRef) and returns the count rotated.
// The handler rotates the registry secret first; RotateUser then refreshes
// the gateway-side cache so the §4.9 LLM proxy injects the new material on
// the next upstream request for each running session. The lease token is
// unchanged, so running sessions are not interrupted — they pick up the
// new material immediately (the proxy-mode equivalent of the §4.9 "rotated
// via RotateCredentials RPC ... within one rotation cycle" guarantee).
//
// spec: §4.9 line 1350 (PUT rotates active leases), 1423
// (user_credential_rotated trigger).
func (m *Materializer) RotateUser(ctx context.Context, tenantID, credentialRef string) (int, error) {
	cred, err := m.store.Get(ctx, tenantID, credentialRef)
	if err != nil {
		return 0, err
	}
	key := credential.CredentialKey{Source: credential.SourceUser, TenantID: tenantID, CredentialRef: credentialRef}
	leases := m.leases.LeasesByCredential(key)
	if len(leases) == 0 {
		return 0, nil
	}
	m.creds.Put(key, cred.Secret)
	return len(leases), nil
}

// RevokeUser invalidates every active lease backed by (tenant,
// credentialRef) and returns the count terminated. It adds the user-shaped
// deny-list entry {source: user, tenantId, credentialRef} (propagated
// across replicas when a Revoker is wired), drops the cached upstream
// secret so the proxy can no longer inject it, and removes the leases this
// replica holds. A peer replica that still holds a lease rejects it via
// the propagated deny-list entry on its next upstream request.
//
// spec: §4.9 lines 1350-1351 (revoke invalidates active leases), 1640-1652
// (user-shaped deny-list entry), 1424 (user_credential_revoked trigger).
func (m *Materializer) RevokeUser(_ context.Context, tenantID, credentialRef string) (int, error) {
	key := credential.CredentialKey{Source: credential.SourceUser, TenantID: tenantID, CredentialRef: credentialRef}
	if m.revoker != nil {
		m.revoker.Revoke(key)
	}
	leases := m.leases.LeasesByCredential(key)
	for _, lease := range leases {
		m.leases.Remove(lease.LeaseID)
	}
	m.creds.Remove(key)
	return len(leases), nil
}
