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
// outage window. That degraded-mode path lives in the leasestore/pgstore
// subpackage, behind the Failover wrapper (failover.go): the gateway
// wires Failover{primary: this Redis store, fallback: pgstore} so a
// Redis outage routes lease operations through Postgres advisory locks
// at higher latency instead of breaking lease acquisition.
package leasestore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/storage/rediskeys"
	"github.com/lennylabs/lenny/pkg/redisconn"
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
	// ErrEmptyScope — an erasure primitive was called with an empty
	// tenant id (or user id). Empty arguments must never be treated as
	// "delete everything" (§12.8).
	ErrEmptyScope = errors.New("leasestore: erasure requires a non-empty tenant_id (and user_id)")
)

// LeaseStore is the §12.2 LeaseStore role interface. *Store satisfies
// it. Alongside the session-coordination primitives it exposes the
// §12.1 mandatory erasure pair so a substitute backend that omits
// either method cannot compile into the gateway binary.
//
// spec: §12.1 — every store role interface MUST expose
// DeleteByUser and DeleteByTenant, enforced at compile time by Go
// interface satisfaction.
type LeaseStore interface {
	Acquire(ctx context.Context, tenantID, sessionID, holder string, ttl time.Duration) (Lease, error)
	Renew(ctx context.Context, tenantID, sessionID, holder string, ttl time.Duration) (Lease, error)
	Release(ctx context.Context, tenantID, sessionID, holder string) error
	Get(ctx context.Context, tenantID, sessionID string) (Lease, error)
	DeleteByUser(ctx context.Context, tenantID, userID string) (int, error)
	DeleteByTenant(ctx context.Context, tenantID string) (int, error)
}

var _ LeaseStore = (*Store)(nil)

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

// TenantKeyPrefix returns the §12.4 prefix every session lease of tenantID
// carries: `t:{tenant_id}:lease:session:`. The tenant segment is the
// rediskeys.TenantPrefix hash tag, so a tenant's leases share one Redis
// Cluster slot and the tenant erasure SCAN runs on the master that owns it.
func TenantKeyPrefix(tenantID string) string {
	return rediskeys.TenantPrefix(tenantID) + "lease:session:"
}

// Key returns the §12.4 Redis key of the session lease:
// `t:{tenant_id}:lease:session:{session_id}`. The Postgres fallback store
// derives its advisory-lock hash from the same string.
func Key(tenantID, sessionID string) string {
	return TenantKeyPrefix(tenantID) + sessionID
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
	k := Key(tenantID, sessionID)
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
	k := Key(tenantID, sessionID)
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
	k := Key(tenantID, sessionID)
	_, err := releaseScript.Run(ctx, s.client, []string{k}, holder).Int()
	return err
}

// Get returns the current lease for the session, or ErrNotFound when
// none is held.
func (s *Store) Get(ctx context.Context, tenantID, sessionID string) (Lease, error) {
	k := Key(tenantID, sessionID)
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

// DeleteByUser satisfies the §12.1 mandatory-erasure primitive. Session
// leases are coordination state keyed by session, not user data, and
// every lease carries a TTL that self-expires, so there is nothing
// user-scoped to erase here; the §12.8 step-1 lease release for the
// user's sessions is driven per-session by the erasure orchestrator,
// not by this whole-user method. It rejects empty arguments (§12.8) and otherwise returns (0, nil).
//
// spec: §12.1; §12.8 step 1 (lease
// release is session-scoped); F-12.1.3 suggested resolution ("for
// LeaseStore the erasure primitive can be a no-op — leases are
// TTL-bound — but the method must still be present").
func (s *Store) DeleteByUser(_ context.Context, tenantID, userID string) (int, error) {
	if tenantID == "" || userID == "" {
		return 0, ErrEmptyScope
	}
	return 0, nil
}

// DeleteByTenant satisfies the §12.1 mandatory-erasure primitive. It
// removes every session lease under the tenant's §12.4 key prefix
// t:{tenant_id}:lease:session:* and returns the count dropped. Leases
// are TTL-bound, so an unreleased lease would self-expire; the active
// sweep makes the §12.8 Phase-4 tenant teardown deterministic rather
// than waiting out the TTL. It is idempotent: a tenant with no leases
// is a no-op returning (0, nil).
//
// spec: §12.1; §12.2 (LeaseStore key
// prefix); §12.8 Phase 4 (tenant deletion).
func (s *Store) DeleteByTenant(ctx context.Context, tenantID string) (int, error) {
	if tenantID == "" {
		return 0, ErrEmptyScope
	}
	return redisconn.ScanDelete(ctx, s.client, rediskeys.TenantPrefix(tenantID), TenantKeyPrefix(tenantID)+"*")
}
