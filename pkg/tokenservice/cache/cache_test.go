// SPDX-License-Identifier: MIT

package cache_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/auth/jwt"
	"github.com/lennylabs/lenny/pkg/kms"
	tokencache "github.com/lennylabs/lenny/pkg/tokenservice/cache"
)

// newCache returns a Redis-backed token cache wired to a fresh
// miniredis instance for the test scope. The cleanup function closes
// the client and the miniredis process automatically.
func newCache(t *testing.T) (*tokencache.Cache, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	cl := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = cl.Close() })
	provider, err := kms.NewLocalRandom()
	if err != nil {
		t.Fatalf("kms.NewLocalRandom: %v", err)
	}
	c, err := tokencache.New(cl, provider)
	if err != nil {
		t.Fatalf("tokencache.New: %v", err)
	}
	return c, mr
}

// spec: §4.3 line 201 — Put then Get round-trips the claims through the
// envelope-encrypted cache.
func TestPutGetRoundtrip(t *testing.T) {
	c, _ := newCache(t)
	claims := jwt.Claims{
		Subject:  "alice@acme.com",
		TenantID: "acme",
		Audience: []string{"lenny-gateway"},
		Expiry:   time.Now().Add(time.Hour).Unix(),
		IssuedAt: time.Now().Unix(),
		JWTID:    "jti_1",
	}
	if err := c.Put(context.Background(), "jti_1", claims); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := c.Get(context.Background(), "jti_1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Subject != "alice@acme.com" || got.TenantID != "acme" {
		t.Errorf("Get returned the wrong claims: %+v", got)
	}
	if got.JWTID != "jti_1" {
		t.Errorf("JWTID = %q, want jti_1", got.JWTID)
	}
}

// spec: §4.3 line 201 — Get on a missing jti returns ErrNotFound so
// callers fall through to the authoritative Postgres lookup.
func TestGetMissingReturnsErrNotFound(t *testing.T) {
	c, _ := newCache(t)
	_, err := c.Get(context.Background(), "absent")
	if !errors.Is(err, tokencache.ErrNotFound) {
		t.Fatalf("Get of missing key: got %v, want ErrNotFound", err)
	}
}

// spec: §4.3 line 201 — Invalidate removes the entry; a subsequent
// Get falls back to the authoritative store.
func TestInvalidateRemovesEntry(t *testing.T) {
	c, _ := newCache(t)
	claims := jwt.Claims{
		Subject: "alice@acme.com",
		Expiry:  time.Now().Add(time.Hour).Unix(),
		JWTID:   "jti_2",
	}
	if err := c.Put(context.Background(), "jti_2", claims); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := c.Invalidate(context.Background(), "jti_2"); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if _, err := c.Get(context.Background(), "jti_2"); !errors.Is(err, tokencache.ErrNotFound) {
		t.Fatalf("Get after Invalidate: got %v, want ErrNotFound", err)
	}
}

// spec: §4.3 line 201 — Invalidate of an absent jti is a no-op (DEL
// against a missing key is harmless).
func TestInvalidateMissingIsNoop(t *testing.T) {
	c, _ := newCache(t)
	if err := c.Invalidate(context.Background(), "never-existed"); err != nil {
		t.Fatalf("Invalidate of absent jti: %v", err)
	}
}

// spec: §4.3 line 201 — empty jti is rejected (Put returns an error
// without contacting Redis).
func TestPutEmptyJTIRejected(t *testing.T) {
	c, _ := newCache(t)
	err := c.Put(context.Background(), "", jwt.Claims{Expiry: time.Now().Add(time.Hour).Unix()})
	if err == nil {
		t.Fatalf("Put with empty jti: want error, got nil")
	}
}

// spec: §4.3 line 201 — Put of claims with no Expiry is a no-op so a
// non-expiring or already-expired token does not waste a key.
func TestPutExpiredClaimsIsNoop(t *testing.T) {
	c, mr := newCache(t)
	// Already-expired claims.
	expired := jwt.Claims{Expiry: time.Now().Add(-time.Hour).Unix(), JWTID: "jti_x"}
	if err := c.Put(context.Background(), "jti_x", expired); err != nil {
		t.Fatalf("Put expired: %v", err)
	}
	if mr.Exists("lenny:token:jti_x") {
		t.Errorf("expired claims wrote a Redis key")
	}
}

// spec: §4.3 line 201 — TTL of the entry matches the claim's remaining
// lifetime; an entry past its TTL no longer reads.
func TestEntryRespectsTTL(t *testing.T) {
	c, mr := newCache(t)
	claims := jwt.Claims{
		Subject: "alice@acme.com",
		Expiry:  time.Now().Add(2 * time.Second).Unix(),
		JWTID:   "jti_ttl",
	}
	if err := c.Put(context.Background(), "jti_ttl", claims); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if !mr.Exists("lenny:token:jti_ttl") {
		t.Fatalf("expected the cache key to exist immediately after Put")
	}
	mr.FastForward(10 * time.Second)
	if mr.Exists("lenny:token:jti_ttl") {
		t.Errorf("cache key survived past its TTL")
	}
	if _, err := c.Get(context.Background(), "jti_ttl"); !errors.Is(err, tokencache.ErrNotFound) {
		t.Errorf("Get after TTL: got %v, want ErrNotFound", err)
	}
}

// spec: §4.3 line 201 — a nil *Cache is callable: every method is a
// no-op so a caller can pass nil to disable caching at the call site
// (the "degrade to direct-Postgres when no Redis" path).
func TestNilCacheIsSafe(t *testing.T) {
	var c *tokencache.Cache
	if err := c.Put(context.Background(), "jti", jwt.Claims{}); err != nil {
		t.Errorf("nil-cache Put: %v", err)
	}
	if err := c.Invalidate(context.Background(), "jti"); err != nil {
		t.Errorf("nil-cache Invalidate: %v", err)
	}
	if _, err := c.Get(context.Background(), "jti"); !errors.Is(err, tokencache.ErrNotFound) {
		t.Errorf("nil-cache Get: got %v, want ErrNotFound", err)
	}
}

// spec: §4.3 line 201 — New rejects a nil provider (KMS-envelope
// encryption is mandatory for the cache).
func TestNewRejectsNilKMS(t *testing.T) {
	mr := miniredis.RunT(t)
	cl := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = cl.Close() })
	if _, err := tokencache.New(cl, nil); err == nil {
		t.Fatalf("New with nil KMS provider: want error, got nil")
	}
}

// spec: §4.3 line 201 — New rejects a nil Redis client.
func TestNewRejectsNilClient(t *testing.T) {
	provider, _ := kms.NewLocalRandom()
	if _, err := tokencache.New(nil, provider); err == nil {
		t.Fatalf("New with nil client: want error, got nil")
	}
}
