// SPDX-License-Identifier: MIT

// Package semanticcache implements the §12.2 SemanticCache store: a
// tenant-scoped cache of LLM query/response pairs keyed by the §4.9
// semantic-caching contract. It backs the optional CachePolicy on a
// CredentialPool and is disabled by default, opt-in per pool.
//
// A cache entry is matched to a new query by §4.9 semantic similarity:
// Get embeds the query, scans the entries recorded for the same
// (tenant, scope, model, provider) index, and returns the response of
// the nearest entry whose cosine similarity meets the configured
// threshold. An exact-text repeat is the threshold-1.0 case of the
// same lookup.
//
// Tenant and user isolation is at the key-naming level. Every Redis
// key follows the §12.4 convention t:{tenant_id}:scache:{scope}:{hash}
// where scope is u:{user_id}, s:{session_id}, or t per the pool's
// CacheScope. A write for one tenant can never produce a hit for
// another tenant because the tenant id is a fixed prefix of every key
// the lookup scans. The store exposes the §12.2 mandatory erasure
// primitives DeleteByUser and DeleteByTenant.
package semanticcache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/lennylabs/lenny/pkg/gateway/session/memorystore"
)

// DefaultSimilarityThreshold is the §4.9 CachePolicy similarityThreshold
// default: a candidate entry is a hit when its cosine similarity to the
// query is at least this value.
const DefaultSimilarityThreshold = 0.92

// DefaultTTLSeconds is the §4.9 CachePolicy ttl default in seconds. A
// cache entry self-evicts this long after it is written.
const DefaultTTLSeconds = 300

// CacheScope is the §4.9 cacheScope: the identity granularity a cache
// entry is keyed to. It selects the scope segment of the §12.4
// t:{tenant_id}:scache:{scope}:{hash} key.
type CacheScope string

const (
	// ScopePerUser is the §4.9 default. The cache key includes the
	// user id (scope segment u:{user_id}); a cached response is never
	// shared across users.
	ScopePerUser CacheScope = "per-user"
	// ScopePerSession keys the entry to a single session (scope
	// segment s:{session_id}) — the most restrictive scope.
	ScopePerSession CacheScope = "per-session"
	// ScopeTenant omits the user id (scope segment t), allowing
	// cross-user sharing within the tenant. §4.9 makes this a
	// deployer opt-in and forbids it under a regulated complianceProfile.
	ScopeTenant CacheScope = "tenant"
)

// Valid reports whether s is one of the §4.9 cacheScope values.
func (s CacheScope) Valid() bool {
	switch s {
	case ScopePerUser, ScopePerSession, ScopeTenant:
		return true
	default:
		return false
	}
}

// Sentinel errors.
var (
	// ErrEmptyTenant — an operation supplied an empty tenant id. §12.4
	// tenant isolation is at the key prefix, so an empty tenant id is
	// rejected rather than producing an unprefixed key.
	ErrEmptyTenant = errors.New("semanticcache: tenant id is required")
	// ErrEmptyUser — DeleteByUser, or a per-user-scoped Get/Put,
	// supplied an empty user id.
	ErrEmptyUser = errors.New("semanticcache: user id is required")
	// ErrEmptySession — a per-session-scoped Get/Put supplied an empty
	// session id.
	ErrEmptySession = errors.New("semanticcache: session id is required for per-session scope")
	// ErrInvalidScope — the Key carries a CacheScope outside the §4.9
	// enum.
	ErrInvalidScope = errors.New("semanticcache: invalid cache scope")
)

// Key identifies the tenant, identity scope, model, and provider a
// cache lookup or write is scoped to. The query text is supplied
// separately to Get and Put because it is embedded, not stored
// verbatim in the key.
type Key struct {
	// TenantID is the owning tenant. Required; it is the fixed prefix
	// of every Redis key, the §12.4 tenant-isolation boundary.
	TenantID string
	// Scope selects the identity granularity. An empty Scope defaults
	// to ScopePerUser, the §4.9 default.
	Scope CacheScope
	// UserID is required for ScopePerUser and recorded for erasure. It
	// is ignored for ScopeTenant.
	UserID string
	// SessionID is required for ScopePerSession.
	SessionID string
	// Model and Provider are part of the §4.9 cache key space: a
	// response cached for one model or provider is never returned for
	// another.
	Model    string
	Provider string
}

// scopeSegment returns the §12.4 scope segment for the key — u:{user},
// s:{session}, or t — validating that the id the scope needs is set.
func (k Key) scopeSegment() (string, error) {
	switch k.effectiveScope() {
	case ScopePerUser:
		if k.UserID == "" {
			return "", ErrEmptyUser
		}
		return "u:" + k.UserID, nil
	case ScopePerSession:
		if k.SessionID == "" {
			return "", ErrEmptySession
		}
		return "s:" + k.SessionID, nil
	case ScopeTenant:
		return "t", nil
	default:
		return "", ErrInvalidScope
	}
}

// effectiveScope resolves an empty Scope to the §4.9 ScopePerUser
// default.
func (k Key) effectiveScope() CacheScope {
	if k.Scope == "" {
		return ScopePerUser
	}
	return k.Scope
}

// Validate checks the key has a tenant id, a valid scope, and the
// identity id its scope requires. Store implementations call it before
// any Redis access so a malformed key never produces an unprefixed or
// cross-scope key.
func (k Key) Validate() error {
	if k.TenantID == "" {
		return ErrEmptyTenant
	}
	if !k.effectiveScope().Valid() {
		return ErrInvalidScope
	}
	_, err := k.scopeSegment()
	return err
}

// EntryKey returns the §12.4 t:{tenant_id}:scache:{scope}:{hash} key
// for one cache entry.
func (k Key) EntryKey(hash string) (string, error) {
	seg, err := k.scopeSegment()
	if err != nil {
		return "", err
	}
	return keyPrefix(k.TenantID) + seg + ":" + hash, nil
}

// IndexKey returns the per-(tenant, scope, model, provider) index key.
// The index records the hashes of every entry sharing those fields so
// a lookup can scan candidates for the nearest embedding. It lives
// under the same scope segment as the entries it indexes, so the §12.2
// erasure prefix scans cover it.
func (k Key) IndexKey() (string, error) {
	seg, err := k.scopeSegment()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%s:idx:%s:%s",
		keyPrefix(k.TenantID), seg, fieldToken(k.Model), fieldToken(k.Provider)), nil
}

// QueryHash is the §12.4 {hash} segment of the entry key: a content
// hash over the normalized query plus the model and provider, so an
// exact-text repeat of a query overwrites its own entry rather than
// accumulating duplicates. The semantic lookup does not depend on this
// hash; it scans the per-(tenant, scope, model, provider) index.
func (k Key) QueryHash(query string) string {
	h := sha256.New()
	h.Write([]byte(normalizeQuery(query)))
	h.Write([]byte{0})
	h.Write([]byte(k.Model))
	h.Write([]byte{0})
	h.Write([]byte(k.Provider))
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// Entry is one cached LLM query/response pair returned by a hit.
type Entry struct {
	// Query is the LLM query text the entry was cached under.
	Query string
	// Response is the cached LLM response.
	Response string
	// Model and Provider identify the LLM the response came from.
	Model    string
	Provider string
	// Similarity is the cosine similarity of the lookup query to this
	// entry's query, in [0, 1]. It is 1.0 for an exact-text repeat.
	Similarity float64
}

// SemanticCache is the §4.9 / §12.2 contract name for a pluggable
// semantic cache. It aliases Store so the spec's
// ValidateSemanticCacheErasure(t, cache SemanticCache) signature reads
// against the spec name while the package keeps Store internally.
type SemanticCache = Store

// Store is the §12.2 SemanticCache contract. Every method is
// tenant-scoped: the tenant id in the Key (or the explicit tenantID
// argument) is the §12.4 key-prefix isolation boundary.
type Store interface {
	// Get returns the cached response for query under key when a
	// recorded entry's cosine similarity to query meets the store's
	// threshold. The second result is false on a miss. A miss is never
	// an error.
	Get(ctx context.Context, key Key, query string) (Entry, bool, error)
	// Put records response for query under key with the store's TTL.
	Put(ctx context.Context, key Key, query, response string) error
	// DeleteByUser removes every cached entry keyed to (tenantID,
	// userID) — the §12.2 erasure primitive. It is idempotent and
	// rejects empty ids.
	DeleteByUser(ctx context.Context, tenantID, userID string) error
	// DeleteByTenant removes every cached entry scoped to tenantID —
	// the §12.2 tenant-deletion primitive. It is idempotent and
	// rejects an empty id.
	DeleteByTenant(ctx context.Context, tenantID string) error
}

// keyPrefix returns the §12.4 t:{tenant_id}:scache: prefix for a
// tenant.
func keyPrefix(tenantID string) string {
	return "t:" + tenantID + ":scache:"
}

// TenantKeyPrefix returns the §12.4 t:{tenant_id}:scache: prefix that
// every cache key for the tenant starts with. DeleteByTenant scans it.
func TenantKeyPrefix(tenantID string) string {
	return keyPrefix(tenantID)
}

// UserKeyPrefix returns the §12.4 prefix for a single user's per-user
// scoped entries and their index sets:
// t:{tenant_id}:scache:u:{user_id}:. DeleteByUser scans it.
func UserKeyPrefix(tenantID, userID string) string {
	return keyPrefix(tenantID) + "u:" + userID + ":"
}

// fieldToken makes a model or provider name safe to embed in a Redis
// key segment by replacing the segment separator. An empty value
// becomes "_" so the key segment is never empty.
func fieldToken(s string) string {
	if s == "" {
		return "_"
	}
	return strings.ReplaceAll(s, ":", "_")
}

// normalizeQuery lower-cases and collapses whitespace so a query that
// differs only in casing or spacing maps to one entry and one hash.
func normalizeQuery(query string) string {
	return strings.Join(strings.Fields(strings.ToLower(query)), " ")
}

// Similarity returns the §4.9 cosine similarity of two query
// embeddings in [0, 1]. When either embedding is absent (the Embedder
// declined the text), it falls back to an exact normalized-text
// comparison: 1.0 on an exact match and 0.0 otherwise, so a degraded
// Embedder still serves exact-text repeats. q and eq are the query
// texts behind qv and ev.
func Similarity(qv, ev []float32, q, eq string) float64 {
	if qv == nil || ev == nil {
		if normalizeQuery(q) == normalizeQuery(eq) {
			return 1.0
		}
		return 0.0
	}
	sim := 1.0 - memorystore.CosineDistance(qv, ev)
	if sim < 0 {
		return 0
	}
	if sim > 1 {
		return 1
	}
	return sim
}
