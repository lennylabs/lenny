// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §8.5 lenny/await_children contract end
// to end through the real cmd/lenny-gateway binary. A parent delegates
// two children via lenny/delegate_task and then exercises both
// documented await modes: mode="any" returns as soon as the first
// child settles while leaving the still-running sibling untouched, and
// mode="all" blocks until every awaited child has reached a terminal
// state and returns every child's TaskResult.
//
// The shared mcpClient, runningSession, and delegateChild helpers live
// in elicitation_test.go (same package).

package tier4_integration_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// awaitTaskResult is the subset of the §8.8 TaskResult schema this
// test asserts on: the child's taskId and its settled state.
type awaitTaskResult struct {
	TaskID string `json:"taskId"`
	State  string `json:"state"`
}

// terminateSession transitions a session to completed via the §15.1
// /terminate endpoint, which accepts any non-terminal state (including
// the `running` state a freshly delegated child starts in, since it is
// materialized synchronously within lenny/delegate_task).
func (c mcpClient) terminateSession(id string) {
	c.t.Helper()
	code, body := c.rest(http.MethodPost, "/v1/sessions/"+id+"/terminate", nil)
	if code != http.StatusOK {
		c.t.Fatalf("terminate session %s: status %d (%v)", id, code, body)
	}
}

// awaitChildren calls the lenny/await_children platform MCP tool and
// decodes its TaskResult list.
func (c mcpClient) awaitChildren(parentID string, childIDs []string, mode string) []awaitTaskResult {
	c.t.Helper()
	argBytes, err := json.Marshal(struct {
		SessionID string   `json:"sessionId"`
		ChildIDs  []string `json:"childIds"`
		Mode      string   `json:"mode"`
	}{SessionID: parentID, ChildIDs: childIDs, Mode: mode})
	if err != nil {
		c.t.Fatalf("marshal await_children arguments: %v", err)
	}
	rpc := c.callTool("lenny/await_children", string(argBytes))
	text, isErr := toolResultText(c.t, rpc)
	if isErr {
		c.t.Fatalf("lenny/await_children failed: %s", text)
	}
	var out struct {
		Results []awaitTaskResult `json:"results"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		c.t.Fatalf("await_children result is not JSON: %v (%q)", err, text)
	}
	return out.Results
}

// spec: 8.5 (lenny/await_children modes and result collection)
// diagnosis: a parent delegating to multiple children and awaiting
// them through the real cmd/lenny-gateway binary did not return the
// documented TaskResult set for the "any" and "all" modes — the
// lenny/await_children tool, the child-state poll loop, or the
// §8.8 TaskResult projection diverged from §8.5 when driven through
// one process.
func TestDelegationAwaitChildren(t *testing.T) {
	gw := gateway.StartWith(t, "--dev-mode")
	c := mcpClient{t: t, base: gw.BaseURL()}

	t.Run("mode_any_returns_first_settled_leaves_sibling_running", func(t *testing.T) {
		parent := c.runningSession()
		childA := c.delegateChild(parent, "echo-any-a")
		childB := c.delegateChild(parent, "echo-any-b")

		c.terminateSession(childA)

		// spec: §8.5 line 530 / §8.8 lines 947-950 — "any" returns as
		// soon as any child reaches a terminal state, returning the
		// first TaskResult.
		results := c.awaitChildren(parent, []string{childA, childB}, "any")
		if len(results) != 1 {
			t.Fatalf("await_children mode=any returned %d results, want 1: %+v", len(results), results)
		}
		if results[0].TaskID != childA {
			t.Errorf("await_children mode=any result taskId = %q, want the settled child %q", results[0].TaskID, childA)
		}
		if results[0].State != "completed" {
			t.Errorf("await_children mode=any result state = %q, want completed", results[0].State)
		}

		// spec: §8.8 lines 947-950 — "Remaining children continue
		// running — they are not auto-cancelled."
		code, body := c.rest(http.MethodGet, "/v1/sessions/"+childB, nil)
		if code != http.StatusOK {
			t.Fatalf("GET child B: status %d (%v)", code, body)
		}
		if got, _ := body["state"].(string); got != "running" {
			t.Errorf("child B state after mode=any await = %q, want it left untouched (running); "+
				"a delegated child is materialized to running within delegate_task, and "+
				"mode=any must not auto-cancel the still-running sibling", got)
		}
	})

	t.Run("mode_all_collects_every_child_result", func(t *testing.T) {
		parent := c.runningSession()
		childA := c.delegateChild(parent, "echo-all-a")
		childB := c.delegateChild(parent, "echo-all-b")

		c.terminateSession(childA)
		c.terminateSession(childB)

		// spec: §8.5 line 530 / §8.8 lines 947-950 — "all" waits until
		// all children reach a terminal state and returns the list of
		// every child's TaskResult.
		results := c.awaitChildren(parent, []string{childA, childB}, "all")
		if len(results) != 2 {
			t.Fatalf("await_children mode=all returned %d results, want 2: %+v", len(results), results)
		}
		seen := map[string]string{}
		for _, r := range results {
			seen[r.TaskID] = r.State
		}
		for _, id := range []string{childA, childB} {
			state, ok := seen[id]
			if !ok {
				t.Errorf("await_children mode=all result set %+v is missing child %s", results, id)
				continue
			}
			if state != "completed" {
				t.Errorf("await_children mode=all result for %s state = %q, want completed", id, state)
			}
		}
	})
}
