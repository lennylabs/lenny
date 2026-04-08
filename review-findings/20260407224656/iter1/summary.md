# Technical Design Review Findings -- 2026-04-07 (Iteration 14)

**Document reviewed:** `technical-design.md` (8,691 lines)
**Review framework:** `review-povs.md` (25 perspectives)
**Iteration:** 1 (of 8) -- continuation from 13 prior iterations
**Total findings:** 62 across 24 active perspectives (1 clean)
**Deduplicated findings:** 58 (4 cross-perspective duplicates removed)

## Findings Summary

| Severity | Count |
| -------- | ----- |
| Critical | 0     |
| High     | 0     |
| Medium   | 54    |
| Low      | 4     |

### Carried Forward from Iteration 13 (still present)

| # | ID | Finding | Section |
|---|------|---------|---------|
| 1 | K8S-035 / NET-034 | `lenny-pool-config` ghost webhook -- referenced but never formally defined | 13.2, 4.6.1 | **Skipped** — carried forward, previously skipped |
| 2 | WPL-030 | Failover formula 25s wrong -- should be 17s (leaseDuration + retryPeriod, not renewDeadline) | 4.6.1, 4.6.2, 12.3, 17.8 | **Skipped** — carried forward, previously skipped |
| 3 | DEL-039 | `settled=all` redundant mode in `lenny/await_children` | 8.5, 8.8 | **Skipped** — carried forward, previously skipped |
| 4 | FLR-038 | Redis runbook references 5 phantom metrics/alerts/config params | 12.4, 16.1, 16.5, 17.7 | **Skipped** — carried forward, previously skipped |
| 5 | CMP-041 | Salt rotation cannot re-pseudonymize billing records (original user_id already erased) | 12.8 | **Skipped** — carried forward, previously skipped |
| 6 | EXP-033A | Results API cursor format says "use platform-standard" but also "must not use base64-encoded JSON" -- platform standard IS base64-encoded JSON | 11.5 | **Skipped** — carried forward, previously skipped |
| 7 | EXP-033B | Multi-variant hash bucketing formula undefined (only binary control/treatment) | 11.5 | **Skipped** — carried forward, previously skipped |
| 8 | EXP-033C | Gateway creating materialized view at runtime contradicts DDL-through-migrations pattern | 11.5 | **Skipped** — carried forward, previously skipped |
| 9 | POL-041 | Cross-phase priority ordering error -- interceptors at 201-249 described as running between QuotaEvaluator and DelegationPolicyEvaluator, but those fire at different phases | 4.8 | **Skipped** — carried forward, previously skipped |
| 10 | MSG-037 | `delivery_receipt` schema omits `error` from populated-status list | 15.4.1 | **Skipped** — carried forward, previously skipped |
| 11 | CRD-031/032 | Secret shape table missing rows for `vault_transit` and `github` providers | 4.9 | **Skipped** — carried forward, previously skipped |

---

## New Findings

### Kubernetes Infrastructure (K8S)

#### K8S-036. `kubeApiServerCIDR` Helm value has no preflight validation [Medium] — **Fixed**
**Section:** 13.2, 17.6
If this value is wrong, gateway egress to kube-apiserver and all fail-closed admission webhook ingress silently break. The preflight Job validates pod/service CIDRs but not the API server CIDR, despite it being deterministically discoverable from within the cluster.
**Recommendation:** Add a preflight check that resolves the kube-apiserver endpoint and verifies it falls within `kubeApiServerCIDR`.
**Resolution:** Added "kube-apiserver CIDR" preflight check to Section 17.6 that resolves the kube-apiserver endpoint and verifies it falls within the configured CIDR.

#### K8S-037. CRD API group `lenny.dev` assumed for upstream `agent-sandbox` CRDs [Medium]
**Section:** 17.6, 4.6.1
Preflight check lists all four agent-sandbox CRDs with the `lenny.dev` API group, but the upstream project would register under its own API group. The spec never documents forking or re-registering under `lenny.dev`.
**Recommendation:** Document the API group decision -- either use the upstream group or explain the re-registration.

#### K8S-038. `lenny-system` namespace missing PSS labels [Low]
**Section:** 17.2
PSS labels are specified for agent namespaces but not for `lenny-system`. Defense-in-depth gap.
**Recommendation:** Add `warn: restricted` and `audit: restricted` PSS labels to `lenny-system`.

#### K8S-039. CRD admission webhook validates cross-resource `terminationGracePeriodSeconds` without explaining how [Low]
**Section:** 10.1
`SandboxWarmPool` CRD webhook references the gateway pod spec's `terminationGracePeriodSeconds`, but the mechanism for obtaining this cross-resource value is unspecified.
**Recommendation:** Clarify the mechanism (injected Helm value, reference field, or lookup).

---

### Security (SEC)

#### SEC-035. Interceptor MODIFY restrictions on immutable fields have no enforcement mechanism [Medium]
**Section:** 4.8
The spec prohibits external interceptors from modifying `user_id`, `tenant_id`, etc., but the `InterceptResponse` returns opaque `bytes modified_content` with no specified comparison step. A buggy/malicious interceptor can change `tenant_id` and bypass tenant isolation.
**Recommendation:** Specify that the gateway snapshots immutable fields and rejects MODIFYs that alter them.

#### SEC-036. `lenny/get_task_tree` exposes `runtimeRef` to child sessions without scoping [Medium]
**Section:** 8.5, 7.2
A child session can see the entire delegation tree including sibling runtime types, enabling reconnaissance by compromised agents.
**Recommendation:** Scope `lenny/get_task_tree()` to the calling session's own subtree and ancestors; redact `runtimeRef` from sibling nodes.

#### SEC-037. Direct-mode credential in-memory exposure not documented as residual risk [Low]
**Section:** 4.9
In direct delivery mode, the real API key persists in runtime process heap memory for the session lifetime. This is a known residual risk but not documented.
**Recommendation:** Add a residual risk note in the Security Boundaries subsection of Section 4.9.

---

### Network Security (NET)

#### NET-035. Gateway ingress NetworkPolicy missing port 8443 for LLM proxy traffic [Medium] — **Fixed**
**Section:** 13.2
The agent-side policy opens egress to gateway:8443 for LLM proxy, but the gateway's ingress allow-list only mentions port 50051 (gRPC). Under default-deny, all proxy-mode LLM traffic would be dropped.
**Recommendation:** Add port 8443 to the gateway's ingress NetworkPolicy from agent namespaces.
**Resolution:** Added TCP 8443 (LLM proxy port) alongside TCP 50051 in the Gateway component's ingress allow-list in the Section 13.2 component table.

#### NET-036. MinIO missing from `lenny-system` NetworkPolicy component table [Medium] — **Fixed**
**Section:** 13.2
Self-managed MinIO at `minio.lenny-system:9000` has no ingress/egress rules. Under default-deny, it would be unreachable.
**Recommendation:** Add a MinIO row to the component table with "(self-managed profile only)" annotation, matching PgBouncer's pattern.
**Resolution:** Added MinIO row to the Section 13.2 component table with `lenny.dev/component: minio` selector, self-managed profile annotation matching PgBouncer's pattern, egress to kube-system CoreDNS (UDP/TCP 53), and ingress from gateway pods on TCP 9443 (TLS port matching the gateway's existing egress rule).

---

### Protocol Design (PRT)

#### PRT-036. Translation Fidelity Matrix tags MCP `ref` as `[exact]` but adapters dereference it [Medium] — **Fixed**
**Section:** 15.4.1
The matrix cell acknowledges adapters dereference `lenny-blob://` URIs before sending to external clients, yet still tags `ref` as `[exact]`. Should be `[dropped]`.
**Recommendation:** Change the tag to `[dropped]` matching the OpenAI Completions treatment.
**Resolution:** Changed MCP `ref` tag from `[exact]` to `[dropped]` with updated description matching the dereferencing behavior. Added MCP to the `ref` row of the round-trip asymmetry summary table.

---

### Developer Experience (DXP)

#### DXP-035. `terminate` lifecycle channel `reason` enum missing `task_complete` value [Medium] — **Fixed**
**Section:** 4.7, 5.2, 15.4.1
The `reason` enum is `session_complete | budget_exhausted | eviction | operator`, but task-mode between-task signaling uses `terminate(task_complete)`. `task_complete` is not a valid value.
**Recommendation:** Add `task_complete` to the `reason` enum.
**Resolution:** Added `"task_complete"` to the `terminate` lifecycle message's `reason` enum in Section 4.7, making it consistent with the task-mode between-task signaling described in Sections 5.2 and 15.4.1.

#### DXP-036. Task-mode between-task signaling requires lifecycle channel but no tier restriction on task mode [Medium] — **Fixed**
**Section:** 5.2, 4.7
Lifecycle channel is Full-tier only, but task mode is not restricted to Full-tier. A Minimum/Standard-tier runtime in a task-mode pool has no way to receive between-task signals.
**Recommendation:** Either restrict task mode to Full-tier runtimes, or define between-task signaling via stdin for lower tiers.
**Resolution:** Added a "Task mode and integration tiers" note to Section 5.2 clarifying that pod reuse (between-task lifecycle channel signaling, scrub, reuse cycle) requires Full-tier. For Standard/Minimum-tier runtimes, the adapter sends `shutdown` on stdin and the pod is replaced from the warm pool (effectively `maxTasksPerPod: 1`). Added a "Task mode pod reuse" row to the Tier Comparison Matrix in Section 15.4.3.

---

### Operator Experience (OPS)

#### OPS-043. `EtcdQuotaNearLimit` alert hardcodes "recommended: 8 GB" but Tier 1 is 4 GB [Medium] — FIXED
**Section:** 16.5, 4.6.1
The alert description says "recommended: 8 GB" but Section 17.8.2 specifies Tier 1 etcd as 4 GB. Section 4.6.1 correctly differentiates tiers.
**Recommendation:** Parameterize the alert description or use "recommended: see Section 17.8.2".
**Resolution:** Updated alert description in Section 16.5 to specify per-tier recommended quotas (4 GB for Tier 1, 8 GB for Tier 2/3) with a reference to Section 17.8.2.

---

### Multi-Tenancy (TNT)

#### ~~TNT-035. `session_dlq_archive` table missing from erasure scope and tenant deletion order [Medium]~~ **FIXED**
**Section:** 7.2, 12.8
*Duplicate of STR-040.* Stores inter-session messages with user content but is absent from the GDPR erasure scope table and tenant deletion Phase 4 order.
**Recommendation:** Add `session_dlq_archive` to both the erasure scope table and tenant deletion Phase 4.
**Resolution:** Added `session_dlq_archive` to the erasure scope table (Section 12.8) with DELETE action for archived DLQ messages, and inserted it into the Phase 4 tenant deletion dependency order between `EvictionStateStore` and `EventStore`.

#### TNT-036. Durable inbox Redis key missing from canonical key prefix table [Medium] — **Fixed**
**Section:** 7.2, 12.4
*Duplicate of STR-041.* `t:{tenant_id}:session:{session_id}:inbox` is not listed in the exhaustive key prefix table. Tenant isolation test coverage gap.
**Recommendation:** Add the inbox key pattern to the canonical table.
**Resolution:** Added `t:{tenant_id}:session:{session_id}:inbox` row to the Section 12.4 canonical key prefix table with role "Durable session inbox (Redis list)" and notes referencing §7.2. Extended the tenant isolation integration test requirements to include inbox key coverage.

#### TNT-037. `SemanticCache` Redis key missing from canonical key prefix table [Medium] — **Fixed**
**Section:** 12.4
Listed in the erasure scope and explicitly cross-references Section 12.4, but not present in the table.
**Recommendation:** Add the `SemanticCache` key pattern to the table.
**Resolution:** Added `t:{tenant_id}:scache:{scope}:{hash}` row to the Section 12.4 canonical key prefix table with role "Semantic cache entry" and notes describing the scope variants (`u:{user_id}`, `s:{session_id}`, `t`) per `cacheScope` configuration. Extended the tenant isolation integration test requirements to include semantic cache key coverage.

---

### Storage (STR)

#### STR-042. Delete-marker lifecycle rule scoped only to checkpoints but versioning is bucket-wide [Medium] — **Fixed**
**Section:** 12.5
Delete markers from non-checkpoint prefixes (uploads, transcripts, eviction context) accumulate indefinitely, degrading `ListObjects` performance.
**Recommendation:** Extend the delete-marker lifecycle rule to cover all object prefixes, or add prefix-specific rules for each object type.
**Resolution:** Changed the lifecycle rule from checkpoint-prefix-scoped to bucket-wide scope, matching the bucket-wide versioning configuration. Updated the rationale to explain that the GC job deletes artifacts across all prefixes (not just checkpoints), so delete-marker expiration must also be bucket-wide. Updated the Helm chart `mc ilm add` reference to specify no prefix filter.

---

### Delegation (DEL)

#### DEL-040. Orphan cleanup job only detects root-terminated orphans, not mid-tree orphans [Medium] — **Skipped**
**Section:** 8.10
`cascadeOnFailure: detach` creates orphans at any depth when a mid-tree parent terminates. These are invisible to cleanup until the root terminates.
**Recommendation:** Change the predicate to check the direct parent's terminal status, not just the root's.
**Resolution (Skipped — correct by design):** The `detach` policy intentionally allows children to survive parent termination (Section 8.10: "To allow children to outlive a parent that completes normally, set `cascadeOnFailure: detach`"). Detached children are not orphans requiring cleanup — they are legitimately running sessions. Their lifetime is bounded by `cascadeTimeoutSeconds` (default 3600s, line 3526), which is the designed mechanism for limiting detached children's persistence. The root-termination predicate in the cleanup job catches the final case: detached children lingering after the entire tree is done and `cascadeTimeoutSeconds` has expired. Checking the direct parent's terminal status would prematurely terminate intentionally detached children, breaking the `detach` contract. Mid-tree cascade behavior is already handled by each node's own `cascadeOnFailure` policy (Section 8.10: detached orphans "retain and execute [their] own `cascadeOnFailure` policy").

---

### Session Lifecycle (SLC)

#### SLC-041. `resuming` classified as internal-only but referenced in external API tables [Medium] — **Fixed**
**Section:** 15.1
*Overlaps with API-051.* `resuming` is listed as internal-only (never returned in API responses), yet the `derive` endpoint lists it as a valid precondition state and the `resume` endpoint shows transitions through it.
**Recommendation:** Either add `resuming` to the external state table, or remove it from endpoint preconditions and show `resume_pending -> running` as the external transition.
**Resolution:** Removed `resuming` from the `derive` endpoint's precondition states (clients cannot observe it). Changed the `resume` endpoint's resulting transition from `resume_pending → resuming → running` to `resume_pending → running`, with a note clarifying that `resuming` is an internal-only transient state. This aligns the endpoint tables with the internal-only state list on line 6155.

#### SLC-042. `suspended` state transition block omits `cancelled` and `expired` exits [Medium] — **Fixed**
**Section:** 6.2 vs 7.2
Section 6.2's `suspended` block lists 4 transitions, but Section 7.2 defines 6 (adding `cancelled` and `expired`). Implementers reading only Section 6.2 would miss two valid exits.
**Recommendation:** Add the two missing transitions to the Section 6.2 `suspended` state block.
**Resolution:** Added `suspended → cancelled (client/parent cancels while suspended)` and `suspended → expired (deadline reached while suspended)` to the Section 6.2 `suspended` state transition block, matching the format and wording used in Section 7.2.

#### SLC-043. `suspended -> expired` transition has no defined trigger mechanism [Medium] — **Fixed**
**Section:** 7.2, 6.2
Both timers that could trigger `expired` (`maxSessionAge`, `maxIdleTimeSeconds`) are explicitly paused during `suspended`. No `maxSuspendedDuration` or wall-clock deadline is defined.
**Recommendation:** Either remove the transition as unreachable, define a `maxSuspendedDuration` timer, or clarify that delegation lease wall-clock expiry is the trigger.
**Resolution:** Clarified that the trigger is the delegation lease's `perChildMaxAge` (Section 8.3), which is a wall-clock deadline not paused during suspension. Updated transition descriptions in Sections 6.2 and 7.2 to specify the trigger explicitly, and added a "suspended → expired trigger mechanism" note in Section 7.2 explaining that only delegation child sessions can reach this transition (root sessions in `suspended` cannot reach `expired`).

---

### Observability (OBS)

#### OBS-035. Checkpoint duration SLO missing from burn-rate alerts table [Medium] — **Fixed**
**Section:** 16.5
The SLO targets table defines a checkpoint duration SLO and the spec mandates burn-rate alerting for "all" SLOs, but the burn-rate table omits it.
**Recommendation:** Add a `CheckpointDurationBurnRate` entry or exclude checkpoint duration from the "all SLOs" language.
**Resolution:** Added `CheckpointDurationBurnRate` row to the burn-rate alerts table in Section 16.5, matching the existing format (1 h / 14× fast window, 6 h / 3× slow window, Critical/Warning severity).

#### OBS-036. Warm pool startup metric has two conflicting names [Medium] — **Fixed**
**Section:** 4.6.1, 16.1, 17.7
`lenny_warmpool_pod_startup_duration_seconds` (Section 16.1) vs `lenny_warmpool_warmup_latency_seconds` (Sections 4.6.1, 17.7). Same measurement, different names.
**Recommendation:** Unify to the canonical Section 16.1 name.
**Resolution:** Replaced both occurrences of `lenny_warmpool_warmup_latency_seconds` (in Sections 4.6.1 and 17.7) with the canonical `lenny_warmpool_pod_startup_duration_seconds` from Section 16.1. All four references now use the same metric name.

#### OBS-037. Runbooks reference three phantom alert names [Medium] — **Fixed**
**Section:** 17.7
`WarmPoolBelowMinimum`, `PostgresDown`, `GatewayReplicasLow` do not exist in Section 16.5. The actual alerts are `WarmPoolLow`/`WarmPoolExhausted`, `SessionStoreUnavailable`, `GatewayNoHealthyReplicas`.
**Recommendation:** Update runbook trigger lines to use canonical alert names.
**Resolution:** Replaced the three phantom alert names in Section 17.7 runbook trigger lines with their canonical Section 16.5 counterparts: `WarmPoolBelowMinimum` → `WarmPoolLow` / `WarmPoolExhausted`, `PostgresDown` → `SessionStoreUnavailable`, `GatewayReplicasLow` → `GatewayNoHealthyReplicas`.

---

### Compliance (CMP)

#### CMP-042. INSERT-only grants and immutability triggers block GDPR erasure operations [Medium] — **Fixed**
**Section:** 11.7, 11.2.1, 12.8
The erasure job runs as `lenny_app` (INSERT-only on audit/billing tables), but GDPR erasure requires UPDATE (pseudonymization) and DELETE (audit purge). No elevated role, trigger bypass, or separate grant is defined.
**Recommendation:** Define a `lenny_erasure` database role with scoped UPDATE/DELETE grants on billing and audit tables, used exclusively by the erasure background job. Add immutability trigger bypass via `SET LOCAL lenny.erasure_mode = true` checked in the trigger function.
**Resolution:** Added item 6 to the Section 11.7 integrity controls defining the `lenny_erasure` database role with scoped UPDATE grants on billing PII columns and DELETE on audit tables, used exclusively by the erasure background job. The immutability triggers check `current_setting('lenny.erasure_mode', true)` and allow operations when set to `'true'` via `SET LOCAL` (transaction-scoped, no leak via connection pooler). Startup verification extended to confirm the guard clause is present in trigger function bodies. The `lenny_erasure` role's grants are explicitly excluded from `AuditGrantDrift` detection.

---

### API Design (API)

#### API-050. `PUT /v1/admin/external-adapters/{name}/validate` uses PUT for side-effecting action [Medium] — **Fixed**
**Section:** 15.1
Every other action endpoint uses POST. This endpoint runs a test suite and transitions state.
**Recommendation:** Change to POST.
**Resolution:** Changed `PUT` to `POST` for the `/v1/admin/external-adapters/{name}/validate` endpoint in the Section 15.1 API table, the Section 15.2.1 compliance gate narrative (two occurrences), and the Section 24.7 CLI command mapping table. The endpoint transitions adapter status (`pending_validation` → `active` / `validation_failed`), making it an action rather than an idempotent update.

#### API-052. `terminate` endpoint omits `starting` from valid precondition states [Medium] — **Fixed**
**Section:** 15.1
Description says "valid in any non-terminal, non-setup state" but `starting` is non-terminal and non-setup per the external state table, yet is omitted from the precondition column.
**Recommendation:** Add `starting` to the precondition states, or add it to the "setup states" definition.
**Resolution:** Added `starting` to the `terminate` endpoint's precondition states in Section 15.1, making the explicit state list consistent with the "any non-terminal, non-setup state" description. `starting` is non-terminal and non-setup per the external state table (line 6155).

#### API-053. `dryRun` blanket "all admin POST and PUT" claim is incorrect [Medium] — **FIXED**
**Section:** 15.1
Multiple action POST endpoints and the `warm-count` PUT are explicitly excluded from dryRun, contradicting the "all" claim.
**Recommendation:** Change to "Most admin POST and PUT endpoints support `dryRun`" and list the exceptions.
**Resolution:** Changed "All admin POST and PUT endpoints" to "Most admin POST and PUT endpoints" and added a note listing the exceptions (action endpoints and DELETE endpoints) in Section 15.1.

---

### Competitive Positioning (CPS)

#### CPS-035. TTHW paragraph references wrong section for `make run` [Medium] — FIXED
**Section:** 23.2
References "Section 18" (Build Sequence) instead of Section 17.4 (local dev mode).
**Recommendation:** Change to "Section 17.4".
**Status:** Fixed — changed "Section 18" to "Section 17.4" in TTHW paragraph.

#### CPS-036. Enterprise persona entry point references wrong section [Medium] — FIXED
**Section:** 23.2
Directs to "Section 16" (Observability) instead of Sections 4.8, 8, and 10 (enterprise controls).
**Recommendation:** Fix the cross-reference.
**Status:** Fixed — changed "Section 16" to "Sections 4.8, 8, and 10" in enterprise persona row.

#### CPS-037. Phase 0 milestone overstates openness for external contribution [Medium] — **Fixed**
**Section:** 18, 23.2
Phase 0 says "repository open for external contribution" but Phase 17a says no external PR solicitation before 17a completes. At Phase 0, there's no CONTRIBUTING.md, no `make run`, no documentation.
**Recommendation:** Reword to "repository publicly visible" or "repository available for early visibility" rather than "open for external contribution".
**Resolution:** Changed Phase 0 milestone text from "repository open for external contribution" to "repository publicly visible for early community awareness", accurately reflecting that Phase 0 only commits the license and ADRs — CONTRIBUTING.md, `make run`, and community onboarding are Phase 2 deliverables.

---

### Warm Pool (WPL)

#### WPL-031. minWarm recommended values omit safety_factor from formula [Medium] — FIXED
**Section:** 17.8
Table values match `claim_rate * (failover + startup)` without the `safety_factor` multiplier that the formula immediately below includes. Tier 2: 175 vs 262.5; Tier 3: 1050 vs 1260.
**Recommendation:** Either include `safety_factor` in the computed values, or remove it from the formula and note it as an operator-applied adjustment.
**Resolution:** Added a note below the warm pool sizing table clarifying that the recommended `minWarm` values intentionally use `safety_factor = 1.0` (no margin) as baseline starting points for first deployments, and that operators should apply the per-tier safety_factor from the formula based on their risk tolerance.

---

### Credentials (CRD)

#### CRD-033. Secret shape table "single data key" claim contradicts multi-key entries [Medium] — **Fixed**
**Section:** 4.9
Introductory sentence says each Secret has a "single `data` key" but `aws_bedrock` has two keys and `azure_openai` has three.
**Recommendation:** Change to "one or more `data` keys" or "the following `data` keys".
**Resolution:** Changed "a single `data` key whose name is the provider's canonical key field" to "one or more `data` keys whose names are the provider's canonical key fields", accurately reflecting that providers like `aws_bedrock` (2 keys) and `azure_openai` (3 keys) have multiple data keys.

---

### Content Model & Schemas (SCH)

#### SCH-041. Schema versioning cross-reference cites Section 8.7 instead of 8.8 for TaskRecord [Medium] — **Fixed**
**Section:** 15.5
"TaskRecord (Section 8.7)" should be "TaskRecord (Section 8.8)". Section 8.7 is File Export Model.
**Recommendation:** Fix the cross-reference.
**Resolution:** Changed "Section 8.7" to "Section 8.8" in both occurrences (Section 15.5 item 7 and Section 13.5 OutputPart durable-consumer rule) to correctly reference the TaskRecord definition.

#### SCH-042. Inbound MCP translation table uses wrong mimeType source for ImageContent [Medium]
**Section:** 15.4.1
Both ImageContent rows say `OutputPart.mimeType` source is `url.mimeType`, but MCP's `ImageContent` has `mimeType` as a top-level field. There is no `url.mimeType` nested field.
**Recommendation:** Change `url.mimeType` to `mimeType` in both rows.
**Resolution:** Changed `url.mimeType` to `mimeType` in both ImageContent rows (url variant and base64 variant) in the Section 15.4.1 inbound translation table.

#### SCH-043. `tool_result` field naming inconsistency: `isError` vs `is_error` [Medium] — FIXED
**Section:** 4.8, 15.4.1
Stdin protocol uses `isError` (camelCase), interceptor payloads use `is_error` (snake_case) for the same field on the same data structure.
**Recommendation:** Unify to `isError` (matching MCP convention and the stdin protocol).
**Resolution:** Replaced `is_error` with `isError` in both `PreToolResult` and `PostConnectorResponse` interceptor payload schemas in Section 4.8.

---

### Build Sequence (BLD)

#### BLD-035. Phase 5.4 deliverables infeasible on managed Kubernetes [Medium] — FIXED
**Section:** 18
`EncryptionConfiguration` manifest in Helm chart and `etcdctl get` CI gate are impossible on EKS/GKE/AKS. The spec itself acknowledges this limitation in Section 17.6 (preflight emits non-blocking warning only).
**Recommendation:** Scope Phase 5.4 deliverables to self-managed clusters; for managed K8s, document the provider-specific alternative (e.g., AWS KMS plugin, GKE application-layer encryption).
**Resolution:** Added "(self-managed clusters only; managed K8s uses provider-specific encryption — see Section 4.9 topology table)" qualifier to Phase 5.4 deliverables list. The provider-specific alternatives were already documented in Section 4.9's encryption topology table and Section 17.6's preflight warning; the Phase 5.4 text just needed an explicit scope qualifier.

---

### Failure Modes (FLR)

#### FLR-039. Circuit breaker Redis fallback behavior undefined [Medium] -- FIXED
**Section:** 11.6, 12.4
Circuit breaker state is in Redis with a 5-second in-process cache. The Redis failure behavior table omits circuit breakers. When Redis is unavailable and cache expires, behavior is undefined -- fail-open would silently re-enable operator-blocked traffic during incidents.
**Recommendation:** Add circuit breaker to the Redis failure behavior table. Define fail-closed behavior (circuit breaker state persists as last-known until Redis recovers) since circuit breakers are incident management tools used during infrastructure degradation.
**Resolution:** Added circuit breaker row to the Redis failure behavior table in Section 12.4. Defined fail-closed behavior: last-known state persists from in-process cache, open breakers remain enforced, no breaker can transition to closed without confirmed Redis read, and replicas re-read all states on Redis recovery.

---

### Experimentation (EXP)

#### EXP-034. `variant_weight` defined as percentage but used as raw multiplier in formulas [Medium] — **Fixed**
**Section:** 11.5, 5.1
YAML shows `weight: 10` (percentage), but scaling formulas use `variant_weight` as a multiplier. A 10% variant would produce 10x expected pool size.
**Recommendation:** Normalize: either define weight as a fraction (0.0-1.0) in YAML and formulas, or add `/ 100` in the formula where weight is used.
**Resolution:** Changed YAML `weight` from integer percentage (`weight: 10 # percentage`) to fractional representation (`weight: 0.10 # fraction (0.0–1.0); 0.10 = 10% of traffic`), consistent with how formulas already use `variant_weight` as a direct multiplier and how `Σ variant_weights` is clamped to `[0, 1)`. Updated the `initialMinWarm` sizing guidance to remove the now-unnecessary `/ 100` conversion.

#### EXP-035. `lenny_eval_score` Gauge metric cannot support mean computation for rollback [Medium] — **Fixed**
**Section:** 16.1, 11.5
Rollback trigger requires "mean safety score" but Prometheus Gauge only stores the last value.
**Recommendation:** Change to Histogram or Summary, or use a separate counter pair (sum + count).
**Resolution:** Changed `lenny_eval_score` metric type from Gauge to Histogram in Section 16.1, since each eval run records one score observation and Histogram natively supports mean computation via `rate(sum) / rate(count)`. Updated the Section 11.5 rollback trigger table to use the correct PromQL expression (`rate(lenny_eval_score_sum[10m]) / rate(lenny_eval_score_count[10m])`) instead of the raw Gauge metric name.

#### EXP-036. Results API response violates standard pagination envelope [Medium] — **Fixed**
**Section:** 11.5, 15.1
Uses `{ experiment_id, variants, cursor }` instead of the `{ items, cursor, hasMore, total }` envelope defined in Section 15.1, despite being listed as a paginated endpoint.
**Recommendation:** Wrap the response in the standard pagination envelope.
**Resolution:** The Results API returns a single experiment's aggregated scores (not a list of items), so the standard pagination envelope does not naturally apply. Removed `GET /v1/admin/experiments/{name}/results` from the Section 15.1 paginated endpoints list, removed the `cursor` field from the Section 11.5 response JSON, updated the response description to explicitly state the endpoint is not paginated, and replaced the cursor format paragraph with a blinding note. The response size is inherently bounded by the number of variants (operator-configured, typically 2-5).

---

### Document Quality (DOC)

#### DOC-035. ~~Duplicate "Approval Modes" heading creates navigation ambiguity~~ **[Fixed]** [Medium]
**Section:** 8.4, 8.6
Section 8.4 "Approval Modes" covers delegation approval. Section 8.6 subheading "Approval Modes" covers lease extension approval. Different concepts, identical names.
**Recommendation:** Rename Section 8.6's subheading to "Lease Extension Approval Modes".
**Resolution:** Renamed Section 8.6's subheading from "Approval Modes" to "Lease Extension Approval Modes".

#### DOC-036. Orphaned footnote number [Low]
**Section:** 8.6
Line 3198 has footnote `4` with no corresponding `1`/`2`/`3` anywhere in the document.
**Recommendation:** Renumber to `1` or convert to inline note.

---

### Messaging (MSG)

#### MSG-039. `delivery: "immediate"` behavior undefined for `input_required` sessions [Medium] -- FIXED
**Section:** 15.4.1, 7.2
`immediate` says gateway interrupts and delivers for `running` sessions. `input_required` is a sub-state of `running`. But path 4 says stdin delivery is impossible during `input_required` -- messages are unconditionally buffered. Contradiction.
**Recommendation:** Add explicit clause that `immediate` does not override path 4 buffering for `input_required`.
**Fix applied:** Added explicit exception clause to the `immediate` delivery row in Section 15.4.1 stating that `input_required` sub-state is not overridden by `delivery: "immediate"` — path 4 buffering applies, receipt is `queued`.

#### MSG-040. `delegationDepth` described as MessageEnvelope field but absent from canonical JSON schema [Medium] -- FIXED
**Section:** 15.4.1
Referenced at two locations in the text but missing from the MessageEnvelope JSON schema block.
**Recommendation:** Add `delegationDepth` to the schema block.
**Fix applied:** Added `"delegationDepth": 0` to the canonical MessageEnvelope JSON schema block in Section 15.4.1.

---

### Policy Engine (POL)

#### POL-042. `INTERCEPTOR_TIMEOUT` return behavior self-contradiction [Medium] -- FIXED
**Section:** 4.8
Opening sentence says error is returned "regardless of `failPolicy`", but same paragraph says it is NOT returned when `failPolicy: fail-open`.
**Recommendation:** Remove "regardless of `failPolicy`" from the opening sentence.
**Fix applied:** Removed "regardless of `failPolicy`" from the opening sentence in the interceptor timeout error code paragraph.

#### POL-043. Timeout table missing 6 policy-relevant configurable timeouts [Medium] -- FIXED
**Section:** 11.3
Missing: `rateLimitFailOpenMaxSeconds`, `quotaFailOpenCumulativeMaxSeconds`, `delegation.usageQuiescenceTimeoutSeconds`, `delegation.cascadeTimeoutSeconds`, `delegation.maxTreeRecoverySeconds`, `delegation.maxLevelRecoverySeconds`.
**Recommendation:** Add all 6 to the timeout table.
**Fix applied:** Added all 6 timeouts to the Section 11.3 timeout table with correct defaults, Helm paths, and source section references.

---

### Execution Modes (EXM)

#### EXM-035. `maxTaskRetries` missing from all `taskPolicy` YAML examples [Medium] — **Fixed**
**Section:** 5.2
Described as a `taskPolicy` field but absent from all three YAML examples. Implementers won't know it exists.
**Recommendation:** Add `maxTaskRetries` to at least one YAML example.
**Resolution:** Added `maxTaskRetries: 1` with explanatory comment to the most complete `taskPolicy` YAML example in Section 5.2.

#### EXM-036. Per-slot OOM attribution contradicts "no per-slot cgroup" statement [Medium]
**Section:** 5.3
Line references "OOM within the slot's cgroup" but 3 lines later says "no per-slot cgroup subdivision in v1." Direct contradiction.
**Recommendation:** Remove the per-slot cgroup OOM reference and describe the failure as pod-level OOM, or clarify that per-slot cgroups exist.
**Resolution:** Changed "OOM within the slot's cgroup" to "pod-level OOM kill" in the Failure isolation bullet, resolving the contradiction with the "no per-slot cgroup subdivision in v1" statement.

#### EXM-037. `cleanupTimeoutSeconds` undefined for concurrent mode [Medium] — FIXED
**Section:** 5.3
Used in concurrent-workspace slot cleanup formula and CRD validation, but only exists in `taskPolicy`. `concurrentWorkspacePolicy` YAML lacks this field.
**Recommendation:** Add `cleanupTimeoutSeconds` to the `concurrentWorkspacePolicy` schema or reference the `taskPolicy` field.
**Resolution:** Added `cleanupTimeoutSeconds: 60` to the `concurrentWorkspacePolicy` YAML with a comment explaining the per-slot formula (`max(cleanupTimeoutSeconds / maxConcurrent, 5)`) and the constraint (`must be ≥ maxConcurrent × 5`).

#### EXM-038. `scrubPolicy` values are task-mode-only but concurrent mode has `podReuse: true` [Medium] — **Fixed**
**Section:** 5.3, 13.1
`scrubPolicy` is "present when `podReuse: true`" and concurrent mode has `podReuse: true`, but all enumerated scrub values are task-mode-specific. No concurrent-mode scrub value defined.
**Recommendation:** Define a concurrent-mode scrub policy or specify that concurrent mode uses a fixed scrub behavior.
**Resolution:** Extended the `scrubPolicy` field in the `sessionIsolationLevel` table (Section 7.1) to define concurrent-mode values: `"best-effort-per-slot"` for concurrent-workspace mode (same scrub operations applied per-slot) and `"none"` for concurrent-stateless mode (no workspace, no scrub). Also updated `residualStateWarning` to be `true` for concurrent-workspace mode, covering shared process namespace, `/tmp`, cgroup memory, and network stack residual state.

#### EXM-039. Scaling formula burst term incorrectly divided by task-mode `mode_factor` [Medium] — FIXED
**Section:** 5.1
Task-mode `mode_factor` (50) divides the burst term, implying task pods absorb 50x more burst. But task pods process tasks sequentially (1 at a time), so instantaneous burst capacity equals session mode.
**Recommendation:** Apply `mode_factor` only to the steady-state term, not the burst term.
**Resolution:** Introduced `burst_mode_factor` (1.0 for session and task modes, `maxConcurrent` for concurrent mode) and applied it only to the burst term. The steady-state term retains `mode_factor` for lifetime reuse. Updated the explanatory text to distinguish steady-state throughput (lifetime reuse) from burst absorption (instantaneous concurrency).

---

## Cross-Cutting Themes

1. **Phantom references**: Multiple runbooks, alerts, and tables reference metrics, alert names, config params, or webhook names that don't exist in their respective canonical definitions (FLR-038, OBS-036, OBS-037, K8S-035/NET-034).

2. **Cross-reference errors**: Several section cross-references point to wrong sections (CPS-035, CPS-036, SCH-041). These are simple fix-by-number corrections.

3. **Missing table entries**: The canonical Redis key prefix table, erasure scope table, and NetworkPolicy component table each have missing entries that were added in other parts of the spec without updating the authoritative tables (TNT-035/036/037, NET-035/036, STR-042).

4. **State machine / transition incompleteness**: Session lifecycle transitions are inconsistently documented between Section 6.2 and Section 7.2 (SLC-041/042/043), with one transition (`suspended -> expired`) having no defined trigger mechanism.

5. **Naming inconsistencies**: Same concepts use different names in different sections (OBS-036 metric names, SCH-043 field casing, DOC-035 heading names).

6. **Execution mode schema gaps**: Concurrent mode borrows fields and policies from task mode but several are undefined or contradictory in the concurrent context (EXM-036/037/038/039).

7. **Experimentation arithmetic**: Weight representation (percentage vs fraction), metric types (Gauge vs Histogram), and pagination envelope all have internal inconsistencies (EXP-034/035/036).
