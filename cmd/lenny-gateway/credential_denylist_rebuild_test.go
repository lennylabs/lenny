// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialstore"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/denylist"
)

// stubPoolLister is a revokedPoolLister test double: it returns a fixed
// revoked-pool set or a fixed error, and counts calls so a test can assert the
// backoff loop retried. It is safe to mutate the error from another goroutine
// while the rebuild loop polls it (the recovery test).
type stubPoolLister struct {
	mu      sync.Mutex
	revoked []credentialpoolstore.RevokedCredential
	err     error
	calls   int
}

func (s *stubPoolLister) RevokedCredentials(context.Context) ([]credentialpoolstore.RevokedCredential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.revoked, s.err
}

func (s *stubPoolLister) setErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.err = err
}

func (s *stubPoolLister) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// stubUserLister is a revokedUserLister test double.
type stubUserLister struct {
	mu      sync.Mutex
	revoked []credentialstore.RevokedUserCredential
	err     error
	calls   int
}

func (s *stubUserLister) RevokedCredentials(context.Context) ([]credentialstore.RevokedUserCredential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.revoked, s.err
}

func (s *stubUserLister) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// stubLeaseCounter is a leaseCounter test double keyed by credential key. A key
// present in counts reports that count; a key present in errs reports an error
// (the store could not answer); an absent key reports (0, nil).
type stubLeaseCounter struct {
	counts map[credential.CredentialKey]int
	errs   map[credential.CredentialKey]error
}

func (s *stubLeaseCounter) LeasesByCredentialCount(_ context.Context, key credential.CredentialKey, _ time.Time) (int, error) {
	if s.errs != nil {
		if err, ok := s.errs[key]; ok {
			return 0, err
		}
	}
	return s.counts[key], nil
}

var (
	poolKeyA = credential.CredentialKey{Source: credential.SourcePool, PoolID: "acme-openai", CredentialID: "cred-1"}
	userKeyA = credential.CredentialKey{Source: credential.SourceUser, TenantID: "acme", CredentialRef: "ref-1"}
	userKeyB = credential.CredentialKey{Source: credential.SourceUser, TenantID: "acme", CredentialRef: "ref-2"}
)

func onePool() []credentialpoolstore.RevokedCredential {
	return []credentialpoolstore.RevokedCredential{{TenantID: "acme", PoolName: "acme-openai", CredentialID: "cred-1"}}
}

func oneUser() []credentialstore.RevokedUserCredential {
	return []credentialstore.RevokedUserCredential{{TenantID: "acme", CredentialRef: "ref-1"}}
}

// TestRebuildDenyListSeedsBothTerms pins that the §4.9 startup rebuild seeds
// both the pool and user terms of the two-store union in one authoritative
// Reset, and that a revocation confined to one store seeds only that term.
//
// spec: §4.9 lines 1692-1697 (both terms of the rebuild union seeded in one Reset).
func TestRebuildDenyListSeedsBothTerms(t *testing.T) {
	leases := &stubLeaseCounter{counts: map[credential.CredentialKey]int{poolKeyA: 1, userKeyA: 1}}

	keys, err := rebuildCredentialDenyListKeys(context.Background(),
		&stubPoolLister{revoked: onePool()}, &stubUserLister{revoked: oneUser()}, leases, time.Now())
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	dl := denylist.New()
	dl.Reset(keys)
	if !dl.Revoked(poolKeyA) {
		t.Errorf("pool term not seeded: %+v", poolKeyA)
	}
	if !dl.Revoked(userKeyA) {
		t.Errorf("user term not seeded: %+v", userKeyA)
	}

	// A pool-store-only revocation seeds the pool key and no user key.
	poolOnly, err := rebuildCredentialDenyListKeys(context.Background(),
		&stubPoolLister{revoked: onePool()}, &stubUserLister{}, leases, time.Now())
	if err != nil {
		t.Fatalf("pool-only rebuild: %v", err)
	}
	if len(poolOnly) != 1 || poolOnly[0] != poolKeyA {
		t.Errorf("pool-only rebuild = %+v, want just %+v", poolOnly, poolKeyA)
	}

	// A token-store-only revocation seeds the user key and no pool key.
	userOnly, err := rebuildCredentialDenyListKeys(context.Background(),
		&stubPoolLister{}, &stubUserLister{revoked: oneUser()}, leases, time.Now())
	if err != nil {
		t.Fatalf("user-only rebuild: %v", err)
	}
	if len(userOnly) != 1 || userOnly[0] != userKeyA {
		t.Errorf("user-only rebuild = %+v, want just %+v", userOnly, userKeyA)
	}
}

// TestRebuildDenyListLeaseExistenceFilter pins the §4.9 active-lease bound: the
// rebuild seeds a deny entry only for a revoked credential that still has an
// active lease. A revoked credential whose only lease is expired or absent is
// not seeded, and an all-expired set produces an empty Reset.
//
// spec: §4.9 lines 1694-1695 (rebuild seeds only revoked credentials with an active lease).
func TestRebuildDenyListLeaseExistenceFilter(t *testing.T) {
	// userKeyA has an active lease; userKeyB has none (absent from counts).
	leases := &stubLeaseCounter{counts: map[credential.CredentialKey]int{userKeyA: 1, userKeyB: 0}}
	users := &stubUserLister{revoked: []credentialstore.RevokedUserCredential{
		{TenantID: "acme", CredentialRef: "ref-1"},
		{TenantID: "acme", CredentialRef: "ref-2"},
	}}

	keys, err := rebuildCredentialDenyListKeys(context.Background(), &stubPoolLister{}, users, leases, time.Now())
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	dl := denylist.New()
	dl.Reset(keys)
	if !dl.Revoked(userKeyA) {
		t.Errorf("credential with an active lease was not seeded: %+v", userKeyA)
	}
	if dl.Revoked(userKeyB) {
		t.Errorf("credential with no active lease was seeded (unbounded growth): %+v", userKeyB)
	}

	// All-expired set yields an empty Reset.
	allExpired := &stubLeaseCounter{counts: map[credential.CredentialKey]int{userKeyA: 0}}
	empty, err := rebuildCredentialDenyListKeys(context.Background(), &stubPoolLister{},
		&stubUserLister{revoked: oneUser()}, allExpired, time.Now())
	if err != nil {
		t.Fatalf("all-expired rebuild: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("all-expired rebuild seeded %+v, want empty", empty)
	}
}

// TestRebuildDenyListPerKeyStoreErrorRetainsKey pins the fail-closed direction
// of the lease-existence filter: when the per-key count query errors (a boot-
// time Postgres or KMS fault), the candidate key is retained rather than
// dropped, so a transient fault over-approximates the deny list.
//
// spec: §4.9 lines 1694-1695 (fail closed on a per-key store error).
func TestRebuildDenyListPerKeyStoreErrorRetainsKey(t *testing.T) {
	leases := &stubLeaseCounter{
		counts: map[credential.CredentialKey]int{},
		errs:   map[credential.CredentialKey]error{userKeyA: errors.New("kms unavailable")},
	}
	keys, err := rebuildCredentialDenyListKeys(context.Background(), &stubPoolLister{},
		&stubUserLister{revoked: oneUser()}, leases, time.Now())
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	dl := denylist.New()
	dl.Reset(keys)
	if !dl.Revoked(userKeyA) {
		t.Errorf("per-key count error dropped the key %+v; want it retained (fail closed)", userKeyA)
	}
}

// TestRebuildDenyListListingErrorCommitsNoPartialReset pins that a non-nil
// error from either listing query aborts the whole rebuild without producing a
// key set, so no Reset is ever committed on a partial union.
//
// spec: §4.9 lines 1692-1697 (fail closed on a listing-query error).
func TestRebuildDenyListListingErrorCommitsNoPartialReset(t *testing.T) {
	leases := &stubLeaseCounter{counts: map[credential.CredentialKey]int{poolKeyA: 1, userKeyA: 1}}

	// A user-term (token store) error aborts even though the pool term
	// listed successfully.
	if _, err := rebuildCredentialDenyListKeys(context.Background(),
		&stubPoolLister{revoked: onePool()}, &stubUserLister{err: errors.New("pg down")}, leases, time.Now()); err == nil {
		t.Fatal("user-term listing error: want an error, got nil (a partial Reset would have committed)")
	}
	// A pool-term error aborts before the user term is even queried.
	users := &stubUserLister{revoked: oneUser()}
	if _, err := rebuildCredentialDenyListKeys(context.Background(),
		&stubPoolLister{err: errors.New("pg down")}, users, leases, time.Now()); err == nil {
		t.Fatal("pool-term listing error: want an error, got nil")
	}
	if n := users.callCount(); n != 0 {
		t.Errorf("user listing queried %d times after a pool-term error; want 0 (abort before the second query)", n)
	}
}

// TestRebuildDenyListGatesReadinessThenCommitsOnRecovery pins the readiness
// gating contract: while a listing query errors the rebuild loop commits no
// Reset and leaves the readiness flag unset (503), and once the store recovers
// it commits the complete union and flips the flag so /readyz admits the
// replica.
//
// spec: §4.9 lines 1692-1697; §10.4 readiness precedence.
func TestRebuildDenyListGatesReadinessThenCommitsOnRecovery(t *testing.T) {
	pools := &stubPoolLister{err: errors.New("pg down at boot")}
	users := &stubUserLister{revoked: oneUser()}
	leases := &stubLeaseCounter{counts: map[credential.CredentialKey]int{userKeyA: 1}}
	dl := denylist.New()
	var committed atomic.Bool

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		rebuildCredentialDenyList(ctx, pools, users, leases, dl,
			func() { committed.Store(true) }, time.Now)
		close(done)
	}()

	// While the pool listing errors the flag stays unset (readiness gated)
	// and the deny list holds no entry for the retained revoked lease.
	deadline := time.After(500 * time.Millisecond)
	for pools.callCount() < 1 {
		select {
		case <-deadline:
			t.Fatal("rebuild loop never issued its first listing query")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if committed.Load() {
		t.Fatal("readiness flag flipped while the listing query was still failing")
	}
	if dl.Revoked(userKeyA) {
		t.Fatal("deny list seeded before the rebuild committed (partial Reset)")
	}

	// Recover the store; the backoff loop must retry, commit the complete
	// union, and flip the flag.
	pools.setErr(nil)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("rebuild did not commit after the store recovered")
	}
	if !committed.Load() {
		t.Fatal("readiness flag not set after the rebuild committed")
	}
	if !dl.Revoked(userKeyA) {
		t.Errorf("retained revoked user credential not on the deny list after recovery: %+v", userKeyA)
	}
}
