// SPDX-License-Identifier: MIT

package opsservice

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/eventsubscription"
)

// SubscriptionCache is the production §25.5 SubscriptionSource adapter:
// it bridges the eventsubscription.Store API (which is request-scoped
// with a context and an error return) to the synchronous
// webhookloop.SubscriptionSource interface the worker reads on every
// reconcile.
//
// The cache holds the last successful List result; the worker reads
// from the cache so a brief Postgres outage does not stop delivery
// against the last-known subscription set. A background refresh
// updates the cache at RefreshInterval until Stop is called.
type SubscriptionCache struct {
	store           eventsubscription.Store
	refreshInterval time.Duration
	logger          *slog.Logger

	mu   sync.RWMutex
	subs []WebhookSubscription

	stop   chan struct{}
	closed bool
	closeM sync.Mutex
}

// SubscriptionCacheConfig configures a SubscriptionCache.
type SubscriptionCacheConfig struct {
	// Store is the subscription registry the cache reads from. Required.
	Store eventsubscription.Store
	// RefreshInterval is the cache refresh period. A zero value defaults
	// to 30 seconds, matching the §25.5 worker reconcile cadence.
	RefreshInterval time.Duration
	// Logger receives cache-refresh failure logs. A nil logger uses
	// slog.Default.
	Logger *slog.Logger
}

// NewSubscriptionCache returns a started SubscriptionCache. The cache
// performs one synchronous refresh before returning so the worker reads
// a populated cache on its first reconcile; subsequent refreshes run
// in a background goroutine until Stop is called.
func NewSubscriptionCache(ctx context.Context, cfg SubscriptionCacheConfig) *SubscriptionCache {
	if cfg.RefreshInterval <= 0 {
		cfg.RefreshInterval = 30 * time.Second
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	c := &SubscriptionCache{
		store:           cfg.Store,
		refreshInterval: cfg.RefreshInterval,
		logger:          logger,
		stop:            make(chan struct{}),
	}
	// Best-effort initial load; the cache stays empty on failure so
	// the worker can still come up.
	c.refresh(ctx)
	go c.loop()
	return c
}

// Subscriptions returns the cached subscription set.
func (c *SubscriptionCache) Subscriptions() []WebhookSubscription {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]WebhookSubscription, len(c.subs))
	copy(out, c.subs)
	return out
}

// Refresh forces a synchronous refresh against the Store. The error is
// returned so callers (admin tools, tests) can observe immediate
// failures; the background loop logs and continues.
func (c *SubscriptionCache) Refresh(ctx context.Context) error {
	return c.refresh(ctx)
}

// Stop terminates the background refresh loop. Subscriptions remain
// readable from the last successful refresh after Stop returns.
func (c *SubscriptionCache) Stop() {
	c.closeM.Lock()
	defer c.closeM.Unlock()
	if c.closed {
		return
	}
	close(c.stop)
	c.closed = true
}

func (c *SubscriptionCache) loop() {
	t := time.NewTicker(c.refreshInterval)
	defer t.Stop()
	for {
		select {
		case <-c.stop:
			return
		case <-t.C:
			ctx, cancel := context.WithTimeout(context.Background(), c.refreshInterval)
			if err := c.refresh(ctx); err != nil {
				c.logger.Warn("ops subscription cache refresh failed", "error", err)
			}
			cancel()
		}
	}
}

func (c *SubscriptionCache) refresh(ctx context.Context) error {
	rows, err := c.store.List(ctx)
	if err != nil {
		return err
	}
	subs := make([]WebhookSubscription, 0, len(rows))
	for _, r := range rows {
		if !r.Active {
			continue
		}
		subs = append(subs, WebhookSubscription{
			ID:          r.ID,
			CallbackURL: r.CallbackURL,
			// §25.5 stores the secret as a SHA-256 hash at rest, never the
			// plaintext. The worker recovers the plaintext signing key from
			// the in-memory reveal cache populated at create/rotate; that
			// retention is the F-25.5.13 cache subsystem. Until it lands the
			// store-backed reload carries no signing key.
			Types: append([]string(nil), r.Types...),
		})
	}
	c.mu.Lock()
	c.subs = subs
	c.mu.Unlock()
	return nil
}
