// SPDX-License-Identifier: MIT

// Package propagator carries §13.3 token revocations across gateway
// replicas over Redis pub/sub.
//
// pkg/gateway/revocation.Cache is a single-replica primitive: Revoke
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
// Redis pub/sub is at-most-once. A dropped revocation is reconciled by
// the next index rehydration, so a missed publish degrades propagation
// latency from sub-second to one rehydration interval rather than
// dropping the revocation.
package propagator

import (
	"context"

	"github.com/lennylabs/lenny/pkg/gateway/revocation"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pubsub"
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
	if err := p.bus.Publish(context.Background(), Channel, []byte(jti)); err != nil && p.onError != nil {
		p.onError(err)
	}
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
func (p *Propagator) Run(ctx context.Context) {
	p.bus.Subscribe(ctx, Channel, func(payload []byte) {
		p.local.Revoke(string(payload))
	})
}
