// SPDX-License-Identifier: MIT

//go:build component

// Tier-2 component scaffolds for §12.2.3 Gateway internal subsystems.
// The gateway is internally partitioned into Session Orchestrator,
// File Fabric, MCP Fabric, Admin Plane, and LLM Proxy. Each
// subsystem has a component-tier suite that wires it to real stores
// and mocked peers.

package gateway_subsystems_test

import "testing"

// TestSessionOrchestrator — create → attach → prompt → complete with
// streaming-echo, against real Postgres + Redis + MinIO.
func TestSessionOrchestrator(t *testing.T) {
	t.Skip("not implemented: §12.2.3 Session Orchestrator — requires streaming-echo runtime + Postgres SessionStore + Redis LeaseStore wired through the gateway")
}

// TestFileFabric — upload/download through the gateway against real
// MinIO. Multipart, archive, gitClone modes.
func TestFileFabric(t *testing.T) {
	t.Skip("not implemented: §12.2.3 File Fabric — pkg/upload archive validators ship; this suite requires the gateway upload handler + MinIO + the gitClone executor")
}

// TestMCPFabricPlatformTools — platform MCP tools (lenny/output,
// lenny/request_elicitation, lenny/memory_write, lenny/memory_query,
// lenny/send_message, lenny/request_input) against real stores.
func TestMCPFabricPlatformTools(t *testing.T) {
	t.Skip("not implemented: §12.2.3 MCP Fabric — requires the platform MCP server + the tool handler set + Postgres-backed memory store")
}

// TestAdminPlane — the full admin REST surface against real stores
// and a real OIDC stub. Includes role ceiling enforcement and
// idempotency-key handling.
func TestAdminPlane(t *testing.T) {
	t.Skip("not implemented: §12.2.3 Admin Plane — requires the /v1/admin/* handler set + OIDC verifier wired to a stub IdP + role-ceiling middleware")
}

// TestLLMProxy — lease-token validation, native translator for
// anthropic_direct, request/response/SSE translation, deny-list
// enforcement, per-subsystem isolation, circuit-breaker behavior.
// Validated against the mock LLM provider.
func TestLLMProxy(t *testing.T) {
	t.Skip("not implemented: §12.2.3 LLM Proxy — requires pkg/llmproxy with the native anthropic_direct translator + lease-token verifier + the mock LLM provider")
}
