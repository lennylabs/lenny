// SPDX-License-Identifier: MIT

// Package credleasestore holds the §4.9 credential leases a gateway
// replica has issued. The §4.9 LLM reverse proxy resolves an agent
// pod's bearer lease token to a lease through this store on every
// upstream request; the lease lifecycle records a lease here when it is
// assigned and removes it when the session ends or the lease is
// revoked.
//
// The store is in-memory and per-replica. §4.9 places the durable lease
// record in the Postgres TokenStore; this store is the gateway-process
// working set the proxy hot path reads. A coordinator handoff
// re-establishes a session's lease on the new coordinating replica.
package credleasestore

import (
	"sync"

	"github.com/lennylabs/lenny/pkg/credential"
)

// LeaseStore is the §4.9 gateway-replica credential-lease store
// contract. The in-memory Store and the Postgres-backed
// credleasestore/pgstore both satisfy it, so a deployment can choose a
// per-replica working set or a durable backend without the §4.9 LLM
// proxy or the lease lifecycle observing a difference.
type LeaseStore interface {
	Put(lease credential.Lease) error
	GetByToken(token string) (lease credential.Lease, ok bool)
	GetByID(leaseID string) (lease credential.Lease, ok bool)
	Remove(leaseID string)
	Len() int
	// LeasesBySession returns every lease the store holds whose SessionID
	// is one of sessionIDs. It backs the §7.1 step 23 session-teardown
	// lease release and the §11.4 full_revoke revocation step.
	LeasesBySession(sessionIDs []string) []credential.Lease
}

// Store indexes issued credential leases by lease ID and, for
// proxy-mode leases, by the opaque lease token the agent pod presents.
// Every method is goroutine-safe.
type Store struct {
	mu      sync.RWMutex
	byID    map[string]credential.Lease
	byToken map[string]string // lease token -> lease ID
}

var _ LeaseStore = (*Store)(nil)

// New returns an empty Store.
func New() *Store {
	return &Store{
		byID:    make(map[string]credential.Lease),
		byToken: make(map[string]string),
	}
}

// Put records a lease, replacing any prior lease with the same lease
// ID. The lease is validated first; an invalid lease is rejected and
// not stored. A proxy-mode lease is additionally indexed by its lease
// token so the LLM proxy can resolve a request to it.
func (s *Store) Put(lease credential.Lease) error {
	if err := lease.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Drop the prior token index for this lease ID so a re-issued lease
	// with a fresh token does not leave a dangling entry.
	if prior, ok := s.byID[lease.LeaseID]; ok && prior.Proxy != nil {
		delete(s.byToken, prior.Proxy.LeaseToken)
	}
	s.byID[lease.LeaseID] = lease
	if lease.DeliveryMode == credential.DeliveryProxy && lease.Proxy != nil {
		s.byToken[lease.Proxy.LeaseToken] = lease.LeaseID
	}
	return nil
}

// GetByToken resolves a proxy-mode lease by the bearer lease token the
// agent pod presents. ok is false when no lease holds the token.
func (s *Store) GetByToken(token string) (lease credential.Lease, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.byToken[token]
	if !ok {
		return credential.Lease{}, false
	}
	lease, ok = s.byID[id]
	return lease, ok
}

// GetByID resolves a lease by its lease ID. ok is false when this
// replica holds no lease with the ID.
func (s *Store) GetByID(leaseID string) (lease credential.Lease, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	lease, ok = s.byID[leaseID]
	return lease, ok
}

// Remove drops the lease with the given ID and its token index. It is
// a no-op when this replica holds no such lease.
func (s *Store) Remove(leaseID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	lease, ok := s.byID[leaseID]
	if !ok {
		return
	}
	if lease.Proxy != nil {
		delete(s.byToken, lease.Proxy.LeaseToken)
	}
	delete(s.byID, leaseID)
}

// Len reports how many leases this replica holds.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.byID)
}

// LeasesBySession returns every lease this replica holds whose
// SessionID is one of sessionIDs. It backs the §11.4 full_revoke
// credential-lease revocation step: the caller resolves a revoked
// user's sessions, collects their leases here, removes them, and adds
// each lease's credential to the §4.9 deny list. The returned slice is
// a copy, so the caller may iterate it while removing the leases. An
// empty sessionIDs set yields no leases.
func (s *Store) LeasesBySession(sessionIDs []string) []credential.Lease {
	if len(sessionIDs) == 0 {
		return nil
	}
	want := make(map[string]struct{}, len(sessionIDs))
	for _, id := range sessionIDs {
		want[id] = struct{}{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []credential.Lease
	for _, lease := range s.byID {
		if _, ok := want[lease.SessionID]; ok {
			out = append(out, lease)
		}
	}
	return out
}

// LeasesByCredential returns every lease this replica holds whose
// backing credential identity equals key. It backs the §4.9 emergency
// credential revocation: the admin handler resolves the leases against
// a revoked pool credential here, adds the credential to the deny list,
// and removes the leases, reporting the count as `leasesTerminated`.
// The returned slice is a copy, so the caller may iterate it while
// removing the leases. The match is source-aware: a pool key never
// aliases a user key (§4.9).
//
// spec: §4.9 lines 1640-1652 — look up all active leases backed by the
// credential and terminate them.
func (s *Store) LeasesByCredential(key credential.CredentialKey) []credential.Lease {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []credential.Lease
	for _, lease := range s.byID {
		if lease.CredentialKey() == key {
			out = append(out, lease)
		}
	}
	return out
}
