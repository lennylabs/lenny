# Technical Design Review Findings — 2026-04-19 (Iteration 1)

**Document reviewed:** `spec/` (28 files, ~17,741 lines)
**Review framework:** `spec-reviews/review-povs.md` (25 perspectives + Web Playground)
**Iteration:** 1 of 3
**Total findings:** 52 across 26 review perspectives

## Findings Summary

| Severity | Count |
| -------- | ----- |
| Critical | 5     |
| High     | 12    |
| Medium   | 25    |
| Low      | 10    |
| Info     | 0     |

---

## Critical Findings

### OBS-001 Undefined Metric for CheckpointDurationHigh Alert [Critical]
**Files:** `16_observability.md`

The alert `CheckpointDurationHigh` (line 364) references metric `lenny_checkpoint_duration_seconds` that is never defined in the metrics table. The specification states: "P95 of `lenny_checkpoint_duration_seconds` for Full-level or embedded-adapter pools exceeds 2.5 seconds" but this metric name does not appear in Section 16.1 (Metrics). Similarly, the burn-rate alert `CheckpointDurationBurnRate` (line 501) also references this undefined metric.

**Recommendation:** Define `lenny_checkpoint_duration_seconds` explicitly in the metrics table (Section 16.1) with: metric name, type (Histogram), labels (pool, level, or both), and description of the measurement boundary (e.g., time from checkpoint quiescence request to snapshot upload complete).

**Status:** Fixed — Replaced the generic "Checkpoint size and duration" row in Section 16.1 with an explicit entry for `lenny_checkpoint_duration_seconds`: Histogram type, labels `pool`/`level`/`trigger` (matching the normative definition in Section 4.4), measurement boundary "initial quiescence request through snapshot upload complete" (matching the SLO row in Section 16.6), and cross-references to the `CheckpointDurationHigh`/`CheckpointDurationBurnRate` alerts and Section 4.4.

---

### OBS-004 Undefined Metrics in Burn-Rate Alerts [Critical]
**Files:** `16_observability.md`

The SLO burn-rate table (lines 494–501) references metric filters that do not align with metric definitions. Specifically: Line 500 `TTFTBurnRate` uses `lenny_session_time_to_first_token_seconds` — metric defined at line 15 without `isolation_profile` label. Metric labels are `pool`, `runtime_class` only. If isolation-level breakdown is needed, the metric definition must be updated.

**Recommendation:** For TTF burn-rate alert on line 500, either (a) add `isolation_profile` label to `lenny_session_time_to_first_token_seconds` definition or (b) remove the expectation of per-isolation-profile SLO breakdown from the alert narrative.

**Status:** Fixed — Added `isolation_profile` label to `lenny_session_time_to_first_token_seconds` metric definition in §16.1 (line 15), aligning with sister metric `lenny_session_startup_duration_seconds` and enabling per-isolation-profile TTFT SLO breakdown consistent with the runc/gVisor phase budget distinction in §6.4.

---

### API-001 Missing Error Code in Catalog [Critical]
**Files:** `15_external-api-surface.md` (line 394, error catalog lines 532-606)

The `/v1/admin/tenants/{id}/suspend` endpoint explicitly documents that session creation and message injection are "rejected with `TENANT_SUSPENDED`", but this error code is **not defined in the error code catalog table** (Section 15.1, lines 532-606). This violates the contract test requirement (Section 15.2.1(d), line 871) that all error responses must use codes from the shared taxonomy.

**Recommendation:** Add `TENANT_SUSPENDED` to the error code catalog table with category `POLICY`, HTTP status `403`, and description: "Tenant is suspended. New session creation and message injection are rejected. The suspension is recorded in the audit trail. Wait for tenant resumption or contact administrators."

**Status:** Fixed — Added `TENANT_SUSPENDED` row to the error code catalog in §15.1 (category `POLICY`, HTTP 403) with the recommended description and a cross-reference back to `POST /v1/admin/tenants/{id}/suspend`, placed adjacent to the sibling tenant-scoped `POLICY` row `CROSS_TENANT_MESSAGE_DENIED` for logical grouping. Table column alignment matches adjacent rows.

---

### BLD-001 Phase Reference Mismatch: AuthEvaluator Phase Number [Critical]
**Files:** `18_build-sequence.md` (lines 21, 31)

In Phase 4.5, the spec states: "This deliverable is the named auth-complete milestone that Phase 7's `AuthEvaluator` depends on." However, Phase 5.75 explicitly implements the `AuthEvaluator`: "Wire `AuthEvaluator` (JWT validation + `tenant_id` extraction, backed by Phase 4.5 authentication infrastructure)..." The reference is incorrect — Phase 4.5 authentication infrastructure is a prerequisite for Phase 5.75's `AuthEvaluator`, not Phase 7's.

**Recommendation:** Update Phase 4.5 milestone statement to say "This deliverable is the named auth-complete milestone that Phase 5.75's `AuthEvaluator` depends on."

**Status:** Fixed — Corrected the phase reference in `18_build-sequence.md` line 21 (Phase 4.5) from "Phase 7's `AuthEvaluator`" to "Phase 5.75's `AuthEvaluator`", matching the actual `AuthEvaluator` implementation in Phase 5.75. Also fixed the same inconsistency in line 31 (Phase 5.5), which had an identical incorrect reference to "Phase 7's `AuthEvaluator`" in its TokenStore-ownership statement; updated to "Phase 5.75's `AuthEvaluator`" so both dependency statements point to the real implementing phase. Verified no other "Phase 7"+"AuthEvaluator" references remain anywhere in the spec.

---

### EXM-001 Concurrent-workspace leaked slots not counted in failure threshold [Critical]
**Files:** `06_warm-pod-model.md` (lines 142, 156), `05_runtime-registry-and-pool-model.md` (line 514)

The spec states leaked slots "count as a failed slot for the purposes of the `ceil(maxConcurrent/2)` unhealthy threshold" (6.2:156). However, in 5.2:514, the whole-pod replacement trigger is defined as "When `ceil(maxConcurrent / 2)` or more **slots on the same pod fail**" — using "fail", not "unhealthy". This creates ambiguity about whether leaked slots (which remain `slot_cleanup → leaked`, never reaching `failed` state) are counted.

**Recommendation:** Explicitly enumerate the failure categories that trigger pod replacement in 5.2:514: "When `ceil(maxConcurrent / 2)` or more slots on the same pod **fail or leak** within a rolling 5-minute window..."

**Status:** Fixed — harmonized "fail or leak" terminology across §5.2:514 (whole-pod replacement trigger), §6.2:142 (state-machine transition comment), §6.2:156 (`leaked` slot semantics), and §10 concurrent-workspace connection-loss reference, eliminating ambiguity about whether leaked slots count toward the `ceil(maxConcurrent/2)` threshold.

---

## High Findings

### K8S-035 Postgres-Authoritative-State Validating Webhook Lacks Formal Definition and Alert [High]
**Files:** `04_system-components.md` §4.6.3 (line 578), `16_observability.md` alerts table (§16.5)

Section 4.6.3 describes a validating admission webhook that protects Postgres-authoritative CRD state. The webhook is never formally named in the spec (no label like `lenny-pool-config-validator`). More critically, no corresponding unavailability alert is defined in §16.5's alert inventory, even though all other fail-closed webhooks have dedicated alerts (CosignWebhookUnavailable, AdmissionWebhookUnavailable, SandboxClaimGuardUnavailable, DataResidencyWebhookUnavailable).

**Recommendation:** Formally name this webhook (e.g., `lenny-pool-config-validator`) and add a corresponding `PoolConfigValidatorUnavailable` alert entry to §16.5. Update §4.6.3 to formally name the webhook at first mention.

**Status:** Fixed — Named the webhook `lenny-pool-config-validator` at its first mention in §4.6.3 (with an inline cross-reference to the new alert). Added a `PoolConfigValidatorUnavailable` row (Warning severity) to the §16.5 alerts table modeled on `DataResidencyWebhookUnavailable`; the condition text explains why Warning (not Critical) is appropriate — the webhook is a defense-in-depth backstop, with SSA field-manager conflicts providing primary enforcement. Regression-updated §10.1 where the same webhook is referenced as "the `SandboxWarmPool` CRD admission webhook" to cite the formal name, eliminating the naming ambiguity across sections.

---

### PRT-001 Elicitation Capability Mismatch: A2AAdapter Capabilities Not Reflected in `adapterCapabilities` [High]
**Files:** `15_external-api-surface.md`, `21_planned-post-v1.md`

When an A2AAdapter-initiated session has `elicitationDepthPolicy: block_all` set, the A2AAdapter's `adapterCapabilities.supportsElicitation` must be `false` for those sessions. However, `Capabilities()` is called once at adapter registration time, so the gateway-wide capability is static, but A2A elicitation support is dynamic per session. The discovery path advertises `supportsElicitation: true` (the default from BaseAdapter) even though A2A-initiated sessions will have elicitation blocked.

**Recommendation:** Document that A2AAdapter overrides `Capabilities()` to return `supportsElicitation: false` unconditionally for v1 (matching blocked elicitation behavior).

**Status:** Fixed — Extended §21.1 item 3 ("Limitation documented for callers") in `21_planned-post-v1.md` to explicitly state that `A2AAdapter` overrides `Capabilities()` to return `supportsElicitation: false` unconditionally for v1, matching `elicitationDepthPolicy: block_all` enforcement at session creation. The documentation enumerates the concrete discovery surfaces (REST `GET /v1/runtimes`, MCP `list_runtimes`, and per-runtime A2A agent card at `/a2a/runtimes/{name}/.well-known/agent.json`) where the `adapterCapabilities` block advertises this consistently, and clarifies that the override is explicit rather than relying on the `BaseAdapter` zero-value default so the discovery output remains aligned with runtime enforcement independent of any future `BaseAdapter` default changes. Cross-references `§15` (`AdapterCapabilities` / `BaseAdapter`) and §9 discovery (`adapterCapabilities` block), no other capability references needed adjustment.

---

### PRT-002 OutputPart `schemaVersion` Round-Trip Loss in A2AAdapter Implementation Risk [High]
**Files:** `15_external-api-surface.md` (Section 15.4.1, rows 1106–1121)

The Translation Fidelity Matrix documents that `schemaVersion` is `[lossy]` in A2AAdapter (mapped to `metadata.schemaVersion` string, not integer). When an A2A-initiated session delegates to a child, and the child's output is persisted in a `TaskRecord`, the `OutputPart.schemaVersion` will be stored as a string instead of integer. Durable consumers expecting integer must handle both forms or fail gracefully.

**Recommendation:** In Section 21.1, explicitly document the durable-consumer obligation: "Durable consumers of `TaskRecord` objects from A2A-initiated delegation chains must be prepared to encounter `metadata.schemaVersion` as a string and must convert it to integer during schema version comparisons. Consumers must not fail if the integer `schemaVersion` field is absent; treat absence as version 1."

**Status:** Fixed — Added a new paragraph "Durable-consumer obligation for A2A-mediated `OutputPart.schemaVersion`" to §21.1 in `21_planned-post-v1.md`, placed immediately after the A2A outbound push block and before §21.2. The paragraph (1) cross-references the Translation Fidelity Matrix in §15.4.1 and reiterates the `[lossy]` string-vs-integer mapping for `A2AAdapter`, (2) cross-references §8.8 (`TaskRecord` schema) to ground the persistence path from A2A-initiated delegation chains, (3) states the exact MUST-level consumer obligations (accept `metadata.schemaVersion` as string, convert to integer for comparisons, treat absence of the integer `schemaVersion` field as version 1), and (4) frames this as a supplement to the general durable-consumer forward-compatibility contract in §15.5 item 7 without altering the matrix's `[lossy]` classification. Regression-verified no contradiction with §15.4.1 matrix rows, §15.5 durable-consumer forward-read rules, or §8.8 `TaskRecord` schema.

---

### TNT-001 `noEnvironmentPolicy` Default Not Enforced at Gateway Startup [High]
**Files:** `10_gateway-internals.md`, `09_environments.md`, `11_policy-and-controls.md`

The spec establishes `noEnvironmentPolicy: deny-all` as the normative default when a session does not name an environment, but the gateway startup path does not perform a validation step that guarantees the platform-level `noEnvironmentPolicy` (or per-tenant override) is configured before the gateway marks itself ready. A misconfigured Helm deployment could start with undefined behavior.

**Recommendation:** Add an explicit gateway readiness requirement in §10: "The gateway MUST validate that a platform-level `noEnvironmentPolicy` is configured (either `deny-all` or a named policy reference) during startup. If the setting is missing, the gateway MUST refuse to become ready and MUST emit a `LENNY_CONFIG_MISSING` structured log entry." Cross-reference from §11 and §9.

**Status:** Fixed — Added a "Gateway startup configuration validation" block to §10.3 (adjacent to the existing startup TLS probe) that enumerates required platform-level configuration keys — `auth.oidc.issuerUrl`, `auth.oidc.clientId`, `defaultMaxSessionDuration`, and `noEnvironmentPolicy` — and mandates that the gateway refuses to become ready and emits a `FATAL`-level `LENNY_CONFIG_MISSING` structured log entry (with `config_key`, `scope`, and `remediation` fields) for any missing key, exiting non-zero so Kubernetes surfaces the failure as `CrashLoopBackOff`. Extended the integration test requirement in the same section to mandate a `TestGatewayConfigValidation` test covering each required key. Added a cross-reference sentence to the existing `noEnvironmentPolicy` declaration in §10.6 pointing to the §10.3 validation table, and added an "Access-path default policy (`noEnvironmentPolicy`)" paragraph immediately after the §11.1 admission matrix that links back to §10.3 and §10.6 so the policy section surfaces the startup-validation guarantee where it is operationally relevant. Verified `LENNY_CONFIG_MISSING` does not collide with any existing `LENNY_*` log/config token in the spec (existing tokens: `LENNY_ENV`, `LENNY_DEV_TLS`, `LENNY_DEV_MODE`, `LENNY_AGENT_BINARY`, `LENNY_AGENT_RUNTIME`, `LENNY_PG_PASSWORD`, `LENNY_REDIS_PASSWORD`, `LENNY_API_URL`, `LENNY_API_TOKEN`, `LENNY_OPS_URL`, `LENNY_PG_BILLING_AUDIT_DSN`, `LENNY_POOLER_MODE`). Note: the finding's reference to "§9 environments" maps to the actual spec structure's §10.6 Environment Resource and RBAC Model — there is no standalone environments section in the current spec; the cross-reference was placed at §10.6 where `noEnvironmentPolicy` is normatively introduced. §17.5 already contains extensive `noEnvironmentPolicy` bootstrap guidance that is consistent with the new startup validation (Helm chart must supply the key; gateway enforces presence).

---

### OBS-002 Missing Metric Names in Metrics Table [High]
**Files:** `16_observability.md`

Multiple entries in the metrics table lack metric names and are listed only as descriptions:
- Line 29: "Policy denials (by `error_type`, `tenant_id`)"
- Line 30: "Checkpoint size and duration"
- Line 7: "Session creation latency (phases)"
- Line 20: "Time-to-claim (session request to pod claimed)"
- Line 22: "Pod state transition durations (per state)"
- Line 25: "Upload bytes/second and queue depth"
- Line 26: "Token usage (by user, runtime, tenant)"
- Line 27: "Retry count (by failure classification)"
- Line 28: "Resume success/failure rate"
- Line 47: "Delegation depth distribution"

**Recommendation:** Assign explicit metric names (backtick-delimited `lenny_*` identifiers) to all metrics. Names must be consistent with the naming convention established in Section 16.1.1.

**Status:** Fixed — Assigned explicit `lenny_*` metric names, types, labels, and measurement boundaries to all ten flagged rows in §16.1: `lenny_session_creation_duration_seconds` (phase-labeled; aligned with the OpenSLO export citation in §16.10 and the `SessionCreationLatencyBurnRate` alert), `lenny_pod_claim_duration_seconds`, `lenny_pod_state_transition_duration_seconds` (`from_state`/`to_state`), `lenny_upload_bytes_total` + `lenny_upload_queue_depth` (counter + gauge pair), `lenny_tokens_consumed_total` (labeled by `tenant_id`/`runtime_class`; `user_id` intentionally excluded per §16.1.1 high-cardinality rule), `lenny_session_retry_total` (`failure_class` using the §16.3 `TRANSIENT`/`PERMANENT`/`POLICY`/`UPSTREAM` taxonomy), `lenny_session_resume_attempts_total` (`outcome`), `lenny_delegation_depth`, `lenny_policy_denials_total`, and `lenny_checkpoint_size_bytes` (added to the OBS-001-fixed checkpoint row to formalize the previously unnamed "size is additionally exported as a histogram" clause). All names follow the `lenny_*` convention in §16.1.1 and reuse labels from the authoritative §16.1.1 attribute table; regression-verified that the new names do not collide with any existing metric elsewhere in the spec.

---

### API-002 Inconsistent If-Match Requirement Documentation [High]
**Files:** `15_external-api-surface.md` (lines 376-377, 396, general rule at line 730)

Section 15.1 states at line 730: "Every admin `PUT` request **must** include an `If-Match` header." However, three PUT endpoints do not mention this requirement:
1. `PUT /v1/admin/pools/{name}/warm-count`
2. `PUT /v1/admin/pools/{name}/circuit-breaker`
3. `PUT /v1/admin/tenants/{id}/rbac-config`

In contrast, other PUT endpoints explicitly state "(requires `If-Match`)". This asymmetry creates ambiguity for SDK generators and clients.

**Recommendation:** Explicitly add "(requires `If-Match`)" to the endpoint descriptions for `warm-count`, `circuit-breaker`, and `rbac-config` PUT endpoints to match the pattern of other mutable endpoints.

**Status:** Fixed — Appended "(requires `If-Match`)" to the three endpoints called out in the finding (`PUT /v1/admin/pools/{name}/warm-count` line 376, `PUT /v1/admin/pools/{name}/circuit-breaker` line 377, `PUT /v1/admin/tenants/{id}/rbac-config` line 396) and to one additional endpoint discovered via regression scan with the same omission (`PUT /v1/admin/tenants/{id}/users/{user_id}/role` line 401). All 13 `PUT /v1/admin/*` endpoints in §15.1 now consistently document the `If-Match` requirement stated as the global rule at line 731.

---

### BLD-002 Phase 5.75 Dependency on Phase 4.5 Not Explicitly Stated [High]
**Files:** `18_build-sequence.md` (line 38)

Phase 5.75 states: "Wire `AuthEvaluator` (JWT validation + `tenant_id` extraction, backed by Phase 4.5 authentication infrastructure)..." but does not explicitly list Phase 4.5 as a prerequisite in a "Prerequisite" section like other phases do (e.g., Phase 5.5 lists Phase 5.4).

**Recommendation:** Add explicit prerequisite statement to Phase 5.75: "**Prerequisite:** Phase 4.5 (authentication infrastructure) must be complete before this phase begins."

**Status:** Fixed — Added explicit `**Prerequisite:** Phase 4.5 (authentication infrastructure) must be complete before this phase begins.` sentence to the end of Phase 5.75's Components cell in `18_build-sequence.md`, matching the format used by Phase 5.5 (which states the same for Phase 5.4).

---

### FLR-001 Billing Stream MAXLEN Formula Uses Incorrect RTO Window [High]
**Files:** `17_deployment-topology.md` (line 1027), `12_storage-architecture.md` (line 151)

The Tier 3 billing Redis stream MAXLEN derivation formula uses an incorrect RTO window duration. The specification states that Postgres RTO is `< 30s` (12_storage-architecture.md:151), but the billing stream MAXLEN calculation in 17_deployment-topology.md uses a 60-second window: `600 × 60 × 2 = 72,000`. With the stated 30s RTO and 2x safety factor, the correct formula should be: `600 events/s × 30s × 2 = 36,000`.

**Recommendation:** Clarify the actual Postgres failover RTO target for Tier 3. If `< 30s` is the true target, correct the billing stream formula to `600 × 30 × 2 = 36,000`. If operational experience has shown that 60s RTO is necessary, update Section 12.3 to state `RTO < 60s` for Tier 3 and add a note explaining the extended window.

**Status:** Fixed — Rewrote the §17.8.2 footnote derivation to accurately describe the 60s window as the outage-plus-recovery envelope (raw Postgres RTO < 30s per §12.3 + `XAUTOCLAIM` reclaim worst case of 45s per §11.2.1 + flush catch-up overlap), retaining the 72,000 MAXLEN default. Added a forward-reference sentence in §12.3 after the `RTO: < 30s` bullet noting that downstream buffers (notably the billing Redis stream) must budget the full outage-plus-recovery envelope rather than the raw RTO, and documented a 36,000 floor (`peak_billing_events × raw_RTO × 2x`) for operators who measure a shorter envelope.

---

### MSG-001 DLQ Overflow `message_dropped` Reason Code Unspecified [High]
**Files:** `07_session-lifecycle.md` (line 295), `15_external-api-surface.md` (lines 1202, 1208)

Section 7.2 defines two message overflow scenarios with inconsistent reason code specification:
1. **Inbox overflow (line 236):** "sender receives a `message_dropped` delivery receipt with `reason: "inbox_overflow"`"
2. **DLQ overflow (line 295):** "on overflow, the oldest DLQ entry is dropped and the sender receives a `message_dropped` delivery receipt" — **does not specify reason code**.

The delivery_receipt schema states: `reason: "<string — populated when status is dropped, expired, or rate_limited>"`.

**Recommendation:** Explicitly specify the reason code for DLQ overflow, e.g., change line 295 to: "...the sender receives a `message_dropped` delivery receipt with `reason: "dlq_overflow"`."

**Status:** Fixed — Appended `reason: "dlq_overflow"` to the DLQ overflow sentence in §7.2 line 295 (matching the `inbox_overflow` style at line 236), and extended the `dropped` status narrative in §15.4.1 line 1209 to enumerate both reason codes (`inbox_overflow` for session inbox full, `dlq_overflow` for DLQ full) symmetrically with the existing `error`-status reason examples.

---

### WPP-001 OIDC Cookie-to-Bearer Token Exchange Mechanism Undefined [High]
**Files:** `27_web-playground.md:47`, `10_gateway-internals.md` (auth sections), `15_external-api-surface.md`

The spec states the gateway "exchanges the cookie for MCP WebSocket bearer tokens on the user's behalf" but provides no implementation detail on: how the gateway extracts the ID token from the HttpOnly cookie, how the exchange is initiated, whether the exchange result is cached, whether concurrent connections share tokens, and TTL/rotation. Cross-section inconsistency: §10.2 and §15 do not mention playground-specific auth flows.

**Recommendation:** Document the complete OIDC-to-bearer exchange flow: specify the endpoint, request format, response with TTL and refresh semantics, and cross-reference this mechanism in §10.2 and §15.1.

**Status:** Fixed — Added §27.3.1 "OIDC cookie-to-MCP-bearer exchange" specifying the full flow: login at `GET /playground/auth/login` (PKCE-protected authorization-code, with a signed per-login `lenny_playground_oidc_state` cookie carrying the PKCE verifier and CSRF state); callback at `GET /playground/auth/callback` that validates the state, performs the provider token exchange, validates the ID token, and establishes a server-side playground session record keyed by an opaque id stored in `lenny_playground_session` (HttpOnly; Secure; SameSite=Strict; Path=/playground/; default TTL 1 h, configurable via `playground.oidcSessionTtlSeconds`) — the raw ID token never reaches the cookie; bearer exchange at `POST /v1/playground/token` (cookie-auth only, rejects `Authorization: Bearer`) that mints a standard gateway session-capability JWT via the §10.2 `JWTSigner` with default 15 min TTL (bounded 60–3600 s via `playground.bearerTtlSeconds`), `reusable: true` semantics (a single bearer covers any number of concurrent WebSocket connections for the same user within TTL; client caches in-memory only, re-exchanges with 60 s skew budget), and an added `origin: "playground"` claim; WebSocket upgrade via `Authorization: Bearer` with a `Sec-WebSocket-Protocol` sub-protocol carrier fallback (`lenny.bearer.<token>`) for browsers that cannot set the header, explicitly marked as a credential (stripped from access logs and audit emission); refresh and rotation semantics (silent re-exchange while cookie valid; in-flight WebSockets unaffected by fresh mint; `oidcSessionTtlSeconds` expiry returns `401 UNAUTHORIZED` with `details.reason: "playground_session_expired"` and redirects to `/playground/auth/login`); integration with `POST /v1/admin/users/{user_id}/invalidate` (§11.4) so revocation synchronously invalidates the playground session record and deny-lists already-minted bearers; bearer revocation primitive reusing the §10.3 Redis pub/sub deny-list pattern keyed by JWT `jti`, bounded by bearer TTL; `playground.bearer_minted` / `playground.bearer_revoked` audit events mirroring §11.7 redaction rules; and a failure-modes table covering all four codepaths (`UNAUTHORIZED`/`playground_session_expired`, `UNAUTHORIZED`/`user_invalidated`, `KMS_SIGNING_UNAVAILABLE`, WebSocket close `4401 bearer_revoked`). Added cross-reference paragraph to §10.2 ("Playground cookie-to-bearer exchange") pointing to §27.3.1 and noting that minted bearers are standard session-capability JWTs produced by the same `JWTSigner` — no playground-specific MCP/admin codepath exists, only the cookie-auth endpoints. Added a dedicated "Web playground auth endpoints" block to §15.1 listing all four endpoints (`/playground/auth/login`, `/playground/auth/callback`, `/playground/auth/logout`, `/v1/playground/token`) with description, cookie-auth requirement, and explicit note that they are **not** exposed as admin-API MCP tools (no `x-lenny-mcp-tool`/`x-lenny-scope`). Refined §27.5 to clarify that while all session/chat/discovery traffic continues to use the public MCP surface, the four cookie-auth endpoints in §27.3.1 are the only playground-specific endpoints and exist solely to bridge browser OIDC sessions to standard MCP bearer tokens. Line 47's original vague sentence now points to §27.3.1. Verified no naming conflicts (`/v1/playground/token`, `lenny_playground_session`, `lenny_playground_oidc_state`, `playground.bearer_minted`, `playground.bearer_revoked`, `origin: "playground"` claim — none collide with existing spec endpoints, cookies, events, or claims). Anchor format `#2731-oidc-cookie-to-mcp-bearer-exchange` matches the established GitHub-style `#NNNN-slug` convention used throughout the spec.

---

### DEL-001 `maxDelegationPolicy` Inheritance Semantics Ambiguity [High]
**Files:** `08_recursive-delegation.md` §8.3

The spec states: "Child leases inherit the intersection of the parent's effective policy; they cannot specify a `maxDelegationPolicy` that is less restrictive than the parent's effective `maxDelegationPolicy`." When a parent session has `maxDelegationPolicy: "read-only-policy"` and creates a child with no explicit `maxDelegationPolicy` (null), the spec does not clarify whether the child automatically inherits, or the child gets a fresh policy context.

**Recommendation:** Clarify the inheritance model: explicitly state that children inherit the parent's `maxDelegationPolicy` value as a default when not set, OR define the semantics of null `maxDelegationPolicy` in a child when the parent has a non-null cap.

**Status:** Fixed — §8.3 now defines explicit inheritance rules: a null/absent child `maxDelegationPolicy` MUST inherit the parent's effective cap verbatim (fail-safe default); a non-null child value MUST be at least as restrictive and is rejected with the new `DELEGATION_POLICY_WEAKENING` error (added to §15.1) otherwise.

---

### DEL-003 Deep Tree Recovery with `maxDelegationPolicy` Restrictions on Intermediate Nodes [High]
**Files:** `08_recursive-delegation.md` §8.10, §8.3

The spec provides clear guidance on deep-tree recovery timing (depth 5+ requires `maxTreeRecoverySeconds ≥ 900 + 600 + 120 = 1620s`). However, does not address what happens if an intermediate node in a depth-5 tree has a `maxDelegationPolicy` that was narrowed between initial delegation and recovery phase.

**Recommendation:** Explicitly state that delegation policy enforcement during tree recovery uses the policy at the time of original delegation (stored in session record), not the live policy. This aligns with "once a child session is running, its delegation was already approved."

**Status:** Fixed — Added a "Policy enforcement during recovery (no re-evaluation of existing delegations)" block to §8.10 (placed immediately after the Helm config line for `maxTreeRecoverySeconds` and before the "Non-adjacent simultaneous failures" block) specifying that the gateway MUST NOT re-evaluate any node's `delegationPolicyRef`, `maxDelegationPolicy`, `contentPolicy`, or `minIsolationProfile` against live policy state during recovery. The block names the lease record in `SessionStore` as the authoritative source, cross-references §8.3's effective-`maxDelegationPolicy` inheritance resolution and the `snapshotted_pool_ids` field (populated when `snapshotPolicyAtLease: true`), and enumerates four concrete implications: (1) existing child resumption uses the unchanged lease even when parent policy narrowed mid-tree, (2) new post-recovery delegations follow the same pre-failure rules (snapshotted pool set vs. live policy per the root lease's `snapshotPolicyAtLease` setting), (3) live interceptor configuration still applies per §8.3's scope-of-snapshot rule (interceptors remain live even when the policy is snapshotted), and (4) policy deletion during the recovery window is already prevented by §8.3's `RESOURCE_HAS_DEPENDENTS` guard (active leases of recovering nodes count as dependents). Added a reciprocal bullet "Tree recovery does not re-evaluate approved delegations" to §8.3's "Tag evaluation semantics and security implications" list (placed between the existing "Subsequent delegations are affected" and "Security implication" bullets) that derives the recovery-path behavior from the existing point-in-time evaluation principle and links forward to §8.10. The fix grounds the new language in existing spec mechanisms (`SessionStore` lease persistence, `snapshotted_pool_ids`, `RESOURCE_HAS_DEPENDENTS` deletion guard) rather than introducing new state or behavior — consistent with the Step 5 gate check classification as a clarification.

---

## Medium Findings

### PRT-003 Missing SSRF Validation Enforcement Point for A2AAdapter Push Notifications [Medium]
**Files:** `21_planned-post-v1.md` (Section 21.1, item 3)

Section 21.1 states: "The A2A adapter MUST validate the URL at `OpenOutboundChannel` time, not at delivery time, and reject task registration with `400 INVALID_CALLBACK_URL` if validation fails." However, there is no corresponding error code in Section 15.1's error catalog for `INVALID_CALLBACK_URL`. The closest error is `UPSTREAM_ERROR` (502), which is inadequate because it does not distinguish SSRF validation failure from actual upstream unavailability.

**Recommendation:** Add `INVALID_CALLBACK_URL` to the error code catalog in Section 15.1 with HTTP status 400 and category PERMANENT. Definition: "The `pushNotification.url` field in the A2A task request failed SSRF validation (private IP, non-HTTPS scheme, DNS pinning rejection, or domain allowlist mismatch). The task was rejected."

**Status:** Fixed — Added `INVALID_CALLBACK_URL` (category `PERMANENT`, HTTP 400) to the §15.1 error code catalog, appended after `TENANT_SUSPENDED`. The description names the A2A adapter's `pushNotification.url` field on `POST /a2a/{runtime}/tasks` as the registration point, specifies that rejection occurs at `OpenOutboundChannel` before the subscription is stored, enumerates the SSRF conditions from §14 (HTTPS-only, public-IP DNS pinning, cloud-metadata host rejection, optional domain allowlist), defines a `details.reason` enum (`scheme_not_https`, `private_ip`, `metadata_host`, `domain_not_allowlisted`), disambiguates from `WEBHOOK_VALIDATION_FAILED` (the 422 code scoped to `lenny-ops` event subscriptions in §25.5), marks it non-retryable, and cross-references §21.1 (A2A outbound push) and §14 (SSRF rules). Per-spec, §21.3's AP adapter "same contract as `A2AAdapter`" clause transitively covers Agent Protocol push registration under the same code, and no overlap with `UPSTREAM_ERROR` (reserved for actual upstream-dependency failures) is introduced.

---

### PRF-001 Startup Latency SLO Scope Ambiguity [Medium]
**Files:** `06_warm-pod-model.md` §6.3, `16_observability.md` §16.5

The term "file upload time" in the SLO definition is ambiguous. Section 6.3 (line 318) says startup latency SLO excludes "file upload time"; line 330 says "workspace materialization... excluded from pod-warm SLO"; but the latency budget table line 334 lists a "Total (platform-controlled)" of ≤ 6s/≤ 9s that includes workspace materialization.

**Recommendation:** Update Section 6.3 to clarify: explicitly define what "excluding file upload time" means (client-side upload only, or also gateway-to-pod delivery). If workspace materialization is truly excluded from the SLO, add a note explaining that the SLO (2s/5s) is stricter than the total indicative budget (6s/9s). If included, remove line 330's exclusion statement.

**Status:** Fixed — Standardized the SLO boundary language across §6.3 and §16 so the three statements no longer contradict. §6.3 line 318 now reads "excluding client file upload and workspace materialization time" with an inline definition of each term (client file upload = client→gateway transfer, not platform-controlled; workspace materialization = gateway→pod delivery, excluded because duration depends on payload size) and an explicit reconciliation sentence stating the 2s / 5s SLO is intentionally stricter than the indicative ≤ 6s / ≤ 9s total envelope in the per-phase budget table. The line 334 "Total (platform-controlled, no setup cmds)" row's Notes cell was updated to explicitly state the total **includes workspace materialization** and is broader than the pod-warm startup latency SLO, so the table and SLO definitions cross-reference consistently. Line 330 "excluded from pod-warm SLO" is preserved (now consistent with the clarified SLO scope). Regression-updated the `lenny_session_startup_duration_seconds` metric description in §16.1 (line 14) and the two Startup latency SLO rows in §16.5 (lines 483–484) to use the same "excluding client file upload and workspace materialization" phrasing with a cross-reference back to §6.3, so the three authoritative locations (SLO row, metric definition, per-phase table) all agree. The `StartupLatencyBurnRate` / `StartupLatencyGVisorBurnRate` alert rows (§16.5 lines 499–500) already resolve their boundary through the metric-label condition, which now carries the authoritative definition — no additional edits required. Cross-checked §17 / §4 / §7 for other startup-latency cross-references: only the generic non-normative reference in §2 goals (`low startup latency`) appears, which does not require alignment.

---

### OPS-001 Bootstrap Snapshot Stale During Operational Phase [Medium]
**Files:** `17_deployment-topology.md` (§17.6), `25_agent-operability.md` (§25.10)

The `bootstrap_seed_snapshot` table is updated only at OpsRoll and upgrade completion. Between upgrades, manual runtime or pool changes via admin API cause the snapshot to become stale. Drift detection then compares running state against a snapshot that is no longer the desired state.

**Recommendation:** (1) Emit a warning in the drift response if the snapshot age exceeds a configurable threshold (e.g., `>7 days since refresh`), directing operators to `POST /v1/admin/drift/snapshot/refresh`. (2) Provide a runbook step recommending `snapshot/refresh` as a post-hotfix cleanup task.

**Status:** Fixed — Added a "Snapshot staleness warning" block to §25.10 documenting three new `GET /v1/admin/drift` response fields derived from `bootstrap_seed_snapshot.written_at` (`snapshot_written_at`, `snapshot_age_seconds`, `snapshot_stale`) plus a human-readable `snapshot_stale_warning` string. The `snapshot_stale` boolean flips `true` when the snapshot is older than the tunable `ops.drift.snapshotStaleWarningDays` threshold (default 7 days; 0 disables), and is `false` when the caller supplied `{"desired": {...}}` in the body (no stored snapshot read). The new field is advisory (does not alter diff computation, HTTP status, or suppress drift findings) and is governed by the §15.5 forward-compatibility contract (pre-dating consumers treat absence as `false`). Added `snapshotStaleWarningDays: 7` to the `ops.drift` block in the `lenny-ops` config YAML in §25.4 with inline comment directing operators to §25.10. Added a new "Drift snapshot stale after manual admin-API change" runbook entry to §17.7 (`docs/runbooks/drift-snapshot-refresh.md`) with full Trigger/Diagnosis/Remediation structure that (a) cites the `snapshot_stale` signal as the trigger, (b) instructs reconciling the GitOps source of truth *before* refresh, (c) prescribes the `POST /v1/admin/drift/snapshot/refresh` call with `{"desired": {...}, "confirm": true}` as the remediation, and (d) formalizes "call `snapshot/refresh` after incident resolution" as a permanent tail step for every hotfix-style runbook entry in §17.7. Added the new `drift.snapshot_refreshed` audit event to the §25.10 audit events line and mirrored it into the §16 consolidated audit events list (§16.7) so the audit taxonomy stays consistent. The `POST /v1/admin/drift/snapshot/refresh` endpoint itself was already defined at §25.10 line 3513 — this fix is purely additive (new response fields, new config key, new runbook, new audit event) and introduces no endpoint signature change.

---

### STR-001 Artifact GC Strategy Specification Incomplete [Medium]
**Files:** `12_storage-architecture.md` (lines 303–312), `11_policy-and-controls.md`

The spec specifies GC via TTL-based deletion but does not explicitly formalize the idempotency guarantees for concurrent GC invocations when the same artifact is being deleted by multiple leaders or when checkpoint rotation conflicts with TTL expiration. Lack of explicit concurrency semantics creates ambiguity.

**Recommendation:** Add a concurrency section to Section 12.5 explicitly documenting that the GC job is leader-elected per-tenant (or globally), and that checkpoint rotation and TTL-based deletion both use `WHERE deleted_at IS NULL` guards to prevent double-deletion.

**Status:** Fixed — Added a "GC concurrency model (single-writer + soft-delete guard)" block at the end of §12.5 (after line 312, before §12.6) formalizing six concurrency guarantees: (1) global leader-election via an equivalent `lenny-gateway-leader` Lease (analogous to the `lenny-warm-pool-controller` / `lenny-pool-scaling-controller` primitives in §4.6), with the 25s crash-case failover bound cited from §4.6.1 and per-tenant GC sharding explicitly deferred as a Tier 4 scaling-interface extension in §12.6; (2) a `WHERE id = $1 AND deleted_at IS NULL` predicate mandated on every `artifact_store` UPDATE from both the TTL path and the checkpoint-rotation path, making the state transition strictly monotonic and turning a second writer's UPDATE into a safe 0-rows no-op; (3) MinIO `DeleteObject` delete-on-absent idempotency as the MinIO-side convergence mechanism (no conditional delete or object tagging required); (4) Redis decrement gated by the Postgres guard via an explicit `rows_affected == 1` check, cross-referenced to the existing §11.2 step 3 ordering rule so double-decrement is impossible across concurrent leaders, crash-resumed runs, and rotation-vs-TTL overlap; (5) checkpoint rotation and TTL deletion coexistence — both select from the same table with the same guard, so exactly one writer wins per row, and legal-hold rows (`legal_hold = true`, §12.8) are excluded at the SELECT level so rotation cannot race with a fresh legal-hold grant; (6) partial-manifest backstop sweep reusing the same guard so a completing resume path between SELECT and DELETE makes the backstop a no-op. Concluded with a summary sentence characterizing the whole path as a convergent, idempotent, single-writer system whose correctness properties derive from three existing mechanisms (leader election in §4.6, soft-delete guard reused from the §11.2 quota-reconciliation path, S3-compatible delete-on-absent) — no new architectural primitives introduced. Regression-checked: the new block is consistent with line 307's "leader-elected goroutine inside the gateway process" and "existing leader-election lease" language (no contradiction — the new paragraph names the lease), with line 308's existing `WHERE deleted_at IS NULL` note and Redis-ordering rationale (the new paragraph generalizes it across rotation + TTL + crash-recovery + leader-failover), with §11.2 step 3's "UPDATE artifact_store SET deleted_at = now() WHERE id = $1" wording and double-decrement hazard analysis (cross-referenced), with §12.8's legal-hold exemption (rotation/TTL SELECT-level exclusion preserves the existing `POST /v1/admin/legal-hold` semantics), and with §4.6's 25s crash-case failover bound. No new error codes, metrics, or CRD fields required.

---

### STR-002 Checkpoint Scaling Metrics and Alerts Insufficient [Medium]
**Files:** `12_storage-architecture.md` (lines 301–312), `16_observability.md`

The spec mandates monitoring checkpoint storage via `lenny_checkpoint_storage_bytes_total` gauge and an alert `CheckpointStorageHigh` fires at an unspecified threshold. The spec does not quantify the alert threshold, per-tenant checkpoint quota limits, or scaling guidance for high-checkpoint-volume workloads.

**Recommendation:** Define: (1) per-tenant checkpoint storage quota (separate from artifact storage quota); (2) alert threshold for `CheckpointStorageHigh` (e.g., 80% of quota); (3) scaling guidance for concurrent-workspace deployments; (4) legal-hold storage limit enforcement or explicit opt-out mechanism.

**Status:** Fixed — `CheckpointStorageHigh` now anchors to 80% of the existing per-tenant `storageQuotaBytes` (checkpoints share the artifact quota bucket; no new field introduced); §12.5 gains sizing guidance with per-pod multipliers for task/stateless and `maxConcurrent × 2` concurrent-workspace pools plus legal-hold accumulation rates; §12.8 documents operator sizing responsibility and confirms v1 has no legal-hold cap or opt-out (spoliation avoidance is the binding constraint).

---

### STR-003 MinIO SSE-KMS per-Tenant Key Provisioning Timing Unclear [Medium]
**Files:** `12_storage-architecture.md` (line 297), `17_deployment-topology.md` (§17.6)

Section 12.5 mandates T4 tenants must use SSE-KMS with a tenant-specific KMS key, and states the preflight check "does not validate per-tenant KMS key existence — this is validated at runtime". This creates a just-in-time provisioning requirement not covered by the spec regarding (1) pre-provisioning vs on-demand creation, (2) timing window for eventually-consistent KMS, (3) rotation procedure.

**Recommendation:** Add a section documenting: (1) the KMS key provisioning model (require pre-provisioning or specify on-demand creation flow); (2) failure behavior when KMS key is unavailable during checkpoint write; (3) key rotation procedure.

**Status:** Fixed — Added a "T4 per-tenant KMS key lifecycle" subsection to §12.5 that mandates pre-provisioning (operators create the `tenant:{tenant_id}` KMS key before setting `workspaceTier: T4`, with on-demand creation explicitly unsupported in v1), adds an admin-time KMS availability probe gated on the `PUT /v1/admin/tenants/{id}` call that rejects the tier change with `CLASSIFICATION_CONTROL_VIOLATION` if the key is unreachable (eliminating the eventually-consistent timing window at T4 promotion), documents the write-time failure behavior (fail-closed with `CLASSIFICATION_CONTROL_VIOLATION`, no silent downgrade to the deployment-wide key, `lenny_checkpoint_storage_failure_total{reason="kms_unavailable"}` increment, Postgres minimal-state fallback for eviction, operator restoration via idempotent tier re-assertion), and documents rotation semantics (operator-managed via KMS provider, automatic rotations require no coordination because prior key versions remain usable, manual rotations follow a suspend/drain/rotate/verify/resume sequence using existing admin endpoints). The deletion side was already defined in §12.8 Phase 4a and is cross-referenced.

---

### SES-001 Derive Lock Release Timing Vulnerability [Medium]
**Files:** `07_session-lifecycle.md` (line 92), `04_system-components.md`

The derive endpoint acquires an advisory lock on the source session's workspace snapshot reference, released immediately after reading the snapshot reference, before the actual copy begins. The spec does not explicitly cover the race condition between lock release, MinIO copy, and new checkpoint writing a new reference. Ambiguous whether this race is acceptable failure semantics (retry) vs lost data.

**Recommendation:** Clarify: "Derives may fail transiently if the source snapshot's reference is updated by a concurrent checkpoint after the lock is released but before the derive copy completes. Clients MUST retry the entire derive operation; the gateway does not resume partial copies." Explicit guidance that `503 DERIVE_SNAPSHOT_UNAVAILABLE` is retriable.

**Status:** Fixed — Extended §7.1 item 2 with a "Partial-copy and retry semantics" clause stating that the gateway does not resume partial derive copies, that any partially written destination object is deleted and the derived session is marked `failed` on any post-lock copy failure (including `503 DERIVE_SNAPSHOT_UNAVAILABLE` and other transient I/O errors), that clients MUST retry the entire `POST /v1/sessions/{id}/derive` call because there is no mid-copy resume endpoint, and that `503 DERIVE_SNAPSHOT_UNAVAILABLE` is classified `TRANSIENT` and retriable with a pointer to the §15.1 error catalog (which already marks it `TRANSIENT` / 503).

---

### OBS-003 Inconsistent Metric Label Names Across Delegation Metrics [Medium]
**Files:** `16_observability.md`

Lines 52-54 define `lenny_delegation_tree_memory_bytes` and `lenny_delegation_memory_budget_utilization_ratio` both labeled by `pool` and `tenant_id`, but line 48 defines `lenny_delegation_budget_utilization_ratio` with NO label specification. This creates ambiguity: is the budget metric global, per-pool, or per-tenant?

**Recommendation:** Clarify whether `lenny_delegation_budget_utilization_ratio` should be labeled by `pool` and/or `tenant_id` to match sibling metrics. If truly global, document why per-tenant/pool variants are not needed.

**Status:** Fixed — Updated §16.1 line 48 to label `lenny_delegation_budget_utilization_ratio` by `pool`, `tenant_id` (matching sibling `lenny_delegation_memory_budget_utilization_ratio`), noted that values are aggregated across active trees per (pool, tenant) with per-tree detail in structured logs, cited the §16.1.1 `root_session_id` forbidden-label rule to explain why per-tree labels are not used, and documented that the `DelegationBudgetNearExhaustion` alert's "any active delegation tree" condition is satisfied by `max by (pool, tenant_id)` evaluation.

---

### OBS-005 Missing Pool-Specific Labeling on Critical Warm Pool Metrics [Medium]
**Files:** `16_observability.md`

Alert `PodClaimQueueSaturated` fires when `lenny_pod_claim_queue_depth > 0.25 × pool.minWarm` for > 30s AND `lenny_warmpool_idle_pods > 0`. The alert definition does not explicitly state the pool filter in the condition, creating ambiguity about whether this is per-pool or global.

**Recommendation:** In the alert definition for `PodClaimQueueSaturated`, explicitly document: "per pool, when queue depth exceeds 25% of `minWarm` for that pool and idle pods exist for that pool."

**Status:** Fixed — Rewrote the `PodClaimQueueSaturated` condition in §16.5 to make per-pool evaluation explicit: prefixed with "Evaluated per pool (grouped `by (pool)`)" and rewrote the PromQL-style expression with `{pool="<p>"}` label selectors on both `lenny_pod_claim_queue_depth` and `lenny_warmpool_idle_pods`, plus a parenthetical prose restatement that both sub-conditions apply to the same pool. Gate check: both metrics are already declared with a `pool` label in §16.1 (lines 8 and 63), so no metric-label changes were needed.

---

### API-003 Missing HTTP Status Code Documentation for ETAG_REQUIRED Error [Medium]
**Files:** `15_external-api-surface.md` (line 542)

The error code `ETAG_REQUIRED` maps to HTTP status `428 Precondition Required`. However, line 730 states the gateway "returns `428 Precondition Required`" but the REST contract does not document edge cases (missing header parsing errors returning `400`).

**Recommendation:** Confirm the HTTP status mapping in the error code table is the only possible status for missing `If-Match`, or document the edge cases in a note below the error code table.

**Status:** Fixed — Refined the "PUT requests — `If-Match` required" normative bullet in §15.1 to distinguish three cases: (1) missing/empty header returns `428 ETAG_REQUIRED`, (2) malformed header (not a quoted decimal version per RFC 7232 §2.3, including weak-validator `W/` prefix which is unsupported for admin resources) returns `400 VALIDATION_ERROR` with a `details.fields` entry naming `If-Match`, (3) well-formed but stale header returns `412 ETAG_MISMATCH`. No new error codes introduced — malformed-header edge case reuses the existing `VALIDATION_ERROR` (400) row, so the §15.2 REST/MCP contract test matrix (line 895) remains aligned.

---

### CPS-001 License Decision Status Inconsistency [Medium]
**Files:** `23_competitive-landscape.md` (lines 62, 137-138), `18_build-sequence.md` (line 7), `19_resolved-decisions.md` (line 20)

The Feature Comparison Matrix in Section 23 (line 62) lists Lenny's license as "MIT (ADR-008)", implying the license decision has been finalized. However, the Build Sequence (Phase 0) and Resolved Decisions (item 14) both state that license selection is a Phase 0 gating item where the decision must be made before Phase 1 begins with four candidates: MIT, Apache 2.0, AGPL + commercial, BSL.

**Recommendation:** Update the Feature Comparison Matrix (line 62) to reflect the actual status: "TBD (ADR-008 pending Phase 0 decision)" or list the four candidates. If the decision has actually been made to MIT, update Section 18/19 language to past tense.

**Status:** Fixed — Ground truth confirmed: `LICENSE` (MIT) is committed at the repo root, `docs/about/governance.md` states MIT was chosen with rationale, and `docs/adr/index.md` catalogs ADR-0008 as "Open-source license selection (MIT)". The §23 matrix was already correct; updated §18 Phase 0 (row and Phase 1 prerequisite wording), §18 line 71 "Open-source readiness" note, §19 item 14, and §23.2 "Open-source license" paragraph from forward-looking/gating language to resolved past-tense form ("Resolved — MIT (ADR-008)") while preserving the evaluation criteria and candidate list for historical context. Any future change now requires a superseding ADR. Regression grep for "license selection" / "gating item" confirmed the remaining matches reference the resolved state, not a pending decision; Phase 17a's "license confirmed in all repository artifacts" task remains valid as a launch-time verification.

---

### CNT-001 RuntimeOptions Schema Temperature Range Mismatch [Medium]
**Files:** `14_workspace-plan-schema.md`

The `claude-code` runtime's `runtimeOptions` schema declares `temperature` with bounds `{ "type": "number", "minimum": 0, "maximum": 1 }` (line 150), while all other first-party runtimes declare `"maximum": 2` (openai-assistants line 181, gemini-cli line 199, codex line 215, chat line 243).

**Recommendation:** Verify intent. If Claude models legitimately support `maximum: 2`, change `claude-code` to `"maximum": 2` for consistency. If intentional, add a comment explaining why `claude-code` differs.

**Status:** Fixed — Confirmed intent: the Anthropic Messages API accepts `temperature` in [0, 1], while OpenAI-family APIs (used by openai-assistants, codex, chat) and the Gemini API accept [0, 2]. The `claude-code` bound of `maximum: 1` is correct. Extended the `claude-code` `temperature` field's `description` in §14 (line 150) with an inline note explaining the per-runtime difference and pointing to the other runtime schemas below, so spec implementers do not mistake the mismatch for a typo. Other runtime schemas already match their respective provider APIs and need no change.

---

### BLD-003 Echo Runtime Sufficiency Not Verified for Early Phases [Medium]
**Files:** `18_build-sequence.md` (phases 2–5.5)

Phases 2–5.4 reference the echo runtime for testing but do not explicitly confirm that the echo runtime (Phase 2) is sufficient for all testing in these phases. Phase 2.8 introduces `streaming-echo` with streaming and Full-level lifecycle support.

**Recommendation:** Add a note after Phase 2: "The basic echo runtime (Phase 2) is sufficient for all CI validation through Phase 5.5. `streaming-echo` (Phase 2.8) extends this with streaming output and Full-level lifecycle support required by Phase 6+ milestones."

**Status:** Fixed — Added a "test runtime sufficiency" note to §18 between the existing Phase 3+ digest-pinned-images note and the Phase 4+ table. It confirms the basic echo runtime (Phase 2) is sufficient for CI validation through Phase 5.5 and enumerates specific Phase 3–5.5 usages (RuntimeUpgrade echo v1→v2 test, admission/mTLS integration tests, session lifecycle, admin API and authentication, REST/MCP/Completions surfaces, etcd encryption verification, basic credential leasing); notes `streaming-echo` (Phase 2.8) is required for Phase 6+ streaming, quota, and checkpoint paths; and cross-references `delegation-echo` (Phase 9) so all three built-in test runtimes are discoverable from one place. The existing line-40 Phase 6–8 CI note remains authoritative for its specific gate. No phase description contradicts the new note.

---

### EXP-001 Results API Dimension Aggregation Mismatch [Medium]
**Files:** `10_gateway-internals.md` §10.7 (lines 814-871), `04_system-components.md` (line 720)

The Results API response schema states dimensions object is present only when at least one EvalResult has a non-null scores field; dimension keys are union of all keys. However, computing dimension aggregates (mean, p50, p95, count per dimension) is not specified for: (1) sessions that submitted only some dimensions; (2) whether `count` for a dimension equals scorer's total count or counts only sessions with that dimension.

**Recommendation:** Add explicit aggregation semantics to Section 10.7: "`count` for a dimension = number of EvalResult records where `scores[dimension]` is non-null; mean/percentiles computed only over non-null values for that dimension."

**Status:** Fixed — Added explicit per-dimension aggregation semantics to §10.7 after the existing `dimensions`-presence sentence: for each dimension `d`, `count` equals the number of `EvalResult` records for that scorer/variant where `scores[d]` is non-null, and `mean`/`p50`/`p95` are computed only over those non-null values, making it explicit that a dimension's `count` may be lower than the enclosing scorer's `count` when some results omit that dimension. Appended a selection-bias caveat warning that direct cross-dimension comparisons may reflect different underlying sample populations when per-dimension counts differ, and advising consumers to inspect per-dimension `count` values (filtering to the intersection of submitting results if unbiased comparison is required). No change to the `EvalResult` schema in §04 line 720 — the submission contract already permits partial `scores` objects; this edit clarifies aggregation behavior only.

---

### EXM-002 Task mode scrub-for-stateless conflation in sessionIsolationLevel [Medium]
**Files:** `07_session-lifecycle.md` (line 72), `05_runtime-registry-and-pool-model.md` (lines 468, 487)

The sessionIsolationLevel response includes `scrubPolicy` with values including `"none"` for concurrent-stateless. However, 5.2 states concurrent-stateless has no workspace materialization. The `scrubPolicy: "none"` field inclusion may mislead clients into believing concurrent-stateless pods undergo the same state lifecycle as other modes.

**Recommendation:** Clarify in 7.1 sessionIsolationLevel documentation that `scrubPolicy: "none"` for concurrent-stateless mode indicates not just "no cleanup" but "no per-request state tracking or lifecycle management by Lenny." Add a note: "For concurrent-stateless mode, the gateway does not track per-request state or lifecycle."

**Status:** Fixed — Extended the `scrubPolicy` row in §7.1's `sessionIsolationLevel` table so that the concurrent-stateless `"none"` value now explicitly states that it indicates more than "no cleanup": the gateway does not track per-request state or lifecycle for this mode, and no per-request scrub, checkpoint, or slot-level lifecycle management is performed by Lenny. Added a cross-reference to §5.2's concurrent-stateless limitations block so clients can find the full list of non-guarantees. No schema change; the field values remain unchanged.

---

### MSG-002 Message Delivery Path 4 Timeout Behavior Underspecified [Medium]
**Files:** `07_session-lifecycle.md` (line 276), `15_external-api-surface.md` (line 1190)

Path 4 documents interrupt and delivery for `delivery: "immediate"` but the two specifications diverge on timeout handling. Section 7.2 says "If the runtime does not consume the message within the delivery timeout (default: 30 seconds), the message falls through to inbox buffering with receipt status `queued`." Section 15.4.1 says "For all other `running` sub-states, receipt: `delivered`" with the timeout check missing.

**Recommendation:** Synchronize 15.4.1 with 7.2 by adding: "If the runtime does not confirm message consumption within 30 seconds, the message falls through to inbox buffering with receipt status `queued`."

**Status:** Fixed — Harmonized §15.4.1's `delivery: "immediate"` table row with §7.2 path 4 by amending the "For all other `running` sub-states, receipt: `delivered`" clause to state that `delivered` is emitted only once the runtime confirms stdin consumption within the delivery timeout (default: 30 seconds), and that if the runtime does not confirm within this timeout, the message falls through to inbox buffering (path 5 behavior in §7.2) with receipt status `queued`. Regression-grepped receipt-status mentions across the spec: §7.2 path 1 (`delivered`, no timeout — no stdin write), path 2 (already specifies confirmed-consumption-within-timeout rule), path 3 (`queued`), path 4 (timeout → `queued` fallthrough), path 6 (`delivered` on resume-and-deliver, `queued` on podless), path 7 (DLQ semantics), plus §15.4.1's `delivery_receipt` schema enumeration — none contradict the new timeout language.

---

### MSG-003 Path 3 Precedence Wording Ambiguous Under `await_children` [Medium]
**Files:** `07_session-lifecycle.md` (lines 269, 275), `08_recursive-delegation.md` (line 809)

Section 7.2 (line 269) states: "When a runtime has multiple concurrent blocking tool calls (e.g., `lenny/await_children` and `lenny/request_input` in flight simultaneously via parallel tool execution), the session-level `input_required` state (path 3) takes precedence over the runtime-level `await_children` condition (path 5)." The wording "multiple concurrent blocking tool calls" could be misread to suggest being blocked in `await_children` alone triggers path 3 precedence.

**Recommendation:** Clarify line 269 to explicitly state: "...when a single runtime has multiple concurrent blocking tool calls (specifically, when `lenny/request_input` is in flight concurrently with other tool calls like `await_children`)..."

**Status:** Fixed — Rewrote the path-precedence paragraph in §7.2 (line 269) to state that the path 3 vs. path 5 rule applies **only** when a single runtime has `lenny/request_input` in flight concurrently with one or more other blocking tool calls (such as `lenny/await_children`), and added an explicit counter-statement that being blocked in `lenny/await_children` alone does **not** trigger path 3 precedence (such a runtime is governed by path 5). Tightened the parallel "Overlap with path 5" note inside path 3 (line 275) with the same scoping: the overlap only exists when `request_input` is concurrent with `await_children`, and a lone `await_children` block is not `input_required`. Reviewed §8.5 (line 818) "`lenny/await_children` unblocks on `input_required`" — this describes the child's partial-result yield, not path precedence, so no edit needed; §8 line 809 is the unrelated `TaskResult.schemaVersion` paragraph.
**Files:** `08_recursive-delegation.md` §8.3

The spec documents `snapshotPolicyAtLease: true` snapshots matching pool IDs but not interceptor configuration. It does not define: (1) detection mechanism for interceptor config changes; (2) application point (retroactive or only to new calls); (3) rollback implications if failPolicy is weakened and then strengthened.

**Recommendation:** Define cache invalidation strategy for interceptor config changes. Specify whether in-flight delegation calls use the interceptor configuration at the time of the call or at the time the delegation was submitted.

**Status:** Fixed — Appended a normative "Interceptor configuration lifecycle (explicit rules)" block to §8.3 immediately under the existing `snapshotPolicyAtLease` scope-of-snapshot paragraph. The six rules state: (1) interceptor config is never snapshotted or cached into the lease — the interceptor registry in §4.8 is the single source of truth read per invocation; (2) detection mechanism is the admin API config-reload bus with a ≤ 5s cluster-wide propagation SLO; (3) application is per-invocation at the time `PreDelegation` fires, which is synchronous within `delegate_task` — so "config at submission time" and "config at interceptor-firing time" are equivalent by construction; (4) config changes are never retroactive against already-approved delegations — running children are not re-inspected; (5) `failPolicy` oscillation (weakened → strengthened → weakened) never revisits past approvals, with `interceptor.fail_policy_weakened`/`_strengthened` audit events providing the reconstruction trail; (6) interceptor deletion is blocked by `RESOURCE_HAS_DEPENDENTS` while any active `DelegationPolicy` references it, eliminating the dangling-ref failure mode. Preserves consistency with §8.10's existing "Live interceptor configuration still applies" recovery rule and §11.7 audit event definitions.

---

### DEL-004 Extension Denial Cool-Off Persistence Across Gateway Failover [Medium]
**Files:** `08_recursive-delegation.md` §8.6

The spec states cool-off flag is "persisted to the `delegation_tree_budget` Postgres table" but does NOT specify: (1) query semantics — does new replica read before processing pending requests? (2) race condition — can in-flight extension request bypass cool-off? (3) clock skew — comparison of cool_off_expiry across replicas.

**Recommendation:** Define the handoff protocol for the `extension-denied` flag: (a) new replica MUST read the flag before resuming lease extension state machine, (b) specify how in-flight requests are handled, (c) add UTC clock requirement for cool-off expiry comparison.

**Status:** Fixed — Expanded the "Durability" bullet under §8.6 elicitation-mode "User rejects" with three new sub-bullets that pin down the handoff protocol. (1) Query semantics: every extension request MUST read `extension-denied` and `cool_off_expiry` from Postgres (no in-memory cache), and if `cool_off_expiry > NOW()` the gateway auto-rejects with `EXTENSION_COOL_OFF_ACTIVE` without entering elicitation — so the newly elected replica observes the denial on its very first request without replaying predecessor state. (2) In-flight atomicity: in-flight extension requests re-check the flag and expiry **inside the same transaction** that increments `delegation_tree_budget` counters, under the per-row lock keyed by `root_session_id`/`subtree_id`; if the flag is set within the transaction the grant is rolled back and `EXTENSION_COOL_OFF_ACTIVE` returned, closing the read-then-commit race. (3) Clock reference: `cool_off_expiry` is stored as `TIMESTAMPTZ` (UTC) and all comparisons MUST use database `NOW()` / `clock_timestamp()`; the gateway MUST NOT compare against its local Go `time.Now()`, eliminating replica clock-skew bypass or over-extension.

---

### DEL-005 `CREDENTIAL_PROVIDER_MISMATCH` Rejection During Multi-Hop Cross-Environment Tree [Medium]
**Files:** `08_recursive-delegation.md` §8.3, `10_gateway-internals.md` §10.6

The spec defines that cross-environment `inherit`-mode delegation rejects with `CREDENTIAL_PROVIDER_MISMATCH` if parent's credential pool providers do not intersect with child's `supportedProviders`. However, for multi-hop trees (Root Env A → Child Env B → GrandChild Env C) the spec does NOT state whether the compatibility check uses Env A's providers, Env B's providers, or some other reference.

**Recommendation:** Clarify the credential pool identity rule for cross-environment multi-hop trees.

**Status:** Fixed — Added a "Credential pool identity in multi-hop `inherit` chains" normative paragraph plus a worked example to §8.3 immediately after the existing `CREDENTIAL_PROVIDER_MISMATCH` paragraph. The new rule states that the compatibility check always compares the **origin pool** (the pool at the top of the contiguous `inherit` chain — i.e., the last `independent` hop or the root, whichever is closer) against the immediate target runtime's `supportedProviders`, re-checked at every environment boundary along the `inherit` chain. The worked Root(Env A, pool P_A) → Child(Env B) → GrandChild(Env C) example makes explicit that at the Child→GrandChild hop the check uses P_A's providers and GrandChildRuntime_C's `supportedProviders` — never Env B's providers and never an accumulated intersection. The rule is framed as a direct consequence of the existing per-hop `credentialPropagation` model, so no conflicts with §8.3's earlier "per-hop" wording or §10.6's cross-environment bilateral checks were introduced; no edit needed in §10.6.

---

### DOC-001 Incorrect Cross-File Relative Path References [Medium]
**Files:** `04_system-components.md`, `08_recursive-delegation.md`, `12_storage-architecture.md`, `25_agent-operability.md`

16 instances of references use `../spec/` relative path when all spec files are in the same `/Users/joan/projects/lenny/spec/` directory. Examples: Line 230 (04_system-components.md): `[Section 11.7](../spec/11_policy-and-controls.md#117-audit-logging)`. The `../spec/` prefix suggests the files are one level up, which is incorrect.

**Recommendation:** Replace all `](../spec/` with `](` to use relative same-directory references.

**Status:** Fixed — Replaced all 16 instances of `](../spec/` with `](` across the 4 spec files (3 in `04_system-components.md`, 2 in `08_recursive-delegation.md`, 5 in `12_storage-architecture.md`, 6 in `25_agent-operability.md`). Verified no remaining matches in `/Users/joan/projects/lenny/spec/`. `docs/` was intentionally left untouched.

---

### CMP-042 Pluggable MemoryStore Erasure Callback Gap [Medium]
**Files:** `12_storage-architecture.md` (§12.1, 12.8), `09_mcp-integration.md` (§9.4)

The `MemoryStore` interface permits pluggable vector database backends. Section 12.8 specifies DeleteByUser includes a MemoryStore deletion step. However, the interface definition does not define a mandatory erasure callback contract. A deployer integrating a custom MemoryStore without proper `DeleteByUser` implementation will silently proceed with erasure, leaving undeleted memories while audit records successful completion. GDPR Article 17 / HIPAA compliance risk.

**Recommendation:** Define a mandatory `DeleteByUser` interface method in the `MemoryStore` contract. Add a preflight check to the erasure job that verifies the configured MemoryStore backend exposes the required method. Document in the deployment guide that custom MemoryStore implementations are responsible for implementing DeleteByUser.

**Status:** Fixed — (1) Added `DeleteByUser(ctx, tenantID, userID) error` and `DeleteByTenant(ctx, tenantID) error` as mandatory methods on the `MemoryStore` interface in §9.4, plus an "Erasure contract (mandatory)" paragraph that requires synchronous deletion, empty-scope rejection, and idempotency. (2) Added a mandatory-erasure principle to §12.1 covering every pluggable store role, noting compile-time enforcement via Go interface satisfaction. (3) Added a "MemoryStore erasure preflight" block to §12.8 describing two layers of defense: startup preflight (`ValidateMemoryStoreErasure` seeds a row, calls `DeleteByUser`, and verifies zero-row follow-up query — gateway refuses to start on stub implementations) and per-job preflight (same check before erasure step 8, aborting the job and firing `ErasureJobFailed` on regression). (4) Added "Deployment guidance for custom MemoryStore backends" in §12.8 listing the deployer's responsibilities (synchronous semantics, empty-scope rejection, idempotency, contract-test CI, metrics emission).

---

### CMP-043 GDPR Article 20 Export Scope Ambiguity [Medium]
**Files:** `12_storage-architecture.md` (§12.8), `15_external-api-surface.md` (§15.1 GDPR section)

Section 12.8 documents data portability (Article 20) but does not clarify the scope of "audit events." Specifically: (1) Cross-tenant audit reads (platform-admin operations) emit audit events recorded under the platform tenant — unspecified whether these should be included in a user's portable export. (2) The spec requires deployers to "account for all Lenny stores" but does not require documenting which audit event categories are included.

**Recommendation:** Clarify in §12.8 that user portable exports MUST include: (a) all audit events where `user_id` (actor) matches the requesting user, AND (b) all audit events from platform-admin impersonation operations that accessed or modified data scoped to that user. Add a template DSAR audit query to the deployment guide.

**Status:** Fixed — Expanded §12.8's Article 20 bullet with (1) a normative "Audit event scope for portable exports" block defining the two mandatory categories (actor-scoped rows where `audit_log.user_id` equals the target user; subject-scoped rows written under the platform tenant whose `payload.target_user_id` and `payload.target_tenant_id` identify the target), (2) an exclusion list (incidental free-text payload mentions; `gdpr.*` retention receipts), (3) a SQL template DSAR query parameterised on target `user_id`, target `tenant_id`, and the platform tenant id that UNIONs both categories via `payload->>'target_user_id'` / `payload->>'target_tenant_id'` JSONB extracts, and (4) equivalent `/v1/admin/audit-events` REST invocations (noting that category (b) requires client-side payload filtering because the API has no server-side payload filter, so the SQL path via a scoped `dsar_reader` role is authoritative). Tightened the closing RoPA paragraph to require enumerating which audit event categories are in scope — a RoPA that omits category (b) is now explicitly non-compliant. Cross-referenced §11.7 Write-time tenant validation (which mandates platform-tenant scoping for cross-tenant impersonation events) and §16.7 `admin.impersonation_started`/`_ended` payload fields. No §15.1 edit needed — §15.1 only enumerates endpoints; the normative Article 20 content belongs in §12.8 where the rest of the DSAR primitives live.

---

### WPP-002 Cookie Path Scope Insufficient Against Path Traversal [Medium]
**Files:** `27_web-playground.md:47`

The spec says the ID token cookie is "scoped to `/playground`" but does not specify the `Path=/` attribute in the Set-Cookie header. Without explicit path specification, browser behavior defaults to the path of the request that set the cookie. If future endpoints like `/playground-admin` or `/playground/proxy` are added, the cookie scope becomes ambiguous.

**Recommendation:** Explicitly specify in §27.3: "The ID token cookie is set with `Path=/playground/` (exact path boundary). The gateway sets: `Set-Cookie: lenny_playground_session=<token>; Path=/playground/; HttpOnly; Secure; SameSite=Strict; Max-Age=<TTL>`"

**Status:** Fixed — Replaced the vague "scoped to `/playground`" language in §27.3's OIDC bullet with the explicit `Set-Cookie: lenny_playground_session=<opaque-session-id>; Path=/playground/; HttpOnly; Secure; SameSite=Strict; Max-Age=<oidcSessionTtlSeconds>` header (matching the canonical form already used in §27.3.1's cookie-issuance step so the two sections agree verbatim). Called out that the trailing slash on `Path=/playground/` is load-bearing: because browser cookie path matching is prefix-based, `Path=/playground/` scopes the cookie to `/playground/` and its sub-paths only and excludes sibling paths like `/playground-admin` or `/playground.json` that would otherwise match a bare `Path=/playground`. Also clarified in the same bullet that the raw ID token is never placed in the cookie (only the opaque server-side session id), reinforcing the separation of concerns already specified in §27.3.1. No changes required in §27.3.1 — its existing `Set-Cookie` examples for issuance and logout already use the correct `Path=/playground/` form.

---

### WPP-004 Session Cleanup on Playground Close Relies on Best-Effort Hint [Medium]
**Files:** `27_web-playground.md:84`, `07_session-lifecycle.md`, `06_warm-pod-model.md:261`

The playground sends `session.cancel` on browser close but is best-effort. §6.2 shows default idle timeout is 600 seconds (10 minutes). This creates a gap where a session remains active for up to 10 minutes before idle timeout fires; hard duration cap is 30 minutes.

**Recommendation:** Define a playground-specific max idle timeout: "Playground sessions MUST NOT remain idle for longer than 5 minutes. The gateway enforces `playground.maxIdleTimeSeconds = 300` (5 min) as a hard override of the runtime's `maxIdleTimeSeconds` for all playground-initiated sessions."

**Status:** Fixed — Added `playground.maxIdleTimeSeconds` (default `300`) to the Helm-values table in §27.2 and a normative "Idle-timeout override" bullet to §27.6: playground-initiated sessions MUST NOT idle longer than this value, with the gateway enforcing `min(runtime.limits.maxIdleTimeSeconds, playground.maxIdleTimeSeconds)` as a hard override keyed off the `origin: "playground"` JWT claim (minted in §27.3.1). Cross-referenced from §6.2's `maxIdleTimeSeconds` paragraph so readers of the general timer docs are pointed at the playground override. The override only tightens — never relaxes — a stricter runtime limit.

---

## Low Findings

### PRT-004 `publishedMetadata` Access Control Consistency Across Adapters [Low]
**Files:** `15_external-api-surface.md` (Section 15.1, line 278)

Section 15.1 lists `GET /v1/runtimes/{name}/meta/{key}` as "Get published metadata for a runtime (visibility-controlled)." The phrase "visibility-controlled" is vague — it does not specify whether visibility rules are enforced identically across REST, MCP, and A2A discovery paths.

**Recommendation:** In Section 15 or 21.1, add explicit cross-adapter visibility guarantee: "Published metadata visibility rules (public/private) are enforced identically across REST, MCP, and A2A discovery endpoints."

---

### SES-002 SIGSTOP Checkpoint Mid-Interrupt Race Not Fully Characterized [Low]
**Files:** `04_system-components.md` (lines 242-250), `06_warm-pod-model.md` (lines 187-201)

In embedded adapter mode, `SIGSTOP` directly freezes the agent process. The spec does not explicitly state whether the operation lock protects against concurrent `SIGSTOP` from two different actors on the same session, or whether pod crash during SIGCONT polling leaves the process in stopped state.

**Recommendation:** Add: "In embedded adapter mode, the operation lock ensures that at most one checkpoint or interrupt operation is in flight at a time. If a pod crash occurs while `SIGCONT` confirmation polling is in progress, the new pod always starts the agent process in the running state."

---

### SES-003 SSE Buffer Overflow & Slow-Client Semantics Ambiguity [Low]
**Files:** `07_session-lifecycle.md` (line 317), `15_external-api-surface.md` (lines 122-163)

The spec does not clarify: (1) buffer size semantics (count, bytes, queue depth?); (2) closed-loop feedback on rapid reconnect; (3) interaction with inbox overflow; (4) event atomicity mid-frame.

**Recommendation:** Clarify: (1) "The buffer depth limit applies per OutboundChannel instance, measured in event count. Default: 1000 events per session." (2) Reconnect protection semantics. (3) Event atomicity: multi-part events are NOT atomic on overflow; clients implement deduplication by event ID.

---

### OBS-006 Inconsistent Checkpoint Metric Description [Low]
**Files:** `16_observability.md`

Line 364 describes `CheckpointDurationHigh` as "P95 of `lenny_checkpoint_duration_seconds` for Full-level or embedded-adapter pools exceeds 2.5 seconds." This conflates two separate filters: the pool's isolation level should be a label, not a textual qualifier.

**Recommendation:** Ensure `lenny_checkpoint_duration_seconds` (when defined) includes labels sufficient to break down by isolation level. If only pool-level breakdown is available, re-specify the SLO as "P95 across Full-level and embedded-adapter pools in aggregate."

---

### EXM-003 Concurrent-workspace preConnect incompatibility gap [Low]
**Files:** `06_warm-pod-model.md` (line 64), `05_runtime-registry-and-pool-model.md` (line 361)

Section 6.1:64 states "The pool controller rejects pool definitions that combine `executionMode: concurrent`, `concurrencyStyle: workspace`, and `capabilities.preConnect: true`". However, 5.2:361 states graph-aware runtimes are session-mode runtimes, implying preConnect could be registered with any execution mode.

**Recommendation:** Add in 5.2 Execution Modes section: "Graph-aware runtimes that benefit from SDK pre-connection (`preConnect: true`) are designed for session mode exclusively."

---

### DOC-002 Confusing Section Title Mixing [Low]
**Files:** `16_observability.md`, `README.md`

Sections 16.7 and 16.8 have titles that reference "Section 25":
- `### 16.7 Section 25 Audit Events`
- `### 16.8 Section 25 Metrics`

The titles are confusing because they mix section numbers.

**Recommendation:** Clarify the titles: `16.7 Agent Operability Audit Events` (forward-reference to Section 25 in body); `16.8 Agent Operability Metrics`. Or keep titles but add clarifying intro: "The following audit event types are introduced by Section 25 (Agent Operability):"

---

### OPS-002 Pool Configuration Reconciliation Direction Underspecified [Low]
**Files:** `17_deployment-topology.md` (§17.6), `04_system-components.md` (§4.6.2)

The spec does not explicitly state: "Operators must not directly edit `SandboxTemplate` or `SandboxWarmPool` CRDs; they must use the admin API or bootstrap seed." The PoolScalingController will reconcile from Postgres and overwrite manual CRD edits, but operators lack guidance.

**Recommendation:** Add to §17.6: "After bootstrap, all pool configuration changes must be made through the admin API; do not edit `SandboxTemplate` or `SandboxWarmPool` CRDs directly. The PoolScalingController automatically reconciles Postgres state into CRDs, overwriting any manual edits."

---

### OPS-003 `lenny-ops` Mandatory for All Deployments But Not Documented As Blocker [Low]
**Files:** `25_agent-operability.md` (§25.1, §25.2), `17_deployment-topology.md` (§17.1)

Section 25.1 states clearly: "`lenny-ops` is mandatory in every Lenny installation." However, this critical constraint is buried in §25.1, not highlighted in the deployment topology section (§17.1) where operators deciding "what to deploy" would first look.

**Recommendation:** Add a prominent note to §17.1 after the `lenny-ops` row: "**Note:** `lenny-ops` is not optional. It is the exclusive host for operability endpoints. Removing it disables all operations features listed in Section 25."

---

### WPP-003 CSP Missing Object and Media Sources Directives [Low]
**Files:** `27_web-playground.md:96–103`

The CSP policy does not include `object-src` or `media-src` directives. While defaulting to `default-src`, explicit inclusion improves defense-in-depth.

**Recommendation:** Augment the CSP to include `object-src 'none'; media-src 'none'`.

---

### STR-004 Redis Fail-Open Behavior for Rate Limits and Quota Diverges [Low]
**Files:** `12_storage-architecture.md` (lines 205, 209, 220), `11_policy-and-controls.md`

Rate limit counters fail open with 60s bounded window; quota counters fail open with per-replica ceiling and 300s cumulative. These different risk profiles for the same resource (tokens) are not justified in the spec.

**Recommendation:** Clarify in Section 12.4 whether rate limits and quota counters use the same fail-open window logic or distinct logic, and if distinct, justify the difference. Consolidate the cumulative-timer mechanism description.

---

## Cross-Cutting Themes

1. **Undefined / orphaned references**: Several alerts, error codes, and metrics are referenced but not defined (OBS-001, OBS-002, OBS-004, API-001, PRT-003, FLR-001).

2. **Startup-time configuration enforcement**: Gateway and controllers have normative defaults not guaranteed at startup (TNT-001, K8S-035).

3. **Cross-section value asymmetries**: Numerical or state values diverge across sections (FLR-001, CNT-001, OBS-004, API-002).

4. **Delegation inheritance semantics underspecified**: Multiple DEL findings (DEL-001, DEL-002, DEL-003, DEL-005).

5. **Phase / lifecycle references stale**: Build sequence and roadmap references (BLD-001, CPS-001, BLD-003).

6. **Concurrent-mode semantics leak into non-applicable sections**: Task and stateless execution modes share response schemas with concurrent-workspace (EXM-001, EXM-002).

7. **Schema/error catalogs drift from usage**: Error codes and metrics used in endpoint docs or alert rules are not always registered in their canonical catalogs (API-001, PRT-003, OBS-001, OBS-002).
