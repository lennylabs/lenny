// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"log"

	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/admin"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/llmproxy"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
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
// spec: spec/04_system-components.md lines 1383-1411 (Fallback Flow).

// proxyFallbackRotator mints a §4.9 replacement lease from the fallback
// chain's next pool and pushes it to the session's pod. It reuses the
// credential-assignment service and warm-pod registry the proactive
// renewal loop uses. A nil assigner or registry degrades to a no-op
// push, consistent with the renewal wiring's nil-receiver behavior.
type proxyFallbackRotator struct {
	assign   credassign.Assigner
	registry *podsession.Registry
}

// Rotate issues a replacement lease for the faulted lease's provider
// from nextPool and pushes it to the session's pod (Fallback Flow steps
// 5-7). spec: spec/04_system-components.md lines 1405-1411.
func (r proxyFallbackRotator) Rotate(faulted credential.Lease, nextPool string, trigger credential.RotationTrigger) {
	if r.assign == nil {
		return
	}
	// The SPIFFE-binding identity is re-derived from the lease record at
	// proxy-request time, so the fault mint does not need it here (the
	// renewal path mints the same way).
	wire, err := r.assign.AssignProto(nextPool, faulted.SessionID, "", faulted.TenantID)
	if err != nil {
		log.Printf("lenny-gateway: §4.9 fallback: mint replacement for session %s from pool %s: %v",
			faulted.SessionID, nextPool, err)
		return
	}
	if r.registry == nil {
		return
	}
	bind, ok := r.registry.Get(faulted.SessionID)
	if !ok || bind.Adapter == nil {
		// No pod binding on this replica: the replacement lease is
		// recorded, but there is no local pod to push the rotation to.
		return
	}
	// The §4.7 RotateCredentials leases map is keyed by the
	// runtime-facing provider; the adapter rewrites only that provider's
	// credential-file entry and retains the rest.
	provider := string(faulted.Provider)
	wire.Provider = provider
	ctx, cancel := context.WithTimeout(context.Background(), credRotateRPCTimeout)
	defer cancel()
	// §4.9 line 1413 / §4.7 line 822: the fault trigger that drove this
	// fallback rides the RPC so the adapter applies the 300s in-flight
	// gate ceiling rather than the unbounded proactive_renewal wait — the
	// faulted credential is no longer trustworthy. An empty trigger
	// (which AllRotationTriggers excludes) is treated as a fault by the
	// adapter for fail-closed safety. F-13.3.10.
	if err := bind.Adapter.RotateCredentials(ctx, faulted.SessionID,
		map[string]*adapterv1.CredentialLease{provider: wire}, trigger); err != nil {
		log.Printf("lenny-gateway: §4.9 fallback: RotateCredentials push to session %s pod failed: %v",
			faulted.SessionID, err)
	}
}

// proxyFallbackAudit emits the §4.9.2 credential.fallback_exhausted
// audit event when a session's fallback chain is exhausted.
type proxyFallbackAudit struct {
	sink admin.AuditSink
}

// FallbackExhausted records the §4.9.2 audit event with the spec-named
// fields. spec: spec/04_system-components.md line 1746.
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
