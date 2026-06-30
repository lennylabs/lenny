// SPDX-License-Identifier: MIT

// Package proxycache wires the §4.9 SemanticCache into the LLM reverse
// proxy. It implements the proxy's ProxyCache seam: given a credential
// lease it resolves the backing pool's CachePolicy and CacheScope, keys
// a semanticcache lookup to the §12.4 (tenant, scope, model, provider)
// key space, and serves or records the response.
//
// §4.9 caching is disabled by default and opt-in per pool: a lease whose
// pool declares no CachePolicy, or one with Enabled false, is never
// cached. Per-user scope (the §4.9 default) keys on the session's owning
// user; a request whose user cannot be resolved is left uncached rather
// than keyed without the user id.
package proxycache

import (
	"context"
	"encoding/json"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/semanticcache"
)

// PoolGetter resolves a pool by (tenant, name) for its §4.9 CachePolicy
// and CacheScope. credentialpoolstore.Store satisfies it.
type PoolGetter interface {
	Get(ctx context.Context, tenantID, name string) (credentialpoolstore.CredentialPool, error)
}

// SessionUserLookup resolves a session's owning user for §4.9 per-user
// cache keying. A nil lookup, or a miss, makes a per-user-scoped request
// unkeyable and therefore uncached.
type SessionUserLookup interface {
	UserID(ctx context.Context, tenantID, sessionID string) (string, bool)
}

// Adapter implements the proxy's ProxyCache over a semanticcache.Store.
// Construct with New.
type Adapter struct {
	pools PoolGetter
	users SessionUserLookup
	cache semanticcache.Store
}

// New returns an Adapter. A nil users lookup leaves per-user-scoped pools
// uncached (their key needs a user id); per-session and tenant scopes do
// not consult it.
func New(pools PoolGetter, cache semanticcache.Store, users SessionUserLookup) *Adapter {
	return &Adapter{pools: pools, users: users, cache: cache}
}

// Lookup returns a cached response for reqBody under the lease's pool
// cache configuration, or hit == false on a miss or when caching is off
// for the pool. A resolution failure (unknown pool, unkeyable scope) is
// a miss, never an error to the caller.
func (a *Adapter) Lookup(ctx context.Context, lease credential.Lease, reqBody []byte) ([]byte, bool) {
	key, ok := a.keyFor(ctx, lease, reqBody)
	if !ok {
		return nil, false
	}
	entry, hit, err := a.cache.Get(ctx, key, string(reqBody))
	if err != nil || !hit {
		return nil, false
	}
	return []byte(entry.Response), true
}

// Store records respBody for reqBody under the lease's pool cache
// configuration. It is a no-op when caching is off for the pool or the
// scope is unkeyable.
func (a *Adapter) Store(ctx context.Context, lease credential.Lease, reqBody, respBody []byte) {
	key, ok := a.keyFor(ctx, lease, reqBody)
	if !ok {
		return
	}
	_ = a.cache.Put(ctx, key, string(reqBody), string(respBody))
}

// keyFor resolves the §4.9 semanticcache key for a lease's request, or
// ok == false when the pool declares no enabled CachePolicy or the
// scope's identity id cannot be resolved. The query text is supplied
// separately to Get/Put, so the key omits it.
func (a *Adapter) keyFor(ctx context.Context, lease credential.Lease, reqBody []byte) (semanticcache.Key, bool) {
	if a.cache == nil || lease.TenantID == "" || lease.PoolID == "" {
		return semanticcache.Key{}, false
	}
	pool, err := a.pools.Get(ctx, lease.TenantID, lease.PoolID)
	if err != nil || !pool.IsActive() {
		return semanticcache.Key{}, false
	}
	cp := pool.CachePolicy
	if cp == nil || !cp.Enabled {
		return semanticcache.Key{}, false
	}
	// Only the launch strategy (`semantic`, the empty default) is cached.
	if cp.Strategy != "" && cp.Strategy != "semantic" {
		return semanticcache.Key{}, false
	}
	scope := semanticcache.CacheScope(pool.CacheScope)
	if scope == "" {
		scope = semanticcache.ScopePerUser
	}
	key := semanticcache.Key{
		TenantID:  lease.TenantID,
		Scope:     scope,
		SessionID: lease.SessionID,
		Model:     keyModel(reqBody),
		Provider:  string(lease.Provider),
	}
	switch scope {
	case semanticcache.ScopePerUser:
		// The §4.9 default scope keys on the session's owning user. A
		// request whose user cannot be resolved is left uncached rather
		// than keyed without the user id.
		if a.users == nil {
			return semanticcache.Key{}, false
		}
		uid, ok := a.users.UserID(ctx, lease.TenantID, lease.SessionID)
		if !ok || uid == "" {
			return semanticcache.Key{}, false
		}
		key.UserID = uid
	case semanticcache.ScopePerSession:
		if lease.SessionID == "" {
			return semanticcache.Key{}, false
		}
	}
	return key, true
}

// keyModel extracts the upstream model id from an Anthropic Messages
// request body for the §4.9 (model, provider) cache key dimension. An
// absent or unparseable model yields the empty string, which the
// semanticcache key treats as a distinct model bucket.
func keyModel(reqBody []byte) string {
	var req struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(reqBody, &req)
	return req.Model
}
