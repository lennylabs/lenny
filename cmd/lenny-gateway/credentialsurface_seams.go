// SPDX-License-Identifier: MIT

package main

import (
	"context"

	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// credentialAuditor adapts the gateway audit sink to the
// credentialserver.AuditSink interface, drawing the §11.7 actor fields
// from the request principal and the §4.9.2 credential_ref from the
// event detail so the audit query can target the affected credential.
type credentialAuditor struct {
	sink admin.AuditSink
}

func (a credentialAuditor) EmitCredentialEvent(ctx context.Context, eventType string, detail map[string]any) {
	if a.sink == nil {
		return
	}
	ev := admin.AuditEvent{Type: eventType, Detail: detail, At: clockinject.Now().UTC()}
	if p, ok := authmw.FromContext(ctx); ok {
		ev.ActorSubject = p.Subject
		ev.ActorTenantID = p.TenantID
	}
	if ref, ok := detail["credential_ref"].(string); ok {
		ev.TargetResource = ref
	}
	a.sink.EmitAdminEvent(ctx, ev)
}
