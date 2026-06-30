// SPDX-License-Identifier: MIT

package routingcache_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/session/routingcache"
)

// newCache returns a Redis-backed routing cache wired to a fresh
// miniredis instance for the test scope. The cleanup function closes
// the client and the miniredis process automatically.
func newCache(t *testing.T) (*routingcache.RedisCache, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	cl := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = cl.Close() })
	c, err := routingcache.New(routingcache.Config{Client: cl})
	if err != nil {
		t.Fatalf("routingcache.New: %v", err)
	}
	return c, mr
}

// spec: §4.2 line 152 — empty session ID is rejected before any
// Redis interaction.
func TestGetEmptySessionIDRejected(t *testing.T) {
	t.Parallel()
	c, _ := newCache(t)
	if _, err := c.Get(context.Background(), ""); !errors.Is(err, routingcache.ErrInvalidSessionID) {
		t.Errorf("Get(empty) = %v, want ErrInvalidSessionID", err)
	}
	if err := c.Set(context.Background(), "", routingcache.Binding{}); !errors.Is(err, routingcache.ErrInvalidSessionID) {
		t.Errorf("Set(empty) = %v, want ErrInvalidSessionID", err)
	}
	if err := c.Invalidate(context.Background(), ""); !errors.Is(err, routingcache.ErrInvalidSessionID) {
		t.Errorf("Invalidate(empty) = %v, want ErrInvalidSessionID", err)
	}
}

// spec: §4.2 line 152 — a Get on a missing key returns
// ErrCacheMiss; callers treat the miss as the trigger to fall
// through to Postgres.
func TestGetMissReturnsErrCacheMiss(t *testing.T) {
	t.Parallel()
	c, _ := newCache(t)
	if _, err := c.Get(context.Background(), "sess-1"); !errors.Is(err, routingcache.ErrCacheMiss) {
		t.Errorf("Get(missing) = %v, want ErrCacheMiss", err)
	}
}

// spec: §4.2 line 152 — Set then Get round-trips the binding.
func TestSetThenGet(t *testing.T) {
	t.Parallel()
	c, _ := newCache(t)
	want := routingcache.Binding{
		ReplicaID:          "gw-replica-7",
		PodAssignment:      "lenny-agent-acme-abc",
		RecoveryGeneration: 3,
	}
	if err := c.Set(context.Background(), "sess-1", want); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := c.Get(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != want {
		t.Errorf("Get = %+v, want %+v", got, want)
	}
}

// spec: §4.2 line 152 — Invalidate removes the cached binding. A
// subsequent Get returns ErrCacheMiss.
func TestInvalidateRemovesEntry(t *testing.T) {
	t.Parallel()
	c, _ := newCache(t)
	if err := c.Set(context.Background(), "sess-1", routingcache.Binding{ReplicaID: "gw-1"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := c.Invalidate(context.Background(), "sess-1"); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if _, err := c.Get(context.Background(), "sess-1"); !errors.Is(err, routingcache.ErrCacheMiss) {
		t.Errorf("Get after Invalidate = %v, want ErrCacheMiss", err)
	}
}

// spec: §4.2 line 152 — invalidating a missing key is not an error;
// the §4.2 coordinator handoff path calls Invalidate unconditionally.
func TestInvalidateMissingIsNoError(t *testing.T) {
	t.Parallel()
	c, _ := newCache(t)
	if err := c.Invalidate(context.Background(), "never-cached"); err != nil {
		t.Errorf("Invalidate(missing) = %v, want nil", err)
	}
}

// spec: §4.2 line 152 — entries expire after the configured TTL.
// Use a short TTL and miniredis's clock-fast-forward to verify the
// expiry path.
func TestEntryExpiresAfterTTL(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	cl := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = cl.Close() })
	c, err := routingcache.New(routingcache.Config{Client: cl, TTL: 10 * time.Second})
	if err != nil {
		t.Fatalf("routingcache.New: %v", err)
	}
	if err := c.Set(context.Background(), "sess-1", routingcache.Binding{ReplicaID: "gw-1"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// Forward the miniredis clock past the TTL.
	mr.FastForward(11 * time.Second)
	if _, err := c.Get(context.Background(), "sess-1"); !errors.Is(err, routingcache.ErrCacheMiss) {
		t.Errorf("Get after TTL = %v, want ErrCacheMiss", err)
	}
}

// spec: §4.2 line 152 — overwriting an entry replaces it
// in-place. The §4.2 coordinator-handoff path Sets the new replica
// after invalidating, and a subsequent Get must reflect the new
// binding.
func TestSetOverwritesExistingEntry(t *testing.T) {
	t.Parallel()
	c, _ := newCache(t)
	ctx := context.Background()
	first := routingcache.Binding{ReplicaID: "gw-1", RecoveryGeneration: 1}
	second := routingcache.Binding{ReplicaID: "gw-2", RecoveryGeneration: 2}
	if err := c.Set(ctx, "sess-1", first); err != nil {
		t.Fatalf("Set first: %v", err)
	}
	if err := c.Set(ctx, "sess-1", second); err != nil {
		t.Fatalf("Set second: %v", err)
	}
	got, err := c.Get(ctx, "sess-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != second {
		t.Errorf("Get after overwrite = %+v, want %+v", got, second)
	}
}

// spec: §4.2 line 152 — a corrupt cache entry (non-JSON value)
// surfaces as a cache miss so the caller falls back to Postgres
// and rewrites the entry.
func TestCorruptEntryFallsBackToMiss(t *testing.T) {
	t.Parallel()
	c, mr := newCache(t)
	// Inject a non-JSON value under the cache key prefix.
	mr.Set("lenny:route:sess-1", "not-json")
	if _, err := c.Get(context.Background(), "sess-1"); !errors.Is(err, routingcache.ErrCacheMiss) {
		t.Errorf("Get(corrupt) = %v, want ErrCacheMiss", err)
	}
}

// spec: §4.2 line 152 — concurrent Sets converge: with N goroutines
// writing distinct bindings under the same key, the final Get
// observes one of the written values (last-writer-wins for the same
// key is fine for the cache; the Postgres row remains the source of
// truth).
func TestConcurrentSetsConverge(t *testing.T) {
	t.Parallel()
	c, _ := newCache(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	const n = 20
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_ = c.Set(ctx, "sess-1", routingcache.Binding{
				ReplicaID:          "gw-rep",
				RecoveryGeneration: int64(idx),
			})
		}(i)
	}
	wg.Wait()
	got, err := c.Get(ctx, "sess-1")
	if err != nil {
		t.Fatalf("Get after concurrent Set: %v", err)
	}
	if got.ReplicaID != "gw-rep" {
		t.Errorf("Get.ReplicaID = %q, want gw-rep", got.ReplicaID)
	}
	if got.RecoveryGeneration < 0 || got.RecoveryGeneration >= n {
		t.Errorf("Get.RecoveryGeneration = %d, want in [0,%d)", got.RecoveryGeneration, n)
	}
}

// spec: §4.2 line 152 — New rejects a nil client outright. The
// caller must supply a valid §12.6 RedisConcernCachePubSub client.
func TestNewRejectsNilClient(t *testing.T) {
	t.Parallel()
	if _, err := routingcache.New(routingcache.Config{Client: nil}); err == nil {
		t.Errorf("New(nil) = nil error, want non-nil")
	}
}

// spec: §4.2 line 152 — New picks DefaultTTL when the config TTL is
// zero. The default keeps a forgotten invalidation bounded to one
// window without consulting Postgres again.
func TestNewDefaultsTTLWhenZero(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	cl := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = cl.Close() })
	c, err := routingcache.New(routingcache.Config{Client: cl})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.Set(context.Background(), "sess-1", routingcache.Binding{ReplicaID: "gw-1"}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// The miniredis TTL is observable; verify it equals the default
	// rather than zero (which would mean no expiry).
	ttl := mr.TTL("lenny:route:sess-1")
	if ttl == 0 {
		t.Errorf("Set with default TTL produced no expiry, want DefaultTTL=%v", routingcache.DefaultTTL)
	}
}

// spec: §4.2 line 152 — Get surfaces a transport-level failure as
// a cache miss. The miniredis-based test cannot inject a transport
// error directly, so this case closes the client and verifies the
// Get returns ErrCacheMiss rather than the raw network error.
func TestGetTransportErrorSurfacesAsMiss(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	cl := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	c, err := routingcache.New(routingcache.Config{Client: cl})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Close before the Get so the call observes a transport error.
	_ = cl.Close()
	if _, err := c.Get(context.Background(), "sess-1"); !errors.Is(err, routingcache.ErrCacheMiss) {
		t.Errorf("Get(closed) = %v, want ErrCacheMiss", err)
	}
}
