// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	revocationprop "github.com/lennylabs/lenny/pkg/gateway/credentials/revocation/propagator"
	"github.com/lennylabs/lenny/pkg/redisconn"
	"github.com/lennylabs/lenny/tests/testinfra/tlsredis"
)

// redisFlags returns Token Service flags with every Redis flag bound to
// an empty or false value, so a case sets only the flags it exercises.
func redisFlags() *tokenServiceFlags {
	s := func() *string { v := ""; return &v }
	b := func() *bool { v := false; return &v }
	return &tokenServiceFlags{
		redisURL:              s(),
		redisPassword:         s(),
		redisSentinelAddrs:    s(),
		redisSentinelMaster:   s(),
		redisSentinelPassword: s(),
		redisClusterAddrs:     s(),
		redisTLS:              b(),
		redisAllowInsecure:    b(),
		redisCachePubSubURL:   s(),
	}
}

// TestTokenServiceRedisTopologySelection_spec_12_4 pins the Token
// Service's reading of the §17.9.3 Redis topologies the chart renders
// for the gateway: a direct URL, Sentinel, or Cluster. Each selects one
// redisconn configuration carrying the AUTH, TLS, and opt-out settings,
// two topologies at once are a startup error rather than a silent
// precedence rule, and a Cache/Pub-Sub concern URL with no base
// connection is refused, because the gateway routes that concern off
// the base client.
//
// spec: 12.4 (Redis HA and Failure Modes) "Logical separation of Redis concerns", 17.9.3 (Redis topology), 4.3 (Token Service)
func TestTokenServiceRedisTopologySelection_spec_12_4(t *testing.T) {
	cases := []struct {
		name    string
		set     func(f *tokenServiceFlags)
		want    redisconn.Config
		wantOK  bool
		wantErr string
	}{
		{name: "no redis flag", set: func(*tokenServiceFlags) {}},
		{
			name: "direct url",
			set: func(f *tokenServiceFlags) {
				*f.redisURL = "rediss://:pw@redis-master:6380/0"
				*f.redisPassword = "pw2"
			},
			want:   redisconn.Config{URL: "rediss://:pw@redis-master:6380/0", Password: "pw2"},
			wantOK: true,
		},
		{
			name: "direct url with the dev opt-out",
			set: func(f *tokenServiceFlags) {
				*f.redisURL = "redis://redis:6379"
				*f.redisAllowInsecure = true
			},
			want:   redisconn.Config{URL: "redis://redis:6379", AllowInsecure: true},
			wantOK: true,
		},
		{
			name: "sentinel",
			set: func(f *tokenServiceFlags) {
				*f.redisSentinelAddrs = "s-0:26379, s-1:26379,,s-2:26379"
				*f.redisSentinelMaster = "lenny-master"
				*f.redisPassword = "data-pw"
				*f.redisSentinelPassword = "sentinel-pw"
				*f.redisTLS = true
			},
			want: redisconn.Config{
				SentinelAddrs:    []string{"s-0:26379", "s-1:26379", "s-2:26379"},
				MasterName:       "lenny-master",
				Password:         "data-pw",
				SentinelPassword: "sentinel-pw",
				TLS:              true,
			},
			wantOK: true,
		},
		{
			name: "cluster",
			set: func(f *tokenServiceFlags) {
				*f.redisClusterAddrs = "c-0:6379,c-1:6379"
				*f.redisPassword = "pw"
				*f.redisTLS = true
			},
			want:   redisconn.Config{ClusterAddrs: []string{"c-0:6379", "c-1:6379"}, Password: "pw", TLS: true},
			wantOK: true,
		},
		{
			name: "url and sentinel",
			set: func(f *tokenServiceFlags) {
				*f.redisURL = "rediss://:pw@r:6380"
				*f.redisSentinelAddrs = "s-0:26379"
			},
			wantErr: "mutually exclusive",
		},
		{
			name: "sentinel and cluster",
			set: func(f *tokenServiceFlags) {
				*f.redisSentinelAddrs = "s-0:26379"
				*f.redisClusterAddrs = "c-0:6379"
			},
			wantErr: "mutually exclusive",
		},
		{
			name:    "cache/pub-sub concern without a base connection",
			set:     func(f *tokenServiceFlags) { *f.redisCachePubSubURL = "rediss://:pw@redis-cache:6380" },
			wantErr: "--redis-cache-pubsub-url",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := redisFlags()
			tc.set(f)
			got, ok, err := f.redisConfig()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("redisConfig() error = %v, want one containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("redisConfig() error = %v", err)
			}
			if ok != tc.wantOK || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("redisConfig() = %+v, %v; want %+v, %v", got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// startupWithRedisFlags runs the Token Service build steps in
// runTokenService order with the Redis flags set.
func startupWithRedisFlags(t *testing.T, set func(f *tokenServiceFlags)) *tokenServiceWiring {
	t.Helper()
	f := signingWiringFlags(filepath.Join(t.TempDir(), "master.key"))
	r := redisFlags()
	set(r)
	f.redisURL, f.redisPassword = r.redisURL, r.redisPassword
	f.redisSentinelAddrs, f.redisSentinelMaster, f.redisSentinelPassword = r.redisSentinelAddrs, r.redisSentinelMaster, r.redisSentinelPassword
	f.redisClusterAddrs, f.redisTLS, f.redisAllowInsecure, f.redisCachePubSubURL = r.redisClusterAddrs, r.redisTLS, r.redisAllowInsecure, r.redisCachePubSubURL
	w := &tokenServiceWiring{f: f}
	w.buildMetrics()
	w.buildKMSAndSigner()
	w.buildStores()
	w.buildEventEmitterAndCache()
	if w.redisCleanup != nil {
		t.Cleanup(w.redisCleanup)
	}
	w.buildDriftMonitor()
	w.buildServer()
	return w
}

// TestTokenServicePublishesRevocationsOnTheCachePubSubConcern_spec_13_3
// is the F-4.3.21 regression for a split Redis topology. The gateway
// replicas and lenny-ops subscribe to token:revocations on the client of
// the §12.4 Cache/Pub-Sub concern, which the chart can point at its own
// instance through redis.concerns.cachePubSubUrl. A Token Service given
// the base connection and that concern URL publishes a revoked jti on
// the concern's instance, where the subscribers are, and not on the base
// instance, where none of them listen.
//
// spec: 13.3 (Credential Flow) "Token rotation and revocation", 12.4 (Redis HA and Failure Modes) "Logical separation of Redis concerns", 25.4 (The lenny-ops Service: Authentication, revocation set rule 2)
func TestTokenServicePublishesRevocationsOnTheCachePubSubConcern_spec_13_3(t *testing.T) {
	base := tlsredis.Start(t, "base-secret")
	cache := tlsredis.Start(t, "cache-secret")
	w := startupWithRedisFlags(t, func(f *tokenServiceFlags) {
		*f.redisURL = base.URL
		*f.redisCachePubSubURL = cache.URL
	})
	if w.revocationBus == nil {
		t.Fatal("with a Redis connection the Token Service built no revocation bus")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	subscribe := func(c *redis.Client) *redis.PubSub {
		sub := c.Subscribe(ctx, revocationprop.Channel)
		t.Cleanup(func() { _ = sub.Close() })
		if _, err := sub.Receive(ctx); err != nil {
			t.Fatalf("subscribe: %v", err)
		}
		return sub
	}
	onCache, onBase := subscribe(cache.Client), subscribe(base.Client)

	if err := revocationPublisher(w.revocationBus)(ctx, "acme", "jti-rotated"); err != nil {
		t.Fatalf("publish: %v", err)
	}
	select {
	case msg := <-onCache.Channel():
		if msg.Payload != "jti-rotated" {
			t.Fatalf("the cache/pub-sub instance received %q, want jti-rotated", msg.Payload)
		}
	case <-ctx.Done():
		t.Fatal("the revocation never reached the cache/pub-sub instance the gateway and lenny-ops subscribe on")
	}
	select {
	case msg := <-onBase.Channel():
		t.Fatalf("the revocation went to the base instance (%q), where no subscriber listens", msg.Payload)
	case <-time.After(300 * time.Millisecond):
	}
}

// TestTokenServiceRedisAuthAndTLSInvariant_spec_12_4 pins the §12.4
// AUTH-and-TLS startup invariant on the Token Service's Redis client
// and its dev opt-out. A plaintext, password-less Redis is refused
// unless --redis-allow-insecure is set, which the chart renders only
// under global.devMode; with it the client connects. A Cluster
// topology builds a Cluster client, and a per-concern URL takes the
// opt-out from the base settings.
//
// spec: 12.4 (Redis HA and Failure Modes), 17.4 (Local Development Mode) "Dev mode guard rails"
func TestTokenServiceRedisAuthAndTLSInvariant_spec_12_4(t *testing.T) {
	plain := miniredis.RunT(t)
	plainURL := "redis://" + plain.Addr() + "/0"

	f := redisFlags()
	*f.redisURL = plainURL
	if _, err := newTokenServiceRedis(f); err == nil {
		t.Fatal("a plaintext, password-less Redis was accepted without --redis-allow-insecure")
	}

	*f.redisAllowInsecure = true
	*f.redisCachePubSubURL = plainURL
	r, err := newTokenServiceRedis(f)
	if err != nil {
		t.Fatalf("with --redis-allow-insecure: %v", err)
	}
	t.Cleanup(r.Close)
	if r == nil || r.base == nil || r.cachePubSub() == nil {
		t.Fatal("with --redis-allow-insecure the Token Service built no Redis client")
	}
	if err := r.cachePubSub().Ping(context.Background()).Err(); err != nil {
		t.Fatalf("ping the cache/pub-sub client: %v", err)
	}

	c := redisFlags()
	*c.redisClusterAddrs = "c-0:6379,c-1:6379"
	*c.redisPassword = "pw"
	*c.redisTLS = true
	rc, err := newTokenServiceRedis(c)
	if err != nil {
		t.Fatalf("cluster topology: %v", err)
	}
	t.Cleanup(rc.Close)
	if _, ok := rc.base.(*redis.ClusterClient); !ok {
		t.Fatalf("cluster topology built %T, want *redis.ClusterClient", rc.base)
	}

	if r, err := newTokenServiceRedis(redisFlags()); err != nil || r != nil {
		t.Fatalf("no Redis flag: got %v, %v; want nil, nil", r, err)
	}
}
