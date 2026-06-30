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
	"sync"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/denylist"
	"github.com/lennylabs/lenny/pkg/gateway/storage/pubsub"
)

// Channel is the Redis pub/sub channel revoked credential keys are
// published on. Every gateway replica subscribes to it.
const Channel = "credential:denylist:events"

// PGChannel is the Postgres LISTEN/NOTIFY channel the §4.9 fallback uses
// when Redis is unavailable. It is a plain SQL identifier (Redis's
// colon-delimited Channel is not a valid LISTEN identifier without
// quoting); the two substrates carry the same JSON-encoded credential
// key. spec: §4.9 line 1647.
const PGChannel = "lenny_credential_denylist"

// Fallback is the §4.9 Postgres LISTEN/NOTIFY substrate the propagator
// drives when Redis pub/sub is down or disabled. pkg/gateway/pgnotify.Bus
// satisfies it. The interface mirrors the pubsub.Bus surface so the two
// transports are interchangeable.
type Fallback interface {
	// Publish raises a notification on channel carrying payload.
	Publish(ctx context.Context, channel string, payload []byte) error
	// Subscribe delivers every notification payload on channel to handler
	// until ctx is cancelled. It blocks.
	Subscribe(ctx context.Context, channel string, handler func(payload []byte))
}

// Propagator wraps a local §4.9 credential deny list with Redis pub/sub
// fan-out. The zero value is not usable; construct with New. A
// Propagator built with a nil Bus applies revocations locally and
// publishes nothing, which is the single-replica mode.
type Propagator struct {
	local *denylist.DenyList
	bus   *pubsub.Bus
	// fallback carries the §4.9 Postgres LISTEN/NOTIFY transport used when
	// Redis publish fails or no Redis bus is wired. Nil disables it.
	fallback Fallback
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

// WithFallback wires the §4.9 Postgres LISTEN/NOTIFY fallback. A Revoke
// publishes on it when the Redis publish fails (or when no Redis bus is
// wired), and Run additionally subscribes on it so a revocation raised
// by a peer over Postgres reaches this replica even while Redis is down.
// spec: §4.9 line 1647. F-13.3.8.
func WithFallback(fb Fallback) Option {
	return func(p *Propagator) { p.fallback = fb }
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
//
// §4.9 line 1647: Redis pub/sub is the primary transport with Postgres
// LISTEN/NOTIFY as fallback. Redis is attempted first when a Redis bus
// is wired; the Postgres fallback carries the revocation when the Redis
// publish fails (Redis down) or when no Redis bus is configured at all.
func (p *Propagator) Revoke(key credential.CredentialKey) {
	p.local.Revoke(key)
	payload, err := json.Marshal(key)
	if err != nil {
		if p.onError != nil {
			p.onError(err)
		}
		return
	}
	redisOK := false
	if p.bus != nil {
		if perr := p.bus.Publish(context.Background(), Channel, payload); perr != nil {
			if p.onError != nil {
				p.onError(perr)
			}
		} else {
			redisOK = true
		}
	}
	if !redisOK && p.fallback != nil {
		if perr := p.fallback.Publish(context.Background(), PGChannel, payload); perr != nil && p.onError != nil {
			p.onError(perr)
		}
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
//
// When a §4.9 Postgres fallback is wired, Run subscribes on both the
// Redis channel and the Postgres LISTEN/NOTIFY channel concurrently, so
// a revocation reaches this replica over whichever transport is live.
// apply is idempotent (Revoke on a set), so receiving the same key on
// both channels is harmless. spec: §4.9 line 1647. F-13.3.8.
func (p *Propagator) Run(ctx context.Context) {
	if p.fallback == nil {
		p.bus.Subscribe(ctx, Channel, p.apply)
		return
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		p.bus.Subscribe(ctx, Channel, p.apply)
	}()
	go func() {
		defer wg.Done()
		p.fallback.Subscribe(ctx, PGChannel, p.apply)
	}()
	wg.Wait()
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
