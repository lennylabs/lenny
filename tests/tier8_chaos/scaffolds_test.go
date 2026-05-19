// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos test scaffolds. Each test in this file corresponds to a
// TESTING.md-named chaos scenario that cannot be genuinely exercised
// against the e2e install on the Kind cluster: the scenario needs a
// live gateway session driven onto the warm agent-pod workload, a true
// HA / multi-AZ store topology, a network-partition or clock-injection
// harness, or a KMS adapter — none of which the e2e install provides.
// The install does run a two-pool warm agent-pod workload, so the
// scenarios are no longer blocked on the absence of agent pods; they
// are blocked on a live session running on those pods. Each test calls
// t.Skip with a diagnosis naming the spec section and the precise
// missing infrastructure.
//
// The chaos scenarios whose subject is the live control plane, the
// in-cluster data stores, the CRDs, the NetworkPolicies, the migration
// framework, the admission webhooks, or cert-manager are implemented
// against the live cluster. See:
//
//   - leader_election_test.go   — controller leader-election failover
//   - pod_disruption_test.go    — gateway / webhook pod disruption
//   - store_failure_test.go     — Postgres / Redis / MinIO / dual-store
//   - component_failure_test.go — Token Service / cred-guard / cert-manager
//   - config_drift_test.go      — pool-config / NetworkPolicy / migration drift
//   - concurrency_test.go       — double-claim / claim race / finalizer hang
//   - audit_chain_test.go       — §11.7 audit hash-chain gap detection
//
// Naming follows TESTING.md §12.8 and the runbook map. Every chaos
// test follows the same form: bring the system to a known-good state,
// inject a failure, assert the documented behavior, resolve the
// failure, assert recovery, and assert no data loss (or the documented
// bounded loss).

package tier8_chaos_test

import "testing"

// --- Store failures ---
//
// TestPostgresUnavailable, TestRedisClusterDegraded, TestMinIOUnavailable,
// and TestDualStoreUnavailable are implemented against the live
// in-cluster data stores in store_failure_test.go.

func TestPostgresFailover(t *testing.T) {
	t.Skip("not implemented: §12.8 store failure / Postgres failover — the e2e cluster runs a single-replica lenny-postgres Deployment; a failover test needs an HA Postgres topology (primary + standby with automatic promotion), which is not deployed")
}

func TestRedisSentinelFailover(t *testing.T) {
	t.Skip("not implemented: §12.8 store failure / Redis Sentinel failover — the e2e cluster runs a single-replica lenny-redis Deployment with no Sentinel; a Sentinel-failover test needs a Redis Sentinel topology, which is not deployed")
}

func TestMinIOReplicationLag(t *testing.T) {
	t.Skip("not implemented: §12.8 store failure / MinIO replication lag — the e2e cluster runs a single-replica lenny-minio Deployment; a replication-lag test needs MinIO with cross-zone replication and a latency-injection probe, neither of which is deployed")
}

func TestKMSUnavailable(t *testing.T) {
	t.Skip("not implemented: §12.8 store failure / KMS unavailable — the dev-mode gateway uses a local HMAC key and no KMS adapter is wired; a KMS-outage test needs a KMS adapter plus a fail-closed assertion for T3/T4 writes")
}

func TestKMSKeyProbeStale(t *testing.T) {
	t.Skip("not implemented: §12.8 store failure / KMS probe stale — needs the t4KmsProbeInterval KMS key probe and the T4KmsKeyUnusable alert wiring; no KMS adapter is deployed on the dev-mode cluster")
}

func TestPgBouncerSaturation(t *testing.T) {
	t.Skip("not implemented: §12.8 store failure / PgBouncer saturation — the e2e cluster connects the gateway directly to lenny-postgres with no PgBouncer; a connection-exhaustion test needs PgBouncer in front of Postgres, which is not deployed")
}

// --- Component failures ---
//
// TestGatewayReplicaFailure and TestAdmissionWebhookOutage are
// implemented against the live control plane in pod_disruption_test.go.
// TestControllerLeaderElectionDisruption is in leader_election_test.go.
// TestCertManagerOutage, TestTokenServiceOutage, and
// TestEphemeralContainerCredGuardOutage are implemented in
// component_failure_test.go.

func TestDNSOutage(t *testing.T) {
	t.Skip("not implemented: §12.8 component failure / DNS outage — the e2e cluster has no dedicated lenny CoreDNS; only the cluster-wide kube-system/coredns exists, and scaling it to zero is a cluster-wide outage that would break every other test sharing the cluster (cert-manager, the webhooks, the harness). A safe DNS-outage test needs a dedicated, isolatable CoreDNS, which is not deployed")
}

// --- Lifecycle failures ---
//
// TestSandboxFinalizerHang is implemented against the live SandboxClaim
// CRD in concurrency_test.go.

func TestPodKillDuringActiveSession(t *testing.T) {
	t.Skip("not implemented: §12.8 lifecycle failure / pod kill during session — the e2e overlay runs a warm agent-pod workload but does not drive a live gateway session onto it; the §4.4 checkpoint pipeline can only be exercised against a live agent pod with an attached session")
}

func TestNodeDrainDuringMinIOOutage(t *testing.T) {
	t.Skip("not implemented: §12.8 lifecycle failure / node drain during MinIO outage — needs an active agent-pod workload on the drained node so the §12.5 drain-readiness webhook gates a real eviction; the e2e overlay runs a warm agent-pod workload but does not drive a live gateway session onto it")
}

func TestRuntimeUpgradeStuck(t *testing.T) {
	t.Skip("not implemented: §12.8 lifecycle failure / runtime upgrade stuck — needs the runtime upgrade pipeline driving real agent pods through a rolling runtime-image change; the e2e overlay runs a warm agent-pod workload but does not drive a live gateway session onto it")
}

func TestPoolUpgradeRollbackDuringExpanding(t *testing.T) {
	t.Skip("not implemented: §12.8 lifecycle failure / pool upgrade rollback during expanding — needs the pool upgrade state machine warming real agent pods so a mid-expand rollback can be triggered; the e2e overlay runs a warm agent-pod workload but does not drive a live gateway session onto it")
}

// --- Network failures ---
//
// TestNetworkPolicyDrift is implemented against the live lenny-system
// NetworkPolicies in config_drift_test.go.

func TestGatewayToPodPartition(t *testing.T) {
	t.Skip("not implemented: §12.8 network failure / gateway-to-pod partition — needs a network-partition injector (toxiproxy or a Chaos Mesh NetworkChaos) to sever the gateway-to-pod path during a live session; the e2e overlay runs a warm agent-pod workload but deploys no partition injector and drives no live gateway session")
}

func TestAgentToLLMProviderPartition(t *testing.T) {
	t.Skip("not implemented: §12.8 network failure / agent-to-LLM partition — needs an external-provider partition injector severing a live session's LLM-proxy egress; the e2e overlay runs a warm agent-pod workload but deploys no partition injector and drives no live gateway session")
}

func TestCrossZonePartition(t *testing.T) {
	t.Skip("not implemented: §12.8 network failure / cross-zone partition — the e2e Kind cluster is single-zone; a cross-zone partition test needs a multi-AZ cluster and an inter-zone partition injector, neither of which is deployed")
}

// --- Credential failures ---

func TestEmergencyRevocationDuringActiveSession(t *testing.T) {
	t.Skip("not implemented: §12.8 credential failure / emergency revocation — needs an active streaming session on a live agent pod so the Token Service revocation propagates to a running consumer; the e2e overlay runs a warm agent-pod workload but does not drive a live gateway session onto it")
}

func TestRotationFailure(t *testing.T) {
	t.Skip("not implemented: §12.8 credential failure / rotation failure — needs the RotateCredentials RPC exercised against a live credential lease plus a fault-injection harness; the e2e overlay runs a warm agent-pod workload but drives no live session that holds a credential lease")
}

func TestDenyListPropagationUnderRedisOutage(t *testing.T) {
	t.Skip("not implemented: §12.8 credential failure / deny-list under Redis outage — needs an active session consuming the deny-list propagation path while Redis is down; the e2e overlay runs a warm agent-pod workload but does not drive a live gateway session onto it")
}

func TestCredentialPoolExhaustion(t *testing.T) {
	t.Skip("not implemented: §12.8 credential failure / pool exhaustion — needs the credential pool with finite leases driven by real agent pods plus an over-allocation harness; the e2e overlay runs a warm agent-pod workload but does not drive a live gateway session onto it")
}

// --- Delegation failures ---

func TestChildCrashMidTask(t *testing.T) {
	t.Skip("not implemented: §12.8 delegation failure / child crash — needs a parent and child agent pod in a live delegation; the e2e overlay runs a warm agent-pod workload but does not drive a live gateway session onto it")
}

func TestParentCrashDuringAwaitChildren(t *testing.T) {
	t.Skip("not implemented: §12.8 delegation failure / parent crash during await_children — needs a live parent agent pod blocked in await_children with the §8 lease held; the e2e overlay runs a warm agent-pod workload but does not drive a live gateway session onto it")
}

func TestDelegationBudgetExhaustion(t *testing.T) {
	t.Skip("not implemented: §12.8 delegation failure / budget exhaustion — needs the §8 budget primitives exercised by a live delegation tree plus a budget-burn harness; the e2e overlay runs a warm agent-pod workload but does not drive a live gateway session onto it")
}

func TestLeaseExtensionCoolOffPersistence(t *testing.T) {
	t.Skip("not implemented: §12.8 delegation failure / cool-off persistence — needs a live delegation holding a lease through a gateway restart; the e2e overlay runs a warm agent-pod workload but does not drive a live gateway session onto it")
}

// --- Compliance failures ---
//
// TestAuditChainGapDetection is implemented against the live
// Postgres-backed §11.7 audit chain in audit_chain_test.go.

func TestErasureJobFailureMidSequence(t *testing.T) {
	t.Skip("not implemented: §12.8 compliance failure / erasure job mid-sequence — needs the erasure orchestrator driving a real user's data across every store plus a fault injector at step N; the dev-mode install runs no agent-pod workload and seeds no per-user state to erase")
}

func TestLegalHoldOverrideFlow(t *testing.T) {
	t.Skip("not implemented: §12.8 compliance failure / legal-hold override — needs the legal_hold_escrow_kek path and a region-scoped escrow bucket; no KMS adapter and no region-scoped escrow bucket are deployed on the dev-mode cluster")
}

func TestT3T4SLABreach(t *testing.T) {
	t.Skip("not implemented: §12.8 compliance failure / T3/T4 SLA breach — needs the erasure orchestrator plus clock injection to simulate an SLA-breach deadline; no clock-injection harness is deployed")
}

// --- Concurrency ---
//
// TestSandboxClaimRaceUnder100Goroutines and TestDoubleClaimVerification
// are implemented against the live API server and the
// lenny-sandboxclaim-guard webhook in concurrency_test.go.

func TestElicitationDeadlockDetection(t *testing.T) {
	t.Skip("not implemented: §12.8 concurrency / elicitation deadlock — needs the §9.2 elicitation chain exercised across live agent pods plus a deadlock-construction harness; the e2e overlay runs a warm agent-pod workload but does not drive a live gateway session onto it")
}

func TestDelegationDepthDeadlockDetection(t *testing.T) {
	t.Skip("not implemented: §12.8 concurrency / delegation depth deadlock — needs the §8 cycle detector exercised by a live delegation tree built to maximum depth; the e2e overlay runs a warm agent-pod workload but does not drive a live gateway session onto it")
}

// --- Time ---
//
// TestCertificateExpiryAdvance below; cert-manager outage (not expiry)
// is implemented in component_failure_test.go.

func TestGatewayClockDrift(t *testing.T) {
	t.Skip("not implemented: §12.8 time / clock drift — needs a clock-injection harness to skew a gateway replica's wall clock past the §13 drift-tolerance thresholds; no clock-injection harness is deployed")
}

func TestCertificateExpiryAdvance(t *testing.T) {
	t.Skip("not implemented: §12.8 time / certificate expiry — the §10.3 Certificates carry duration 24h / renewBefore 8h; exercising renewal-before-expiry needs a clock-injection harness to advance time past renewBefore, which is not deployed (cert-manager controller outage, a distinct scenario, is covered by TestCertManagerOutage)")
}

// --- Configuration ---
//
// TestPoolConfigDrift, TestNetworkPolicyConfigDrift, and
// TestSchemaMigrationDirtyFlag are implemented against the live
// pool-config validator, NetworkPolicies, and migration framework in
// config_drift_test.go.

func TestCRDUpgradeImmutableFieldChange(t *testing.T) {
	t.Skip("not implemented: §12.8 configuration / CRD upgrade with immutable field changes — the live lenny.dev CRDs are single-version (v1) with conversion strategy None and carry no x-kubernetes-validations immutability rules; exercising the conversion-webhook immutable-field rejection path needs a multi-version CRD with a conversion webhook, which is not deployed")
}
