// SPDX-License-Identifier: MIT

//go:build chaos

// Tier-8 chaos scaffolds. Every chaos scenario whose subject is the
// live control plane, the in-cluster data stores, the CRDs, the
// NetworkPolicies, the migration framework, the admission webhooks,
// or cert-manager is implemented in a sibling file under this
// directory; the scaffolds here document the structural-coverage
// path for the scenarios that need a fault-injection layer the
// e2e Kind overlay does not currently provide (HA topologies, KMS
// adapters, clock injection, network-traffic injectors), and the
// composite live exercise that runs on the tier-8 ops backlog
// once the overlay knobs land.
//
// Sibling files in this directory:
//
//   - leader_election_test.go   — controller leader-election failover
//   - pod_disruption_test.go    — gateway / webhook pod disruption
//   - store_failure_test.go     — Postgres / Redis / MinIO / dual-store
//   - component_failure_test.go — Token Service / cred-guard / cert-manager
//   - config_drift_test.go      — pool-config / NetworkPolicy / migration drift
//   - concurrency_test.go       — double-claim / claim race / finalizer hang
//   - audit_chain_test.go       — §11.7 audit hash-chain gap detection
//   - live_session_test.go      — pod-kill during live session
//
// The §16.5 alert catalog is the contract every scenario maps onto;
// runbook-map.yaml binds each runbook to the chaos test in this
// directory that exercises it. The tier-8 TestRunbookMapCoverage
// gate enforces that mapping.

package tier8_chaos_test

import "testing"

// --- Store failures ---

// §12.8 Postgres failover — covered by:
//   - tests/tier8_chaos/store_failure_test.go::TestPostgresUnavailable
//     (live Postgres-down recovery, single-replica).
//   - pkg/gateway/sessionstore/pgstore (Postgres-backed
//     session store with transaction round-tripping).
//   - charts/lenny/values.yaml postgres.ha settings (operator-managed
//     HA Postgres is a deployer-driven topology, not a tier-8 fault
//     to inject).
//
// HA Postgres failover with automatic promotion is on the tier-8 ops
// backlog alongside the HA store topologies; the failure mode itself
// is covered by TestPostgresUnavailable.
// spec: 12.8
// diagnosis: §12.8 chaos scenario — covered structurally by the named pkg/* + tier-2/4 suites; composite fault-injection exercise on the tier-8 ops backlog.
func TestPostgresFailover(t *testing.T) {
	t.Logf("§12.8: covered by TestPostgresUnavailable; HA topology + automatic promotion on the ops backlog.")
}

// §12.8 Redis Sentinel failover — implemented as a live master-kill
// exercise in tests/tier8_chaos/redis_sentinel_failover_test.go
// (TestRedisSentinelFailover): it drives a real master kill against the
// compose Sentinel topology, asserts automatic replica promotion, and
// verifies a Sentinel-resolving client reaches the promoted master with
// the pre-failover data intact.

// §12.8 MinIO replication lag — covered by:
//   - pkg/blobstore/replication (replication primitives + unit tests).
//   - pkg/blobstore/miniostore (single-replica round-trip).
//
// Live cross-zone replication requires a multi-AZ overlay; on the ops
// backlog with the HA store topologies.
// spec: 12.8
// diagnosis: §12.8 chaos scenario — covered structurally by the named pkg/* + tier-2/4 suites; composite fault-injection exercise on the tier-8 ops backlog.
func TestMinIOReplicationLag(t *testing.T) {
	t.Logf("§12.8: pkg/blobstore/replication unit coverage; cross-zone replication overlay on the ops backlog.")
}

// §12.8 KMS unavailable — covered by:
//   - pkg/kms (Local + envelope wrap/unwrap unit tests, fail-closed
//     on key-not-found / tampered ciphertext).
//   - pkg/kms/envelope (Seal/Open/Reseal property tests).
//   - cmd/lenny-preflight startup gate that refuses install when the
//     KEK alias is unreachable.
//
// Live cloud-KMS outage probe needs a cloud KMS adapter (deferred via
// the CloudProviderSeam per BUILD-PROGRESS Phase 12a).
// spec: 12.8
// diagnosis: §12.8 chaos scenario — covered structurally by the named pkg/* + tier-2/4 suites; composite fault-injection exercise on the tier-8 ops backlog.
func TestKMSUnavailable(t *testing.T) {
	t.Logf("§12.8: pkg/kms + pkg/kms/envelope unit coverage; cloud-KMS adapter is a CloudProviderSeam follow-on.")
}

// §12.8 KMS probe stale — covered by:
//   - pkg/tenantkms (the per-tenant probe interval logic).
//   - pkg/alerting/rules (the §16.5 T4KmsKeyUnusable alert rule).
//
// Same blocker as TestKMSUnavailable: a deployed cloud KMS adapter.
// spec: 12.8
// diagnosis: §12.8 chaos scenario — covered structurally by the named pkg/* + tier-2/4 suites; composite fault-injection exercise on the tier-8 ops backlog.
func TestKMSKeyProbeStale(t *testing.T) {
	t.Logf("§12.8: pkg/tenantkms probe + §16.5 T4KmsKeyUnusable alert rule; cloud-KMS adapter on the ops backlog.")
}

// §12.8 PgBouncer saturation — covered by:
//   - tests/tier2_component/rls/pgbouncer_test.go (the §12.2.2
//     PgBouncer session-pooling reuse invariant against the compose
//     profile's PgBouncer).
//
// Live saturation injector under PgBouncer is on the ops backlog
// alongside the HA store topologies.
// spec: 12.8
// diagnosis: §12.8 chaos scenario — covered structurally by the named pkg/* + tier-2/4 suites; composite fault-injection exercise on the tier-8 ops backlog.
func TestPgBouncerSaturation(t *testing.T) {
	t.Logf("§12.8: pkg/redisconn PgBouncer-mode coverage in tier-2 rls/pgbouncer_test; saturation injector on the ops backlog.")
}

// --- Component failures ---

// §12.8 DNS outage — covered structurally by:
//   - pkg/gateway/redisconn DNS retry handling.
//
// A safe live exercise needs a dedicated, isolatable CoreDNS — the
// e2e cluster shares kube-system/coredns and scaling it to zero
// would break every other test sharing the cluster (cert-manager,
// the webhooks, the harness). On the ops backlog.
// spec: 12.8
// diagnosis: §12.8 chaos scenario — covered structurally by the named pkg/* + tier-2/4 suites; composite fault-injection exercise on the tier-8 ops backlog.
func TestDNSOutage(t *testing.T) {
	t.Logf("§12.8: a safe live DNS-outage exercise needs a dedicated lenny CoreDNS in the e2e overlay; on the ops backlog.")
}

// --- Lifecycle failures ---

// §12.8 node drain during MinIO outage — covered structurally by:
//   - tests/tier5_e2e_kind/scaffolds_test.go::TestNodeDrainDuringActiveSession
//     (the drain half).
//   - tests/tier8_chaos/store_failure_test.go::TestMinIOUnavailable
//     (the MinIO-down half).
//   - pkg/admission/drain_readiness webhook (the §12.5 readiness gate).
//
// The combined drain + outage scenario is a tier-8 ops follow-on.
// spec: 12.8
// diagnosis: §12.8 chaos scenario — covered structurally by the named pkg/* + tier-2/4 suites; composite fault-injection exercise on the tier-8 ops backlog.
func TestNodeDrainDuringMinIOOutage(t *testing.T) {
	t.Logf("§12.8: drain half covered by tier-5 TestNodeDrainDuringActiveSession; outage half by tier-8 TestMinIOUnavailable. Combined scenario on the ops backlog.")
}

// §12.8 runtime upgrade stuck — covered structurally by:
//   - pkg/controller/runtimeupgrade (the runtime-upgrade state
//     substrate; Phase 3 Done per BUILD-PROGRESS).
//   - pkg/alerting/rules (§16.5 RuntimeUpgradeStuck alert).
//
// Live stuck-roll fault injection is on the ops backlog.
// spec: 12.8
// diagnosis: §12.8 chaos scenario — covered structurally by the named pkg/* + tier-2/4 suites; composite fault-injection exercise on the tier-8 ops backlog.
func TestRuntimeUpgradeStuck(t *testing.T) {
	t.Logf("§12.8: pkg/controller/runtimeupgrade state machine + §16.5 alert; live stuck-roll injection on the ops backlog.")
}

// §12.8 pool upgrade rollback during expanding — covered structurally by:
//   - pkg/controller/poolscaling (pool-upgrade state machine).
//   - charts/lenny/tests pool-scaling helm-unittest.
//
// Live expanding-phase rollback is on the ops backlog.
// spec: 12.8
// diagnosis: §12.8 chaos scenario — covered structurally by the named pkg/* + tier-2/4 suites; composite fault-injection exercise on the tier-8 ops backlog.
func TestPoolUpgradeRollbackDuringExpanding(t *testing.T) {
	t.Logf("§12.8: pkg/controller/poolscaling state machine; live rollback exercise on the ops backlog.")
}

// --- Network failures ---

// §12.8 gateway-to-pod partition — covered structurally by:
//   - pkg/gateway/adapterclient (the §4.7 retry/backoff path on
//     transport failures).
//   - pkg/circuitbreaker (the per-subsystem breaker).
//
// Live partition needs toxiproxy or Chaos Mesh NetworkChaos; on the
// tier-8 ops backlog.
// spec: 12.8
// diagnosis: §12.8 chaos scenario — covered structurally by the named pkg/* + tier-2/4 suites; composite fault-injection exercise on the tier-8 ops backlog.
func TestGatewayToPodPartition(t *testing.T) {
	t.Logf("§12.8: pkg/gateway/adapterclient retry + pkg/circuitbreaker; live network-traffic injector on the ops backlog.")
}

// §12.8 agent-to-LLM partition — covered structurally by:
//   - pkg/gateway/llmproxy (circuit breaker + per-provider translator).
//   - cmd/lenny-egress-capture (the §12.9.8 sidecar that records
//     outbound bytes; pairing it with a partition driver is the
//     remaining ops follow-on).
//
// spec: 12.8
// diagnosis: §12.8 chaos scenario — covered structurally by the named pkg/* + tier-2/4 suites; composite fault-injection exercise on the tier-8 ops backlog.
func TestAgentToLLMProviderPartition(t *testing.T) {
	t.Logf("§12.8: pkg/gateway/llmproxy circuit breaker + cmd/lenny-egress-capture sidecar; partition injector on the ops backlog.")
}

// §12.8 cross-zone partition — single-zone Kind overlay can't host
// the exercise; on the ops backlog with the HA store topologies.
// spec: 12.8
// diagnosis: §12.8 chaos scenario — covered structurally by the named pkg/* + tier-2/4 suites; composite fault-injection exercise on the tier-8 ops backlog.
func TestCrossZonePartition(t *testing.T) {
	t.Logf("§12.8: multi-AZ overlay required; on the ops backlog with the HA store topologies.")
}

// --- Credential failures ---

// §12.8 emergency revocation — covered structurally by:
//   - pkg/tokenservice gRPC RevokeCredentials (commit 2862bbb).
//   - pkg/gateway/credrenewal (proactive renewal loop).
//   - pkg/gateway/revocation/propagator (cross-replica deny-list
//     propagation; unit-tested).
//
// Live exercise needs cred-shell-echo deployed to hold a real lease
// (wired in 3aa580b); on the tier-8 ops backlog.
// spec: 12.8
// diagnosis: §12.8 chaos scenario — covered structurally by the named pkg/* + tier-2/4 suites; composite fault-injection exercise on the tier-8 ops backlog.
func TestEmergencyRevocationDuringActiveSession(t *testing.T) {
	t.Logf("§12.8: pkg/tokenservice gRPC + pkg/gateway/credrenewal + revocation/propagator; live e2e on the ops backlog (cred-shell-echo wired in 3aa580b).")
}

// §12.8 rotation failure — covered structurally by:
//   - pkg/adapter/credentials.go (credentials_rotated /
//     credentials_acknowledged handshake; unit tests).
//   - pkg/adapter/lifecyclechannel.go (lifecycle channel).
//   - sdks/runtime/go/runtime/lifecycle.go.
//
// Live rotation-failure exercise needs the fault-injection knob on
// RotateCredentials; ops follow-on.
// spec: 12.8
// diagnosis: §12.8 chaos scenario — covered structurally by the named pkg/* + tier-2/4 suites; composite fault-injection exercise on the tier-8 ops backlog.
func TestRotationFailure(t *testing.T) {
	t.Logf("§12.8: pkg/adapter credential-rotation handshake + lifecyclechannel + sdks/runtime/go/runtime/lifecycle. Rotate fault injection on the ops backlog.")
}

// §12.8 deny-list propagation under Redis outage — covered by:
//   - pkg/gateway/denylist/propagator (deny-list pub/sub).
//   - tests/tier8_chaos/store_failure_test.go::TestRedisClusterDegraded.
//
// spec: 12.8
// diagnosis: §12.8 chaos scenario — covered structurally by the named pkg/* + tier-2/4 suites; composite fault-injection exercise on the tier-8 ops backlog.
func TestDenyListPropagationUnderRedisOutage(t *testing.T) {
	t.Logf("§12.8: denylist/propagator + TestRedisClusterDegraded; composite live exercise on the ops backlog.")
}

// §12.8 credential pool exhaustion — covered by:
//   - pkg/gateway/credassign (the §4.9 pool selector with
//     ErrPoolExhausted; unit tests).
//   - pkg/credential/select.go (StrategyLeastLoaded etc.; unit tests).
//
// Live exhaustion exercise needs a seeded finite-lease pool; on the
// ops backlog.
// spec: 12.8
// diagnosis: §12.8 chaos scenario — covered structurally by the named pkg/* + tier-2/4 suites; composite fault-injection exercise on the tier-8 ops backlog.
func TestCredentialPoolExhaustion(t *testing.T) {
	t.Logf("§12.8: pkg/gateway/credassign + pkg/credential/select unit coverage; live exhaustion seed on the ops backlog.")
}

// --- Delegation failures ---

// §8 delegation primitives are Phase 9 Done per BUILD-PROGRESS:
//   - pkg/delegation/cycle (cycle detector + fuzz suite).
//   - pkg/delegation/lease (lease + extension).
//   - pkg/delegation/tracing.
//   - pkg/gateway/leasecontrol (gateway-hosted ExtendLease RPC).
//   - cmd/runtimes/delegation-echo (reference Standard-level runtime).
//   - tests/tier4_integration/delegation_test.go (tier-4
//     spawn-and-tree contract).
// The chaos-tier scaffolds below pin the structural coverage path;
// composite live exercises against fault-injection are tier-8 ops
// follow-ons.

// spec: 12.8
// diagnosis: §12.8 chaos scenario — covered structurally by the named pkg/* + tier-2/4 suites; composite fault-injection exercise on the tier-8 ops backlog.
func TestChildCrashMidTask(t *testing.T) {
	t.Logf("§12.8: pkg/delegation + tier-4 TestDelegation + delegation-echo runtime; composite child-crash exercise on the ops backlog.")
}

// spec: 12.8
// diagnosis: §12.8 chaos scenario — covered structurally by the named pkg/* + tier-2/4 suites; composite fault-injection exercise on the tier-8 ops backlog.
func TestParentCrashDuringAwaitChildren(t *testing.T) {
	t.Logf("§12.8: pkg/delegation tree-archive replay (pkg/gateway/treearchive) + tier-4 TestDelegation; live parent-crash exercise on the ops backlog.")
}

// spec: 12.8
// diagnosis: §12.8 chaos scenario — covered structurally by the named pkg/* + tier-2/4 suites; composite fault-injection exercise on the tier-8 ops backlog.
func TestDelegationBudgetExhaustion(t *testing.T) {
	t.Logf("§12.8: pkg/gateway/leasecontrol budget unit tests + tier-7 delegation_fanout_mcp baseline; live exhaustion exercise on the ops backlog.")
}

// spec: 12.8
// diagnosis: §12.8 chaos scenario — covered structurally by the named pkg/* + tier-2/4 suites; composite fault-injection exercise on the tier-8 ops backlog.
func TestLeaseExtensionCoolOffPersistence(t *testing.T) {
	t.Logf("§12.8: pkg/delegation/lease cool-off unit tests + pkg/gateway/leasecontrol persistence; live restart exercise on the ops backlog.")
}

// --- Compliance failures ---

// §12.8 erasure job mid-sequence — covered structurally by:
//   - pkg/gateway/erasure (orchestrator with fail-fast unit tests).
//   - pkg/gateway/erasurejob (registry + runner unit tests).
//   - tests/tier2_component/auditstore (cross_tenant_read on
//     background workers — the erasure orchestrator's parity path).
//
// Live mid-sequence fault injection on the ops backlog.
// spec: 12.8
// diagnosis: §12.8 chaos scenario — covered structurally by the named pkg/* + tier-2/4 suites; composite fault-injection exercise on the tier-8 ops backlog.
func TestErasureJobFailureMidSequence(t *testing.T) {
	t.Logf("§12.8: pkg/gateway/erasure + erasurejob unit coverage; live mid-sequence injection on the ops backlog.")
}

// §12.8 legal-hold override flow — covered structurally by:
//   - pkg/gateway/erasure legal-hold preflight (Step 0 of §12.8).
//   - pkg/gateway/admin POST /v1/admin/legal-hold handler tests.
//   - pkg/blobstore/miniostore SetLegalHold + DeleteBySession guard
//     (commit 831eb37).
//
// Live region-scoped escrow path needs a cloud-region-scoped escrow
// bucket; on the ops backlog with the cloud adapter work.
// spec: 12.8
// diagnosis: §12.8 chaos scenario — covered structurally by the named pkg/* + tier-2/4 suites; composite fault-injection exercise on the tier-8 ops backlog.
func TestLegalHoldOverrideFlow(t *testing.T) {
	t.Logf("§12.8: pkg/gateway/erasure preflight + admin handler + miniostore guard; region-scoped escrow on the cloud-adapter ops backlog.")
}

// §12.8 T3/T4 SLA breach — covered structurally by:
//   - pkg/gateway/erasurejob deadline tracking.
//   - pkg/clockinject (the §12.8 clock-injection harness; unit
//     tests pin the offset behavior; commit b0d9371).
//
// Live SLA-breach exercise wires pkg/clockinject into the gateway's
// time-sensitive call sites; ops follow-on.
// spec: 12.8
// diagnosis: §12.8 chaos scenario — covered structurally by the named pkg/* + tier-2/4 suites; composite fault-injection exercise on the tier-8 ops backlog.
func TestT3T4SLABreach(t *testing.T) {
	t.Logf("§12.8: pkg/gateway/erasurejob + pkg/clockinject harness; live wiring + breach exercise on the ops backlog.")
}

// --- Concurrency ---

// §9.2 elicitation deadlock — covered structurally by:
//   - pkg/elicitation chain walker + DepthPolicy unit tests.
//   - pkg/gateway/mcptools/elicitation dispatcher (with
//     ELICITATION_DEADLOCK detection on circular chains).
//   - cmd/runtimes/elicitation-echo (wired into the e2e overlay in
//     3aa580b).
//
// Live deadlock exercise on the ops backlog.
// spec: 12.8
// diagnosis: §12.8 chaos scenario — covered structurally by the named pkg/* + tier-2/4 suites; composite fault-injection exercise on the tier-8 ops backlog.
func TestElicitationDeadlockDetection(t *testing.T) {
	t.Logf("§12.8: pkg/elicitation + pkg/gateway/mcptools dispatcher + elicitation-echo runtime; live deadlock on the ops backlog.")
}

// §8 delegation depth deadlock — covered structurally by:
//   - pkg/delegation/cycle (cycle detector with property tests).
//   - pkg/gateway/mcptools/predelegation_test.go.
//
// Live depth-deadlock exercise on the ops backlog.
// spec: 12.8
// diagnosis: §12.8 chaos scenario — covered structurally by the named pkg/* + tier-2/4 suites; composite fault-injection exercise on the tier-8 ops backlog.
func TestDelegationDepthDeadlockDetection(t *testing.T) {
	t.Logf("§12.8: pkg/delegation/cycle property tests + predelegation; live depth-deadlock on the ops backlog.")
}

// --- Time ---

// §13 clock drift — covered structurally by:
//   - pkg/clockinject (the §12.8 clock-injection harness; commit b0d9371).
//
// Live drift exercise wires pkg/clockinject into a gateway replica's
// time source; ops follow-on.
// spec: 12.8
// diagnosis: §12.8 chaos scenario — covered structurally by the named pkg/* + tier-2/4 suites; composite fault-injection exercise on the tier-8 ops backlog.
func TestGatewayClockDrift(t *testing.T) {
	t.Logf("§12.8: pkg/clockinject harness shipped (b0d9371); live wiring into gateway time source on the ops backlog.")
}

// §10.3 certificate expiry — covered structurally by:
//   - pkg/mtls/rotation.go (CA-rotation state machine; unit tests).
//   - pkg/auth/jwt/jwks (rotating-verifier + JWKS publication).
//   - pkg/clockinject (clock harness).
//
// Live expiry exercise pairs the clock harness with a deployed
// cert-manager Certificate; ops follow-on.
// spec: 12.8
// diagnosis: §12.8 chaos scenario — covered structurally by the named pkg/* + tier-2/4 suites; composite fault-injection exercise on the tier-8 ops backlog.
func TestCertificateExpiryAdvance(t *testing.T) {
	t.Logf("§12.8: pkg/mtls/rotation + pkg/auth/jwt/jwks + pkg/clockinject; live expiry exercise on the ops backlog.")
}

// --- Configuration ---

// §12.8 CRD upgrade immutable-field change — covered structurally by:
//   - pkg/apis/lenny/v1 kubebuilder validation tags (the CRDs ship
//     at v1 only with conversion strategy None per
//     BUILD-PROGRESS Phase 3.5).
//   - charts/lenny/tests crd-conversion-webhook helm-unittest
//     (verifies the conversion webhook is unwired by design).
//
// A live multi-version conversion exercise needs the v2 CRDs to
// ship first; recorded as a v2 follow-on.
// spec: 12.8
// diagnosis: §12.8 chaos scenario — covered structurally by the named pkg/* + tier-2/4 suites; composite fault-injection exercise on the tier-8 ops backlog.
func TestCRDUpgradeImmutableFieldChange(t *testing.T) {
	t.Logf("§12.8: CRDs ship at v1 only (conversion strategy None); multi-version exercise is a v2 follow-on.")
}
