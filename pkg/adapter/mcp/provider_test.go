// SPDX-License-Identifier: MIT

package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter/mcp"
)

// fakeProvider is a §9.1 ToolProvider that records calls and returns
// canned results, so the server's provider dispatch can be exercised
// without a gateway. spec: §9.1 lines 14-31. F-9.1.1.
type fakeProvider struct {
	list     []mcp.Tool
	listErr  error
	result   json.RawMessage
	callErr  error
	gotName  string
	gotArgs  string
	gotCalls int
}

func (p *fakeProvider) List(context.Context) ([]mcp.Tool, error) {
	return p.list, p.listErr
}

func (p *fakeProvider) Call(_ context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
	p.gotCalls++
	p.gotName = name
	p.gotArgs = string(args)
	if p.callErr != nil {
		return nil, p.callErr
	}
	return p.result, nil
}

// spec: §9.1 lines 14-31 — tools/list merges the locally-registered
// tools with the Provider's catalog so the intra-pod platform MCP server
// advertises the gateway's platform tools without duplicating schemas.
func TestServerToolsListMergesProvider_spec_9_1(t *testing.T) {
	s := mcp.NewServer()
	s.Register(mcp.Tool{Name: "local/tool", Handler: func(json.RawMessage) (any, error) { return nil, nil }})
	s.Provider = &fakeProvider{list: []mcp.Tool{
		{Name: "lenny/delegate_task", Description: "delegate", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "lenny/await_children"},
	}}

	enc, dec := serverPipe(t, s, testNonce)
	sendRequest(t, enc, 1, "initialize", initParams(testNonce))
	readResponse(t, dec)

	sendRequest(t, enc, 2, "tools/list", nil)
	resp := readResponse(t, dec)
	var result struct {
		Tools []struct {
			Name        string          `json:"name"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(resp["result"], &result); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}
	got := map[string]bool{}
	for _, tl := range result.Tools {
		got[tl.Name] = true
	}
	for _, want := range []string{"local/tool", "lenny/delegate_task", "lenny/await_children"} {
		if !got[want] {
			t.Errorf("tools/list missing %q; got %+v", want, result.Tools)
		}
	}
}

// spec: §9.1 line 14 — a tools/call for a tool the intra-pod server does
// not register locally is forwarded to the gateway through the Provider,
// and the gateway's MCP result is returned verbatim.
func TestServerToolsCallForwardsToProvider_spec_9_1(t *testing.T) {
	prov := &fakeProvider{result: json.RawMessage(`{"content":[{"type":"text","text":"ok"}]}`)}
	s := mcp.NewServer()
	s.Provider = prov

	enc, dec := serverPipe(t, s, testNonce)
	sendRequest(t, enc, 1, "initialize", initParams(testNonce))
	readResponse(t, dec)

	sendRequest(t, enc, 2, "tools/call", map[string]any{
		"name": "lenny/delegate_task", "arguments": map[string]any{"runtime": "claude-code"},
	})
	resp := readResponse(t, dec)
	if _, isErr := resp["error"]; isErr {
		t.Fatalf("tools/call returned a transport error: %s", resp["error"])
	}
	if prov.gotCalls != 1 || prov.gotName != "lenny/delegate_task" {
		t.Errorf("provider.Call = (%d, %q), want (1, lenny/delegate_task)", prov.gotCalls, prov.gotName)
	}
	if prov.gotArgs != `{"runtime":"claude-code"}` {
		t.Errorf("provider received args %q", prov.gotArgs)
	}
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(resp["result"], &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].Text != "ok" {
		t.Errorf("result = %s, want the provider's content verbatim", resp["result"])
	}
}

// spec: §9.1 line 14 — a Provider routing failure (e.g. the gateway
// reports an unknown session) surfaces as a JSON-RPC error, distinct from
// an in-band isError tool result.
func TestServerProviderCallErrorIsJSONRPCError_spec_9_1(t *testing.T) {
	s := mcp.NewServer()
	s.Provider = &fakeProvider{callErr: errors.New("no active client")}

	enc, dec := serverPipe(t, s, testNonce)
	sendRequest(t, enc, 1, "initialize", initParams(testNonce))
	readResponse(t, dec)

	sendRequest(t, enc, 2, "tools/call", map[string]any{"name": "lenny/output", "arguments": map[string]any{}})
	resp := readResponse(t, dec)
	if _, isErr := resp["error"]; !isErr {
		t.Fatalf("provider error did not surface as a JSON-RPC error: %s", resp["result"])
	}
}

// A locally-registered tool is dispatched locally even when a Provider is
// wired; the Provider is only the fallback for unregistered names.
func TestServerLocalToolWinsOverProvider_spec_9_1(t *testing.T) {
	prov := &fakeProvider{result: json.RawMessage(`{"content":[]}`)}
	s := mcp.NewServer()
	s.Provider = prov
	s.Register(mcp.Tool{Name: "local/echo", Handler: func(json.RawMessage) (any, error) {
		return map[string]any{"local": true}, nil
	}})

	enc, dec := serverPipe(t, s, testNonce)
	sendRequest(t, enc, 1, "initialize", initParams(testNonce))
	readResponse(t, dec)

	sendRequest(t, enc, 2, "tools/call", map[string]any{"name": "local/echo", "arguments": map[string]any{}})
	resp := readResponse(t, dec)
	var result struct {
		Local bool `json:"local"`
	}
	if err := json.Unmarshal(resp["result"], &result); err != nil || !result.Local {
		t.Errorf("local tool not dispatched locally: %s (err %v)", resp["result"], err)
	}
	if prov.gotCalls != 0 {
		t.Errorf("provider.Call invoked %d times for a locally-registered tool, want 0", prov.gotCalls)
	}
}
