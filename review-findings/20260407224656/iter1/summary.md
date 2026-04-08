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
| 1 | K8S-035 / NET-034 | `lenny-pool-config` ghost webhook -- referenced but never formally defined | 13.2, 4.6.1 |
| 2 | WPL-030 | Failover formula 25s wrong -- should be 17s (leaseDuration + retryPeriod, not renewDeadline) | 4.6.1, 4.6.2, 12.3, 17.8 |
| 3 | DEL-039 | `settled=all` redundant mode in `lenny/await_children` | 8.5, 8.8 |
| 4 | FLR-038 | Redis runbook references 5 phantom metrics/alerts/config params | 12.4, 16.1, 16.5, 17.7 |
| 5 | CMP-041 | Salt rotation cannot re-pseudonymize billing records (original user_id already erased) | 12.8 |
| 6 | EXP-033A | Results API cursor format says "use platform-standard" but also "must not use base64-encoded JSON" -- platform standard IS base64-encoded JSON | 11.5 |
| 7 | EXP-033B | Multi-variant hash bucketing formula undefined (only binary control/treatment) | 11.5 |
| 8 | EXP-033C | Gateway creating materialized view at runtime contradicts DDL-through-migrations pattern | 11.5 |
| 9 | POL-041 | Cross-phase priority ordering error -- interceptors at 201-249 described as running between QuotaEvaluator and DelegationPolicyEvaluator, but those fire at different phases | 4.8 |
| 10 | MSG-037 | `delivery_receipt` schema omits `error` from populated-status list | 15.4.1 |
| 11 | CRD-031/032 | Secret shape table missing rows for `vault_transit` and `github` providers | 4.9 |

---

## New Findings

### Kubernetes Infrastructure (K8S)

#### K8S-036. `kubeApiServerCIDR` Helm value has no preflight validation [Medium]
**Section:** 13.2, 17.6
If this value is wrong, gateway egress to kube-apiserver and all fail-closed admission webhook ingress silently break. The preflight Job validates pod/service CIDRs but not the API server CIDR, despite it being deterministically discoverable from within the cluster.
**Recommendation:** Add a preflight check that resolves the kube-apiserver endpoint and verifies it falls within `kubeApiServerCIDR`.

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

#### NET-035. Gateway ingress NetworkPolicy missing port 8443 for LLM proxy traffic [Medium]
**Section:** 13.2
The agent-side policy opens egress to gateway:8443 for LLM proxy, but the gateway's ingress allow-list only mentions port 50051 (gRPC). Under default-deny, all proxy-mode LLM traffic would be dropped.
**Recommendation:** Add port 8443 to the gateway's ingress NetworkPolicy from agent namespaces.

#### NET-036. MinIO missing from `lenny-system` NetworkPolicy component table [Medium]
**Section:** 13.2
Self-managed MinIO at `minio.lenny-system:9000` has no ingress/egress rules. Under default-deny, it would be unreachable.
**Recommendation:** Add a MinIO row to the component table with "(self-managed profile only)" annotation, matching PgBouncer's pattern.

---

### Protocol Design (PRT)

#### PRT-036. Translation Fidelity Matrix tags MCP `ref` as `[exact]` but adapters dereference it [Medium]
**Section:** 15.4.1
The matrix cell acknowledges adapters dereference `lenny-blob://` URIs before sending to external clients, yet still tags `ref` as `[exact]`. Should be `[dropped]`.
**Recommendation:** Change the tag to `[dropped]` matching the OpenAI Completions treatment.

---

### Developer Experience (DXP)

#### DXP-035. `terminate` lifecycle channel `reason` enum missing `task_complete` value [Medium]
**Section:** 4.7, 5.2, 15.4.1
The `reason` enum is `session_complete | budget_exhausted | eviction | operator`, but task-mode between-task signaling uses `terminate(task_complete)`. `task_complete` is not a valid value.
**Recommendation:** Add `task_complete` to the `reason` enum.

#### DXP-036. Task-mode between-task signaling requires lifecycle channel but no tier restriction on task mode [Medium]
**Section:** 5.2, 4.7
Lifecycle channel is Full-tier only, but task mode is not restricted to Full-tier. A Minimum/Standard-tier runtime in a task-mode pool has no way to receive between-task signals.
**Recommendation:** Either restrict task mode to Full-tier runtimes, or define between-task signaling via stdin for lower tiers.

---

### Operator Experience (OPS)

#### OPS-043. `EtcdQuotaNearLimit` alert hardcodes "recommended: 8 GB" but Tier 1 is 4 GB [Medium]
**Section:** 16.5, 4.6.1
The alert description says "recommended: 8 GB" but Section 17.8.2 specifies Tier 1 etcd as 4 GB. Section 4.6.1 correctly differentiates tiers.
**Recommendation:** Parameterize the alert description or use "recommended: see Section 17.8.2".

---

### Multi-Tenancy (TNT)

#### TNT-035. `session_dlq_archive` table missing from erasure scope and tenant deletion order [Medium]
**Section:** 7.2, 12.8
*Duplicate of STR-040.* Stores inter-session messages with user content but is absent from the GDPR erasure scope table and tenant deletion Phase 4 order.
**Recommendation:** Add `session_dlq_archive` to both the erasure scope table and tenant deletion Phase 4.

#### TNT-036. Durable inbox Redis key missing from canonical key prefix table [Medium]
**Section:** 7.2, 12.4
*Duplicate of STR-041.* `t:{tenant_id}:session:{session_id}:inbox` is not listed in the exhaustive key prefix table. Tenant isolation test coverage gap.
**Recommendation:** Add the inbox key pattern to the canonical table.

#### TNT-037. `SemanticCache` Redis key missing from canonical key prefix table [Medium]
**Section:** 12.4
Listed in the erasure scope and explicitly cross-references Section 12.4, but not present in the table.
**Recommendation:** Add the `SemanticCache` key pattern to the table.

---

### Storage (STR)

#### STR-042. Delete-marker lifecycle rule scoped only to checkpoints but versioning is bucket-wide [Medium]
**Section:** 12.5
Delete markers from non-checkpoint prefixes (uploads, transcripts, eviction context) accumulate indefinitely, degrading `ListObjects` performance.
**Recommendation:** Extend the delete-marker lifecycle rule to cover all object prefixes, or add prefix-specific rules for each object type.

---

### Delegation (DEL)

#### DEL-040. Orphan cleanup job only detects root-terminated orphans, not mid-tree orphans [Medium]
**Section:** 8.10
`cascadeOnFailure: detach` creates orphans at any depth when a mid-tree parent terminates. These are invisible to cleanup until the root terminates.
**Recommendation:** Change the predicate to check the direct parent's terminal status, not just the root's.

---

### Session Lifecycle (SLC)

#### SLC-041. `resuming` classified as internal-only but referenced in external API tables [Medium]
**Section:** 15.1
*Overlaps with API-051.* `resuming` is listed as internal-only (never returned in API responses), yet the `derive` endpoint lists it as a valid precondition state and the `resume` endpoint shows transitions through it.
**Recommendation:** Either add `resuming` to the external state table, or remove it from endpoint preconditions and show `resume_pending -> running` as the external transition.

#### SLC-042. `suspended` state transition block omits `cancelled` and `expired` exits [Medium]
**Section:** 6.2 vs 7.2
Section 6.2's `suspended` block lists 4 transitions, but Section 7.2 defines 6 (adding `cancelled` and `expired`). Implementers reading only Section 6.2 would miss two valid exits.
**Recommendation:** Add the two missing transitions to the Section 6.2 `suspended` state block.

#### SLC-043. `suspended -> expired` transition has no defined trigger mechanism [Medium]
**Section:** 7.2, 6.2
Both timers that could trigger `expired` (`maxSessionAge`, `maxIdleTimeSeconds`) are explicitly paused during `suspended`. No `maxSuspendedDuration` or wall-clock deadline is defined.
**Recommendation:** Either remove the transition as unreachable, define a `maxSuspendedDuration` timer, or clarify that delegation lease wall-clock expiry is the trigger.

---

### Observability (OBS)

#### OBS-035. Checkpoint duration SLO missing from burn-rate alerts table [Medium]
**Section:** 16.5
The SLO targets table defines a checkpoint duration SLO and the spec mandates burn-rate alerting for "all" SLOs, but the burn-rate table omits it.
**Recommendation:** Add a `CheckpointDurationBurnRate` entry or exclude checkpoint duration from the "all SLOs" language.

#### OBS-036. Warm pool startup metric has two conflicting names [Medium]
**Section:** 4.6.1, 16.1, 17.7
`lenny_warmpool_pod_startup_duration_seconds` (Section 16.1) vs `lenny_warmpool_warmup_latency_seconds` (Sections 4.6.1, 17.7). Same measurement, different names.
**Recommendation:** Unify to the canonical Section 16.1 name.

#### OBS-037. Runbooks reference three phantom alert names [Medium]
**Section:** 17.7
`WarmPoolBelowMinimum`, `PostgresDown`, `GatewayReplicasLow` do not exist in Section 16.5. The actual alerts are `WarmPoolLow`/`WarmPoolExhausted`, `SessionStoreUnavailable`, `GatewayNoHealthyReplicas`.
**Recommendation:** Update runbook trigger lines to use canonical alert names.

---

### Compliance (CMP)

#### CMP-042. INSERT-only grants and immutability triggers block GDPR erasure operations [Medium]
**Section:** 11.7, 11.2.1, 12.8
The erasure job runs as `lenny_app` (INSERT-only on audit/billing tables), but GDPR erasure requires UPDATE (pseudonymization) and DELETE (audit purge). No elevated role, trigger bypass, or separate grant is defined.
**Recommendation:** Define a `lenny_erasure` database role with scoped UPDATE/DELETE grants on billing and audit tables, used exclusively by the erasure background job. Add immutability trigger bypass via `SET LOCAL lenny.erasure_mode = true` checked in the trigger function.

---

### API Design (API)

#### API-050. `PUT /v1/admin/external-adapters/{name}/validate` uses PUT for side-effecting action [Medium]
**Section:** 15.1
Every other action endpoint uses POST. This endpoint runs a test suite and transitions state.
**Recommendation:** Change to POST.

#### API-052. `terminate` endpoint omits `starting` from valid precondition states [Medium]
**Section:** 15.1
Description says "valid in any non-terminal, non-setup state" but `starting` is non-terminal and non-setup per the external state table, yet is omitted from the precondition column.
**Recommendation:** Add `starting` to the precondition states, or add it to the "setup states" definition.

#### API-053. `dryRun` blanket "all admin POST and PUT" claim is incorrect [Medium]
**Section:** 15.1
Multiple action POST endpoints and the `warm-count` PUT are explicitly excluded from dryRun, contradicting the "all" claim.
**Recommendation:** Change to "Most admin POST and PUT endpoints support `dryRun`" and list the exceptions.

---

### Competitive Positioning (CPS)

#### CPS-035. TTHW paragraph references wrong section for `make run` [Medium]
**Section:** 23.2
References "Section 18" (Build Sequence) instead of Section 17.4 (local dev mode).
**Recommendation:** Change to "Section 17.4".

#### CPS-036. Enterprise persona entry point references wrong section [Medium]
**Section:** 23.2
Directs to "Section 16" (Observability) instead of Sections 4.8, 8, and 10 (enterprise controls).
**Recommendation:** Fix the cross-reference.

#### CPS-037. Phase 0 milestone overstates openness for external contribution [Medium]
**Section:** 18, 23.2
Phase 0 says "repository open for external contribution" but Phase 17a says no external PR solicitation before 17a completes. At Phase 0, there's no CONTRIBUTING.md, no `make run`, no documentation.
**Recommendation:** Reword to "repository publicly visible" or "repository available for early visibility" rather than "open for external contribution".

---

### Warm Pool (WPL)

#### WPL-031. minWarm recommended values omit safety_factor from formula [Medium]
**Section:** 17.8
Table values match `claim_rate * (failover + startup)` without the `safety_factor` multiplier that the formula immediately below includes. Tier 2: 175 vs 262.5; Tier 3: 1050 vs 1260.
**Recommendation:** Either include `safety_factor` in the computed values, or remove it from the formula and note it as an operator-applied adjustment.

---

### Credentials (CRD)

#### CRD-033. Secret shape table "single data key" claim contradicts multi-key entries [Medium]
**Section:** 4.9
Introductory sentence says each Secret has a "single `data` key" but `aws_bedrock` has two keys and `azure_openai` has three.
**Recommendation:** Change to "one or more `data` keys" or "the following `data` keys".

---

### Content Model & Schemas (SCH)

#### SCH-041. Schema versioning cross-reference cites Section 8.7 instead of 8.8 for TaskRecord [Medium]
**Section:** 15.5
"TaskRecord (Section 8.7)" should be "TaskRecord (Section 8.8)". Section 8.7 is File Export Model.
**Recommendation:** Fix the cross-reference.

#### SCH-042. Inbound MCP translation table uses wrong mimeType source for ImageContent [Medium]
**Section:** 15.4.1
Both ImageContent rows say `OutputPart.mimeType` source is `url.mimeType`, but MCP's `ImageContent` has `mimeType` as a top-level field. There is no `url.mimeType` nested field.
**Recommendation:** Change `url.mimeType` to `mimeType` in both rows.

#### SCH-043. `tool_result` field naming inconsistency: `isError` vs `is_error` [Medium]
**Section:** 4.8, 15.4.1
Stdin protocol uses `isError` (camelCase), interceptor payloads use `is_error` (snake_case) for the same field on the same data structure.
**Recommendation:** Unify to `isError` (matching MCP convention and the stdin protocol).

---

### Build Sequence (BLD)

#### BLD-035. Phase 5.4 deliverables infeasible on managed Kubernetes [Medium]
**Section:** 18
`EncryptionConfiguration` manifest in Helm chart and `etcdctl get` CI gate are impossible on EKS/GKE/AKS. The spec itself acknowledges this limitation in Section 17.6 (preflight emits non-blocking warning only).
**Recommendation:** Scope Phase 5.4 deliverables to self-managed clusters; for managed K8s, document the provider-specific alternative (e.g., AWS KMS plugin, GKE application-layer encryption).

---

### Failure Modes (FLR)

#### FLR-039. Circuit breaker Redis fallback behavior undefined [Medium]
**Section:** 11.6, 12.4
Circuit breaker state is in Redis with a 5-second in-process cache. The Redis failure behavior table omits circuit breakers. When Redis is unavailable and cache expires, behavior is undefined -- fail-open would silently re-enable operator-blocked traffic during incidents.
**Recommendation:** Add circuit breaker to the Redis failure behavior table. Define fail-closed behavior (circuit breaker state persists as last-known until Redis recovers) since circuit breakers are incident management tools used during infrastructure degradation.

---

### Experimentation (EXP)

#### EXP-034. `variant_weight` defined as percentage but used as raw multiplier in formulas [Medium]
**Section:** 11.5, 5.1
YAML shows `weight: 10` (percentage), but scaling formulas use `variant_weight` as a multiplier. A 10% variant would produce 10x expected pool size.
**Recommendation:** Normalize: either define weight as a fraction (0.0-1.0) in YAML and formulas, or add `/ 100` in the formula where weight is used.

#### EXP-035. `lenny_eval_score` Gauge metric cannot support mean computation for rollback [Medium]
**Section:** 16.1, 11.5
Rollback trigger requires "mean safety score" but Prometheus Gauge only stores the last value.
**Recommendation:** Change to Histogram or Summary, or use a separate counter pair (sum + count).

#### EXP-036. Results API response violates standard pagination envelope [Medium]
**Section:** 11.5, 15.1
Uses `{ experiment_id, variants, cursor }` instead of the `{ items, cursor, hasMore, total }` envelope defined in Section 15.1, despite being listed as a paginated endpoint.
**Recommendation:** Wrap the response in the standard pagination envelope.

---

### Document Quality (DOC)

#### DOC-035. Duplicate "Approval Modes" heading creates navigation ambiguity [Medium]
**Section:** 8.4, 8.6
Section 8.4 "Approval Modes" covers delegation approval. Section 8.6 subheading "Approval Modes" covers lease extension approval. Different concepts, identical names.
**Recommendation:** Rename Section 8.6's subheading to "Lease Extension Approval Modes".

#### DOC-036. Orphaned footnote number [Low]
**Section:** 8.6
Line 3198 has footnote `4` with no corresponding `1`/`2`/`3` anywhere in the document.
**Recommendation:** Renumber to `1` or convert to inline note.

---

### Messaging (MSG)

#### MSG-039. `delivery: "immediate"` behavior undefined for `input_required` sessions [Medium]
**Section:** 15.4.1, 7.2
`immediate` says gateway interrupts and delivers for `running` sessions. `input_required` is a sub-state of `running`. But path 4 says stdin delivery is impossible during `input_required` -- messages are unconditionally buffered. Contradiction.
**Recommendation:** Add explicit clause that `immediate` does not override path 4 buffering for `input_required`.

#### MSG-040. `delegationDepth` described as MessageEnvelope field but absent from canonical JSON schema [Medium]
**Section:** 15.4.1
Referenced at two locations in the text but missing from the MessageEnvelope JSON schema block.
**Recommendation:** Add `delegationDepth` to the schema block.

---

### Policy Engine (POL)

#### POL-042. `INTERCEPTOR_TIMEOUT` return behavior self-contradiction [Medium]
**Section:** 4.8
Opening sentence says error is returned "regardless of `failPolicy`", but same paragraph says it is NOT returned when `failPolicy: fail-open`.
**Recommendation:** Remove "regardless of `failPolicy`" from the opening sentence.

#### POL-043. Timeout table missing 6 policy-relevant configurable timeouts [Medium]
**Section:** 11.3
Missing: `rateLimitFailOpenMaxSeconds`, `quotaFailOpenCumulativeMaxSeconds`, `delegation.usageQuiescenceTimeoutSeconds`, `delegation.cascadeTimeoutSeconds`, `delegation.maxTreeRecoverySeconds`, `delegation.maxLevelRecoverySeconds`.
**Recommendation:** Add all 6 to the timeout table.

---

### Execution Modes (EXM)

#### EXM-035. `maxTaskRetries` missing from all `taskPolicy` YAML examples [Medium]
**Section:** 5.2
Described as a `taskPolicy` field but absent from all three YAML examples. Implementers won't know it exists.
**Recommendation:** Add `maxTaskRetries` to at least one YAML example.

#### EXM-036. Per-slot OOM attribution contradicts "no per-slot cgroup" statement [Medium]
**Section:** 5.3
Line references "OOM within the slot's cgroup" but 3 lines later says "no per-slot cgroup subdivision in v1." Direct contradiction.
**Recommendation:** Remove the per-slot cgroup OOM reference and describe the failure as pod-level OOM, or clarify that per-slot cgroups exist.

#### EXM-037. `cleanupTimeoutSeconds` undefined for concurrent mode [Medium]
**Section:** 5.3
Used in concurrent-workspace slot cleanup formula and CRD validation, but only exists in `taskPolicy`. `concurrentWorkspacePolicy` YAML lacks this field.
**Recommendation:** Add `cleanupTimeoutSeconds` to the `concurrentWorkspacePolicy` schema or reference the `taskPolicy` field.

#### EXM-038. `scrubPolicy` values are task-mode-only but concurrent mode has `podReuse: true` [Medium]
**Section:** 5.3, 13.1
`scrubPolicy` is "present when `podReuse: true`" and concurrent mode has `podReuse: true`, but all enumerated scrub values are task-mode-specific. No concurrent-mode scrub value defined.
**Recommendation:** Define a concurrent-mode scrub policy or specify that concurrent mode uses a fixed scrub behavior.

#### EXM-039. Scaling formula burst term incorrectly divided by task-mode `mode_factor` [Medium]
**Section:** 5.1
Task-mode `mode_factor` (50) divides the burst term, implying task pods absorb 50x more burst. But task pods process tasks sequentially (1 at a time), so instantaneous burst capacity equals session mode.
**Recommendation:** Apply `mode_factor` only to the steady-state term, not the burst term.

---

## Cross-Cutting Themes

1. **Phantom references**: Multiple runbooks, alerts, and tables reference metrics, alert names, config params, or webhook names that don't exist in their respective canonical definitions (FLR-038, OBS-036, OBS-037, K8S-035/NET-034).

2. **Cross-reference errors**: Several section cross-references point to wrong sections (CPS-035, CPS-036, SCH-041). These are simple fix-by-number corrections.

3. **Missing table entries**: The canonical Redis key prefix table, erasure scope table, and NetworkPolicy component table each have missing entries that were added in other parts of the spec without updating the authoritative tables (TNT-035/036/037, NET-035/036, STR-042).

4. **State machine / transition incompleteness**: Session lifecycle transitions are inconsistently documented between Section 6.2 and Section 7.2 (SLC-041/042/043), with one transition (`suspended -> expired`) having no defined trigger mechanism.

5. **Naming inconsistencies**: Same concepts use different names in different sections (OBS-036 metric names, SCH-043 field casing, DOC-035 heading names).

6. **Execution mode schema gaps**: Concurrent mode borrows fields and policies from task mode but several are undefined or contradictory in the concurrent context (EXM-036/037/038/039).

7. **Experimentation arithmetic**: Weight representation (percentage vs fraction), metric types (Gauge vs Histogram), and pagination envelope all have internal inconsistencies (EXP-034/035/036).
