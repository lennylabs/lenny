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

// §13.12 / §13.23 / §13.25 — credential lifecycle. The §4.9 / §15.1
// end-user credential lifecycle (TestCredentialLifecycle,
// TestCredentialRotation, TestCredentialRevocation) is converted in
// credential_test.go: the /v1/credentials register / list / rotate /
// revoke / delete surface is exercised against the live gateway. The
// §4.7 runtime-side AssignCredentials / RotateCredentials fan-out and
// the cross-replica revocation propagation need a pod and a Redis
// EventBus the integration harness does not provide.

// §13.20 — delegation. TestDelegation is converted in delegation_test.go:
// the §8.2 delegate_task spawn-and-tree contract and the §8.3
// tracingContext propagation are exercised against the live gateway.

// §13.22 — MCP fabric.
// TestMCPElicitationChain and TestMCPProvenance are converted in
// elicitation_test.go: the §9.2 hop-by-hop elicitation chain through
// the platform MCP server and the url-mode provenance controls are
// built and exercised against the live gateway.

// §13.9 — admin API + bootstrap.
func TestAdminBootstrap(t *testing.T) {
	t.Skip("not implemented: §10.2 / §17.6 — the gateway's POST /v1/admin/bootstrap and /v1/admin/tenants endpoints exist, but this test exercises the lenny-ctl bootstrap subcommand and the lenny-bootstrap Helm Job that drive a from-empty platform bring-up, neither of which is gateway-resident")
}

// §13.28 — audit pipeline. TestAuditPipeline is converted in
// audit_pipeline_test.go: the §11.7 audit pipeline is exercised end to
// end — an audit event flows through the Postgres-backed hash chain,
// the OCSF translator state machine, and the SIEM forwarder, against a
// real Postgres container and the fake SIEM endpoint.

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

// §13.18 — policy engine end-to-end. TestPolicyGate and TestPolicyAudit
// are converted in policy_test.go: the §4.8 QuotaEvaluator is wired
// onto the session-creation admission path and exercised against the
// live gateway subprocess, and a chain REJECT emits the §16.7
// `interceptor.rejected` row to the per-tenant audit hash chain.

// §13.18 — quota enforcement. TestQuotaEnforcement and TestQuotaRecovery
// are converted in quota_test.go: the §11.2 Redis-backed per-tenant
// token counter is wired into the §4.8 admission path, and the §11.2
// Redis MAX-rule reconciliation (quota.ReconcileMax) is exercised end
// to end against the live gateway subprocess.

// §13.2 — database migrations.
func TestMigrationUpgrade(t *testing.T) {
	t.Skip("not implemented: §10.5 expand-contract migrations + Phase 3 gate query — Phase 1.5 ships the harness; this test exercises a live gateway against a migrating Postgres")
}

// §13.32 — environments + cross-environment delegation.
// TestEnvironmentResource and TestEnvironmentFiltering are converted
// in environment_test.go: the §10.6 admin /v1/admin/environments CRUD
// and the transparent-filtering environment-resolver middleware are
// built and exercised against the live gateway.

// §13.33 — experiments.
func TestExperimentRouting(t *testing.T) {
	t.Skip("not implemented: §10.7 ExperimentRouter interceptor — requires the PoolScalingController variant-pool minWarm coupling and the admin /v1/admin/experiments surface")
}

// §13.25 — OAuth connectors.
// TestOAuthConnector is converted in connector_oauth_test.go: the §9.3
// connector OAuth 2.1 authorize→callback flow, the PKCE state store,
// and the connector-credential exchange are built and exercised
// against the live gateway with a fake provider token endpoint.

// §13.26 — type:mcp runtime support. TestMCPRuntimeLifecycle is
// converted in mcp_runtime_lifecycle_test.go: the type: mcp runtime-
// side adapter path is built and exercised end to end against the
// reference type: mcp runtime (cmd/runtimes/mcp-reference).
func TestMCPRuntimeEndpoints(t *testing.T) {
	t.Skip("not implemented: §15.2 / §15.1 gateway-side type:mcp endpoints — the type:mcp runtime-side adapter path and the reference runtime ship in Phase 12b (see mcp_runtime_lifecycle_test.go); the gateway-side dedicated MCP endpoints at /mcp/runtimes/{name} are a Phase 5 deliverable and are not yet built")
}
