// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §9.2 elicitation chain end-to-end
// through the real cmd/lenny-gateway binary. It builds a §8.2
// delegation tree through the platform MCP server's lenny/delegate_task
// tool, raises an elicitation from the deepest child via
// lenny/request_elicitation, and asserts the elicitation forwards
// hop-by-hop to the human-facing root where a §15.1 REST respond
// resolves it and unblocks the raising child. A second case asserts
// the §9.2 url-mode provenance controls: an agent-initiated url-mode
// elicitation is dropped while a connector-initiated one is admitted.

package tier4_integration_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// mcpClient drives the live gateway's §15.2 MCP adapter at /mcp and
// its §15.1 REST surface. The platform MCP tools dispatch against the
// gateway's fixed `default` MCP tenant, so the REST calls that seed
// and resolve sessions use the X-Lenny-Tenant-ID: default header to
// land in the same tenant.
type mcpClient struct {
	t    *testing.T
	base string
}

const mcpTenant = "default"

// rest issues a REST request against the live gateway under the
// `default` MCP tenant and returns the status and decoded body.
func (c mcpClient) rest(method, path string, body any) (int, map[string]any) {
	c.t.Helper()
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, reader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lenny-Tenant-ID", mcpTenant)
	req.Header.Set("X-Lenny-User-ID", "alice@acme.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	return resp.StatusCode, out
}

// callTool invokes one MCP tool via JSON-RPC tools/call and returns
// the decoded JSON-RPC response.
func (c mcpClient) callTool(name, argsJSON string) map[string]any {
	c.t.Helper()
	reqBody := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` +
		name + `","arguments":` + argsJSON + `}}`
	resp, err := http.DefaultClient.Post(c.base+"/mcp", "application/json", strings.NewReader(reqBody))
	if err != nil {
		c.t.Fatalf("POST /mcp %s: %v", name, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out
}

// toolResultText returns the text content of an MCP tool result, and
// whether the result was flagged isError.
func toolResultText(t *testing.T, rpc map[string]any) (string, bool) {
	t.Helper()
	result, ok := rpc["result"].(map[string]any)
	if !ok {
		t.Fatalf("MCP response carried no result: %v", rpc)
	}
	isErr, _ := result["isError"].(bool)
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		return "", isErr
	}
	c0, _ := content[0].(map[string]any)
	text, _ := c0["text"].(string)
	return text, isErr
}

// runningSession creates and starts a session under the `default`
// tenant, returning its id in the running state.
func (c mcpClient) runningSession() string {
	c.t.Helper()
	code, created := c.rest(http.MethodPost, "/v1/sessions/start", map[string]any{
		"runtimeRef": "echo",
		"userId":     "alice@acme.com",
	})
	if code != http.StatusCreated {
		c.t.Fatalf("create+start session: status %d (%v)", code, created)
	}
	id, _ := created["id"].(string)
	if id == "" {
		c.t.Fatal("session create returned no id")
	}
	return id
}

// delegateChild spawns a child session under parent through the
// lenny/delegate_task MCP tool and returns the child session id. The
// runtimeRef is distinct per hop so the §8 cycle detector — which
// keys on the (runtime, pool) tuple — does not reject the chain.
func (c mcpClient) delegateChild(parentID, runtimeRef string) string {
	c.t.Helper()
	// spec: §8.2 — the opaque `target` id replaces `runtimeRef`. F-8.2.1.
	rpc := c.callTool("lenny/delegate_task",
		`{"parentSessionId":"`+parentID+`","target":"`+runtimeRef+`"}`)
	text, isErr := toolResultText(c.t, rpc)
	if isErr {
		c.t.Fatalf("lenny/delegate_task failed: %s", text)
	}
	var out struct {
		ChildSessionID string `json:"childSessionId"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		c.t.Fatalf("delegate_task result is not JSON: %v (%q)", err, text)
	}
	if out.ChildSessionID == "" {
		c.t.Fatalf("delegate_task returned no childSessionId: %q", text)
	}
	return out.ChildSessionID
}

// startSession transitions an already-created session to running so
// it can itself delegate. A delegated child lands in the created
// state, so the lifecycle walk is created -> finalize -> ready ->
// start -> running.
func (c mcpClient) startSession(id string) {
	c.t.Helper()
	for _, step := range []string{"finalize", "start"} {
		code, body := c.rest(http.MethodPost, "/v1/sessions/"+id+"/"+step, nil)
		if code != http.StatusOK {
			c.t.Fatalf("%s session %s: status %d (%v)", step, id, code, body)
		}
	}
}

// spec: 9.2 (hop-by-hop elicitation chain through the platform MCP server)
// diagnosis: a §9.2 elicitation raised by a delegated child did not
// forward hop-by-hop to the human-facing root through the real
// cmd/lenny-gateway binary. The lenny/request_elicitation tool, the
// elicitation-chain dispatcher walking the §8 delegation tree, the
// resolver selection, or the §15.1 respond authorization triple
// diverged from §9.2 when driven through one process.
func TestMCPElicitationChain(t *testing.T) {
	gw := gateway.StartWith(t, "--dev-mode")
	c := mcpClient{t: t, base: gw.BaseURL()}

	// Build a root -> child -> leaf §8 delegation tree. Each ancestor
	// must be running to delegate; the leaf stays in its created state
	// (non-terminal), which is all lenny/request_elicitation requires.
	root := c.runningSession()
	child := c.delegateChild(root, "echo-mid")
	c.startSession(child)
	leaf := c.delegateChild(child, "echo-leaf")

	// The leaf raises an elicitation. The §9.2 dispatcher walks the
	// delegation tree leaf -> child -> root; the root is the
	// human-facing edge, so the elicitation is recorded against the
	// root for resolution.
	const elicitationID = "elic-chain-1"
	done := make(chan map[string]any, 1)
	go func() {
		// spec: §8.5 line 559 (F-8.5.13) — request_elicitation requires
		// both `message` and `schema`. An empty schema object declares a
		// free-form prompt, which is all this chain test needs.
		done <- c.callTool("lenny/request_elicitation",
			`{"sessionId":"`+leaf+`","message":"approve the deploy?","schema":{},"elicitationId":"`+elicitationID+`"}`)
	}()

	// §9.2: the elicitation forwarded up to the root. A REST respond
	// against the root resolves it. The elicitation is recorded after
	// the tool's internal Put, so the respond is retried until the
	// pending elicitation exists (404 until then).
	resolved := false
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		code, _ := c.rest(http.MethodPost,
			"/v1/sessions/"+root+"/elicitations/"+elicitationID+"/respond",
			map[string]any{"response": "approved"})
		if code == http.StatusOK {
			resolved = true
			break
		}
		if code != http.StatusNotFound {
			t.Fatalf("respond at root: unexpected status %d", code)
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !resolved {
		t.Fatal("the elicitation was never recorded against the human-facing root")
	}

	// §9.2: the elicitation was NOT recorded against an intermediate
	// hop — a respond against the raising leaf or the middle child
	// finds no pending elicitation.
	for _, hop := range []struct{ name, id string }{{"leaf", leaf}, {"child", child}} {
		code, _ := c.rest(http.MethodPost,
			"/v1/sessions/"+hop.id+"/elicitations/"+elicitationID+"/respond",
			map[string]any{"response": "x"})
		if code != http.StatusNotFound {
			t.Errorf("elicitation resolvable at intermediate hop %s (status %d); the chain must terminate at the root",
				hop.name, code)
		}
	}

	// The raising leaf's blocked lenny/request_elicitation call
	// unblocks with the human response from the root.
	select {
	case rpc := <-done:
		text, isErr := toolResultText(t, rpc)
		if isErr {
			t.Fatalf("request_elicitation returned an error: %s", text)
		}
		if !strings.Contains(text, "approved") {
			t.Errorf("request_elicitation result = %q, want the human response", text)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request_elicitation did not return after the root resolved it")
	}
}

// spec: 9.2 (url-mode elicitation provenance controls)
// diagnosis: the §9.2 url-mode provenance controls did not behave as
//
//	specified through the real cmd/lenny-gateway binary. An
//	agent-initiated url-mode elicitation must be dropped when
//	the pool does not allowlist url-mode (the gateway default),
//	while a connector-initiated url-mode elicitation is
//	admitted because §9.2 reserves url-mode for gateway-
//	registered connectors.
func TestMCPProvenance(t *testing.T) {
	gw := gateway.StartWith(t, "--dev-mode")
	c := mcpClient{t: t, base: gw.BaseURL()}

	root := c.runningSession()

	// §9.2 control 1: an agent-initiated url-mode elicitation is
	// blocked when the pool does not allowlist agent url-mode at all.
	// The gateway is started with no per-pool url-mode allowlist, so
	// the §9.2 default — block agent-initiated url-mode — applies.
	agentRPC := c.callTool("lenny/request_elicitation",
		`{"sessionId":"`+root+`","message":"sign in",`+
			`"url":"https://accounts.evil.test/oauth","initiatorType":"agent",`+
			`"elicitationId":"elic-agent-url"}`)
	agentText, agentErr := toolResultText(t, agentRPC)
	if !agentErr {
		t.Fatalf("an agent-initiated url-mode elicitation must be dropped, got: %q", agentText)
	}
	if !strings.Contains(agentText, "DOMAIN_NOT_ALLOWLISTED") {
		t.Errorf("agent url-mode drop reason = %q, want DOMAIN_NOT_ALLOWLISTED", agentText)
	}

	// §9.2 control 3: a connector-initiated url-mode elicitation is
	// admitted even against an empty pool allowlist — url-mode is
	// reserved for gateway-registered connectors. It forwards up the
	// chain to the root (a single-session chain here) and blocks for a
	// human response; a REST respond resolves it, proving the
	// connector url-mode elicitation was recorded rather than dropped.
	const connElicID = "elic-connector-url"
	done := make(chan map[string]any, 1)
	go func() {
		done <- c.callTool("lenny/request_elicitation",
			`{"sessionId":"`+root+`","message":"sign in",`+
				`"url":"https://github.com/login/oauth/authorize","initiatorType":"connector",`+
				`"elicitationId":"`+connElicID+`"}`)
	}()

	resolved := false
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		code, _ := c.rest(http.MethodPost,
			"/v1/sessions/"+root+"/elicitations/"+connElicID+"/respond",
			map[string]any{"response": "signed-in"})
		if code == http.StatusOK {
			resolved = true
			break
		}
		if code != http.StatusNotFound {
			t.Fatalf("respond to connector url-mode elicitation: unexpected status %d", code)
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !resolved {
		t.Fatal("a connector-initiated url-mode elicitation was not recorded; §9.2 admits connector url-mode")
	}
	select {
	case rpc := <-done:
		text, isErr := toolResultText(t, rpc)
		if isErr {
			t.Fatalf("connector url-mode request_elicitation returned an error: %s", text)
		}
		if !strings.Contains(text, "signed-in") {
			t.Errorf("connector url-mode result = %q, want the human response", text)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("connector url-mode request_elicitation did not return after resolution")
	}
}
