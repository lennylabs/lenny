// SPDX-License-Identifier: MIT

// Package connectortools bridges the §9.3 GatewayControl connector tool
// forwarding RPCs to the gateway connector-invocation surface.
//
// A type:agent runtime reaches a §9.3 connector's tools by dialing the
// intra-pod @lenny-connector-<id> MCP server the adapter opens for every
// connector its session's effective delegation policy permits. The
// adapter forwards each tools/list and tools/call to the gateway over
// GatewayControl.{ListSessionConnectors,ListConnectorTools,CallConnectorTool};
// this bridge is the gateway-side dispatch:
//
//   - ListSessionConnectors resolves the calling session's tenant, lists
//     the tenant's registered connectors, and filters them by the
//     session's §8.3 effective delegation policy, so a connector the
//     policy denies is never advertised to the pod.
//   - ListConnectorTools and CallConnectorTool resolve the session's
//     tenant, owner, and environment, then drive the connectorinvoke
//     Invoker, which dials the external endpoint as the MCP client with
//     the gateway-held OAuth credential. The credential never transits
//     the pod, satisfying the §9.3 line 163 invariant.
//
// The connector-access boundary (§9.3 line 164) is enforced twice:
// ListSessionConnectors filters at discovery, and the Invoker re-checks
// every ListTools and CallTool against the policy before any outbound
// dial, so a stale advertised connector cannot be exercised after the
// policy tightens.
//
// spec: §9.3 lines 142-164. F-9.1.2.
package connectortools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/lennylabs/lenny/pkg/gateway/connectorauthz"
	"github.com/lennylabs/lenny/pkg/gateway/connectorinvoke"
	"github.com/lennylabs/lenny/pkg/gateway/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/leasecontrol"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// SessionResolver resolves a session id to its row tenant-agnostically.
// *sessionstore via GetByID satisfies it. The bridge needs the
// tenant-agnostic lookup because the adapter forwards only the session
// id; the session's own tenant, owner, and environment scope the
// dispatch.
type SessionResolver interface {
	GetByID(ctx context.Context, id string) (sessionstore.Session, error)
}

// Authorizer reports whether the calling session's §8.3 effective
// delegation policy permits a connector. *connectorauthz.Authorizer
// satisfies it. ListSessionConnectors consults it to filter the
// advertised connector set. A nil Authorizer leaves the discovery gate
// open (no policy layer wired); the Invoker still re-checks at call time.
//
// spec: §9.3 line 164.
type Authorizer interface {
	AuthorizeConnector(ctx context.Context, tenantID, sessionID, connectorID string, labels map[string]string) error
}

// Invoker dials a registered connector's external MCP endpoint for the
// calling session. *connectorinvoke.Invoker satisfies it. ListTools and
// CallTool both enforce the §9.3 line 164 connector-access boundary
// before any outbound dial.
type Invoker interface {
	ListTools(ctx context.Context, tenantID, sessionID, connectorID, userID, environment string) ([]connectorinvoke.ToolDescriptor, error)
	CallTool(ctx context.Context, tenantID, sessionID, connectorID, userID, environment, toolName string, arguments json.RawMessage) (json.RawMessage, error)
}

// Bridge implements leasecontrol.ConnectorToolService over a session
// resolver, the connector registry, the policy authorizer, and the
// outbound connector invoker.
type Bridge struct {
	sessions   SessionResolver
	connectors connectorstore.Store
	authz      Authorizer
	invoker    Invoker
}

// New returns a Bridge. sessions, connectors, and invoker are required;
// authz may be nil (the discovery gate is then open, but the Invoker
// still re-checks every call).
func New(sessions SessionResolver, connectors connectorstore.Store, authz Authorizer, invoker Invoker) *Bridge {
	return &Bridge{sessions: sessions, connectors: connectors, authz: authz, invoker: invoker}
}

var _ leasecontrol.ConnectorToolService = (*Bridge)(nil)

// ListSessionConnectors returns the connectors sessionID's effective
// delegation policy permits. It resolves the session's tenant, lists the
// tenant's active connectors, and filters them by the policy, so the
// adapter opens one intra-pod MCP server per permitted connector. An
// unknown session maps to leasecontrol.ErrSessionNotFound.
// spec: §9.3 line 142. F-9.1.2.
func (b *Bridge) ListSessionConnectors(ctx context.Context, sessionID string) ([]leasecontrol.SessionConnectorDescriptor, error) {
	sess, err := b.sessions.GetByID(ctx, sessionID)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			return nil, leasecontrol.ErrSessionNotFound
		}
		return nil, fmt.Errorf("connectortools: resolve session %s: %w", sessionID, err)
	}
	conns, err := b.connectors.List(ctx, sess.TenantID, connectorstore.ListFilter{})
	if err != nil {
		return nil, fmt.Errorf("connectortools: list connectors for tenant %s: %w", sess.TenantID, err)
	}
	out := make([]leasecontrol.SessionConnectorDescriptor, 0, len(conns))
	for _, c := range conns {
		if !c.IsActive() {
			continue
		}
		if b.authz != nil {
			if aerr := b.authz.AuthorizeConnector(ctx, sess.TenantID, sessionID, c.ID, c.Labels); aerr != nil {
				if errors.Is(aerr, connectorauthz.ErrConnectorNotPermitted) {
					continue
				}
				return nil, fmt.Errorf("connectortools: authorize connector %s: %w", c.ID, aerr)
			}
		}
		out = append(out, leasecontrol.SessionConnectorDescriptor{ID: c.ID, DisplayName: c.DisplayName})
	}
	return out, nil
}

// ListConnectorTools returns the tool catalog the named connector
// exposes, scoped to the calling session. It resolves the session's
// tenant, owner, and environment and drives the Invoker, which dials the
// external endpoint with the gateway-held credential. A denied connector
// maps to leasecontrol.ErrConnectorNotPermitted. spec: §9.3 lines 142-164.
// F-9.1.2.
func (b *Bridge) ListConnectorTools(ctx context.Context, sessionID, connectorID string) ([]leasecontrol.PlatformToolDescriptor, error) {
	sess, err := b.sessions.GetByID(ctx, sessionID)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			return nil, leasecontrol.ErrSessionNotFound
		}
		return nil, fmt.Errorf("connectortools: resolve session %s: %w", sessionID, err)
	}
	tools, err := b.invoker.ListTools(ctx, sess.TenantID, sessionID, connectorID, sess.UserID, sess.Environment)
	if err != nil {
		return nil, mapInvokeErr(err)
	}
	out := make([]leasecontrol.PlatformToolDescriptor, 0, len(tools))
	for _, t := range tools {
		out = append(out, leasecontrol.PlatformToolDescriptor{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: append([]byte(nil), t.InputSchema...),
		})
	}
	return out, nil
}

// CallConnectorTool forwards one external tool call on behalf of
// sessionID and returns the JSON-encoded §15.2 MCP tool result plus its
// isError flag. It resolves the session's tenant, owner, and environment
// and drives the Invoker, which enforces the §9.3 line 164 policy gate
// and dials the external endpoint with the gateway-held credential. A
// denied connector maps to leasecontrol.ErrConnectorNotPermitted; a
// transport failure maps to a generic error the handler reports as
// Internal. spec: §9.3 lines 142-164. F-9.1.2.
func (b *Bridge) CallConnectorTool(ctx context.Context, sessionID, connectorID, toolName string, arguments []byte) ([]byte, bool, error) {
	sess, err := b.sessions.GetByID(ctx, sessionID)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			return nil, false, leasecontrol.ErrSessionNotFound
		}
		return nil, false, fmt.Errorf("connectortools: resolve session %s: %w", sessionID, err)
	}
	raw, err := b.invoker.CallTool(ctx, sess.TenantID, sessionID, connectorID, sess.UserID, sess.Environment, toolName, json.RawMessage(arguments))
	if err != nil {
		return nil, false, mapInvokeErr(err)
	}
	return raw, isErrorResult(raw), nil
}

// mapInvokeErr translates a §9.3 line 164 policy denial into the
// leasecontrol sentinel the GatewayControl handler maps to
// codes.PermissionDenied. Any other error is returned verbatim and the
// handler reports it as Internal.
func mapInvokeErr(err error) error {
	if errors.Is(err, connectorauthz.ErrConnectorNotPermitted) {
		return leasecontrol.ErrConnectorNotPermitted
	}
	return err
}

// isErrorResult reports whether the external MCP tools/call result object
// carries `isError: true`. A result that does not parse as an object, or
// omits the field, is treated as a success so a non-conforming connector
// response does not spuriously flip the flag.
func isErrorResult(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var r struct {
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return false
	}
	return r.IsError
}
