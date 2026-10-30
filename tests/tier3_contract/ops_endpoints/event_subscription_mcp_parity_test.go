// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract test confirming the §15.1 admin-API MCP extension
// contract holds for the §25.5 event-subscription CRUD family: a
// subscription created through one surface (REST or MCP) is visible,
// and reads identically, through the other.
package ops_endpoints_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// mcpToolTextPayload decodes the JSON body of a successful §25.12
// tools/call result's first text content block.
func mcpToolTextPayload(t *testing.T, result map[string]any) map[string]any {
	t.Helper()
	contents, ok := result["content"].([]any)
	if !ok || len(contents) == 0 {
		t.Fatalf("tools/call result has no content: %v", result)
	}
	first, ok := contents[0].(map[string]any)
	if !ok {
		t.Fatalf("tools/call first content is not an object: %v", contents[0])
	}
	text, _ := first["text"].(string)
	var payload map[string]any
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("tools/call content text is not JSON: %v\ntext=%s", err, text)
	}
	return payload
}

// mcpToolCall issues a §25.12 tools/call against srv and returns the
// decoded tools/call result object, failing the test on a non-200
// transport response, a JSON-RPC error, or isError:true.
func mcpToolCall(t *testing.T, srv *opsserver.Server, tool string, args map[string]any) map[string]any {
	t.Helper()
	rec, body := request(t, srv, http.MethodPost, "/mcp/management", nil, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("tools/call %s status = %d, want 200; body=%v", tool, rec.Code, body)
	}
	if rpcErr, ok := body["error"].(map[string]any); ok {
		t.Fatalf("tools/call %s returned a JSON-RPC error: %v", tool, rpcErr)
	}
	result, ok := body["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/call %s has no result: %v", tool, body)
	}
	if result["isError"] == true {
		t.Fatalf("tools/call %s reported isError:true: %v", tool, result)
	}
	return result
}

// TestEventSubscriptionRESTMCPParityContract pins the §15.1 admin-API
// MCP extension contract for the §25.5 event-subscription CRUD family: a
// subscription created through REST is readable, listed, and deletable
// through the generated lenny_event_subscription_* MCP tools, and a
// subscription created through MCP is readable through REST, because
// both surfaces replay onto the same lenny-ops eventsubscription.Service
// (spec: §25.12 "Every MCP tool invocation ... is translated into a REST
// call ... That REST call passes through the standard ... role-based
// authorization check").
//
// spec: §15.1 (Admin API MCP extension contract — "Every admin-API
// endpoint with documented RBAC MUST be exposed as an MCP tool on
// /mcp/management"; the endpoint table lists
// `/v1/admin/event-subscriptions` among the lenny-ops-hosted §25.4+
// endpoint family, not among the endpoints carrying a `null`
// x-lenny-mcp-tool).
// diagnosis: a failure means the generated lenny_event_subscription_*
// tools no longer replay onto the same lenny-ops event-subscription
// store the REST surface uses. A resource created through one surface
// would not be visible, or would read differently, through the other,
// contradicting the §15.1 MCP extension contract for this endpoint
// family.
func TestEventSubscriptionRESTMCPParityContract(t *testing.T) {
	srv := eventStreamServer(t, 0)

	// Create through REST; read the same resource through MCP.
	createRec, created := request(t, srv, http.MethodPost, "/v1/admin/event-subscriptions", nil,
		map[string]any{
			"callbackUrl": "https://hooks.acme.com/lenny",
			"types":       []string{"dev.lenny.alert_fired"},
			"description": "acme incident bridge",
		})
	if createRec.Code != http.StatusCreated {
		t.Fatalf("REST create status = %d, want 201; body=%v", createRec.Code, created)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("REST create response missing subscription id")
	}
	wantFingerprint, _ := created["secretFingerprint"].(string)

	getPayload := mcpToolTextPayload(t, mcpToolCall(t, srv, "lenny_event_subscription_get", map[string]any{"id": id}))
	if getPayload["id"] != id {
		t.Errorf("MCP get id = %v, want %q (the REST-created subscription)", getPayload["id"], id)
	}
	if getPayload["callbackUrl"] != "https://hooks.acme.com/lenny" {
		t.Errorf("MCP get callbackUrl = %v, want the REST-created value", getPayload["callbackUrl"])
	}
	if getPayload["secretFingerprint"] != wantFingerprint {
		t.Errorf("MCP get secretFingerprint = %v, want %q (matching the REST create response)", getPayload["secretFingerprint"], wantFingerprint)
	}
	if _, leaked := getPayload["secret"]; leaked {
		t.Error("MCP get leaked the plaintext secret the REST read view redacts")
	}

	listPayload := mcpToolTextPayload(t, mcpToolCall(t, srv, "lenny_event_subscriptions_list", map[string]any{}))
	subs, _ := listPayload["subscriptions"].([]any)
	var foundInList bool
	for _, raw := range subs {
		if sub, ok := raw.(map[string]any); ok && sub["id"] == id {
			foundInList = true
		}
	}
	if !foundInList {
		t.Errorf("MCP list did not include the REST-created subscription %q: %v", id, subs)
	}

	// Create through MCP; read the same resource through REST.
	createPayload := mcpToolTextPayload(t, mcpToolCall(t, srv, "lenny_event_subscription_create", map[string]any{
		"callbackUrl": "https://hooks.acme.com/mcp-created",
		"types":       []string{"dev.lenny.pool_state_changed"},
		"description": "mcp-created subscription",
	}))
	mcpID, _ := createPayload["id"].(string)
	if mcpID == "" {
		t.Fatal("MCP create response missing subscription id")
	}
	if mcpID == id {
		t.Fatal("MCP create returned the same id as the REST-created subscription")
	}

	restRec, restBody := request(t, srv, http.MethodGet, "/v1/admin/event-subscriptions/"+mcpID, nil, nil)
	if restRec.Code != http.StatusOK {
		t.Fatalf("REST get of the MCP-created subscription status = %d, want 200; body=%v", restRec.Code, restBody)
	}
	if restBody["callbackUrl"] != "https://hooks.acme.com/mcp-created" {
		t.Errorf("REST get callbackUrl = %v, want the MCP-created value", restBody["callbackUrl"])
	}

	// Delete through MCP; confirm REST no longer serves the resource.
	mcpToolCall(t, srv, "lenny_event_subscription_delete", map[string]any{"id": mcpID})
	restRec2, restBody2 := request(t, srv, http.MethodGet, "/v1/admin/event-subscriptions/"+mcpID, nil, nil)
	if restRec2.Code != http.StatusNotFound {
		t.Errorf("REST get after MCP delete status = %d, want 404; body=%v", restRec2.Code, restBody2)
	}
}
