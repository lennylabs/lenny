//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §4.3 Token Service Redis-backed access-token
// cache (pkg/tokenservice/cache) against a real Redis container rather
// than an in-memory miniredis. It verifies the observable at-rest
// contract the unit tests cannot: that the value written to Redis is
// envelope ciphertext (never the plaintext claims), that a real Redis
// PEXPIRE drives TTL-based expiry, and that a full Put/Get round-trip
// survives the real client-server hop.
package stores_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/kms/envelope"
	tokencache "github.com/lennylabs/lenny/pkg/tokenservice/cache"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// newTokenCache builds a §4.3 access-token cache backed by the supplied
// real Redis container and a fresh local KMS provider.
func newTokenCache(t *testing.T, rd *containers.Redis) *tokencache.Cache {
	t.Helper()
	provider, err := kms.NewLocalRandom()
	if err != nil {
		t.Fatalf("kms.NewLocalRandom: %v", err)
	}
	c, err := tokencache.New(rd.Client, provider)
	if err != nil {
		t.Fatalf("tokencache.New: %v", err)
	}
	return c
}

// spec: 4.3 (token service — "Access tokens short-lived, cached in
// Redis (encrypted, not plaintext)")
// diagnosis: the §4.3 access-token cache did not store envelope
// ciphertext at rest against a real Redis server, or a real Redis TTL
// did not expire the entry, or the round-trip through a real Redis
// client-server hop did not reconstruct the claims. A failure means a
// Redis compromise could yield plaintext access-token claims, or the
// cache does not respect the short-lived-token contract.
func TestTokenCacheEncryptedAtRestOnRealRedis(t *testing.T) {
	t.Parallel()
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx := context.Background()
	c := newTokenCache(t, rd)

	const (
		jti      = "jti_realredis_1"
		subject  = "alice@acme.com"
		tenant   = "acme"
		redisKey = "lenny:token:" + jti
	)
	claims := jwt.Claims{
		Subject:  subject,
		TenantID: tenant,
		Audience: []string{"lenny-gateway"},
		Expiry:   time.Now().Add(time.Hour).Unix(),
		IssuedAt: time.Now().Unix(),
		JWTID:    jti,
	}
	if err := c.Put(ctx, jti, claims); err != nil {
		t.Fatalf("Put: %v", err)
	}

	// The spec requires the cached value to be encrypted, not plaintext.
	// Read the raw stored bytes straight off the wire and assert none of
	// the plaintext claim material is recoverable, and that the blob is a
	// well-formed envelope record.
	raw, err := rd.Client.Get(ctx, redisKey).Bytes()
	if err != nil {
		t.Fatalf("raw GET of %s: %v", redisKey, err)
	}
	for _, marker := range [][]byte{[]byte(subject), []byte(tenant), []byte(jti), []byte("lenny-gateway"), []byte("\"sub\"")} {
		if bytes.Contains(raw, marker) {
			t.Errorf("stored value leaks plaintext %q; §4.3 requires encrypted-at-rest, got raw=%q", marker, raw)
		}
	}
	if _, err := envelope.Decode(raw); err != nil {
		t.Errorf("stored value is not a well-formed envelope record: %v", err)
	}

	// The round-trip through the real client-server hop reconstructs the
	// claims: envelope open plus JSON unmarshal recovers the subject and
	// tenant the cache was asked to store.
	got, err := c.Get(ctx, jti)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Subject != subject || got.TenantID != tenant || got.JWTID != jti {
		t.Errorf("round-trip claims = %+v, want subject=%s tenant=%s jti=%s", got, subject, tenant, jti)
	}
}

// spec: 4.3 (token service — "Access tokens short-lived, cached in
// Redis")
// diagnosis: a real Redis PEXPIRE did not drive TTL-based expiry of a
// cached access token. The unit tests only assert this against
// miniredis FastForward; this pins it to a live Redis server so a
// cached token cannot outlive its access-token lifetime.
func TestTokenCacheTTLExpiryOnRealRedis(t *testing.T) {
	t.Parallel()
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx := context.Background()
	c := newTokenCache(t, rd)

	const jti = "jti_ttl_realredis"
	claims := jwt.Claims{
		Subject: "bob@acme.com",
		Expiry:  time.Now().Add(1 * time.Second).Unix(),
		JWTID:   jti,
	}
	if err := c.Put(ctx, jti, claims); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// A positive TTL must be set on the key, tracking the token lifetime.
	ttl, err := rd.Client.PTTL(ctx, "lenny:token:"+jti).Result()
	if err != nil {
		t.Fatalf("PTTL: %v", err)
	}
	if ttl <= 0 {
		t.Fatalf("expected a positive TTL on the cache key, got %v", ttl)
	}

	// After the TTL elapses, the real Redis server must have evicted the
	// entry: a subsequent Get falls through with ErrNotFound.
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := c.Get(ctx, jti)
		if errors.Is(err, tokencache.ErrNotFound) {
			break
		}
		if err != nil {
			t.Fatalf("Get during expiry poll: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("cache entry survived past its TTL on real Redis")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
