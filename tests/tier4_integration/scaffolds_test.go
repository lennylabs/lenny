// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test scaffolds for every TESTING.md-named
// integration test whose backing implementation does not yet exist.
// Each test calls t.Skip with a precise diagnosis pointing at the
// spec section and the missing implementation, so:
//
//   - The static tier sees the test file and confirms it compiles.
//   - The integration tier reports a clear "skipped: not implemented"
//     entry, so the gap is visible at every run.
//   - When the implementation lands, the skip is removed and the
//     test starts asserting against the wire contract.
//
// This is the TDD anchor: the test names are stable, the diagnoses
// name the spec section, and the implementer knows exactly what to
// build to flip the skip into a pass.

package tier4_integration_test

import "testing"

// §13.12 / §13.23 / §13.25 — credential lifecycle. Requires the
// Token Service binary wired into the gateway for /v1/credentials
// CRUD + credential pool admin endpoints + AssignCredentials RPC
// down to the runtime. Phase 12a ships pkg/tokenservice; the runtime
// integration lands with the Token Service Postgres + KMS work.
func TestCredentialLifecycle(t *testing.T) {
	t.Skip("not implemented: §4.9 / §13.12 — requires Token Service Postgres-backed credential store, /v1/credentials admin surface, and AssignCredentials/RotateCredentials/RevokeCredentials RPCs from the gateway to the runtime")
}

func TestCredentialRotation(t *testing.T) {
	t.Skip("not implemented: §4.9 Fallback Flow + §4.7 RotateCredentials RPC — requires fault-driven rotation pipeline + lifecycle channel credentials_rotated emission")
}

func TestCredentialRevocation(t *testing.T) {
	t.Skip("not implemented: §4.9 Emergency Credential Revocation — requires POST /v1/admin/credential-pools/{name}/credentials/{credId}/revoke + cross-replica revocation propagation via Redis EventBus")
}

// §13.20 — delegation. Requires the §8 delegation lease, the
// virtual MCP child interface, and the lenny/delegate_task tool
// implementation in the gateway's MCP fabric.
func TestDelegation(t *testing.T) {
	t.Skip("not implemented: §8.2 lenny/delegate_task — requires platform MCP server, virtual child interface, Redis-backed delegation budget Lua scripts, and the delegation-echo Standard-level runtime")
}

// §13.22 — MCP fabric. Requires the §9.2 elicitation chain dispatcher
// and the §9 platform MCP server hosted in the gateway.
func TestMCPElicitationChain(t *testing.T) {
	t.Skip("not implemented: §9.2 elicitation chain — requires hop-by-hop forwarding through the platform MCP server, the maxElicitationWait timer, and the respond_to_elicitation authorization triple")
}

func TestMCPProvenance(t *testing.T) {
	t.Skip("not implemented: §9.2 URL-mode elicitation provenance — requires connector-domain allowlist enforcement and the §11.7 audit emission for url-mode rejections")
}

// §13.9 — admin API + bootstrap.
func TestAdminBootstrap(t *testing.T) {
	t.Skip("not implemented: §10.2 / §17.6 — requires lenny-ctl bootstrap subcommand, lenny-bootstrap Helm Job, and the admin /v1/admin/tenants endpoint")
}

// §13.28 — audit pipeline.
func TestAuditPipeline(t *testing.T) {
	t.Skip("not implemented: §11.7 audit_log table + per-tenant advisory lock + OCSF translator + SIEM forwarder — requires Postgres-backed EventStore")
}

// §13.19 — checkpoint/resume.
func TestCheckpointResume(t *testing.T) {
	t.Skip("not implemented: §4.4 checkpoint pipeline — requires MinIO ArtifactStore, the cooperative quiescence handshake on the lifecycle channel, and the Postgres minimal-state fallback row")
}

// §13.16 — interactive sessions / streaming.
func TestStreamingReconnect(t *testing.T) {
	t.Skip("not implemented: §10.4 stream reconnect + §7.2 SessionEvent ring buffer — requires the gateway's stream proxy subsystem and the streaming-echo runtime")
}

// §13.15 — LLM Proxy.
func TestLLMProxyAnthropic(t *testing.T) {
	t.Skip("not implemented: §4.9 LLM Proxy + native translator — requires the §4.9 proxy-mode dialect translator and an outbound HTTP client against the Anthropic API")
}

// §13.18 — policy engine end-to-end.
func TestPolicyGate(t *testing.T) {
	t.Skip("not implemented: §11 policy interceptor chain — requires AuthEvaluator + QuotaEvaluator + audit emission wired into the gateway admission path")
}

func TestPolicyAudit(t *testing.T) {
	t.Skip("not implemented: §11.7 audit + §11 policy decisions — requires the policy interceptor chain to emit decision rows to the EventStore")
}

// §13.18 — quota enforcement.
func TestQuotaEnforcement(t *testing.T) {
	t.Skip("not implemented: §11.2 quota Redis Lua + per-tenant counters — requires Redis-backed quota store wired into the gateway admission path")
}

func TestQuotaRecovery(t *testing.T) {
	t.Skip("not implemented: §11.2 quota recovery — requires the Redis MAX rule reconciliation and pod-side ReportUsage replay on coordinator handoff")
}

// §13.2 — database migrations.
func TestMigrationUpgrade(t *testing.T) {
	t.Skip("not implemented: §10.5 expand-contract migrations + Phase 3 gate query — Phase 1.5 ships the harness; this test exercises a live gateway against a migrating Postgres")
}

// §13.32 — environments + cross-environment delegation.
func TestEnvironmentResource(t *testing.T) {
	t.Skip("not implemented: §10.6 admin API for /v1/admin/environments — requires environment CRUD endpoints + OIDC group resolver")
}

func TestEnvironmentFiltering(t *testing.T) {
	t.Skip("not implemented: §10.6 transparent-filtering middleware — requires the environment-resolver middleware that joins authorized runtimes across the user's environments")
}

// §13.33 — experiments.
func TestExperimentRouting(t *testing.T) {
	t.Skip("not implemented: §10.7 ExperimentRouter interceptor — requires the PoolScalingController variant-pool minWarm coupling and the admin /v1/admin/experiments surface")
}

// §13.25 — OAuth connectors.
func TestOAuthConnector(t *testing.T) {
	t.Skip("not implemented: §9.3 ConnectorDefinition + OAuth/OIDC flow — requires the gateway's connector OAuth callback handler, PKCE state store, and the connector-credential exchange")
}

// §13.26 — type:mcp runtime support.
func TestMCPRuntimeLifecycle(t *testing.T) {
	t.Skip("not implemented: §15.4 type:mcp runtime kind — requires the MCP-style runtime adapter and the corresponding pool execution mode")
}

func TestMCPRuntimeEndpoints(t *testing.T) {
	t.Skip("not implemented: §15.2 MCP API endpoints + type:mcp runtime — requires the gateway's MCP server hosting the Tasks protocol over WebSockets")
}
