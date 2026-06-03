// SPDX-License-Identifier: MIT

package opsserver

import (
	"context"
	"net/http"
)

// AuditRecorder records a §25.4 lenny-ops audit event. The me and
// Operations Inventory endpoints emit identity.discovered and
// operations.inventory_queried through it. cmd/lenny-ops wires it to the
// structured logger today; the durable audit-store sink the §25.9 / §11.7
// audit trail requires lands with F-25.4.22. A nil recorder on the Server
// drops the event.
//
// spec: §25.4 line 1641 (identity.discovered), line 1779
// (operations.inventory_queried).
type AuditRecorder interface {
	RecordOpsAudit(ctx context.Context, event string, fields map[string]any)
}

// recordOpsAudit emits a §25.4 audit event enriched with the caller
// identity and the §25.17 operation-correlation context. It is a no-op
// when no AuditRecorder is wired.
func (s *Server) recordOpsAudit(r *http.Request, event string, fields map[string]any) {
	if s.audit == nil {
		return
	}
	if fields == nil {
		fields = map[string]any{}
	}
	fields["actorId"] = callerIdentity(r)
	if opID := callerOperationID(r); opID != "" {
		fields["operationId"] = opID
	}
	if agent := callerAgentName(r); agent != "" {
		fields["agentName"] = agent
	}
	s.audit.RecordOpsAudit(r.Context(), event, fields)
}

// LogAuditRecorder is a minimal AuditRecorder that emits each event to a
// caller-supplied sink. cmd/lenny-ops uses it to log §25.4 audit events
// until the durable audit-store client lands (F-25.4.22), matching the
// escalation and backup audit posture.
type LogAuditRecorder struct {
	Sink func(event string, fields map[string]any)
}

// RecordOpsAudit forwards the event to the sink.
func (l LogAuditRecorder) RecordOpsAudit(_ context.Context, event string, fields map[string]any) {
	if l.Sink != nil {
		l.Sink(event, fields)
	}
}
