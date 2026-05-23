// SPDX-License-Identifier: MIT

// Package routingcache is the §4.2 line 152 "Redis hot routing cache".
// The Session Manager's primary store is Postgres; this cache short-
// circuits the per-request lookup of session → replica binding so
// hot-path traffic does not contend on the database.
//
// The cache holds the minimal binding tuple needed to route an
// inbound request to the gateway replica currently coordinating a
// session: the session ID, the coordinating replica's identity, the
// pod assignment, and the recovery generation that produced the
// binding. The TTL is short (default 5 minutes) so a forgotten entry
// becomes stale rather than stuck; the Session Manager invalidates
// the entry explicitly on coordinator handoff, on terminal
// transitions, and on bind/rebind.
//
// Cache misses fall through to Postgres at the call site — this
// package never reads or writes the primary store. Cache failures
// are non-fatal: every Get returns ErrCacheMiss on miss or on
// transport error so callers always fall back to Postgres, and
// every Set / Invalidate logs and swallows transport errors.
//
// spec: §4.2 line 152 ("Backed by: Postgres (primary), Redis (hot
// routing cache, short-lived locks)").
package routingcache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// DefaultTTL is the §4.2 hot-routing-cache entry TTL. The value is
// short enough that a forgotten invalidation produces stale routes
// for at most one window, and long enough that the cache absorbs the
// per-request lookup load between coordinator changes.
const DefaultTTL = 5 * time.Minute

// keyPrefix namespaces every cache entry so a single Redis instance
// can hold the routing cache alongside the other §4.2 Redis
// concerns (delegation budgets, quota counters, etc.). The §12.6
// concern split for the cache is RedisConcernCachePubSub.
const keyPrefix = "lenny:route:"

// Binding is the cached session → replica binding. The fields mirror
// the §4.2 line 156 session row columns: ReplicaID names the
// gateway replica currently coordinating the session, PodAssignment
// is the §4.2 line 160 pod-to-session binding, RecoveryGeneration
// is the §4.2 generation counter that produced the binding. The
// triple lets a fresh replica detect a stale cache entry without
// consulting Postgres: a recorded RecoveryGeneration older than the
// replica's view of the session is a stale hit and must be treated
// as a miss.
type Binding struct {
	// ReplicaID identifies the gateway replica that holds the
	// authoritative coordinator lease for this session.
	ReplicaID string `json:"replicaId"`

	// PodAssignment is the §4.2 pod-to-session binding the session
	// is currently running on. Empty when the session has no live
	// pod (created but not yet started, or already drained).
	PodAssignment string `json:"podAssignment,omitempty"`

	// RecoveryGeneration is the §4.2 line 156 recovery counter.
	// Callers compare against the row's RecoveryGeneration on
	// fall-through to detect a stale cache hit produced by an out-
	// of-date generation.
	RecoveryGeneration int64 `json:"recoveryGeneration"`
}

// Errors returned by the cache.
var (
	// ErrCacheMiss reports that no live entry exists for the session
	// id. Callers treat the miss as the trigger for a Postgres
	// fall-through and a subsequent Set on success.
	ErrCacheMiss = errors.New("routingcache: miss")

	// ErrInvalidSessionID reports that an empty session id was
	// supplied. The cache never holds a row for an empty key.
	ErrInvalidSessionID = errors.New("routingcache: session id is required")
)

// Cache is the §4.2 hot routing cache contract. Backed by Redis in
// production; tests use a miniredis-backed instance.
type Cache interface {
	// Get returns the cached binding for sessionID or ErrCacheMiss
	// when no entry exists. Transport errors are also surfaced as
	// ErrCacheMiss so callers always fall back to Postgres.
	Get(ctx context.Context, sessionID string) (Binding, error)

	// Set writes the binding under sessionID with the configured
	// TTL. A nil error from a writer that fails the transport is
	// acceptable — the cache is best-effort and the caller must
	// hold the Postgres row as the source of truth.
	Set(ctx context.Context, sessionID string, b Binding) error

	// Invalidate removes the cached binding for sessionID. Called
	// on coordinator handoff, terminal transitions, and pod
	// rebind. A missing entry is not an error.
	Invalidate(ctx context.Context, sessionID string) error
}

// RedisCache is the Redis-backed Cache implementation. Construct
// with New.
type RedisCache struct {
	client redis.UniversalClient
	ttl    time.Duration
}

// Config configures New.
type Config struct {
	// Client is the §12.6 Redis client routed to the
	// RedisConcernCachePubSub concern. Required.
	Client redis.UniversalClient

	// TTL is the per-entry expiry. A zero value defaults to
	// DefaultTTL.
	TTL time.Duration
}

// New constructs a RedisCache from the supplied config.
func New(cfg Config) (*RedisCache, error) {
	if cfg.Client == nil {
		return nil, fmt.Errorf("routingcache: redis client is required")
	}
	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &RedisCache{client: cfg.Client, ttl: ttl}, nil
}

var _ Cache = (*RedisCache)(nil)

// Get returns the cached binding for sessionID. Returns ErrCacheMiss
// on a missing key or any transport error — the caller falls back
// to Postgres in both cases.
func (c *RedisCache) Get(ctx context.Context, sessionID string) (Binding, error) {
	if sessionID == "" {
		return Binding{}, ErrInvalidSessionID
	}
	raw, err := c.client.Get(ctx, keyPrefix+sessionID).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return Binding{}, ErrCacheMiss
		}
		// spec: §4.2 line 152 — the cache is best-effort. Surface a
		// transport error as a miss so the caller falls through to
		// Postgres rather than failing the request.
		return Binding{}, ErrCacheMiss
	}
	var b Binding
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		// A corrupt entry is treated as a miss; the caller writes a
		// fresh value on fall-through.
		return Binding{}, ErrCacheMiss
	}
	return b, nil
}

// Set writes the binding under sessionID with the configured TTL.
// Transport errors are returned to the caller but never block the
// hot path — the caller logs and proceeds; the Postgres row is the
// source of truth.
func (c *RedisCache) Set(ctx context.Context, sessionID string, b Binding) error {
	if sessionID == "" {
		return ErrInvalidSessionID
	}
	payload, err := json.Marshal(b)
	if err != nil {
		return fmt.Errorf("routingcache: marshal binding: %w", err)
	}
	if err := c.client.Set(ctx, keyPrefix+sessionID, payload, c.ttl).Err(); err != nil {
		return fmt.Errorf("routingcache: set: %w", err)
	}
	return nil
}

// Invalidate removes the cached binding for sessionID. A missing
// entry is not an error; callers invoke Invalidate on every
// coordinator handoff and terminal transition.
func (c *RedisCache) Invalidate(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return ErrInvalidSessionID
	}
	if err := c.client.Del(ctx, keyPrefix+sessionID).Err(); err != nil {
		return fmt.Errorf("routingcache: del: %w", err)
	}
	return nil
}
