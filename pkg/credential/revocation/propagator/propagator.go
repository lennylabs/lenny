// SPDX-License-Identifier: MIT

// Package propagator carries §13.3 token revocations across gateway
// replicas over Redis pub/sub.
//
// pkg/credential/revocation.Cache is a single-replica primitive: Revoke
// marks a jti revoked in one replica's in-memory set. The cache is
// also rehydrated periodically from the Postgres issued-token index,
// so a revocation eventually reaches every replica, but the rehydration
// cadence (30s) is too slow for the security-critical case. Propagator
// closes that window: a Revoke updates the local cache and publishes
// the jti on a Redis channel, and Run subscribes to the channel and
// applies a peer replica's revocations onto the local cache. A token
// revoked on any replica is then rejected on every replica within
// pub/sub latency.
//
// Cache's own methods are unchanged. Propagator's Revoke has the same
// signature as Cache.Revoke, so it satisfies the admin router's
// RevocationCache interface: wiring the propagator in place of the raw
// cache routes the admin revoke endpoint through the fan-out with no
// other change. IsRevoked and Len delegate to the wrapped cache so the
// propagator is a drop-in for the auth middleware's read path too.
//
// Redis pub/sub is at-most-once. A revocation published while a
// replica holds no confirmed subscription never reaches that replica,
// whether the subscription is down or not yet established at startup, so
// Run takes a rehydration callback and calls it each time Redis confirms
// the subscription: the replica rereads the issued-token index as soon as
// the subscription is up rather than at its next periodic rehydration. A revocation dropped on a
// connected subscription is reconciled by the next periodic rehydration,
// so a missed publish degrades propagation latency from sub-second to at
// most one rehydration interval rather than dropping the revocation.
package propagator

import (
	"context"

	"github.com/lennylabs/lenny/pkg/credential/revocation"
	"github.com/lennylabs/lenny/pkg/storage/pubsub"
)

// Channel is the Redis pub/sub channel revoked jtis are published on.
// Every gateway replica subscribes to it.
const Channel = "token:revocations"

// Propagator wraps a local §13.3 revocation cache with Redis pub/sub
// fan-out. The zero value is not usable; construct with New. A
// Propagator built with a nil Bus applies revocations locally and
// publishes nothing, which is the single-replica mode.
type Propagator struct {
	local *revocation.Cache
	bus   *pubsub.Bus
	// onError observes a publish failure. Nil means failures are
	// swallowed; the gateway passes a logging callback.
	onError func(error)
}

// Option configures a Propagator at construction.
type Option func(*Propagator)

// WithErrorHandler sets a callback invoked when a publish fails. A
// publish failure is not fatal: the jti is still revoked locally, and
// the periodic index rehydration reconciles a missed propagation.
func WithErrorHandler(fn func(error)) Option {
	return func(p *Propagator) { p.onError = fn }
}

// New returns a Propagator over local that fans revocations out on bus.
// bus may be nil, in which case the Propagator is a local-only pass
// through with no cross-replica propagation.
func New(local *revocation.Cache, bus *pubsub.Bus, opts ...Option) *Propagator {
	p := &Propagator{local: local, bus: bus}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Cache returns the wrapped revocation cache so callers that only read
// it (the auth middleware) or rehydrate it can reach the primitive
// directly.
func (p *Propagator) Cache() *revocation.Cache { return p.local }

// Revoke marks jti revoked in the local cache and publishes it so peer
// replicas revoke it too. An empty jti is a local no-op and is not
// published. The signature matches revocation.Cache.Revoke, so a
// Propagator satisfies the admin router's RevocationCache interface.
func (p *Propagator) Revoke(jti string) {
	if jti == "" {
		return
	}
	p.local.Revoke(jti)
	if err := Publish(context.Background(), p.bus, jti); err != nil && p.onError != nil {
		p.onError(err)
	}
}

// Publish sends a revoked jti on Channel over bus, the message every
// Propagator.Run and the lenny-ops revocation set apply. A process that
// revokes a token without holding a local revocation cache, such as the
// Token Service on a rotation, publishes through it. A nil bus publishes
// nothing and returns nil.
//
// spec: §13.3 (Credential Flow) "Token rotation and revocation".
func Publish(ctx context.Context, bus *pubsub.Bus, jti string) error {
	return bus.Publish(ctx, Channel, []byte(jti))
}

// IsRevoked delegates to the wrapped cache so a Propagator is a drop-in
// for the auth middleware's read path.
func (p *Propagator) IsRevoked(jti string) bool { return p.local.IsRevoked(jti) }

// Len delegates to the wrapped cache.
func (p *Propagator) Len() int { return p.local.Len() }

// Run subscribes to the revocation channel and applies a peer replica's
// revocations onto the local cache until ctx is cancelled. It blocks;
// the gateway runs it in a goroutine alongside the other background
// loops. A Propagator built with a nil Bus has Run block until ctx is
// cancelled and apply nothing.
//
// rehydrate, when non-nil, is called each time Redis confirms the
// subscription, the first confirmation included, because every
// revocation published while no subscription was confirmed is lost. It
// runs on the subscribe loop, so it must not block: the gateway passes a
// non-blocking signal to its rehydration loop, which spaces the reads it
// starts. lenny-ops passes nil and keeps its own periodic read.
//
// spec: §13.3 (Credential Flow) "Authoritative durability for revocation".
func (p *Propagator) Run(ctx context.Context, rehydrate func()) {
	var opts []pubsub.SubscribeOption
	if rehydrate != nil {
		opts = append(opts, pubsub.OnSubscribed(rehydrate))
	}
	p.bus.Subscribe(ctx, Channel, func(payload []byte) {
		p.local.Revoke(string(payload))
	}, opts...)
}
