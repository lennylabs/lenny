# Technical Design Review Findings — 2026-04-07 (Iteration 7)

**Document reviewed:** `docs/technical-design.md`
**Review framework:** `docs/review-povs.md`
**Iteration:** 7 (25 agents, 1 per perspective)
**Total findings:** ~80 across 25 perspectives
**Scope:** Critical, High, and Medium

## Findings Summary

| Severity | Count |
| -------- | ----- |
| Critical | 0     |
| High     | 5     |
| Medium   | ~75   |

### High Findings

| # | ID | Perspective | Finding | Section |
|---|-----|-------------|---------|---------|
| 1 | NET-026 | Network | lenny-system "explicit deny" is not a valid K8s NetworkPolicy construct | 13.2 |
| 2 | DXP-025 | DX | Lifecycle channel message schemas never defined — blocks Full-tier authors | 4.7, 15.4 |
| 3 | OBS-032 | Observability | `CredentialProactiveRenewalExhausted` alert missing from §16.5 (regression) | 4.9, 16.5 |
| 4 | OBS-033 | Observability | `NetworkPolicyCIDRDrift` + `AdmissionWebhookUnavailable` absent from §16.5 | 13.2, 17.2, 16.5 |
| 5 | POL-032 | Policy | Five policy error codes missing from §15.1 catalog | 8.3, 15.1 |

### Medium Finding Categories (~75 total)

- **Error catalog completeness** (~15 codes): API-035, PRT-026, CMP-027, CRD-023, DEL-026, plus overlap with POL-032
- **§16.5 alert table completeness** (~10 alerts): OBS-034, OBS-035, OBS-036, CMP-028, CMP-029
- **lenny-ctl §24 completeness** (~5 commands): OPS-030, OPS-031, OPS-032, API-036
- **Cross-reference errors** (8): DOC-124 through DOC-131
- **Carry-forwards from iter1** (~20): EXM-003/004/005/006, WPL-008/009/012, SLC-009/011, STR-026/027, CPS-005/006, DEL-027, SCH-031, etc.
- **New granular gaps** (~17): K8S-024, SEC-038, SCL-027/028, PRT-024/025/027, DXP-026/027, TNT-023/024, SLC-030, MSG-028/029/030/031, WPL-024, EXM-027, EXP-025/026, etc.

_Detailed findings from each perspective are in the subagent outputs. This file is the consolidated summary with fix tracking below._

---

## Detailed Findings by Perspective

---

## P1. Kubernetes Infrastructure & Controller Design

### K8S-024 `lenny-sandboxclaim-guard` Webhook Lacks HA Deployment Spec, PDB, and Alert [Medium]
**Section:** 4.6.1, 16.5

The `lenny-sandboxclaim-guard` ValidatingAdmissionWebhook is deployed with `failurePolicy: Fail` — any outage blocks ALL `PATCH`/`PUT` on SandboxClaim resources, halting new session creation platform-wide. Yet the spec provides no replica count, PodDisruptionBudget, or availability alert. Every other fail-closed webhook (OPA/Gatekeeper in §17.2, `lenny-label-immutability` in §13.2, cosign, `lenny-data-residency-validator`) specifies `replicas: 2`, PDB `minAvailable: 1`, and a named availability alert. This webhook is the only one on the session-creation hot path without these safeguards.

**Recommendation:** Add `replicas: 2`, PDB `minAvailable: 1`, and `SandboxClaimGuardUnavailable` Critical alert (>30s unreachable) to §4.6.1. Add alert to §16.5. Add preflight check to §17.6.

---

## P2. Security & Threat Modeling

### SEC-038 A2A `pushNotification.url` Has No SSRF Mitigations [Medium]
**Section:** 21, 14

The A2A outbound push path accepts a caller-supplied `pushNotification.url` and POSTs session events to it. The `callbackUrl` (§14) has comprehensive SSRF mitigations (HTTPS-only, DNS pinning, private IP rejection, isolated worker, optional domain allowlist). None of these apply to the A2A push URL. An authenticated A2A caller can target internal cluster IPs. A2A is post-v1, but `OutboundSubscription.CallbackURL` is defined in the current adapter interface spec (§15) with no SSRF gap noted.

**Recommendation:** Specify that A2A `pushNotification.url` must pass the same SSRF validation as `callbackUrl` (§14) before storing. Add a note in §21 that these mitigations are required before the A2A adapter is enabled.

---

## P3. Network Security & Isolation

### NET-026 lenny-system Gateway Row Claims "Explicit Deny" — Not a Valid K8s NetworkPolicy Construct [High]
**Section:** 13.2

The lenny-system NetworkPolicy table's gateway row states "Explicit deny for cloud metadata endpoints." Kubernetes NetworkPolicy v1 is allowlist-only — explicit deny rules do not exist. No Helm chart can render this. The underlying IMDS protection requires `except` blocks on any broad CIDR rule for the kube-apiserver egress path, but neither the CIDR source nor the `except` block is defined. The controller row also lists "kube-apiserver (TCP 443)" without CIDR or IMDS protection.

**Recommendation:** Remove "Explicit deny" language. Specify the kube-apiserver CIDR (e.g., `{{ .Values.kubeApiServerServiceIP }}/32`). Add preflight validation. Apply same spec to controller row.

### NET-027 MinIO In-Transit TLS Not Mandated [Medium]
**Section:** 12.5, 10.3

Redis and PgBouncer have mandatory TLS with server-side enforcement, startup probes, and CI tests. MinIO has none. The Helm example uses `http://minio.lenny-system:9000` (plaintext). MinIO stores checkpoints, workspace snapshots, and session transcripts — SSE-KMS encrypts at rest but data is decrypted before transmission over plaintext.

**Recommendation:** Add MinIO TLS enforcement to §10.3. Change Helm example to `https://`. Add "MinIO TLS" to profile-invariant requirements. Add dev-mode plaintext exception.

### NET-028 Token Service "mTLS Port" Never Defined — NetworkPolicy Unrenderable [Medium]
**Section:** 13.2, 10.3

Both gateway egress and Token Service ingress rows specify "mTLS port" with no numeric value and no Helm reference. Every other row has a concrete port. Without a port number, both NetworkPolicy manifests are unrenderable.

**Recommendation:** Introduce `{{ .Values.tokenService.grpcPort }}` (default: 50052). Update both NetworkPolicy rows and the §10.3 certificate table.

---

## P4. Scalability & Performance Engineering

_(Detailed findings in `review-findings-20260407093942-scl.md`)_

### SCL-027 §12.4 Tier 3 Redis Throughput Table Built on 10× Wrong Session Count [Medium]
**Section:** 12.4

The table opens with "~1,000 concurrent sessions" but Tier 3 is 10,000. Scaling by 10× produces ~6,000–6,500/s sustained writes, not 650/s. The conclusion "trivially safe on a single primary" is wrong at actual Tier 3 scale.

**Recommendation:** Change to "~10,000 concurrent sessions" and revise per-source rates proportionally. Revised conclusion: within budget given Tier 3 Redis Cluster topology, not trivially safe on Sentinel.

### SCL-028 `PodClaimQueueSaturated` Alert Condition References `maxConcurrent` — Wrong for Session-Mode [Medium]
**Section:** 16.5

The condition uses "50% of the pool's `maxConcurrent` session rate" — `maxConcurrent` is for concurrent-mode only, undefined for session/task modes. The alert is permanently broken for the common case.

**Recommendation:** Replace with `lenny_pod_claim_queue_depth > 0.25 × pool.minWarm for > 30s AND lenny_warmpool_idle_pods > 0`.

---

## P5. Protocol Design & Future-Proofing

### PRT-024 `AdapterCapabilities` Type Declared But Never Defined [Medium]
**Section:** 15

`ExternalProtocolAdapter` declares `Capabilities() AdapterCapabilities` but `AdapterCapabilities` has no struct definition anywhere in the 8,469-line spec. Any adapter author implementing the interface cannot know what fields to return.

**Recommendation:** Define `AdapterCapabilities` struct with fields: `PathPrefix`, `Protocol`, `SupportsSessionContinuity`, `SupportsDelegation`, `SupportsElicitation`, `SupportsInterrupt`. Document `BaseAdapter`'s default return.

### PRT-025 `mcp_version_deprecated` Header Uses Non-Standard Underscore Naming [Medium]
**Section:** 15.2

HTTP headers should use hyphens per RFC 7230. Underscores are dropped by some proxies (nginx default `underscores_in_headers off`). Every other custom header in the spec uses hyphens (`X-Lenny-Signature`, etc.).

**Recommendation:** Rename to `X-Lenny-Mcp-Version-Deprecated`.

### PRT-026 Three Normative Error Codes Absent from §15.1 Catalog [Medium]
**Section:** 15.1, 15.4.1, 4.6

`INVALID_DELIVERY_VALUE` (400), `OUTPUTPART_INLINE_REF_CONFLICT` (400), `SDK_DEMOTION_NOT_SUPPORTED` (422) are used normatively but have no catalog entries.

**Recommendation:** Add all three to the error catalog with category and retryable flag.

### PRT-027 Runtime Adapter gRPC Version Negotiation Unspecified in §15.4 [Medium]
**Section:** 15.4, 15.5

§15.5 says "the adapter advertises a protocol version at INIT" but §15.4 (the interim authoritative reference) has no version exchange documented — no field, no RPC, no format, no error on mismatch.

**Recommendation:** Add a "Protocol version negotiation (interim)" subsection to §15.4.2 specifying the `adapterProtocolVersion` field, gateway response, and compatibility check.

---

## P6. Developer Experience (Runtime Authors)

### DXP-025 Lifecycle Channel Message Schemas Never Defined [High]
**Section:** 4.7, 15.4.3, 15.4.5

Ten lifecycle channel messages (`lifecycle_capabilities`, `checkpoint_request`, `checkpoint_complete`, `interrupt_request`, `credentials_rotated`, `terminate`, `DEADLINE_APPROACHING`, etc.) are named and described semantically but have no JSON schemas anywhere. §15.4 declares itself the "authoritative interim reference" but only covers stdin/stdout. The lifecycle channel wire format is completely absent — blocking Full-tier runtime authors.

**Recommendation:** Add §15.4.6 Lifecycle Channel Message Reference with per-message JSON schemas, capability-negotiation exchange, and an annotated Full-tier protocol trace.

### DXP-026 `credentials.json` File Top-Level Structure Undefined [Medium]
**Section:** 4.7, 4.9, 15.4

The spec describes `/run/lenny/credentials.json` delivery and per-provider `materializedConfig` schemas, but the top-level JSON structure (single lease vs array vs wrapper object, which fields are written vs platform-internal, multi-provider representation) is never specified.

**Recommendation:** Add a "Runtime credential file contract" block under §4.7 item 4 with a canonical JSON example and field inclusion rules.

### DXP-027 `mcpNonce` Presentation Wire Format in MCP `initialize` Not Specified [Medium]
**Section:** 4.7, 15.4.3

Standard/Full-tier runtimes must present the `mcpNonce` "as the first message of the MCP `initialize` handshake" but the spec never says how: custom `params` field? pre-protocol message? `clientInfo` embedding? Runtime authors using existing MCP client libraries cannot implement this.

**Recommendation:** Add a "Nonce wire format" note in §15.4.3 with the exact mechanism and a concrete JSON example.

---

## P7. Operator & Deployer Experience

### OPS-030 `set-warm-count` API Mapping in §24.3 Contradicts §15.1 [Medium]
**Section:** 24.3, 15.1

§24.3 maps `set-warm-count` to `PATCH /v1/admin/pools/{name}` but §15.1 defines it as `PUT /v1/admin/pools/{name}/warm-count`. Different method and path.

**Recommendation:** Correct §24.3 to match §15.1.

### OPS-031 Warm-Pool Exhaustion Runbook Uses Wrong `lenny-ctl` Syntax [Medium]
**Section:** 17.7, 24.3

Runbook uses positional `lenny-ctl admin pools <name> set-warm-count --min <N+10>`. §24.3 canonical form is `lenny-ctl admin pools set-warm-count --pool <name> --min <N>`. Incompatible during incident.

**Recommendation:** Update §17.7 to use canonical §24.3 form.

### OPS-032 `lenny-ctl` Missing Entries for `sync-status` and `circuit-breaker` [Medium]
**Section:** 24.3

`GET /v1/admin/pools/{name}/sync-status` (for `PoolConfigDrift` alert) and `PUT /v1/admin/pools/{name}/circuit-breaker` (SDK-warm recovery) have no lenny-ctl entries.

**Recommendation:** Add both to §24.3.

---

## P8. Multi-Tenancy & Tenant Isolation

### TNT-023 `billing_seq_{tenant_id}` Postgres Sequence Provisioning Undocumented [Medium]
**Section:** 11.2.1, 15.1, 12.8

Per-tenant Postgres sequences for billing event monotonic numbering are required but no documentation covers creation at tenant registration or cleanup at deletion. A missing sequence causes a fatal error on the first billing event.

**Recommendation:** Document sequence creation in `POST /v1/admin/tenants` handler. Document cleanup in §12.8 tenant deletion lifecycle.

### TNT-024 No `TenantScopedSemanticCacheWrapper` at Gateway Interface Boundary [Medium]
**Section:** 4.9, 9.4

Third iteration of this concern (TNT-005/STR-023/STR-026). The spec has contract tests and `cacheScope` defaults but no structural gateway wrapper that enforces tenant scoping at call time. A buggy custom implementation can still serve cross-tenant hits in production.

**Recommendation:** Introduce a wrapper that always sets `TenantID` from authenticated context and post-filters results.

---

## P9. Storage Architecture & Data Management

### STR-026 Semantic Cache Custom-Backend Tenant Enforcement Is Contract-Only [Medium]
**Section:** 4.9

Carry-forward of STR-011/STR-023. The gateway dispatches directly to custom `SemanticCache` backends with no structural wrapper. A buggy implementation can serve cross-tenant cache hits. The contract test is development-time only.

**Recommendation:** Add a `TenantScopedSemanticCacheWrapper` that prepends `{tenant_id}:{user_id}:` to every key before delegating.

### STR-027 MinIO Bucket Versioning Enabled but No Delete-Marker Lifecycle Policy [Medium]
**Section:** 12.5

Bucket versioning creates delete markers on each checkpoint rotation. No lifecycle rule expires them. At Tier 3 scale, accumulated markers degrade `ListObjects` performance continuously.

**Recommendation:** Add a MinIO lifecycle rule expiring delete markers after 24h on checkpoints prefix. Add `NoncurrentVersionExpiration` of 1 day. Document in Helm chart responsibility.

---

## P10. Recursive Delegation & Task Trees

### DEL-026 Five Delegation-Specific Error Codes Absent from §15.1 Catalog [Medium]
**Section:** 8.3, 15.1

`INPUT_TOO_LARGE`, `CONTENT_POLICY_WEAKENING`, `CONTENT_POLICY_INTERCEPTOR_SUBSTITUTION`, `BUDGET_EXHAUSTED`, and `TOKEN_BUDGET_EXHAUSTED`/`TREE_SIZE_EXCEEDED` (internal Lua values) are used normatively but missing from the catalog. `BUDGET_EXHAUSTED` is the primary client-facing delegation rejection code.

**Recommendation:** Add catalog entries. Clarify `TOKEN_BUDGET_EXHAUSTED` and `TREE_SIZE_EXCEEDED` are internal Lua values; wire code is `BUDGET_EXHAUSTED` with `details.limitType`.

### DEL-027 `approvalMode: approval` Is a One-Line Stub — No API, Timeout, or Denial Behavior [Medium]
**Section:** 8.4

The `approval` mode has only "Gateway pauses parent, surfaces delegation request to client for approval." No API endpoint, no timeout, no denial error code, no concurrent-request handling.

**Recommendation:** Either fully specify with an approval API endpoint, timeout, and error codes, or mark as reserved/unimplemented in v1.

### DEL-028 `snapshotPolicyAtLease` Absent from `DelegationPolicy` YAML Schema [Medium]
**Section:** 8.3

The field is well-specified in prose (behavior, semantics, gateway snapshot mechanism) but absent from the normative YAML example. Deployers writing policy YAML from the example won't discover it.

**Recommendation:** Add `snapshotPolicyAtLease: false` to the DelegationPolicy YAML example.

---

## P11. Session Lifecycle & State Management

### SLC-009 Workspace Materialization on `scrub_warning` Pods — No Reset Step [Medium]
**Section:** 5.2, 6.2

Carry-forward from iter1. When scrub fails and the pod returns to pool with `scrub_warning`, workspace materialization adds new files but doesn't specify clearing `/workspace/current` first. Prior task files may persist.

**Recommendation:** Add normative statement: "Before materializing, the adapter MUST remove all files from `/workspace/current`."

### SLC-011 Derive Response Missing Active Connectors Re-Auth Hint [Medium]
**Section:** 7.1

Carry-forward from iter1. Derived sessions have no connector tokens, but the derive response doesn't indicate which connectors were active on the source. Clients building derive-heavy workflows silently create sessions that fail on connector tool use.

**Recommendation:** Add `activeConnectorsOnSource` array (connectorId, connectorDisplayName) to the derive response.

### SLC-030 `maxIdleTimeSeconds` Behavior Across Session States Undefined [Medium]
**Section:** 6.2, 11.3

The `maxSessionAge` timer has a complete per-state behavior table in §6.2 (from SLC-007 fix). The `maxIdleTimeSeconds` timer has none — no definition of what "idle" means, no per-state behavior, no persistence mechanism, no expiry transition.

**Recommendation:** Add a `maxIdleTimeSeconds` per-state behavior table to §6.2. Define: trigger condition (no agent_output, no tool_use), paused in recovery states, persisted via `last_agent_activity_at` timestamp, expiry fires `expired`.

---

## P12. Observability & Operational Monitoring

### OBS-032 `CredentialProactiveRenewalExhausted` Alert Missing from §16.5 [High]
**Section:** 4.9, 16.5

The CRD-021 fix explicitly states "Added `CredentialProactiveRenewalExhausted` warning alert." The metric is in §4.9 prose but the alert is not in §16.5. Regression.

**Recommendation:** Add to §16.5 Warning table with condition and cross-reference.

### OBS-033 `NetworkPolicyCIDRDrift` and `AdmissionWebhookUnavailable` Absent from §16.5 [High]
**Section:** 13.2, 17.2, 16.5

Two security-critical alerts defined in prose but missing from §16.5. `NetworkPolicyCIDRDrift` is a lateral-movement vulnerability signal. `AdmissionWebhookUnavailable` blocks all pod admission.

**Recommendation:** Add both to §16.5 Critical table.

### OBS-034 `DualStoreUnavailable` Alert Absent from §16.5 [Medium]
**Section:** 10.1, 16.5

Defined in §10.1 but absent from §16.5. Platform-wide degradation event.

**Recommendation:** Add to §16.5 Critical table.

### OBS-035 Four Prose-Only Warning Alerts Absent from §16.5 [Medium]
**Section:** 4.6.1, 4.6.2, 10.7, 13.2, 16.5

`WarmPoolBootstrapping`, `CRDSSAConflictStuck`, `ExperimentTargetingCircuitOpen`, `PgAuditSinkDeliveryFailed` — all defined in feature prose, absent from §16.5.

**Recommendation:** Add all four to §16.5 Warning table.

### OBS-036 gVisor Startup Latency SLO Has No Burn-Rate Alert [Medium]
**Section:** 16.5

`StartupLatencyBurnRate` covers runc P95 < 2s but not gVisor P95 < 5s. gVisor is the default isolation for multi-tenant deployments.

**Recommendation:** Add `StartupLatencyGVisorBurnRate` or split the existing alert into runc and gVisor variants.

---

## P13. Compliance, Governance & Data Sovereignty

### CMP-027 `COMPLIANCE_SIEM_REQUIRED` and `COMPLIANCE_CROSS_USER_CACHE_PROHIBITED` Absent from Catalog [Medium]
**Section:** 11.7, 9.4, 15.1

Both compliance enforcement error codes are normatively referenced but missing from the §15.1 catalog.

**Recommendation:** Add both with category, HTTP status, retryable flag.

### CMP-028 `DataResidencyWebhookUnavailable` Alert Absent from §16.5 [Medium]
**Section:** 12.8, 16.5

The webhook runs `failurePolicy: Fail` — outage blocks all residency-constrained resource operations. Alert defined in prose but not in §16.5.

**Recommendation:** Add as Critical to §16.5.

### CMP-029 `DataResidencyViolationAttempt` Alert Severity Understated [Medium]
**Section:** 12.8, 16.5

Audit event is critical severity; §16.5 alert is Warning. A cross-border transfer attempt is not Warning-grade.

**Recommendation:** Elevate to Critical in §16.5.

### CMP-030 GDPR Processing Restriction Doesn't Cover `POST /v1/sessions/start` [Medium]
**Section:** 12.8, 15.1

The `processing_restricted` flag blocks `POST /v1/sessions` but §12.8 doesn't mention the convenience endpoint `POST /v1/sessions/start`. A client using the convenience endpoint bypasses the GDPR Article 18 control.

**Recommendation:** Extend restriction to cover all session creation entry points. Apply at the session creation gate upstream of endpoint-specific logic.

### CMP-031 No `GET /v1/admin/legal-holds` Endpoint [Medium]
**Section:** 12.8, 15.1

Legal hold can be set/cleared but not enumerated. Legal teams cannot audit current holds. Operators must query Postgres directly.

**Recommendation:** Add `GET /v1/admin/legal-holds` with query params, role requirements, and response schema.

---

## P14. API Design & External Interface Quality

### API-035 Two Normative Error Codes Absent from §15.1 Catalog [Medium]
**Section:** 15.4.1, 15.1

`OUTPUTPART_INLINE_REF_CONFLICT` (400) and `INVALID_DELIVERY_VALUE` (400) are cited normatively but not in the catalog.

**Recommendation:** Add both to the catalog.

### API-036 `PUT /v1/admin/external-adapters/{name}/validate` Absent from §15.1 Table [Medium]
**Section:** 15.1, 15.2.1

The adapter validation gate endpoint is normatively referenced in §15.2.1 but missing from §15.1. Also missing from §24 lenny-ctl.

**Recommendation:** Add to §15.1 admin table and §24 lenny-ctl reference.

### API-037 `PUT /v1/credentials/{ref}` and `POST .../revoke` Missing from §15.1 [Medium]
**Section:** 4.9, 15.1

Both user credential management endpoints are fully specified in §4.9 but absent from the canonical §15.1 table.

**Recommendation:** Add both to §15.1 user credential management section.

---

## P15. Competitive Positioning & Open Source Strategy

_(Detailed findings in `review-findings-20260407093942-cps.md`)_

### CPS-022 `GOVERNANCE.md` Ship Phase Contradiction [Medium]
**Section:** 2, 19, 23.2, 18

§2/§23.2 say Phase 2; §19 says Phase 17a. Contradictory.

**Recommendation:** Align: draft in Phase 2, finalized in Phase 17a. Update §19.

### CPS-023 LangSmith "No Self-Hosted Path" Claim Factually Inaccurate [Medium]
**Section:** 23

LangSmith has had self-hosted K8s deployment since 2024. The blanket claim undermines credibility.

**Recommendation:** Replace with accurate nuanced comparison.

### CPS-005 External Interceptors Require gRPC — Polyglot Barrier Undocumented [Medium]
**Section:** 4.8, 23.2

Carry-forward from iter1. Deployers writing custom interceptors need gRPC — never disclosed.

**Recommendation:** One sentence in §4.8 noting gRPC requirement; HTTP webhook variants as future enhancement.

### CPS-006 No Community Runtime Registry Concept [Medium]
**Section:** 23.2, 5.1

Carry-forward from iter1. No place for runtime authors to publish adapters for operator discovery.

**Recommendation:** One sentence in §23.2 scoping a registry out of v1 as planned post-v1.

---

## P16. Warm Pool & Pod Lifecycle Management

### WPL-008 PDB `minAvailable = minWarm` Deadlocks Node Drains [Medium]
**Section:** 4.6.1, 6.2

Carry-forward from iter1. When pool has exactly `minWarm` idle pods (steady state), PDB blocks all evictions. Node drains stall.

**Recommendation:** Replace with `maxUnavailable: 1`. Document proactive replacement pod creation.

### WPL-009 `ConfigureWorkspace` RPC Semantics Underspecified [Medium]
**Section:** 4.7, 6.1

Carry-forward from iter1. No timeout, failure mode, idempotency, or state transition on failure.

**Recommendation:** Add: 10s timeout, fallback to DemoteSDK + pod-warm path on failure, idempotent, pod state `failed` if fallback also fails.

### WPL-012 `pod_warmup_seconds` Referenced as "Configured" but No Pool Field Exists [Medium]
**Section:** 16.5, 4.6.2, 17.8.2

Incomplete fix. The `WarmPoolReplenishmentSlow` alert says "the pool's configured `pod_warmup_seconds` baseline" but no such field exists on any pool CRD or API.

**Recommendation:** Add `scalingPolicy.podWarmupSecondsBaseline` (default: 30) to pool schema.

### WPL-024 No Per-Pod Watchdog for `sdk_connecting` State [Medium]
**Section:** 6.1, 6.2

SDK process hangs during pre-connection leave pods stuck in `sdk_connecting` indefinitely — phantom-warm pods that appear ready but are unclaimed. No timeout, no alert, no metric.

**Recommendation:** Add `sdkConnectTimeoutSeconds` (default: 60s). WarmPoolController transitions pods past deadline to `failed`. Add `lenny_warmpool_sdk_connect_timeout_total` counter and `SDKConnectTimeout` alert.

---

## P17. Credential Management & Secret Handling

### CRD-023 `CREDENTIAL_MATERIALIZATION_ERROR` Absent from Error Catalog [Medium]
**Section:** 4.9, 15.1

Normatively named and categorized in §4.9 but absent from §15.1.

**Recommendation:** Add to catalog: `INTERNAL` / 500 / `retryable: false`.

### CRD-024 Revoked Credential Re-Enable Path Undefined [Medium]
**Section:** 4.9, 17.7

Runbook says "re-enabling the credential ID" after rotation, but no admin API endpoint exists for un-revoking. The `revoked` status in `CredentialPoolStore` may be permanent, contradicting the runbook.

**Recommendation:** Either specify revocation as permanent (add new credential after rotation) or add a `re-enable` endpoint. Update runbook accordingly.

---

## P18. Content Model, Data Formats & Schema Design

_(Detailed findings in `review-findings-20260407093942-sch.md`)_

### SCH-031 `capabilityInferenceMode` Field Still Absent — Incomplete Fix [Medium]
**Section:** 5.1

Iter4 added a WARN log but the default remains `admin` and no `capabilityInferenceMode` field was created. Third-party unannotated tools silently fail with `TOOL_CAPABILITY_DENIED`.

**Recommendation:** Add `capabilityInferenceMode` field (`strict`/`permissive`) to RuntimeDefinition. Change default for unannotated tools from `admin` to `write` in `permissive` mode.

### SCH-034 Webhook Payload Schema Has Three Undocumented Gaps [Medium]
**Section:** 14

`data` field documented only for `session.completed` (5 other event types undocumented). `callbackSecret` absent from WorkspacePlan JSON example. `X-Lenny-Signature` format undocumented (no encoding, no signed payload construction, no replay window).

**Recommendation:** Document per-event `data` schemas. Add `callbackSecret` to examples. Specify HMAC signing input format.

### SCH-035 BillingEvent Flat Schema Has No Null/Absent Field Contract Per Event Type [Medium]
**Section:** 11.2.1

Conditional fields annotated "(for X events only)" with no null/absent semantics. `corrects_sequence` typed `uint64` with no sentinel for absent. Analytics consumers cannot write portable readers.

**Recommendation:** Define null/absent policy. Use nullable types or a discriminated union keyed on `event_type`.

---

## P19. Build Sequence & Implementation Risk

### BLD-023 Phase 5.75 References Non-Existent §4.10 [Medium]
**Section:** 18

"see Section 4.10" — Section 4 ends at 4.9. AuthEvaluator is in §4.8.

**Recommendation:** Change to "see Section 4.8".

### BLD-024 GOVERNANCE.md Phase Assignment Contradicted Across Four Locations [Medium]
**Section:** 2, 18, 19, 23.2

Three say Phase 2, one (§19) says Phase 17a.

**Recommendation:** Align §19: "draft in Phase 2; finalized in Phase 17a."

### BLD-025 `set-warm-count` CLI Mapping and Runbook Both Inconsistent with §15.1 [Medium]
**Section:** 15.1, 17.7, 24.3

§24.3 maps to `PATCH /v1/admin/pools/{name}` but §15.1 defines `PUT /v1/admin/pools/{name}/warm-count`. Runbook uses positional syntax incompatible with §24.3 flag-based form.

**Recommendation:** Correct §24.3 to match §15.1. Update §17.7 to canonical form.

---

## P20. Failure Modes & Resilience Engineering

_(Detailed findings in `review-findings-20260407093942-flr.md`)_

### FLR-023 GDPR Erasure Crash Window Between Commit and Verification [Medium]
**Section:** 12.8

Crash between erasure transaction commit and verification leaves `processing_restricted: true` set, salt deleted, and no way to distinguish "completed" from "failed."

**Recommendation:** Add a `phase` field to the erasure job record. On resume, check phase to determine recovery path.

### FLR-024 Tenant Guard Trigger Disabled-but-Present Not Detected [Medium]
**Section:** 12.3, 17.6

PostgreSQL `DISABLE TRIGGER` leaves the pg_trigger row but sets `tgenabled = 'D'`. Neither the preflight check nor gateway startup inspects `tgenabled`. A superuser disable is invisible.

**Recommendation:** Check `tgenabled != 'D'` in both the preflight Job and gateway startup verification.

### FLR-025 CheckpointBarrier Global ACK Timeout vs Tiered Cap Ambiguity [Medium]
**Section:** 10.1, 4.4

Global `checkpointBarrierAckTimeoutSeconds` (45s) can expire while a large workspace legitimately uploads within its tier-3 cap (90s). No metric distinguishes legitimate slow upload from unresponsive pod.

**Recommendation:** Enforce `checkpointBarrierAckTimeoutSeconds >= max_tiered_checkpoint_cap` in CRD validation. Add `lenny_checkpoint_barrier_ack_timeout_total` counter.

### FLR-026 `awaiting_client_action` Expiry — DLQ Entries Abandoned Without Sender Notification [Medium]
**Section:** 7.2, 7.3

When session expires, DLQ entries with remaining TTL are abandoned. Senders waiting on delivery confirmation are held in limbo for up to `maxResumeWindowSeconds` (900s).

**Recommendation:** On session terminal transition, drain DLQ and send `message_expired` receipts to all senders. Define as part of terminal-state cascade logic.

---

## P21. Experimentation & A/B Testing Primitives

### EXP-022 Results API Cursor Still Encodes Raw Primary Key [Medium]
**Section:** 10.7

Carry-forward from iter4. Base64-encoded `{"last_id":"abc123"}` leaks submission ordering across variants.

**Recommendation:** Use encrypted/opaque cursor encoding. Document that cursor format is not stable.

### EXP-023 Three Experiment Metrics/Alert Absent from §16.1/§16.5 [Medium]
**Section:** 10.7, 16.1, 16.5

`lenny_experiment_targeting_circuit_open`, `lenny_experiment_sticky_cache_invalidations_total`, and `ExperimentTargetingCircuitOpen` alert not in canonical tables.

**Recommendation:** Add all to §16.1/§16.5.

### EXP-024 Manual Rollback Trigger Table References Three Undefined Metrics [Medium]
**Section:** 10.7

`lenny_session_error_total`, `lenny_session_total`, `lenny_eval_score` — none exist in §16.1. Operators writing alert rules get silent evaluation failures.

**Recommendation:** Define the metrics in §16.1 or replace with metrics that exist.

### EXP-025 Percentage-Mode Hash Undefined for Null/Anonymous `user_id` [Medium]
**Section:** 10.7

`hash(null + experiment_id)` produces a constant bucket. All anonymous sessions go to one variant. `sticky: user` with null user_id is incoherent.

**Recommendation:** Specify bypass for null `user_id` (route to control). Add `excludeAnonymousSessions: true` default.

### EXP-026 Variant Pool Assignment Lacks Isolation Monotonicity Check [Medium]
**Section:** 10.7, 5.3, 8.3

`ExperimentRouter` can route to a variant pool with weaker isolation than the environment's `minIsolationProfile`. No check equivalent to the delegation monotonicity gate.

**Recommendation:** Verify variant pool isolation satisfies session's effective `minIsolationProfile` before assignment. Fall through to base runtime on failure.

---

## P22. Document Quality, Consistency & Completeness

### DOC-124 §17.6 Cites §13.4 for CNI — Should Be §13.2 [Medium]
### DOC-125 Four "Section 5" References Should Be "Section 8" for Delegation [Medium]
### DOC-126 §17.8.1 Self-References "Section 17.8" Circularly [Medium]
### DOC-127 §8.5 Tool List Inconsistent with §4.7 and §9.1 Canonical Lists [Medium]
### DOC-128 `deliveryMode` Field Missing from §11.2.1 Billing Event Schema [Medium]
### DOC-129 §16.5 Alerts Reference Non-Existent "Section 14.3" [Medium]
### DOC-130 Phase 5.75 References Non-Existent §4.10 [Medium]
### DOC-131 §13.2 References Non-Existent "Section 18.1" [Medium]

Eight cross-reference errors. All are either stale from iter1 (DOC-124 through DOC-128 — carry-forwards of DOC-105 through DOC-107, DOC-108, DOC-111) or new broken references from editing rounds (DOC-129 through DOC-131).

**Recommendation:** Fix all eight cross-references to point to correct sections.

---

## P23. Messaging, Conversational Patterns & Multi-Turn

### MSG-028 §7.2 Delivery Receipt Schema Diverged from §15.4 Canonical Schema [Medium]
**Section:** 7.2, 15.4

§7.2 inline schema has `status: "delivered|queued|dropped|error"` with fields `targetState`, `queueTTL`. §15.4 has `status: "delivered|queued|dropped|expired|rate_limited"` with fields `reason`, `deliveredAt`, `queueDepth`. Substantively different.

**Recommendation:** Remove §7.2 inline snippet, replace with reference to canonical §15.4 schema.

### MSG-029 `DUPLICATE_MESSAGE_ID` Deduplication Window Undefined [Medium]
**Section:** 7.2, 15.1

No window duration, backing store, TTL, or configurability for message ID deduplication.

**Recommendation:** Define: session-scoped or time-windowed, Redis key pattern, TTL, Helm config name.

### MSG-030 Message Delivery for Pre-Running States Undefined [Medium]
**Section:** 7.2

The six delivery paths don't cover `created`, `ready`, `starting`, `finalizing` states. A message to a `starting` session has undefined behavior.

**Recommendation:** Add rows for pre-running states: buffer in inbox or reject with `TARGET_NOT_READY`.

### MSG-031 `deadlock_detected` Event Has No JSON Schema [Medium]
**Section:** 8.8

`request_input_expired` got a schema; `deadlock_detected` still has only prose. Parent agents can't write deterministic deserialization.

**Recommendation:** Add normative JSON schema alongside the existing prose.

---

## P24. Policy Engine & Admission Control

### POL-032 Five Policy Error Codes Missing from §15.1 Catalog [High]
**Section:** 8.3, 11.7, 15.1

`INPUT_TOO_LARGE`, `CONTENT_POLICY_WEAKENING`, `CONTENT_POLICY_INTERCEPTOR_SUBSTITUTION`, `COMPLIANCE_SIEM_REQUIRED`, `BUDGET_EXHAUSTED` — all normatively used, none cataloged.

**Recommendation:** Add all five with category, HTTP status, retryable flag, and details fields.

### POL-033 "Priority > 600 Runs After All Built-ins" Is Incorrect at Guardrail-Active Phases [Medium]
**Section:** 4.8

`GuardrailsInterceptor` at 400 fires before external interceptors at 401-599. The blanket "> 600" claim is wrong for phases where guardrails are active. A MODIFY at 401-599 is not re-evaluated by the guardrail.

**Recommendation:** Replace blanket claim with phase-accurate statement. Note that downstream MODIFY is not re-evaluated.

### POL-034 `maxExtendableBudget` Layering Table Conflates Tenant Base Value with Ceiling [Medium]
**Section:** 8.6

Table shows "Tenant=300K" as if it's a cap, but it's the tenant base value. The actual cap is `leaseExtension.max.maxExtendableBudget`. Deployers may believe they can hard-cap via the base value.

**Recommendation:** Add footnote distinguishing base value from ceiling. Specify RBAC for per-runtime overrides.

---

## P25. Execution Modes & Concurrent Workloads

### EXM-003 Multi-Slot Pod Connection-Loss Behavior Undefined [Medium]
**Section:** 5.2, 10.4

Carry-forward from iter1. Coordinator-loss spec is for single-session pods. Multi-slot concurrent-workspace pods have undefined behavior: do all slots fail? interaction with whole-pod replacement trigger? slot counter reconciliation?

**Recommendation:** Add "Concurrent-workspace pod connection loss" subsection covering: all slots enter hold simultaneously, replacement trigger fires on timeout, Redis counter rehydrated from Postgres.

### EXM-004 Concurrent-Workspace Cross-Slot Residual State Enumeration Incomplete [Medium]
**Section:** 5.2

Carry-forward from iter1. Task mode has detailed residual state enumeration. Concurrent mode (worse isolation — simultaneous, not sequential) has only 4 items vs task mode's 8+. Missing: procfs cross-visibility, kill(2) reachability, IPC namespace, timing channels.

**Recommendation:** Add full "Cross-slot residual state vectors" paragraph modeled after task-mode enumeration.

### EXM-005 Graph-Aware Runtime Trace Span Emission Contract Undefined [Medium]
**Section:** 5.2, 16.3

Carry-forward from iter1. "Optionally emit trace spans via the observability protocol" but no mechanism specified (no MCP tool, no adapter endpoint, no trace context field in manifest).

**Recommendation:** Either specify `lenny/emit_span` MCP tool or remove the claim and defer to v2.

### EXM-006 Credential Lease Lifecycle Across Task Boundaries Unspecified [Medium]
**Section:** 5.2, 4.9

Carry-forward from iter1. Per-pod vs per-task lease semantics not stated. Manifest re-generation per task implies per-task credentials but this is never confirmed. Rotation and pool capacity implications differ significantly.

**Recommendation:** Add "Task-mode credential lease lifecycle" paragraph specifying per-pod or per-task semantics.

### EXM-027 Task-Mode `input_required` Holds Pod Indefinitely with No Task-Specific Timeout [Medium]
**Section:** 5.2, 8.6

A task-mode pod blocked on `lenny/request_input` is effectively idle, holding pool capacity and credential lease for up to `maxSessionAge` (7200s). Defeats the high-reuse task-mode design assumption.

**Recommendation:** Add `maxRequestInputWait` to `taskPolicy` (default 300s, separate from `maxSessionAge`). On expiry, transition task to `failed` with `input_required_timeout`.
