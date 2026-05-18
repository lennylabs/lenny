// SPDX-License-Identifier: MIT

//go:build contract

// Tier-3 contract scaffolds for §12.3.1 REST ↔ MCP consistency.
// The single largest contract suite: for every overlapping operation,
// the harness sends the same logical request through REST and MCP and
// asserts semantic equivalence.

package rest_mcp_consistency_test

import "testing"

// TestRESTMCPSessionLifecycle — session create/get/list/delete,
// message send/list. Identical payloads modulo transport envelope.
func TestRESTMCPSessionLifecycle(t *testing.T) {
	t.Skip("not implemented: §12.3.1 REST↔MCP consistency — requires the platform MCP server + the MCP session handlers wired to the same sessionserver internals as REST")
}

// TestRESTMCPWorkspaceUpload — workspace upload (multipart, archive,
// gitClone). Identical validation errors and category flags.
func TestRESTMCPWorkspaceUpload(t *testing.T) {
	t.Skip("not implemented: §12.3.1 REST↔MCP workspace upload — requires the MCP upload tool surface and the shared upload validators across REST and MCP")
}

// TestRESTMCPTasks — task create, list, cancel, get-task-tree.
// Identical state transition sequences.
func TestRESTMCPTasks(t *testing.T) {
	t.Skip("not implemented: §12.3.1 REST↔MCP Tasks — requires §15.2 MCP API endpoints over WebSockets and the shared task store")
}

// TestRESTMCPElicitation — elicitation request, respond, dismiss.
// Identical denial behavior on authz rejections.
func TestRESTMCPElicitation(t *testing.T) {
	t.Skip("not implemented: §12.3.1 REST↔MCP elicitation — the §9.2 chain and the REST respond/dismiss endpoints exist, and the MCP adapter exposes lenny/request_elicitation, but the MCP adapter registers no respond_to_elicitation / dismiss_elicitation tool, so there is no MCP resolution surface to compare against the REST endpoints for the consistency contract")
}

// TestRESTMCPMemory — memory write, query, delete. Identical
// pagination semantics.
func TestRESTMCPMemory(t *testing.T) {
	t.Skip("not implemented: §12.3.1 REST↔MCP memory — requires the MemoryStore implementation and the lenny/memory_* tool handlers in the platform MCP server")
}

// TestRESTMCPDelegation — delegate_task, await_children, cancel_child,
// discover_agents. Identical budget carving and scope narrowing.
func TestRESTMCPDelegation(t *testing.T) {
	t.Skip("not implemented: §12.3.1 REST↔MCP delegation — requires §8.2 lenny/delegate_task + the delegation REST surface + shared budget/cycle primitives")
}

// TestRESTMCPWebhookSubscription — webhook subscription CRUD.
// Identical empty-result shapes.
func TestRESTMCPWebhookSubscription(t *testing.T) {
	t.Skip("not implemented: §12.3.1 REST↔MCP webhook subscriptions — requires the webhook subscription store and the equivalent MCP tools")
}

// TestRESTMCPAdmin — admin runtime/pool/connector/tenant/credential-
// pool CRUD plus audit query. Identical pagination cursor semantics.
func TestRESTMCPAdmin(t *testing.T) {
	t.Skip("not implemented: §12.3.1 REST↔MCP admin — requires the §13.9 admin REST surface and the corresponding admin MCP tools")
}

// TestRESTMCPRetryableFlags — every operation returns identical
// `retryable` and `category` flags across surfaces.
func TestRESTMCPRetryableFlags(t *testing.T) {
	t.Skip("not implemented: §12.3.1 REST↔MCP error envelope parity — requires the shared error-classifier package wired into both transports")
}
