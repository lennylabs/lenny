// SPDX-License-Identifier: MIT

package credential

// AuditEventType is a §4.9.2 credential audit event type. The value is
// the audit_log row's event_type string — the same identifier the
// §4.9.2 catalog lists in backticks. The §4.9.2 events are a credential
// domain concern and are declared here rather than in the §16.7 catalog
// (pkg/observability/audit), which enumerates only the §25 events added
// to the §11.7 path. The pkg/audit/ocsf mapping table pins each of these
// to its OCSF class.
type AuditEventType string

// The §4.9.2 credential audit event catalog. Each constant transcribes
// one row of the spec/04_system-components.md §4.9.2 table.
const (
	// AuditCredentialRegistered: a user registered a new credential via
	// POST /v1/credentials. Fields: tenant_id, user_id, provider,
	// credential_ref.
	AuditCredentialRegistered AuditEventType = "credential.registered"

	// AuditCredentialDeleted: a user deleted a credential via
	// DELETE /v1/credentials/{credential_ref}. Fields: tenant_id,
	// user_id, provider, credential_ref.
	AuditCredentialDeleted AuditEventType = "credential.deleted"

	// AuditCredentialRotated: a user rotated credential material via
	// PUT /v1/credentials/{credential_ref}. Fields: tenant_id, user_id,
	// provider, credential_ref, active_leases_rotated.
	AuditCredentialRotated AuditEventType = "credential.rotated"

	// AuditCredentialUserRevoked: a user explicitly revoked their
	// credential via POST /v1/credentials/{credential_ref}/revoke.
	// Fields: tenant_id, user_id, provider, credential_ref, reason,
	// active_leases_terminated.
	AuditCredentialUserRevoked AuditEventType = "credential.user_revoked"

	// AuditCredentialLeased: a credential is assigned to a session at
	// session start. Fields: tenant_id, session_id, source,
	// delivery_mode, rotation_mode, and the source-specific identifiers.
	AuditCredentialLeased AuditEventType = "credential.leased"

	// AuditCredentialRevoked: a pool credential is emergency-revoked by
	// an operator. Fields: tenant_id, pool_id, credential_id,
	// revoked_by, reason, active_leases_terminated.
	AuditCredentialRevoked AuditEventType = "credential.revoked"

	// AuditCredentialReEnabled: a previously revoked pool credential is
	// re-enabled by an operator. Fields: tenant_id, pool_id,
	// credential_id, reason, re_enabled_by.
	AuditCredentialReEnabled AuditEventType = "credential.re_enabled"

	// AuditCredentialRenewed: a credential lease is proactively renewed
	// before expiry. Fields: tenant_id, session_id, pool_id,
	// credential_id, rotation_trigger.
	AuditCredentialRenewed AuditEventType = "credential.renewed"

	// AuditCredentialRotationCeilingHit: the §4.7 300-second in-flight
	// gate ceiling fired for a non-proactive rotation. Tier-1
	// compromise indicator. Fields: tenant_id, session_id, lease_id,
	// pool_id, credential_id, rotation_trigger, outstanding_inflight_count,
	// elapsed_seconds.
	AuditCredentialRotationCeilingHit AuditEventType = "credential.rotation_ceiling_hit"

	// AuditCredentialFallbackExhausted: all fallback providers are
	// exhausted; the session terminates with CREDENTIAL_FALLBACK_EXHAUSTED.
	// Fields: tenant_id, session_id, rotation_count, last_failure_reason,
	// fallback_chain_attempted.
	AuditCredentialFallbackExhausted AuditEventType = "credential.fallback_exhausted"

	// AuditCredentialLeaseSpiffeMismatch: a proxy request was rejected
	// because the pod's SPIFFE URI does not match the lease record.
	// Fields: tenant_id, session_id, lease_id, expected_spiffe_uri,
	// actual_spiffe_uri.
	AuditCredentialLeaseSpiffeMismatch AuditEventType = "credential.lease_spiffe_mismatch"

	// AuditCredentialProxyModeSpiffeBindingDisabled: a single-tenant or
	// development deployment registered or updated a CredentialPool with
	// deliveryMode: proxy + spiffeBinding: disabled. Fields: tenant_id,
	// pool_id, tenancy_mode, authorizing_user_sub.
	AuditCredentialProxyModeSpiffeBindingDisabled AuditEventType = "credential.proxy_mode_spiffe_binding_disabled"
)

// allCredentialAuditEventTypes is the closed §4.9.2 catalog in spec
// table order.
var allCredentialAuditEventTypes = []AuditEventType{
	AuditCredentialRegistered,
	AuditCredentialDeleted,
	AuditCredentialRotated,
	AuditCredentialUserRevoked,
	AuditCredentialLeased,
	AuditCredentialRevoked,
	AuditCredentialReEnabled,
	AuditCredentialRenewed,
	AuditCredentialRotationCeilingHit,
	AuditCredentialFallbackExhausted,
	AuditCredentialLeaseSpiffeMismatch,
	AuditCredentialProxyModeSpiffeBindingDisabled,
}

// AllCredentialAuditEventTypes returns the closed §4.9.2 catalog. The
// slice is fresh on every call so callers may sort or filter it freely.
func AllCredentialAuditEventTypes() []AuditEventType {
	return append([]AuditEventType(nil), allCredentialAuditEventTypes...)
}

// IsCredentialAuditEvent reports whether t is one of the §4.9.2
// credential audit event types.
func IsCredentialAuditEvent(t AuditEventType) bool {
	for _, v := range allCredentialAuditEventTypes {
		if t == v {
			return true
		}
	}
	return false
}

// String returns the audit event type identifier.
func (t AuditEventType) String() string { return string(t) }
