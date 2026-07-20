// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
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

// redisEdgeCallbacks carries the §25.5 replica-level Redis reachability edge
// hooks the source-health loop fires. onRedisDown opens the recovery flush's
// outage window by recording where the local ring stood when Redis went away;
// onRedisRecovered flushes that window to the recovered stream and is a no-op
// when no window is open, so a reachable Redis with nothing buffered costs
// nothing. The window is also opened by a failed XADD, which the probe never
// observes, so the flush is offered on every reachable refresh rather than on a
// down-to-up edge alone. spec: §25.5 (best-effort recovery flush).
type redisEdgeCallbacks struct {
	onRedisDown      func()
	onRedisRecovered func(context.Context)
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
// Redis probe is a PING; the gateway probe is a cheap GET against the
// gateway liveness endpoint, treated only as a reachability signal.
// The gateway reachability is consulted only when Redis is down (the
// §25.5 case-1 vs case-4 branch), so a flaky gateway probe affects the
// response only during an actual Redis outage. A nil gwClient leaves the
// gateway reported reachable (no probe wired). spec: §25.5 lines
// 2768-2780.
//
// run also drives the §25.5 best-effort recovery flush. The up-to-down edge
// opens the flush's outage window once per observed outage. The flush itself is
// offered on every refresh that finds Redis reachable, because the window has a
// second opener the probe never observes: a failed XADD opens it at the event
// that failed, so an interruption shorter than one refresh interval produces a
// window with no accompanying down edge. Firing only on the edge would abandon
// those events and leave the stale window to widen the next flush into
// already-delivered history. The flush is a replica-level property,
// independent of any open read connection, so a consumer that connects only
// after Redis recovers still observes the events lenny-ops buffered locally
// during the outage. Nil callbacks disable the flush. spec: §25.5
// (best-effort recovery flush).
func (p *sourceHealthProbe) run(ctx context.Context, interval time.Duration, redisCli redis.UniversalClient, gwClient *gateway.Client, edges redisEdgeCallbacks) {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	// Seed the edge detector with the optimistic startup state so a first
	// probe that finds Redis already up is not treated as a recovery edge
	// (nothing was buffered during an outage that never happened).
	prevRedisUp := p.redisUp.Load()
	refresh := func() {
		nowUp := probeRedis(ctx, redisCli)
		p.redisUp.Store(nowUp)
		p.gatewayUp.Store(probeGateway(ctx, gwClient))
		switch {
		case !nowUp && prevRedisUp && edges.onRedisDown != nil:
			edges.onRedisDown()
		case nowUp && edges.onRedisRecovered != nil:
			// Every refresh that finds Redis reachable offers the flush its
			// chance, rather than only the refresh that follows an observed
			// down edge. The outage window is opened by two signals and the
			// probe sees only one of them: a failed XADD opens it the instant
			// the write fails, so a Redis interruption shorter than one refresh
			// interval leaves a window open that no down-to-up edge would ever
			// close. The flush is a no-op with no window open, and consuming
			// the window is what makes it fire once per outage.
			edges.onRedisRecovered(ctx)
		}
		prevRedisUp = nowUp
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

// gatewayLivenessPath is the unauthenticated gateway liveness endpoint the
// §25.5 source-health probe measures reachability with. The probe answers
// whether the gateway process is up, so it must not ride on an admin
// endpoint whose authorization outcome is independent of reachability: the
// lenny-ops service-account principal does not hold the platform-admin role
// the admin API requires, so an admin GET reports a reachable gateway as
// down and pins the read surface in the §25.5 dual-outage case. The §25.4
// gateway-auth self-health check probes the same path for the same reason.
// spec: §25.5 (case 1 vs case 4 branch).
const gatewayLivenessPath = "/healthz"

// gatewayProbeTimeout bounds one source-health gateway probe.
const gatewayProbeTimeout = 3 * time.Second

// probeGateway reports whether the gateway answered a liveness GET within a
// short timeout. Any HTTP status counts as reachable, including a 4xx or the
// 503 the gateway serves while degraded: the response proves the process is
// serving, which is what the §25.5 case-1 gateway-buffer fall-back depends
// on. Only a transport failure (dial, TLS, or timeout) reports the gateway
// down and escalates the read surface to the dual-outage case. A nil client
// (no gateway wired, e.g. dev) reports reachable so the read surface does
// not spuriously escalate to the dual-outage 503.
func probeGateway(ctx context.Context, gwClient *gateway.Client) bool {
	if gwClient == nil {
		return true
	}
	cctx, cancel := context.WithTimeout(ctx, gatewayProbeTimeout)
	defer cancel()
	// A nil out skips body decoding: the probe consumes only the outcome.
	err := gwClient.Get(cctx, gatewayLivenessPath, nil)
	if err == nil {
		return true
	}
	var httpErr *gateway.HTTPError
	return errors.As(err, &httpErr)
}
