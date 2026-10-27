// SPDX-License-Identifier: MIT

// Package impersonation implements the §13.3 "Platform-admin
// impersonation" flow: the distinct code path by which a platform-admin
// acts as a tenant user. It is not routed through /v1/oauth/token, which
// rejects cross-tenant exchanges (§13.3), and it is the sole producer of
// the §16.7 admin.impersonation_started and admin.impersonation_ended
// audit events.
//
// An impersonation session is the issued_tokens row of the bearer it
// mints, and the gateway keeps no other session state, so every gateway
// replica lists and ends every session and a restart loses none. Issuance
// checks the caller's own bearer, writes admin.impersonation_started, signs
// the target-user bearer, and records it under the user-row fence. Ending
// a session revokes the row in one conditional update, pushes the jti into
// the revocation cache, and only then writes admin.impersonation_ended.
// Both events are written under the platform tenant carrying
// target_tenant_id, so the §11.7 CMP-058 platform-tenant audit residency
// gate routes them to the target tenant's regional platform-Postgres.
//
// spec: §13.3; §16.7; §11.7; §12.3; §15.1.
package impersonation

import (
	"errors"
	"time"
)

// Ticket is one impersonation session as issuance establishes it. The
// reason and the ticket reference travel only on the started audit event;
// the issued_tokens row holds the rest.
type Ticket struct {
	// ID is the gateway-assigned impersonation session id, which is the
	// jti of the minted bearer. Distinct from TicketRef, which is the
	// operator's external justification reference.
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
	// TicketRef is the external justification reference (for example a
	// support ticket id), recorded as the §16.7 ticket_id payload field.
	TicketRef string
	// Duration is the requested impersonation_duration_seconds.
	Duration time.Duration
	// IssuedAt and ExpiresAt bound the minted bearer's validity.
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// Session is one active impersonation session as the
// GET /v1/admin/impersonation listing reports it: the fields the
// issued_tokens row holds.
//
// spec: §15.1 (GET /v1/admin/impersonation); §13.3 (Platform-admin
// impersonation, rule 2).
type Session struct {
	ID             string
	AdminSub       string
	TargetTenantID string
	TargetUserID   string
	IssuedAt       time.Time
	ExpiresAt      time.Time
}

// EndResult is the outcome of a winning DELETE /v1/admin/impersonation/{id}.
//
// spec: §15.1 (DELETE /v1/admin/impersonation/{id}).
type EndResult struct {
	SessionID string
	EndedAt   time.Time
}

// Errors the package returns.
var (
	// ErrNotFound is returned when no impersonation session has the id.
	ErrNotFound = errors.New("impersonation: session not found")
	// ErrAlreadyEnded is returned when the session's bearer is already
	// revoked or expired. It wraps the store's *NotActiveError, so
	// errors.As recovers the current state.
	ErrAlreadyEnded = errors.New("impersonation: session not active")
	// ErrInvalidDuration is returned when the requested duration is below
	// the §15.1 minimum or above the configured maximum.
	ErrInvalidDuration = errors.New("impersonation: duration out of range")
	// ErrMissingField is returned when a required issue field is empty.
	ErrMissingField = errors.New("impersonation: required field missing")
	// ErrIssuanceRefused is returned when §13.3 "Platform-admin
	// impersonation" rule 1 refuses the call: the caller's bearer is an
	// impersonation bearer or is revoked, or the caller's or the target's
	// user row records a hard disable or a full revoke. It wraps the
	// refusal the store reported.
	ErrIssuanceRefused = errors.New("impersonation: issuance refused")
)
