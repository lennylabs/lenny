# Technical Design Review Findings — 2026-04-07 (Iteration 2)

**Document reviewed:** `docs/technical-design.md`
**Review framework:** `docs/review-povs.md`
**Iteration:** 2 of 5
**Total findings:** 137 across 25 review perspectives

## Findings Summary

| Severity | Count |
| -------- | ----- |
| Critical | 4     |
| High     | 26    |
| Medium   | 61    |
| Low      | 40    |
| Info     | 6     |

### Critical Findings

| # | Finding | Section | Status |
|---|---------|---------|--------|
| 1 | K8S-013 Tier 3 maxSessionsPerReplica identical to Tier 2 — no scaling path | 4.1, 2 | **Fixed** |
| 2 | SCL-015 maxSessionsPerReplica arithmetic doesn't scale to Tier 3 without extraction | 4.1, 2, 16.5 | **Fixed** |
| 3 | DOC-119 API table contradicts CMP-006 erasure-salt fix (regression) | 15.1 | **Fixed** |
| 4 | CRD-016 Admin API circuit-breaker override field undocumented in REST table | 15.1 | **Fixed** |

### Comparison with Iteration 1

| Severity | Iter 1 | Iter 2 | Change |
|----------|--------|--------|--------|
| Critical | 30     | 4      | -87%   |
| High     | 105    | 26     | -75%   |
| Medium   | 138    | 61     | -56%   |
| Low      | 71     | 40     | -44%   |
| Info     | 16     | 6      | -63%   |
| **Total** | **353** | **137** | **-61%** |

### Key Themes in Iteration 2

1. **Regressions from iter1 fixes**: DOC-119 (API table contradicts erasure-salt fix), SEC-026/OPS-017 (Redis runbook still says `replica_count=1`), SLC-019 (Section 15.1 derive preconditions not updated), CRD-016 (circuit-breaker field undocumented)
2. **Carry-forward Medium/Low from iter1**: ~80% of iter2 findings are Medium/Low items that were out of scope for iter1 fixes (Critical+High only)
3. **Observability gaps persist**: 10 OBS findings from iter1 remain unresolved (metrics not in canonical table, alerting gaps)
4. **Tier 3 capacity remains unvalidated**: K8S-013 and SCL-015 identify that the 10,000-session claim has no empirical support

---

## Detailed Fix Log — Iteration 2 Critical + High Fixes (2026-04-07)

### K8S-013 [Critical] — Tier 3 maxSessionsPerReplica identical to Tier 2

**Status:** Fixed

**Section:** §4.1, §16.5

**Problem:** The provisional `maxSessionsPerReplica` table had identical values (200) for Tier 2 and Tier 3. This implied no scaling path exists — Tier 3 at 10× the session count would simply require 10× replicas with the same per-replica ceiling, but gave no rationale for why this was acceptable or what architectural prerequisite would enable a higher ceiling.

**Fix applied:**
- §4.1 table: Updated Tier 3 provisional value from 200 to **400**, with explicit prerequisite that the LLM Proxy subsystem must be extracted (or `lenny_llm_proxy_active_connections / lenny_gateway_active_sessions` sustainably < 0.3:1 at Tier 2 peak) before this value is valid. If the prerequisite is not met, the value reverts to 200.
- §4.1 explanatory text: Expanded to document that the Tier 3 value is derived from the assumption that LLM Proxy extraction removes the long-lived upstream goroutine workload, roughly doubling per-replica session capacity. Added Phase 13.5 load-test confirmation requirements.
- §16.5 capacity tier table: Updated Tier 3 `maxSessionsPerReplica` from `200 (provisional)` to `400 (provisional — requires LLM Proxy extraction; see §4.1)`.

**Regression check:** The calibration methodology in §4.1 and the `GatewaySessionBudgetNearExhaustion` alert in §16.5 still reference `maxSessionsPerReplica` correctly. The bench requirement in §16.5 now reflects both Phase 2 (Tier 1/2) and Phase 13.5 (Tier 3) validation gates.

---

### SCL-015 [Critical] — Fleet-level GC metric and Tier 3 aggregate health indicator missing

**Status:** Fixed (partially addressed by K8S-013; additional metrics and alert added)

**Section:** §4.1, §2, §16.5

**Problem:** No fleet-level (cross-replica) GC pressure metric or alert existed. The per-replica `lenny_gateway_gc_pause_p99_ms` was already defined in §4.1, but there was no aggregate signal visible at the fleet level, and no alert for Tier 3 GC pressure as a health indicator for the extraction decision.

**Fix applied:**
- §16.1 metrics table: Added two new metrics:
  - `lenny_gateway_gc_pause_p99_ms` (labeled by `replica_id`) — per-replica GC pause gauge (already referenced in §4.1, now canonically listed).
  - `lenny_gateway_gc_pause_fleet_p99_ms` — fleet-wide P99 GC pause (max across all replicas); the Tier 3 aggregate health indicator.
- §16.5 warning alerts table: Added `Tier3GCPressureHigh` alert — fires when `lenny_gateway_gc_pause_fleet_p99_ms` exceeds 50 ms for > 5 min at Tier 3 scale (≥ 5,000 active sessions). Suppressed at Tier 1/2 scale.

**Regression check:** The §4.1 "Shared-process GC pressure signal" paragraph already references the 50 ms threshold; the new alert operationalizes exactly that signal at fleet scope.

---

### DOC-119 [Critical] — API table contradicts CMP-006 erasure-salt fix

**Status:** Fixed

**Section:** §15.1

**Problem:** The REST API table entry for `POST /v1/admin/tenants/{id}/rotate-erasure-salt` still read "Old salt retained in `previous_erasure_salts` for referential integrity" — directly contradicting the §12.8 specification (CMP-006 fix) which specifies immediate deletion of the old salt, with no `previous_erasure_salts` field.

**Fix applied:**
- §15.1 admin API table: Updated description to "The old salt is **deleted immediately** upon rotation (not retained); a one-time re-hash migration job re-pseudonymizes historical billing records under the new salt before deletion. See Section 12.8."

**Regression check:** Confirmed §12.8 text is consistent (salt deleted immediately, no `previous_erasure_salts` reference in that section). The API table now matches.

---

### CRD-016 [Critical] — Admin API circuit-breaker override field undocumented

**Status:** Fixed

**Section:** §15.1

**Problem:** Section 6.1 referenced `PUT /v1/admin/pools/{name}` with `{"sdkWarm": {"circuitBreakerOverride": "enabled"}}` for re-enabling SDK-warm after the circuit breaker fires, but the REST API table in §15.1 did not document this endpoint or the `circuitBreakerOverride` field values, error codes, or audit event.

**Fix applied:**
- §15.1 admin API table: Added new row `PUT /v1/admin/pools/{name}/circuit-breaker` with:
  - Field values: `enabled` | `disabled` | `auto`
  - Value semantics documented for each option
  - Error code: `409 INVALID_STATE_TRANSITION` when applied to non-SDK-warm pools
  - Audit event: `pool.sdk_warm_circuit_breaker_override` recording operator identity, previous state, and new value
  - Role requirement: `platform-admin` or `tenant-admin`
  - Reference to §6.1 for circuit-breaker background

---

### K8S-014 [High] — CRD API version graduation webhook dependency assumed but never deployed

**Status:** Fixed

**Section:** §15.5, §10.5

**Problem:** Section 15.5 listed CRD versioning as `v1alpha1 → v1beta1 → v1` but Section 10.5 said CRDs shipped at `v1beta1` initially — a contradiction. More critically, neither section specified (a) which version ships initially, (b) graduation criteria, or (c) the step-by-step conversion webhook deployment procedure. The conversion webhook is a hard dependency for multi-version coexistence but its deployment was never described.

**Fix applied:**
- §10.5: Corrected "shipping at `v1beta1` initially" to "shipping at `v1alpha1` initially".
- §15.5 item 4: Expanded from a single sentence to a full specification:
  - Initial API version: `v1alpha1` for all four CRDs (`SandboxTemplate`, `SandboxWarmPool`, `Sandbox`, `SandboxClaim`).
  - Graduation criteria: `v1alpha1` → `v1beta1` (Phase 2 benchmark + 60-day stability); `v1beta1` → `v1` (Phase 14.5 GA + 6-month stability).
  - Conversion webhook deployment procedure: 6-step ordered procedure covering pre-deploy validation, Service existence check, CRD apply, both-version confirmation, storage migration, and old-version removal.
  - HA requirement: 2 replicas + PDB `minAvailable: 1`.
  - Preflight integration: `lenny-preflight` Job validates webhook availability as an upgrade gate.

---

### K8S-015 [High] — SSA field manager conflict handling on crash not specified

**Status:** Fixed

**Section:** §4.6.3

**Problem:** Section 4.6.3 documented the SSA field ownership model but did not specify how controllers should handle SSA HTTP 409 conflict errors after a crash and restart — specifically whether they should re-read before retrying and whether `Force: true` is ever permitted.

**Fix applied:**
- §4.6.3: Added "SSA conflict retry policy (crash recovery)" block specifying:
  1. Always re-read before re-applying: on any 409, discard cached copy and issue fresh `GET`.
  2. Never force-conflicts: `Force: true` is explicitly prohibited in reconciliation code.
  3. Bounded retry with backoff: re-read → re-apply → jitter (100ms–2s) → 5 consecutive 409s triggers `crd_ssa_conflict_stuck` log event and `lenny_crd_ssa_conflict_total` counter increment.
  4. No force on normal startup: post-crash reconciliation always re-reads first.
  - Added `CRDSSAConflictStuck` warning alert (fires when conflict counter > 10 in 5 minutes on a single resource).

---

### NET-015 [High] — Port 8443 open to all managed pods regardless of deliveryMode

**Status:** Fixed

**Section:** §13.2

**Problem:** The `allow-pod-egress-base` NetworkPolicy included both port 50051 (gRPC control) and port 8443 (LLM proxy) for all pods labeled `lenny.dev/managed: "true"`, regardless of whether the pod's pool had `deliveryMode: proxy`. Pods in `direct` pools could reach the LLM proxy port unnecessarily, increasing blast radius.

**Fix applied:**
- §13.2: Split the single `allow-pod-egress-base` policy into two:
  1. `allow-pod-egress-base` — port 50051 + DNS only; applied to **all** `lenny.dev/managed: "true"` pods.
  2. `allow-pod-egress-llm-proxy` — port 8443 only; applied **only** to pods labeled `lenny.dev/delivery-mode: proxy` (set by WarmPoolController on proxy-mode pool pods).
- Added note that `lenny.dev/delivery-mode` label is subject to the same `lenny-label-immutability` webhook enforcement as `lenny.dev/managed`.

---

### NET-016 [High] — Dedicated CoreDNS Corefile missing

**Status:** Fixed

**Section:** §13.2

**Problem:** Section 13.2 described the dedicated CoreDNS instance's capabilities (query logging, rate limiting, response filtering) but provided no Corefile configuration reference, leaving deployers and runtime authors unable to validate or extend the configuration.

**Fix applied:**
- §13.2: Added "Reference Corefile for the dedicated CoreDNS instance" block containing:
  - Full reference Corefile with `log`, `ratelimit`, `filter`, `forward`, `health`, `prometheus`, `cache`, `reload`, and `errors` plugins.
  - All configurable parameters annotated with their Helm value paths and defaults.
  - Note that `coredns-ratelimit` and `coredns-filter` are non-standard plugins compiled into the `lenny-coredns` image, vendored under `build/coredns-plugins/`.
  - Guidance for deployers building custom CoreDNS images.

---

### SCL-016 [High] — Redis Lua budget script unbounded serialization

**Status:** Fixed

**Section:** §8.3, §12.4

**Problem:** The `budget_reserve.lua` script description correctly documented its atomic semantics but did not analyze the serialization contention impact at high `maxParallelChildren` values. No ceiling guidance existed for `maxParallelChildren` relative to Redis P99 latency SLOs.

**Fix applied:**
- §8.3: Added "Lua script serialization and contention analysis" block under the Reservation step:
  - Contention table: estimated burst rates and serialization time for `maxParallelChildren` at 4 bands (≤10, 11–50, 51–100, >100).
  - `maxParallelChildren` ceiling guidance: soft ceiling 50 (safe for all standard topologies); hard ceiling 100 (above which Lua bursts can spike LeaseStore P99 above 5 ms SLO).
  - Built-in presets (`orchestrator: 10`) are safe without review.
  - New metric: `lenny_delegation_parallel_children_high_watermark` (gauge per root session) for retroactive fan-out detection.

---

### SCL-017 [High] — KEDA still optional for Tier 3

**Status:** Fixed

**Section:** §10.1

**Problem:** Section 10.1 described KEDA as the "recommended option for Tier 3 deployments" — implying it was still optional. At Tier 3 session arrival rates (200/s) with a 60s Prometheus Adapter pipeline lag, the burst exposure window (12,000 session attempts) cannot be safely absorbed by `minReplicas` alone, making KEDA effectively mandatory.

**Fix applied:**
- §10.1: Reclassified KEDA from "recommended option" to **"mandatory platform requirement at Tier 3"**.
- Added explicit rationale: 200/s × 60s = 12,000 session attempts during Prometheus Adapter lag window; KEDA reduces this to ~4,000 (within `minReplicas` sizing).
- Tier 3 GA gate (Phase 14.5) now explicitly requires KEDA deployment.
- Added fallback path: deployers who cannot deploy KEDA at Tier 3 must use the Section 17.8 `minReplicas` formula that accounts for the full 60s lag window (larger buffer required).

---

_Detailed findings from each perspective are available in the subagent outputs. The findings above represent the consolidated summary._

---

## Iteration 2 — High Finding Fixes (2026-04-07, batch 2)

### SCL-018 [High] — Postgres write saturation alert lacks empirical basis

**Status:** Fixed

**Section:** §12.3

**Problem:** The `PostgresWriteSaturation` alert was defined as firing when write IOPS exceed 80% of "the estimated instance ceiling" but no per-tier numeric ceiling was stated anywhere in the spec. Operators had no basis for configuring the alert threshold or knowing when the platform's assumptions were violated.

**Fix applied:**
- §12.3: Added "Per-tier write ceiling reference table" immediately before the "Horizontal write scaling route" block, containing estimated write ceilings for Tier 1 (200 IOPS on `db.t3.medium`), Tier 2 (600 IOPS on `db.r6g.xlarge`), and Tier 3 (1,600 IOPS on `db.r6g.2xlarge`), with 80% alert thresholds and burst ceilings for each.
- Added `postgres.writeCeilingIops` Helm override to allow operators to supply their measured ceiling.
- Added Phase 2 / Phase 13.5 load-test requirement to validate these estimates.

**Regression check:** The existing Write IOPS estimation table (Tier 3 ~1,300/s sustained) is consistent with the new Tier 3 ceiling (1,600 IOPS). The alert logic remains unchanged; only the ceiling reference is now explicit.

---

### SEC-019 [High] — SO_PEERCRED loopback self-test not mandated as startup prerequisite

**Status:** Fixed

**Section:** §4.7

**Problem:** Section 4.7 documented `SO_PEERCRED` UID verification as a security control but did not mandate a startup self-test to verify that `SO_PEERCRED` is functional in the current pod environment. Environments where `SO_PEERCRED` is silently broken would silently bypass UID verification without any signal.

**Fix applied:**
- §4.7 item 1 (Adapter-Agent Security Boundary): Added "Mandatory `SO_PEERCRED` startup self-test (adapter prerequisite)" block specifying a 5-step loopback self-test procedure: open temporary socket, connect from same process, call `getsockopt(SO_PEERCRED)`, assert UID matches `os.Getuid()`, fail-fast with fatal log and counter increment if the assertion fails.
- Self-test runs on every pod start, not only in CI.
- Configurable via `adapter.requireSoPeercred` (default `true`); can be set `false` only when gVisor divergence is confirmed and nonce-only mode is explicitly accepted.
- New metric: `lenny_adapter_sopeercred_selftest_failed_total` counter.

**Regression check:** The nonce handshake (existing control) is unaffected. The self-test is additive and does not change behavior on passing environments.

---

### SEC-026 [High] — Redis runbook says `replica_count = 1` (contradicts STR-001 fix)

**Status:** Fixed

**Section:** §17.7

**Problem:** The Redis failure runbook stated "the platform operates in fail-open mode with `replica_count = 1` assumption" — contradicting the STR-001 fix which changed the quota fail-open logic to use `cached_replica_count` (last successfully cached gateway replica count) rather than a hard-coded `1`.

**Fix applied:**
- §17.7 Redis failure runbook remediation step (1): Replaced `replica_count = 1 assumption` with `cached_replica_count` and added a brief description of what that means (last successfully cached gateway replica count).

**Regression check:** No other occurrences of `replica_count = 1` remain in the runbook. The behavior description is now consistent with the quota fail-open logic documented elsewhere in the spec.

---

### OPS-017 [High] — Redis runbook `replica_count = 1` (same as SEC-026)

**Status:** Already Fixed (resolved by SEC-026 fix above)

**Section:** §17.7

**Problem:** Identical to SEC-026 — both findings referenced the same `replica_count = 1` text in the Redis runbook.

**Resolution:** The SEC-026 fix applied above resolves this finding. No additional changes required.

---

### OBS-017 [High] — Key metrics defined in body but absent from canonical table

**Status:** Fixed

**Section:** §16.1

**Problem:** Three metrics were referenced in body text but missing from the canonical §16.1 metrics table: `lenny_task_reuse_count` (§5.2 task-mode section), `lenny_warmpool_sdk_demotions_total` (§6.1 demotion rate), and `lenny_warmpool_claims_total` (§6.1 demotion rate denominator). Their absence from the canonical table created an incomplete instrumentation contract.

**Fix applied:**
- §16.1 metrics table: Added three new rows under the "Warm Pool Replenishment" subsection:
  - `lenny_warmpool_claims_total` (Counter, labeled by `pool`, `runtime_class`) — warm pod claim events.
  - `lenny_warmpool_sdk_demotions_total` (Counter, labeled by `pool`, `runtime_class`) — SDK-warm demotions with cross-reference to §6.1.
  - `lenny_task_reuse_count` (Gauge, labeled by `pool`, `pod_name`) — task-mode pod reuse count for retirement tracking.

**Regression check:** Body-text references to all three metrics are unchanged and now have matching canonical entries.

---

### PRT-011 [High] — OutboundChannel has no back-pressure contract

**Status:** Fixed

**Section:** §15

**Problem:** The `OutboundChannel` interface's `Send` method noted that "events may be buffered or dropped according to the adapter's back-pressure policy" but no normative policy was defined. This left each adapter free to implement arbitrarily different (and potentially blocking) delivery semantics, with no platform-wide guarantee against a slow subscriber blocking the gateway event-dispatch loop.

**Fix applied:**
- §15 (ExternalAdapterRegistry, immediately after the `OutboundChannel` interface definition): Added a normative back-pressure policy comment block defining:
  - **Buffered-drop policy** (REQUIRED for webhook-based adapters): max buffer depth of `MaxOutboundBufferDepth` (default 256, configurable 16–4096); head-drop on overflow; eviction counter `lenny_outbound_channel_buffer_drop_total`.
  - **Bounded-error policy** (REQUIRED for connection-coupled adapters — SSE, long-poll): non-blocking write with 100 ms timeout; non-nil error from `Send` triggers channel close and subscriber reconnect.
  - Shared invariants: `Send` MUST NOT block > `MaxOutboundSendTimeoutMs` (default 100 ms); `Send` must be goroutine-safe; buffer limit applies per channel instance (not globally).
  - `BaseAdapter` default: buffered-drop with depth 256.

**Regression check:** No existing `Send` implementations are modified — the policy is additive. Existing adapters that embed `BaseAdapter` inherit the buffered-drop policy.

---

### PRT-012 [High] — MCP version support creates silent breakage on third release

**Status:** Fixed

**Section:** §15.2, §15.5

**Problem:** When a third MCP version ships, the oldest version exits its 6-month deprecation window and is dropped. Any long-lived session that negotiated the old version at connect time would have its protocol deserialization path removed mid-session — causing silent breakage without reconnection. The spec had no session-lifetime exception for in-flight sessions.

**Fix applied:**
- §15.2 compatibility policy: Added "Session-lifetime exception for deprecated versions" paragraph specifying:
  - Version removal applies only to **new** connection negotiations.
  - Established connections (completed `initialize` handshake before deprecation deadline) continue for the session's full lifetime using the `negotiatedVersion` field.
  - `lenny_mcp_deprecated_version_active_sessions` gauge emitted during pre-deployment preflight to allow operator drain before deployment.
  - Fallback path if undrained sessions survive the deployment: degradation annotation rather than abrupt termination.

**Regression check:** The existing support-window policy (current + previous, 6-month deprecation) is unchanged. The exception is additive.

---

### SCH-019 [High] — OutputPart schemaVersion consumer obligation contradicts itself

**Status:** Fixed

**Section:** §15.4.1, §15.5

**Problem:** Section 15.4.1 stated that durable consumers (TaskRecord readers) "MUST reject the read with a structured error" on unrecognized `schemaVersion`, while Section 15.5 item 7 stated durable consumers "MUST forward-read." The two rules directly contradicted each other.

**Fix applied:**
- §15.4.1 "Consumer obligation — durable storage (TaskRecord)": Changed from "MUST reject" to "MUST forward-read," aligning with Section 15.5 item 7. Updated the rationale to explain that rejection at read time creates compliance gaps for 13-month-retained records.
- §15.5 item 7 (final paragraph): Updated the cross-reference to explicitly say "forward-read rule" and reference the updated §15.4.1 language, ensuring both sections say the same thing.

**Regression check:** Section 15.5 item 7 general durable-consumer rule is unchanged. The live-consumer rule (MAY reject or forward-read with degradation signal) is unchanged. Only the durable-storage paragraph in §15.4.1 is corrected.

---

### SCH-020 [High] — `lenny-blob://` resolution requires undocumented `GET /v1/blobs/{ref}` endpoint

**Status:** Fixed

**Section:** §15.1, §15.4.1

**Problem:** Section 15.4.1 documented the `lenny-blob://` URI scheme and stated "REST clients may dereference directly via `GET /v1/blobs/{ref}`" — but this endpoint was absent from the §15.1 REST API table. There was no documented auth requirement, error behavior, or response contract for the endpoint.

**Fix applied:**
- §15.1 REST API table: Added a new "Blob resolution" table with one entry:
  - `GET /v1/blobs/{ref}` — full description including: URL-encoded `lenny-blob://` URI in path; access control (caller must have read access to the owning tenant + session); response is blob bytes with `Content-Type` set to blob's `mimeType`; `404` on expired/missing blob; `403` on access denied.
  - Cross-reference to §15.4.1 `LennyBlobURI` scheme.
  - Normative note that external protocol adapters (MCP, OpenAI, A2A) MUST dereference internally and MUST NOT expose `lenny-blob://` URIs to external callers.

**Regression check:** The §15.4.1 reference to `GET /v1/blobs/{ref}` is unchanged and now has a corresponding table entry.

---

### SCH-021 [High] — WorkspacePlan schemaVersion rules absent

**Status:** Fixed

**Section:** §14, §15.5

**Problem:** Section 14 defined `WorkspacePlan` with a `schemaVersion` field in the JSON example but provided no versioning rules — no producer obligation, no consumer obligation, no migration SLA, and no guidance on unknown `source.type` values. Section 15.5 item 7 lists `WorkspacePlan` as a schema-versioned type but §14 had no corresponding versioning subsection.

**Fix applied:**
- §14: Added new subsection **14.1 WorkspacePlan Schema Versioning** covering:
  - Producer obligation: set `schemaVersion` to highest version required by emitted fields.
  - Live consumer (gateway reconciliation) obligation: MUST reject with `WORKSPACE_PLAN_SCHEMA_UNSUPPORTED` (HTTP 422) when encountering a higher `schemaVersion` than understood — incorrect workspace materialization is a correctness hazard.
  - Durable consumer obligation: MUST forward-read per Section 15.5 item 7 rules.
  - Migration window SLA: gateways within 24h (rolling upgrade); durable consumers within 90 days.
  - Backwards compatibility guarantee: new `schemaVersion` values MUST NOT remove/rename existing fields.
  - Unknown `source.type` handling: open string; unknown types skipped with warning, not rejected.

**Regression check:** Section 15.5 item 7 already lists `WorkspacePlan` as a schema-versioned type; the new §14.1 subsection fulfills that reference. No other section references are affected.

---

## Iteration 2 — High Finding Fixes (2026-04-07, batch 3)

### SLC-019 [High] — Derive preconditions contradiction: Section 15.1 shows terminal-only states

**Status:** Fixed

**Section:** §7.1, §15.1

**Problem:** The §7.1 derive session semantics block correctly documented that non-terminal sessions (`running`, `suspended`, `resume_pending`, `resuming`, `awaiting_client_action`) are permitted with `allowStale: true`. However, the §15.1 preconditions table for `POST /v1/sessions/{id}/derive` listed only terminal states (`completed`, `failed`, `cancelled`, `expired`) as valid preconditions — directly contradicting §7.1 and creating an inconsistency between the semantics documentation and the API reference table.

**Fix applied:**
- §15.1 state-mutating endpoint preconditions table: Updated the `POST /v1/sessions/{id}/derive` row to list both terminal states (default) and non-terminal states (requires `allowStale: true` in the request body), with references to the workspace snapshot source and staleness warning. The table now accurately reflects the full §7.1 derive precondition specification.

**Regression check:** §7.1 derive semantics text is unchanged. The table now matches the semantics text. No other sections reference this table's precondition list directly.

---

### DEL-013 [High] — Cross-environment delegation has no isolation monotonicity check

**Status:** Fixed

**Section:** §8.3, §10.6

**Problem:** The §10.6 gateway enforcement path for cross-environment delegation (4-step sequence) had no isolation monotonicity check. This meant a session in environment A with `minIsolationProfile: sandboxed` could cross-environment-delegate to a `standard` (runc) pool in environment B, bypassing the monotonicity invariant documented in §8.3. The §8.3 enforcement applies at delegation time but only for same-environment delegations — the cross-environment path had its own 4-step sequence that lacked the equivalent check.

**Fix applied:**
- §10.6 gateway enforcement at delegation time: Added step 3.5 as an explicit isolation monotonicity check in the cross-environment enforcement path. The check verifies the target pool's isolation profile is at least as restrictive as the calling session's `minIsolationProfile`, identical to the §8.3 same-environment enforcement. Violations are rejected with `ISOLATION_MONOTONICITY_VIOLATED` and a `delegation.isolation_violation` audit event is emitted with `cross_environment: true`.

**Regression check:** §8.3 isolation monotonicity text is unchanged. The §10.6 enforcement path now applies the same monotonicity rule for cross-environment delegation that §8.3 applies for same-environment delegation.

---

### FLR-016 [High] — Tiered checkpoint cap + BarrierAck timeout can exceed terminationGracePeriodSeconds

**Status:** Fixed

**Section:** §10.1

**Problem:** The §10.1 preStop hook drain specification documented (a) a tiered checkpoint cap (30s / 60s / 90s depending on workspace size) and (b) a `checkpointBarrierAckTimeoutSeconds` (default 45s). The text noted that the tiered cap "must remain below `terminationGracePeriodSeconds - 30s`" but applied only to the checkpoint cap alone, not to the sum of cap + BarrierAck timeout. With default values, 90s (tiered cap) + 45s (BarrierAck) + 30s (stream drain) = 165s — significantly exceeding the default `terminationGracePeriodSeconds` of 120s. No CRD-level validation prevented this misconfiguration, leaving deployers to discover the overflow at runtime via SIGKILL-interrupted checkpoints.

**Fix applied:**
- §10.1 tiered cap section: Added "CRD validation rule — tiered cap + BarrierAck budget" block specifying the combined constraint formula `max_tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30 ≤ terminationGracePeriodSeconds`. The `SandboxWarmPool` CRD admission webhook enforces this, rejecting configurations that would overflow the grace period. Helm chart default updated to `terminationGracePeriodSeconds: 180` to provide headroom. Rejection error and metric documented.

**Regression check:** The existing text about the cap being clamped to `terminationGracePeriodSeconds - 30s` is unchanged (it remains as a runtime clamp). The new validation webhook is an additional guard at admission time, not a replacement for the runtime clamp.

---

### BLD-020 [High] — Phase 0 license gate contradicts CPS-008 open finding

**Status:** Fixed

**Section:** §18

**Problem:** Section 18 Phase 0 clearly documents that open-source license selection (ADR-008) is a hard gating item before Phase 1 begins. However, CPS-008 from iteration 1 (titled "No Licensing or OSS Governance Model") existed as an unresolved open finding, creating the contradiction that the license is both a Phase 0 gate and an open unresolved concern. Any reviewer reading both would conclude the spec was internally inconsistent about whether the license decision was made.

**Fix applied:**
- §19 Resolved Decisions table: Added entry 14 for "Open-source license and OSS governance (CPS-008)" documenting that the license is resolved by Phase 0 (ADR-008), with evaluation criteria, candidate licenses, and the Phase 2/17a schedule for `CONTRIBUTING.md` and `GOVERNANCE.md`. The entry explicitly marks CPS-008 as resolved.

**Regression check:** §18 Phase 0 text and §23.2 community adoption text are unchanged. The §19 table entry consolidates the existing Phase 0 and §23.2 statements into the resolved decisions registry.

---

### EXM-011 [High] — Concurrent-slot checkpoint has no tiered cap

**Status:** Fixed

**Section:** §5.2

**Problem:** The §5.2 concurrent-workspace slot failure and cleanup section documented per-slot checkpoints but applied no tiered cap to them. The same tiered cap that governs preStop hook checkpoint waits in §10.1 (based on workspace size) was absent from the per-slot checkpoint path. This meant a preStop hook on a concurrent-workspace pod with multiple large-workspace slots could accumulate unbounded checkpoint wait time — far exceeding what the single-session tiered cap analysis assumed.

**Fix applied:**
- §5.2 checkpoint granularity bullet: Added that per-slot checkpoints are subject to the same tiered cap as session-mode checkpoints, with the cap selected per-slot based on `last_checkpoint_workspace_bytes` for the `(session_id, slot_id)` pair. Added that the `SandboxWarmPool` CRD validation webhook enforces the sum `maxConcurrent × max_tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30 ≤ terminationGracePeriodSeconds` for concurrent-workspace pools.

**Regression check:** The §10.1 FLR-016 fix is consistent — that fix addressed the single-session formula; this fix extends it to concurrent-workspace pools. The §5.2 per-slot checkpoint text previously had no timeout mention; the addition is additive.

---

### EXM-012 [High] — Slot cleanup timeout formula produces sub-minimum values

**Status:** Fixed

**Section:** §5.2

**Problem:** The §5.2 slot cleanup timeout formula `cleanupTimeoutSeconds / maxConcurrent` (minimum 5s) relies on the runtime adapter to enforce the 5s floor. However, no CRD-level validation prevents a deployer from configuring `cleanupTimeoutSeconds: 8, maxConcurrent: 4`, which would produce `8/4 = 2s` — below the minimum. The adapter would silently clamp to 5s, masking the misconfiguration. At high `maxConcurrent` values this could leave deployers confused about actual cleanup timeout behavior.

**Fix applied:**
- §5.2 slot cleanup bullet: Updated the cleanup timeout description to `max(cleanupTimeoutSeconds / maxConcurrent, 5)` with an explicit note that the minimum is enforced at runtime by the adapter. Added a "CRD validation rule" paragraph specifying that the `SandboxWarmPool` admission webhook rejects any pool where `cleanupTimeoutSeconds < maxConcurrent × 5`, with the rejection error message and rationale.

**Regression check:** The adapter runtime floor (minimum 5s) is unchanged. The CRD validation is an additive admission-time guard that surfaces misconfiguration before runtime.

---

### WPL-013 [High] — Pool drain has no defined backpressure for in-flight sessions

**Status:** Fixed

**Section:** §15.1

**Problem:** The `POST /v1/admin/pools/{name}/drain` endpoint in §15.1 had only the description "Drain a pool" with no specification of how the gateway handles new session creation requests targeting a pool that is being drained. Clients retrying against a draining pool could spin indefinitely, and operators had no way to know when drain would complete. The absence of backpressure semantics meant different gateway implementations could diverge on behavior (reject immediately, queue, drop silently).

**Fix applied:**
- §15.1 admin API table: Expanded `POST /v1/admin/pools/{name}/drain` description to include: pool transitions to `draining` state; new session creation requests targeting the pool return `503 POOL_DRAINING` with `Retry-After: <seconds>` header; `Retry-After` value is computed from the longest active session age; response body includes `activeSessions` and `estimatedDrainSeconds`; `GET /v1/admin/pools/{name}` returns `"phase": "draining"` during drain; metric `lenny_pool_draining_sessions_total` tracks in-flight sessions.

**Regression check:** No existing endpoint behavior is changed — the expansion is additive documentation. The `POOL_DRAINING` error code is new and consistent with the existing error taxonomy (§16.3 `TRANSIENT` category, `retryable: true` with `Retry-After`).

---

### API-016 [High] — Experiments API uses inconsistent path identifier {name} vs {id}

**Status:** Fixed

**Section:** §15.1

**Problem:** All experiments admin endpoints used `{name}` as the path identifier (`GET /v1/admin/experiments/{name}`, `PUT /v1/admin/experiments/{name}`, `DELETE /v1/admin/experiments/{name}`) except `PATCH /v1/admin/experiments/{id}`, which used `{id}`. This was inconsistent with the §15.1 admin API design constraint ("All admin CRUD resources use `{name}` as the path identifier") and with every other experiments endpoint.

**Fix applied:**
- §15.1 admin API table: Changed `PATCH /v1/admin/experiments/{id}` to `PATCH /v1/admin/experiments/{name}`, making all experiments endpoints use `{name}` consistently.

**Regression check:** The `PATCH` body and semantics are unchanged. The path parameter type change from `{id}` to `{name}` aligns with the admin API design constraint stated in §15.1 and all other experiments endpoints.

---

### API-017 [High] — GET /v1/experiments/{name}/results not in admin path

**Status:** Fixed

**Section:** §15.1

**Problem:** The experiment results endpoint `GET /v1/experiments/{name}/results` was listed outside the admin path, inconsistent with all other experiment management endpoints (which are all under `/v1/admin/experiments/`). Experiment results contain per-variant session counts and usage metrics that should be admin-scoped — not accessible on the public client-facing API without role checks.

**Fix applied:**
- §15.1 admin API table: Moved `GET /v1/experiments/{name}/results` to `GET /v1/admin/experiments/{name}/results` and added a description specifying: returns per-variant session counts, token usage, and custom metric aggregates from eval hooks; requires `platform-admin` or `tenant-admin` role; cross-reference to Section 10.7.

**Regression check:** The `{name}` path identifier is now consistent with API-016 fix. The endpoint is now in the admin table alongside all other experiment endpoints. No non-admin experiment endpoints remain in the API table.

---

### API-018 [High] — POST /v1/sessions/{id}/replay undocumented

**Status:** Fixed

**Section:** §15.1

**Problem:** `POST /v1/sessions/{id}/replay` appeared in the §15.1 "Evaluation hooks" table with only a one-line description ("Re-run a session against a different runtime version using the same workspace and prompt history") and no specification of request body, preconditions, response contract, credential handling, or supported replay modes. This left implementors and runtime authors without a normative contract.

**Fix applied:**
- §15.1 immediately after the evaluation hooks table: Added a new **Session Replay Semantics** subsection documenting:
  - Request body fields: `targetRuntime` (required), `targetPool` (optional), `replayMode` (`prompt_history` | `workspace_derive`), `evalRef` (optional).
  - `replayMode: prompt_history` semantics: replays source transcript verbatim; workspace from source sealed/checkpoint snapshot.
  - `replayMode: workspace_derive`: equivalent to `derive` with runtime substitution; no pre-loaded prompt history.
  - Preconditions: source session must be terminal with a resolvable workspace snapshot; non-terminal returns `409 REPLAY_ON_LIVE_SESSION`.
  - Response: same structure as `POST /v1/sessions` (new `session_id`, `uploadToken`, `sessionIsolationLevel`).
  - Credential handling: independent `CredentialPolicy` evaluation per Section 7.1, step 6.

**Regression check:** The §15.1 evaluation hooks table entry for `POST /v1/sessions/{id}/replay` is updated to reference the new subsection. The new subsection is additive — no existing text is altered.
