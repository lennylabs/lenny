// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/lennylabs/lenny/pkg/ops/gateway"
)

// sourceHealthProbe is the §25.5 degradation-matrix source-health signal
// for the operational event stream. It caches the reachability of the
// Redis ops:events:stream and the gateway event buffer, refreshed by a
// background ticker so the per-request poll / SSE path never blocks on a
// network probe. It satisfies opsstream.SourceHealth. spec: §25.5 lines
// 2768-2780.
type sourceHealthProbe struct {
	redisUp   atomic.Bool
	gatewayUp atomic.Bool
}

// newSourceHealthProbe returns a probe that starts optimistic (both
// sources reachable) so the read surface serves normally until the first
// refresh resolves the live state, rather than 503-ing on a cold start.
func newSourceHealthProbe() *sourceHealthProbe {
	p := &sourceHealthProbe{}
	p.redisUp.Store(true)
	p.gatewayUp.Store(true)
	return p
}

// RedisAvailable reports the cached Redis ops:events:stream reachability.
func (p *sourceHealthProbe) RedisAvailable() bool { return p.redisUp.Load() }

// GatewayAvailable reports the cached gateway event-buffer reachability.
func (p *sourceHealthProbe) GatewayAvailable() bool { return p.gatewayUp.Load() }

// run refreshes the cached health every interval until ctx is done. The
// Redis probe is a PING; the gateway probe is a cheap authenticated GET
// against the §25.4 version endpoint, treated only as a liveness signal.
// The gateway reachability is consulted only when Redis is down (the
// §25.5 case-1 vs case-4 branch), so a flaky gateway probe affects the
// response only during an actual Redis outage. A nil gwClient leaves the
// gateway reported reachable (no probe wired). spec: §25.5 lines
// 2768-2780.
func (p *sourceHealthProbe) run(ctx context.Context, interval time.Duration, redisCli redis.UniversalClient, gwClient *gateway.Client) {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	refresh := func() {
		p.redisUp.Store(probeRedis(ctx, redisCli))
		p.gatewayUp.Store(probeGateway(ctx, gwClient))
	}
	refresh()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			refresh()
		}
	}
}

// probeRedis reports whether a PING against redisCli succeeds within a
// short timeout. A nil client (no Redis deployment) reports down.
func probeRedis(ctx context.Context, redisCli redis.UniversalClient) bool {
	if redisCli == nil {
		return false
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return redisCli.Ping(cctx).Err() == nil
}

// probeGateway reports whether the gateway responds to a cheap version
// GET within a short timeout. A nil client (no gateway wired, e.g. dev)
// reports reachable so the read surface does not spuriously escalate to
// the dual-outage 503.
func probeGateway(ctx context.Context, gwClient *gateway.Client) bool {
	if gwClient == nil {
		return true
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var discard map[string]any
	return gwClient.Get(cctx, "/v1/admin/platform/version", &discard) == nil
}
