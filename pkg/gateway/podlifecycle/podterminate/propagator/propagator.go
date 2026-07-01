// SPDX-License-Identifier: MIT

// Package propagator fans a §11.4 full_revoke pod-termination out across
// gateway replicas over Redis pub/sub.
//
// On §11.4 full_revoke the gateway sends the §4.7 Terminate RPC (reason
// USER_REVOKED) to every pod hosting one of the revoked user's sessions
// (step 2: "For each session in the user's task tree"). A pod binding
// lives in one replica's per-replica podsession.Registry, so the
// handling replica reaches only the pods it itself coordinates; a pod
// bound on a peer replica keeps running until the §8.10 orphan sweep
// reaps it. The credential-lease and token revocations (steps 5-6)
// already fan out over pub/sub, but the Terminate RPC (steps 2-4) did
// not, so a revoked user's peer-replica pods ran for up to the
// orphan-sweep window without the graceful SIGTERM the spec mandates.
//
// Propagator closes that gap. Publish marshals the user's session ids
// and publishes them on a Redis channel; Run subscribes and applies a
// peer replica's request to the local LocalTerminator, which terminates
// the pods this replica holds among those sessions. The handling replica
// terminates its own pods synchronously (for the response counts) and
// stamps its publish with its replica id so it does not re-terminate
// them when its own message round-trips. A full_revoke on any replica
// then reaches every replica's pods.
//
// Redis pub/sub is at-most-once, matching the §4.9 credential-deny-list
// and §13.3 token-revocation fan-outs the gateway already runs. A
// dropped request leaves the peer's pod running until the §8.10 orphan
// sweep reaps it (the pre-existing backstop), and the credential and
// token revocations that did land already bar the pod from reaching the
// LLM proxy or calling back into the gateway. Propagator does not
// redeliver.
package propagator

import (
	"context"
	"encoding/json"

	"github.com/lennylabs/lenny/pkg/gateway/storage/pubsub"
)

// Channel is the Redis pub/sub channel §11.4 full_revoke pod-termination
// requests are published on. It is distinct from the §4.9/§13.3
// revocation channel: a termination carries a session set rather than a
// credential key and drives the pod adapter rather than a deny list.
const Channel = "lenny:session:terminate"

// Request is the cross-replica §11.4 step-2 pod-termination payload: the
// revoked user's session ids whose hosting pods each replica must
// terminate. Origin is the publishing replica's id, used so a replica
// skips its own request (it already terminated those pods synchronously
// for the response counts). spec: §11.4 step 2.
type Request struct {
	TenantID   string   `json:"tenantId"`
	UserID     string   `json:"userId"`
	Reason     string   `json:"reason"`
	Origin     string   `json:"origin"`
	SessionIDs []string `json:"sessionIds"`
}

// Result reports one local termination pass: the pods this replica
// terminated and the sessions whose pod termination failed.
type Result struct {
	PodsTerminated int
	FailedSessions []string
}

// LocalTerminator terminates the pods this replica holds a binding for
// among a request's sessions. The gateway's pod-termination fan-out over
// the per-replica podsession.Registry satisfies it. The implementation
// must not itself re-publish — Run drives it for a request that already
// arrived over the bus.
type LocalTerminator interface {
	TerminateLocal(ctx context.Context, req Request) Result
}

// Propagator fans a §11.4 full_revoke pod-termination across replicas.
// The zero value is unusable; construct with New. A Propagator built
// with a nil Bus publishes nothing and its Run blocks until ctx is
// cancelled, which is the single-replica posture.
type Propagator struct {
	local     LocalTerminator
	bus       *pubsub.Bus
	replicaID string
	onError   func(error)
}

// Option configures a Propagator at construction.
type Option func(*Propagator)

// WithErrorHandler sets a callback invoked when a publish fails. A
// publish failure is not fatal: the handling replica already terminated
// its own pods and the §8.10 orphan sweep reaps a missed peer's pod.
func WithErrorHandler(fn func(error)) Option {
	return func(p *Propagator) { p.onError = fn }
}

// New returns a Propagator that applies remote requests to local and
// fans local requests out on bus. bus may be nil, in which case the
// Propagator publishes nothing and Run blocks — the single-replica
// posture. replicaID identifies this replica so a self-originated
// message is skipped on receipt.
func New(local LocalTerminator, bus *pubsub.Bus, replicaID string, opts ...Option) *Propagator {
	p := &Propagator{local: local, bus: bus, replicaID: replicaID}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Publish fans a §11.4 pod-termination request out to peer replicas. It
// stamps the request with this replica's id (so the publisher skips its
// own message on receipt) and publishes it on Channel. A publish failure
// is reported through the error handler and is not fatal. A nil Bus or an
// empty session set publishes nothing.
func (p *Propagator) Publish(ctx context.Context, req Request) {
	if p.bus == nil || len(req.SessionIDs) == 0 {
		return
	}
	req.Origin = p.replicaID
	payload, err := json.Marshal(req)
	if err != nil {
		if p.onError != nil {
			p.onError(err)
		}
		return
	}
	if perr := p.bus.Publish(ctx, Channel, payload); perr != nil && p.onError != nil {
		p.onError(perr)
	}
}

// Run subscribes to Channel and applies a peer replica's pod-termination
// request to the local terminator until ctx is cancelled. It blocks; the
// gateway runs it in a goroutine alongside the other revocation
// subscribers. A request this replica published is skipped — the
// publishing replica terminated those pods synchronously, so
// re-terminating on receipt would be a redundant RPC. A Propagator built
// with a nil Bus blocks until ctx is cancelled and applies nothing.
func (p *Propagator) Run(ctx context.Context) {
	p.bus.Subscribe(ctx, Channel, func(payload []byte) { p.apply(ctx, payload) })
}

// apply decodes one request and terminates the local pods for its
// sessions. A request this replica originated is skipped. A payload that
// does not decode is ignored so a malformed message cannot stall the
// subscribe loop.
func (p *Propagator) apply(ctx context.Context, payload []byte) {
	var req Request
	if err := json.Unmarshal(payload, &req); err != nil {
		return
	}
	if req.Origin == p.replicaID {
		return
	}
	p.local.TerminateLocal(ctx, req)
}
