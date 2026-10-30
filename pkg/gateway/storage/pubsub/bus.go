// SPDX-License-Identifier: MIT

// Package pubsub is the Redis pub/sub substrate the gateway uses to
// converge its per-replica security caches across replicas. The §10.3
// mTLS certificate deny list, the §13.3 token-revocation cache, and the
// §4.9 credential deny list are each an in-memory set local to one
// gateway replica; a mutation on one replica must reach every other
// replica so a revocation takes effect fleet-wide.
//
// Bus is a thin wrapper over a redis.UniversalClient. Publish sends a
// payload on a channel; Subscribe runs a consume loop that delivers
// every message to a handler until the context is cancelled. The
// propagators in the sibling security-cache packages sit on top of Bus;
// the substrate itself carries no message semantics.
//
// Redis pub/sub is at-most-once: a publish that lands while no
// subscriber is connected, or during a Redis failover, is dropped.
// Each security cache that uses Bus also has an independent rehydration
// or TTL path that bounds staleness, so a dropped message degrades
// convergence latency rather than correctness. Bus does not attempt
// redelivery.
//
// The pub/sub loop mirrors the canonical pattern in
// pkg/gateway/breakerstore/cachingstore: client.Subscribe, then a range
// over pubsub.Channel() in a Run-style loop selected against
// ctx.Done().
package pubsub

import (
	"context"

	"github.com/redis/go-redis/v9"
)

// Bus is a Redis pub/sub fan-out over a redis.UniversalClient. A nil
// *Bus is a valid no-op: Publish and Subscribe on it do nothing, so a
// caller wired without Redis runs single-replica without branching on
// the Redis flag at every call site.
type Bus struct {
	client redis.UniversalClient
}

// New returns a Bus backed by client. Passing a nil client yields a
// Bus whose Publish and Subscribe are no-ops, which is the in-process
// single-replica mode.
func New(client redis.UniversalClient) *Bus {
	if client == nil {
		return nil
	}
	return &Bus{client: client}
}

// Publish sends payload on channel. A nil Bus, or a Bus built from a
// nil client, publishes nothing and returns nil: a replica running
// without Redis keeps its caches local with no error path. A publish
// failure is returned so the caller can log it; the failure is not
// fatal because every cache on top of Bus has an independent staleness
// bound.
func (b *Bus) Publish(ctx context.Context, channel string, payload []byte) error {
	if b == nil || b.client == nil {
		return nil
	}
	return b.client.Publish(ctx, channel, payload).Err()
}

// Subscribe runs a consume loop on channel, delivering every message
// payload to handler until ctx is cancelled. It blocks; callers run it
// in a goroutine, the same way the gateway starts cachingstore.Run.
//
// A nil Bus blocks until ctx is cancelled and delivers nothing, so a
// caller that always starts a subscribe goroutine does not have to
// special-case the no-Redis mode. The handler runs inline on the loop
// goroutine; a handler that may block should hand off its own work.
func (b *Bus) Subscribe(ctx context.Context, channel string, handler func(payload []byte)) {
	if b == nil || b.client == nil {
		<-ctx.Done()
		return
	}
	sub := b.client.Subscribe(ctx, channel)
	defer func() { _ = sub.Close() }()
	messages := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-messages:
			if !ok {
				return
			}
			handler([]byte(msg.Payload))
		}
	}
}
