// SPDX-License-Identifier: MIT

// Package cachingstore wraps the Redis-backed §11.6 circuit-breaker
// registry with an in-process open-breaker cache. The breaker
// middleware reads the open-breaker set on every request; serving it
// from a local cache removes a Redis round-trip from the hot path and
// lets the gateway keep admitting requests during a Redis outage.
//
// The cache is kept current two ways: a Redis pub/sub subscription
// refreshes it on every Open or Close from any replica, and a periodic
// refresh bounds staleness when a pub/sub message is dropped (Redis
// pub/sub is at-most-once). When a refresh fails the previous snapshot
// is retained: §11.7 permits serving a breaker snapshot up to 5
// seconds stale while Redis is unavailable.
package cachingstore

import (
	"context"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/circuitbreaker"
	"github.com/lennylabs/lenny/pkg/gateway/breakerstore"
	cbmw "github.com/lennylabs/lenny/pkg/gateway/middleware/circuitbreaker"
)

// channel is the Redis pub/sub channel circuit-breaker changes are
// announced on.
const channel = "cb:events"

// DefaultRefreshInterval bounds cache staleness when a pub/sub message
// is missed. It sits well inside the §11.7 5-second staleness budget.
const DefaultRefreshInterval = 2 * time.Second

// Registry is the §11.6 store the cache reads through to. The
// Redis-backed breakerstore satisfies it.
type Registry interface {
	breakerstore.Store
	cbmw.Registry
}

// Store wraps a Registry with an in-process open-breaker cache.
type Store struct {
	inner    Registry
	client   redis.UniversalClient
	interval time.Duration

	mu          sync.RWMutex
	open        []circuitbreaker.Breaker
	lastRefresh time.Time
}

// New returns a cache over inner. client carries the pub/sub fan-out;
// inner carries the actual reads and writes.
func New(inner Registry, client redis.UniversalClient) *Store {
	return &Store{inner: inner, client: client, interval: DefaultRefreshInterval}
}

var (
	_ breakerstore.Store = (*Store)(nil)
	_ cbmw.Registry      = (*Store)(nil)
)

// Open writes through to the registry, announces the change so peer
// replicas refresh, and updates the local cache.
func (s *Store) Open(ctx context.Context, b circuitbreaker.Breaker) (circuitbreaker.Breaker, error) {
	out, err := s.inner.Open(ctx, b)
	if err != nil {
		return out, err
	}
	s.announce()
	_ = s.Refresh(context.Background())
	return out, nil
}

// Close writes through to the registry, announces the change, and
// updates the local cache.
func (s *Store) Close(ctx context.Context, name string) (circuitbreaker.Breaker, error) {
	out, err := s.inner.Close(ctx, name)
	if err != nil {
		return out, err
	}
	s.announce()
	_ = s.Refresh(context.Background())
	return out, nil
}

// Get reads through to the registry. Admin reads want fresh data and
// are not on the request hot path, so they are not cached.
func (s *Store) Get(ctx context.Context, name string) (circuitbreaker.Breaker, error) {
	return s.inner.Get(ctx, name)
}

// List reads through to the registry.
func (s *Store) List(ctx context.Context) ([]circuitbreaker.Breaker, error) {
	return s.inner.List(ctx)
}

// Snapshot returns the cached open-breaker set. It never blocks on
// Redis: the request-path breaker check is served from the in-process
// cache so a Redis outage cannot stall request admission.
func (s *Store) Snapshot(context.Context) ([]circuitbreaker.Breaker, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]circuitbreaker.Breaker, len(s.open))
	copy(out, s.open)
	return out, nil
}

// Refresh reloads the open-breaker set from the registry. On error the
// previous snapshot is retained so a Redis outage degrades to stale
// reads rather than an empty set.
func (s *Store) Refresh(ctx context.Context) error {
	open, err := s.inner.Snapshot(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.open = open
	s.lastRefresh = time.Now()
	s.mu.Unlock()
	return nil
}

// LastRefresh reports when the cache was last reconciled with the
// registry. The §25.3 health service compares it against the §11.7
// 5-second staleness budget to mark the dependency degraded.
func (s *Store) LastRefresh() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastRefresh
}

// Run keeps the cache current until ctx is cancelled. It subscribes to
// the pub/sub channel and refreshes on every announced change; a
// ticker refreshes periodically so a dropped message cannot leave the
// cache stale beyond the refresh interval.
func (s *Store) Run(ctx context.Context) {
	_ = s.Refresh(ctx)
	pubsub := s.client.Subscribe(ctx, channel)
	defer func() { _ = pubsub.Close() }()
	messages := pubsub.Channel()
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = s.Refresh(ctx)
		case <-messages:
			_ = s.Refresh(ctx)
		}
	}
}

// announce publishes a change notification so peer replicas refresh
// promptly. A dropped publish is tolerated: the periodic refresh
// reconciles within the interval.
func (s *Store) announce() {
	_ = s.client.Publish(context.Background(), channel, "changed").Err()
}
