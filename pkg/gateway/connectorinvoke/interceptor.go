// SPDX-License-Identifier: MIT

package connectorinvoke

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
)

// §4.8 lines 1057-1058, 1077, §15.1 lines 1014-1015. A deliberate REJECT
// by a PreConnectorRequest interceptor returns CONNECTOR_REQUEST_REJECTED
// (HTTP 403); a PostConnectorResponse REJECT returns
// CONNECTOR_RESPONSE_REJECTED (HTTP 502). Both are distinct from
// INTERCEPTOR_TIMEOUT, which a fail-closed interceptor error or timeout
// produces, and from INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION, which a MODIFY
// that alters tool_name/connector_id produces.
const (
	CodeConnectorRequestRejected  = "CONNECTOR_REQUEST_REJECTED"
	CodeConnectorResponseRejected = "CONNECTOR_RESPONSE_REJECTED"
)

// ConnectorChain is the §4.8 interceptor chain the gateway runs at the
// PreConnectorRequest and PostConnectorResponse phases on the connector
// proxy path. *interceptor.Chain satisfies it; a nil chain disables the
// connector interceptor phases. spec: §4.8 line 1077.
type ConnectorChain interface {
	Run(ctx context.Context, req interceptor.Request) interceptor.Result
	Len(phase interceptor.Phase) int
}

// RejectionError is returned by CallTool when a connector interceptor
// phase rejects the call. Code is the §15.1 error code the gateway
// surfaces to the pod (CONNECTOR_REQUEST_REJECTED,
// CONNECTOR_RESPONSE_REJECTED, INTERCEPTOR_TIMEOUT, or
// INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION). spec: §4.8 lines 1057-1058,
// 1077; §15.1 lines 1014-1015.
type RejectionError struct {
	Phase  interceptor.Phase
	Code   string
	Reason string
}

func (e *RejectionError) Error() string {
	return fmt.Sprintf("connectorinvoke: %s rejected connector call at %s: %s", e.Code, e.Phase, e.Reason)
}

// ConnectorRejectionCode returns the §15.1 error code. It lets the
// leasecontrol GatewayControl handler map the rejection to a gRPC status
// without importing this package. spec: §15.1 lines 1014-1015.
func (e *RejectionError) ConnectorRejectionCode() string { return e.Code }

// ConnectorRejectionReason returns the interceptor's human-readable
// rejection reason for the gRPC status message.
func (e *RejectionError) ConnectorRejectionReason() string { return e.Reason }

// preConnectorPayload is the §4.8 line 1057 PreConnectorRequest content:
// the outgoing tool call before the gateway proxies it. The interceptor
// may modify arguments but may not alter tool_name or connector_id (the
// chain enforces the immutability via phaseImmutableFields).
type preConnectorPayload struct {
	ToolName    string          `json:"tool_name"`
	Arguments   json.RawMessage `json:"arguments"`
	ConnectorID string          `json:"connector_id"`
}

// runPreConnectorRequest runs the PreConnectorRequest chain over the
// serialized outgoing tool call and returns the (possibly
// MODIFY-rewritten) arguments. A nil or empty-phase chain returns the
// arguments unchanged. A REJECT returns a *RejectionError carrying the
// §15.1 code. spec: §4.8 line 1057.
func (iv *Invoker) runPreConnectorRequest(ctx context.Context, tenantID, sessionID, connectorID, toolName string, arguments json.RawMessage) (json.RawMessage, error) {
	if iv.interceptors == nil || iv.interceptors.Len(interceptor.PhasePreConnectorRequest) == 0 {
		return arguments, nil
	}
	args := arguments
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	content, err := json.Marshal(preConnectorPayload{ToolName: toolName, Arguments: args, ConnectorID: connectorID})
	if err != nil {
		return nil, fmt.Errorf("connectorinvoke: marshal PreConnectorRequest payload: %w", err)
	}
	res := iv.interceptors.Run(ctx, interceptor.Request{
		Phase:     interceptor.PhasePreConnectorRequest,
		SessionID: sessionID,
		TenantID:  tenantID,
		Content:   content,
	})
	switch res.Action {
	case interceptor.ActionReject:
		return nil, rejectionFor(interceptor.PhasePreConnectorRequest, res)
	case interceptor.ActionModify:
		var p preConnectorPayload
		if err := json.Unmarshal(res.ModifiedContent, &p); err != nil {
			return nil, fmt.Errorf("connectorinvoke: PreConnectorRequest MODIFY produced invalid payload: %w", err)
		}
		if len(p.Arguments) == 0 {
			return json.RawMessage(`{}`), nil
		}
		return p.Arguments, nil
	default:
		return arguments, nil
	}
}

// runPostConnectorResponse runs the PostConnectorResponse chain over the
// serialized connector result and returns the (possibly MODIFY-rewritten)
// result. The phase payload merges the routing identity (tool_name,
// connector_id) onto the MCP tool result so the chain can enforce the
// immutable routing fields while permitting content/isError edits. A nil
// or empty-phase chain returns the result unchanged. A REJECT returns a
// *RejectionError carrying the §15.1 code. spec: §4.8 line 1058.
func (iv *Invoker) runPostConnectorResponse(ctx context.Context, tenantID, sessionID, connectorID, toolName string, result json.RawMessage) (json.RawMessage, error) {
	if iv.interceptors == nil || iv.interceptors.Len(interceptor.PhasePostConnectorResponse) == 0 {
		return result, nil
	}
	content, err := mergePostConnectorPayload(toolName, connectorID, result)
	if err != nil {
		return nil, err
	}
	res := iv.interceptors.Run(ctx, interceptor.Request{
		Phase:     interceptor.PhasePostConnectorResponse,
		SessionID: sessionID,
		TenantID:  tenantID,
		Content:   content,
	})
	switch res.Action {
	case interceptor.ActionReject:
		return nil, rejectionFor(interceptor.PhasePostConnectorResponse, res)
	case interceptor.ActionModify:
		return applyPostConnectorModify(result, res.ModifiedContent)
	default:
		return result, nil
	}
}

// mergePostConnectorPayload builds the §4.8 line 1058 PostConnectorResponse
// content `{ tool_name, connector_id, content, isError? }` by overlaying
// the routing identity onto the connector's MCP tool result. content and
// isError are taken from the result object when present; a result that is
// not a JSON object carries its raw bytes as content.
func mergePostConnectorPayload(toolName, connectorID string, result json.RawMessage) ([]byte, error) {
	payload := map[string]json.RawMessage{
		"tool_name":    mustJSONString(toolName),
		"connector_id": mustJSONString(connectorID),
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(result, &obj); err == nil && obj != nil {
		if c, ok := obj["content"]; ok {
			payload["content"] = c
		}
		if e, ok := obj["isError"]; ok {
			payload["isError"] = e
		}
	} else if len(result) > 0 {
		payload["content"] = result
	}
	return json.Marshal(payload)
}

// applyPostConnectorModify rebuilds the MCP tool result from the original
// result and the interceptor's modified PostConnectorResponse payload,
// applying only the content and isError edits and preserving any other
// result fields (e.g. structuredContent) the connector returned. tool_name
// and connector_id are dropped because they belong to the phase payload,
// not the MCP result.
func applyPostConnectorModify(original, modified json.RawMessage) (json.RawMessage, error) {
	var mod map[string]json.RawMessage
	if err := json.Unmarshal(modified, &mod); err != nil {
		return nil, fmt.Errorf("connectorinvoke: PostConnectorResponse MODIFY produced invalid payload: %w", err)
	}
	out := map[string]json.RawMessage{}
	if err := json.Unmarshal(original, &out); err != nil || out == nil {
		out = map[string]json.RawMessage{}
	}
	if c, ok := mod["content"]; ok {
		out["content"] = c
	}
	if e, ok := mod["isError"]; ok {
		out["isError"] = e
	}
	return json.Marshal(out)
}

// rejectionFor maps an interceptor REJECT result to the *RejectionError
// the connector proxy surfaces. A fail-closed timeout/error carries
// INTERCEPTOR_TIMEOUT; an immutable-field violation carries its own code;
// a deliberate REJECT carries the phase-specific connector code. spec:
// §4.8 line 1077; §15.1 lines 1014-1015.
func rejectionFor(phase interceptor.Phase, res interceptor.Result) *RejectionError {
	code := res.Code
	if code == "" {
		if phase == interceptor.PhasePreConnectorRequest {
			code = CodeConnectorRequestRejected
		} else {
			code = CodeConnectorResponseRejected
		}
	}
	return &RejectionError{Phase: phase, Code: code, Reason: res.Reason}
}

func mustJSONString(s string) json.RawMessage {
	b, _ := json.Marshal(s)
	return b
}

// AsRejection reports whether err is a connector-interceptor RejectionError
// and returns it. The leasecontrol GatewayControl handler uses it to map
// the §15.1 code to a gRPC status. spec: §4.8 line 1077.
func AsRejection(err error) (*RejectionError, bool) {
	var re *RejectionError
	if errors.As(err, &re) {
		return re, true
	}
	return nil, false
}
