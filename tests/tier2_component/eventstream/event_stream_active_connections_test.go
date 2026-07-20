//go:build component

// SPDX-License-Identifier: MIT

// Tier-2 component coverage for the §25.5
// lenny_ops_events_sse_active_connections gauge's input against a real Redis
// ops:events:stream. The gauge must count SSE connections whichever source each
// one is served from, so it is exercised here with Redis as the primary source
// and across a switch into the Redis-down fall-back.
package eventstream_test

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	opsstream "github.com/lennylabs/lenny/pkg/ops/events"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// flippingHealth is a SourceHealth whose Redis reachability changes at runtime,
// so a connection can be moved off the Redis source while it stays open.
type flippingHealth struct {
	redis atomic.Bool
}

func (h *flippingHealth) RedisAvailable() bool   { return h.redis.Load() }
func (h *flippingHealth) GatewayAvailable() bool { return true }

// TestOpsEventStreamActiveConnectionsCountsRedisServedConnections asserts the
// §25.5 active-connection count reports every open SSE connection while Redis
// is the read source, holds steady when a connection moves to the Redis-down
// fall-back, and returns to zero once the connections close.
//
// spec: 25.5 (lenny_ops_events_sse_active_connections; the Redis stream is the
// primary SSE source whenever Redis is reachable) — a connection served from
// the Redis stream tails Redis on a client of its own and installs no
// subscription on this replica's ring buffer, so a count derived from the
// ring's subscriber list reads zero for the deployment the read side is built
// for and only becomes non-zero during a degradation. The count also must not
// change when a connection switches source mid-life, since one connection is
// one connection whichever source is serving it.
//
// diagnosis: a zero count with connections open means the gauge is reading the
// local ring's subscriber list rather than the SSE connection count, so
// operators see no event-stream consumers in the steady state; a count that
// changes across the source switch means a connection is being counted per
// stint rather than per connection; a count that does not return to zero means
// a closed connection is never released.
func TestOpsEventStreamActiveConnectionsCountsRedisServedConnections(t *testing.T) {
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const key = "ops:events:stream:activeconns"
	emitter := newStreamEmitter(t, rd.Client, key, 1000)
	if err := emitter.Emit(ctx, alertEvent("pool/backlog")); err != nil {
		t.Fatalf("seed the stream: %v", err)
	}

	health := &flippingHealth{}
	health.redis.Store(true)
	svc := opsstream.New(opsstream.Options{
		RedisClient:    opsstream.NewRedisStreamClient(rd.Client),
		RedisStreamKey: key,
		SourceHealth:   health,
		ReplicaID:      "ops-1",
	})

	if n := svc.ActiveStreams(); n != 0 {
		t.Fatalf("active connections before any connection opened = %d, want 0", n)
	}

	type conn struct {
		cancel context.CancelFunc
		done   chan struct{}
		sink   *tailSink
	}
	open := func() *conn {
		t.Helper()
		cctx, ccancel := context.WithCancel(ctx)
		req, err := http.NewRequestWithContext(cctx, http.MethodGet, "/v1/admin/events/stream", nil)
		if err != nil {
			t.Fatalf("build stream request: %v", err)
		}
		c := &conn{cancel: ccancel, done: make(chan struct{}), sink: newTailSink()}
		go func() {
			defer close(c.done)
			svc.HandleStream(c.sink, req)
		}()
		return c
	}

	first, second := open(), open()
	defer func() {
		first.cancel()
		second.cancel()
	}()

	// Both connections are served from the Redis stream: each replays the
	// backlog from Redis and registers no subscription on the local ring.
	for _, c := range []*conn{first, second} {
		waitUntil(t, 10*time.Second, "the Redis-served backlog frame", func() bool {
			return strings.Contains(c.sink.String(), "pool/backlog")
		})
	}
	if n := svc.SubscriberCount(); n != 0 {
		t.Fatalf("Redis-served connections installed %d local-ring subscriptions; the count backing the gauge cannot come from there", n)
	}
	waitUntil(t, 10*time.Second, "both connections to be counted", func() bool {
		return svc.ActiveStreams() == 2
	})

	// A Redis outage moves both connections onto the fall-back. The count is
	// per connection, so it must not move.
	health.redis.Store(false)
	waitUntil(t, 10*time.Second, "the degradation announcement on the source switch", func() bool {
		return strings.Contains(first.sink.String(), ":degradation") && strings.Contains(second.sink.String(), ":degradation")
	})
	if n := svc.ActiveStreams(); n != 2 {
		t.Errorf("active connections across the source switch = %d, want 2 (one connection is one connection whichever source serves it)", n)
	}

	// Closing one connection releases exactly one count.
	first.cancel()
	<-first.done
	waitUntil(t, 10*time.Second, "the closed connection to be released", func() bool {
		return svc.ActiveStreams() == 1
	})

	second.cancel()
	<-second.done
	waitUntil(t, 10*time.Second, "every connection to be released", func() bool {
		return svc.ActiveStreams() == 0
	})
}
