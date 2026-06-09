// SPDX-License-Identifier: MIT

// Package impersonation implements the §13.3 narrow platform-admin
// impersonation code path: the distinct flow by which a platform-admin
// acts as a tenant user. It is deliberately NOT routed through
// /v1/oauth/token — that surface rejects cross-tenant exchanges (§13.3
// line 585) — and is the sole producer of the §16.7
// admin.impersonation_started / admin.impersonation_ended audit events.
//
// An impersonation session is established by writing the
// admin.impersonation_started audit row first and minting the
// target-user bearer only after the audit commits, because the audit
// record of a cross-tenant admin action MUST be durable before any
// externally observable side effect (§11.7 lines 430-433, §16.7 line
// 680). Both events are written under the platform tenant carrying the
// target_tenant_id, so the §11.7 CMP-058 platform-tenant audit residency
// gate routes them to the target tenant's regional platform-Postgres; an
// unresolvable target region fails the issuance closed with
// PLATFORM_AUDIT_REGION_UNRESOLVABLE and establishes no session.
//
// spec: §13.3 line 585; §16.7 line 680; §11.7 lines 430-433; §12.8 line 960.
package impersonation

import (
	"errors"
	"time"
)

// EndReason records why an impersonation session terminated, stamped on
// the admin.impersonation_ended event.
type EndReason string

const (
	// EndReasonExplicit is an operator-initiated end via
	// DELETE /v1/admin/impersonation/{id}.
	EndReasonExplicit EndReason = "explicit"
	// EndReasonExpired is the sweep terminating a session whose minted
	// bearer reached its impersonation_duration_seconds expiry.
	EndReasonExpired EndReason = "expired"
)

// Ticket is one impersonation session. The minted bearer is short-lived
// (bounded by Duration); the Ticket is the durable record the §16.7
// ended event and the GET /v1/admin/impersonation listing read.
type Ticket struct {
	// ID is the gateway-assigned impersonation session id (the jti of the
	// minted bearer). Distinct from TicketRef, which is the operator's
	// external justification reference.
	ID string
	// AdminSub is the impersonating platform-admin's OIDC sub.
	AdminSub string
	// AdminTenantID is the platform tenant the admin acts from.
	AdminTenantID string
	// TargetTenantID is the impersonated user's tenant.
	TargetTenantID string
	// TargetUserID is the impersonated user's sub.
	TargetUserID string
	// Reason is the operator-supplied impersonation_reason free text.
	Reason string
	// TicketRef is the external justification reference (e.g. a support
	// ticket id), recorded as the §16.7 ticket_id payload field.
	TicketRef string
	// Duration is the requested impersonation_duration_seconds.
	Duration time.Duration
	// IssuedAt / ExpiresAt bound the minted bearer's validity.
	IssuedAt  time.Time
	ExpiresAt time.Time
	// EndedAt / EndedBy / EndReason are zero until the session terminates.
	EndedAt   time.Time
	EndedBy   string
	EndReason EndReason
}

// Active reports whether the session has not yet ended.
func (t Ticket) Active() bool { return t.EndedAt.IsZero() }

// Errors the package returns.
var (
	// ErrNotFound is returned when no impersonation session matches the id.
	ErrNotFound = errors.New("impersonation: session not found")
	// ErrAlreadyEnded is returned when ending a session that already ended.
	ErrAlreadyEnded = errors.New("impersonation: session already ended")
	// ErrInvalidDuration is returned when the requested duration is
	// non-positive or exceeds the configured maximum.
	ErrInvalidDuration = errors.New("impersonation: duration out of range")
	// ErrMissingField is returned when a required issue field is empty.
	ErrMissingField = errors.New("impersonation: required field missing")
)
