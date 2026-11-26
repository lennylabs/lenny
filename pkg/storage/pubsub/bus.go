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
// over the subscription's receive channel in a Run-style loop selected
// against ctx.Done(). Subscribe reads the channel that carries the
// subscription confirmations as well as the messages, so it can tell a
// caller each time Redis confirms the subscription (OnSubscribed).
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

// SubscribeOption configures one Subscribe call.
type SubscribeOption func(*subscribeConfig)

// subscribeConfig holds the options of one Subscribe call.
type subscribeConfig struct {
	onSubscribed func()
}

// OnSubscribed sets a callback that Subscribe calls each time Redis
// confirms the subscription: when it is first established, and again
// each time go-redis re-establishes a dropped connection and resubscribes
// on the new one. Every message published while no subscription was
// confirmed is lost: during an outage, and before the first confirmation,
// which can come long after Subscribe was called when Redis is
// unreachable at startup. A cache on top of Bus reconciles that loss by
// rereading its authoritative store in the callback. The callback runs
// on the loop goroutine, as the handler does, so a callback that may
// block hands its work off.
func OnSubscribed(fn func()) SubscribeOption {
	return func(c *subscribeConfig) { c.onSubscribed = fn }
}

// Subscribe runs a consume loop on channel, delivering every message
// payload to handler until ctx is cancelled. It blocks; callers run it
// in a goroutine, the same way the gateway starts cachingstore.Run.
//
// A nil Bus blocks until ctx is cancelled and delivers nothing, so a
// caller that always starts a subscribe goroutine does not have to
// special-case the no-Redis mode; it never calls the OnSubscribed
// callback. The handler runs inline on the loop goroutine; a handler
// that may block should hand off its own work.
//
// go-redis drops a message whose delivery to a full receive channel
// exceeds its send timeout and reports the drop only through its logger,
// so Subscribe reports no dropped message. The caller's periodic reread
// of its authoritative store covers that loss.
func (b *Bus) Subscribe(ctx context.Context, channel string, handler func(payload []byte), opts ...SubscribeOption) {
	if b == nil || b.client == nil {
		<-ctx.Done()
		return
	}
	var cfg subscribeConfig
	for _, o := range opts {
		o(&cfg)
	}
	sub := b.client.Subscribe(ctx, channel)
	defer func() { _ = sub.Close() }()
	received := sub.ChannelWithSubscriptions()
	for {
		select {
		case <-ctx.Done():
			return
		case v, ok := <-received:
			if !ok {
				return
			}
			switch m := v.(type) {
			case *redis.Message:
				handler([]byte(m.Payload))
			case *redis.Subscription:
				if isConfirmation(m, channel) && cfg.onSubscribed != nil {
					cfg.onSubscribed()
				}
			}
		}
	}
}

// isConfirmation reports whether s is Redis's confirmation of a
// SUBSCRIBE to channel, as opposed to an unsubscribe notice or a
// confirmation for another channel.
func isConfirmation(s *redis.Subscription, channel string) bool {
	return s.Kind == "subscribe" && s.Channel == channel
}
