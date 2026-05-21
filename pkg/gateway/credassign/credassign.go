// SPDX-License-Identifier: MIT

// Package credassign is the §4.9 credential-assignment service. At
// session start it selects a credential from a pool, mints a
// CredentialLease, records the lease in the gateway's credential-lease
// store, and caches the real upstream credential in the gateway's
// credential cache so the §4.9 LLM reverse proxy can resolve and inject
// it. Releasing a lease frees the credential's session slot.
package credassign

import (
	"errors"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credcache"
	"github.com/lennylabs/lenny/pkg/gateway/credleasestore"
)

// ErrPoolNotFound reports that no credential pool is registered under
// the requested name.
var ErrPoolNotFound = errors.New("credassign: no such credential pool")

// PoolCredential is one upstream credential in a §4.9 credential pool.
type PoolCredential struct {
	// ID identifies the credential within its pool.
	ID string
	// APIKey is the real upstream provider credential. It is held only
	// in memory and never leaves the gateway process.
	APIKey string
	// Healthy reports whether the credential is currently assignable.
	// An unhealthy credential (rate-limited, auth-failed, revoked) is
	// skipped by the assignment strategy.
	Healthy bool
}

// Pool is a §4.9 credential pool: a set of interchangeable upstream
// credentials plus the policy for leasing them.
type Pool struct {
	// Name is the pool identifier.
	Name string
	// Provider is the §4.9 upstream credential provider.
	Provider credential.Provider
	// DeliveryMode selects proxy or direct credential delivery.
	DeliveryMode credential.DeliveryMode
	// Strategy is the §4.9 credential assignment strategy.
	Strategy credential.AssignmentStrategy
	// ProxyURL and ProxyDialect configure proxy-mode delivery; they are
	// required when DeliveryMode is DeliveryProxy.
	ProxyURL     string
	ProxyDialect string
	// LeaseTTLSeconds overrides the §4.9 default lease TTL; 0 keeps it.
	LeaseTTLSeconds int
	// RenewBeforeSeconds overrides the §4.9 renew-before buffer; 0
	// keeps the default.
	RenewBeforeSeconds int
	// Credentials are the pool's upstream credentials.
	Credentials []PoolCredential
}

// poolState is a registered pool plus the mutable assignment state the
// service threads across Assign calls.
type poolState struct {
	pool      Pool
	selection credential.SelectionState
	// active counts in-use leases per credential ID, feeding the
	// least-loaded strategy.
	active map[string]int
}

// Service is the §4.9 credential-assignment service. It is
// goroutine-safe.
type Service struct {
	leases credleasestore.LeaseStore
	creds  *credcache.Cache

	mu       sync.Mutex
	pools    map[string]*poolState
	now      func() time.Time
	observer func(LeaseAssignment)
}

// LeaseAssignment reports a §4.9 credential lease the service minted. It
// is delivered to the observer registered with OnAssigned so a caller —
// the §4.9 Proactive Lease Renewal worker — can track the lease for
// renewal without depending on the lease internals. PoolName names the
// §4.9 pool the lease was leased from, so the renewal worker can re-mint
// a replacement from the same pool.
type LeaseAssignment struct {
	// PoolName is the §4.9 credential pool the lease was minted from.
	PoolName string
	// Lease is the minted credential lease.
	Lease credential.Lease
}

// New returns a credential-assignment service that records leases in
// leases and upstream credentials in creds. leases is the
// credleasestore.LeaseStore the §4.9 LLM reverse proxy resolves a lease
// token against; passing the proxy's store lets a lease the service
// mints resolve on the proxy hot path.
func New(leases credleasestore.LeaseStore, creds *credcache.Cache) *Service {
	return &Service{
		leases: leases,
		creds:  creds,
		pools:  make(map[string]*poolState),
		now:    time.Now,
	}
}

// OnAssigned registers an observer invoked with every §4.9 credential
// lease the service mints, after the lease is recorded in the lease
// store and its upstream credential cached. The §4.9 Proactive Lease
// Renewal worker registers here so each assigned lease is tracked for
// renewal. A nil observer clears any prior registration. OnAssigned is
// goroutine-safe; it must be called before the service mints leases
// concurrently. The observer runs on the Assign caller's goroutine, so
// a slow observer should hand off its own work.
func (s *Service) OnAssigned(fn func(LeaseAssignment)) {
	s.mu.Lock()
	s.observer = fn
	s.mu.Unlock()
}

// RegisterPool adds or replaces a credential pool. Replacing a pool
// resets its assignment state.
func (s *Service) RegisterPool(p Pool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pools[p.Name] = &poolState{pool: p, active: make(map[string]int)}
}

// Assign leases a credential from the named pool to a session. It
// selects a credential per the pool's §4.9 assignment strategy, mints
// the lease, records it in the credential-lease store, and caches the
// real upstream credential so the LLM proxy can inject it. spiffeURI is
// the issuing pod's SPIFFE identity for proxy-mode SPIFFE-binding; an
// empty value disables binding.
//
// Assign returns ErrPoolNotFound for an unknown pool and
// credential.ErrPoolExhausted when the pool has no assignable
// credential.
func (s *Service) Assign(poolName, sessionID, spiffeURI string) (credential.Lease, error) {
	lease, observer, err := s.assignLocked(poolName, sessionID, spiffeURI)
	if err != nil {
		return credential.Lease{}, err
	}
	// The renewal observer runs outside s.mu: the §4.9 Proactive Lease
	// Renewal worker tracks the lease here, and holding the lock across
	// a caller-supplied callback would serialize unrelated Assign calls.
	if observer != nil {
		observer(LeaseAssignment{PoolName: poolName, Lease: lease})
	}
	return lease, nil
}

// assignLocked performs the §4.9 credential selection and lease mint
// under s.mu and returns the minted lease together with the registered
// renewal observer, which the caller invokes after releasing the lock.
func (s *Service) assignLocked(poolName, sessionID, spiffeURI string) (credential.Lease, func(LeaseAssignment), error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	ps, ok := s.pools[poolName]
	if !ok {
		return credential.Lease{}, nil, ErrPoolNotFound
	}

	candidates := make([]credential.CredentialCandidate, len(ps.pool.Credentials))
	for i, c := range ps.pool.Credentials {
		candidates[i] = credential.CredentialCandidate{
			CredentialID:   c.ID,
			ActiveSessions: ps.active[c.ID],
			Healthy:        c.Healthy,
		}
	}

	selected, nextState, err := credential.SelectCredential(ps.pool.Strategy, candidates, ps.selection)
	if err != nil {
		return credential.Lease{}, nil, err
	}

	lease, err := credential.MintLease(credential.MintRequest{
		SessionID:          sessionID,
		Provider:           ps.pool.Provider,
		Source:             credential.SourcePool,
		PoolID:             poolName,
		CredentialID:       selected.CredentialID,
		DeliveryMode:       ps.pool.DeliveryMode,
		PoolTTLSeconds:     ps.pool.LeaseTTLSeconds,
		RenewBeforeSeconds: ps.pool.RenewBeforeSeconds,
		FallbackAllowed:    true,
		Now:                s.now(),
		SpiffeURI:          spiffeURI,
		ProxyURL:           ps.pool.ProxyURL,
		ProxyDialect:       ps.pool.ProxyDialect,
	})
	if err != nil {
		return credential.Lease{}, nil, err
	}

	if err := s.leases.Put(lease); err != nil {
		return credential.Lease{}, nil, err
	}
	s.creds.Put(lease.CredentialKey(), credentialSecret(ps.pool, selected.CredentialID))

	ps.selection = nextState
	ps.active[selected.CredentialID]++
	return lease, s.observer, nil
}

// Release frees the credential session slot a lease held and removes
// the lease from the credential-lease store. The cached upstream
// credential is left in place because other leases on the same pool
// credential continue to need it. Release is a no-op for an unknown
// lease ID.
func (s *Service) Release(leaseID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lease, ok := s.leases.GetByID(leaseID)
	if !ok {
		return
	}
	if ps, ok := s.pools[lease.PoolID]; ok && ps.active[lease.CredentialID] > 0 {
		ps.active[lease.CredentialID]--
	}
	s.leases.Remove(leaseID)
}

// credentialSecret returns the real upstream secret for a pool
// credential, or "" when the credential is no longer in the pool.
func credentialSecret(p Pool, credentialID string) string {
	for _, c := range p.Credentials {
		if c.ID == credentialID {
			return c.APIKey
		}
	}
	return ""
}

// UpstreamCredential returns the materialized upstream provider secret
// the service cached for a lease's backing credential. It satisfies the
// §4.3 Token Service gRPC server, which forwards the secret to the
// gateway over mTLS so the gateway's in-memory credential cache
// (`pkg/gateway/credcache`) can serve the §4.9 LLM reverse proxy. ok
// reports whether the secret is currently cached.
func (s *Service) UpstreamCredential(lease credential.Lease) (string, bool) {
	return s.creds.UpstreamCredential(lease)
}
