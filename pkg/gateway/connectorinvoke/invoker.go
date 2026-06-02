// SPDX-License-Identifier: MIT

package connectorinvoke

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/connectorcredstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
	"github.com/lennylabs/lenny/pkg/observability/tracing"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// ErrConnectorInactive is returned when a tools/call targets a
// soft-deleted connector. A deleted connector is not dialable: §9.3
// requires every dialed endpoint to resolve to an active registered
// connector.
var ErrConnectorInactive = errors.New("connectorinvoke: connector is not active")

// ConnectorAuthorizer reports whether the calling session's effective
// §8.3 delegation policy permits an external tool call against a
// connector. The gateway evaluates the connector against the session's
// runtime-level effective policy and the §10.6 environment-default
// policy; a denied connector returns a non-nil error. A nil Authorizer
// on the Invoker leaves the gate open (no policy layer wired).
//
// *connectorauthz.Authorizer is the production implementation.
//
// spec: §9.3 line 164.
type ConnectorAuthorizer interface {
	AuthorizeConnector(ctx context.Context, tenantID, sessionID, connectorID string, labels map[string]string) error
}

// Invoker resolves a registered connector and its gateway-held
// credential, then drives an outbound MCP `tools/call`. It is the
// gateway-side realization of §9.3 lines 142-164: the gateway acts as
// the MCP client to the external tool and supplies the connector
// credential the OAuth flow stored, so the credential never transits a
// pod.
type Invoker struct {
	connectors connectorstore.Store
	creds      connectorcredstore.Store
	client     *Client
	tracer     *tracing.Tracer
	authz      ConnectorAuthorizer
	// environments resolves the §10.6 environment whose connectorSelector
	// capability filter gates a connector tools/call. nil leaves the gate
	// open. spec: §10.6 line 607.
	environments EnvironmentResolver
	clock        func() time.Time
}

// NewInvoker wires the connector registry, the connector-credential
// store, and the outbound MCP client. tracer may be nil; when set, every
// tools/call opens the §16 `mcp.external_tool_call` span. authz may be
// nil; when set, CallTool enforces the §9.3 line 164 connector-access
// boundary before proxying a tool call.
func NewInvoker(connectors connectorstore.Store, creds connectorcredstore.Store, client *Client, tracer *tracing.Tracer, authz ConnectorAuthorizer) *Invoker {
	return &Invoker{connectors: connectors, creds: creds, client: client, tracer: tracer, authz: authz, clock: func() time.Time { return time.Now().UTC() }}
}

// WithClock overrides the clock used to stamp CapabilitiesRefreshedAt.
// Tests inject a deterministic clock; production uses time.Now.
func (iv *Invoker) WithClock(now func() time.Time) *Invoker {
	if now != nil {
		iv.clock = now
	}
	return iv
}

// CallTool invokes toolName on the connector identified by connectorID
// for the calling session sessionID, scoped to (tenantID, userID,
// environment) for credential lookup. The connector must be registered
// and active. Before any outbound dial the §9.3 line 164 connector-access
// boundary is enforced: the connector is validated against the calling
// session's effective delegation policy, and a connector the policy does
// not permit is rejected without contacting the external endpoint. When a
// stored credential exists for the four-tuple it is carried as the bearer
// token; a public connector with no stored credential is dialed
// unauthenticated.
//
// spec: §9.1 line 10; §9.3 lines 142-164.
func (iv *Invoker) CallTool(ctx context.Context, tenantID, sessionID, connectorID, userID, environment, toolName string, arguments json.RawMessage) (json.RawMessage, error) {
	conn, err := iv.connectors.Get(ctx, tenantID, connectorID)
	if err != nil {
		return nil, err
	}
	if !conn.IsActive() {
		return nil, ErrConnectorInactive
	}

	// spec: §9.3 line 164 — the gateway validates the connector_id against
	// the calling pod's effective delegation policy before proxying. A
	// child cannot use connectors its policy does not permit even when a
	// gateway-held credential exists for them at the root level. The check
	// runs before the bearer lookup and the outbound dial so a denied call
	// never reaches the external endpoint.
	if iv.authz != nil {
		if err := iv.authz.AuthorizeConnector(ctx, tenantID, sessionID, connectorID, conn.Labels); err != nil {
			return nil, err
		}
	}

	// spec: §10.6 line 607 — the calling session's environment
	// connectorSelector capability filter governs what an admitted
	// connector may do. A tool whose inferred capability the filter
	// denies is rejected before the outbound dial. F-10.6.2.
	if err := iv.enforceConnectorCapabilities(ctx, tenantID, environment, conn, toolName); err != nil {
		return nil, err
	}

	var span trace.Span
	if iv.tracer != nil {
		ctx, span = iv.tracer.Start(ctx, tracing.SpanMCPExternalToolCall)
		span.SetAttributes(
			attribute.String("connector.id", conn.ID),
			attribute.String("connector.tenant_id", conn.TenantID),
			attribute.String("mcp.tool", toolName),
		)
		defer span.End()
	}

	bearer := iv.bearerFor(ctx, conn, userID, environment)
	sess, _, err := iv.client.Initialize(ctx, conn.MCPServerURL, bearer)
	if err != nil {
		if span != nil {
			tracing.RecordError(span, err)
		}
		return nil, fmt.Errorf("connectorinvoke: initialize %q: %w", connectorID, err)
	}
	result, err := sess.CallTool(ctx, toolName, arguments)
	if err != nil && span != nil {
		tracing.RecordError(span, err)
	}
	return result, err
}

// bearerFor returns the stored access token for the connector's
// four-tuple, or empty for a public connector or a connector whose
// credential has not yet been obtained.
func (iv *Invoker) bearerFor(ctx context.Context, conn connectorstore.Connector, userID, environment string) string {
	if iv.creds == nil || conn.Auth == nil {
		return ""
	}
	cred, err := iv.creds.Get(ctx, conn.TenantID, conn.ID, userID, environment)
	if err != nil || !cred.HasToken() {
		return ""
	}
	return cred.AccessToken
}
