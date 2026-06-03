// SPDX-License-Identifier: MIT

package opsservice

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lennylabs/lenny/pkg/ops/eventsubscription"
)

// SecretResolver supplies the plaintext HMAC signing secret for a
// subscription to the §25.5 delivery worker. The store persists only the
// SHA-256 hash, so the plaintext is recovered from an in-memory reveal
// cache populated when the secret is generated (create/rotate). spec:
// §25.5 lines 2715-2733, 2747-2756.
type SecretResolver interface {
	// Secret returns the plaintext signing secret for subID, or ok=false
	// when none is cached (the secret was generated on another replica, or
	// before this process started). A subscription with no cached secret
	// is still delivered, unsigned, so a receiver that does not validate
	// the signature still gets the event.
	Secret(subID string) ([]byte, bool)
	// Retain drops every cached secret whose subscription id is not in
	// activeIDs, so a deleted subscription's secret does not linger.
	Retain(activeIDs []string)
}

// SubscriptionCache is the production §25.5 SubscriptionSource adapter:
// it bridges the eventsubscription.Store API (which is request-scoped
// with a context and an error return) to the synchronous
// webhookloop.SubscriptionSource interface the worker reads on every
// reconcile.
//
// The cache holds the last successful List result; the worker reads
// from the cache so a brief Postgres outage does not stop delivery
// against the last-known subscription set. A background refresh updates
// the cache at RefreshInterval until Stop is called, and the
// subscription_cache_invalidate RPC (or an in-process CRUD) triggers an
// immediate Invalidate so a change propagates within a few hundred
// milliseconds rather than at the next periodic refresh.
//
// spec: §25.5 lines 2747-2756.
type SubscriptionCache struct {
	store           eventsubscription.Store
	refreshInterval time.Duration
	logger          *slog.Logger
	secrets         SecretResolver
	onAvailability  func(available bool)

	mu   sync.RWMutex
	subs []WebhookSubscription
	gens map[string]int64

	available  atomic.Bool
	determined atomic.Bool

	stop   chan struct{}
	closed bool
	closeM sync.Mutex
}

// SubscriptionCacheConfig configures a SubscriptionCache.
type SubscriptionCacheConfig struct {
	// Store is the subscription registry the cache reads from. Required.
	Store eventsubscription.Store
	// RefreshInterval is the cache refresh period. A zero value defaults
	// to 60 seconds, the §25.5 line 2752 periodic-refresh cadence.
	RefreshInterval time.Duration
	// Logger receives cache-refresh failure logs. A nil logger uses
	// slog.Default.
	Logger *slog.Logger
	// Secrets resolves the plaintext signing secret for each subscription.
	// A nil resolver delivers every webhook unsigned.
	Secrets SecretResolver
	// OnAvailabilityChange, when non-nil, is invoked whenever the cache's
	// Postgres availability changes, including the cold-start
	// determination. The §25.5 line 2753 cold-start contract uses it to
	// emit ops_health_status_changed with subscriptionsUnavailable when
	// the cache cannot load on startup, and again when Postgres recovers.
	OnAvailabilityChange func(available bool)
}

// NewSubscriptionCache returns a started SubscriptionCache. The cache
// performs one synchronous refresh before returning so the worker reads
// a populated cache on its first reconcile; subsequent refreshes run
// in a background goroutine until Stop is called.
func NewSubscriptionCache(ctx context.Context, cfg SubscriptionCacheConfig) *SubscriptionCache {
	if cfg.RefreshInterval <= 0 {
		cfg.RefreshInterval = 60 * time.Second
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	c := &SubscriptionCache{
		store:           cfg.Store,
		refreshInterval: cfg.RefreshInterval,
		logger:          logger,
		secrets:         cfg.Secrets,
		onAvailability:  cfg.OnAvailabilityChange,
		gens:            map[string]int64{},
		stop:            make(chan struct{}),
	}
	// Best-effort initial load; the cache stays empty on failure so the
	// worker can still come up. A failed cold start signals
	// subscriptionsUnavailable via OnAvailabilityChange.
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

// Current implements webhookloop.GenerationChecker: it reports whether
// subID is still an active subscription at the given generation. The
// worker calls it immediately before each delivery so a subscription
// deleted or updated since the tick snapshot is skipped. spec: §25.5
// line 2751.
func (c *SubscriptionCache) Current(subID string, generation int64) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	g, ok := c.gens[subID]
	return ok && g == generation
}

// Available reports whether the most recent refresh succeeded. The
// §25.5 line 2753 cold-start path reads false while Postgres is down.
func (c *SubscriptionCache) Available() bool { return c.available.Load() }

// Refresh forces a synchronous refresh against the Store. The error is
// returned so callers (admin tools, tests) can observe immediate
// failures; the background loop logs and continues.
func (c *SubscriptionCache) Refresh(ctx context.Context) error {
	return c.refresh(ctx)
}

// Invalidate forces an immediate refresh. The §25.5
// subscription_cache_invalidate RPC and the in-process CRUD hook both
// call it so a subscription change reaches the delivery worker within a
// few hundred milliseconds. spec: §25.5 lines 2751, 2756.
func (c *SubscriptionCache) Invalidate(ctx context.Context) error {
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
		c.setAvailable(false)
		return err
	}
	subs := make([]WebhookSubscription, 0, len(rows))
	gens := make(map[string]int64, len(rows))
	activeIDs := make([]string, 0, len(rows))
	for _, r := range rows {
		if !r.Active {
			continue
		}
		gens[r.ID] = r.Generation
		activeIDs = append(activeIDs, r.ID)
		sub := WebhookSubscription{
			ID:          r.ID,
			CallbackURL: r.CallbackURL,
			Types:       append([]string(nil), r.Types...),
			Generation:  r.Generation,
		}
		// §25.5 recovers the plaintext signing key from the in-memory reveal
		// cache populated at create/rotate; the store keeps only the hash.
		if c.secrets != nil {
			if secret, ok := c.secrets.Secret(r.ID); ok {
				sub.Secret = secret
			}
		}
		subs = append(subs, sub)
	}
	c.mu.Lock()
	c.subs = subs
	c.gens = gens
	c.mu.Unlock()
	if c.secrets != nil {
		c.secrets.Retain(activeIDs)
	}
	c.setAvailable(true)
	return nil
}

// setAvailable records the latest Postgres availability and fires
// OnAvailabilityChange on the cold-start determination and on every
// subsequent transition. spec: §25.5 line 2753.
func (c *SubscriptionCache) setAvailable(available bool) {
	prev := c.available.Swap(available)
	first := !c.determined.Swap(true)
	if (first || prev != available) && c.onAvailability != nil {
		c.onAvailability(available)
	}
}

var _ SubscriptionSource = (*SubscriptionCache)(nil)
var _ GenerationChecker = (*SubscriptionCache)(nil)
