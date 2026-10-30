// SPDX-License-Identifier: MIT

package events

import (
	"context"
	"crypto/tls"
	"net"
	"testing"

	"github.com/redis/go-redis/v9"
)

// spec: 25.5 (the SSE handler reads Redis via XREAD BLOCK 0 in a goroutine,
// one independent read cursor per connection), 17.9.3 (Redis deployment
// topologies) — every topology lenny-ops is deployed with must be able to
// establish the live tail. go-redis fills a default dialer in Options.init()
// but ClusterOptions.init() does not, so a Cluster client built the way
// pkg/redisconn constructs one exposes a nil Dialer. A tail that refused to
// start there would leave every SSE connection on a Cluster deployment serving
// the XRANGE backlog and nothing live.
func TestRedisTailClient_EstablishesOnEveryRedisTopology_spec_25_5(t *testing.T) {
	cases := []struct {
		name   string
		client redis.UniversalClient
	}{
		{
			name:   "direct",
			client: redis.NewClient(&redis.Options{Addr: "127.0.0.1:6379"}),
		},
		{
			// Built exactly as pkg/redisconn.NewUniversalClient builds the
			// §17.9.3 Cluster path: Addrs, Password, and TLSConfig only, with
			// no Dialer, which ClusterOptions.init() does not fill in.
			name: "cluster",
			client: redis.NewClusterClient(&redis.ClusterOptions{
				Addrs:     []string{"127.0.0.1:6379", "127.0.0.1:6380"},
				Password:  "s3cret",
				TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12},
			}),
		},
		{
			// The Sentinel path resolves to a *redis.Client, and a failover
			// client carries its own dialer; both go through the same branch.
			name: "sentinel-failover",
			client: redis.NewFailoverClient(&redis.FailoverOptions{
				MasterName:    "mymaster",
				SentinelAddrs: []string{"127.0.0.1:26379"},
			}),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() { _ = tc.client.Close() }()
			tail, err := NewRedisStreamClient(tc.client).TailClient()
			if err != nil {
				t.Fatalf("TailClient() on the %s topology returned %v; no SSE connection on this topology can establish a live XREAD BLOCK 0 tail", tc.name, err)
			}
			if tail == nil {
				t.Fatal("TailClient() returned a nil client with no error")
			}
			if err := tail.Close(); err != nil {
				t.Errorf("closing the tail client: %v", err)
			}
		})
	}
}

// spec: 25.5 (XREAD BLOCK 0 per-connection live tail) — the tail must keep a
// handle on every socket its client dials, which is what ends the blocked read
// on disconnect. The fallback dialer installed for a client that exposes none
// tracks its sockets the same way a configured dialer's do.
func TestTrackedDialer_FallbackDialerTracksItsSockets_spec_25_5(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			defer func() { _ = conn.Close() }()
		}
	}()

	tail := &redisTailConn{client: redis.NewClient(&redis.Options{Addr: ln.Addr().String()})}
	defer func() { _ = tail.client.Close() }()

	// A nil dial is what a §17.9.3 Cluster client's options carry.
	dial := trackedDialer(nil, 0, nil, tail)
	conn, err := dial(context.Background(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("fallback dialer failed to dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	tail.mu.Lock()
	tracked := len(tail.conns)
	tail.mu.Unlock()
	if tracked != 1 {
		t.Errorf("fallback dialer tracked %d socket(s), want 1; Close cannot end the blocked read on an untracked socket", tracked)
	}
}
