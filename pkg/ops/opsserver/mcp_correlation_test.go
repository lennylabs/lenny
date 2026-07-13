// SPDX-License-Identifier: MIT

package opsserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/pkg/ops/coordination"
	"github.com/lennylabs/lenny/pkg/ops/opsserver"
)

// lockOperationIDFromResult extracts the operationId of the lock a
// tools/call created, reading it out of the MCP text-content envelope that
// carries the underlying REST response body.
func lockOperationIDFromResult(t *testing.T, body map[string]any) string {
	t.Helper()
	result, _ := body["result"].(map[string]any)
	content, _ := result["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("tools/call result carried no content; body=%v", body)
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	var lock struct {
		OperationID string `json:"operationId"`
	}
	if err := json.Unmarshal([]byte(text), &lock); err != nil {
		t.Fatalf("lock content is not JSON: %v; text=%q", err, text)
	}
	return lock.OperationID
}

// A §25.12 tools/call that supplies the operation ID only through MCP
// request metadata (_meta.operationId, absent from the tool input
// arguments) must still tie the created remediation lock to that operation
// ID. This exercises the full adapter path: the metadata operation ID
// reaches the underlying REST call as the X-Lenny-Operation-ID
// correlation value, and the lock handler records it because the request
// body omits operationId. A tool input operationId would flow into the
// REST body directly, so _meta is the case that isolates the header and
// correlation propagation the adapter owns.
//
// diagnosis: the §25.12 MCP adapter dropped _meta.operationId, so an
// operation ID an agent supplied through MCP request metadata never
// reached the underlying REST call and the remediation lock recorded an
// empty operationId, breaking "show every action taken by this
// remediation effort" for metadata-instrumented agents.
//
// spec: §25.12 (Headers and Correlation) — "X-Lenny-Operation-ID from ...
// MCP tool call metadata (_meta.operationId in the tools/call request) →
// same HTTP header on REST calls."
func TestMCPManagementMetaOperationIDPropagatesToLock_spec_25_12(t *testing.T) {
	srv := opsserver.New(opsserver.Options{Locks: coordination.NewMemStore()})
	rec, body := doJSON(t, srv, http.MethodPost, "/mcp/management", nil, map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{
			"name":      "lenny_lock_acquire",
			"arguments": map[string]any{"scope": "pool:default-gvisor", "operation": "scale", "ttlSeconds": float64(300)},
			"_meta":     map[string]any{"operationId": "550e8400-meta"},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", rec.Code, body)
	}
	if got := lockOperationIDFromResult(t, body); got != "550e8400-meta" {
		t.Errorf("lock operationId = %q, want 550e8400-meta (from _meta.operationId on the tools/call request)", got)
	}
}
