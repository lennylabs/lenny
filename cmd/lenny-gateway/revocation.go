// SPDX-License-Identifier: MIT

package main

import (
	"log"
	"time"

	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/gateway/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credrenewal"
	credrenewalprop "github.com/lennylabs/lenny/pkg/gateway/credrenewal/propagator"
	"github.com/lennylabs/lenny/pkg/gateway/denylist"
	"github.com/lennylabs/lenny/pkg/gateway/pgnotify"
	revocationprop "github.com/lennylabs/lenny/pkg/gateway/revocation/propagator"
	mtlsdenylistprop "github.com/lennylabs/lenny/pkg/mtls/denylist/propagator"
)

// buildRevocationWiring is the §4.1 composition-root build step (R1) for the
// cross-replica revocation propagators and the §4.9 proactive lease-renewal
// worker. It wraps the §13.3 token-revocation cache, the §10.3 mTLS deny list,
// and the §4.9 credential deny list with their Redis pub/sub (and Postgres
// LISTEN/NOTIFY fallback) propagators, constructs the §4.9 credrenewal worker
// (tracking each minted credential lease by its renewBefore deadline and
// wiring the §4.9 emergency-revocation direct-mode rotate hook), rebuilds the
// credential-lease propagator over the live worker, and threads the
// user-credential revoker. It records the propagators, the deny list, and the
// renewal worker on the accumulator so the admin router, the LLM proxy, the
// HTTP surface, and the background workers read them back.
//
// spec: §4.1 gateway subsystem seams; §13.3 token / mTLS revocation; §4.9
// credential-lease revocation and proactive renewal.
func (w *gatewayWiring) buildRevocationWiring() {
	credentialsExpiryWarningLeadSeconds := w.f.credentialsExpiryWarningLeadSeconds
	opsEmitter := w.opsEmitter
	userCredMaterializer := w.userCredMaterializer

	// §13.3 revocation cache: the auth middleware rejects a token
	// whose jti is in this set. It is rehydrated from the Postgres
	// issued-token index below. The propagator wraps the cache with
	// Redis pub/sub fan-out so a revocation on any replica reaches every
	// replica within pub/sub latency; with no Redis the propagator is a
	// local-only pass-through. revCache stays the read primitive the
	// auth middleware and the rehydration loop use directly. revCache is
	// constructed above (shared with the §8.2 child-token minter).
	revProp := revocationprop.New(w.revCache, w.securityBus, revocationprop.WithErrorHandler(func(err error) {
		log.Printf("lenny-gateway: token-revocation pub/sub publish failed: %v", err)
	}))

	// §10.3 mTLS certificate deny list: the per-replica SPIFFE-URI deny
	// set checked on every mTLS handshake (declared earlier so the
	// §10.3 NET-063 interceptor dials can consult it). Its propagator
	// carries an Add or Remove across replicas over Redis pub/sub. The
	// deny list is a single-replica primitive; the propagator owns the
	// fan-out the package doc defers to a wrapping controller.
	mtlsDenyProp := mtlsdenylistprop.New(w.mtlsDeny, w.securityBus, mtlsdenylistprop.WithErrorHandler(func(err error) {
		log.Printf("lenny-gateway: mTLS deny-list pub/sub publish failed: %v", err)
	}))

	// §4.9 credential deny list: the per-replica set of revoked
	// credential identities the LLM proxy checks before every upstream
	// call. The §4.9 LLM proxy below reads it directly on the hot path;
	// the credential-lease revocation propagator built next wraps it
	// with cross-replica Redis pub/sub fan-out, so the admin router's
	// §11.4 full_revoke fan-out and the emergency-revocation path revoke
	// a credential onto every replica's deny list.
	credDeny := denylist.New()

	// ----- §4.9 Proactive Lease Renewal worker -----
	// The credrenewal.Worker tracks each active credential lease by its
	// renewBefore deadline and issues a replacement before the original
	// expires, so a long-lived session never sees its LLM credential
	// lapse. credRenewal binds the worker to the credential-assignment
	// service that mints the replacement and to the warm-pod registry it
	// pushes the rotated credential to via the §4.7 RotateCredentials
	// RPC. credRenewal is nil when no credential pools are wired; a nil
	// receiver leaves every renewal hook a no-op.
	credRenewal := newCredRenewalWiring(w.credAssign, w.podRegistry, opsEmitter)
	// credRenewalProp carries a §4.9 credential-lease revocation across
	// replicas: a Revoke updates the local deny list, drops the renewal
	// worker's tracked leases bound to the credential, and fans out over
	// the same Redis pub/sub channel the §4.9 credential-deny-list
	// propagator uses. The §11.4 full_revoke fan-out and the emergency-
	// revocation path route through it so a revoked credential lease
	// stops reaching the provider on every replica, and no replica
	// proactively renews a credential that is no longer trustworthy.
	// §4.9 line 1647: the credential deny-list revocation propagates via
	// Redis pub/sub with Postgres LISTEN/NOTIFY as fallback. The Postgres
	// half is wired only when Postgres is configured (the option is
	// omitted otherwise so a no-Postgres dev gateway keeps a true-nil
	// fallback); it carries a revocation when Redis is down or disabled
	// and feeds the LISTEN subscribe loop so a peer's revocation still
	// converges. F-13.3.8.
	credDenyPropOpts := []credrenewalprop.Option{
		credrenewalprop.WithErrorHandler(func(err error) {
			log.Printf("lenny-gateway: credential-lease revocation pub/sub publish failed: %v", err)
		}),
	}
	if w.pgPool != nil {
		credDenyPropOpts = append(credDenyPropOpts, credrenewalprop.WithFallback(pgnotify.New(w.pgPool)))
	}
	// §4.9 line 1649 emergency-revocation step 5: when the gateway mints
	// leases in-process, wire the direct-mode rotate as a revoke hook on
	// the credential-lease propagator. A revoked pool credential then
	// proactively rotates every direct-delivery pod off the materialized
	// key on whichever replica holds the binding, minting the replacement
	// from a different credential in the same pool. The deny list already
	// terminates proxy-mode access fleet-wide; this adds the direct-mode
	// proactive push the deny list cannot deliver. The token-service
	// minting path (--token-service-grpc-addr) carries no per-credential
	// revocation surface yet, so the rotate is wired only for the
	// in-process path; the deny-list termination still applies in both.
	if w.inProcessAssign != nil {
		if ls, ok := w.llmLeases.(poolLeaseStore); ok {
			directRotator := &directModeRevocationRotator{
				leases:      ls,
				markRevoked: w.inProcessAssign.RevokeCredential,
				rotate:      proxyFallbackRotator{assign: w.credAssign, registry: w.podRegistry}.Rotate,
			}
			credDenyPropOpts = append(credDenyPropOpts,
				credrenewalprop.WithRevokeHook(directRotator.onRevoke))
		}
	}
	var credRenewalWorker *credrenewal.Worker
	credRenewalProp := credrenewalprop.New(credDeny, nil, w.securityBus, credDenyPropOpts...)
	if credRenewal != nil {
		// spec: §11.3 line 215 — credentials.expiryWarningLeadSeconds.
		// 0 disables warnings; -1 keeps the package default; any other
		// non-negative value is the explicit operator override.
		expiryWarningLead := time.Duration(*credentialsExpiryWarningLeadSeconds) * time.Second
		credRenewalWorker = credrenewal.New(credRenewal, credrenewal.Options{
			// §4.9: a proactive renewal that rotates a lease onto a fresh
			// credential pushes it to the lease's pod via RotateCredentials.
			OnRenewed: credRenewal.onRenewed,
			// §4.9: a lease whose renewal cannot proceed falls through to
			// fault rotation. The worker drops it; onExhausted clears its
			// pool binding.
			OnExhausted: credRenewal.onExhausted,
			Clock:       clockinject.Now,
			// spec: §11.3 line 215 — operator-tunable expiry-warning lead.
			// F-11.3.20.
			ExpiryWarningLead: expiryWarningLead,
			OnExpiryWarning:   logCredentialExpiryWarning,
		})
		log.Printf("lenny-gateway: §11.3 line 215 credentials.expiryWarningLeadSeconds=%ds", int(expiryWarningLead/time.Second))
		// Every §4.9 credential lease the assignment service mints — at
		// session start and at fault rotation — is tracked by the renewal
		// worker so its renewBefore deadline drives a proactive renewal.
		w.credAssign.OnAssigned(func(a credassign.LeaseAssignment) {
			credRenewal.track(credRenewalWorker, a.PoolName, string(a.Lease.Provider), a.Lease)
		})
		// Rebuild the propagator over the live worker so a peer replica's
		// credential-lease revocation also drops this replica's tracked
		// leases for the credential, not just its deny-list entry.
		credRenewalProp = credrenewalprop.New(credDeny, credRenewalWorker, w.securityBus, credDenyPropOpts...)
	}
	// spec: §4.9 lines 1640-1652 — wire the user-credential revocation onto
	// the cross-replica deny-list propagator so a POST /v1/credentials/{ref}
	// /revoke adds the user-shaped deny-list entry on every replica. Set
	// after the propagator's final form (it is rebuilt above over the live
	// renewal worker).
	if userCredMaterializer != nil {
		userCredMaterializer.SetRevoker(credRenewalProp)
	}

	w.revProp = revProp
	w.mtlsDenyProp = mtlsDenyProp
	w.credDeny = credDeny
	w.credRenewalWorker = credRenewalWorker
	w.credRenewalProp = credRenewalProp
}
