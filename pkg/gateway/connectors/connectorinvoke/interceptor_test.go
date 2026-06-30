// SPDX-License-Identifier: MIT

package connectorinvoke

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorcredstore"
	"github.com/lennylabs/lenny/pkg/gateway/connectors/connectorstore"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
)

// spec: §4.8 lines 1057-1058, 1077, §15.1 lines 1014-1015 — the
// PreConnectorRequest and PostConnectorResponse interceptor phases on the
// gateway-proxied connector path. A REJECT returns
// CONNECTOR_REQUEST_REJECTED / CONNECTOR_RESPONSE_REJECTED, a MODIFY
// rewrites the arguments / response content, a MODIFY of tool_name or
// connector_id is an INTERCEPTOR_IMMUTABLE_FIELD_VIOLATION, and a
// fail-closed interceptor error returns INTERCEPTOR_TIMEOUT.

// progIc is a built-in interceptor whose Intercept is a provided closure,
// so a test can return a fixed Result, an error, or a content-derived
// MODIFY. Builtin() == true lets it register at any phase.
type progIc struct {
	name string
	fn   func(interceptor.Request) (interceptor.Result, error)
	fail interceptor.FailPolicy
}

func (p progIc) Name() string    { return p.name }
func (p progIc) Priority() int32 { return 400 }
func (p progIc) Builtin() bool   { return true }
func (p progIc) FailPolicy() interceptor.FailPolicy {
	if p.fail == "" {
		return interceptor.FailClosed
	}
	return p.fail
}
func (p progIc) Timeout() time.Duration { return 0 }
func (p progIc) Intercept(_ context.Context, req interceptor.Request) (interceptor.Result, error) {
	return p.fn(req)
}

func connectorChain(t *testing.T, phase interceptor.Phase, ic interceptor.Interceptor) *interceptor.Chain {
	t.Helper()
	c := interceptor.NewChain()
	if err := c.Register(phase, ic); err != nil {
		t.Fatalf("register %s: %v", phase, err)
	}
	return c
}

func seedGithub(t *testing.T, connectors connectorstore.Store) {
	seedConnector(t, connectors, connectorstore.Connector{
		TenantID: "acme", ID: "github", MCPServerURL: "https://mcp.github.example",
	})
}

// callDoer returns a doer programmed for one successful initialize + ack +
// tools/call exchange whose result is the provided JSON-RPC result body.
func callDoer(result string) *fakeDoer {
	return &fakeDoer{responses: []*http.Response{
		jsonResp(200, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-06-18"}}`, nil),
		jsonResp(202, ``, nil),
		jsonResp(200, result, nil),
	}}
}

func TestPreConnectorRequestReject_spec_4_8_1057(t *testing.T) {
	connectors := connectorstore.NewMemory()
	seedGithub(t, connectors)
	doer := &fakeDoer{}
	chain := connectorChain(t, interceptor.PhasePreConnectorRequest, progIc{
		name: "blocker",
		fn: func(interceptor.Request) (interceptor.Result, error) {
			return interceptor.Result{Action: interceptor.ActionReject, Reason: "destructive op blocked"}, nil
		},
	})
	iv := NewInvoker(connectors, connectorcredstore.NewMemory(clock), New(doer), nil, nil).WithInterceptors(chain)

	_, err := iv.CallTool(context.Background(), "acme", "sess-1", "github", "alice", "", "delete", nil)
	rej, ok := AsRejection(err)
	if !ok {
		t.Fatalf("err = %v, want *RejectionError", err)
	}
	if rej.Code != CodeConnectorRequestRejected {
		t.Errorf("code = %q, want %q", rej.Code, CodeConnectorRequestRejected)
	}
	if len(doer.reqs) != 0 {
		t.Errorf("dialed %d times on a PreConnectorRequest reject, want 0", len(doer.reqs))
	}
}

func TestPreConnectorRequestModifyArguments_spec_4_8_1057(t *testing.T) {
	connectors := connectorstore.NewMemory()
	seedGithub(t, connectors)
	doer := callDoer(`{"jsonrpc":"2.0","id":2,"result":{"content":[]}}`)
	chain := connectorChain(t, interceptor.PhasePreConnectorRequest, progIc{
		name: "redactor",
		fn: func(req interceptor.Request) (interceptor.Result, error) {
			var p preConnectorPayload
			if err := json.Unmarshal(req.Content, &p); err != nil {
				return interceptor.Result{}, err
			}
			p.Arguments = json.RawMessage(`{"redacted":true}`)
			out, _ := json.Marshal(p)
			return interceptor.Result{Action: interceptor.ActionModify, ModifiedContent: out}, nil
		},
	})
	iv := NewInvoker(connectors, connectorcredstore.NewMemory(clock), New(doer), nil, nil).WithInterceptors(chain)

	if _, err := iv.CallTool(context.Background(), "acme", "sess-1", "github", "alice", "", "search", json.RawMessage(`{"q":"secret"}`)); err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !strings.Contains(doer.bodies[2], "redacted") || strings.Contains(doer.bodies[2], "secret") {
		t.Errorf("tools/call did not carry the rewritten arguments: %s", doer.bodies[2])
	}
}

func TestPreConnectorRequestImmutableConnectorID_spec_4_8_1060(t *testing.T) {
	connectors := connectorstore.NewMemory()
	seedGithub(t, connectors)
	doer := &fakeDoer{}
	chain := connectorChain(t, interceptor.PhasePreConnectorRequest, progIc{
		name: "rerouter",
		fn: func(req interceptor.Request) (interceptor.Result, error) {
			var p preConnectorPayload
			_ = json.Unmarshal(req.Content, &p)
			p.ConnectorID = "other" // forbidden: would bypass connector access control
			out, _ := json.Marshal(p)
			return interceptor.Result{Action: interceptor.ActionModify, ModifiedContent: out}, nil
		},
	})
	iv := NewInvoker(connectors, connectorcredstore.NewMemory(clock), New(doer), nil, nil).WithInterceptors(chain)

	_, err := iv.CallTool(context.Background(), "acme", "sess-1", "github", "alice", "", "search", nil)
	rej, ok := AsRejection(err)
	if !ok {
		t.Fatalf("err = %v, want *RejectionError", err)
	}
	if rej.Code != interceptor.CodeInterceptorImmutableFieldViolation {
		t.Errorf("code = %q, want %q", rej.Code, interceptor.CodeInterceptorImmutableFieldViolation)
	}
	if len(doer.reqs) != 0 {
		t.Errorf("dialed %d times on an immutable-field violation, want 0", len(doer.reqs))
	}
}

func TestPostConnectorResponseReject_spec_4_8_1058(t *testing.T) {
	connectors := connectorstore.NewMemory()
	seedGithub(t, connectors)
	doer := callDoer(`{"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"4111-1111-1111-1111"}]}}`)
	chain := connectorChain(t, interceptor.PhasePostConnectorResponse, progIc{
		name: "leakblock",
		fn: func(interceptor.Request) (interceptor.Result, error) {
			return interceptor.Result{Action: interceptor.ActionReject, Reason: "PII in response"}, nil
		},
	})
	iv := NewInvoker(connectors, connectorcredstore.NewMemory(clock), New(doer), nil, nil).WithInterceptors(chain)

	_, err := iv.CallTool(context.Background(), "acme", "sess-1", "github", "alice", "", "read", nil)
	rej, ok := AsRejection(err)
	if !ok {
		t.Fatalf("err = %v, want *RejectionError", err)
	}
	if rej.Code != CodeConnectorResponseRejected {
		t.Errorf("code = %q, want %q", rej.Code, CodeConnectorResponseRejected)
	}
	// The external call was made before the response was inspected.
	if len(doer.reqs) != 3 {
		t.Errorf("dialed %d times, want 3 (the call ran before the response reject)", len(doer.reqs))
	}
}

func TestPostConnectorResponseModifyContent_spec_4_8_1058(t *testing.T) {
	connectors := connectorstore.NewMemory()
	seedGithub(t, connectors)
	doer := callDoer(`{"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"raw"}],"structuredContent":{"k":"v"}}}`)
	chain := connectorChain(t, interceptor.PhasePostConnectorResponse, progIc{
		name: "redactor",
		fn: func(interceptor.Request) (interceptor.Result, error) {
			out := []byte(`{"tool_name":"read","connector_id":"github","content":[{"type":"text","text":"[redacted]"}],"isError":true}`)
			return interceptor.Result{Action: interceptor.ActionModify, ModifiedContent: out}, nil
		},
	})
	iv := NewInvoker(connectors, connectorcredstore.NewMemory(clock), New(doer), nil, nil).WithInterceptors(chain)

	result, err := iv.CallTool(context.Background(), "acme", "sess-1", "github", "alice", "", "read", nil)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(result, &got); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if !strings.Contains(string(got["content"]), "[redacted]") {
		t.Errorf("content not redacted: %s", got["content"])
	}
	if string(got["isError"]) != "true" {
		t.Errorf("isError = %s, want true", got["isError"])
	}
	if _, ok := got["structuredContent"]; !ok {
		t.Error("structuredContent was dropped; MODIFY should preserve untouched result fields")
	}
	if _, ok := got["tool_name"]; ok {
		t.Error("tool_name leaked into the MCP result; it belongs to the phase payload only")
	}
}

func TestPreConnectorRequestFailClosed_spec_4_8_1077(t *testing.T) {
	connectors := connectorstore.NewMemory()
	seedGithub(t, connectors)
	doer := &fakeDoer{}
	chain := connectorChain(t, interceptor.PhasePreConnectorRequest, progIc{
		name: "broken",
		fail: interceptor.FailClosed,
		fn: func(interceptor.Request) (interceptor.Result, error) {
			return interceptor.Result{}, context.DeadlineExceeded
		},
	})
	iv := NewInvoker(connectors, connectorcredstore.NewMemory(clock), New(doer), nil, nil).WithInterceptors(chain)

	_, err := iv.CallTool(context.Background(), "acme", "sess-1", "github", "alice", "", "x", nil)
	rej, ok := AsRejection(err)
	if !ok {
		t.Fatalf("err = %v, want *RejectionError", err)
	}
	if rej.Code != interceptor.CodeInterceptorTimeout {
		t.Errorf("code = %q, want %q", rej.Code, interceptor.CodeInterceptorTimeout)
	}
	if len(doer.reqs) != 0 {
		t.Errorf("dialed %d times on a fail-closed interceptor, want 0", len(doer.reqs))
	}
}

// TestConnectorPhasesNoInterceptorPassthrough confirms a chain that
// registers no interceptor for the connector phases leaves the call
// unmodified: the §4.8 phases are no-ops when nothing is registered.
func TestConnectorPhasesNoInterceptorPassthrough(t *testing.T) {
	connectors := connectorstore.NewMemory()
	seedGithub(t, connectors)
	doer := callDoer(`{"jsonrpc":"2.0","id":2,"result":{"content":[]}}`)
	// A chain with an interceptor on an unrelated phase only.
	chain := connectorChain(t, interceptor.PhasePostAuth, progIc{
		name: "elsewhere",
		fn: func(interceptor.Request) (interceptor.Result, error) {
			return interceptor.Result{Action: interceptor.ActionReject}, nil
		},
	})
	iv := NewInvoker(connectors, connectorcredstore.NewMemory(clock), New(doer), nil, nil).WithInterceptors(chain)

	if _, err := iv.CallTool(context.Background(), "acme", "sess-1", "github", "alice", "", "x", json.RawMessage(`{"a":1}`)); err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !strings.Contains(doer.bodies[2], `"a":1`) {
		t.Errorf("arguments altered by an unrelated-phase interceptor: %s", doer.bodies[2])
	}
}
