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
	"crypto/tls"
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

	// ClusterAddrs is the list of `host:port` seed nodes for a Redis
	// Cluster topology. When non-empty, NewUniversalClient builds a
	// go-redis ClusterClient — the `CLUSTER KEYSLOT`-aware client the
	// §12.4 Tier 2→3 migration pre-plan requires for the Quota/Rate
	// Limiting instance. Cluster mode takes precedence over the URL and
	// Sentinel fields; NewClient (which returns a concrete *redis.Client)
	// ignores it, so a caller that needs cluster support must use
	// NewUniversalClient.
	//
	// spec: §12.4 line 264 — "The QuotaStore and rate limiter
	// implementations must use CLUSTER KEYSLOT-aware client libraries
	// (e.g., go-redis/v9 with cluster mode)".
	ClusterAddrs []string

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

	// TLS requests TLS on the Sentinel path (the data-plane Redis and
	// the sentinels themselves). The direct-URL path derives TLS from
	// the rediss:// scheme instead, so this field is ignored when URL
	// is set. When AUTH-and-TLS enforcement is active (AllowInsecure
	// is false) TLS is mandatory on the Sentinel path regardless of
	// this field; it exists so a dev deployment that has opted out of
	// enforcement can still dial a TLS-fronted sentinel topology.
	//
	// spec: §12.4 line 197 — Redis AUTH (ACLs) and TLS are required.
	TLS bool

	// AllowInsecure disables the §12.4 AUTH-and-TLS startup invariant.
	// The spec mandates AUTH and TLS on every Redis instance, so the
	// constructor fails closed when a password or TLS is absent. A dev
	// or local deployment that runs an unauthenticated, plaintext
	// Redis sets this (the gateway/ops --redis-allow-insecure flag) to
	// opt out; production leaves it false so a misconfiguration fails
	// fast at construction rather than silently dialing an
	// unauthenticated, plaintext Redis.
	//
	// spec: §12.4 line 197.
	AllowInsecure bool
}

// minTLSVersion pins the floor for every TLS connection the
// constructor builds. go-redis accepts the standard-library defaults
// otherwise; §12.4 requires TLS and a 1.2 floor matches the
// gateway-wide policy applied to the Postgres and adapter paths.
const minTLSVersion = tls.VersionTLS12

// Errors returned by NewClient.
var (
	// ErrNoSource reports that neither URL nor SentinelAddrs was set.
	// The caller's deployment is misconfigured.
	ErrNoSource = errors.New("redisconn: neither URL nor SentinelAddrs is set")
	// ErrMissingMasterName reports that SentinelAddrs is set but
	// MasterName is empty — Sentinel discovery cannot run without it.
	ErrMissingMasterName = errors.New("redisconn: SentinelAddrs requires MasterName")
	// ErrAuthRequired reports that no Redis AUTH credential was
	// supplied while enforcement is active. §12.4 mandates AUTH on
	// every Redis instance; set AllowInsecure (the
	// --redis-allow-insecure flag) only for a dev or local Redis.
	ErrAuthRequired = errors.New("redisconn: Redis AUTH is required (§12.4); " +
		"supply a password (direct-URL userinfo or --redis-password) or set --redis-allow-insecure for a dev Redis")
	// ErrTLSRequired reports that TLS was not configured while
	// enforcement is active. §12.4 mandates TLS on every Redis
	// instance; the direct-URL path requires the rediss:// scheme and
	// the Sentinel path requires --redis-tls (or active enforcement,
	// which forces it). Set AllowInsecure only for a dev Redis.
	ErrTLSRequired = errors.New("redisconn: Redis TLS is required (§12.4); " +
		"use a rediss:// URL or the Sentinel TLS path, or set --redis-allow-insecure for a dev Redis")
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
		// §12.4 AUTH-and-TLS invariant. ParseURL sets a non-nil
		// TLSConfig only for the rediss:// scheme, so a nil TLSConfig
		// means the operator chose a plaintext redis:// URL.
		if !cfg.AllowInsecure {
			if opts.Password == "" {
				return nil, ErrAuthRequired
			}
			if opts.TLSConfig == nil {
				return nil, ErrTLSRequired
			}
		}
		pinTLSFloor(opts.TLSConfig)
		return redis.NewClient(opts), nil
	case len(cfg.SentinelAddrs) > 0:
		if cfg.MasterName == "" {
			return nil, ErrMissingMasterName
		}
		// Enforcement forces TLS on the Sentinel path; without it the
		// FailoverClient dials plaintext regardless of operator
		// intent (the prior gap reported in §12.4). A dev deployment
		// that has opted out can still request TLS via cfg.TLS.
		if !cfg.AllowInsecure && cfg.Password == "" {
			return nil, ErrAuthRequired
		}
		var tlsCfg *tls.Config
		if cfg.TLS || !cfg.AllowInsecure {
			tlsCfg = &tls.Config{MinVersion: minTLSVersion}
		}
		return redis.NewFailoverClient(&redis.FailoverOptions{
			MasterName:       cfg.MasterName,
			SentinelAddrs:    cfg.SentinelAddrs,
			SentinelPassword: cfg.SentinelPassword,
			Password:         cfg.Password,
			DB:               cfg.DB,
			TLSConfig:        tlsCfg,
		}), nil
	default:
		return nil, ErrNoSource
	}
}

// pinTLSFloor raises a TLS config to the §12.4 minimum version when
// the operator left MinVersion unset. ParseURL builds the rediss://
// TLSConfig with go-redis defaults, so without this the negotiated
// floor would be whatever the standard library defaults to. A nil
// config (plaintext, only reachable under AllowInsecure) is left
// untouched.
func pinTLSFloor(c *tls.Config) {
	if c != nil && c.MinVersion == 0 {
		c.MinVersion = minTLSVersion
	}
}

// NewUniversalClient returns a redis.UniversalClient built from cfg.
// When ClusterAddrs is set it builds a §12.4 Redis Cluster client;
// otherwise it delegates to NewClient for the direct-URL / Sentinel
// paths. The returned client is closed by the caller. Cluster mode is
// the only path that produces a CLUSTER KEYSLOT-aware client, so the
// §12.4 Tier 2→3 migration of the Quota/Rate Limiting instance must
// route through here rather than NewClient.
//
// spec: §12.4 lines 260-264 — Redis Cluster migration pre-plan.
func NewUniversalClient(cfg Config) (redis.UniversalClient, error) {
	if len(cfg.ClusterAddrs) == 0 {
		return NewClient(cfg)
	}
	// §12.4 AUTH-and-TLS invariant on the Cluster path mirrors the
	// direct and Sentinel paths: production fails closed when a password
	// or TLS is absent. A dev cluster opts out via AllowInsecure.
	if !cfg.AllowInsecure && cfg.Password == "" {
		return nil, ErrAuthRequired
	}
	var tlsCfg *tls.Config
	if cfg.TLS || !cfg.AllowInsecure {
		tlsCfg = &tls.Config{MinVersion: minTLSVersion}
	}
	return redis.NewClusterClient(&redis.ClusterOptions{
		Addrs:     cfg.ClusterAddrs,
		Password:  cfg.Password,
		TLSConfig: tlsCfg,
	}), nil
}

// PingWithTimeout pings the client with the supplied deadline. It is
// a convenience for the main-binary boot path: the gateway and
// lenny-ops both ping after constructing the client to surface a
// configuration error at startup rather than on the first request.
// It accepts the redis.UniversalClient surface so a Cluster client
// (NewUniversalClient) pings through the same boot path as a direct
// *redis.Client.
func PingWithTimeout(client redis.UniversalClient, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return client.Ping(ctx).Err()
}
