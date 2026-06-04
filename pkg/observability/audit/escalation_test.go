// SPDX-License-Identifier: MIT

package audit

import "testing"

// spec: §16.7 lines 661, 670, 674, 682, 687 — the audit event types that
// are additionally routed onto the §25.5 operational event stream. Every
// event whose §16.7 entry carries the "also routed to the operational
// event stream" clause must report true; every other audit event must
// report false so the gateway does not double-emit ordinary audit rows.
func TestEscalatesToOperationalStream_spec_16_7(t *testing.T) {
	escalating := []EventType{
		EventDelegationSelfRecursionAllowed,
		EventElicitationContentTamperDetected,
		EventDeploymentFeatureFlagDowngradeAcknowledged,
		EventAuditOcsfRetranslateRequested,
		EventAuditPartitionDropForced,
		EventEventBusRepublishRequested,
	}
	for _, et := range escalating {
		if !EscalatesToOperationalStream(et) {
			t.Errorf("EscalatesToOperationalStream(%q)=false, want true", et)
		}
	}

	nonEscalating := []EventType{
		EventTokenExchanged,
		EventTokenRevoked,
		EventSecurityAuditWriteRejected,
		EventDelegationSpawned,
		EventType("admin.tenant.created"),
		EventType(""),
	}
	for _, et := range nonEscalating {
		if EscalatesToOperationalStream(et) {
			t.Errorf("EscalatesToOperationalStream(%q)=true, want false", et)
		}
	}
}

// spec: §16.7 line 661 — every escalating event type is also a known
// §16.7 catalogue entry, so an audit-sink validator never discards an
// event the ops-stream escalation path emits.
func TestEscalationSetIsKnownCatalog_spec_16_7(t *testing.T) {
	for _, et := range []EventType{
		EventDelegationSelfRecursionAllowed,
		EventElicitationContentTamperDetected,
		EventDeploymentFeatureFlagDowngradeAcknowledged,
		EventAuditOcsfRetranslateRequested,
		EventAuditPartitionDropForced,
		EventEventBusRepublishRequested,
	} {
		if !IsKnownEventType(et) {
			t.Errorf("escalating event %q is not a known catalogue type", et)
		}
	}
}
