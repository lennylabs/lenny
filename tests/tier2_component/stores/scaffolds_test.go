// SPDX-License-Identifier: MIT

//go:build component

// Tier-2 component scaffolds for the §12.2.1 store-interface contract
// suites. Every store role listed in spec §12.2 (and the table in
// TESTING.md §12.2.1) has one suite here. Each test calls t.Skip with
// a precise diagnosis pointing at the missing implementation:
//
//   - The static tier sees the test file and confirms it compiles.
//   - The component tier reports a clear "skipped: not implemented"
//     entry, so every store gap is visible at every run.
//   - When the implementation lands, the skip is removed and the
//     test starts asserting against the real backend through
//     testcontainers-go.
//
// Naming follows TESTING.md §12.2.1: TestSessionStoreContract,
// TestLeaseStoreContract, etc. Implementers will land a sibling
// _test.go file per store and remove the corresponding entry from
// this scaffold sheet.

package stores_test

import "testing"

// TestSessionStoreContract is implemented in sessionstore_test.go,
// which exercises the Postgres-backed pkg/gateway/sessionstore/pgstore
// against a real container.

// TestLeaseStoreContract — the Redis lease primitives are implemented
// in tests/tier2_component/leases against pkg/gateway/leasestore. The
// §12.4 Redis-outage Postgres advisory-lock fallback is not yet built.

// TestQuotaStoreContract — Redis-backed token-usage counter contract.
// Fixed-window counter: tests/tier2_component/quota/quotastore_test.go.
// Sliding-window counter (SlidingAdd / SlidingUsage):
// tests/tier2_component/quota/quotastore_sliding_test.go.
// §11.2 arithmetic (Check, HierarchicalCheck, FailOpenCeiling,
// PerUserFailOpenCeiling, MaxOvershoot, ReconcileMax) is unit-tested
// in pkg/quota. The §11.2 per-replica fail-open accounting timer
// layered over the counter and the Postgres-checkpoint pipeline that
// invokes ReconcileMax on Redis recovery are the follow-on tracked
// in BUILD-GAPS.md.

// TestTokenStoreContract is implemented in
// connectorcredstore_test.go, which exercises the §13.3 Postgres
// encrypted TokenStore (pkg/gateway/connectorcredstore/pgstore)
// against a real container with migration 0048 applied: KMS-envelope
// encryption of the access and refresh tokens, SHA-256 hash storage
// for the revocation-lookup hot path, upsert + UpdatedAt monotonicity,
// cross-tenant isolation under the RLS policy, and Delete /
// ListByConnector semantics.

// TestTokenIssuanceStoreContract is implemented in
// issuedtokenstore_test.go against pkg/gateway/issuedtokenstore.

// TestArtifactStoreContract — MinIO artifact store. Coverage:
// tenant-prefix validation, SSE-KMS for T3/T4, per-tenant key for T4,
// checkpoint rotation, legal-hold suspension, soft-delete idempotency,
// tombstone hard-prune, partial-manifest cleanup, eviction-context
// cleanup, GC exception handling, T4 KMS probe, MinIO-outage fallback
// to minimal state.
//
// spec: 12.2.1
// diagnosis: pkg/blobstore/miniostore is a thin MinIO client with
// tenant-prefix validation; the §12.5 ArtifactStore surface has no
// SSE-KMS configuration code path, no soft-delete tombstone column or
// hard-prune worker, no legal-hold suspension handling, no
// partial-manifest cleanup, no T4 per-tenant KMS probe, and no
// MinIO-outage fallback to Postgres minimal state.
func TestArtifactStoreContract(t *testing.T) {
	t.Skip("blocked: §12.2.1 ArtifactStore — SSE-KMS configuration, soft-delete tombstones, hard-prune worker, legal-hold suspension, partial-manifest cleanup, T4 per-tenant KMS probe, and the MinIO-outage Postgres-minimal-state fallback are not built on top of pkg/blobstore/miniostore")
}

// TestEventStoreContract is implemented in eventstore_test.go, which
// exercises the Postgres-backed §12.2.1 EventStore (pkg/gateway/auditstore)
// against a real container: the §11.7 audit hash chain, the OCSF
// translation state machine, the §12.3.7 EventBus publish-state
// machine, the startup chain-continuity check, RLS, and erasure.

// TestCredentialPoolStoreContract is implemented in
// credentialpoolstore_test.go, which exercises the Postgres-backed
// pkg/gateway/credentialpoolstore/pgstore against a real container.

// TestEvictionStateStoreContract is implemented in
// evictionstatestore_test.go, which exercises the Postgres-backed
// pkg/gateway/evictionstatestore/pgstore against a real container.

// TestMemoryStoreContract is implemented in memorystore_test.go, which
// exercises the Postgres-backed pkg/gateway/memorystore/pgstore. The
// pgvector default backend is a later-wave addition.

// TestEvalResultStoreContract is implemented in evalstore_test.go,
// which exercises the Postgres-backed pkg/gateway/evalstore/pgstore
// against a real container, including the sessions FK cascade.

// TestSemanticCacheContract is implemented in semanticcache_test.go,
// which exercises the Redis-backed pkg/gateway/semanticcache/redisstore
// against a real Redis container: the §12.4 t:{tenant}:scache:{scope}
// key scheme, per-tenant and per-user isolation, the §4.9 similarity
// lookup and TTL, and the §12.2 DeleteByUser / DeleteByTenant erasure.

// TestPodRegistryContract is implemented in
// pkg/podregistry/crd_test.go, which exercises the §12.6
// CRDPodRegistry over the controller-runtime fake client: every
// PodRegistry method (GetPod, UpdatePodState with mismatched-From
// rejection, ClaimPod with ErrPoolExhausted, ReleasePod by reason,
// ListPodsByPool with state filtering, CountByState, CreatePod,
// DeletePod, WatchPods) is covered. The §12.6 optimistic-locking
// CAS uses Sandbox.metadata.resourceVersion; the watch loop polls
// at the §12.6 P99 event-latency budget.

// TestStoreRouterContract is implemented in storerouter_test.go,
// which exercises the v1 pkg/storerouter.SingleShardRouter against
// a real Postgres + Redis container.

// TestEventBusContract is implemented in eventstore_test.go, which
// exercises the §12.3.7 RedisEventBus (pkg/gateway/eventbus) over a
// real Redis container: the CloudEvents v1.0.2 envelope, the
// tenant-prefixed channels, and at-most-once delivery isolation.

// TestDeleteByUserAndTenantInterface is implemented in
// erasure_interface_test.go, which compile-checks the §12.2.1 /
// §14.10 mandatory-erasure interface against every tenant-scoped
// store the gateway depends on.
