// SPDX-License-Identifier: MIT

package main

import (
	"net/http"

	"github.com/lennylabs/lenny/pkg/auth/authmw"
)

// idempotencyPrincipal returns the record scope of a request that carries
// an idempotency key: the tenant, the subject, and the bound session of the
// principal the auth middleware attached to the request context. The REST
// idempotency middleware and the MCP idempotency hook both resolve the
// scope through it, so a key sent over either path is filed under one
// scope. The subject is the principal's Subject, which is the owner the
// session surfaces give a session the request creates. A request with no
// principal resolves to an empty scope, which both callers refuse.
//
// spec: §11.5 (Idempotency)
func idempotencyPrincipal(r *http.Request) (tenantID, subject, sessionID string) {
	p, ok := authmw.FromContext(r.Context())
	if !ok {
		return "", "", ""
	}
	return p.TenantID, p.Subject, p.SessionID
}
