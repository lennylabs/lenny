# Technical Design Review Findings — 2026-04-07 (Iteration 8)

**Document reviewed:** `docs/technical-design.md` (8,649 lines)
**Review framework:** `docs/review-povs.md`
**Iteration:** 8 (25 agents, 1 per perspective)
**Total findings:** ~60 (0 Critical, 5 High, ~53 Medium, 2 Low)

## High Findings

| # | ID | Finding | Section | Status |
|---|-----|---------|---------|--------|
| 1 | K8S-025 | WarmPoolController RBAC missing `create`/`delete` Sandbox access | 4.6.3 | **Fixed** (partially — SandboxClaim claim rejected) |
| 2 | STR-028 | Billing stream flusher has no replica coordination — concurrent duplicate billing events | 12.3 | **Fixed** |
| 3 | OBS-037 | Availability SLO burn-rate formula is mathematically inverted | 16.5 | **Fixed** |
| 4 | OBS-038 | Head-based sampling at 10% incompatible with 100%-error-sampling requirement | 16.3 | **Fixed** |
| 5 | API-038 | §15.1 "Comprehensive Admin API" table omits operational endpoints from §24 | 15.1 | **Fixed** |

### High Finding Dispositions

**FINDING: K8S-025**
**CHALLENGE RESULT:** Partially correct. The RBAC text listed only `update` on Sandbox for WPC, but WPC owns `spec.*` and `status.*` on Sandbox and creates/deletes pods — so `create`/`delete` verbs were genuinely missing. However, the finding's claim about missing SandboxClaim access is wrong: §4.6.3 explicitly states SandboxClaim is owned by "Gateway (not a controller)" — WPC does not need SandboxClaim verbs.
**STATUS:** Fixed
**RATIONALE:** An implementor writing the WPC RBAC ClusterRole would get 403 on pod creation. Genuine error.
**CHANGES:** Updated §4.6.3 RBAC text: WPC now has `create`/`update`/`delete` on `Sandbox` (was: `update` only). SandboxClaim verbs NOT added (correctly Gateway-owned).

**FINDING: STR-028**
**CHALLENGE RESULT:** Factually correct and a genuine problem. The billing stream flusher was described as "a background flusher goroutine per tenant" that polls the Redis stream. With N gateway replicas, N goroutines would each XRANGE the same entries and insert duplicate rows with distinct Postgres sequence numbers. No leader election or consumer group coordination was specified.
**STATUS:** Fixed
**RATIONALE:** Would cause N-fold billing event duplication in any multi-replica deployment. Genuine design flaw.
**CHANGES:** Updated §11.2.1 (billing stream flusher) to specify Redis consumer group coordination: `XGROUP CREATE` with group name `billing-flusher`, per-replica consumer IDs, `XREADGROUP` instead of polling, `XACK`+`XDEL` on successful INSERT, and `XAUTOCLAIM` for pending-entry reclaim from crashed replicas.

**FINDING: OBS-037**
**CHALLENGE RESULT:** Factually correct. The formula `(1 - error_rate) / (1 - slo_target)` computes the ratio of actual success rate to error budget. At 0% errors this yields 1/(1-target) — a huge burn rate. At 100% errors this yields 0 — zero burn. Completely inverted. The standard Google SRE burn-rate formula is `error_rate / (1 - slo_target)`.
**STATUS:** Fixed
**RATIONALE:** Any SRE implementing this formula would get alerts that fire when everything is healthy and stay silent during outages. Unambiguous mathematical error.
**CHANGES:** Changed formula from `(1 - error_rate) / (1 - slo_target)` to `error_rate / (1 - slo_target)` in §16.5.

**FINDING: OBS-038**
**CHALLENGE RESULT:** Factually correct. The spec said "Head-based sampling at 10%" but then required "100% sampling for errors." Head-based sampling makes the keep/drop decision at trace creation time, before errors or latency are known — making retroactive 100% error sampling impossible. The spec also said "the collector handles sampling" which contradicted "head-based." The architecture actually needs tail-based sampling in the Collector.
**STATUS:** Fixed
**RATIONALE:** An implementor configuring head-based sampling at 10% would lose 90% of error traces. The existing text was internally contradictory.
**CHANGES:** Rewrote §16.3 sampling paragraph: gateway now emits 100% of traces to the Collector; the Collector applies tail-based sampling (10% probabilistic default, 100% for errors/slow requests). Explicitly states sampling decisions are made after spans complete.

**FINDING: API-038**
**CHALLENGE RESULT:** Factually correct. The §15.1 heading says "Comprehensive Admin API" and §24 defines REST API mappings for 12+ endpoints (pool upgrade lifecycle, bootstrap-override DELETE, credential pool membership, quota reconcile, user token rotation, preflight, billing correction approval) that were absent from the §15.1 table.
**STATUS:** Fixed
**RATIONALE:** An implementor treating §15.1 as the authoritative API surface would miss the entire pool upgrade state machine and several operational endpoints. The "Comprehensive" label makes omission actively misleading.
**CHANGES:** Added 13 endpoint rows to the §15.1 admin API table covering pool upgrade lifecycle (start/proceed/pause/resume/rollback/status), bootstrap-override DELETE, credential pool membership POST, quota reconcile POST, user token rotation POST, billing correction approval POST, and preflight GET. Added cross-reference note pointing to §24 for CLI wrappers.

_Per-perspective detailed findings are in the subagent outputs and individual files in this directory._

---

## Recovered Detailed Findings by Perspective

---

## P2. Security & Threat Modeling

### SEC-039 `PostAuth` MODIFY Prohibition on `user_id`/`tenant_id` Has No Enforcement Mechanism [Medium]
**Section:** 4.8

External interceptors at priority 101-199 returning a MODIFY that rewrites `user_id` in serialized metadata would cause QuotaEvaluator and DelegationPolicyEvaluator to enforce against an attacker-supplied identity. The prohibition is stated but the gateway implementation contract for enforcing it (stripping overwrites) is absent.

**Recommendation:** Add normative enforcement: gateway extracts `user_id`/`tenant_id` from original authenticated context before passing interceptor result downstream. If MODIFY alters these fields, gateway strips the change and emits `security.interceptor_identity_overwrite_attempt` audit event.

### SEC-040 JWT KMS Key Rotation Has No Early-Close Mechanism for Overlap Window [Medium]
**Section:** 10.2

If a signing key is suspected compromised, tokens signed with it remain valid for the full 24h overlap window. No mechanism exists to close the window early or force JWT re-issuance. Compare: cert deny list (§10.3) provides immediate revocation for pod certificates.

**Recommendation:** Add `revokedKidSet` (in-memory, Redis pub/sub propagated). Emergency rotation adds old `kid` to revoked set immediately. Active sessions must re-authenticate on next interaction.

### SEC-041 Experiment Targeting Webhook URL Has No SSRF Mitigations [Medium]
**Section:** 10.7

`experimentTargeting.generic.webhookUrl` is admin-configured but has no SSRF protections. A compromised tenant-admin can point it at internal services. Called once per session creation — high-frequency SSRF vector.

**Recommendation:** Apply same URL validation as `callbackUrl` (§14): HTTPS required, reject private/reserved IPs, reject metadata hostnames. Add `targetingWebhookAllowedDomains` for internal endpoints.

---

## P3. Network Security & Isolation

### NET-029 `lenny-label-immutability` Webhook Does Not Cover `lenny.dev/delivery-mode` [Medium]
**Section:** 13.2

§5482 asserts `lenny.dev/delivery-mode` is "subject to the same immutability enforcement as `lenny.dev/managed`" but the webhook's formal rule set only guards `lenny.dev/managed`. A principal with pod-PATCH access can mutate a non-proxy pod's label to `proxy`, gaining port 8443 egress.

**Recommendation:** Add creation and mutation guards for `lenny.dev/delivery-mode` to the webhook rule set.

### NET-030 `lenny.dev/egress-profile` Label Has No Immutability Protection [Medium]
**Section:** 13.2

Pre-created egress NetworkPolicies select by `lenny.dev/egress-profile`. This label has no webhook protection. A pod-PATCH can re-label `restricted` → `internet`, gaining unrestricted outbound access.

**Recommendation:** Add `lenny.dev/egress-profile` (and `lenny.dev/pool`) to the webhook's protected label set.

### NET-031 Task-Mode Tenant Label Uses `lenny.io/` Domain, Inconsistent with All Others (`lenny.dev/`) [Medium]
**Section:** 5.2

`lenny.io/tenant-id` is the sole `lenny.io/` label in the spec. Every other label uses `lenny.dev/`. The tenant-pinning webhook watches `lenny.io/tenant-id`; any code normalizing to `lenny.dev/` silently creates a gap.

**Recommendation:** Change `lenny.io/tenant-id` to `lenny.dev/tenant-id` throughout §5.2.

---

## P6. Developer Experience (Runtime Authors)

### DXP-028 `credentials.json` Single-Provider Statement Contradicts Multi-Provider Lease Model [Medium]
**Section:** 4.7, 4.9

§4.7 says "only the single assigned credential provider is present." §4.9 says a session may hold multiple leases — "one per provider in the intersection." A runtime declaring `supportedProviders: [anthropic_direct, aws_bedrock]` has no way to access the second provider's credentials.

**Recommendation:** Either define a `providers` map at top level or confirm single-provider-at-a-time semantics with explicit provider-switch mechanism.

### DXP-029 `tool_call`/`tool_result` Scope Undefined for Standard-Tier [Medium]
**Section:** 15.4.1

"The stdin `tool_call`/`tool_result` channel is used for adapter-local tools only" — but "adapter-local tools" is never defined. No set, no registration mechanism, no examples beyond hypothetical `read_file`/`write_file`.

**Recommendation:** Define the fixed set of adapter-local tools (filesystem tools). Confirm platform/connector tools use MCP exclusively.

### DXP-030 `type: mcp` Runtime Author Path Entirely Unspecified in §15.4 [Medium]
**Section:** 4.7, 5.1, 15.4

`type: mcp` is a first-class runtime type but §15.4 covers `type: agent` only. No guidance on what `type: mcp` runtimes do with the manifest, which RPCs apply, or why `mcpNonce` is delivered when they have no MCP client connections.

**Recommendation:** Add a `type: mcp` note to §15.4 or §5.1 clarifying the runtime experience.

### DXP-031 `delegation-echo` Test Runtime Referenced Without Specification [Medium]
**Section:** 17.4, 18

§17.4 says delegation testing requires `delegation-echo` from Phase 9. No pseudocode, schema, or specification exists. Phase 9 in §18 has no mention of it.

**Recommendation:** Add specification alongside echo-runtime at §15.4.4, or add a Phase 9 deliverable note.

---

## P7. Operator & Deployer Experience

### OPS-033 Bootstrap Seed List Has Duplicate Item Number and Misplaced Entry [Medium]
**Section:** 17.6

The bootstrap list runs 1,2,3,4,5,6,7,6 — item 7 (`lenny-ctl policy audit-isolation`) doesn't belong in the bootstrap mechanism, and the next item is numbered 6 instead of 8.

**Recommendation:** Remove item 7 (already in §24.10). Renumber "Build sequence integration" as 7.

### OPS-034 Admin Token Rotation Procedure References Undefined Username `lenny-bootstrap-admin` [Medium]
**Section:** 17.6

`lenny-ctl admin users rotate-token --user lenny-bootstrap-admin` — username `lenny-bootstrap-admin` appears nowhere else. Bootstrap creates a `platform-admin` user but the username is never stated.

**Recommendation:** Explicitly define the username in bootstrap item 6. Add `lenny-ctl admin users list` to §24.8.

---

## P8. Multi-Tenancy & Tenant Isolation

### TNT-025 `lenny.io/tenant-id` Label Uses Wrong Domain [Medium]
**Section:** 5.2

Same as NET-031. Sole `lenny.io/` label in the spec; all others use `lenny.dev/`.

**Recommendation:** Change to `lenny.dev/tenant-id`.

### TNT-026 `GET /v1/metering/events` Specifies Undefined `billing-read` OAuth Scope [Medium]
**Section:** 11.2.1

The platform's access control is entirely role-based (§10.2). `billing-read` scope appears exactly once and is never enumerated or mapped to a role.

**Recommendation:** Replace "requires `billing-read` scope" with "Requires `billing-viewer`, `tenant-admin`, or `platform-admin` role."

---

## P10. Recursive Delegation & Task Trees

### DEL-029 `await_children(mode="all")` Blocks Indefinitely on `cancelled`/`expired` Children [Medium]
**Section:** 8.8

`mode="all"` says "wait until all children complete or fail" — excludes `cancelled` and `expired` terminal states. A child that expires via `perChildMaxAge` leaves the parent blocked forever. Same issue for `mode="any"` which says "any child completes" — excludes `failed`.

**Recommendation:** Redefine both modes to trigger on any terminal state (completed, failed, cancelled, expired).

### DEL-030 Cycle Detection Mechanism Undefined for New Pod Allocations [Medium]
**Section:** 8.2

The spec checks "the resolved target's session_id" in the caller's lineage, but `delegate_task` targets are runtime names. A new pod has no session_id until after allocation — the check happens before allocation. The mechanism can never trigger for the primary use case.

**Recommendation:** Clarify target resolution semantics. For runtime-name targets, cycle detection is inapplicable (new ID). For external agents, use connector registration ID as stable identifier.

---

## P13. Compliance, Governance & Data Sovereignty

### CMP-032 `POST /v1/admin/legal-hold` Has No `note` Field but GET Returns One [Medium]
**Section:** 12.8, 15.1

The GET response includes `note` but the POST body has no `note` field — orphan field with no setter. Legal teams need justification records.

**Recommendation:** Add optional `note` field to POST body. Required when `hold: true`.

### CMP-033 Eviction Fallback MinIO Objects Not in Erasure Scope [Medium]
**Section:** 4.4, 12.8

When eviction context exceeds 2KB, it's stored as a MinIO object at `/{tenant_id}/eviction/{session_id}/context`. Neither the erasure scope table nor the GC job covers this path.

**Recommendation:** Add `/{tenant_id}/eviction/` to ArtifactStore erasure scope. Specify cleanup at session terminal state.

### CMP-034 `dataResidencyRegion` "Stricter" Inheritance Rule Undefined [Medium]
**Section:** 12.8

"An environment may specify a stricter region than its tenant" — but regions have no natural ordering. "Stricter" is undefined.

**Recommendation:** Replace with: if tenant has a region set, environment must use the same value or inherit. Different-region override rejected with `REGION_CONSTRAINT_VIOLATED`.

### CMP-035 `audit.retentionPreset` Has No Minimum Enforcement for Compliance Profile [Medium]
**Section:** 11.7, 16.4

A HIPAA deployment can start with `audit.retentionPreset: soc2` (365 days) instead of `hipaa` (2190 days). The SIEM check passes, suppressing the only retention alert. No signal that Postgres retention is 5 years short.

**Recommendation:** Validate `audit.retentionDays >= framework_minimum` at startup for regulated tenants. Add `AuditRetentionMisaligned` alert not suppressed by SIEM.

---

## P14. API Design & External Interface Quality

### API-038 §15.1 "Comprehensive Admin API" Omits 12 Operational Endpoints [High]
**Section:** 15.1, 10.5, 24

Missing: 6 pool upgrade lifecycle endpoints (start/proceed/pause/resume/rollback/status), bootstrap-override DELETE, credential pool membership POST, preflight POST, quota reconcile POST, user token rotation POST, billing correction reasons POST. Pool upgrade cluster is the most severe — a multi-phase state machine with no §15.1 contract.

**Recommendation:** Add all 12 endpoints to §15.1. For pool upgrade, include request/response schemas and precondition states.

---

## P20. Failure Modes & Resilience Engineering

### FLR-027 `checkpointBarrierAckTimeoutSeconds` Floor Constraint Not in §4.4 or §17.8.1 [Medium]
**Section:** 10.1, 4.4, 17.8.1

§10.1 adds the CRD validation floor but §4.4 still presents 45s as the default without qualification. §17.8.1 doesn't include the parameter at all.

**Recommendation:** Add cross-reference note in §4.4. Add parameter to §17.8.1 quick-reference table.

### FLR-028 Partial Checkpoint Manifest Assembly Has No Timeout [Medium]
**Section:** 10.1

MinIO `ListMultipartUploads` + part reassembly has no timeout. Coordinator can be stuck for 5 minutes (resuming watchdog) listing objects. No behavior defined for empty-part case (parts already cleaned up).

**Recommendation:** Add 30s reconstruction timeout. Define empty-part case as "fall back to last full checkpoint immediately."

### FLR-029 `dualStoreUnavailableMaxSeconds` Timer Reset Semantics Undefined [Medium]
**Section:** 10.1

Timer is per-replica or coordinated? Different replicas detect failures at different times. "When Postgres recovers" — health check success or write transaction success? Read-only replica during promotion could trigger premature termination.

**Recommendation:** Clarify per-replica timer. "Recovery" = successful write probe, not health check.

---

## P21. Experimentation

**Clean.** All five iter7 findings resolved. No new issues.

---

## P22. Document Quality

### DOC-132 §17.8.1 Omits `checkpointBarrierAckTimeoutSeconds` [Medium]
**Section:** 17.8.1 — Same as FLR-027. Add to quick-reference table.

### DOC-133 `lenny.io/tenant-id` vs `lenny.dev/` Inconsistency [Medium]
**Section:** 4.6.1 — Same as NET-031/TNT-025. Change to `lenny.dev/tenant-id`.

### DOC-134 §3 Diagram References `ExternalAdapterRegistry` Before Introduction [Low]
**Section:** 3 — Add "(see §4.1, §15)" parenthetical.

### DOC-135 §5.2 Concurrent-Stateless References "Section 4.x" Placeholder [Medium]
**Section:** 5.2 — Replace `Section 4.x` with `Section 9.3`.

### DOC-136 §17.8.1 Missing `maxTreeRecoverySeconds` [Medium]
**Section:** 17.8.1 — Add with note about leaf-resume truncation.

---

## P23. Messaging

### MSG-032 `deadlock_detected` Event Still Has No JSON Schema [Medium]
**Section:** 8.8 — Carry-forward from iter7 MSG-031. Add schema: `type`, `cycle: [task_id]`, `detected_at`, `policy_action`.

### MSG-033 `PLATFORM_DEGRADED` SSE Event Has No Defined Schema [Medium]
**Section:** 10.1 — Inline JSON in prose only. No SSE `event:` field, no `reason` enum, no client handling contract.

**Recommendation:** Add event schema to §15.4 or §15.1 events reference.

---

## P24. Policy Engine

### POL-035 `INTERCEPTOR_TIMEOUT` Not in §15.1 Catalog [Medium]
**Section:** 4.8, 15.1 — Normatively specified in §4.8 with category/HTTP/retryable but missing from catalog.

### POL-036 `CONNECTOR_REQUEST_REJECTED`, `CONNECTOR_RESPONSE_REJECTED`, `LLM_REQUEST_REJECTED`, `LLM_RESPONSE_REJECTED` Not in Catalog [Medium]
**Section:** 4.8, 15.1 — Four interceptor-phase error codes normatively used, none cataloged.

### POL-037 `CIRCUIT_BREAKER_OPEN` `retryable: false` Is Semantically Wrong [Medium]
**Section:** 11.6, 15.1 — Circuit breaker trips are transient by definition. `retryable: false` tells SDKs never to retry. Should be `retryable: true` with `retry_after` suggestion.

---

## P25. Execution Modes

### EXM-028 Task-Mode Scrub Does Not Purge `/run/lenny/credentials.json` [Medium]
**Section:** 5.2 — Scrub purges env vars and workspace but not the credential file. Previous user's credentials persist to next task.

**Recommendation:** Add step 3b to scrub: "Remove `/run/lenny/credentials.json`."

### EXM-029 Concurrent-Workspace `/workspace/slots/{slotId}/` Directory Structure Not in §6.4 [Medium]
**Section:** 5.2, 6.4 — §6.4 covers session-mode only. No canonical layout for the `/workspace/slots/` subtree.

**Recommendation:** Add "Concurrent-workspace slot filesystem layout" subsection to §6.4.

---

## Medium Findings — Disposition Summary

| ID | Finding | Status | Rationale |
|-----|---------|--------|-----------|
| SEC-039 | MODIFY enforcement on user_id/tenant_id | **Skipped** | Policy guidance, not a spec error. The prohibition is stated; enforcement details are implementation. |
| SEC-040 | JWT KMS key rotation early-close | **Skipped** | Feature request. No internal contradiction. |
| SEC-041 | Experiment targeting webhook SSRF | **Skipped** | Security hardening suggestion. Not a spec error. |
| NET-029 | delivery-mode label immutability | **Skipped** | Security hardening suggestion. The assertion of coverage exists; webhook implementation details are operational. |
| NET-030 | egress-profile label immutability | **Skipped** | Same as NET-029. Security hardening suggestion. |
| NET-031 | `lenny.io/tenant-id` domain inconsistency | **Fixed** | Factual error. Changed all occurrences of `lenny.io/tenant-id` to `lenny.dev/tenant-id`. |
| SCL-029 | Quota drift table formula basis | **Skipped** | The table values are correct for the fail-open interpretation. Confusing but not wrong — an implementor would not build the wrong thing. |
| DXP-028 | credentials.json single-provider contradiction | **Fixed** | Internal contradiction. §4.7 said "only the single assigned credential provider is present" but §4.9 says sessions hold multiple leases. Updated §4.7 to reference `providers` array. |
| DXP-029 | adapter-local tools undefined | **Skipped** | "Spec doesn't say X" — not a flaw. |
| DXP-030 | type:mcp path unspecified | **Skipped** | "Spec doesn't say X" — not a flaw. |
| DXP-031 | delegation-echo test runtime unspecified | **Skipped** | "Spec doesn't say X" — test fixture detail. |
| OPS-033 | Bootstrap list duplicate numbering | **Fixed** | Factual error. Removed misplaced item 7 (belongs in §24.10), renumbered "Build sequence integration" as item 7. |
| OPS-034 | Undefined username `lenny-bootstrap-admin` | **Fixed** | Factual error. Changed to `lenny-admin` and explicitly defined the username in bootstrap item 6. |
| TNT-025 | `lenny.io/tenant-id` wrong domain | **Fixed** | Duplicate of NET-031. Fixed together. |
| TNT-026 | Undefined `billing-read` OAuth scope | **Fixed** | Factual error. Replaced undefined scope with role-based access: `billing-viewer`, `tenant-admin`, or `platform-admin`. |
| STR-029 | Billing sequence "no gaps allowed" false | **Fixed** | Factually incorrect. Postgres sequences produce gaps on rollback. Changed to "gaps indicate lost events and should trigger replay." |
| STR-030 | erasure_salt rotation contradiction | **Fixed** | Internal contradiction. "Deleted immediately" conflicted with "before deletion." Replaced with explicit ordered procedure: generate new salt, run re-hash migration, delete old salt after completion. |
| STR-031 | DeleteByUser doesn't decrement quota counter | **Skipped** | Valid observation but the spec describes GC as the decrement path and the Redis counter is rehydrated from Postgres on restart. The gap is bounded and self-healing. Not a contradiction. |
| DEL-029 | await_children terminal states incomplete | **Fixed** | Internal contradiction. `all` mode said "complete or fail" but `settled` said "completed, failed, cancelled, or expired." Aligned `all` and `any` to trigger on any terminal state. |
| DEL-030 | Cycle detection mechanism undefined | **Skipped** | The finding is correct that new pods get new IDs, but this means cycle detection is trivially satisfied (no cycle possible). Not a design flaw. |
| SLC-031 | `finalizing` classified as TARGET_TERMINAL | **Fixed** | Factual error. `finalizing` is a pre-running state, not terminal. Moved to Pre-running row, removed the incorrect `finalizing` row. |
| SLC-032 | Timer table references internal `attached` state | **Fixed** | Layer confusion. Replaced `attached` with `starting` in the `maxSessionAge` timer table with explicit timer semantics. |
| SLC-033 | ready/starting no bounded lifetime | **Skipped** | Design suggestion. `maxSessionAge` provides an outer bound. Tighter per-state timeouts are optimization. |
| OBS-039 | HPA step 4 contradicts metric role table | **Fixed** | Internal contradiction. Step 4 used session capacity ratio as HPA trigger; canonical table explicitly prohibits this. Corrected step 4 to use `request_queue_depth`. |
| OBS-040 | Fleet GC metric cross-replica aggregation | **Skipped** | The metric description explicitly says "computed as max() over all replica instances" — clearly a recording rule expression. Not wrong, just a different kind of metric. |
| OBS-041 | CheckpointDurationHigh threshold misaligned | **Fixed** | Factual error. Alert at 10s is unreachable (max ~5.1s at 512MB limit) and 5x above the 2s SLO. Changed threshold to 2.5s (25% headroom above SLO). |
| CMP-032 | legal-hold POST missing note field | **Fixed** | Internal contradiction. GET returns `note` but POST had no way to set it. Added `note` field to POST body. |
| CMP-033 | Eviction MinIO objects not in erasure scope | **Skipped** | The eviction fallback writes to Postgres `session_eviction_state`, not MinIO (the MinIO path is for full checkpoints, which are already in erasure scope). The 2KB context goes to Postgres. |
| CMP-034 | "stricter" region undefined | **Fixed** | Undefined term. Replaced "stricter" with explicit rule: environment must use same region as tenant or inherit it. |
| CMP-035 | retention preset no minimum enforcement | **Skipped** | Feature request for validation logic. Not a spec error. |
| PRT-028 | Adapter routing algorithm unspecified | **Skipped** | Underspecified but not wrong. The prefix uniqueness constraint and natural longest-prefix behavior are sufficient for implementation. |
| PRT-029 | lennyNonce MCP deviation | **Skipped** | The spec already acknowledges the compatibility risk ("MCP client libraries that do not support clientInfo.extensions"). Not wrong. |
| PRT-030 | Wrong tool name `discover_sessions` | **Fixed** | Factual error. Changed to `discover_agents` (the correct name used everywhere else). |
| CPS-024 | E2B self-hosting claim contradicts §23 table | **Fixed** | Internal contradiction. §23 correctly said E2B has self-hosting; §23.1 said E2B requires hosted infrastructure. Fixed §23.1. |
| WPL-025 | Timezone K8s version check wrong | **Fixed** | Factual error. The timezone is handled by Go cron library, not K8s CronJob. Removed the incorrect K8s ≥1.27 requirement. |
| CRD-025 | azure_openai materializedConfig missing | **Fixed** | Design flaw. Five other providers had complete schemas; `azure_openai` had none. Added full materializedConfig table for both API-key and Azure AD token credential types. |
| SCH-036 | delegation.completed status enum incomplete | **Fixed** | Factual error. Status only listed `completed|failed` but children can also be `terminated`, `cancelled`, or `expired`. Added all terminal states. |
| BLD-026 | Credential elicitation contradiction | **Fixed** | Internal contradiction. Phase 11/11.5 referenced "credential elicitation flow" which §4.9 explicitly prohibits. Changed to "pre-authorized registration" language. |
| BLD-027 | Phase 16 underspecified | **Skipped** | Phase 16 is light but not wrong. Under-specification is not a design flaw. |
| FLR-027 | checkpointBarrierAckTimeoutSeconds missing from §17.8.1 | **Fixed** | Missing cross-reference. Added parameter to §17.8.1 quick-reference table. |
| FLR-028 | Partial checkpoint no timeout | **Skipped** | "Spec doesn't say X" — the watchdog timer already bounds this path. |
| FLR-029 | dualStoreUnavailableMaxSeconds timer semantics | **Skipped** | "Spec doesn't say X" — implementation detail. |
| DOC-132 | §17.8.1 missing checkpointBarrierAckTimeoutSeconds | **Fixed** | Duplicate of FLR-027. Fixed together. |
| DOC-133 | lenny.io/tenant-id inconsistency | **Fixed** | Duplicate of NET-031. Fixed together. |
| DOC-134 | Diagram ref ExternalAdapterRegistry | **Skipped** | Low priority. Forward references are normal in a spec. |
| DOC-135 | Section 4.x placeholder | **Fixed** | Factual error. Replaced placeholder with correct section reference (§9.3). |
| DOC-136 | §17.8.1 missing maxTreeRecoverySeconds | **Fixed** | Missing parameter. Added to §17.8.1 quick-reference table. |
| MSG-032 | deadlock_detected event schema | **Skipped** | Finding is incorrect. The schema exists at §8.8 with `type`, `deadlockedSubtreeRoot`, `blockedRequests`, `detectedAt`, `willTimeoutAt`. |
| MSG-033 | PLATFORM_DEGRADED SSE event schema | **Skipped** | "Spec doesn't say X" — the event is referenced contextually, not as a formal API. |
| POL-035 | INTERCEPTOR_TIMEOUT not in catalog | **Skipped** | Finding is incorrect. `INTERCEPTOR_TIMEOUT` IS in the error catalog at §15.1 (line 6349). |
| POL-036 | 4 interceptor codes not in catalog | **Skipped** | Finding is incorrect. All four codes (`CONNECTOR_REQUEST_REJECTED`, `CONNECTOR_RESPONSE_REJECTED`, `LLM_REQUEST_REJECTED`, `LLM_RESPONSE_REJECTED`) ARE in the catalog at §15.1 (lines 6350-6353). |
| POL-037 | CIRCUIT_BREAKER_OPEN retryable:false | **Skipped** | Not wrong. These are operator-managed circuit breakers, deliberately opened. `retryable: false` is correct — clients should not auto-retry against an operator's deliberate shutdown. |
| EXM-028 | Task-mode scrub missing credentials.json | **Fixed** | Design flaw. Previous task's credential lease persists to next task. Added step 3b to scrub procedure. |
| EXM-029 | Slot filesystem layout unspecified | **Skipped** | "Spec doesn't say X" — not a flaw. |

**Summary: 24 Fixed, 29 Skipped (including 3 incorrect findings: MSG-032, POL-035, POL-036).**
