// SPDX-License-Identifier: MIT

// Package propagator carries §4.9 credential-deny-list revocations
// across gateway replicas over Redis pub/sub.
//
// pkg/gateway/denylist.DenyList is a single-replica primitive: Revoke
// adds a credential identity to one replica's in-memory set, which the
// §4.9 LLM reverse proxy checks on every upstream request. §4.9
// specifies that revocations propagate across replicas over Redis
// pub/sub and that the list is rebuilt at startup from the stores'
// revoked entries. Propagator is the pub/sub half: a Revoke updates the
// local deny list and publishes the credential key on a Redis channel,
// and Run subscribes to the channel and applies a peer replica's
// revocations onto the local deny list. A credential revoked on any
// replica is then rejected on every replica.
//
// DenyList's own methods are unchanged. Propagator's Revoke has the
// same signature as DenyList.Revoke, and Revoked delegates to the
// wrapped list, so the propagator is a drop-in wherever the raw deny
// list is wired today.
//
// Redis pub/sub is at-most-once. A dropped revocation is reconciled by
// the §4.9 startup rebuild on the next replica restart; for the live
// window, a missed publish leaves a revoked credential reachable on the
// replicas that missed it until the rebuild. The same at-most-once
// caveat applies to the circuit-breaker fan-out the gateway already
// runs.
package propagator

import (
	"context"
	"encoding/json"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/denylist"
	"github.com/lennylabs/lenny/pkg/gateway/pubsub"
)

// Channel is the Redis pub/sub channel revoked credential keys are
// published on. Every gateway replica subscribes to it.
const Channel = "credential:denylist:events"

// Propagator wraps a local §4.9 credential deny list with Redis pub/sub
// fan-out. The zero value is not usable; construct with New. A
// Propagator built with a nil Bus applies revocations locally and
// publishes nothing, which is the single-replica mode.
type Propagator struct {
	local *denylist.DenyList
	bus   *pubsub.Bus
	// onError observes a publish failure. Nil means failures are
	// swallowed; the gateway passes a logging callback.
	onError func(error)
}

// Option configures a Propagator at construction.
type Option func(*Propagator)

// WithErrorHandler sets a callback invoked when a publish fails. A
// publish failure is not fatal: the credential is still revoked
// locally, and the §4.9 startup rebuild reconciles a missed
// propagation.
func WithErrorHandler(fn func(error)) Option {
	return func(p *Propagator) { p.onError = fn }
}

// New returns a Propagator over local that fans revocations out on bus.
// bus may be nil, in which case the Propagator is a local-only pass
// through with no cross-replica propagation.
func New(local *denylist.DenyList, bus *pubsub.Bus, opts ...Option) *Propagator {
	p := &Propagator{local: local, bus: bus}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Local returns the wrapped deny list so callers on the §4.9 upstream
// hot path read the local set directly without going through the
// propagator.
func (p *Propagator) Local() *denylist.DenyList { return p.local }

// Revoke adds key to the local deny list and publishes it so peer
// replicas revoke it too. The signature matches denylist.DenyList.Revoke,
// so a Propagator is a drop-in for the raw deny list.
func (p *Propagator) Revoke(key credential.CredentialKey) {
	p.local.Revoke(key)
	payload, err := json.Marshal(key)
	if err != nil {
		if p.onError != nil {
			p.onError(err)
		}
		return
	}
	if err := p.bus.Publish(context.Background(), Channel, payload); err != nil && p.onError != nil {
		p.onError(err)
	}
}

// Revoked delegates to the wrapped deny list so a Propagator satisfies
// the §4.9 LLM proxy's DenyList interface.
func (p *Propagator) Revoked(key credential.CredentialKey) bool { return p.local.Revoked(key) }

// Len delegates to the wrapped deny list.
func (p *Propagator) Len() int { return p.local.Len() }

// Run subscribes to the credential-deny-list channel and applies a peer
// replica's revocations onto the local deny list until ctx is
// cancelled. It blocks; the gateway runs it in a goroutine alongside
// the other background loops. A Propagator built with a nil Bus has Run
// block until ctx is cancelled and apply nothing.
func (p *Propagator) Run(ctx context.Context) {
	p.bus.Subscribe(ctx, Channel, p.apply)
}

// apply decodes one pub/sub payload and revokes the credential on the
// local deny list. A payload that does not decode is ignored: a
// malformed message must not stall the subscribe loop.
func (p *Propagator) apply(payload []byte) {
	var key credential.CredentialKey
	if err := json.Unmarshal(payload, &key); err != nil {
		return
	}
	p.local.Revoke(key)
}
