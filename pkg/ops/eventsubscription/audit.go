// SPDX-License-Identifier: MIT

package eventsubscription

// §25.5 lines 2731, 2804-2806 subscription audit event types. The
// opsserver Service emits these through the AuditSink; the deps wiring
// maps them onto the §16.7 hash-chained audit store (logged until the
// lenny-ops audit-store client lands, matching the drift/backup/
// escalation posture). F-25.5.20.
const (
	EventSubscriptionCreated       = "ops_event.subscription_created"
	EventSubscriptionUpdated       = "ops_event.subscription_updated"
	EventSubscriptionDeleted       = "ops_event.subscription_deleted"
	EventSubscriptionSecretRotated = "ops_event.subscription_secret_rotated"
)

// AuditEvent is one §25.5 subscription audit event. Type is one of the
// EventXxx constants; Actor is the OIDC sub of the caller; Details
// carries the per-event payload (the secret fingerprint, never the
// secret). spec: §25.5 lines 2731-2733.
type AuditEvent struct {
	Type    string
	Actor   string
	Details map[string]any
}

// AuditSink receives §25.5 subscription audit events. spec: §25.5 lines
// 2804-2806.
type AuditSink interface {
	Emit(ev AuditEvent)
}

// noopAuditSink is the default AuditSink: it discards events so a Service
// constructed without an audit wiring still runs.
type noopAuditSink struct{}

func (noopAuditSink) Emit(AuditEvent) {}
