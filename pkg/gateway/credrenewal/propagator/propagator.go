// SPDX-License-Identifier: MIT

// Package propagator carries §4.9 credential-lease revocations across
// gateway replicas over Redis pub/sub.
//
// A §4.9 credential-lease revocation has two per-replica effects. The
// credential's source-aware identity must land on the replica's
// in-memory deny list, which the §4.9 LLM reverse proxy checks on every
// upstream request. The replica's §4.9 CredentialRenewalWorker must
// also drop every tracked lease bound to the revoked credential, so a
// proactive renewal is not issued against a credential that is no
// longer trustworthy and the affected sessions fall through to fault
// rotation. denylist.DenyList and credrenewal.Worker are each a
// single-replica primitive: a Revoke on one mutates one replica's
// state.
//
// Propagator joins the two and fans the revocation out. A Revoke
// updates the local deny list, drops the local renewal worker's
// tracked leases, and publishes the credential key on a Redis channel;
// Run subscribes to the channel and applies a peer replica's
// revocations onto the local deny list and renewal worker. A
// credential-lease revocation on any replica then takes effect on every
// replica.
//
// The fan-out reuses the channel and the credential-key encoding the
// §4.9 credential-deny-list propagator (pkg/gateway/denylist/propagator)
// already publishes on, so an emergency revocation and a renewal
// exhaustion converge through the same Redis pub/sub mechanism the
// §13.3 token revocation and the §10.3 mTLS certificate revocation use.
// There is no second pub/sub substrate.
//
// Redis pub/sub is at-most-once. A dropped revocation is reconciled by
// the §4.9 startup deny-list rebuild on the next replica restart; for
// the live window, a missed publish leaves a revoked credential
// reachable on the replicas that missed it until the rebuild. The same
// at-most-once caveat applies to the credential-deny-list fan-out and
// the circuit-breaker fan-out the gateway already runs.
package propagator

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/denylist"
	denylistprop "github.com/lennylabs/lenny/pkg/gateway/denylist/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/pubsub"
)

// Channel is the Redis pub/sub channel revoked credential keys are
// published on. It is the same channel the §4.9 credential-deny-list
// propagator uses: a credential-lease revocation and a deny-list
// revocation are the same fleet-wide event, so they share one channel
// and one subscriber keyspace rather than a second pub/sub mechanism.
const Channel = denylistprop.Channel

// PGChannel is the §4.9 Postgres LISTEN/NOTIFY fallback channel, shared
// with the credential-deny-list propagator so both transports carry the
// same fleet-wide revocation event. spec: §4.9 line 1647.
const PGChannel = denylistprop.PGChannel

// CredentialRevoker is the subset of credrenewal.Worker the propagator
// drives: marking a pool credential revoked so the renewal worker drops
// every tracked lease bound to it on the next sweep. *credrenewal.Worker
// satisfies it.
type CredentialRevoker interface {
	Revoke(credentialID string)
}

// Propagator joins a §4.9 credential deny list and a §4.9
// CredentialRenewalWorker with Redis pub/sub fan-out. The zero value is
// not usable; construct with New. A Propagator built with a nil Bus
// applies revocations locally and publishes nothing, which is the
// single-replica mode.
type Propagator struct {
	denyList *denylist.DenyList
	worker   CredentialRevoker
	bus      *pubsub.Bus
	// fallback carries the §4.9 Postgres LISTEN/NOTIFY transport used when
	// Redis publish fails or no Redis bus is wired. Nil disables it.
	fallback denylistprop.Fallback
	// onError observes a publish failure. Nil means failures are
	// swallowed; the gateway passes a logging callback.
	onError func(error)
}

// Option configures a Propagator at construction.
type Option func(*Propagator)

// WithErrorHandler sets a callback invoked when a publish fails. A
// publish failure is not fatal: the credential is still revoked
// locally, and the §4.9 startup deny-list rebuild reconciles a missed
// propagation.
func WithErrorHandler(fn func(error)) Option {
	return func(p *Propagator) { p.onError = fn }
}

// WithFallback wires the §4.9 Postgres LISTEN/NOTIFY fallback. A Revoke
// publishes on it when the Redis publish fails (or when no Redis bus is
// wired), and Run additionally subscribes on it so a peer replica's
// revocation reaches this replica over Postgres even while Redis is
// down. spec: §4.9 line 1647. F-13.3.8.
func WithFallback(fb denylistprop.Fallback) Option {
	return func(p *Propagator) { p.fallback = fb }
}

// New returns a Propagator that revokes credentials on denyList and
// worker and fans the revocation out on bus. worker may be nil — a
// gateway with no credential pools runs no renewal worker — in which
// case only the deny list is updated. bus may be nil, in which case the
// Propagator is a local-only pass through with no cross-replica
// propagation.
func New(denyList *denylist.DenyList, worker CredentialRevoker, bus *pubsub.Bus, opts ...Option) *Propagator {
	p := &Propagator{denyList: denyList, worker: worker, bus: bus}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Revoke revokes a §4.9 credential lease fleet-wide. It adds key to the
// local deny list, marks the credential revoked on the local renewal
// worker so its tracked leases are dropped on the next sweep, and
// publishes key so peer replicas do the same. The signature matches
// denylist.DenyList.Revoke, so a Propagator is a drop-in wherever the
// raw deny list or the §4.9 credential-deny-list propagator is wired:
// the §11.4 full_revoke fan-out and the emergency-revocation path route
// through it with no other change.
func (p *Propagator) Revoke(key credential.CredentialKey) {
	p.applyLocal(key)
	payload, err := json.Marshal(key)
	if err != nil {
		if p.onError != nil {
			p.onError(err)
		}
		return
	}
	// §4.9 line 1647: Redis pub/sub is primary with Postgres
	// LISTEN/NOTIFY as fallback. The Postgres path carries the revocation
	// when the Redis publish fails (Redis down) or when no Redis bus is
	// configured. F-13.3.8.
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
func (p *Propagator) Revoked(key credential.CredentialKey) bool { return p.denyList.Revoked(key) }

// Len delegates to the wrapped deny list.
func (p *Propagator) Len() int { return p.denyList.Len() }

// IsCrossReplicaPropagator is a marker that wiring sites (notably the
// §11.4 full_revoke fan-out — see cmd/lenny-gateway/user_revocation.go)
// require to refuse a bare *denylist.DenyList that would silently lose
// cross-replica fan-out. A Propagator constructed with a nil bus still
// returns true: the local-only single-replica posture is a deliberate
// constructor choice, not an accidental wiring downgrade. spec: §11.4
// step 6.
func (p *Propagator) IsCrossReplicaPropagator() {}

// Run subscribes to the credential-revocation channel and applies a
// peer replica's revocations onto the local deny list and renewal
// worker until ctx is cancelled. It blocks; the gateway runs it in a
// goroutine alongside the other background loops. A Propagator built
// with a nil Bus has Run block until ctx is cancelled and apply
// nothing.
func (p *Propagator) Run(ctx context.Context) {
	if p.fallback == nil {
		p.bus.Subscribe(ctx, Channel, p.apply)
		return
	}
	// §4.9 line 1647: subscribe on both transports so a revocation reaches
	// this replica over whichever is live. apply is idempotent, so a key
	// received on both channels is harmless. F-13.3.8.
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

// apply decodes one pub/sub payload and revokes the credential locally.
// A payload that does not decode is ignored: a malformed message must
// not stall the subscribe loop.
func (p *Propagator) apply(payload []byte) {
	var key credential.CredentialKey
	if err := json.Unmarshal(payload, &key); err != nil {
		return
	}
	p.applyLocal(key)
}

// applyLocal revokes a credential on this replica's deny list and
// renewal worker. The renewal worker tracks leases by the pool
// credential id, so only a pool-backed key carries a worker revocation;
// a user-backed key updates the deny list alone.
func (p *Propagator) applyLocal(key credential.CredentialKey) {
	p.denyList.Revoke(key)
	if p.worker != nil && key.Source == credential.SourcePool && key.CredentialID != "" {
		p.worker.Revoke(key.CredentialID)
	}
}
