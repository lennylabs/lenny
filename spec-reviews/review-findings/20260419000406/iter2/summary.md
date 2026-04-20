# Technical Design Review Findings — 2026-04-19 (Iteration 2)

**Document reviewed:** `spec/` (28 files, ~17,991 lines)
**Review framework:** `spec-reviews/review-povs.md` (25 perspectives + Web Playground)
**Iteration:** 2 of 3
**Total findings:** 73 across 26 review perspectives

## Findings Summary

| Severity | Count |
| -------- | ----- |
| Critical | 1     |
| High     | 17    |
| Medium   | 29    |
| Low      | 26    |
| Info     | 0     |

---

## Critical Findings

### API-004 Two Cross-Referenced Error Codes Missing From §15.1 Catalog [Critical]
**Files:** `spec/15_external-api-surface.md` (catalog lines 543–633), `spec/08_recursive-delegation.md` (lines 636–637), `spec/12_storage-architecture.md` (lines 297, 301, 303, 978).

Two error codes are returned by `/v1/*` endpoints (and/or internal flows that surface errors to clients) but are **not listed** in the canonical error-code catalog in §15.1:

1. **`EXTENSION_COOL_OFF_ACTIVE`** — emitted by the extension-request path (`08_recursive-delegation.md` lines 636–637: "the gateway auto-rejects the request with `EXTENSION_COOL_OFF_ACTIVE`"; returned inside a transaction that commits/rolls back budget counters). This is a client-visible rejection reason that appears on lease-extension responses. It has no catalog row, no documented category, no HTTP status, and no `retryable` flag.

2. **`CLASSIFICATION_CONTROL_VIOLATION`** — emitted by:
   - `PUT /v1/admin/tenants/{id}` when the T4 KMS availability probe fails (`12_storage-architecture.md` line 301: "if the probe fails, the update is rejected with `CLASSIFICATION_CONTROL_VIOLATION`").
   - Artifact/checkpoint writes when the tenant-scoped KMS key is unavailable (line 303).
   - Storage-interface boundary when tier mismatches occur (line 978: "rejected at write time with a `CLASSIFICATION_CONTROL_VIOLATION` error").

   This is a first-class admin-API error on `PUT /v1/admin/tenants/{id}` (a visible, operator-facing failure path) and is not listed anywhere in §15.1.

**Impact:** Identical to API-001 in iter1. Section 15.2.1(d) mandates that every error response use a code from the shared taxonomy with identical `code`, `category`, and `retryable` values across REST and every adapter surface; contract tests cannot assert equivalence for codes that have no canonical catalog row. Third-party UIs, SDK generators, and operator runbooks reading §15.1 will not know these codes exist, their HTTP status, or whether they are retryable — leading to divergent client behavior across adapters.

**Recommendation:** Add two rows to the §15.1 error-code catalog, placed next to logically related entries:

- `EXTENSION_COOL_OFF_ACTIVE` | `POLICY` | 403 | "Lease-extension request auto-rejected because the requesting subtree is within its rejection cool-off window after a prior user-denied extension elicitation. `details.subtreeId` and `details.coolOffExpiresAt` are included. Not retryable until cool-off expires or an operator clears the extension-denied flag via `DELETE /v1/admin/trees/{rootSessionId}/subtrees/{sessionId}/extension-denial`. See [Section 8.x](08_recursive-delegation.md)." Place near `BUDGET_EXHAUSTED` (other delegation/budget rejection).

- `CLASSIFICATION_CONTROL_VIOLATION` | `POLICY` | 422 | "Operation rejected because a storage-tier classification control cannot be satisfied (e.g., tenant T4 KMS key unavailable during the admin-time availability probe or at artifact-write time; T4 data would be written to a store not configured for envelope encryption). Not retryable at the API layer — operator must restore KMS key availability or correct the tier/store configuration. See [Section 12.9](12_storage-architecture.md#129-data-classification)." Place near `COMPLIANCE_PGAUDIT_REQUIRED` / `COMPLIANCE_SIEM_REQUIRED` (other compliance-profile errors).

The HTTP status for `CLASSIFICATION_CONTROL_VIOLATION` at admin time should match the surrounding compliance family (422 for config-level rejection); the artifact-write path is internal and surfaces through the critical alert `CheckpointStorageUnavailable` rather than a client HTTP response, so a single 422 status row is sufficient.

---

## High Findings

### K8S-036 `lenny-pool-config-validator` webhook has two conflicting responsibility definitions [High]
**Files:** `04_system-components.md` §4.6.3 (line 578), `10_gateway-internals.md` §10.1 (lines 110–118)

The iter1 fix named the webhook `lenny-pool-config-validator` in §4.6.3 and added a matching regression-touched reference in §10.1. In doing so, it conflated two materially different validation duties under one webhook name, and the two descriptions now disagree on what the webhook actually does.

- §4.6.3 scopes the webhook to **authorization-based denial**: "rejects manual `kubectl edit` or `kubectl apply` updates to `SandboxTemplate.spec` and `SandboxWarmPool.spec` fields **unless the request's `userInfo` maps to the PoolScalingController ServiceAccount**". Under this definition, any write originating from the PoolScalingController SA bypasses the webhook entirely.
- §10.1 lines 110–118 asserts the same webhook enforces `max_tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30 > terminationGracePeriodSeconds` and the BarrierAck floor rule on pool configuration. These are semantic validation rules that must apply to **every** SSA apply of `SandboxWarmPool.spec` — including the PSC's reconciliation writes — otherwise the PSC can happily write a configuration that guarantees a SIGKILL during drain, and no admission gate will catch it.

If the webhook applies the §4.6.3 `userInfo` bypass to the §10.1 rules, the §10.1 validation is effectively dead code for the only writer that produces those fields. If the webhook applies the §10.1 rules universally, the §4.6.3 "unless userInfo maps to PSC" claim is false. Either way, one of the two sections is normatively wrong.

**Recommendation:** Split this into two separately named and separately scoped webhooks (e.g., keep `lenny-pool-config-validator` for the §10.1 semantic/budget rules applying to *all* writes, and introduce `lenny-pool-config-writer-guard` for the §4.6.3 `userInfo`-based manual-edit denial). Alternately, clarify in both sections that this is one webhook whose rule set is the *union* — §10.1 semantic rules always apply; the §4.6.3 `userInfo` check applies additionally only to the authorization path — and update the §16.5 `PoolConfigValidatorUnavailable` alert body to reflect both consequences (manual edits denied *and* pool reconciliation writes denied) during webhook outage.

---

### NET-047 Gateway label selector inconsistency silently breaks LLM upstream egress policy [High]
**Files:** `13_security-model.md` (line 314)

The `allow-gateway-egress-llm-upstream` NetworkPolicy selects gateway pods via `app: lenny-gateway`:

```yaml
podSelector:
  matchLabels:
    app: lenny-gateway
```

Every other NetworkPolicy in `13_security-model.md` that targets the gateway uses `lenny.dev/component: gateway` (lines 63, 91, 132, 201, 258, 285). The §13.2 lenny-system allow-list table (line 201) also identifies the gateway by `lenny.dev/component: gateway`. If the gateway Deployment carries the conventional label, the `app: lenny-gateway` selector silently matches zero pods and has no effect; `lenny-system`'s default-deny egress policy then blocks all LLM provider traffic. Because `NetworkPolicy` with no matching pods applies nothing (rather than denying), this fails silently — no diagnostic surface until proxy-mode requests start timing out in production.

**Recommendation:** Change line 314 to `lenny.dev/component: gateway`. Add a normative statement in §13.2 that all Lenny NetworkPolicy pod selectors use `lenny.dev/component` exclusively. Consider a preflight check that verifies each rendered NetworkPolicy selector matches at least one live pod.

---

### NET-048 Missing lenny-system ingress allow-rule for OTLP collector breaks default OTLP egress [High]
**Files:** `13_security-model.md` (lines 139–168, 197–207)

The `allow-pod-egress-otlp` policy in agent namespaces (line 147) defaults `observability.otlpNamespace: lenny-system` (line 158), authorising agent-pod egress *into* lenny-system. However, `lenny-system` has a default-deny policy (line 190) and the component-specific allow-list table (lines 201–207) enumerates ingress per component — gateway, Token Service, controller, PgBouncer, MinIO, admission webhooks, dedicated CoreDNS — **with no entry for an otel-collector component**.

Deployers following the default configuration and deploying an `otel-collector` (matching `app: otel-collector` per line 161) in lenny-system will have all traces silently dropped at lenny-system's ingress. Symptom: missing OTel traces with no NetworkPolicy drop counter visible on the agent side (the agent egress appears correctly configured), making this extremely hard to diagnose.

**Recommendation:** Either (a) add an `otel-collector` row to the §13.2 allow-list table (`lenny.dev/component: otel-collector`, ingress from `.Values.agentNamespaces` on `{{ .Values.observability.otlpPort }}`), rendered conditionally when `observability.otlpEndpoint` is set and `otlpNamespace == lenny-system`; or (b) default `otlpNamespace` to a separate `observability` namespace. Add a preflight check that fails when an in-cluster OTLP target lacks a matching ingress allow-rule.

---

### NET-050 lenny-ops egress uses wrong gateway selector and omits namespaceSelector [High]
**Files:** `25_agent-operability.md` (line 1103)

```yaml
egress:
  - to:
      - podSelector: { matchLabels: { app: lenny-gateway } }
    ports: [{ protocol: TCP, port: 8080 }]
```

Same defect class as NET-047: the selector is `app: lenny-gateway` rather than the `lenny.dev/component: gateway` convention. If the gateway pods carry only the conventional label, this egress rule matches zero pods and `lenny-ops` cannot reach the gateway — breaking operability entirely.

Additionally, the rule omits `namespaceSelector`, so the `podSelector` only applies within `lenny-ops`'s own namespace. If `lenny-ops` runs in `lenny-system` this accidentally works, but line 1130 explicitly supports `lenny-ops` in a separate namespace for "tenant workload isolation"; in that configuration the rule matches zero pods regardless of the label inconsistency.

**Recommendation:** Change selector to `lenny.dev/component: gateway` and add an explicit `namespaceSelector: { matchLabels: { kubernetes.io/metadata.name: lenny-system } }` on the `to:` clause. Audit both `lenny-ops-egress` and `lenny-ops-ingress` for same-namespace assumptions.

---

### PRT-005 `ExternalProtocolAdapter` Interface References Undefined Types [High]
**Files:** `spec/15_external-api-surface.md` (Section 15, lines 10–39, 83, 1902)

The `ExternalProtocolAdapter` interface — the load-bearing contract that third-party adapters must implement — references four types that are **never structurally defined anywhere in the specification**:

- `SessionMetadata` (line 23) — `OnSessionCreated` argument
- `SessionEvent` (lines 24, 36, 81, 111, 116) — `OnSessionEvent`, `OutboundChannel.Send`, `SupportedEventKinds`
- `TerminationReason` (lines 25, 1902) — `OnSessionTerminated`, Runtime SDK `OnTerminate`
- `AuthorizedRuntime` (line 16) — slice element passed to `HandleDiscovery`

Full-repo search confirms zero struct definitions, zero field enumerations, and no JSON-Schema / protobuf mirrors. The §15.4 machine-readable artifacts cover only the runtime-adapter contract.

Consequences for third-party `ExternalProtocolAdapter` implementors (admin-API-registered tier, §15 line 168): they cannot determine (a) which session lifecycle fields `OnSessionCreated` delivers; (b) the event-kind vocabulary `SessionEvent.Kind` carries (line 83 lists six "well-known" kinds but nothing states the set is closed); (c) what termination reasons must map to each protocol's terminal state; or (d) which per-runtime fields `HandleDiscovery` may surface. `RegisterAdapterUnderTest` (line 893) cannot assert against unspecified types. §21.1's `A2AAdapter` leans on `SessionEvent` repeatedly (`OpenOutboundChannel`, `Send`, `SupportedEventKinds`) with no schema to bind to.

**Recommendation:** In §15, after the `BaseAdapter` paragraph (line 158), add normative Go struct definitions for all four types with field-level commentary matching `AdapterCapabilities`. Minimum shape: `SessionEvent{Kind (closed enum), SeqNum, Payload, Timestamp}`; `TerminationReason` closed enum over `{completed, failed, cancelled, expired, drained}` plus `Detail`; `SessionMetadata{TenantID, SessionID, RuntimeName, DelegationDepth, CallerIdentity, NegotiatedProtocolVersion}`; `AuthorizedRuntime` mirrors the `GET /v1/runtimes` element (name, `agentInterface`, `mcpEndpoint`, `adapterCapabilities`, visibility-filtered `publishedMetadata` refs). Cross-reference from §15.4.1 fidelity matrix, §21.1 A2A outbound push, and §25 agent operability.

---

### DXP-001 Runtime-author onboarding still routes to Tier 1 after Tier 0 became primary [High]
**Files:** `15_external-api-surface.md` §15.4.5 step 6 (line 1797); `17_deployment-topology.md` §17.4 (lines 98–165, 232–261); `23_competitive-landscape.md` lines 123, 127

§17.4 declares Tier 0 (`lenny up`) "the primary path for deployers evaluating or using Lenny" and names runtime authors as Tier 0's intended audience (line 108). It is the only local mode that ships reference runtimes and exercises the real Kubernetes code path. Three onboarding surfaces still point Basic-level authors at Tier 1 only: §15.4.5 step 6 ("Use `make run`"), §23.2 persona table (line 123, "`make run` local dev mode"), and §23.2 TTHW (line 127, "clone the repo, run `make run`").

Worse, **there is no documented path for plugging a custom runtime into Tier 0.** §17.4 "Plugging in a custom runtime" (lines 232–261) covers only Tier 1 and Tier 2. A Basic-level author following the primary recommendation hits a wall.

**Recommendation:** (a) Add a Tier 0 case to "Plugging in a custom runtime" showing registration against the embedded gateway. (b) Update §15.4.5 step 6 and §23.2 persona/TTHW text to list both modes with pick-guidance.

---

### DXP-002 "MUST start from the scaffolder" contradicts Basic-level zero-SDK claims [High]
**Files:** `26_reference-runtime-catalog.md` §26.1 (line 6); `15_external-api-surface.md` §15.4.1 (line 1100), §15.4.3 (lines 1501, 1584)

§26.1: "Teams building their own runtimes MUST start from the scaffolder (`lenny-ctl runtime init`, §24.18)." The scaffolder generates skeletons built on the Go/Python/TypeScript Runtime Author SDKs. This contradicts §15.4.1 line 1100 ("**No SDK required**"), §15.4.3 line 1501 ("Zero Lenny knowledge required"), and §15.4.3 line 1584 ("Basic level prioritizes simplicity and zero Lenny knowledge").

Consequences: (1) a runtime author in a language the SDK does not support (Rust, Java, Ruby) sees an unsatisfiable MUST — §24.18 offers only `{go|python|typescript|binary}`. (2) The "zero Lenny knowledge" promise is negated by a mandatory Lenny CLI + template.

**Recommendation:** Soften §26.1 to: "Teams building Standard- or Full-level runtimes SHOULD start from the scaffolder. Basic-level runtimes MAY implement the stdin/stdout protocol directly (see §15.4.4)." Also confirm §24.18's `binary`/`minimal` template emits a Basic-level-compliant skeleton with no SDK imports, and document that explicitly.

---

### TNT-002 `noEnvironmentPolicy` omission semantics contradict the §10.3 startup gate [High]
**Files:** `spec/10_gateway-internals.md` (lines 274, 536), `spec/11_policy-and-controls.md` (line 13)

§10.6 line 536 still states: "an omitted `noEnvironmentPolicy` field — whether at the platform level (Helm) or at the tenant level (admin API) — MUST be treated as `deny-all` by the gateway." This directly contradicts the §10.3 table row (line 274) which mandates the opposite for the platform branch: "the gateway does not infer a default at runtime — the value must reach the gateway as an explicit setting so that a misconfigured chart (with the default stripped) fails closed at startup." An implementer following §10.6 treats a missing Helm value as `deny-all`; §10.3 requires refusing to start. The two rules disagree on the platform-level branch of the `OR`. This is a regression the TNT-001 fix introduced without updating §10.6.

**Recommendation:** In §10.6 line 536, split platform- and tenant-level behavior: "At the **tenant** level, an omitted `noEnvironmentPolicy` MUST be treated as `deny-all`. At the **platform** level, an omitted value is a fatal startup error — see [§10.3](#103-mtls-pki)." This preserves tenant-level forgiveness (needed for backward-compatible tenant creation via admin API) while aligning with the §10.3 startup guard.

---

### STR-005 `CLASSIFICATION_CONTROL_VIOLATION` referenced but not defined in §15.1 catalog [High]
**Files:** `/Users/joan/projects/lenny/spec/12_storage-architecture.md` (§12.5, lines 297, 301, 303), `/Users/joan/projects/lenny/spec/15_external-api-surface.md` (§15.1 error catalog, lines 541–633)

The iter1 STR-003 fix cites `CLASSIFICATION_CONTROL_VIOLATION` three times in §12.5:

1. **Runtime write rejection** (line 297): returned "if the tenant-scoped KMS key is unavailable" at first-artifact-write time.
2. **Admin-time T4 promotion probe** (line 301): "the update is rejected with `CLASSIFICATION_CONTROL_VIOLATION` and the tenant remains at its prior tier."
3. **Post-promotion write rejection** (line 303): "the gateway MUST reject the write with `CLASSIFICATION_CONTROL_VIOLATION`. The `ArtifactStore` does **not** fall back to the deployment-wide SSE key..."

This error code is **not present** in the §15.1 catalog table (lines 541–633) — a direct regression pattern matching iter1 API-001 (`TENANT_SUSPENDED` missing). It violates the §15.2.1(d) contract test (line 871: "All error responses — REST and MCP — use the error categories defined in [Section 16.3]"), which requires every wire error code be cataloged with category, HTTP status, and description so SDKs and conformance tests can discover it.

Impact: admin callers of `PUT /v1/admin/tenants/{id}` and runtime checkpoint paths receive an undocumented wire code with no discoverable category, retryability, or remediation.

**Recommendation:** Add `CLASSIFICATION_CONTROL_VIOLATION` to the §15.1 catalog. Suggested single-row entry:

| Code | Category | HTTP Status | Description |
| --- | --- | --- | --- |
| `CLASSIFICATION_CONTROL_VIOLATION` | `POLICY` | 422 (admin-time) / 503 (runtime write) | Tenant classification control could not be enforced. Returned on (a) `PUT /v1/admin/tenants/{id}` when the online KMS availability probe fails while setting `workspaceTier: T4` (tenant remains at prior tier); and (b) runtime `ArtifactStore` writes when the tenant's per-tenant KMS key (`tenant:{tenant_id}`) is unavailable and fallback to the shared SSE key is forbidden. `details.tenantId`, `details.kmsKeyRef`, and `details.probeError` are included. See [Section 12.5](12_storage-architecture.md#125-artifact-store) (T4 per-tenant KMS key lifecycle). |

Alternatively, split into `KMS_PROBE_FAILED` (422-admin) / `KMS_KEY_UNAVAILABLE` (503-runtime) to align with the existing `KMS_REGION_UNRESOLVABLE` (422) vs. `REGION_UNAVAILABLE` (503) precedent. Either choice must update the three §12.5 citations.

---

### DEL-006 `EXTENSION_COOL_OFF_ACTIVE` error code undefined in §15.1 catalog [High]
**Files:** `08_recursive-delegation.md` §8.6 (lines 636-637); `15_external-api-surface.md` §15.1 (error table around lines 560-633)

The DEL-004 fix introduced the wire error code `EXTENSION_COOL_OFF_ACTIVE`, used in two distinct rejection paths:

- Line 636: handoff-safe path — "the gateway auto-rejects the request with `EXTENSION_COOL_OFF_ACTIVE` and does not enter the elicitation path."
- Line 637: atomic transaction path — "the gateway rolls back the budget increment and returns `EXTENSION_COOL_OFF_ACTIVE` instead of `GRANTED`."

However, `EXTENSION_COOL_OFF_ACTIVE` is **not** present in the §15.1 error catalog. A full-text search of `15_external-api-surface.md` returns zero matches for the identifier. This is a regression against the catalog-completeness convention used for every other delegation error (`DELEGATION_POLICY_WEAKENING`, `CONTENT_POLICY_WEAKENING`, `CREDENTIAL_PROVIDER_MISMATCH`, `BUDGET_EXHAUSTED`, `ISOLATION_MONOTONICITY_VIOLATED`, `DELEGATION_CYCLE_DETECTED` are all catalogued with category, HTTP status, and `details` fields).

Consequences:

1. Clients and SDK generators have no canonical HTTP status, category (`POLICY` vs `TRANSIENT`), or `retryable` flag for this error.
2. Adapters cannot distinguish `EXTENSION_COOL_OFF_ACTIVE` from `BUDGET_EXHAUSTED` (both reject extensions) without spec-defined semantics — yet their retry behavior differs (cool-off is time-bounded; `BUDGET_EXHAUSTED` is terminal absent extension).
3. No `details` schema is defined, so clients cannot surface the `cool_off_expiry` timestamp to the end user ("retry in N seconds") that the durability section painstakingly establishes.

**Recommendation:** Add `EXTENSION_COOL_OFF_ACTIVE` to the §15.1 error catalog with:
- `category: POLICY`, `status: 429` (consistent with `BUDGET_EXHAUSTED` and `EVAL_QUOTA_EXCEEDED`, which are the closest rate-style siblings);
- `retryable: true` after `cool_off_expiry`;
- `details.rootSessionId`, `details.subtreeSessionId`, `details.coolOffExpiry` (RFC 3339 UTC timestamp), `details.rejectionCoolOffSeconds`;
- cross-link to §8.6 and to the admin clear endpoint `DELETE /v1/admin/trees/{rootSessionId}/subtrees/{sessionId}/extension-denial` (§15.1 line 441).

---

### OBS-007 Alert-Referenced Metrics Missing From §16.1 Registry [High]

**Files:** `16_observability.md`

Multiple alerts in §16.5 reference metrics that never appear as first-class entries in the §16.1 metrics table. Because §16.1 is declared the canonical registry and §16.1.1 declares the attribute-naming table "single source of truth," any alert that consumes an unregistered metric breaks the discoverability contract:

- `GatewaySessionBudgetNearExhaustion` (line 351) uses `lenny_gateway_active_sessions` — not present in §16.1; the closest entry is "Active sessions (by runtime, pool, state, tenant)" (line 7) which has no metric identifier.
- `KMSSigningUnavailable` (line 366) uses `lenny_gateway_kms_signing_errors_total` — not in §16.1.
- `SDKConnectTimeout` (line 372) uses `lenny_warmpool_sdk_connect_timeout_total` — not in §16.1.
- `CRDSSAConflictStuck` (line 400) uses `lenny_crd_ssa_conflict_total` — not in §16.1.
- `DataResidencyViolationAttempt` (line 336) uses `lenny_data_residency_violation_total` — not in §16.1.
- `SessionEvictionTotalLoss` (line 338) uses `lenny_session_eviction_total_loss_total` — not in §16.1.
- `NetworkPolicyCIDRDrift` (line 331) uses `lenny_network_policy_cidr_drift_total` — not in §16.1.
- `BillingStreamEntryAgeHigh` (line 340) uses `lenny_billing_redis_stream_oldest_entry_age_seconds` — not in §16.1.

**Recommendation:** Add each metric name, type, and label set to §16.1 with a cross-reference to the alert. Either add a new sub-block (e.g., "Gateway Capacity", "KMS", "SDK Warm", "CRD Ownership", "Data Residency", "Network Drift") or inline each in the existing relevant block. These are regressions of OBS-002 for metrics introduced or renamed after iter1.

---

### OBS-008 Delegation Tree Size Metric Still Unnamed [High]

**Files:** `16_observability.md`

Line 25 still reads `Delegation tree size distribution | Histogram` with no metric name. The sibling on line 24 was correctly named `lenny_delegation_depth`; this entry was missed. Any alert or dashboard that wants to correlate tree breadth with depth cannot reference a stable identifier.

**Recommendation:** Assign a metric name (e.g., `lenny_delegation_tree_size`, histogram labeled by `pool` — observed at tree completion, counting total nodes in the completed tree) with a cross-reference to §8.

---

### CMP-044 GDPR Erasure Not Propagated to Backups [High]
**Files:** `12_storage-architecture.md` (§12.8 erasure scope and erasure propagation), `25_agent-operability.md` (§25.11 Backup and Restore API)

Section 12.8 specifies `DeleteByUser`/`DeleteByTenant` across every runtime store and propagates an `erasure.requested` event to SIEM and billing sinks, but the erasure scope table is silent on **backups**. Section 25.11 defines daily full Postgres and MinIO backups (including `SessionStore`, `EventStore` billing, `MemoryStore`, `UserStore`, `TokenStore`, artifacts) retained up to `retainDays: 90` at Tier 3. `pg_restore` reads the archive verbatim with no per-user or per-tenant filtering.

Consequences:

1. **Latent personal data retention.** A user erased on Day 1 is still present in every backup taken before erasure until the retention window elapses (up to 90 days at Tier 3). This sits inside the window during which the ICO, CNIL, and other DPAs treat personal data in backups as still under GDPR Article 17 absent explicit controls (documented retention policy, crypto-shredding, or "brought forward at first restore").
2. **Resurrection on restore.** `POST /v1/admin/restore/execute` pulls back the entire archive. After a restore, an erased user's sessions, memories, OAuth tokens, billing events with the original `user_id` (the per-tenant `erasure_salt` was destroyed — re-identification is now theoretically impossible, but the raw `user_id` is back on the row), and audit rows are resurrected. `processing_restricted` is also reverted. There is no post-restore reconciler that replays completed erasures — the `gdpr.*` receipts survive the restore under `audit.gdprRetentionDays` (7y) and would be usable for this, but no mechanism consumes them.
3. **HIPAA §164.530(c)(1) collision.** A restore that resurrects an erased patient's PHI is an unauthorized disclosure under 45 C.F.R. §164.502.

This is a material new finding not covered by CMP-042/043.

**Recommendation:** Add a "Backups in erasure scope" subsection to §12.8. Prefer a **post-restore reconciler** that, between `restore_completed` and the gateway restart, scans `audit_log` for `gdpr.*` completion receipts with `completed_at > backupTakenAt` and replays `DeleteByUser`/`DeleteByTenant` for each affected subject against the restored databases (the receipts survive restore under the 7-year retention). As alternative fallbacks: per-tenant backup crypto-shredding (tenant-scoped wrap key destroyed on `DeleteByTenant` — tenant-level only), or documenting that `backups.retainDays` must be ≤ the GDPR erasure SLA (72h for T3, 1h for T4). The reconciler path is the only one that satisfies per-user erasure without forcing short retention, and should be the default.

---

### CMP-045 Backup Storage Ignores Data Residency [High]
**Files:** `12_storage-architecture.md` (§12.8 Data residency, Multi-region reference architecture), `25_agent-operability.md` (§25.11 Backup MinIO layout and KMS configuration)

Section 12.8 enforces `dataResidencyRegion` at pod routing, storage routing (Postgres, MinIO, Redis, KMS), and session admission — failing closed with `REGION_CONSTRAINT_UNRESOLVABLE` on unresolvable regions, and prohibiting cross-region transfer of T4 data.

§25.11 defines a single MinIO backup location (`backups/{type}/{id}/{timestamp}.tar.gz.enc`) accessed via a single `lenny-backup-minio` credential. `backups.encryption.kmsKeyId` is a single scalar, not a per-region map. `backups.*` has no per-region variant analogous to `storage.regions.<region>.*`. The `pg_dump` flow is a full-shard dump via `AllSessionShards()` — in a multi-region topology this aggregates every region's rows into one archive.

Consequences:

1. In a multi-region deployment (e.g., `eu-west-1` + `us-east-1`), EU tenant data (T3, subject to `dataResidencyRegion: eu-west-1`) is dumped and written to a MinIO bucket whose endpoint is not region-constrained. If the bucket is US-hosted (likely, given `minio.endpoint` is a single scalar), this is a prohibited cross-border transfer of T3 data under §12.8 rules and a GDPR Article 44-46 transfer without a documented legal basis.
2. The KMS decrypt capability for backup archives is single-region. Per §12.8 "KMS key residency," cross-region decrypt capability IS a cross-border transfer even if the encrypted bytes stay in-region.
3. The multi-region reference architecture says "one Lenny control plane per region" but makes no statement on whether each region has its own `lenny-ops` + backup pipeline. The spec is silent on the only correct configuration (per-region backup pipeline + per-region backup bucket + per-region KMS).

**Recommendation:** Extend `backups.*` to per-region maps consistent with `storage.regions.<region>.*`: require `backups.regions.<region>.{minioEndpoint,kmsKeyId,accessCredentialSecret}` when any tenant has `dataResidencyRegion` set. `lenny-ops` must route per-shard dumps to their region's backup endpoint (per-region `pg_dump`, not a global dump) and reject configurations that would cross regions. Add a `BackupRegionUnresolvable` fail-closed path mirroring `REGION_CONSTRAINT_UNRESOLVABLE`, emit a `DataResidencyViolationAttempt` audit event when a backup write would cross regions, and document the per-region backup pipeline as part of the multi-region reference architecture.

---

### FLR-002 Gateway Deployment Lacks Concrete PDB and RollingUpdate Strategy [High]
**Files:** `17_deployment-topology.md` (line 7, line 89), `10_gateway-internals.md` (line 130)

The gateway — the single external-facing component and coordinator of all session state — specifies neither a concrete `PodDisruptionBudget` value nor a rolling-update strategy. Other components are explicit: Token Service `minAvailable: 1` (17:8), PgBouncer `minAvailable: 1` (12:44), lenny-ops `minAvailable: 1` (17:15), admission webhook `minAvailable: 1` (17:42). lenny-ops even pins `maxUnavailable: 0, maxSurge: 1` (25:3272). For the gateway, 17:7 says only "HPA, PDB, multi-zone, topology spread" with no value. No `maxUnavailable`/`maxSurge` is stipulated, so the Kubernetes default (`25%/25%`) applies.

Consequence: at Tier 3 with ~20 replicas and default `maxUnavailable: 25%`, a rolling upgrade drains up to 5 gateway replicas concurrently. Stage 2 of the preStop hook (10:100–108) fans out CheckpointBarrier to up to 400 pods per replica (10:130), so five concurrent drains can trigger up to **2,000 simultaneous MinIO checkpoint uploads** — far beyond the "400 simultaneous uploads" budget documented in 10:130 and the MinIO throughput budget in 17.8.2. Without a PDB floor, a node drain or evict-all automation can also take every gateway replica offline at once.

**Recommendation:** Add concrete PDB at 17:7 (e.g., `minAvailable: 2` Tier 1/2; `minAvailable: ceil(replicas/2)` Tier 3). Specify `RollingUpdate` with `maxUnavailable: 1, maxSurge: 25%` so the CheckpointBarrier fan-out stays within the MinIO budget. Cross-reference 10:130.

---

### CNT-002 WorkspacePlan JSON Schema Coverage Claim Contradicts the Canonical Example [High]

**Files:** `14_workspace-plan-schema.md`

**Description:** Section 14.1 declares that the published JSON Schema at `https://schemas.lenny.dev/workspaceplan/v1.json` covers the full `WorkspacePlan` object, enumerating: `sources[]`, `setupCommands[]`, `env`, `labels`, `timeouts`, `retryPolicy`, `credentialPolicy`, `callbackUrl`, `callbackSecret`, `runtimeOptions`, and `delegationLease`.

However, the canonical request example in the same section (lines 7–81) places **only** `$schema`, `schemaVersion`, `sources`, and `setupCommands` **inside** the `workspacePlan` object. All other listed fields (`env`, `labels`, `runtimeOptions`, `timeouts`, `retryPolicy`, `credentialPolicy`, `callbackUrl`, `callbackSecret`, `delegationLease`) are **siblings** of `workspacePlan` at the top-level request body.

**Quote from spec (§14 example, abbreviated):**
```json
{
  "pool": "...",
  "isolationProfile": "gvisor",
  "workspacePlan": {
    "$schema": "...",
    "schemaVersion": 1,
    "sources": [...],
    "setupCommands": [...]
  },
  "env": { ... },
  "labels": { ... },
  "runtimeOptions": { ... },
  "timeouts": { ... },
  "retryPolicy": { ... },
  "credentialPolicy": { ... },
  "callbackUrl": "...",
  "callbackSecret": "...",
  "delegationLease": { ... }
}
```

Section 14.1 states the JSON Schema "covers" all these fields as part of the `WorkspacePlan` object, but then says the gateway "performs identical validation at `POST /v1/sessions`" — i.e., validates against the full request body, not just the `workspacePlan` sub-object. The terminology conflates two distinct envelopes: the **`WorkspacePlan` proper** (sources, setupCommands, schemaVersion) and the **session-creation request body** that embeds it (env, labels, timeouts, callback*, delegationLease, runtimeOptions, etc.).

**Impact:** (1) Clients implementing local validation against the published schema will not know which fields the schema describes. (2) `WORKSPACE_PLAN_INVALID` (§15.4 error table) and `WORKSPACE_PLAN_SCHEMA_UNSUPPORTED` (§14.1 versioning) become ambiguous — do they apply to the whole request body or just the inner plan? (3) The `$schema` keyword is described as reference-able "on their `workspacePlan` object", which is inconsistent with a schema that covers outer fields like `callbackUrl`.

**Recommendation:** Either (a) move `env`, `labels`, `timeouts`, `retryPolicy`, `credentialPolicy`, `callbackUrl`, `callbackSecret`, `runtimeOptions`, `delegationLease` **inside** the `workspacePlan` object in the example so the structure matches the schema claim, or (b) rename the outer envelope (e.g., `CreateSessionRequest`) and clarify that the published schema covers only the inner `WorkspacePlan` sub-object (sources, setupCommands, schemaVersion), with sibling fields documented under a separate envelope schema. Option (b) better matches common REST patterns and the shape shown in §26.2 where only `sources`/`setupCommands` live under `workspacePlan`.

---

### WPP-005 Idle-timeout override and duration cap do not bind `apiKey` / `dev` playground sessions [High]

**Files:** `27_web-playground.md:37,49–50,144–146`; `06_warm-pod-model.md:261`.

§27.6 enforces the 5-min idle override "whenever the session was established through the playground bearer-exchange path (detected via the `origin: "playground"` JWT claim minted in §27.3.1)." §27.3.1 is explicitly `oidc`-only (step 1 heading "Login and cookie issuance (`playground.authMode=oidc` only)"). The `origin: "playground"` claim is therefore minted **only** in OIDC mode.

In `apiKey` mode (§27.3:49) the browser presents a user-supplied API key on every request; the session JWT produced flows through the standard gateway auth chain (§10.2), which does not stamp `origin: "playground"`. `dev` mode mints a dev-grade token with no auth, again without the claim. Consequences:

1. **Idle override never fires** for `apiKey` / `dev` sessions — they inherit the runtime default `maxIdleTimeSeconds` (600s per §6.2 / §17.8), not 300s. The iter1 WPP-004 fix is silently mode-scoped.
2. **`playground.maxSessionMinutes = 30` cap** (§27.6 bullet 1) has no defined enforcement path keyed off the `origin` claim either — §27.2 wording says "playground-initiated sessions", but the only detector the spec provides is the claim. Same gap.
3. **§27.8 dashboard slice** — "`origin=playground` session label is the primary way to slice dashboards" — also misses `apiKey` / `dev` sessions unless the label is applied by a path the spec does not document for non-OIDC modes.

This is a cross-section gap introduced by iter1 WPP-004's fix, not a fresh design issue.

**Recommendation.** Pick one and apply consistently:
- **Preferred:** mint the `origin: "playground"` claim on **every** session token produced for a `/playground/*`-originated request, regardless of `authMode`. The `/playground/*` handler attaches the claim to the downstream mint (apiKey: after API-key validation; dev: on the dev HMAC token). Then §27.6's override, §27.2's duration cap, and §27.8's label slice work uniformly.
- **Alternative:** key the override and label on the request-ingress path (a `session.origin_playground` flag set at session-create time from the `/playground/*` route) rather than the JWT claim. Update §27.6 and §6.2:261 to cite the ingress signal.

Update §27.3 to state which modes carry the claim/label and under what mechanism; align §27.6 text with that coverage.

---

## Medium Findings

### K8S-037 `templates/admission-policies/` enumeration missed by the iter1 fix [Medium]

**Files:** `17_deployment-topology.md` §17.2 (line 40), `13_security-model.md` §13.2 (line 206)

The iter1 K8S-035 fix note explicitly identified a "deployment gap" — that the Helm `templates/admission-policies/` enumeration in §17.2 line 40 omitted the Postgres-authoritative-state webhook — but the fix did not update that enumeration. The §17.2 list still stops at five items: (1) PSS runc enforcement, (2) PSS gVisor/Kata relaxed enforcement, (3) `POD_SPEC_HOST_SHARING_FORBIDDEN`, (4) label-based namespace targeting, (5) `lenny-label-immutability`. The webhook inventory is additionally contradicted by §13.2 line 206, which names `lenny-label-immutability`, `lenny-direct-mode-isolation`, `lenny-sandboxclaim-guard`, and "CRD validation webhooks" as living in the `admission-webhook` component — none of which (except label-immutability) appear in the §17.2 enumeration. `lenny-pool-config-validator`, `lenny-data-residency-validator`, `lenny-direct-mode-isolation`, and `lenny-sandboxclaim-guard` are all specified elsewhere as Helm-deployed webhooks (e.g., §4.6.1 says `lenny-sandboxclaim-guard` is "deployed as part of the Helm chart under `templates/admission-policies/`"), but the canonical §17.2 enumeration lists none of them.

This creates two concrete risks: (1) a Helm-chart author reading §17.2 as the source of truth will ship an incomplete chart that silently omits fail-closed webhooks whose absence would go undetected (no preflight check covers these webhooks specifically), and (2) the `admissionController.replicas: 2` + `podDisruptionBudget.minAvailable: 1` HA requirement in §17.2 line 42 is stated only for "RuntimeClass-aware admission policy webhooks", leaving it ambiguous whether the same HA is required for the five unenumerated webhooks (§4.6.1 states it for `lenny-sandboxclaim-guard`, §13.2 line 178 states it for `lenny-label-immutability`, but §4.6.3 does not state it for `lenny-pool-config-validator`).

**Recommendation:** Expand §17.2 line 40 to enumerate every webhook shipped under `templates/admission-policies/` (at minimum: `lenny-label-immutability`, `lenny-direct-mode-isolation`, `lenny-sandboxclaim-guard`, `lenny-data-residency-validator`, `lenny-pool-config-validator`, plus CRD conversion webhooks if they live in the same template directory). Explicitly state — either in §17.2 or per-webhook — that the `replicas: 2` + `PDB minAvailable: 1` HA requirement applies to all of them. Add a `lenny-preflight` check that enumerates the deployed `ValidatingWebhookConfiguration` resources and verifies the expected set is present, so a missing webhook fails the install rather than silently shipping.

---

### SEC-001 Derive / replay bypasses isolation monotonicity [Medium]

**Files:** `spec/07_session-lifecycle.md` §7.1 (lines 81–96, "Derive copy semantics" / "Derive session semantics"); `spec/15_external-api-surface.md` §15.1 (lines 204, 309–324, 602–608); `spec/08_recursive-delegation.md` §8.3 (line 251).

**Issue.** §8.3 requires that a delegated child's pool use an isolation profile **at least as restrictive** as the parent (`standard < sandboxed < microvm`), enforced by `minIsolationProfile` on the lease and rejected with `ISOLATION_MONOTONICITY_VIOLATED`. Derive (`POST /v1/sessions/{id}/derive`) and replay (`POST /v1/sessions/{id}/replay` with `replayMode: workspace_derive`) reproduce the delegation-like data-flow outcome — they copy the source session's workspace snapshot into a **new** session — but the documented rules do not include any isolation-profile check:

- §7.1 "Derive session semantics" enumerates allowed source states, concurrent-derive serialization, credential lease handling, and connector state, but says nothing about `targetPool` / `targetRuntime` isolation.
- §15.1 replay defines `targetRuntime` as "required … Must be a registered runtime with the same `executionMode` as the source session" and notes `INCOMPATIBLE_RUNTIME` only on `executionMode` mismatch. No isolation-profile compatibility is asserted.
- The error catalog (§15.1 lines 606–608) for derive lists `DERIVE_ON_LIVE_SESSION`, `DERIVE_LOCK_CONTENTION`, `DERIVE_SNAPSHOT_UNAVAILABLE`, but no isolation error.

A user with access to a `microvm` (Kata) session that has processed sensitive material can call `POST /v1/sessions/{id}/derive` (or replay with `workspace_derive`) targeting a `standard` (runc) pool; the derived session inherits the full workspace tar — including secrets in workspace files, partial tool outputs, and `.env`-style artifacts — but runs under weaker kernel isolation. This sidesteps the threat model that motivated the original `microvm` placement.

**Recommendation.** Apply an equivalent isolation-monotonicity gate at derive/replay time. Add to §7.1 derive semantics: "the target pool's isolation profile MUST be at least as restrictive as the source session's `sessionIsolationLevel.isolationProfile`, else reject with `ISOLATION_MONOTONICITY_VIOLATED`." Optionally accept an explicit `allowIsolationDowngrade: true` flag that requires `platform-admin` or emits an audit event (`derive.isolation_downgrade`). Update §15.1 error catalog to list `ISOLATION_MONOTONICITY_VIOLATED` as a possible response for the derive and replay endpoints, and mirror the `pool.isolation_warning` audit event from §11.7 for affected derives.

---

### SEC-002 `shareProcessNamespace` requirement in §4.4 contradicts §13.1 blanket prohibition [Medium]

**Files:** `spec/04_system-components.md` §4.4 line 246 ("SIGCONT confirmation"); `spec/13_security-model.md` §13.1 lines 16–23.

**Issue.** §4.4 SIGCONT confirmation states: *"On Linux, `/proc/{pid}/stat` is only available when `shareProcessNamespace: true` is set on the pod spec; the embedded adapter mode requires this setting for SIGCONT confirmation to function."* §13.1 is categorical in the opposite direction: `shareProcessNamespace: true` is **forbidden** on every pod template Lenny generates, the admission webhook rejects any CR that would produce such a pod with `POD_SPEC_HOST_SHARING_FORBIDDEN`, and the startup preflight Job hard-fails in production if any Lenny-managed pod template has it set. There is no carve-out for agent pods using the embedded adapter.

Two problems:

1. **Factual error.** The embedded adapter, by its own definition (§4.4 line 242, §4.7 line 812–826), runs in the **same container** as the agent process. Same-container processes share the container's PID namespace irrespective of `shareProcessNamespace`, and `/proc/<adapter_self_view>/<pid>/stat` is readable without any pod-spec change. The cited prerequisite is not required for the described polling path. This misstates the kernel/cgroup requirement and will mislead implementers.

2. **If §4.4 is taken literally**, then SIGCONT confirmation polling is *unreachable* in any conformant Lenny deployment: admission blocks the required pod shape, the adapter falls through to the `sigcont_confirmation_unavailable` warning path, and liveness detection degrades silently to the 60-second watchdog on every embedded-adapter pod. That undermines the §4.4 liveness-integration claim that `checkpointStuck` is set "immediately … avoids waiting for the full 60-second watchdog timeout."

Either way the spec ships an internally inconsistent security-critical statement.

**Recommendation.** Correct §4.4 to reflect that same-container `/proc/{pid}/stat` access does not require `shareProcessNamespace: true`, and remove the conditional fallback text. If any variant of embedded adapter does run in a sibling container, document it in §13.1 as an explicit, admission-webhook-whitelisted exception with a threat-model justification covering Token Service cache exposure.

---

### NET-049 allow-ingress-controller-to-gateway lacks podSelector, admits all pods in the ingress namespace [Medium]

**Files:** `13_security-model.md` (lines 249–268)

The `allow-ingress-controller-to-gateway` NetworkPolicy identifies the source only by `namespaceSelector` (`kubernetes.io/metadata.name: ingress-nginx`) with no `podSelector` clause. Any pod in that namespace — sidecar, metrics exporter, cert-manager validation pod, debug container, or the ingress controller's own admission webhook — can reach the gateway's external TLS listener on TCP 443 directly, bypassing the ingress controller's authentication, rate limiting, WAF rules, and header normalization.

This deviates from the gateway-centric model's defense-in-depth posture: the gateway's 443 listener is designed to receive ingress-mediated, authenticated requests; co-located workloads can bypass the controller's defensive perimeter.

**Recommendation:** Add a `podSelector` to the `from:` clause that matches the actual ingress controller pod label (e.g., `app.kubernetes.io/name: ingress-nginx` for ingress-nginx, configurable via new Helm values `ingress.controllerPodLabel`/`Value`). Extend the NET-038 preflight check (line 270) to validate at least one pod in the configured namespace matches the configured label.

---

### PRF-002 Tier 3 "new sessions/s" Figure in §12.4 Uses Tier 4 Number [Medium]

**Files:** `12_storage-architecture.md` §12.4 (line 247), `16_observability.md` §16.5 (line 446)

**Issue:**

The authoritative capacity-tier table (§16.5 line 446) gives sustained session creation rates of 5/s, 30/s, **200/s**, **2,000/s** for Tier 1/2/3/4. §10.1 and §17.8.2 correctly use `burst_arrival_rate = 200/s` at Tier 3; §4.6.2 / §17.8.2 correctly use 30/s sustained.

§12.4 line 247 opens the "Tier 3 Redis write throughput quantification" block with "At Tier 3 scale (~10,000 concurrent sessions, **2,000 new sessions/s**)". The 2,000/s figure is the Tier 4 session creation rate, not Tier 3.

The table that follows (lines 249–256) is sized against this wrong rate:
- Quota counter `INCR` ~2,000/s and Rate-limit counter `INCR` ~2,000/s both appear 10x too high if session creation is the driver.
- "Burst at session storm may reach ~20,000/s" (line 256) is similarly inflated.

Note: Rate-limit INCRs actually scale with Gateway RPS (50,000/s at Tier 3, §16.5 line 447), not with session creation — so that row may be under-estimated for a different reason. Quota counter INCR scales with gateway→pod RPC rate (active sessions × turn rate).

**Impact:**

- The "Tier 3 Redis Cluster topology is required; Sentinel is appropriate only through Tier 2" conclusion (line 258) rests on the ~6,500/s sustained total built from the 10x-inflated rows. If corrected, the Sentinel→Cluster threshold may move, affecting when Tier 2→3 operators must plan cluster migration (lines 260–266).
- The §16.5 line 466 reconciliation paragraph ties single-tenant ~365 ops/s back to the §17.8 single-tenant estimate, but the §12.4 multi-tenant ~6,500/s aggregate does not reconcile cleanly if the underlying session-creation rate is wrong.

**Recommendation:**

Replace "2,000 new sessions/s" with "200 new sessions/s sustained" (cross-reference §16.5). Recalculate the Quota and Rate-limit rows using the correct drivers (gateway→pod RPC rate and Gateway RPS respectively, not session creation rate). Revise the 20,000/s burst and the ~6,000–6,500/s sustained total. Re-examine whether the Sentinel-appropriate-through-Tier-2 conclusion still holds.

---

### PRF-003 Warm Pool `minWarm` Table Contradicts Its "Production Value" Note [Medium]

**Files:** `17_deployment-topology.md` §17.8.2 (lines 866–876), `04_system-components.md` §4.6.2 (line 512)

**Issue:**

The §17.8.2 "Warm pool sizing" table publishes `Recommended minWarm (per hot pool)` = **20 / 175 / 1050** for Tier 1/2/3. The column header is explicitly "Recommended".

The note immediately below (line 874) mandates the opposite: "The recommended `minWarm` values above use `safety_factor = 1.0` (no safety margin)... **For production deployments**, operators MUST apply the per-tier `safety_factor`: Tier 1/2 with 1.5 yields 27 / 263; Tier 3 with 1.2 yields **1,260**. Use the safety-factor-adjusted values as the production `minWarm`."

So the "Recommended" column is a non-production value explicitly prohibited for production deployment. A Tier 3 operator reading the table sets 1,050 and has zero headroom during the 35-second failover window.

Compounding this, §4.6.2 line 512 says `safety_factor` defaults are **1.5 / 2.0** (agent-type / mcp-type). §17.8.2 line 871–872 shows Tier 3 values of **1.2 / 1.5**. Applying §4.6.2's default to Tier 3 produces `ceil(30 * 1.5 * 35) = 1,575` — a third number for the same parameter. The delegation-fan-out worked example (§17.8.2 line 898) uses `safety_factor = 1.2`, matching §17.8.2's table but not §4.6.2's stated default.

**Impact:**

Three different sizing outputs (1,050 / 1,260 / 1,575) for Tier 3 `minWarm` across adjacent sections. An operator or AI-DevOps agent reading §17.8.2's table will apply 1,050 and miss the mandated 1,260 unless they read the prose note.

**Recommendation:**

(1) Rename the table column from "Recommended minWarm" to "Raw demand estimate (no safety margin)" and add a second row "Production `minWarm` (with tier safety factor)" showing 27 / 263 / 1260. (2) Reconcile §4.6.2 line 512 with §17.8.2 lines 871–872: either §4.6.2 cites per-tier overrides, or §17.8.2 matches §4.6.2's defaults. The delegation-fan-out example (line 898) must then be kept consistent.

---

### PRT-006 `SupportedEventKinds` Vocabulary Is Not Authoritatively Enumerated [Medium]
**Files:** `spec/15_external-api-surface.md` (§15 lines 81–85, line 160), `spec/21_planned-post-v1.md` (§21.1 line 23)

`OutboundCapabilitySet.SupportedEventKinds`'s comment lists six "well-known" kinds: `state_change`, `output`, `elicitation`, `tool_use`, `error`, `terminated`. §21.1 `A2AAdapter` declares four of those six. No normative section (a) enumerates the closed set the gateway will ever pass to `OutboundChannel.Send`, (b) maps each kind to the `OutputPart` / `MessageEnvelope` sub-schema it carries, or (c) states how an adapter MUST behave on receipt of an undeclared kind.

Consequences: (1) the gateway outbound dispatcher (§15 line 160) has no deterministic filter rule; (2) the A2AAdapter's omission of `elicitation`/`tool_use` is consistent with `block_all` but nothing cross-references the correspondence — a future maintainer flipping `elicitationDepthPolicy` without updating `SupportedEventKinds` would silently desynchronize; (3) third-party adapters cannot know whether §7.2 session sub-states (`input_required`, `suspended`, …) will surface as `SessionEvent` kinds.

**Recommendation:** Add a "SessionEvent Kind Registry" subsection immediately after `OutboundCapabilitySet` that (1) enumerates the complete closed set, including any §7.2 sub-states adapters may surface, (2) maps each kind to its `SessionEvent.Payload` sub-schema, and (3) states the normative dispatch-filter rule: adapters whose `SupportedEventKinds` omits a kind MUST NOT receive events of that kind. Cross-reference from §21.1 binding `A2AAdapter`'s four-kind declaration to the registry and to `elicitationDepthPolicy: block_all`.

---

### DXP-003 `from_mcp_content()` package path conflicts with Runtime Author SDK package name [Medium]
**Files:** `15_external-api-surface.md` §15.4.1 line 1098; §15.7 lines 1878–1880

§15.4.1 says the Go helper ships in `github.com/lennylabs/lenny-sdk-go/outputpart`. §15.7 declares the Runtime Author SDK as `github.com/lennylabs/runtime-sdk-go`. Different modules; `lenny-sdk-go` is also ambiguous with the Client SDKs in §15.6.

**Recommendation:** Change §15.4.1 line 1098 to `github.com/lennylabs/runtime-sdk-go/outputpart` (or the exact chosen sub-package). Audit SDK package references across §15.4.1, §15.6, §15.7 for consistency.

---

### DXP-004 "Conformance test suite" referenced but not defined in §15.4.3 [Medium]
**Files:** `26_reference-runtime-catalog.md` §26.1 line 8; `15_external-api-surface.md` §15.4.3

§26.1: "Each ships a conformance test suite ([§15.4.3]...)" and "Reference runtimes claim a **conformance level** in their README; CI fails the release if conformance tests for the claimed level regress." §15.4.3 defines Integration Levels and the capability matrix but **does not define conformance tests, their structure, how they run, or what they assert.** "Conformance level" is undefined — unclear whether it equals Basic/Standard/Full or is independent.

**Recommendation:** Either (a) add a §15.4.6 "Conformance Test Suite" listing test categories per level (stdin/stdout protocol, 10 s heartbeat ack, shutdown-within-`deadline_ms`, MCP nonce handshake, lifecycle-channel handling) and the `lenny runtime validate` entry point; or (b) retarget §26.1's cross-reference to §24.18 and define "conformance level" as equal to Integration Level.

---

### OPS-004 "Tier 0/1/2" local-dev labels collide with "Tier 1/2/3" capacity-tier labels inside §17 [Medium]

**Files:** `17_deployment-topology.md` (§17.4, §17.6, §17.7 Day-0 walkthrough line 443, §17.8.2, §17.9).

**Issue.** §17.4 renames local-dev modes to **Tier 0** (`lenny up`), **Tier 1** (`make run`), **Tier 2** (`docker compose up`). §17.8.2 and §17.9 continue using **Tier 1 / Tier 2 / Tier 3** as capacity-planning labels. The tokens "Tier 1" and "Tier 2" now mean two different things inside one section. Concrete collisions:

1. Line 433: "validated in Tier 2; Tier 1 (`make run`) skips preflight" — local-dev sense.
2. Line 443 Day-0 walkthrough: "this walkthrough covers production-style **Tier 2** installs." Reader cannot tell from context whether this means *docker-compose local dev* (Tier-2 local from §17.4) or *capacity Tier 2* (from §17.8.2); line 439 immediately above points to `make run` for local dev, which is local-dev "Tier 1".
3. §17.6 wizard presents "Capacity tier: `tier1|tier2|tier3`" and "Target environment: `local|dev|prod`" without explaining they are independent axes.
4. §17.9.2 line 1192: "Layered with `values-tier1.yaml` for Tier 2 dev" — one sentence requires both tier namespaces.

**Root cause.** §17.4's three-tier rename landed after capacity-tier labels were established; no disambiguating prefix was introduced.

**Impact.** Medium. A deployer reading line 443 ("Tier 2 installs") may plausibly reach for `values-tier2.yaml` (capacity) or for `docker-compose up` (local-dev Tier 2). The install wizard preserves the ambiguity in its question surface.

**Recommendation.** Rename local-dev modes to a non-"Tier" noun and reserve "Tier 1/2/3" for capacity. Suggested:
- Tier 0 (`lenny up`) → **"Embedded Mode"**
- Tier 1 (`make run`) → **"Source Mode"**
- Tier 2 (`docker compose up`) → **"Compose Mode"**

At minimum, rewrite the four collision sites above with explicit "local-dev" vs "capacity" prefixes; line 443 should read "production-style **capacity-Tier 2** installs."

---

### TNT-003 `LENNY_CONFIG_MISSING` `remediation` field has no documented Helm key for `noEnvironmentPolicy` [Medium]
**Files:** `spec/10_gateway-internals.md` (lines 274, 276), `spec/17_deployment-topology.md` (§17.6)

§10.3 line 276 promises the `LENNY_CONFIG_MISSING` structured log `remediation` field will point to "the relevant Helm value or admin API path." For `auth.oidc.*` and `defaultMaxSessionDuration`, Helm key paths exist elsewhere in the spec. For `noEnvironmentPolicy` at platform scope, no Helm value name is defined anywhere (searches for `global.noEnvironmentPolicy`, `platform.noEnvironmentPolicy`, `Helm.*noEnvironmentPolicy` return zero matches). §17.6 line 345 only describes a *tenant-scoped* `rbacConfig.noEnvironmentPolicy` via bootstrap seed — a different setting from the platform-level Helm default §10.3 requires. An operator hitting `LENNY_CONFIG_MISSING{config_key=noEnvironmentPolicy, scope=platform}` has no documented Helm key to set.

**Recommendation:** Name the platform-level Helm value explicitly (suggested: `global.noEnvironmentPolicy`) in §17 values reference, and cite that key in the §10.3 table's Rationale column.

---

### SES-004 `resumeMode` Enum Mismatch Between §7.2 and §10.1 [Medium]
**Files:** `07_session-lifecycle.md` (line 124), `10_gateway-internals.md` (line 120), `04_system-components.md` (line 263)

**Description:**
The `session.resumed` event schema is inconsistent across sections:

- **§7.2 line 124** (authoritative event table): `resumeMode` is `full | conversation_only`, plus boolean `workspaceLost`.
- **§4.4 line 263** (eviction fallback): `resumeMode: "conversation_only"` + `workspaceLost: true` — consistent.
- **§10.1 line 120** (partial-manifest path): `resumeMode: "partial_workspace"` + `workspaceRecoveryFraction` — a value not in the §7.2 enum and an extra field not declared in the event schema.

Clients validating strictly against the §7.2 enum will reject the partial-manifest variant.

**Recommendation:**
Extend §7.2 enum to `full | conversation_only | partial_workspace`, add optional `workspaceRecoveryFraction` (0.0–1.0) to the event schema, and cross-reference the §10.1 partial-manifest path. A distinct value is a more honest signal than overloading `full`.

---

### SES-005 `starting` Has No `resume_pending` Path for Mid-Start Pod Crashes [Medium]
**Files:** `07_session-lifecycle.md` (lines 141-177), `06_warm-pod-model.md` (lines 82-89, 235, 267-273)

**Description:**
The session-level state machine (§7.2) includes `running → resume_pending` and `input_required → resume_pending` for pod crashes, but omits `starting → resume_pending`. `starting` is externally visible (§15.1 line 232); the only documented exit is watchdog → `failed` via `STARTING_TIMEOUT`.

§6.2 line 89 shows `starting_session → failed`, and the "Pre-attached failure retry policy" (§6.2 lines 267-273) describes synchronous retry with 2 max retries on fresh pods. But:

1. §7.2's state machine exposes `starting` as a visible state with no retry-on-new-pod fork.
2. Pre-attached retry is "per client request, not per pod" — but `POST /start` is fire-and-forget; a pod crash 60s into `starting` isn't obviously covered by synchronous retry.
3. §15.1 preconditions (line 217) show only `ready → starting → running`.

This creates ambiguity around whether a pod crash during `starting` is recoverable via the same `resume_pending` path used by `running`.

**Recommendation:**
Add explicit transitions to §7.2 and §6.2:
- `starting → resume_pending` (pod crash / gRPC error during agent runtime launch, `retryCount < maxRetries`)
- `starting → failed` (retries exhausted or `STARTING_TIMEOUT`)

And state whether pre-attached retries produce a visible `resume_pending` or remain internal.

---

### OBS-009 Narrative-Only Metric Rows in §16.1 [Medium]

**Files:** `16_observability.md`

Several rows in §16.1 remain as prose descriptions without `lenny_*` identifiers:

- Line 7 "Active sessions (by runtime, pool, state, tenant)" — needed by OBS-007 above.
- Line 9 "Stale warm pods (idle beyond threshold, by pool)".
- Line 26 "Gateway replica count".
- Line 27 "Gateway active streams (per replica)" — referenced narratively by `GatewayActiveStreamsHigh` (line 350) but alert text says "Active streams per replica > 80% of configured max" without citing a metric name.
- Line 35 "Postgres connection pool utilization (per replica)".
- Line 36 "Redis memory usage and eviction rate".
- Line 38 "Credential lease assignments (by provider, pool, source)".
- Line 40 "Credential pool utilization (active leases / total credentials, by pool)" — referenced by `CredentialPoolLow` (line 348) without a metric identifier.
- Line 41 "Credential pool health (credentials in cooldown, by pool)".
- Line 42 "Credential lease duration".
- Line 43 "Credential pre-claim mismatch".

**Recommendation:** Assign `lenny_*` names consistent with §16.1.1 attribute naming rules. `GatewayActiveStreamsHigh` and `CredentialPoolLow` alert conditions should cite the concrete metric.

---

### CRD-001 Credential-file contract example uses `http://` proxyUrl, contradicting mandatory TLS for proxy mode [Medium]

**Location:** `spec/04_system-components.md` line 894 (multi-provider credential file example) vs. line 1445 (SPIFFE-binding / proxy endpoint transport security requirement).

The "Runtime credential file contract" multi-provider JSON example includes:

```json
"materializedConfig": { "proxyUrl": "http://proxy.lenny.internal/v1", "leaseToken": "lt-..." }
```

This directly contradicts the binding rule later in the same section: "The proxy endpoint **must** use TLS (`https://`). […] The controller **rejects** pool registrations where `proxyEndpoint` uses an `http://` scheme and emits a validation error (`InvalidProxyEndpointScheme`)."

Because the example lives in the normative runtime-author contract (the shape runtime authors will copy verbatim when implementing credential-file parsing/testing), a plaintext `proxyUrl` will propagate into third-party runtime test fixtures and could mask the SPIFFE-binding / TLS preconditions that make proxy mode secure.

**Recommendation:** Update the example to `"proxyUrl": "https://proxy.lenny.internal/v1"` and add a one-line note immediately above the JSON block: "`proxyUrl` values are always `https://` — see the LLM Reverse Proxy section." This also ensures the schema validator emitted from this example (if one is generated from spec by docs-tooling) carries the right constraint.

---

### CRD-003 User-scoped credential storage callout cross-references the wrong KMS rotation section [Medium]

**Location:** `spec/04_system-components.md` line 1284 (user-scoped credential storage callout), re-referenced at line 1321.

The callout states: "User-scoped credentials are subject to the same KMS key rotation procedure ([Section 10.5](10_gateway-internals.md#105-upgrade-and-rollback-strategy))". Section 10.5 is **"Upgrade and Rollback Strategy"** — it does not describe KMS key rotation at all. The KMS key rotation procedure lives at §4.9.1 (line 1632, "KMS Key Rotation Procedure"). This is the only section with the DEK rotation, re-encryption job, rollback, and old-key retention procedure that the callout claims user credentials inherit.

**Impact:** Operators reading the user-credential security documentation are sent to the upgrade-rollback section for key-rotation details and will find nothing; this silently undermines the T4 Restricted classification's "identical encryption-at-rest, key rotation, and access-control treatment" guarantee (line 1279).

**Recommendation:** Replace `[Section 10.5](10_gateway-internals.md#105-upgrade-and-rollback-strategy)` with `[Section 4.9.1](#491-kms-key-rotation-procedure)` in the user-scoped credential storage callout.

This is a pure cross-reference fix and does not conflict with the §12.5 STR-003 fix — §12.5's T4 per-tenant KMS lifecycle concerns MinIO SSE-KMS (artifact-at-rest), while §4.9.1 concerns the Token Service's application-layer envelope encryption key for Postgres-stored credentials. They are distinct key hierarchies; the cross-reference just points to the wrong one.

---

### CRD-004 Operationally-added credentials silently bypass RBAC validation [Medium]

**Location:** `spec/04_system-components.md` lines 1119, 1158; interacts with `POST /v1/admin/credential-pools/{name}/credentials`.

The spec states: "The Token Service validates **on startup** that all `secretRef` values referenced in the database are accessible via its RBAC grants" (line 1119). However, credentials can be added post-startup via `POST /v1/admin/credential-pools/{name}/credentials`. The RBAC grant (`resourceNames` list) is populated "at install time" (line 1158); operationally-added credentials "require a manual RBAC patch or a re-run of the `helm upgrade`."

**Gap:** The add-credential admin API accepts a new credential with a `secretRef` whose Secret is not yet in the Token Service's `resourceNames` list. No fail-fast validation occurs at add time — the pool entry is persisted in Postgres, then the Token Service fails at lease-materialization time with "forbidden" on `get secrets`, surfaced as `CREDENTIAL_POOL_EXHAUSTED`. This is indistinguishable from a transient pool-exhaustion event by clients, and it blocks all sessions the new credential was meant to unblock.

**Recommendation:** The admin add-credential handler MUST issue a live-probe `get` on the referenced Secret using the Token Service's SA before committing the pool row. On RBAC failure, reject with `400 CREDENTIAL_SECRET_RBAC_MISSING`, message naming the missing `resourceName`. This converts a latent runtime failure (observable only once a session attempts to use the credential) into an admin-time failure with a clear remediation step (run the emitted RBAC patch from `lenny-ctl admin credential-pools add-credential`).

---

### CRD-005 Full-level rotation in-flight gate is unboundedly stuck if a malicious/buggy runtime never emits `llm_request_completed` [Medium]

**Location:** `spec/04_system-components.md` line 788 (in-flight gate) and §4.9 Emergency Credential Revocation (lines 1556–1601).

In direct mode, the adapter tracks outbound LLM requests via runtime-sent `llm_request_started`/`llm_request_completed` lifecycle messages. The gate clears when the counter reaches zero. The spec explicitly states: "The wait is intentionally unbounded […] imposing a timeout would risk an auth failure on an otherwise successful request." A `credential_rotation_inflight_wait_long` warning fires at 60 s, but rotation does not progress.

**Attack/failure mode:** A compromised or buggy runtime can simply stop emitting `llm_request_completed` messages. Because the adapter-agent security boundary (§4.9 / §4.7 "Adapter-Agent Security Boundary") explicitly treats the agent as untrusted, the runtime's self-reported in-flight counter is a trust-in-untrusted-party control path for emergency revocation. A malicious or wedged runtime can indefinitely block `credentials_rotated` from being sent — which directly defeats emergency revocation's "no window where the compromised key continues to reach the provider" guarantee in direct mode (line 1581 already flags the residual risk of already-extracted keys, but this is a separate orthogonal gap: the on-pod key itself can continue to be used because rotation is blocked).

Proxy mode is not affected (the counter is gateway-tracked via stream count), but direct mode in the path described is at risk.

**Recommendation:** Either (a) cap the unbounded gate with a hard ceiling (e.g., 5 minutes) specifically for **revocation-triggered** rotations (flag `rotationTrigger: emergency_revocation` or `fault_driven_rate_limited`), after which the adapter sends `credentials_rotated` regardless and the session falls through to the standard fault-rotation path, or (b) explicitly document this as an accepted residual risk in direct mode and strengthen the revocation runbook text in line 1581 to note that direct-mode emergency revocation is best-effort when the runtime is suspected compromised — the provider-side key rotation remains the only authoritative control. Option (a) is preferred because it aligns with the stated "no-exposure-window" guarantee.

---

### FLR-003 Inbox-Drain Failure Counter Undefined and Unalerted [Medium]
**Files:** `07_session-lifecycle.md` (line 265), `16_observability.md` (metrics table, alert table)

Section 7.2 specifies that when the session inbox-to-DLQ drain fails during a `resume_pending` transition (e.g., Redis unavailable), the transition is committed anyway and inbox messages are **permanently lost**. The spec says `lenny_inbox_drain_failure_total` is incremented and a `WARN` log is emitted. However:

1. `lenny_inbox_drain_failure_total` is **not defined** in the 16.1 metrics table. The only reference spec-wide is the inline mention in 07:265. By the catalog convention, monitoring stacks scraping against 16_observability.md will not know this counter exists.
2. No corresponding alert exists in 16.5. Compare: `SessionEvictionTotalLoss` fires on any non-zero increment of its counter; `CheckpointStorageUnavailable` fires on MinIO retry exhaustion. Inbox-drain loss is the only acknowledged silent-data-loss path without an alert.

Failure mode: a Redis outage that overlaps a pod failure (the same class of infrastructure events triggers both `resume_pending` and Redis degradation) will silently discard every in-flight inbox. Operators see only a WARN log, which may not reach aggregation if the replica is itself crashing.

**Recommendation:** Add `lenny_inbox_drain_failure_total` (counter, labels `pool`, `session_state`) to 16.1 under Session Lifecycle. Add `InboxDrainFailure` warning alert in 16.5 firing on any non-zero increment over 5 minutes.

---

### FLR-004 preStop Stage 2 Undefined When Postgres Is Unreachable [Medium]
**File:** `10_gateway-internals.md` (line 108)

Stage 2 of the preStop hook reads `last_checkpoint_workspace_bytes` from Postgres to select the tiered cap (30s / 60s / 90s). 10:108 handles only the "absent → 30s default" branch. It does **not** specify what happens when the Postgres read itself fails — a plausible state during Postgres failover (up to 30s per 12:150) or PgBouncer outage (12:46).

Two implicit interpretations diverge on safety:
- **A. Treat read failure as "absent":** Cap defaults to 30s. A 500MB workspace gets SIGKILLed at 30s, falling through to partial-manifest recovery (10:120) or to the total-loss path (4:283) if Postgres stays down.
- **B. Block on read:** preStop consumes the entire `terminationGracePeriodSeconds` budget, leaving zero time for stream drain (stage 3).

Neither is safe by default, and without spec guidance implementations will diverge.

**Recommendation:** Specify behavior explicitly in 10:108. Preferred: the gateway caches `last_checkpoint_workspace_bytes` in-replica on every successful checkpoint and uses the cached value if Postgres is unreachable during preStop. On cache miss (recent coordinator handoff), fall back to the 90s maximum tier rather than the 30s default — this trades 60s of extra preStop wait for avoiding truncated checkpoints during correlated infrastructure outages.

---

### EXP-002 Results API Has No Filters for delegation_depth / inherited / submitted_after_conclusion [Medium]
**Files:** `10_gateway-internals.md` §10.7 (lines 752, 754, 789–791, 821, 827, 884), `15_external-api-surface.md` line 423

The spec stores three analysis-critical per-`EvalResult` fields and prescribes filtering by them:

- `delegation_depth` (uint32) — "distinguish direct eval results (depth 0) from propagated child results" (line 752).
- `inherited` (boolean) — mirrors `experimentContext.inherited`.
- `submitted_after_conclusion` (boolean) — "Enables operators to filter post-conclusion submissions in analysis" (line 791).

The **Sample contamination warning for `control` propagation mode** paragraph (line 754) explicitly directs operators: *"filter by `delegation_depth == 0` (or `inherited == false`) to obtain uncontaminated per-variant aggregates, or segment results by `delegation_depth` to analyze the effect at each level separately."*

However, `GET /v1/admin/experiments/{name}/results` accepts no query parameters. The response (line 827–882) is a single pre-aggregated object with one bucket per variant — `sample_count`, `scorers[*].{mean,p50,p95,count}`. There is no way for an operator to:

1. Exclude `inherited == true` rows (the recommended sample-contamination mitigation under `control` propagation).
2. Exclude `submitted_after_conclusion == true` rows (recommended for post-conclusion eval hygiene).
3. Segment by `delegation_depth`.

The advice is operationally unreachable via the platform's own API. Operators would need direct Postgres access (bypassing RLS-on-API) or a per-row export endpoint that does not exist. This is a contradiction between prescriptive guidance ("operators should filter by X") and the available interface.

**Recommendation:** Pick one of the following and document it in §10.7:

- **A (preferred):** Add query-string filters to the Results endpoint: `?delegation_depth=0`, `?inherited=false`, `?exclude_post_conclusion=true`. Aggregation is recomputed over the filtered subset; the `lenny_eval_aggregates` materialized view retains its pre-aggregated role only when no filter is supplied.
- **B:** Add a `?breakdown_by=` param (`delegation_depth`, `inherited`, `submitted_after_conclusion`) that splits each variant bucket into sub-buckets.
- **C:** Add a per-row export endpoint (`GET /v1/admin/experiments/{name}/eval-results`, cursor-paginated) and explicitly state §10.7 guidance uses that endpoint.

Whichever path is chosen, line 754's guidance and line 922's rollback-trigger signal ("Mean eval score degradation") must match the actual interface. Today they point at a capability the API does not expose.

---

### MSG-004 Path 5 Delivery Receipt Status Not Explicitly Stated [Medium]

**Files:** `07_session-lifecycle.md` (§7.2 line 277)

**Description:** Path 5 is the canonical "buffered-in-inbox" path and is referenced by paths 2 and 4 as the fall-through target (with explicit `queued` status). However, path 5's own bullet does not state the resulting delivery-receipt status:

> 5. **No matching pending request, runtime busy (...)** → buffered in the session inbox (see inbox definition above); delivered in FIFO order when the runtime next enters `ready_for_input`.

Every other path (1, 2, 3, 4, 6) ends its bullet with an explicit `Delivery receipt status: <value>` sentence. Path 5 is the only path that omits this — even though it is textually the "base case" that paths 2 and 4 reference when falling through. A reader scanning the seven paths top-down will find the status missing; the only indirect signal is the generic summary at line 299 ("`queued` (buffered in inbox or DLQ)").

This is a cross-path consistency gap introduced by the stylistic convention set by the other six bullets. It does not create functional ambiguity (the summary at line 299 and the fall-through clauses in paths 2 and 4 make the status inferable), but it does break the uniform pattern and invites an implementer to mis-infer, e.g., `delivered` if they treat the ACK-on-dequeue model as eventually delivered, or `error` if they stop reading before line 299.

**Recommendation:** Append to line 277: "Delivery receipt status: `queued`." — matching the style used in paths 1, 3, and 6.

---

### POL-014 `AdmissionController` not placed in interceptor priority chain [Medium]

**Finding.** §11.6 ("AdmissionController evaluation") specifies a hard ordering requirement: "The gateway evaluates all active (open) circuit breakers at the start of every session-creation and delegation admission check, **before quota and policy evaluation**." §4.8 lists `AdmissionController` among the "Built-in policy engine components" (line 922: "`AdmissionController` | Queue/reject/prioritize, circuit breakers") but does **not** include it in the built-in priority table (§4.8 lines 934–941), which enumerates only `AuthEvaluator` (100), `QuotaEvaluator` (200), `DelegationPolicyEvaluator` (250), `ExperimentRouter` (300), `GuardrailsInterceptor` (400), `RetryPolicyEvaluator` (600). Nor is any phase assigned to `AdmissionController`.

Three downstream consistency problems result:

1. **Priority reservation contradiction.** §4.8 reserves priorities 1–100 for "built-in security-critical interceptors" (line 983) and says external interceptors must be > 100. To run "before quota" (QuotaEvaluator @ 200) as §11.6 prescribes, `AdmissionController` must occupy priority 101–199 (assuming it is an in-chain interceptor) or priority ≤ 100 (if it is security-critical). The spec picks neither. If 101–199, it collides with the external-interceptor range; an external interceptor at, say, priority 150 could interpose between circuit-breaker evaluation and quota evaluation — which is not the §11.6 intent. If ≤ 100, §11.6's "before quota" becomes trivial but the security-critical reservation's rationale ("running before authentication completes") does not fit a circuit-breaker check that needs authenticated tenant/runtime context.
2. **Phase ambiguity.** §4.8's chain model is explicitly per-phase: "Each phase runs its own interceptor chain independently." Circuit-breaker evaluation is described as gating session creation and delegation, which maps to `PostAuth` (session creation) and `PreDelegation` (delegation). Neither phase lists `AdmissionController` in the built-in tables at §4.8 line 1023 onward. A reader cannot determine whether circuit breakers fire in one phase chain or in both, nor whether they precede or follow `AuthEvaluator` within `PostAuth`.
3. **Short-circuit semantics unspecified.** §4.8's short-circuit rule ("If any interceptor returns `REJECT`, the chain short-circuits immediately — no subsequent interceptors are invoked") governs interceptor-chain REJECTs. §11.6 says a circuit breaker produces `CIRCUIT_BREAKER_OPEN` (HTTP 503, `retryable: false`). It is unclear whether this rejection flows through the same short-circuit audit-payload mechanism (the audit record captures payload at point-of-rejection with preceding MODIFYs applied) or whether circuit breakers are an out-of-chain pre-filter that bypasses the interceptor-audit contract entirely.

**Recommendation.** Pick one of two resolutions and make it normative:

- **Option A (in-chain):** Add `AdmissionController` to the §4.8 built-in priority table at a declared priority (e.g., 150, with phase binding to `PostAuth` and `PreDelegation`), and carve out that priority from the external-interceptor reservation (e.g., "priorities 150 and 160 are additionally reserved for built-in `AdmissionController` and any future admission-tier built-ins"). Confirm that the `AdmissionController` REJECT produces the same interceptor-audit payload semantics as other chain REJECTs.
- **Option B (out-of-chain pre-filter):** Clarify in both §4.8 and §11.6 that circuit-breaker evaluation is a pre-chain gate performed before the `PostAuth` / `PreDelegation` interceptor chain runs, and is not itself an interceptor. Add a note to §4.8 that `AdmissionController` is named as a policy-engine component for taxonomy but is not registered in the priority chain; its REJECT path emits a distinct audit event type (e.g., `admission.circuit_breaker_rejected`) rather than an `interceptor.rejected` event.

Either option is acceptable; the spec must not leave the placement implicit because deployers wiring external interceptors at priorities 101–199 will legitimately wonder whether their interceptor runs before or after the circuit-breaker evaluation, and the answer determines whether a `fail-open` MODIFY interceptor could change request fields the circuit breaker reads.

---

### EXM-004 Concurrent-stateless residualStateWarning omitted despite same-tenant process cotenancy [Medium]

**Files:** `07_session-lifecycle.md` (line 73), `05_runtime-registry-and-pool-model.md` (lines 483, 485)

The `sessionIsolationLevel.residualStateWarning` field (7.1:73) is set `true` "when `executionMode` is `task` (any scrub variant) or `concurrent` with `concurrencyStyle: workspace`" — concurrent-stateless is explicitly excluded. However, 5.2:485 says concurrent-stateless pods "share a network namespace and process space across all concurrent requests" and 5.2:483 routes same-tenant requests to pinned pod IPs. Within a tenant, concurrent-stateless has the same residual vectors as concurrent-workspace (shared `/tmp`, cgroup memory, network stack, page cache) plus stronger ones (no per-request workspace reset, no slot cleanup clearing `/tmp` between requests). The field as documented sets `residualStateWarning: false` for concurrent-stateless, signaling cleaner isolation than concurrent-workspace — opposite of actual posture. Clients that "reject sessions where this field is `true`" (5.2:458) will accept concurrent-stateless sessions with weaker per-request isolation than concurrent-workspace.

**Recommendation:** Extend 7.1:73 to `` `true` when `executionMode` is `task` (any scrub variant) or `concurrent` (either `concurrencyStyle`) `` and enumerate the concurrent-stateless vectors (process space, network stack, `/tmp`, page cache — none cleared since there is no per-request scrub). Alternatively, add a companion `stateTracking: false` field so `residualStateWarning: false` for stateless doesn't read as "no residual state".

---

### EXM-005 preConnect pool invariant violated for task-mode pods with scrub_warning [Medium]

**Files:** `06_warm-pod-model.md` (lines 32, 128, 132), `05_runtime-registry-and-pool-model.md` (line 429)

Section 6.1:32 establishes the invariant: "**All pods are SDK-warm when the runtime supports it.** Pools referencing a `preConnect`-capable runtime warm **all** pods to SDK-warm state." The task-mode state machine (6.2:121-135) specifies the post-scrub transitions:

- 6.2:127 — `task_cleanup → idle` when scrub succeeds (non-preConnect path)
- 6.2:128 — `task_cleanup → idle [scrub_warning]` when scrub fails with `onCleanupFailure: warn` (no preConnect guard)
- 6.2:132 — `task_cleanup → sdk_connecting` only when **scrub succeeds** and preConnect is true

The gap: a preConnect=true task-mode pod that experiences a scrub **failure** in `warn` mode bypasses the `sdk_connecting` re-warm transition (line 132 requires "scrub succeeds") and returns to `idle` directly (line 128). The returned pod has no SDK process running, violating the "all pods are SDK-warm" invariant for preConnect pools. 5.2:429 describes the `scrub_warning` path ("the pod is returned to the available pool with a `scrub_warning` annotation ... accepts the next task") without reconciling it with preConnect re-warm. The next task's claim will then either (a) hit an idle pod that should have been SDK-warm but is pod-warm, silently forfeiting the SDK-warm latency benefit for that claim, or (b) require implicit on-claim SDK connect that isn't in the documented state machine.

**Recommendation:** Either add a new state transition `task_cleanup → sdk_connecting` when "preConnect: true, scrub fails (warn), maxScrubFailures not reached, uptime limit not reached" so scrub_warning pods also re-warm SDK before returning to idle — or explicitly document in 6.2 that `scrub_warning` pods on preConnect pools skip SDK re-warm and are returned as pod-warm, with an accompanying note that the `lenny_warmpool_sdk_warm_pods` inventory metric (if tracked) may drop transiently. The first option preserves the invariant; the second documents an explicit exception.

---

### DOC-003 Broken cross-file anchor `#62-session-lifecycle-state-machine-and-timers` [Medium]

**File:** `27_web-playground.md:145`

Reference reads `[§6.2](06_warm-pod-model.md#62-session-lifecycle-state-machine-and-timers)`. The target section 6.2 in `06_warm-pod-model.md:67` is titled **"Pod State Machine"** (anchor `#62-pod-state-machine`); no section in `06_warm-pod-model.md` uses the phrase "session lifecycle state machine and timers". From the surrounding text ("hard override of the runtime's `maxIdleTimeSeconds`"), the intended link is most likely `07_session-lifecycle.md#72-interactive-session-model` (which documents `maxIdleTimeSeconds`), or `06_warm-pod-model.md#62-pod-state-machine` if the pod-level state machine was meant.

**Recommendation:** Either
- change target to `07_session-lifecycle.md#72-interactive-session-model` and rename the link text to `§7.2`, or
- change anchor to `#62-pod-state-machine` and keep `§6.2`.

---

### DOC-004 Broken cross-file anchor `#255-event-stream` [Medium]

**File:** `15_external-api-surface.md:633` (`INVALID_CALLBACK_URL` row)

Reference reads `[Section 25.5](25_agent-operability.md#255-event-stream)`. The actual H2 at `25_agent-operability.md:2315` is **"25.5 Operational Event Stream"**, whose GitHub anchor is `#255-operational-event-stream`. Simple typo — the word "Operational" is missing from the anchor fragment. README line 153 uses the correct anchor.

**Recommendation:** `#255-event-stream` → `#255-operational-event-stream`.

---

### DOC-005 Broken intra-file anchor `#166-service-level-objectives` [Medium]

**File:** `16_observability.md:30` (Checkpoint-duration metric row)

Reference reads `[Section 16.6](#166-service-level-objectives)`. Section 16.6 at line 506 is actually titled **"Operational Events Catalog"** (anchor `#166-operational-events-catalog`). There is no dedicated "Service Level Objectives" section — SLOs are documented inline in 16.5 "Alerting Rules and SLOs". The "Checkpoint duration SLO" is defined within 16.5.

**Recommendation:** Change target to `#165-alerting-rules-and-slos` (the checkpoint-duration SLO lives there). If a future refactor breaks SLOs into a dedicated 16.6, keep the rename in mind.

---

### WPP-006 `playground.oidcSessionTtlSeconds` and `playground.bearerTtlSeconds` absent from §27.2 Helm-values table [Medium]

**Files:** `27_web-playground.md:31–38, 63, 80`.

§27.3.1 introduces two configurable Helm values in prose only:
- `playground.oidcSessionTtlSeconds` (default `3600`, line 63) — cookie/session-record lifetime.
- `playground.bearerTtlSeconds` (default `900`, bounded `60 ≤ ttl ≤ 3600`, line 80) — bearer-token TTL.

Neither appears in §27.2's consolidated `playground.*` Helm-values table — the only concentrated reference for operators writing `values.yaml`. The `bearerTtlSeconds` bound is especially easy to miss when it's buried in paragraph prose rather than tabulated.

**Recommendation.** Add two rows to §27.2:

| Helm value | Default | Effect |
|---|---|---|
| `playground.oidcSessionTtlSeconds` | `3600` | Lifetime of the server-side playground session record and the `lenny_playground_session` cookie. See [§27.3.1](#2731-oidc-cookie-to-mcp-bearer-exchange). |
| `playground.bearerTtlSeconds` | `900` | TTL of MCP bearer tokens minted by `POST /v1/playground/token` (bounded `60 ≤ ttl ≤ 3600`). See [§27.3.1](#2731-oidc-cookie-to-mcp-bearer-exchange). |

---

## Low Findings

- **PRT-007**: MCP Target Version Currency Note Is Stale — `spec/15_external-api-surface.md` §15.2 line 840.
- **DXP-005**: §15.4.4 echo pseudocode omits the Basic-level shorthand it advertises — `15_external-api-surface.md` §15.4.1 lines 1088–1094; §15.4.4 lines 1598–1622.
- **OPS-005**: `lenny up` embedded mode lacks a documented upgrade path when embedded-Postgres major bumps — `17_deployment-topology.md` §17.4; `10_gateway-internals.md` §10.5.
- **OPS-006**: Operational defaults table (§17.8.1) omits `ops.drift.*` tunables — `17_deployment-topology.md` §17.8.1; `25_agent-operability.md` §25.10.
- **TNT-004**: `TestGatewayConfigValidation` test scope omits dev-mode positive assertion — `spec/10_gateway-internals.md` line 278.
- **STR-006**: `CheckpointStorageHigh` and `StorageQuotaHigh` share threshold numerator — operability gap — `spec/12_storage-architecture.md` §12.5 lines 322–327; `spec/16_observability.md` §16.5 lines 362–363.
- **DEL-007**: Orphan tenant-cap fallback does not specify audit observability — `08_recursive-delegation.md` §8.10 line 1000.
- **SES-006**: `resuming → cancelled/completed` Missing for Client Actions — `06_warm-pod-model.md` lines 115-119; `15_external-api-surface.md` lines 219, 223.
- **SES-007**: Derive Partial-Copy Contradicts §7.1 Atomicity Paragraph — `07_session-lifecycle.md` lines 28, 92.
- **SES-008**: SSE Reconnect Rate-Limit Still Undocumented (SES-003 Partial) — `07_session-lifecycle.md` line 317; `15_external-api-surface.md` lines 137-143.
- **OBS-010**: `CheckpointDurationHigh` Condition Uses Prose Qualifier, Not `level` Label Value — `16_observability.md`.
- **OBS-011**: `GatewaySubsystemCircuitOpen` Label Vocabulary Incomplete — `16_observability.md`.
- **CPS-002**: Differentiator cross-reference off-by-two — `spec/22_explicit-non-decisions.md` line 13.
- **CRD-002**: Credential-file example uses undefined provider `openai_proxy` — `spec/04_system-components.md` line 891.
- **BLD-004**: Phase 0 framing is stale now that license is resolved — `spec/18_build-sequence.md` line 7.
- **FLR-005**: PgBouncer Readiness Probe Amplifies Postgres Failover Window — `12_storage-architecture.md` line 45.
- **EXP-003**: `ExperimentDefinition` Has No Hard Cap on Variant Count — `10_gateway-internals.md` §10.7 lines 578–589, 827; `15_external-api-surface.md` line 730.
- **EXP-004**: Paused-Experiment Sticky Cache Wording Is Internally Contradictory — `10_gateway-internals.md` §10.7 lines 738, 892.
- **MSG-005**: Path 7 / Recovering-State DLQ Row Omits Explicit Receipt Status Value — `07_session-lifecycle.md` §7.2 dead-letter table line 295.
- **MSG-006**: Terminal-Target Row Uses Synchronous Error Rather Than Delivery Receipt — Contract Mixed — `07_session-lifecycle.md` §7.2 line 294; `15_external-api-surface.md` lines 1208, 1220; `08_recursive-delegation.md` line 442.
- **EXM-006**: mode_factor bootstrap fallback omits task-mode retirement-config-change guard — `05_runtime-registry-and-pool-model.md` line 554.
- **CNT-003**: `gitClone` Source Type Referenced But Not Catalogued in Section 14 — `14_workspace-plan-schema.md`; `26_reference-runtime-catalog.md`.
- **CNT-004**: Translation Fidelity Matrix Header Includes Post-V1 Adapter — `15_external-api-surface.md`.
- **DOC-006**: Section titles "16.7 Section 25 Audit Events" / "16.8 Section 25 Metrics" still confusing — `16_observability.md:514, 533`; `README.md:105–106`.
- **DOC-007**: TOC omits first subsection of three sections — `README.md`.
- **WPP-007**: CSP still missing `object-src` and `media-src` directives — `27_web-playground.md:155–166`.
