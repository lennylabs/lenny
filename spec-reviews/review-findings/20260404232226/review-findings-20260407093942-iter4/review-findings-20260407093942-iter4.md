# Technical Design Review Findings — 2026-04-07 (Iteration 4)

**Document reviewed:** `docs/technical-design.md`
**Review framework:** `docs/review-povs.md`
**Iteration:** 4 of 5
**Total findings:** 74 across 25 review perspectives
**Scope:** Critical, High, and Medium (no fixes applied this iteration — review only)

---

## Medium Findings Resolution (Post-Iteration Processing — Batch 1)

| Finding | Status | Notes |
|---------|--------|-------|
| K8S-020 | Fixed | Added controller pod anti-affinity row to §17.8.2 controller tuning table: `preferred` for Tier 1, `required` for Tier 2/3 |
| K8S-021 | Fixed | Added agent-sandbox CRD presence check to §17.6 preflight table: verifies all four CRDs are installed at expected API version |
| K8S-022 | Skipped | Admission-time enforcement (RuntimeClass nodeSelector + dedicated-node taint) already provides hard scheduling constraints; post-scheduling reconciliation is defense-in-depth with no new operational failure mode |
| NET-022 | Fixed | Added CIDR exclusion block to §13.2: preflight validation + WarmPoolController continuous drift detection with `NetworkPolicyCIDRDrift` alert; also added preflight table row |
| NET-023 | Fixed | Added PgBouncer row to §13.2 lenny-system NetworkPolicy table: egress to Postgres TCP 5432, ingress from gateway/token-service/controller; self-managed profile only note included |
| SCL-023 | Fixed | Added per-tenant circuit breaker to §10.7 experiment targeting section: opens after 5 failures/10s, 30s open duration, `lenny_experiment_targeting_circuit_open` gauge + `ExperimentTargetingCircuitOpen` alert |
| SCL-024 | Fixed | Added KEDA/HPA mutual exclusivity paragraph to §10.1: conflict explanation, `ScaledObject.advanced.horizontalPodAutoscalerConfig` guidance, migration note, Helm chart enforcement via `autoscaling.provider` |
| SCL-025 | Fixed | Defined `--status-update-dedup-window` semantics in §4.6.1 etcd tuning section; added per-tier values (500ms/500ms/250ms) to §17.8.2 controller tuning table |

---

## High Findings Resolution (Post-Iteration Processing)

| Finding | Status | Notes |
|---------|--------|-------|
| NET-021 | Fixed | Updated lenny-system NetworkPolicy table: gateway and token-service Redis egress changed from TCP 6379 to TCP 6380 (TLS port, `{{ .Values.redis.tlsPort }}`) |
| PRT-020 | Fixed | Replaced `"delivery": "at-least-once"` with `"delivery": "queued"` in the §15.4.1 Inbound message example |
| SCH-029 | Skipped | The `scores` JSONB field is already documented with clear semantics (key = scorer-defined dimension name, value = float64). Aggregation works correctly as the union of all keys per scorer/variant. Adding a vocabulary registry is a v1 nicety with no operational impact — scorers already interoperate by using the scalar `score` field for cross-scorer comparison. |
| SLC-026 | Fixed | Added explicit re-await protocol to §8.10 step 4d: when resumed parent re-issues `lenny/await_children`, gateway streams settled results from `session_tree_archive` in original-settlement order before entering live-wait |
| DEL-021 | Fixed | Added "Detached orphan cascade and budget semantics" section to §8.10: detached orphans retain their own `cascadeOnFailure` policy; budget return on orphan completion is a no-op (parent session terminal) |
| MSG-023 | Fixed | Added `maxRequestInputWaitSeconds` (default 600s) to §11.3 timeout table; added `maxRequestInputWait` explanation paragraph with `request_input_expired` event definition for `await_children` stream |
| BLD-021 | Skipped | The spec already documents Phase 5.75 as "a hard prerequisite for Phase 6" with the explicit note "No session using real LLM credentials may be exercised in integration tests or development until Phase 5.75 is complete." Adding a technical enforcement mechanism (env var, startup check) is implementation detail, not spec — the spec's job is to state the requirement unambiguously, which it does. |
| FLR-021 | Fixed | Added orphan session reconciliation note to the coordinator hold-state section: a periodic reconciler cross-references `agent_pod_state` and forcibly transitions `running`/`attached` sessions whose pods are `Terminated` to `failed`, emitting `lenny_orphan_session_reconciliations_total` |
| WPL-021 | Fixed | Added explicit documentation that `sdkWarmBlockingPaths: []` disables demotion-path checking entirely; added guidance for derived runtimes including `CLAUDE.md` in `workspaceDefaults` |
| CRD-021 | Fixed | Added `lenny_credential_proactive_renewal_exhausted_total` counter and `CredentialProactiveRenewalExhausted` warning alert; documented that the fallback fault rotation counts against `maxRotationsPerSession` with guidance to set `maxRotationsPerSession >= 1 + expected_proactive_retries` |
| EXP-021 | Fixed | Added sticky assignment cache invalidation section: on `active → paused` and `active → concluded` transitions, gateway flushes all `sticky: user` cached assignments for the experiment; added `lenny_experiment_sticky_cache_invalidations_total` counter |

---

## Findings Summary

| Severity | Count |
| -------- | ----- |
| Critical | 0     |
| High     | 11    |
| Medium   | 63    |
| Info     | 0     |

### Comparison Across Iterations

| Severity | Iter 1 | Iter 2 | Iter 3 | Iter 4 | Trend |
|----------|--------|--------|--------|--------|-------|
| Critical | 30     | 4      | 1      | 0      | Clean |
| High     | 105    | 26     | 2      | 11     | ↓90%  |
| Medium   | 138    | 61     | 0*     | 63     | Carry-forward |
| Total    | 353    | 137    | 3      | 74     | ↓79%  |

*Iteration 3 was scoped to Critical/High only.

### Key Observations

1. **Zero Critical findings** — The spec is clean at Critical severity for the first time.
2. **11 High findings** — Mix of carry-forwards from iter1 Medium (now elevated based on scale/security impact) and 1 regression (NET-021 Redis TLS port).
3. **63 Medium findings** — Predominantly carry-forwards from iter1 that were out of scope for iterations 2-3 (which fixed Critical+High only). These represent the remaining specification gaps.
4. **No regressions from iter3 fixes** — The 3 iter3 fixes are all confirmed clean.

---

## Detailed Findings

---

## High Findings

---

### NET-021 Redis TLS Port Regression in lenny-system NetworkPolicy Table [High]
**Section:** 13.2, 10.3

Section 10.3 (NET-004 fix) mandates Redis must disable plaintext port (`port 0`) and listen only on the TLS port (default 6380). However, the lenny-system component NetworkPolicy allow-list table (§13.2) shows gateway egress as `Redis (TCP 6379)` and token-service egress as `Redis (TCP 6379)` — the plaintext port that is supposed to be disabled. This is a regression introduced when the lenny-system table was added: it hard-codes the wrong port. Any deployer who builds NetworkPolicies from this table will allow the wrong port and block TLS Redis connections.

**Recommendation:** Update the lenny-system NetworkPolicy table to use `Redis (TCP 6380)` (the TLS port, matching `{{ .Values.redis.tlsPort }}`) for both gateway and token-service egress rows.

---

### PRT-020 Protocol Trace Example Contains Invalid `delivery` Value [High]
**Section:** 15.4.1

The annotated minimum-tier protocol trace shows `"delivery": "at-least-once"`. The `delivery` field is a closed enum with exactly two valid values: `"immediate"` and `"queued"`. The spec explicitly states: "No other values are valid. The gateway rejects unknown `delivery` values with `400 INVALID_DELIVERY_VALUE`." This example is the primary copy-paste reference for Minimum-tier runtime authors — any author following it verbatim will produce protocol-invalid messages.

**Recommendation:** Replace `"delivery": "at-least-once"` with `"queued"` or omit the field entirely (absent defaults to `"queued"`). Audit other protocol trace examples for similar inconsistencies.

---

### SCH-029 `EvalResult.scores` JSONB Has No Minimum Schema or Dimension Vocabulary [High]
**Section:** 10.7

The `scores` field in `EvalResult` is unschematized JSONB. Different scorers using different key names for the same concept (`coherence` vs `Coherence` vs `coherence_score`) produce disjoint aggregation buckets, making cross-scorer comparison impossible. The aggregation query must do a full union scan of all JSONB keys with no index support. Community integrators building scorers have no standard vocabulary to conform to.

**Recommendation:** Define a minimal required schema for each `scores` entry: `{"value": float64 (0.0–1.0, required), "weight": float64 (optional)}`. Add an informational standard dimension vocabulary (`coherence`, `relevance`, `safety`, `faithfulness`, `helpfulness`) as a registry in §10.7.

---

### SLC-026 `await_children` Re-Attach Protocol After Parent Resume Unspecified [High]
**Section:** 8.8, 8.10

When a parent pod recovers from failure, §8.10 specifies that the gateway re-injects virtual child interfaces with `children_reattached`. However, the parent's `await_children` streaming call was lost on pod failure. The spec never describes the "re-await protocol": when the resumed parent re-issues `await_children`, how does the gateway stream already-settled child results from `session_tree_archive` before entering live-wait for still-running children? Without this, implementations will diverge on whether to replay archived results or only report live children.

**Recommendation:** Add a "re-await protocol" subsection to §8.10: when `await_children` is re-issued after `children_reattached`, the gateway streams all archived settled results from `session_tree_archive` in original-settlement order before entering live-wait for remaining unsettled children.

---

### DEL-021 Detached Children's Own Cascade Behavior and Budget-Return Unspecified [High]
**Section:** 8.10

When a parent fails with `cascadeOnFailure: detach`, children become orphaned. If an orphaned child subsequently fails, §8.10 is silent on: (1) whether the orphan's own `cascadeOnFailure` policy applies to its descendants, and (2) whether unused token budget is returned to the parent's (now-terminal) tree. The budget accounting model is incomplete: allocated budget in detached subtrees appears permanently consumed with no reconciliation path.

**Recommendation:** Add to §8.10: (1) detached orphans retain and execute their own `cascadeOnFailure` policy when they fail; (2) on orphan completion, budget return is a no-op — the parent session is terminal and the `INCRBY` call is discarded. Document this explicitly in §8.3.

---

### MSG-023 `lenny/request_input` Has No Dedicated Timeout — Child Blocks Until Session Expires [High]
**Section:** 8.8, 7.2, 11.3

`lenny/request_input` is an inter-agent blocking tool call (agent-to-agent, NOT human elicitation — `lenny/request_elicitation` is the human-facing tool governed by `maxElicitationWait`). When a child calls `request_input`, it enters `input_required` sub-state and blocks until a parent/sibling responds via `lenny/send_message` with `inReplyTo`. The only expiry mechanisms are session-level: `maxSessionAge` or `maxIdleTime`. There is no dedicated `maxRequestInputWait` timeout for this inter-agent call. A child whose parent is slow, crashed, or simply ignoring the request will block for up to `maxSessionAge` (default 7200s) — consuming a pod slot and credential lease for the entire duration with no progress.

Additionally, when a `request_input` eventually resolves (by session expiry or cancellation), the parent's `await_children` stream transitions the child to `expired` or `cancelled` — but there is no distinct event distinguishing "child expired because nobody answered its `request_input`" from "child expired because it ran out of time doing normal work." The parent cannot diagnose the cause.

**Recommendation:** (1) Add `maxRequestInputWaitSeconds` (default: 600s) to the §11.3 canonical timeout table, configurable per pool alongside `maxElicitationWait`. Configure it on the pool definition (e.g., in the `limits:` block of the RuntimeDefinition YAML in §5.1, same pattern as `maxSessionAge` and `maxElicitationWait`). When the timeout fires, the gateway resolves the blocked `lenny/request_input` tool call with a `REQUEST_INPUT_TIMEOUT` error — the child runtime receives this as a normal tool-call error and can handle it (retry, fall back, or fail). This is distinct from `maxElicitationWait` (which governs `lenny/request_elicitation`, the human-facing elicitation chain). (2) Add a `request_input_expired` event to the `await_children` stream in §8.8: `{ "type": "request_input_expired", "childId": "...", "requestId": "...", "expiredAt": "<ISO8601>" }` so the parent can distinguish "child's input request timed out" from "child expired for other reasons." (3) Reference the new timeout in the `input_required` sub-state definition at §6.2 and in the message delivery path 4 description at §7.2.

---

### BLD-021 Real-LLM Testing Precedes Auth Completion — Phase 5.75 Gate Is Documentation-Only [High]
**Section:** 18

Phase 5.5 introduces the Basic Token Service and credential pools with real API keys — two sub-phases before the Phase 5.75 auth gate. If Phase 5.5 is implemented before 5.75, real secrets exist in the cluster without JWT-auth and quota enforcement. The Phase 5.75 note states it is a "hard prerequisite for Phase 6" but this is a documentation constraint with no technical enforcement (no feature flag, env var, or startup check prevents the Token Service from loading real credentials before 5.75 is deployed).

**Recommendation:** Either (a) require the Token Service startup to refuse non-bootstrap credentials unless an `auth_gate_cleared` config value is present (set by Phase 5.75), or (b) restructure Phase 5.5 to store credentials in sealed form that the gateway cannot unwrap until the auth interceptor is wired. Add a CI gate preventing integration tests from using real credentials without `AUTH_GATE_CLEARED=1`.

---

### FLR-021 Coordinator Hold-State Timeout Produces Orphaned Pod with No Session Record [High]
**Section:** 10.1

When the adapter enters hold state and no coordinator fences within `coordinatorHoldTimeoutSeconds` (120s), the adapter "emits a `session.terminated` event once a coordinator reconnects (or writes it to local disk for post-mortem if no coordinator ever returns)." If no coordinator ever reconnects, the session record in Postgres is never updated to terminal state. The pod terminates but the session row remains `running`/`attached` indefinitely — an orphaned session occupying quota.

**Recommendation:** The adapter must write a minimal termination record to pod-local tmpfs before shutdown. The preStop hook must attempt to write to Postgres. The gateway must detect stale `running` sessions whose pods are `Terminated` in Kubernetes (via `agent_pod_state` mirror table) and forcibly transition them to `failed` during periodic reconciliation.

---

### WPL-021 `sdkWarmBlockingPaths` Default Not Overridable for Inherited Defaults [High]
**Section:** 6.1

`sdkWarmBlockingPaths` defaults to `["CLAUDE.md", ".claude/*"]`. Files from `workspaceDefaults` (in the Runtime definition) are included in the matching check. A derived runtime that includes `CLAUDE.md` in `workspaceDefaults` — a very natural thing for Claude-based agents — triggers 100% demotion, hitting the circuit breaker immediately. There is no documented way to set `sdkWarmBlockingPaths: []` to opt out of demotion matching entirely.

**Recommendation:** Explicitly document in §6.1 that setting `sdkWarmBlockingPaths: []` on the Runtime definition disables demotion-path checking entirely. Add a note that derived runtimes inheriting `CLAUDE.md` in `workspaceDefaults` should set `sdkWarmBlockingPaths: []` if the file is always present and the SDK process is designed to tolerate it.

---

### CRD-021 Proactive Lease Renewal Race — Exhaustion Falls Through to Fault Rotation [High]
**Section:** 4.9

The `CredentialRenewalWorker` retries proactive renewal up to 3 times at half the remaining TTL interval. If all retries fail, it falls through to the standard Fallback Flow at `expiresAt`, which **consumes a `maxRotationsPerSession` slot** (proactive renewals are excluded, but the fallback fault rotation is not). At short TTLs, the total retry window could extend past `expiresAt`, meaning the lease expires before all retries complete. There is no alert for proactive renewal exhaustion distinct from fault rotation exhaustion.

**Recommendation:** Add `lenny_credential_proactive_renewal_exhausted_total` counter (labeled by pool, provider) and `CredentialProactiveRenewalExhausted` warning alert. Document that the fallback fault rotation at `expiresAt` counts against `maxRotationsPerSession`, and deployers should set `maxRotationsPerSession >= 1 + expected_proactive_retries` for long sessions.

---

### EXP-021 External Targeting `sticky: user` Cache Has No Invalidation Path [High]
**Section:** 10.7

`sticky: user` caches variant assignments across sessions. No invalidation mechanism is specified for: (1) variant paused or concluded, (2) user eligibility changes, (3) variant list changes. A concluded variant's cached assignment may route sessions to a non-existent pool (causing `WARM_POOL_EXHAUSTED` or `RUNTIME_NOT_FOUND`).

**Recommendation:** Key caches by `(user_id, experiment_id)` with a `stickyAssignmentTtlSeconds` field (default 86400s). On `active → paused` and `active → concluded` transitions, flush all sticky caches for the experiment. Add `lenny_experiment_sticky_cache_invalidations_total` counter.

---

## Medium Findings

---

## 1. Kubernetes Infrastructure & Controller Design

### K8S-020 Controller Anti-Affinity Remains Advisory [Medium]
**Section:** 4.6.1, 17.8

§4.6.1 states "operators should use pod anti-affinity" but the actual Helm chart default is `preferredDuringSchedulingIgnoredDuringExecution` (soft). Under resource pressure, both controllers can land on the same node, doubling the blast radius of a node failure.

**Recommendation:** Change to `requiredDuringSchedulingIgnoredDuringExecution` for Tier 2/3 Helm chart defaults. Add a `lenny-preflight` check that warns if anti-affinity is downgraded to `preferred` in non-development profiles.

**Status: Fixed.** Added `Controller pod anti-affinity` row to the §17.8.2 controller tuning table specifying `preferred` for Tier 1 (dev-compatible) and `required` for Tier 2/3 (WPC and PSC leaders must not share a node).

---

### K8S-021 agent-sandbox CRD Presence Not Validated at Controller Startup [Medium]
**Section:** 4.6

No startup check verifies the four agent-sandbox CRDs (`SandboxTemplate`, `SandboxWarmPool`, `Sandbox`, `SandboxClaim`) are installed and at the expected API version. A deployment that omits the CRD apply step will fail with opaque API errors at reconciliation time.

**Recommendation:** Add `validateAgentSandboxCRDs()` startup check alongside `validateRuntimeClasses()`. Verify all four CRD names exist and `spec.versions[*].name` includes the expected version. Mirror in the `lenny-preflight` Job.

**Status: Fixed.** Added "Agent-sandbox CRDs" check row to the §17.6 preflight table: verifies all four CRDs (`sandboxtemplates.lenny.dev`, `sandboxwarmpools.lenny.dev`, `sandboxes.lenny.dev`, `sandboxclaims.lenny.dev`) are present and at the expected API version before installation proceeds.

---

### K8S-022 Kata Node Pool Taint Has No Post-Scheduling Validation [Medium]
**Section:** 5.3, 17.2

The admission webhook enforces the RuntimeClass reference at creation time, but a misconfigured RuntimeClass with no `scheduling.nodeSelector` would allow Kata pods to schedule on standard runc nodes with no post-scheduling check.

**Recommendation:** Add a controller reconciliation check verifying every `Sandbox` pod with `runtimeClassName: kata-microvm` is on a node labeled `lenny.dev/node-pool: kata`. Define a `KataIsolationViolation` critical alert in §16.5.

**Status: Skipped.** The spec already requires the `kata-microvm` RuntimeClass to include `scheduling.nodeSelector: {lenny.dev/node-pool: kata}` and Kata nodes to carry the `lenny.dev/isolation=kata:NoSchedule` taint. This provides hard admission-time enforcement — a misconfigured RuntimeClass missing the nodeSelector would be caught at preflight (RuntimeClass check) and by the taint/toleration enforcement at scheduling. Post-scheduling periodic reconciliation is defense-in-depth but adds controller complexity for a scenario already blocked at multiple admission points.

---

## 3. Network Security & Isolation

### NET-022 Internet Egress CIDR Exclusions Require Manual Helm Values with No Drift Detection [Medium]
**Section:** 13.2

The `internet` egress profile uses `egressCIDRs.excludeClusterPodCIDR` and `egressCIDRs.excludeClusterServiceCIDR` Helm values as `except` clauses on the `0.0.0.0/0` rule. These must be manually set to match the cluster's actual pod and service CIDRs. If wrong at deploy time, or stale after a cluster CIDR resize or node pool expansion between Helm deployments, agent pods with `internet` egress can reach internal cluster IPs — enabling lateral movement and internal service discovery. No automated extraction, deploy-time validation, or continuous drift detection exists.

**Recommendation:** Two-layer fix: (1) **Deploy-time preflight** — the `lenny-preflight` Job reads actual cluster CIDRs (from node `spec.podCIDR` and kube-controller-manager `--service-cluster-ip-range` or the `kubernetes` Service CIDR) and fails if the Helm values don't match. This catches misconfiguration at install/upgrade time. (2) **Continuous drift detection** — a lightweight goroutine in the WarmPoolController (or gateway) re-reads cluster CIDRs every 5 minutes and compares against the installed NetworkPolicy `except` blocks. On drift, emit a `NetworkPolicyCIDRDrift` critical alert. Auto-patching the NetworkPolicy is preferable but requires granting the controller NetworkPolicy write RBAC (currently avoided by design — §13.2 states "the warm pool controller does NOT create or modify NetworkPolicies"); if that RBAC is not granted, alerting-only is the pragmatic v1 answer, with the operator re-running `helm upgrade` to re-sync values. A ValidatingAdmissionWebhook is not the right tool here: it operates on pod specs, not NetworkPolicy content, and would need to read NetworkPolicies from the API server at admission time — adding latency and an availability dependency for every pod creation.

**Status: Fixed.** Added CIDR exclusion correctness block to §13.2 `internet` profile hardening: (1) preflight validation reads actual cluster pod/service CIDRs and fails Helm install/upgrade on mismatch; (2) WarmPoolController goroutine re-reads cluster CIDRs every 5 minutes, increments `lenny_network_policy_cidr_drift_total` counter and fires `NetworkPolicyCIDRDrift` critical alert on drift (alert-only, no auto-patch). Also added "Internet egress CIDR exclusions" check row to the §17.6 preflight table.

---

### NET-023 PgBouncer-to-Postgres NetworkPolicy Not Specified [Medium]
**Section:** 13.2

The lenny-system NetworkPolicy table covers gateway, token-service, controller, and CoreDNS but PgBouncer is absent. If PgBouncer runs in `lenny-system` as a Deployment, the default-deny policy blocks PgBouncer-to-Postgres traffic.

**Recommendation:** Add PgBouncer as a row in the table specifying allowed egress (TCP 5432 to Postgres). Alternatively, add a note that PgBouncer is external and NetworkPolicy does not govern it.

**Status: Fixed.** Added PgBouncer row to the §13.2 lenny-system component NetworkPolicy table: egress to Postgres TCP 5432 + CoreDNS; ingress from gateway, token-service, and controller pods on TCP 5432. Row includes a note that it applies to the self-managed profile only (absent on cloud-managed deployments where the provider proxy is external to the cluster).

---

## 4. Scalability & Performance Engineering

### SCL-023 Experiment Targeting Webhook Has No Circuit Breaker [Medium]
**Section:** 10.7

The external webhook is called synchronously on the session creation hot path with a 200ms timeout. No circuit breaker exists. At 200/s session creation rate, repeated 200ms timeouts reduce throughput by ~40× with no escape.

**Recommendation:** Add a circuit breaker: after 5 consecutive failures in 10s, open the circuit and return empty assignment for 30s. Document config fields alongside `timeoutMs`. Emit `lenny_experiment_targeting_circuit_open` gauge and alert.

**Status: Fixed.** Added "Targeting webhook circuit breaker" paragraph to §10.7 experiment targeting section: per-tenant circuit breaker opens after 5 consecutive failures in 10s, returns empty assignment with zero wait while open (30s), probes on half-open. Config fields `circuitBreaker.failureThreshold`, `circuitBreaker.windowSeconds`, `circuitBreaker.openDurationSeconds` documented alongside `timeoutMs`. Added `lenny_experiment_targeting_circuit_open` gauge and `ExperimentTargetingCircuitOpen` warning alert.

---

### SCL-024 KEDA and Standalone HPA Coexistence Not Specified [Medium]
**Section:** 10.1, 17.8

§10.1 describes both HPA `behavior` configuration and KEDA `ScaledObject` without clarifying they are mutually exclusive. KEDA creates and manages the HPA resource — a deployer who also applies a standalone HPA creates a conflict.

**Recommendation:** Add explicit note: when using KEDA, do NOT deploy a standalone HPA. All `behavior.*` settings go in ScaledObject's `advanced.horizontalPodAutoscalerConfig`. Provide a migration note for Prometheus Adapter → KEDA upgrades.

**Status: Fixed.** Added "KEDA and standalone HPA are mutually exclusive" paragraph to §10.1 immediately before the Tier 3 KEDA requirement: explains conflict mechanism, documents that `behavior.*` settings go in `ScaledObject.advanced.horizontalPodAutoscalerConfig`, includes migration note (delete standalone HPA before creating ScaledObject), and notes that the Helm chart enforces mutual exclusivity via `autoscaling.provider` value.

---

### SCL-025 `statusUpdateDeduplicationWindow` Controller Flag Undocumented [Medium]
**Section:** 4.6.1, 17.8

This flag is listed as the primary etcd write-pressure mitigation for managed Kubernetes but is never defined: no default value, no min/max bounds, no per-tier recommendations, no description of deduplication semantics (trailing window? minimum inter-write interval?).

**Recommendation:** Define as `--status-update-dedup-window` (type: duration, default: 500ms, semantics: minimum interval between consecutive status writes for the same `Sandbox` resource). Add per-tier values to §17.8 controller tuning table.

**Status: Fixed.** (1) Added `statusUpdateDeduplicationWindow` semantics block to §4.6.1 etcd tuning table: defines `--status-update-dedup-window` as `time.Duration`, default 500ms, trailing-window coalescing semantics (last observed status always written, intermediate writes suppressed). (2) Added `statusUpdateDeduplicationWindow` row to §17.8.2 controller tuning table with per-tier values: 500ms (Tier 1/2), 250ms (Tier 3).

---

## 2. Security & Threat Modeling

### SEC-029 Agent-Initiated URL-Mode Elicitation Allowlist Requires No Domain Constraint [Medium]
**Section:** 9.2

The per-pool allowlist for agent-initiated URL-mode elicitations has no defined structure and no mandatory `domainAllowlist`. A deployer could enable URL-mode elicitation with an empty domain constraint, allowing agents to craft arbitrary phishing URLs.

**Recommendation:** Define the allowlist as a structured object with a required non-empty `domainAllowlist` array. Reject pool registrations where URL-mode is enabled but `domainAllowlist` is empty.

---

### SEC-030 Session Inbox `from` Field Authentication Not Documented as Security Invariant [Medium]
**Section:** 7.2

The spec does not explicitly state that `from` is always set by the gateway from the authenticated caller's session identity and cannot be forged. A compromised pod might supply a spoofed `from` value.

**Recommendation:** Add a security invariant paragraph: "The `from` field is always set by the gateway. The gateway ignores any `from` value supplied by the sender. `messagingScope` enforcement uses the gateway-authenticated caller identity."

---

### SEC-031 Webhook `callbackUrl` DNS Pinning — Stale IP Handling and `dryRun` Behavior [Medium]
**Section:** 14, 15.1

The `callbackUrl` SSRF protection is well-designed: DNS is resolved at registration, the IP is validated as non-private, and the custom `DialContext` connects directly to the pinned IP at delivery time — completely ignoring subsequent DNS changes. This correctly blocks DNS rebinding (an attacker re-pointing their DNS to an internal IP after registration has no effect, since the gateway never re-resolves).

The remaining gap is **reliability, not security**: if the legitimate target's IP changes (CDN rotation, infrastructure migration) during a long session (up to 7200s), the pinned IP becomes unreachable. All delivery attempts fail, retries exhaust, and events land in the undelivered queue (`GET /v1/sessions/{id}/webhook-events`). The operator has no signal that the failure is caused by a stale pin vs. an actual target outage, and no mechanism to refresh the pin without creating a new session.

A secondary gap is `dryRun` clarity: §15.1 states "`dryRun` never makes outbound network calls." For `POST /v1/sessions/start` with a `callbackUrl`, it is unspecified whether `dryRun` performs DNS resolution to validate the URL (catching typos and unreachable hostnames) or skips it entirely (consistent with "no outbound calls" but less useful for pre-validation). DNS resolution is arguably not an "outbound network call" in the same category as HTTP requests, but the spec doesn't draw this line.

**Recommendation:** (1) **Stale pin handling:** On TCP connection failure during delivery, the gateway should re-resolve the hostname, re-validate the new IP against the same private/reserved range checks applied at registration, and update the pin if the new IP passes. If the new IP fails validation (resolves to a private range — potential rebinding attack), reject the delivery and emit a `callback.dns_rebinding_blocked` security audit event. This preserves the DNS-rebinding defense while recovering from legitimate IP changes. Add a `lenny_callback_pin_refreshed_total` counter. (2) **`dryRun` clarification:** Add to §15.1 endpoint-specific `dryRun` semantics: "`POST /v1/sessions/start` with `callbackUrl`: URL format and scheme validation is performed; DNS resolution is NOT performed (consistent with 'no outbound network calls'). To pre-validate DNS reachability, the client can resolve the hostname independently before submitting."

---

### SEC-032 No Content-Type/MIME Validation on Uploaded Files [Medium]
**Section:** 7.4, 13.4

No server-side content-type sniffing or MIME-type validation. A client can upload a `.txt` file containing a PE binary or a polyglot file. Runtimes parsing based on extension may behave unexpectedly.

**Recommendation:** Add server-side MIME detection via magic-byte sniffing. Reject files where detected type is inconsistent with declared extension. At minimum, document as a gap with deployer mitigation note.

---

### SEC-033 Admin Bootstrap Endpoint Has No Explicit Audit Logging Specification [Medium]
**Section:** 15.1, 17.6

`POST /v1/admin/bootstrap` can silently downgrade isolation profiles. Neither §15.1 nor §17.6 specifies that every bootstrap call is audit-logged with the acting service account identity and a summary of changes.

**Recommendation:** Specify that every bootstrap call emits a `platform.bootstrap_applied` audit event (T3) recording service account identity, seed file SHA-256 hash, and resource changes. The bootstrap Job should use a minimal-RBAC ServiceAccount.

---

### SEC-034 Semantic Cache `user_id` Not Required for Pool-Scoped Credentials [Medium]
**Section:** 4.9

For pool-scoped credentials, the cache key is `(tenant_id, query_embedding, model, provider)` — `user_id` is omitted. Two users in the same tenant with semantically similar queries could share cached responses, leaking conversation context.

**Recommendation:** Default to always including `user_id` in the cache key. Add a `cacheScope` field with options `tenant` (explicit opt-in for cross-user sharing), `per-user` (default), `per-session`.

---

## 13. Compliance, Governance & Data Sovereignty

### CMP-020 No Compliance Controls Mapping for SOC2/HIPAA/FedRAMP [Medium]
**Section:** 12.8, 12.9, 16.4

The spec defines `complianceProfile` values and enforces SIEM requirements, but there is no systematic mapping from each framework's controls to platform mechanisms. Deployers cannot perform gap analysis. Critical for FedRAMP (requires controls traceability) and HIPAA (§164.312 technical safeguards).

**Recommendation:** Add a compliance controls appendix mapping each framework's controls to: platform mechanism, required configuration, and responsibility tier (platform/deployer/out-of-scope). Flag unaddressed controls. This is a pre-GA deliverable for regulated deployments.

---

### CMP-021 KMS Key Residency Not Required to Match `dataResidencyRegion` [Medium]
**Section:** 12.8, 12.9, 4.3

Data residency is enforced for storage (Postgres, MinIO, Redis) but the KMS key used for envelope encryption may reside in a different jurisdiction. Under GDPR and data localization laws, decryption capability in another jurisdiction may constitute a cross-border data transfer.

**Recommendation:** Add a `kmsRegion` field derived from `dataResidencyRegion`. Validate at session creation that the KMS endpoint is in the same region. Fail-closed if no KMS endpoint matches.

---

### CMP-022 Erasure SLA Has No Hard Stop on New Data Processing [Medium]
**Section:** 12.8

When an erasure request is submitted, the platform continues accepting new sessions for that user. GDPR Article 18 requires restricting processing during pending erasure. New sessions generate PII not in scope for the running erasure job.

**Recommendation:** Set `processing_restricted: true` on the user record when erasure is initiated. Block `POST /v1/sessions` for that `user_id` with `403 ERASURE_IN_PROGRESS`. Clear when the erasure job completes.

---

### CMP-023 Task-Mode Residual State Can Retain PHI Without Routing Enforcement [Medium]
**Section:** 5.2, 12.9

Workspace classification is per-tenant, not per-session. A tenant with `workspaceTier: T3` cannot route individual high-sensitivity sessions to T4-isolation pools at session creation time. A HIPAA deployer could inadvertently route PHI to standard runc pools with task-mode reuse.

**Recommendation:** Add a `dataClassification` field to the session request or WorkspacePlan. When `T4`, enforce routing to T4-compatible pools at admission time via the Policy Engine.

---

## 24. Policy Engine & Admission Control

### POL-024 Circuit Breaker Specification Is a Bare Stub [Medium]
**Section:** 11.6

§11.6 lists five circuit breaker scenarios but specifies no storage mechanism, no replica propagation, no admin API endpoint, no AdmissionController evaluation rules, and no audit logging. Operators have no documented procedure for engaging or disengaging circuit breakers during incidents.

**Recommendation:** Specify: (a) Redis storage with pub/sub propagation; (b) `POST /v1/admin/circuit-breakers/{name}/{action}` endpoint; (c) AdmissionController reads circuit state on every admission; (d) `circuit_breaker.state_changed` audit events; (e) `CircuitBreakerActive` metric/alert.

**Status: Fixed.** Expanded §11.6 with: (a) Redis storage (`cb:{name}` key) with pub/sub propagation on `circuit_breaker_events` channel + 5s polling cache; (b) full admin API table (`GET`/`POST open`/`POST close` on `/v1/admin/circuit-breakers/{name}`); (c) AdmissionController evaluation block — rejects with `CIRCUIT_BREAKER_OPEN` (HTTP 503) before quota/policy; (d) `circuit_breaker.state_changed` audit event spec; (e) `lenny_circuit_breaker_open` gauge + `CircuitBreakerActive` alert. `CIRCUIT_BREAKER_OPEN` added to error catalog (§15.1).

---

### POL-025 Canonical Timeout Table Is Incomplete [Medium]
**Section:** 11.3

§11.3 has 8 timeout entries. 9+ operation timeouts are defined only in prose elsewhere: `CoordinatorFence` (5s), gRPC keepalive (10s+5s), `checkpointBarrierAckTimeoutSeconds` (45s), `coordinatorHoldTimeoutSeconds` (120s), elicitation per-hop forwarding (30s), interceptor PreLLMRequest (100ms), cert expiry warning (1h), DNS resolution timeout, admission webhook timeout (5s).

**Recommendation:** Extend §11.3 to include all platform timeouts, with Helm value names and configurability columns. Or add a separate "Platform Timeout Reference" subsection cross-referenced from §11.3.

**Status: Fixed.** Expanded §11.3 timeout table from 9 rows to 21 rows. Added columns for Helm value/flag name, configurability, and source section. New rows cover: gRPC keepalive interval (10s) and timeout (5s), coordinator hold timeout (120s), CoordinatorFence RPC (5s, hard-coded), checkpoint barrier ack (45s), elicitation per-hop forwarding (30s, hard-coded), external interceptor default (500ms), PreLLMRequest/PostLLMResponse interceptor (100ms), connector interceptor (200ms), credential cert expiry warning (1h), admission webhook (5s), DNS resolution timeout (5s, cluster-level).

---

### POL-026 `maxDelegationPolicy` Field in Delegation Lease Is Undefined [Medium]
**Section:** 8.3

The lease schema shows `"maxDelegationPolicy": null` with only "Session-level override" as description. Type (named reference vs inline?), interaction with `delegationPolicyRef`, precedence rules, and `null` semantics are all unspecified.

**Recommendation:** Add a definition block: type is named `DelegationPolicy` reference, applied as additional intersection with `delegationPolicyRef` (restriction only, never expansion), `null` means no additional restriction. Include a concrete example.

**Status: Fixed.** Added `maxDelegationPolicy` definition block to §8.3: type is `string | null` (named `DelegationPolicy` reference); `null` means no additional restriction; non-null applies as additional intersection with `delegationPolicyRef` (restriction-only, never expansion); precedence is after `delegationPolicyRef` and derived-runtime policy; inheritance rule (child cannot be less restrictive than parent's effective `maxDelegationPolicy`); concrete example showing `broad-policy` capped by `read-only-policy`.

---

### POL-027 Interceptor Timeout Has No Distinct Error Code [Medium]
**Section:** 4.8

A `PreLLMRequest` interceptor timeout with `fail-closed` returns `LLM_REQUEST_REJECTED` — same as an explicit DENY. Callers cannot distinguish "policy blocked this" from "interceptor service is degraded."

**Recommendation:** Add `INTERCEPTOR_TIMEOUT` to the error catalog (category `TRANSIENT`, HTTP 503, `retryable: true`). Return this on timeout regardless of `failPolicy`. Include `timeout_ms`, `interceptor_ref`, `phase` in the audit event.

**Status: Fixed.** Added "Interceptor timeout error code" paragraph to §4.8: `INTERCEPTOR_TIMEOUT` returned on timeout when `failPolicy: fail-closed` (request rejected); suppressed (request proceeds with `interceptor_bypassed` audit flag) when `failPolicy: fail-open`. Audit event includes `timeout_ms`, `interceptor_ref`, `phase`. Added `INTERCEPTOR_TIMEOUT` to the error catalog (§15.1): `TRANSIENT`, HTTP 503, `retryable: true`, details include `interceptor_ref`, `phase`, `timeout_ms`.

---

### POL-028 Budget Return Does Not Specify In-Flight Usage Quiescence [Medium]
**Section:** 8.3

`budget_return.lua` fires when a child reaches terminal state, but the last `ReportUsage` RPC may still be in-flight. The parent receives slightly more budget than correct.

**Recommendation:** Specify a quiescence step: wait for a `FINAL_USAGE_REPORT` from the child pod (or gRPC close) before executing `budget_return.lua`, with a bounded timeout (default 5s). On timeout, use last known usage counter and emit `delegation.budget_return_usage_lag` warning.

**Status: Fixed.** Added "Usage quiescence before budget return" block to §8.3 step 3: (1) gateway waits for `FINAL_USAGE_REPORT` or gRPC stream close from the adapter before executing `budget_return.lua`; (2) if neither arrives within `delegation.usageQuiescenceTimeoutSeconds` (default 5s), proceeds with last known counter and emits `delegation.budget_return_usage_lag` warning with `delta_tokens_unknown: true`; (3) `budget_return.lua` executes only after quiescence completes. Added `lenny_delegation_budget_return_usage_lag_total` counter.

---

### POL-029 DelegationPolicy Tag Evaluation Uses Live Labels — Policy Window Undocumented [Medium]
**Section:** 8.3

"Tags can change without redeploying — policy re-evaluated on each delegation." A pool's labels changed mid-session can dynamically grant or revoke delegation permissions with no documentation of whether this is intentional.

**Recommendation:** Clarify: (a) whether evaluation is point-in-time at delegation or live on every spawn; (b) whether mid-session label changes affect active trees; (c) document the security implication. Consider `snapshotPolicyAtLease: true` option.

**Status: Fixed.** Added "Tag evaluation semantics and security implications" block to §8.3: (a) evaluation is point-in-time at each `delegate_task` call using live labels at that instant; (b) mid-session label changes do NOT affect active child sessions — only subsequent `delegate_task` calls; (c) security implication documented (operator label mutation is intentional incident gate); (d) `snapshotPolicyAtLease: true` option added to `DelegationPolicy` — when set, gateway snapshots matching pool IDs at lease issuance, stored as `snapshotted_pool_ids`; all subsequent tree delegations evaluate against the snapshot rather than live labels.

---

## 5. Protocol Design & Future-Proofing

### PRT-021 `publishedMetadata` Auto-Generation Contradicts Opaque Pass-Through [Medium]
**Section:** 5.1, 21.1

§5.1 says gateway treats `publishedMetadata` as "opaque pass-through." §21.1 references "A2A card auto-generation" from the data. These conflict: auto-generation implies parsing; pass-through means no parsing.

**Recommendation:** Clarify whether A2A card generation is write-time (runtime stores pre-formatted card; gateway serves verbatim) or read-time (gateway constructs on the fly). Add a `?format=a2a` query parameter for format-specific retrieval.

---

### PRT-022 MCP Feature Dependency vs Adapter-Layer Version Not Distinguished [Medium]
**Section:** 15.2, 15.5

MCP spec version negotiation (adapter-layer) is well-handled. But MCP Tasks and Elicitation are structural gateway dependencies, not just adapter features. If a future MCP spec revises these, gateway internals are affected.

**Recommendation:** Add a "MCP core dependency inventory" note identifying Tasks and Elicitation as structural dependencies with mitigation: Lenny maintains an internal canonical task state machine independent of MCP spec wording.

---

### PRT-023 OpenAI Completions Adapter Lifecycle Limitations Not Declared [Medium]
**Section:** 15

No `AdapterCapabilities` structure declares which lifecycle operations each adapter supports. The OpenAI adapter has no session continuity, delegation, interrupt, or multi-turn inbox — but clients have no machine-readable way to discover this.

**Recommendation:** Define `AdapterCapabilities` with boolean fields (`sessionContinuity`, `delegation`, `interrupt`, `elicitation`, `streaming`). Expose via metadata endpoint. Document behavior on unsupported operations (`405` or `400 UNSUPPORTED_BY_ADAPTER`).

---

## 18. Content Model, Data Formats & Schema Design

### SCH-030 DelegationLease Budget Fields Have No Overflow Semantics [Medium]
**Section:** 8.3

The reservation model caps what a child is granted, but a pod can consume more tokens than its slice before per-turn counting catches up. The spec doesn't define what happens when a child's actual consumption exceeds its slice at settlement time.

**Recommendation:** Add "over-run semantics" subsection: define whether children are hard-capped at grant (LLM proxy enforces ceiling per lease) or soft-capped with settlement against parent budget. Document the settlement calculation.

---

### SCH-031 Capability Inference Default for Unannotated Tools Is Counter-Intuitively `admin` [Medium]
**Section:** 5.1

Most third-party MCP tools omit annotations. Unannotated tools are inferred as requiring `admin` — the most restrictive level. This silently fails when assigned to pools without `admin` capability, with no clear error.

**Recommendation:** Change default from `admin` to `write`. Add `capabilityInferenceMode` field on RuntimeDefinition (`strict` keeps `admin`; `permissive` uses `write`). Log a warning when capability is inferred from absent annotations.

---

### SCH-032 BillingEvent `sequence_number` Gap-Detection Remediation Undefined [Medium]
**Section:** 11.2.1

`sequence_number` enables gap detection but no remediation protocol exists: no replay API, no sequencing authority named, no failover behavior defined.

**Recommendation:** Name the sequencing authority (Postgres sequence per tenant). Specify provisional numbers in the in-memory buffer are renumbered on flush. Document a replay endpoint `GET /v1/metering/events?since_sequence=N`.

---

### SCH-033 Adapter Manifest `version` Is Integer Not Semver; `minPlatformVersion` Absent [Medium]
**Section:** 4.7, 15.4

Manifest `version` is a single integer with no major/minor/patch distinction. No `minPlatformVersion` field exists to declare minimum gateway compatibility. The gateway's compatibility check on registration is not documented.

**Recommendation:** Change to semver string. Add `minPlatformVersion` semver field. Document: gateway rejects `minPlatformVersion > current`; accepts higher manifest `version` for additive changes; rejects breaking manifests.

---

## 14. API Design & External Interface Quality

### API-027 OpenAPI Spec Has No Published Well-Known URL [Medium]
**Section:** 15.1, 15.5, 15.6

SDKs are generated from the OpenAPI spec, but no well-known endpoint (e.g., `GET /openapi.yaml`) is documented. Community integrators have no URL, format, or versioning strategy for the live spec.

**Recommendation:** Specify `GET /openapi.yaml` as a gateway-served well-known endpoint. Document OpenAPI version (3.x), authentication, and API-version alignment.

---

### API-028 `RESOURCE_HAS_DEPENDENTS` Details Omit Per-Resource IDs [Medium]
**Section:** 15.1

The error includes `type`, `name`, and `count` but no `id` for individual blocking resources. UIs cannot construct links to blocking sessions. Operators must issue separate queries.

**Recommendation:** Add an `ids` array (capped at 20) per entry where a stable identifier exists. When `count > 20`, set `truncated: true`.

---

### API-029 Sortable Fields Not Enumerated Per Resource Type [Medium]
**Section:** 15.1

The `sort` query parameter is accepted but valid fields vary by resource with no documentation. Clients must guess by trial-and-error.

**Recommendation:** Add per-resource sortable-fields to each list endpoint. At minimum: sessions (`created_at`, `state`, `runtime`), pools (`name`, `created_at`), billing events (`sequence_number`, `timestamp`).

---

### API-030 No PATCH Endpoints for Complex Admin Resources [Medium]
**Section:** 15.1

All admin updates are full-body PUT. For large resources like `RuntimeDefinition` or `DelegationPolicy`, this requires read-modify-write with race conditions even with ETags.

**Recommendation:** Add `PATCH` (JSON Merge Patch, RFC 7396) for at minimum `RuntimeDefinition`, `DelegationPolicy`, and `SandboxWarmPool`. Require `If-Match`. Document merge semantics for array fields.

---

## 6. Developer Experience (Runtime Authors)

### DXP-022 No "For Runtime Authors: Start Here" Entry in §1 [Medium]
**Section:** 1, 15.4.5

§15.4.5 has a well-structured Runtime Author Roadmap but §1 has no forward reference to it. A runtime author reading the spec encounters the executive summary with no indication of the author-specific entry point at page ~175.

**Recommendation:** Add a "For Runtime Authors: Start Here" callout in §1 (after Core Design Principles) with a one-paragraph summary and direct reference to §15.4.5.

---

### DXP-023 Local Dev Does Not Document Custom Runtime Substitution [Medium]
**Section:** 17.4

§17.4 documents `make run` and `docker compose up` but has no guidance on substituting a custom runtime binary. This is the first practical step after reading §15.4.4.

**Recommendation:** Add a "Plugging in a custom runtime" subsection: (a) Tier 1 `make run` variable override for custom binary path; (b) Tier 2 `LENNY_AGENT_RUNTIME=<name>` env var and seed file registration. A 10-line example is sufficient.

---

### DXP-024 Abstract Unix Socket Transport Without macOS Compatibility Note [Medium]
**Section:** 15.4.3, 4.7

Abstract Unix sockets (`@` prefix) are Linux-only. §17.4 implies macOS is a supported dev platform, but Standard-tier development on macOS fails silently.

**Recommendation:** Add platform compatibility note: "Abstract Unix sockets require Linux. macOS development of Standard/Full-tier runtimes requires Docker (`docker compose up`). `make run` supports macOS for Minimum-tier only."

---

## 11. Session Lifecycle & State Management

### SLC-027 Session `created` State Has No Maximum TTL [Medium]
**Section:** 15.1, 7.1

A session in `created` state holds a claimed pod and credential lease indefinitely. No `maxCreatedStateTimeoutSeconds` exists. Stalled sessions silently exhaust warm pool and credential pool at scale.

**Recommendation:** Define `maxCreatedStateTimeoutSeconds` (default 300s). On expiry, release pod and credential, transition to `expired`. Clarify whether `created`-state sessions count against concurrency quotas.

---

### SLC-028 `resuming` Timeout and `coordinatorHoldTimeoutSeconds` Gap [Medium]
**Section:** 6.2, 10.1

The pod self-terminates at 120s hold but the gateway's 300s resuming watchdog hasn't fired yet, leaving a ~180s window where the pod is dead but the session appears `resuming`. No mechanism notifies the gateway of orphan pod self-termination.

**Recommendation:** Specify that on hold timeout, the adapter writes `Sandbox.status.phase = failed` to the CRD before exiting. The gateway's pod health monitoring detects this and fires `resuming → resume_pending` independently of the 300s watchdog.

---

## 10. Recursive Delegation & Task Trees

### DEL-022 `maxTreeRecoverySeconds` Default Shorter Than `maxResumeWindowSeconds` [Medium]
**Section:** 8.10, 7.3

Tree recovery timeout (600s) is shorter than individual resume window (900s). Leaf nodes are force-terminated at 600s even though their own policy allows 900s. The existing deep-tree formula doesn't account for `maxResumeWindowSeconds` at leaf level.

**Recommendation:** Extend the formula: `maxTreeRecoverySeconds ≥ maxResumeWindowSeconds + (maxDepth - 1) × maxLevelRecoverySeconds`. Add a note that the default 600s intentionally truncates leaf windows for bounded worst-case recovery.

---

### DEL-023 No Cycle Detection in Delegation Target Resolution [Medium]
**Section:** 8.2, 8.3

`maxDepth` prevents infinite depth but not graph cycles (A→B→A). `lenny/discover_agents` doesn't filter out ancestors. Cross-tree cycles create undetected deadlocks not covered by the §8.8 subtree deadlock detector.

**Recommendation:** Record full session lineage in the delegation lease. Reject targets whose `session_id` appears in the caller's lineage with `DELEGATION_CYCLE_DETECTED`. Add a note that the deadlock detector covers subtree cycles, not cross-tree.

---

### DEL-024 Detached Orphan Pods Not Counted Toward Concurrency Quota [Medium]
**Section:** 8.10

Orphaned detached pods run for up to `cascadeTimeoutSeconds` (3600s) without quota accounting. No `maxOrphanTasksPerTenant` limit exists. A malicious orchestrator can accumulate unbounded orphan pods.

**Recommendation:** Add `maxOrphanTasksPerTenant` (default 100). If exceeded, `detach` falls back to `cancel_all`. Add `lenny_orphan_tasks_active_per_tenant` gauge and `OrphanTasksPerTenantHigh` alert.

---

## 23. Messaging, Conversational Patterns & Multi-Turn

### MSG-024 `ready_for_input` Signal Undefined for Concurrent Tool Execution [Medium]
**Section:** 7.2

When a runtime executes multiple tool calls simultaneously, `ready_for_input` is undefined. Different adapters will emit it at different points, creating non-deterministic path 2/3 routing.

**Recommendation:** Define normatively: adapter MUST emit `ready_for_input` only when the runtime has no in-flight tool calls AND is actively polling stdin. During concurrent tool calls, messages route to path 3 (inbox buffer).

---

### MSG-025 `await_children(mode: any)` Cascade on Parent Completion Unspecified [Medium]
**Section:** 8.8, 8.5

§8.10 defines `cascadeOnFailure` for parent failure, but not for parent completion. A parent that completes after `await_children(mode="any")` may leave siblings running indefinitely.

**Recommendation:** Specify that `cascadeOnFailure` applies on all parent terminal states (completed, failed, cancelled, expired), not just failure. Document that `detach` allows children to outlive the parent.

---

### MSG-026 Sibling Membership Instability Not Documented as Limitation [Medium]
**Section:** 7.2

`get_task_tree` is a snapshot. Siblings spawned after enumeration are missed with no notification. No `sibling_joined` event or `subscribe_task_tree` streaming call exists. Agent authors building dynamic teams will discover this at runtime.

**Recommendation:** Add a normative statement documenting snapshot semantics as a v1 limitation. Recommend coordinator-hub patterns for dynamic fan-out. Note that `sibling_joined` notification is deferred to post-v1.

---

## 7. Operator & Deployer Experience

### OPS-024 `lenny-ctl` Command Surface Undocumented [Medium]
**Section:** Throughout

12+ `lenny-ctl` subcommands are referenced across the spec but no consolidated command reference exists. Operators must hunt across 8,000+ lines to discover available commands.

**Recommendation:** Add a `lenny-ctl` command reference appendix listing each command group, sub-commands, required flags, API endpoint mapping, and minimum role. Cross-reference from §17.7 runbooks.

---

### OPS-025 Scale-to-Zero Cron Timezone Unspecified [Medium]
**Section:** 5.2

Cron expressions for `scaleToZero` don't specify whether they're UTC, cluster-local, or configurable. Operators in non-UTC timezones will misconfigure scale-to-zero windows.

**Recommendation:** Specify UTC default. Add optional `timezone` field (IANA string). Document minimum K8s 1.27 for native timezone support.

---

### OPS-026 cert-manager Minimum Version Not Specified [Medium]
**Section:** 10.3, 17.6

No minimum cert-manager version, required CRDs, or install ordering documented. An incompatible cert-manager passes CRD existence checks but fails at runtime with schema mismatches.

**Recommendation:** Add minimum version `v1.12.0`. Add cert-manager webhook health check to preflight. Document as optional Helm dependency.

---

## 9. Storage Architecture & Data Management

### STR-021 `last_message_context` 64KB TEXT Triggers TOAST [Medium]
**Section:** 4.4, 12.1

The `session_eviction_state.last_message_context` field (64KB TEXT) consistently triggers TOAST storage, contradicting the "never Postgres for blobs" principle at §12.1.

**Recommendation:** Cap in-Postgres storage at 2KB. Store full context as a MinIO object when exceeding 2KB, with the Postgres row holding only the MinIO key. Truncate to 2KB on MinIO unavailability with `context_truncated: true`.

---

### STR-022 GC Cycle Interval Tier-Configurability Incomplete [Medium]
**Section:** 12.5, 17.8

§17.8 shows Tier 3 GC at 5min, but the interval is not exposed as a Helm value. T4 tenants with 1-hour erasure SLAs have no sub-5-minute per-tenant priority path.

**Recommendation:** Expose `gc.cycleIntervalSeconds` as a Helm value (default 900, min 60). Add per-tenant `gcPriority: high` flag that triggers immediate GC sweep when an erasure job completes.

---

### STR-023 Custom Semantic Cache Implementations Have No Runtime Tenant Enforcement [Medium]
**Section:** 4.9, 9.4

The gateway does not proxy or wrap calls to third-party cache backends. A buggy custom implementation can silently serve cross-tenant cache hits. The contract test is development-time only.

**Recommendation:** Introduce a `TenantScopedSemanticCacheWrapper` in the gateway that prepends `tenant_id` to every cache key before delegating to the backend, making cross-tenant access structurally impossible.

---

## 8. Multi-Tenancy & Tenant Isolation

### TNT-019 `session_eviction_state` Has No `tenant_id` Column or RLS Policy [Medium]
**Section:** 4.4, 12.3

The table schema has no `tenant_id` column. It is absent from the RLS-protected tables list and the `lenny_tenant_guard` trigger scope. The table stores T3 Confidential data (`last_message_context`).

**Recommendation:** Add `tenant_id` column. Include in RLS policy scope and `TestRLSTenantGuardMissingSetLocal` coverage.

---

### TNT-020 Detached Orphan Sessions Exempt from Quota Enforcement [Medium]
**Section:** 8.10

Explicitly stated: "Detached orphan pods are not counted toward the originating user's concurrency quota." With `maxParallelChildren: 50` and `cascadeTimeoutSeconds: 3600`, a single tenant can generate 50×N unquoted pods.

**Recommendation:** Count detached children toward tenant concurrency quota during the detached window. Add `maxDetachedSessionsPerTenant` limit.

---

### TNT-021 Tenant Deletion Has No SLA or Overdue Alert [Medium]
**Section:** 12.8

The deletion lifecycle has 6 phases but no wall-clock SLA and no `TenantDeletionOverdue` alert. An unbounded deletion window is a compliance exposure for T4 tenants.

**Recommendation:** Define SLA (T3: 72h, T4: 4h). Add `TenantDeletionOverdue` warning alert. Add `lenny_tenant_deletion_duration_seconds` histogram.

---

## 12. Observability & Operational Monitoring

### OBS-027 Budget Operation OTel Spans Absent [Medium]
**Section:** 8.3, 16.3

`budget_reserve.lua` and `budget_return.lua` have no OTel spans in §16.3. Lua serialization contention is invisible in traces, making the SCL-016 contention analysis unobservable.

**Recommendation:** Add `delegation.budget_reserve` and `delegation.budget_return` spans with `outcome`, `tenant_id`, `root_session_id`, `lua_queue_wait_ms` attributes.

---

### OBS-028 Metric Name Inconsistency for Pod Claim Wait Time [Medium]
**Section:** 16.1, 17.8.2

`lenny_pod_claim_queue_wait_seconds` in §16.1 vs `lenny_warmpool_claim_wait_seconds_p99` in §17.8.2. Operators will query a metric that doesn't match the canonical table.

**Recommendation:** Designate `lenny_pod_claim_queue_wait_seconds` as canonical. Update §17.8.2 to reference it with a note that P99 is derived from the histogram.

---

### OBS-029 Memory Store Operation Metrics Absent [Medium]
**Section:** 9.4, 16.1

No metrics for `MemoryStore` operations (latency, errors, record count). Custom backends have no instrumentation contract. Degradation is invisible.

**Recommendation:** Add `lenny_memory_store_operation_duration_seconds`, `lenny_memory_store_errors_total`, `lenny_memory_store_record_count` to §16.1. Require for all implementations via the contract helper.

---

### OBS-030 PgBouncer Alerts Missing from §16.5 [Medium]
**Section:** 12.3, 16.5

§12.3 specifies PgBouncer metrics but no alerts in §16.5. PgBouncer saturation manifests as session 503s with no distinguishable root cause.

**Recommendation:** Add `PgBouncerPoolSaturated` (Warning, `cl_waiting_time > 1s` for 60s) and `PgBouncerAllReplicasDown` (Critical). Cross-reference from §17.7 runbook.

---

## 19. Build Sequence & Implementation Risk

### BLD-022 Phase 16 After Phase 15 Despite PoolScalingController Needing Both [Medium]
**Section:** 18

Phase 16 (experiments) comes after Phase 15 (environments), but experiment variant routing has no per-environment scoping. An A/B experiment could route a user from a sandboxed-environment session to an unsandboxed variant pool.

**Recommendation:** Add a Phase 16 prerequisite: variant pools must satisfy the requesting session's environment `minIsolationProfile`. Or document the limitation that variant pools are not environment-scoped in v1.

---

## 20. Failure Modes & Resilience Engineering

### FLR-022 Dual-Store Forced Termination Event Delivery Gap [Medium]
**Section:** 10.1

If dual unavailability exceeds 60s, sessions are terminated but the `session.terminated` event is emitted only when Postgres recovers — possibly hours later. During this window, terminated sessions have no terminal record and quota counters are frozen.

**Recommendation:** Enqueue terminated sessions in an in-memory buffer, batch-write to Postgres on recovery. Add `lenny_dual_store_forced_terminations_total` counter. Clarify whether pods receive `terminate` RPCs or only the session record is updated.

---

## 16. Warm Pool & Pod Lifecycle Management

### WPL-022 Pool Fill Grace Period Not Applied During Experiment Re-Activation [Medium]
**Section:** 4.6.1, 16.5

`WarmPoolExhausted` grace period applies only on first pool creation. Re-activating a variant pool (`paused → active`) sets `minWarm` from 0 to positive but triggers no grace period, causing immediate `WarmPoolLow` alerts.

**Recommendation:** Reset `fill_grace_period_until` timestamp whenever `minWarm` transitions from 0 to positive, regardless of whether it's first-creation or re-activation.

---

## 17. Credential Management & Secret Handling

### CRD-022 `maxRotationsPerSession` Not Per-Provider [Medium]
**Section:** 4.9

The counter applies across all providers. If one noisy provider exhausts the budget (3 rotations), a healthy unused provider is also blocked. Operators cannot diagnose which provider caused exhaustion.

**Recommendation:** Track per-provider. Add `perProviderRotations` map alongside the global `maxRotationsPerSession`. Add `lenny_credential_rotation_budget_exhausted_total` counter labeled by `provider`.

---

## 25. Execution Modes & Concurrent Workloads

### EXM-026 Concurrent-Workspace Slot Retry Has No Atomic Reservation [Medium]
**Section:** 5.2

Two concurrent retries targeting the same pod can both see "1 slot available" and both be assigned, temporarily exceeding `maxConcurrent`. No atomic slot reservation (CAS) is specified for the retry path.

**Recommendation:** Use atomic Redis INCR with cap check (INCR only if result ≤ `maxConcurrent`). If CAS fails, fall through to another pod. Add `lenny_slot_assignment_conflict_total` counter.

---

## 21. Experimentation & A/B Testing Primitives

### EXP-022 Results API Cursor Leaks Cross-Variant Ordering Information [Medium]
**Section:** 10.7

The cursor encodes a raw `EvalResult` primary key. Time-ordered UUIDs leak when evals were submitted across variants, potentially biasing manual experiment interpretation.

**Recommendation:** Encrypt cursors with a gateway-managed key (or use server-side cursor). Document that cursor values are opaque and non-stable.

---

## 22. Document Quality, Consistency & Completeness

### DOC-023 §17.8 Referenced 30+ Times But Content Partially Specified [Medium]
**Section:** 17.8

§17.8 is cited for per-tier HPA configs, `minReplicas`, `minWarm`, controller tuning, and `terminationGracePeriodSeconds`. Many promised values are absent. Readers following cross-references reach an incomplete section.

**Recommendation:** Either populate §17.8 with promised per-tier tables (as concrete or provisional values) or change cross-references to say "see Section 17.8 (Phase 2 deliverable)" to make incomplete state transparent.

---

## 15. Competitive Positioning & Open Source Strategy

### CPS-021 §23.1 MCP Tasks Differentiator Needs Internal vs External Clarification [Medium]
**Section:** 23.1

§23.1 positions MCP Tasks as a key differentiator but the internal delegation machinery uses custom gRPC, not MCP. Potential adopters may overestimate MCP end-to-end integration.

**Recommendation:** Add one sentence in §23.1: "Lenny implements MCP Tasks at the gateway's external MCP interface; internal delegation uses a custom gRPC protocol with equivalent semantics (see §4.7 and §9)."
