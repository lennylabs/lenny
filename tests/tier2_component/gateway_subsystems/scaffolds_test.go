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
// streaming-echo, against real Postgres + Redis + MinIO. The end-to-end
// flow is already covered at tier 4 (gateway_postgres_e2e_test.go,
// gateway_redis_e2e_test.go drive cmd/lenny-gateway against real
// containers), and pkg/gateway/sessionserver does not consume a Redis
// LeaseStore today, so the scaffold's "real Redis LeaseStore" half has
// no production consumer.
//
// spec: 12.2.3
// diagnosis: sessionserver has no LeaseStore wiring; tier 4 already
// drives the create→attach→prompt→complete loop against real
// Postgres+Redis+MinIO containers.
func TestSessionOrchestrator(t *testing.T) {
	t.Skip("blocked: §12.2.3 Session Orchestrator component-tier wiring — pkg/gateway/sessionserver does not consume a LeaseStore today, and the create→attach→prompt→complete loop against real Postgres+Redis+MinIO is already covered at tier 4 (gateway_postgres_e2e_test.go, gateway_redis_e2e_test.go)")
}

// TestFileFabric — upload/download through the gateway against real
// MinIO. Multipart, archive, gitClone modes.
//
// spec: 12.2.3
// diagnosis: pkg/upload archive validators are pure functions and
// pkg/gateway/sessionserver/upload.go implements the single-blob
// upload handler, but the §15.1 POST /v1/sessions/{id}/upload-archive
// HTTP handler is not yet wired into sessionserver (the package
// comment marks it as a later commit) and the §7.4 gitClone executor
// path is not built into the gateway. A component test for the
// "multipart, archive, gitClone" trio cannot exercise behaviors that
// have no HTTP entry point.
func TestFileFabric(t *testing.T) {
	t.Skip("blocked: §12.2.3 File Fabric — the §15.1 upload-archive HTTP handler is unbuilt and the §7.4 gitClone executor path is not wired into the gateway; only the single-blob upload handler exists today")
}

// TestMCPFabricPlatformTools — platform MCP tools (lenny/output,
// lenny/request_elicitation, lenny/memory_write, lenny/memory_query,
// lenny/send_message, lenny/request_input) against real stores.
// pkg/gateway/mcptools implements all six tools and the unit suites
// cover dispatch against memstore; the backing Postgres stores are
// covered by the tier-2 stores contract tests, so a duplicated
// MCP-on-Postgres component test adds no production-code coverage.
//
// spec: 12.2.3
// diagnosis: the pkg/gateway/mcptools unit suites cover dispatch and
// the Postgres-backed stores are covered by the stores contract
// tests; a tier-2 MCP-on-Postgres wiring adds no new code path.
func TestMCPFabricPlatformTools(t *testing.T) {
	t.Skip("blocked: §12.2.3 MCP Fabric platform-tools wiring — the per-tool handler unit suites in pkg/gateway/mcptools cover the dispatch logic, and the Postgres-backed store contract tests in tests/tier2_component/stores/{memorystore,sessionstore,interactionstore}_test.go cover the backing stores; a duplicated MCP-on-Postgres tier-2 test adds no production-code coverage")
}

// TestAdminPlane — the full admin REST surface against real stores
// and a real OIDC stub. Includes role ceiling enforcement and
// idempotency-key handling.
//
// spec: 12.2.3
// diagnosis: pkg/gateway/admin has handlers for every documented
// /v1/admin/* surface, but the OIDC verifier wiring to a stub IdP and
// the role-ceiling middleware compose are not assembled as a
// component-tier harness. Each handler is unit-tested with its store
// dependencies; a tier-2 component test of the assembled admin plane
// needs an OIDC stub and a role-ceiling middleware fixture that are
// not in tests/testinfra today.
func TestAdminPlane(t *testing.T) {
	t.Skip("blocked: §12.2.3 Admin Plane component harness — pkg/gateway/admin handlers are unit-covered, but no tests/testinfra harness assembles them with the OIDC verifier wired to a stub IdP and the §10.2 role-ceiling middleware as a single component fixture")
}

// TestLLMProxy — lease-token validation, native translator for
// anthropic_direct, request/response/SSE translation, deny-list
// enforcement, per-subsystem isolation, circuit-breaker behavior.
// Validated against the mock LLM provider. The proxy, four
// translators, circuit breaker, and lease-token verifier ship in
// pkg/gateway/llmproxy and are unit-covered there; the missing piece
// for a component-tier wiring is the mock LLM provider recorder,
// which is not in tests/testinfra.
//
// spec: 12.2.3
// diagnosis: the mock LLM provider recorder is not in
// tests/testinfra, and the Postgres-backed credleasestore wiring
// duplicates the existing credleasestore_test.go contract suite.
func TestLLMProxy(t *testing.T) {
	t.Skip("blocked: §12.2.3 LLM Proxy component harness — the mock LLM provider recorder is not in tests/testinfra and the Postgres-backed credleasestore wiring duplicates tests/tier2_component/stores/credleasestore_test.go; the per-translator and per-handler unit suites in pkg/gateway/llmproxy cover the documented behaviors")
}
