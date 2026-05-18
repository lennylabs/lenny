// SPDX-License-Identifier: MIT

// Package propagator carries §10.3 mTLS certificate-deny-list
// mutations across gateway replicas over Redis pub/sub.
//
// pkg/mtls/denylist.DenyList is a single-replica primitive: an Add or
// Remove updates one replica's in-memory set. The deny list's own
// package doc states that cross-replica fan-out is the wrapping
// controller's responsibility. Propagator is that wrapper. Add and
// Remove update the local DenyList and publish the mutation on a Redis
// channel; Run subscribes to the channel and replays a peer replica's
// mutations onto the local DenyList. The result is that a certificate
// revoked on any replica is rejected on every replica.
//
// DenyList's own methods are unchanged. Code that already holds a
// *denylist.DenyList keeps calling it directly; the propagator is an
// additional path, not a replacement, so a local-only deployment (no
// Redis) keeps working with a nil Bus.
//
// Redis pub/sub is at-most-once. A dropped add is bounded by the
// short certificate TTL: the §10.3 deny list exists to cover the
// window before a 4h cert rotates, and a missed propagation closes
// when the certificate expires. A dropped remove is rare and benign:
// the entry expires at its own TTL regardless.
package propagator

import (
	"context"
	"encoding/json"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/pubsub"
	"github.com/lennylabs/lenny/pkg/mtls/denylist"
)

// Channel is the Redis pub/sub channel mTLS deny-list mutations are
// published on. Every gateway replica subscribes to it.
const Channel = "mtls:denylist:events"

// op enumerates the mutation kinds carried in a message.
type op string

const (
	opAdd    op = "add"
	opRemove op = "remove"
)

// message is the wire form of a deny-list mutation. Expiry is the
// Unix-nanosecond certificate expiry; it is unused for a remove.
type message struct {
	Op     op     `json:"op"`
	URI    string `json:"uri"`
	Expiry int64  `json:"expiry,omitempty"`
}

// Propagator wraps a local §10.3 deny list with Redis pub/sub fan-out.
// The zero value is not usable; construct with New. A Propagator built
// with a nil Bus applies mutations locally and publishes nothing,
// which is the single-replica mode.
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
// publish failure is not fatal: the deny list still updated locally,
// and the certificate TTL bounds a missed propagation.
func WithErrorHandler(fn func(error)) Option {
	return func(p *Propagator) { p.onError = fn }
}

// New returns a Propagator over local that fans mutations out on bus.
// bus may be nil, in which case the Propagator is a local-only pass
// through with no cross-replica propagation.
func New(local *denylist.DenyList, bus *pubsub.Bus, opts ...Option) *Propagator {
	p := &Propagator{local: local, bus: bus}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Local returns the wrapped deny list so callers on the request hot
// path (the mTLS handshake check) read the local set directly without
// going through the propagator.
func (p *Propagator) Local() *denylist.DenyList { return p.local }

// Add inserts uri into the local deny list with the supplied certificate
// expiry and publishes the addition so peer replicas apply it too. The
// local DenyList enforces the same expiry semantics it always has; the
// publish carries the expiry so a peer recreates the entry faithfully.
func (p *Propagator) Add(uri string, expiry time.Time) {
	p.local.Add(uri, expiry)
	p.publish(message{Op: opAdd, URI: uri, Expiry: expiry.UnixNano()})
}

// Remove drops uri from the local deny list and publishes the removal
// so peer replicas drop it too. Remove is used when a revocation is
// rescinded explicitly; the natural certificate TTL is the expected
// eviction path.
func (p *Propagator) Remove(uri string) {
	p.local.Remove(uri)
	p.publish(message{Op: opRemove, URI: uri})
}

// publish marshals m and sends it on the deny-list channel. A nil Bus
// publishes nothing. A publish failure is reported to onError, if set,
// and otherwise dropped: the mutation already applied locally.
func (p *Propagator) publish(m message) {
	payload, err := json.Marshal(m)
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

// Run subscribes to the deny-list channel and replays a peer replica's
// mutations onto the local DenyList until ctx is cancelled. It blocks;
// the gateway runs it in a goroutine alongside the other background
// loops. A Propagator built with a nil Bus has Run block until ctx is
// cancelled and apply nothing.
func (p *Propagator) Run(ctx context.Context) {
	p.bus.Subscribe(ctx, Channel, p.apply)
}

// apply decodes one pub/sub payload and replays the mutation on the
// local deny list. A payload that does not decode is ignored: a
// malformed message must not stall the subscribe loop. Note this
// re-applies the replica's own publishes too; an Add of an entry the
// local list already holds is idempotent, and DenyList.Add ignores an
// earlier expiry, so the round-trip is harmless.
func (p *Propagator) apply(payload []byte) {
	var m message
	if err := json.Unmarshal(payload, &m); err != nil {
		return
	}
	switch m.Op {
	case opAdd:
		p.local.Add(m.URI, time.Unix(0, m.Expiry))
	case opRemove:
		p.local.Remove(m.URI)
	}
}
