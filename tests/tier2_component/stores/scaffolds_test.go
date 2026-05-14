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

// TestSessionStoreContract — Postgres-backed session store. Coverage:
// CRUD, state machine, concurrent claim via SELECT ... FOR UPDATE
// SKIP LOCKED, RLS, delegation-tree co-location, lineage, orphan
// reconciliation, retry-state persistence, FK cascade.
func TestSessionStoreContract(t *testing.T) {
	t.Skip("not implemented: §12.2.1 SessionStore — requires Postgres-backed pkg/sessionstore implementation + RLS migrations + claim-with-SKIP-LOCKED transaction helper")
}

// TestLeaseStoreContract — Redis + Postgres advisory store. Coverage:
// acquire/renew/release, TTL expiry, Redis-outage fallback to advisory
// lock, atomic re-acquisition after crash.
func TestLeaseStoreContract(t *testing.T) {
	t.Skip("not implemented: §12.2.1 LeaseStore — requires Redis lease primitives with pg_advisory_xact_lock fallback + crash-recovery re-acquisition path")
}

// TestQuotaStoreContract — Redis + Postgres quota store. Coverage:
// increment/decrement, sliding window, fail-open semantics, per-user
// fraction, per-replica hard cap, cumulative timeout, MAX-rule
// reconciliation, storage-quota pre-check, GC decrement.
func TestQuotaStoreContract(t *testing.T) {
	t.Skip("not implemented: §12.2.1 QuotaStore — pkg/quota arithmetic exists; this suite needs the Redis-backed counters and the MAX-rule reconciliation pipeline")
}

// TestTokenStoreContract — Postgres encrypted token store. Coverage:
// KMS-envelope encryption, hash storage, revocation lookup, rotation,
// RLS.
func TestTokenStoreContract(t *testing.T) {
	t.Skip("not implemented: §12.2.1 TokenStore — requires Postgres token table + KMS-envelope writer + revocation index migration")
}

// TestTokenIssuanceStoreContract — Postgres token-issuance store.
// Coverage: JTI uniqueness, revocation index, parent-jti tracking,
// expired-row GC.
func TestTokenIssuanceStoreContract(t *testing.T) {
	t.Skip("not implemented: §12.2.1 TokenIssuanceStore — requires the token-issuance ledger schema and the parent-jti relationship for delegation lineage")
}

// TestArtifactStoreContract — MinIO artifact store. Coverage:
// tenant-prefix validation, SSE-KMS for T3/T4, per-tenant key for T4,
// checkpoint rotation, legal-hold suspension, soft-delete idempotency,
// tombstone hard-prune, partial-manifest cleanup, eviction-context
// cleanup, GC exception handling, T4 KMS probe, MinIO-outage fallback
// to minimal state.
func TestArtifactStoreContract(t *testing.T) {
	t.Skip("not implemented: §12.2.1 ArtifactStore — requires pkg/artifactstore over MinIO with SSE-KMS, soft-delete tombstones, and Postgres-minimal-state fallback")
}

// TestEventStoreContract — Postgres event store. Coverage: audit hash
// chain, sequence monotonicity, event schema versioning, OCSF
// translation state machine, EventBus publish state,
// retranscribe-worker sweep, terminal-failure escalation, T2 batching
// loss alert, startup chain-continuity check, RLS, erasure.
func TestEventStoreContract(t *testing.T) {
	t.Skip("not implemented: §12.2.1 EventStore — requires pkg/audit-backed Postgres event ledger + retranscribe worker + OCSF translation pipeline")
}

// TestCredentialPoolStoreContract — Postgres encrypted credential
// pool. Coverage: pool CRUD, lease assignment, health scoring,
// deny-list, revocation, encryption, RLS, erasure.
func TestCredentialPoolStoreContract(t *testing.T) {
	t.Skip("not implemented: §12.2.1 CredentialPoolStore — requires the Token Service Postgres credential-pool tables and the health/deny-list scoring loop")
}

// TestEvictionStateStoreContract — Postgres eviction state. Coverage:
// eviction-state CRUD, MinIO context-key storage, terminal-state
// cleanup, RLS.
func TestEvictionStateStoreContract(t *testing.T) {
	t.Skip("not implemented: §12.2.1 EvictionStateStore — requires pkg/checkpoint-backed eviction tracking table and MinIO context-key index")
}

// TestMemoryStoreContract — Postgres + pgvector memory store with
// pluggable backends. Coverage: RLS, user-scope isolation,
// tenant-scope isolation, mandatory DeleteByUser/DeleteByTenant,
// startup preflight, per-job preflight, custom-backend contract
// validation, retention TTL, capacity eviction.
func TestMemoryStoreContract(t *testing.T) {
	t.Skip("not implemented: §12.2.1 MemoryStore — requires Postgres+pgvector default backend and the MemoryStore interface contract (mandatory DeleteByUser/DeleteByTenant)")
}

// TestEvalResultStoreContract — Postgres eval result store. Coverage:
// score CRUD, FK to sessions, RLS, erasure cascade.
func TestEvalResultStoreContract(t *testing.T) {
	t.Skip("not implemented: §12.2.1 EvalResultStore — Phase 17b feature; requires eval_results schema and the session FK cascade")
}

// TestSemanticCacheContract — Redis semantic cache with pluggable
// backends. Coverage: scope isolation (u:/s:/t:), per-tenant prefix,
// miss-on-outage, erasure.
func TestSemanticCacheContract(t *testing.T) {
	t.Skip("not implemented: §12.2.1 SemanticCache — Phase 17b feature; requires Redis-backed semantic cache with scope-aware key prefixes")
}

// TestPodRegistryContract — Kubernetes API CRDPodRegistry. Coverage:
// all ops listed in spec §12.6, optimistic locking via
// resource_version, WatchPods events within 500 ms P99.
func TestPodRegistryContract(t *testing.T) {
	t.Skip("not implemented: §12.2.1 PodRegistry — requires CRDPodRegistry over a real Kubernetes API (envtest) + WatchPods event-latency assertions")
}

// TestStoreRouterContract — Postgres + Redis store router. Coverage:
// session-shard extraction, tenant-shard routing, billing/audit-shard
// routing (R-03), scatter-gather concurrency and timeout,
// partial-result semantics.
func TestStoreRouterContract(t *testing.T) {
	t.Skip("not implemented: §12.2.1 StoreRouter — requires the pkg/storerouter implementation with R-03 routing rules and scatter-gather concurrency control")
}

// TestEventBusContract — Redis pub/sub event bus. Coverage:
// CloudEvents envelope, tenant-prefixed channels, at-most-once
// delivery, retranscribe semantics.
func TestEventBusContract(t *testing.T) {
	t.Skip("not implemented: §12.2.1 EventBus — requires Redis pub/sub event bus implementation + CloudEvents v1.0.2 envelope writer")
}

// TestDeleteByUserAndTenantInterface — every tenant-scoped store MUST
// expose DeleteByUser(ctx, tenantID, userID) error and
// DeleteByTenant(ctx, tenantID) error. The interface is
// compile-time-enforced; this test confirms the implementation
// actually deletes.
func TestDeleteByUserAndTenantInterface(t *testing.T) {
	t.Skip("not implemented: §12.2.1 / §14.10 — requires every Store implementation to satisfy the mandatory erasure interface; lands as each store does")
}
