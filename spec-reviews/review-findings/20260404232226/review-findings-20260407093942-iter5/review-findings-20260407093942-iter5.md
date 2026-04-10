# Technical Design Review Findings — 2026-04-07 (Iteration 5 — Final)

**Document reviewed:** `docs/technical-design.md`
**Review framework:** `docs/review-povs.md`
**Iteration:** 5 of 5
**Total findings:** 18 across 25 review perspectives (after deduplication)
**Scope:** Critical, High, and Medium

## Findings Summary

| Severity | Count |
| -------- | ----- |
| Critical | 0     |
| High     | 0     |
| Medium   | 18    |

### Comparison Across All Iterations

| Severity | Iter 1 | Iter 2 | Iter 3 | Iter 4 | Iter 5 | Trend |
|----------|--------|--------|--------|--------|--------|-------|
| Critical | 30     | 4      | 1      | 0      | 0      | Clean since iter4 |
| High     | 105    | 26     | 2      | 11     | 0      | Clean |
| Medium   | 138    | 61     | 0*     | 63     | 18     | ↓87% from iter1 |
| Total    | 353    | 137    | 3      | 74     | 18     | ↓95% from iter1 |

*Iteration 3 was scoped to Critical/High only.

**The spec is clean at Critical and High severity.** The 18 Medium findings below are a mix of: iter4 fix regressions/incompletions (5), newly discovered gaps (8), previously-skipped-but-reopened (1), and canonical table completeness gaps (4).

---

## Detailed Findings

---

### SCL-026 Gateway HPA Primary Metric Contradicted in Three Places [Medium]
**Section:** 4.1, 10.1, 17.8.2

§4.1 designates `lenny_gateway_active_sessions / gateway.maxSessionsPerReplica` as the "primary HPA custom metric." §10.1 describes `lenny_gateway_active_streams` as the metric surfaced to the HPA. §17.8.2 uses `lenny_gateway_request_queue_depth` as the "HPA queue depth target." These three metrics measure different things (Postgres sessions, live goroutines, instantaneous back-pressure) and are not interchangeable. An implementor faces contradictory guidance.

**Recommendation:** Clarify in a single paragraph in §10.1: `queue_depth` is the primary scale-out trigger, `active_sessions / maxSessionsPerReplica` is the capacity ceiling alert (not HPA trigger), `active_streams` is a secondary HPA metric alongside CPU. Update §4.1 and §17.8.2 to cross-reference this.

**Status: Fixed.** §4.1 now contains a canonical three-row metric-role table (primary trigger / secondary HPA / alert-only). §10.1 adds a cross-reference paragraph. §17.8.2 "HPA queue depth target" row is consistent with the new canonical table.

---

### NET-024 IMDS `except` Block Claim for `allow-pod-egress-base` Is Factually Inaccurate [Medium]
**Section:** 13.2

§13.2 NET-002 hardening block states `allow-pod-egress-base` "carries explicit `except` blocks" for IMDS addresses. The actual policy is an allowlist (specific gateway + DNS selectors) with no CIDR rule — there is no place for `except` clauses. The security outcome is correct (allowlist blocks IMDS implicitly), but the prose contradicts the YAML.

**Recommendation:** Correct the prose: supplemental policies with broad CIDRs carry `except` blocks; the base policy uses allowlist-only and implicitly blocks IMDS.

**Status: Fixed.** Both incorrect prose statements corrected: §13.2 note now explains the two-mechanism model (allowlist-only base policy implicitly blocks IMDS; supplemental policies with broad CIDRs use explicit `except` clauses). NET-002 hardening note corrected to remove the false claim that the base policy carries `except` blocks.

---

### SEC-035 `from` Field Security Invariant Is Adapter-Side Only, Not Gateway-Enforced [Medium]
**Section:** 7.2, 15.4

The spec says "any runtime-supplied `from` is silently overwritten by the adapter" — enforcement is inside the pod. A compromised adapter that skips this lets the runtime forge `from`. The gateway has no documented step that strips `from` from `lenny/send_message` tool call arguments at the trust boundary.

**Recommendation:** Add a security invariant paragraph to §7.2: the `from` field is always set by the gateway from the mTLS-authenticated caller's session identity. Any value in the tool call is discarded at the gateway, not the adapter.

**Status: Skipped.** This is a variant of SEC-030 (iter4). The `from` field is injected into the `MessageEnvelope` delivered to the **target** runtime's stdin; it is not a field that a runtime supplies in outbound `lenny/send_message(to, message)` calls. A compromised adapter could forge `from` in the stdin bytes it writes, but a compromised adapter is already a full pod compromise — the threat model doesn't change incrementally. Gateway enforcement would require the gateway to own the stdin write path (a major architectural change). Existing controls (mTLS on adapter↔gateway gRPC, manifest-nonce handshake, pod sandbox) bound the blast radius. No spec change warranted.

---

### SEC-036 No Server-Side MIME Validation on Uploads [Medium]
**Section:** 13.4

Upload controls cover path traversal, size limits, and zip-slip but no MIME detection. A `.txt` containing a PE binary passes validation. Runtimes routing on extension may process adversarial content.

**Recommendation:** Add magic-byte MIME detection to the upload pipeline. Reject known executable types (`application/x-executable`, etc.). Add deployer-configurable `uploadMimePolicy` with `strict` and `audit-only` modes.

**Status: Skipped.** Re-raise of SEC-032 (iter4 Skipped). Existing controls remain sufficient: uploads are delivered to sandboxed (gVisor/Kata) pods; runtimes treat workspace files as untrusted input per §7.2 security note; content interceptors at `PreToolResult`/`PostAgentOutput` phases can inspect file content. MIME-based executable detection adds marginal value given the sandbox boundary and would require magic-byte parsing of every uploaded byte stream. No new justification provided.

---

### SEC-037 Proactive Credential Renewal Retry Has No `expiresAt` Guard [Medium]
**Section:** 4.9

The `CredentialRenewalWorker` retries "at half the remaining TTL, up to 3 times" but doesn't check whether `expiresAt` has already passed before each retry. For short TTLs with slow KMS, retries can extend past expiry, making the proactive loop redundant while the fault rotation fires. No first-failure signal exists before exhaustion.

**Recommendation:** Add `expiresAt` guard before each retry. Add `lenny_credential_proactive_renewal_first_failure_total` counter as early-warning signal.

**Status: Fixed.** §4.9 renewal failure handling paragraph updated: (1) `expiresAt` guard added — before each retry the worker checks `now >= lease.expiresAt` and skips to immediate `CREDENTIAL_RENEWAL_FAILED` if already expired; (2) `lenny_credential_proactive_renewal_first_failure_total` counter added as first-failure early-warning signal.

---

### CMP-024 Compliance Controls Mapping Appendix Absent [Medium]
**Section:** 12.8, 12.9, 11.7, 16.4

No compliance controls traceability table exists. Deployers cannot perform gap analysis for FedRAMP/HIPAA/SOC2 without cross-referencing 8,000+ lines manually.

**Recommendation:** Add §25 with a per-framework table: `[Control ID] | [Platform Mechanism / Section] | [Required Config]`. Minimum: SOC2 CC6/CC7/CC9, FedRAMP AU/AC/SC/IA, HIPAA §164.312.

**Status: Skipped.** Re-raise of CMP-020 (iter4 Skipped). A compliance traceability table is a documentation deliverable, not a spec gap — the controls themselves are fully specified across §12.x, §16.x, §4.x. Adding a 200-row framework-mapping table to the technical spec would expand an already 8,400-line document without changing any implementation requirement. This belongs in a separate compliance guide deliverable. No new justification provided.

---

### CMP-025 Per-Session Data Classification Not Supported [Medium]
**Section:** 12.9, 5.1

`workspaceTier` exists only at tenant/environment level. A mixed-use HIPAA tenant cannot route individual PHI sessions to T4 pools at creation time.

**Recommendation:** Add optional `dataClassification.workspaceTier` to the session request body. Enforce routing to T4 pools when set. May only set equal-or-stricter than tenant floor.

**Status: Skipped.** Re-raise of CMP-023 (iter4 Skipped). T4 data classification is enforced at the pool and node level — deployers route PHI workloads to dedicated T4 pools via pool selection in `POST /v1/sessions` (`poolSelector`). Per-session `dataClassification` override adds complexity (validation, floor-enforcement, routing logic) with no functional gap since the pool selector already provides the routing mechanism. No new justification provided.

---

### CMP-026 Failed Erasure Job Blocks User with No Escalation Alert [Medium]
**Section:** 12.8

When an erasure job fails, `processing_restricted: true` remains set, blocking all sessions for that user. No alert fires. A batch failure silently denies service to many users.

**Recommendation:** Add `lenny_erasure_job_failed_total` counter and `ErasureJobFailed` warning alert. Document in §16.5.

**Status: Fixed.** §12.8 processing restriction paragraph extended with "Erasure job failure alerting" block: `lenny_erasure_job_failed_total` counter (labeled by `tenant_id`, `failure_phase`) added; `ErasureJobFailed` Warning alert added to §16.5 alert inventory with remediation steps pointing to retry and restriction-clear endpoints.

---

### POL-030 `DelegationPolicyEvaluator` and `RetryPolicyEvaluator` Have No Priority [Medium]
**Section:** 4.8

The Evaluators table lists these modules but the Built-in interceptors priority table omits them. External interceptors at priority 101-499 may MODIFY content before these evaluators run, with undefined interaction.

**Recommendation:** Add both to the priority table (e.g., DelegationPolicyEvaluator: 250, RetryPolicyEvaluator: 600) with phases and read-field documentation.

**Status: Fixed.** Both evaluators added to the built-in interceptor table in §4.8: `DelegationPolicyEvaluator` at priority 250 (`PreDelegation` phase) and `RetryPolicyEvaluator` at priority 600 (`PostRoute` phase), with fields-read and MODIFY interaction documented. Prose updated to reflect the extended priority range (external interceptors at > 600 run after all built-ins).

---

### API-031 Error Catalog Missing ~13 Post-Iteration Error Codes [Medium]
**Section:** 15.1

Iter2-4 fixes introduced error codes referenced normatively but absent from the catalog: `POOL_DRAINING`, `DELEGATION_CYCLE_DETECTED`, `OUTPUTPART_TOO_LARGE`, `REQUEST_INPUT_TIMEOUT`, `ERASURE_IN_PROGRESS`, `CIRCUIT_BREAKER_OPEN`, `URL_MODE_ELICITATION_DOMAIN_REQUIRED`, `DUPLICATE_MESSAGE_ID`, `UNREGISTERED_PART_TYPE`, `REPLAY_ON_LIVE_SESSION`, `INCOMPATIBLE_RUNTIME`, `DOMAIN_NOT_ALLOWLISTED`, `COMPLIANCE_PGAUDIT_REQUIRED`.

**Recommendation:** Add all missing codes to the catalog with category, HTTP status, and retryable flag.

**Status: Fixed.** Challenged: §16.1 analogy applies (metrics defined in feature sections). However, error codes are different — each carries implementation-critical attributes (HTTP status, category, retryable) that implementors need in one place to build consistent error-handling middleware. Feature-section prose alone is insufficient for this purpose. `CIRCUIT_BREAKER_OPEN` was already in the catalog. The remaining 12 codes added to the §15.1 error catalog with full attributes.

---

### API-032 Experiment Results Endpoint Path Inconsistency [Medium]
**Section:** 10.7, 15.1

§10.7 references `GET /v1/experiments/{id}/results` (non-admin, `{id}`). §15.1 defines `GET /v1/admin/experiments/{name}/results` (admin, `{name}`). Different path prefix and identifier type. Also: `GET /v1/sessions/{id}/messages` and `DELETE /v1/admin/erasure-jobs/{job_id}/processing-restriction` are referenced in prose but absent from §15.1.

**Recommendation:** Standardize to `/v1/admin/experiments/{name}/results`. Add the two missing endpoints to §15.1.

**Status: Fixed.** §10.7 prose corrected to `GET /v1/admin/experiments/{name}/results` (matching §15.1 admin table). `GET /v1/sessions/{id}/messages` added to the async job support table. `POST /v1/admin/erasure-jobs/{job_id}/retry` and `DELETE /v1/admin/erasure-jobs/{job_id}/processing-restriction` added to the admin API table.

---

### OBS-031 §16.1 Metrics Table Missing ~16 Post-Iteration Metrics [Medium]
**Section:** 16.1

Iter2-4 fixes defined metrics in feature sections but didn't add them to §16.1: `lenny_experiment_targeting_circuit_open`, `lenny_experiment_sticky_cache_invalidations_total`, `lenny_credential_proactive_renewal_exhausted_total`, `lenny_crd_ssa_conflict_total`, `lenny_network_policy_cidr_drift_total`, `lenny_orphan_tasks_active_per_tenant`, `lenny_delegation_budget_return_usage_lag_total`, `lenny_slot_assignment_conflict_total`, `lenny_pool_draining_sessions_total`, `lenny_mcp_deprecated_version_active_sessions`, `lenny_orphan_session_reconciliations_total`, `lenny_quota_redis_fallback_total`, `lenny_billing_write_ahead_buffer_utilization`, plus others.

**Recommendation:** Add all missing metrics to §16.1 under appropriate subsection headers.

**Status: Skipped.** Challenge upheld. Every listed metric IS fully defined in its feature section with name, type, labels, and semantics — implementors have complete information at the definition site. The §16.1 table is a summary reference, not the source of truth for individual metrics; it does not claim to be comprehensive. Adding ~16 entries to the central table is synchronization work with no implementation impact — no metric is missing its definition, only its duplicate listing. This is lower-value editorial work not warranting a spec change at this stage.

---

### SLC-029 `created` State Description Contradicts §7.1 Pod Claim Sequence [Medium]
**Section:** 15.1, 7.1

§15.1 states `created` sessions "have not yet claimed a pod." §7.1 shows pod claim at step 4 and credential assignment at step 6 — both before the session_id is returned. Regression from SLC-027 iter4 fix.

**Recommendation:** Correct §15.1: "Session created; a warm pod has been claimed and credentials assigned, awaiting workspace file uploads or finalization."

**Status: Fixed.** §15.1 `created` state description corrected: now states "a warm pod has been claimed and credentials assigned (see §7.1 steps 4–6), awaiting workspace file uploads or finalization" and documents that expiry releases the pod claim and revokes the credential lease. Regression resolved.

---

### MSG-027 MSG-023 Fix Incomplete — Error Catalog, §5.1, §6.2 Not Updated [Medium]
**Section:** 11.3, 15.1, 5.1, 6.2

`REQUEST_INPUT_TIMEOUT` absent from error catalog. `maxRequestInputWaitSeconds` absent from §5.1 RuntimeDefinition YAML `limits:` block. §6.2 `input_required` sub-state doesn't reference the new timeout.

**Recommendation:** Add catalog entry, add field to §5.1 YAML, add cross-reference in §6.2.

**Status: Fixed.** Three locations updated: (1) `REQUEST_INPUT_TIMEOUT` added to §15.1 error catalog (also covered by API-031 fix); (2) `maxRequestInputWaitSeconds: 600` with comment added to the `limits:` block of the §5.1 standalone runtime YAML example; (3) §6.2 `input_required` sub-state transition prose updated to reference `maxRequestInputWaitSeconds` timeout and `REQUEST_INPUT_TIMEOUT` tool-call error delivery.

---

### DEL-025 `request_input_expired` Event Absent from §8.8 Stream Documentation [Medium]
**Section:** 8.8, 11.3

The event is defined in §11.3 but §8.8 (the canonical `await_children` stream reference) doesn't document it. Parent agent authors won't know to handle it.

**Recommendation:** Add `request_input_expired` entry to §8.8 alongside `input_required` and `deadlock_detected`.

**Status: Fixed.** §8.8 extended with a `request_input_expired` event block immediately before the subtree deadlock detection paragraph. Documents the event schema, distinguishes it from `input_required` (child still blocked) and terminal `failed` (child transitions back to `running` after timeout), and marks it as MUST-handle for parent authors.

---

### OPS-027 `lenny-ctl` Missing Orphan Reconciliation and Bootstrap-Override Commands [Medium]
**Section:** 24, 10.1, 17.8.2

The §24 command reference is missing `reconcile-orphans` and `exit-bootstrap` commands documented in §10.1 and §17.8.2.

**Recommendation:** Add both commands to §24 with API endpoint mappings.

**Status: Fixed (partial — bootstrap-override only).** Challenge on `reconcile-orphans`: §10.1 describes orphan session reconciliation as an automatic background process (every 60s); there is no manual trigger API endpoint defined anywhere in the spec. The finding's claim that this is "documented in §10.1" as a command is incorrect — it is an automatic reconciler, not an operator command. No `reconcile-orphans` command is warranted. `exit-bootstrap` (`DELETE /v1/admin/pools/{name}/bootstrap-override`) IS a real API endpoint documented in §17.8.2 and was absent from §24.3. Added `lenny-ctl admin pools exit-bootstrap --pool <name>` to the §24.3 pool management table with API mapping. A note added clarifying that orphan reconciliation is automatic and observable via `lenny_orphan_session_reconciliations_total`.

---

### STR-024 Billing Write-Ahead Buffer Not in Erasure Scope [Medium]
**Section:** 12.3, 12.8

Billing events staged in the Redis stream or in-memory buffer during Postgres unavailability are not in scope for `EventStore.DeleteByUser()`. Post-recovery flush creates billing records for erased users.

**Recommendation:** Add a step to the erasure job that purges staged billing events for the target user_id from the Redis stream and in-memory buffer.

**Status: Fixed.** §12.8 erasure scope table extended with a `Billing write-ahead buffer` row covering the Redis stream (`t:{tenant_id}:billing:stream`) and in-memory buffer. The row specifies that the erasure job MUST purge staged events for the target `user_id` from both before marking erasure complete, and explains the post-recovery flush risk.

---

### TNT-022 Concurrent-Workspace Slot Counters Not in Erasure or Reconciliation Scope [Medium]
**Section:** 5.2, 11.2, 12.8

Per-pod slot counters (`lenny:pod:{pod_id}:active_slots`) are pod-scoped, not tenant-scoped, and have no Postgres checkpoint. On Redis restart, counters reset to zero even if sessions still hold slots. Not in the key prefix table or erasure cleanup path.

**Recommendation:** Add to §12.4 key prefix table. Specify slot counter rehydration from SessionStore on Redis restart.

**Status: Fixed.** §12.4 tenant key isolation block replaced with a canonical key prefix table covering all Redis key patterns including `lenny:pod:{pod_id}:active_slots`. The table entry documents: pod-scoped (not tenant-prefixed, with rationale), Redis restart behaviour (reset to zero), and rehydration path (`SessionStore.GetActiveSlotsByPod(pod_id)` called before first slot allocation post-recovery). Erasure scope note: slot counters are pod-scoped operational state, not user PII — they are implicitly zeroed when the user's sessions are terminated as part of erasure, so no explicit erasure step is required beyond session termination.
