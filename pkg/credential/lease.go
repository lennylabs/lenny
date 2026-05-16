// SPDX-License-Identifier: MIT

package credential

import (
	"fmt"
	"time"
)

// DeliveryMode is the §4.9 credential delivery mode for a pool and the
// leases it issues.
type DeliveryMode string

const (
	// DeliveryProxy keeps the upstream credential inside the gateway:
	// the runtime receives a lease token and reaches the provider only
	// through the §4.9 LLM reverse proxy.
	DeliveryProxy DeliveryMode = "proxy"
	// DeliveryDirect materializes the upstream credential into the
	// agent pod's credential file.
	DeliveryDirect DeliveryMode = "direct"
)

// IsValid reports whether m is one of the two §4.9 delivery modes.
func (m DeliveryMode) IsValid() bool {
	return m == DeliveryProxy || m == DeliveryDirect
}

// ProxyConfig is the §4.9 proxy-mode materializedConfig: the uniform,
// provider-agnostic configuration a proxy-mode lease carries instead of
// a real upstream credential. The runtime points its SDK at ProxyURL
// and authenticates with LeaseToken; the gateway translates and injects
// the real upstream credential in-process.
type ProxyConfig struct {
	// ProxyURL is the HTTPS endpoint of the §4.9 LLM reverse proxy.
	ProxyURL string
	// ProxyDialect is the wire format the runtime speaks to the proxy:
	// "openai" or "anthropic".
	ProxyDialect string
	// LeaseToken is the opaque bearer token authenticating the runtime
	// to ProxyURL. The runtime supplies it as its SDK API key.
	LeaseToken string
	// UpstreamModel is an optional hint for the upstream model ID; the
	// runtime may override it per request.
	UpstreamModel string
}

// Lease is a §4.9 CredentialLease: the credential grant the gateway
// issues to a session. The Source field discriminates a tagged union —
// a pool-backed lease (SourcePool) carries PoolID and CredentialID, a
// user-backed lease (SourceUser) carries TenantID and CredentialRef.
type Lease struct {
	// LeaseID is the lease's opaque identifier.
	LeaseID string
	// SessionID is the session the lease was issued to.
	SessionID string
	// Provider is the §4.9 upstream credential provider.
	Provider Provider
	// Source discriminates a pool-backed from a user-backed lease.
	Source LeaseSource
	// PoolID and CredentialID identify the credential for a pool-backed
	// lease. Empty for a user-backed lease.
	PoolID       string
	CredentialID string
	// TenantID and CredentialRef identify the credential for a
	// user-backed lease. Empty for a pool-backed lease.
	TenantID      string
	CredentialRef string
	// DeliveryMode is the lease's §4.9 credential delivery mode.
	DeliveryMode DeliveryMode
	// ExpiresAt is the lease expiry. The §4.9 LLM proxy rejects a
	// request whose lease has expired before any upstream call.
	ExpiresAt time.Time
	// RenewBefore is the proactive-renewal trigger time.
	RenewBefore time.Time
	// FallbackAllowed reports whether the §4.9 fallback flow may rotate
	// this lease.
	FallbackAllowed bool
	// Proxy is the proxy-mode materializedConfig. It is non-nil exactly
	// when DeliveryMode is DeliveryProxy.
	Proxy *ProxyConfig
	// SpiffeURI is the issuing pod's SPIFFE identity, recorded at
	// AssignCredentials for the §4.9 proxy-mode SPIFFE-binding check.
	// Empty when SPIFFE-binding is disabled (single-tenant or
	// development deployments only).
	SpiffeURI string
}

// LeaseError captures Lease.Validate failures.
type LeaseError struct {
	LeaseID    string
	Violations []string
}

func (e *LeaseError) Error() string {
	return fmt.Sprintf("credential: lease %q: %v", e.LeaseID, e.Violations)
}

// Validate reports the structural errors in a Lease. It returns nil on
// success and a *LeaseError listing every violation otherwise.
func (l Lease) Validate() error {
	v := []string{}
	if l.LeaseID == "" {
		v = append(v, "leaseId is required")
	}
	if l.SessionID == "" {
		v = append(v, "sessionId is required")
	}
	if l.Provider == "" {
		v = append(v, "provider is required")
	}
	if !l.Source.IsValid() {
		v = append(v, fmt.Sprintf("source %q must be \"pool\" or \"user\"", l.Source))
	}
	switch l.Source {
	case SourcePool:
		if l.PoolID == "" || l.CredentialID == "" {
			v = append(v, "a pool-backed lease requires poolId and credentialId")
		}
	case SourceUser:
		if l.TenantID == "" || l.CredentialRef == "" {
			v = append(v, "a user-backed lease requires tenantId and credentialRef")
		}
	}
	if !l.DeliveryMode.IsValid() {
		v = append(v, fmt.Sprintf("deliveryMode %q must be \"proxy\" or \"direct\"", l.DeliveryMode))
	}
	if l.DeliveryMode == DeliveryProxy {
		switch {
		case l.Proxy == nil:
			v = append(v, "a proxy-mode lease requires a proxy materializedConfig")
		case l.Proxy.ProxyURL == "" || l.Proxy.ProxyDialect == "" || l.Proxy.LeaseToken == "":
			v = append(v, "a proxy materializedConfig requires proxyUrl, proxyDialect, and leaseToken")
		}
	}
	if l.ExpiresAt.IsZero() {
		v = append(v, "expiresAt is required")
	}
	if len(v) == 0 {
		return nil
	}
	return &LeaseError{LeaseID: l.LeaseID, Violations: v}
}

// Expired reports whether the lease's grant has lapsed as of now. The
// §4.9 LLM proxy enforces expiry server-side: a request on an expired
// lease is rejected before any upstream call.
func (l Lease) Expired(now time.Time) bool {
	return !now.Before(l.ExpiresAt)
}

// DenyListKey is the §4.9 source-aware credential deny-list key. The
// pool and user keyspaces never overlap; Source is the discriminator.
type DenyListKey struct {
	// Source discriminates the two keyspaces.
	Source LeaseSource
	// PoolID and CredentialID are set for a pool-backed key.
	PoolID       string
	CredentialID string
	// TenantID and CredentialRef are set for a user-backed key.
	TenantID      string
	CredentialRef string
}

// DenyListKey returns the §4.9 source-aware deny-list key for the
// lease's backing credential. The §4.9 LLM proxy looks this key up
// against the deny list on every request.
func (l Lease) DenyListKey() DenyListKey {
	if l.Source == SourceUser {
		return DenyListKey{Source: SourceUser, TenantID: l.TenantID, CredentialRef: l.CredentialRef}
	}
	return DenyListKey{Source: SourcePool, PoolID: l.PoolID, CredentialID: l.CredentialID}
}

// LeaseRejection names the §4.9 check a proxy request failed. The empty
// value means the request may proceed.
type LeaseRejection string

const (
	// RejectNone reports that every proxy-request check passed.
	RejectNone LeaseRejection = ""
	// RejectExpired reports that the lease expired before the request.
	RejectExpired LeaseRejection = "expired"
	// RejectRevoked reports that the lease's credential is on the §4.9
	// deny list.
	RejectRevoked LeaseRejection = "revoked"
	// RejectSpiffeMismatch reports that the request's mTLS peer SPIFFE
	// identity does not match the identity bound to the lease.
	RejectSpiffeMismatch LeaseRejection = "spiffe_mismatch"
)

// ProxyRequestCheck is the live request context the §4.9 LLM proxy
// checks a resolved lease against on every upstream request.
type ProxyRequestCheck struct {
	// Now is the current time, checked against the lease expiry.
	Now time.Time
	// PeerSPIFFEURI is the SPIFFE URI extracted from the request's
	// mTLS peer certificate.
	PeerSPIFFEURI string
	// Revoked reports whether the lease's credential is on the §4.9
	// deny list. The caller resolves it from the lease's DenyListKey.
	Revoked bool
}

// ValidateProxyRequest applies the §4.9 LLM-proxy checks to a resolved
// lease: expiry, then revocation, then SPIFFE-binding. It returns the
// first failed check, or RejectNone when the request may proceed. A
// lease with an empty SpiffeURI skips the SPIFFE check, which §4.9
// permits only in single-tenant and development deployments.
func (l Lease) ValidateProxyRequest(c ProxyRequestCheck) LeaseRejection {
	if l.Expired(c.Now) {
		return RejectExpired
	}
	if c.Revoked {
		return RejectRevoked
	}
	if l.SpiffeURI != "" && l.SpiffeURI != c.PeerSPIFFEURI {
		return RejectSpiffeMismatch
	}
	return RejectNone
}
