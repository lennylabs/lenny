// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos test scaffolds. Each test in this file corresponds to a
// TESTING.md-named chaos scenario that cannot be genuinely exercised
// against the e2e install on the Kind cluster: the scenario needs a
// true HA / multi-AZ store topology, a network-partition or
// clock-injection harness, a KMS adapter, or §8 delegation / §9.2
// elicitation primitives — none of which the e2e install provides.
// The sessiondriver harness now drives live sessions onto the warm
// agent-pod workload (used by TestPodKillDuringActiveSession in
// live_session_test.go), so the "no live session" half of the prior
// blocker is resolved; the remaining blockers are the specific
// fault-injection layer or production-code primitive each scenario
// names. Each test calls t.Skip with a diagnosis naming the spec
// section and the precise missing infrastructure.
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
	// The compose profile now ships a Redis Sentinel topology (one
	// master, one replica, three sentinels) per BUILD-GAPS, but the
	// gateway has no Sentinel-aware Redis client wired and the
	// chaos tier targets the Kind e2e cluster; the Sentinel
	// failover round-trip from §12.8 cannot be asserted against
	// Lenny's behavior until a redis.FailoverClient is plumbed into
	// pkg/store/redis and exposed through the production overlay.
	t.Skip("blocked: §12.8 store failure / Redis Sentinel failover — the compose profile ships a Sentinel topology but the gateway carries no Sentinel-aware Redis client, so a failover test cannot assert Lenny's behavior end-to-end")
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
// TestPodKillDuringActiveSession is implemented against a live gateway
// session driven onto the warm pool through the sessiondriver harness
// in live_session_test.go.

func TestNodeDrainDuringMinIOOutage(t *testing.T) {
	t.Skip("blocked: §12.8 lifecycle failure / node drain during MinIO outage — the sessiondriver harness now drives live sessions, but a faithful node-drain test also needs a cordon + drain step against the e2e Kind node that runs the active session, plus the §12.5 drain-readiness webhook installed (enabled by features.drainReadiness in the e2e overlay). The combined drain + MinIO-outage scenario is not yet automated against the e2e cluster")
}

func TestRuntimeUpgradeStuck(t *testing.T) {
	t.Skip("blocked: §12.8 lifecycle failure / runtime upgrade stuck — the sessiondriver harness drives live sessions, but the runtime upgrade pipeline (rolling runtime-image change with mid-roll stuck-pod detection) is not yet wired in the e2e gateway. Driving a stuck-upgrade scenario needs the runtime upgrade controller plus a fault-injection knob")
}

func TestPoolUpgradeRollbackDuringExpanding(t *testing.T) {
	t.Skip("blocked: §12.8 lifecycle failure / pool upgrade rollback during expanding — the pool upgrade state machine (Phase, ExpansionTarget) and the rollback path are not yet wired in the e2e WarmPoolController. The sessiondriver harness can drive sessions onto the pool, but there is no expanding-phase pool to roll back from on this install")
}

// --- Network failures ---
//
// TestNetworkPolicyDrift is implemented against the live lenny-system
// NetworkPolicies in config_drift_test.go.

func TestGatewayToPodPartition(t *testing.T) {
	t.Skip("blocked: §12.8 network failure / gateway-to-pod partition — the sessiondriver harness now drives live sessions, but the partition itself needs a network-traffic injector (toxiproxy or a Chaos Mesh NetworkChaos custom resource) on the gateway-to-pod path. The e2e overlay deploys no such injector; without it the test cannot distinguish a real partition from any other connectivity loss")
}

func TestAgentToLLMProviderPartition(t *testing.T) {
	t.Skip("blocked: §12.8 network failure / agent-to-LLM partition — the sessiondriver harness drives live sessions, but the §10.1 LLM-proxy egress requires the lenny-llmproxy egress pod and an external-provider partition injector. The e2e overlay enables features.llmProxy but stands up no fault-injection layer on the proxy-to-provider hop")
}

func TestCrossZonePartition(t *testing.T) {
	t.Skip("not implemented: §12.8 network failure / cross-zone partition — the e2e Kind cluster is single-zone; a cross-zone partition test needs a multi-AZ cluster and an inter-zone partition injector, neither of which is deployed")
}

// --- Credential failures ---

func TestEmergencyRevocationDuringActiveSession(t *testing.T) {
	t.Skip("blocked: §12.8 credential failure / emergency revocation — the sessiondriver harness drives live sessions, but the §10.4 emergency-revocation flow requires a credential lease held by the session (the echo runtime declares no credentials) and the Token Service RevokeCredentials gRPC. Driving the revocation against an empty lease set would assert nothing. Needs a runtime with declared credentials plus the lease-holding session")
}

func TestRotationFailure(t *testing.T) {
	t.Skip("blocked: §12.8 credential failure / rotation failure — the sessiondriver harness drives live sessions, but the rotation path requires a credential lease (the echo runtime declares none) and a fault-injection knob on the Token Service RotateCredentials RPC. Neither is wired in the e2e install")
}

func TestDenyListPropagationUnderRedisOutage(t *testing.T) {
	t.Skip("blocked: §12.8 credential failure / deny-list under Redis outage — the sessiondriver harness drives live sessions and tests/tier8_chaos/store_failure_test.go injects Redis outages, but the deny-list propagation path requires the §10.4 deny-list publish/consume primitive driven by a credential-holding session. The echo runtime declares no credentials")
}

func TestCredentialPoolExhaustion(t *testing.T) {
	t.Skip("blocked: §12.8 credential failure / pool exhaustion — the sessiondriver harness can drive multiple concurrent sessions, but the credential-pool exhaustion scenario requires a finite-lease credential pool (the e2e gateway runs with an empty credentialpoolstore) plus an over-allocation harness. Needs the pool seeded with a small lease ceiling")
}

// --- Delegation failures ---

func TestChildCrashMidTask(t *testing.T) {
	t.Skip("blocked: §12.8 delegation failure / child crash — the sessiondriver harness drives single sessions, but the §8 delegation chain (parent → child sessions, await_children, lease tracking) is not wired in the e2e gateway. There is no DelegateTask RPC and no child-session orchestration to exercise. Needs the §8 delegation primitive shipped first")
}

func TestParentCrashDuringAwaitChildren(t *testing.T) {
	t.Skip("blocked: §12.8 delegation failure / parent crash during await_children — depends on the §8 delegation primitive (parent / child sessions, await_children, lease state machine) which is not wired in the e2e gateway. Same root cause as TestChildCrashMidTask")
}

func TestDelegationBudgetExhaustion(t *testing.T) {
	t.Skip("blocked: §12.8 delegation failure / budget exhaustion — depends on the §8 delegation primitives (budget, lease, delegate_task) which are not wired in the e2e gateway")
}

func TestLeaseExtensionCoolOffPersistence(t *testing.T) {
	t.Skip("blocked: §12.8 delegation failure / cool-off persistence — depends on the §8 delegation lease state machine which is not wired in the e2e gateway. The sessiondriver harness can survive a gateway restart for a regular session, but there is no lease to extend on the echo runtime")
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
	t.Skip("blocked: §12.8 concurrency / elicitation deadlock — the sessiondriver harness can drive sessions, but the §9.2 elicitation chain (lenny/request_elicitation, the responder agent, the chained respondent agent) requires runtime support for elicitations. The echo runtime does not emit elicitations, so there is no chain to deadlock")
}

func TestDelegationDepthDeadlockDetection(t *testing.T) {
	t.Skip("blocked: §12.8 concurrency / delegation depth deadlock — depends on the §8 delegation primitives (delegate_task, cycle detector) which are not wired in the e2e gateway")
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
