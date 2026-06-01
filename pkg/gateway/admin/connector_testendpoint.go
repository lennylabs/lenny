// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/gateway/connectorcredstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectorinvoke"
	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/ratelimit"
)

// connectorTestRateLimit is the §15.1 line 1180 cap: the connector live
// test runs at most this many times per connector per minute, so the
// endpoint cannot be abused as a network scanner.
const connectorTestRateLimit = 10

// ConnectorTester runs a §15.1 connector live-connectivity check. The
// production implementation is *connectorinvoke.Tester; tests inject a
// fake.
type ConnectorTester interface {
	Test(ctx context.Context, conn connectorstore.Connector, bearer string) connectorinvoke.TestReport
}

// WithConnectorTest wires the §15.1 `POST /v1/admin/connectors/{name}/test`
// endpoint: the live-connectivity tester, the connector-credential store
// the test reads the stored credential from, and the per-connector
// fixed-window rate limiter. A nil tester leaves the route unregistered.
func (r *Router) WithConnectorTest(tester ConnectorTester, creds connectorcredstore.Store, limiter ratelimit.Counter) *Router {
	r.connectorTester = tester
	r.connectorCreds = creds
	r.connectorTestLimiter = limiter
	return r
}

// handleTestConnector implements §15.1 line 791:
// `POST /v1/admin/connectors/{name}/test`. It performs a live connectivity
// check (DNS, TLS, MCP initialize, auth validation) against an
// already-registered connector using the connector's stored credential.
// It does not accept inline credential overrides (§15.1 line 1180).
//
// spec: §15.1 line 791, lines 1163-1180.
func (r *Router) handleTestConnector(w http.ResponseWriter, req *http.Request) {
	id := req.PathValue("name")
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	tenantID := listTenantScope(principal, req)
	conn, err := r.connectors.Get(req.Context(), tenantID, id)
	if err != nil {
		if errors.Is(err, connectorstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "connector not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if !conn.IsActive() {
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "connector not found", nil)
		return
	}

	// §15.1 line 1180 — 10 requests per connector per minute. The key is
	// scoped by the connector's owning tenant so one tenant's tests do
	// not consume another tenant's budget for a same-named connector.
	if r.connectorTestLimiter != nil {
		key := "connector-test:" + conn.TenantID + ":" + conn.ID
		count, lerr := r.connectorTestLimiter.Incr(req.Context(), key, r.clock())
		if lerr == nil && count > connectorTestRateLimit {
			w.Header().Set("Retry-After", "60")
			writeError(w, http.StatusTooManyRequests, "RATE_LIMITED",
				"connector test is limited to 10 requests per connector per minute", nil)
			return
		}
	}

	bearer := r.connectorTestBearer(req.Context(), conn, principal.Subject)
	report := r.connectorTester.Test(req.Context(), conn, bearer)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(report)
}

// connectorTestBearer returns the calling principal's stored credential
// for the connector, or empty when none exists. The test uses the
// connector's stored credentials and never an inline override (§15.1
// line 1180); the no-environment scope is used because the admin test is
// not run inside an environment-scoped session.
func (r *Router) connectorTestBearer(ctx context.Context, conn connectorstore.Connector, userID string) string {
	if r.connectorCreds == nil || conn.Auth == nil || userID == "" {
		return ""
	}
	cred, err := r.connectorCreds.Get(ctx, conn.TenantID, conn.ID, userID, "")
	if err != nil || !cred.HasToken() {
		return ""
	}
	return cred.AccessToken
}
