// SPDX-License-Identifier: MIT

package connectorinvoke

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

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
}

// NewInvoker wires the connector registry, the connector-credential
// store, and the outbound MCP client. tracer may be nil; when set, every
// tools/call opens the §16 `mcp.external_tool_call` span.
func NewInvoker(connectors connectorstore.Store, creds connectorcredstore.Store, client *Client, tracer *tracing.Tracer) *Invoker {
	return &Invoker{connectors: connectors, creds: creds, client: client, tracer: tracer}
}

// CallTool invokes toolName on the connector identified by connectorID,
// scoped to (tenantID, userID, environment) for credential lookup. The
// connector must be registered and active. When a stored credential
// exists for the four-tuple it is carried as the bearer token; a public
// connector with no stored credential is dialed unauthenticated.
//
// spec: §9.1 line 10; §9.3 lines 142-164. The caller is responsible for
// the §9.3 delegation-policy authorization check before invoking (see
// F-9.3.1) — this method performs the transport, not the policy gate.
func (iv *Invoker) CallTool(ctx context.Context, tenantID, connectorID, userID, environment, toolName string, arguments json.RawMessage) (json.RawMessage, error) {
	conn, err := iv.connectors.Get(ctx, tenantID, connectorID)
	if err != nil {
		return nil, err
	}
	if !conn.IsActive() {
		return nil, ErrConnectorInactive
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
