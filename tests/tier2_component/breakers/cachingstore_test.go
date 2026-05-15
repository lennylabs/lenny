//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §11.6 circuit-breaker caching layer,
// exercising pkg/gateway/breakerstore/cachingstore against a real
// Redis container. Covers pub/sub propagation of opens and closes
// across replicas and the periodic-refresh fallback that reconciles a
// change whose pub/sub message was missed.
package breakers_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/breakerstore/cachingstore"
	"github.com/lennylabs/lenny/pkg/gateway/breakerstore/redisstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// waitFor polls cond until it holds or the timeout elapses.
func waitFor(t *testing.T, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for: %s", timeout, desc)
}

// hasOpen reports whether the cache's open-breaker snapshot names the
// given breaker.
func hasOpen(store *cachingstore.Store, name string) bool {
	snap, _ := store.Snapshot(context.Background())
	for _, b := range snap {
		if b.Name == name {
			return true
		}
	}
	return false
}

func TestBreakerCacheContract(t *testing.T) {
	t.Parallel()
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// Two cachingstores over the same Redis stand in for two gateway
	// replicas: a change on one must reach the other.
	cacheA := cachingstore.New(redisstore.New(rd.Client), rd.Client)
	cacheB := cachingstore.New(redisstore.New(rd.Client), rd.Client)
	go cacheA.Run(ctx)
	go cacheB.Run(ctx)

	t.Run("an open propagates to peer replicas", func(t *testing.T) {
		name := "prop-open-" + randHex(t)
		if _, err := cacheA.Open(ctx, runtimeBreaker(name, "echo")); err != nil {
			t.Fatalf("Open: %v", err)
		}
		waitFor(t, 5*time.Second, "replica B to see the open breaker", func() bool {
			return hasOpen(cacheB, name)
		})
	})

	t.Run("a close propagates to peer replicas", func(t *testing.T) {
		name := "prop-close-" + randHex(t)
		if _, err := cacheA.Open(ctx, runtimeBreaker(name, "echo")); err != nil {
			t.Fatalf("Open: %v", err)
		}
		waitFor(t, 5*time.Second, "replica B to see the open breaker", func() bool {
			return hasOpen(cacheB, name)
		})
		if _, err := cacheA.Close(ctx, name); err != nil {
			t.Fatalf("Close: %v", err)
		}
		waitFor(t, 5*time.Second, "replica B to drop the closed breaker", func() bool {
			return !hasOpen(cacheB, name)
		})
	})

	t.Run("the periodic refresh reconciles a missed change", func(t *testing.T) {
		// Write straight to the registry, bypassing the cachingstore
		// pub/sub announce, so only the periodic refresh can observe it.
		name := "missed-" + randHex(t)
		direct := redisstore.New(rd.Client)
		if _, err := direct.Open(ctx, runtimeBreaker(name, "echo")); err != nil {
			t.Fatalf("direct Open: %v", err)
		}
		waitFor(t, 5*time.Second, "the periodic refresh to pick up the missed open", func() bool {
			return hasOpen(cacheB, name)
		})
	})
}
