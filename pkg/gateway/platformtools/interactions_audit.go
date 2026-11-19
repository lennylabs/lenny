// SPDX-License-Identifier: MIT

package platformtools

import (
	"context"
	"time"
)

const (
	// Interaction-resolution audit event types written by the §15.1
	// approve/deny/respond/dismiss endpoints. The strings match the
	// §16.7 catalog. spec: §7.2; §11.7; §16.7. F-7.2.8.
	auditToolUseApproved      = "tool_use.approved"
	auditToolUseDenied        = "tool_use.denied"
	auditElicitationResponded = "elicitation.responded"
	auditElicitationDismissed = "elicitation.dismissed"
)

// InteractionAuditSink receives the §7.2 / §11.7 / §16.7
// interaction-resolution audit events the gateway writes when a user
// decision (tool-use approve/deny, elicitation respond/dismiss)
// resolves a pending interaction. It is a separate sink from
// LifecycleAuditSink because the event payload (the resolved
// interaction id, the resolution phase, an optional dismissal reason)
// is distinct from a session lifecycle transition. Implementations
// must be non-blocking — the resolution path emits best-effort and
// never waits for delivery.
//
// spec: §7.2 table; §11.7 (the
// audit row contract); §16.7 (the OCSF mapping). F-7.2.8.
type InteractionAuditSink interface {
	EmitInteractionResolution(ctx context.Context, ev InteractionResolutionEvent)
}

// InteractionResolutionEvent is the §11.7 audit payload for one
// resolved interaction. The audit row's tenant scope is the session's
// own TenantID per the §11.7 write-time tenant-validation
// rule. Field names feed the §16.7 OCSF mapping.
//
// spec: §7.2 table; §11.7; §16.7. F-7.2.8.
type InteractionResolutionEvent struct {
	// EventType is one of auditToolUseApproved, auditToolUseDenied,
	// auditElicitationResponded, auditElicitationDismissed.
	EventType string
	TenantID  string
	SessionID string
	// UserID is the resolving user's subject — the authenticated caller
	// per the §15.1 (session_id, user_id, interaction_id) triple.
	UserID string
	// InteractionID is the tool_call_id (for tool-use approve/deny) or
	// elicitation_id (for elicitation respond/dismiss) the resolution
	// targets. Recording it is the post-incident link from the audit
	// row back to the interaction row.
	InteractionID string
	// Phase is the new interaction phase ("approved" / "denied" /
	// "responded" / "dismissed").
	Phase string
	// Reason carries the optional dismissal reason supplied by the
	// /deny body. Empty for the other three resolution paths.
	Reason string
	// At is the resolution wall-clock time.
	At time.Time
}
