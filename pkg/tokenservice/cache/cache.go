// SPDX-License-Identifier: MIT

// Package cache implements the §4.3 line 201 "access tokens
// short-lived, cached in Redis (encrypted, not plaintext)" cache for
// the Token Service. The cache stores the validated claims of a
// recently exchanged token under the key `lenny:token:{jti}`, with the
// claims envelope-encrypted under the Token Service's KMS KEK so that
// even a Redis compromise does not yield plaintext claims. TTL matches
// the remaining lifetime of the access token, so an expired entry
// times out on its own without an explicit Del call.
//
// The cache is read on the token-validation hot path to short-circuit
// the Postgres revocation lookup; a miss falls through to the
// authoritative Postgres lookup, and a hit returns the claims directly.
// Invalidate is called by the revocation path so a revoked token never
// reads back from the cache.
//
// spec: §4.3 line 201; §13.3 revocation cache discipline.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/kms/envelope"
)

// ErrNotFound is returned by Get when the cache has no entry for the
// requested jti. The caller falls through to the authoritative
// Postgres lookup.
var ErrNotFound = errors.New("tokenservice/cache: jti not found")

// keyPrefix is the §4.3 cache key prefix the Token Service uses for
// every access-token entry. spec: §4.3 line 201.
const keyPrefix = "lenny:token:"

// Cipher is the subset of envelope.Cipher the cache needs.
type Cipher interface {
	Seal(ctx context.Context, plaintext []byte) (envelope.Sealed, error)
	Open(ctx context.Context, sealed envelope.Sealed) ([]byte, error)
}

// Cache is the §4.3 Redis-backed access-token cache. Construct with
// New. A nil Cache value is callable: every method is a no-op,
// matching the "degrade to direct-Postgres when no Redis" path
// documented at §4.3.
type Cache struct {
	client redis.UniversalClient
	cipher Cipher
}

// CacheKEKAlias is the platform-scoped KEK alias under which cached
// access-token claims are envelope-encrypted. spec: §4.3 line 201.
const CacheKEKAlias = "platform:token-service-cache"

// New returns a Redis-backed cache. provider must not be nil: the
// §4.3 contract requires claim ciphertext at rest, so a nil KMS
// provider cannot satisfy the cache. client must not be nil; pass a
// nil *Cache to disable caching at the call site.
func New(client redis.UniversalClient, provider kms.Provider) (*Cache, error) {
	if client == nil {
		return nil, errors.New("tokenservice/cache: nil redis client")
	}
	if provider == nil {
		return nil, errors.New("tokenservice/cache: nil kms provider; §4.3 mandates encrypted-at-rest token cache")
	}
	cipher, err := envelope.New(provider, CacheKEKAlias)
	if err != nil {
		return nil, fmt.Errorf("tokenservice/cache: envelope: %w", err)
	}
	return &Cache{client: client, cipher: cipher}, nil
}

// Put writes the claims under `lenny:token:{jti}` with a TTL equal to
// the remaining lifetime of the access token (claims.Expiry - now).
// A non-positive TTL is treated as a no-op: the token has already
// expired so caching it would only waste a Redis key. A nil receiver
// is a no-op.
//
// spec: §4.3 line 201.
func (c *Cache) Put(ctx context.Context, jti string, claims jwt.Claims) error {
	if c == nil {
		return nil
	}
	if jti == "" {
		return errors.New("tokenservice/cache: jti is empty")
	}
	ttl := remainingTTL(claims)
	if ttl <= 0 {
		return nil
	}
	plain, err := json.Marshal(claims)
	if err != nil {
		return fmt.Errorf("tokenservice/cache: marshal claims: %w", err)
	}
	sealed, err := c.cipher.Seal(ctx, plain)
	if err != nil {
		return fmt.Errorf("tokenservice/cache: seal: %w", err)
	}
	blob, err := envelope.Encode(sealed)
	if err != nil {
		return fmt.Errorf("tokenservice/cache: encode sealed: %w", err)
	}
	if err := c.client.Set(ctx, keyPrefix+jti, blob, ttl).Err(); err != nil {
		return fmt.Errorf("tokenservice/cache: redis SET: %w", err)
	}
	return nil
}

// Get reads the cached claims for jti, or ErrNotFound when the cache
// has no entry. A nil receiver returns ErrNotFound — callers fall
// through to the authoritative Postgres lookup transparently.
//
// spec: §4.3 line 201.
func (c *Cache) Get(ctx context.Context, jti string) (jwt.Claims, error) {
	if c == nil || jti == "" {
		return jwt.Claims{}, ErrNotFound
	}
	raw, err := c.client.Get(ctx, keyPrefix+jti).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return jwt.Claims{}, ErrNotFound
		}
		return jwt.Claims{}, fmt.Errorf("tokenservice/cache: redis GET: %w", err)
	}
	sealed, err := envelope.Decode(raw)
	if err != nil {
		return jwt.Claims{}, fmt.Errorf("tokenservice/cache: decode sealed: %w", err)
	}
	plain, err := c.cipher.Open(ctx, sealed)
	if err != nil {
		return jwt.Claims{}, fmt.Errorf("tokenservice/cache: open: %w", err)
	}
	var claims jwt.Claims
	if err := json.Unmarshal(plain, &claims); err != nil {
		return jwt.Claims{}, fmt.Errorf("tokenservice/cache: unmarshal claims: %w", err)
	}
	return claims, nil
}

// Invalidate removes the cached entry for jti. Called on the
// revocation path so a revoked token never reads back from the cache.
// A nil receiver is a no-op. Returns nil for an absent key (Redis DEL
// of a non-existent key is success-with-zero-affected).
//
// spec: §4.3 line 201, §13.3 revocation.
func (c *Cache) Invalidate(ctx context.Context, jti string) error {
	if c == nil || jti == "" {
		return nil
	}
	if err := c.client.Del(ctx, keyPrefix+jti).Err(); err != nil {
		return fmt.Errorf("tokenservice/cache: redis DEL: %w", err)
	}
	return nil
}

// remainingTTL computes time-until-expiry for the supplied claims.
// Zero or past expiry returns 0; callers treat 0 as "do not cache".
func remainingTTL(c jwt.Claims) time.Duration {
	if c.Expiry == 0 {
		return 0
	}
	exp := time.Unix(c.Expiry, 0).UTC()
	d := time.Until(exp)
	if d <= 0 {
		return 0
	}
	return d
}
