// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log"

	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/api/admin"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/meteringproxy/llmproxy"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// This file wires the §4.9 credentialPolicy Fallback Flow into the
// gateway's LLM reverse proxy. When the proxy observes an upstream
// credential fault (RATE_LIMITED / AUTH_EXPIRED / PROVIDER_UNAVAILABLE)
// the credfallback.Controller records the faulted pool's cooldown and
// the session's rotation budget; the rotator mints a replacement lease
// from the chain's next pool and pushes it to the session's pod via the
// §4.7 RotateCredentials RPC, mirroring the proactive-renewal push path.
//
// spec: §4.9.

// proxyFallbackRotator is the session core's side of GWSEAM-RotateCredential,
// which the metering proxy's credential fallback calls. It mints a §4.9
// replacement lease from the fallback chain's next pool and pushes it to the
// session's pod. It reuses the
// credential-assignment service and warm-pod registry the proactive
// renewal loop uses. A nil assigner or registry degrades to a no-op
// push, consistent with the renewal wiring's nil-receiver behavior.
type proxyFallbackRotator struct {
	assign   credassign.Assigner
	registry *podsession.Registry
}

// RotateCredential is GWSEAM-RotateCredential: it issues a replacement lease
// for the faulted lease's provider from the next pool and pushes it to the
// session's pod (Fallback Flow steps 5-7). The RPC is bounded by
// credRotateRPCTimeout under ctx.
// spec: §4.9, GWSEAM-RotateCredential
func (r proxyFallbackRotator) RotateCredential(ctx context.Context, rot seam.CredentialRotation) {
	r.rotate(ctx, leaseRotation{
		tenantID:   rot.TenantID,
		sessionID:  rot.SessionID,
		provider:   rot.Provider,
		generation: rot.CoordinationGeneration,
		nextPool:   rot.NextPool,
		trigger:    credential.RotationTrigger(rot.Trigger),
	})
}

// rotateLease rotates a lease's session off the lease for the emergency
// revocation's direct-mode rotate, which holds the lease itself. The rotate
// carries no caller context, so the RPC is bounded by credRotateRPCTimeout
// alone.
func (r proxyFallbackRotator) rotateLease(faulted credential.Lease, nextPool string, trigger credential.RotationTrigger) {
	r.rotate(context.Background(), leaseRotation{
		tenantID:   faulted.TenantID,
		sessionID:  faulted.SessionID,
		provider:   string(faulted.Provider),
		generation: faulted.CoordinationGeneration,
		nextPool:   nextPool,
		trigger:    trigger,
	})
}

// leaseRotation names the session, tenant, provider, and coordination
// generation a replacement lease is minted and attributed under, the pool it
// comes from, and the rotation trigger the pod's in-flight gate reads.
type leaseRotation struct {
	tenantID   string
	sessionID  string
	provider   string
	generation int64
	nextPool   string
	trigger    credential.RotationTrigger
}

// rotate mints the replacement lease and pushes it to the session's pod.
func (r proxyFallbackRotator) rotate(ctx context.Context, lr leaseRotation) {
	if r.assign == nil {
		return
	}
	// The SPIFFE-binding identity is re-derived from the lease record at
	// proxy-request time, so the fault mint does not need it here (the
	// renewal path mints the same way).
	// spec: §7.3 (Credential leases on the replacement pod) — the
	// replacement records the faulted lease's coordination generation, so
	// the session's pod release scopes it as it scoped the faulted lease.
	wire, err := r.assign.AssignProto(lr.nextPool, lr.sessionID, "", lr.tenantID, lr.generation)
	if err != nil {
		log.Printf("lenny-gateway: §4.9 fallback: mint replacement for session %s from pool %s: %v",
			lr.sessionID, lr.nextPool, err)
		return
	}
	if r.registry == nil {
		return
	}
	bind, ok := r.registry.Get(lr.sessionID)
	if !ok || bind.Adapter == nil {
		// No pod binding on this replica: the replacement lease is
		// recorded, but there is no local pod to push the rotation to.
		return
	}
	// The §4.7 RotateCredentials leases map is keyed by the
	// runtime-facing provider; the adapter rewrites only that provider's
	// credential-file entry and retains the rest.
	wire.Provider = lr.provider
	ctx, cancel := context.WithTimeout(ctx, credRotateRPCTimeout)
	defer cancel()
	// §4.9 / §4.7: the fault trigger that drove this
	// fallback rides the RPC so the adapter applies the 300s in-flight
	// gate ceiling rather than the unbounded proactive_renewal wait — the
	// faulted credential is no longer trustworthy. An empty trigger
	// (which AllRotationTriggers excludes) is treated as a fault by the
	// adapter for fail-closed safety. F-13.3.10.
	if err := bind.Adapter.RotateCredentials(ctx, lr.sessionID,
		map[string]*adapterv1.CredentialLease{lr.provider: wire}, lr.trigger); err != nil {
		log.Printf("lenny-gateway: §4.9 fallback: RotateCredentials push to session %s pod failed: %v",
			lr.sessionID, err)
	}
}

// proxyFallbackAudit emits the §4.9.2 credential.fallback_exhausted
// audit event when a session's fallback chain is exhausted.
type proxyFallbackAudit struct {
	sink admin.AuditSink
}

// FallbackExhausted records the §4.9.2 audit event with the spec-named
// fields. spec: §4.9.2.
func (a proxyFallbackAudit) FallbackExhausted(ev llmproxy.FallbackExhaustedEvent) {
	if a.sink == nil {
		return
	}
	a.sink.EmitAdminEvent(context.Background(), admin.AuditEvent{
		Type:           string(credential.AuditCredentialFallbackExhausted),
		ActorTenantID:  ev.TenantID,
		TargetResource: ev.SessionID,
		Detail: map[string]any{
			"session_id":               ev.SessionID,
			"rotation_count":           ev.RotationCount,
			"last_failure_reason":      ev.LastFailureReason,
			"fallback_chain_attempted": ev.ChainAttempted,
		},
		At: clockinject.Now().UTC(),
	})
}
