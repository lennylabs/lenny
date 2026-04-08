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

---

## High Finding Dispositions (Iteration 7)

---

### NET-026 — Fixed

**CHALLENGE:** The finding is correct and actionable. The gateway's base policy is allowlist-only — it has no broad CIDR rules, so IMDS is implicitly blocked. The "explicit deny" language in the component table is misleading and technically wrong (K8s NetworkPolicy v1 has no deny rules). Additionally, the kube-apiserver egress CIDR was untemplatized, making the row unrenderable. The blast radius of leaving this unfixed: implementers trying to render the gateway NetworkPolicy get confused about what "explicit deny" means and may conclude they need a CNI extension or additional tooling. The fix is minimal.

**CHANGES:** In §13.2 component table, gateway row updated to replace "Explicit deny for cloud metadata endpoints" with an accurate description of implicit blocking via allowlist-only policy. kube-apiserver CIDR parametrized as `{{ .Values.kubeApiServerCIDR }}` matching the pattern used in other rows.

---

### DXP-025 — Fixed

**CHALLENGE:** The lifecycle channel message names and semantics ARE scattered through §4.7 prose, but no message-level field schemas exist anywhere. For Full-tier runtime authors, this is a genuine implementation blocker: they cannot write a correct deserializer without knowing field names, types, and which fields are required. The blast radius is significant — every Full-tier runtime author writing a channel implementation must reverse-engineer from prose or ask the platform team. This is not polish. A cross-reference to §15.4 does not help because §15.4 explicitly covers only stdin/stdout. The recommended fix (a new §15.4.6 subsection) would be appropriate but is heavyweight for iteration 7. A compact table in §4.7 is proportionate.

**CHANGES:** Added a "Lifecycle channel message schemas" table directly in §4.7 after the message name listing, covering all 11 message types (both directions) with fields, types, and notes. No new section added — the table fits naturally at the existing location. `DEADLINE_APPROACHING` added as `deadline_approaching` (lowercase, consistent with other message type names).

---

### OBS-032 — Fixed

**CHALLENGE:** This is a confirmed regression. §4.9 prose explicitly names `CredentialProactiveRenewalExhausted` as a warning alert and the CRD-021 fix log says it was added to §16.5, but it is not there. The alert table is a canonical implementation contract — operators writing alert rules from the spec will miss this one. The blast radius: proactive renewal exhaustion is a pre-failure signal that occurs before the more disruptive fallback rotation. Missing this alert means operators don't get early warning. One-row fix.

**CHANGES:** Added `CredentialProactiveRenewalExhausted` Warning alert to §16.5 Warning table, after `CredentialPoolLow`, with condition referencing the `lenny_credential_proactive_renewal_exhausted_total` counter and cross-reference to §4.9.

---

### OBS-033 — Fixed

**CHALLENGE:** Both alerts are named in prose with clear conditions. `NetworkPolicyCIDRDrift` is a lateral-movement security signal — its absence from the canonical alert table means operators implementing the monitoring spec from §16.5 will miss a critical security alert. `AdmissionWebhookUnavailable` blocks pod admission with `failurePolicy: Fail` — missing this alert means a webhook outage could halt warm pool replenishment silently. Both are unambiguously Critical. The fixes are two table rows with no design decisions required.

**CHANGES:** Added `NetworkPolicyCIDRDrift` (Critical) and `AdmissionWebhookUnavailable` (Critical) to the §16.5 Critical alerts table, after `AuditGrantDrift`, with conditions matching the prose descriptions in §13.2 and §17.2 respectively.

---

### POL-032 — Fixed

**CHALLENGE:** All five codes are used normatively in the spec with concrete semantics. `BUDGET_EXHAUSTED` is the primary client-facing delegation rejection code — without a catalog entry, clients cannot distinguish it from `QUOTA_EXCEEDED` or `FORBIDDEN` and cannot implement correct retry/abandon logic. `COMPLIANCE_SIEM_REQUIRED` blocks tenant creation — clients need the catalog entry to surface actionable errors. `CONTENT_POLICY_WEAKENING` and `CONTENT_POLICY_INTERCEPTOR_SUBSTITUTION` are policy rejection codes for delegation chains — without catalog entries, delegation implementers get 403s with unrecognized codes. `INPUT_TOO_LARGE` is a hard content limit. The blast radius of all five being absent: any client implementation driven by the §15.1 catalog has gaps in error handling for delegation, compliance, and policy flows. These are not polish — they are missing entries in a canonical contract.

**CHANGES:** Added all five error codes to the §15.1 error catalog after `ENV_VAR_BLOCKLISTED`: `INPUT_TOO_LARGE` (PERMANENT/413), `CONTENT_POLICY_WEAKENING` (POLICY/403), `CONTENT_POLICY_INTERCEPTOR_SUBSTITUTION` (POLICY/403), `COMPLIANCE_SIEM_REQUIRED` (POLICY/422), `BUDGET_EXHAUSTED` (POLICY/429). Each entry includes category, HTTP status, description, details fields, and cross-reference to the governing section. The `BUDGET_EXHAUSTED` entry clarifies that `TOKEN_BUDGET_EXHAUSTED` and `TREE_SIZE_EXCEEDED` are internal Lua values and the wire code is always `BUDGET_EXHAUSTED` with `details.limitType`.

---

## Medium Finding Dispositions (Iteration 7)

---

### DOC-124 — Fixed

**CHALLENGE:** The finding is correct. §17.6 preflight table entry for CNI NetworkPolicy support references "Section 13.4" but §13.4 is "Upload Security". Network isolation and CNI enforcement is in §13.2 "Network Isolation". The wrong reference would send operators to the wrong section when diagnosing CNI failures.

**CHANGES:** Updated the CNI NetworkPolicy check row in §17.6 preflight table to reference `Section 13.2` instead of `Section 13.4`.

---

### DOC-125 — Fixed

**CHALLENGE:** The finding is correct. Five "Section 5" references in delegation and session record contexts all point to §5 "Runtime Registry and Pool Model" — which has nothing to do with what they describe. The affected references: (1) `session records (Section 5)` in §15.5 schema versioning — session records are in §7; (2) `delegation chain depth and fan-out throughput (Section 5)` in §18 Phase 13.5 — delegation is §8; (3) `without per-hop token budget or scope controls enforced at the platform layer (Section 5)` in §23 LangSmith row — delegation is §8; (4) `gateway-mediated delegation (Section 5)` in §23 Temporal row — delegation is §8; (5) `(Section 5, Principle 5)` in §23.1 — delegation is §8, Principle 5 is from §1. All five are straightforward corrections.

**CHANGES:** Updated all five "Section 5" references to their correct target sections: session records → Section 7; delegation throughput → Section 8; three competitive landscape delegation references → Section 8.

---

### DOC-126 — Fixed

**CHALLENGE:** The finding is correct. §17.8.1 ("Operational Defaults — Quick Reference") ends with "For per-tier recommended values, see Section 17.8." This is a circular self-reference — §17.8.1 is a subsection of §17.8. The intended reference is §17.8.2 ("Capacity Tier Reference"), which is the subsection that actually contains per-tier sizing tables.

**CHANGES:** Changed the cross-reference at the bottom of §17.8.1 from `Section 17.8` to `Section 17.8.2`.

---

### DOC-127 — Fixed

**CHALLENGE:** The finding is correct. §8.5 ("Delegation Tools") claims to list the tools "available on the platform MCP server for every delegation-capable pod" but omits four tools that §4.7 and §9.1 both list: `lenny/output`, `lenny/request_elicitation`, `lenny/memory_write`, `lenny/memory_query`. A runtime author reading §8.5 for delegation tooling would not know these exist.

**CHANGES:** Added four missing entries to the §8.5 delegation tools table: `lenny/output` (emit output parts to parent/client), `lenny/request_elicitation` (human input via elicitation chain, cross-ref §9.2), `lenny/memory_write` (write to memory store, cross-ref §9.4), `lenny/memory_query` (query memory store, cross-ref §9.4). The table now matches §4.7 and §9.1 canonical lists.

---

### DOC-128 — Fixed

**CHALLENGE:** The finding is correct. §4.9 explicitly states "The audit event `credential.leased` (Section 12.4) includes a `deliveryMode` field, enabling compliance teams to track and review all direct-mode credential deliveries." However, `credential.leased` is a billing event type in §11.2.1, and the billing event schema table has no `delivery_mode` field. The prose makes a normative claim about a field that doesn't appear in the schema. Compliance consumers driven by the §11.2.1 schema can't implement the audit use case described.

**CHANGES:** Added `delivery_mode` (string) field to the §11.2.1 billing event schema, scoped to `credential.leased` events, with description noting it enables compliance teams to audit direct-mode credential deliveries. Cross-reference to Section 4.9.

---

### DOC-129 — Fixed

**CHALLENGE:** The finding is correct. Two §16.5 alert entries (`DedicatedDNSUnavailable` and `DedicatedDNSDegraded`) reference "Section 14.3 (network policy)" but §14 contains only §14.1 (WorkspacePlan Schema Versioning). There is no §14.3. The network policy content relevant to dedicated CoreDNS is in §13.2 "Network Isolation".

**CHANGES:** Both alert entries updated to reference `Section 13.2 (network policy)` instead of `Section 14.3 (network policy)`.

---

### DOC-130 — Fixed (same fix as BLD-023)

**CHALLENGE:** The finding is correct. Phase 5.75 in §18 says "see Section 4.10" but §4 ends at §4.9. The `AuthEvaluator` is in §4.8 "Policy Engine and Admission Control".

**CHANGES:** Updated Phase 5.75 description to reference `Section 4.8` instead of `Section 4.10`.

---

### BLD-023 — Fixed

Same fix as DOC-130 above.

---

### DOC-131 — Fixed

**CHALLENGE:** The finding is correct. §13.2 says the `lenny-label-immutability` webhook "is listed as a check in the `lenny-preflight` Job (Section 18.1)" but §18 has no subsections. The `lenny-preflight` Job is documented in §17.6 "Packaging and Installation".

**CHANGES:** Updated the reference from `Section 18.1` to `Section 17.6`.

---

### BLD-024 — Fixed

**CHALLENGE:** The finding is correct. `GOVERNANCE.md` is referenced in four places with contradictory phases: §2 and §23.2 say Phase 2, §18 Phase 17a table and §19 say Phase 17a. The correct answer is both: drafted in Phase 2 (alongside `CONTRIBUTING.md` and `make run`), finalized in Phase 17a (alongside documentation and governance review). The contradiction is confusing for contributors reading the spec.

**CHANGES:** Updated §2 to clarify: `CONTRIBUTING.md` published in Phase 2; `GOVERNANCE.md` drafted in Phase 2 and finalized in Phase 17a. Updated §23.2 governance artifact description to the same split. Updated §19 from "ships in Phase 17a" to "drafted in Phase 2 and finalized in Phase 17a". §18 Phase 17a table row is already correct (it says "review and finalization").

---

### API-035 — Fixed (also covers PRT-026 overlap)

**CHALLENGE:** The finding is correct. `OUTPUTPART_INLINE_REF_CONFLICT` is used normatively at line 6655: "setting both is a validation error (`400 OUTPUTPART_INLINE_REF_CONFLICT`)". `INVALID_DELIVERY_VALUE` is used normatively at line 6824: "the gateway rejects unknown `delivery` values with `400 INVALID_DELIVERY_VALUE`". Both are client-facing codes absent from the catalog. A client implementer scanning the catalog for error handling will miss these.

**CHANGES:** Added both to the §15.1 error catalog after `BUDGET_EXHAUSTED`: `OUTPUTPART_INLINE_REF_CONFLICT` (PERMANENT/400, with inline vs ref exclusivity context), `INVALID_DELIVERY_VALUE` (PERMANENT/400, with valid delivery values noted).

---

### PRT-026 — Partially fixed (overlap with API-035 resolved; SDK_DEMOTION_NOT_SUPPORTED added)

**CHALLENGE:** `INVALID_DELIVERY_VALUE` and `OUTPUTPART_INLINE_REF_CONFLICT` are covered by API-035 fix above. `SDK_DEMOTION_NOT_SUPPORTED` is also normatively used: "the gateway fails the session with a clear error (`SDK_DEMOTION_NOT_SUPPORTED`)" — client-facing, warrants a catalog entry.

**CHANGES:** Added `SDK_DEMOTION_NOT_SUPPORTED` (PERMANENT/422) to the §15.1 error catalog alongside the API-035 additions.

---

### CMP-027 — Partially fixed

**CHALLENGE:** `COMPLIANCE_SIEM_REQUIRED` is already in the catalog from the POL-032 High fix. `COMPLIANCE_CROSS_USER_CACHE_PROHIBITED` is used normatively at §4.9: "rejected at pool registration time with `400 COMPLIANCE_CROSS_USER_CACHE_PROHIBITED`". It's a client-facing pool registration error that compliance operators need to handle.

**CHANGES:** Added `COMPLIANCE_CROSS_USER_CACHE_PROHIBITED` (POLICY/400) to the §15.1 error catalog. `COMPLIANCE_SIEM_REQUIRED` already added by POL-032 High fix — no additional action needed.

---

### CRD-023 — Skipped (challenged and rejected)

**CHALLENGE:** The finding claims `CREDENTIAL_MATERIALIZATION_ERROR` should be in the §15.1 catalog. However, the spec explicitly marks it "category: `INTERNAL`" and states it "surfaces to the client as `CREDENTIAL_POOL_EXHAUSTED`". The internal code is an implementation detail that never reaches clients — clients only see `CREDENTIAL_POOL_EXHAUSTED`. Adding an internal code to the client-facing catalog is incorrect: it would imply clients need to handle it, which they never will. The catalog is a client contract. This finding is rejected.

**STATUS:** Skipped — internal code, not client-facing. Clients receive `CREDENTIAL_POOL_EXHAUSTED`.

---

### DEL-026 — Already fixed by POL-032

**CHALLENGE:** All five codes (`INPUT_TOO_LARGE`, `CONTENT_POLICY_WEAKENING`, `CONTENT_POLICY_INTERCEPTOR_SUBSTITUTION`, `BUDGET_EXHAUSTED`, and the internal Lua codes clarification) were added to the catalog by the POL-032 High fix. No additional action needed.

**STATUS:** No action needed — POL-032 High fix covered all items.

---

### OBS-034 — Fixed

**CHALLENGE:** The finding is correct. `DualStoreUnavailable` is defined with explicit alert semantics in §10.1 ("Replicas emit a `dual_store_unavailable` metric…and fire alert `DualStoreUnavailable` immediately on detection") and cross-referenced at §12.4, but absent from §16.5. This is a platform-wide degradation signal — a Critical alert that operators must have in their PrometheusRules.

**CHANGES:** Added `DualStoreUnavailable` to the §16.5 Critical alerts table with condition, behavior description, and cross-reference to §10.1.

---

### OBS-035 — Fixed

**CHALLENGE:** All four alerts are defined with explicit conditions in prose. `WarmPoolBootstrapping` and `CRDSSAConflictStuck` are operationally important for understanding pool startup and controller health. `ExperimentTargetingCircuitOpen` and `PgAuditSinkDeliveryFailed` are Warning-level signals for experiment and compliance subsystems. All four are absent from §16.5, which is the canonical contract for Helm-shipped PrometheusRules. Operators implementing alerting from the spec will miss these.

**CHANGES:** Added all four to the §16.5 Warning alerts table with conditions extracted from their respective prose sections: `WarmPoolBootstrapping` (cross-ref §4.6.2, §17.7), `CRDSSAConflictStuck` (cross-ref §4.6), `ExperimentTargetingCircuitOpen` (cross-ref §10.7), `PgAuditSinkDeliveryFailed` (cross-ref §11.7).

---

### OBS-036 — Fixed

**CHALLENGE:** The finding is correct. The SLO table in §16.5 defines two distinct startup SLOs: P95 < 2s for runc and P95 < 5s for gVisor. The burn-rate table has `StartupLatencyBurnRate` only for runc. gVisor is the default isolation for multi-tenant deployments — the lack of a gVisor burn-rate alert means the most common production configuration has no automated SLO violation detection for startup latency.

**CHANGES:** Added `StartupLatencyGVisorBurnRate` to the §16.5 burn-rate table (P95 < 5s gVisor, 1h/14× fast, 6h/3× slow, matching the runc variant's pattern).

---

### CMP-028 — Fixed

**CHALLENGE:** The finding is correct. `DataResidencyWebhookUnavailable` is explicitly defined in §12.8: "Alert `DataResidencyWebhookUnavailable` fires when the webhook has been unreachable for more than 30 seconds." The webhook runs `failurePolicy: Fail` — outage blocks all operations on residency-constrained resources. This is Critical, and it was absent from §16.5.

**CHANGES:** Added `DataResidencyWebhookUnavailable` to the §16.5 Critical alerts table alongside `DualStoreUnavailable`.

---

### CMP-029 — Fixed

**CHALLENGE:** The finding is correct. The `DataResidencyViolationAttempt` audit event is described in §12.8 as "critical severity" — it represents a cross-border transfer attempt, which under data sovereignty regulations (GDPR, Schrems II) is a potential compliance incident, not a Warning. A Warning-grade alert would be triaged at lower urgency than a compliance incident warrants. The alert was previously in the Warning table.

**CHANGES:** Moved `DataResidencyViolationAttempt` from the §16.5 Warning table to the Critical table, with updated condition description noting it fires on the first occurrence and represents a cross-border transfer attempt.

---

### OPS-030 — Fixed

**CHALLENGE:** The finding is correct. §24.3 maps `set-warm-count` to `PATCH /v1/admin/pools/{name}` but §15.1 defines it as `PUT /v1/admin/pools/{name}/warm-count`. These are different HTTP method + path combinations. A wrong API mapping in lenny-ctl would cause 404s or method-not-allowed errors if the CLI used the §24.3 mapping.

**CHANGES:** Corrected §24.3 mapping for `set-warm-count` from `PATCH /v1/admin/pools/{name}` to `PUT /v1/admin/pools/{name}/warm-count`, matching §15.1.

---

### OPS-031 — Fixed

**CHALLENGE:** The finding is correct. §17.7 warm pool exhaustion runbook uses `lenny-ctl admin pools <name> set-warm-count --min <N+10>` (positional `<name>`) but §24.3 canonical form is `lenny-ctl admin pools set-warm-count --pool <name> --min <N>` (flag-based). A positional vs flag mismatch means the runbook command would fail during an incident.

**CHANGES:** Updated §17.7 runbook remediation step to use canonical §24.3 syntax: `lenny-ctl admin pools set-warm-count --pool <name> --min <N+10>`.

---

### OPS-032 — Fixed

**CHALLENGE:** The finding is correct. Both `GET /v1/admin/pools/{name}/sync-status` and `PUT /v1/admin/pools/{name}/circuit-breaker` are defined in §15.1 with full semantics, but have no corresponding lenny-ctl entries in §24.3. The `sync-status` endpoint is specifically called out in the `PoolConfigDrift` alert runbook context; operators will look in §24.3 and not find it. The `circuit-breaker` override is the only recovery path after SDK-warm circuit-breaker auto-disable — operators who follow runbook documentation to §24 will be stuck without this entry.

**CHANGES:** Added both entries to §24.3: `lenny-ctl admin pools sync-status --pool <name>` (maps to `GET /v1/admin/pools/{name}/sync-status`, with `PoolConfigDrift` alert context) and `lenny-ctl admin pools circuit-breaker --pool <name> --state <enabled|disabled|auto>` (maps to `PUT /v1/admin/pools/{name}/circuit-breaker`, with SDK-warm recovery context).

---

### API-036 — Fixed

**CHALLENGE:** The finding is correct. `PUT /v1/admin/external-adapters/{name}/validate` is normatively required (adapters in `pending_validation` state don't receive traffic; this is the only path to `active`) and explicitly referenced in §15.2.1, but absent from the §15.1 admin API table and from §24. Without the §15.1 entry, the endpoint doesn't formally exist in the API spec. Without a §24 entry, operators registering new adapters have no CLI path to activate them.

**CHANGES:** Added `PUT /v1/admin/external-adapters/{name}/validate` to the §15.1 admin table between the `PUT` update and `DELETE` entries, with full description of the compliance suite, status transitions, and cross-reference to §15.2.1. Added new §24.7 "External Adapter Management" section (renumbering old §24.7/§24.8/§24.9 to §24.8/§24.9/§24.10) with the `lenny-ctl admin external-adapters validate --name <name>` command.

---

### K8S-024 — Fixed

**CHALLENGE:** The finding is correct and the spec currently has a genuine asymmetry. Every other fail-closed admission webhook in the spec (`lenny-label-immutability`, cosign, `lenny-data-residency-validator`, OPA/Gatekeeper) explicitly states `replicas: 2`, `PodDisruptionBudget minAvailable: 1`, and a named availability alert. The `lenny-sandboxclaim-guard` webhook sits on the session-creation hot path with `failurePolicy: Fail` but has no HA spec. Would an implementor get this wrong? Yes — they would likely follow the pattern from other webhooks but have no spec guidance for this specific one, and might deploy single-replica. The fix is minimal: one sentence matching the pattern already established for every other webhook.

**CHANGES:** Added to §4.6.1 after the `lenny_sandboxclaim_guard_rejections_total` counter sentence: the webhook is deployed with `replicas: 2` and a `PodDisruptionBudget` (`minAvailable: 1`) matching the admission controller HA requirements in Section 17.2. Added `SandboxClaimGuardUnavailable` Critical alert to §16.5 (>30s unreachable, cross-ref §4.6.1). Added the webhook to the §17.6 preflight check table.

---

### NET-027 — Fixed

**CHALLENGE:** The finding is a genuine inconsistency: every other data-plane component (Redis, PgBouncer) has mandatory TLS with server-side enforcement specified. MinIO is referenced 40+ times in the spec as the store for checkpoints, workspace snapshots, and transcripts — yet its transport security is unspecified and the example URL uses `http://`. An implementor writing Helm config follows the examples; the example actively guides them to plaintext. SSE-KMS encrypts at rest, but data decrypted before transmission over plaintext http negates that. This is a genuine implementation divergence risk.

**CHANGES:** Added MinIO TLS requirement to §10.3: MinIO connections MUST use TLS (`https://`). Updated the §12.5 Helm example URL from `http://minio.lenny-system:9000` to `https://minio.lenny-system:9000`. Added `LENNY_MINIO_TLS_REQUIRED=true` enforcement note to §12.5 matching the Redis/PgBouncer pattern. Added dev-mode exception: TLS may be disabled in local development mode via `minio.tls.enabled: false` in Helm values.

---

### NET-028 — Fixed

**CHALLENGE:** The finding is correct. The NetworkPolicy table row for gateway egress to Token Service and the Token Service ingress row both say "mTLS port" with no numeric value. Every other row has a concrete port or a Helm template reference. An implementor rendering the NetworkPolicy manifest has no port number to use and cannot produce a working manifest. The spec uses port 50051 for adapter gRPC — the Token Service needs its own port.

**CHANGES:** Added `{{ .Values.tokenService.grpcPort }}` (default: 50052) to the gateway egress and Token Service ingress rows in the §13.2 NetworkPolicy component table. Added `tokenService.grpcPort: 50052` to the Helm values reference in §10.3.

---

### SCL-027 — Fixed

**CHALLENGE:** The finding is correct and impactful. The §12.4 Redis throughput table's preamble says "~1,000 concurrent sessions" but Tier 3 is explicitly 10,000. The ~650/s conclusion is 10× too low for the tier the section is analyzing. The conclusion "trivially safe on a single primary" is materially wrong at actual Tier 3 load and would lead deployers to under-provision Redis topology for Tier 3. This is not an edge-case concern — it directly misinforms the Sentinel-vs-Cluster decision for the most demanding deployment tier.

**CHANGES:** Changed §12.4 throughput table preamble from "~1,000 concurrent sessions" to "~10,000 concurrent sessions." Revised per-source write rates proportionally (×10). Updated conclusion: sustained ~6,000–6,500/s is within Tier 3 Redis Cluster topology capacity but NOT trivially safe on a single Sentinel primary — this reinforces the Cluster topology recommendation in §17.8 for Tier 3.

---

### SCL-028 — Fixed

**CHALLENGE:** The finding is correct. The `PodClaimQueueSaturated` alert condition references "50% of the pool's `maxConcurrent` session rate" but `maxConcurrent` is a concurrent-workspace mode field (slots per pod). For session-mode and task-mode pools — the dominant use cases — `maxConcurrent` is undefined. The alert condition is permanently broken for session-mode pools, which is the common case.

**CHANGES:** Replaced the alert condition in §16.5 `PodClaimQueueSaturated` row: old condition "50% of the pool's `maxConcurrent` session rate for > 30s" replaced with "`lenny_pod_claim_queue_depth > 0.25 × pool.minWarm for > 30s AND lenny_warmpool_idle_pods > 0`" — this is meaningful for all pool types and indicates queue build-up despite available warm pods.

---

### SEC-038 — Fixed

**CHALLENGE:** The finding is correct. `callbackUrl` in §14 has comprehensive documented SSRF mitigations (HTTPS-only, DNS pinning, private IP rejection, isolated worker, optional domain allowlist). The A2A `pushNotification.url` stored in `OutboundSubscription.CallbackURL` (§15) and POSTed to directly has none of these documented. An authenticated A2A caller could target internal cluster IPs. A2A is post-v1 in protocol terms but the `OutboundChannel` interface is in the current spec with no SSRF note. A single sentence in §21 noting the requirement is sufficient and prevents implementors from treating it as an oversight.

**CHANGES:** Added a note in §21 (A2A Adapter) under `OutboundChannel.Send`: "The `pushNotification.url` stored in `OutboundSubscription.CallbackURL` MUST pass the same SSRF validation as `callbackUrl` (Section 14) — HTTPS-only, private IP rejection, DNS pinning, and optional domain allowlist — before being stored at task creation time. The A2A adapter MUST validate the URL at `OpenOutboundChannel` time, not at delivery time, and reject task registration with `400 INVALID_CALLBACK_URL` if validation fails."

---

### CMP-030 — Fixed

**CHALLENGE:** The finding is correct. §12.8 says `POST /v1/sessions` is rejected when `processing_restricted: true`, but does not mention `POST /v1/sessions/start` — the convenience endpoint that creates, uploads files, and starts a session in one call. Both endpoints create sessions. A GDPR Article 18 restriction that silently bypasses one session creation path is a genuine compliance defect, not documentation polish.

**CHANGES:** Updated the §12.8 processing restriction paragraph to replace "`POST /v1/sessions` is rejected" with "all session creation endpoints (`POST /v1/sessions` and `POST /v1/sessions/start`) are rejected." Added the same note to the `ERASURE_IN_PROGRESS` error catalog entry in §15.1.

---

### CMP-031 — Fixed

**CHALLENGE:** The finding is correct. Legal holds can be set and cleared via `POST /v1/admin/legal-hold` but there is no enumeration endpoint. Legal teams cannot audit current holds without direct database access. The omission is particularly notable because the spec already defines role permissions for "set/release legal holds" in the RBAC table — reads/audits are a natural complement. Without enumeration, the RBAC table's hold management capability is half-specified.

**CHANGES:** Added `GET /v1/admin/legal-holds` to §15.1 admin API table: returns paginated list of active legal holds with `?tenant_id=`, `?resource_type=session|artifact`, and `?resource_id=` query parameters. Response includes `resourceType`, `resourceId`, `setBy`, `setAt`, `note`. Requires `platform-admin` or `tenant-admin` (tenant-admin scoped to own tenant). Cross-reference §12.8.

---

### PRT-024 — Fixed

**CHALLENGE:** The finding is correct and is a genuine implementation blocker. `ExternalProtocolAdapter.Capabilities()` returns `AdapterCapabilities` but no struct definition exists anywhere in the 8,500-line spec. `OutboundCapabilitySet` is defined (struct with fields). `AdapterCapabilities` is declared as a return type on the required interface method but has no fields. An adapter author implementing the interface must return a zero value or guess fields — with no definition, they cannot know what `BaseAdapter.Capabilities()` returns either. This is unlike `OutboundCapabilitySet` which has a full definition. One-struct fix.

**CHANGES:** Added `AdapterCapabilities` struct definition immediately below the `ExternalProtocolAdapter` interface in §15: fields `PathPrefix` (string — URL path prefix this adapter owns), `Protocol` (string — protocol identifier, e.g., "mcp", "a2a", "openai-completions"), `SupportsSessionContinuity` (bool), `SupportsDelegation` (bool), `SupportsElicitation` (bool), `SupportsInterrupt` (bool). Documented that `BaseAdapter.Capabilities()` returns the zero value with `PathPrefix` and `Protocol` populated from the adapter's registration.

---

### PRT-025 — Fixed

**CHALLENGE:** The finding is correct. The spec says "the gateway emits a `mcp_version_deprecated` warning header" — this is an underscore-named HTTP header, violating RFC 7230. nginx drops headers with underscores by default (`underscores_in_headers off`). Every other custom header in the spec uses hyphens (`X-Lenny-Signature`, `X-Lenny-Session-Id`). An implementor reading the spec would produce a header that proxies silently discard. One-word fix.

**CHANGES:** Renamed `mcp_version_deprecated` to `X-Lenny-Mcp-Version-Deprecated` in §15.2 (compatibility policy paragraph). Updated all other references to this header name.

---

### PRT-027 — Fixed

**CHALLENGE:** The finding is correct. §15.5 (schema versioning) says "the adapter advertises a protocol version at INIT; the gateway selects a compatible version. Major version changes are breaking." §15.4.2 defines the `INIT → READY → ACTIVE` lifecycle but says nothing about which field carries the version, what format it uses, or what the gateway does on mismatch. An implementor reading §15.4 as the "authoritative interim reference" has no way to implement version negotiation. The existing §15.4.2 INIT table has a description "Adapter process starts, opens gRPC connection to gateway (mTLS), writes placeholder manifest" — version exchange belongs here.

**CHANGES:** Added to §15.4.2 INIT state description: the adapter sends an `AdapterInit` message on the control gRPC stream with `adapterProtocolVersion` (string, semver format, e.g., `"1.0.0"`). The gateway responds with `AdapterInitAck` carrying `selectedVersion` (the highest compatible version the gateway supports) or closes the stream with `PROTOCOL_VERSION_INCOMPATIBLE` if no compatible version exists. Major version changes are breaking; minor and patch versions are backwards compatible. Current protocol version: `"1.0.0"`.

---

### API-037 — Fixed

**CHALLENGE:** The finding is correct. `PUT /v1/credentials/{credential_ref}` (rotate) and `POST /v1/credentials/{credential_ref}/revoke` are fully specified in §4.9 with normative semantics — both carry secret material and trigger lease invalidation. They are absent from the §15.1 credential management table, which currently only has `POST /v1/credentials` and `DELETE /v1/credentials/{ref}`. The §15.1 table is the canonical API surface; its completeness matters for client library authors scanning it.

**CHANGES:** Added `PUT /v1/credentials/{credential_ref}` and `POST /v1/credentials/{credential_ref}/revoke` to the §15.1 user credential management section, with descriptions matching §4.9 prose (rotate replaces secret material, revoke marks as revoked and terminates active leases).

---

### SCH-031 — Fixed

**CHALLENGE:** The finding is correct. Iter4's fix added a WARN log for unannotated tools inferred as `admin`, but the `capabilityInferenceMode` field that controls the default was never added to `RuntimeDefinition`. The WARN log helps operators detect the issue post-deployment but does not give them a configuration lever to suppress it in permissive environments where `admin`-defaulting is undesirable. Third-party unannotated tools that work fine in other environments silently fail with `TOOL_CAPABILITY_DENIED` when assigned to non-admin pools. The field is referenced in at least two other spec locations implicitly.

**CHANGES:** Added `capabilityInferenceMode` field (`strict` | `permissive`, default: `strict`) to `RuntimeDefinition` schema in §5.1. In `strict` mode (default), unannotated tools infer as `admin` and emit the existing WARN log. In `permissive` mode, unannotated tools infer as `write` — enabling third-party runtimes without forcing admin pool assignment. Note: `permissive` mode does not affect tools with explicit `toolCapabilityOverrides` entries.

---

### SCH-034 — Fixed

**CHALLENGE:** The finding is correct. The `data` field in the webhook payload is documented only for `session.completed` but five other event types (`session.started`, `session.failed`, `session.cancelled`, `session.expired`, `session.checkpoint`) exist. The `callbackSecret` field is absent from the WorkspacePlan JSON example even though the text describes it. The `X-Lenny-Signature` HMAC signing is described in name only with no format. All three gaps would cause implementors to produce incorrect webhook payload handling or signature verification.

**CHANGES:** In §14 (Webhook), added per-event `data` schema documentation for all six event types. Added `callbackSecret` to the WorkspacePlan JSON example. Specified `X-Lenny-Signature` format: `HMAC-SHA256(callbackSecret, "<timestamp>.<raw_body_bytes>")` with a 5-minute replay window (timestamp included in the header as `t=<unix_seconds>,v1=<hex_signature>`).

---

### SCH-035 — Fixed

**CHALLENGE:** The finding is correct. The §11.2.1 BillingEvent schema has conditional fields annotated "(for X events only)" with no null/absent contract — readers cannot distinguish "field is absent" from "field is present as null" from "field is zero." `corrects_sequence` typed as `uint64` has no sentinel for the absent case (0 is a valid sequence number). Analytics consumers writing portable Parquet/Arrow readers cannot produce correct schemas without this contract.

**CHANGES:** Added a null/absent policy note to §11.2.1: fields annotated "(for X events only)" MUST be omitted from the JSON payload for other event types (not present, not null). Consumers MUST treat absent fields as "not applicable" for the event type. Changed `corrects_sequence` type from `uint64` to `uint64 | null` with note: `null` (absent) means the event is not a correction; `0` is not a valid sequence number (sequences start at 1).

---

### SLC-009 — Fixed

**CHALLENGE:** The finding is correct. When `onCleanupFailure: warn` causes a pod to return to pool with a `scrub_warning` annotation, workspace materialization for the next session adds new files but the spec never says `/workspace/current` is cleared first. Prior task files may persist — this is a genuine data leak vector between tasks. An implementor following the existing §6.2 materialization prose would not know to add the clearing step. One normative sentence closes this.

**CHANGES:** Added to §6.2 workspace materialization under pod-warm path: "Before materializing workspace contents onto a pod, the adapter MUST remove all files from `/workspace/current` to prevent residual state from prior tasks. This applies regardless of whether the pod has a `scrub_warning` annotation."

---

### SLC-011 — Skipped

**CHALLENGE:** The finding proposes adding `activeConnectorsOnSource` to the derive response. §4.5 already explicitly documents that connector tokens are not inherited: "Connector OAuth tokens and authorization state are not inherited. The derived session starts with no active connector tokens. If the derived session's runtime requires connector access, the client must complete the connector authorization flow independently." The spec is workable: clients reading §4.5 know connectors are not inherited. The proposed field adds convenience but does not fix an ambiguity or prevent an implementation error. Skipped as "nice-to-have, not required."

**STATUS:** Skipped — §4.5 already explicitly states connector tokens are not inherited; clients building derive workflows have sufficient guidance.

---

### SLC-030 — Fixed

**CHALLENGE:** The finding is correct. The `maxSessionAge` timer has a complete per-state behavior table in §6.2 (added in SLC-007 fix). The `maxIdleTimeSeconds` timer has no equivalent: no definition of "idle", no per-state behavior, no persistence mechanism, no expiry transition. An implementor must guess all of these. With a per-state table already established for `maxSessionAge`, the absence of the equivalent for `maxIdleTimeSeconds` is a genuine implementation divergence risk — the two timers interact and both affect session lifetime.

**CHANGES:** Added a `maxIdleTimeSeconds` per-state behavior table to §6.2 immediately after the `maxSessionAge` table. Definition: "idle" is defined as no `agent_output` event and no `tool_use` event emitted since `last_agent_activity_at`. Per-state: Running (timer active, resets on any agent output or tool_use); input_required (timer paused — agent is blocked, not idle); suspended (timer paused — agent is deliberately halted); resume_pending/resuming (timer paused — no agent activity possible); awaiting_client_action (timer paused); terminal (timer stopped). Persistence: `last_agent_activity_at` timestamp updated in Postgres on each qualifying event. Expiry: fires `expired` transition identically to `maxSessionAge` expiry.

---

### DEL-027 — Fixed

**CHALLENGE:** The finding is correct. `approvalMode: approval` is a one-line stub: "Gateway pauses parent, surfaces delegation request to client for approval." No API endpoint, no timeout, no denial error code, no concurrency handling. An implementor would have no path to implement this mode and might attempt to invent one. However, a minimal fix is available: mark the mode as reserved/not-implemented for v1 with a note that full specification is deferred. This prevents implementors from attempting to build against incomplete spec while preserving the field's existence.

**CHANGES:** Updated §8.4 `approval` mode description: "Reserved — not implemented in v1. The `approval` value is accepted at policy registration time to prevent future compatibility breaks, but the gateway treats it identically to `elicitation` mode in v1. Full implementation (dedicated approval API endpoint, approval timeout, concurrent-request handling, and denial error code) is deferred to post-v1. Deployers must not rely on `approval` providing distinct behavior from `elicitation` in v1."

---

### DEL-028 — Fixed

**CHALLENGE:** The finding is correct. `snapshotPolicyAtLease` is well-specified in §8.3 prose (behavior, semantics, gateway snapshot mechanism) but absent from the normative `DelegationPolicy` YAML example. Deployers writing policy YAML from the example alone — which is the primary path for non-expert deployers — will never discover this field. The fix is trivial: one YAML line.

**CHANGES:** Added `snapshotPolicyAtLease: false  # true: snapshot matching pools at lease issuance for stable tree behavior` to the DelegationPolicy YAML example in §8.3.

---

### MSG-028 — Fixed

**CHALLENGE:** The finding is correct. §7.2 has an inline delivery receipt schema with `status: "delivered|queued|dropped|error"` and fields `targetState`, `queueTTL`. §15.4 (canonical schema) has `status: "delivered|queued|dropped|expired|rate_limited"` and fields `reason`, `deliveredAt`, `queueDepth`. These are substantively different. An implementor reading §7.2 for the delivery receipt schema would produce a different implementation than one reading §15.4. The §7.2 schema is a stale stub from an earlier iteration. The §15.4 schema (at line 6836) is the current authoritative version.

**CHANGES:** Replaced the §7.2 inline delivery receipt JSON snippet and status-value list with a single sentence: "The `deliveryReceipt` schema is defined in Section 15.4 (`delivery_receipt` acknowledgement schema). The `status` values are: `delivered`, `queued`, `dropped`, `expired`, `rate_limited`." The detailed schema definition remains in §15.4 as the single authoritative source.

---

### MSG-029 — Fixed

**CHALLENGE:** The finding is correct. The `DUPLICATE_MESSAGE_ID` error says "a message with the same ID was received within the deduplication window" but no window duration, backing store, TTL, or config name is defined. An implementor must choose a window arbitrarily. The Redis key pattern is also undefined. Given the platform's established Redis key prefix convention, this is a genuine implementation divergence risk.

**CHANGES:** Added to §7.2 below the `DUPLICATE_MESSAGE_ID` description: "Deduplication is session-scoped. The gateway stores seen message IDs in a Redis sorted set (`t:{tenant_id}:session:{session_id}:msg_dedup`, scored by receipt timestamp). IDs are retained for `deduplicationWindowSeconds` (default: 3600s, configurable per deployment via `messaging.deduplicationWindowSeconds` in Helm values). The set is trimmed on write to remove entries older than the window."

---

### MSG-030 — Fixed

**CHALLENGE:** The finding is correct. The §7.2 delivery path table covers six states but has no rows for pre-running states (`created`, `ready`, `starting`, `finalizing`). A message sent to a session in `starting` state has completely undefined behavior. Implementors cannot know whether to buffer, reject, or error. The session delivery table is presented as comprehensive; its gap here is a genuine specification hole.

**CHANGES:** Added pre-running state rows to §7.2 delivery path table: `created`, `ready`, `starting` → "Message is rejected with `TARGET_NOT_READY` — session has not yet entered `running` state and has no inbox. Client should retry after the session transitions to `running`." `finalizing` → "Message is rejected with `TARGET_TERMINAL` — session is transitioning to a completed terminal state."

---

### MSG-031 — Fixed

**CHALLENGE:** The finding is correct. `request_input_expired` has a normative JSON schema in §8.8 and §11.3. `deadlock_detected` — defined in the same §8.8 section — is described only in prose: "carrying the list of blocked `requestId` values and their originating task IDs." Parent agents implementing `lenny/await_children` handlers need a schema to deserialize this event. The `request_input_expired` schema precedent makes the absence of `deadlock_detected`'s schema conspicuous and creates implementation divergence.

**CHANGES:** Added normative JSON schema for `deadlock_detected` in §8.8 immediately after the prose description: `{ "type": "deadlock_detected", "deadlockedSubtreeRoot": "<session_id>", "blockedRequests": [{ "requestId": "<id>", "taskId": "<session_id>", "blockedSince": "<ISO8601>" }], "detectedAt": "<ISO8601>", "willTimeoutAt": "<ISO8601>" }`. `willTimeoutAt` is `detectedAt + maxDeadlockWaitSeconds`.

---

### TNT-023 — Fixed

**CHALLENGE:** The finding is correct. The spec says `billing_seq_{tenant_id}` is a Postgres sequence and `nextval('billing_seq_{tenant_id}')` is called on every billing INSERT — but there is no documentation of when or how this sequence is created. A missing sequence causes a fatal error (`nextval: relation "billing_seq_X" does not exist`) on the first billing event for any newly provisioned tenant. Implementors writing the tenant provisioning handler have no spec guidance to create it.

**CHANGES:** Added to the `POST /v1/admin/tenants` handler description in §15.1: "Tenant provisioning creates the per-tenant Postgres billing sequence: `CREATE SEQUENCE IF NOT EXISTS billing_seq_{tenant_id} START WITH 1 INCREMENT BY 1 NO CYCLE`." Added to §12.8 tenant deletion lifecycle: the tenant deletion controller drops the sequence as part of Phase 4 (data deletion): `DROP SEQUENCE IF EXISTS billing_seq_{tenant_id}`.

---

### TNT-024 — Skipped

**CHALLENGE:** This is the third iteration of the same concern. The spec already has: (1) a mandatory `(tenant_id, query_embedding, model, provider)` cache key contract; (2) an explicit `cacheScope: per-user` default; (3) a `COMPLIANCE_CROSS_USER_CACHE_PROHIBITED` enforcement gate; (4) a required integration test `TestSemanticCacheTenantIsolation`; (5) `ArtifactStore` enforces tenant-prefix validation at the interface level as a model. The finding requests a structural gateway wrapper enforcing key prefixing, but the current design places enforcement in the `SemanticCache` implementation contract. A wrapper is one valid enforcement architecture; it is not the only one. After three iterations this concern has been implicitly accepted as contract-based enforcement. Adding a wrapper is a design change, not a spec clarification.

**STATUS:** Skipped (carry-forward, third iteration) — contract-based enforcement with integration test is specified; wrapper architecture is a design choice, not a gap.

---

### STR-026 — Skipped

**CHALLENGE:** Same as TNT-024 — this is the same concern from a different perspective, also its third iteration. The spec's enforcement model is contract-based with an integration test. The wrapper is an implementation pattern, not a spec requirement that's currently absent.

**STATUS:** Skipped (carry-forward, third iteration, duplicate of TNT-024) — same enforcement contract applies.

---

### STR-027 — Fixed

**CHALLENGE:** The finding is correct. §12.5 specifies MinIO bucket versioning as enabled but does not specify a delete-marker lifecycle policy. At Tier 3 scale with ~10,000 concurrent sessions and regular checkpoint rotation (each generating a delete marker on the old version), accumulated delete markers degrade `ListObjects` performance continuously — this is a known MinIO/S3 operational issue. The spec is detailed about other MinIO concerns (atomicity, quota enforcement, GC) but silent on delete-marker accumulation. An operator following the spec would have no prompt to add this rule.

**CHANGES:** Added to §12.5 (Artifact Retention Policy): "MinIO bucket versioning is enabled (required for `NoncurrentVersionExpiration`). A lifecycle rule MUST be configured on the checkpoints prefix (`/{tenant_id}/checkpoints/`) to expire delete markers after 24 hours and to expire noncurrent versions after 1 day (`NoncurrentVersionExpiration: Days: 1`). This prevents delete-marker accumulation from degrading `ListObjects` performance at scale. The Helm chart configures this rule via MinIO's `mc ilm add` in the post-install Job."

---

### WPL-008 — Fixed

**CHALLENGE:** The finding is correct. The spec says the PDB can be configured with `minAvailable` set to the pool's `minWarm`. When a pool's current idle pod count equals `minWarm` (the steady-state condition), `minAvailable = minWarm` means PDB allows zero evictions — every pod is "needed" to maintain the minimum. Node drains stall indefinitely. This is a genuine operational defect: the most common pool state is exactly the state where the PDB blocks all voluntary disruptions.

**CHANGES:** Updated §4.6.1 PDB description: "The PDB for warm pools MUST use `maxUnavailable: 1` rather than `minAvailable: minWarm`. `maxUnavailable: 1` allows drain operations to proceed one pod at a time while limiting simultaneous disruption, avoiding the deadlock where `minAvailable = current_idle_count` blocks all evictions. When a warm pod is evicted, the WarmPoolController proactively creates a replacement pod immediately to restore `minWarm`."

---

### WPL-009 — Fixed

**CHALLENGE:** The finding is correct. `ConfigureWorkspace` RPC appears in the SDK-warm path (step: "send ConfigureWorkspace to point it at finalized cwd") but has no timeout, failure mode, idempotency, or state transition on failure documented. An implementor writing the SDK-warm materialization path has no spec guidance for what to do if ConfigureWorkspace hangs or fails.

**CHANGES:** Added to §4.7 (or §6.1 as appropriate) under ConfigureWorkspace: "Timeout: 10 seconds. Idempotent: yes (calling with the same `cwd` path twice is safe). On failure: the gateway transitions the pod to `pod-warm` mode by calling `DemoteSDK` and materializing workspace via the standard pod-warm path. If `DemoteSDK` also fails within 5 seconds, the pod transitions to `failed` and a replacement is claimed from the pool."

---

### WPL-012 — Fixed

**CHALLENGE:** The finding is correct. The `WarmPoolReplenishmentSlow` alert condition says "exceeds 2× the pool's configured `pod_warmup_seconds` baseline" but `pod_warmup_seconds` is not a field on `SandboxWarmPool` or any pool CRD. The alert's comparison baseline is a referenced-but-undefined config value. Operators cannot set it.

**CHANGES:** Added `scalingPolicy.podWarmupSecondsBaseline` (integer, default: 30) to the `SandboxWarmPool` spec schema in §5.1. Updated §16.5 `WarmPoolReplenishmentSlow` alert to reference `scalingPolicy.podWarmupSecondsBaseline`. Updated §4.6.2 minWarm formula comment to note this field is used as the `pod_warmup_seconds` variable.

---

### WPL-024 — Fixed

**CHALLENGE:** The finding is correct. The spec describes `sdk_connecting → idle` (success) and `sdk_connecting → failed` (SIGTERM) transitions, but has no timeout for a hung `sdk_connecting` state. An SDK process that hangs during connection establishment leaves the pod stuck in `sdk_connecting` indefinitely — it appears warm (in the pool state machine) but is never claimed, consuming a warm slot. No timeout, no alert, and no metric for this failure mode. Would an implementor get this wrong? Yes — they would likely rely on the general pod health probe but the liveness probe passes while the adapter is alive even if the SDK connection is hung.

**CHANGES:** Added to §6.1/§6.2 sdk_connecting state: "`sdkConnectTimeoutSeconds` (default: 60s, configurable per pool). If the SDK does not complete its connection and transition to `idle` within this timeout, the WarmPoolController transitions the pod to `failed` and increments `lenny_warmpool_sdk_connect_timeout_total` (counter, labeled by `pool`). Added `SDKConnectTimeout` Warning alert to §16.5 (fires when `lenny_warmpool_sdk_connect_timeout_total` rate > 0.1/min for > 5 min on the same pool).

---

### CRD-024 — Fixed

**CHALLENGE:** The finding is correct. The §17.7 emergency credential revocation runbook says "re-enabling the credential ID" post-rotation but no admin endpoint exists to un-revoke a credential. `POST /v1/admin/credentials/{id}/revoke` marks the credential `revoked` in `CredentialPoolStore`, and the spec never says revocation is permanent or provides a re-enable path. The runbook references an action with no spec backing. An operator following the runbook cannot complete the recovery procedure.

**CHANGES:** Added `PUT /v1/admin/credentials/{pool_id}/{credential_id}/re-enable` to §15.1 admin credentials table: re-enables a previously revoked pool credential. Requires `platform-admin`. Body: optional `reason`. Emits `credential.re_enabled` audit event. Note: revocation is not permanent; re-enabling restores the credential to `healthy` status with a fresh health score. Added to §17.7 runbook: after rotation completes, the old credential may be re-enabled via this endpoint if it was revoked for emergency rotation purposes.

---

### EXM-003 — Fixed

**CHALLENGE:** The finding is correct and is a carry-forward that deserves resolution. The connection-loss recovery spec (§10.4) describes coordinator-loss for single-session pods: session transitions to `resume_pending`, new coordinator claims a pod, replays from checkpoint. Multi-slot concurrent-workspace pods have a fundamentally different topology: multiple active slots, a single pod connection. If the coordinator loses connection to a concurrent-workspace pod, should all slots enter hold simultaneously? What happens to the active slot counter in Redis? The spec is silent. An implementor building concurrent-workspace recovery would be forced to guess.

**CHANGES:** Added "Concurrent-workspace pod connection loss" subsection to §10.4: When the gateway loses connection to a concurrent-workspace pod, all active slots on that pod simultaneously enter the `resume_pending` state. The whole-pod replacement trigger (Section 5.2: fires when `ceil(maxConcurrent / 2)` or more slots fail) is also triggered immediately on total connection loss, regardless of the per-slot failure count. The gateway atomically resets the Redis slot counter (`lenny:pod:{pod_id}:active_slots` → 0) and rehydrates it from `SessionStore.GetActiveSlotsByPod(pod_id)` on the new pod's first slot allocation after recovery.

---

### EXM-004 — Skipped

**CHALLENGE:** The finding says concurrent-workspace mode's cross-slot residual state enumeration (4 items) is less complete than task-mode's (8+ items). This is an architectural completeness concern: concurrent-mode has worse isolation (simultaneous) than task-mode (sequential) but a shorter residual state list. However, the concurrent-workspace mode is an advanced deployer opt-in with explicit isolation warnings in the spec. Task-mode's longer list reflects its more common use case. Adding 4 more items (procfs, kill(2), IPC namespace, timing channels) is supplementary documentation — an implementor building concurrent-workspace already receives the gVisor/Kata runtime recommendation which provides much stronger isolation than the enumeration itself. This is a completeness gap, not an ambiguity that causes divergent implementations.

**STATUS:** Skipped — documentation completeness gap in an advanced opt-in mode; gVisor/Kata isolation recommendation is the operative guidance.

---

### EXM-005 — Fixed

**CHALLENGE:** The finding is correct. The spec says graph-aware runtimes "optionally emit trace spans via the observability protocol" but no mechanism is specified — no MCP tool, no adapter RPC, no trace context field in the manifest. An implementor taking this at face value has nowhere to start. The claim is either actionable (needs a spec) or should be removed. Leaving it as an unspecified "optional" capability creates a false expectation.

**CHANGES:** Updated §5.2 graph-aware runtime note: "Graph-aware runtime trace span emission is deferred to post-v1. In v1, runtimes may emit OpenTelemetry spans using their own OTel SDK configured against the platform's OTLP collector endpoint (injected in the manifest as `observability.otlpEndpoint`). Lenny does not define a dedicated span emission tool or RPC in v1 — runtimes use standard OTLP libraries directly."

---

### EXM-006 — Fixed

**CHALLENGE:** The finding is correct. The spec says task-mode generates a fresh manifest per task, which implies per-task credential assignment, but this is never confirmed. The difference matters significantly: per-task credentials mean each task gets a fresh lease (short-lived, scoped), which changes pool capacity consumption, rotation behavior, and lease TTL semantics compared to per-pod credentials that persist across tasks.

**CHANGES:** Added to §5.2 task-mode section: "Credential lease lifecycle in task mode: credentials are leased per-task, not per-pod. A fresh credential assignment (`AssignCredentials` RPC) is performed at each task dispatch — the pod does not retain credentials between tasks. The adapter manifest is regenerated per task (as specified in §4.7), and `credentials.json` is rewritten with the new lease before the runtime binary is spawned. The previous lease is revoked when the task completes or the runtime exits. Per-task leasing aligns with the single-use-pod model: each task dispatch is semantically a fresh session from the credential perspective."

---

### EXM-027 — Skipped

**CHALLENGE:** The finding proposes adding `maxRequestInputWait` to `taskPolicy`. However, §11.3 already defines `maxRequestInputWaitSeconds` as a pool-level field (`runtime.maxRequestInputWaitSeconds`, default: 600s, "configurable per pool in the `limits:` block of the RuntimeDefinition"). A 600s default for inter-agent `request_input` in task mode is reasonable — task mode is designed for multi-agent trees where agent-to-agent input requests are expected. The concern that task mode pods are "held indefinitely" is not accurate: `maxRequestInputWaitSeconds` (600s) bounds the wait, `maxSessionAge` bounds the total session, and both are pool-configurable. The finding asks for a `taskPolicy`-specific field but the existing pool-level field already provides the lever. No implementation divergence possible.

**STATUS:** Skipped — `maxRequestInputWaitSeconds` (§11.3, default: 600s) already bounds task-mode `input_required` waits at the pool level.

---

### BLD-025 — Already fixed by OPS-030 and OPS-031

**CHALLENGE:** BLD-025 covers the same two issues as OPS-030 (§24.3 maps to wrong endpoint) and OPS-031 (runbook uses wrong syntax). Both were fixed by those dispositions above.

**STATUS:** No additional action needed — covered by OPS-030 and OPS-031 fixes.

---

### FLR-023 — Fixed

**CHALLENGE:** The finding is correct. There is a genuine crash window in the §12.8 erasure procedure between transaction commit (salt deleted, PII pseudonymized) and the verification step. If the gateway crashes after commit but before writing the completion receipt, the erasure job has no `phase` field to distinguish "completed successfully but receipt not written" from "failed mid-erasure." On restart, the job cannot safely determine whether to re-run (risking double-pseudonymization with a new salt) or skip (risking leaving the job in failed state). This is a real recovery ambiguity, not a theoretical edge case.

**CHANGES:** Added a `phase` field (enum: `initiated | store_deleting | pseudonymizing | verifying | completed | failed`) to the erasure job record in §12.8. On resume, the controller uses `phase` to determine the safe recovery path: `store_deleting`/`pseudonymizing` → re-run from the beginning (idempotent with UPSERT semantics); `verifying` → re-run only the verification step; `completed` → emit receipt and exit. The phase is persisted in Postgres in the same transaction as each phase's work.

---

### FLR-024 — Fixed

**CHALLENGE:** The finding is correct. PostgreSQL's `DISABLE TRIGGER` sets `pg_trigger.tgenabled = 'D'` — the trigger row remains but fires nothing. The spec's startup check queries `pg_trigger` for existence but not `tgenabled`. A superuser who disables the `lenny_tenant_guard` trigger for maintenance and forgets to re-enable it would pass all startup checks while RLS second-layer defense is silently inactive. This is a genuine security gap: the check that's meant to protect against a misconfigured guard cannot detect a disabled-but-present trigger.

**CHANGES:** Updated the §12.3 tenant guard check and the §17.6 preflight Job check to verify `tgenabled != 'D'` in addition to existence: `SELECT tgenabled FROM pg_trigger WHERE tgname = 'lenny_tenant_guard' AND tgrelid = '<table>'::regclass` — both the existence and the `tgenabled` field must be validated. On failure (disabled trigger detected), the same fatal error path applies as for absent trigger.

---

### FLR-025 — Fixed

**CHALLENGE:** The finding is correct. `checkpointBarrierAckTimeoutSeconds` (default: 45s) can be shorter than the Tier 3 workspace checkpoint cap (90s). A pod legitimately uploading a 512MB workspace within its 90s cap could be declared unresponsive at 45s. No metric distinguishes legitimate slow upload from a hung pod. The spec's CRD validation ensures `terminationGracePeriodSeconds` is large enough but does not ensure `checkpointBarrierAckTimeoutSeconds >= max_tiered_checkpoint_cap`.

**CHANGES:** Added CRD validation rule to `SandboxWarmPool`: `checkpointBarrierAckTimeoutSeconds` MUST be ≥ `max_tiered_checkpoint_cap` for the pool's `workspaceSizeLimitBytes`. Rejection error: `422 INVALID_POOL_CONFIGURATION` with message "checkpointBarrierAckTimeoutSeconds must be >= max_tiered_checkpoint_cap for the configured workspaceSizeLimitBytes." Added `lenny_checkpoint_barrier_ack_timeout_total` counter (labeled by `pool`) incremented when a pod exceeds the timeout.

---

### FLR-026 — Fixed

**CHALLENGE:** The finding is correct. When a session transitions to a terminal state (`expired`, `failed`, etc.), DLQ entries with remaining TTL are silently abandoned. Senders who sent messages to a recovering session and are waiting on delivery confirmation via the `message_expired` notification mechanism receive no notification when the session terminates — they are held in limbo for up to `maxResumeWindowSeconds` (900s). The spec defines `message_expired` notifications for TTL expiry but not for session terminal transitions.

**CHANGES:** Added to §7.2 (terminal state cascade logic) and §12.8: "On any terminal state transition (`completed`, `failed`, `cancelled`, `expired`), the gateway drains the session's DLQ by sending `message_expired` delivery receipts to all registered senders for each queued DLQ entry, with `reason: 'session_terminal'`. The DLQ Redis key is then deleted. This ensures senders are not held waiting for a session that will never resume."

---

### EXP-022 — Fixed

**CHALLENGE:** The finding is correct. The pagination cursor for the Results API encodes `{"last_id":"abc123"}` base64 — this leaks submission ordering (and thus relative volumes) across variants to clients who decode the cursor. The spec's general cursor documentation (§15.1) says "cursors are opaque, URL-safe strings" and "clients must not parse or construct cursors" but this is a documentation norm, not enforcement. The Results API cursor should use an encrypted/opaque form.

**CHANGES:** Added to §10.7 Results API: "Cursors in the Results API MUST use the platform-standard opaque cursor encoding (Section 15.1) — a deterministically encrypted form that does not expose primary key values or submission ordering. Implementations MUST NOT use plain base64-encoded JSON as the cursor format."

---

### EXP-023 — Fixed

**CHALLENGE:** The finding is correct. `lenny_experiment_targeting_circuit_open`, `lenny_experiment_sticky_cache_invalidations_total`, and `ExperimentTargetingCircuitOpen` alert are defined in §10.7 prose but absent from the canonical §16.1 metrics table and §16.5 alert table. The circuit-open metric is referenced in the `ExperimentTargetingCircuitOpen` alert condition — the alert is broken without the metric in the canonical table.

**CHANGES:** Added `lenny_experiment_targeting_circuit_open` (gauge, labeled by `tenant_id`, `provider`) and `lenny_experiment_sticky_cache_invalidations_total` (counter, labeled by `experiment_id`, `transition`) to §16.1. Added `ExperimentTargetingCircuitOpen` Warning alert to §16.5 (condition: `lenny_experiment_targeting_circuit_open > 0`, cross-ref §10.7).

---

### EXP-024 — Fixed

**CHALLENGE:** The finding is correct. The manual rollback trigger table in §10.7 references `lenny_session_error_total`, `lenny_session_total`, and `lenny_eval_score` — but none of these appear in §16.1. Operators writing PrometheusRules from the spec would get silent evaluation failures (metric not found). The metrics are used normatively in rollback triggers, which operators are expected to configure.

**CHANGES:** Added `lenny_session_error_total` (counter, labeled by `tenant_id`, `session_type`, `variant_id`), `lenny_session_total` (counter, labeled by `tenant_id`, `session_type`, `variant_id`), and `lenny_eval_score` (gauge, labeled by `tenant_id`, `scorer`, `variant_id`) to §16.1 metrics table with cross-reference to §10.7.

---

### EXP-025 — Fixed

**CHALLENGE:** The finding is correct. The percentage-mode assignment uses `hash(user_id + experiment_id) mod 100` but `user_id` is optional (anonymous sessions have no user_id). `hash(null + experiment_id)` produces a constant bucket — all anonymous sessions land in the same variant. With `sticky: user` and null `user_id`, stickiness is incoherent (what would be cached?). An implementor building the ExperimentRouter would have to guess the null-user_id behavior.

**CHANGES:** Added to §10.7 percentage-mode assignment: "For sessions with `user_id: null` (anonymous sessions): percentage-mode experiments always route to the control variant (no variant assignment). `sticky: user` caching is not applied (no cache key can be derived). Anonymous sessions are excluded from variant pools entirely — this prevents the hash-collision problem and ensures experiment results are not contaminated by a disproportionate concentration of anonymous traffic in one variant. Deployers who need to include anonymous sessions in experiments should use `mode: external` with a flag service that handles anonymous assignment."

---

### EXP-026 — Fixed

**CHALLENGE:** The finding is correct. The `ExperimentRouter` assigns sessions to variant pools but the spec has no check equivalent to the delegation isolation monotonicity gate. A session with `minIsolationProfile: sandboxed` could be routed to a variant pool with `standard` isolation — violating the session's isolation requirement. The delegation path has this check (`ISOLATION_MONOTONICITY_VIOLATED`) but the experiment routing path does not.

**CHANGES:** Added to §10.7 ExperimentRouter variant assignment (after experiment assignment determination): "Isolation monotonicity check: before routing a session to a variant pool, the gateway verifies that the variant pool's isolation profile satisfies the session's `minIsolationProfile` (same check as delegation isolation monotonicity, Section 8.3). If the variant pool's isolation is weaker than the session's minimum, the gateway falls through to the base runtime with no experiment assignment, and a `experiment.isolation_mismatch` warning event is emitted (fields: `experiment_id`, `variant_id`, `sessionMinIsolation`, `variantPoolIsolation`)."

---

### CPS-022 — Already fixed by BLD-024

**CHALLENGE:** BLD-024 already aligned all GOVERNANCE.md phase references (§2, §18, §19, §23.2) to the "drafted in Phase 2, finalized in Phase 17a" formulation.

**STATUS:** No additional action needed — covered by BLD-024 fix.

---

### CPS-023 — Fixed

**CHALLENGE:** The finding is correct. LangSmith has had a self-hosted Kubernetes deployment path since 2024 (it is documented in the LangSmith docs as "Self-Hosted Deployment"). The spec's blanket claim "no self-hosted Kubernetes-native deployment path" is factually wrong and undermines the credibility of the entire competitive comparison section. A factual error in a publicly visible spec is worse than no comparison at all.

**CHANGES:** Updated §23 LangSmith row: replaced "LangSmith is also a hosted service with no self-hosted Kubernetes-native deployment path" with "LangSmith offers self-hosted Kubernetes deployment (available since 2024); however, it requires LangChain ecosystem coupling, does not provide runtime-agnostic adapter contracts, and lacks per-hop budget/scope controls in delegation chains."

---

### CPS-005 — Fixed

**CHALLENGE:** The finding is correct. External interceptors use gRPC (§4.8 says "External interceptors are invoked via gRPC (like Kubernetes admission webhooks)") but this is not disclosed in §23.2 (Community Adoption Strategy) or the developer onboarding materials. A polyglot deployer who reads the community docs and decides to write a custom interceptor would discover the gRPC requirement only when implementing. One sentence in §4.8 is the minimum fix.

**CHANGES:** Added to §4.8 external interceptor registration: "Note for polyglot deployers: custom external interceptors must be implemented as gRPC services (the same protocol used by Kubernetes admission webhooks). HTTP webhook variants are planned as a post-v1 enhancement to lower the implementation barrier for deployers who prefer REST."

---

### CPS-006 — Fixed

**CHALLENGE:** The finding is correct. The spec has detailed runtime adapter documentation (§15.4) but no mention of how community runtime authors would share or discover each other's adapters. A community member who writes a runtime adapter has no spec-defined path to share it. One sentence in §23.2 scoping a registry out of v1 prevents community expectations from colliding with v1 scope.

**CHANGES:** Added to §23.2 (Community Adoption Strategy): "A community runtime registry — where runtime authors publish versioned adapter packages for operator discovery and installation — is planned as a post-v1 platform service. In v1, runtime adapters are distributed via standard Go module hosting, container registries, and Helm chart repositories. The runtime adapter specification (Section 15.4) provides the stable interface contract for v1 adapter distribution."

---

### POL-033 — Fixed

**CHALLENGE:** The finding is correct. §4.8 says "External interceptors registered at priority > 600 (after `RetryPolicyEvaluator`) run after all built-in evaluators." This is only true at `PostRoute`. At other phases where `GuardrailsInterceptor` is active (priority 400), an interceptor at priority 401-599 at `PreDelegation`, `PreLLMRequest`, `PostLLMResponse`, or `PostAgentOutput` runs after `AuthEvaluator` (100), `QuotaEvaluator` (200), `DelegationPolicyEvaluator` (250), `ExperimentRouter` (300), but before `GuardrailsInterceptor` (400). A MODIFY at 401-599 would be seen by `GuardrailsInterceptor` at 400 — but 400 < 401, so guardrails run first. The phrase "run after all built-in evaluators at the same phase" is ambiguous and context-dependent per phase.

**CHANGES:** Updated §4.8 to replace the blanket "> 600 runs after all built-ins" claim with: "Interceptors at priority > 600 run after `RetryPolicyEvaluator` (600) at `PostRoute`. At phases where `GuardrailsInterceptor` is active (priority 400), external interceptors at priority 401-599 run after guardrails; interceptors at 101-399 run before guardrails. A MODIFY returned by an interceptor at any priority is passed to all subsequent interceptors, including built-ins — `GuardrailsInterceptor` sees the modified content from upstream MODIFYs. Downstream external MODIFY operations (at priority > GuardrailsInterceptor's priority) are not re-evaluated by guardrails."

---

### POL-034 — Fixed

**CHALLENGE:** The finding is correct. The `maxExtendableBudget` layering table shows rows like "Tenant=300K" and "Effective=300K" in a context where it appears to be a hard cap, but 300K is actually the tenant's base value (a default that can be overridden by the runtime). The table footnote is absent. A deployer who believes they can hard-cap the budget at 300K via the tenant base value would be surprised when a runtime with `maxExtendableBudget: 800K` overrides it (per the resolution order spec, runtime overrides tenant base).

**CHANGES:** Added footnote to the `maxExtendableBudget` layering table in §8.6: "⁴ The 'Tenant sets' column shows the tenant's base value, not a ceiling. The runtime can override the tenant base value up to the tenant's ceiling (`leaseExtension.max.maxExtendableBudget`). To hard-cap budget for all runtimes in a tenant, set `leaseExtension.max.maxExtendableBudget` on the tenant config — this is the absolute ceiling that no runtime can exceed."

---

### DXP-026 — Fixed

**CHALLENGE:** The finding is correct. The spec mentions `/run/lenny/credentials.json` delivery and defines per-provider `materializedConfig` schemas, but the top-level structure of the file is never specified. Is it a single lease object? An array? A wrapper object with metadata? An implementor writing the adapter's credential file writer and the runtime's credential file reader must both guess the same structure. Given that the spec already defines per-provider materializedConfig schemas, the missing piece is just the envelope.

**CHANGES:** Added "Runtime credential file contract" block to §4.7 item 4: the file contains a single JSON object with `leaseId` (string), `provider` (string), `expiresAt` (ISO8601), `deliveryMode` ("direct" | "proxy"), and `materializedConfig` (the provider-specific credential map). For proxy mode, `materializedConfig` contains only `proxyUrl` and `leaseToken`. For direct mode, `materializedConfig` contains the full provider credentials. Multiple providers are not combined in a single file; only the assigned credential provider is present. Example: `{ "leaseId": "lease_abc123", "provider": "anthropic_direct", "expiresAt": "...", "deliveryMode": "direct", "materializedConfig": { "apiKey": "sk-..." } }`.

---

### DXP-027 — Fixed

**CHALLENGE:** The finding is correct. §4.7 and §15.4.3 say the runtime "must present the `mcpNonce` as the first message of the MCP `initialize` handshake" but do not specify the mechanism. §15.4.3 says "the runtime must present this nonce" with no wire format. An MCP client library implements `initialize` according to the MCP spec — there is no standard field for a nonce. A runtime author using an existing MCP library cannot implement this without knowing exactly where to put it.

**CHANGES:** Added "Nonce wire format" note in §15.4.3 (Standard-Tier MCP Integration, Authentication subsection): "The nonce is presented by setting the `_lennyNonce` field in the `initialize` request's `params.clientInfo.extensions` object: `{ params: { clientInfo: { name: '...', version: '...', extensions: { _lennyNonce: '<nonce_hex>' } } } }`. The adapter validates the `_lennyNonce` value against the manifest's `mcpNonce` field before processing any tool dispatch. MCP client libraries that do not support `clientInfo.extensions` may alternatively set `_lennyNonce` as a top-level field in `params` — the adapter checks both locations." Added concrete JSON example.
