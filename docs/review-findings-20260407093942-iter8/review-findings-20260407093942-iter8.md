# Technical Design Review Findings — 2026-04-07 (Iteration 8)

**Document reviewed:** `docs/technical-design.md` (8,649 lines)
**Review framework:** `docs/review-povs.md`
**Iteration:** 8 (25 agents, 1 per perspective)
**Total findings:** ~60 (0 Critical, 5 High, ~53 Medium, 2 Low)

## High Findings

| # | ID | Finding | Section |
|---|-----|---------|---------|
| 1 | K8S-025 | WarmPoolController RBAC missing `create`/`delete` Sandbox + SandboxClaim access | 4.6.3 |
| 2 | STR-028 | Billing stream flusher has no replica coordination — concurrent duplicate billing events | 12.3 |
| 3 | OBS-037 | Availability SLO burn-rate formula is mathematically inverted | 16.5 |
| 4 | OBS-038 | Head-based sampling at 10% incompatible with 100%-error-sampling requirement | 16.3 |
| 5 | API-038 | §15.1 "Comprehensive Admin API" table omits 12 operational endpoints | 15.1 |

## Key Design Flaws Found This Iteration

- **K8S-025**: RBAC table has WPC with only `update` on Sandbox — missing `create`, `delete`, and all SandboxClaim verbs. Implementor gets 403 on every pod creation.
- **STR-028**: All N gateway replicas concurrently flush the same Redis billing stream entries to Postgres — doubling billing events with distinct sequence numbers.
- **OBS-037**: Burn-rate formula `(1 - error_rate) / (1 - slo_target)` yields max burn at 0% errors and zero burn at 100% errors — completely inverted.
- **OBS-038**: Head-based sampling decides discard before errors/latency are known — 100% error sampling is architecturally impossible with head-based decisions.
- **API-038**: 12 endpoints (6 pool upgrade lifecycle, bootstrap-override, credential pool membership, preflight, quota reconcile, token rotation, billing reasons) are in §24 lenny-ctl but not in the "Comprehensive" §15.1 table.

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
