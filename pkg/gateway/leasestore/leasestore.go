// SPDX-License-Identifier: MIT

// Package leasestore is the §12.2 LeaseStore: the Redis-backed
// distributed session-coordination lease that lets one gateway
// replica claim exclusive ownership of a session (§10.1 horizontal
// scaling). A replica acquires the lease before driving a session,
// renews it on a heartbeat, and releases it when done; if the replica
// crashes, the lease TTL-expires and another replica can take over.
//
// The lease lives at the §12.4 key t:{tenant_id}:lease:session:
// {session_id}. Acquire, Renew, and Release each compare the caller's
// holder identity against the stored value and act atomically via a
// Lua script, so a lease can never be stolen or renewed by a replica
// that does not hold it.
//
// §12.4 specifies a Postgres advisory-lock fallback for the Redis
// outage window; that degraded-mode path is not yet implemented.
package leasestore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Sentinel errors.
var (
	// ErrHeld — the session lease is currently held by a different
	// holder.
	ErrHeld = errors.New("leasestore: session lease held by another holder")
	// ErrNotHeld — the caller is not the current holder, so the
	// renew or release had no effect (the lease expired or was taken
	// over).
	ErrNotHeld = errors.New("leasestore: caller does not hold the session lease")
	// ErrNotFound — no lease exists for the session.
	ErrNotFound = errors.New("leasestore: no lease for session")
)

// Lease describes a held session-coordination lease.
type Lease struct {
	TenantID  string
	SessionID string
	// Holder identifies the gateway replica that owns the lease.
	Holder string
	// ExpiresAt is when the lease TTL lapses absent a renew.
	ExpiresAt time.Time
}

// Store is the Redis-backed lease store. Construct with New.
type Store struct {
	client redis.UniversalClient
	now    func() time.Time
}

// New returns a Store backed by client.
func New(client redis.UniversalClient) *Store {
	return &Store{client: client, now: func() time.Time { return time.Now().UTC() }}
}

func leaseKey(tenantID, sessionID string) string {
	return "t:" + tenantID + ":lease:session:" + sessionID
}

// acquireScript sets the lease only when it is unheld or already held
// by the same holder, refreshing the TTL in either case. Returns 1
// when the caller holds the lease afterwards, 0 when another holder
// has it.
var acquireScript = redis.NewScript(`
local cur = redis.call('GET', KEYS[1])
if cur == false or cur == ARGV[1] then
  redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
  return 1
end
return 0
`)

// renewScript extends the TTL only when the caller is the holder.
var renewScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  redis.call('PEXPIRE', KEYS[1], ARGV[2])
  return 1
end
return 0
`)

// releaseScript deletes the lease only when the caller is the holder.
var releaseScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  redis.call('DEL', KEYS[1])
  return 1
end
return 0
`)

// Acquire claims the session lease for holder with the given TTL. It
// succeeds when the lease is unheld, and is idempotent when holder
// already owns it (the TTL is refreshed). Returns ErrHeld when a
// different holder owns the lease.
func (s *Store) Acquire(ctx context.Context, tenantID, sessionID, holder string, ttl time.Duration) (Lease, error) {
	if ttl <= 0 {
		return Lease{}, fmt.Errorf("leasestore: ttl must be positive, got %s", ttl)
	}
	k := leaseKey(tenantID, sessionID)
	res, err := acquireScript.Run(ctx, s.client, []string{k}, holder, ttl.Milliseconds()).Int()
	if err != nil {
		return Lease{}, err
	}
	if res != 1 {
		return Lease{}, ErrHeld
	}
	return Lease{
		TenantID:  tenantID,
		SessionID: sessionID,
		Holder:    holder,
		ExpiresAt: s.now().Add(ttl),
	}, nil
}

// Renew extends the lease TTL. Returns ErrNotHeld when holder is not
// the current holder — the lease lapsed, or another replica took over.
func (s *Store) Renew(ctx context.Context, tenantID, sessionID, holder string, ttl time.Duration) (Lease, error) {
	if ttl <= 0 {
		return Lease{}, fmt.Errorf("leasestore: ttl must be positive, got %s", ttl)
	}
	k := leaseKey(tenantID, sessionID)
	res, err := renewScript.Run(ctx, s.client, []string{k}, holder, ttl.Milliseconds()).Int()
	if err != nil {
		return Lease{}, err
	}
	if res != 1 {
		return Lease{}, ErrNotHeld
	}
	return Lease{
		TenantID:  tenantID,
		SessionID: sessionID,
		Holder:    holder,
		ExpiresAt: s.now().Add(ttl),
	}, nil
}

// Release drops the lease when holder owns it. It is a no-op when the
// lease is already gone or held by another replica, so a replica can
// safely release on shutdown without racing the TTL.
func (s *Store) Release(ctx context.Context, tenantID, sessionID, holder string) error {
	k := leaseKey(tenantID, sessionID)
	_, err := releaseScript.Run(ctx, s.client, []string{k}, holder).Int()
	return err
}

// Get returns the current lease for the session, or ErrNotFound when
// none is held.
func (s *Store) Get(ctx context.Context, tenantID, sessionID string) (Lease, error) {
	k := leaseKey(tenantID, sessionID)
	holder, err := s.client.Get(ctx, k).Result()
	if errors.Is(err, redis.Nil) {
		return Lease{}, ErrNotFound
	}
	if err != nil {
		return Lease{}, err
	}
	pttl, err := s.client.PTTL(ctx, k).Result()
	if err != nil {
		return Lease{}, err
	}
	if pttl < 0 {
		// -2: key vanished between GET and PTTL. -1: no TTL, which a
		// lease must always carry — treat both as "no lease".
		return Lease{}, ErrNotFound
	}
	return Lease{
		TenantID:  tenantID,
		SessionID: sessionID,
		Holder:    holder,
		ExpiresAt: s.now().Add(pttl),
	}, nil
}
