# Technical Design Review Findings — 2026-04-07 (Iteration 8)

**Document reviewed:** `docs/technical-design.md` (8,649 lines)
**Iteration:** 8
**Perspectives covered:** 15 (Competitive), 16 (Warm Pool), 17 (Credentials), 18 (Schema), 19 (Build Sequence)
**Skipped (by instruction):** CRD-022, SCH-029, BLD-021/022
**Category prefixes:** CPS-022+, WPL-025+, CRD-025+, SCH-036+, BLD-026+
**Scope:** Critical, High, Medium only. Genuine design flaws or errors only.

---

## Prior Findings Status (Verified This Iteration)

**P15 Competitive:**
- CPS-022 (`GOVERNANCE.md` ship phase contradiction §19 vs §2/§23.2): Still open — §19 entry 14 says "`GOVERNANCE.md` ships in Phase 17a" while §2 and §23.2 say "drafted in Phase 2, finalized in Phase 17a." Contradiction persists.
- CPS-023 (LangSmith "no self-hosted path" claim): **Fixed** — §23 table now reads "LangSmith offers self-hosted Kubernetes deployment (available since 2024)..." with nuanced comparison.
- CPS-005 (external interceptors require gRPC, undisclosed): Still open.
- CPS-006 (no community runtime registry concept): **Fixed** — §23.2 now has a "Community runtime registry" paragraph scoping it as post-v1.

**P16 Warm Pool:**
- WPL-008 (PDB `minAvailable: minWarm` deadlock): **Fixed** — §4.6.1 now specifies `maxUnavailable: 1` with explicit deadlock rationale.
- WPL-012 (`pod_warmup_seconds` missing from pool schema): **Fixed** — `scalingPolicy.podWarmupSecondsBaseline` present in §4.6.2 and referenced in `WarmPoolReplenishmentSlow` alert.
- WPL-024 (`sdk_connecting` state watchdog missing): **Fixed** — `sdkConnectTimeoutSeconds` (default 60s), `lenny_warmpool_sdk_connect_timeout_total`, and `SDKConnectTimeout` alert all present.

**P18 Schema:**
- SCH-034 (webhook payload schema gaps): **Fixed** — per-event `data` schemas added for all event types, `callbackSecret` in WorkspacePlan JSON example, `X-Lenny-Signature` format fully specified.
- SCH-035 (`BillingEvent` null/absent field contract): **Fixed** — explicit null/absent contract paragraph added at end of §11.2.1 event schema table, including `corrects_sequence` semantics.
- SCH-031 (`capabilityInferenceMode` field absent): **Partially fixed** — field added to `RuntimeDefinition` prose in §5.1 (line 1771) with `strict`/`permissive` semantics and `toolCapabilityOverrides` reference. `ConnectorDefinition` still lacks the field (iter7 recommendation was to add it to both). Residual gap not re-numbered.

**P19 Build Sequence:**
- BLD-025 (`set-warm-count` CLI inconsistency): **Fixed** — §24.3 now correctly maps to `PUT /v1/admin/pools/{name}/warm-count`.

---

## Findings Summary

| Severity | Count |
| -------- | ----- |
| Critical | 0     |
| High     | 0     |
| Medium   | 6     |

---

## Detailed Findings

---

### CPS-024 §23.1 Differentiator 3 Claims E2B "Requires Hosted Infrastructure" — Contradicts §23 Table [Medium]
**Section:** 23, 23.1

§23's competitive table (line 8510) accurately states:

> "E2B — Market-leading AI sandbox with Firecracker microVMs, ~150ms boot times, **self-hosting options**. Primary comparison point."

§23.1 differentiator 3 (line 8530) states:

> "**E2B, Fly.io Sprites, and Modal require their hosted infrastructure**; Temporal Cloud is available but self-hosted Temporal adds significant operational burden."

These statements are directly contradictory within the same document. E2B has supported self-hosted deployment (open-source version on Docker and Kubernetes) since 2024. The §23 table correctly acknowledges this; §23.1 repeats an obsolete claim that was accurate of E2B's earlier hosted-only period.

**Impact:** §23.1 differentiators are the primary competitive persuasion surface. An enterprise evaluator currently self-hosting E2B will immediately identify this error, undermining confidence in the entire competitive analysis. The spec also contradicts itself — a reader comparing §23 to §23.1 gets conflicting signals.

**Recommendation:** Update §23.1 differentiator 3 to accurately represent E2B: "E2B offers an open-source self-hosted option, but requires operators to manage Firecracker/microVM infrastructure separately from their existing Kubernetes clusters. Fly.io Sprites and Modal require their hosted infrastructure. Lenny runs as a native Kubernetes deployment on any existing cluster using standard CRDs and RuntimeClasses, with no additional VM infrastructure layer."

---

### WPL-025 `scaleToZero.timezone` Support Incorrectly Attributed to Kubernetes CronJob — Version Requirement Is Wrong [Medium]
**Section:** 4.6.1

§4.6.1 documents the `scaleToZero` feature (line 413):

> "**Cron timezone:** Both `schedule` and `resumeAt` are interpreted as **UTC by default**. An optional `timezone` field (IANA timezone string, e.g., `"America/New_York"`) overrides the default... **Native timezone support in Kubernetes CronJob requires Kubernetes ≥ 1.27; the Helm chart validates the cluster version and rejects non-UTC `timezone` values on older clusters.**"

The `scaleToZero` schedule is evaluated by the **PoolScalingController** — a Go binary — not by Kubernetes `CronJob` resources. The PoolScalingController runs cron schedules internally using a Go cron library (e.g., `github.com/robfig/cron`). Go cron libraries support IANA timezone strings natively, regardless of Kubernetes version. The Kubernetes ≥ 1.27 requirement is accurate only for the `.spec.timeZone` field on `kind: CronJob` resources — a completely different feature that is not used here.

**Consequences of the error:**

1. Operators on Kubernetes 1.24–1.26 are incorrectly told they cannot use `scaleToZero.timezone`. They can — the feature is Go-library-based, not Kubernetes-CronJob-based.

2. The Helm chart "validates the cluster version and rejects non-UTC timezone values on older clusters" — if this check is implemented as written, it silently forces UTC on < 1.27 clusters even when the PoolScalingController can handle the timezone correctly.

3. If the PoolScalingController actually does use Kubernetes CronJob resources to implement `scaleToZero`, none of the CronJob resource management is documented: no CronJob template in the Helm chart section, no controller reconciliation for CronJob lifecycle, no interaction with `SandboxWarmPool` CRD on CronJob trigger. This absence makes the CronJob interpretation implausible.

**Recommendation:** Remove the "Kubernetes CronJob requires Kubernetes ≥ 1.27" sentence. Replace with: "The `timezone` field is parsed by the PoolScalingController's embedded cron scheduler. Any IANA timezone string is accepted on all Kubernetes versions; timezone handling is Go-library-based and independent of Kubernetes CronJob support." If the Helm chart currently contains a cluster-version check gating `timezone` values, remove it.

---

### CRD-025 `azure_openai` Provider `materializedConfig` Schema Entirely Absent from §4.9 [Medium]
**Section:** 4.9 (`materializedConfig` Schema by Provider table)

The `materializedConfig` schema table (§4.9, lines 1096–1128) documents field-level schemas for `anthropic_direct`, `aws_bedrock`, `vertex_ai`, `github`, and `vault_transit`. The `azure_openai` provider has **no entry** in this table.

`azure_openai` is a documented first-class provider:
- Credential Provider table (§4.9, line 950): "Azure AD / API key → Short-lived token + endpoint config"
- `leaseTTLSeconds` table (line 993): own row with Azure AD token ceiling (86400s) and API-key notes
- Secret shape table (line 1022): `apiKey` field only

But `azure_openai` has no `materializedConfig` row. Runtime authors implementing Azure OpenAI support cannot know:
- What fields are in `materializedConfig` (`apiKey`? `accessToken`? `endpoint`? `deploymentName`? `apiVersion`?)
- Whether an `expiresAt` field exists (required for Azure AD token-backed pools, which expire; the `vertex_ai` and `github` rows include this for the same reason)
- How proxy mode is represented (`proxyUrl` / `leaseToken` pattern, as in other providers)
- The `required` vs optional classification of each field

The Secret shape table documents only `apiKey` — but the provider description says "Azure AD / API key," implying two distinct credential types. Azure AD token-backed pools produce a short-lived `accessToken`, not an `apiKey`. These have different fields, different `expiresAt` semantics, and different proxy-mode behavior. A runtime must know which type it received to correctly authenticate with Azure OpenAI.

**Impact:** Runtime authors must reverse-engineer `azure_openai` materialized config structure from the provider description prose. The spec's own warning (line 1132) — "Runtimes MUST treat all fields whose names contain `key`, `token`, `secret`... as sensitive" — cannot be applied to unknown fields.

**Recommendation:** Add `azure_openai` to the `materializedConfig` schema table, covering both API-key and Azure AD token credential types:

| Provider | Field | Type | Required | Encoding / Notes |
|---|---|---|---|---|
| `azure_openai` | `apiKey` | string | yes (direct, API-key pools) | Azure OpenAI API key. Omitted for Azure AD pools and in proxy mode. |
| | `accessToken` | string | yes (direct, Azure AD pools) | Short-lived Azure AD access token. Plaintext. Omitted for API-key pools and in proxy mode. |
| | `endpoint` | string | yes | Azure OpenAI endpoint URL (e.g., `https://<resource>.openai.azure.com`). Present in both modes. |
| | `deploymentName` | string | yes | Azure model deployment name. Present in both modes. |
| | `apiVersion` | string | no | Azure OpenAI REST API version. Defaults to latest stable if omitted. |
| | `expiresAt` | string | yes (Azure AD pools) | Token expiry (ISO 8601 UTC). Must equal or precede lease `expiresAt`. Absent for API-key pools. |
| | `proxyUrl` | string | yes (proxy) | Set in proxy mode. |
| | `leaseToken` | string | yes (proxy) | Set in proxy mode. |

Add a note distinguishing API-key pools (no `expiresAt`) from Azure AD pools (`expiresAt` required), matching the pattern of the `anthropic_direct` synthetic-TTL note.

---

### SCH-036 `delegation.completed` Webhook Event `status` Field Documents Incomplete Enum [Medium]
**Section:** 14 (Webhook Delivery Model, per-event `data` schemas)

The per-event `data` schema for `delegation.completed` (§14, line 5796) documents:

```
"delegation.completed": { "childSessionId": "<id>", "status": "completed|failed", "usage": { ... } }
```

A child delegation session can reach any of five terminal states: `completed`, `failed`, `terminated` (externally cancelled by admin), `cancelled` (user/runtime), and `expired` (timeout). §8 "Completed subtree offloading" (line 2864) explicitly states: "When a child session reaches a terminal state (**completed, failed, cancelled, expired**)." The five sibling session-level webhook events already defined in §14 cover the full terminal state set: `session.completed`, `session.failed`, `session.terminated`, `session.cancelled`, `session.expired`.

The `delegation.completed` event is fired for **any** child session terminal state, not just `completed` and `failed`. When a child session is cancelled or expires, the parent and any webhook receiver get a `delegation.completed` event with an undocumented `status` value. Receivers coded against the documented `"completed|failed"` enum cannot handle these cases.

**Specific failure scenario:** A parent orchestration session runs for 30 minutes. A child delegation times out due to `maxSessionAge`. The parent receives `delegation.completed` with some undocumented `status` value. If the receiver treats unrecognized status as an error condition, it may abort the parent prematurely. If it treats it as `completed`, it may proceed with incorrect results.

**Recommendation:** Update the `delegation.completed` data schema to match the full terminal state vocabulary:

```
"delegation.completed": {
  "childSessionId": "<id>",
  "status": "completed|failed|terminated|cancelled|expired",
  "terminatedBy": "<admin|system>  (present when status is terminated)",
  "cancellationReason": "<string>  (present when status is cancelled)",
  "expiryReason": "max_session_age|max_idle_time  (present when status is expired)",
  "errorCode": "<error_code>  (present when status is failed)",
  "usage": { "inputTokens": N, "outputTokens": N }
}
```

Align with the `session.terminated`, `session.cancelled`, `session.expired`, and `session.failed` event data schemas already defined in §14, reusing identical field names and semantics.

---

### BLD-026 Phase 11.5 References "User-Scoped Credential Elicitation Flow" — Feature Explicitly Prohibited in §4.9 [Medium]
**Section:** 18 (Phase 11.5), 4.9

Phase 11.5's load test scenario list (§18, line 8403) includes:

> "(4) user-scoped credential elicitation flow throughput"

§4.9 explicitly prohibits this feature (line 1204):

> "**Why credential elicitation via MCP is not supported.** Credential elicitation via the MCP `lenny/request_input` channel is intentionally not supported. The MCP message path is subject to logging, tracing, interceptor inspection, and transcript persistence — credential material flowing through this path would be exposed to all of these systems. User-scoped credentials must be registered via the dedicated `POST /v1/credentials` endpoint..."

There is no "credential elicitation flow" in the design. The user-scoped credential registration path (`POST /v1/credentials`) is a direct REST call, not an MCP elicitation round-trip. Phase 11.5 cannot load-test throughput of a feature that is architecturally prohibited.

**Compounding issue:** Phase 11 itself (line 8402) contains: "user-scoped credentials (**elicitation flow**, `/v1/credentials` endpoints for pre-authorized registration and resolution)." This uses "elicitation flow" as a description of the `POST /v1/credentials` REST path — which is incorrect. The term "elicitation" in Lenny's protocol vocabulary refers to the MCP `lenny/request_input` mechanism. Using it here implies a feature that §4.9 expressly rejects.

**Impact:** An implementor reading Phase 11 and Phase 11.5 in sequence may attempt to build an MCP-based credential elicitation mechanism. Doing so violates the security design. The contradiction sends conflicting signals: §4.9 says "not supported," §18 says "measure its throughput."

**Recommendation:**
1. In Phase 11 (line 8402), replace "user-scoped credentials (elicitation flow, `/v1/credentials` endpoints...)" with "user-scoped credentials (pre-authorized registration via `POST /v1/credentials` endpoints; resolution at session creation time)."
2. In Phase 11.5 scenario (4), replace "user-scoped credential elicitation flow throughput" with "user-scoped credential registration and session-creation resolution throughput: `POST /v1/credentials` registration latency under concurrent load; session creation with `preferredSource: user` latency vs pool-mode baseline."

---

### BLD-027 Phase 16 Has No Deliverable Detail, Definition-of-Done, or Integration Test Gate [Medium]
**Section:** 18 (Phase 16)

Phase 16 in the build sequence table reads in its entirety:

> `| 16 | Experiment primitives, PoolScalingController experiment integration. | A/B testing infrastructure |`

This is the least-specified phase in the build sequence. Adjacent phases provide substantially more detail:
- **Phase 15**: lists 7 named deliverables (tag-based selectors, member RBAC, `mcpRuntimeFilters`, `connectorSelector`, cross-environment delegation enforcement, billing rollup endpoint, membership analytics endpoints).
- **Phase 12b/12c**: explicit integration test gates listed as prerequisites for merging.
- **Phase 13.5**: seven specific load test scenarios with cross-references to SLOs.
- **Phase 17a**: six deliverables with explicit community-launch gate semantics.

Section 10.7 ("Experiment Primitives") defines at minimum 10 subsystems that would need Phase 16 implementation: `ExperimentSpec` CRD, `ExperimentRouter` (percentage and user-list modes), sticky assignment (session and user scoped), `VariantPoolSpec`, PoolScalingController variant pool lifecycle (creation, `minWarm` recomputation, teardown on pause/conclude), rollback trigger monitoring, isolation monotonicity check for variant pools, and the `ExperimentTargetingCircuitOpen` circuit-breaker. None of these are named in Phase 16.

The phase also omits:
- Whether Phase 15 (environment resource) is a hard prerequisite (the `ExperimentRouter` uses environment-scoped pool selection)
- Integration test gate (unlike Phase 12b/12c)
- Whether Phase 13.5 should baseline experiment-targeting latency and sticky-cache throughput (it does not list those scenarios)
- Definition-of-done criteria for "A/B testing infrastructure"

**Impact:** A build agent or engineering team reading Phase 16 cannot determine what to build, in what order, or when the phase is complete. All of §10.7 is implicitly in scope with no sequencing. Without a Phase 15 prerequisite declaration, the environment resource integration may be attempted out of order.

**Recommendation:** Expand Phase 16 to Phase 12b/12c specification level. At minimum add:
- Named deliverables: `ExperimentSpec` admin API + CRD, `ExperimentRouter` (both targeting modes), sticky assignment, variant pool lifecycle in PoolScalingController, rollback trigger monitoring, isolation monotonicity check.
- Prerequisite: Phase 15 (environment resource) is a hard prerequisite.
- Integration test gate: `ExperimentRouter` assignment consistency (same bucket → same variant), sticky assignment persistence across reconnects, variant pool `minWarm` correctly recomputed when base pool demand changes.
- Add experiment-targeting latency and sticky-cache throughput as scenarios (7) and (8) in Phase 13.5.

---

## Findings Summary Table

| ID | Perspective | Section | Severity | Description |
|----|-------------|---------|----------|-------------|
| CPS-024 | P15 Competitive | 23, 23.1 | Medium | §23.1 claims E2B requires hosted infrastructure; §23 table correctly says E2B has self-hosting options — internal contradiction |
| WPL-025 | P16 Warm Pool | 4.6.1 | Medium | `scaleToZero.timezone` attributed to Kubernetes CronJob ≥ 1.27; feature is Go-library-based, version requirement is wrong |
| CRD-025 | P17 Credentials | 4.9 | Medium | `azure_openai` `materializedConfig` schema entirely absent; five other providers are fully documented |
| SCH-036 | P18 Schema | 14 | Medium | `delegation.completed` webhook `status` enum documents only `completed\|failed`; omits `terminated`, `cancelled`, `expired` terminal states |
| BLD-026 | P19 Build Sequence | 18 (Phase 11.5) | Medium | Phase 11.5 load test references "credential elicitation flow" that §4.9 explicitly prohibits |
| BLD-027 | P19 Build Sequence | 18 (Phase 16) | Medium | Phase 16 has single-line entry with no deliverable detail, no integration test gate, no prerequisite declaration |

**Total new findings: 6 (all Medium). Zero High or Critical.**

No regressions on previously Fixed findings detected within the scope of these five perspectives.
