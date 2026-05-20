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

// TestQuotaStoreContract — Redis + Postgres quota store. The
// fixed-window token-usage counter is implemented in
// tests/tier2_component/quota against pkg/gateway/quotastore, and the
// §11.2 arithmetic lives in pkg/quota. The remaining QuotaStore
// surface — the rolling sliding window, the fail-open per-replica
// accounting, the cumulative fail-open timer, and the MAX-rule
// Postgres reconciliation — is not yet built.
//
// spec: 12.2.1
// diagnosis: pkg/gateway/quotastore exposes only the fixed-window
// counter, and pkg/quota has the §11.2 arithmetic (Check,
// HierarchicalCheck, FailOpenCeiling, ReconcileMax). The sliding-
// window counter, the per-replica fail-open accounting layered over
// it, the cumulative fail-open timer, and the Postgres-checkpoint
// reconciliation pipeline that drives the §11.2 MAX rule on Redis
// recovery have no callable code path.
func TestQuotaStoreContract(t *testing.T) {
	t.Skip("blocked: §12.2.1 QuotaStore — pkg/gateway/quotastore has only the fixed-window Add/Usage methods; the rolling sliding window, the per-replica fail-open accounting, the cumulative fail-open timer, and the Postgres-checkpoint MAX-rule reconciliation pipeline are not built")
}

// TestTokenStoreContract — Postgres encrypted token store. Coverage:
// KMS-envelope encryption, hash storage, revocation lookup, rotation,
// RLS.
//
// spec: 12.2.1
// diagnosis: no Postgres-backed encrypted TokenStore exists. The
// migrations table issued_tokens stores only the SHA-256 hash of the
// raw token, never the token bytes; the §13.3 TokenStore role for
// encrypted downstream tokens is unimplemented, with no KMS-envelope
// writer wired through pkg/kms/envelope for token secrets and no
// revocation lookup index for the encrypted-token shape.
func TestTokenStoreContract(t *testing.T) {
	t.Skip("blocked: §12.2.1 TokenStore — the Postgres encrypted-token table, the KMS-envelope writer for token secrets, and the matching revocation-lookup migration are not built")
}

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

// TestPodRegistryContract — Kubernetes API CRDPodRegistry. Coverage:
// all ops listed in spec §12.6, optimistic locking via
// resource_version, WatchPods events within 500 ms P99.
//
// spec: 12.2.1
// diagnosis: no CRDPodRegistry implementation backed by the
// Kubernetes API exists. pkg/podsession.Registry is the in-process
// per-session pod-binding map used by sessionserver; the §12.6
// PodRegistry surface (optimistic locking via resource_version,
// WatchPods with bounded event latency, the documented CRUD ops) is
// not built.
func TestPodRegistryContract(t *testing.T) {
	t.Skip("blocked: §12.2.1 PodRegistry — no CRDPodRegistry over the Kubernetes API exists; pkg/podsession.Registry is an in-process map and there is no WatchPods event-latency surface to assert against")
}

// TestStoreRouterContract is implemented in storerouter_test.go,
// which exercises the v1 pkg/storerouter.SingleShardRouter against
// a real Postgres + Redis container.

// TestEventBusContract is implemented in eventstore_test.go, which
// exercises the §12.3.7 RedisEventBus (pkg/gateway/eventbus) over a
// real Redis container: the CloudEvents v1.0.2 envelope, the
// tenant-prefixed channels, and at-most-once delivery isolation.

// TestDeleteByUserAndTenantInterface — every tenant-scoped store MUST
// expose DeleteByUser(ctx, tenantID, userID) error and
// DeleteByTenant(ctx, tenantID) error. Today only sessionstore,
// memorystore, interactionstore, and semanticcache expose both;
// billing, eval, experiment, environment, runtime, connector, user,
// custom-role, transcript, agent-pod-state, etc. carry no erasure
// adapter, so a compile-time interface assertion fails today.
//
// spec: 12.2.1
// diagnosis: only a subset of tenant-scoped stores implement both
// DeleteByUser and DeleteByTenant; the remaining stores need
// erasure adapters before a single compile-time check is meaningful.
func TestDeleteByUserAndTenantInterface(t *testing.T) {
	t.Skip("blocked: §12.2.1 / §14.10 mandatory-erasure interface — only a subset of tenant-scoped stores expose DeleteByUser and DeleteByTenant today; the remaining stores need erasure adapters before a single compile-time interface check is meaningful")
}
