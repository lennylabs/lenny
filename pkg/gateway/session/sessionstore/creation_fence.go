// SPDX-License-Identifier: MIT

package sessionstore

import (
	"errors"
	"fmt"
)

// Codes of the creation fence's refusals. Every refusal is HTTP 403, and a
// request whose session several markers refuse answers the first code in
// this order. spec: §12.8 (Lifecycle fence and drain), §15.1 (Error code
// catalog).
const (
	RefusalUserInvalidated   = "USER_INVALIDATED"
	RefusalErasureInProgress = "ERASURE_IN_PROGRESS"
	RefusalTenantSuspended   = "TENANT_SUSPENDED"
	RefusalTenantNotActive   = "TENANT_NOT_ACTIVE"
)

// CreationMarkers is the lifecycle-marker state the creation fence reads for
// one session insert: the tenant row and the row of the user who owns the
// new session. The Postgres store reads both rows FOR SHARE inside the
// transaction that inserts the session row, so a marker write, which holds
// an exclusive lock on the row it changes, serializes with the insert.
// spec: §12.8 (Lifecycle fence and drain), §11.4 (User Invalidation).
type CreationMarkers struct {
	// TenantFound is false when no tenant row exists. In Postgres the
	// sessions.tenant_id foreign key rejects such an insert before the fence
	// could matter, so a false value is reachable only in the in-memory
	// store.
	TenantFound bool
	// TenantSuspended is the operator suspension marker
	// (POST /v1/admin/tenants/{id}/suspend).
	TenantSuspended bool
	// TenantState is the §12.8 TenantState. The empty value and "active"
	// admit, as tenantstore.Tenant.AcceptsNewWork does.
	TenantState string
	// UserFound is false for a session with no owning user and for an owner
	// with no registry row. Neither carries a user marker.
	UserFound bool
	// UserDisabled is users.disabled, the §11.4 soft_disable marker.
	UserDisabled bool
	// UserDeleted is users.deleted_at IS NOT NULL, the §11.4 hard_disable
	// and full_revoke marker.
	UserDeleted bool
	// ProcessingRestricted is the §12.8 Article 18 restriction an erasure
	// job sets on its user.
	ProcessingRestricted bool
	// ErasureJobID names the erasure job that set ProcessingRestricted.
	ErasureJobID string
	// IsDelegatedChild is Session.DelegationDepth > 0. Only the delegation
	// service writes a non-zero depth. ParentSessionID does not classify a
	// row, because derive, the derive-failure audit row, and replay set it to
	// the source session, and classifying by it would admit a soft-disabled
	// owner's derive or replay.
	IsDelegatedChild bool
}

// CreationRefusal is the typed error the creation fence returns when it
// refuses an insert or a reservation. Every refusal is HTTP 403 with Code
// and Details on the wire. Callers branch on it with errors.As.
// spec: §12.8 (Lifecycle fence and drain).
type CreationRefusal struct {
	Code    string
	Message string
	Details map[string]any
}

// Error implements error.
func (e *CreationRefusal) Error() string {
	return fmt.Sprintf("sessionstore: creation refused: %s: %s", e.Code, e.Message)
}

// Refusal returns the first rule that refuses a session owned by userID in
// tenantID, or nil when the markers admit it. The rules, in order:
//
//  1. The owner is hard-disabled or fully revoked, or is soft-disabled and
//     the row is not a delegated child: USER_INVALIDATED. This is the one
//     marker-specific rule: §11.4 lets a soft-disabled user's in-flight work
//     keep delegating, and hard_disable also blocks delegated children.
//  2. The owner's processing is restricted by an erasure job:
//     ERASURE_IN_PROGRESS with userId and jobId.
//  3. The tenant is suspended: TENANT_SUSPENDED with tenantId.
//  4. The tenant's state is neither empty nor active: TENANT_NOT_ACTIVE with
//     tenantId and state.
//
// A missing tenant row and a missing user row carry no marker. Tenant
// markers and the restriction refuse every row, delegated children and rows
// created terminal included.
//
// spec: §12.8 (Lifecycle fence and drain), §11.4 (User Invalidation).
func (m CreationMarkers) Refusal(tenantID, userID string) *CreationRefusal {
	if m.UserFound && (m.UserDeleted || (m.UserDisabled && !m.IsDelegatedChild)) {
		return &CreationRefusal{
			Code:    RefusalUserInvalidated,
			Message: "the session owner has been invalidated and cannot create sessions",
		}
	}
	if m.UserFound && m.ProcessingRestricted {
		return &CreationRefusal{
			Code:    RefusalErasureInProgress,
			Message: "this user has a pending erasure request; new sessions cannot be created until erasure completes",
			Details: map[string]any{"userId": userID, "jobId": m.ErasureJobID},
		}
	}
	if m.TenantFound && m.TenantSuspended {
		return &CreationRefusal{
			Code:    RefusalTenantSuspended,
			Message: "the tenant is suspended and is not accepting new sessions",
			Details: map[string]any{"tenantId": tenantID},
		}
	}
	if m.TenantFound && m.TenantState != "" && m.TenantState != tenantStateActive {
		return &CreationRefusal{
			Code:    RefusalTenantNotActive,
			Message: "the tenant is being disabled or deleted and is not accepting new sessions",
			Details: map[string]any{"tenantId": tenantID, "state": m.TenantState},
		}
	}
	return nil
}

// tenantStateActive mirrors tenantstore.TenantStateActive. The session store
// does not import the tenant store, so the value is repeated here; a tier-1
// test pins the two predicates to each other.
const tenantStateActive = "active"

// ReservationEndedRefusal is the refusal CreateReserved returns when the
// reservation of a row with an owner no longer exists, because the owner's
// erasure or the tenant's deletion removed it. It carries the owner as
// userId and no jobId. spec: §12.8 (Lifecycle fence and drain).
func ReservationEndedRefusal(userID string) *CreationRefusal {
	return &CreationRefusal{
		Code:    RefusalErasureInProgress,
		Message: "the session ID reservation was ended by the owner's erasure; the session cannot be created",
		Details: map[string]any{"userId": userID},
	}
}

// ErrTranscriptCopierUnwired is returned by CreateWithTranscript on a store
// built without a transcript copier. Nothing is inserted.
var ErrTranscriptCopierUnwired = errors.New("sessionstore: CreateWithTranscript requires a transcript copier")
