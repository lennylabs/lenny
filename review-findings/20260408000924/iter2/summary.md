# Technical Design Review Findings — 2026-04-08 (Iteration 2)

**Document reviewed:** `technical-design.md` (9,926 lines)
**Review framework:** `review-povs.md` (25 perspectives)
**Iteration:** 2 of 8 — continuation from iteration 1
**Total findings:** ~65 across 25 review perspectives
**Deduplicated findings:** ~60 (5 cross-perspective duplicates removed)

## Findings Summary

| Severity | Count |
| -------- | ----- |
| Critical | 0     |
| High     | 3     |
| Medium   | ~42   |
| Low      | ~15   |

### Carried Forward from Iteration 1 (still present / skipped)

| # | ID | Finding | Status |
|---|------|---------|--------|
| 1 | K8S-035 / NET-034 | `lenny-pool-config` ghost webhook — referenced but never formally defined | Skipped |
| 2 | WPL-030 | Failover formula 25s — intentionally conservative | Skipped |
| 3 | DEL-039 | `settled=all` redundant mode in `lenny/await_children` | Skipped |
| 4 | FLR-038 | Redis runbook references phantom metrics/alerts/config params | Skipped |
| 5 | CMP-041 | Salt rotation cannot re-pseudonymize billing records | Skipped |
| 6 | POL-041 | Cross-phase priority ordering error | Skipped |
| 7 | MSG-037 | `delivery_receipt` schema omits `error` from populated-status list | Skipped |
| 8 | CRD-031/032 | Secret shape table missing rows for `vault_transit` and `github` providers | Skipped |
| 9 | DOC-036 | Orphaned footnote number 4 | Carried forward |

### Cross-Perspective Duplicates (removed from totals)

| Primary | Duplicate | Topic |
|---------|-----------|-------|
| K8S-048 | SLC-053, FLR-049 | Adapter CRD write contradicts zero-RBAC agent pod security model |
| SLC-052 | API-063 | `terminate` endpoint precondition states omit `created` |
| PRT-045 | MSG-051 | Protocol mapping table doesn't cover session-level states |

### High Findings

| # | ID | Perspective | Finding | Section |
|---|------|-------------|---------|---------|
| 1 | NET-043 | Network Security | `dnsPolicy: cluster-default` DNS egress rule lacks pod-level scoping — degrades DNS security for all pools in namespace | 13.2 |
| 2 | SCL-047 | Scalability | Tier 2 `burst_arrival_rate` (20/s) below sustained rate (30/s), undersizing `minReplicas` on Prometheus Adapter path | 17.8.2, 16.5 |
| 3 | API-064 | API Design | No admin API endpoints for `runtime_tenant_access` / `pool_tenant_access` grants | 15.1, 4.2 |

---

## Detailed Findings by Perspective

---

## 1. Kubernetes Infrastructure (K8S)

### K8S-045. Fallback claim path creates SandboxClaim without webhook validation against existing claims [Medium]
**Section:** 4.6.1
The Postgres fallback claim path queries the `agent_pod_state` mirror table (which may be stale — the fallback triggers when WPC is down) and creates a `SandboxClaim` CRD. A race exists between the normal CRD-based claim path and the Postgres fallback: a gateway replica could claim a pod via the Kubernetes API while the Postgres mirror still shows it as `idle`, allowing a second gateway replica to attempt a duplicate `SandboxClaim`. The `lenny-sandboxclaim-guard` webhook description says it "rejects the request if `.status.phase` is not `idle`" — this reads as checking the incoming claim's phase, not whether an existing claim already targets the same `Sandbox`.
**Recommendation:** Clarify that the `lenny-sandboxclaim-guard` webhook checks whether any non-idle `SandboxClaim` already exists for the target `Sandbox`, or that the Postgres fallback path validates against the `SandboxClaim` list before creating a claim.

### K8S-046. Concurrent-workspace preStop budget can exceed node drain timeout [Medium]
**Section:** 5.2, 10.1
With `maxConcurrent: 8` and 512 MB workspaces (90s cap per slot), the CRD validation formula requires `terminationGracePeriodSeconds` >= 840s (14 minutes). Many cluster automation tools (kOps, EKS, GKE) impose drain deadlines of 1800s or less. A 14-minute grace period on a single pod can block an entire node drain, and if the drain timeout is hit, Kubernetes sends SIGKILL — the exact data loss scenario the tiered checkpoint caps are designed to prevent.
**Recommendation:** Document the interaction between high `terminationGracePeriodSeconds` and cluster-level drain timeouts. Add a CRD validation warning when the computed value exceeds 600s. Alternatively, specify that concurrent-workspace checkpoint during preStop should be parallelized across slots.

### K8S-047. WarmPoolController RBAC grants NetworkPolicy read access but Section 13.2 implies zero NetworkPolicy RBAC [Low]
**Section:** 4.6.3, 13.2
Section 13.2 states "The warm pool controller does NOT create or modify NetworkPolicies — it only labels pods" implying zero NetworkPolicy RBAC, but Section 4.6.3 grants read-only access for CIDR drift detection.
**Recommendation:** Add a note in Section 13.2 acknowledging the read-only exception, cross-referencing Section 4.6.3.

### K8S-048. Adapter CRD write during coordinatorHoldTimeout requires RBAC not specified for agent pods [High → Primary for SLC-053 and FLR-049]
**Section:** 10.1, 10.3, 4.6.3, 13.2
Section 10.1 states the adapter "MUST write `Sandbox.status.phase = failed` to the CRD via a direct Kubernetes API call" when `coordinatorHoldTimeoutSeconds` expires. But Section 10.3 states agent pod ServiceAccounts have "zero RBAC bindings — no Kubernetes API access." Section 13.2's NetworkPolicy also blocks egress to kube-apiserver from agent pods. The Kyverno/Gatekeeper policy prohibits RoleBindings on agent SAs. This creates a three-layer impossibility: no RBAC, admission blocks adding RBAC, no network path to the API server.
**Recommendation:** Remove the MUST CRD write requirement. Either (a) rely on the orphan session reconciler (expand its scope per SLC-054), or (b) have the adapter send a final gRPC signal to the gateway before exiting, or (c) grant a narrow RBAC exception and document it as a security-model exception.

### K8S-049. `PoolWarmingUp` condition cleared at idlePodCount >= 1 creates flapping risk [Low]
**Section:** 5.2
At a pool boundary where pods are claimed as fast as they become idle, the condition flaps rapidly between True and False.
**Recommendation:** Add a stabilization window (e.g., 10 seconds) before clearing the condition.

---

## 2. Security & Threat Modeling (SEC)

### SEC-048. Task-mode scrub verification does not cover credential file [Medium]
**Section:** 5.2
Step 3b removes `/run/lenny/credentials.json`, but step 6 (scrub verification) only stat-checks the workspace path, `/tmp`, and `/dev/shm`. If step 3b fails silently, the next task inherits the previous task's credential material.
**Recommendation:** Add `/run/lenny/credentials.json` to the step 6 verification check.

### SEC-049. `concurrent-stateless` mode has no tenant pinning in multi-tenant deployments [Medium]
**Section:** 5.2
Task-mode and concurrent-workspace pods are explicitly tenant-pinned. `concurrent-stateless` pods have no documented tenant pinning. In multi-tenant deployments, multiple tenants' requests could be routed to the same pod.
**Recommendation:** Either require tenant pinning for `concurrent-stateless` pods, or prohibit this mode in multi-tenant deployments, or require explicit deployer acknowledgment.

### SEC-050. `lenny/send_message` lacks explicit cross-tenant validation [Medium]
**Section:** 7.2, 8.5
The `messagingScope` enforcement validates structural reachability within the delegation tree but does not normatively state that the gateway validates the target `taskId` belongs to the same `tenant_id`. While tree structure implies same-tenant ancestry, the in-memory routing path may not enforce this.
**Recommendation:** Add a normative statement: "The gateway MUST validate that the target session's `tenant_id` matches the calling session's `tenant_id` before routing."

### SEC-051. `anthropic_direct` provider leases long-lived API key in direct mode [Low]
**Section:** 4.9
The `materializedConfig.apiKey` contains the actual long-lived Anthropic API key. Unlike AWS STS or GCP tokens, this key remains valid indefinitely after the Lenny lease expires.
**Recommendation:** Add a security note to the `materializedConfig` schema for `anthropic_direct` documenting the asymmetry.

### SEC-052. Delegation cycle detection does not cover cross-environment identity aliasing [Low]
**Section:** 8.2
Cycle detection uses `(runtime_name, pool_name)` tuples. The same `runtime_name` in different pools across environments would not be detected as a cycle.
**Recommendation:** Document explicitly that cycle detection operates on `(runtime_name, pool_name)` and does not treat same `runtime_name` across different pools as equivalent.

### SEC-053. Webhook `callbackSecret` storage classification not specified [Low]
**Section:** 14
The `callbackSecret` is described as "stored encrypted" but the encryption mechanism, data tier, and storage location are unspecified.
**Recommendation:** Specify the classification (T3/T4), storage location, and encryption mechanism.

---

## 3. Network Security (NET)

### NET-043. `dnsPolicy: cluster-default` supplemental DNS egress rule lacks pod-level scoping [High]
**Section:** 13.2
When a pool opts out of dedicated CoreDNS, a supplemental DNS egress rule to `kube-system` is rendered. The spec does not specify that this rule must be scoped to only the opted-out pool's pods. Since NetworkPolicies are additive, all managed pods in the namespace gain a permitted egress path to `kube-system` CoreDNS, bypassing the dedicated CoreDNS's query logging, rate limiting, and response filtering.
**Recommendation:** The supplemental DNS egress rule must use a `podSelector` scoped to only opted-out pools (e.g., matching `lenny.dev/dns-policy: cluster-default`). Add this label to the immutability enforcement in the `lenny-label-immutability` webhook.

---

## 4. Scalability & Performance (SCL)

### SCL-047. Tier 2 `burst_arrival_rate` below sustained session creation rate, undersizing `minReplicas` [High]
**Section:** 17.8.2, 16.5
The burst formula tables use `burst_arrival_rate = 20/s` for Tier 2. Section 16.5 defines Tier 2 sustained session creation rate as 30/s. A peak rate cannot be lower than the sustained rate. Impact on Path B: `minReplicas` is 6 instead of 9 (50% undersized), causing 600 sessions (33%) to be rejected during burst. KEDA path is accidentally unaffected (floor of 3 matches).
**Recommendation:** Replace `burst_arrival_rate = 20/s` with `30/s` (or higher) for Tier 2. Update Path B `minReplicas` to 9.

### SCL-048. Postgres write headroom margin cited inconsistently (18% vs 23%) [Medium]
**Section:** 12.3, 16.5
Section 12.3: "approximately 18%". Section 16.5 (`PostgresWriteBurstIops` alert): "approximately 23%". The actual value is `(1600-1300)/1600 = 18.75%`. The 23% figure is arithmetically incorrect, leading operators to believe they have more headroom than they do.
**Recommendation:** Replace "approximately 23%" in Section 16.5 with "approximately 19%" to match the arithmetic.

---

## 5. Protocol Design (PRT)

### PRT-044. `/.well-known/agent.json` returns JSON array, violating A2A spec's single-object contract [Medium]
**Section:** 15.1, 21.1
Both references state the endpoint returns "a JSON array of agent card objects." The A2A spec defines this endpoint as returning a single `AgentCard` JSON object. Standard A2A clients will fail to parse the response.
**Recommendation:** Return a single agent card at the well-known endpoint (require deployers to designate a primary runtime), or serve per-runtime endpoints, or acknowledge the deviation explicitly.

### PRT-045. Protocol mapping table (Section 8.8) does not cover session-level states [Medium → Primary for MSG-051]
**Section:** 8.8, 15.1, 7.2
The protocol mapping table maps only canonical task states. The external API exposes additional session states (`suspended`, `resume_pending`, `awaiting_client_action`, `created`, `finalizing`, `ready`, `starting`) with no defined MCP/A2A mapping.
**Recommendation:** Extend the protocol mapping table to cover all externally visible session states, or explicitly define a mapping strategy (e.g., pre-running states → `submitted`, `suspended`/`resume_pending` → `working`, `awaiting_client_action` → `input-required`).

### PRT-046. `expired` state mapping note contradicts the mapping table [Low]
**Section:** 8.8
Table shows `expired` → `failed`. Note below says `failed`/`canceled`. The slash notation implies the mapping could be either.
**Recommendation:** Remove the `/canceled` alternative from the note text.

---

## 6. Developer Experience (DXP)

### DXP-047. `adapterLocalTools` missing from adapter manifest field reference and JSON example [Medium]
**Section:** 4.7
Section 15.4.1 introduces `adapterLocalTools` but the adapter manifest JSON example and field reference table in Section 4.7 do not include it. Runtime authors reading Section 4.7 would not know the field exists.
**Recommendation:** Add `adapterLocalTools` to the manifest JSON example and field reference table.

### DXP-048. Standard-tier and Full-tier echo pseudocode omit required stdout flush calls [Medium]
**Section:** 15.4.4
DXP-046 fix added `flush(stdout)` to Minimum-tier sample but not Standard-tier and Full-tier samples. A runtime author copying either sample would experience silent session hangs.
**Recommendation:** Add `flush(stdout)` after every `write_line(stdout, ...)` in both samples.

### DXP-049. Full-tier sample handles `deadline_approaching` without declaring `deadline_signal` capability [Medium]
**Section:** 15.4.4, 4.7
The Full-tier pseudocode declares `supported = ["checkpoint", "interrupt"]` but then handles `deadline_approaching` in its background handler, which requires the `deadline_signal` capability.
**Recommendation:** Update the `supported` array to `["checkpoint", "interrupt", "deadline_signal"]`.

---

## 7. Operator Experience (OPS)

### OPS-056. Day 0 walkthrough uses undefined CLI command `lenny-ctl pool status` [Medium]
**Section:** 17.6, 24.3
The walkthrough instructs `lenny-ctl pool status --name echo-pool` at step 6. This command does not exist; the CLI reference defines `lenny-ctl admin pools get <name>`.
**Recommendation:** Replace with `lenny-ctl admin pools get echo-pool`.

### OPS-057. `lenny-ctl preflight` cannot be both a thin Admin API client and a pre-deployment tool [Medium]
**Section:** 17.6, 24, 24.2
Section 24 says `lenny-ctl` is "a thin client over the Admin API" requiring `LENNY_API_URL`. But the Day 0 walkthrough invokes `lenny-ctl preflight` before `helm install` — when neither gateway nor API exists.
**Recommendation:** Either (a) acknowledge `preflight` as an exception that embeds check logic directly, or (b) split into standalone pre-install mode and API-backed post-install mode.

---

## 8. Multi-Tenancy (TNT)

### TNT-045. Tenant deletion Phase 4 omits access tables, role mappings, and tenant config record [Medium]
**Section:** 12.8
Phase 4's deletion order does not include `runtime_tenant_access`, `pool_tenant_access`, user role mappings, custom role definitions, or the tenant configuration record (which holds KMS-encrypted `erasure_salt`).
**Recommendation:** Add a Phase 4 sub-step that deletes these records, with explicit `erasure_salt` KMS key destruction.

### TNT-046. Tenant-scoping model for delegation policies, connectors, experiments, external adapters unspecified [Medium]
**Section:** 4.2, 10.2, 15.1
Section 4.2 enumerates which records carry `tenant_id` (sessions, tasks, quotas, tokens, memory store, credential pool store) and declares runtimes/pools as platform-global. Delegation policies, connectors, experiments, and external adapters fall into neither declared category.
**Recommendation:** Explicitly classify each resource type as tenant-scoped with RLS or platform-global with access-table filtering.

### TNT-047. `platform-admin` RLS bypass path unspecified [Medium]
**Section:** 4.2, 10.2, 12.3
The RLS model requires `SET LOCAL app.current_tenant` for every query, but `platform-admin` needs cross-tenant access. The mechanism (sentinel value, `BYPASSRLS` role, per-tenant iteration) is not specified.
**Recommendation:** Specify the mechanism and add test coverage verifying it works only through the intended path.

---

## 9. Storage Architecture (STR)

### STR-053. Storage quota pre-upload check is non-atomic [Medium]
**Section:** 11.2
The pre-upload check reads `storage_bytes_used` from Redis non-atomically. N concurrent uploads each read the same counter value and all proceed, overshooting the quota. The delegation budget uses an atomic Lua script for this reason, but storage quota does not.
**Recommendation:** Use an atomic Redis Lua script for the pre-upload check (read, check, and reserve in one operation), matching the delegation budget pattern.

### STR-054. `billing_seq_{tenant_id}` DDL uses string interpolation with no `tenant_id` format constraint [Medium]
**Section:** 11.2.1, 15.1
`CREATE SEQUENCE IF NOT EXISTS billing_seq_{tenant_id}` interpolates `tenant_id` directly into DDL. No format constraint is defined — a `tenant_id` containing SQL metacharacters could inject arbitrary SQL.
**Recommendation:** Add a format validation rule: `tenant_id` MUST match `^[a-zA-Z0-9_-]{1,128}$`. Enforce at tenant creation and OIDC claim extraction.

### STR-055. `delegation_tree_budget` and `session_checkpoint_meta` tables missing from tenant deletion Phase 4 [Low]
**Section:** 12.8, 11.2, 10.1
These tenant-scoped tables are not listed in Phase 4's deletion sequence.
**Recommendation:** Add them to Phase 4 before `SessionStore`, or specify they use FK `ON DELETE CASCADE`.

---

## 10. Recursive Delegation (DEL)

### DEL-051. `maxChildrenTotal` and `maxParallelChildren` not in atomic `budget_reserve.lua` script [Medium]
**Section:** 8.3
The Lua script covers only token budget, token usage, and tree-size counters. `maxChildrenTotal` and `maxParallelChildren` are validated outside the script, creating a TOCTOU window for concurrent `delegate_task` calls.
**Recommendation:** Extend `budget_reserve.lua` to atomically validate these counters, or document the acceptable over-commit window.

### DEL-052. Default delegation slice formula is dimensionally incorrect [Low]
**Section:** 8.3
`min(remaining_parent_budget, deployer_configurable_default_fraction)` takes `min()` of tokens and a unitless fraction. Should be `remaining_parent_budget × defaultDelegationFraction`.
**Recommendation:** Change to the multiplicative form.

### DEL-053. `perChildRetryBudget` field undocumented — appears only in JSON example [Medium]
**Section:** 8.3
The delegation lease JSON example includes `"perChildRetryBudget": 1` but the field has no description anywhere — not in field descriptions, `LeaseSlice` table, extendable/non-extendable lists, or any prose.
**Recommendation:** Add a field description specifying semantics, relationship to `retryPolicy.maxRetries`, budget behavior on retry, and `maxTreeSize` interaction.

### DEL-054. `cascadeOnFailure` naming acknowledged as misleading but not renamed [Low]
**Section:** 8.10
The spec acknowledges this is a misnomer (governs all parent terminal transitions, not only failure) but retains the name. Pre-implementation is the lowest-cost rename opportunity.
**Recommendation:** Rename to `cascadeOnTerminal` or `childTerminationPolicy`.

---

## 11. Session Lifecycle (SLC)

### SLC-052. `terminate` endpoint precondition states omit `created` [Medium → Primary for API-063]
**Section:** 15.1
The Notes column says "Valid in any non-terminal state" but the explicit list omits `created`. `DELETE` correctly accepts `created`. A client wanting to gracefully terminate before uploading files must use `DELETE` (producing `cancelled`) instead of `terminate` (producing `completed`).
**Recommendation:** Add `created` to the valid precondition states for `terminate`.

### SLC-053. Adapter CRD write contradicts zero-RBAC agent pod security model [High → Duplicate of K8S-048]

### SLC-054. Orphan session reconciler scope too narrow [Medium]
**Section:** 10.1
The reconciler checks only `running`/`attached` sessions. A pod can terminate while the session is in `suspended`, `starting`, `finalizing`, or `input_required`. If both coordinator and pod are lost, these sessions are permanently stuck.
**Recommendation:** Expand the reconciler query to check all non-terminal, pod-holding session states.

### SLC-055. No complete session-level state machine diagram for pre-running states [Low]
**Section:** 7.2, 6.2, 15.1
The external states `created`, `finalizing`, `ready`, `starting` do not appear in any state machine diagram. Their transitions are only discoverable by cross-referencing 4+ sections.
**Recommendation:** Add a consolidated session-level state machine diagram covering all states from `created` through terminal.

---

## 12. Observability (OBS)

### OBS-046. Burn-rate alerts have no named metric to query [Medium]
**Section:** 16.1, 16.2, 16.5, 7.1
`StartupLatencyBurnRate`, `StartupLatencyGVisorBurnRate`, and `TTFTBurnRate` must query Prometheus histograms, but no end-to-end histogram metric name is assigned for either SLO. Per-phase histograms exist but no aggregated end-to-end metric.
**Recommendation:** Define `lenny_session_startup_duration_seconds` and `lenny_session_time_to_first_token_seconds` histograms. Add to Section 16.1 and reference in alert definitions.

### OBS-047. Tail-based sampling rule uses dynamic "P99 latency" instead of concrete threshold [Medium]
**Section:** 16.3
The OTel Collector's tail-based sampling processor uses static policies, not dynamically-computed percentiles. "P99 latency" cannot be directly expressed as a Collector policy.
**Recommendation:** Replace with "session creation exceeding 500ms" (the P99 SLO target).

### OBS-048. Section 10.1 and 4.4 metrics absent from Section 16.1 canonical table [Medium]
**Section:** 4.4, 10.1, 16.1
At least 9 metrics defined in body sections (4 from Section 10.1, 5+ from Section 4.4) are absent from the 16.1 canonical table. Alert conditions in 16.5 reference additional absent metrics.
**Recommendation:** Perform a full-document audit of `lenny_` prefixed metric names and add missing entries to Section 16.1.

### OBS-049. `lenny_gateway_request_queue_depth` (primary HPA trigger) absent from Section 16.1 [Low]
**Section:** 4.1, 10.1, 16.1
The primary HPA scale-out trigger is only mentioned in description text of another metric, not as its own row.
**Recommendation:** Add as an explicit row in Section 16.1.

---

## 13. Compliance & Governance (CMP)

### CMP-054. `complianceProfile: gdpr` referenced but not in allowed enum [Medium]
**Section:** 12.8, 18, 11.7
The allowed values are `none`, `soc2`, `fedramp`, `hipaa`. Two locations reference `complianceProfile: gdpr` as an enforcement trigger for the 6-year retention floor. Since `gdpr` is not in the enum, the enforcement condition can never evaluate to true for GDPR-focused deployments.
**Recommendation:** Either add `gdpr` to the enum, or change the enforcement to apply unconditionally across all regulated profiles (`soc2`, `fedramp`, `hipaa`).

### CMP-055. `lenny_erasure` database role cannot INSERT erasure receipts [Medium]
**Section:** 11.7, 12.8
Section 11.7 grants `lenny_erasure` only UPDATE and DELETE. Section 12.8 requires the erasure job to produce erasure receipts stored in the audit trail (INSERT). The role lacks INSERT.
**Recommendation:** Grant `lenny_erasure` a narrowly scoped INSERT on the audit table restricted to `event_type LIKE 'gdpr.%'`.

---

## 14. API Design (API)

### API-063. `terminate` endpoint precondition states omit `created` [Medium → Duplicate of SLC-052]

### API-064. No admin API endpoints for `runtime_tenant_access` / `pool_tenant_access` grants [High]
**Section:** 15.1, 4.2, 4.3
The spec describes these join tables as the mechanism for making platform-global runtimes/pools visible to tenants, but no admin API endpoint exists to create, list, or revoke these grants. Multi-tenant deployments cannot make newly created runtimes/pools visible to any tenant via the API.
**Recommendation:** Add sub-resource endpoints: `POST /v1/admin/runtimes/{name}/tenant-access`, `GET ...`, `DELETE .../tenant-access/{tenantId}`. Mirror for pools. Add `tenantAccess` to the bootstrap seed schema.

### API-065. `billing-correction-reasons` endpoint absent from Section 15.1 admin API table [Medium]
**Section:** 15.1, 11.2.1
Section 11.2.1 describes `POST /v1/admin/billing-correction-reasons` but it has no row in the Section 15.1 API table. No `GET` or `DELETE` endpoints are defined either.
**Recommendation:** Add POST, GET, DELETE endpoints to the Section 15.1 table.

### API-066. `/rotate-token` uses `{name}` while sibling endpoints use `{user_id}` [Medium]
**Section:** 15.1
`invalidate` and `erase` use `{user_id}`. `rotate-token` uses `{name}`. All three operate on user identities.
**Recommendation:** Standardize to `{user_id}` for all three endpoints.

---

## 15. Competitive Positioning (CPS)

No findings. The competitive positioning and open-source strategy sections are internally consistent after prior iteration fixes.

---

## 16. Warm Pool & Pod Lifecycle (WPL)

### WPL-039. Task-mode state machine omits `sdk_connecting` re-warm transition [Medium]
**Section:** 6.2, 6.1
The task-mode state transitions show `task_cleanup → idle` as the only post-scrub path. For SDK-warm pools, Section 6.1 specifies the adapter re-establishes SDK-warm state after scrub, implying `task_cleanup → sdk_connecting → idle`.
**Recommendation:** Add the conditional transition `task_cleanup → sdk_connecting` (when `preConnect: true`, scrub succeeded, not draining) to the task-mode state machine.

### WPL-040. Delegation-adjusted `minWarm` formula drops burst term [Medium]
**Section:** 17.8.2, 4.6.2
The delegation-adjusted formula includes only the steady-state term. The base formula has a second burst term `+ burst_p99_claims × pod_warmup_seconds`. Delegation fan-out is inherently bursty.
**Recommendation:** Add a delegation-adjusted burst term to the formula.

---

## 17. Credential Management (CRD)

### CRD-041. `credential.leased` audit event lacks fields for user-scoped leases [Medium]
**Section:** 4.9.2
The event records `pool_id` and `credential_id`. For user-scoped leases, these are meaningless. The event does not include `source`, `user_id`, or `credential_ref`.
**Recommendation:** Add `source` and conditionally `user_id`/`credential_ref` or `pool_id`/`credential_id` based on source type.

### CRD-042. DELETE credential behavior during proactive lease renewal unspecified [Medium]
**Section:** 4.9
`DELETE /v1/credentials/{credential_ref}` silently removes the record while active leases remain. When proactive renewal fires, the renewal worker has no credential to renew from and no documented fallback.
**Recommendation:** Specify that on renewal of a deleted user-scoped credential, the gateway falls through to the `credentialPolicy` fallback chain.

### CRD-043. Synthetic TTL enforcement for `anthropic_direct` in direct mode unspecified [Medium]
**Section:** 4.9
In direct mode, the pod holds the plaintext API key. The spec defines no mechanism by which the adapter enforces `expiresAt` locally — no `lease_expired` message, no local timer. The runtime could continue using an expired-but-functional key indefinitely.
**Recommendation:** Specify the adapter MUST set a local timer for `expiresAt` and delete the credential file when it fires without a replacement.

---

## 18. Content Model & Schema (SCH)

### SCH-056. `TaskResult.artifactRefs` URI format doesn't match `LennyBlobURI` scheme [Medium]
**Section:** 8.8, 15.4.1
The `LennyBlobURI` scheme is `lenny-blob://{tenant_id}/{session_id}/{part_id}?ttl={seconds}&enc=aes256gcm`. The `TaskResult` example shows `lenny-blob://session_xyz/workspace.tar.gz` — missing `tenant_id`, using a filename instead of `part_id`, and omitting query parameters.
**Recommendation:** Fix the example to match the defined URI format.

### SCH-057. `TaskRecord.state` vs `TaskResult.status` naming inconsistency [Medium]
**Section:** 8.8
`TaskRecord` uses `state`. `TaskResult` uses `status`. Both draw from the same canonical task state enum.
**Recommendation:** Unify on one field name (`state` is more internally consistent).

### SCH-058. `MessageEnvelope` schema omits `slotId` field [Medium]
**Section:** 15.4.1
The `MessageEnvelope` JSON schema does not include `slotId` despite it being a normative protocol field for concurrent-workspace mode, required in inbound and outbound messages per Section 5.2.
**Recommendation:** Add `slotId` to the `MessageEnvelope` schema, protocol reference examples, and field descriptions.

---

## 19. Build Sequence (BLD)

### BLD-047. Phase 12a lists `RotateCredentials` RPC already implemented in Phase 11 [Medium]
**Section:** 18
Phase 11 delivers `RotateCredentials` RPC. Phase 12a lists it again. An agent following the build sequence would implement it twice.
**Recommendation:** Remove `RotateCredentials RPC` from Phase 12a's deliverable list.

### BLD-048. Phase 12b dependency list understates dependency on Phase 5 [Medium]
**Section:** 18
The parallelism note says Phase 12b "depends only on Phase 5.5 and core session infrastructure." Phase 12b's MCP runtime support directly builds on Phase 5's `ExternalAdapterRegistry` and MCP endpoint routing.
**Recommendation:** Update to "Phase 12b depends on Phase 5, Phase 5.5, and core session infrastructure."

### BLD-049. Phase 13.5 load baseline doesn't account for Phase 12 parallel completion uncertainty [Low]
**Section:** 18
If one of the parallel Phase 12 tracks is still in progress when Phase 13 completes, Phase 13.5 benchmarks an incomplete system.
**Recommendation:** Add: "All three Phase 12 tracks must be complete before Phase 13 begins."

---

## 20. Failure Modes & Resilience (FLR)

### FLR-049. Adapter CRD write on hold-state timeout contradicts zero-RBAC model [High → Duplicate of K8S-048]

### FLR-050. Section 11.6 cross-references conflate operator-managed and automatic circuit breakers [Medium]
**Section:** 4.1, 4.3, 10.2, 11.6
Section 11.6 defines operator-managed circuit breakers (Redis-backed, manual open/close). Sections 4.1, 4.3, and 10.2 cross-reference 11.6 for automatic, in-memory circuit breakers with trip conditions and half-open states. An implementer following the 11.6 reference would build the wrong type.
**Recommendation:** Add a paragraph to Section 11.6 distinguishing the two types. Update cross-references to point to the local inline specification.

---

## 21. Experimentation (EXP)

### EXP-042. `EvalResult.experiment_id` typed as `uuid` but experiment identifiers are strings [Medium]
**Section:** 10.7
The Postgres schema declares `experiment_id uuid`. Every other reference uses human-readable strings (e.g., `claude-v2-rollout`). A `uuid` column cannot store these.
**Recommendation:** Change the type from `uuid` to `string`.

### EXP-043. Variant pool sizing formula doesn't account for first-match priority ordering [Low]
**Section:** 4.6.2, 10.7
The formula uses `variant_weight` directly, but first-match priority reduces effective traffic to lower-priority experiments. Over-provisions lower-priority variant pools.
**Recommendation:** Add a note that `variant_weight` slightly over-estimates actual demand for non-highest-priority experiments, bounded by the `safety_factor`.

---

## 22. Document Quality (DOC)

### DOC-046. "Section 5.3" cross-references should be "Section 5.2" [Medium]
**Section:** 7.2 (sessionIsolationLevel response table, lines 2661-2662)
Two references to "Section 5.3" point to the wrong section. Concurrent-workspace slot cleanup and deployer acknowledgment are in Section 5.2, not Section 5.3 (Isolation Profiles).
**Recommendation:** Change both references to "Section 5.2".

---

## 23. Messaging & Conversational Patterns (MSG)

### MSG-051. Protocol mapping table omits session-level states [Medium → Duplicate of PRT-045]

### MSG-052. Dead-letter state table omits `resuming` state [Low]
**Section:** 7.2
The dead-letter handling table categorizes states into Pre-running, Terminal, and Recovering buckets. `resuming` (internal-only, transient between `resume_pending` and `running`) is absent from all buckets.
**Recommendation:** Add `resuming` to the Recovering row.

### MSG-053. `submitted` task state has no corresponding session lifecycle state [Low]
**Section:** 8.8, 7.2, 15.1
The canonical task state machine starts with `submitted → running`, but sessions start at `created`. No session is ever in state `submitted`.
**Recommendation:** Add a note that `submitted` maps to Lenny session states `created`, `finalizing`, `ready`, `starting` (all pre-`running` states).

---

## 24. Policy Engine (POL)

### POL-053. `maxTreeMemoryBytes` not in atomic `budget_reserve.lua` [Medium]
**Section:** 8.2, 8.3
Section 8.2 states `maxTreeMemoryBytes` is tracked "via an atomic Redis counter alongside `maxTreeSize`." But `budget_reserve.lua` only covers 3 counters (token budget, token usage, tree-size). `maxTreeMemoryBytes` enforcement is outside the script, creating a TOCTOU window.
**Recommendation:** Extend `budget_reserve.lua` to include tree memory atomically, or document the bounded over-commit window.

### POL-054. `snapshotPolicyAtLease` doesn't address `maxDelegationPolicy` pool matching [Medium]
**Section:** 8.3
The option snapshots matching pool IDs for `delegationPolicyRef` but doesn't state whether `maxDelegationPolicy` is also snapshotted. If not, a label change mid-tree alters the effective policy despite snapshot mode.
**Recommendation:** Specify that all `DelegationPolicy` resources in the effective computation are snapshotted when the flag is set.

### POL-055. `AdmissionController` listed as evaluator module but absent from interceptor chain specification [Low]
**Section:** 4.8, 11.6
The evaluator table lists `AdmissionController` but it has no priority, phase, or entry in the built-in interceptor table. Ambiguous whether it's a chain participant or a pre-chain check.
**Recommendation:** Clarify its execution model.

### POL-056. Timeout table cites wrong source section for `delegation.usageQuiescenceTimeoutSeconds` [Low]
**Section:** 11.3, 8.3
The timeout table cites Section 8.10 as the source. The actual definition is in Section 8.3.
**Recommendation:** Change source section to 8.3.

---

## 25. Execution Modes (EXM)

### EXM-047. SDK-warm preConnect table contradicts Lenny scrub step 1 for task mode [Medium]
**Section:** 6.1, 5.2
The preConnect table states "the SDK process persists across tasks" for task mode. But scrub step 1 runs `kill -9 -1` as the sandbox user, which kills the agent/SDK process. The follow-on text ("the adapter re-establishes SDK-warm state") is correct; the persistence claim is not.
**Recommendation:** Replace "the SDK process persists across tasks" with "the SDK process is terminated during Lenny scrub step 1."

### EXM-048. Task-mode state machine missing `attached → cancelled` transition [Medium]
**Section:** 6.2
The session state machine includes `attached → cancelled`. The task-mode state machine does not. If a client or parent cancels a task-mode pod's active task, there is no defined transition path.
**Recommendation:** Add `attached → cancelled` to the task-mode state machine with a note on cleanup behavior.
