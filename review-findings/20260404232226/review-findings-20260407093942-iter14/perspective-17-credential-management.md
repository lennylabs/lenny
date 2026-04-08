# Technical Design Review Findings — 2026-04-07 (Iteration 14)

**Document reviewed:** `technical-design.md` (8,691 lines)
**Perspective:** 17 — Credential Management & Secret Handling
**Iteration:** 14
**Prior finding status:** CRD-031/032 (vault_transit/github missing from Secret shape table) — STILL PRESENT (lines 1016-1025)
**New findings:** 1

## Medium

| # | ID | Finding | Section | Lines |
|---|-----|---------|---------|-------|
| 1 | CRD-033 | Secret shape table introductory sentence states "Each credential Secret has a **single** `data` key" but the table immediately below includes `aws_bedrock (access keys)` with **two** keys (`accessKeyId` and `secretAccessKey`) and `azure_openai (Azure AD)` with **three** keys (`clientId`, `clientSecret`, `tenantId`). The prose contradicts 2 of the 6 table entries. Fix: change "a single `data` key" to "one or more `data` keys" or "a fixed set of `data` keys". | 4.9 | 1016 |

## Still-open prior findings

| # | ID | Finding | Section | Lines |
|---|-----|---------|---------|-------|
| 2 | CRD-031 | Secret shape table (lines 1018-1025) is missing a row for the `vault_transit` provider. Operators configuring `vault_transit` pools have no documentation of what Kubernetes Secret key(s) to use for the source material (Vault token). Every other built-in provider has an entry. | 4.9 | 1018-1025 |
| 3 | CRD-032 | Secret shape table (lines 1018-1025) is missing a row for the `github` provider. Operators configuring `github` pools have no documentation of what Kubernetes Secret key(s) to use for the source material (GitHub App credentials / private key). Every other built-in provider has an entry. | 4.9 | 1018-1025 |

## Verification notes

Checked the following areas for issues; all were internally consistent:

- **LLM reverse proxy as bottleneck risk** (Section 4.9, lines 1281-1307): proxy has its own subsystem boundary with dedicated goroutine pool, concurrency limits, circuit breaker, and per-subsystem metrics. Circuit breaker open-state behavior is fully specified (immediate rejection, in-flight stream continuation, half-open retry). No bottleneck gap found.
- **Credential rotation mid-session** (Section 4.7, lines 723-734): Full-tier rotation protocol covers in-flight request completion gate, `credentials_acknowledged` 60s timeout with fallback to Standard-tier checkpoint/restart, old credential grace period. Standard/Minimum tier falls back to checkpoint-restart. Internally consistent.
- **Credential pool exhaustion handling** (Section 4.9, lines 1057-1061): pre-claim availability check prevents pod waste; race condition between check and assignment handled by releasing pod back to warm pool. `CREDENTIAL_POOL_EXHAUSTED` error code and metric (`lenny_gateway_credential_preclaim_mismatch_total`) both defined. Consistent with error catalog (line 6366).
- **Three credential modes** (Section 4.9, lines 1170-1180): pool, user-scoped, and fallback chain modes clearly defined. `preferredSource` semantics fully specified with all four values. User credential storage uses same envelope encryption as OAuth tokens. Internally consistent.
- **KMS integration completeness** (Section 4.9.1, lines 1489-1501): envelope encryption (AES-256-GCM) for Postgres-stored tokens, per-tenant DEK wrapped by KMS KEK, `key_version` column for multi-key support, re-encryption migration job, Redis cache invalidation on rotation. etcd encryption separately addressed in lines 1027-1036 with per-cloud-provider guidance. Complete.
- **Proactive lease renewal** (Section 4.9, lines 1255-1272): renewal heap design, renewal-does-not-consume-maxRotationsPerSession, retry on failure with expiresAt guard, fallback to fault rotation. Internally consistent.
- **Emergency credential revocation** (Section 4.9, lines 1413-1452): revocation endpoint, in-memory deny list propagated via Redis pub/sub, proxy-mode immediate rejection, direct-mode RotateCredentials RPC, audit events. Consistent with alert `CredentialCompromised` (line 7588).
- **Credential propagation through delegation** (Section 8.3, lines 3031-3064): three modes (inherit/independent/deny), per-hop semantics, worked example, pre-check at delegation time, fan-out guidance. Internally consistent.
- **SPIFFE-binding for proxy mode** (Section 4.9, lines 1320-1325): lease tokens bound to pod SPIFFE identity in multi-tenant mode, server-side enforcement, configurable disable for single-tenant. Complete.
- **Secret-per-credential topology** (Section 4.9, lines 1008-1014): revocation granularity, RBAC granularity, rotation isolation rationale. Naming convention and Tier 3 External Secrets Operator guidance present. Consistent.
- **Credential governance boundaries** (Section 4.9, lines 1389-1411): proxy vs direct mode comparison table covers credential injection, request visibility, interception, recommendation scope. Consistent.
