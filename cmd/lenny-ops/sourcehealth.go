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

// redisEdgeCallbacks carries the §25.5 replica-level Redis reachability edge
// hooks the source-health loop fires. onRedisDown opens the recovery flush's
// outage window by recording where the local ring stood when Redis went away;
// onRedisRecovered flushes that window to the recovered stream and fires once
// per observed down-to-up edge. spec: §25.5 (best-effort recovery flush).
//
// The window has a second opener the probe never observes: a failed XADD opens
// it at the event that failed. That opener carries its own edge, on the write
// path, so the emitter invokes the same recovery callback on its first
// successful XADD after a failure (see redisFanOutEmitter.Emit). Both edges are
// therefore covered without offering the flush on every reachable refresh.
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
// run also drives the §25.5 best-effort recovery flush on the Redis
// reachability edges it observes: the up-to-down edge opens the flush's outage
// window, and the down-to-up edge flushes it, each once per observed outage.
// The flush is a replica-level property, independent of any open read
// connection, so a consumer that connects only after Redis recovers still
// observes the events lenny-ops buffered locally during the outage. An
// interruption shorter than one refresh interval is invisible to this loop; the
// write path covers it by invoking the same recovery callback on its first
// successful XADD after a failed one. Nil callbacks disable the flush. spec:
// §25.5 (best-effort recovery flush).
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
		case nowUp && !prevRedisUp && edges.onRedisRecovered != nil:
			// The down-to-up edge, once per outage this loop observed. An
			// outage the loop never saw a down edge for is flushed by the write
			// path's own edge instead.
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

// gatewayBufferProbePath is the §25.3 gateway event-buffer endpoint the §25.5
// source-health probe measures the gateway with. The signal gates a data path,
// so it is measured on the surface that data path consumes: the §25.5 case-1
// fall-back serves gateway-originated events by fanning this endpoint across
// the gateway replicas, and it is reachable to lenny-ops only when the
// credential lenny-ops presents is admitted by the gateway's admin gate
// (§25.4). Probing an unauthenticated liveness endpoint instead answers "is
// the gateway process serving", which disagrees with "will the gateway serve
// lenny-ops the event buffer" in exactly the state that matters: the read
// surface would classify case 1 and route the request to a source that returns
// nothing. spec: §25.5 (case 1 vs case 4 branch), §25.4 (lenny-ops reaches the
// admin API as a service account holding platform-admin).
const gatewayBufferProbePath = "/v1/admin/events/buffer?limit=1"

// gatewayProbeTimeout bounds one source-health gateway probe.
const gatewayProbeTimeout = 3 * time.Second

// probeGateway reports whether the gateway served lenny-ops the §25.3 event
// buffer within a short timeout. Only a served response counts as up: a
// transport failure and a refusal alike (a 403 from the admin gate, a 5xx from
// a degraded gateway) report the gateway down, so a case-1 classification
// always implies a source that can actually answer, and a gateway that refuses
// lenny-ops escalates the read surface to the dual-outage case it is really
// in. A nil client (no gateway wired, e.g. dev) reports reachable so the read
// surface does not spuriously escalate to the dual-outage 503.
func probeGateway(ctx context.Context, gwClient *gateway.Client) bool {
	if gwClient == nil {
		return true
	}
	cctx, cancel := context.WithTimeout(ctx, gatewayProbeTimeout)
	defer cancel()
	// A nil out skips body decoding: the probe consumes only the outcome.
	return gwClient.Get(cctx, gatewayBufferProbePath, nil) == nil
}
