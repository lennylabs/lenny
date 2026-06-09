// SPDX-License-Identifier: MIT

package playground

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
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

	// Labels is the §27.2 line 41 playground.sessionLabels map
	// applied to the session record for audit/accounting consumers.
	// Config.EffectiveLabels guarantees the load-bearing origin label
	// is present; operators can add labels via the chart value.
	Labels map[string]string `json:"labels,omitempty"`

	// IssuedAt is the gateway clock instant the record was created.
	IssuedAt time.Time `json:"issued_at"`

	// LastActivityAt is the gateway clock instant of the record's most
	// recent activity, refreshed on every bearer mint (the playground's
	// per-session heartbeat). The §27.6 idle-timeout sweep reclaims a
	// record whose LastActivityAt predates the idle reclamation window. A
	// zero value (a legacy record written before this field existed)
	// falls back to IssuedAt. spec: §27.6 line 201.
	LastActivityAt time.Time `json:"last_activity_at,omitempty"`

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

// SessionRef identifies one playground session record by its tenant and
// opaque session id. The §27.6 idle-timeout sweep enumerates idle records
// as SessionRefs and revokes each through Handler.RevokeSession.
type SessionRef struct {
	Tenant string
	ID     string
}

// recordActivityBefore reports whether rec's last activity predates
// cutoff. It anchors on LastActivityAt, falling back to IssuedAt for a
// legacy record written before LastActivityAt existed.
func recordActivityBefore(rec SessionRecord, cutoff time.Time) bool {
	anchor := rec.LastActivityAt
	if anchor.IsZero() {
		anchor = rec.IssuedAt
	}
	return anchor.Before(cutoff)
}

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

	// SessionsForUser returns the session ids the named user holds under
	// tenant. The §11.4 user-invalidation fan-out
	// (Handler.RevokeSessionsForUser) reads it to revoke every playground
	// session the user established. The ids are a best-effort lookup
	// hint: the caller revalidates each against GetSession, so a stale id
	// (a session already revoked or expired) is harmless. spec: §27.3.1
	// line 148, §11.4.
	SessionsForUser(ctx context.Context, tenant, userID string) ([]string, error)

	// IdleSessions returns a reference to every playground session record
	// whose last activity predates cutoff — the §27.6 idle-timeout sweep's
	// reclamation candidates. A record with no recorded LastActivityAt
	// falls back to its IssuedAt. An already-invalidated record is skipped
	// (the §11.4 path revoked it). The scan is best-effort: a record that
	// expires or is revoked between the scan and the sweep's RevokeSession
	// is harmless because revocation is idempotent. spec: §27.6 line 201.
	IdleSessions(ctx context.Context, cutoff time.Time) ([]SessionRef, error)

	// TenantForSession resolves the tenant that owns an opaque session
	// id through the §27.3.1 fan-in index, so the
	// lenny_playground_session cookie carries only the opaque session id
	// (line 81) and never the tenant. ok is false when no index entry
	// exists (the cookie expired or was never issued). The error is
	// non-nil only when the backing store is unreachable, so a caller on
	// the auth path fails closed. The index is written by PutSession and
	// removed by RevokeSession. spec: §27.3.1 line 81 (the cookie carries
	// only the opaque session id).
	TenantForSession(ctx context.Context, id string) (tenant string, ok bool, err error)
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

// sessTenantIndexKey is the §27.3.1 fan-in index that recovers the
// tenant owning an opaque session id. It lets the
// lenny_playground_session cookie carry only the opaque session id
// (§27.3.1 line 81) rather than embedding the tenant in the cookie
// value. The session id is a 256-bit opaque random (newOpaqueID), so
// this platform-scoped lookup discloses no tenant data on its own:
// resolving the index requires already possessing the opaque id, which
// is the cookie credential. The entry is written under the session TTL
// by PutSession and deleted by RevokeSession. spec: §27.3.1 line 81.
func sessTenantIndexKey(id string) string {
	return "pg:sess-tenant:" + id
}

// userIndexKey is the §11.4 Redis key for the set of playground session
// ids a user holds. The §11.4 user-invalidation fan-out reads it to
// revoke every playground session the user established. It carries the
// §12.4 per-tenant prefix so a cross-tenant read is impossible. spec:
// §27.3.1 line 148, §27.6 line 204.
func userIndexKey(tenant, userID string) string {
	return "t:" + tenant + ":pg:user:" + userID
}

// revocationChannel is the §27.3.1 per-tenant pub/sub channel the
// revocation fan-out publishes on. It is a dedicated playground-role
// channel that sits alongside the EventBus channels rather than
// multiplexing onto them.
func revocationChannel(tenant string) string {
	return "t:" + tenant + ":pg:revocations"
}

// revocationChannelPattern matches every tenant's §27.3.1 revocation
// channel. A single PSUBSCRIBE on this pattern subscribes a gateway
// replica to all current and future tenants, so a tenant provisioned
// after gateway start still warms the replica's negative cache without
// a per-tenant subscription enrolment step. spec: §27.6 line 204 /
// §27.3.1 line 96 — "Every gateway replica subscribes to this channel".
// F-27.6.7.
const revocationChannelPattern = "t:*:pg:revocations"

// tenantFromRevocationChannel extracts the tenant id from a concrete
// revocation channel name produced by revocationChannel. It returns
// ok=false for any string that is not a t:{tenant}:pg:revocations
// channel. Tenant ids match ^[a-zA-Z0-9_-]{1,128}$ (§10.2) and carry no
// colon, so trimming the fixed prefix and suffix is unambiguous.
func tenantFromRevocationChannel(channel string) (string, bool) {
	const prefix = "t:"
	const suffix = ":pg:revocations"
	if !strings.HasPrefix(channel, prefix) || !strings.HasSuffix(channel, suffix) {
		return "", false
	}
	tenant := channel[len(prefix) : len(channel)-len(suffix)]
	if tenant == "" {
		return "", false
	}
	return tenant, true
}

// encodeRevocationMsg renders the §27.3.1 pub/sub payload as
// "<originReplicaID>|<publishUnixNano>|<jti>". The origin replica id
// lets a subscriber skip its own publishes when measuring cross-replica
// propagation latency, and the publish timestamp lets a peer compute
// the end-to-end §27.8 propagation sample on receipt. The jti is placed
// last so a SplitN keeps it intact even though §10.2 jti material never
// contains the delimiter. spec: §27.8 line 241. F-27.6.6.
func encodeRevocationMsg(originReplicaID string, publishNano int64, jti string) string {
	return originReplicaID + "|" + strconv.FormatInt(publishNano, 10) + "|" + jti
}

// parseRevocationMsg inverts encodeRevocationMsg. tsOK is false for a
// payload that does not carry the origin/timestamp envelope (the jti is
// still recovered so the negative cache is warmed, but no propagation
// sample is recorded for it).
func parseRevocationMsg(payload string) (originReplicaID string, publishNano int64, jti string, tsOK bool) {
	parts := strings.SplitN(payload, "|", 3)
	if len(parts) != 3 {
		return "", 0, payload, false
	}
	nano, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", 0, parts[2], false
	}
	return parts[0], nano, parts[2], true
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
	// idTenant is the §27.3.1 fan-in index: opaque session id -> tenant,
	// so TenantForSession recovers the tenant the cookie no longer
	// carries. F-27.3.8.
	idTenant map[string]memTenantIndex
	now      func() time.Time
}

type memSession struct {
	rec       SessionRecord
	expiresAt time.Time
}

// memTenantIndex is one §27.3.1 session-id -> tenant index entry, held
// under the session TTL so a stale id self-expires. F-27.3.8.
type memTenantIndex struct {
	tenant    string
	expiresAt time.Time
}

// NewMemorySessionStore returns an empty MemorySessionStore.
func NewMemorySessionStore() *MemorySessionStore {
	return &MemorySessionStore{
		sessions: map[string]memSession{},
		revoked:  map[string]time.Time{},
		idTenant: map[string]memTenantIndex{},
		now:      time.Now,
	}
}

var _ SessionStore = (*MemorySessionStore)(nil)

// PutSession implements SessionStore.
func (m *MemorySessionStore) PutSession(_ context.Context, tenant, id string, rec SessionRecord, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	exp := m.now().Add(ttl)
	m.sessions[sessionKey(tenant, id)] = memSession{rec: rec, expiresAt: exp}
	// §27.3.1 fan-in index so the cookie carries only the opaque id.
	m.idTenant[id] = memTenantIndex{tenant: tenant, expiresAt: exp}
	return nil
}

// TenantForSession implements SessionStore.
func (m *MemorySessionStore) TenantForSession(_ context.Context, id string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.idTenant[id]
	if !ok || m.now().After(e.expiresAt) {
		if ok {
			delete(m.idTenant, id)
		}
		return "", false, nil
	}
	return e.tenant, true, nil
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
	delete(m.idTenant, id)
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

// SessionsForUser implements SessionStore. It scans the in-process
// session map for the live records the user holds under tenant. The
// scan is always consistent (an expired or deleted record is never
// returned), so the in-memory store needs no separate user index.
func (m *MemorySessionStore) SessionsForUser(_ context.Context, tenant, userID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	prefix := sessionKey(tenant, "")
	now := m.now()
	var ids []string
	for key, s := range m.sessions {
		if !strings.HasPrefix(key, prefix) || now.After(s.expiresAt) || s.rec.UserID != userID {
			continue
		}
		ids = append(ids, strings.TrimPrefix(key, prefix))
	}
	return ids, nil
}

// IdleSessions implements SessionStore. It scans the in-process session
// map for live, non-invalidated records whose last activity predates
// cutoff.
func (m *MemorySessionStore) IdleSessions(_ context.Context, cutoff time.Time) ([]SessionRef, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	var refs []SessionRef
	for key, s := range m.sessions {
		if now.After(s.expiresAt) || s.rec.Invalidated {
			continue
		}
		if !recordActivityBefore(s.rec, cutoff) {
			continue
		}
		// The record carries its own tenant; the id is the key suffix
		// after the t:{tenant}:pg:sess: prefix.
		id := strings.TrimPrefix(key, sessionKey(s.rec.TenantID, ""))
		refs = append(refs, SessionRef{Tenant: s.rec.TenantID, ID: id})
	}
	return refs, nil
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

	// replicaID is a stable per-process identifier stamped on every
	// published revocation message. The pub/sub fan-out reaches every
	// subscriber including the originating replica, so the subscribe
	// loop compares this id against the message origin to record the
	// §27.8 propagation sample only for messages a *peer* published.
	// spec: §27.8 line 241. F-27.6.6.
	replicaID string

	// propObserver, when set, receives the §27.8
	// lenny_playground_session_revocation_propagation_seconds samples the
	// subscribe loop observes (outcome, seconds). Nil disables sampling.
	propObserver func(outcome string, seconds float64)

	cacheMu sync.RWMutex
	cache   map[string]time.Time // revokedKey -> negative-cache entry expiry
}

// NewRedisSessionStore returns a SessionStore backed by client. A nil
// client is rejected (the caller wires the in-memory store instead).
func NewRedisSessionStore(client redis.UniversalClient) *RedisSessionStore {
	id, err := newOpaqueID()
	if err != nil {
		// rand failure is catastrophic; fall back to a fixed id so the
		// store still functions (self-publish skipping degrades to never
		// skipping, which only adds near-zero propagation samples).
		id = "replica"
	}
	return &RedisSessionStore{
		client:    client,
		now:       time.Now,
		replicaID: id,
		cache:     map[string]time.Time{},
	}
}

// WithMetrics wires the §27.8 propagation-latency histogram into the
// subscribe loop and returns s for chaining. The gateway calls it so a
// peer-observed revocation records a lenny_playground_session_revocation_propagation_seconds
// sample. revocationPropagation is itself nil-safe, so WithMetrics(nil)
// leaves sampling disabled. spec: §27.8 line 241. F-27.6.6.
func (s *RedisSessionStore) WithMetrics(m *Metrics) *RedisSessionStore {
	if m == nil {
		return s
	}
	s.propObserver = m.revocationPropagation
	return s
}

var _ SessionStore = (*RedisSessionStore)(nil)

// PutSession implements SessionStore. It writes the session record and
// indexes the session under the user so the §11.4 user-invalidation
// fan-out can revoke every playground session the user holds. The user
// index is a best-effort lookup hint (RevokeSessionsForUser revalidates
// each member against GetSession), so a member that outlives its record
// is harmless; the index set self-expires at the session TTL.
func (s *RedisSessionStore) PutSession(ctx context.Context, tenant, id string, rec SessionRecord, ttl time.Duration) error {
	payload, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	pipe := s.client.TxPipeline()
	pipe.Set(ctx, sessionKey(tenant, id), payload, ttl)
	// §27.3.1 fan-in index so the cookie carries only the opaque id; it
	// shares the session TTL so it self-expires with the record.
	pipe.Set(ctx, sessTenantIndexKey(id), tenant, ttl)
	if rec.UserID != "" {
		uk := userIndexKey(tenant, rec.UserID)
		pipe.SAdd(ctx, uk, id)
		if ttl > 0 {
			pipe.Expire(ctx, uk, ttl)
		}
	}
	_, err = pipe.Exec(ctx)
	return err
}

// TenantForSession implements SessionStore. It reads the §27.3.1 fan-in
// index. A missing entry returns ok=false; a transport error is
// returned so the auth path fails closed.
func (s *RedisSessionStore) TenantForSession(ctx context.Context, id string) (string, bool, error) {
	tenant, err := s.client.Get(ctx, sessTenantIndexKey(id)).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return tenant, true, nil
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
	pipe.Del(ctx, sessTenantIndexKey(id))
	for _, jti := range jtis {
		if jti == "" {
			continue
		}
		pipe.Set(ctx, revokedKey(tenant, jti), "1", revokedTTL)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}
	publishNano := s.now().UnixNano()
	for _, jti := range jtis {
		if jti == "" {
			continue
		}
		// A publish failure is non-fatal: Redis remains the
		// authoritative store consulted on every request, so the
		// fan-out is a propagation accelerator (§27.3.1). The payload
		// carries this replica's id and the publish instant so a peer
		// can record the §27.8 cross-replica propagation sample. F-27.6.6.
		_ = s.client.Publish(ctx, revocationChannel(tenant), encodeRevocationMsg(s.replicaID, publishNano, jti)).Err()
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
	_ = s.client.Publish(ctx, revocationChannel(tenant), encodeRevocationMsg(s.replicaID, s.now().UnixNano(), jti)).Err()
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

// SessionsForUser implements SessionStore. It reads the §11.4 user
// index set. A missing key returns an empty slice, so a user with no
// playground session yields no ids.
func (s *RedisSessionStore) SessionsForUser(ctx context.Context, tenant, userID string) ([]string, error) {
	return s.client.SMembers(ctx, userIndexKey(tenant, userID)).Result()
}

// idleSessionScanPattern matches every tenant's playground session-record
// key (t:{tenant}:pg:sess:{id}). It deliberately excludes the global
// pg:sess-tenant:{id} fan-in index (no t: prefix) and the per-user
// pg:user:* index (pg:user:, not pg:sess:), so a single SCAN over it
// enumerates session records and nothing else.
const idleSessionScanPattern = "t:*:pg:sess:*"

// IdleSessions implements SessionStore. It SCANs every tenant's session
// record (the §12.4 hash-tagged keys), unmarshals each, and returns a
// reference to those that are idle past cutoff and not invalidated. A
// record whose JSON cannot be parsed is skipped rather than failing the
// whole sweep, so one corrupt entry does not strand the rest. The SCAN is
// O(records) and runs on the sweep cadence (minutes), well outside the
// per-request hot path. spec: §27.6 line 201.
func (s *RedisSessionStore) IdleSessions(ctx context.Context, cutoff time.Time) ([]SessionRef, error) {
	const scanBatch = 256
	var (
		cursor uint64
		refs   []SessionRef
	)
	for {
		keys, next, err := s.client.Scan(ctx, cursor, idleSessionScanPattern, scanBatch).Result()
		if err != nil {
			return nil, err
		}
		for _, key := range keys {
			raw, err := s.client.Get(ctx, key).Bytes()
			if errors.Is(err, redis.Nil) {
				continue // expired between SCAN and GET
			}
			if err != nil {
				return nil, err
			}
			var rec SessionRecord
			if json.Unmarshal(raw, &rec) != nil {
				continue
			}
			if rec.Invalidated || !recordActivityBefore(rec, cutoff) {
				continue
			}
			id := strings.TrimPrefix(key, sessionKey(rec.TenantID, ""))
			refs = append(refs, SessionRef{Tenant: rec.TenantID, ID: id})
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return refs, nil
}

// SubscribeAllRevocations runs the §27.3.1 pub/sub consume loop over a
// single PSUBSCRIBE on revocationChannelPattern, so the replica warms
// its negative cache for every tenant — including tenants provisioned
// after gateway start — without a per-tenant enrolment step. It blocks
// until ctx is cancelled; the gateway runs it in a goroutine. A dropped
// subscription is re-established and the outage duration is recorded as
// a §27.8 {outcome="resubscribe"} sample. A nil client subscribes to
// nothing and returns when ctx is cancelled.
//
// spec: §27.6 line 204 / §27.3.1 line 96 — every replica subscribes and
// a dropped subscription re-subscribes and emits the resubscribe
// outcome. F-27.6.6, F-27.6.7.
func (s *RedisSessionStore) SubscribeAllRevocations(ctx context.Context) {
	if s.client == nil {
		<-ctx.Done()
		return
	}
	var droppedAt time.Time // zero on the first subscribe (no prior outage)
	const resubscribeBackoff = 250 * time.Millisecond
	for {
		if err := ctx.Err(); err != nil {
			return
		}
		// A re-subscribe means the prior subscription dropped; the gap
		// from the drop to a healthy subscription is the propagation
		// outage the §27.8 resubscribe outcome reports.
		if !droppedAt.IsZero() {
			s.recordPropagation("resubscribe", s.now().Sub(droppedAt).Seconds())
		}
		sub := s.client.PSubscribe(ctx, revocationChannelPattern)
		ch := sub.Channel()
		s.drainRevocations(ctx, ch)
		_ = sub.Close()
		if ctx.Err() != nil {
			return
		}
		// The channel closed without ctx cancellation: the subscription
		// dropped. Mark the outage start and back off before re-subscribing.
		droppedAt = s.now()
		select {
		case <-ctx.Done():
			return
		case <-time.After(resubscribeBackoff):
		}
	}
}

// drainRevocations consumes pub/sub messages until ch closes or ctx is
// cancelled, applying each to the negative cache and propagation
// histogram via handleRevocationMessage.
func (s *RedisSessionStore) drainRevocations(ctx context.Context, ch <-chan *redis.Message) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			s.handleRevocationMessage(msg.Channel, msg.Payload)
		}
	}
}

// handleRevocationMessage applies one received revocation pub/sub
// message: it warms the per-tenant negative cache for the carried jti
// and, when the message was published by a *peer* replica and carries a
// timestamp, records the §27.8 end-to-end propagation latency under the
// pubsub_delivered outcome. A message this replica published itself
// warms the cache but is not sampled (it is not a cross-replica
// observation). spec: §27.8 line 241. F-27.6.6.
func (s *RedisSessionStore) handleRevocationMessage(channel, payload string) {
	tenant, ok := tenantFromRevocationChannel(channel)
	if !ok {
		return
	}
	originReplicaID, publishNano, jti, tsOK := parseRevocationMsg(payload)
	if jti == "" {
		return
	}
	s.cacheMu.Lock()
	s.cache[revokedKey(tenant, jti)] = s.now().Add(maxBearerTTL)
	s.cacheMu.Unlock()
	if tsOK && originReplicaID != s.replicaID {
		latency := s.now().Sub(time.Unix(0, publishNano)).Seconds()
		if latency < 0 {
			latency = 0
		}
		s.recordPropagation("pubsub_delivered", latency)
	}
}

// recordPropagation observes a §27.8 propagation sample when an
// observer is wired. Nil-safe.
func (s *RedisSessionStore) recordPropagation(outcome string, seconds float64) {
	if s.propObserver == nil {
		return
	}
	s.propObserver(outcome, seconds)
}
