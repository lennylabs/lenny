// SPDX-License-Identifier: MIT

// Package redisconn constructs the Redis client used by the gateway
// and the lenny-ops binary. It accepts either a direct Redis URL or a
// §12.8 Sentinel-failover configuration (sentinel addrs + master name)
// and returns a *redis.Client wired through go-redis. Callers downstream
// see the same redis.UniversalClient surface regardless of which path
// produced it, so a topology change rotates only this constructor.
//
// The Sentinel path uses go-redis's NewFailoverClient, which discovers
// the current master via the configured sentinels on every reconnect.
// A Sentinel quorum that promotes a replica during a master outage
// is therefore transparent to callers — they keep reading through the
// same handle.
package redisconn

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Config configures NewClient.
type Config struct {
	// URL is the direct Redis connection string
	// (`redis://host:port/db`). Mutually exclusive with the Sentinel
	// fields below; a non-empty URL forces direct mode.
	URL string

	// SentinelAddrs is the list of `host:port` pairs for the §12.8
	// Sentinel topology. When non-empty (and URL is empty), the
	// constructor uses go-redis NewFailoverClient and discovers the
	// master via these sentinels.
	SentinelAddrs []string

	// MasterName is the Sentinel monitor name (e.g., `lenny-master`).
	// Required when SentinelAddrs is set.
	MasterName string

	// Password is the Redis AUTH password applied to both direct and
	// Sentinel modes. Sentinel mode uses the same password against
	// the data-plane Redis (Sentinel itself can use SentinelPassword
	// below).
	Password string

	// SentinelPassword is the AUTH password for the sentinels
	// themselves. Optional; sentinels usually run without auth.
	SentinelPassword string

	// DB selects the Redis logical database. Default 0.
	DB int
}

// Errors returned by NewClient.
var (
	// ErrNoSource reports that neither URL nor SentinelAddrs was set.
	// The caller's deployment is misconfigured.
	ErrNoSource = errors.New("redisconn: neither URL nor SentinelAddrs is set")
	// ErrMissingMasterName reports that SentinelAddrs is set but
	// MasterName is empty — Sentinel discovery cannot run without it.
	ErrMissingMasterName = errors.New("redisconn: SentinelAddrs requires MasterName")
)

// NewClient returns a Redis client built from cfg. The returned
// *redis.Client satisfies redis.UniversalClient and is closed by the
// caller. The constructor does not ping; pinging surfaces a real
// dial error at the boundary the caller chose, which keeps the
// dial latency observable.
func NewClient(cfg Config) (*redis.Client, error) {
	switch {
	case cfg.URL != "":
		opts, err := redis.ParseURL(cfg.URL)
		if err != nil {
			return nil, fmt.Errorf("redisconn: parse url: %w", err)
		}
		if cfg.Password != "" {
			opts.Password = cfg.Password
		}
		if cfg.DB != 0 {
			opts.DB = cfg.DB
		}
		return redis.NewClient(opts), nil
	case len(cfg.SentinelAddrs) > 0:
		if cfg.MasterName == "" {
			return nil, ErrMissingMasterName
		}
		return redis.NewFailoverClient(&redis.FailoverOptions{
			MasterName:       cfg.MasterName,
			SentinelAddrs:    cfg.SentinelAddrs,
			SentinelPassword: cfg.SentinelPassword,
			Password:         cfg.Password,
			DB:               cfg.DB,
		}), nil
	default:
		return nil, ErrNoSource
	}
}

// PingWithTimeout pings the client with the supplied deadline. It is
// a convenience for the main-binary boot path: the gateway and
// lenny-ops both ping after constructing the client to surface a
// configuration error at startup rather than on the first request.
func PingWithTimeout(client *redis.Client, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return client.Ping(ctx).Err()
}
