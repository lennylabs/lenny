// SPDX-License-Identifier: MIT

package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lennylabs/lenny/pkg/auth/authmw"
)

// TestIdempotencyPrincipalReadsTheAuthenticatedPrincipal checks that the
// gateway's record-scope resolver returns the tenant, subject, and bound
// session of the principal on the request context, and ignores the dev
// identity headers, so a request without a principal resolves to an empty
// scope that the middleware and the MCP hook refuse.
//
// spec: §11.5 (Idempotency)
func TestIdempotencyPrincipalReadsTheAuthenticatedPrincipal(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions", nil)
	req.Header.Set("X-Lenny-Tenant-ID", "globex")
	req.Header.Set("X-Lenny-User-ID", "mallory")
	if tenant, subject, session := idempotencyPrincipal(req); tenant != "" || subject != "" || session != "" {
		t.Fatalf("no principal: scope = (%q, %q, %q), want empty", tenant, subject, session)
	}

	for _, p := range []authmw.Principal{
		{TenantID: "acme", Subject: "alice"},
		{TenantID: "acme", Subject: "alice", SessionID: "s1"},
		{TenantID: "acme", SessionID: "s1"},
	} {
		r := req.WithContext(authmw.WithPrincipal(req.Context(), p))
		tenant, subject, session := idempotencyPrincipal(r)
		if tenant != p.TenantID || subject != p.Subject || session != p.SessionID {
			t.Errorf("principal %+v: scope = (%q, %q, %q)", p, tenant, subject, session)
		}
	}
}
