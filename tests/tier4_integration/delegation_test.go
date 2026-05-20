// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §8.2 delegation contract end-to-end
// through the real cmd/lenny-gateway binary. It spawns a child session
// through the platform MCP server's lenny/delegate_task tool and
// asserts the parent's GET /v1/sessions/{id}/tree records the new
// child node with the correct lineage and node count.
//
// The shared mcpClient and toolResultText helpers live in
// elicitation_test.go (same package).

package tier4_integration_test

import (
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// spec: 8.2 (recursive delegation: child session creation and task tree)
// diagnosis: a delegate_task call did not produce a child session that
// the parent's task tree records. The lenny/delegate_task tool, the
// child-session row insert, the §7.1 ParentSessionID lineage write, or
// the GET /v1/sessions/{id}/tree walker diverged from §8.2 when driven
// through one process.
func TestDelegation(t *testing.T) {
	gw := gateway.StartWith(t, "--dev-mode")

	t.Run("spawn_child_appears_in_tree", func(t *testing.T) {
		c := mcpClient{t: t, base: gw.BaseURL()}
		parent := c.runningSession()
		child := c.delegateChild(parent, "echo-child")

		code, body := c.rest(http.MethodGet, "/v1/sessions/"+parent+"/tree", nil)
		if code != http.StatusOK {
			t.Fatalf("GET /v1/sessions/%s/tree: status %d (%v)", parent, code, body)
		}
		root, _ := body["root"].(map[string]any)
		if root == nil {
			t.Fatalf("tree response carried no root: %v", body)
		}
		if got, _ := root["sessionId"].(string); got != parent {
			t.Errorf("tree root sessionId = %q, want %q", got, parent)
		}
		children, _ := root["children"].([]any)
		if len(children) != 1 {
			t.Fatalf("tree root children = %d, want 1: %v", len(children), children)
		}
		c0, _ := children[0].(map[string]any)
		if got, _ := c0["sessionId"].(string); got != child {
			t.Errorf("tree child sessionId = %q, want %q", got, child)
		}
		nc, _ := body["nodeCount"].(float64)
		if int(nc) != 2 {
			t.Errorf("nodeCount = %d, want 2", int(nc))
		}
		if got, _ := c0["state"].(string); got == "" {
			t.Errorf("child state field is empty: %v", c0)
		}
		if got, _ := c0["runtimeRef"].(string); got != "echo-child" {
			t.Errorf("child runtimeRef = %q, want %q", got, "echo-child")
		}
	})
}
