// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credrenewal"
	"github.com/lennylabs/lenny/pkg/gateway/events"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// This file wires the §4.9 Proactive Lease Renewal loop into the
// gateway. The credrenewal.Worker tracks each active credential lease
// by its renewBefore deadline and issues a replacement lease before the
// original expires; the wiring here connects the worker to the §4.9
// credential-assignment service that mints the replacement and to the
// §4.7 RotateCredentials RPC that pushes it to the lease's pod.
//
// Every dependency is optional. A gateway with no credential pools runs
// no renewal worker at all (newCredRenewalWiring returns nil), and a
// gateway without warm-pod placement renews leases without a pod to
// push to. The nil receiver methods keep those degraded modes from
// branching at every call site.

// credRotateRPCTimeout bounds each §4.7 RotateCredentials RPC the
// renewal loop sends to a pod. A Full-level runtime's
// credentials_rotated handshake is bounded by the §4.7 unbounded
// in-flight wait for a proactive_renewal trigger, but the gateway-side
// RPC still needs an upper bound so a wedged pod does not pin the
// renewal sweep goroutine.
const credRotateRPCTimeout = 30 * time.Second

// renewalProvider is the per-provider §4.9 pool binding the renewal
// worker needs to re-mint a lease. credrenewal.Lease carries only the
// renewBefore/expiresAt windows the worker schedules on, so the wiring
// records the pool name and provider key a tracked lease was minted
// from and replays them when the worker asks for a replacement.
type renewalProvider struct {
	// pool is the §4.9 credential pool the lease was minted from.
	pool string
	// provider is the §4.7 AssignCredentials leases-map key (the
	// runtime-facing provider name) the lease serves.
	provider string
	// tenantID is the owning tenant the original lease was minted for,
	// replayed so the replacement carries the same tenant attribution
	// (spec: §4.9 line 1468 — proxy-extracted usage attribution).
	tenantID string
}

// credRenewalWiring binds the §4.9 credrenewal.Worker to the gateway's
// credential-assignment service and warm-pod registry. It satisfies
// credrenewal.Renewer and supplies the worker's OnRenewed and
// OnExhausted hooks.
type credRenewalWiring struct {
	// assign mints replacement leases — the §4.9 AssignCredentials path
	// the renewal worker reuses (spec §4.9 Proactive Lease Renewal,
	// step 2). The interface lets the wiring run against either the
	// in-process credassign.Service (used by lenny-token-service) or
	// the gateway-side credassign.Client (the §4.3 mTLS gRPC client).
	assign credassign.Assigner
	// registry resolves a session's pod so a rotated credential can be
	// pushed to it. Nil without warm-pod placement.
	registry *podsession.Registry
	// emitter, when set, publishes §16.6 credential_rotated and
	// credential_pool_exhausted events on the renewal lifecycle. §4.0
	// requires the credential pool manager to emit both events; a nil
	// emitter is a no-op.
	emitter events.EventEmitter

	mu sync.Mutex
	// pools maps a tracked lease ID to the pool/provider it was minted
	// from, so a replacement can be leased from the same pool.
	pools map[string]renewalProvider
}

// newCredRenewalWiring returns the renewal wiring, or nil when no
// credential pools are registered. registry may be nil when the gateway
// runs without warm-pod placement; a renewal then refreshes the lease
// record without a RotateCredentials push. emitter is the §4.0 events
// sink for credential_rotated / credential_pool_exhausted; nil disables
// emission without affecting the renewal lifecycle.
func newCredRenewalWiring(assign credassign.Assigner, registry *podsession.Registry, emitter events.EventEmitter) *credRenewalWiring {
	if assign == nil {
		return nil
	}
	return &credRenewalWiring{
		assign:   assign,
		registry: registry,
		emitter:  emitter,
		pools:    make(map[string]renewalProvider),
	}
}

// track records the pool and provider a lease was minted from and
// registers the lease with the worker for proactive renewal. The
// session-start path calls it for each lease the §4.7 binder assigned,
// so the worker can renew the lease and re-mint a replacement from the
// same pool.
func (w *credRenewalWiring) track(worker *credrenewal.Worker, pool, provider string, lease credential.Lease) {
	if w == nil || worker == nil {
		return
	}
	w.mu.Lock()
	w.pools[lease.LeaseID] = renewalProvider{pool: pool, provider: provider, tenantID: lease.TenantID}
	w.mu.Unlock()
	worker.Track(credrenewal.Lease{
		LeaseID:      lease.LeaseID,
		SessionID:    lease.SessionID,
		CredentialID: lease.CredentialID,
		RenewBefore:  lease.RenewBefore,
		ExpiresAt:    lease.ExpiresAt,
	})
}

// Renew issues a replacement lease for a lease approaching its
// renewBefore deadline. It leases a fresh credential from the same §4.9
// pool the original lease was minted from (spec §4.9 Proactive Lease
// Renewal, step 2), records the replacement's pool binding, and drops
// the spent lease's binding. It satisfies credrenewal.Renewer.
func (w *credRenewalWiring) Renew(_ context.Context, lease credrenewal.Lease) (credrenewal.Lease, error) {
	w.mu.Lock()
	rp, ok := w.pools[lease.LeaseID]
	w.mu.Unlock()
	if !ok {
		// The lease was not registered through track; without its pool
		// binding the worker cannot re-mint. Returning an error lets the
		// worker retry and ultimately fall through to fault rotation.
		return credrenewal.Lease{}, errNoPoolBinding
	}

	// §4.9 step 2: the renewal worker calls the same path as
	// AssignCredentials, selecting a credential from the same pool. The
	// SPIFFE-binding identity is re-derived at proxy-request time from
	// the lease record, so the renewal mint does not need it here.
	next, err := w.assign.Assign(rp.pool, lease.SessionID, "", rp.tenantID)
	if err != nil {
		return credrenewal.Lease{}, err
	}

	w.mu.Lock()
	delete(w.pools, lease.LeaseID)
	w.pools[next.LeaseID] = rp
	w.mu.Unlock()

	return credrenewal.Lease{
		LeaseID:      next.LeaseID,
		SessionID:    next.SessionID,
		CredentialID: next.CredentialID,
		RenewBefore:  next.RenewBefore,
		ExpiresAt:    next.ExpiresAt,
	}, nil
}

// onRenewed pushes a proactively renewed lease to its session's pod via
// the §4.7 RotateCredentials RPC (spec §4.9 Proactive Lease Renewal,
// step 3 — "Push the replacement credential to the runtime via the
// standard RotateCredentials RPC"). The push targets the pod this
// replica holds the binding for; a lease whose pod is bound on another
// replica, or already released, is skipped. The rotated credential is
// converted to the §4.7 wire form before the push. The same call also
// emits the §16.6 credential_rotated event for §4.0.
func (w *credRenewalWiring) onRenewed(renewed credrenewal.Lease) {
	if w == nil {
		return
	}
	w.emitCredentialRotated(renewed)
	if w.registry == nil {
		return
	}
	bind, ok := w.registry.Get(renewed.SessionID)
	if !ok || bind.Adapter == nil {
		// No pod binding on this replica: the renewal worker still
		// tracks the fresh lease, and the lease record is updated, but
		// there is no local pod to push the rotation to.
		return
	}

	wire, err := w.assign.ProtoLeaseByID(renewed.LeaseID)
	if err != nil {
		log.Printf("lenny-gateway: §4.9 proactive renewal: encode rotated lease %s for session %s: %v",
			renewed.LeaseID, renewed.SessionID, err)
		return
	}

	w.mu.Lock()
	rp := w.pools[renewed.LeaseID]
	w.mu.Unlock()
	// The §4.7 RotateCredentials leases map is keyed by the
	// runtime-facing provider; the adapter rewrites only that provider's
	// credential-file entry and retains the rest.
	provider := rp.provider
	if provider == "" {
		provider = wire.GetProvider()
	}
	wire.Provider = provider

	ctx, cancel := context.WithTimeout(context.Background(), credRotateRPCTimeout)
	defer cancel()
	// §4.9 rotationTrigger: proactive_renewal. The trigger is internal
	// gateway rotation context; the §4.7 wire contract carries only the
	// session id and the rotated lease map.
	if err := bind.Adapter.RotateCredentials(ctx, renewed.SessionID,
		map[string]*adapterv1.CredentialLease{provider: wire}); err != nil {
		log.Printf("lenny-gateway: §4.9 proactive renewal: RotateCredentials push to session %s pod failed: %v",
			renewed.SessionID, err)
	}
}

// onExhausted handles a lease whose proactive renewal cannot proceed —
// the lease expired, or the retry budget is exhausted, or its
// credential was revoked. It drops the spent lease's pool binding and
// emits §16.6 credential_pool_exhausted for §4.0. The §4.9 fall-through
// to fault rotation and the cross-replica deny-list fan-out are driven
// by the caller's OnExhausted composition in main.
func (w *credRenewalWiring) onExhausted(lease credrenewal.Lease) {
	if w == nil {
		return
	}
	w.mu.Lock()
	rp := w.pools[lease.LeaseID]
	delete(w.pools, lease.LeaseID)
	w.mu.Unlock()
	w.emitCredentialPoolExhausted(lease, rp.pool, rp.provider)
}

// emitCredentialRotated publishes the §16.6 credential_rotated event
// per §4.0 with pool, credentialId, sessionId, leaseId, and the
// proactive_renewal trigger that drives the renewal worker.
func (w *credRenewalWiring) emitCredentialRotated(renewed credrenewal.Lease) {
	if w.emitter == nil {
		return
	}
	w.mu.Lock()
	rp := w.pools[renewed.LeaseID]
	w.mu.Unlock()
	data, _ := json.Marshal(map[string]any{
		"pool":         rp.pool,
		"credentialId": renewed.CredentialID,
		"sessionId":    renewed.SessionID,
		"leaseId":      renewed.LeaseID,
		"provider":     rp.provider,
		"reason":       string(credential.TriggerProactiveRenewal),
	})
	_, _ = w.emitter.Emit(context.Background(), events.OperationalEvent{
		Source:          "//lenny.dev/credential-pool",
		Type:            events.EventCredentialRotated.CloudEventsType(),
		Severity:        "info",
		DataContentType: "application/json",
		Data:            data,
	})
}

// emitCredentialPoolExhausted publishes the §16.6
// credential_pool_exhausted event per §4.0. pool and provider may be
// empty when the lease was never registered through track (the pool
// binding had already been dropped); the event still surfaces the lease
// and session so an ops agent can correlate the fall-through.
func (w *credRenewalWiring) emitCredentialPoolExhausted(lease credrenewal.Lease, pool, provider string) {
	if w.emitter == nil {
		return
	}
	data, _ := json.Marshal(map[string]any{
		"pool":         pool,
		"credentialId": lease.CredentialID,
		"sessionId":    lease.SessionID,
		"leaseId":      lease.LeaseID,
		"provider":     provider,
	})
	_, _ = w.emitter.Emit(context.Background(), events.OperationalEvent{
		Source:          "//lenny.dev/credential-pool",
		Type:            events.EventCredentialPoolExhausted.CloudEventsType(),
		Severity:        "warning",
		DataContentType: "application/json",
		Data:            data,
	})
}

// errNoPoolBinding reports that a lease handed to Renew was never
// registered through track, so the worker has no pool to re-mint from.
var errNoPoolBinding = errNoPoolBindingErr{}

type errNoPoolBindingErr struct{}

func (errNoPoolBindingErr) Error() string {
	return "credrenewal: lease has no recorded pool binding; cannot re-mint a replacement"
}
