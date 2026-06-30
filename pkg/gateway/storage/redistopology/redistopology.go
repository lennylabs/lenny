// SPDX-License-Identifier: MIT

// Package redistopology builds the per-concern Redis clients that
// implement the §12.4 "Logical separation of Redis concerns"
// deployment-time split. An operator supplies a separate connection
// string per store role (Coordination, Quota/Rate Limiting,
// Cache/Pub-Sub, ...); this package constructs one client per distinct
// URL and hands each concern its dedicated client. A concern with no
// dedicated URL falls back to the shared base client, so the single
// Sentinel topology used at Tiers 1 and 2 needs no per-concern URLs at
// all.
//
// The split is the only code this package owns; the spec's claim that
// "no code changes are required because each store role already has its
// own interface" holds because every store accepts a
// redis.UniversalClient at construction. The gateway resolves each
// store's client through Clients.For at wiring time.
//
// spec: §12.4 lines 237-245 (logical separation of Redis concerns).
package redistopology

import (
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/redisconn"
	"github.com/lennylabs/lenny/pkg/storerouter"
)

// Concerns is the set of §12.4 RedisConcern roles an operator can split
// onto separate instances.
var Concerns = []storerouter.RedisConcern{
	storerouter.RedisConcernCoordination,
	storerouter.RedisConcernQuota,
	storerouter.RedisConcernCachePubSub,
	storerouter.RedisConcernSessionData,
	storerouter.RedisConcernDelegation,
}

// Clients holds the shared base Redis client and the per-concern split.
// The zero value (and a nil *Clients) is safe: For returns nil and
// ByConcern returns nil, matching a Postgres-only / in-memory
// deployment with no Redis at all.
type Clients struct {
	base      redis.UniversalClient
	byConcern map[storerouter.RedisConcern]redis.UniversalClient // overrides only
	built     []redis.UniversalClient                            // clients this package created
}

// Build constructs the per-concern clients. base is the already-built
// shared client (the --redis-url / Sentinel / Cluster client) and is
// never closed by Clients — the caller owns it. perConcernURLs maps a
// concern to a dedicated Redis URL; an absent or empty URL routes that
// concern to base. template carries the AUTH/TLS settings (password,
// TLS, allow-insecure) applied to each per-concern URL. When base is
// nil (no Redis configured) Build returns an empty Clients regardless
// of perConcernURLs.
//
// Two concerns that name the same URL share a single client (one
// connection pool, one Guard install).
func Build(base redis.UniversalClient, perConcernURLs map[storerouter.RedisConcern]string, template redisconn.Config) (*Clients, error) {
	c := &Clients{base: base, byConcern: map[storerouter.RedisConcern]redis.UniversalClient{}}
	if base == nil {
		return c, nil
	}
	builtByURL := map[string]redis.UniversalClient{}
	for _, concern := range Concerns {
		url := perConcernURLs[concern]
		if url == "" {
			continue // falls back to base via For/RedisShard
		}
		if existing, ok := builtByURL[url]; ok {
			c.byConcern[concern] = existing
			continue
		}
		cfg := template
		cfg.URL = url
		// A per-concern URL is always a direct address; clear the
		// Sentinel/Cluster fields the base template may carry so a
		// concern URL is taken at face value.
		cfg.SentinelAddrs = nil
		cfg.ClusterAddrs = nil
		client, err := redisconn.NewClient(cfg)
		if err != nil {
			_ = c.Close()
			return nil, fmt.Errorf("redistopology: concern %q: %w", concern, err)
		}
		builtByURL[url] = client
		c.built = append(c.built, client)
		c.byConcern[concern] = client
	}
	return c, nil
}

// For returns the Redis client serving a concern: its dedicated client
// when the concern was split, otherwise the shared base client.
func (c *Clients) For(concern storerouter.RedisConcern) redis.UniversalClient {
	if c == nil {
		return nil
	}
	if cl, ok := c.byConcern[concern]; ok {
		return cl
	}
	return c.base
}

// ByConcern returns the concern→client override map for
// storerouter.Config.RedisByConcern. It is nil when no concern is
// split, so the router falls back to its single base client for every
// concern.
func (c *Clients) ByConcern() map[storerouter.RedisConcern]redis.UniversalClient {
	if c == nil || len(c.byConcern) == 0 {
		return nil
	}
	return c.byConcern
}

// Split reports whether any concern resolves to a dedicated client.
func (c *Clients) Split() bool { return c != nil && len(c.byConcern) > 0 }

// Close closes every client this package built. The base client is
// owned by the caller and is left open.
func (c *Clients) Close() error {
	if c == nil {
		return nil
	}
	var errs []error
	for _, cl := range c.built {
		if err := cl.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
