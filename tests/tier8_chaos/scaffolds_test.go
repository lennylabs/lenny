// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos test scaffolds. Each test corresponds to a TESTING.md-
// named chaos scenario that requires the production stack plus a
// chaos-injection harness (Chaos Mesh, kube-monkey, toxiproxy, or
// equivalent). Today each calls t.Skip with a diagnosis pointing at
// the spec section and the missing infrastructure.
//
// Naming follows TESTING.md §12.8 and the runbook map. Every chaos
// test follows the same shape: bring system to known-good state,
// inject failure, assert documented behavior, resolve failure, assert
// recovery, assert no data loss (or the documented bounded loss).

package tier8_chaos_test

import "testing"

// --- Store failures ---

func TestPostgresFailover(t *testing.T) {
	t.Skip("not implemented: §12.8 store failure / Postgres failover — requires HA Postgres on Kind + the documented failover runbook")
}

func TestPostgresUnavailable(t *testing.T) {
	t.Skip("not implemented: §12.8 store failure / Postgres unavailable — requires toxiproxy-fronted Postgres + the gateway's degraded-mode behavior")
}

func TestRedisSentinelFailover(t *testing.T) {
	t.Skip("not implemented: §12.8 store failure / Redis Sentinel failover — requires Redis Sentinel topology on Kind + the gateway's reconnect behavior")
}

func TestRedisClusterDegraded(t *testing.T) {
	t.Skip("not implemented: §12.8 store failure / Redis cluster degraded — requires Redis Cluster on Kind + a partial-node-down injector")
}

func TestMinIOUnavailable(t *testing.T) {
	t.Skip("not implemented: §12.8 store failure / MinIO unavailable — requires the MinIO ArtifactStore + the Postgres minimal-state fallback contract")
}

func TestMinIOReplicationLag(t *testing.T) {
	t.Skip("not implemented: §12.8 store failure / MinIO replication lag — requires MinIO with cross-zone replication + a latency-injection probe")
}

func TestKMSUnavailable(t *testing.T) {
	t.Skip("not implemented: §12.8 store failure / KMS unavailable — requires the KMS adapter + a fail-closed assertion for T3/T4 writes")
}

func TestKMSKeyProbeStale(t *testing.T) {
	t.Skip("not implemented: §12.8 store failure / KMS probe stale — requires the t4KmsProbeInterval probe + the T4KmsKeyUnusable alert wiring")
}

func TestDualStoreUnavailable(t *testing.T) {
	t.Skip("not implemented: §12.8 store failure / dual-store unavailable — requires Postgres + Redis simultaneous outage harness + the gateway's full degraded-mode behavior")
}

func TestPgBouncerSaturation(t *testing.T) {
	t.Skip("not implemented: §12.8 store failure / PgBouncer saturation — requires PgBouncer in front of Postgres + a connection-exhaustion injector")
}

// --- Component failures ---

func TestGatewayReplicaFailure(t *testing.T) {
	t.Skip("not implemented: §12.8 component failure / gateway replica — requires multi-replica gateway deployment + the documented load-balancer behavior")
}

func TestControllerLeaderElectionDisruption(t *testing.T) {
	t.Skip("not implemented: §12.8 component failure / controller leader election — requires the WarmPoolController + PoolScalingController + leader-election disruption injector")
}

func TestAdmissionWebhookOutage(t *testing.T) {
	t.Skip("not implemented: §12.8 component failure / admission webhook outage — requires the documented failurePolicy=Fail behavior under webhook unavailability")
}

func TestCertManagerOutage(t *testing.T) {
	t.Skip("not implemented: §12.8 component failure / cert-manager outage — requires the mTLS PKI + the cert-renewal failure injector")
}

func TestDNSOutage(t *testing.T) {
	t.Skip("not implemented: §12.8 component failure / DNS outage — requires the cluster DNS + an outage injector + the gateway's cached-resolution fallback")
}

func TestTokenServiceOutage(t *testing.T) {
	t.Skip("not implemented: §12.8 component failure / Token Service outage — requires the Token Service deployment + the gateway's degraded-mode behavior")
}

func TestEphemeralContainerCredGuardOutage(t *testing.T) {
	t.Skip("not implemented: §12.8 component failure / ephemeral-container-cred-guard outage — requires the guard deployment + the documented fail-closed behavior")
}

// --- Lifecycle failures ---

func TestPodKillDuringActiveSession(t *testing.T) {
	t.Skip("not implemented: §12.8 lifecycle failure / pod kill during session — requires the §4.4 checkpoint pipeline + a forced-pod-delete injector")
}

func TestNodeDrainDuringMinIOOutage(t *testing.T) {
	t.Skip("not implemented: §12.8 lifecycle failure / node drain during MinIO outage — requires the drain-readiness webhook + MinIO outage injector + node drain")
}

func TestSandboxFinalizerHang(t *testing.T) {
	t.Skip("not implemented: §12.8 lifecycle failure / sandbox finalizer hang — requires the SandboxClaim CRD + a stuck-finalizer injector")
}

func TestRuntimeUpgradeStuck(t *testing.T) {
	t.Skip("not implemented: §12.8 lifecycle failure / runtime upgrade stuck — requires the runtime upgrade pipeline + a stuck-state injector")
}

func TestPoolUpgradeRollbackDuringExpanding(t *testing.T) {
	t.Skip("not implemented: §12.8 lifecycle failure / pool upgrade rollback during expanding — requires the pool upgrade state machine + a mid-state rollback trigger")
}

// --- Network failures ---

func TestGatewayToPodPartition(t *testing.T) {
	t.Skip("not implemented: §12.8 network failure / gateway-to-pod partition — requires the gateway + pod network + a toxiproxy/chaos-mesh partition injector")
}

func TestAgentToLLMProviderPartition(t *testing.T) {
	t.Skip("not implemented: §12.8 network failure / agent-to-LLM partition — requires the LLM Proxy + an external-provider partition injector")
}

func TestNetworkPolicyDrift(t *testing.T) {
	t.Skip("not implemented: §12.8 network failure / NetworkPolicy drift — requires the rendered NetworkPolicies + a drift-injection harness")
}

func TestCrossZonePartition(t *testing.T) {
	t.Skip("not implemented: §12.8 network failure / cross-zone partition — requires a multi-AZ cluster + an inter-zone partition injector")
}

// --- Credential failures ---

func TestEmergencyRevocationDuringActiveSession(t *testing.T) {
	t.Skip("not implemented: §12.8 credential failure / emergency revocation — requires the Token Service revocation pipeline + Redis pub/sub propagation + an active streaming session")
}

func TestRotationFailure(t *testing.T) {
	t.Skip("not implemented: §12.8 credential failure / rotation failure — requires the RotateCredentials RPC + a fault-injection harness")
}

func TestDenyListPropagationUnderRedisOutage(t *testing.T) {
	t.Skip("not implemented: §12.8 credential failure / deny-list under Redis outage — requires the deny-list propagation path + a Redis-outage injector")
}

func TestCredentialPoolExhaustion(t *testing.T) {
	t.Skip("not implemented: §12.8 credential failure / pool exhaustion — requires the credential pool with finite leases + an over-allocation harness")
}

// --- Delegation failures ---

func TestChildCrashMidTask(t *testing.T) {
	t.Skip("not implemented: §12.8 delegation failure / child crash — requires the delegation pipeline + a forced-child-kill injector + the parent's reconnect behavior")
}

func TestParentCrashDuringAwaitChildren(t *testing.T) {
	t.Skip("not implemented: §12.8 delegation failure / parent crash during await_children — requires the delegation pipeline + the §8 lease + a parent-kill injector")
}

func TestDelegationBudgetExhaustion(t *testing.T) {
	t.Skip("not implemented: §12.8 delegation failure / budget exhaustion — requires the §8 budget primitives + a budget-burn harness")
}

func TestLeaseExtensionCoolOffPersistence(t *testing.T) {
	t.Skip("not implemented: §12.8 delegation failure / cool-off persistence — requires the lease-extension state machine + a gateway-restart injector")
}

// --- Compliance failures ---

func TestErasureJobFailureMidSequence(t *testing.T) {
	t.Skip("not implemented: §12.8 compliance failure / erasure job mid-sequence — requires the 19-step erasure orchestrator + a fault injector at step N")
}

func TestLegalHoldOverrideFlow(t *testing.T) {
	t.Skip("not implemented: §12.8 compliance failure / legal-hold override — requires the legal_hold_escrow_kek path + the region-scoped escrow bucket")
}

func TestT3T4SLABreach(t *testing.T) {
	t.Skip("not implemented: §12.8 compliance failure / T3/T4 SLA breach — requires the erasure orchestrator + clock-injection to simulate SLA breach")
}

func TestAuditChainGapDetection(t *testing.T) {
	t.Skip("not implemented: §12.8 compliance failure / audit chain gap — requires the §11.7 hash-chain + a direct-DB-write injector to create the gap")
}

// --- Concurrency ---

func TestSandboxClaimRaceUnder100Goroutines(t *testing.T) {
	t.Skip("not implemented: §12.8 concurrency / SandboxClaim race — requires the CAS path + a 100+ goroutine claim harness")
}

func TestDoubleClaimVerification(t *testing.T) {
	t.Skip("not implemented: §12.8 concurrency / double-claim (ADR-007) — requires the lenny-sandboxclaim-guard webhook + the documented double-claim attempt")
}

func TestElicitationDeadlockDetection(t *testing.T) {
	t.Skip("not implemented: §12.8 concurrency / elicitation deadlock — requires the §9.2 elicitation chain + a deadlock-construction harness")
}

func TestDelegationDepthDeadlockDetection(t *testing.T) {
	t.Skip("not implemented: §12.8 concurrency / delegation depth deadlock — requires the §8 cycle detector + a maximum-depth construction harness")
}

// --- Time ---

func TestGatewayClockDrift(t *testing.T) {
	t.Skip("not implemented: §12.8 time / clock drift — requires the gateway + a clock-injection harness + the documented drift-tolerance behavior")
}

func TestCertificateExpiryAdvance(t *testing.T) {
	t.Skip("not implemented: §12.8 time / certificate expiry — requires the mTLS PKI + clock advance + cert-manager renewal behavior")
}

// --- Configuration ---

func TestPoolConfigDrift(t *testing.T) {
	t.Skip("not implemented: §12.8 configuration / pool config drift — requires the pool admission validator + a drift-injection harness")
}

func TestNetworkPolicyConfigDrift(t *testing.T) {
	t.Skip("not implemented: §12.8 configuration / NetworkPolicy drift — requires the rendered NetworkPolicies + a drift-injection harness")
}

func TestSchemaMigrationDirtyFlag(t *testing.T) {
	t.Skip("not implemented: §12.8 configuration / migration dirty flag — requires the migration framework + a concurrent-replica-apply injector")
}

func TestCRDUpgradeImmutableFieldChange(t *testing.T) {
	t.Skip("not implemented: §12.8 configuration / CRD upgrade with immutable field changes — requires the CRD conversion webhook + the immutable-field rejection path")
}
