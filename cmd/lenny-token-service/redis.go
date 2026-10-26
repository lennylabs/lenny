// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/gateway/storage/redistopology"
	"github.com/lennylabs/lenny/pkg/redisconn"
	"github.com/lennylabs/lenny/pkg/storerouter"
)

// tokenServiceRedis holds the Token Service's Redis clients: the base
// client on the §17.9.3 topology the flags select, and the §12.4
// per-concern split, of which the Token Service reads only the
// Cache/Pub-Sub concern.
type tokenServiceRedis struct {
	base    redis.UniversalClient
	clients *redistopology.Clients
}

// cachePubSub returns the client of the §12.4 Cache/Pub-Sub concern: the
// dedicated client when --redis-cache-pubsub-url is set, otherwise the
// base client.
func (r *tokenServiceRedis) cachePubSub() redis.UniversalClient {
	return r.clients.For(storerouter.RedisConcernCachePubSub)
}

// Close closes the per-concern clients and then the base client.
func (r *tokenServiceRedis) Close() {
	_ = r.clients.Close()
	_ = r.base.Close()
}

// newTokenServiceRedis builds the Redis clients the flags describe, or
// returns nil and no error when no Redis flag is set. The §12.4
// AUTH-and-TLS invariant applies to the base client and to the concern
// client unless --redis-allow-insecure is set.
//
// spec: §17.9.3 (Redis topology), §12.4 (Redis HA and Failure Modes)
// "Logical separation of Redis concerns", §13.3 (the revocation publish).
func newTokenServiceRedis(f *tokenServiceFlags) (*tokenServiceRedis, error) {
	cfg, ok, err := f.redisConfig()
	if err != nil || !ok {
		return nil, err
	}
	base, err := redisconn.NewUniversalClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("redis client: %w", err)
	}
	clients, err := redistopology.Build(base,
		map[storerouter.RedisConcern]string{storerouter.RedisConcernCachePubSub: deref(f.redisCachePubSubURL)},
		redisconn.Config{Password: deref(f.redisPassword), AllowInsecure: derefBool(f.redisAllowInsecure)})
	if err != nil {
		_ = base.Close()
		return nil, fmt.Errorf("cache/pub-sub redis client: %w", err)
	}
	return &tokenServiceRedis{base: base, clients: clients}, nil
}

// errRedisTopology reports more than one §17.9.3 Redis topology.
var errRedisTopology = errors.New("--redis-url, --redis-sentinel-addrs, and --redis-cluster-addrs are mutually exclusive")

// redisConfig selects the base Redis configuration from the topology
// flags. ok is false when no topology flag is set. Configuring two
// topologies is an error rather than a precedence rule, and so is a
// Cache/Pub-Sub concern URL with no base connection, because the
// concern split hangs off the base client and silently dropping the
// concern would leave revocations unpublished. The pointer fields are
// read nil-safely, so a test that builds tokenServiceFlags by hand need
// not bind every Redis flag.
//
// spec: §17.9.3 (Redis topology), §12.4 (Redis HA and Failure Modes).
func (f *tokenServiceFlags) redisConfig() (redisconn.Config, bool, error) {
	url, sentinels, cluster := deref(f.redisURL), deref(f.redisSentinelAddrs), deref(f.redisClusterAddrs)
	set := 0
	for _, v := range []string{url, sentinels, cluster} {
		if v != "" {
			set++
		}
	}
	if set > 1 {
		return redisconn.Config{}, false, errRedisTopology
	}
	password, insecure := deref(f.redisPassword), derefBool(f.redisAllowInsecure)
	switch {
	case url != "":
		return redisconn.Config{URL: url, Password: password, AllowInsecure: insecure}, true, nil
	case cluster != "":
		return redisconn.Config{
			ClusterAddrs:  splitAddrs(cluster),
			Password:      password,
			TLS:           derefBool(f.redisTLS),
			AllowInsecure: insecure,
		}, true, nil
	case sentinels != "":
		return redisconn.Config{
			SentinelAddrs:    splitAddrs(sentinels),
			MasterName:       deref(f.redisSentinelMaster),
			Password:         password,
			SentinelPassword: deref(f.redisSentinelPassword),
			TLS:              derefBool(f.redisTLS),
			AllowInsecure:    insecure,
		}, true, nil
	}
	if deref(f.redisCachePubSubURL) != "" {
		return redisconn.Config{}, false, errors.New("--redis-cache-pubsub-url requires a base connection (--redis-url, --redis-sentinel-addrs, or --redis-cluster-addrs)")
	}
	return redisconn.Config{}, false, nil
}

// splitAddrs splits a comma-separated address list, trimming spaces and
// dropping empty entries.
func splitAddrs(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// deref returns the string a flag pointer holds, or "" for an unbound flag.
func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// derefBool returns the bool a flag pointer holds, or false for an
// unbound flag.
func derefBool(p *bool) bool {
	return p != nil && *p
}
