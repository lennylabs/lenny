// SPDX-License-Identifier: MIT

package credential

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"time"
)

// DefaultLeaseTTLSeconds is the §4.9 synthetic lease TTL applied when a
// pool does not override it. Every built-in provider defaults to one
// hour.
const DefaultLeaseTTLSeconds = 3600

// DefaultRenewBeforeSeconds is the §4.9 default renewBeforeBuffer: how
// far ahead of expiry the proactive-renewal loop refreshes a lease.
const DefaultRenewBeforeSeconds = 300

// ProviderMaxTTLSeconds returns the upstream provider's hard ceiling on
// lease TTL, or 0 when the provider imposes none. §4.9 caps the
// resolved TTL at this ceiling so a lease never outlives the credential
// the provider can issue.
func ProviderMaxTTLSeconds(p Provider) int {
	switch p {
	case ProviderAWSBedrock:
		return 43200 // STS AssumeRole maximum: 12 hours.
	case ProviderAzureOpenAI:
		return 86400 // Azure AD token maximum: 24 hours.
	case ProviderVertexAI:
		return 3600 // GCP access-token maximum: 1 hour.
	case ProviderGitHub:
		return 3600 // GitHub installation-token maximum: 1 hour.
	default:
		// anthropic_direct enforces a synthetic TTL only, and
		// vault_transit is governed by Vault policy; neither carries a
		// Lenny-enforced ceiling.
		return 0
	}
}

// ResolveLeaseTTL returns the §4.9 lease TTL for a pool: the pool's
// leaseTTLSeconds override when set, otherwise DefaultLeaseTTLSeconds,
// capped at the provider's ceiling.
func ResolveLeaseTTL(poolTTLSeconds int, p Provider) time.Duration {
	ttl := poolTTLSeconds
	if ttl <= 0 {
		ttl = DefaultLeaseTTLSeconds
	}
	if max := ProviderMaxTTLSeconds(p); max > 0 && ttl > max {
		ttl = max
	}
	return time.Duration(ttl) * time.Second
}

// MintRequest carries the inputs for minting one §4.9 CredentialLease.
// The Source field selects which credential-identity fields are used:
// a pool-backed lease reads PoolID and CredentialID, a user-backed
// lease reads TenantID and CredentialRef.
type MintRequest struct {
	// SessionID is the session the lease is issued to.
	SessionID string
	// Provider is the §4.9 upstream credential provider.
	Provider Provider
	// Source discriminates a pool-backed from a user-backed lease.
	Source LeaseSource
	// PoolID and CredentialID identify a pool-backed credential.
	PoolID       string
	CredentialID string
	// TenantID and CredentialRef identify a user-backed credential.
	TenantID      string
	CredentialRef string
	// DeliveryMode selects proxy or direct credential delivery.
	DeliveryMode DeliveryMode
	// PoolTTLSeconds is the pool's leaseTTLSeconds override; 0 selects
	// the provider default.
	PoolTTLSeconds int
	// RenewBeforeSeconds is the pool's renewBeforeBufferSeconds; 0
	// selects DefaultRenewBeforeSeconds.
	RenewBeforeSeconds int
	// FallbackAllowed records whether the §4.9 fallback flow may rotate
	// this lease.
	FallbackAllowed bool
	// Now is the lease issue time; the zero value selects time.Now.
	Now time.Time
	// SpiffeURI is the issuing pod's SPIFFE identity for proxy-mode
	// SPIFFE-binding. Empty disables binding.
	SpiffeURI string
	// ProxyURL and ProxyDialect are the proxy endpoint config for a
	// proxy-mode lease. The minter generates the lease token itself.
	ProxyURL     string
	ProxyDialect string
	// UpstreamModel is the optional proxy-mode upstream model hint.
	UpstreamModel string
}

// MintLease mints a §4.9 CredentialLease from req: it resolves the
// synthetic TTL, computes expiresAt and renewBefore, generates the
// opaque lease ID, and — for a proxy-mode lease — generates the opaque
// bearer lease token and assembles the proxy materializedConfig. The
// minted lease is validated before return, so a structurally invalid
// request yields a *LeaseError.
func MintLease(req MintRequest) (Lease, error) {
	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}
	renewBefore := req.RenewBeforeSeconds
	if renewBefore <= 0 {
		renewBefore = DefaultRenewBeforeSeconds
	}
	expiresAt := now.Add(ResolveLeaseTTL(req.PoolTTLSeconds, req.Provider))

	leaseID, err := randomString("cl", 16)
	if err != nil {
		return Lease{}, err
	}
	lease := Lease{
		LeaseID:         leaseID,
		SessionID:       req.SessionID,
		Provider:        req.Provider,
		Source:          req.Source,
		PoolID:          req.PoolID,
		CredentialID:    req.CredentialID,
		TenantID:        req.TenantID,
		CredentialRef:   req.CredentialRef,
		DeliveryMode:    req.DeliveryMode,
		IssuedAt:        now,
		ExpiresAt:       expiresAt,
		RenewBefore:     expiresAt.Add(-time.Duration(renewBefore) * time.Second),
		FallbackAllowed: req.FallbackAllowed,
		SpiffeURI:       req.SpiffeURI,
	}
	if req.DeliveryMode == DeliveryProxy {
		token, err := randomString("lt", 32)
		if err != nil {
			return Lease{}, err
		}
		lease.Proxy = &ProxyConfig{
			ProxyURL:      req.ProxyURL,
			ProxyDialect:  req.ProxyDialect,
			LeaseToken:    token,
			UpstreamModel: req.UpstreamModel,
		}
	}
	if err := lease.Validate(); err != nil {
		return Lease{}, err
	}
	return lease, nil
}

// randomString returns prefix joined to nBytes of cryptographically
// random data, base64url-encoded. It backs the unguessable lease token
// and the lease identifier.
func randomString(prefix string, nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("credential: generate random %s: %w", prefix, err)
	}
	return prefix + "-" + base64.RawURLEncoding.EncodeToString(b), nil
}
