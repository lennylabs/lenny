// SPDX-License-Identifier: MIT

// Package billingfanout constructs §11.2.1 billing events for the
// event types whose primary producer writes to the §11.7 audit chain or
// a typed component hook, and tees them into the per-tenant billing
// stream. The §11.2.1 billing stream is the per-tenant cost-attribution
// record; the spec lists these event types (delegation, interceptor,
// export-scan, credential, pool, derive, token-usage) in the §11.2.1
// closed set specifically so cost-attribution and compliance consumers
// observe them in one ordered, sequence-numbered stream rather than only
// in the audit chain.
//
// The constructors are pure: each maps typed producer-site data (or, for
// the delegation events that arrive as an audit detail map, the
// well-known detail keys) onto a billingstore.Event with the §11.2.1
// event-type-specific Conditional fields populated. Emitter wraps the
// ledger with a nil-safe, best-effort Emit so a teed write never fails
// the primary operation.
//
// spec: §11.2.1 — Billing Event Stream. F-11.2.1.
package billingfanout

import (
	"context"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
)

// emitTimeout bounds a teed billing append so a stuck ledger cannot pin
// the producer's request thread. The teed events ride alongside an audit
// write that is itself best-effort; the authoritative synchronous
// billing path (§11.2.1 immutability) is the session-lifecycle and
// token-checkpoint emission, not these secondary config-change rows.
const emitTimeout = 250 * time.Millisecond

// Emitter tees billing events into the ledger. A nil Emitter, or one
// constructed with a nil store, drops every Emit so callers wire it
// unconditionally and the no-store minimal gateway is a no-op.
type Emitter struct {
	store billingstore.Store
}

// NewEmitter returns an Emitter over store. A nil store yields a
// no-op Emitter.
func NewEmitter(store billingstore.Store) *Emitter {
	if store == nil {
		return nil
	}
	return &Emitter{store: store}
}

// Emit appends ev to the billing ledger best-effort: a validation or
// store error is swallowed so the teed write never fails the producer's
// primary operation (the audit write or the admin mutation). A nil
// Emitter or an event with no tenant id is dropped.
func (e *Emitter) Emit(ctx context.Context, ev billingstore.Event) {
	if e == nil || e.store == nil || ev.TenantID == "" {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, emitTimeout)
	defer cancel()
	_, _ = e.store.Append(cctx, ev)
}

// DelegationSpawned builds the §11.2.1 delegation.spawned event from the
// delegation-audit detail map. The billing event pertains to the spawned
// child session; the user is the parent session owner (the delegating
// caller). delegation.spawned carries no event-type-specific Conditional
// fields beyond the common envelope. Returns ok=false when the detail
// map carries no child session id.
func DelegationSpawned(tenantID, userID string, detail map[string]any) (billingstore.Event, bool) {
	child := asString(detail["child_session_id"])
	if child == "" {
		return billingstore.Event{}, false
	}
	return billingstore.Event{
		TenantID:  tenantID,
		UserID:    userID,
		SessionID: child,
		EventType: billingstore.EventDelegationSpawned,
	}, true
}

// DelegationIsolationViolation builds the §11.2.1
// delegation.isolation_violation event from the delegation-audit detail
// map. The billing event pertains to the parent session whose delegation
// was rejected. It carries the §11.2.1 parent_isolation / target_isolation
// / matched_policy_rule conditional fields.
func DelegationIsolationViolation(tenantID, userID string, detail map[string]any) (billingstore.Event, bool) {
	parent := asString(detail["parentSessionId"])
	return billingstore.Event{
		TenantID:  tenantID,
		UserID:    userID,
		SessionID: parent,
		EventType: billingstore.EventDelegationIsolationViolation,
		Conditional: &billingstore.Conditional{
			ParentIsolation:   asString(detail["parent_isolation"]),
			TargetIsolation:   asString(detail["target_isolation"]),
			MatchedPolicyRule: asString(detail["matched_policy_rule"]),
		},
	}, true
}

// InterceptorFailPolicy builds one of the §11.2.1 interceptor.fail_policy_*
// events. eventType selects weakened / strengthened /
// weakening_cooldown_active. transitionTs and cooldownSeconds apply to the
// weakened and weakening_cooldown_active variants; the strengthened
// variant omits them (tightening is not subject to cooldown).
func InterceptorFailPolicy(eventType billingstore.EventType, tenantID, interceptorRef, oldFailPolicy, newFailPolicy string, affectedCount uint32, affectedNames []string, transitionTs string, cooldownSeconds uint32) billingstore.Event {
	c := &billingstore.Conditional{
		InterceptorRef:      interceptorRef,
		OldFailPolicy:       oldFailPolicy,
		NewFailPolicy:       newFailPolicy,
		AffectedPolicyCount: affectedCount,
		AffectedPolicyNames: affectedNames,
	}
	if eventType != billingstore.EventInterceptorFailPolicyStrengthened {
		c.TransitionTS = transitionTs
		c.CooldownSeconds = cooldownSeconds
	}
	// The weakening_cooldown_active event carries no old/new policy on the
	// §11.2.1 schema — it reports the affected-policy set and the
	// transition window only.
	if eventType == billingstore.EventInterceptorWeakeningCooldownActive {
		c.OldFailPolicy = ""
		c.NewFailPolicy = ""
	}
	return billingstore.Event{
		TenantID:    tenantID,
		EventType:   eventType,
		Conditional: c,
	}
}

// DelegationPolicyExportScan builds the §11.2.1
// delegation_policy.export_scan_weakened / _strengthened event. The
// _strengthened variant omits cooldown_seconds (tightening takes effect
// immediately).
func DelegationPolicyExportScan(eventType billingstore.EventType, tenantID, policyName string, oldScan, newScan bool, transitionTs string, cooldownSeconds uint32) billingstore.Event {
	oc, nc := oldScan, newScan
	c := &billingstore.Conditional{
		PolicyName:           policyName,
		OldScanExportedFiles: &oc,
		NewScanExportedFiles: &nc,
		TransitionTS:         transitionTs,
	}
	if eventType != billingstore.EventDelegationPolicyExportScanStrengthened {
		c.CooldownSeconds = cooldownSeconds
	}
	return billingstore.Event{
		TenantID:    tenantID,
		EventType:   eventType,
		Conditional: c,
	}
}

// ExportFileScan builds the §11.2.1 delegation.export_file_scan_rejected /
// export_scan_failed_open event from the typed export-scan observation.
func ExportFileScan(eventType billingstore.EventType, tenantID, policyName, interceptorRef, filePath string, fileSize uint64, reason string) billingstore.Event {
	return billingstore.Event{
		TenantID:  tenantID,
		EventType: eventType,
		Conditional: &billingstore.Conditional{
			PolicyName:     policyName,
			InterceptorRef: interceptorRef,
			FilePath:       filePath,
			FileSize:       fileSize,
			Reason:         reason,
		},
	}
}

// CredentialLeased builds the §11.2.1 credential.leased event. The
// billing event pertains to the session the credential was leased to and
// carries the credential pool / id / delivery mode.
func CredentialLeased(tenantID, sessionID, credentialPoolID, credentialID, deliveryMode string) billingstore.Event {
	return billingstore.Event{
		TenantID:  tenantID,
		SessionID: sessionID,
		EventType: billingstore.EventCredentialLeased,
		Conditional: &billingstore.Conditional{
			CredentialPoolID: credentialPoolID,
			CredentialID:     credentialID,
			DeliveryMode:     deliveryMode,
		},
	}
}

// CredentialRevoked builds the §11.2.1 credential.revoked event.
func CredentialRevoked(tenantID, credentialPoolID, credentialID, revokedBy, revocationReason string, leasesTerminated uint32) billingstore.Event {
	return billingstore.Event{
		TenantID:  tenantID,
		EventType: billingstore.EventCredentialRevoked,
		Conditional: &billingstore.Conditional{
			CredentialPoolID: credentialPoolID,
			CredentialID:     credentialID,
			RevokedBy:        revokedBy,
			RevocationReason: revocationReason,
			LeasesTerminated: leasesTerminated,
		},
	}
}

// DeriveIsolationDowngrade builds the §11.2.1 derive.isolation_downgrade
// event. The billing event pertains to the source session whose
// derive/replay triggered the platform-admin-gated downgrade.
func DeriveIsolationDowngrade(tenantID, sourceSessionID, sourceIsolation, targetPool, targetIsolation, authorizingUserSub, ticketID string) billingstore.Event {
	return billingstore.Event{
		TenantID:  tenantID,
		SessionID: sourceSessionID,
		EventType: billingstore.EventDeriveIsolationDowngrade,
		Conditional: &billingstore.Conditional{
			SourceSessionID:        sourceSessionID,
			SourceIsolationProfile: sourceIsolation,
			TargetPool:             targetPool,
			TargetIsolationProfile: targetIsolation,
			AuthorizingUserSub:     authorizingUserSub,
			TicketID:               ticketID,
		},
	}
}

// PoolIsolationWarning builds the §11.2.1 pool.isolation_warning event
// for one §8.3 line 350 proactive monotonicity conflict.
func PoolIsolationWarning(tenantID, poolName, poolIsolation, matchedPolicyRule, conflictingPoolName, conflictingIsolation string) billingstore.Event {
	return billingstore.Event{
		TenantID:  tenantID,
		EventType: billingstore.EventPoolIsolationWarning,
		Conditional: &billingstore.Conditional{
			PoolName:             poolName,
			PoolIsolation:        poolIsolation,
			MatchedPolicyRule:    matchedPolicyRule,
			ConflictingPoolName:  conflictingPoolName,
			ConflictingIsolation: conflictingIsolation,
		},
	}
}

// TokenUsageCheckpoint builds the §11.2.1 token_usage.checkpoint event:
// a periodic per-session token-usage snapshot carrying the token counts
// consumed in the checkpoint window. It has no event-type-specific
// Conditional fields — the tokens_input / tokens_output live on the
// common envelope.
func TokenUsageCheckpoint(tenantID, sessionID, userID string, tokensInput, tokensOutput uint64) billingstore.Event {
	return billingstore.Event{
		TenantID:     tenantID,
		SessionID:    sessionID,
		UserID:       userID,
		EventType:    billingstore.EventTokenUsageCheckpoint,
		TokensInput:  tokensInput,
		TokensOutput: tokensOutput,
	}
}

// asString returns v as a string, or "" when v is absent or not a
// string. The delegation detail maps carry string values for the keys
// this package reads.
func asString(v any) string {
	s, _ := v.(string)
	return s
}
