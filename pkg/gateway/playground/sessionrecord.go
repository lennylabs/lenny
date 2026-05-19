// SPDX-License-Identifier: MIT

package playground

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// SessionRecord is the §27.3.1 opaque server-side playground session
// record. It is held in Redis at t:{tenant_id}:pg:sess:{session_id}
// and pinned to the lenny_playground_session cookie lifetime.
type SessionRecord struct {
	// UserID, TenantID, CallerType, and Scope are the validated OIDC
	// subject claims resolved at /playground/auth/callback.
	UserID     string `json:"user_id"`
	TenantID   string `json:"tenant_id"`
	CallerType string `json:"caller_type"`
	Scope      string `json:"scope"`

	// Origin is always "playground" — §27.3.1 records it on the
	// envelope so a downstream reader does not have to infer it.
	Origin string `json:"origin"`

	// IssuedAt is the gateway clock instant the record was created.
	IssuedAt time.Time `json:"issued_at"`

	// CSRFToken is the §27.3.1 anti-forgery token bound to the record.
	CSRFToken string `json:"csrf_token"`

	// BearerJTIs tracks the jti of every bearer the session has minted
	// within the record's lifetime, newest last. The §27.3.1
	// revocation path revokes every entry. The slice is bounded by
	// ⌈oidcSessionTtlSeconds / bearerTtlSeconds⌉ + 1.
	BearerJTIs []string `json:"bearer_jtis,omitempty"`

	// CurrentExp is the exp of the most-recently-minted bearer.
	CurrentExp int64 `json:"current_exp,omitempty"`

	// Invalidated is set when the underlying OIDC principal was
	// invalidated (§11.4); subsequent mints are rejected.
	Invalidated bool `json:"invalidated,omitempty"`
}

// RevocationReason is one §27.8 reason a playground session-record
// revocation is attributed to.
type RevocationReason string

const (
	// RevokeUserLogout — the user called POST /playground/auth/logout.
	RevokeUserLogout RevocationReason = "user_logout"
	// RevokeIdleTimeout — the session was reclaimed on the idle path.
	RevokeIdleTimeout RevocationReason = "idle_timeout"
	// RevokeAdmin — an operator revoked the session.
	RevokeAdmin RevocationReason = "admin_revoke"
	// RevokeOIDCSessionEnded — the OIDC session record TTL elapsed.
	RevokeOIDCSessionEnded RevocationReason = "oidc_session_ended"
	// RevokeUserInvalidated — the OIDC principal was invalidated.
	RevokeUserInvalidated RevocationReason = "user_invalidated"
)

// errSessionNotFound is returned by SessionStore.Get when no record
// exists for the supplied id (cookie expired or never issued).
var errSessionNotFound = errors.New("playground: session record not found")

// SessionStore is the §27.3.1 backing store for the playground
// session record and the per-bearer revocation marker. It is keyed by
// the per-tenant prefix convention from §12.4: every key carries the
// t:{tenant_id}: prefix so a cross-tenant read is impossible.
//
// The §27.3.1 correctness guarantee is that the per-request
// revocation check (IsBearerRevoked) consults the authoritative store
// on every playground-origin request; the in-process negative cache
// in the Redis-backed implementation is a latency accelerator warmed
// by pub/sub, not a substitute for the store lookup.
type SessionStore interface {
	// PutSession writes rec under session id for tenant with the
	// supplied TTL (the remaining cookie lifetime). A second PutSession
	// for the same id replaces the entry without resetting the TTL
	// beyond ttl.
	PutSession(ctx context.Context, tenant, id string, rec SessionRecord, ttl time.Duration) error

	// GetSession returns the record for id under tenant. It returns
	// errSessionNotFound when no record exists.
	GetSession(ctx context.Context, tenant, id string) (SessionRecord, error)

	// RevokeSession deletes the session record and SETs a revocation
	// marker for every bearer jti the record carries, then fans the
	// change out to peer replicas. revokedTTL bounds each marker (the
	// remaining bearer lifetime plus a skew budget). It is the single
	// §27.3.1 / §27.6 revocation primitive: logout, user.invalidated,
	// idle timeout, and admin revocation all converge here. The
	// Redis-backed implementation does not return until the writes
	// have committed, so a caller that returns 200 to the browser
	// after RevokeSession guarantees the deny-list state is durable.
	RevokeSession(ctx context.Context, tenant, id string, jtis []string, revokedTTL time.Duration) error

	// MarkBearerRevoked SETs a revocation marker for a single bearer
	// jti without touching the session record. The idle-timeout and
	// silent-refresh paths use it to revoke a superseded bearer.
	MarkBearerRevoked(ctx context.Context, tenant, jti string, ttl time.Duration) error

	// IsBearerRevoked reports whether jti under tenant has a
	// revocation marker. It is consulted on the auth hot path for
	// every playground-origin request. The error is non-nil only when
	// the backing store is unreachable; §27.3.1 specifies the caller
	// fails closed (503) on that error rather than honoring the
	// bearer.
	IsBearerRevoked(ctx context.Context, tenant, jti string) (bool, error)
}

// sessionKey is the §12.4 / §27.3.1 Redis key for a playground
// session record.
func sessionKey(tenant, id string) string {
	return "t:" + tenant + ":pg:sess:" + id
}

// revokedKey is the §27.3.1 Redis key for a minted-bearer revocation
// marker.
func revokedKey(tenant, jti string) string {
	return "t:" + tenant + ":pg:revoked:" + jti
}

// revocationChannel is the §27.3.1 per-tenant pub/sub channel the
// revocation fan-out publishes on. It is a dedicated playground-role
// channel that sits alongside the EventBus channels rather than
// multiplexing onto them.
func revocationChannel(tenant string) string {
	return "t:" + tenant + ":pg:revocations"
}

// newOpaqueID returns a 256-bit base64url random identifier. It backs
// the opaque session id carried by the lenny_playground_session
// cookie and the CSRF token.
func newOpaqueID() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// MemorySessionStore is the in-process SessionStore. It backs the
// no-Redis single-replica gateway and the package tests. It is safe
// for concurrent use.
//
// The in-process store cannot fan a revocation out to a peer replica,
// which is correct because a single-replica gateway has none: every
// request hits the same process and observes the same map.
type MemorySessionStore struct {
	mu       sync.Mutex
	sessions map[string]memSession
	revoked  map[string]time.Time
	now      func() time.Time
}

type memSession struct {
	rec       SessionRecord
	expiresAt time.Time
}

// NewMemorySessionStore returns an empty MemorySessionStore.
func NewMemorySessionStore() *MemorySessionStore {
	return &MemorySessionStore{
		sessions: map[string]memSession{},
		revoked:  map[string]time.Time{},
		now:      time.Now,
	}
}

var _ SessionStore = (*MemorySessionStore)(nil)

// PutSession implements SessionStore.
func (m *MemorySessionStore) PutSession(_ context.Context, tenant, id string, rec SessionRecord, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[sessionKey(tenant, id)] = memSession{rec: rec, expiresAt: m.now().Add(ttl)}
	return nil
}

// GetSession implements SessionStore.
func (m *MemorySessionStore) GetSession(_ context.Context, tenant, id string) (SessionRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[sessionKey(tenant, id)]
	if !ok || m.now().After(s.expiresAt) {
		return SessionRecord{}, errSessionNotFound
	}
	return s.rec, nil
}

// RevokeSession implements SessionStore.
func (m *MemorySessionStore) RevokeSession(_ context.Context, tenant, id string, jtis []string, revokedTTL time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, sessionKey(tenant, id))
	exp := m.now().Add(revokedTTL)
	for _, jti := range jtis {
		if jti == "" {
			continue
		}
		m.revoked[revokedKey(tenant, jti)] = exp
	}
	return nil
}

// MarkBearerRevoked implements SessionStore.
func (m *MemorySessionStore) MarkBearerRevoked(_ context.Context, tenant, jti string, ttl time.Duration) error {
	if jti == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.revoked[revokedKey(tenant, jti)] = m.now().Add(ttl)
	return nil
}

// IsBearerRevoked implements SessionStore.
func (m *MemorySessionStore) IsBearerRevoked(_ context.Context, tenant, jti string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	exp, ok := m.revoked[revokedKey(tenant, jti)]
	if !ok {
		return false, nil
	}
	if m.now().After(exp) {
		delete(m.revoked, revokedKey(tenant, jti))
		return false, nil
	}
	return true, nil
}

// RedisSessionStore is the §27.3.1 Redis-backed SessionStore. It
// holds the session record and the revocation markers under the
// §12.4 per-tenant key prefix, fans revocations out on the
// per-tenant pub/sub channel, and maintains a bounded in-process
// negative cache warmed by the fan-out.
//
// The negative cache is negative-only: a cache miss still consults
// Redis, so a stale cache never converts a revoked bearer into an
// honored one. A Redis error on the per-request check is surfaced to
// the caller, which fails closed per §27.3.1.
type RedisSessionStore struct {
	client redis.UniversalClient
	now    func() time.Time

	cacheMu sync.RWMutex
	cache   map[string]time.Time // revokedKey -> negative-cache entry expiry
}

// NewRedisSessionStore returns a SessionStore backed by client. A nil
// client is rejected (the caller wires the in-memory store instead).
func NewRedisSessionStore(client redis.UniversalClient) *RedisSessionStore {
	return &RedisSessionStore{
		client: client,
		now:    time.Now,
		cache:  map[string]time.Time{},
	}
}

var _ SessionStore = (*RedisSessionStore)(nil)

// PutSession implements SessionStore.
func (s *RedisSessionStore) PutSession(ctx context.Context, tenant, id string, rec SessionRecord, ttl time.Duration) error {
	payload, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return s.client.Set(ctx, sessionKey(tenant, id), payload, ttl).Err()
}

// GetSession implements SessionStore.
func (s *RedisSessionStore) GetSession(ctx context.Context, tenant, id string) (SessionRecord, error) {
	raw, err := s.client.Get(ctx, sessionKey(tenant, id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return SessionRecord{}, errSessionNotFound
	}
	if err != nil {
		return SessionRecord{}, err
	}
	var rec SessionRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return SessionRecord{}, err
	}
	return rec, nil
}

// RevokeSession implements SessionStore. It DELs the session record,
// SETs a revocation marker for every bearer jti, and PUBLISHes each
// jti on the per-tenant revocation channel so peer replicas warm
// their negative cache. The writes complete before the method
// returns.
func (s *RedisSessionStore) RevokeSession(ctx context.Context, tenant, id string, jtis []string, revokedTTL time.Duration) error {
	pipe := s.client.TxPipeline()
	pipe.Del(ctx, sessionKey(tenant, id))
	for _, jti := range jtis {
		if jti == "" {
			continue
		}
		pipe.Set(ctx, revokedKey(tenant, jti), "1", revokedTTL)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}
	for _, jti := range jtis {
		if jti == "" {
			continue
		}
		// A publish failure is non-fatal: Redis remains the
		// authoritative store consulted on every request, so the
		// fan-out is a propagation accelerator (§27.3.1).
		_ = s.client.Publish(ctx, revocationChannel(tenant), jti).Err()
	}
	return nil
}

// MarkBearerRevoked implements SessionStore.
func (s *RedisSessionStore) MarkBearerRevoked(ctx context.Context, tenant, jti string, ttl time.Duration) error {
	if jti == "" {
		return nil
	}
	if err := s.client.Set(ctx, revokedKey(tenant, jti), "1", ttl).Err(); err != nil {
		return err
	}
	_ = s.client.Publish(ctx, revocationChannel(tenant), jti).Err()
	return nil
}

// IsBearerRevoked implements SessionStore. It consults the in-process
// negative cache first; a miss falls through to the authoritative
// Redis GET. A Redis error is returned so the caller fails closed.
func (s *RedisSessionStore) IsBearerRevoked(ctx context.Context, tenant, jti string) (bool, error) {
	key := revokedKey(tenant, jti)
	s.cacheMu.RLock()
	exp, cached := s.cache[key]
	s.cacheMu.RUnlock()
	if cached && s.now().Before(exp) {
		return true, nil
	}
	n, err := s.client.Exists(ctx, key).Result()
	if err != nil {
		return false, err
	}
	if n > 0 {
		s.cacheMu.Lock()
		s.cache[key] = s.now().Add(maxBearerTTL)
		s.cacheMu.Unlock()
		return true, nil
	}
	return false, nil
}

// SubscribeRevocations runs the §27.3.1 pub/sub consume loop for
// tenant: every revoked jti published on the per-tenant channel warms
// the in-process negative cache so the auth hot path can short-circuit
// the Redis GET. It blocks until ctx is cancelled; the gateway runs it
// in a goroutine. A nil client subscribes to nothing and returns when
// ctx is cancelled.
func (s *RedisSessionStore) SubscribeRevocations(ctx context.Context, tenant string) {
	if s.client == nil {
		<-ctx.Done()
		return
	}
	sub := s.client.Subscribe(ctx, revocationChannel(tenant))
	defer func() { _ = sub.Close() }()
	ch := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			s.cacheMu.Lock()
			s.cache[revokedKey(tenant, msg.Payload)] = s.now().Add(maxBearerTTL)
			s.cacheMu.Unlock()
		}
	}
}
