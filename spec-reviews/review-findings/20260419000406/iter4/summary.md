# Technical Design Review Findings — 2026-04-20 (Iteration 4)

**Document reviewed:** `spec/`
**Review framework:** `spec-reviews/review-povs.md`
**Iteration:** 4 of N (continuing until 0 Critical/High/Medium findings remain)
**Total findings:** 171 across 26 review perspectives

## Findings Summary

| Severity | Count |
| -------- | ----- |
| Critical | 0     |
| High     | 26    |
| Medium   | 74    |
| Low      | 71    |
| Info     | 0     |

### Critical Findings

No Critical findings in this iteration.

### High Findings

| #   | Perspective | Finding | Section |
| --- | ----------- | ------- | ------- |
| K8S-040 | Kubernetes API & Controller Design | `lenny-drain-readiness` Phase 8 build-sequence omission | 18 (Phase 3.5, Phase 8), 17.2 (item 11) |
| SEC-008 | Security & Cryptography | Upload security controls under-specified against zip-bomb, symlink, and traversal threats | (unspecified) |
| SEC-009 | Security & Cryptography | Exported workspace files bypass contentPolicy.interceptorRef, enabling delegation-mediated prompt injection | (unspecified) |
| SEC-010 | Security & Cryptography | Trust-based chained-interceptor exception permits content-policy downgrade across delegation | (unspecified) |
| NET-061 | Network Security & Isolation | `lenny-ops-egress` storage/monitoring rules lack pod selectors | 25.10 |
| NET-062 | Network Security & Isolation | IPv6 CIDRs mixed inside IPv4 `ipBlock` `except` list — invalid NetworkPolicy shape | 13.6, 25.10 |
| NET-063 | Network Security & Isolation | Gateway↔interceptor hop lacks mTLS mandate and peer-identity validation | 10.3, 13.5 |
| PRF-006 | Scalability & Performance Engineering | PDB `maxUnavailable: 1` Blocks HPA Scale-Down Throughput at Tier 3 | spec/17_deployment-topology.md §17.1 (PDB), §17.8.2 (HPA scale-down policy + scale-down time table) |
| PRF-007 | Scalability & Performance Engineering | Tier 3 MinIO Burst Throughput Target Unachievable with Recommended Topology | spec/17_deployment-topology.md §17.8.2 (Tier 3 storage table + 8-node narrative) |
| DXP-009 | Developer Experience & SDK | §15.7 Protocol codec description contradicts the stdin/stdout + Unix-socket contract | 15.7 |
| DXP-010 | Developer Experience & SDK | §15.7 lists MCP helper tools that aren't in the platform MCP tool set | 15.7, 4.7 |
| DXP-011 | Developer Experience & SDK | `integrationLevel` Runtime field is used by §17.4 / §15.4.6 but undefined in §5.1 | 5.1, 15.4.6, 17.4 |
| DXP-012 | Developer Experience & SDK | `lenny image import` and `lenny token print` are undocumented commands used in the primary-path walkthrough | 17.4, 24 |
| OBS-022 | Observability & Metrics | `billing_write_ahead_buffer_utilization` missing `lenny_` prefix | §16.1 (line 142), §16.5 (line 417), §12.3 (line 132) |
| OBS-023 | Observability & Metrics | §25.13 tier-aware defaults still reference alerts that don't exist in §16.5 | §25.13 (lines 4540–4541), §16.5 |
| CMP-049 | Compliance & Legal | DeleteByUser sequence has no legal-hold preflight | spec/12_storage-architecture.md §12.8 (DeleteByUser, lines 778-799) |
| API-010 | API & Wire-Contract Review | — `CREDENTIAL_SECRET_RBAC_MISSING`, `GIT_CLONE_AUTH_UNSUPPORTED_HOST`, `GIT_CLONE_AUTH_HOST_AMBIGUOUS` use HTTP 400 for server-state rejections | (unspecified) |
| CPS-006 | Checkpoint & Partial Manifest | Orphaned partial-chunk objects when gateway crashes between chunk commit and manifest write | 10.1 Partial manifest on checkpoint timeout; 12.5 GC backstop |
| WPL-001 | Warm Pool Lifecycle | Schedulability precondition missing on scrub_warning cleanup transition | spec/06_warm-pod-model.md §6.3 (state diagram), iter3 EXM-008 |
| WPL-002 | Warm Pool Lifecycle | Gateway lacks Node RBAC + informer to evaluate schedulability precondition | spec/06_warm-pod-model.md §6.3 ("Host-node schedulability precondition"), spec/04_system-components.md §4.6.3 (Gateway ServiceAccount), iter3 EXM-008 |
| CNT-007 | Workspace Plan & Content Handling | `gitClone` SSH URL contract unresolved after CNT-005 host-agnostic generalization | 14 (line 91 `gitClone` row; line 93 `gitClone.auth` paragraph), 26.2 (line 202) |
| BLD-009 | Build Sequence | Phase 8 checkpoint/resume does not deploy `lenny-drain-readiness` webhook | spec/18_build-sequence.md (Phase 8), spec/17_deployment-topology.md §17.2 entry #11, line 57 preflight enumeration |
| BLD-011 | Build Sequence | Phase-aware `lenny-preflight` enumeration still unaddressed; pre-Phase-13 installs fail-closed against hard-coded expected set | spec/18_build-sequence.md (Phase 3.5, Phase 5.8, Phase 8, Phase 13); spec/17_deployment-topology.md §17.2 line 57 (`lenny-preflight` expected-set), §17.9 "Checks performed" row 477 (Admission webhook inventory) |
| FLR-012 | Failure Modes & Recovery | Fresh-session (no prior checkpoint) preStop cap is inconsistent between Postgres-healthy and Postgres-unhealthy paths | 10.1 (`10_gateway-internals.md:108`, `10_gateway-internals.md:110`, `10_gateway-internals.md:32`) |
| MSG-011 | Messaging & Events | Durable inbox per-message TTL applies unconditionally and will expire messages during active long sessions | spec/07_session-lifecycle.md §7.2 lines 266, 268 |
| WPP-010 | Web Playground | Gateway startup fails before `lenny-bootstrap` seeds the `default` tenant when `authMode=dev` | 27.3 (line 53); 17.6 (lenny-bootstrap Job, line 382). |

---

## Detailed Findings by Perspective

---

## 1. Kubernetes API & Controller Design

### K8S-040. `lenny-drain-readiness` Phase 8 build-sequence omission [High]

**Status:** Fixed

**Section:** 18 (Phase 3.5, Phase 8), 17.2 (item 11)

Phase 3.5's admission-plane deployment table explicitly states that item 11 (`lenny-drain-readiness`) "is deferred to later phases (Phase 5.8, Phase 13, Phase 13, and Phase 8 respectively) where their gated feature lands." Phase 5.8 and Phase 13 include detailed "Admission control enforcement — deploy X" blocks that bind the deferred webhooks to their HA contract, the `lenny-preflight` inventory check, and the `admission_webhook_inventory_test.go` suite. Phase 8 contains only: "Checkpoint/resume + artifact seal-and-export" with no analogous deploy-and-bind block for `lenny-drain-readiness`. Consequently, an implementer following the build sequence literally will complete Phase 8 without deploying the webhook — yet Phase 8 is the last phase before session recovery tests rely on evictions, and the `lenny-preflight` Job (§17.2 line 57) will FAIL the install/upgrade if the webhook is missing. This creates a guaranteed Phase-8 → Phase-9 gating failure that is documented in Phase 3.5's deferral list but undocumented in the Phase 8 row itself.

**Recommendation:** Add an explicit "Admission control enforcement — deploy `lenny-drain-readiness`" block to the Phase 8 row in §18, mirroring the prose from Phase 5.8 and Phase 13: specify `replicas: 2`, `podDisruptionBudget.minAvailable: 1`, `failurePolicy: Fail`, the `lenny.dev/component: admission-webhook` pod label, the `DrainReadinessWebhookUnavailable` alert wire-up, and the inclusion of the webhook in both the `lenny-preflight` inventory check and the `admission_webhook_inventory_test.go` integration suite from this phase onward.

**Fix applied:** Added the "Admission control enforcement — deploy `lenny-drain-readiness`" block to the Phase 8 row in `spec/18_build-sequence.md`, mirroring the Phase 5.8 and Phase 13 prose pattern. The block specifies the uniform HA contract (`replicas: 2`, `podDisruptionBudget.minAvailable: 1`, `failurePolicy: Fail`, `lenny.dev/component: admission-webhook` pod label), wires the `DrainReadinessWebhookUnavailable` alert per §16.5, and asserts that both the `lenny-preflight` check and the `admission_webhook_inventory_test.go` integration suite must recognise the webhook as present once Phase 8 completes. The Phase 8 milestone was extended with "pre-drain MinIO health check admission gate enforced" to reflect the added deliverable.

### K8S-041. Admission-webhook NetworkPolicy row over-grants gateway internal-port egress to all eight webhook Deployments [Medium] — Fixed

**Section:** 13.2 (Admission Webhooks row in `lenny-system` component-specific allow-lists)

**Fix applied:** Split the §13.2 admission-webhook NetworkPolicy row's egress cell into two sub-rules: (a) a base egress rule selecting all eight webhook pods via `lenny.dev/component: admission-webhook` for `kube-system` CoreDNS only, and (b) a narrower egress rule selecting only the drain-readiness pods via the canonical label combined with an additive per-pod key `lenny.dev/webhook-name: drain-readiness` for Gateway internal-port egress (TCP `{{ .Values.gateway.internalPort }}`, default 8080). Updated the NET-047/NET-050 normative selector invariant (line 203) to add "Exception 2 (NET-068)" explicitly permitting additive per-pod keys in egress allow-lists (never ingress) when paired with the canonical `lenny.dev/component` key, and extended the `lenny-preflight` selector-consistency audit rules to (a) allow additive egress labels only, (b) flag additive keys on the ingress side as failures. Updated §17.2's admission-webhook label-requirement paragraph to document the additive `lenny.dev/webhook-name: drain-readiness` label (applied only to the drain-readiness Deployment), its egress-only scope, and the lint-level preflight check on unreferenced additive labels.

The NetworkPolicy row for "Admission Webhooks" in §13.2 uses a single `podSelector: lenny.dev/component: admission-webhook` and grants Egress to "Gateway internal HTTP port (TCP `{{ .Values.gateway.internalPort }}` — default 8080) for the `lenny-drain-readiness` webhook to call `GET /internal/drain-readiness`". This selector resolves to all eight webhook Deployments (`lenny-label-immutability`, `lenny-direct-mode-isolation`, `lenny-sandboxclaim-guard`, `lenny-data-residency-validator`, `lenny-pool-config-validator`, `lenny-t4-node-isolation`, `lenny-drain-readiness`, `lenny-crd-conversion`) because §17.2 requires all eight to carry the canonical `lenny.dev/component: admission-webhook` pod label, and the NET-047/NET-050 selector-consistency audit (line 203) forbids per-webhook label variants. Only `lenny-drain-readiness` legitimately needs this egress; the other seven webhooks are purely in-process validators (schema/immutability/authorization checks) that never call back into the gateway. This violates least privilege — a compromised non-drain webhook gains unrestricted access to every endpoint on the gateway's internal admin port, which is where `/internal/drain-readiness` lives alongside other internal-only endpoints (see §25.3 endpoint split).

**Recommendation:** Split the admission-webhook NetworkPolicy row into two rules: (a) the base row keeps `kube-system` CoreDNS egress for all eight webhooks (DNS lookups are required for kube-apiserver watch connections); (b) a second, narrower egress rule selects ONLY the drain-readiness Deployment via an additional, per-webhook label (e.g., `lenny.dev/webhook-name: drain-readiness` applied only to that Deployment's pods alongside the canonical `lenny.dev/component: admission-webhook` label). Update §17.2's canonical-selector invariant text to explicitly permit additive, webhook-specific labels for egress-narrowing, and update the `lenny-preflight` selector-consistency audit to treat these additive labels as an allowed exception when they appear in egress allow-lists (never in ingress, where the canonical selector must remain).

### K8S-042. `lenny-sandboxclaim-guard` PATCH/PUT rule examines `SandboxClaim.status.phase`, but the documented state machine lives on `Sandbox` [Medium] — **Fixed**

**Section:** 4.6.1 (`SandboxClaim` admission webhook), 6.2 (State storage table)

§4.6.1 specifies: "For `PATCH`/`PUT`, it rejects the request if the **existing** resource's `.status.phase` (as persisted in etcd) is not `idle` — i.e., the target `Sandbox` already has an active claim. Specifically: if the incoming operation attempts to transition `.status.phase` from any non-`idle` value, the webhook returns `403 Forbidden`". But §6.2 explicitly documents that the authoritative state machine (`idle`, `active`, `draining`, `claimed`, `running`, `terminated`, `failed`) lives in the **`Sandbox` CRD `.status.phase`**, not in `SandboxClaim.status.phase`. The `SandboxClaim` CRD represents a binding between a `Sandbox` and a session — its `.status` would carry the session's binding state, not the pod's warm-pool state. A newly-created `SandboxClaim` is never in `idle` phase because `SandboxClaim` existence implies an active claim. The webhook rule as written would either (a) reject every `SandboxClaim` PATCH/PUT (since `.status.phase` on `SandboxClaim` starts unset or non-`idle`), blocking normal session lifecycle transitions, or (b) rely on reading `.status.phase` from the wrong CRD. Additionally, the `PATCH`/`PUT` rule conflates the admission-level "is this claim valid" check with the session lifecycle transitions that the gateway itself drives on `SandboxClaim` (bind → active → released).

**Recommendation:** Clarify in §4.6.1 whether the webhook reads `.status.phase` from the `SandboxClaim` being modified OR from the referenced `Sandbox` (via `SandboxClaim.spec.sandboxRef` → `Sandbox.status.phase`). Given the purpose (double-claim prevention), the check must read the referenced `Sandbox.status.phase` — not the `SandboxClaim`'s own status. Rewrite the paragraph as: "For `PATCH`/`PUT`, the webhook queries the API server for the referenced `Sandbox` (via `.spec.sandboxRef`) and rejects the request if the `Sandbox.status.phase` is not `claimed` (i.e., the target pod no longer holds an active claim to this SandboxClaim)." Also define the legal `SandboxClaim.status.phase` enumeration explicitly in §4.6.3 CRD field ownership, since gateway is documented as the sole writer of `SandboxClaim` `spec` and `status` but the allowed phase values are never enumerated.

### K8S-043. PSC circuit-breaker state (`spec.sdkWarmDisabled`) has no leader-handoff continuity guarantee [Medium]

**Status:** Fixed

**Section:** 4.6.2, 4.6.3, 6.1 (Circuit-breaker for SDK-warm pools)

§6.1 specifies the demotion-rate circuit breaker: "If the rolling 5-minute demotion rate exceeds 90%... the PoolScalingController automatically disables SDK-warm for the pool by setting `spec.sdkWarmDisabled: true` on the `SandboxWarmPool` CRD". §4.6.3 confirms `spec.sdkWarmDisabled` is PSC-owned via SSA. However: (a) the 5-minute rolling window is computed from in-memory counters (`lenny_warmpool_sdk_demotions_total` / `lenny_warmpool_claims_total`); (b) on PSC leader failover (up to 25s on crash — §4.6.1), the new leader inherits neither the in-memory rolling counter nor the window state; (c) §6.1 operator unblock uses `{"sdkWarm": {"circuitBreakerOverride": "enabled"}}` via admin API — but there is no specified TTL or auto-reset on `spec.sdkWarmDisabled: true`, so once set by a prior leader, it persists indefinitely until operator intervention. Consequently, a PSC leader that trips the breaker at 10:00:00 and crashes at 10:00:30 hands over to a new leader that sees `spec.sdkWarmDisabled: true` with no context — the new leader cannot tell whether the breaker was spurious (brief spike) or confirmed (sustained 90%+ demotion). Conversely, a breaker that SHOULD have tripped at 10:00:30 is now not trippable because the new leader's rolling window starts from zero and will take another 5 minutes to accumulate enough signal.

**Recommendation:** Add an explicit circuit-breaker state persistence contract in §4.6.2 and §4.6.3: (a) the PSC writes the rolling window's aggregate state (counter values + window start timestamp) to the `SandboxWarmPool.status.sdkWarmCircuitBreaker` subresource on every spec reconciliation; (b) on leader startup, the new PSC leader reads this subresource and resumes the window from the persisted counters rather than starting from zero; (c) `spec.sdkWarmDisabled: true` carries an operator-set or PSC-generated `sdkWarmCircuitBreakerOpenedAt` timestamp in `.status` so the new leader can decide whether to keep the breaker open based on a configurable grace window (e.g., "the breaker remains open for at least N minutes after tripping, regardless of leader changes"). Alternatively, document explicitly that the PSC trades circuit-breaker accuracy for simpler failover semantics — but then the operator-facing guidance in §6.1 must note the stale-state window.

**Fix applied:** Added a "Circuit-breaker state persistence across PSC leader failover" contract in `spec/06_warm-pod-model.md` §6.1 that introduces three PSC-owned fields on `SandboxWarmPool.status.sdkWarmCircuitBreaker` — `openedAt`, `openedReason`, and `minOpenUntil` (= `openedAt + sdkWarmCircuitBreakerMinOpenDuration`, default 30 min, operator-configurable via `scalingPolicy.sdkWarmCircuitBreakerMinOpenSeconds`). The PSC writes all three in the same SSA apply that sets `spec.sdkWarmDisabled: true`. On leader startup, the new PSC keeps the breaker open until `minOpenUntil` elapses, preventing a crash shortly after trip from silently resetting protection. Rolling-window counters are intentionally **not** persisted on every reconcile (grace window masks the window cold-start; accepting minor accuracy loss for simpler failover). The operator override (`circuitBreakerOverride: "enabled"`) unconditionally clears the breaker and status fields regardless of `minOpenUntil`. Updated `spec/04_system-components.md` §4.6.3 ownership table with a carve-out row for `status.sdkWarmCircuitBreaker.*` (PSC-owned) plus an updated row excluding that subpath from the WPC-owned `status.*`, and added `get`/`patch` on the `SandboxWarmPool` status subresource to the PSC's RBAC grants. Also added a new responsibility bullet to §4.6.2 referencing the persistence contract.

### K8S-044. Fallback claim path creates a `SandboxClaim` with stale `agent_pod_state` mirror data [Medium] — **Fixed**

**Section:** 4.6.1 (Fallback claim path via Postgres), 12.3 (`agent_pod_state` table), 4.6.3 (CRD Field Ownership)

**Fix applied:** Added two new paragraphs after the "Fallback claim path via Postgres" paragraph in `spec/04_system-components.md` §4.6.1. The first ("Fallback preconditions (mirror freshness and admission reachability)") gates fallback activation on two checks: (1) `lenny_agent_pod_state_mirror_lag_seconds` for the pool is at or below `podClaimFallbackMaxMirrorLagSeconds` (default 10s, gateway-flag configurable) — otherwise the mirror may point at pods already claimed in etcd; (2) a lightweight API-server readiness probe succeeds, since the `lenny-sandboxclaim-guard` CREATE webhook runs `failurePolicy: Fail` and would otherwise collapse the fallback. If either precondition fails, the gateway returns `WARM_POOL_EXHAUSTED` without issuing the Postgres `SELECT FOR UPDATE SKIP LOCKED`. Added a `lenny_pod_claim_fallback_skipped_total{reason=mirror_stale|apiserver_unreachable}` counter. The second paragraph ("WarmPoolController mirror reconciliation on recovery") specifies that on WPC startup and every leader-election acquisition, the controller bulk-UPSERTs all `agent_pod_state` rows from a fresh `Sandbox` list and deletes rows with no corresponding `Sandbox`, establishing a post-outage convergence invariant separate from the existing in-flight drain reconciliation. Until that reconciliation completes, the staleness gauge continues to reflect pre-recovery lag, so the preconditions above naturally keep the fallback disabled.

§4.6.1 specifies that when API-server claim exhausts `podClaimQueueTimeout`, the gateway falls back to `SELECT ... FOR UPDATE SKIP LOCKED` on `agent_pod_state`, then creates a `SandboxClaim` CRD. §12.3 documents `agent_pod_state` as a "read-optimized mirror maintained by the WarmPoolController on every pod state transition" with a `lenny_agent_pod_state_mirror_lag_seconds` staleness gauge. Two failure modes:

1. **WPC down during API server outage**: if both the API server AND the WPC are down (e.g., etcd outage that takes out both Kubernetes and the WPC's ability to write Postgres mirror updates), the Postgres mirror becomes stale — the fallback path reads `state = idle` for a pod that has actually been claimed and written in etcd but not yet mirrored. The gateway creates a duplicate `SandboxClaim` against that pod.
2. **Mirror lag window**: even when the WPC is healthy, §12.3 documents a bounded staleness window (`lenny_agent_pod_state_mirror_lag_seconds`) but specifies no upper bound. If this lag exceeds a threshold, the `SELECT FOR UPDATE SKIP LOCKED` acquires a Postgres-level row-lock on a pod that may have been claimed in etcd milliseconds ago.

The `lenny-sandboxclaim-guard` CREATE-time check partially mitigates this: it "queries the API server for any existing `SandboxClaim` whose `.spec.sandboxRef` matches the target `Sandbox`". But the API server is the condition that triggered fallback in the first place — if it's unreachable, the webhook's fallback behavior is undefined (§4.6.1 says the webhook is `failurePolicy: Fail`, so the CREATE is rejected, collapsing the entire fallback path).

**Recommendation:** §4.6.1 should specify: (a) a maximum allowed `lenny_agent_pod_state_mirror_lag_seconds` threshold (e.g., 10s) above which the fallback path is disabled; the gateway returns `WARM_POOL_EXHAUSTED` instead of risking a stale-mirror claim; (b) an explicit statement that the Postgres fallback requires BOTH mirror freshness AND API server reachability (for the sandboxclaim-guard webhook callback) — if either is missing, the fallback is skipped; (c) a reconciliation invariant: on WPC recovery from an outage, the WPC re-lists all `Sandbox` resources, re-computes `agent_pod_state` rows, and resets the `updated_at` column — document this in §4.6.1's "Controller crash during active scale-down drain" paragraph which currently only addresses the draining phase, not the steady-state mirror consistency.

### K8S-045. `lenny-pool-config-validator` rule set 1 can wedge PSC reconciliation under stale-Postgres conditions [Medium] [FIXED]

**Section:** 4.6.3 (Validating webhook for Postgres-authoritative state)

§4.6.3 specifies that the `lenny-pool-config-validator` webhook enforces two orthogonal rule sets on every `SandboxTemplate.spec`/`SandboxWarmPool.spec` write: (1) semantic/budget rules that "apply to ALL writes, including PoolScalingController SSA applies", and (2) authorization-based denial. Rule set 1 is explicitly NOT bypassable by the PSC's ServiceAccount — "PoolScalingController writes bypass only this second rule, not the first." The budget invariants reference §10.1 tiered-cap + `checkpointBarrierAckTimeoutSeconds` floor rules that depend on pool-level configuration (tier classification, grace periods, barrier timeouts). If Postgres returns inconsistent pool configuration (e.g., during a PgBouncer failover, a schema migration in progress, a Redis-cached stale read, or a tenant config that an admin has left in a transient invalid state), the PSC will compute a reconciled spec that violates rule set 1. The webhook rejects the PSC's apply. The PSC retries per §4.6.3 "SSA conflict retry policy (crash recovery)" paragraph 3 — but that policy specifically handles HTTP 409 Conflicts, not HTTP 400/403 admission denials. There is no specified backoff or abort path for repeated admission denials.

Consequently, under stale-Postgres conditions the PSC enters a tight reconciliation loop: read Postgres → compute invalid spec → apply → webhook denies → retry → read Postgres (still stale) → repeat. This exhausts the PSC's controller-runtime work queue and drives API server pressure. Additionally, the `PoolConfigValidatorUnavailable` Warning alert (§16.5) explicitly notes: "BOTH consequences apply simultaneously: ... (2) PoolScalingController SSA reconciliation writes are also denied because the semantic budget rules apply to every writer including the PSC" — confirming the wedging risk but not specifying how the controller handles it.

**Recommendation:** §4.6.3 must specify the PSC's admission-denial handling policy: (a) on HTTP 400/403 admission denial (distinguished by response body codes `POOL_CHECKPOINT_CAP_EXCEEDS_GRACE_BUDGET` or similar), the PSC must log the denial with full spec context, increment a `lenny_pool_scaling_admission_denied_total` counter labeled by pool and denial reason, and apply exponential backoff (initial 1s, max 60s) rather than tight-looping; (b) after N consecutive denials on the same pool (e.g., 10), emit a `PoolScalingAdmissionStuck` alert and stop retrying that pool until operator intervention; (c) add an explicit test-suite requirement in §18 that Phase 3.5's admission integration tests include a scenario where Postgres returns pool config that violates rule set 1, verifying the PSC does not wedge. Also clarify in §4.6.3 how the PSC distinguishes transient 400s (retry) from permanent 400s (back off).

### K8S-046. `lenny-preflight` is pre-install/pre-upgrade only and cannot detect post-rollback admission-plane drift [Low]

**Section:** 17.2 (Preflight enumeration check), 17.6 (Packaging)

§17.2 states: "The `lenny-preflight` Job ([§17.9] — `Checks performed`) enumerates the deployed `ValidatingWebhookConfiguration` and `CustomResourceDefinition.spec.conversion.webhook` resources in the target cluster and verifies that the full expected set is present before allowing the install or upgrade to proceed." The check is fail-closed and is documented as a Helm pre-install/pre-upgrade hook. However, this timing misses two post-install drift conditions: (1) a partial Helm rollback (e.g., `helm rollback` to a pre-Phase-8 chart version) where the chart no longer renders `lenny-drain-readiness` but the `ValidatingWebhookConfiguration` remains in the cluster as a dangling resource; (2) out-of-band modifications by a cluster administrator (e.g., `kubectl delete validatingwebhookconfiguration lenny-sandboxclaim-guard` to work around a perceived issue) that are not observable until the next install or upgrade runs. The `Admission webhook failure mode` paragraph's 99.9% availability SLO + per-webhook alerts (`SandboxClaimGuardUnavailable`, etc.) only fires when a webhook is deployed but unreachable — not when it has been deleted entirely. Prometheus `up{job="lenny-sandboxclaim-guard"}` returns no samples for a missing job, which typically does NOT trigger an `== 0` expression.

**Recommendation:** Add a continuous admission-plane inventory check to the `lenny-ops` or WarmPoolController reconciliation loop. §17.2 should specify: (a) a goroutine that runs every 5 minutes, lists all `ValidatingWebhookConfiguration` and CRD conversion webhook resources in the cluster, compares against the expected set, and emits `lenny_admission_webhook_missing_total` (counter, labeled by webhook name) on any missing entry; (b) an `AdmissionWebhookMissing` Critical alert that fires when this counter is non-zero for &gt; 60s — this alert fires on DELETE (absent webhook), whereas `*Unavailable` alerts fire on UNREACHABLE (present but down); (c) in §17.2's alert enumeration, call out explicitly that the `*Unavailable` family only covers reachability, not presence. Also clarify in §25 agent-operability section whether this detection is a `lenny-ops` responsibility or a WPC/PSC responsibility.

### K8S-047. `maxUnavailable: 1` warm-pool PDB omits Kata isolation scope [Low]

**Section:** 4.6.1 (Disruption protection for agent pods), 17.2 (Node isolation), 17.1

§4.6.1 specifies a single PDB per `SandboxTemplate` with `maxUnavailable: 1` targeted at `lenny.dev/state: idle` pods, rendered uniformly across all pools. §17.2 "Node isolation" requires Kata (`microvm`) pods to "run on dedicated node pools and **must** use hard scheduling constraints" with the taint `lenny.dev/isolation=kata:NoSchedule` and separate namespace `lenny-agents-kata`. But with a flat `maxUnavailable: 1` PDB per template, if a deployer has multiple Kata templates co-located on the same node pool (e.g., a small dedicated-Kata node pool with 3 nodes hosting 2 Kata templates, 5 warm pods each), a simultaneous voluntary disruption scenario (node drain of one Kata node) could evict one warm pod from each template — a total of 2 simultaneously unavailable pods on the same node pool, even though each template's PDB says only 1. This is by design of the Kubernetes PDB, but the spec's uniform `maxUnavailable: 1` across all templates does not address the cross-template disruption scenario specifically for hardware-isolated Kata pools, where losing 2+ Kata pods simultaneously would exhaust a small dedicated node pool's warm capacity and cause session-creation failures.

**Recommendation:** Add a Kata-specific clarification to §4.6.1 PDB paragraph: for pools where `isolationProfile: microvm`, the PDB must be rendered with a pool-aware selector AND the deployer should ensure that the number of Kata node pool nodes is sufficient to tolerate simultaneous drains across all co-located Kata templates. Either: (a) specify that all Kata templates co-located on the same node pool share a single PDB (selecting on `lenny.dev/isolation: microvm` rather than per-template), OR (b) document in §17.8 capacity planning that Kata node pool sizing must include headroom for `num_kata_templates × maxUnavailable` simultaneous disruptions. Also clarify in §4.6.1 whether the `ManagePDB` interface is called once per template or once per pool — the current text "optional create a PDB per `SandboxTemplate` for warm (unclaimed) pods" implies per-template, but the text "The PDB targets only unclaimed (warm) pods" implies the PDB selector is pool-scoped.

### K8S-048. Admission-webhook deployment HA contract doesn't specify topology spread across zones [Low]

**Section:** 17.2 (High-availability requirement), 17.3 (Cross-zone requirements)

§17.2 "High-availability requirement applies to every entry above" specifies: "`replicas: 2` + `podDisruptionBudget.minAvailable: 1`" for each of the 8 admission-webhook Deployments plus the CRD conversion webhook. But it does NOT specify `topologySpreadConstraints` or anti-affinity rules. If both replicas of a fail-closed webhook (e.g., `lenny-sandboxclaim-guard`) land on the same zone (or same node), a single zonal failure or node failure takes out 100% of the webhook's capacity — at which point `failurePolicy: Fail` blocks every `SandboxClaim` operation, halting session creation entirely. §17.3 "Cross-zone requirements" specifies topology spread for the gateway ("Gateway: replicas spread via topology spread constraints") but omits admission webhooks entirely. The webhook `replicas: 2` + `PDB minAvailable: 1` combination provides voluntary-disruption protection but NOT zonal-failure protection.

**Recommendation:** §17.2 should extend the HA contract to explicitly require `topologySpreadConstraints` with `topologyKey: topology.kubernetes.io/zone, maxSkew: 1, whenUnsatisfiable: DoNotSchedule` on every admission-webhook Deployment (all 8 + CRD conversion). §17.3 should add admission webhooks to the "Cross-zone requirements" bullet list. The `lenny-preflight` Job should be extended to verify that each webhook Deployment has the required topology spread constraint rendered — fail install if absent. Note that this also implies a minimum of 2 zones in the cluster; §17.5 or §17.8 should document this prerequisite.

### K8S-049. `Sandbox` finalizer removal path lacks a leader-election fence for the WPC GC loop [Low]

**Section:** 4.6.1 (Sandbox finalizers, Orphaned `SandboxClaim` detection)

§4.6.1 specifies: "every 60 seconds, the leader replica lists all `SandboxClaim` resources whose `metadata.creationTimestamp` is older than `claimOrphanTimeout`... For each candidate orphaned claim, the controller queries Postgres to check whether an active session references it. If no active session exists, the claim is deleted and the underlying `Sandbox` pod is transitioned back to `idle`, returning it to the warm pool." The same section earlier describes `Sandbox` finalizers: "When a `Sandbox` enters the `Terminating` state, the warm pool controller checks whether any active `SandboxClaim` still references the pod. It removes the finalizer only after confirming one of two conditions: (a) no session references the pod, or (b) the session has been successfully checkpointed."

These two loops both run on the WPC leader and interact: the GC loop deletes orphaned `SandboxClaim`s; the finalizer removal loop waits for no `SandboxClaim` to reference a `Terminating` pod. If a WPC leader failover occurs DURING an orphan GC operation (leader A has deleted a `SandboxClaim` but not yet transitioned the `Sandbox` back to `idle`, then leader A crashes), leader B inherits a pod that has no active claim but is still in `claimed` state. The state machine transitions documented in §6.2 do not include a `claimed → idle` path without explicit gateway release — yet here the orphan GC must trigger it. There is no specified idempotent recovery behavior: leader B's reconciliation pass may not detect that the pod needs to be transitioned back to `idle` (since there's no orphan claim to process anymore — leader A already deleted it). The pod is stuck in `claimed` state indefinitely with no active session, consuming warm-pool capacity.

**Recommendation:** §4.6.1 should specify an idempotent orphan-reclaim protocol: (a) the WPC's GarbageCollect loop must list both orphaned `SandboxClaim`s AND `Sandbox` resources in `claimed` state that have no matching `SandboxClaim`; the second list catches leader-failover-orphaned pods that lost their claim mid-cleanup; (b) for each such `Sandbox`, the WPC queries Postgres for an active session (same logic as orphan-claim path) and, if no session exists, transitions the pod back to `idle`; (c) add a `lenny_orphaned_sandbox_reclaim_total` counter labeled by pool, with a `SandboxOrphanReclaimRateHigh` alert that matches the existing `SandboxClaimOrphanRateHigh` pattern. This closes the leader-handoff window without requiring a generation-stamped operation record.

### K8S-050. `PoolScalingController` separate leader from WPC creates observer gap for `SandboxWarmPool.status` [Low]

**Section:** 4.6.2 (PoolScalingController leader election), 4.6.3 (CRD Field Ownership)

§4.6.2 specifies "The PoolScalingController runs its own Lease-based leader election using a separate lease name (`lenny-pool-scaling-controller`) from the WarmPoolController". §4.6.3 splits CRD field ownership: PSC owns `SandboxWarmPool.spec.{minWarm, maxWarm, scalePolicy, sdkWarmDisabled}`; WPC owns `SandboxWarmPool.status.*`. The PSC's reconciliation loop reads Postgres pool config and writes `SandboxWarmPool.spec` — but its decisions (e.g., `target_minWarm` formula in §4.6.2 using `base_demand_p95 × safety_factor × (failover_seconds + pod_startup_seconds) + burst_p99_claims × pod_warmup_seconds`) depend on observed runtime behavior that the WPC writes to `SandboxWarmPool.status`: ready count, warm count, conditions. If the WPC leader is down during the PSC's reconciliation tick, the PSC is making scaling decisions against stale status data — there is no specified staleness check on `SandboxWarmPool.status.observedGeneration` or `.status.lastTransitionTime` in the PSC's loop. The separate-leader design is documented as preventing blast-radius amplification, but the PSC's dependency on WPC-written status is not documented.

**Recommendation:** §4.6.2 should specify a staleness guard in the PSC's reconciliation loop: (a) read `SandboxWarmPool.status.lastTransitionTime` or `.status.observedGeneration`; (b) if staleness exceeds a threshold (e.g., 60s — well within the WPC's 25s failover + pod creation tick), skip PSC reconciliation for that pool and increment `lenny_pool_scaling_skipped_stale_status_total`; (c) emit a `PoolScalingStalePoolStatus` alert when this counter exceeds N in a window. Alternatively, document explicitly that the PSC's scaling decisions can tolerate up to 25s of stale WPC status (the WPC failover window) — but then the formula in §4.6.2 must account for this: `target_minWarm` should be over-provisioned by `safety_factor * 25s/pod_warmup_seconds` additional pods to absorb the stale-status window.

---

## 2. Security & Cryptography

### SEC-008. Upload security controls under-specified against zip-bomb, symlink, and traversal threats [High]

**Status:** Fixed

**Section:** spec/13_security-model.md §13.4; spec/07_session-lifecycle.md §7.4; spec/15_external-api-surface.md §15.1 (error reference); spec/16_observability.md §16.1; spec/08_recursive-delegation.md §8.7

Description: §13.4 Upload Security lists only six high-level bullets (staging path, type sniffing, size cap, promotion step, quarantine, audit). It does not specify numerical limits or algorithms for the well-known archive/upload threats called out in the review scope: (1) no maximum decompression ratio or absolute decompressed-size cap (zip-bomb); (2) no per-file-entry size limit inside archives; (3) no maximum entry count per archive; (4) no maximum path depth or path length; (5) symlink/hardlink handling during extraction is not defined (symlinks pointing outside the staging root, or to /proc, /dev, credential mounts, must be rejected, not followed); (6) zip-slip/path-traversal rejection is implied by "validated staging path" but not stated as a normative requirement with canonicalization. §13.5's acknowledgement that exported-file content is platform-unchecked compounds the exposure: a malicious parent can craft an archive that the child's runtime decompresses into the workspace before the child agent sees it. With adapter-agent boundaries running as the same UID (§4.7), an exploited decompressor directly owns the agent process.

Recommendation: Expand §13.4 with normative, non-tunable platform defaults: max decompressed size (e.g., 256 MiB), max decompression ratio (e.g., 100:1), max entry count (e.g., 10 000), max path depth (e.g., 32) and length (e.g., 4 096 B); reject any archive entry whose canonicalized path escapes the staging root, and reject symlink/hardlink/device/FIFO entries outright (extract as regular files or fail). State that decompression runs in the adapter's sandbox under the same isolation profile as the session, never in the gateway. Add a `UPLOAD_ARCHIVE_LIMIT_EXCEEDED` error with sub-reasons. Cross-reference from §8.7 so delegated file exports inherit the same validators.

**Fix applied:** Expanded §7.4 Upload Safety (the spec's authoritative upload section) to encode all missing normative archive-extraction validators as non-tunable platform ceilings: max decompressed size (256 MiB), max decompression ratio (100:1 — already present), max entry count (10 000), max per-entry size (64 MiB), max path depth (32 components), max path length (4 096 bytes UTF-8), and explicit rejection of `hardlink`/`character-device`/`block-device`/`FIFO`/`socket` entry types. Zip-slip canonicalization is now stated as a normative requirement (`filepath.Clean` + absolute-root prefix check), and the symlink opt-in path explicitly blocks traversal into `/proc`, `/sys`, `/dev`, and `/run/lenny`. §7.4 now also makes explicit that archive extraction runs inside the gateway's sandboxed Upload Handler subsystem (§4.1), never in agent pods — this is a deliberate departure from the review's adapter-side recommendation because moving decompression to the adapter would conflict with the gateway-mediated upload invariant in §4.1; the Upload Handler's isolated goroutine pool and circuit breaker already contain blast radius within the gateway. Rewrote §13.4 from six terse bullets into a normative summary listing every ceiling above with cross-references to §7.4, §8.7, §15.1, and §16.1. Added `UPLOAD_ARCHIVE_LIMIT_EXCEEDED` (HTTP 413, `PERMANENT`) to the §15.1 error reference with `details.reason` sub-codes (`max_decompressed_size`, `max_decompression_ratio`, `max_entry_count`, `max_entry_size`, `max_path_depth`, `max_path_length`, `path_escapes_root`, `non_regular_entry`, `symlink`). Extended the §16.1 `lenny_upload_extraction_aborted_total{error_type}` label enumeration to match those sub-codes. Added a §8.7 validation bullet stating that archive entries in exported files inherit the same §13.4/§7.4 validators when the child's gateway-mediated materialization unpacks them, so delegation file exports cannot bypass the upload pipeline's archive checks.

### SEC-009. Exported workspace files bypass contentPolicy.interceptorRef, enabling delegation-mediated prompt injection [High]

**Status:** Deferred - Input Required

**Section:** spec/08_recursive-delegation.md §8.7 (File Export Model); spec/13_security-model.md §13.5

Description: §8.7 states that files exported from a child workspace back to the parent are delivered as opaque bytes and are not subject to `contentPolicy.interceptorRef`. §13.5 openly labels this as residual risk. This is the canonical prompt-injection vector through delegation: a compromised or untrusted child (or a child whose own tools were prompt-injected by third-party content) can smuggle attacker-controlled text into the parent's conversation by writing it to an exported file that the parent then reads. Because the interceptor contract exists precisely to gate adversarial text reaching the LLM, leaving the file channel uncovered by default defeats the monotonicity guarantees §8.3 builds elsewhere. The spec does not offer an opt-in control (e.g., `contentPolicy.scanExportedFiles: true`), a size/extension whitelist default, or a mandatory metadata-only mode; parents have no platform-level mechanism to require scanning.

Recommendation: Introduce a `contentPolicy.exportedFileScanning` field (values `required`, `metadata-only`, `disabled`) with a platform default of `metadata-only` (reject binary executables, large archives by default; allow declared MIME types without content scan). When set to `required`, the gateway MUST route exported file content through the parent's declared interceptor (or a platform-default text-extraction+scan pipeline for common MIME types) before the parent can read the file. Apply the §8.3 restrictiveness rule to this new field so a child cannot weaken the parent's setting. Document the control in §13.5 and cross-reference from §8.7; remove the "residual risk" framing once the control exists.

**Deferral rationale.** Every candidate fix that closes the vector is a contract/architecture change requiring user direction before editing:

1. **Recommendation as written is partially incompatible with §22.3.** The recommendation's `metadata-only` platform default and "platform-default text-extraction+scan pipeline for common MIME types" introduce built-in content classification (MIME-sniffing, binary-executable detection, text extraction). §22.3 "No Built-In Guardrail Logic" normatively rejects exactly this — "Lenny never implements AI-specific logic (eval scoring, memory extraction, content classification)". Adopting this variant requires the user to override §22.3 or reshape the proposal.

2. **Minimum viable alternative (still a contract extension).** A simpler `contentPolicy.scanExportedFiles: bool` (default `false`; when `true`, the gateway routes each exported file through the parent's existing `interceptorRef` at a new `PreExportMaterialization` phase, subject to the §8.3 weakening rule) avoids any built-in classifier but still introduces: (a) a new field on the externally-visible `DelegationPolicy` schema, (b) a new `RequestInterceptor` gRPC phase (`PreExportMaterialization`) and payload shape (file bytes + metadata), (c) new §8.3 restrictiveness semantics for the new field, (d) operational concerns (file-size/timeout budgets on the interceptor call, failure-mode semantics that mirror `failPolicy`), (e) fit with §7.4 Upload Safety which already front-loads validator work on the upload pipeline.

3. **Documentation-only fix does not address the core complaint.** Strengthening §8.7/§13.5 warning language and pointing deployers at runtime-side mitigations (runtime-configured ignore-lists for `CLAUDE.md`-class instruction files; workspace-plan `inlineFile` interceptor hooks at the receiving session) leaves the finding's central point ("parents have no platform-level mechanism to require scanning") intact.

**Open questions for the user before implementation:**
- (a) Accept a new `RequestInterceptor` phase (`PreExportMaterialization`) and a new `contentPolicy.scanExportedFiles` boolean field, with the §8.3 weakening rule applied? — minimum-viable variant, no built-in classifier.
- (b) If yes to (a), define the interceptor payload shape: full file bytes up to `fileExportLimits.maxTotalSize` (100 MB default), or a streaming/chunked contract, or path + bytes reference via object storage?
- (c) Define failure-mode semantics: does an interceptor timeout/error on a per-file scan follow the `failPolicy` of the named interceptor (fail-closed → reject export, fail-open → admit)? Does this trigger the §8.3 rule-5 weakening cooldown?
- (d) Where does scan cost count: against the parent's `maxInputSize`-equivalent, against a new `contentPolicy.maxExportedFileSize`, or uncapped below the `fileExportLimits` ceiling?
- (e) Should the platform ship any `metadata-only` behavior at all (MIME-sniff reject executables), or leave that entirely to the deployer's interceptor implementation (consistent with §22.3)?

Given (a)-(e) are new concepts, new parameters, and a new interceptor contract — per the "present proposals before editing" principle — no spec edits are made in this iteration. Direction required from the user on (a) as the gating question; (b)-(e) follow once (a) is answered.

### SEC-010. Trust-based chained-interceptor exception permits content-policy downgrade across delegation [High]

**Section:** (unspecified)

**Status:** Fixed (iter4)

Section: spec/08_recursive-delegation.md §8.3 (interceptorRef restrictiveness, rule 2)

Description: The restrictiveness rule in §8.3 defines three ways a child may specify `contentPolicy.interceptorRef`: (1) identical to parent, (2) `null → non-null` when parent is unset, or explicitly "Chained interceptor (trust-based): child declares a different interceptor that claims to chain the parent's; not enforced by platform." Rule 2 as written permits any delegation hop to swap the interceptor identity for a different one the platform cannot verify, defeating the monotonicity property the rest of §8.3 is constructed to uphold. Because the child's `contentPolicy` is the one applied when the child's LLM reads content, a dishonest or compromised intermediate link in the chain can introduce a no-op shim interceptor whose `chains = ["parent-ref"]` annotation is never cross-checked. This is inconsistent with §13.5's claim that delegation preserves defense-in-depth, and with the §8.3 rule-5 weakening-cooldown control (which only guards the documented identity-based transitions).

Recommendation: Remove the trust-based chaining exception from the normative rule, or make it enforceable: require the child interceptor's registry record to include a signed `chains: [parent-ref@version]` declaration written by the parent interceptor's owner (not the child agent), verified at admission time; reject delegations whose child interceptor's `chains` attribute does not contain the parent ref. If unenforceable chaining is retained, gate it behind an explicit admin-only `allowUnverifiedChaining` profile on the parent interceptor registry entry, defaulted to false, and emit `INTERCEPTOR_CHAIN_UNVERIFIED` with a cooldown identical to rule 5 when it is used.

**Resolution (iter4):** Adopted the minimal-fix option (removal). The "Chained interceptor (trust-based)" condition was deleted from §8.3's `interceptorRef` restrictiveness list and remaining conditions renumbered (1-4). Because the deleted condition was explicitly "trust-based, not enforced by platform," it carried no enforcement contract to break. The rewritten condition 4 (`CONTENT_POLICY_INTERCEPTOR_SUBSTITUTION`) now rejects every different-non-null-reference case unconditionally and explicitly states that out-of-band chaining claims are not accepted; deployers needing composition MUST implement it inside a single named interceptor so the `interceptorRef` stays identical across the delegation boundary (condition 1). Identity-based monotonicity is now intact end-to-end across §8.3, §11.7, and §13.5. No error-code changes; no new registry fields; no cross-reference drift (all external `rule 5` references pointed to the lifecycle-rules list, not the restrictiveness list).

### SEC-011. lenny-cred-readers group grants credential-file read to every process running as the agent UID [Medium]

**Section:** (unspecified)

**Status:** Fixed (iter4)

Section: spec/13_security-model.md §13.1; spec/04_system-components.md §4.7

Description: The iter3 SEC-005 resolution delivers credential files at mode 0440, owned by the adapter UID with group `lenny-cred-readers` supplied via fsGroup, so the agent UID can read them without CAP_CHOWN. Inside a pod, however, Linux DAC evaluates group membership per-process: any process that inherits the supplementary group (through fsGroup the kubelet applies it to every container in the pod) can read the file. In concurrent-mode pools where multiple session slots share a pod (§4 pool model), this means every co-tenant session inside the pod can read every other session's credential file. Even in single-slot pods the model deliberately lets adapter, agent, and any user-launched subprocess share the group. The spec does not enumerate which UIDs/processes are intended to hold the group, nor does it state that concurrent-mode pools MUST run credential-bearing sessions in separate pods, nor does it consider file-capabilities/AppArmor narrowing.

Recommendation: Make the read boundary explicit: document in §13.1 that `lenny-cred-readers` membership is restricted to the adapter and the single agent process, and that concurrent-mode pools MUST either (a) run at most one credential-bearing session per pod, or (b) materialise each session's credential file in a per-session tmpfs mount with a distinct per-slot group GID. Add a compatibility check in §4.7's pool validation that fails creation when `concurrency &gt; 1` and credentials are declared, unless per-slot isolation is configured. Specify an AppArmor/Seccomp rule preventing agent-spawned subprocesses from inheriting the supplementary group (e.g., drop via `setgroups` in the adapter's pre-exec, or use a dedicated reader sidecar that passes bytes over a unix socket).

**Fix applied:** Added two new normative paragraphs to §13.1 Pod Security after the existing `Cross-UID file delivery without CAP_CHOWN` paragraph. (1) `lenny-cred-readers` membership boundary explicitly enumerates the intended members as exactly two UIDs — adapter UID (writes) + agent UID (reads) — and states that no other sidecar, init container, ephemeral debug container, or operator-injected container may include the GID; the admission webhook rejects violations with `POD_SPEC_CRED_GROUP_OVERBROAD`. Ephemeral `kubectl debug` containers are out of scope because the pod `securityContext` pins them to a separate `runAsUser`. For agent-spawned subprocesses, the paragraph documents the runtime-author responsibility — either avoid spawning subprocesses that should not see credentials, or call `setgroups(0, NULL)` in a pre-exec step to drop the supplementary group before `execve`. No AppArmor/Seccomp profile is mandated (scope judgment: the single-session/task-mode agent container is already inside a trust boundary the session owns). (2) Concurrent-workspace mode credential-read scope explicitly states that in `executionMode: concurrent` with `concurrencyStyle: workspace`, per-slot `/run/lenny/slots/{slotId}/credentials.json` files share the same `lenny-cred-readers` group, so any slot's agent code can read every other slot's credential file via filesystem access. This property is folded into the existing `acknowledgeProcessLevelIsolation` deployer acknowledgment ([§5.2](spec/05_runtime-registry-and-pool-model.md)) rather than added as a new flag, because concurrent-workspace mode already requires explicit acknowledgment of shared process namespace, `/tmp`, cgroup memory, and network stack — cross-slot credential readability is an instance of the same process-level co-tenancy. Deployers needing strict credential-lease isolation are directed to `executionMode: session` or `executionMode: task`. A warning-class CRD condition `ConcurrentWorkspaceCredentialSharing=True` is emitted on `SandboxWarmPool` when a concurrent-workspace pool is created against a credential-bearing Runtime (non-empty `supportedProviders`) for operational visibility. Also updated §5.2's `acknowledgeProcessLevelIsolation` rejection-message enumeration to explicitly list "shared credential-file group-read access" alongside the existing four properties, with a cross-reference to §13.1. Scope deliberately narrower than the recommendation: per-slot GIDs, AppArmor/Seccomp profiles, and reader-sidecar architectures are rejected as disproportionate to the residual risk once the deployer-acknowledgment boundary is surfaced explicitly — the concurrent-workspace mode is already a documented single-tenant co-tenancy tradeoff (tenant-pinned, `acknowledgeProcessLevelIsolation` required) and does not hide credential leakage as a novel vector.

### SEC-012. Admin-time RBAC live-probe caller identity and impersonation path unspecified [Medium] — FIXED

**Section:** (unspecified)

Section: spec/04_system-components.md §4.9 (RBAC live-probe per iter3 CRD-009); spec/15_external-api-surface.md §15.1 (CREDENTIAL_SECRET_RBAC_MISSING)

Description: §4.9's live-probe language states that pool creation, credential addition, and credential update MUST verify the Token Service's ServiceAccount can `get`/`read` each referenced Secret before persisting the CR change. The spec does not specify how the gateway-hosted admin handler executes that probe. Options implied elsewhere include (a) the gateway impersonating the Token Service SA via `TokenRequest` for the SA, (b) the gateway calling a Token Service-exposed `POST /v1/internal/probe` endpoint over mTLS, or (c) the gateway running a `SelfSubjectAccessReview` using its own SA (wrong identity). Under (a) the gateway needs `serviceaccounts/token` on the Token Service SA, which itself is a credential-leasing risk and a CAP vector. Under (c) the probe is meaningless because the gateway's RBAC differs from the Token Service's. Without a normative answer the implementation can silently land on the unsafe path, re-opening CRD-009.

Recommendation: Fix the probe path in §4.9: require the gateway to call a Token Service-owned `POST /v1/internal/credential-access-check` endpoint (mTLS with SPIFFE SVID) that the Token Service answers by invoking its own SelfSubjectAccessReview/Get against the named Secret; return `ALLOWED`/`DENIED`/`NOT_FOUND`. Document that the gateway's own SA MUST NOT hold `serviceaccounts/token` on the Token Service SA. Specify the 4xx mapping to `CREDENTIAL_SECRET_RBAC_MISSING` and a separate 5xx `CREDENTIAL_PROBE_UNAVAILABLE` so operators can distinguish a failed probe from a denied probe, avoiding fail-open on probe errors.

### SEC-013. Interceptor weakening cooldown timestamp is admin-writable, negating the control against a compromised admin [Medium] — FIXED

**Section:** (unspecified)

Section: spec/08_recursive-delegation.md §8.3 rule 5 (weakening cooldown); spec/15_external-api-surface.md §15.1 (INTERCEPTOR_WEAKENING_COOLDOWN)

Description: Rule 5 introduces a cooldown on `fail-closed → fail-open` transitions, evaluated per-replica as `now - transition_ts &lt; cooldownSeconds`. `transition_ts` is stored in the shared interceptor registry and updated via `PUT /v1/admin/interceptors/{name}` on the same field the admin edits when changing mode. An admin with `interceptors:write` can therefore write a `transition_ts` in the past (or set `cooldownSeconds: 0`) simultaneously with flipping mode, collapsing the cooldown to zero. The control's threat model implicitly targets stolen or coerced admin credentials (otherwise the weakening is the admin's legitimate decision); allowing the same credential that performs the weakening to also rewrite the timestamp defeats that model. The spec does not require server-minted `transition_ts`, does not gate `cooldownSeconds` changes behind a separate role, and does not record the prior value in an append-only audit table.

Recommendation: Make `transition_ts` server-minted on every mode change (ignore any client-supplied value) and immutable via the admin API; store it in a separate row the admin endpoint cannot write. Require `cooldownSeconds` changes to go through a dedicated `interceptors:policy-admin` role distinct from `interceptors:write`, and enforce a meta-cooldown on `cooldownSeconds` reductions (the new, shorter cooldown does not apply to any pending transition). Persist every mode change and cooldown change to an append-only `audit.interceptor_policy` table with the writer's identity. Document all of this in §8.3 rule 5 and add `INTERCEPTOR_COOLDOWN_IMMUTABLE` to §15.1 for rejected client attempts to set `transition_ts`.

**Resolution (minimal fix):** §8.3 rule 5 now explicitly states that `transition_ts` is server-minted on every `fail-closed → fail-open` transition and is not admin-API-writable; any client-supplied `transition_ts` is rejected with the new `INTERCEPTOR_COOLDOWN_IMMUTABLE` error (added to §15.1) before any state change is persisted, and the registry stores `transition_ts` in a location the admin-API write path does not expose as writable. The cooldown duration is not admin-API-settable either: `gateway.interceptorWeakeningCooldownSeconds` is a cluster-scoped Helm value (already the case in the existing spec), so no new `interceptors:policy-admin` role is introduced — reducing the cooldown requires cluster-config write access, a distinct credential domain from `interceptors:write`. A meta-cooldown rule was added: in-flight cooldowns are evaluated against the `cooldownSeconds` value that was in force at their `transition_ts`, preventing a cluster-config change from cutting short a pending cooldown. The existing `interceptor.fail_policy_weakened` append-only hash-chained audit event (§11.7) already captures writer identity, so no new `audit.interceptor_policy` table is introduced — this is noted inline in rule 5. The `interceptors:policy-admin` role split from the original recommendation was deemed unnecessary (and a contract change) because the cooldown is already cluster-scoped; this note remains available as a smaller follow-up if deployers later want per-interceptor cooldown overrides.
```

---

## 3. Network Security & Isolation

### NET-061. `lenny-ops-egress` storage/monitoring rules lack pod selectors [High] — Fixed

**Section:** 25.10

**Fix applied:** Rewrote the four storage/monitoring egress rules in the `lenny-ops-egress` NetworkPolicy (§25.4) so each `to:` clause combines `namespaceSelector` (keyed on `kubernetes.io/metadata.name`) with a destination `podSelector`: Postgres-via-PgBouncer uses `lenny.dev/component: pgbouncer` (canonical per §13.2 NET-047/NET-050), MinIO uses `lenny.dev/component: minio` (canonical), Redis uses `app: redis` and Prometheus uses `app: prometheus` (upstream-subchart convention, matching the idiom established by `lenny-backup-job`). The Postgres selector is PgBouncer rather than `app: postgres` because `lenny-ops` goes through the pooler — direct Postgres access is reserved for `lenny-backup-job` per §13.2 and §25.4. Added a normative block (NET-061) under the `lenny-ops-egress` policy specifying that any Lenny-rendered NetworkPolicy originating from the operability plane and targeting a storage/monitoring/platform-component destination MUST pair `namespaceSelector` with `podSelector`, and defining the `ops-egress-selector-parity` `lenny-preflight` check that fails the install/upgrade when a clause omits the `podSelector`, uses a non-canonical label key for a Lenny-rendered component, or resolves to zero pods for an expected component. The normative note lives at §25.4 (immediately after the `lenny-ops-egress` block) rather than a new §13.7 subsection because §13 currently stops at §13.5 — creating a new security-model subsection solely for this guarantee was not required, and the operability-plane locality keeps the rule physically adjacent to the policy it governs.

The `lenny-ops-egress` NetworkPolicy in `/Users/joan/projects/lenny/spec/25_agent-operability.md` (lines 1148-1172) permits egress to Postgres, Redis, MinIO, and Prometheus using only `namespaceSelector: {matchLabels: {kubernetes.io/metadata.name: lenny-system}}` for the destinations, with no `podSelector`. This means any pod in `lenny-system` (including gateway, controller, token-service, coredns, webhook) is reachable on ports 5432/6379/9000/9090 from any `lenny-ops` operability pod (runbook runners, backup jobs, diagnostic sidecars). The NET-056 fix correctly added both selectors for the `lenny-backup-job` storage targets, but the identical pattern was not applied to `lenny-ops-egress`. A compromised operability pod can therefore reach `gateway:8443`-adjacent services, the webhook admission controller, and CoreDNS on their Postgres/Redis/MinIO/Prometheus-shaped ports (and any other service that happens to listen on those port numbers) without ever transiting the gateway. This is a direct regression/gap versus NET-056.

**Recommendation:** Rewrite each storage/monitoring egress rule in `lenny-ops-egress` to combine `namespaceSelector` for `lenny-system` with `podSelector: {matchLabels: {app: postgres}}` (resp. `app: redis`, `app: minio`, `app: prometheus`), matching the two-selector pattern used by `lenny-backup-job`. Add a preflight check `lenny-preflight ops-egress-selector-parity` that fails if `lenny-ops-egress` storage rules omit the destination `podSelector`. Document in §13.7 that any operability-plane NetworkPolicy targeting `lenny-system` MUST specify both selectors.

---

### NET-062. IPv6 CIDRs mixed inside IPv4 `ipBlock` `except` list — invalid NetworkPolicy shape [High]

**Status:** Fixed (iter4 remediation). Both offending rules now emit two parallel `ipBlock` peers per K8s NetworkPolicy requirements. `/Users/joan/projects/lenny/spec/13_security-model.md` `allow-gateway-egress-llm-upstream` (§13.6) splits into a `cidr: 0.0.0.0/0` peer (IPv4 cluster CIDRs + RFC1918 + IPv4 link-local + IPv4 IMDS) and a `cidr: ::/0` peer (IPv6 cluster CIDRs + IPv6 ULA + IPv6 link-local + IPv6 IMDS); the Helm template partitions `excludePrivate` by address family (`contains ":"`) at render time. `/Users/joan/projects/lenny/spec/25_agent-operability.md` `lenny-ops-egress` webhook rule (§25.10) applies the same two-peer split. The NET-057 shared-list note was updated to describe the dual-stack partition and to reference the new `ipblock-family-parity` preflight check; the NET-002 IMDS prose at §13.6 was updated to clarify that IPv6 IMDS (`fd00:ec2::254/128`) lives in the `::/0` peer. `/Users/joan/projects/lenny/spec/17_deployment-topology.md` §17.2 preflight table gains a new `NetworkPolicy ipBlock family parity` row that fails any rendered `ipBlock` where an `except` entry's family does not match the enclosing `cidr`, and the existing Gateway/lenny-ops private-range parity row was refined to compare against the same-family peer. Conformance-test coverage for both IPv4-only and dual-stack clusters is now a documented requirement.

**Section:** 13.6, 25.10

The `allow-gateway-egress-llm-upstream` rule in `/Users/joan/projects/lenny/spec/13_security-model.md` (lines 322-364) and the webhook-delivery egress rule in `lenny-ops-egress` (`/Users/joan/projects/lenny/spec/25_agent-operability.md` line 1183) both place IPv6 CIDR blocks (`fc00::/7`, `fe80::/10`, `fd00:ec2::254/128`) inside an `ipBlock` whose `cidr` is `0.0.0.0/0` (IPv4). Kubernetes NetworkPolicy validation (`NetworkPolicySpec.egress[].to[].ipBlock`) requires every entry in `except` to be contained within the `cidr` of the same block, and CIDR family mixing is explicitly rejected by the API server and by CNIs that implement strict validation (Cilium, Calico). As currently written, these manifests will either be rejected by admission or will silently drop the IPv6 except entries depending on CNI leniency — in the latter case, gateway/webhook pods retain unrestricted IPv6 egress to ULA, link-local, and IPv6 IMDS. This undermines the SSRF defense symmetry that NET-057 established.

**Recommendation:** Split each rule into two parallel `ipBlock` entries — one for IPv4 (`cidr: 0.0.0.0/0`, `except: [RFC1918, link-local, IPv4 IMDS, cluster pod/service CIDRs]`) and one for IPv6 (`cidr: ::/0`, `except: [fc00::/7, fe80::/10, fd00:ec2::254/128, cluster IPv6 pod/service CIDRs]`). Update the `egressCIDRs.excludePrivate` Helm template to emit both blocks. Add a preflight check that rejects any `ipBlock` where an `except` entry's address family does not match the `cidr` family. Extend the rendered-manifest conformance test fixtures to exercise dual-stack clusters.

---

### NET-063. Gateway↔interceptor hop lacks mTLS mandate and peer-identity validation [High]

**Status:** Fixed (iter4 remediation). `/Users/joan/projects/lenny/spec/10_gateway-internals.md` §10.3 lifecycle table gains an `In-cluster interceptors` row (24h TTL, SPIFFE URI `spiffe://<trust-domain>/interceptor/{namespace}/{pod-name}` + DNS `<svc>.<namespace>.svc.<cluster>`, cert-manager auto-renewal at 2/3 lifetime). A new `Gateway ↔ Interceptor mTLS peer validation (NET-063)` block, modeled on the NET-060 Pod↔Gateway block, mandates mTLS with symmetric SPIFFE-URI and DNS-SAN peer validation on the gateway→interceptor gRPC hop (port `gateway.interceptorGRPCPort`, default 50053). The gateway validates both the SPIFFE URI (trust domain from `global.spiffeTrustDomain`, namespace bounded by `gateway.interceptorNamespaces`) and the DNS SAN (via explicit `tls.Config.ServerName`), rejects plaintext/`InsecureSkipVerify`, and logs `interceptor_identity_mismatch` on SAN failure. Interceptors MUST validate the gateway's DNS SAN `lenny-gateway.lenny-system.svc` on inbound handshakes. Fail-closed on missing/expired interceptor cert via readiness probe flipping false (NetworkPolicy `podSelector` resolves to zero endpoints, gateway returns `INTERCEPTOR_TIMEOUT` under the default `failPolicy: fail-closed`). `/Users/joan/projects/lenny/spec/16_observability.md` §16.1 adds `lenny_interceptor_mtls_handshake_duration_seconds` histogram (labeled by `result` ∈ {`success`, `san_mismatch`, `cert_expired`, `cert_missing`, `tls_error`}); §16.5 adds the `InterceptorMTLSHandshakeFailure` warning alert on `rate(..._count{result!="success"}[5m]) > 0` sustained for 2 min.

**Section:** 10.3, 13.5

The mTLS PKI section in `/Users/joan/projects/lenny/spec/10_gateway-internals.md` §10.3 mandates SPIFFE-URI-based mutual authentication for Gateway↔Pod, Gateway↔Token Service, and Gateway↔Controller hops, and NET-060 added symmetric SAN validation for Pod↔Gateway. However, the Gateway↔in-cluster Interceptor path (port 50053, used for policy/audit interceptors installed via `gateway.interceptorNamespaces`) has no equivalent requirement. There is no documented TLS mode, no SPIFFE URI scheme for interceptor identities (e.g. `spiffe://&lt;trust-domain&gt;/interceptor/&lt;ns&gt;/&lt;name&gt;`), and no validation that the gateway is authenticating the interceptor's identity before forwarding every request/response envelope through it. A pod that lands in an interceptor namespace with the correct service labels can intercept (and mutate) every gateway-mediated MCP exchange.

**Recommendation:** Require mTLS with SPIFFE-URI-based peer validation on the Gateway↔Interceptor hop. Issue interceptor certificates from cert-manager with SAN `spiffe://&lt;trust-domain&gt;/interceptor/&lt;namespace&gt;/&lt;pod&gt;` and DNS SAN `&lt;svc&gt;.&lt;ns&gt;.svc.&lt;cluster&gt;`. Gateway MUST validate both URI and DNS SANs and MUST reject plaintext connections. Document the certificate rotation SLO in §10.3's lifecycle table. Expose `lenny_interceptor_mtls_handshake_duration_seconds{result}` and add an alert `InterceptorMTLSHandshakeFailure` in §16. Fail-closed if the interceptor certificate is missing or expired.

---

### NET-064. `global.spiffeTrustDomain` / `saTokenAudience` defaults only warn, allowing cross-deployment impersonation [Medium] — Fixed (iter4)

**Section:** 10.3, 17.9 (preflight)

`/Users/joan/projects/lenny/spec/10_gateway-internals.md` line 258 specifies that `global.spiffeTrustDomain` defaults to `lenny.local` and that multi-deployment installs trigger a preflight warning when the default is unchanged. In a cluster running two Lenny deployments that both retain defaults, the SPIFFE trust domains collide — a pod certificate issued in deployment A validates against deployment B's gateway (and vice versa), enabling cross-deployment pod impersonation and audit-log attribution forgery. The same concern applies to `global.saTokenAudience` (token-service audience claim). A warning that an operator can proceed past is insufficient for a cryptographic-trust-boundary parameter.

**Recommendation:** Make `global.spiffeTrustDomain` and `global.saTokenAudience` required values in the Helm chart (no defaults); `helm install/upgrade` fails templating if unset. If defaults must exist for single-cluster quickstarts, derive them from `.Release.Namespace + cluster UID` so they are unique without operator input. Promote the multi-deployment preflight from warn to error when a duplicate trust domain or audience is detected. Document migration guidance for single-deployment users who upgrade to multi-deployment layouts.

**Resolution (iter4):** Removed chart defaults from `global.spiffeTrustDomain` and `global.saTokenAudience` in §10.3; both are now required chart values and `helm install/upgrade` fails templating if unset (with a clear error message citing NET-064). The embedded Quickstart path (`lenny up`) derives both values from `.Release.Namespace` + `kube-system` namespace UID at bootstrap so single-cluster quickstarts remain operator-input-free, without surfacing a default for production templates. Added two fail-closed preflight rows ("SPIFFE trust domain uniqueness (NET-064)" and "SA token audience uniqueness (NET-064)") in `/Users/joan/projects/lenny/spec/17_deployment-topology.md` §17.9 that enumerate the `lenny.dev/spiffe-trust-domain` and `lenny.dev/sa-token-audience` annotations on existing `lenny-gateway` Deployments and abort the install with exit code 1 if the value under installation collides with any existing deployment. No migration guidance was added (no deployments in the wild per project policy).

---

### NET-065. `lenny-ops-egress` external webhook egress omits cluster pod/service CIDR exclusions [Medium]

**Section:** 25.10, 13.6

**Status:** Fixed (iter4).

The `lenny-ops-egress` external HTTPS egress rule (`/Users/joan/projects/lenny/spec/25_agent-operability.md` line 1183) permits `cidr: 0.0.0.0/0` for webhook delivery with only the `egressCIDRs.excludePrivate` list (RFC1918 + link-local + IMDS) in `except`. It does NOT include the cluster pod CIDR or service CIDR that the gateway rule's `egressCIDRs.excludeClusterInternal` template adds. On clusters using CGNAT-range pod CIDRs (`100.64.0.0/10`, default on many managed Kubernetes providers) or custom non-RFC1918 pod CIDRs, operability webhooks can dial any in-cluster pod IP as a "webhook target." Combined with NET-061's selector gap, a compromised operability pod can reach gateway/controller/token-service pod IPs directly over their service ports by crafting a webhook URL pointing at the pod IP.

**Recommendation:** Add `egressCIDRs.excludeClusterInternal` (rendered as `{podCIDR, serviceCIDR}`) to the `except` block of the `lenny-ops-egress` webhook rule, matching the gateway `allow-gateway-egress-llm-upstream` rule. Document the cluster-CIDR discovery story in §13.7 (e.g. read from `kube-system/kubeadm-config` or require operator to set `global.cluster.podCIDR` / `serviceCIDR`). Add a preflight check that fails if the configured cluster CIDRs are not in the rendered `except` list.

**Resolution (iter4):** Rendered `egressCIDRs.excludeClusterPodCIDR` / `excludeClusterServiceCIDR` (plus the v6 variants on dual-stack clusters) and `egressCIDRs.excludeIMDS` into the `except` block of the `lenny-ops-egress` webhook rule in `/Users/joan/projects/lenny/spec/25_agent-operability.md` §25.10, mirroring the gateway `allow-gateway-egress-llm-upstream` rule (§13.2). Added an inline comment enumerating the three symmetric categories (cluster-internal, private/link-local, IMDS) and noting the CGNAT threat model. Updated the §13.2 NET-057/NET-062 normative note with a "Cluster-CIDR and IMDS symmetry (NET-065)" subclause that extends the cross-surface audit to cluster-CIDR and IMDS membership. Extended the NET-022 preflight and drift-detection mechanism in §13.2 to cover the `lenny-ops-egress` webhook rule alongside the gateway external-HTTPS rule and the `internet` egress profile: `lenny-preflight` fails the install if the discovered cluster pod/service CIDRs are absent from any of the three `except` lists, and the WarmPoolController drift check labels the `lenny_network_policy_cidr_drift_total` counter with `policy: internet|gateway-llm-upstream|ops-egress`. No new §13.7 subsection was added — the cluster-CIDR discovery story is already documented under NET-022 in §13.2, and the resolution re-uses that mechanism rather than duplicating it.

---

### NET-066. `lenny-backup-job` DNS egress missing TCP/53 fallback [Medium]

**Status:** Fixed (iter4).

**Section:** 25.10

The `lenny-backup-job` NetworkPolicy in `/Users/joan/projects/lenny/spec/25_agent-operability.md` (line 1261) permits DNS egress on UDP/53 only. Backup jobs resolve object-storage endpoints (MinIO, S3) whose DNS responses frequently exceed 512 bytes (CNAME chains, multi-record `TXT`/`AAAA` responses, EDNS0 OPT records). Per RFC 7766, resolvers fall back to TCP/53 when a UDP response is truncated (`TC=1`). Without TCP/53 in the allow-list, backup jobs will intermittently fail name resolution on larger responses (particularly for cloud object-storage endpoints with many IPs or DNSSEC-signed zones), producing non-deterministic backup failures that look like transient storage outages.

**Recommendation:** Add `protocol: TCP, port: 53` alongside `UDP/53` in the `lenny-backup-job` DNS egress rule. Audit every NetworkPolicy in the chart for the same omission (gateway, controller, ops, interceptors) via a preflight `lenny-preflight dns-tcp-fallback` that scans rendered manifests for DNS rules lacking TCP/53. Document the RFC 7766 rationale in §13.7.

**Resolution:** Added `{ protocol: TCP, port: 53 }` to the `lenny-backup-job` DNS egress rule (now `ports: [{ protocol: UDP, port: 53 }, { protocol: TCP, port: 53 }]`), matching the pre-existing `lenny-ops-egress` DNS rule (§25.10, line 1211) and the agent-pod DNS rule in §13.2 (lines 109-112) — both of which already carried the TCP/53 companion. Added an inline block comment citing RFC 7766, explaining the >512-byte response-size risk for object-storage endpoints, and declaring the `lenny-preflight` gate that fails the install if any Lenny-rendered NetworkPolicy lists UDP/53 without TCP/53 (regression guard covering all current and future DNS egress rules across gateway, controller, ops, interceptors). The audit confirmed no other Lenny-rendered DNS rule was deficient, so only the backup-job rule was modified. RFC 7766 rationale is documented inline with the fix rather than in a separate §13.7 (no such section exists, and the parallel `lenny-ops-egress` DNS rule at line 1211 already follows the inline-documentation pattern — adding a new section for a single rule would be asymmetric).

---

### NET-067. DNS egress rules use broad `namespaceSelector` without CoreDNS `podSelector` [Medium] — **Fixed**

**Section:** 13.6, 25.10

Multiple DNS egress rules permit traffic to `namespaceSelector: {kubernetes.io/metadata.name: kube-system}` on port 53 without a destination `podSelector`. Affected rules: `lenny-ops-egress` DNS rule (`/Users/joan/projects/lenny/spec/25_agent-operability.md` line 1173), `lenny-backup-job` DNS rule (same file, line 1261), and (by pattern) any `lenny-system` component rule that targets the cluster's default CoreDNS. `kube-system` hosts dozens of system pods (CoreDNS, metrics-server, kube-proxy, cloud-provider controllers, CSI drivers) and the namespace is typically managed by the cluster operator, not by Lenny. An operability pod whose DNS rule is "to kube-system on 53" can dial any UDP/53 listener that happens to run in kube-system — and if the operator ever co-locates a custom DNS/relay/proxy pod in kube-system, it becomes reachable too.

**Recommendation:** Constrain every DNS egress rule to `namespaceSelector: {kubernetes.io/metadata.name: kube-system}` AND `podSelector: {matchLabels: {k8s-app: kube-dns}}` (the canonical CoreDNS label). For the dedicated agent CoreDNS case, use the Lenny-specific selector (`app: lenny-coredns` in `lenny-system`). Add a preflight check `lenny-preflight dns-pod-selector` that fails if any NetworkPolicy DNS rule omits a `podSelector`. Update §13.7 to state that DNS egress without a pod selector is a policy violation.

**Resolution:** Added `podSelector: { matchLabels: { k8s-app: kube-dns } }` to the two kube-system-CoreDNS DNS egress rules in §25.10 (`lenny-ops-egress` and `lenny-backup-job`), with inline comments enumerating the `kube-system` co-tenants the namespace-only selector would otherwise expose. Added a new "NetworkPolicy DNS `podSelector` parity (NET-067)" row to the §17.9 preflight table that fails install/upgrade if any Lenny-rendered NetworkPolicy DNS rule (UDP/TCP 53) pairs `namespaceSelector` without `podSelector`. Added a "DNS egress peer requirement (NET-067)" normative note to §13.2 stating the rule and cross-referencing the preflight check. The §13.2 agent-pod DNS rule (lines 101-112) already pairs both selectors correctly and was unchanged.

---

### NET-068. OTLP plaintext port 4317 permitted when `observability.otlpTlsEnabled=false` [Medium]

**Section:** 16.4, 13.6

**Status:** Fixed (tracked in-prose as `OTLP-068` to avoid identifier collision with the K8S-041 additive-per-pod-label fix, which already occupies the `NET-068` label in §13.2 and §17.2)

`/Users/joan/projects/lenny/spec/16_observability.md` introduces `observability.otlpTlsEnabled` (NET-059) but the default behavior and the corresponding NetworkPolicy still permit gRPC egress to port 4317 without enforcing TLS at the transport layer. When an operator leaves `otlpTlsEnabled: false` (or ships a collector without TLS), trace exports transit the cluster network containing tenant metadata (tenant IDs, session IDs, agent pool names), error descriptions that can include redacted-but-structured secret hints, and correlation headers. NetworkPolicy permits egress to the collector's IP/namespace but has no mechanism to refuse plaintext on 4317; gateway/pod application code is the only enforcement point, and a misconfiguration (e.g. operator sets collector endpoint to an external plaintext IP) silently degrades to plaintext.

**Recommendation:** Make `observability.otlpTlsEnabled` default to `true` and make plaintext opt-in with a loud deprecation/warning banner in Helm output. When `otlpTlsEnabled=true`, restrict the OTLP egress NetworkPolicy to port 4318 (HTTPS) only, or require operators to rename the gRPC endpoint port to a TLS-only port (e.g. 4443 via a chart-rendered proxy). Add a preflight probe `lenny-preflight otlp-tls` that opens the configured collector endpoint and fails if the TLS handshake does not complete. Add an alert `OTLPPlaintextEgressDetected` fed by gateway/pod TLS handshake telemetry in §16.5.

**Resolution:** `observability.otlpTlsEnabled: true` was already the production default from the NET-059 fix (§13.2 line 176). Extended that note with an explicit `OTLP-068` plaintext opt-in guard: outside dev mode, setting `otlpTlsEnabled: false` requires the additional `observability.acknowledgeOtlpPlaintext: true` Helm value or the chart refuses to render, and when acknowledged, `helm install/upgrade` NOTES output prints a loud plaintext-export warning banner citing §13.2. Added a new `otlp-tls` preflight check row to §17.9 that opens the configured collector endpoint, performs a TLS 1.2+ handshake against the cluster trust bundle plus any deployer-supplied `observability.otlpCaBundle`, and validates the server certificate's SAN against `observability.otlpEndpoint` — install fails if the handshake does not complete or the SAN does not match. Added the `lenny_otlp_export_tls_handshake_total` counter to §16.1 (labeled by `component`, `result` with a `plaintext` bucket populated when the exporter connects without negotiating TLS) and the `OTLPPlaintextEgressDetected` critical alert to §16.5 that fires on any non-zero plaintext-result rate sustained for more than 60 seconds. Prose in all four locations uses the `OTLP-068` identifier; the existing `NET-068` label (additive per-pod label exception from K8S-041) is untouched. NetworkPolicy port shape was left as-is: the current design already accepts `observability.otlpPort: 4318` for deployers preferring OTLP/HTTP and mandates TLS-over-gRPC at the canonical 4317 otherwise; the new handshake probe + runtime plaintext alert together close the enforcement gap without requiring a chart-rendered proxy.

---

### NET-069. OTLP collector `podSelector` relies on non-standard label defaults [Low]

**Section:** 16.4, 13.6

The OTLP egress NetworkPolicy documented in §16 uses Helm values `observability.otlpPodLabel` / `otlpPodLabelValue` that default to `app: otel-collector`. The canonical OpenTelemetry Collector Helm chart and the OpenTelemetry Operator both label pods with `app.kubernetes.io/name: otel-collector` (plus `app.kubernetes.io/instance: ...`). Operators who install the collector from upstream charts and then install Lenny with chart defaults will end up with a NetworkPolicy that matches zero pods — trace export is silently blocked. The preflight reachability probe checks endpoint TCP connectivity (which may succeed via the Service IP) but does not verify that the egress `podSelector` actually matches the collector's pods.

**Recommendation:** Change the default `observability.otlpPodLabel / Value` to `app.kubernetes.io/name: otel-collector` to match the upstream chart. Extend `lenny-preflight otlp` to resolve the collector endpoint to its backing pod(s) and verify that at least one backing pod carries the labels specified in `otlpPodLabel*`; fail the preflight if no pod matches. Document the `app.kubernetes.io/name` default in §16.4 and note that operators using forked charts must override the value.

---

## 4. Scalability & Performance Engineering

### PRF-006. PDB `maxUnavailable: 1` Blocks HPA Scale-Down Throughput at Tier 3 [High] — **Fixed (iter4)**

**Section:** spec/17_deployment-topology.md §17.1 (PDB), §17.8.2 (HPA scale-down policy + scale-down time table)

The flattened PodDisruptionBudget `maxUnavailable: 1` (iter3 PRF-005 fix) applies to *all* voluntary disruptions, including HPA-triggered evictions. Yet §17.8.2 documents a Tier 3 scale-down policy of `3 pods / 60s` and asserts "Gateway scale-down time (max→min replicas): 8.3 min (30→5, 3 pods/60s)". The eviction API will admit at most one pod at a time under this PDB, so the effective scale-down rate is bounded by `1 / (pod_termination_seconds)`. With the documented preStop tiered cap (up to 90s) plus connection drain, the floor is ~60-120s per pod, meaning 25 pods take ~25-50 minutes — 3-6× the stated 8.3 min. This desynchronizes cost/capacity models and leaves the cluster carrying idle replicas far longer than planned during traffic troughs; at Tier 3 with 25 excess replicas × ~4 vCPU each, that is ~100 vCPU of phantom capacity for 25+ minutes.
**Recommendation:** Either (a) restore a tiered PDB that sizes `maxUnavailable` against the HPA scale-down policy (e.g., `maxUnavailable: 3` at Tier 3 to match `3 pods/60s`), with explicit reasoning documented beside the voluntary-disruption ceiling debate from PRF-005, or (b) keep `maxUnavailable: 1` and correct the §17.8.2 scale-down time row to reflect the actual PDB-bound floor (e.g., "~25 min at Tier 3"), and reduce the HPA `scaleDown` policy to `1 pod/60s` so Kubernetes does not repeatedly attempt evictions it cannot satisfy. Also add a metric `lenny_pdb_blocked_evictions_total{controller="hpa"}` and an alert when it grows, because under the current configuration a scale-down-heavy period will generate continuous eviction-rejection log spam.

**Status:** Fixed (iter4). Applied option (b): option (a) is precluded by the v1-no-tier-splitting rule (a tiered `maxUnavailable` would reintroduce the kind of tier-dependent behavior the platform explicitly rejects). Specifically:

1. **§17.8.2 gateway table:** Changed `HPA scale-down pods per period` at Tier 3 from `3 / 60s` to `1 / 60s` (now flat across all tiers). Changed the `Gateway scale-down time (max→min replicas)` Tier 3 row from `8.3 min (30→5, 3 pods/60s)` to `~25 min (30→5, PDB-bound at 1 pod / ~60–120s; see note below)`; Tier 1/2 now show `~2 min` / `~7 min` to make the approximation consistent.
2. **§17.8.2 new note** "Scale-down rate is PDB-bound, not HPA-policy-bound" inserted immediately after the gateway table: derives the `1 / pod_termination_seconds` floor, shows the ~25–50 min Tier 3 range, explains the availability-over-elasticity trade-off, and documents why the HPA `scaleDown` policy is pinned to `1 pod / 60s` (avoiding repeated eviction attempts the PDB cannot satisfy).
3. **§17.1 gateway row:** Added a "Scale-down consequence" sentence documenting that the PDB also governs HPA-driven scale-down, that the HPA policy is consequently flat at `1 pod / 60s`, and that the trade-off is observable via `lenny_pdb_blocked_evictions_total`.
4. **§10.1 HPA scale-down protection prose:** Replaced the "per-tier scale-down policy adjustments" pointer with an explicit statement that the policy is flat at every tier because the PDB is the binding constraint.
5. **§16.1 metrics:** Added `lenny_pdb_blocked_evictions_total` (counter labeled by `pdb`, `controller ∈ {hpa, cluster_autoscaler, node_drain, other}`) emitted by `lenny-ops` via Kubernetes events/audit watch.
6. **§16.5 alerts:** Added `PDBBlockedEvictions` warning alert with dual firing conditions (sustained rate over 10 min, plus an hourly ceiling for HPA-sourced evictions) and a triage pointer distinguishing expected trough-recovery blocking from `minReplicas` misconfiguration and non-HPA disruption sources.

### PRF-007. Tier 3 MinIO Burst Throughput Target Unachievable with Recommended Topology [High] — **Fixed (iter4)**

**Section:** spec/17_deployment-topology.md §17.8.2 (Tier 3 storage table + 8-node narrative)

The Tier 3 capacity row "Minimum aggregate throughput (burst, max workspace): 20 GB/s" is directly contradicted by the narrative immediately below: "8-node MinIO… provides ~10–12 GB/s aggregate write throughput, giving ~40% headroom above the 8.5 GB/s burst ceiling." The 20 GB/s figure (derived from 400 sessions × 50 MB workspace / some burst window) cannot be satisfied by the recommended cluster; the narrative silently substitutes a lower 8.5 GB/s burst target to justify 8 nodes. This means a Tier 3 CheckpointBarrier event coinciding with a large-workspace population will saturate MinIO and either stall the preStop drain (forcing SIGKILL of pods mid-checkpoint) or spill over the PreStop Stage-2 tiered cap and drop in-flight state.
**Recommendation:** Reconcile the numbers. Either (a) rescale the Tier 3 burst requirement to match a defensible per-session checkpoint size × concurrency × window (document the arithmetic), (b) specify a larger MinIO topology (e.g., 16-node NVMe @ ~22 GB/s) as the Tier 3 reference baseline, or (c) cap `maxSessionsPerReplica` × replica count at Tier 3 so aggregate burst stays ≤ 10 GB/s. Add an explicit invariant: `replicas × maxSessionsPerReplica × avg_workspace_bytes / checkpoint_window_seconds ≤ minio_aggregate_write_throughput × 0.7`. Also document the behavior when this invariant is violated (preStop abort + SIGKILL vs extended drain vs back-pressure).

### PRF-008. Tier 3 `minWarm` 1,260 Uses 1.2× Safety Factor — Insufficient Headroom for a 35s Failover [Medium] — **Fixed (iter4)**

**Section:** spec/17_deployment-topology.md §17.8.2 (warm-pool sizing), spec/04_system-components.md §4.6.2 (formula)

The Tier 3 production minWarm of 1,260 derives from `30/s × 1.2 × 35s` (claim_rate × safety_factor × (failover + startup)). A safety factor of 1.2 yields only 20% headroom. The operational premise — that warm-pool replenishment is blocked for 35s on control-plane failover — is precisely the moment when claim variance also spikes (queued requests release on failover recovery, typical thundering-herd). Any 20% burst above steady-state during that window drains the pool to zero and forces cold starts at 30-90s each, i.e., a &gt;10s P99 latency cliff for the next ~500-1,000 sessions. Tier 1 and Tier 2 use the same 1.2 factor, but those tiers absorb bursts easily because queueing theory noise at low concurrency is a larger fraction of demand; at 30 claims/s the variance is narrower in relative terms, so the *absolute* burst buffer matters more.
**Recommendation:** Document a severity-aware safety factor: `safety_factor(tier) = {T1: 1.5, T2: 1.3, T3: 1.3-1.5}` based on measured burst variance, not a flat 1.2 across tiers. At Tier 3, either raise safety_factor to 1.5 (revised minWarm ≈ 1,575) or explicitly add a `burst_reserve_pods` term separate from the claim-rate term, with the rationale documented. Add an SLO for "fraction of claims served from warm pool during the 35s failover window" and make the measurement the validation gate for the chosen factor.

### PRF-009. Default `ResourceQuota` `pods: 200` Does Not Scale with Tier — Blocks Tier 3 Warm Pool [Medium] — **Fixed (iter4)**

**Section:** spec/17_deployment-topology.md §17.2 (ResourceQuota example)

The example ResourceQuota for the `lenny-agents` namespace hard-caps `pods: 200`. Tier 3 requires minWarm of 1,260 in a single hot pool plus up to 10,000 active session pods plus delegation fan-out (the §17.8.2 worked example references minWarm ~3,400 with the burst term). Unless the example is relabeled as Tier-1-only or the quota is computed from tier inputs, a naive Tier 3 rollout using the template will be unable to create more than 200 pods in the agents namespace and warm-pool scaling will silently stall with "exceeded quota" admission denials — a failure mode not covered by any existing alert (HPA sees desired=actual, autoscaler sees success, but the quota rejects pod creation).
**Recommendation:** Make `pods`, `requests.cpu`, `requests.memory`, and `requests.ephemeral-storage` tier-scaled parameters in the ResourceQuota template: `pods &gt;= (sum of all pool maxWarm) + (concurrency limit × 1.2) + (delegation_fanout_cap)`. Publish explicit per-tier defaults beside the capacity tables (e.g., Tier 3: `pods: 15000`). Add an operator alert `lenny_pool_scaleout_blocked_by_quota` fed by a quota-admission-webhook or by comparing ReplicaSet `desired - available` against ResourceQuota headroom. The spec should also note that ResourceQuota changes in Kubernetes are instant but do not evict existing pods, so scale-up events post-quota-raise proceed normally.

**Resolution (iter4):** §17.2 was rewritten to label the `pods: 200` example as the Tier 1 default, cite the new tier-scaled defaults table, state the sizing formula `pods >= sum(pool.maxWarm) + session_concurrency_limit × 1.2 + concurrent_delegation_fanout_cap`, and reference `.Values.capacityPlanning.tier` as the rendered source. §17.8.2 gained a "ResourceQuota tier defaults" table (Tier 1: `pods: 200`; Tier 2: `pods: 2,500`; Tier 3: `pods: 15,000`) with `requests.cpu` / `requests.memory` / `requests.ephemeral-storage` rows, a Tier 3 worked example (~15,900 floor → 15,000 published default), and a "ResourceQuota changes take effect at next admission but do not evict running pods" paragraph. A new `PoolScaleoutBlockedByQuota` alert in §16.5 fires on the existing `lenny_warmpool_warmup_failure_total{error_type="resource_quota_exceeded"}` counter to give operators a quota-specific signal distinct from the general `WarmPoolReplenishmentFailing`.

### PRF-010. `pod_startup_seconds` vs `pod_warmup_seconds` Conflated in Tier 3 minWarm Worked Example [Medium]

**Section:** spec/04_system-components.md §4.6.2 (minWarm formula definition), spec/17_deployment-topology.md §17.8.2 (Tier 3 example uses 35s for `failover + startup`)

§4.6.2 distinguishes `pod_startup_seconds` (container pull + runtime startup, baselined at 10s) from `pod_warmup_seconds` (SDK init, 30-90s) and uses the former in the base-demand term and the latter only in the burst term. The §17.8.2 Tier 3 example computes `30 × 1.2 × 35s` where "35s" is labeled `failover + startup` (i.e., 5s failover + 30s startup). That 30s is `pod_warmup_seconds`, not `pod_startup_seconds`. If the base-demand term is meant to use `pod_startup_seconds = 10s` per the formula, the correct figure is `30 × 1.2 × 15s = 540` + burst term — a ~60% reduction in base warm pool. If instead the intent is to cover warmup (SDK init) during failover, §4.6.2 should redefine the base-demand term to use `pod_warmup_seconds` and the discrepancy propagates to Tier 1/2 examples too. Either way, the formula and the worked examples are internally inconsistent, and the 1,260 baseline depends on which interpretation is correct.
**Recommendation:** Fix the formula definition in §4.6.2 to use `failover_seconds + pod_warmup_seconds` in the base-demand term (matching the §17.8.2 arithmetic), OR reduce the §17.8.2 examples to use `pod_startup_seconds` (likely requiring a separate pre-warmed, pre-initialized "ready pool" vs "starting pool" distinction in the pool model). Also clarify in §6.3 whether the documented P95 pod-warm &lt; 2s/5s SLO measures container startup only, SDK warmup, or both — because 30-90s SDK init does not reconcile with a 2s P95 SLO and one of the three numbers is describing a different phase than the others.

### PRF-011. Stream Proxy `maxConcurrent: 20,000` Per Replica Lacks Derivation vs 10K Session Target [Low]

**Section:** spec/10_gateway-internals.md §10.1, spec/04_system-components.md §4.1 (`maxSessionsPerReplica: 400` Tier 3)

`maxConcurrent` of 20,000 streams per gateway replica × 30 Tier 3 replicas = 600K aggregate concurrent streams, yet the Tier 3 capacity target is 10,000 sessions. The spec does not document the multiplier (sessions × streams-per-session?) nor how this interacts with `maxSessionsPerReplica: 400` (which would imply 50 streams per session if `maxConcurrent` is the binding constraint). A 50:1 stream-to-session ratio is plausible for MCP (log tail + tool calls + LLM proxy + upload progress per session) but must be documented; otherwise operators cannot tune `maxConcurrent` when moving to heavier/lighter tool workloads, and memory sizing for the gateway pod (file descriptors, goroutine stacks, TLS buffers) has no published basis.
**Recommendation:** Add a §10.1 table: `streams_per_session = log_tail_stream + tool_call_streams_p99 + llm_proxy_streams + upload_streams`. Derive `maxConcurrent` as `maxSessionsPerReplica × streams_per_session × safety_factor`. Document gateway pod memory floor as `maxConcurrent × avg_stream_buffer_bytes`. Add a metric `lenny_gateway_streams_per_session{p99}` and an alert when p99 exceeds the sizing assumption, since tool-heavy workloads could push the ratio past 50:1 and saturate `maxConcurrent` without saturating `maxSessionsPerReplica`.

### PRF-012. HPA Scale-Up `stabilizationWindowSeconds: 0` Paired with PDB-Bound Scale-Down Creates Oscillation Risk [Low]

**Section:** spec/10_gateway-internals.md §10.1, spec/17_deployment-topology.md §17.8.2

Gateway scale-up uses `stabilizationWindowSeconds: 0` (immediate reaction) with up to `8 pods / 15s`, while scale-down is `3 pods / 60s` and — per PRF-006 — further throttled by `maxUnavailable: 1` to ~1 pod/60s+. On bursty diurnal traffic, the gateway will aggressively spin up (8 pods/15s), briefly over-provision, then take 25+ minutes to wind down. For workloads with short bursts (e.g., batch jobs that run for 5-10 min), the gateway will *never* scale down between bursts, cementing cost at peak levels. This is a cost/efficiency finding, not a correctness bug, but it undermines the published Tier 3 cost model.
**Recommendation:** Add `stabilizationWindowSeconds: 60-120` to scale-up (still sub-HPA-eval-tick but smooths 15s spikes), or set `scaleUp.policies.periodSeconds: 30` with `value: 4` to halve the burst rate. Publish a "minimum burst duration for economical auto-scale" metric — if bursts are shorter than the PDB-bound scale-down window, the platform should recommend an elevated `minReplicas` rather than relying on autoscaling. Include a cost-recovery example: "A 10-min burst at Tier 3 adds 25 replicas × ~25 min retention × $X/replica-hour = $Y unrecovered capacity."

---

## 5. Protocol Engineering

### PRT-012. `schema_version_ahead` annotation misused for MCP-version-deprecated sessions [Medium]

**Section:** 15.2 (MCP API, "Session-lifetime exception for deprecated versions")

Line 1186 of `spec/15_external-api-surface.md` says:

&gt; *"If a session on the deprecated version is still active after the deployment (i.e., the operator did not drain), the gateway falls back to the nonce-handshake-only serialization path with a `schema_version_ahead` degradation annotation rather than terminating the session abruptly."*

Two problems:

1. **`schema_version_ahead` semantics reversed.** `schema_version_ahead` is defined in §15.5 item 7 and §15.4.1 OutputPart obligations as the degradation signal surfaced when a consumer encounters a `schemaVersion` **higher** than it understands — i.e., "new writer, old reader." The MCP-version-deprecated case is the exact opposite: the client negotiated an MCP spec version (e.g., `2024-11-05`) whose handler has been removed from the gateway — old writer, new reader. Reusing the `schema_version_ahead` name for this case is misleading and will cause observability dashboards that filter on `schema_version_ahead` (SLO alerts, forward-compat rollout tracking) to conflate two unrelated failure modes. The correct name for this degradation would be something like `mcp_version_dropped` or `protocol_version_retired`.
2. **"Nonce-handshake-only serialization path" is undefined.** Nonce handshake is an intra-pod MCP authentication concept (§15.4.3 "Nonce wire format (v1 — intra-pod only)") — explicitly scoped to adapter↔runtime Unix-socket MCP connections, never used on external-facing MCP. External MCP clients that negotiated `2024-11-05` at gateway-edge `initialize` time have never used, and will never use, a nonce. A "nonce-handshake-only serialization path" for external-facing sessions is not defined anywhere in the spec, and the mechanism is incoherent (a nonce is a one-shot validation, not a serialization format). The fallback appears to be cross-wired from intra-pod MCP text.

The session-lifetime-exception paragraph needs a deliberate answer: either (a) the gateway *continues* to serialize in the deprecated version (handler retained in a "zombie" mode), or (b) the session falls back to a degraded read-only state, or (c) the session is terminated with a structured error. Whichever, it needs an unambiguously-named degradation annotation separate from `schema_version_ahead`.

**Recommendation:** Rewrite line 1186. Replace "nonce-handshake-only serialization path" with a defined fallback mode (e.g., "the gateway retains the deprecated version's serializer for active sessions but rejects new connections — active sessions continue with no degradation signal beyond the `X-Lenny-Mcp-Version-Deprecated` header already emitted during the deprecation window"), and rename the degradation annotation to `mcp_protocol_version_retired` with fields `{"retiredVersion": "2024-11-05", "currentVersions": ["2025-03-26", "2025-06-18"]}`. Update §15.5 item 7 to list this annotation alongside `schema_version_ahead` as a distinct degradation kind.

---

### PRT-013. `MCPAdapter` tool_use event observability surface is unspecified — conflated with elicitation path [Medium]

**Section:** 15.2.1 (REST-only operations paragraph), SessionEvent Kind Registry (`tool_use` row)

The `SessionEvent` Kind Registry row for `tool_use` (§15, line 469) enumerates phases `requested | approved | denied | completed` and says "one event per phase transition." These events are pushed via `OutboundChannel.Send`. For the `A2AAdapter`, §21.1 explicitly omits `tool_use` from `SupportedEventKinds` because `elicitationDepthPolicy: block_all` is in effect.

For the `MCPAdapter` — the built-in V1 adapter — the spec has **not documented** whether tool_use events are delivered over the Streamable HTTP session stream, nor how they map to MCP wire frames. The only MCP-specific text on tool-use (§15.2.1 "REST-only operations", line 1226) says:

&gt; *"The tool-use approval and elicitation response/dismiss endpoints carry no MCP tool equivalents because MCP clients receive and resolve these prompts through the native MCP **Elicitation** feature — the gateway's MCPAdapter surfaces pending elicitations and tool-approval requests as MCP elicitation exchanges on the session's streaming transport, and the client's response flows back over that same channel."*

Three unresolved issues:

1. **Observability ≠ approval.** Collapsing `tool_use` into the MCP Elicitation path conflates two distinct concerns. A client may want to **observe** tool call phase transitions (e.g., for a UI timeline, logging, audit) without wanting to **drive** approvals. MCP elicitation is inherently a request/response prompt; it is not suitable for emitting `phase: "completed"` events after the agent has already received a result. If the MCPAdapter surfaces tool_use exclusively via elicitations, then `phase: requested` for an auto-approved tool call has no delivery channel, and `phase: completed` has no delivery channel. That breaks the "one event per phase transition" contract in the Kind Registry.
2. **`MCPAdapter.OutboundCapabilities()` not declared.** The spec says built-in adapters "embed BaseAdapter and inherit its no-op `OutboundCapabilities` / `OpenOutboundChannel` implementations unchanged" (§21.1 line 29). That makes `MCPAdapter.OutboundCapabilities()` return an empty declaration — which, per the dispatch-filter rule (§15 line 473), means the MCPAdapter receives **no** `SessionEvent`s at all. But the MCPAdapter is the primary streaming surface (`attach_session` returns a streaming task; keepalive, `resumeFromSeq`, replay buffer are all specified). So either the MCPAdapter does implement `OpenOutboundChannel` (and `OutboundCapabilities()` must declare what it handles, including which `SessionEventKind`s are delivered as native MCP notifications vs as MCP elicitations) or its streaming surface is entirely separate from the `OutboundChannel` contract — and the spec needs to say which.
3. **MCP wire mapping for `SessionEvent.Payload`.** For each declared kind, the spec needs to say what the MCP wire frame looks like (notification method name, params shape). `state_change` → ? `output` → MCP `TextContent` block chunks? `tool_use requested` → MCP elicitation? `tool_use completed` → ? Without this, a third-party MCPAdapter implementor cannot pass the `RegisterAdapterUnderTest` matrix for the event-stream resume test (§15.2.1 line 1233) — they don't know what frames to replay.

**Recommendation:** Add an "`MCPAdapter` OutboundChannel mapping" block to §15.2. Declare `MCPAdapter.OutboundCapabilities()` explicitly: `PushNotifications: true`, `SupportedEventKinds: []SessionEventKind{SessionEventStateChange, SessionEventOutput, SessionEventElicitation, SessionEventToolUse, SessionEventError, SessionEventTerminated}`. Then, for each kind, specify the MCP wire projection — e.g., `state_change` as MCP task status notifications, `output` as MCP streaming text/content blocks on the `attach_session` task, `tool_use requested (requires approval)` as an MCP Elicitation, `tool_use requested (auto-approved)` + `tool_use completed` as an MCP notification (define the notification method name), etc. Cross-reference the Kind Registry. Without this, the MCP surface for event observability is ambiguous, and the `A2AAdapter` — whose tool_use omission rationale hinges on the closed-enum dispatch-filter contract — is held to a higher specification standard than the platform's own built-in MCP adapter.

---

### PRT-014. `AuthorizedRuntime.AgentInterface` is a string, but `agentInterface` is a structured descriptor [Medium]

**Section:** 15 Shared Adapter Types (lines 385–395), 5.1 `agentInterface` Field

The `AuthorizedRuntime` struct (§15, lines 381–408, iter3-fixed per PRT-010) declares:

```go
// AgentInterface is the runtime's declared agent interface descriptor
// ([Section 5.1 `agentInterface` Field](05_runtime-registry-and-pool-model.md#agentinterface-field)),
// used by adapters to auto-generate discovery formats (A2A agent cards,
// MCP `list_runtimes` response, REST runtime summaries). Empty string
// for `type: mcp` runtimes, which do not carry an `agentInterface`.
AgentInterface string
```

But §5.1 describes `agentInterface` as a structured block containing `supportsWorkspaceFiles: true`, capability info used for "A2A card auto-generation" and "adapter manifest summaries" — clearly an object, not a single string. §5.1 Card generation says the gateway "generates an A2A agent card at write time … drawn from `agentInterface`" with fields such as `supportsWorkspaceFiles`; the generator reads multiple fields.

If `AuthorizedRuntime.AgentInterface` is an opaque `string`, adapters cannot construct A2A agent cards (they don't have the structured fields), nor MCP `list_runtimes` response shapes that expose `supportsWorkspaceFiles`. And since PRT-010's iter3 fix made `AuthorizedRuntime` "the normative schema for the `GET /v1/runtimes` response," REST clients also receive an opaque string for a field documented (via §5.1 link) as structured.

This regresses on PRT-010's stated "make the type the normative schema" principle: the type is normative in name only; its fields still disagree with §5.1.

**Recommendation:** Either (a) define `AgentInterface` as a struct type (e.g., `AgentInterface struct { SupportsWorkspaceFiles bool; … }`) in Shared Adapter Types and cross-reference §5.1, or (b) leave the rendered card as a `publishedMetadata` entry (type: `agent-interface`) and remove the `AgentInterface` field from `AuthorizedRuntime`, relying on the `publishedMetadata[]` refs to carry structured agent-interface data. The current state — typed as `string` with a link to a structured definition — is an unresolvable mismatch for adapter authors.

---

### PRT-015. `publishedMetadata`-only discovery regresses MCP `list_runtimes` tool preview contract [Low]

**Section:** 15 (line 501), 15.1 `GET /v1/runtimes` row (line 596)

PRT-010's iter3 fix moved `mcpCapabilities.tools` preview from a top-level `AuthorizedRuntime` field to a `mcp-capabilities` `publishedMetadata` entry, fetched via `GET /v1/runtimes/{name}/meta/{key}`. §15 line 501 says: *"a `mcp-capabilities` `publishedMetadata` entry carries the tools preview and is fetched via `GET /v1/runtimes/{name}/meta/{key}`."*

This introduces two MCP-specific regressions:

1. **Double-fetch for `list_runtimes`.** MCP's `list_runtimes` tool used to include the MCP tools preview inline (per the pre-iter3 §15.1 response). Post-fix, an MCP client performing discovery must first call `list_runtimes`, then, for each runtime, call the gateway's `GET /v1/runtimes/{name}/meta/mcp-capabilities` endpoint. There is no MCP tool equivalent for the metadata fetch — the client has to fall back to REST mid-discovery, which breaks the "MCP-native client" story for discovery. A `list_runtime_metadata` MCP tool or a mechanism for the MCP adapter to inline `publishedMetadata` refs in its response was not added in the iter3 fix.
2. **Opaque pass-through defeats capability-based filtering.** Before the fix, a client could filter `list_runtimes` results by `mcpCapabilities.tools[].name` — "show me runtimes that have a `read_repository` tool." Because `publishedMetadata` is "opaque pass-through" (§5.1, line 265), the gateway does not parse it; the filter has to happen client-side after fetching every runtime's full metadata. For deployments with hundreds of runtimes, this is O(N) extra HTTP calls per discovery.

**Recommendation:** Either (a) add a `list_runtime_metadata` MCP tool that batch-fetches `publishedMetadata` entries by key for a set of runtimes, or (b) allow the `MCPAdapter` to inline specific well-known publishedMetadata keys (e.g., `mcp-capabilities`) in the `list_runtimes` response — a small carve-out from the opaque-pass-through rule for gateway-generated cards that carry a `generatorVersion` envelope field. Document the chosen path in §15.2 and cross-reference from §5.1 `publishedMetadata`.

---

### PRT-016. MCP target-version currency note remains stale (re-raised from PRT-011/PRT-007) [Low]

**Section:** 15.2 line 1171, 15.4.3 line 1862

Prior finding PRT-011 (iter3) and PRT-007 (iter2) flagged the MCP target-version currency text. The iter3 summary.md records the fix status for PRT-011 as **empty** (line 329–331 has no "Fix:" or "Resolution:" block, unlike every other Fixed finding in the same section). A grep on iter4's current spec confirms:

- Line 1171: *"Target MCP spec version: MCP 2025-03-26 (latest stable at time of writing)"* — unchanged from iter1.
- Line 1862: *"the adapter's local MCP servers speak MCP 2025-03-26 (the platform's target MCP spec version…)"* — unchanged.

Third appearance of this finding across iterations; iter3 did not apply a fix despite being open.

**Recommendation:** As previously recommended in PRT-007/PRT-011: replace "latest stable at time of writing" with a deterministic currency rule (rebase cadence, ownership, validation gate). Apply the same rule to both §15.2 line 1171 and §15.4.3 line 1862 so they cannot drift. If the policy is deliberately deferred (e.g., "revisit at GA"), state that explicitly rather than leaving the stale parenthetical in place across four review cycles.

---

### PRT-017. `OutboundSubscription.ResponseWriter` hardcodes `net/http` and breaks non-HTTP adapters [Low]

**Section:** 15 Shared Adapter Types (line 104)

`OutboundSubscription` carries delivery context for an active push channel:

```go
type OutboundSubscription struct {
    CallbackURL string
    ResponseWriter http.ResponseWriter
    Metadata map[string]string
}
```

Two issues with typing this on `http.ResponseWriter`:

1. **Protocol-specific assumption in a generic type.** The type is passed to every adapter's `OpenOutboundChannel` regardless of transport. Any future adapter that isn't HTTP-based (e.g., a gRPC streaming adapter, WebSocket adapter, or a transport where "long-poll" means holding a raw TCP socket) has no valid value to assign. The field effectively says "Lenny adapters must be HTTP." The spec's multi-protocol stance (A2A, AP, OpenAI, MCP, future protocols per §15) is undermined by this field's type.
2. **Abstraction-level mismatch.** `http.ResponseWriter` is a low-level interface holding a connection-local write buffer. Adapter authors shouldn't need to reason about `Hijacker`, `Flusher`, or trailer semantics to push events — those are HTTP plumbing concerns that a higher-level "event sink" abstraction could hide. For example, an adapter that batches events for webhook POSTs doesn't need `ResponseWriter` at all; an SSE adapter needs `Flusher`; a long-poll adapter needs connection hijack. The current single type forces all three to coexist in one field.

Not a V1-blocker — v1 built-ins (MCP, OpenAI, Open Responses) are all HTTP-based, and A2A is HTTP-based. But it is a future-proofing gap for the "runtime-agnostic" + multi-protocol story.

**Recommendation:** Replace `ResponseWriter http.ResponseWriter` with an adapter-owned sink interface:

```go
// OutboundSink is an adapter-specific delivery target. The concrete type
// is set by the adapter at OpenOutboundChannel time; the gateway treats it
// as opaque.
type OutboundSink interface{ io.Closer }

type OutboundSubscription struct {
    CallbackURL string
    Sink        OutboundSink   // nil for webhook-only adapters
    Metadata    map[string]string
}
```

Let adapters type-assert to their own concrete subtypes (`HTTPResponseSink`, `SSEWriter`, `GRPCStreamSink`, etc.) inside `OpenOutboundChannel`. The gateway never touches the field; adapters are solely responsible for their delivery semantics. Alternative: remove `ResponseWriter` entirely and rely on the adapter to capture the connection context during `HandleInbound` before returning.

---

## 6. Developer Experience & SDK

### DXP-009. §15.7 Protocol codec description contradicts the stdin/stdout + Unix-socket contract [High]

**Section:** 15.7

§15.7 "What the SDKs provide / Protocol codec" describes `"Line-delimited JSON framing, abstract Unix socket setup (@lenny-&lt;pod_id&gt;-ctl), mTLS handshake using gateway-issued certs, graceful shutdown"`. This is wrong on three load-bearing points that runtime authors will rely on when choosing what the SDK abstracts:

1. The adapter↔binary protocol (§15.4.1, §4.7) is stdin/stdout JSON Lines — there is no `@lenny-&lt;pod_id&gt;-ctl` abstract Unix socket. The only abstract sockets exposed to the runtime are `@lenny-platform-mcp`, per-connector `@lenny-connector-&lt;id&gt;`, and `@lenny-lifecycle` (§4.7 manifest).
2. The runtime binary does not speak mTLS. mTLS is the adapter↔gateway transport (§4.7 "internal gRPC/HTTP+mTLS API — gateway ↔ adapter"). The runtime never sees a gateway cert; authentication is by manifest nonce + `SO_PEERCRED` on intra-pod sockets (§4.7, §15.4.3).
3. "Gateway-issued certs" in the runtime process would imply a cert issuance step the Startup Sequence in §4.7 does not describe.

Authors targeting Basic level will look at this bullet, assume mTLS is required, and either depend on an SDK they didn't need or abandon the task.

**Recommendation:** Rewrite the "Protocol codec" bullet to match §15.4.1 / §4.7: stdin/stdout JSON Lines framing for the binary protocol, abstract Unix socket setup for the Standard-level platform MCP / connector sockets and the Full-level lifecycle channel, manifest-nonce handshake for intra-pod auth, no mTLS in the runtime process, and credential-file (`/run/lenny/credentials.json`) access patterns.

### DXP-010. §15.7 lists MCP helper tools that aren't in the platform MCP tool set [High]

**Section:** 15.7, 4.7

§15.7 "Tool call plumbing" advertises "Typed helpers for `lenny/tool_call`, `lenny/delegate_task`, `lenny/send_message`, `lenny/request_elicitation`, `lenny/interrupt`, and `lenny/ready`." §4.7 defines the authoritative platform MCP tool list, and it contains none of `lenny/tool_call`, `lenny/interrupt`, or `lenny/ready`. `lenny/tool_call` is the adapter/protocol name for outbound tool invocations (via stdout `{type: "tool_call"}`, not an MCP tool callable by the runtime); interrupt is a gateway-initiated lifecycle signal, not an MCP tool; `lenny/ready` is not defined anywhere. Runtime authors who rely on the SDK "typed helpers" will either get compile errors (the helper doesn't exist because the tool doesn't exist) or will be misled about the platform contract.

**Recommendation:** Replace the list with the §4.7 authoritative tool set: `lenny/delegate_task`, `lenny/await_children`, `lenny/cancel_child`, `lenny/discover_agents`, `lenny/output`, `lenny/request_elicitation`, `lenny/memory_write`, `lenny/memory_query`, `lenny/request_input`, `lenny/send_message`, `lenny/get_task_tree`, `lenny/set_tracing_context`. Remove or define `lenny/tool_call`, `lenny/interrupt`, `lenny/ready`.

### DXP-011. `integrationLevel` Runtime field is used by §17.4 / §15.4.6 but undefined in §5.1 [High]

**Section:** 5.1, 15.4.6, 17.4

The primary-path Embedded Mode walkthrough in §17.4 instructs runtime authors to write `integrationLevel: basic` in their `runtime.yaml`. §15.4.6 then says `lenny runtime validate` "reads `runtime.yaml` to discover the claimed integration level". But §5.1 (Runtime schema) never defines an `integrationLevel` field, and none of the reference runtime definitions in §26.3–§26.11 set one — they declare their level only in prose/README. The gateway's actual inference of the level is from the adapter's `lifecycle_support` handshake (§4.7). A third-party author will either (a) be rejected at registration if the field is validated, or (b) write the field and have it silently ignored while `lenny runtime validate` reports an unclaimed level.

**Recommendation:** Either (1) add `integrationLevel: basic | standard | full` to the Runtime schema in §5.1 with §5.1 inheritance rules, have reference runtimes in §26 declare it, and wire `lenny runtime validate` / admission checks to read it; or (2) remove the field from the §17.4 example and have `lenny runtime validate` discover the level by probing behaviour (lifecycle-channel connect, MCP nonce handshake) rather than reading `runtime.yaml`. Pick one and unify across §5.1, §17.4, §15.4.6, and §24.18.

### DXP-012. `lenny image import` and `lenny token print` are undocumented commands used in the primary-path walkthrough [High]

**Section:** 17.4, 24

The Embedded Mode custom-runtime walkthrough (§17.4, lines 263, 283) uses `lenny image import my-agent:dev` and `$(lenny token print)`. §24 ("Lenny-ctl Command Reference") documents `lenny image` nowhere (no 24.x entry mentions `image`) and `lenny token` nowhere (no 24.x entry mentions `token print`; §24.9 is "User and Token Management" but references a different surface). A runtime author following the primary-path walkthrough will get "command not found" and have no documentation to diagnose.

**Recommendation:** Add both commands to §24. A candidate placement is a new "Image Management" subsection for `lenny image import` (loads an image into the embedded k3s containerd store) and an "Auth Helpers" entry for `lenny token print` (prints a bearer token for the local Embedded Mode operator), each with parameters, prerequisites, and Min Role.

### DXP-013. §15.7 `Handler` interface references undefined `CreateRequest`, `Message`, `Reply` types [Medium]

**Section:** 15.7

The Go API surface in §15.7 declares:

```
OnCreate(ctx context.Context, req CreateRequest) error
OnMessage(ctx context.Context, msg Message) (Reply, error)
OnTerminate(ctx context.Context, reason TerminationReason) error
```

`TerminationReason` is fully defined in §15 (with enum values). `CreateRequest`, `Message`, and `Reply` are referenced only in this one paragraph and never defined anywhere in the spec. These are the three core types every runtime author touches on every message. Authors who try to implement the interface cannot know what fields to read from `Message`, what fields their `Reply` must populate, or how `CreateRequest` relates to the `AssignCredentials` / `StartSession` RPCs and the adapter manifest.

**Recommendation:** Define the three types alongside `TerminationReason` in §15 (or in §15.7). Minimum: `Message` should expose the `MessageEnvelope` fields from §15.4.1 plus the manifest-derived session/task IDs; `Reply` should carry the `OutputPart[]` array plus streaming flags; `CreateRequest` should carry the `WorkspacePlan`, `runtimeOptions`, provider leases map, and adapter manifest snapshot (or a reference to it).

### DXP-014. §15.7 scaffolder paragraph still universalizes SDK usage despite §24.18 no-SDK carve-out [Medium]

**Section:** 15.7, 24.18

Iter3 DXP-006 flagged this — §24.18 was updated with the `--language binary --template minimal` no-SDK carve-out, but the §15.7 scaffolder paragraph (end of the section) still says the generated `main.&lt;lang&gt;` "using the SDK's `Handler` interface" without qualifying the binary/minimal exception. A runtime author reading §15.7 top-down still gets the wrong impression that every scaffold uses the SDK.

**Recommendation:** Qualify the §15.7 sentence: "generates a new runtime skeleton: `Dockerfile`, `main.&lt;lang&gt;` using the SDK's `Handler` interface (except for `--language binary --template minimal`, which emits a no-SDK stdin/stdout skeleton; see §24.18), ...".

### DXP-015. §24.18 scaffolder cross-product has 12 combinations but only one is specified [Medium]

**Section:** 24.18

`lenny runtime init &lt;name&gt; --language {go|python|typescript|binary} --template {chat|coding|minimal}` declares a 4×3 = 12 combination matrix. The footnote in §24.18 specifies only what `--language binary --template minimal` emits (no-SDK stdin/stdout skeleton). Nothing defines the output for the remaining eleven combinations — in particular:
- `--language binary --template coding` — does this emit a Basic-level runtime that somehow uses `/workspace/current/` coding conventions without MCP? The `coding` template's pre-wired shared coding-agent workspace (§26.2) expects at least `lenny/output` and the platform MCP server, which are Standard-level requirements.
- `--language binary --template chat` — the `chat` reference runtime (§26.7) is Full-level with LLM-proxy credential access; a binary/chat skeleton without the SDK's credential wrapper is underspecified.
- `--language {go|python|typescript} --template minimal` — does `minimal` still pull in the SDK, or is it always SDK-free?

Runtime authors will guess and file bugs.

**Recommendation:** Either (1) disallow invalid combinations (e.g., reject `--language binary --template {chat, coding}` with a clear error in the CLI and update the §24.18 table to list only the supported pairs), or (2) add a matrix in §24.18 describing each combination's emitted skeleton (integration level, SDK presence, workspace plan pre-wiring).

### DXP-016. §26.1 "scaffolder copies one of these as a template" misrepresents the §24.18 template set [Medium]

**Section:** 26.1, 24.18

§26.1 paragraph 2 reads: "Teams building Standard- or Full-level runtimes SHOULD start from the scaffolder (`lenny-ctl runtime init`, [§24.18]), which copies one of these as a template." The antecedent of "one of these" is the reference runtime catalog (`claude-code`, `gemini-cli`, …). But §24.18 only offers three templates — `chat`, `coding`, `minimal` — and none of them corresponds one-to-one to a reference runtime entry. An author following this pointer will run `lenny runtime init myrt --template claude-code` and get an unknown-template error.

**Recommendation:** Rewrite the sentence: "…start from the scaffolder (`lenny-ctl runtime init`, [§24.18]), which emits one of three templates (`chat`, `coding`, `minimal`). The `coding` template shares the workspace conventions from [§26.2] used by `claude-code`, `gemini-cli`, `codex`, and `cursor-cli`; the `chat` template shares the conventions from the `chat` reference runtime (§26.7). There is no per-reference-runtime template." Or add per-reference-runtime templates to §24.18 if that is the intent.

### DXP-017. §26.12 references `github.com/lennylabs/runtime-templates` repo but never defines its role [Low]

**Section:** 26.12

§26.12 tells new-reference-runtime authors to PR to `github.com/lennylabs/runtime-templates`, but this repo is mentioned nowhere else in the spec and its relationship to the per-runtime repos (`github.com/lennylabs/runtime-claude-code` etc. in §26.3–§26.11) is undefined. Authors won't know whether to put code in `runtime-templates` or in a new `runtime-&lt;name&gt;` repo, whether the former holds scaffolder source / template assets / proposals, or whether the scaffolder tool in §24.18 is distinct from a "template" repo.

**Recommendation:** Add one sentence describing `runtime-templates`' purpose (e.g., "holds the canonical scaffolder template source consumed by `lenny runtime init`; PRs here update the skeletons every author starts from"), and clarify that per-runtime repos (`runtime-&lt;name&gt;`) are the actual implementation homes. If the scaffolder templates are embedded in the `lenny-ctl` binary (consistent with §15.4.6's "fixtures ship inside the lenny binary"), say so.

### DXP-018. §15.4.3 Full-level pseudocode mixes `deadline_signal` capability name with `deadline_approaching` message name [Low]

**Section:** 15.4.3, 4.7, 15.4.6

§4.7 `lifecycle_capabilities.capabilities` enum advertises the string `"deadline_signal"`. The actual delivered message is `{type: "deadline_approaching"}`. §15.4.4 Full-level pseudocode declares `supported = ["checkpoint", "interrupt", "deadline_signal"]` and then switches on `"deadline_approaching"` — which is correct but visually jarring and invites typos. §15.4.6 test label reads "deadline signal handling". Three names for one concept (capability string, message type, test label) makes for confusing SDK code and flaky assertions.

**Recommendation:** Pick one root noun and stick to it. For example: capability `"deadline_signal"` stays; message type becomes `{type: "deadline_signal"}`; test label stays "deadline signal handling". Or rename both capability and test to match `deadline_approaching` if the message name is preferred. Update §4.7, §15.4.4, §15.4.6, and §26.2 cross-refs.

### DXP-019. §15.7 scaffolder description still implies universal SDK use (iter3 DXP-006 carry-over) [Low]

**Section:** 15.7

Iter3 DXP-006 asked to qualify the scaffolder paragraph. §24.18 has the qualification in its own footnote; §15.7 still does not. Reporting separately from DXP-014 only because iter3 already marked this a Low.

**Recommendation:** Same as iter3 DXP-006 / DXP-014 — qualify the §15.7 sentence with the `--language binary --template minimal` exception.

### DXP-020. §26.1 "`local` profile installations" terminology undefined (iter3 DXP-007 carry-over) [Low]

**Section:** 26.1

Iter3 DXP-007 flagged this and it is unchanged. `local` is not an Operating Mode named in §17.4 (Embedded / Source / Compose), nor an Install Profile defined in §17.6 (values-profile layering), nor a Helm install flavor. `lenny up` is Embedded Mode; `make run` is Source Mode.

**Recommendation:** Same as iter3 — replace "`local` profile installations" with "Embedded Mode installations" (i.e., `lenny up`) and cross-reference §17.4.

### DXP-021. §17.4 Embedded-Mode custom-runtime walkthrough omits tenant-access grant for non-`default` tenants (iter3 DXP-008 carry-over) [Low]

**Section:** 17.4

Iter3 DXP-008 flagged this and it is unchanged. The §26.1 "Tenant access" paragraph says reference runtimes have no default tenant access grants and operators must call `POST /v1/admin/runtimes/{name}/tenant-access` (with an Embedded-Mode auto-grant to the `default` tenant). A runtime author registering a custom runtime via `lenny-ctl runtime register --file runtime.yaml` against a non-default tenant will find it unbound and get `RUNTIME_NOT_AUTHORIZED` (or equivalent) at session creation.

**Recommendation:** Append a step 3b in §17.4: "If invoking from a non-`default` tenant, grant access via `lenny-ctl tenant add-runtime-access --tenant &lt;id&gt; --runtime my-agent` (or `POST /v1/admin/runtimes/my-agent/tenant-access`)."

---

## 7. Operations & SRE Ergonomics

### OPS-010. `lenny-ops` Helm values `backups.erasureReconciler.*` and `minio.artifactBackup.*` missing from §17.8.1 operational defaults table [Low]

**Section:** `17_deployment-topology.md` §17.8.1 (lines 809–851); `25_agent-operability.md` §25.4 (Helm values block, lines ~924–973), §25.11 (ArtifactStore Backup subsection).

The iter3 fix passes for CMP-046 (post-restore GDPR erasure reconciler) and CMP-048 (MinIO ArtifactStore bucket replication) added several operator-facing Helm values in the `§25.4` canonical values block:

- `backups.erasureReconciler.enabled` (default `true`)
- `backups.erasureReconciler.legalHoldLedgerFreshnessGate` (default `true`)
- `minio.artifactBackup.enabled`
- `minio.artifactBackup.target.*` (endpoint, bucket, credentials, kmsKeyId)
- `minio.artifactBackup.versioning` (default `true`)
- `minio.artifactBackup.replicationLagRpoSeconds` (operator-facing RPO knob backing the `MinIOArtifactReplicationLagHigh` alert)

§17.8.1's header at line 806 reads *"All tunable defaults collected in one place for operator convenience."* — but the table covers only pre-iter3 defaults. An operator scanning §17.8.1 for backup/erasure/artifact-replication tunables after an iter3 alert fires finds nothing and will incorrectly conclude the knobs are non-existent. This is the same operator-discoverability gap OPS-008 already flags for `ops.drift.*`, extended to the new iter3 operator surface.

**Recommendation:** Add four rows to §17.8.1 pointing to §25.4 / §25.11:

| Setting | Default | Reference |
| --- | --- | --- |
| Post-restore GDPR erasure reconciler (`backups.erasureReconciler.enabled`) | `true` | [§25.11](25_agent-operability.md#2511-backup-and-restore), [§12.8](12_storage-architecture.md#128-compliance-interfaces) |
| Legal-hold ledger freshness gate (`backups.erasureReconciler.legalHoldLedgerFreshnessGate`) | `true` | [§25.11](25_agent-operability.md#2511-backup-and-restore) |
| ArtifactStore MinIO cross-region replication (`minio.artifactBackup.enabled`) | `false` (opt-in) | [§25.11](25_agent-operability.md#2511-backup-and-restore) ArtifactStore Backup |
| ArtifactStore replication-lag RPO (`minio.artifactBackup.replicationLagRpoSeconds`) | 300 s | [§25.11](25_agent-operability.md#2511-backup-and-restore), [§16.5](16_observability.md#165-alerting-rules-and-slos) |

Commit as a single edit alongside the OPS-008 fix to keep the defaults table internally consistent.

---

### OPS-011. `issueRunbooks` lookup table omits `BackupReconcileBlocked` and `MINIO_ARTIFACT_REPLICATION_*` codes introduced in iter3 [Low]

**Section:** `25_agent-operability.md` §25.7 Path B lookup (lines 3069–3078); `17_deployment-topology.md` §17.7 line 698 enumeration; §25.11 alerting-rules table (lines 4136–4141).

The `issueRunbooks` map literal at line 3069 still enumerates only the pre-iter3 eight entries (`WARM_POOL_EXHAUSTED` … `CIRCUIT_BREAKER_OPEN`). Iter3 added:

- `BackupReconcileBlocked` **Critical** alert (line 4141), with a clearly defined agent-facing resolution path (`POST /v1/admin/restore/{id}/confirm-legal-hold-ledger`).
- `MinIOArtifactReplicationLagHigh` / `MinIOArtifactReplicationFailed` alerts (per CMP-048 ArtifactStore Backup subsection in §25.11).

None of these have a corresponding entry in the `issueRunbooks` map — which is the exact §25.7 Path B convention that `WARM_POOL_LOW`, `CERT_EXPIRY_IMMINENT` et al. rely on to get a programmatic `runbook:` pointer to agents consuming the health API. The sentence at §17.7 line 698 ("entries for … are required by §25.7 Path B") enumerates the same eight and has not been extended either.

This is the same pattern already filed as OPS-009 for `DRIFT_SNAPSHOT_STALE`, reintroduced by the iter3 fix pass for both CMP-046 and CMP-048 (every new alert ships without the matching `issueRunbooks` row). Agents get a `BackupReconcileBlocked` alert event and no `runbook:` field — they must fall back to Path C (full-list scan), breaking the cheaper Path B convention.

**Recommendation:** Extend the `issueRunbooks` map in §25.7 to include the iter3 codes (the runbook slugs should be added under OPS-012 below):

```go
"DRIFT_SNAPSHOT_STALE":           "drift-snapshot-refresh",
"BACKUP_RECONCILE_BLOCKED":       "post-restore-reconciler-blocked",
"MINIO_ARTIFACT_REPLICATION_LAG": "artifactstore-replication-recovery",
"MINIO_ARTIFACT_REPLICATION_FAILED": "artifactstore-replication-recovery",
```

And amend the §17.7 line 698 sentence to enumerate these four additional codes. If `BACKUP_RECONCILE_BLOCKED` is only surfaced as an alert (not a health-API issue code), also add the `runbook` annotation to the Prometheus rule so the §25.5 `alert_fired` event path carries the pointer.

---

### OPS-012. §17.7 runbook catalog missing entries for post-restore reconciler block and ArtifactStore replication recovery [Low]

**Section:** `17_deployment-topology.md` §17.7 (lines 690–900+); `25_agent-operability.md` §25.11 (Post-restore reconciler &amp; ArtifactStore Backup subsections, lines ~3869–4161); §25.14 `lenny-ctl` table line 4772.

Iter3 added two new operator-facing recovery procedures but no corresponding §17.7 runbook entries:

1. **`BackupReconcileBlocked` (CMP-046).** The restore flow aborts mid-way with the `restore:platform` lock retained when `ledgerLatestWriteAt &lt;= backupTakenAt`. The spec documents what the operator must do (investigate → confirm legal-hold ledger currency via `POST /v1/admin/restore/{id}/confirm-legal-hold-ledger` or `lenny-ctl restore confirm-legal-hold-ledger &lt;id&gt; --justification &lt;text&gt;`), but the steps are scattered across §25.11 error-code table (line 4155), §25.14 lenny-ctl table (line 4772), and §12.8 compliance interfaces. No single runbook file collects the trigger/diagnosis/remediation structure that §17.7 otherwise standardises.

2. **`MinIOArtifactReplicationLagHigh` / `MinIOArtifactReplicationFailed` (CMP-048).** §25.11 ArtifactStore Backup describes promoting the replication target during an ArtifactStore restore, but there is no §17.7 "artifactstore-replication-recovery" runbook that an agent encountering the alert can pull via `GET /v1/admin/runbooks/{name}/steps`. Without it, agents fall back to Path C text scanning across §25.11 prose.

The §17.7 catalog currently enumerates 18-19 runbooks covering Postgres, Redis, MinIO-gateway, token service, credential pool, etc. The two missing entries are exactly the operator-surface additions the iter3 fixes introduced, and they are the two most likely to wake an on-call human/agent in the near term after upgrade.

**Recommendation:** Add two stubbed runbook entries to §17.7 (matching the existing `&lt;!-- access: trigger --&gt;` / `&lt;!-- access: diagnosis --&gt;` / `&lt;!-- access: remediation --&gt;` machine-consumable structure):

- **`post-restore-reconciler-blocked.md`** — *Trigger:* `BackupReconcileBlocked` alert; `gdpr.backup_reconcile_blocked` audit event; `GET /v1/admin/restore/{id}/status` returns `phase: "reconciler_blocked"`. *Diagnosis:* compare `backupTakenAt` vs `ledgerLatestWriteAt`; enumerate subject IDs pending reconciliation; check whether any legal hold transitions occurred between backup and restore by consulting out-of-band evidence (ticket system, email trail). *Remediation:* if evidence establishes ledger currency, call `POST /v1/admin/restore/{id}/confirm-legal-hold-ledger` (or `lenny-ctl restore confirm-legal-hold-ledger`); if uncertain, abort the restore and take a fresh backup that includes current ledger state, then retry.

- **`artifactstore-replication-recovery.md`** — *Trigger:* `MinIOArtifactReplicationLagHigh` or `MinIOArtifactReplicationFailed`. *Diagnosis:* MinIO replication metrics; source/target bucket reachability; KMS key status in target region. *Remediation:* promote replication target if source is permanently lost (per §25.11 ArtifactStore restore procedure); otherwise restart the replication job after resolving the underlying connectivity/credential issue.

Pair this edit with OPS-011 so the `issueRunbooks` map and the catalog land consistent runbook slugs.

---

### OPS-013. Embedded Mode has no Postgres-major-version mismatch fail-safe (iter2 OPS-005 / iter3 OPS-007 carry-forward) [Low]

**Section:** `17_deployment-topology.md` §17.4 "State and resets" (lines 159–160).

Unchanged since iter2 and iter3 — the iter3 OPS-007 write-up applies verbatim. §17.4 covers schema migrations against embedded Postgres but says nothing about the case where the Postgres *binary major version* itself is bumped in a later Lenny release. `~/.lenny/postgres/` uses a Postgres-major-version-specific on-disk layout (§17.4 pins PostgreSQL 16); a newer `lenny` binary against an on-disk directory written by an older `embedded-postgres` major will either fail to start or crash opaquely. No `PG_VERSION` check, no documented `lenny export`/`import` path, no fail-closed error is specified. Low severity today (PG 16 pinned, no deployments in the wild), but surfaces on the first PG major bump after GA.

**Recommendation:** Add one sentence to §17.4 "State and resets": *"`lenny up` reads `~/.lenny/postgres/PG_VERSION` on start; on mismatch with the expected major, fails closed with `EMBEDDED_PG_VERSION_MISMATCH` and prints the recovery procedure (`lenny export --to &lt;path&gt;` then `lenny down --purge &amp;&amp; lenny up &amp;&amp; lenny import --from &lt;path&gt;`). In-place `pg_upgrade` is not supported in Embedded Mode."* Two-line spec addition that prevents silent data loss on the first PG major bump.

---

### OPS-014. Operational defaults table §17.8.1 still omits `ops.drift.*` tunables (iter2 OPS-006 / iter3 OPS-008 carry-forward) [Low]

**Section:** `17_deployment-topology.md` §17.8.1 (lines 809–851); `25_agent-operability.md` §25.4 (lines 924–928), §25.10.

Unchanged since iter2 and iter3 — the iter3 OPS-008 write-up still applies verbatim. §25.4 defines `ops.drift.snapshotStaleWarningDays` (default 7) and `ops.drift.runningStateCacheTTLSeconds` (default 60) as operator-facing Helm values; both are referenced in §25.10 and in `drift-snapshot-refresh.md`'s trigger blurb (§17.7 line 788). Neither appears in §17.8.1.

**Recommendation:** Add two rows to the §17.8.1 defaults table (folded into the OPS-010 edit so the defaults table pass is done once):

| Setting | Default | Reference |
| --- | --- | --- |
| Drift snapshot-staleness warning threshold (`ops.drift.snapshotStaleWarningDays`) | 7 days (0 disables) | [§25.10](25_agent-operability.md#2510-configuration-drift-detection) |
| Drift running-state cache TTL (`ops.drift.runningStateCacheTTLSeconds`) | 60 s | [§25.10](25_agent-operability.md#2510-configuration-drift-detection) |

---

### OPS-015. `issueRunbooks` lookup still missing `DRIFT_SNAPSHOT_STALE` → `drift-snapshot-refresh` mapping (iter3 OPS-009 carry-forward) [Low]

**Section:** `25_agent-operability.md` §25.7 Path B (lines 3069–3078); `17_deployment-topology.md` §17.7 line 698.

Unchanged since iter3 — `issueRunbooks` at line 3069 still enumerates only the original eight entries and §17.7 line 698 still does not include `DRIFT_SNAPSHOT_STALE`. Fold the fix into the OPS-011 edit (which covers the three additional iter3 codes) so the map picks up all four missing entries in one pass.

**Recommendation:** Add to the `issueRunbooks` map:

```go
"DRIFT_SNAPSHOT_STALE": "drift-snapshot-refresh",
```

Amend the §17.7 line 698 enumeration accordingly. Decide in the same edit whether `snapshot_stale: true` should also surface as an `alert_fired` event on the §25.5 event stream (if yes, add the alert annotation in §16.5; if no, state explicitly in §25.10 that the signal is API-response-only).

---

## 8. Multi-tenancy & Isolation

### TNT-008. Playground `apiKey` mode re-mints session JWTs without scope-narrowing invariants [Medium]

**Section:** spec/10_gateway-internals.md §10.2 (playground auth mint paths), spec/27_web-playground.md §27.3

The retarget of playground `apiKey` to a standard bearer-token paste (iter3 TNT-005 fix) specifies that the supplied bearer is validated via the same extraction table as the primary auth path, and a new playground-origin JWT is minted with `origin: "playground"` so the idle override and duration cap apply. However, the spec does not state that the re-minted JWT must be scope-narrowed to a strict subset of the subject token's claims, nor does it require the subject to be of type "user bearer" rather than "session capability" or "agent-to-agent delegation" token.

A user who holds a narrowly-scoped capability JWT (for example, one issued for a specific session or a delegated sub-task) can paste it into the playground and obtain a broader, fresh-lifetime playground JWT. This mirrors the OAuth token-exchange tenant invariant at `/v1/oauth/token` (§13.3 line 536: `issued_token.tenant_id == subject_token.tenant_id`) but lacks the symmetric scope invariant `issued_token.scope ⊆ subject_token.scope` and a subject-type restriction.

**Recommendation:** In §10.2 playground mint path and §27.3 `apiKey` mode, add two invariants: (a) subject token `typ` must be `user_bearer` (reject `session_capability`, `a2a_delegation`, and service tokens with `LENNY_PLAYGROUND_BEARER_TYPE_REJECTED`); (b) minted playground token scope must be the intersection of the subject scope and the playground-allowed scope set, never the union. Add a test matrix row covering "capability JWT pasted into playground" producing 401.

### TNT-009. OIDC playground callback tenant-claim rejection codes not cross-referenced [Medium]

**Section:** spec/27_web-playground.md §27.3.1, spec/10_gateway-internals.md §10.2

§10.2 lines 174–181 define `TENANT_CLAIM_MISSING`, `TENANT_NOT_FOUND`, and `TENANT_CLAIM_INVALID_FORMAT` as the canonical rejection codes when the gateway extracts `tenant_id` from a bearer JWT, and the retargeted `apiKey` mode (TNT-005 fix) and `dev` mode (TNT-006 fix) both cite these codes explicitly. §27.3.1 describes the OIDC cookie-to-MCP-bearer exchange at callback time but does not name the rejection codes emitted when the IdP returns an ID token without a `tenant_id` claim, with a value that doesn't match a known tenant, or with an invalid format.

This asymmetry leaves the OIDC path's error contract implicit. Operators debugging a misconfigured IdP (common scenario: tenant claim named `organization` upstream and not mapped in `playground.oidc.tenantClaim`) will see a generic 401 without the diagnostic code the other two modes provide, and a conformance test cannot assert OIDC callback behavior without inventing codes the spec didn't pin.

**Recommendation:** In §27.3.1, add a rejection-code table mirroring §10.2's extraction table, with rows `TENANT_CLAIM_MISSING` (callback returned; redirect to error page with code), `TENANT_NOT_FOUND` (extracted tenant does not match a provisioned `Tenant` CR), and `TENANT_CLAIM_INVALID_FORMAT` (value fails `^[a-zA-Z0-9_-]{1,128}$`). Specify the user-visible error URL query parameter name (e.g., `?error=tenant_claim_missing`) and confirm logs are written via the tenant-attribution logger with `tenant_id=__unset__`.

### TNT-010. `playground.devTenantId` format is validated only at gateway startup, not at Helm install [Medium]

**Section:** spec/27_web-playground.md §27.2, spec/17_deployment-topology.md §17.6

§27.2 introduces `playground.devTenantId` with fatal startup codes `LENNY_PLAYGROUND_DEV_TENANT_INVALID` and `LENNY_PLAYGROUND_DEV_TENANT_REQUIRED` (iter3 TNT-006 fix). §17.6 line 335 establishes the `global.noEnvironmentPolicy` required-value pattern, and lines 453–455 show that preflight checks cover PgBouncer pool mode and the `lenny_tenant_guard` trigger. The `playground.devTenantId` value is not mentioned in the Helm `values.schema.json` / preflight check inventory, so a typo (for example, `dev tenant` with a space, or a 200-character value) passes `helm install` and only fails at gateway pod startup — producing a CrashLoopBackOff rather than a rejected install.

This regresses the operator-experience posture established for `noEnvironmentPolicy` in §17.6 where Helm rejects misconfigurations at install time, and complicates GitOps pipelines that expect Helm rendering to fail fast.

**Recommendation:** Add `playground.devTenantId` to the Helm `values.schema.json` `pattern` constraint `^[a-zA-Z0-9_-]{1,128}$` and to the §17.6 preflight-check inventory so `helm install --dry-run` rejects invalid values. Cross-reference this from §27.2 so the startup codes become backstops rather than primary defenses. Keep `LENNY_PLAYGROUND_DEV_TENANT_REQUIRED` as the startup check for the case where `playground.auth.mode=dev` but `playground.devTenantId` is empty, since this is a cross-field validation Helm schema cannot express cleanly.

### TNT-011. OIDC playground session record backing store is unspecified, creating multi-replica logout gap [Medium]

**Section:** spec/27_web-playground.md §27.3.1, §27.6

§27.3.1 describes the OIDC callback exchanging the cookie for a minted MCP bearer and notes that logout must revoke the cookie-bound session server-side. §27.6 describes session lifecycle but does not name the backing store for the opaque server-side session record that ties a cookie to the minted bearer and its `origin: "playground"` claim. Multi-replica gateway deployments are the default topology; without a shared store, a logout request landing on replica A cannot invalidate a cookie presented to replica B, leaving a window where a leaked cookie remains usable until the minted bearer's natural expiry.

The spec elsewhere establishes per-tenant Redis prefixes `t:{tenant_id}:` with documented exceptions (§12.4 lines 177–195), and a playground session record would naturally fit there, but the spec does not pin this choice or its TTL / revocation-list semantics.

**Recommendation:** In §27.3.1, specify the backing store for the playground OIDC session record as Redis with key `t:{tenant_id}:pg:sess:{session_id}` (hash-tag on `session_id` only if cross-replica access requires it; otherwise plain prefix), TTL aligned with the minted bearer's expiry, and a revocation list `t:{tenant_id}:pg:revoked:{jti}` consulted on every authenticated request. Add a metric `playground_session_revocations_total{reason}` to §27.8. Document the multi-replica logout propagation SLO (e.g., "revocation visible within 500ms across all gateway replicas via Redis pub/sub").

### TNT-012. `tenant-admin` cross-tenant `?tenantId=` query parameter has ambiguous authorization outcome [Low]

**Section:** spec/25_agent-operability.md §25.4 (operations inventory authorization rule)

§25.4 line 1693 states tenant-admin visibility is "auto-restricted to its own tenant" but does not specify the outcome when a `tenant-admin` for tenant A issues an operations query with `?tenantId=B`. Three plausible behaviors are defensible and mutually exclusive: (i) 403 with `AUTH_CROSS_TENANT_FORBIDDEN`, (ii) silently ignore the parameter and return tenant A's data, (iii) return tenant A's data with a `Warning:` response header. Each has different audit and UX implications; choosing silently is a confused-deputy risk because automation that thinks it's querying tenant B will happily consume tenant A's data.

The same ambiguity plausibly applies to `billing-viewer` and `tenant-viewer` roles when they pass a mismatched `tenantId`.

**Recommendation:** In §25.4, add an authorization-outcome row stating that any non-platform-admin role passing a `tenantId` query parameter that does not match the role's scoped `tenant_id` claim returns `403 AUTH_CROSS_TENANT_FORBIDDEN` (never silently substituted, never warning-header). Add the same rule to any other listable endpoints (sessions, pods, events) that accept a `tenantId` filter. Confirm this aligns with the `DataResidencyViolationAttempt` / `CrossTenantAccessAttempt` audit-event taxonomy in §13 and add a metric `authz_cross_tenant_attempts_total{role, endpoint}`.

### TNT-013. Playground `apiKey` UI label invites credential misdelivery across tenants and vendors [Low]

**Section:** spec/27_web-playground.md §27.3

After the TNT-005 retarget, `playground.auth.mode=apiKey` accepts a Lenny MCP bearer token (a JWT), not an API key in the colloquial sense (opaque random string, vendor-specific prefix). UI operators are habituated to the term "API key" meaning a secret from an LLM provider (OpenAI `sk-...`, Anthropic `sk-ant-...`) or a different SaaS vendor. Users confronted with a generic "API key" paste box on a Lenny playground page are disproportionately likely to paste an unrelated vendor credential, which then (a) fails validation with `TENANT_CLAIM_MISSING` (correct behavior) but (b) lands in gateway request logs verbatim unless redaction is aggressive, potentially exfiltrating a cross-vendor credential into Lenny's log sink.

This is a multi-tenancy concern because in SaaS deployments the log sink is operator-owned, not tenant-owned — so one tenant's mispasted vendor API key becomes visible to the platform operator.

**Recommendation:** In §27.3, rename the mode identifier to `bearerToken` (keep `apiKey` as a deprecated alias for one minor version) and require the UI label to say "Lenny bearer token (JWT)" with placeholder `eyJ...`. Specify in §10.2 that playground auth failure logs MUST redact the offered bearer to `hash(sha256)[0:12]` rather than the raw string, and add a dedicated audit event `PlaygroundBearerRejected` with `reason` but without the token material. Add a §27.7 CSP note that the paste field uses `autocomplete="off"` and `type="password"` to suppress browser storage of accidentally-pasted secrets.

---

## 9. Storage & Data Architecture

### STR-011. Event replay buffer discard on coordinator handoff discards events Postgres could reconstruct [Medium]

**Section:** `spec/10_gateway-internals.md` §10.4 Event replay buffer, `spec/07_session-lifecycle.md` §7.2 Event ordering and resume, `spec/07_session-lifecycle.md` §7.3 session_tree_archive

§10.4's event replay buffer is "coordinator-local and is deliberately discarded on coordinator handoff" — a client that reattaches via `attach_session` with `resumeFromSeq` after a coordinator handoff "observes the gap-detected path for events that predate the handoff." This decision is correct for volatile state (e.g., `agent_output` deltas that were never persisted), but it also discards replayable state that Postgres DOES durably hold: `session.resumed` carrying `workspaceRecoveryFraction`, `children_reattached` (§7.2 "delivered exactly once per parent resume"), `status_change(input_required)` driving elicitation UX, and `session_complete`. For each of these, the new coordinator has enough state in Postgres (`sessions.state`, `session_tree_archive`, `session_checkpoint_meta`, `session_messages`) to re-synthesize a `SessionEvent` with the original semantic payload; handing the client a bare `gap_detected` instead forces them to re-query REST endpoints to discover "did my children complete?" / "am I still waiting for input?" — precisely the questions the stream is supposed to answer in the first place. STR-007 called this out only for the single-coordinator reconnect case; handoff re-introduces the silent-loss failure mode STR-007 set out to eliminate.

**Recommendation:** After the new coordinator acquires the lease and rehydrates session state, before emitting `gap_detected` on the reattach path, synthesize a post-handoff "state frame" sequence from durable Postgres state: one `session.resumed` (with `resumeMode: "coordinator_handoff"` and `workspaceRecoveryFraction` from checkpoint metadata), zero-or-one `status_change` for the current session state, and one `children_reattached` if the session is a parent with any children archived in `session_tree_archive` since the last known client seq. Assign these synthesized frames fresh monotonic `SeqNum`s continuing the per-session counter (§15 `SeqNum` is per-session, so the counter survives handoff when it's backed by Postgres; if the counter is coordinator-local, add it to the session record). Emit `gap_detected` only for events that CANNOT be reconstructed (e.g., transient `agent_output` deltas). Document this in §10.4 adjacent to the "deliberately discarded" sentence and in §7.2's "Event ordering and resume" paragraph. Add a §15.2.1 contract test: "Coordinator-handoff reattach — the client reattaching with `resumeFromSeq &lt; lastSeqBeforeHandoff` receives a synthesized-state frame sequence reconstructing session/tree/elicitation state before any `gap_detected` frame."

---

### STR-012. Partial-manifest row schema omits `deleted_at` yet backstop sweep references it in DELETE guard [Medium]

**Section:** `spec/10_gateway-internals.md` §10.1 Partial manifest on checkpoint timeout (manifest field list), `spec/12_storage-architecture.md` §12.5 GC concurrency model rule 6 (partial-manifest backstop), `spec/04_system-components.md` §4.4 partial-checkpoint cleanup

The partial-manifest field list in §10.1 enumerates `session_id`, `coordination_generation`, `checkpoint_id`, `checkpoint_started_at`, `checkpoint_timeout_at`, `workspace_bytes_uploaded`, `chunk_count`, `chunk_size_bytes`, `chunk_encoding`, `partial_object_key_prefix`, and `partial: true` — no `deleted_at`. §4.4 states "the gateway MUST delete every chunk object … then delete the Postgres row" (hard DELETE). §12.5 GC rule 6 then describes the backstop sweep's Postgres delete under "the same `WHERE` predicate that selected the row for cleanup (`partial = true AND (terminal_state OR created_at &lt; now() - maxResumeWindowSeconds) AND deleted_at IS NULL`)" — which assumes a soft-delete column. The two cleanup paths use incompatible row-lifecycle models: the primary path issues a hard DELETE, while the backstop guards with `deleted_at IS NULL`. If the primary path committed a soft-delete first, the hard DELETE in §4.4 would silently remove the tombstone before GC observes it; if the primary path hard-deletes, the backstop's `deleted_at IS NULL` guard is vacuously satisfied and the whole concurrency argument for "stale leader that issued MinIO delete before losing the guard race" no longer holds — a re-run would re-attempt MinIO deletes (idempotent, fine) and re-increment nothing (no Redis counter involvement for partial manifests anyway), but the ordering rationale stated in rule 6 is broken.

**Recommendation:** Pick one model and make the three sections consistent. Simplest: add `deleted_at TIMESTAMPTZ` to the partial-manifest row schema in §10.1, and change §4.4's cleanup text from "delete the Postgres row" to "mark the Postgres row `deleted_at = now() AT TIME ZONE 'UTC'` under the `WHERE deleted_at IS NULL` guard; the row is hard-pruned by the same sweep that prunes `artifact_store` tombstones (§12.5)." This preserves the convergent, single-writer, `WHERE deleted_at IS NULL` idempotency guarantees §12.5 rule 6 relies on and makes stale-leader re-runs provably harmless. Alternatively, if the hard-delete is preferred, rewrite §12.5 rule 6's delete predicate to `partial = true AND (terminal_state OR created_at &lt; now() - maxResumeWindowSeconds)` and document that concurrent re-runs rely on MinIO delete-on-absent + PostgreSQL `DELETE` being trivially a no-op on a missing row.

---

### STR-013. Multiple partial manifests per session can accumulate across CheckpointBarrier retries with no uniqueness guard [Medium]

**Section:** `spec/10_gateway-internals.md` §10.1 CheckpointBarrier protocol + Partial manifest on checkpoint timeout, `spec/12_storage-architecture.md` §12.5 partial-manifest backstop

§10.1 records the partial manifest with `(session_id, coordination_generation, checkpoint_id)` but the schema has no uniqueness constraint. When a gateway drain fails BarrierAck (pods don't ack within `checkpointBarrierAckTimeoutSeconds`), a subsequent coordinator can issue its own `CheckpointBarrier` under a new `coordination_generation` and, on timeout, write a NEW partial manifest row. Resume logic ("the new coordinator detects `partial: true` in the latest checkpoint record") picks only the latest. The old partial manifest rows become orphaned — they're never consumed by the primary cleanup path (which runs on resume and sees only the latest), and the GC backstop only fires after `maxResumeWindowSeconds`. With repeated drain-timeouts on a long-lived session, each cycle produces ~16 MiB × chunk_count worth of orphaned MinIO chunks that count against the tenant's `storageQuotaBytes` for up to `maxResumeWindowSeconds` (default multi-hour). A tenant whose session repeatedly misses BarrierAck can exhaust their storage quota from stale partial manifests alone.

**Recommendation:** On every new partial-manifest write, the gateway MUST first delete all prior partial-manifest rows for `(session_id, slot_id)` where `partial = true AND session_id = $1 AND deleted_at IS NULL AND checkpoint_id != $new_checkpoint_id`, listing and deleting their chunk objects under `partial_object_key_prefix` before the new row commits — i.e., a single active partial manifest per session/slot. Document this in §10.1 adjacent to the manifest write. Add a `UNIQUE (session_id, slot_id) WHERE partial = true AND deleted_at IS NULL` partial index to enforce at the DB level. Update §12.5 to note that the backstop sweep is now a true backstop (rare), since the primary cleanup path runs both on resume AND on every new partial-manifest write. Add metric `lenny_checkpoint_partial_manifests_superseded_total{pool}` counting supersession events.

---

### STR-014. `eventbus_publish_state='failed'` rows lack a specified retry worker; persistent failures never retranscribe [Medium]

**Section:** `spec/12_storage-architecture.md` §12.6 EventBus "Publish failure after durable commit", `spec/11_policy-and-controls.md` §11.7 Dead-letter handling (comparison)

§12.6 defines the `eventbus_publish_state` column enum (`pending | retry_pending | published | failed`) and specifies behavior on publish failure after durable commit: increment drop counter, write to bounded in-memory replay buffer, mark source audit row `eventbus_publish_state = 'failed'`. On Redis reconnect, "the replay buffer is drained in FIFO order … drained events flip the source audit row's state to `published`." The replay buffer is explicitly "a latency optimization, not a durability mechanism" — "if the replay buffer is lost (gateway replica restart before reconnect), subscribers must still be able to reconcile from Postgres." This leaves a critical gap: **nothing in the spec describes a worker that retranscribes `failed` rows back to `pending → published`.** The `ocsf_translation_state` column (§11.7) has an explicit retry loop with `audit.ocsf.retryInterval` (default 30s), `audit.ocsf.maxAttempts` (default 10), and the `audit.ocsf_retranslate_requested` admin endpoint. `eventbus_publish_state` has no analog — the only recovery is the in-memory replay buffer OR subscriber-initiated Postgres reconciliation via `/v1/admin/audit-events?eventbus_publish_state=failed`. Subscribers that depend on push-based delivery (webhook consumers, §25.5 SSE clients) will never see `failed` events unless they independently poll Postgres, which is exactly the failure mode the EventBus abstraction is supposed to avoid for audit-bearing events. The enum value `retry_pending` is declared in the §25.9 query API filter but has no writer anywhere in the spec.

**Recommendation:** Specify an EventBus retranscribe worker analogous to the OCSF translator retry loop. Additions: (1) In §12.6, after the "Publish failure after durable commit" paragraph, add: "A leader-elected background worker in the gateway sweeps `audit_log` rows with `eventbus_publish_state IN ('failed', 'retry_pending')` every `eventBus.retryInterval` (default 60s). For each matching row the worker re-serializes the CloudEvents envelope from the canonical tuple and re-invokes `EventBus.Publish`; on success the row flips to `published`, on failure the row's `retry_count` is incremented and state set to `retry_pending`. After `eventBus.maxRetryAttempts` (default 20) consecutive failures the row remains `failed` and a `EventBusPublishFinalFailure` warning alert fires carrying `event_id`, `topic`, `retry_count`, and `tenant_id`." (2) Add an admin endpoint `POST /v1/admin/audit-events/{id}/republish` (scope `audit:republish`, audited) that manually requeues a `failed` row. (3) Add metrics `lenny_event_bus_retranscribe_duration_seconds` and `lenny_event_bus_retranscribe_attempts_total{outcome}`. (4) Clarify that the bounded in-memory replay buffer is a latency optimization and the durable retranscribe worker is the correctness mechanism — update the "not a durability mechanism" sentence to read "the in-memory buffer is a latency optimization; the durable correctness path is the retranscribe worker above."

---

### STR-015. Sampled-HEAD test-restore success rate tolerates large absolute artifact-loss rates at Tier 3 scale [Low]

**Section:** `spec/25_agent-operability.md` §25.11 Test Restore (ArtifactStore sampled-HEAD verification)

§25.11 specifies `backups.verification.artifactSampleSize: 100` and a 99% success floor on the sampled HEAD check: "asserts that ≥ 99% of samples exist at the target … A sampled success rate &lt; 99% sets `lenny_restore_test_success = 0`." With the default N=100, the threshold tolerates up to one missing sampled object per test — and at Tier 3 production scale (`artifact_store` row count commonly in the tens of millions), the statistical inference from 100 samples is weak: a 0.5% actual replication loss has ~40% probability of producing a 100/100 sample pass. The monthly cadence plus a small sample thus allows persistent, silent artifact loss on the order of 0.1–0.5% without ever tripping `lenny_restore_test_success = 0`. Unlike the continuous `lenny_minio_replication_lag_seconds` gauge (which catches fresh loss via lag), this gate is the only mechanism that detects OLD replication failures — objects that successfully replicated at write time but were subsequently removed out-of-band on the target (bit rot, operator error, provider-side lifecycle policy applied to wrong bucket). The default N=100 substantially undershoots this detection mission.

**Recommendation:** Tier the `backups.verification.artifactSampleSize` default by tier preset (Tier 1: 100; Tier 2: 1000; Tier 3: 10000) and document in §25.11 that the threshold is a statistical bound — the sample size governs the smallest loss rate detectable at a given confidence. Add a companion `backups.verification.artifactAbsoluteMissingThreshold` (default: 0 at Tier 3) — when any sample miss is detected at Tier 3, `lenny_restore_test_success = 0` regardless of percentage, forcing operator investigation. Alternatively: change the gate from sampled-HEAD to a periodic full bucket-inventory diff (MinIO `mc diff` against replication target, once per month, emitting `lenny_restore_test_artifact_missing_total`) for Tier 3 — the operational cost (one scan/month) is bounded and the inference is certain. Document the trade-off.

---

### STR-016. Backup reconciler does not pre-validate `audit.gdprRetentionDays &gt; backups.retention.retainDays` invariant [Low]

**Section:** `spec/12_storage-architecture.md` §12.8 Post-restore reconciler phase 1 + Receipt survivability, `spec/25_agent-operability.md` §25.11 Retention Enforcement

§12.8 asserts: "`gdpr.*` receipts survive restore unconditionally because they are retained under `audit.gdprRetentionDays` (default 2555 days / 7 years) … which always exceeds the 90-day maximum `backups.retention.retainDays` — so the restored audit log always contains every completed erasure that occurred after the backup was taken." This correctness argument rests on a strict inequality: `audit.gdprRetentionDays &gt; backups.retention.retainDays`. The spec caps `backups.retention.retainDays` at 90 (Tier 3 override) but nowhere enforces the inequality at config-render or startup time. Three configurations can violate it: (a) a deployer who raises `backups.retention.retainDays` above 90 for regulatory reasons (the spec says "operator override: any of these values can be raised" with no upper bound), (b) a deployer who lowers `audit.gdprRetentionDays` below the default 2555 days (the spec does not declare a minimum), or (c) the `audit.retentionPreset` system where each preset has its own `gdprRetentionDays` default that might not track `backups.retention.retainDays` raises. If the invariant breaks, the reconciler's enumeration step can silently miss receipts — a user who was erased, their receipt was purged by audit retention, and the backup still holds their data would silently be re-materialized by the restore with no reconciler replay, violating GDPR Article 17.

**Recommendation:** Add a config-time enforcement gate in `lenny-ops` startup validation and chart render: `audit.gdprRetentionDays MUST BE &gt;= backups.retention.retainDays + 7` (the +7 accounts for the 7-day pre-restore backup window). Failure: `CONFIG_INVALID: audit.gdprRetentionDays (&lt;N&gt;) is less than backups.retention.retainDays (&lt;M&gt;) + 7; backup reconciler cannot guarantee receipt survivability. Raise audit.gdprRetentionDays or lower backups.retention.retainDays.` Document the invariant in §12.8 adjacent to the "always exceeds the 90-day maximum" sentence as a first-class rule, not a consequence. Add a runtime monitor alert `GdprReceiptRetentionBelowBackupRetention` that fires if the live config relationship is detected to violate this at any point (e.g., operator ran `PUT /v1/admin/backups/policy` raising retention without a corresponding audit-retention bump).

---

## 10. Session Lifecycle

### SES-015. Resuming-failure table in §6.2 missing `resuming → cancelled` and `resuming → completed` edges [Medium]

**Section:** spec/06_warm-pod-model.md §6.2 (resuming failure transitions table, lines 116-120 and prose at lines 225-231); spec/07_session-lifecycle.md §7.2 (lines 180-181)

§7.2's session state-machine enumeration now lists `resuming → cancelled (snapshot-close: client terminate / admin cancel arrives during resume)` and `resuming → completed (snapshot-close: session finishes during resume)` as part of the iter3 SES-010 fix. However, §6.2's "Resuming failure transitions" table — which iter3 SES-013 explicitly designated as the single authoritative source for resuming-edge transitions — still enumerates only the four prior edges (to `resume_pending` via transient / `awaiting_client_action` via client-fault). The §6.2 prose paragraph at lines 225-231 likewise lists only the transient vs client-fault split. This recreates the exact §7.2-vs-§6.2 divergence iter3 SES-013 was opened to eliminate: the two tables no longer agree on the full set of edges leaving `resuming`.
**Recommendation:** Add two rows to the §6.2 resuming-failure table covering `resuming → cancelled` (trigger: client terminate / admin cancel reaches session during snapshot replay) and `resuming → completed` (trigger: terminal session outcome emitted during snapshot replay), each referencing §7.2's snapshot-close semantics (abort snapshot, skip seal, release pod, apply terminal handling). Update the §6.2 prose paragraph to call out that the `resuming` state can also collapse directly to terminal states via snapshot-close without passing through `resume_pending` or `awaiting_client_action`. Cross-link both directions so §6.2 remains the single authoritative resuming-edge reference promised by iter3 SES-013.

### SES-016. §7.2 state-machine enumeration missing collapsed `resume_pending → cancelled` / `resume_pending → completed` edges required by §15.1 [Medium]

**Section:** spec/07_session-lifecycle.md §7.2 (state-machine enumeration, lines 149-186); spec/15_external-api-surface.md §15.1 (`POST /terminate` preconditions, line 538)

§15.1's `POST /terminate` preconditions row lists `resume_pending` as a valid originating state and declares the resulting transition is to `completed`/`cancelled`. §7.2's enumeration, however, lists only the forward edges out of `resume_pending` (`→ resuming`, `→ awaiting_client_action`, `→ failed`, `→ expired`) and never calls out the direct `resume_pending → cancelled` or `resume_pending → completed` edges that a client-initiated `/terminate` (or admin cancel) must produce when the pod is not yet re-attached. The behaviour is implied only by the narrative paragraph at lines 192-199 about mid-resume collapse, which covers the `resuming` case, not the `resume_pending` case. This leaves the authoritative state-machine listing inconsistent with the API surface and with iter3 SES-010's intent that all terminal collapses be enumerated.
**Recommendation:** Add `resume_pending → cancelled` (trigger: client `POST /terminate` or admin cancel arrives before pod re-attach) and `resume_pending → completed` (trigger: session terminal outcome materialises before pod re-attach, e.g. rehydrated budget exhausted) to the §7.2 enumeration, each noting that no pod is currently attached so no snapshot-close sequence runs and no `CoordinatorFence` round-trip is required. Cross-reference §15.1 preconditions and the §6.2 resuming/resume_pending edge table so all three sections agree.

### SES-017. `recovery_generation` behaviour during mid-resume collapse not specified in snapshot-close semantics [Medium]

**Section:** spec/07_session-lifecycle.md §7.2 (mid-resume terminal transitions, lines 192-199); spec/04_system-components.md §4.2 (lines 268-269)

The iter3 SES-010 resolution added a four-step snapshot-close sequence (abort snapshot replay, skip seal, release pod, apply terminal handling) but does not say what happens to `recovery_generation` and `coordination_generation` when a `resuming` session collapses directly to `cancelled`/`completed`. §4.2 defines `recovery_generation` as monotonically incremented for each recovery attempt and `coordination_generation` as the CAS fence used for writes; whether either counter is bumped, rolled back, or frozen when the collapse short-circuits the normal reattach/seal flow is undefined. This is important because: (a) audit reconstruction relies on every terminal transition carrying a consistent generation tuple; (b) any later crash-recovery pass that observes the orphaned partial snapshot must be able to tell whether a `resuming → cancelled` edge was taken intentionally vs. lost mid-flight; (c) iter3 SES-010 explicitly called out `recovery_generation` rollback as a sub-item still to be resolved.
**Recommendation:** Extend the snapshot-close bullet list in §7.2 with an explicit step covering generation bookkeeping: state that `coordination_generation` is bumped once under the terminal write (so any racing stale coordinator is fenced), `recovery_generation` is frozen at its current value (no rollback — the attempt is recorded as failed-by-terminal rather than retried), and the partial snapshot manifest is tagged with both values plus a `terminated_during_resume` reason. Cross-link §4.2 so the generation invariants remain coherent across §4.2, §7.2, and §10.1 partial-checkpoint GC.

### SES-018. `persistDeriveFailureRows: true` opt-in creates an unenumerated `created → failed` path that violates §7.1 atomicity rule [Medium]

**Section:** spec/07_session-lifecycle.md §7.1 (atomicity rule at line 28; derive rule 2 at line 92); §7.2 (state-machine enumeration, lines 149-186)

§7.1 line 28 asserts "there is no `created → failed` transition" — derive failures roll back the Session row atomically. The iter3 SES-009 fix then introduces a `gateway.persistDeriveFailureRows: true` opt-in mode in derive rule 2 that, when enabled, persists a Session row in `failed` state so operators can audit derive failures. This is a valid operational choice, but it (a) directly contradicts the absolute statement at line 28 — that sentence is no longer true under the opt-in; (b) introduces a transition (`created → failed` under the opt-in, or equivalently "derive-failed seed" as a new initial state) that §7.2's state-machine enumeration does not list anywhere; (c) leaves undefined which downstream APIs are reachable for such a row (can `GET /sessions/{id}` return it? can `/terminate` target it? is it event-stream-visible?). The result is a documented backdoor whose existence contradicts the adjacent atomicity invariant and is absent from the authoritative state diagram.
**Recommendation:** Reword §7.1 line 28 to condition the atomicity rule on `persistDeriveFailureRows: false` (the default) and describe the opt-in as an explicit audit-row write that materialises the session in `failed` with an annotated `failure_class = derive_failure` label. Add a corresponding entry to the §7.2 enumeration (e.g. "`created → failed` (derive failure, only under `gateway.persistDeriveFailureRows: true`; session is read-only, not event-stream-visible, and not targetable by `/terminate`/`/start`)"). Specify in §15.1 which API endpoints return or reject such rows so clients cannot observe a partial-derive session through an unintended code path.

### SES-019. `POST /start` precondition table still omits `resume_pending` as a possible resulting state [Low]

**Section:** spec/15_external-api-surface.md §15.1 (`POST /start` row, line 536)

Iter3 SES-011 flagged that §15.1's `POST /start` precondition row lists only `starting → running` as the allowable transition, even though the session lifecycle permits `starting → resume_pending` when the warm pod carries a saved snapshot and must rehydrate before accepting inbound messages. The table has not been updated in iter4; it still lists a single target state, which leaves clients unable to predict when `/start` will return a session already in `resume_pending` (and thus needing to wait on events rather than immediately sending a message).
**Recommendation:** Extend the `POST /start` row to list both `starting → running` and `starting → resume_pending` as valid resulting transitions, and add a sentence in the surrounding prose explaining that clients must inspect the returned session state before publishing the first message. Cross-reference §7.2's starting-state edges so the two sections agree.

### SES-020. Event-stream SSE still lacks a reconnect rate limit and per-reconnect replay cap [Low]

**Section:** spec/07_session-lifecycle.md §7.2 (replay window + SSE bounded-error policy, around line 323); spec/15_external-api-surface.md §15.1 (event-stream section)

Iter3 SES-012 flagged that a malicious or buggy client can loop-reconnect to the session event stream and force the gateway to replay up to the full 1200-second window each time, amplifying gateway load and egress. Iter4 still specifies the 1200 s replay window and the bounded-error OutboundChannel policy but does not add a per-session or per-principal reconnect rate limit, nor a cap on the number of events replayed per reconnection beyond the raw window. This is unchanged from iter3.
**Recommendation:** Add a reconnect rate limit (e.g. minimum inter-reconnect interval per session+principal, token-bucket or fixed window) and a per-reconnect replay cap (maximum events returned before switching to live-only delivery), with a documented `EVENT_STREAM_REPLAY_TRUNCATED` reason surfaced to the client. Specify the behaviour under rate-limit violation (429 with Retry-After) in §15.1 and reflect the new limits in §17 (rate-limit/quota tables) if present.

### SES-021. `awaiting_client_action → expired` trigger still under-specified [Low]

**Section:** spec/07_session-lifecycle.md §7.2 (state-machine enumeration, line 185)

Iter3 SES-014 flagged the enumeration of `awaiting_client_action → expired` as too vague — the spec says "lease/budget/deadline exhausted" without saying which of the three triggers this specific edge, and whether multiple simultaneous conditions collapse to a single transition with a deterministic precedence. Iter4 leaves the wording unchanged. Since the three underlying timers (`maxAwaitingClientActionSeconds`, session-level budget timers, client-action lease) can each independently fire, the enumeration should disambiguate which timer(s) cause `expired` vs. which cause `failed` or `cancelled`.
**Recommendation:** Replace the shared "lease/budget/deadline exhausted" phrase with an explicit precedence list: (1) `maxAwaitingClientActionSeconds` exhaustion → `expired` with `reason = awaiting_client_action_timeout`; (2) session budget cap (tokens/wall) exhaustion → `expired` with `reason = budget_exhausted`; (3) absolute session deadline (TTL) exhaustion → `expired` with `reason = session_ttl`. If any competing condition should instead route to `failed`/`cancelled`, call that out. Mirror the change in §6.2 and §15.1's event-reason table so the precedence is observable to clients.

---

## 11. Observability & Metrics

### OBS-022. `billing_write_ahead_buffer_utilization` missing `lenny_` prefix [High]

**Section:** §16.1 (line 142), §16.5 (line 417), §12.3 (line 132)

A new gauge added to the §16.1 metrics table is registered without the mandatory `lenny_` prefix: `billing_write_ahead_buffer_utilization`. The `BillingWriteAheadBufferHigh` alert in §16.5 evaluates the same unprefixed name, and §12.3 ("write-ahead buffer as back-pressure") propagates the bare identifier as operator guidance. This is a direct regression against the §16.1.1 rule ("All Lenny-emitted Prometheus metrics use the `lenny_` prefix to avoid collisions with cluster/node exporters") — a rule that iter3 OBS-012 was opened specifically to enforce.
**Recommendation:** Rename the metric to `lenny_billing_write_ahead_buffer_utilization` in §16.1 row, in the §16.5 alert definition, and in the §12.3 reference. Add a §16.1.1 compliance note or lint check (as discussed for the bundled alert source) to reject unprefixed registrations at build time so that this regression cannot recur.

### OBS-023. §25.13 tier-aware defaults still reference alerts that don't exist in §16.5 [High] — Fixed

**Section:** §25.13 (lines 4540–4541), §16.5

The iter3 "fix" for OBS-016 canonicalized `PostgresReplicationLagHigh` only. §25.13's Tier-Aware Defaults table continues to list six alert names that have no definition anywhere in §16.5: the Universal row cites `PostgresUnreachable` and `RedisUnreachable`; the Tier-dependent row cites `GatewayQueueDepthHigh`, `GatewayLatencyHigh`, `WarmPoolReplenishmentLag`, and `CredentialPoolUtilizationHigh`. §16.5 is declared the authoritative source in §25.13 and §16.9, so the table promises operator-visible alerts whose rules do not ship — an operability contract violation that spec readers (and any bundled-rules unit test) will surface immediately.
**Recommendation:** For each missing alert either (a) add the corresponding `PrometheusRule` entry to §16.5 with query, window, and severity, registering any referenced metrics in §16.1, or (b) delete the alert name from §25.13 and substitute the actual canonical alert the tier preset adjusts. Drive both §25.13 and §16.5 from the shared `pkg/alerting/rules` source so that a registered alert and a referenced alert cannot diverge.

**Resolution:** Verified current §25.13 state — two already-canonical names (`WarmPoolReplenishmentSlow`, `CredentialPoolLow`) are correctly listed; three names are missing rules (`RedisUnreachable`, `GatewayQueueDepthHigh`, `GatewayLatencyHigh`); and one obsolete name remains (`PostgresUnreachable` appears in illustrative examples at §25.13 lines 471 and 503 only — not in the tier-aware defaults table). Added two new alert rows to §16.5 Warning table: `GatewayQueueDepthHigh` using `lenny_gateway_{subsystem}_queue_depth` (tier-scaled threshold, 5 min window) and `GatewayLatencyHigh` using `lenny_gateway_{subsystem}_request_duration_seconds` (tier-scaled threshold, 10 min window) — both cite §17.8.2 for tier defaults. Substituted `RedisUnreachable` with `DualStoreUnavailable` in the §25.13 Universal defaults row (the dual-store critical alert is the correct canonical response). Replaced obsolete `PostgresUnreachable` with `SessionStoreUnavailable` in the §25.13 illustrative examples (lines 471, 503) and updated the example JSON `issue` field accordingly.

### OBS-024. LLM-proxy active-connections metric is inconsistently named across sections [Medium] — Fixed

**Section:** §16.1 (line 84), §4.1 (line 130), §17 (line 1153), §18 (line 44)

§16.1 registers the gauge as `lenny_gateway_llm_proxy_active_connections`, while the §4.1 extraction-readiness table, the §17 deployment-topology extraction check, and the §18 build sequence all cite `lenny_llm_proxy_active_connections`. The two forms cannot both be correct: the latter is what extraction triggers and build gates actually evaluate, but the former is what Prometheus scrapes. Operators implementing an extraction decision will evaluate a non-existent metric and receive a flat zero.
**Recommendation:** Pick one canonical name and propagate everywhere. Consistency with the sibling unified subsystem metrics at §16.1 line 83 (`lenny_gateway_subsystem_circuit_state{subsystem}`) argues for `lenny_gateway_llm_proxy_active_connections` (or, better, unify into a labeled `lenny_gateway_subsystem_active_connections{subsystem}`). Update §4.1, §17, §18, and any bundled queries accordingly.

**Resolution:** Canonicalized on `lenny_gateway_llm_proxy_active_connections` (the registered form in §16.1 line 86, matching the subsystem template family `lenny_gateway_{subsystem}_*`). Renamed four occurrences in §4.1 (lines 84, 126 twice, 130), one in §17.11 (line 1207 extraction check), and fixed the contradiction within §16.1 line 231 where the "also canonical" list incorrectly cited the unprefixed form. §18 line 44 already used the canonical form. Also canonicalized `lenny_stream_proxy_queue_depth` → `lenny_gateway_stream_proxy_queue_depth` within the §16.1 line 231 list (since `queue_depth` is part of the subsystem template family, not an extraction-specific metric). §4.1 extraction table retains `lenny_llm_proxy_upstream_goroutines` and `lenny_llm_proxy_p99_ttfb_ms` unchanged (these are extraction-threshold-specific metrics, not members of the subsystem template family).

### OBS-025. `PodStateMirrorStale` alert referenced in §10 but undefined in §16.5; backing metric unregistered [Medium] — Fixed

**Section:** §10.1 (line 51), §16.5, §16.1

§10.1 introduces the metric `lenny_agent_pod_state_mirror_lag_seconds` and its associated alert `PodStateMirrorStale` as part of the gateway-internal pod-state-mirror design. Neither the metric appears in the §16.1 registry nor the alert in the §16.5 catalog. Per §16.1.1's single-source-of-truth rule and the §25.13/§16.9 declaration that §16.5 is authoritative, this alert cannot be rendered by the bundled `PrometheusRule`.
**Recommendation:** Register `lenny_agent_pod_state_mirror_lag_seconds` (gauge, labels `tenant_id`, `pool`, `runtime_class`, `replica_service_instance_id`) in §16.1 and add `PodStateMirrorStale` to §16.5 with an explicit query, window, and severity. Ensure every alert or metric introduced in subsystem sections surfaces back to §16.1/§16.5.

**Resolution:** Registered `lenny_agent_pod_state_mirror_lag_seconds` as a Gauge in §16.1 under the "Warm Pool Controller" subsection (alongside `lenny_pod_claim_fallback_total`), with `pool` as the sole label — matching the §10.1 design ("per pool") and the schema-driven semantics of `agent_pod_state.updated_at` ([§12.6](12_storage-architecture.md#126-interface-design)); the recommendation's suggested additional labels (`tenant_id`, `runtime_class`, `replica_service_instance_id`) were not adopted because the mirror is a per-pool cluster-wide view (not tenant/runtime/replica-scoped), and §16.1.1 requires labels to reflect the metric's actual cardinality. Added the `PodStateMirrorStale` warning alert to §16.5 with explicit PromQL `max by (pool) (lenny_agent_pod_state_mirror_lag_seconds) > 60` sustained for 60s, Warning severity, and cross-references to §4.6.1 fallback preconditions and §10.1 staleness detection.

### OBS-026. Gateway-internal metrics referenced in §10 are absent from the §16.1 registry [Medium] — Fixed

**Section:** §10.1 (lines 122, 136, 140, 573), §10.5 (line 1044), §16.1

Several `lenny_`-prefixed metrics are introduced in §10 (`lenny_pool_termination_budget_exceeded_total`, `lenny_checkpoint_partial_total`, `lenny_gateway_sigkill_streams_total`, `lenny_noenvironmentpolicy_allowall_total`, `lenny_session_duration_seconds`) but no row for any of them exists in §16.1. §10 also uses `lenny_session_duration_seconds{quantile="0.95", variant_id="treatment"}` in an experimentation alert expression that would require `variant_id` to be an authorised label under §16.1.1 — it is not enumerated in the attribute table.
**Recommendation:** Add one row per metric to §16.1 with type, labels, and semantics. If `variant_id` is a legitimate dimension for experimentation metrics, add it to §16.1.1's "Other domain labels" enumeration with cardinality bounds; otherwise rework the §10.5 alert to avoid a label that violates the single-source rule.

**Resolution:** Registered all five missing metrics as rows in §16.1 with type, labels, and cross-references to their §10 introduction points. `lenny_pool_termination_budget_exceeded_total` (counter, `pool`) and `lenny_gateway_sigkill_streams_total` (counter, `service_instance_id`) were added to the gateway/pool-config subsections near the existing `lenny_pool_scaling_admission_denied_total` and preStop/CheckpointBarrier families. `lenny_checkpoint_partial_total` (counter, `pool`, `recovered`, `manifest_reason`) was placed in the Checkpoint Failure Tracking subsection immediately before the already-registered `lenny_checkpoint_partial_manifests_superseded_total` row (which referenced it). `lenny_noenvironmentpolicy_allowall_total` (counter, `tenant_id`) was added next to `lenny_policy_denials_total` as a tenant-RBAC audit counter per §10.6. `lenny_session_duration_seconds` (histogram, `tenant_id`, `session_type`, `variant_id`) was placed in the Experiment Targeting subsection alongside the existing variant-labeled `lenny_session_error_total`/`lenny_session_total` rows. `variant_id` and `experiment_id` were added to §16.1.1's "Other domain labels" enumeration with cardinality bounds (one `experiment_id` per active experiment; small set of `variant_id` values per experiment — typically `control` plus 1–3 treatment arms), legitimising the §10.7 rollback-trigger alert expression.

### OBS-027. `coordinator_resume_deduplicated_total` metric and `coordinator_resume_meta_source` label both lack `lenny_` prefix [Medium] — Fixed

**Section:** §10.1 (line 148)

§10.1 describes a resume-deduplication counter as `coordinator_resume_deduplicated_total` with a `coordinator_resume_meta_source` label. The counter name violates the §16.1.1 prefix rule (no `lenny_`). Even after prefix correction, the label name replicates the metric name rather than the concise label vocabulary used elsewhere (`source`, `reason`, etc.).
**Recommendation:** Rename the counter to `lenny_coordinator_resume_deduplicated_total` and the label to a concise form (e.g., `meta_source` with a well-defined enum or simply `source`). Register the metric and label in §16.1/§16.1.1.

**Resolution:** Renamed the counter to `lenny_coordinator_resume_deduplicated_total` and the label to `source` (enum `postgres`, `checkpoint_manifest`) in §10.1 step 5. Registered the metric as a new row in §16.1 adjacent to the other CheckpointBarrier metrics, with a description referencing the §10.1 resume-deduplication flow. The `source` label is consistent with existing usage elsewhere in §16.1 (e.g., `lenny_credential_lease_assignments_total`, `lenny_prestop_cap_selection_total`), which §16.1.1 documents as a Lenny-specific domain label whose enum is defined inline at each point of use.

### OBS-028. Extraction-threshold metrics from §4.1 are not registered in §16.1; §16.1 footnote violates the single-source rule [Medium] — Fixed

**Section:** §4.1 (lines 121–130), §16.1 (line 220)

The §4.1 extraction-readiness table operationalises extraction decisions against `lenny_stream_proxy_goroutines`, `lenny_stream_proxy_p99_attach_latency_ms`, `lenny_upload_handler_queue_depth`, `lenny_upload_handler_p99_latency_ms`, `lenny_mcp_fabric_goroutines`, `lenny_mcp_fabric_p99_orchestration_latency_ms`, `lenny_llm_proxy_upstream_goroutines`, and `lenny_llm_proxy_p99_ttfb_ms`. None are registered as §16.1 rows. The §16.1 footnote at line 220 acknowledges four related gauges ("must be instrumented") rather than registering them as first-class table entries — which leaves §16.1.1's "every label must appear in §16.1.1; every metric must appear in §16.1" rule literally violated for the metrics that gate extraction.
**Recommendation:** Add each extraction-gate metric as a §16.1 row (type, labels, semantics, and the §4.1 extraction-threshold pointer). Remove or demote the line 220 footnote to a cross-reference once the table is complete. Also reconsider the `_ms`-suffixed names — §16.1.1 requires SI seconds for duration/latency metrics (`_seconds`); either migrate the names or document an explicit exception.

**Resolution:** Added a new **Gateway Subsystem Extraction Metrics** subsection to §16.1 (right after the gateway GC-pressure rows) registering all eleven extraction-threshold metrics from §4.1 as first-class gauge rows with labels, semantics, and cross-references to §4.1 — the eight enumerated in the finding plus the three previously-acknowledged-but-unregistered gauges from the footnote (`lenny_stream_proxy_queue_depth`, `lenny_upload_handler_active_uploads`, `lenny_mcp_fabric_active_delegations`). Renamed the four `_ms`-suffixed names to `_seconds` (`lenny_stream_proxy_p99_attach_latency_seconds`, `lenny_upload_handler_p99_latency_seconds`, `lenny_mcp_fabric_p99_orchestration_latency_seconds`, `lenny_llm_proxy_p99_ttfb_seconds`) in both §16.1 (new rows) and §4.1 (extraction table + calibration methodology ramp-test steps), and propagated the renames to `docs/operator-guide/scaling.md` and `docs/reference/metrics.md`. The P99-in-name metrics are registered as gauges (pre-computed P99 exposed on the scrape interval) with an inline note that operators who want raw distribution data should consume the subsystem template family's `request_duration_seconds` histogram. Kept the non-`lenny_gateway_` prefix for these metrics (distinct from the four-member subsystem template family); this matches the approach taken in OBS-024's resolution. Demoted the line-220 footnote to a one-sentence cross-reference pointing readers from the template family to the new extraction-metrics subsection.

### OBS-029. Templated `lenny_gateway_{subsystem}_*` metric names at §16.1 lines 80–82 still violate unified-labeled form [Low]

**Section:** §16.1 (lines 80–82, 83)

Lines 80–82 register `lenny_gateway_{subsystem}_request_duration_seconds`, `lenny_gateway_{subsystem}_errors_total`, and `lenny_gateway_{subsystem}_queue_depth` with `{subsystem}` as a name template. Line 83 immediately below uses the unified labeled form `lenny_gateway_subsystem_circuit_state{subsystem=…}`. Iter3 OBS-013 addressed this for `circuit_state` only because "no alert references the other three". Regardless of alert coverage, registering one metric name per subsystem proliferates time-series and breaks cross-subsystem aggregation; the §16.1.1 single-source rule and Prometheus best practice favour label dimensions over name templates for a bounded enum.
**Recommendation:** Collapse the three entries into their labeled counterparts — `lenny_gateway_subsystem_request_duration_seconds{subsystem}`, `lenny_gateway_subsystem_errors_total{subsystem}`, `lenny_gateway_subsystem_queue_depth{subsystem}` — matching the circuit-state form. Confirm `subsystem` is already enumerated in §16.1.1's attribute table and document the fixed value set.

### OBS-030. `admin.impersonation_ended` absent from the §16.7 bulleted catalog [Low]

**Section:** §16.7 (line 567)

The §16.7 prose narrating `admin.impersonation_started` says "A matching `admin.impersonation_ended` event is emitted when the impersonation session terminates." but the catalog bullets only enumerate `admin.impersonation_started`; `admin.impersonation_ended` never appears as a catalogued audit event. The §16.6 intro explicitly declares "the catalog below is the canonical enumeration" — so downstream OCSF translator tests and SIEM field maps built from §16.7 will have no row for `admin.impersonation_ended` and may silently discard it.
**Recommendation:** Add an `admin.impersonation_ended` bullet to §16.7 with its payload fields (`admin_sub`, `admin_tenant_id`, `target_tenant_id`, `target_user_id`, `ticket_id`, `end_reason` ∈ `{expired, explicit_revoke, admin_logout}`, `duration_seconds_actual`, `impersonation_session_id` correlating with the `_started` event). Cross-reference §13.3.

---

## 12. Compliance & Legal

### CMP-049. DeleteByUser sequence has no legal-hold preflight [High] — Fixed

**Section:** spec/12_storage-architecture.md §12.8 (DeleteByUser, lines 778-799)

The DeleteByUser sequence (20 steps) contains no legal-hold check before proceeding with destructive operations. Iter3 CMP-046 added a legal-hold ledger freshness gate to the post-restore erasure reconciler (spec/25_agent-operability.md §25.11 restore flow), but the symmetric preflight on the normal erasure path was not added. An operator invoking `POST /v1/admin/users/{user_id}/erase` for a user with active legal holds on sessions, artifacts, or audit entries will destroy evidence subject to preservation orders, constituting spoliation. The reconciler gate only protects replay-after-restore; it does not protect first-time execution. Legal-hold interaction is mentioned for tenant deletion (line 841) but not for user-scoped erasure.
**Recommendation:** Add step 0 to DeleteByUser: enumerate active `LegalHold` resources scoped to `user_id` (including holds on the user's sessions, artifacts, audit ranges, and workspace snapshots). If any are present, fail with `ERASURE_BLOCKED_BY_LEGAL_HOLD`, emit `gdpr.erasure_blocked_by_hold` audit event, and require explicit hold release or `--acknowledge-hold-override` admin action (audited separately as `gdpr.legal_hold_overridden`). Mirror the same check into `DeleteByTenant` Phase 4 (see CMP-052). Document the check in spec/25_agent-operability.md erasure API section and list `ERASURE_BLOCKED_BY_LEGAL_HOLD` in the error-codes table.

**Resolution:** Added Step 0 "Legal-hold preflight (CMP-049, fail-closed)" to the `DeleteByUser` dependency-ordered deletion sequence in `spec/12_storage-architecture.md` §12.8 (before the existing step 1). The preflight enumerates active legal holds scoped to `user_id` across sessions (`sessions.legal_hold = true`), artifacts (`artifacts.legal_hold = true`), and audit-range / workspace-snapshot holds recorded in the legal-hold ledger. On any present hold the job aborts before step 1 with `ERASURE_BLOCKED_BY_LEGAL_HOLD` (HTTP 409, `POLICY`), sets `failure_phase: "legal_hold_preflight"`, increments `lenny_erasure_job_failed_total{failure_phase="legal_hold_preflight"}`, and emits a `gdpr.erasure_blocked_by_hold` critical audit event carrying the list of blocking holds. `processing_restricted` is explicitly NOT set in this path (the erasure never initiates, so GDPR Article 18 is not triggered). A `platform-admin` may override via `POST /v1/admin/users/{user_id}/erase` body `{"acknowledgeHoldOverride": true, "justification": "<text>"}` — the override requires non-empty justification, is rejected for `tenant-admin` callers, does not clear underlying holds, and emits a separate `gdpr.legal_hold_overridden` critical audit event. The preflight is documented as symmetric with the existing iter3 CMP-046 post-restore reconciler legal-hold ledger freshness gate (reconciler protects replay-after-restore; this preflight protects first-time execution). Companion changes: `spec/15_external-api-surface.md` error-codes table now lists `ERASURE_BLOCKED_BY_LEGAL_HOLD` and the `/v1/admin/users/{user_id}/erase` endpoint row documents the preflight and override flag; `spec/16_observability.md` §16.5 alerting rules adds `LegalHoldOverrideUsed` (Warning, fires on every `gdpr.legal_hold_overridden` event) and updates the `ErasureJobFailed` entry to describe the `legal_hold_preflight` failure phase; §16.7 audit-event inventory registers `gdpr.erasure_blocked_by_hold` and `gdpr.legal_hold_overridden` with their payload field lists. Existing step numbers (MemoryStore = step 8 at line 741 of §12.8, billing pseudonymization = step 15 at line 814 of §12.8) are preserved; only Step 0 was inserted. `DeleteByTenant` Phase 4 was intentionally NOT modified — the symmetric tenant-level check is tracked as CMP-052.

### CMP-050. chainIntegrity enum lacks post-erasure-redaction state; legitimate erasures trigger tamper alerts [Medium] — Fixed

**Section:** spec/12_storage-architecture.md §12.8 (DeleteByUser step 14, OCSF dead-letter redaction), spec/11_policy-and-controls.md §11.7 (hash chaining), spec/16_observability.md (chainIntegrity enum, AuditChainGap alert)

Iter3 CMP-047 introduced PII redaction of `unmapped.lenny.raw_canonical_b64` in OCSF dead-letter rows during erasure. Redacting the raw canonical payload alters the byte content that was hashed into the audit chain, so verification of the chain across redacted entries will legitimately fail. The `chainIntegrity` enum values (`verified|broken|unchecked|rechained_post_outage|gap_suspected`) have no state representing "redacted under GDPR Art. 17 with provenance receipt." Verification tooling will flag these as `broken`, triggering `AuditChainGap` and raising the tamper counter, producing false-positive SIEM alerts indistinguishable from genuine tampering. The redaction is authorized and logged, but the integrity model cannot express the distinction.
**Recommendation:** Add `redacted_gdpr` (or `redacted_art17`) to the `chainIntegrity` enum in spec/16_observability.md and spec/11_policy-and-controls.md §11.7. On redaction (§12.8 step 14), rewrite the affected row's `hash_prev`/`hash_curr` using the redacted payload AND persist a signed `RedactionReceipt` containing: original hash, new hash, erasure job ID, legal basis, redactor identity, timestamp. Chain verifiers must treat `redacted_gdpr` as a valid discontinuity when accompanied by a verified receipt, and raise `broken` only when no receipt is present. Update `AuditChainGap` alert rule to exclude `redacted_gdpr` gaps backed by receipts; add a new `AuditRedactionReceiptMissing` alert for orphaned gaps. Reference the receipt schema from spec/12_storage-architecture.md §12.8.

**Resolution:** Added `redacted_gdpr` to the `chainIntegrity` enumeration in `spec/11_policy-and-controls.md` §11.7 item 3 (defined as an authorized discontinuity that is valid only when accompanied by a signature-verifying `RedactionReceipt`; otherwise it is classified `broken`). Extended the `spec/12_storage-architecture.md` §12.8 DeleteByUser Step 14 (OCSF dead-letter PII redaction) to describe the rewrite mechanics explicitly: the erasure job recomputes the per-row hash over the redacted canonical tuple, writes the re-sealed `prev_hash` across the row's position and the immediately subsequent row, sets the verifier state to `redacted_gdpr`, and persists a signed `RedactionReceipt`. Defined the new `RedactionReceipt` schema inline in §12.8 with columns `receipt_id`, `audit_event_id`, `tenant_id`, `sequence_number`, `original_hash`, `new_hash`, `erasure_job_id`, `legal_basis` (enum pinned to `gdpr_art17` | `gdpr_art17_with_art17_3_exception` | `operator_acknowledged_override`), `redactor_identity`, `timestamp`, `signature` (detached JCS signature over the full tuple using the platform's KMS-held audit signing key), and `signature_kms_key_id`. The new `audit_redaction_receipts` table is grant-restricted (`lenny_erasure` INSERT only, `lenny_app` SELECT only, no UPDATE/DELETE anywhere), is retained for `audit.gdprRetentionDays` in lockstep with the receipt events it references, and is exempt from `DeleteByUser`/`DeleteByTenant` because it holds only hashes and identifiers. In `spec/16_observability.md` §16.1 Audit Integrity, added two metrics: `lenny_audit_chain_integrity_total{tenant_id,state}` (per-state counter covering the full enum including `redacted_gdpr`) and `lenny_audit_redaction_receipt_missing_total{tenant_id}` (orphaned-redaction detector). In §16.5, rewrote `AuditChainGap` to fire on `increase(lenny_audit_chain_integrity_total{state="broken"}[15m]) > 0` and explicitly exclude `redacted_gdpr` rows backed by a signature-verifying receipt; added new critical alert `AuditRedactionReceiptMissing` firing on `increase(lenny_audit_redaction_receipt_missing_total[15m]) > 0` for orphaned discontinuities. In §16.7 updated the `gdpr.erasure_deadletter_redacted` payload to include `post_redaction_prev_hash` and `redaction_receipt_id`. In §16.8 expanded the Section 25 audit-metric enumeration to list the two new metrics. In `spec/25_agent-operability.md` §25.9 updated the `chainIntegrity` bullet list (adding `redacted_gdpr` with its provenance-receipt requirement) and the `chainIntegrityReport` envelope field enumeration. Enum values are now consistent across §11.7, §16.1 metric labels, §16.5 alert conditions, and §25.9 documentation.

### CMP-051. Dead-letter receipt reconciliation with downstream SIEM not specified [Medium] — Fixed

**Section:** spec/12_storage-architecture.md §12.8 (DeleteByUser step 14), spec/11_policy-and-controls.md §11.7 (OCSF dead-letter handling), spec/16_observability.md (gdpr.erasure_deadletter_redacted event)

Iter3 CMP-047 recommendation 5 called for explicit treatment of dead-letter rows that were already forwarded to an external SIEM before erasure (they may still contain pre-redaction canonical payloads outside Lenny's control). The spec redacts in-tree storage and emits a `gdpr.erasure_deadletter_redacted` audit event, but does not require emission of a downstream erasure notification to SIEM sinks or document that offline copies are out of scope. GDPR Art. 17(2) requires controllers to take reasonable steps to inform other processors holding the data. Without an explicit SIEM reconciliation signal or operator guidance, the erasure is incomplete for any dead-letter content previously streamed.
**Recommendation:** Extend §12.8 step 14 to also emit a structured `erasure.requested` OCSF event (category 5 / audit-activity) per redacted dead-letter row, addressed to the same OCSF sinks that received the original dead-letter event and carrying the original `audit_id`, `original_hash`, erasure job ID, and legal basis. SIEM operators can then act on it or acknowledge that downstream erasure is their responsibility. Document in spec/25_agent-operability.md operator runbook that copies extracted from SIEM stores before redaction are out of Lenny's cryptographic control and must be erased by the SIEM operator. List the new event in spec/16_observability.md alongside `gdpr.erasure_deadletter_redacted`.

**Resolution:** Added a "Downstream SIEM erasure notification" paragraph to `spec/12_storage-architecture.md` §12.8 Step 14 (after the `audit_redaction_receipts` grant-and-retention paragraph) specifying that the erasure job MUST emit a new `gdpr.erasure_deadletter_downstream_notified` audit event per redacted dead-letter row, mapped to OCSF class 5001 Entity Management with `activity_id: 4 Delete` and `category_uid: 5`, routed through the same OCSF translator to the SIEM forwarder, pgaudit sink consumer, and subscribed webhooks that received the original class-2004 dead-letter receipt. Payload carries `audit_event_id`, `tenant_id`, `original_event_type`, `original_sequence_number`, `original_hash` (from the signed `RedactionReceipt`), `erasure_job_id`, `legal_basis`, `redaction_receipt_id`, `redacted_at`, and `downstream_action_required: true`, carrying only hashes and identifiers so a translator failure on the notification cannot itself leak personal data. Registered the new event in `spec/16_observability.md` §16.7 alongside `gdpr.erasure_deadletter_redacted` with full payload documentation. Added a new "Downstream SIEM Scope Boundary (GDPR Erasure)" subsection to `spec/25_agent-operability.md` §25.9 stating that Lenny's cryptographic control ends at its own stores, that the new event is an **action-required** signal for the SIEM operator to delete previously-ingested copies, that deployers MUST document downstream SIEM erasure responsibility in their GDPR data-processing agreement, and that offline/air-gapped copies (e-discovery exports, quarterly SIEM backups, legal-hold snapshots) are out of Lenny's scope by the same rationale. The notification is framed as Lenny's fulfillment of GDPR Art. 17(2) "reasonable steps to inform other processors holding the data"; §11.7 dead-letter handling required no change because its existing "Erasure interaction" clause already links to §12.8 Step 14.

### CMP-052. Tenant force-delete bypasses per-resource legal-hold filtering [Medium] — Fixed

**Section:** spec/12_storage-architecture.md §12.8 (Tenant deletion lifecycle Phases 1-6 + 4a, line 841 legal-hold interaction)

The tenant deletion lifecycle mentions legal-hold interaction pre-Phase-4 but does not specify what happens when an operator uses an admin force-delete override. Phase 4 crypto-shreds the tenant KMS key, which renders all tenant-scoped artifact-store content and any resource encrypted under that key unrecoverable, including resources under per-resource legal holds that should outlive the tenant deletion. The force-delete path must treat legal-hold-covered resources as exceptions and preserve them in a legal-hold-only escrow (separate KMS key) before KMS destruction, or refuse the force-delete entirely. This is the tenant-level analog of CMP-049 and CMP-046.
**Recommendation:** In §12.8 tenant deletion lifecycle, add an explicit Phase 3.5 "Legal-hold segregation" step that: (a) enumerates all `LegalHold` resources scoped to the tenant, (b) re-encrypts held resources (audit rows, artifact objects, session transcripts) under a separate platform-managed `legal_hold_escrow_kek` before Phase 4 tenant KMS destruction, (c) migrates them to a dedicated escrow bucket with an independent retention policy tied to hold release, (d) records the migration in the legal-hold ledger. Force-delete without `--acknowledge-hold-override` must fail with `TENANT_DELETE_BLOCKED_BY_LEGAL_HOLD`. The override itself must be logged as `gdpr.legal_hold_overridden_tenant` and retained under the `audit.gdprRetentionDays` floor. Update spec/25_agent-operability.md tenant-delete API error-codes table accordingly.

**Resolution:** Added explicit Phase 3.5 "Legal-hold segregation (CMP-052)" row to the tenant deletion lifecycle table in `spec/12_storage-architecture.md` §12.8, positioned between Phase 3 (Revoke credentials) and Phase 4 (Delete data). Rewrote the "Legal hold interaction during deletion" paragraph into three paragraphs documenting the fail-closed gate, the four-sub-step segregation flow under `--acknowledge-hold-override`, and the operator-guidance wrap-up. Standard path: when any active hold scoped to the tenant exists (sessions with `legal_hold = true`, artifacts with `legal_hold = true`, or audit-range / workspace-snapshot holds in the legal-hold ledger), the controller pauses at Phase 3, emits `admin.tenant.deletion_blocked`, and rejects any attempt to advance Phase 4 with `TENANT_DELETE_BLOCKED_BY_LEGAL_HOLD` (HTTP 409, `POLICY`). Override path (`POST /v1/admin/tenants/{id}/force-delete` with `{"acknowledgeHoldOverride": true, "justification": "<required text>"}` by a `platform-admin`; `tenant-admin` callers are rejected `403`, missing justification `400`): (1) enumerate held resources and pin the tuple list into the erasure receipt's `overridden_holds`, (2) decrypt held ciphertext under the tenant KEK and re-wrap the DEK under the platform-managed `legal_hold_escrow_kek` (held in keyring `platform:legal_hold_escrow`, single / long-lived / platform-wide, distinct from every tenant KEK, decrypt permission scoped to the platform legal-hold escrow service account), (3) migrate ciphertext to the `legal-hold-escrow` MinIO bucket under key `legal-hold-escrow/{original_tenant_id}/{resourceType}/{resourceId}` with retention tied to hold release (`retain-until-hold-release`, MinIO object-lock `COMPLIANCE` for the duration), and (4) write a `legal_hold.escrowed` audit event per migrated resource carrying `tenant_id`, `resourceType`, `resourceId`, `original_hold_set_at`, `escrow_object_key`, `escrow_kek_id: "platform:legal_hold_escrow"`, `tenant_delete_job_id`, and `migrated_at`. Phase 4's `DeleteByTenant` now explicitly excludes any resource marked `legal_hold_escrow: true`; escrow release is via `POST /v1/admin/legal-hold` (hold: false) on the tombstoned tenant, which emits `legal_hold.escrow_released`. A `gdpr.legal_hold_overridden_tenant` critical audit event (distinct from the user-scope `gdpr.legal_hold_overridden` from CMP-049) is emitted with `tenant_id`, `job_id`, `override_by`, `override_justification`, `override_at`, `overridden_holds`, and `escrow_object_keys`, retained under `audit.gdprRetentionDays` (2555-day default, per-regulated-profile floor 2190 days), written to the platform tenant so it survives the tombstone. Companion changes: `spec/15_external-api-surface.md` error-codes table registers `TENANT_DELETE_BLOCKED_BY_LEGAL_HOLD` (`POLICY`, 409) with the full semantics and override path, and the `/v1/admin/tenants/{id}/force-delete` endpoint row now documents the `{acknowledgeHoldOverride, justification}` body, the Phase 3.5 segregation, and the new audit event / alert; `spec/16_observability.md` §16.5 adds the `LegalHoldOverrideUsedTenant` warning alert (fires on every `gdpr.legal_hold_overridden_tenant`); §16.7 registers `gdpr.legal_hold_overridden_tenant`, `legal_hold.escrowed`, and `legal_hold.escrow_released` with payload field lists and notes the tenant-scope analog relationship to `gdpr.legal_hold_overridden`; `spec/24_lenny-ctl-command-reference.md` updates the `lenny-ctl admin tenants force-delete` flags to `--acknowledge-hold-override --justification <text>`; `spec/25_agent-operability.md` MCP tool table splits into `lenny_tenant_delete` (blocked-by-hold) and new `lenny_tenant_force_delete` (override path) referencing the §15 error code and the §12.8 Phase 3.5 semantics. The new identifiers (`TENANT_DELETE_BLOCKED_BY_LEGAL_HOLD`, `gdpr.legal_hold_overridden_tenant`, `legal_hold.escrowed`, `legal_hold.escrow_released`, `LegalHoldOverrideUsedTenant`, `legal_hold_escrow_kek`) do not collide with any existing names. The fix mirrors CMP-049 style: additive Phase 3.5 / Step 0, no refactor of existing phases; tenant-scope symmetric to user-scope.

### CMP-053. ArtifactStore MinIO replication has no runtime residency fail-closed guard [Medium] — Fixed

**Section:** spec/25_agent-operability.md §25.11 (ArtifactStore replication, line 3869; runtime validation gap at line 3896), spec/12_storage-architecture.md §12.8 (per-region backup residency lines 883-888), spec/16_observability.md (MinIOArtifactReplicationLagHigh/Failed)

Iter3 CMP-048 added MinIO ArtifactStore replication to the backup topology, and §12.8 enforces residency for Postgres backups via `BACKUP_REGION_UNRESOLVABLE` (fail-closed at runtime when a source region cannot be mapped to a compliant backup region). The ArtifactStore replication path only validates target jurisdiction at startup config load; there is no runtime analog. A late reconfiguration, DNS rebinding, region-tag drift in the target MinIO cluster's metadata, or an operator mis-edit during an incident can silently route tenant artifacts across a jurisdiction boundary. Replication lag alerts fire for throughput but not for jurisdiction mismatches. This is a GDPR Art. 44 (transfers) and data-residency risk.
**Recommendation:** Add a runtime replication preflight that re-validates the target MinIO cluster's advertised jurisdiction tag against the source tenant's `data_residency_region` on every replication batch (or at minimum every N minutes). On mismatch: halt replication for the affected tenant, emit `ARTIFACT_REPLICATION_REGION_UNRESOLVABLE` (mirror of `BACKUP_REGION_UNRESOLVABLE`) and raise a Critical alert `ArtifactReplicationResidencyViolation`. Document the new error code in spec/25_agent-operability.md error-codes table and the new alert in spec/16_observability.md. Include the replication target's jurisdiction tag in the cross-region replication audit event so chain-of-custody includes residency at write time, not just config time.

**Resolution:** Added a runtime residency preflight to the ArtifactStore continuous-replication path in `spec/25_agent-operability.md` §25.11, mirroring the `BACKUP_REGION_UNRESOLVABLE` fail-closed pattern used for the Postgres `pg_dump` pipeline and closing the post-startup drift gap. Two new paragraphs were inserted directly after the startup-time `CONFIG_INVALID: minio.regions.<region>.artifactBackup.target incomplete` paragraph: the first (**Runtime residency preflight**) documents cadence (before every replication batch AND on a periodic tick `minio.artifactBackup.residencyCheckIntervalSeconds`, default 300s — both gates present so long idle gaps between batches cannot hide a silent redirection), probe mechanics (three checks: (a) `s3:GetBucketTagging` on the destination bucket reading the mandatory `lenny.dev/jurisdiction-region` tag, (b) comparison to the source region's `dataResidencyRegion`, (c) DNS-resolution check against optional `backups.regions.<region>.allowedDestinationCidrs` as a DNS-rebinding guard), behaviour on mismatch (replication suspended via `mc replicate disable` / provider equivalent, `ops_artifact_replication_state.status: "suspended_residency_violation"`, `ARTIFACT_REPLICATION_REGION_UNRESOLVABLE` surfaced on admin queries, `DataResidencyViolationAttempt` audit event with `operation: "artifact_replication"` reusing the existing `lenny_data_residency_violation_total` counter, new dedicated counter `lenny_minio_replication_residency_violation_total{region, violation_type}`, critical alert `ArtifactReplicationResidencyViolation` fires immediately), and resume semantics (operator-only via `POST /v1/admin/artifact-replication/{region}/resume`, `platform-admin`, audited with justification, preflight re-runs synchronously at resume time — no silent retry, no automatic resume). The second paragraph (**Cross-region replication audit**) establishes a positive audit trail: on every successful preflight round `lenny-ops` emits `artifact.cross_region_replication_verified` (sampled per `minio.artifactBackup.residencyAuditSamplingWindowSeconds`, default 3600s, first event per `(region, destination_endpoint)` per window) with `source_region`, `source_data_residency_region`, `destination_endpoint`, `destination_bucket`, `destination_jurisdiction_tag`, `verified_at`, `batch_object_count` — so chain-of-custody records the destination's advertised jurisdiction at the write-time of each batch rather than only the startup config-load time. Companion changes: `spec/25_agent-operability.md` §25.11 registers the new endpoints `POST /v1/admin/artifact-replication/{region}/resume` and `GET /v1/admin/artifact-replication/{region}/status`, registers error code `ARTIFACT_REPLICATION_REGION_UNRESOLVABLE` (`PERMANENT`, 422) in the §25.11 error-codes table, adds the two new Helm values (`residencyCheckIntervalSeconds`, `residencyAuditSamplingWindowSeconds`) to the Required Helm values block, and extends the §25.11 Audit Events line to list `artifact.cross_region_replication_verified`, `artifact_replication.resumed`, and the expanded `DataResidencyViolationAttempt` operation coverage. `spec/16_observability.md` §16.1 registers the new counter metric `lenny_minio_replication_residency_violation_total{region, violation_type}` with enumerated violation types (`jurisdiction_tag_mismatch` | `jurisdiction_tag_missing` | `dns_rebinding_outside_cidr` | `tag_probe_failed`); §16.5 adds the `ArtifactReplicationResidencyViolation` critical alert firing on `rate(lenny_minio_replication_residency_violation_total[5m]) > 0`, explicitly distinguished from the throughput-only `MinIOArtifactReplicationLagHigh` and object-level `MinIOArtifactReplicationFailed`; §16.7 registers `artifact.cross_region_replication_verified` and `artifact_replication.resumed` in the Section 25 audit-event inventory with full payload field lists and the note that `DataResidencyViolationAttempt` now covers `operation: "write" | "backup" | "artifact_replication"`; §16.8 adds `lenny_minio_replication_residency_violation_total{region, violation_type}` to the Backup/restore metric enumeration. `spec/12_storage-architecture.md` §12.8 "Backup pipeline residency" gains a new point 5 documenting the ArtifactStore continuous-replication runtime residency preflight as the continuous-surface analog of `BackupRegionUnresolvable`, cross-referencing §25.11 for the full protocol. `spec/11_policy-and-controls.md` §11.4 Platform first-responder signals table expands the `DataResidencyViolationAttempt` row to mention artifact-replication bypass and adds a new `ArtifactReplicationResidencyViolation` critical-signal row pointing to §25.11. The new identifiers (`ARTIFACT_REPLICATION_REGION_UNRESOLVABLE`, `ArtifactReplicationResidencyViolation`, `lenny_minio_replication_residency_violation_total`, `artifact.cross_region_replication_verified`, `artifact_replication.resumed`, `minio.artifactBackup.residencyCheckIntervalSeconds`, `minio.artifactBackup.residencyAuditSamplingWindowSeconds`, `backups.regions.<region>.allowedDestinationCidrs`, `ops_artifact_replication_state`) do not collide with any pre-existing names. The fix is defensive and additive — no refactor of existing ArtifactStore replication, backup pipeline, or alert behaviour; mirrors the iter3 CMP-048 scope and the §12.8 `BACKUP_REGION_UNRESOLVABLE` pattern exactly.

---

## 13. API & Wire-Contract Review

### API-010. — `CREDENTIAL_SECRET_RBAC_MISSING`, `GIT_CLONE_AUTH_UNSUPPORTED_HOST`, `GIT_CLONE_AUTH_HOST_AMBIGUOUS` use HTTP 400 for server-state rejections [High] — Fixed

**Section:** spec/15_external-api-surface.md §15.4 error-codes table (lines 981, 1050, 1051); spec/04_system-components.md §4.9 (lines 1138, 1207); spec/14_workspace-plan-schema.md §14 (line 93).

**Resolution:** All three codes described server-state rejections — the request body was well-formed but the server's current configuration (Token Service RBAC grants; tenant VCS credential-pool topology) prevented acceptance. Per the canonical §15.4 taxonomy embedded in the same table, HTTP 400 is reserved for malformed client input (the `VALIDATION_ERROR` family and its peers), while unprocessable server-state / configuration conflicts use HTTP 422 (`INVALID_POOL_CONFIGURATION`, `COMPLIANCE_PGAUDIT_REQUIRED`, `COMPLIANCE_SIEM_REQUIRED`, `CREDENTIAL_PROVIDER_MISMATCH`, `CONFIGURATION_CONFLICT`, `KMS_REGION_UNRESOLVABLE`, `REGION_CONSTRAINT_UNRESOLVABLE`, `WORKSPACE_PLAN_SCHEMA_UNSUPPORTED`, `ISOLATION_MONOTONICITY_VIOLATED`, etc.). The three offending rows were re-categorized to match the canonical pattern: `CREDENTIAL_SECRET_RBAC_MISSING` moves to `PERMANENT` 422 (matches the `INVALID_POOL_CONFIGURATION` / `COMPLIANCE_PGAUDIT_REQUIRED` pattern — a server-side admin-configuration precondition is unmet, not a malformed request), and both `GIT_CLONE_AUTH_UNSUPPORTED_HOST` and `GIT_CLONE_AUTH_HOST_AMBIGUOUS` move to `POLICY` 422 (matches the `CREDENTIAL_PROVIDER_MISMATCH` pattern — tenant VCS-pool topology produces zero / non-unique host→pool resolution, a POLICY rejection of a well-formed request). The §15.4 rows were updated in-place; the two inline call-sites in §4.9 ("zero matches yields … and multiple matches yield …" at line 1138; "maps to `400 CREDENTIAL_SECRET_RBAC_MISSING`" at line 1207) and the one inline call-site in §14 ("rejected at session creation with `400 GIT_CLONE_AUTH_UNSUPPORTED_HOST`; … `400 GIT_CLONE_AUTH_HOST_AMBIGUOUS`" at line 93) were rewritten to `422`. No other wire-contract references existed. The category change on `CREDENTIAL_SECRET_RBAC_MISSING` was preserved as `PERMANENT` because the error is admin-time and unrelated to policy evaluation; the two Git-clone codes became `POLICY` because the rejection arises from evaluating tenant VCS-pool configuration against the request, which is the same decision shape used by `CREDENTIAL_PROVIDER_MISMATCH`. The `details` payload schemas, retry guidance, and cross-references are preserved.

### API-011. — `ISOLATION_MONOTONICITY_VIOLATED` consolidated to 422 but cognate delegation-admission POLICY codes remain 403 [Medium] — Fixed

**Section:** spec/15_external-api-surface.md §15.4 error-codes table (lines 1053, 1054, 1055).

**Resolution:** The iter3 API-005 / SEC-003 consolidation moved `ISOLATION_MONOTONICITY_VIOLATED` to a single canonical row at HTTP 422 POLICY on the premise that POLICY rejections of well-formed requests (where the request body is syntactically and semantically valid but is rejected by a delegation-admission / lease-construction check) map to HTTP 422 — distinct from HTTP 403, which in the §15.4 taxonomy is reserved for role/scope-based authz denials (`FORBIDDEN`, `PERMISSION_DENIED`, `SCOPE_FORBIDDEN`) and for credential-identity rejections (`CREDENTIAL_REVOKED`, `LEASE_SPIFFE_MISMATCH`, `INJECTION_REJECTED`, etc.). Three cognate codes — `CONTENT_POLICY_WEAKENING`, `CONTENT_POLICY_INTERCEPTOR_SUBSTITUTION`, and `DELEGATION_POLICY_WEAKENING` — describe structurally identical delegation-admission rejections (child-lease construction fails because the child would weaken a parent-enforced monotonicity invariant: content-scan interceptor retention, interceptor identity, or `maxDelegationPolicy` tightness). They sit in the same admission pipeline as `ISOLATION_MONOTONICITY_VIOLATED` (per §8.3 delegation policy and lease), and the cognate `CREDENTIAL_PROVIDER_MISMATCH` and `VARIANT_ISOLATION_UNAVAILABLE` codes in the same table are already 422 POLICY. Per the canonical §15.4 taxonomy, the three offending rows were re-categorised from `POLICY` 403 to `POLICY` 422 in §15.4 (lines 1053, 1054, 1055), aligning them with `ISOLATION_MONOTONICITY_VIOLATED` (line 1042), `CREDENTIAL_PROVIDER_MISMATCH` (line 1010), and `VARIANT_ISOLATION_UNAVAILABLE` (line 1043). The rationale and alignment pointer were added inline to each row. Inline references to these codes in §8.3 (lines 135, 136, 185) do not quote HTTP status codes and therefore required no change; the §8.3 prose links to §15.1/§15.4 for the canonical catalog entry. The `details` payload schemas, retry guidance, cross-references, and the REST/MCP consistency contract test assertion (§15.2.1 single `(code, http_status, category, retryable)` tuple per code) remain intact — each code continues to have exactly one catalog row.

### API-012. — `DELEGATION_PARENT_REVOKED` HTTP 409 PERMANENT inconsistent with existing `CREDENTIAL_REVOKED` 403 POLICY [Medium] — Fixed

**Section:** spec/15_external-api-surface.md §15.4 error-codes table (line 1025).

**Resolution:** `DELEGATION_PARENT_REVOKED` and `CREDENTIAL_REVOKED` are structurally identical credential-identity revocation rejections: in both cases a previously-good token/credential has been placed on the revocation deny list, and the admission pipeline detects the revoked `jti` (or equivalent identity) inside the issuance/lease-construction transaction. The iter3 DEL-009 fix originally catalogued `DELEGATION_PARENT_REVOKED` at `PERMANENT`/409 on the reading that the parent-rotation race is a conflict with existing state, but per the canonical §15.4 taxonomy consolidated by iter4 API-010 / API-011, HTTP 409 is reserved for resource-state conflicts (`INVALID_STATE_TRANSITION`, `RESOURCE_ALREADY_EXISTS`, `RESOURCE_HAS_DEPENDENTS`, `TARGET_TERMINAL`, `SEED_CONFLICT`, `REPLAY_ON_LIVE_SESSION`, `DERIVE_ON_LIVE_SESSION`) while HTTP 403 POLICY is reserved for credential-identity rejections (`CREDENTIAL_REVOKED`, `LEASE_SPIFFE_MISMATCH`, `INJECTION_REJECTED`) as well as role/scope-based authz denials (`FORBIDDEN`, `PERMISSION_DENIED`, `SCOPE_FORBIDDEN`). A revoked parent token is a credential-identity failure (the actor_token's `jti` is on the deny list), not a resource-state conflict, so the correct canonical placement is `POLICY`/403 alongside `CREDENTIAL_REVOKED`. The §15.4 row at line 1025 was re-categorised from `PERMANENT`/409 to `POLICY`/403 in place, with an inline rationale pointer added to the description (matches `CREDENTIAL_REVOKED`, `LEASE_SPIFFE_MISMATCH`, `INJECTION_REJECTED`; distinguished from the 409 resource-state-conflict family). Retry guidance (`Not retryable — the caller must re-authenticate or the parent session has been terminated`) and the `details.parentSessionId` / `details.revocationReason` payload schema are preserved. The inline reference in §8.2 (line 61) names the error code but does not quote an HTTP status, so no prose change is required (same pattern iter4 API-011 followed for §8.3 references). The REST/MCP consistency contract in §15.2.1 item 3 continues to hold: each code has exactly one catalog row carrying a single `(code, http_status, category, retryable)` tuple, and both sides of the §15.2.1 contract test assert identical tuples across REST and MCP transports.


### API-013. — REST/MCP contract-test matrix not updated to cover new session-creation error codes (`VARIANT_ISOLATION_UNAVAILABLE`, etc.) [Medium] — Fixed

**Section:** (unspecified)

**Resolution:** The §15.2.1 `RegisterAdapterUnderTest` test matrix's "All error classes" line previously listed only nine error codes (`VALIDATION_ERROR`, `QUOTA_EXCEEDED`, `RATE_LIMITED`, `RESOURCE_NOT_FOUND`, `INVALID_STATE_TRANSITION`, `PERMISSION_DENIED`, `CREDENTIAL_REVOKED`, `CREDENTIAL_POOL_EXHAUSTED`, `ISOLATION_MONOTONICITY_VIOLATED`), none of which are the iter3/iter4-vintage session-creation rejections that §15.4 catalogs. Without those codes in the matrix, a third-party adapter could pass `POST /v1/admin/external-adapters/{name}/validate` while diverging from REST on the `code`/`category`/`retryable` tuples for session-creation rejections that clients depend on for their error-handling and retry logic. The matrix line was extended with the full session-creation rejection family that §15.4 documents as "Session creation rejected" / "Session creation failed" / "New session creation ... rejected": `VARIANT_ISOLATION_UNAVAILABLE` (POLICY/422, the iter3-added ExperimentRouter isolation-monotonicity fail-closed code), `REGION_CONSTRAINT_UNRESOLVABLE` (PERMANENT/422), `GIT_CLONE_AUTH_UNSUPPORTED_HOST` (POLICY/422), `GIT_CLONE_AUTH_HOST_AMBIGUOUS` (POLICY/422), `ENV_VAR_BLOCKLISTED` (PERMANENT/400), `SDK_DEMOTION_NOT_SUPPORTED` (PERMANENT/422), `POOL_DRAINING` (TRANSIENT/503), `CIRCUIT_BREAKER_OPEN` (POLICY/503), `ERASURE_IN_PROGRESS` (POLICY/403), and `TENANT_SUSPENDED` (POLICY/403). The clarification also anchors each code to its §15.4 catalog row (no `(code, status, category, retryable)` tuple is restated in §15.2.1, so the single-source-of-truth discipline iter4 API-011 established is preserved), and adds an explicit maintenance rule that any future session-creation rejection added to §15.4 MUST be added to the matrix in the same change so the two sections cannot drift. Delegation-only rejections like `CONTENT_POLICY_WEAKENING`, `CONTENT_POLICY_INTERCEPTOR_SUBSTITUTION`, `DELEGATION_POLICY_WEAKENING`, and `CREDENTIAL_PROVIDER_MISMATCH` were intentionally not added because `lenny/delegate_task` is an MCP-only tool with no REST counterpart (the matrix scope is "overlapping endpoints" per §15.2.1 item 5); `ISOLATION_MONOTONICITY_VIOLATED` was retained in its pre-existing slot because §15.4 documents it as applying uniformly to `delegate_task`, `derive`, and `replay` (the matrix already exercises those REST-overlapping paths).



### API-014. — iter3 API-006 catalog uniqueness invariant still not stated [Low]

**Section:** (unspecified)



### API-015. — `UNREGISTERED_PART_TYPE` uses `WARNING` category outside the canonical taxonomy stated one line above [Low]

**Section:** (unspecified)



### API-016. — `RESTORE_ERASURE_RECONCILE_FAILED` HTTP 500 for a known operator-action failure path [Low]

**Section:** (unspecified)

Let me write the final output:

---

---

## 14. Checkpoint & Partial Manifest

### CPS-006. Orphaned partial-chunk objects when gateway crashes between chunk commit and manifest write [High] — Fixed

**Section:** 10.1 Partial manifest on checkpoint timeout; 12.5 GC backstop

**Resolution:** Adopted the intent-row-first ordering approach (recommendation option b). Added a new "Intent-row-first ordering (orphan prevention)" paragraph to §10.1's partial-manifest subsection specifying the four-step flow: (1) gateway INSERTs the manifest row with `partial = true, chunk_count = 0, workspace_bytes_uploaded = 0` plus `partial_object_key_prefix`, `chunk_size_bytes`, `chunk_encoding`, `checkpoint_started_at`, `checkpoint_timeout_at`, and `manifest_reason` **before** any chunk `PutObject` is issued (atomic in the same transaction as the existing supersede-on-write step); (2) adapter uploads chunks, gateway incrementally updates `chunk_count` / `workspace_bytes_uploaded` via a monotonic conditional `UPDATE` keyed on `checkpoint_id`; (3) finalisation on tier-cap fire flushes the last observed counters and, if `chunk_count == 0`, soft-deletes the row in the same transaction so an empty partial manifest never reaches the resume path (CPS-010 interlock); (4) crash semantics: the intent row is durable from step 1, so the §12.5 backstop's existing `partial = true AND deleted_at IS NULL` sweep reliably discovers both the manifest row and its chunk objects under `partial_object_key_prefix`, closing the orphan window. Updated the partial-manifest-field description paragraph to document that `chunk_count` and `workspace_bytes_uploaded` are initialised to `0` at intent-row INSERT and updated monotonically (not only set at timeout), and that `chunk_size_bytes`, `chunk_encoding`, and `partial_object_key_prefix` are captured at intent-row INSERT and immutable thereafter. Updated the "Partial manifest on checkpoint timeout" opener to reference the intent-row-first ordering explicitly. Updated §12.5's partial-checkpoint-manifest bullet to list intent-row-first ordering as the third primary-path defense (alongside supersede-on-write and the partial unique index) and to note that the backstop's `partial = true AND deleted_at IS NULL` sweep predicate is now provably sufficient — no `PutObject` is ever issued without a referencing manifest row, so the backstop cannot miss a chunk. The supersede-on-write paragraph was updated to reference the intent-row INSERT step explicitly (the two now compose into a single transaction). GC backstop semantics and the existing `deleted_at IS NULL` monotonicity guard are unchanged.

The partial-manifest write flow implicitly has this ordering: (1) adapter uploads chunks via `PutObject` under `/{tenant_id}/checkpoints/{session_id}/partial/{checkpoint_id}/partial-{n}.tar.gz`, (2) tier cap fires, (3) gateway writes the `partial: true` manifest row to Postgres with `chunk_count` and `partial_object_key_prefix`. If the gateway crashes or loses Postgres connectivity between (1) and (3), chunks are already committed in MinIO under a `checkpoint_id` that no manifest row references. §12.5 GC backstop scans rows where `partial = true` — it cannot discover orphaned chunks whose manifest never committed. The eviction-fallback path ([§4.4](04_system-components.md#44-event--checkpoint-store)) logs committed keys via `lenny_checkpoint_eviction_partial_keys_logged_total` only when the total-loss Postgres path is reached; for the common case of "partial manifest intended but Postgres write delayed/crashed", no cleanup mechanism exists. Because `checkpoint_id` is a fresh UUID per attempt, orphans accumulate indefinitely: a single large tenant with repeated drain-during-outage events could leak hundreds of MB per drain cycle, bypassing the `storage_bytes_used` quota counter (the `artifact_store` rows are only inserted for completed artifacts per §11.2). This is a storage-leak and quota-integrity gap, not merely orphan noise.
**Recommendation:** Add to §12.5 artifact store GC a prefix-scan backstop: periodically list objects under `/{tenant_id}/checkpoints/*/partial/*/` whose parent `checkpoint_id` does not correspond to any row in the partial-manifest table AND whose `LastModified` exceeds `maxResumeWindowSeconds + gc.cycleIntervalSeconds`, and delete them per-key. Alternatively, invert the ordering: the adapter writes an "intent" row to Postgres with the `checkpoint_id` and `partial_object_key_prefix` BEFORE the first `PutObject`, sets `partial = true, chunk_count = 0` initially, and updates `chunk_count` as chunks commit — so every committed chunk has an associated (growing) manifest row even when the timeout fires mid-upload or the gateway crashes. Either approach closes the orphan window; the intent-row approach is simpler and preserves the existing GC guard.

### CPS-007. CheckpointBarrier timeout loses recoverable partial state (asymmetric with Stage 2 partial manifest) [Medium] — Fixed

**Section:** 10.1 CheckpointBarrier protocol lines 142–150; 10.1 Partial manifest on checkpoint timeout lines 126–136

**Resolution:** Extended the partial-manifest mechanism to cover the CheckpointBarrier BarrierAck timeout path, composing with CPS-006's intent-row-first ordering so that no new pod-side RPC is required. Because the CPS-006 fix guarantees that the partial-manifest intent row is written to Postgres **before** the adapter issues the first chunk `PutObject` for every chunked-upload path (including barrier-triggered checkpoints), the gateway on BarrierAck timeout can read the intent row directly from Postgres and finalise it as a partial manifest without invoking a `GetCheckpointProgress` RPC against a possibly-unresponsive pod. Specifically: (1) Updated the "Partial manifest on checkpoint timeout" opener in §10.1 to list both the Stage 2 tier-cap deadline and the `checkpointBarrierAckTimeoutSeconds` deadline as triggers, explicitly calling out that the BarrierAck path is the dominant drain-time path and that the mechanism is identical for both triggers — eliminating the earlier asymmetry. (2) Updated the Intent-row write step (step 1 of the intent-row flow) to derive `checkpoint_timeout_at` from either the Stage 2 tier cap or from `checkpoint_started_at + checkpointBarrierAckTimeoutSeconds`, depending on the triggering deadline, and updated the `manifest_reason` enum documentation to note that `timeout` covers both tier-cap and BarrierAck paths. (3) Updated the Finalisation step (step 3) to read "Finalisation on deadline fire" (not only tier-cap fire), with an explicit note that barrier-triggered finalisation is performed by the gateway's BarrierAck-timeout handler reading the intent row directly from Postgres — no pod RPC needed because intent-row-first ordering already made the gateway the authoritative holder of `chunk_count` / `partial_object_key_prefix`. (4) Added a new **BarrierAck-timeout partial-capture path (CPS-007)** paragraph to step 3 of the CheckpointBarrier protocol ("Checkpoint flush") specifying the five-rule handler: rule 1 reads the intent row keyed by `(session_id, slot_id)` under `partial = true AND deleted_at IS NULL` (deterministic by the partial unique index); rule 2 finalises the row when `chunk_count > 0` with identical semantics to the tier-cap path, emitting `lenny_checkpoint_partial_total{trigger="barrier_ack_timeout"}`; rule 3 soft-deletes and falls back when `chunk_count == 0` (composes with CPS-010's empty-manifest rejection); rule 4 falls back to the last periodic checkpoint when no intent row exists (pre-CPS-007 behavior preserved for pods that never began chunked upload); rule 5 falls back when the Postgres read itself fails. Explicitly noted that the handler operates on Postgres-only state and does not perturb the CRD-validated drain budget. (5) Updated the `lenny_checkpoint_barrier_ack_total` counter (§16.1) to add the `partial_captured` outcome value alongside the existing `success`/`timeout`/`error` values; `outcome="timeout"` now means "no partial state recovered" (rules 3–5) and `outcome="partial_captured"` means "partial manifest finalised from intent row" (rule 2). (6) Updated the `lenny_checkpoint_partial_total` counter (§16.1 and §10.1 Cleanup paragraph) to add a `trigger` label with values `tier_cap` | `barrier_ack_timeout` | `resume_snapshot_close`, enabling operators to distinguish Stage 2 and BarrierAck partials on a single counter. The path is load-balancing-tight: no new RPC surface, no new table, no new deadline; it composes atomically with the CPS-006 intent-row-first invariant.

The partial-manifest path is scoped to "a checkpoint upload does not complete within the applicable **tier cap**" (§10.1 Stage 2), but the CheckpointBarrier protocol has its own `checkpointBarrierAckTimeoutSeconds` deadline (default: 90s) and on timeout "the gateway falls back to the last successful periodic checkpoint for those sessions" (§10.1 line 146). Barrier-triggered checkpoints are the dominant drain-time path — every coordinated session emits one — yet they have no partial-recovery story. A 500 MB workspace that reaches 400 MB uploaded when the BarrierAck timer fires discards 400 MB of bandwidth and reverts to a checkpoint up to `periodicCheckpointIntervalSeconds` (10 min) stale, while Stage 2's tier-cap timeout (for the much rarer in-flight pre-drain checkpoint) would have written a partial manifest that a resume could consume. The CRD-validation-rule (`max_tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30 &gt; terminationGracePeriodSeconds`) further guarantees these two timers fire at different points in the drain, so the asymmetry is by design — but it leaves the common-case drain-loss path worse-protected than the rare pre-drain in-flight path.
**Recommendation:** Extend the partial-manifest mechanism to cover BarrierAck timeouts. Specifically: if `CheckpointBarrierAck` is not received by `checkpointBarrierAckTimeoutSeconds`, the gateway should (1) query the pod's chunk-commit state via a lightweight `GetCheckpointProgress` RPC (chunk_count, partial_object_key_prefix), (2) write the partial manifest with those values, (3) apply the same reassembly logic on resume. If the RPC itself is unanswered, the current "fall back to last periodic checkpoint" behavior applies. This removes the asymmetry between in-flight pre-drain checkpoints and the far-more-common barrier-driven drain checkpoints, and uses the mechanism the spec already defines.

### CPS-008. `partial-{n}.tar.gz` naming convention inconsistent with `chunk_encoding: tar` [Medium] — Fixed

**Section:** 10.1 Partial manifest on checkpoint timeout line 128; 10.1 Reassembly on resume line 134

The chunk naming pattern is hard-coded to `partial-{n}.tar.gz` (line 128: `naming each chunk deterministically as /{tenant_id}/checkpoints/{session_id}/partial/{checkpoint_id}/partial-{n}.tar.gz`). But the manifest's `chunk_encoding` field may be `tar` (line 130: `chunk_encoding (tar or tar.gz)`), in which case the chunk contents are uncompressed tar bytes, not gzip-compressed. The `.tar.gz` suffix is then a misleading label on an uncompressed payload. This is not merely cosmetic — operators performing manual reconstruction per the `lenny_checkpoint_eviction_partial_keys_logged_total` log path ([§4.4](04_system-components.md#44-event--checkpoint-store) `MinIO object key logging for manual recovery`) will reasonably attempt `gzip -dc &lt; partial-00000.tar.gz` and receive "not in gzip format" errors on the `tar` encoding, then misattribute the failure to chunk corruption. Worse, an external S3 lifecycle rule or MIME-type-aware tool may apply gzip-specific handling to an uncompressed file.
**Recommendation:** Either (a) make the chunk suffix reflect the actual encoding (`partial-{n}.tar` for `tar`, `partial-{n}.tar.gz` for `tar.gz`) and update the §10.1 Cleanup path + §12.5 GC backstop glob patterns accordingly, or (b) drop the encoding-suggestive suffix entirely (e.g., `partial-{n}` with no extension, keeping `chunk_encoding` in the manifest as the single source of truth). Option (a) is preferable because it keeps content-type inferrable from object name for manual recovery and for storage-lifecycle tools; option (b) removes the ambiguity at the cost of losing a hint. Either way, the operational runbook for manual reconstruction should state that the decompression step is conditional on `chunk_encoding`, not on the object key suffix.

**Resolution:** Applied option (a). The chunk object-key template in §10.1 Chunked-object storage model (line 139) now reads `/{tenant_id}/checkpoints/{session_id}/partial/{checkpoint_id}/partial-{n}.{chunk_encoding}`, with explicit worked examples for both encodings (`partial-00000.tar, partial-00001.tar, …` for `chunk_encoding: tar`; `partial-00000.tar.gz, partial-00001.tar.gz, …` for `chunk_encoding: tar.gz`). The partial-manifest row's "derivable keys" note (§10.1 line 141) now expands the key derivation to `partial_object_key_prefix + partial-{n}.{chunk_encoding}` and restates the two concrete forms. The §10.1 Reassembly-on-resume streaming example (line 155) now shows both encoding-specific key sequences and makes explicit that the decode pipeline is selected from the manifest's `chunk_encoding` column — never inferred from the object-key suffix. The §12.5 GC backstop (line 314) now clarifies that its `ListObjectsV2` call is scoped by prefix and is encoding-agnostic, naturally matching both `partial-{n}.tar` and `partial-{n}.tar.gz` without a glob or suffix-specific filter — so the backstop requires no change in behaviour beyond the documentation clarification. The §4.4 "Checkpoint Atomicity" note (line 234) and the §4.4 "MinIO object key logging for manual recovery" structured-log schema (line 279) both now carry the `partial-{n}.{chunk_encoding}` form; the manual-recovery log additionally adds `chunk_encoding` as an explicit top-level field (so operators select `gzip -dc | tar -x` vs `tar -x` from the logged column, not from the suffix) and states the invariant in imperative form: "The decompression step MUST be selected from the logged `chunk_encoding` field, not from the object-key suffix."

### CPS-011. iter3 CPS-003 / CPS-006 / CPS-007 remain unresolved in current spec [Medium] — Fixed

**Section:** 10.1 Partial manifest on checkpoint timeout lines 126–136; 10.1 CheckpointBarrier Barrier signal line 144

The iter3 review (`spec-reviews/review-findings/20260419000406/iter3/CPS.md`) identified three issues that are not addressed in the current spec:
- **iter3 CPS-003 (`partial_recovery_threshold_bytes` configuration surface undefined).** Line 134 still reads "configurable, default: 50% of last full checkpoint size" without naming a Helm value, CRD field, or pool-level knob. Operators cannot tune the threshold deterministically, and the "last full checkpoint size" quantity is still evaluated implicitly at resume time — see CPS-009 above for the compounding denominator problem.
- **iter3 CPS-006 (partial-manifest resume lacks `coordination_generation` filter).** Line 134 still says "the new coordinator detects `partial: true` in the latest checkpoint record" with no explicit `max(coordination_generation)` filter. A late-committed older-generation partial manifest can win against a timestamp-ordered selection under the split-brain scenarios described in iter3.
- **iter3 CPS-007 (CheckpointBarrier fan-out reads in-memory coordinator cache).** Line 144 still says "to every pod currently coordinated by this replica" and the source is the in-memory lease cache. Sessions handed off to or from this replica during preStop may be skipped or double-barriered.
**Recommendation:** Resolve each iter3 finding with its original iter3 recommendation: (1) name a Helm value for the threshold surface (`gateway.partialRecoveryThresholdFraction`, default `0.5`) and freeze the denominator in the manifest per CPS-009; (2) add the `max(coordination_generation)` filter to the partial-manifest resume selection in §10.1; (3) source the barrier-target set from a Postgres `coordination_lease` query with an in-memory-cache fallback and a `lenny_prestop_barrier_target_source` metric label. Each of these is a narrow edit within §10.1 and does not cross architectural layers.

**Resolution:** All three iter3 sub-findings resolved with narrow edits in §10.1, §16.1, and §17.8.1. (1) **iter3 CPS-003** — §10.1 intent-row-write step 1 (line 130) now freezes a `baseline_full_checkpoint_bytes` column on the manifest row at INSERT time (the session's current `last_checkpoint_workspace_bytes`, the same field used by preStop Stage 2 tier selection), and the new Helm value `gateway.partialRecoveryThresholdFraction` (default `0.5`, a fraction in `[0.0, 1.0]`) is registered in the §17.8.1 operational-defaults table; the resume-time threshold is evaluated as `workspace_bytes_uploaded >= baseline_full_checkpoint_bytes * partialRecoveryThresholdFraction` against the frozen denominator, making both the threshold check and the downstream `workspaceRecoveryFraction` self-contained against GC or rotation of the prior full checkpoint (also addresses iter4 CPS-009's compounding denominator problem). The partial-manifest fields enumeration was extended to carry the new column with explicit `NULL` semantics for sessions with no prior successful full checkpoint. (2) **iter3 CPS-006** — §10.1 **Reassembly on resume** paragraph (line 155) now selects the active partial row under an explicit `coordination_generation = (SELECT MAX(coordination_generation) FROM checkpoint_manifest WHERE session_id = $session_id AND slot_id = $slot_id AND partial = TRUE AND deleted_at IS NULL)` filter, so a late-committed older-generation partial manifest cannot win against a fenced newer-generation writer under split-brain; generation-stale rows are left to the §12.5 GC backstop and never consulted by the resume path. The existing full-checkpoint-wins-over-partial rule at matching-or-higher generation is preserved as a fencing-model extension (parallel to §4.4 eviction fallback). (3) **iter3 CPS-007** — §10.1 **CheckpointBarrier Barrier signal** step (line 165) now sources the barrier-target set from a bounded (2 s deadline) Postgres query against `coordination_lease` rather than the in-memory lease cache, with cache fallback on read failure or deadline expiry; the new `lenny_prestop_barrier_target_source_total` counter (`source ∈ {postgres, cache_fallback}`, labeled by `pool`) is registered in §16.1 alongside `lenny_prestop_cap_selection_total` so operators can detect how often the degraded path is exercised. Pods receiving a false-positive barrier (cache-fallback produced a stale entry) reject it as a generation-stale RPC under existing fencing rules — no new special-case logic required. None of the three edits conflicts with the just-applied iter4 CPS-006 intent-row-first ordering (which writes `coordination_generation` on the intent row at INSERT time — the same column the new `MAX(coordination_generation)` selection filter reads).

### CPS-009. `workspaceRecoveryFraction` denominator undefined when last full checkpoint was rotated out [Low]

**Section:** 10.1 Reassembly on resume line 134; 12.5 Checkpoint retention policy line 311; 07 session.resumed event schema

The `session.resumed` event carries `workspaceRecoveryFraction` computed as "bytes recovered / last full checkpoint bytes" (line 134 step 3) for `resumeMode: "partial_workspace"`. The retention policy (§12.5 line 311) keeps "only the latest 2 checkpoints per active session" and rotates older ones. Consider: full checkpoint F₁ (400 MB), later partial-on-timeout checkpoint P (300 MB uploaded), later full F₂ (450 MB) completes — retention rotates F₁ out. If F₂ is later deemed bad and the session resumes to P (because P sits between them and is chosen for partial reassembly), the denominator "last full checkpoint bytes" is ambiguous: F₁'s size (no longer queryable because F₁'s row was GC'd — §12.5 line 316 soft-deletes via `deleted_at` but the size column may be gone) or F₂'s size (the "most recent" full but one the reassembly path is explicitly bypassing). `partial_recovery_threshold_bytes` (iter3 CPS-003, still unresolved) has the identical denominator problem — "50% of last full checkpoint size" is evaluated against what, exactly, when multiple fulls exist or none survive? This propagates to operator-facing metrics (`workspaceRecoveryFraction` as a dashboard signal) and to the go/no-go threshold decision at resume time.
**Recommendation:** Stamp the denominator at partial-manifest-write time rather than recomputing it at resume. Add `baseline_full_checkpoint_bytes` to the partial manifest (set to the current `last_checkpoint_workspace_bytes` at manifest-write time, same value used for preStop tier selection). Compute `workspaceRecoveryFraction = bytes_recovered / baseline_full_checkpoint_bytes` and `partial_recovery_threshold_bytes = baseline_full_checkpoint_bytes * partialRecoveryThresholdFraction` — both sourced from the self-contained manifest row. This also addresses iter3 CPS-003 (threshold surface) by making the threshold operator-tunable via a single named fraction rather than a dynamic quantity.

### CPS-010. Contiguity check underspecified for `chunk_count == 0` edge case [Low]

**Section:** 10.1 Reassembly on resume line 134 step 1

Step 1 of reassembly requires "every index `n` in `[0, chunk_count)` must be present; a gap (a missing intermediate index), an out-of-order index, or an unexpected extra index outside `[0, chunk_count)` fails reassembly atomically." If `chunk_count == 0` (a manifest written after the timeout fired but before any `PutObject` call succeeded — plausible when MinIO becomes unavailable simultaneously with drain), the interval `[0, 0)` is empty, so `ListObjectsV2` should return zero objects and the check trivially passes. But the subsequent step (2) feeds "ascending index order" into a decompress→untar pipeline with zero input — the pipeline sees an immediate EOF. `tar -x` on an empty stream exits with status 0 having extracted nothing; the `bytes_recovered` post-extraction on-disk total is 0. The threshold check (`workspace_bytes_uploaded &gt;= partial_recovery_threshold_bytes`) should fail this case (50% of any positive number &gt; 0), but the spec does not explicitly state that a manifest with `chunk_count == 0` is rejected at manifest-write time. A zero-chunk manifest that somehow slips past the threshold check (e.g., `partial_recovery_threshold_bytes == 0` due to unresolved iter3 CPS-003 interpretation) would silently succeed with an empty workspace under `resumeMode: partial_workspace`, which is strictly worse than falling back to the full-checkpoint resume path.
**Recommendation:** Add to §10.1 line 130: "The manifest is NOT written if `chunk_count == 0`; the gateway instead proceeds directly to the CheckpointStorageUnavailable eviction path ([§4.4](04_system-components.md#44-event--checkpoint-store))." This keeps the partial-manifest path as a signal of actual partial progress rather than a fallback state carrier. Separately, make the reassembly-threshold check reject `workspace_bytes_uploaded == 0` unconditionally, independent of the threshold-fraction value, so that a regression in the threshold computation cannot produce an empty-workspace "partial" resume.

---

## 15. Warm Pool Lifecycle

### WPL-001. Schedulability precondition missing on scrub_warning cleanup transition [High] — Fixed

**Section:** spec/06_warm-pod-model.md §6.3 (state diagram), iter3 EXM-008

Iter3 EXM-008 added the "host node is schedulable" precondition to the `task_cleanup → sdk_connecting` transition (line 133) and defined an explicit `task_cleanup → draining` path when the node is unschedulable. However, the sibling `task_cleanup ──→ sdk_connecting [scrub_warning]` transition at line 134 — which fires on `onCleanupFailure: warn` with the cleanup budget still available — was not updated with the same precondition. Result: a pod on a cordoned node that experiences a scrub warning will still attempt the 60 s SDK re-warm and return to `idle`, exactly the behavior EXM-008 set out to prevent, and the same pod can be handed to a new claim before the Deployment rollout observes it. The two transitions are functionally equivalent from the perspective of "does this pod go back into the warm pool?", so the precondition must apply to both.
**Recommendation:** Amend line 134 to mirror the success-scrub guard, e.g. append `, host node is schedulable` to the preconditions list, and add to the trailing prose: "If the host node is unschedulable at this point, the pod transitions to `draining` instead (see 'Host-node schedulability precondition' below)." Apply the same update anywhere the scrub_warning edge is restated (e.g. the "Cleanup outcomes" subsection if it enumerates per-outcome transitions).

**Resolution:** Amended the `task_cleanup ──→ sdk_connecting [scrub_warning]` transition in spec/06_warm-pod-model.md §6.3 (now line 153) to include `, host node is schedulable` in the preconditions list, mirroring the sibling success-scrub edge on line 152. Added trailing prose to that edge stating that the pod transitions to `draining` instead when the host node is unschedulable, pointing to the "Host-node schedulability precondition" paragraph. Generalized that paragraph (line 179) so its scoping sentence now covers both `task_cleanup → sdk_connecting` and `task_cleanup → sdk_connecting [scrub_warning]` transitions, and added an explicit clause that a pod carrying a `scrub_warning` annotation on a cordoned node must not re-enter the warm pool. Verified that §5.2 `onCleanupFailure: warn` prose (lines 248 and 444) only forwards readers to §6.2/§6.3 and does not restate per-outcome transitions, so no downstream edits were required.

### WPL-002. Gateway lacks Node RBAC + informer to evaluate schedulability precondition [High] — Fixed

**Section:** spec/06_warm-pod-model.md §6.2 ("Host-node schedulability precondition"), spec/04_system-components.md §4.6.1 / §4.6.3 (WarmPoolController labeling and ServiceAccount RBAC), iter3 EXM-008

The EXM-008 fix states: "The gateway evaluates this condition by reading the Node object via its informer cache and checking `node.Spec.Unschedulable` plus the `node.kubernetes.io/unschedulable` taint." Neither prerequisite exists in the spec:
1. The Gateway ServiceAccount RBAC (§4.6.3, line 576) grants `get/patch` on Pods and claim/sandbox verbs but no verbs on Nodes. Only the WarmPoolController (line 574) has `get/list` on Nodes.
2. No §4 or §10 text describes a Node informer in the gateway process, its cache sizing, resync period, or startup synchronization requirement (a gateway that evaluates the precondition before the informer's initial LIST completes will either allow-all or deny-all incorrectly).
Without (1) the API call will 403; without (2) the phrase "informer cache" is unimplementable.
**Recommendation:** Either (a) add a Nodes reader entry to the Gateway ServiceAccount grant list ("Nodes: `get`, `list`, `watch` — cluster-scoped, for schedulability evaluation during warm-pod reuse") and document the Node informer in §4.6.2 (including "gateway waits for Node informer `HasSynced` before accepting claims"), **or** (b) move the evaluation to the WarmPoolController and expose the result through a pod annotation/status field the gateway can read without direct Node access (e.g. WPC labels pods with `lenny.dev/host-schedulable=false` on cordon, gateway treats that label as the gate). Option (b) is preferable because it keeps cluster-scoped reads out of the gateway's blast radius.

**Resolution:** Implemented option (b). Added a new "Host-node schedulability labeling (`lenny.dev/host-schedulable`)" paragraph to §4.6.1 (spec/04_system-components.md, inserted after "Orphaned `SandboxClaim` detection") declaring the WarmPoolController as the sole evaluator of per-pod host-node schedulability and defining the `lenny.dev/host-schedulable` pod label (`"true"` / `"false"`) that WPC maintains from its Node informer on every reconcile, including: re-labeling of all pods on an affected Node within a single reconcile cycle on cordon/uncordon events, a fail-safe default that treats an absent label as unschedulable, and an explicit note that the label is not in the `lenny-label-immutability` webhook's immutable set (which covers only `lenny.dev/managed`, `lenny.dev/delivery-mode`, `lenny.dev/egress-profile`, and `lenny.dev/tenant-id`) so WPC can legitimately flip its value. Extended the WarmPoolController ServiceAccount RBAC grant in §4.6.3 (line 586) from `get`/`list` on Nodes to `get`/`list`/`watch` on Nodes — the added `watch` verb is required for the Node informer that drives the new labeling behavior, and the grant comment now cross-references both CIDR-drift-detection and host-schedulable labeling as motivations; no other controller RBAC was touched. Rewrote the "Host-node schedulability precondition" paragraph in spec/06_warm-pod-model.md §6.2 (line 179) so that the gateway reads the `lenny.dev/host-schedulable` label via its existing `get` access on `Pods` — the prior wording ("gateway evaluates this condition by reading the Node object via its informer cache") is gone; the new text explicitly states the evaluation is performed by the WarmPoolController (not the gateway), cross-references §4.6.1 for the controller-side contract, documents the absent-label fail-safe (`"false"` or absent → pod transitions to `draining`), and explicitly notes the gateway holds no Node verbs on its ServiceAccount and requires no new RBAC. The Gateway ServiceAccount RBAC grant list in §4.6.3 is unchanged — no cluster-scoped Node read is added to the gateway's blast radius, which is the security property option (b) was chosen to preserve. Regression check: no spec text now says the gateway queries Nodes or holds a Node informer; all remaining "Node informer" references in spec/04_system-components.md and spec/06_warm-pod-model.md are scoped to the WarmPoolController. The new `lenny.dev/host-schedulable` label follows the `lenny.dev/` prefix convention shared by existing WPC-surfaced labels (`lenny.dev/state`, `lenny.dev/tenant-id`), composes with the iter3 EXM-008 schedulability semantics unchanged (the observable state machine behavior is identical — only the evaluator and the data path differ), and composes with the WPL-001 fix (both `task_cleanup → sdk_connecting` and `task_cleanup → sdk_connecting [scrub_warning]` edges now read the same label).

### WPL-003. preStop cap-selection alert is not computable from the emitted metric labels [Medium] — **Fixed**

**Section:** spec/16_observability.md §16.2 (metric), §16.5 (`PreStopCapFallbackRateHigh`), spec/10_gateway-internals.md preStop Stage 2, iter3 FLR-008

`lenny_prestop_cap_selection_total` is defined with labels `pool` and `source ∈ {postgres, cache_hit, cache_miss_max_tier}` (§16.2 line 38; §10 line 114). The §16.5 alert prose reads: "For any gateway replica, the share of `lenny_prestop_cap_selection_total{source="cache_miss_max_tier"}` over total selections exceeds 20% for 10 minutes." Per-replica aggregation is impossible from this metric alone — there is no `service_instance_id` / `pod` / `replica` label on the series, so any PromQL of the form `sum by (service_instance_id) (rate(...))` will return the cluster-wide rate collapsed into a single series. Either the alert fires cluster-wide (masking a single-replica stampede by averaging with healthy replicas) or it simply cannot be evaluated as written.
**Recommendation:** Pick one of: (a) add `service_instance_id` as a label on `lenny_prestop_cap_selection_total` (and any related `lenny_prestop_cache_priming_*` counters) and state in §16.2 that the Prometheus scrape config's `honor_labels: false` preserves it, then rewrite the alert PromQL explicitly as `(sum by (service_instance_id, pool) (rate(...{source="cache_miss_max_tier"}[5m])) / sum by (service_instance_id, pool) (rate(...[5m]))) &gt; 0.2`; or (b) drop the "For any gateway replica" qualifier and make the alert cluster-scoped per `pool`, updating the threshold accordingly (cluster-wide 20% of selections missing cache is a weaker signal than per-replica, so the threshold should drop to ~10%). Option (a) is preferable because a single misbehaving replica with a cold cache is exactly the failure mode this alert is meant to catch.

**Resolution:** Implemented option (a). Added `service_instance_id` as a label on `lenny_prestop_cap_selection_total` in three places: (1) the metric-table entry in spec/16_observability.md §16.2 line 41 now reads "counter labeled by `pool`, `service_instance_id`, `source`: …" and explains that the label is required so the per-replica alert condition can be evaluated (a single replica with a cold cache would otherwise be masked by fleet averaging); (2) the "Observability — preStop tier selection source" paragraph in spec/10_gateway-internals.md §10.1 line 114 now reads "counter, labeled by `pool`, `service_instance_id`, and `source`" and cross-references §16.1.1 for the OTel `service.instance.id` attribute; (3) the `PreStopCapFallbackRateHigh` alert row in spec/16_observability.md §16.5 line 434 now carries an explicit PromQL expression `(sum by (service_instance_id, pool) (increase(lenny_prestop_cap_selection_total{source="cache_miss_max_tier"}[15m])) / sum by (service_instance_id, pool) (increase(lenny_prestop_cap_selection_total[15m]))) > 0.05` with a sentence explaining why per-replica grouping is required (single-replica cold cache is the dominant failure mode). `service_instance_id` is already the canonical OTel attribute in §16.1.1 line 266 for replica-scoped metrics, so no new attribute-table entry was required; the label cardinality is bounded by gateway replica count × pool count × 3 source values and is consistent with existing replica-scoped counters (e.g., `lenny_gateway_sigkill_streams_total`, line 43). Scope was limited to `lenny_prestop_cap_selection_total` and its alert as called out in the finding; the sibling `lenny_prestop_barrier_target_source_total` was not modified because it is out of scope for WPL-003 (its per-replica correlation is expressed in prose only and is not the subject of a per-replica alert). Regression check: no other spec text references the old label set of `lenny_prestop_cap_selection_total`; the cross-reference from `lenny_prestop_barrier_target_source_total` (line 42) to "the same replica" for `lenny_prestop_cap_selection_total{source="cache_miss_max_tier"}` is now actually expressible via `service_instance_id` matching, which is an improvement rather than a regression.

### WPL-004. Unschedulable-node branch missing from `task_cleanup` state diagram [Medium] — Fixed

**Section:** spec/06_warm-pod-model.md §6.3 (state diagram lines 130-135 vs. "Host-node schedulability precondition" paragraph)

The schedulability-precondition paragraph (line 160) specifies: "If the host node is unschedulable, the pod transitions to `draining` instead [of `sdk_connecting` or `idle`]." The state diagram at lines 130-135, however, enumerates only three `task_cleanup → draining` transitions (maxTasksPerPod exhausted, maxPodUptimeSeconds exhausted, scrub fails with `onCleanupFailure: drain`). There is no "host node unschedulable" arrow, so readers implementing the state machine directly from the diagram will not emit the transition, and linters/tests that compare the diagram against the prose will flag the prose as orphaned. This also makes it ambiguous whether the unschedulable branch uses `drain_reason = "node_unschedulable"` or reuses an existing reason for metric/audit purposes.
**Recommendation:** Add a fourth `task_cleanup ──→ draining` arrow to the state diagram explicitly for the unschedulable-node case, e.g.: `task_cleanup ──→ draining (host node is unschedulable — see 'Host-node schedulability precondition'; drain_reason = "node_unschedulable")`. Confirm in §16.2 that `lenny_pod_drain_total` includes a `reason="node_unschedulable"` label value, and cross-reference the new `drain_reason` in the §6.5 draining table. Also mirror the change into the scrub_warning branch fix from WPL-001 so both cleanup paths converge on the same unschedulable-node transition.

**Resolution:** Applied a targeted subset of the recommendation. Added two explicit arrows to the `task_cleanup` state enumeration in spec/06_warm-pod-model.md §6.2 (the Task-mode state transitions block): (1) `task_cleanup ──→ draining` for the preConnect + scrub-succeeds + host-unschedulable case and (2) `task_cleanup ──→ draining [scrub_warning]` for the preConnect + scrub-warning + host-unschedulable case. Both arrows carry the full guard conditions (`maxTasksPerPod not reached`, `maxPodUptimeSeconds not reached`, `maxScrubFailures not reached` where applicable) and cross-reference the "Host-node schedulability precondition" paragraph at §6.2 line 181 (which already authoritatively describes both branches identically). This makes the explicit diagram enumeration match the paragraph's prose, closing the readability gap where state-machine implementers reading only the arrow list would have missed the transition. The previous parenthetical "if the host node is unschedulable at this point, the pod transitions to `draining` instead" inside the `task_cleanup ──→ sdk_connecting [scrub_warning]` arrow was removed because the new explicit arrows now carry that information; the sibling `task_cleanup ──→ sdk_connecting` arrow was not similarly annotated so no text was lost there. The recommendation's additional asks were intentionally NOT implemented because they are out of scope for a diagram-vs-prose consistency fix: (a) `lenny_pod_drain_total` does not exist in the spec — the retirement metric is `lenny_task_pod_retirement_total` (spec/16_observability.md §16.2 line 11 and spec/05_runtime-registry-and-pool-model.md §5.2 line 453) with enum labels `{task_count_limit, uptime_limit, scrub_failure_limit}`; adding a new `node_unschedulable` reason would be an observability-contract change not motivated by this finding. (b) There is no §6.5 draining table in the spec (the §6 subsections are §6.1 through §6.4 only), so the cross-reference has no target. (c) The scrub_warning branch from WPL-001 was already fixed in iter3 — the precondition paragraph at line 181 explicitly states "The rule applies identically to the scrub-success and scrub-warning preConnect edges", so there was no asymmetry to mirror. Regression check: the "Host-node schedulability precondition" paragraph at line 181 still refers to the `task_cleanup → sdk_connecting` and `task_cleanup → sdk_connecting [scrub_warning]` transitions as the ones where the precondition applies, which remains accurate because those are the schedulable-path transitions the precondition is on; the new unschedulable-path arrows are the fallback when the precondition fails, consistent with the paragraph's "transitions to `draining` instead" wording. No other spec section references the specific arrow list in the diagram, so no downstream cross-references required updating.

---

## 16. CRD Semantics & Controller Ownership

### CRD-012. Admin API endpoint table missing `PUT` and `DELETE` credential endpoints referenced by RBAC live-probe and CLI [Medium] — FIXED

**Section:** 15 (§15.2 Admin Endpoints, around lines 702–770), cross-refs 4.9 line 1182, 15.1 line 878, 24.5 lines 88–89

The iter3 CRD-009 fix extends the admin-time RBAC live-probe to cover `PUT /v1/admin/credential-pools/{name}/credentials/{credId}` (update-credential). The new `CREDENTIAL_SECRET_RBAC_MISSING` error row in §15.1 at line 878 and the CLI row at §24.5 line 88 both reference this `PUT`, and §24.5 line 89 additionally references `DELETE /v1/admin/credential-pools/{name}/credentials/{credId}` (remove-credential). Neither endpoint appears in the canonical admin-endpoint table at §15.2 (lines 702–770). The table lists pool-level `POST`/`GET`/`PUT`/`DELETE` on `/v1/admin/credential-pools/{name}`, plus `POST` on `/v1/admin/credential-pools/{name}/credentials` (add-credential) at line 770 and the `.../revoke` / `.../re-enable` actions, but no per-credential `PUT` or `DELETE`. Readers of the admin API surface cannot discover these operations; API clients built from the §15 table alone will be unable to exercise the RBAC live-probe on update (nor remove a credential without revoking), even though §4.9 declares both as required code paths. This is a partial-fix gap against iter3 CRD-009: the live-probe is wired into an endpoint the admin API doesn't document.
**Recommendation:** Add two rows to the §15.2 admin endpoint table after line 770:
- `PUT /v1/admin/credential-pools/{name}/credentials/{credId}` — "Update a credential in the pool (body may change `secretRef`). Requires `If-Match`. When `secretRef` changes, the handler performs the admin-time RBAC live-probe ([§4.9](04_system-components.md#49-credential-leasing-service)); fails with `400 CREDENTIAL_SECRET_RBAC_MISSING` if the Token Service ServiceAccount lacks `get` on the new Secret."
- `DELETE /v1/admin/credential-pools/{name}/credentials/{credId}` — "Remove a credential from a pool. Active leases backed by the credential are rotated via the standard fallback path."

**Resolution:** Added two rows to the §15.2 admin endpoint table (immediately after the existing `POST /v1/admin/credential-pools/{name}/credentials` row) documenting `PUT /v1/admin/credential-pools/{name}/credentials/{credId}` (update-credential with `If-Match` and live-probe on `secretRef` change, returning `422 CREDENTIAL_SECRET_RBAC_MISSING`) and `DELETE /v1/admin/credential-pools/{name}/credentials/{credId}` (remove-credential with standard fallback rotation of active leases). Used `422` rather than the recommendation's `400` to match the canonical status code in §15.1 line 981 for `CREDENTIAL_SECRET_RBAC_MISSING`, keeping the table self-consistent. Both rows cross-link to §4.9 (credential-leasing service) and §24.5 (CLI mapping).

### CRD-013. `fault_driven_rate_limited` rotation-trigger value still undefined, leaves AUTH_EXPIRED / PROVIDER_UNAVAILABLE attack paths unbounded [Medium]

**Status:** Fixed (iter4)

**Section:** 4.7 line 795; 4.9 Fallback Flow lines 1352–1380 and Proactive Lease Renewal line 1396; 16.1 line 49; 16.5 line 380

iter3 CRD-008 is carried forward (not marked fixed in iter3 summary) and is the substantive half of the iter2 CRD-005 ceiling mechanism. The 300 s revocation-triggered rotation ceiling at §4.7 line 795 still keys on `rotationTrigger ∈ {emergency_revocation, fault_driven_rate_limited}`, but §4.9 Fallback Flow (lines 1357–1368) classifies fault rotations by `error_type ∈ {RATE_LIMITED, AUTH_EXPIRED, PROVIDER_UNAVAILABLE}` and no `rotationTrigger` enum value named `fault_driven_rate_limited` is defined anywhere else — including the only canonical appearance of `rotationTrigger` at §4.9.2 (line 1686, `proactive_renewal`). There is still no `fault_auth_expired` or `fault_provider_unavailable` trigger. Under current text, a compromised runtime that suppresses `llm_request_completed` during an `AUTH_EXPIRED`- or `PROVIDER_UNAVAILABLE`-triggered rotation is NOT subject to the 300 s ceiling (because its trigger doesn't match the set), yet the threat model explicitly calls these out as identical paths ("a compromised or buggy runtime that failed to emit `llm_request_completed`"). This is the same security ambiguity CRD-008 flagged in iter3; it remains unresolved and gives the exact adversary carve-out iter2 CRD-005 was meant to close.
**Recommendation:** Rewrite the ceiling rule at line 795 to key on the **complement** of `proactive_renewal`: "When `rotationTrigger ≠ proactive_renewal` (i.e., any revocation-initiated, fault-initiated, or operator-initiated rotation), the in-flight gate is capped at 300 seconds." Then explicitly enumerate the `rotationTrigger` enum in §4.9 alongside `proactive_renewal`: `emergency_revocation`, `fault_rate_limited`, `fault_auth_expired`, `fault_provider_unavailable`, `user_credential_rotated`, `user_credential_revoked`. Update the `lenny_credential_rotation_inflight_ceiling_hit_total` label description (§16.1 line 49) and the `OutstandingInflightAtRotationCeiling` alert expression (§16.5 line 380) to reflect the same enum. This is the option CRD-008 flagged as "stronger — any fault-driven or operator-initiated rotation should cap the in-flight gate."

**Fix applied:** Rewrote the §4.7 "Revocation-triggered rotation ceiling" rule to key on the complement of `proactive_renewal`: the 300 s in-flight gate cap now applies to every rotation whose `rotationTrigger ≠ proactive_renewal`, closing the AUTH_EXPIRED / PROVIDER_UNAVAILABLE / user-revocation carve-out. Added a normative "`rotationTrigger` enum" definition to §4.9 (immediately after the Fallback Flow, before Proactive Lease Renewal) enumerating all seven values — `proactive_renewal`, `fault_rate_limited`, `fault_auth_expired`, `fault_provider_unavailable`, `emergency_revocation`, `user_credential_rotated`, `user_credential_revoked` — each with its emitter and cause, plus an explicit statement that only `proactive_renewal` carries the unbounded wait. Replaced the dangling `fault_driven_rate_limited` label reference in the §16.1 `lenny_credential_rotation_inflight_ceiling_hit_total` description and the §16.5 `OutstandingInflightAtRotationCeiling` alert row with the full "≠ `proactive_renewal`" enumeration, both linking back to the §4.9 enum anchor. No runtime-contract change: the adapter's 300 s ceiling behavior and the `lenny_credential_rotation_inflight_ceiling_hit_total{trigger}` label cardinality are unchanged; the edit is a prose/enum-definition reconciliation that brings §4.7, §4.9, §16.1, and §16.5 onto a single vocabulary.

### CRD-014. Revocation-triggered rotation ceiling still emits no audit event for forensic reconstruction [Medium]

**Status:** Fixed

**Section:** 4.7 line 795; 4.9.2 Credential Audit Events lines 1677–1689

iter3 CRD-010 is carried forward (not marked fixed in iter3 summary). The ceiling hit at §4.7 line 795 records a Prometheus counter (`lenny_credential_rotation_inflight_ceiling_hit_total`) and fires a warning alert (`OutstandingInflightAtRotationCeiling`). Both are volatile: counters reset on replica restart, and alerts are delivery-best-effort. No durable audit event is written to `EventStore`. §4.9.2 line 1673 explicitly states "All credential lifecycle events are written to the `EventStore`," yet the ceiling-hit event — which the spec itself classifies as a compromise indicator ("a compromised or buggy runtime that failed to emit `llm_request_completed`") — is absent from the table at lines 1678–1689. A forensic investigation after a suspected runtime compromise has no way to correlate which specific session, lease, pod, or credential experienced the forced rotation. SIEM streaming under §11.7 also cannot cover it because it is not an audit event. The `CredentialCompromised` critical alert fires separately on `lenny_credential_revoked_with_active_leases`, but that metric clears once the rotation finally completes — leaving no record of the specific ceiling event.
**Recommendation:** Add a `credential.rotation_forced` (or `credential.rotation_ceiling_hit`) row to §4.9.2 at line 1686-ish, with fields: `tenant_id`, `session_id`, `lease_id`, `pool_id`, `credential_id`, `rotation_trigger` (one of the enum values per CRD-013), `outstanding_inflight_count` (the in-flight counter value at ceiling), `elapsed_seconds`. Emit it at the same code point where the counter increments. Mark it SIEM-streamable under §11.7 because it is a tier-1 compromise-indicator signal.

**Resolution:** Added a new `credential.rotation_ceiling_hit` row to the §4.9.2 Credential Audit Events table with the exact field set recommended: `tenant_id`, `session_id`, `lease_id`, `pool_id`, `credential_id`, `rotation_trigger` (enumerated to the six non-renewal `rotationTrigger` values from CRD-013's enum: `fault_rate_limited`, `fault_auth_expired`, `fault_provider_unavailable`, `emergency_revocation`, `user_credential_rotated`, `user_credential_revoked`), `outstanding_inflight_count` (uint32), and `elapsed_seconds` (float64, always ≥ 300). The row description explicitly ties the event to the §4.7 ceiling code point, the `lenny_credential_rotation_inflight_ceiling_hit_total` counter, and the `OutstandingInflightAtRotationCeiling` alert, and marks the event as a tier-1 compromise-indicator SIEM-streamable signal (inheriting §4.9.2's blanket SIEM-streaming contract via §11.7). Updated the §4.7 Revocation-triggered rotation ceiling paragraph (line 822) to state that the adapter emits this durable audit event to the `EventStore` at the same code point as the counter increment, noting that the counter is volatile (resets on replica restart) and alerts are delivery-best-effort, so the audit event is the forensic system of record. The event naming follows the existing `lenny_credential_rotation_inflight_ceiling_hit_total` metric name for operator consistency. The event slots between `credential.renewed` (also a lease-lifecycle rotation event) and `credential.fallback_exhausted` (the subsequent fallback-path terminal event) for narrative order.

### CRD-015. `CREDENTIAL_SECRET_RBAC_MISSING` error path still undocumented on admin endpoint table and CLI rows [Low]

**Section:** 15 lines 702–770 (endpoint rows); 24.5 lines 85–89

iter3 CRD-011 is carried forward (not marked fixed in iter3 summary). The error code itself is well-documented at §15.1 line 878, but the admin endpoint rows that actually return it (`POST /v1/admin/credential-pools` line 702, `POST .../credentials` line 770, and — pending CRD-012 — the missing `PUT .../credentials/{credId}`) do not mention the 400 response, the `details.resourceNames`/`details.rbacPatch` payload, or the admin-time RBAC live-probe. Mirror in §24.5: the `add-credential` CLI row at line 87 says "also emits required RBAC patch command" but does not describe the 400 error the server returns, nor that the response payload now includes a ready-to-apply patch. The `update-credential` row at line 88 doesn't mention the live-probe at all. Operators reading the admin-API or CLI docs cannot discover the failure path or the self-remediating response field, defeating the discoverability purpose of iter2 CRD-004's original fix.
**Recommendation:** In the §15.2 endpoint rows for `POST /v1/admin/credential-pools`, `POST .../credentials`, and (per CRD-012) `PUT .../credentials/{credId}`, append: "Fails with `400 CREDENTIAL_SECRET_RBAC_MISSING` if the Token Service ServiceAccount lacks `get` on any referenced Secret; `details.resourceNames` lists the missing Secrets and `details.rbacPatch` contains the RBAC patch command. See [§4.9](04_system-components.md#49-credential-leasing-service) Admin-time RBAC live-probe." In §24.5 lines 87–88, replace the parenthetical with "Live-probes the Token Service ServiceAccount's RBAC on the referenced Secret; on failure, the CLI surfaces `CREDENTIAL_SECRET_RBAC_MISSING` with the required patch command pre-filled."

### CRD-016. Admin-time RBAC live-probe mechanism unspecified (impersonation vs SelfSubjectAccessReview vs direct probe) [Low]

**Section:** 4.9 line 1182

The live-probe paragraph says the handler "MUST issue a live-probe `get` on each referenced Secret using the Token Service's ServiceAccount before committing the write." Three distinct implementation strategies give three different security postures, and the spec does not pick one:
1. The gateway uses Kubernetes **impersonation** (`Impersonate-User`/`Impersonate-Group` headers with the Token Service SA's identity). This requires the gateway's own SA to hold the `impersonate` verb against `serviceaccounts`, which is a cluster-wide privilege and a lateral-movement risk.
2. The gateway performs a **`SelfSubjectAccessReview`** (or `SubjectAccessReview`) against the Token Service SA's identity. This requires only `create subjectaccessreviews.authorization.k8s.io`, does not require impersonation, and returns `allowed: true/false` without actually reading the Secret.
3. The gateway uses the Token Service SA's **own token** (via a TokenRequest mount or a sidecar call to the Token Service that asks it to self-probe) to issue a real `get`.
Each has different failure modes (an SSAR can false-positive on webhook-authorized access; impersonation can false-negative on audit-only RBAC denials; asking the Token Service to self-probe introduces a circular dependency at install time). Without picking one, implementers may ship any of them, including #1 which materially increases the gateway's cluster-RBAC blast radius.
**Recommendation:** Specify the mechanism as `SelfSubjectAccessReview` with `resourceAttributes: {namespace: lenny-system, resource: secrets, verb: get, name: &lt;secretName&gt;}` against the Token Service SA's identity. Document the gateway's required RBAC (`create` on `subjectaccessreviews.authorization.k8s.io`). Explicitly state that the gateway MUST NOT use Kubernetes impersonation headers for this probe, and MUST NOT actually read the Secret (the point is to verify the Token Service can read it, not to read it itself).

### CRD-017. CLI RBAC scope contradicts admin-API and §4.9 tenant-scoping claims [Low]

**Section:** 24.5 lines 85–92; vs 15.2 line 702; vs 4.9 line 1075

§4.9 line 1075 states: "A `tenant-admin` can create, update, and delete credential pools for their own tenant via the admin API." §15.2 line 702 describes `POST /v1/admin/credential-pools` as "tenant-scoped; `tenant-admin` sees own tenant's pools, `platform-admin` sees all." The §4.9 Admin-time RBAC live-probe text (line 1182) even invokes the `tenant-admin` persona to motivate the check. But every credential-pool CLI row in §24.5 lines 85–92 lists `platform-admin` as the sole required role — including `list`, `get`, `add-credential`, `update-credential`, `remove-credential`, `revoke-credential`, `revoke-pool`, and `re-enable`. Either the CLI enforces a stricter role than the API it wraps (in which case `tenant-admin`s cannot use the CLI at all for their own tenant's pools, contradicting the admin-API tenant-scoping) or the CLI documentation is wrong. The live-probe's "tenant-admin who lacks rights to patch RBAC" workflow is only coherent if tenant-admin can actually drive the pool-creation path.
**Recommendation:** Align the CLI rows with the admin-API scoping. For `list`, `get`, `add-credential`, `update-credential`, `remove-credential`, and re-enable paths that are tenant-scoped per §4.9/§15, change the "Required role" column to `platform-admin` or `tenant-admin` (scoped to own tenant). Keep `revoke-credential` / `revoke-pool` at `platform-admin` if emergency revocation is intentionally platform-only (if so, document that choice); otherwise align those too. If the intent is actually to restrict these commands to `platform-admin`, update §4.9 line 1075 and §15.2 line 702 to match.

### CRD-018. Credential audit events do not distinguish `rotationTrigger`, making compromise-correlation queries require cross-join with metrics [Low]

**Section:** 4.9.2 line 1686

The only audit-event row that carries a `rotation_trigger` field is `credential.renewed` (line 1686), and it is hard-coded to `proactive_renewal`. The other two rotation-capable events — `credential.leased` (line 1683, emitted on session start including the post-fallback assignment) and `credential.rotated` (line 1681, user-initiated rotation via `PUT /v1/credentials/{credential_ref}`) — have no `rotation_trigger` field. There is no audit event at all for adapter-level fault-driven rotation (the common path where the runtime reports `RATE_LIMITED`/`AUTH_EXPIRED` and the fallback chain picks a new credential). Investigators reconstructing "what caused this lease to rotate" must join `credential.leased` against the `lenny_credential_rotations` counter series, which lacks session-level labels per the §16.1.1 high-cardinality rule. Combined with CRD-014 (no ceiling-hit audit event) and CRD-013 (undefined fault-rotation triggers), the effect is that only proactive renewal leaves an audit trail; every other rotation is forensically silent.
**Recommendation:** Add a `credential.rotated_fallback` audit event emitted by the gateway on each fault-driven rotation (step 4 of the Fallback Flow), with fields: `tenant_id`, `session_id`, `pool_id`, `old_credential_id`, `new_credential_id`, `rotation_trigger` (per CRD-013 enum), `error_type` (`RATE_LIMITED` | `AUTH_EXPIRED` | `PROVIDER_UNAVAILABLE`), `rotation_count`, `delivery_mode`. Add a `rotation_trigger` field to the existing `credential.leased` event when `source: pool` and the lease is a post-rotation re-assignment (distinguishing the initial assignment from the replacement). Together with CRD-014's `credential.rotation_forced`, this gives a complete per-session rotation trail.

---

## 17. Workspace Plan & Content Handling

### CNT-007. `gitClone` SSH URL contract unresolved after CNT-005 host-agnostic generalization [High]

**Section:** 14 (line 91 `gitClone` row; line 93 `gitClone.auth` paragraph), 26.2 (line 202)

Iter3 CNT-005 generalized `auth.leaseScope` to `vcs.&lt;provider&gt;.{read|write}` and introduced host-based pool resolution via `hostPatterns`. The fix is correct for HTTPS URLs but leaves a gap for the other URL shape the schema still explicitly permits. The `url` cell in §14's `sources[]` catalogue still reads "HTTPS or SSH Git URL" (line 91), yet:

1. **SSH URL host parsing is undefined.** The post-fix `gitClone.auth` paragraph says "the gateway parses the URL's host, compares it against `hostPatterns`". SCP-style SSH URLs (`git@github.com:owner/repo.git`) are not RFC 3986 URIs and are rejected by `net/url.Parse`; the standard `ssh://git@host/owner/repo.git` form is parseable but uses a different port/scheme path than HTTPS. The spec does not state which form(s) the gateway accepts, how it extracts the host, or whether SCP-style is rewritten to `ssh://` first.
2. **No SSH key credential delivery is specified.** §4.9's `github` provider secret shape (`04_system-components.md:1145`) lists HTTPS-App credentials only (installation access token). There is no `sshPrivateKey` entry in the Secret-shape table for any VCS provider, no `~/.ssh/id_*` mount path in §6.4, no `known_hosts` provisioning, and no ssh-agent forwarding model. The in-pod "credential helper that calls the gateway's token endpoint" in §14 line 93 and §26.2 line 202 is HTTPS-specific; `git` over SSH does not consult credential helpers.
3. **`GIT_CLONE_AUTH_UNSUPPORTED_HOST` does not describe the SSH-parse-failure case.** §15.1 line 942 says "the URL's host does not match any VCS credential pool's `hostPatterns`". A parseable SSH URL whose host does match a registered GitHub pool would pass the check but then fail at clone time because no SSH credential exists in the pool. The failure mode and error code for this path are not documented.

A client writing a legal-per-schema plan `{"type":"gitClone","url":"git@github.com:me/private-repo.git","ref":"main","auth":{"mode":"credential-lease","leaseScope":"vcs.github.read"}}` has no defined behavior.

**Recommendation:** Either (a) narrow `url` in §14 line 91 to "HTTPS Git URL" in v1 and move SSH URL support to §21 post-V1, updating §26.2 line 202 to match; or (b) keep SSH in scope and extend the spec to cover: accepted SSH URL forms (explicit `ssh://` only vs. SCP-style), host-extraction algorithm for both forms, an additional Secret key shape (`sshPrivateKey` + optional `knownHostsEntry`) on VCS pools, the in-pod mount path, and an explicit statement that the credential-helper flow applies to HTTPS only while SSH uses a mounted key. Option (a) is simpler for v1 and consistent with the "v1 ships `github` as the only built-in provider" scope.

**Status: Fixed.** Adopted recommendation (a). Changes:
- `spec/14_workspace-plan-schema.md` §14 line 91 `gitClone` row: `url` narrowed from "HTTPS or SSH Git URL" to "HTTPS Git URL — scheme MUST be `https`; see §14 `gitClone.url` notes".
- `spec/14_workspace-plan-schema.md` §14: added a new `gitClone.url restrictions` paragraph (immediately before `gitClone.auth`) specifying: HTTPS-only for v1, `^https://` JSON Schema pattern, rejection as `400 WORKSPACE_PLAN_INVALID`, host extraction via `net/url` authority component, and explicit deferral of SSH forms (both `ssh://` and SCP-style) to §21.9.
- `spec/14_workspace-plan-schema.md` §14 `gitClone.auth` paragraph updated to reference the HTTPS-only restriction when describing host extraction, and adds an explicit statement that the in-pod credential-helper flow is HTTPS-specific (git over SSH does not consult credential helpers — one reason SSH is deferred).
- `spec/26_reference-runtime-catalog.md` §26.2 line 119 (shared credential-lease paragraph; the authoritative site for the VCS-scope description — line 202 is the `runtimeOptions` paragraph, unrelated): updated to state `gitClone.url` is HTTPS-only in v1, cross-link §14 and §21.9, and specify the in-pod helper as an HTTPS credential helper.
- `spec/04_system-components.md` §4.9 `hostPatterns` paragraph: clarified that v1 host extraction operates only on parsed HTTPS URLs, with a cross-link to §21.9 for SSH deferral.
- `spec/21_planned-post-v1.md`: added new §21.9 enumerating the post-v1 work required for SSH support — accepted URL forms + extraction algorithm, `sshPrivateKey` + `knownHostsEntry` Secret key shapes, in-pod `~/.ssh/` mount + rotation model, a new `auth.mode` (e.g., `"ssh-key-mount"`), and the post-v1 error-code path. Includes today's workaround (sidecar-mirror to an HTTPS endpoint).

Non-HTTPS schemes (including both SSH forms and `git://`) are rejected at session creation via the existing `WORKSPACE_PLAN_INVALID` error code's JSON Schema pattern check; no new error code is required. The existing `GIT_CLONE_AUTH_UNSUPPORTED_HOST` and `GIT_CLONE_AUTH_HOST_AMBIGUOUS` codes (§15.1) now unambiguously cover only HTTPS URL cases and remain correct as-written.

### CNT-009. `uploadArchive.stripComponents` semantics undefined for `zip` format [Medium]

**Section:** 14 (line 89 `uploadArchive` row), 7.4 (Upload Safety extraction rules)

The `uploadArchive` catalogue row declares `stripComponents` (integer, default 0) as an optional field alongside the `format` enum `tar | tar.gz | zip`. `stripComponents` is a tar-idiomatic operation (`--strip-components=N` on GNU tar) that drops N leading path segments from each archive member. The operation has no standard analog in the zip format — zip archives store flat path strings without the tar member-header indirection, and popular extractors (`unzip`, Go's `archive/zip`) do not expose an equivalent flag natively.

Consequences:

1. Whether `stripComponents: 2` against a `zip` archive is (a) rejected at validation with a new error code, (b) applied by string-splitting each entry's path before write, or (c) silently ignored is not stated in §14 or §7.4.
2. If implementation choice (b) is selected, the behavior around `zip` entries that have fewer than N leading segments (e.g., entry `a.txt` with `stripComponents: 2`) is undefined — drop the entry, place at root, or fail the extraction?
3. The `format_error` label on `lenny_upload_extraction_aborted_total` (§16 line 20) does not distinguish a `stripComponents`-related rejection from a corrupt-archive rejection.

**Recommendation:** Add a sentence to the `uploadArchive` row in §14 line 89 (or the §7.4 archive extraction rules) stating the canonical behavior: recommended is "For `zip` archives, `stripComponents` is applied by splitting each entry's path on `/` and removing the first N segments before extraction; entries with fewer than N leading segments are skipped and emit a `workspace_plan_strip_components_skip` warning event." Alternatively, restrict `stripComponents` to tar variants only and reject non-zero values for `format: zip` with a new permanent error code, documented in §15.1.

**Status: Fixed.** Added a new `uploadArchive.stripComponents` field note to §14 (directly after `setupCommands[].timeoutSeconds` in the "Field notes" block) defining format-independent canonical semantics: the gateway splits each entry path on `/`, drops N leading segments, and re-joins under `pathPrefix`. The same algorithm applies to `tar`, `tar.gz`, and `zip` (noting zip stores flat `/`-separated paths per APPNOTE.TXT §4.4.17 so the split semantics are well-defined). Entries with fewer than N segments or an empty post-strip path are **skipped** (not fatal) and emit a new `workspace_plan_strip_components_skip` warning event per entry (fields: `sourceIndex`, `entryPath`, `segmentCount`, `stripComponents`). Added a cross-referencing `stripComponents` application bullet to §7.4's archive-extraction rules (between `Symlink handling` and `Atomic cleanup`) that points to the §14 field note for the canonical algorithm. No new error code is introduced: the chosen semantic is skip-with-warning, so the `lenny_upload_extraction_aborted_total` `format_error` ambiguity raised in consequence #3 is moot (stripComponents does not produce an abort). No asymmetry between formats remains.

### CNT-011. Published WorkspacePlan JSON Schema's per-variant `additionalProperties` policy unspecified [Medium]

**Section:** 14 (lines 83-91 `sources[]` catalogue; 14.1 lines 306, 325-327 Published JSON Schema / Unknown source.type)

§14 describes the `sources[]` entry shape as a discriminated union: each concrete type (`inlineFile`, `uploadFile`, `uploadArchive`, `mkdir`, `gitClone`) has a distinct set of required and optional fields. The "Unknown `source.type` handling" paragraph (line 327) specifies open-string `type` extensibility. But the published JSON Schema contract — `https://schemas.lenny.dev/workspaceplan/v1.json`, the document the gateway validates against at session creation and the one clients will use for local validation — does not state:

1. Whether each variant's shape uses `additionalProperties: false` (strict — reject unknown keys on a known `type`) or `additionalProperties: true` (permissive — silently drop them on the server side).
2. Whether the outer `sources[]` item schema uses a JSON Schema 2020-12 `oneOf` with per-variant `if`/`then`, or a looser shape that permits the union of all fields regardless of `type`.
3. What the gateway's response is when a client submits `{"type":"inlineFile","path":"x","content":"y","url":"https://..."}` (a mixed shape that combines `inlineFile` required fields with a `gitClone` field). Is this `400 WORKSPACE_PLAN_INVALID`, or silently stripped?

This matters because CNT-002's iter2 fix explicitly declared §14 the schema of record. The runtime-options schemas in the same file (§14 lines 165, 181, 198, etc.) all set `"additionalProperties": false`, so there's asymmetry in strictness between the `runtimeOptions` schema and the `sources[]` item schema.

**Recommendation:** Add a paragraph to §14.1 (after the "Unknown `source.type` handling" paragraph) stating: "Within a known `source.type` variant, the published JSON Schema sets `additionalProperties: false`; unknown fields on a known type are rejected at session creation with `400 WORKSPACE_PLAN_INVALID`. The open-string extensibility applies only to the `type` discriminator itself, not to the per-variant field set. Clients extending the schema with vendor fields MUST register a new `type` value rather than attaching extra fields to a built-in type." This closes the contract ambiguity and aligns the `sources[]` strictness with the existing `runtimeOptions` precedent.

**Status: Fixed.** Added a new "Per-variant field strictness" paragraph to §14.1 directly after the existing "Unknown `source.type` handling" paragraph. The new paragraph: (1) states that within a known `source.type` variant (`inlineFile`, `uploadFile`, `uploadArchive`, `mkdir`, `gitClone`) the published JSON Schema sets `additionalProperties: false` and unknown fields are rejected at session creation with `400 WORKSPACE_PLAN_INVALID` plus a JSON Schema validation report; (2) scopes open-string extensibility to the `type` discriminator only, explicitly excluding the per-variant field set; (3) cites the `runtimeOptions` schemas in the same file as the strictness precedent now aligned with; (4) resolves the iter4 concrete example (the `inlineFile` + `gitClone.url` mixed shape is rejected under this rule); (5) requires clients extending the schema with vendor fields to register a new `type` value rather than attaching extras to a built-in type; and (6) describes the `sources[]` item schema construction (JSON Schema 2020-12 `oneOf` over known variants with `if`/`then` branching on `type`, plus an open fallthrough branch for unknown-`type` entries, which the consumer skips per the adjacent paragraph). No new error codes were introduced; `400 WORKSPACE_PLAN_INVALID` was already the idiomatic gateway rejection for inner-plan schema failures (§14.1 line 313). This is a spec clarification only; no runtime contract change.

Now let me order by severity and finalize.

Severity order: HIGH, MEDIUM, MEDIUM, LOW, LOW

- CNT-007 [HIGH] SSH URL gitClone
- CNT-011 [MEDIUM] additionalProperties policy
- CNT-009 [MEDIUM] stripComponents for zip
- CNT-010 [LOW] warning events not in catalog
- CNT-008 [LOW] CNT-007 (prior) partial in §14

Let me renumber to keep them sequential starting from CNT-007 in severity order:

- CNT-007 [HIGH] SSH
- CNT-008 [MEDIUM] additionalProperties
- CNT-009 [MEDIUM] stripComponents
- CNT-010 [LOW] warning events
- CNT-011 [LOW] §14 session-binding

Here are my final findings:

### CNT-008. Iter3 CNT-007 fix partially applied: §14 `gitClone.auth` paragraph still lacks session-binding sentence [Low]

**Section:** 14 (line 93 `gitClone.auth` paragraph), 26.2 (line 202)

Iter3 CNT-007 recommended "Add one sentence to §14's `gitClone.auth` paragraph" binding the credential lease to the originating session for audit traceability. The iter3 summary marks CNT-007 without a "Status:" line, and a cross-check shows the binding sentence ("The lease is bound to the originating session ID for audit traceability.") now appears in §26.2 line 202 but was not added to the §14 paragraph (line 93) as originally requested. §14 is the canonical WorkspacePlan schema document, and operators/clients reading the `gitClone.auth` field contract will not see the binding unless they follow the §4.9 cross-reference. §26.2 is a runtime-catalog downstream section.

**Recommendation:** Append one sentence to §14 line 93 after the credential-helper sentence: "The lease issued for a `gitClone` source is bound to the originating `session_id` and recorded in the `credential.leased` audit event ([§4.9](04_system-components.md#49-credential-leasing-service)) for traceability." This matches the §26.2 wording and closes the iter3 CNT-007 gap in the authoritative section.

### CNT-010. `workspace_plan_unknown_source_type` and `workspace_plan_path_collision` warning events are not in the §16.6 Operational Events Catalog [Low]

**Section:** 14 (lines 327, 329), 16.6 (Operational Events Catalog enumeration, lines 552-555)

§14 defines two warning event types emitted by the gateway during `WorkspacePlan` materialization: `workspace_plan_unknown_source_type` (line 327, fields: `schemaVersion`, `unknownType`) and `workspace_plan_path_collision` (line 329, fields: `path`, `winningSourceIndex`, `losingSourceIndex`). §16.6 is declared the "canonical enumeration" of operational event types; neither `workspace_plan_*` identifier appears in the `Gateway-emitted` list at line 552 nor elsewhere in §16.6. This creates three gaps:

1. The `dev.lenny.&lt;short_name&gt;` CloudEvents `type` attribute for these events is undefined — §16.6 says the CloudEvents `type` is `dev.lenny.&lt;short_name&gt;` where `&lt;short_name&gt;` is the catalog identifier.
2. The agent-operability SSE stream (§25.5) uses §16.6 as the source-of-truth for filterable event types; clients cannot subscribe to workspace-plan warnings.
3. The `unregistered_platform_type` warning discipline from §15.4.1 OutputPart namespacing suggests platform events should be registered; these two are not.

**Recommendation:** Add `workspace_plan_unknown_source_type` and `workspace_plan_path_collision` to the Gateway-emitted list in §16.6 line 552, with a short sentence enumerating their payload fields. Optionally, add a short cross-reference from §14 lines 327/329 back to §16.6.

---

## 18. Build Sequence

### BLD-009. Phase 8 checkpoint/resume does not deploy `lenny-drain-readiness` webhook [High]

**Section:** spec/18_build-sequence.md (Phase 8), spec/17_deployment-topology.md §17.2 entry #11, line 57 preflight enumeration

Iter3 BLD-009 (Low) asked Phase 8 to explicitly deploy `lenny-drain-readiness` (item 11 of the §17.2 admission-plane enumeration). The summary.md block for BLD-009 has no "Status: Fixed" line (contrast with BLD-005/006/007/008 which all carry an explicit Status: Fixed paragraph), and Phase 8 remains the one-liner `| 8 | Checkpoint/resume + artifact seal-and-export | Sessions survive pod failure; artifacts retrievable |`. Severity escalates from Low to HIGH in iter4 because the BLD-005 fix (lines 14) now explicitly defers item 11 to "Phase 8" — so Phase 3.5 promises Phase 8 owns the deployment, yet Phase 8 does not claim ownership. This is a build-sequence internal consistency regression introduced by the BLD-005 fix itself: the deferral statement in Phase 3.5 ("11 (`lenny-drain-readiness`) are deferred to later phases (... Phase 8 ...) where their gated feature lands") creates a normative expectation of Phase 8 deployment that the Phase 8 row does not honor. Functionally, a Phase 8 install cannot succeed against the hard-coded 8-webhook `lenny-preflight` expected set (§17.2 line 57, §17.9 row 477) because `lenny-drain-readiness` is absent until someone explicitly adds it. An AI implementer following Phase 8's one-line description will not deploy the webhook, the preflight fails, and the reason is not discoverable from Phase 8's text.

**Recommendation:** Rewrite Phase 8 to match the BLD-007/008 pattern applied in iter3:

&gt; Phase 8: Checkpoint/resume + artifact seal-and-export. **Admission control enforcement — deploy `lenny-drain-readiness`** — this phase is responsible for first-deploy of item 11 of the [§17.2](17_deployment-topology.md#172-namespace-layout) admission-plane enumeration, which Phase 3.5 explicitly defers here because the webhook gates the MinIO-backed checkpoint pipeline that lands in this phase. The `lenny-drain-readiness` `ValidatingAdmissionWebhook` blocks pod eviction when MinIO cannot accept checkpoint uploads (NET-037 in [§13.2](13_security-model.md#132-network-isolation)), preventing data loss during node drains. Deployment MUST match the uniform HA contract in [§17.2](17_deployment-topology.md#172-namespace-layout): `replicas: 2`, `podDisruptionBudget.minAvailable: 1`, `failurePolicy: Fail`, the `lenny.dev/component: admission-webhook` pod label, and the `DrainReadinessWebhookUnavailable` alert wired per [§16.5](16_observability.md#165-alerting-rules-and-slos). Also: the `lenny-preflight` check and the `admission_webhook_inventory_test.go` integration suite must both recognise the webhook as present once this phase completes.

**Status: Already Fixed.** Verification against the current `spec/18_build-sequence.md` Phase 8 row (line 48) shows Phase 8 has already been rewritten to match the BLD-007/008 pattern: it now carries the "Admission control enforcement — deploy `lenny-drain-readiness`" clause with the item-11 attribution, the Phase-3.5-deferral rationale, cross-references to [§17.2](../../../../spec/17_deployment-topology.md#172-namespace-layout), [Section 12.5](../../../../spec/12_storage-architecture.md#125-artifact-store), and NET-037 in [Section 13.2](../../../../spec/13_security-model.md#132-network-isolation), the uniform HA contract (`replicas: 2`, `podDisruptionBudget.minAvailable: 1`, `failurePolicy: Fail`, `lenny.dev/component: admission-webhook` pod label), the `DrainReadinessWebhookUnavailable` alert wired per [§16.5](../../../../spec/16_observability.md#165-alerting-rules-and-slos), and the `lenny-preflight` + `admission_webhook_inventory_test.go` recognition requirement. The milestone column also reflects the new gate ("pre-drain MinIO health check admission gate enforced"). `spec/17_deployment-topology.md` §17.2 item 11 (line 52), the preflight expected-set enumeration (line 57), §17.9 row 483 ("Drain-readiness webhook"), row 484 ("Admission webhook inventory"), and `spec/16_observability.md` line 447 (`DrainReadinessWebhookUnavailable` alert) are all consistent with Phase 8's ownership claim.

### BLD-011. Phase-aware `lenny-preflight` enumeration still unaddressed; pre-Phase-13 installs fail-closed against hard-coded expected set [High]

**Section:** spec/18_build-sequence.md (Phase 3.5, Phase 5.8, Phase 8, Phase 13); spec/17_deployment-topology.md §17.2 line 57 (`lenny-preflight` expected-set), §17.9 "Checks performed" row 477 (Admission webhook inventory)

Iter3 BLD-005 flagged two concerns: (A) Phase 3.5's deliverable list did not enumerate the admission-plane items, and (B) §17.2's `lenny-preflight` enumeration check hard-codes all 8 webhooks as the expected set, which "actively prevents the build from reaching Phase 5.8 / 8 / 13 because webhooks not yet in the chart will be reported as missing." The iter3 fix for BLD-005 addressed (A) by enumerating the Phase 3.5 slice and naming the deferred items. Concern (B) — "feature-gated on the phase under test — either by tagging each webhook in the chart with a `phase` marker or by running `lenny-preflight` in a 'phase-aware' mode" — is not addressed anywhere in the spec.

Concrete consequences, strictly reading the current build sequence:

1. Phase 3.5 Helm install renders 4 webhooks (items 5, 7, 9, 12) and 4 policy manifests. `lenny-preflight` (§17.2 line 57; §17.9 row 477) expects 8 webhooks: items 5, 6, 7, 8, 9, 10, 11, 12. Four are missing at Phase 3.5 (items 6, 8, 10, 11). Preflight is `fail-closed`: *"any missing webhook causes the Job to fail with 'expected ValidatingWebhookConfiguration \&lt;name\&gt; not found; chart-rendered webhook is missing — re-render with the current chart or run helm template and diff against the expected set'"*. Build cannot progress past Phase 3.5 without either (a) disabling preflight (`preflight.enabled: false`, which the same row warns against), (b) manually deploying webhooks that have no enforcement path yet, or (c) forking the preflight expected-set per-phase — none of which are specified.
2. Same problem re-occurs at Phase 5.8 (items 8, 10, 11 still missing) and Phase 8 (items 8, 10 still missing).
3. `tests/integration/admission_webhook_inventory_test.go` (§17.2 line 59) "verifies that every webhook enumerated in the list above is rendered by `helm template` against the default values". This test must pass for Phase 3.5 to merge, but the Phase 3.5 chart does not render items 6/8/10/11.

**Recommendation:** Add a new "Phase-aware preflight and chart inventory" note to §18 (after the admission-policy-deferral paragraph in Phase 3.5) specifying exactly how Phase 3.5 builds reconcile with the §17.2 + §17.9 preflight expected-set. Two mechanically-equivalent options:

- **Option A — per-webhook chart feature flags.** Each of `lenny-direct-mode-isolation`, `lenny-data-residency-validator`, `lenny-t4-node-isolation`, `lenny-drain-readiness` renders only when a Helm feature-flag is `true` (e.g., `features.llmProxy`, `features.compliance`, `features.drainReadiness`). The §17.9 preflight expected-set is derived from the same feature flags so "expected" and "rendered" always match. Phase 3.5's build sets no feature flags; Phase 5.8 flips `features.llmProxy=true`; Phase 8 flips `features.drainReadiness=true`; Phase 13 flips `features.compliance=true`.
- **Option B — phase marker on each webhook manifest.** Each `templates/admission-policies/*.yaml` carries a `metadata.annotations[lenny.dev/deploy-phase]` annotation, and `lenny-preflight` reads a `preflight.phase` Helm value (integer) and only expects webhooks with annotation value ≤ `preflight.phase`.

Either mechanism must be named in §18 Phase 3.5's admission-plane paragraph (as an explicit pointer to the control that makes Phase 3.5/5.8/8/13 installs succeed) and echoed in §17.9 row 477's "Admission webhook inventory" check body. The `admission_webhook_inventory_test.go` companion in §17.2 line 59 must also be parameterised accordingly. Without one of these, BLD-005's fix is cosmetic — Phase 3.5 reads well but cannot install.

**Status: Fixed.** Adopted Option A (per-webhook chart feature flags). In §17.2, replaced the hard-coded expected-set language with a new "Feature-gated chart inventory (single source of truth)" paragraph defining three Helm values — `features.llmProxy` (gates `lenny-direct-mode-isolation`, first-deploy Phase 5.8), `features.drainReadiness` (gates `lenny-drain-readiness`, first-deploy Phase 8), and `features.compliance` (gates `lenny-data-residency-validator` + `lenny-t4-node-isolation`, first-deploy Phase 13) — with default `false`. The `lenny-preflight` expected set is composed by union from these flags so a Phase 3.5 install expects exactly the five baseline entries (the four always-rendered webhooks `lenny-label-immutability`/`lenny-sandboxclaim-guard`/`lenny-pool-config-validator` plus the `lenny-crd-conversion` conversion webhook) and passes; Phase 5.8 flips `features.llmProxy=true` (adds one entry); Phase 8 flips `features.drainReadiness=true` (adds one); Phase 13 flips `features.compliance=true` (adds two). The `admission_webhook_inventory_test.go` companion suite is now parameterised over the same three flags with four phase-aligned test cases. The §17.9 "Admission webhook inventory" row (row 484 post-edit) was rewritten to compose its expected set from the same flags, and the adjacent "T4 node isolation webhook" / "Drain-readiness webhook" rows were gated on `features.compliance` / `features.drainReadiness` so they skip cleanly when the webhook isn't rendered. The §18 Phase 3.5 deferred-items paragraph gained a "Phase-aware preflight and chart inventory" note pointing at the §17.2/§17.9 mechanism, and Phases 5.8/8/13 each now explicitly call out the feature-flag flip as part of their webhook first-deploy action. Per-webhook unavailability `PrometheusRule` templates are gated on the same flag as the corresponding webhook template, so pre-Phase-13 installs do not produce spurious paging. Sections modified: spec/17_deployment-topology.md §17.2 (preflight paragraph + new Feature-gated-inventory paragraph + parameterised inventory-test paragraph), spec/17_deployment-topology.md §17.9 rows 494/495/496 (T4/drain-readiness/inventory checks); spec/18_build-sequence.md Phase 3.5 (added phase-aware note), Phase 5.8/8/13 (added feature-flag flip in each first-deploy clause).

### BLD-010. Phase 1 wire-contract artifacts still do not include Shared Adapter Types / SessionEventKind registry [Low]

**Section:** spec/18_build-sequence.md line 8 (Phase 1); spec/15_external-api-surface.md "Shared Adapter Types" (line 178), "SessionEventKind closed enum registry" (lines 310–323, 462+)

Iter3 BLD-010 (Low) asked for Phase 1 to include a normative Go-type artifact (e.g., `pkg/adapter/shared.go`) that codifies the Shared Adapter Types and the `SessionEventKind` closed enum added in iter2 PRT-005/006/007. The iter3 summary.md BLD-010 block has no "Status: Fixed" line, and the Phase 1 wire-contract list (line 8) remains unchanged: `schemas/lenny-adapter.proto`, `schemas/lenny-adapter-jsonl.schema.json`, `schemas/outputpart.schema.json`, `schemas/workspaceplan-v1.json` — no Go shared-types artifact. Phase 5's `ExternalAdapterRegistry` deliverable (line 24) also doesn't name the Shared Adapter Types commit as a prerequisite, so neither of the two iter3 fix options (extend Phase 1; or pin in Phase 5) is implemented. The `SessionEventKind` closed-enum contract at §15.2 line 462 — *"The `SessionEventKind` enum above is closed — the gateway will never dispatch a kind value not listed below, and third-party adapters MUST NOT rely on receiving unknown kinds through `OutboundChannel.Send`. The registry below is authoritative for the gateway outbound dispatcher ... additions require a `SessionEvent` schema version bump"* — defines a normative dependency that every Phase 5 external adapter relies on, but the source-of-truth artifact is not committed in Phase 1 when other wire contracts are frozen.

**Recommendation:** Extend Phase 1's "**Machine-readable wire-contract artifacts committed to the repository**" list with one of:

- `pkg/adapter/shared.go` — Go package containing `SessionMetadata`, `AuthorizedRuntime`, `AdapterCapabilities`, `OutboundCapabilitySet`, `SessionEvent`, `PublishedMetadataRef`, and the closed `SessionEventKind` enum, as committed source-of-truth types that Phase 5 `ExternalAdapterRegistry` consumers and Phase 12b `type: mcp` runtimes MUST import;
- or a `schemas/session-event-v1.json` + `schemas/adapter-shared-v1.json` JSON Schema pair with matching Go codegen, paralleling the `workspaceplan-v1.json` pattern already in Phase 1.

Add the same CI gate already used for the other Phase 1 schemas: §15 additions mirror code changes (closed-enum additions bump the schema version and require a `SessionEventKind` row + `AdapterCapabilities.SupportedEventKinds` documentation update).

---

## 19. Failure Modes & Recovery

### FLR-012. Fresh-session (no prior checkpoint) preStop cap is inconsistent between Postgres-healthy and Postgres-unhealthy paths [High]

**Section:** 10.1 (`10_gateway-internals.md:108`, `10_gateway-internals.md:110`, `10_gateway-internals.md:32`)

The preStop Stage 2 tier selection has a correctness asymmetry for sessions that have never successfully checkpointed (a legitimate state for any session created less than one `periodicCheckpointIntervalSeconds` ago — up to 10 minutes at default):

- **Postgres reachable path (line 108):** "If the field is absent (no prior checkpoint), the 30s default applies."
- **Postgres unreachable path (line 110):** "cache miss — … possible when … the cache was primed with NULL … the gateway MUST select the 90s maximum tier (not the 30s default) to avoid truncated checkpoints during correlated infrastructure outages."

The step 0 priming (line 32) only writes the cache "if non-null", so a fresh never-checkpointed session enters Stage 2 with a NULL cache entry. If Postgres is healthy, the SELECT returns NULL and the code applies 30s. If Postgres is unhealthy, cache miss applies 90s. But the session's current workspace size is the same in both cases — the session may have materialized a 512 MB workspace at step 12 (§7.1) and simply not yet hit a periodic checkpoint. In the Postgres-healthy path, a fresh drain on such a session will SIGKILL mid-upload (30s cap against a workspace that requires up to 90s). This is precisely the bug that the 90s cache-miss fallback was designed to prevent — the Postgres-healthy path bypasses that protection because it reads the Postgres NULL rather than treating the read as indeterminate.

Observability aggravation: in the Postgres-healthy path, the `lenny_prestop_cap_selection_total` counter increments with `source: postgres` (per line 114) — the operator has no signal that a session without a known workspace size was subject to the 30s cap. The `cache_miss_max_tier` label only fires when Postgres *fails*, so the analogous risk under Postgres-healthy operation is invisible.

Practical consequence: every rolling update or node drain within the first `periodicCheckpointIntervalSeconds` window of any large-workspace session (default 600s) will interrupt that session with SIGKILL, regardless of Postgres availability. At Tier 3 steady state (10,000 concurrent sessions, sessions created at ~17/s per the burst math), the fleet continuously contains fresh sessions — drains during that window are routine.

**Recommendation:** Treat a Postgres-returned NULL identically to a cache miss: select the 90s maximum tier when `last_checkpoint_workspace_bytes` is absent (whether from Postgres or from cache). Alternatively, populate `last_checkpoint_workspace_bytes` eagerly at `FinalizeWorkspace` (step 12 of §7.1) with the staged workspace size, so fresh sessions always have a known-upper-bound workspace size before they can be drained. Add an explicit `source: postgres_null` label (or `cache_primed_null`) to the `lenny_prestop_cap_selection_total` metric so operators can observe this path and set a companion alert. Update line 108 to state the fallback tier for `NULL` explicitly; the current wording ("the 30s default applies") is the 30s-default bug the FLR-004/FLR-008 chain was trying to eliminate.

**Status:** Fixed

**Fix applied:** Adopted the primary recommendation (symmetric NULL handling, not the alternative `FinalizeWorkspace` pre-population — see below for rationale). Rewrote §10.1 line 108 so that an absent `last_checkpoint_workspace_bytes` on the Postgres-healthy path selects the 90s maximum tier rather than the 30s default, with an explicit cross-reference to §7.1 step 12 (`FinalizeWorkspace`) explaining that a fresh session can legitimately have a `workspaceSizeLimitBytes`-scale workspace without yet having its first periodic checkpoint — the same rationale that motivated the Postgres-unreachable cache-miss 90s fallback at line 110. The clamp-against-stream-drain constraint is preserved. Added `postgres_null` as a fourth value of the `source` label on `lenny_prestop_cap_selection_total` (§10.1 line 114 and the §16.1 metric row at line 41), distinct from `cache_miss_max_tier` so operators can tell "Postgres healthy, fresh session" from "Postgres unreachable, cold cache" in the per-source breakdown. Updated the cold-start paragraph at §10.1 line 112 to document both sources as distinct diagnostic signals even though they trigger the same 90s conservative tier. Rewrote the `PreStopCapFallbackRateHigh` alert expression in §16.5 (line 434) to fire on the combined `postgres_null + cache_miss_max_tier` share exceeding 5% per-replica over 15 minutes, because both sources signal operationally that a replica's preStop is depending on the 90s conservative cap; the per-source breakdown in the metric allows operators to disambiguate cause (dual-store unavailability vs. fresh-session churn) when the alert fires. Chose the symmetric-NULL fix over `FinalizeWorkspace` pre-population because (a) the latter conflates two distinct semantics on `last_checkpoint_workspace_bytes` (last-successful-checkpoint vs. last-known-workspace), (b) it would not handle mid-session workspace growth before the first periodic checkpoint (a 100 MB initial workspace that grows to 500 MB still needs the 90s cap), (c) it adds a session-creation-path write that the current schema does not require, and (d) the 90s symmetric-fallback path already delivers the correctness outcome without any schema or §7.1 semantic change. Sections modified: §10.1 (lines 108, 112, 114); §16.1 (line 41); §16.5 (line 434).

---

### FLR-013. PDB `maxUnavailable: 1` serializes node drains at Tier 3 without a documented bound [Medium]

**Section:** 17.1 (`17_deployment-topology.md:7`), 17.8.2 (`17_deployment-topology.md:872-873`)

The iter3 PRF-005 fix set the gateway PDB to `maxUnavailable: 1` uniformly, solving the FLR-006 drain-blocking problem and the FLR-007 percentage-expressibility problem. The replacement has a new operability side effect that the spec does not acknowledge.

At Tier 3 (`minReplicas: 5`, `maxReplicas: 30`) with `terminationGracePeriodSeconds: 300` and topology spread across zones, a node hosting multiple gateway pods is a realistic scenario — Kubernetes topology-spread is probabilistic, and at ≥10 replicas per zone a worker node can host 2–3 gateway pods. `kubectl drain` on such a node must evict them serially because the PDB allows only one eviction at a time, and each eviction consumes up to `terminationGracePeriodSeconds + preStop stage 2 budget` (worst case 300s) before the next is admitted.

Serialized drain times:
- 2 gateway pods on a node: up to 2 × 300s = **10 minutes** per node
- 3 gateway pods on a node: up to 3 × 300s = **15 minutes** per node
- Full-cluster rolling update across 30 replicas: up to 30 × 300s = **150 minutes** (2.5 hours)

No row in §17.8.2 captures this, and the `Gateway scale-down time (max→min replicas)` row (17:874) accounts only for scale-down, not drain. Node drains during cluster-autoscaler consolidation, spot interruption simulations, or security patching are routine at Tier 3 and will exceed common drain-timeout defaults (`kubectl drain --timeout` is 0/forever by default, but IaC tooling often sets 5–10 minutes). Operators who bound drain time in their tooling will force-delete pods past the PDB, converting the voluntary disruption into an involuntary disruption that bypasses all checkpoint guarantees — reintroducing the failure mode the PDB was designed to prevent, via a different route.

The PRF-005 recommendation text in iter3/summary.md:290 explicitly called for "a hard ceiling of concurrent gateway evictions independent of replica count," and the fix delivered that. What is missing is the operational guidance that operators must also bound their drain tooling to expect serialized, per-pod-grace-period times at Tier 3, AND a metric/alert to detect when drain serialization is pushing aggregate drain time past operator-set bounds.

**Recommendation:** Add a "Tier 3 node-drain expectations" note to §17.8.2 adjacent to the `Gateway scale-down time` row stating `worst-case single-node drain time = pods_on_node × terminationGracePeriodSeconds` with an explicit Tier-3 figure (15 minutes for 3 co-located pods). Add an anti-affinity recommendation (or required PodAntiAffinity with `topologyKey: kubernetes.io/hostname`) so at Tier 3 no node hosts more than one gateway pod; this caps single-node drain time at one grace period. Add a `lenny_gateway_drain_queue_depth` gauge (number of gateway pods currently in a node-drain queue) and a `GatewayDrainSerializationHigh` alert firing when drain queue depth &gt; 2 for more than 15 minutes, so operators detect cases where the PDB bound is causing serialized drain storms.

**Status:** Fixed (iter4)

**Fix applied:** Adopted the prose-addition core of the recommendation; deferred the new metric/alert addition as out of scope for a doc-gap finding (`lenny_pdb_blocked_evictions_total` already provides the observable signal needed to detect PDB-serialized drains, so a new `lenny_gateway_drain_queue_depth` gauge would duplicate the existing observability surface — see §17.8.2 note wording below). Added a new "Tier 3 node-drain expectations" blockquote in §17.8.2 immediately after the existing "Scale-down rate is PDB-bound" blockquote (and before "Native translator footprint") containing: (1) the closed-form formula `worst_case_single_node_drain_time = pods_on_node × terminationGracePeriodSeconds`; (2) explicit Tier 3 figures of 10 min (2 co-located pods) and 15 min (3 co-located pods) at `terminationGracePeriodSeconds: 300`; (3) an operator MUST directive to size `kubectl drain --timeout` and IaC wrappers above this bound so force-delete fallback does not bypass preStop checkpoint guarantees; (4) a SHOULD recommendation for Tier 3 to add a `podAntiAffinity` rule at `topologyKey: kubernetes.io/hostname`, with guidance on when to use `requiredDuringSchedulingIgnoredDuringExecution` (cluster has ≥ `maxReplicas` schedulable nodes) vs. `preferredDuringSchedulingIgnoredDuringExecution` (to avoid blocking scale-up under transient node shortage); (5) a MAY for Tier 1/2 to omit the rule since 240s × 3 co-located = 12 min is tolerable at those replica counts; and (6) an explicit cross-reference to `lenny_pdb_blocked_evictions_total` (§16.1) as the signal operators use to detect drain serialization. Also extended the §17.1 Gateway row with a brief "Node-drain consequence" sub-paragraph (parallel to the pre-existing "Scale-down consequence" sub-paragraph) summarizing the formula and pointing to the §17.8.2 note, so the node-drain semantics are discoverable from the PDB definition site. The anti-affinity recommendation aligns with the existing tier-configurable anti-affinity pattern already established at §17.8.2 line 1085 (WPC/PSC `--controller-anti-affinity`). Sections modified: §17.1 (Gateway row, line 7); §17.8.2 (new blockquote between the existing "Scale-down rate is PDB-bound" and "Native translator footprint" notes).

---

### FLR-014. `InboxDrainFailure` alert rule text still not evaluable PromQL [Low]

**Section:** 16.5 (`16_observability.md:432`)

FLR-009 from iter3 remains unresolved. The alert description at 16:432 still reads `"lenny_inbox_drain_failure_total incremented (any non-zero increase over a 5-minute window)"` — prose, not an `expr:` field. The peer alerts immediately adjacent (`DurableInboxRedisUnavailable` at 16:433, `SandboxClaimGuardUnavailable`, etc.) use concrete PromQL expressions with `rate()` or comparisons. Iter3/summary.md FLR-009 flagged this; the iter3 fix apparently did not apply.

**Recommendation:** Replace the prose with `expr: increase(lenny_inbox_drain_failure_total[5m]) &gt; 0` and a `for: 0s` clause (immediate firing — this metric's semantics are "any increment is actionable"). Specify whether the alert fires per-label (`pool`, `session_state`) or aggregated — per-label is operationally more useful since it points at the affected pool.

---

### FLR-015. PgBouncer readiness probe amplifies Postgres failover window — FLR-010 unresolved [Low]

**Section:** 12.3 (`12_storage-architecture.md:45`)

FLR-010 from iter3 remains unresolved. The PgBouncer probe settings at 12:45 are still `periodSeconds: 5, failureThreshold: 2, timeoutSeconds: 3`. A 30s Postgres failover therefore produces a 40–45s window during which PgBouncer pods are removed from the Service even though they are healthy and will reconnect once Postgres recovers. This pushes `dualStoreUnavailableMaxSeconds` (60s) and `coordinatorHoldTimeoutSeconds` (120s) closer to breach during any overlapping Redis degradation — the combined envelope (17:833 "Effective degraded window") of 120s already assumes Postgres recovers within the 60s dual-store max, which is tight once the probe lag is added.

**Recommendation:** Widen `failureThreshold: 8` (≈ 40s before NotReady, which matches the `PgBouncerBackendUnreachable` alert's own pickup window) so PgBouncer's own retry logic absorbs Postgres failover without removing readiness, OR decouple the readiness probe from backend reachability and rely solely on the `PgBouncerBackendUnreachable` alert. If the current values are a deliberate accept-as-is, add a "Known limitation" note under 12.3 quantifying the amplification window so operators can factor it into their Tier-3 RTO budgets — specifically noting that `dualStoreUnavailableMaxSeconds` effectively accounts for ~75–80s of actual Postgres unavailability (probe lag + failover RTO) rather than the nominal 30s RTO.

---

### FLR-016. `Minimum healthy gateway replicas (alert)` table row has no backing alert rule [Low]

**Section:** 17.8.2 (`17_deployment-topology.md:875`), 16.5

§17.8.2 table row 875 ("Minimum healthy gateway replicas (alert): 2 / 3 / 5 per tier") implies an alert rule that enforces the per-tier minimum. The `lenny_gateway_replica_count` metric is defined in 16.1 (16:29), but 16.5 contains no alert using this metric — no `GatewayReplicasBelowMinimum`, `GatewayAvailabilityLow`, or equivalent. Operators wiring in per-tier thresholds must author the rule themselves, despite the table suggesting it is supplied.

This is pertinent to the FLR-013 scenario above: when PDB-serialized drains push the fleet below the minimum healthy count, operators have no signal — `kube_deployment_status_replicas_available` is the only upstream proxy, and the per-tier threshold isn't encoded in any shipped rule.

**Recommendation:** Add a `GatewayReplicasBelowMinimum` warning alert to 16.5 firing when `lenny_gateway_replica_count &lt; {tier_minimum}` for &gt; 2 minutes. Alternatively, cross-reference the existing `kube-state-metrics` alert pattern (e.g., `KubeDeploymentReplicasMismatch`) in §17.8.2 with explicit Prometheus expression templates per tier. Either way, close the loop so the table row corresponds to a concrete alert artifact in the shipped `PrometheusRule`.

---

### FLR-017. `Gateway preStop drain timeout` table row (17:872) is not referenced by any preStop logic [Low]

**Section:** 17.8.2 (`17_deployment-topology.md:872`)

The per-tier row `Gateway preStop drain timeout: 60s / 60s / 120s` appears in the capacity-tier reference table but is not referenced by any parameter in §10.1 preStop logic. The preStop stages use `terminationGracePeriodSeconds` (240s/240s/300s), `max_tiered_checkpoint_cap` (up to 90s), `checkpointBarrierAckTimeoutSeconds` (90s default), and the clamp leaving at least 30s for stage 3. 60s/60s/120s does not match any of these. The budget formula `max_tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30` yields 210s at defaults, leaving 30s of slack at Tier 1/2 and 90s at Tier 3 — not 60s or 120s.

Possibilities: (a) the row is stale from a prior design iteration and should be removed; (b) the row describes a distinct parameter (e.g., a maximum for Stage 3 polling) that is not named in §10.1; (c) the row describes the floor for `terminationGracePeriodSeconds - (max_cap + BarrierAck)` but with the wrong value (should be 30s at all tiers per the clamp rule, not 60/60/120). In any case an operator reading the table cannot determine what this value governs or how to tune it.

**Recommendation:** Either (a) remove the row if obsolete; (b) rename it and cross-reference the specific parameter in §10.1 it describes; or (c) derive the value from the formula (e.g., `terminationGracePeriodSeconds - 210` = 30/30/90 at Tier 1/2/3) and correct it. If the intent is to document the stage-3 headroom, state explicitly "Stage 3 minimum stream-drain budget" and link to §10.1 Stage 3.

---

## 20. Experimentation & Rollout

### EXP-013. `experiment.*` operational events emitted by §10.7 are not registered in §16.6 event catalog [Medium]

**Status:** Fixed — added an "Experiment events (gateway-emitted, operational)" block to §16.6 enumerating the five operational events (`experiment.unknown_variant_from_provider`, `experiment.unknown_external_id`, `experiment.targeting_failed`, `experiment.multi_eligible_skipped`, `experiment.isolation_mismatch`) with inline payload fields and a cross-reference to §10.7; and added a new `experiment.status_changed` bullet to §16.7 with payload fields (`tenant_id`, `experiment_id`, `previous_status`, `new_status`, `actor_sub`, `transition_at`) cross-referencing §10.7 Experiment status transitions. Anchor `10_gateway-internals.md#107-experiment-primitives` verified against `spec/README.md`.

**Section:** 10.7 (lines 750, 752, 756, 773, 775, 1013); 16.6 (lines 548-555); 16.7

§10.7 specifies emission of six `experiment.*` events: `experiment.unknown_variant_from_provider` (line 750), `experiment.unknown_external_id` (line 752), `experiment.targeting_failed` (line 756), `experiment.multi_eligible_skipped` (line 773), `experiment.isolation_mismatch` (line 775 — introduced by iter3 EXP-009 fix), and `experiment.status_changed` (line 1013 — audit event). None appear in the §16.6 Operational Events Catalog nor §16.7 Audit Events list. The §16.6 catalog is declared canonical ("the catalog below is the canonical enumeration"), so consumers subscribing to the operational event stream ([§25.5](agent-operability.md#255-operational-event-stream)) and webhook subscribers have no way to discover, filter on, or schema-validate these events. Agent-operability subscribers get undocumented CloudEvents `type` values like `dev.lenny.experiment.isolation_mismatch` that aren't in the published enumeration.

**Recommendation:** Add a new "Experiment events" bullet under §16.6 "Gateway-emitted" listing all six event names (`experiment.unknown_variant_from_provider`, `experiment.unknown_external_id`, `experiment.targeting_failed`, `experiment.multi_eligible_skipped`, `experiment.isolation_mismatch`, `experiment.status_changed`) with the inline fields documented in §10.7. Classify `experiment.status_changed` as an audit event in §16.7 (it is explicitly written to the audit path per line 1013) and other five as operational events. Cross-reference §10.7 from the new bullet.

### EXP-014. Fail-closed isolation rejection is not validated at experiment-creation time [Medium]

**Status:** Fixed — added an "Admission-time isolation-monotonicity validation" paragraph in §10.7 immediately after the runtime-time fail-closed paragraph (line 853) that specifies `POST/PUT /v1/admin/experiments` compares each variant's pool `sessionIsolationLevel.isolationProfile` against the base runtime's default pool profile and rejects weaker variant pools with `422 CONFIGURATION_CONFLICT` (already in catalog line 998) carrying `details.conflicts[].fields: ["variants[<id>].pool", "baseRuntime"]`; noted the check runs under `?dryRun=true` as well. Updated the §15.1 dryRun table rows for `POST` and `PUT /v1/admin/experiments` (lines 1173–1174) and the narrative "Experiments" bullet (line 1122) to reflect the new check. Added an `lenny_experiment_isolation_rejections_total` counter (labeled `tenant_id`, `experiment_id`, `variant_id`) to the Experiment Targeting metrics table in §16.1 and cross-referenced it from the §10.7 runtime-time paragraph (line 851) so operators can detect rejection-population bias without log scraping. Anchor `16_observability.md#161-metrics` verified against `spec/README.md` (line 99).

**Section:** 10.7 (line 775); 15.1 (dryRun table line 1061)

The iter3 EXP-009 fix correctly rejects session creation at runtime when the variant pool's isolation is weaker than the session's `minIsolationProfile` (line 775, `VARIANT_ISOLATION_UNAVAILABLE`). However, `POST/PUT /v1/admin/experiments` (and the dryRun table at line 1061) does not validate this configuration property. The dryRun row states "Validates definition and variant weights; no capacity check" — it also has no isolation check. Operators can activate an experiment whose variant pool has `isolationProfile: standard` while the base runtime's typical traffic includes sessions with `minIsolationProfile: sandboxed`; they only discover the mismatch when a fraction of the base runtime's traffic starts being rejected with 422s post-activation. For strict-isolation workloads this becomes a silent availability regression.

Additionally, the fail-closed approach creates an **unauditable selection bias**: rejected sessions never produce `EvalResult` rows, so the Results API cannot show operators that a systematic subset of the workload population has been excluded. Combined with EXP-009's control-contamination fix, operators gain control-bucket purity at the cost of an undetectable rejection-population bias.

**Recommendation:** (a) At `POST/PUT /v1/admin/experiments` validation time, compare each variant pool's `isolationProfile` against the base runtime's typical session `minIsolationProfile` distribution (or, at minimum, against the base pool's `sessionIsolationLevel.isolationProfile`). Return `422 CONFIGURATION_CONFLICT` (already in catalog, line 891) with `details.conflicts[].message` describing the monotonicity gap, or introduce a new warning/advisory `details.warnings[]` in the dryRun response for non-blocking guidance. Update line 1061's dryRun table row accordingly. (b) Add a `lenny_experiment_isolation_rejections_total` counter (labeled by `experiment_id`, `variant_id`) emitted alongside the `experiment.isolation_mismatch` event so operators can detect the rejection-population-bias without querying logs.

### EXP-010. Iter3 EXP-005 regression: `INVALID_QUERY_PARAMS` referenced but undefined [Low]

**Section:** 10.7 (line 871); 15.1 (error catalog, lines 862-939)

The iter2 EXP-002 fix introduced a mutual-exclusion rule: `?delegation_depth=0&amp;breakdown_by=delegation_depth` is rejected with `400 INVALID_QUERY_PARAMS` (spec/10_gateway-internals.md line 871). Iter3 EXP-005 flagged this as Low severity because no such code exists in the §15.1 error catalog. Iter3 summary.md does **not** mark EXP-005 as `Status: Fixed`, and grep confirms `INVALID_QUERY_PARAMS` is still not in the spec's error catalog (only `VALIDATION_ERROR` and family-specific 400 codes exist). The error-code consistency test at spec/15_external-api-surface.md line 1230 also does not list this code. A client encountering the rejection cannot look up the code, and the error-consistency integration test will not cover it.

**Recommendation:** Either (a) change line 871 to `400 VALIDATION_ERROR with details.fields[0].rule: "breakdown_collision"` (reuse the existing catalog entry, consistent with `cursor_expired` precedent at line 1116), or (b) add `INVALID_QUERY_PARAMS` as a new 400 row to the §15.1 error catalog and append it to the error-consistency test list at line 1230.

### EXP-011. Iter3 EXP-007 regression: variant count still unbounded [Low]

**Section:** 10.7 (lines 608-624, 862); 15.1 (dryRun table line 1061); 22 (explicit non-decisions)

Iter3 EXP-007 (re-surfacing iter2 EXP-003) flagged that no `maxVariantsPerExperiment` / `TOO_MANY_VARIANTS` control exists. Iter3 summary.md shows no `Status: Fixed` for EXP-007. Grep confirms neither token appears anywhere in the spec. Line 862 still claims "bounded by operator configuration (typically 2–5)" without a config key, and the dryRun table at line 1061 validates only `Σ variant_weights`. An adversarial or buggy `POST /v1/admin/experiments` request with 500 weight-0.001 variants would create 500 `SandboxWarmPool` CRDs, make the bucketing walk at line 680 O(500), and make the paused sticky-cache `DEL ...sticky:*` scan unbounded.

**Recommendation:** As previously proposed — add an explicit `maxVariantsPerExperiment` tenant-config key (default 10) enforced at `POST/PUT /v1/admin/experiments` validation time (and echoed in the dryRun row at line 1061) with a new `TOO_MANY_VARIANTS` 422 code in §15.1. Update line 862's "typically 2–5" language to cite the concrete default.

### EXP-012. Iter3 EXP-008 regression: sticky cache `paused → active` wording still contradictory [Low]

**Section:** 10.7 (line 1015)

Iter3 EXP-008 (re-surfacing iter2 EXP-004) flagged the self-contradictory sentence. Iter3 summary.md does not mark EXP-008 as `Status: Fixed`. Line 1015 still reads verbatim: *"On `paused → active` re-activation, no flush is required — the existing cached assignment remains valid."* This follows the preceding clause that flushed all entries via `DEL t:{tenant_id}:exp:{experiment_id}:sticky:*` on the `active → paused` transition. Because paused experiments are not evaluated by the `ExperimentRouter` (line 773: first-match rule walks only active experiments), no cache entries can be populated during the paused window. The "existing cached assignment remains valid" sentence refers to a state that cannot exist. Correctness under `paused → active` relies on HMAC-SHA256 determinism (line 694), not cache persistence. Also, sessions created during the paused window have `experimentContext: null`; they are not retroactively enrolled on re-activation — not stated anywhere.

**Recommendation:** Rewrite line 1015's second half to: *"On `paused → active` re-activation, no re-seeding is required: percentage-mode assignment is deterministic (HMAC-SHA256 of `assignment_key + experiment_id`, line 674), so the first post-re-activation session for a given user recomputes the same variant as before the pause. The cache is lazily repopulated on demand. For `mode: external` experiments, re-evaluation is delegated to the OpenFeature provider per session."* Also add a sentence: "Sessions created during the paused window have `experimentContext: null` and are not retroactively enrolled on re-activation, regardless of `sticky` mode."

### EXP-015. `BreakdownResponse` example shows per-bucket dimension-set divergence without explanation [Low]

**Section:** 10.7 (lines 930-1005)

The iter3 EXP-006 fix specifies per-bucket aggregation semantics: "a dimension's `count` within a bucket counts only rows in that bucket where the dimension is non-null" (line 933). The JSON example at lines 936-1002 demonstrates this by including `coherence` and `safety` in `control.breakdowns[bucket_value=0]` but omitting `relevance` — while the default (flat) response example at lines 876-927 includes all three dimensions for the same `control` variant. A reader comparing the two examples would reasonably assume the spec is inconsistent. Additionally, the spec does not state whether the bucket's `dimensions` object is **present but empty** vs. **absent** when a bucket contains rows whose `scores` are all null for every dimension; the example `control.breakdowns[bucket_value=1].scorers["llm-judge"]` has a `dimensions` object while `treatment.breakdowns[*].scorers["llm-judge"]` does not. Clients cannot reliably distinguish "no dimensional scores in this bucket" from "this bucket has null dimensions" without a convention.

**Recommendation:** Add a sentence after line 933: "A bucket may have a different dimension key set from other buckets of the same variant because per-bucket dimension keys are the union of non-null `scores` keys seen in that bucket's rows only. In the example below, `control.breakdowns[bucket_value=0]` omits `relevance` because no row in that bucket had a non-null `relevance` score, even though the variant's default (flat) response includes `relevance`." Also state whether `dimensions` is omitted or returned as `{}` when empty — recommend "omitted when no dimension keys exist for any row in the bucket; present (possibly empty) otherwise" and make the example consistent.

### EXP-016. Session rejected with `VARIANT_ISOLATION_UNAVAILABLE` has no retry-with-fallback pathway [Low]

**Section:** 10.7 (line 775); 15.1 (line 935)

The iter3 EXP-009 fix says: "Not retryable as-is. The caller must either relax the session's `minIsolationProfile`, or the operator must re-provision the variant pool." This is correct for a session whose caller actually needs the stricter isolation — but many callers default `minIsolationProfile` from tenant config and aren't aware they're triggering the check. The response gives no machine-discoverable hint that the caller could, for example, opt into a non-experimented path (e.g., with a `?skipExperiment=true` request flag). Without such a pathway, experiment rollouts that introduce a weaker-isolation variant pool become hard blockers for any strict-isolation session, even if the session's owner would rather run base-runtime-control than be rejected.

**Recommendation:** Either (a) document that the session's caller can retry with `experimentOptOut: true` (or similar) on session creation to bypass experiment routing and run the base runtime unconditionally — this preserves the "no control contamination" invariant because opt-out sessions are tagged `experimentContext: null`, not `variant_id: "control"`. Add the flag to the session-create request body in §15.1 and reference it in `VARIANT_ISOLATION_UNAVAILABLE.details.remediation`. Or (b) explicitly document in §10.7 line 775 and §15.1 line 935 that no per-session opt-out exists by design — callers cannot run non-experimented sessions while an experiment is active with an incompatible variant pool, and this is the intended policy. Either commitment is fine; the current spec leaves the question undefined.

---

## 21. Documentation & Cross-Reference Integrity

### DOC-013. New intra-file anchor `#1781-operational-defaults--quick-reference` does not resolve in `10_gateway-internals.md` [Medium]

**Section:** `10_gateway-internals.md:128`

Introduced by the iter3 CPS-004 fix ("partial-manifest chunked object model", commit 08db10b). The line reads `...partialChunkSizeBytes, default: 16 MiB, see [§17.8.1](#1781-operational-defaults--quick-reference)...`. This is an **intra-file** anchor (no filename prefix) but `10_gateway-internals.md` contains no §17.8.1 heading — the heading `17.8.1 Operational Defaults — Quick Reference` lives at `17_deployment-topology.md:805` (anchor `#1781-operational-defaults--quick-reference` is correct, but the file prefix is missing). On GitHub, this link resolves to the top of the current file rather than the intended section. This is the **same class of bug as iter3 DOC-008** — an intra-file anchor that points across files. DOC-008 was closed in this commit chain, yet this exact class of regression was simultaneously introduced by the CPS-004 edit. The companion line 732 in `17_deployment-topology.md` references the same anchor correctly (as intra-file) because §17.8.1 is in that file.
**Recommendation:** Change `[§17.8.1](#1781-operational-defaults--quick-reference)` → `[§17.8.1](17_deployment-topology.md#1781-operational-defaults--quick-reference)`.

### DOC-014. Cross-file anchor `25_agent-operability.md#253-endpoint-split-between-gateway-and-lenny-ops` does not exist [Medium] — Fixed

**Section:** `13_security-model.md:208`

Introduced by the iter3 NET-051 fix ("`lenny-ops` absent from `lenny-system` NetworkPolicy allow-lists", commit 38e2969). The Gateway row in the §13.2 allow-list table ends `... — see [Section 25.3](25_agent-operability.md#253-endpoint-split-between-gateway-and-lenny-ops) — NET-051`. The actual heading at `25_agent-operability.md:399` is `## 25.3 Gateway-Side Ops Endpoints` whose anchor is `#253-gateway-side-ops-endpoints`. There is no heading titled "Endpoint Split Between Gateway and Lenny-Ops" anywhere in the spec — the fragment was fabricated (likely from a draft-only section title). The iter3 NET-051 status note explicitly claims "Cross-reference integrity: `[Section 25.1](25_agent-operability.md#251-overview)`, `[Section 25.3](25_agent-operability.md#253-endpoint-split-between-gateway-and-lenny-ops)`, and `[Section 25.4](25_agent-operability.md#254-the-lenny-ops-service)` all resolve." — this claim is incorrect; only `#254-the-lenny-ops-service` resolves.
**Recommendation:** Change `(25_agent-operability.md#253-endpoint-split-between-gateway-and-lenny-ops)` → `(25_agent-operability.md#253-gateway-side-ops-endpoints)`.

**Resolution:** Updated the broken anchor in `spec/13_security-model.md` (Gateway row of the §13.2 NetworkPolicy allow-list table) from `(25_agent-operability.md#253-endpoint-split-between-gateway-and-lenny-ops)` to `(25_agent-operability.md#253-gateway-side-ops-endpoints)`. Verified the target heading `## 25.3 Gateway-Side Ops Endpoints` exists at `spec/25_agent-operability.md:407`, producing the canonical anchor `#253-gateway-side-ops-endpoints`. A post-edit grep across `spec/` confirms zero remaining references to the fabricated `#253-endpoint-split-between-gateway-and-lenny-ops` fragment.

### DOC-015. Cross-file anchor `25_agent-operability.md#251-overview` does not exist [Medium] — Fixed

**Section:** `13_security-model.md:218`

Introduced by the same iter3 NET-051 fix as DOC-014. The "`lenny-ops` counterparty rules (NET-051)" blockquote reads `... (which it always is — `lenny-ops` is mandatory per [Section 25.1](25_agent-operability.md#251-overview))`. The actual heading at `25_agent-operability.md:7` is `## 25.1 Design Philosophy and Agent Model` whose anchor is `#251-design-philosophy-and-agent-model`. There is no "Overview" heading in §25. The NET-051 fix note's self-verification ("Cross-reference integrity: `[Section 25.1](25_agent-operability.md#251-overview)` ... resolves") was not actually checked before commit.
**Recommendation:** Either change the anchor to `#251-design-philosophy-and-agent-model`, or — since the prose is citing §25.1 purely as the "lenny-ops is mandatory" source — retarget to a more specific anchor like `#254-the-lenny-ops-service` where the mandatoriness is normatively stated.

**Resolution:** Retargeted the broken citation in `spec/13_security-model.md` (the "`lenny-ops` counterparty rules (NET-051)" blockquote) from `[Section 25.1](25_agent-operability.md#251-overview)` to `[Section 25.4](25_agent-operability.md#254-the-lenny-ops-service)`, following the recommendation's preferred option: the cited fact ("`lenny-ops` is mandatory") is normatively stated in §25.4 (heading `## 25.4 The `lenny-ops` Service` at `spec/25_agent-operability.md:776`, anchor `#254-the-lenny-ops-service`), which is both a valid anchor and a more specific citation than §25.1's "Design Philosophy and Agent Model" heading. A post-edit grep across `spec/` confirms zero remaining references to the broken `#251-overview` fragment.

### DOC-016. Intra-file anchor `#165-alerts` does not resolve in `16_observability.md` [Medium] [Fixed]

**Section:** `16_observability.md:243`

Introduced by the iter3 OBS-018 fix ("deployment_tier label documented in §16.1.1", commit 08db10b). The line reads `... the `Tier3GCPressureHigh` alert in [§16.5](#165-alerts) and the OpenSLO export in [§16.10](#1610-openslo-export)...`. The §16.5 heading at line 340 is `### 16.5 Alerting Rules and SLOs` whose correct anchor is `#165-alerting-rules-and-slos`. There is no `#165-alerts` heading in the file. The same paragraph gets `[§16.10](#1610-openslo-export)` correct (the next heading exists). The same paragraph further above (line 226) correctly references `[§16.5](#165-alerting-rules-and-slos)`, demonstrating this is a localized typo, not an anchor-rename issue. This is a regression of the exact class DOC-005 (iter2 `#166-service-level-objectives` → `#165-alerting-rules-and-slos` rewrite) was designed to close.
**Recommendation:** Change `[§16.5](#165-alerts)` → `[§16.5](#165-alerting-rules-and-slos)`.

**Resolution:** Fixed. The broken anchor occurrence was located on line 278 of `spec/16_observability.md` (the "Other domain labels" paragraph describing the `deployment_tier` label's usage in the `Tier3GCPressureHigh` alert). Changed `[§16.5](#165-alerts)` → `[§16.5](#165-alerting-rules-and-slos)`. Verified via grep that the target anchor `#165-alerting-rules-and-slos` exists (heading `### 16.5 Alerting Rules and SLOs` at line 375) and that no remaining `#165-alerts` references exist anywhere under `spec/`.

### DOC-017. Headings "16.7 Section 25 Audit Events" / "16.8 Section 25 Metrics" still confusing [Low]

**Section:** (unspecified)

**Files:** `16_observability.md:556, 577`, `README.md:105–106`

Re-file of iter3 DOC-011, iter2 DOC-006, iter1 DOC-002. Titles juxtapose two section numbers (`16.7` + "Section 25"), which reads as a structural error on first pass. The README TOC mirrors the same string. This finding has now survived **three iterations** — it is either blocked on a policy decision or lost in the backlog. The iter3 fix commit inventory does not list DOC-011 as closed.
**Recommendation:** Same as DOC-011 — rename to `### 16.7 Agent Operability Audit Events` / `### 16.8 Agent Operability Metrics`, with an opening sentence in each body stating "Introduced by §25 (Agent Operability)". Update `README.md` lines 105–106 and any internal references. This also opens a path for cleaner anchor naming if the heading ever ships.

### DOC-018. README TOC still omits first subsection of three sections [Low]

**Section:** (unspecified)

**File:** `spec/README.md`

Re-file of iter3 DOC-012, iter2 DOC-007. Confirmed unfixed (three iterations):
- `4.0 Agent Operability Additions` (`04_system-components.md:3`) — README line 14–23 lists 4.1 through 4.9 but not 4.0.
- `24.0 Packaging and Installation` (`24_lenny-ctl-command-reference.md:19`) — README line 127–147 lists 24.1 through 24.20 but not 24.0.
- `18.1 Build Artifacts Introduced by Section 25` (`18_build-sequence.md:75`) — README line 119 lists only `18. Build Sequence` with zero subsection entries, unlike every other multi-subsection chapter.

The §24.0 anchor (`#240-packaging-and-installation`) is referenced in running prose at `17_deployment-topology.md:328` and resolves — the omission is TOC-only.
**Recommendation:** Same as DOC-012 — insert three TOC lines under their respective parent entries in the README TOC.

---

## 22. Messaging & Events

### MSG-011. Durable inbox per-message TTL applies unconditionally and will expire messages during active long sessions [High]

**Status:** Fixed

**Section:** spec/07_session-lifecycle.md §7.2 lines 266, 268

Line 266 states each durable-inbox entry carries `per_message_ttl` "(default: `maxResumeWindowSeconds`, or 900s)". Line 268 says "A background goroutine on the coordinating replica trims expired messages from the list head using `LRANGE` + `LTRIM` every 30 seconds." No state-gating is specified — the trimmer runs while the session is `running` as well.

This is incorrect behavior:
- `maxResumeWindowSeconds` (default 900s) is a resume-window timer applicable only to `resume_pending` — it is not a sensible TTL for messages sitting in the inbox of an actively-running session.
- A legitimate long `await_children` block (up to `maxSessionAgeSeconds`, default 7200s) will see correctly-buffered messages disappear after 900s even though the coordinator is alive and the session is healthy.
- Compounding the problem: §15.4.1 line 1564 restricts `dlq_ttl_expired` reason to "Pre-terminal DLQ TTL elapsed while the target session remained in a recovering state." Durable-inbox per-message-TTL expiry has no canonical `message_expired.reason`, meaning senders lose observability of the expiry entirely.

**Recommendation:** Clarify that `per_message_ttl` applies **only** while the session is in a recovering state (`resume_pending` or `awaiting_client_action`), matching §7.2 line 279 which already scopes the `EXPIRE` on the inbox key to the `resume_pending` transition. During `running`, the TTL trimmer must be a no-op or the per-message TTL must be extended to match `maxSessionAgeSeconds`. Add a third `message_expired.reason` enum value (e.g., `durable_inbox_ttl_expired`) or explicitly state that per-message TTL expiry uses `dlq_ttl_expired` and expand the §15.4.1 line 1564 definition accordingly.

**Resolution:** §7.2 durable-mode table (lines 292, 294) updated to: (a) label `per_message_ttl` as a "recovery-window cleanup bound, not a live-session delivery deadline"; (b) state-gate the trimmer — explicit no-op while session is in `running` state (including `input_required`, `await_children`, in-flight tool calls), activating only during recovering states (`resume_pending`, `awaiting_client_action`), matching the `EXPIRE` scope on line 305; (c) specify that TTL expiry emits a `message_expired` event with new `reason: "durable_inbox_ttl_expired"`. §7.2 line 347 and §15.4.1 lines 1714–1721 updated to expand the canonical `message_expired.reason` enum from two to three values, adding `durable_inbox_ttl_expired` alongside `dlq_ttl_expired` and `target_terminated`.

### MSG-012. MSG-007 iter3 fix omitted LTRIM from §12.4 Redis command allowlist [Medium] — Fixed

**Section:** spec/12_storage-architecture.md §12.4 line 186; spec/07_session-lifecycle.md §7.2 line 268

Iter3 MSG-007 recommended adding "TTL trim via `LTRIM`" to the §12.4 inbox-key row. The iter3 fix updated line 186 to `RPUSH`/`LPOP`/`LREM`/`LRANGE` but **did not include `LTRIM`**. §7.2 line 268 still says expired messages are trimmed "using `LRANGE` + `LTRIM` every 30 seconds." The original defect MSG-007 identified (a reader building a Redis command allowlist or ACL from §12.4 alone will miss `LTRIM` and the background trimmer will fail silently) is therefore only partially fixed.

**Recommendation:** Update §12.4 line 186 to: "Created when `messaging.durableInbox: true`; enqueue via `RPUSH`, dequeue/ack via `LREM`, recovery via `LRANGE`, overflow drop via `LPOP`, TTL trim via `LRANGE`+`LTRIM` (FIFO — see [§7.2](07_session-lifecycle.md#72-interactive-session-model))". This closes the allowlist gap that MSG-007 flagged.

### MSG-013. `delivery_receipt.reason` field has no canonical enum [Medium]

**Section:** spec/15_external-api-surface.md §15.4.1 lines 1548–1558

§15.4.1 canonicalizes a two-value enum for `message_expired.reason` (line 1560 table) but does not do the same for the `delivery_receipt.reason` field at line 1552. Instead, the receipt's reason values are only listed inline in prose at line 1558: `inbox_overflow`, `dlq_overflow`, `inbox_unavailable`, `scope_denied`. Multiple values the spec references elsewhere are missing from that prose:

- No reason is listed for `rate_limited` status (§7.2 line 319 says `rate_limited` = "inbound rate cap exceeded" but does §15 expect `maxPerMinute`, `maxInboundPerMinute`, or `maxPerSession` as the reason?)
- No reason is listed for `expired` status on the receipt itself (§15.4.1 says `expired` = "DLQ TTL elapsed before delivery" — is there a `reason` at all, or is the status self-describing?)
- `target_terminated` applies as a receipt reason when a message is sent to a terminal session via MSG-006's unresolved TARGET_TERMINAL-as-receipt path, but is not listed in line 1558.
- `error` status has `inbox_unavailable` and `scope_denied`, but other error reasons (e.g., `coordinator_unreachable`) are not enumerated even though they are reachable per §7.2 line 302's coordinator-forwarding fallback.

Without a canonical enum, receivers cannot build exhaustive handlers and implementers will invent ad-hoc strings.

**Recommendation:** Add a canonical `delivery_receipt.reason` enum table at §15.4.1 next to line 1558, one row per `(status, reason)` pair, sourced by searching all §7.2 and §15.4.1 callsites. At minimum: `inbox_overflow`, `dlq_overflow` (for `dropped`); `maxPerMinute`, `maxInboundPerMinute`, `maxPerSession` (for `rate_limited`); `inbox_unavailable`, `scope_denied`, `coordinator_unreachable` (for `error`). Mirror the closed-enum discipline established for `message_expired.reason` in the same section.

**STATUS: Fixed** — Added a canonical `delivery_receipt.reason` enum table to `spec/15_external-api-surface.md` §15.4.1, immediately after the existing inline prose at line 1712 and before the adjacent `message_expired.reason` table. The new table enumerates four `(status, reason)` pairs that the spec genuinely uses today: `dropped`/`inbox_overflow`, `dropped`/`dlq_overflow`, `error`/`inbox_unavailable`, `error`/`scope_denied`. Trailing clarification states that `reason` is omitted for `delivered` and `queued` (self-describing statuses), and that v1 does not define additional reason strings for `expired` or `rate_limited`. Speculative values (`maxPerMinute`, `maxInboundPerMinute`, `maxPerSession`, `target_terminated`, `coordinator_unreachable`) were deliberately **not** added: `target_terminated` is a `message_expired` event reason per §7.2 line 343 (TARGET_TERMINAL is an error response, not a receipt) and `coordinator_unreachable` corresponds to path 7 coordinator-forwarding fallback which yields a `queued` receipt per §7.2 line 328 — neither is a receipt reason today. Rate-limited and expired reason strings are explicitly left undefined for v1 pending a spec update. MSG-016's complementary schema-comment fix (line 1706) is out of scope for this surgical fix and will be addressed separately.

### MSG-014. `message_expired` event schema not canonicalized; only the `reason` enum is defined [Medium] — FIXED

**Resolution:** Added a canonical `message_expired` event schema block in §15.4.1 immediately after the `reason` enum table (new block after line 1733). The block declares the exact field set (`schemaVersion`, `type`, `messageId`, `targetSessionId`, `reason`, `expiredAt`) with per-field descriptions, states unambiguously that the event is delivered on the sender session's event stream (not on the synchronous `delivery_receipt`), and adds a normative closing paragraph: senders MUST NOT infer expiry from any other signal. Reconciled three §7.2 sites that mis-described the transport: (a) the recovering-state dead-letter row (§7.2 line 341) replaced "via the `delivery_receipt` mechanism" with an event-stream reference and an explicit "not a field on the synchronous `delivery_receipt`" clarifier; (b) the inline JSON example (§7.2 line 347) now includes the full canonical field set and references §15.4.1 as the authoritative schema; (c) the §7.3 `awaiting_client_action` DLQ-drain bullet (line 425) replaced "sending `message_expired` delivery receipts" with "emitting a `message_expired` event on each registered sender session's event stream". The existing §7.2 "Inbox drain on terminal transition" paragraph (line 343) already used correct event-stream phrasing and was left unchanged.

**Section:** spec/07_session-lifecycle.md §7.2 line 321; spec/15_external-api-surface.md §15.4.1 line 1560

§15.4.1 line 1560 canonicalizes the `message_expired.reason` enum but does not canonicalize the `message_expired` event **schema**. The only schema example is an inline JSON snippet in §7.2 line 321: `{ "type": "message_expired", "messageId": "msg_abc123", "reason": "dlq_ttl_expired" }`. This minimal example leaves unspecified:
- Whether the event carries a `targetSessionId` (important for senders managing multiple outstanding messages to different targets).
- Whether the event carries a `timestamp` / `expiredAt` field.
- Whether the event uses the `schemaVersion` field that applies to `MessageEnvelope` (§15.4.1 line 1515).
- How the event relates to `delivery_receipt` on the wire — §7.2 line 315 says expiry is delivered "via the `delivery_receipt` mechanism", §7.2 line 321 says it's an "event on the sender's event stream", §7.2 line 397 says "sending `message_expired` delivery receipts", and §15.4.1 line 1560 says "event to the sender's event stream." These four phrasings describe at least two different transport mechanisms.

**Recommendation:** Add a dedicated `message_expired` event schema block at §15.4.1 immediately following the reason-enum table (line 1565). Declare the exact field set: `type`, `messageId`, `targetSessionId`, `reason`, `expiredAt` (RFC 3339), `schemaVersion`. State unambiguously that the event is delivered on the sender session's event stream (not as part of the synchronous `delivery_receipt`). Then reconcile §7.2 lines 315, 321, and 397 to reference this canonical schema and transport, removing the "via the `delivery_receipt` mechanism" and "sending `message_expired` delivery receipts" phrasings which mis-describe the transport.

### MSG-015. §7.2 uses `message_dropped` receipt terminology conflicting with canonical `status: "dropped"` [Low]

**Section:** spec/07_session-lifecycle.md §7.2 lines 256, 267, 315

Three §7.2 sites describe the overflow receipt as "a `message_dropped` delivery receipt":
- Line 256 (in-memory inbox overflow): "the sender receives a `message_dropped` delivery receipt with `reason: "inbox_overflow"`"
- Line 267 (durable inbox overflow): "Overflow drops the oldest entry (`LPOP` + drop) with a `message_dropped` receipt"
- Line 315 (DLQ overflow): "the sender receives a `message_dropped` delivery receipt with `reason: "dlq_overflow"`"

But per §15.4.1 line 1551, the canonical `status` enum value is **`dropped`** (not `message_dropped`). There is no event type `message_dropped` — the receipt is the canonical `delivery_receipt` object with `status: "dropped"`. The `message_dropped` term does not appear in §15.4.1 or elsewhere in the spec.

Implementers building a receipt handler from §7.2 alone will search for a `message_dropped` event/type that does not exist; reviewers comparing §7.2 to §15.4.1 will have to manually translate `message_dropped` to `status: dropped` on each mention.

**Recommendation:** Replace all three `message_dropped` occurrences with the canonical phrasing: "the sender receives a `delivery_receipt` with `status: "dropped"` and `reason: "inbox_overflow"` (or `dlq_overflow`)". This mirrors the `delivery_receipt` grammar used elsewhere and eliminates the implicit rename.

### MSG-016. `delivery_receipt.reason` schema comment contradicts the status-value prose [Low]

**Section:** spec/15_external-api-surface.md §15.4.1 lines 1552, 1558

The schema-block comment at line 1552 says `reason` is "populated when status is dropped, expired, or rate_limited" — explicitly omitting `error`. But the following prose at line 1558 lists `error` reasons: "`error` (delivery failed due to infrastructure error, e.g., `reason: "inbox_unavailable"` when Redis is unreachable for durable inbox, or `reason: "scope_denied"` when messaging scope denies the target)."

These two statements are directly contradictory: the schema comment says error status does **not** populate `reason`; the prose shows examples of error status **with** `reason` populated. An implementer reading the schema block alone will omit `reason` on error receipts; a consumer building a parser from §15.4.1 will have ambiguous expectations.

Additionally, line 1552 says `reason` is populated for `expired`, but a `message_expired` event (the async notification) carries the reason on the **event**, not the receipt — so it is unclear whether a synchronous receipt with `status: expired` even exists on the synchronous `lenny/send_message` return path, or only the async event.

**Recommendation:** Update the line 1552 schema comment to: "`reason`: `&lt;string — populated when status is dropped, expired, rate_limited, or error; omitted when delivered or queued&gt;`" so it matches line 1558's example list. In the same pass, clarify whether `status: expired` is ever returned on the synchronous receipt (vs. only in the async `message_expired` event), because §15.4.1 line 1546 says every call returns a receipt but §7.2 line 315 routes expiry to the async event stream.

### MSG-017. `msg_dedup` Redis key referenced in §15 but not registered in §12.4 key prefix table [Low]

**Section:** spec/12_storage-architecture.md §12.4 lines 177–195; spec/15_external-api-surface.md §15.4.1 line 1569

§15.4.1 line 1569 references a Redis key `t:{tenant_id}:session:{session_id}:msg_dedup` (sorted set) used for message-ID deduplication within `deduplicationWindowSeconds`. The §12.4 Redis key prefix table (lines 180–193) does not include this key. The §12.4 table is normative: its introduction at line 177 says "The following table lists all canonical key prefix patterns in use." The §12.4 text at line 195 is used by the `TestRedisTenantKeyIsolation` integration test to drive per-key tenant-isolation assertions. A reader (or test author) building the tenant-isolation coverage from §12.4 alone will omit `msg_dedup` and a cross-tenant dedup collision will go undetected in the test suite.

This is the same cross-section completeness defect that MSG-007 flagged for the durable inbox key, now reappearing for the dedup key.

**Recommendation:** Add a row to §12.4 line 186's neighborhood:
`| t:{tenant_id}:session:{session_id}:msg_dedup | Message deduplication set | Sorted set scored by receipt timestamp; retains seen message IDs for `deduplicationWindowSeconds` (default 3600s); trimmed on write; see [§15.4.1](15_external-api-surface.md#1541-adapterbinary-protocol) |`
Also update line 195's `TestRedisTenantKeyIsolation` coverage sentence to add: "(g) a `msg_dedup` write for tenant A's session must not be visible to a deduplication check scoped to tenant B's session."

---

## 23. Policy & Admission Control

### POL-018. Audit payload field names in §11.6 inconsistent with §16.7 and §4.8 after iter3 POL-015/017 fixes [Medium] — FIXED

**Resolution.** Rewrote the "Audit events" paragraph at `spec/11_policy-and-controls.md:302` to match the §16.7 canonical payload for `admission.circuit_breaker_rejected`: replaced `caller_user_id` with `caller_sub`/`caller_tenant_id`; added `limit_tier` (with the full `runtime | pool | connector | operation_type` enumeration matching the metric label vocabulary), `replica_service_instance_id`, `parent_session_id`, and `delegation_depth`; explicitly split the snapshot into "`session_id` when admitting a continuation" vs. "`parent_session_id` + `delegation_depth` when admitting a delegation child". Added an explicit "§16.7 is the authoritative schema source for this event" cross-reference to prevent future drift. §11.6 line 302 is now consistent with §11.6 line 300 (POL-014 callout), §4.8 line 979, and §16.7 line 623. The edit also incidentally removed the trailing "/ equivalent" placeholder (POL-020).

**Section:** `spec/11_policy-and-controls.md:302`, `spec/16_observability.md:566`, `spec/04_system-components.md:952`

The iter3 POL-015 fix added a POL-014 "Pre-chain gate" callout at `spec/11_policy-and-controls.md:300` that correctly states the `admission.circuit_breaker_rejected` payload "carries the authenticated caller identity (`caller_sub`, `caller_tenant_id`)". Two lines below, the canonical "Audit events" paragraph at `spec/11_policy-and-controls.md:302` still declares the payload as:

&gt; "`circuit_name`, `reason`, `opened_at`, and the admission-time request snapshot (session_id or delegation parent, requested runtime/pool, **caller_user_id**)"

This contradicts (a) the immediately-preceding POL-014 callout on line 300 in the same section, (b) `spec/04_system-components.md:952` ("carrying the breaker name, open-state reason, and the request snapshot at admission time — including the authenticated caller identity (`caller_sub`, `caller_tenant_id`)"), and (c) the authoritative event catalog at `spec/16_observability.md:566` which pins the schema to:

&gt; "`circuit_name`, `reason`, `opened_at`, `limit_tier`, `replica_service_instance_id`, and the admission-time request snapshot (`session_id` when admitting a continuation, `parent_session_id` + `delegation_depth` when admitting a delegation child, requested `runtime`, requested `pool`, `caller_sub`, `caller_tenant_id`)"

Beyond the `caller_user_id` vs. `caller_sub`/`caller_tenant_id` naming divergence, §11.6 line 302 is missing five schema fields that §16.7 (the authoritative catalog) specifies: `limit_tier` (added by iter3 POL-017), `replica_service_instance_id` (added by iter3 POL-017), `parent_session_id`, `delegation_depth`, and the explicit split between the continuation-admission snapshot and the delegation-admission snapshot. A deployer reading §11.6 as the canonical source for the subsystem (reasonable, since that is the circuit-breaker subsystem's home) will wire the SIEM consumer and retention policy against a strict subset of the actual payload, and then be surprised when §16.7-driven OCSF translation emits fields they weren't prepared for. This matters for: (1) PII/retention analysis — `caller_sub` is an OIDC subject identifier with different GDPR exposure than a gateway-internal `user_id`; (2) per-replica correlation — `replica_service_instance_id` changes how on-call correlates a rejection storm to gateway replicas; (3) audit-sampling metric/audit parity — `limit_tier` is the correlation key between the rejection-suppressed metric and the sampled audit rows, and its omission from the §11.6 payload doc breaks the "correlate a metric spike to its sampled audit rows with an exact label equality" guarantee the same paragraph promises two sentences later (line 304).

**Recommendation:** Rewrite the §11.6 line 302 "Audit events" paragraph to match §16.7 verbatim for the `admission.circuit_breaker_rejected` payload, using `caller_sub`/`caller_tenant_id` (not `caller_user_id`), and adding `limit_tier`, `replica_service_instance_id`, `parent_session_id`, `delegation_depth`, and the continuation-vs-delegation snapshot split. Alternatively, replace the §11.6 line 302 schema enumeration with a single cross-reference: "Every request REJECTed by a tripped breaker emits an `admission.circuit_breaker_rejected` audit event whose payload is catalogued in [§16.7](16_observability.md#167-section-25-audit-events)" — making §16.7 the single source of truth and eliminating the duplication that produced this drift. The `circuit_breaker.state_changed` state-change event in the same sentence should also be cross-referenced to §16.7 (currently §16.7 line 566 does not enumerate it, which is a separate gap worth filing if §16.7 intends to be comprehensive).

---

### POL-019. `AdmissionController` admission-path behavior during Redis outage is unspecified (fail-open/fail-closed posture undefined) [Medium] — **FIXED**

**Resolution:** Added an "Admission-path Redis-outage posture" paragraph to `spec/11_policy-and-controls.md` §11.6 (immediately after the existing "In-process fallback for the health service" paragraph) that pins the posture: (a) `AdmissionController` evaluates exclusively against the in-process cache and never blocks on live Redis RTT; (b) running-replica Redis outage serves stale cache indefinitely with observability — `lenny_circuit_breaker_cache_stale_seconds` gauge, `CircuitBreakerStale` alert at 60 s, and the sampled `admission.circuit_breaker_cache_stale` audit event for both `admitted` and `rejected` outcomes so incident investigators can tell a true closed-breaker from a stale-cache fail-open; (c) fresh-replica cold-start with Redis unreachable refuses readiness (`/readyz` returns 503 with `CIRCUIT_BREAKER_CACHE_UNINITIALIZED`) until the startup Redis read succeeds — this is the security-salient fail-closed branch that prevents operator-declared breakers from being silently bypassed on replica startup. Dual-store outage continues to be governed by `DualStoreUnavailable` which rejects all new sessions regardless of breaker state. Added three metrics to `spec/16_observability.md` §16.1 (`lenny_circuit_breaker_cache_stale_seconds` gauge, `lenny_circuit_breaker_cache_stale_serves_total` counter with `outcome` label, `lenny_circuit_breaker_cache_initialized` gauge); added `CircuitBreakerStale` alert to §16.5; added `admission.circuit_breaker_cache_stale` audit event to §16.7.

**Section:** `spec/11_policy-and-controls.md:283-285`, `spec/04_system-components.md:952`, `spec/16_observability.md:566`

Circuit-breaker state lives in Redis (`cb:{name}` keys, §11.6 line 283). `spec/11_policy-and-controls.md:285` ("In-process fallback for the health service") documents a 5s-stale in-process cache used **only** by the health service (§25.3) when Redis is unreachable. The admission-path fallback is not specified:

1. **Cold-start Redis outage.** §11.6 line 283 says "Gateway replicas also re-read all circuit states from Redis at startup and cache in-process." If Redis is unreachable at startup, the cache is empty. The spec does not say whether (a) the replica refuses to become ready (fail-closed at admission), (b) the replica serves admission against an empty cache (fail-open — all breakers appear closed, session creation and delegation proceed against a "degraded" runtime that operators intended to isolate), or (c) some other disposition. For an operator-declared breaker whose purpose is incident response ("Runtime X degraded", "Uploads temporarily disabled"), a silent fail-open on fresh replica startup subverts the control.

2. **Runtime Redis outage.** During a running replica's Redis outage, the 5s cache TTL expires. The spec does not say whether the replica (a) continues serving admission against the stale cache (eventual-consistency fail-open for up to the `rateLimitFailOpenMaxSeconds` / `quotaFailOpenCumulativeMaxSeconds` windows, which govern quota counters but are not wired to breaker state), (b) fails admission closed, or (c) some hybrid. §11.6 line 285 says "entries up to 5 s stale may be served while Redis is unavailable" but scopes that guarantee to the health service, not the admission path.

3. **Observability gap.** There is no metric or alert for "admission-controller evaluated against stale/empty circuit-breaker cache". `RateLimitDegraded` (§16.5 line 386) covers the quota/rate-limit fail-open window but explicitly not the circuit-breaker path. `CircuitBreakerActive` (§16.5 line 453) only fires when the breaker is open **and visible** to at least one replica — it would never fire for a breaker that an operator set to open but that the admission path can no longer see because Redis is unreachable and the cache is cold.

4. **Latency coupling.** Because the AdmissionController runs synchronously on every session-creation and delegation admission (§11.6 line 298), a naive "read Redis on every check" recovery path would add a Redis RTT to every admission decision during a Redis partial-outage and amplify the outage. The spec should explicitly state the read discipline for the admission path (cache-only vs. best-effort refresh).

This is material for the POL perspective because the operator-managed circuit-breaker IS the incident-response primitive: a deployer who declares "Runtime X degraded" during an incident and whose Redis then blips for 30s must know whether the block holds or silently lifts.

**Recommendation:** Extend `spec/11_policy-and-controls.md:285` ("In-process fallback") to explicitly cover the admission path, and pin the posture:

&gt; "**Admission-path Redis-outage posture.** The AdmissionController evaluates exclusively against the in-process circuit-breaker cache; it never blocks an admission decision on a live Redis RTT. On a running replica, cache entries up to `cb.cacheStaleMaxSeconds` (default 5 s, aligned with the poll interval) stale may be served during a Redis outage; `CircuitBreakerCacheStale{replica}` is incremented (new metric) and the `CircuitBreakerStale` alert (new, §16.5) fires when stale-serve duration exceeds 60 s. On fresh replica startup with Redis unreachable, the replica **refuses readiness** (`/readyz` returns 503 with reason `CIRCUIT_BREAKER_CACHE_UNINITIALIZED`) until the startup Redis read succeeds at least once — this prevents cold-start fail-open for operator-declared breakers. During a sustained Redis outage, already-ready replicas remain ready and continue to serve against the last cached state; new replicas cannot become ready until Redis recovers. Emit `admission.circuit_breaker_cache_stale` (new audit event, §16.7) when the cache serves a rejection or (critically) a non-rejection past the staleness threshold, so incident investigators can distinguish "breaker was genuinely closed" from "breaker state was unknown and fail-open was served."

Add `lenny_circuit_breaker_cache_stale_seconds` (gauge) to §16.1 and the `CircuitBreakerStale` alert to §16.5.

---

### POL-020. Iter3 POL-016 fix incomplete: "/ equivalent" placeholder wording retained after anchor correction [Low]

**Section:** `spec/11_policy-and-controls.md:302`

The iter3 POL-016 recommendation was to both fix the broken anchor AND drop the "/ equivalent" placeholder. The anchor was corrected from `#167-audit-event-catalogue` to `#167-section-25-audit-events`, but the trailing "/ equivalent" was not removed:

&gt; "...appear in the catalogued audit event list in [§16.7](16_observability.md#167-section-25-audit-events) **/ equivalent**."

The "/ equivalent" reads as placeholder authoring — it suggests the author was unsure where the audit event list actually lives, and the correction addressed one half of the iter3 recommendation but not the other. Every other §16.7 cross-reference in the spec ends cleanly without the "/ equivalent" stub (e.g., `spec/11_policy-and-controls.md:387`, `spec/11_policy-and-controls.md:343`).

**Recommendation:** Drop "/ equivalent" so the sentence ends:

&gt; "...appear in the catalogued audit event list in [§16.7](16_observability.md#167-section-25-audit-events)."

---

### POL-021. `limit_tier=operation_type` value set never enumerated; operator-declared degraded states (uploads, delegation-depth) have no canonical mapping [Low]

**Section:** `spec/11_policy-and-controls.md:280-281,304`, `spec/16_observability.md:175-176,566`

The iter3 POL-017 fix introduced the `limit_tier` label with vocabulary `runtime | pool | connector | operation_type` (now shared across `lenny_circuit_breaker_rejections_total`, `lenny_circuit_breaker_rejections_suppressed_total`, and the `admission.circuit_breaker_rejected` audit event). Three of those four values — `runtime`, `pool`, `connector` — map 1:1 to the first three operator-declarable degraded states at §11.6 lines 277-279 ("Runtime X degraded / offline", "Pool Y full", "External connector Z down"). The fourth value, `operation_type`, is never enumerated, yet §11.6 lines 280-281 lists two operator-declarable states that clearly would need it: "Uploads temporarily disabled" and "Delegation depth &gt; N disabled during incident". Leaving `operation_type` as a free-form string is inconsistent with the closed enumerations used for every other `limit_tier` label in the spec (`caller_per_second | caller_per_minute | tenant_per_second` in §13.3 token limits; the `limit_tier` values for quota rejections) and breaks the "metric spike correlates 1:1 with sampled audit rows" guarantee because a deployer can mint arbitrary `operation_type` strings when opening a breaker via the admin API.

**Recommendation:** Enumerate the closed `operation_type` value set in §11.6 and link the circuit-breaker Admin API body (`POST /v1/admin/circuit-breakers/{name}/open`) to that enumeration. At minimum, cover the two states listed at lines 280-281: `operation_type` ∈ `{uploads | delegation_depth | &lt;new_in_future&gt;}`. Reject `open` requests whose breaker-match criteria reference an unknown `operation_type` at Admin-API time with `INVALID_OPERATION_TYPE`. Cross-reference the enumeration from §16.1 (line 175/176) and §16.7 (line 566) so the metric-label and audit-payload vocabularies stay authoritative.

---

### POL-022. §11.6 line 298 uses prose "before quota and policy evaluation" instead of the canonical phase vocabulary established by iter3 POL-015 [Low]

**Section:** `spec/11_policy-and-controls.md:298-300`

The iter3 POL-015 fix canonicalized the admission-chain ordering as `AuthEvaluator` (`PreAuth`, priority 100) → `AdmissionController` (pre-chain gate) → `PostAuth` / `PreDelegation` interceptor chains. The POL-014 callout at line 300 uses this phase vocabulary correctly. However, the "AdmissionController evaluation" sentence two lines above at line 298 retains the pre-iter3 prose framing: "before quota and policy evaluation". This is technically consistent (quota is evaluated by `QuotaEvaluator` at `PostAuth`, policy by `DelegationPolicyEvaluator` at `PreDelegation` — both run after `AdmissionController`), but the two phrasings use different vocabularies for the same ordering and leave the reader to reconcile them. A deployer skimming the section for "where does my admission hook run?" must synthesize the answer from two different framings in back-to-back paragraphs.

**Recommendation:** Rewrite line 298 to use the phase vocabulary, eliminating the redundant prose framing:

&gt; "**AdmissionController evaluation.** The gateway evaluates all active (open) circuit breakers as a pre-chain gate at the start of every session-creation and delegation admission check — after `AuthEvaluator` completes at `PreAuth` and before the `PostAuth` and `PreDelegation` interceptor chains run (see the pre-chain gate callout below and [§4.8](04_system-components.md#48-gateway-policy-engine)). If any open circuit breaker applies to the requested runtime, pool, connector, or operation type, the request is rejected immediately with `CIRCUIT_BREAKER_OPEN` (HTTP 503, `retryable: false`). The error body includes `circuit_name`, `reason`, and `opened_at`."

The callout at line 300 can then be trimmed to reference-only (no re-stating of the ordering), reducing the amount of prose that must stay synchronized on future edits.

---

## 24. Extended State Machine

### EXM-009. `Host-node schedulability precondition` not applied to scrub_warning re-warm transition [Medium] — Already Fixed

**Section:** 6.2 (lines 133, 134, 160)

The iter3 EXM-008 fix added "host node is schedulable" as a precondition on the `task_cleanup → sdk_connecting` success transition (line 133) and defined the semantics in a dedicated paragraph (line 160). However, the sibling transition at line 134 — `task_cleanup → sdk_connecting [scrub_warning]` — does not list the precondition at all. Its guard is only `preConnect: true, scrub fails with onCleanupFailure: warn, maxScrubFailures not reached, maxTasksPerPod not reached, maxPodUptimeSeconds not reached`. Meanwhile the normative paragraph at line 160 states "This rule applies to all preConnect pools" and the rationale ("SDK re-warm on a cordoned node would produce an idle pod whose next eviction is imminent") applies equally to the scrub_warning path. The asymmetry means: a preConnect pod on a cordoned node that experiences a scrub warning will still enter `sdk_connecting` (wasting 60s of `sdkConnectTimeoutSeconds` budget on an imminent-eviction pod), whereas a cleanly-scrubbed pod on the same cordoned node will correctly transition to `draining`. This contradicts the "all preConnect pools" scope stated in the definition paragraph, and it leaves the scrub_warning transition inconsistent with its sibling for no principled reason.
**Recommendation:** Add "host node is schedulable" to the line 134 transition guard, so both re-warm paths respect the precondition uniformly: `task_cleanup ──→ sdk_connecting [scrub_warning] (preConnect: true, scrub fails with onCleanupFailure: warn, maxScrubFailures/maxTasksPerPod/maxPodUptimeSeconds not reached, host node is schedulable — ...)`. If the host node is unschedulable on a scrub_warning outcome, route to `draining` (same fallback as line 133). This matches the line 160 paragraph's "all preConnect pools" scope and eliminates the asymmetry.

**Resolution:** Already fixed as a side-effect of the iter4 WPL-004 resolution (idx 71). The scrub_warning re-warm arrow in spec/06_warm-pod-model.md §6.2 (now line 155) already carries the "host node is schedulable" precondition verbatim: `task_cleanup ──→ sdk_connecting [scrub_warning] (preConnect: true, scrub fails with onCleanupFailure: warn, maxScrubFailures not reached, maxTasksPerPod not reached, maxPodUptimeSeconds not reached, host node is schedulable — pod re-warms SDK before returning to idle and the scrub_warning annotation persists on the pod)`. The sibling unschedulable-fallback arrow `task_cleanup ──→ draining [scrub_warning]` was added in the same WPL-004 fix (line 153), providing the same `draining` fallback as the scrub-success path. The normative "Host-node schedulability precondition" paragraph at line 181 was also rewritten to state "The 'host node is schedulable' precondition on both the `task_cleanup → sdk_connecting` and `task_cleanup → sdk_connecting [scrub_warning]` transitions..." and explicitly notes "The rule applies identically to the scrub-success and scrub-warning preConnect edges". All four arrows (2× schedulable→sdk_connecting, 2× unschedulable→draining) are present with uniform guards, closing the asymmetry flagged by this finding. Regression check: cross-reference in spec/05_runtime-registry-and-pool-model.md §5.2 line 444 references the scrub_warning transition without restating guards, so no downstream update needed.

### EXM-010. `cancelled → task_cleanup` transition does not define retirement-counter increment [Low]

**Section:** 6.2 (line 127), 5.2 (lines 432-438)

iter3 EXM-009 flagged this and remains unfixed in iter4. Line 127 (`cancelled ──→ task_cleanup`) says "pod runs scrub, then proceeds to idle or draining per normal task_cleanup rules", but neither 6.2 nor 5.2's task-mode retirement policy ([§5.2](05_runtime-registry-and-pool-model.md) lines 432-438) specifies whether a cancelled task increments the pod's completed-task count for `maxTasksPerPod`. §5.2 line 434 says the trigger is "The pod's completed task count reaches `maxTasksPerPod`" — which suggests a cancelled task does NOT count (because it did not "complete"). If that is the intent, then deployers with `maxTasksPerPod: 10` running a workload with frequent cancellation will observe pods effectively serving many more than 10 tasks (each cancelled task being a "free" slot that does not advance the retirement counter), defeating the explicit reuse-limit choice the spec says `maxTasksPerPod` forces. Additionally, line 127 does not say whether the cancelled-task scrub outcome counts toward `maxScrubFailures` (it should, since scrub failure is orthogonal to task outcome) or whether preConnect re-warm applies after cancellation cleanup.
**Recommendation:** Add a sentence at line 127 or as a companion paragraph to the task-mode retirement policy in §5.2: "Cancelled tasks DO count toward `maxTasksPerPod` — a cancellation that reaches `task_cleanup` is equivalent to a task completion for retirement-counter purposes, since scrub runs regardless of task outcome. Scrub failures during cancellation cleanup count toward `maxScrubFailures` identically to normal post-task scrub. The preConnect re-warm rules (lines 133-134) apply uniformly: a cancelled task on a preConnect pool routes through `sdk_connecting` if the standard guards pass." Explicitly stating "DO count" (vs "do NOT count") is a deployer-facing choice that materially affects pool sizing.

### EXM-011. Retirement-config-change staleness unaddressed (iter3 EXM-010 persists) [Low]

**Section:** 5.2 (line 554)

iter3 EXM-010 flagged that deployer changes to `maxTasksPerPod`, `maxScrubFailures`, or `maxPodUptimeSeconds` leave the pool sized against a stale `mode_factor` histogram, and iter4 did not fix it. Line 554 says `mode_factor` converges over a 100-sample window and is "bounded above by `maxTasksPerPod`", but the bound is stated only for the converged case — not applied dynamically on config change. When a deployer tightens `maxTasksPerPod` from 50 to 10, the pool continues to size against a `mode_factor ≈ 50` for hours at low request rates, under-provisioning by 5x relative to the new target. This is operationally relevant: the very cases where a deployer tightens `maxTasksPerPod` (tighter security posture, audit finding, incident response) are exactly the cases where stale sizing is most harmful.
**Recommendation:** Add a sentence to line 554: "On deployer config changes to `maxTasksPerPod`, `maxScrubFailures`, or `maxPodUptimeSeconds`, the PoolScalingController immediately clamps `mode_factor ← min(mode_factor_current, maxTasksPerPod_new)` and resets the observed-sample window so subsequent pod cycles re-converge against the new retirement limits. Alternatively, hard-clamp `mode_factor ≤ maxTasksPerPod` on every scaling evaluation (not just at convergence)." This preserves the existing histogram mechanism while making it responsive to config tightening without waiting for 100 fresh samples.

### EXM-012. Overlapping `attached → failed` and `attached → resume_pending` transitions lack disambiguating guard [Low]

**Section:** 6.2 (lines 125, 126)

The task-mode state machine defines two transitions from `attached` on the same trigger: line 125 `attached ──→ failed (pod crash / node failure / unrecoverable gRPC error during active task)` and line 126 `attached ──→ resume_pending (pod crash / gRPC error during active task, retryCount &lt; maxTaskRetries)`. Line 126 has an explicit retry-count guard; line 125 does not have a complementary guard (`retryCount &gt;= maxTaskRetries`). A reader/implementer cannot tell whether line 125 fires before or instead of line 126 on the first crash. The existing `Pod crash during active task-mode task` prose at lines 164-171 clarifies the intent (line 125 is the "retries exhausted" branch, line 126 is the "retries remain" branch), but the transitions themselves are ambiguous without reading the prose. Other transitions in the same block (e.g., lines 89-90, 113-114, 117-120 for `starting_session`, `input_required`, `resuming`) properly carry explicit "retries exhausted" vs "retryCount &lt; maxRetries" guards on both sides.
**Recommendation:** Add the symmetric guard to line 125: `attached ──→ failed (pod crash / node failure / unrecoverable gRPC error during active task, retries exhausted or non-retryable)`. This matches the pattern used consistently elsewhere in the diagram (lines 89, 114, 118-119) and eliminates the ambiguity without changing intended behavior.

---

## 25. Web Playground

### WPP-010. Gateway startup fails before `lenny-bootstrap` seeds the `default` tenant when `authMode=dev` [High]

**Status:** Fixed.

**Resolution:** Adopted the preferred Ready-gate approach. Split `playground.devTenantId` validation into format (startup-gated, unchanged) and tenant-existence (now Ready-gated per-request at `/playground/*`). On a fresh `helm install` where the tenant row is not yet seeded, the gateway starts normally and serves non-playground routes (including `/healthz` for the `lenny-bootstrap` Job's readiness poll); `/playground/*` requests return `503 LENNY_PLAYGROUND_DEV_TENANT_NOT_SEEDED` with `Retry-After: 5` until the row commits, self-healing without a gateway restart. Edits:
- `spec/27_web-playground.md` §27.2 (Helm values table row for `playground.devTenantId`) rewritten to describe the Ready-gated tenant existence check.
- `spec/27_web-playground.md` §27.2 validation-layering block extended from 3 layers to 4 (Helm schema, preflight, startup format/cross-field backstop, per-request Ready-gate for tenant existence).
- `spec/27_web-playground.md` §27.3 `authMode=dev` bullet rewritten to state format-vs-existence split and the 503 error contract (code, `Retry-After: 5`, error envelope body, self-healing semantics, why the split avoids deadlock).
- `spec/27_web-playground.md` §27.8 metrics table: added `lenny_playground_dev_tenant_not_seeded_total` counter to surface sustained anomalies beyond the bootstrap window.
- `spec/10_gateway-internals.md` §10.3 required-keys table: added `playground.devTenantId` row (conditional on `playground.enabled=true` AND `playground.authMode=dev`), documenting the split semantics and why it is load-bearing.
- `spec/17_deployment-topology.md` §17.6 `lenny-bootstrap` Job paragraph: added "Ordering with `playground.devTenantId`" subsection explaining how the post-install hook interacts with the Ready-gate and why the deadlock is avoided.

**Section:** 27.3 (line 53); 17.6 (lenny-bootstrap Job, line 382).

§27.3:53 (iter3 TNT-006 fix) requires that "the gateway refuses to start with `LENNY_PLAYGROUND_DEV_TENANT_INVALID` if the configured tenant is absent or malformed" when `authMode=dev`. The `devTenantId` default is `default`. But the `lenny-bootstrap` Job that creates the `default` tenant is `helm.sh/hook: post-install,post-upgrade` (§17.6:382) — it runs **after** the gateway Deployment is scheduled and the gateway pods start. On a fresh production Helm install with `playground.enabled=true` and `playground.authMode=dev` (or any dev-mode Helm install that enables the playground), the gateway will enter `CrashLoopBackOff` at startup because the `default` tenant row does not yet exist in Postgres. The bootstrap Job needs the admin API to be Ready (it polls `GET /healthz`), so the two are deadlocked: bootstrap waits for gateway Ready; gateway refuses to start until bootstrap has seeded the tenant. Embedded Mode (`lenny up`, §17.4:143) "auto-provisions" the default tenant in-process and side-steps this, but the stated invariant binds all non-Embedded `authMode=dev` deployments (Compose Mode, Source Mode, any production or staging dev-mode Helm install).

The fix is also out-of-catalog: §10.3's startup-configuration validation table (lines 295–302) enumerates the gateway's required keys (`auth.oidc.issuerUrl`, `auth.oidc.clientId`, `defaultMaxSessionDuration`, `noEnvironmentPolicy`) but does not include any `playground.*` key, so the `LENNY_PLAYGROUND_DEV_TENANT_INVALID` failure has no canonical declaration anywhere other than §27.3's prose — operators cannot locate it via the `LENNY_CONFIG_MISSING` runbook.

**Recommendation:** Pick one:
- **Preferred:** relax the startup gate to a **Ready-gate** rather than a startup-gate — when `authMode=dev` and `devTenantId` is not yet in Postgres, the gateway starts but `/playground/*` routes return `503 LENNY_PLAYGROUND_DEV_TENANT_NOT_SEEDED` until the tenant appears (bootstrap-ordering bug becomes self-healing once bootstrap completes). Other routes (admin API, `/healthz`) remain available, unblocking the bootstrap Job. Add a corresponding row to §10.3's startup-config table noting that `playground.devTenantId` is Ready-gated rather than startup-gated.
- **Alternative:** move the tenant-existence check from the gateway to `lenny-preflight` (pre-install) for `global.devMode=false` installs; allow dev-mode installs to proceed with a deferred check (the `lenny-bootstrap` Job creates the tenant before the gateway serves any playground traffic because `playground.enabled` defaults to `false` anyway, and any dev install enabling the playground explicitly can accept that order).

In either case, add a row to §10.3's required-keys table for `playground.devTenantId` (conditional on `playground.enabled=true` AND `playground.authMode=dev`), and cross-reference §27.3.

---

### WPP-011. `apiKey` mode has no production-safety guardrail [Medium]

**Status:** Fixed — added `playground.acknowledgeApiKeyMode` Helm value (§27.2 feature-flag table), a non-suppressible yellow "API KEY MODE — paste only operator-issued tokens" gateway-rendered banner bullet in §27.9, a paste-form phishing-surface advisory bullet in §27.9 that cross-references the preflight check and the `monitoring.acknowledgeNoPrometheus` analog, and a new `playground.apiKeyMode (warning)` row in §17.6 preflight (fires when `playground.enabled=true AND playground.authMode=apiKey AND global.devMode=false AND playground.acknowledgeApiKeyMode=false`). Non-blocking per the established acknowledgement pattern; `apiKey` remains a legitimate production mode for operator-driven / headless workflows, but human-user deployments must now explicitly acknowledge the posture to silence the preflight warning.

**Section:** 27.2 (line 34); 27.3 (line 52); 27.9 (line 198–204).

iter3 TNT-005/WPP-008 retargeted `apiKey` mode to accept a "standard gateway bearer token" validated by the §10.2 auth chain — making it production-capable (no `global.devMode=true` guard) and therefore a legitimate default choice for operators who haven't configured OIDC. Post-iter3, `apiKey` mode is now asymmetric with its two siblings in ways that are not covered by any guardrail:

1. **No warning banner.** §27.9:203 requires a persistent red "DEV MODE — NOT FOR PRODUCTION" banner in `dev` mode but is silent on `apiKey`. Yet `apiKey` ships a bearer-token paste form as its primary UX — operationally identical to asking a human user to paste a production credential into a web form, which is a standard phishing vector. The spec's advisory prose ("human-user access should use `authMode=oidc`") is not enforceable and not visible to end users.
2. **No OIDC-configured gate.** `apiKey` mode is installable regardless of whether `auth.oidc.issuerUrl` is set. An operator who skipped OIDC configuration during install (accidentally or because the wizard didn't require it) can still ship the playground with `authMode=apiKey`.
3. **No Helm-value audit for the combination `playground.enabled=true AND playground.authMode=apiKey AND global.devMode=false`.** Nothing in §17.6 preflight / post-upgrade hook flags this as a configuration worth an operator's acknowledgement (analogous to the SIEM endpoint warning at §17.6:478).
4. **§27.9 covers XSS via the raw-frame inspector and workspace-plan tarballs** but does not mention the `apiKey` paste-form phishing surface or any bearer-token handling advisory — operators reading §27.9 could reasonably conclude the mode is secure because OIDC tokens are strongly typed.

**Recommendation:** Add to §27.9 an explicit security note for `apiKey` mode covering: (a) the paste-form phishing surface; (b) a server-rendered, non-suppressible top-of-page banner reading "API key mode — paste only operator-issued tokens" with a link to the doc page explaining when to use `oidc` instead; (c) a `lenny-preflight` WARNING (non-blocking) when `playground.enabled=true`, `playground.authMode=apiKey`, and `global.devMode=false` require acknowledgement via `playground.acknowledgeApiKeyMode: true` (same pattern as `monitoring.acknowledgeNoPrometheus` in §17.6:479). Alternatively, require `playground.authMode=apiKey` to be explicit (no default fall-through) and reject `helm install` without either `oidc` or `acknowledgeApiKeyMode: true`.

---

### WPP-012. `apiKey`-mode bearer mint point is undocumented; `POST /v1/playground/token` rejects `Authorization: Bearer` [Medium] — **Fixed**

**Resolution:** Applied recommendation (a): extended `POST /v1/playground/token` to a mode-polymorphic endpoint with explicit per-mode admission material. Updated §27.3 "Mode-agnostic `origin: \"playground\"` JWT claim" to name `POST /v1/playground/token` as the single mint endpoint for all three modes and explain per-mode subject resolution. Updated §27.5 chat-stream protocol bullet to reference the same mode-polymorphic endpoint in all three modes (replacing the earlier "minted by the `/playground/*` handler from the user-supplied bearer token in `apiKey` mode and the dev signer in `dev` mode" phrasing). Updated §10.2 `apiKey` and `dev` bullets to explicitly cross-reference `POST /v1/playground/token` and the [§27.3.1 Auth by mode](27_web-playground.md#2731-oidc-cookie-to-mcp-bearer-exchange) table. Updated §15.1:896 row for `POST /v1/playground/token` from "Cookie-auth only (rejects `Authorization: Bearer` headers)" to a per-mode summary (cookie in `oidc`; `Authorization: Bearer` in `apiKey`; no admission material in `dev`) with `400 LENNY_PLAYGROUND_WRONG_AUTH_MATERIAL` cross-mode rejection and authoritative pointer to the §27.3.1 table. The authoritative per-mode Auth by mode table at §27.3.1:128 (already present) now has consistent upstream references from every other section that mentions the mint path.

**Section:** 27.3.1 (lines 78, 91); 27.5 (line 143); 10.2 (line 194).

§27.3 "Mode-agnostic `origin: "playground"` JWT claim" (line 57) and §27.5:143 say: in `apiKey` mode "the JWT is obtained from … minted by the `/playground/*` handler from the user-supplied bearer token". §10.2:194 echoes: "the handler … invokes the session-JWT mint with the `origin: "playground"` claim attached". But the only mint endpoint specified in §27.3.1 is `POST /v1/playground/token`, which is explicitly **cookie-authenticated and rejects `Authorization: Bearer`** (§27.3.1:78; §15.1:791). No other mint endpoint is defined for `apiKey`/`dev` modes. Implementers are left with three incompatible interpretations:

1. The bearer minted for `apiKey` mode is the pasted token itself (used directly at the WebSocket) — but then there is no `origin: "playground"` claim because the token was issued by an external IdP, and the §27.6 idle-timeout override cannot fire.
2. A second, bearer-auth version of `POST /v1/playground/token` exists but is unspecified — conflicts with §27.3.1:78's "no `Authorization` header accepted" language.
3. The session-capability JWT mint that §10.2:161 describes as happening "after authentication" is re-minted at each session creation with the claim attached when the ingress route is `/playground/*` — but this JWT is internal to the gateway and is NOT delivered to the browser for the MCP WebSocket upgrade (the browser needs a bearer on the WS upgrade, per §27.3.1:96).

The interpretation gap is load-bearing: WPP-005 (iter2) and WPP-008 (iter3) fixes depend on the browser presenting a bearer that carries `origin: "playground"`. Without a defined mint endpoint for `apiKey`/`dev`, the flow is unimplementable as written.

**Recommendation.** Explicitly document the non-OIDC mint path. Either:
- **(a)** extend `POST /v1/playground/token` to accept `Authorization: Bearer` when `playground.authMode ∈ {apiKey, dev}` and cookie otherwise, with clear per-mode semantics; remove the blanket "rejects `Authorization: Bearer`" clause from §27.3.1:78 and §15.1:791 and replace it with a per-mode table; or
- **(b)** define a new endpoint (e.g., `POST /v1/playground/exchange`) for non-OIDC modes that takes the user's bearer and returns a `/playground/`-origin-stamped session JWT, and point §27.5:143 and §10.2:194 at it.

Either way, update §27.3.1's "Bearer token exchange" and §15.1:791 to cover all three modes, not only `oidc`.

---

### WPP-013. `playground.oidcSessionTtlSeconds` has no range bound [Low]

**Section:** 27.2 (line 39).

§27.2's Helm-value table specifies a bound of `60 ≤ ttl ≤ 3600` for `playground.bearerTtlSeconds` (line 40) but specifies no bound for `playground.oidcSessionTtlSeconds` (line 39, default `3600`). `oidcSessionTtlSeconds` governs a long-lived browser session cookie — an operator can set it to `31536000` (1 year) with no Helm-validate rejection and no `lenny-preflight` warning, producing a cookie that survives laptop theft, account offboarding, and OIDC provider-side revocation cycles until the server-side record expires. The "OIDC claim invalidation" path (§27.3.1:104) requires an explicit admin-API invalidation call; the natural-expiry safeguard fails when `oidcSessionTtlSeconds` is set unbounded. The bound is especially important because the server-side record holds the OIDC refresh token (if granted), per §27.3.1:73.

**Recommendation:** Add a range bound to §27.2:39, e.g., `300 ≤ ttl ≤ 28800` (5 min to 8 h), and render this via the `values.schema.json` generator (§17.6:608) so Helm rejects out-of-range values at install time. 28800 matches a typical "business-day" ceiling and is well below the 24-h gateway cert TTL (§10.3) so the cookie never outlives the signing-key overlap window.

---

### WPP-014. `apiKey`-mode bearer in `sessionStorage` is script-accessible; divergent from OIDC `HttpOnly` posture [Low]

**Section:** 27.3 (line 52); 27.9.

§27.3:52 stores the `apiKey`-mode pasted bearer in `sessionStorage` "only (not `localStorage`, never cookies)". `sessionStorage` is script-accessible via `window.sessionStorage`, so the token is reachable by any JS running on the page — in contrast to the OIDC mode cookie (§27.3.1:73), which is `HttpOnly` and unreachable from JS. The page's CSP (§27.7) restricts `script-src 'self'` which is the right posture, but a stored XSS in the playground's own hashed bundle, a Trusted Types bypass, or a DevTools-console exfiltration attack can read the token directly. Post-iter3, `apiKey` is the only playground auth mode where the browser has JS-level access to a platform-wide bearer; §27.9 does not call this out.

**Recommendation.** Add to §27.9 a note documenting the `apiKey`-mode script-accessibility asymmetry and: (a) recommend operators running `apiKey` in non-dev installs restrict the playground bundle to trusted users only; (b) document that a stolen `apiKey`-mode token is revocable only via admin-API user invalidation (`POST /v1/admin/users/{user_id}/invalidate` — §11.4) because the token is an upstream OIDC / service-account credential, not a minted session JWT; (c) set a shorter expiry ceiling on bearer-derived session JWTs when the source is `apiKey` mode (e.g., reuse `playground.bearerTtlSeconds` as the cap so a stolen `apiKey` bearer's playground-minted JWT can't outlive a 15-min window).

---

### WPP-015. Helm-validate rejection of `devTenantId` on "multiple tenants seeded" is not realizable at install time [Low]

**Section:** 27.2 (line 35); 27.3 (line 53).

iter3 TNT-006 added: "Helm-validate rejects the chart if `authMode=dev`, `auth.multiTenant=true`, and this value is left at `default` while multiple tenants are seeded" (§27.2:35; §27.3:53). The phrase "multiple tenants are seeded" is ambiguous at Helm-install time: (a) if it refers to `bootstrap.tenants` in Helm values, the current schema (§17.6:369) defines only a single `bootstrap.tenant` (not an array), so this criterion is trivially unreachable on a fresh install; (b) if it refers to post-install admin-API tenant creation, Helm-validate cannot observe that state — the rejection would have to move to a runtime check or a `helm upgrade` preflight that queries the admin API; (c) if it refers to `auth.multiTenant=true` alone (regardless of actual tenant count), then the reject-at-install rule is a true static Helm-validate condition but the wording is wrong. This is structurally the same class of issue as the (still-open) WPP-009 — an unrealizable install-time constraint phrased as a static Helm-validate rule.

**Recommendation.** Pick one:
- Make the rule fully static and reword: "Helm-validate rejects the chart if `authMode=dev`, `auth.multiTenant=true`, and `playground.devTenantId == \"default\"`." — this is trivially Helm-checkable and closes the "silently bind to the wrong tenant" gap via explicit-opt-in.
- Move the "multiple tenants seeded" dimension out of Helm-validate and into `lenny-preflight` (pre-upgrade), which can query the admin API to count tenants. Cite the preflight check with a named error code and list it in §17.6 preflight table.

Either way, ensure the rule matches the currently-supported `bootstrap.tenants` shape (singular `bootstrap.tenant` in §17.6:369) or extend the bootstrap schema to an array and reference the array cardinality explicitly.

---

### WPP-016. `session.cancel` frame name still undefined [Low]

**Section:** 27.6 (line 156); 09; 15.

— carryover from iter3 PARTIAL
§27.6:156 specifies that the playground client "sends `session.cancel` with reason `playground_client_closed`" on browser close — flagged as PARTIAL in iter3 because no frame named `session.cancel` is defined in §9 (MCP integration), §7 (session lifecycle uses `DELETE /v1/sessions/{id}`), or §15 (tool catalog uses `cancel_session`). iter4: no fix landed — §27.6:156 still uses the shorthand. With iter3's idle-timeout override now binding uniformly across all three modes, the best-effort cancel is the primary cleanup path and its non-delivery is the trigger for the 5-min idle fallback — the frame taxonomy is load-bearing for the §27.6 design.

**Recommendation:** Replace `session.cancel` in §27.6:156 with the documented cancel primitive — either the `cancel_session` MCP tool call (§15.6.1) with a `cancelReason` enum that includes `playground_client_closed`, or a WebSocket control frame if the post-unload path cannot issue a tool call. Define the reason-code enum wherever the frame is canonicalized.

---

## 26. Delegation Model

### DEL-012. `treeVisibility` missing from lease schema at §8.3 [Medium]

**Status: Fixed (iter4)** — Added `"treeVisibility": "full"` to the §8.3 delegation lease JSON schema; added a `treeVisibility` field description paragraph referencing the §7.2 `messagingScope` pairing and the existing `TREE_VISIBILITY_INSUFFICIENT_FOR_MESSAGING_SCOPE` rejection; added a three-rule inheritance/monotonicity paragraph (inherit on absence; narrow permitted; widen rejected with `TREE_VISIBILITY_WEAKENING`); added a snapshot/extension clarification paragraph (not part of `snapshotPolicyAtLease`; not extendable, explicitly added to the §8.6 non-extendable list alongside `maxDepth` / `minIsolationProfile` / `delegationPolicyRef` / `perChildRetryBudget`); catalogued `TREE_VISIBILITY_WEAKENING` (`POLICY`, HTTP 422) in §15.1 adjacent to `CONTENT_POLICY_WEAKENING` / `DELEGATION_POLICY_WEAKENING` with `details.parentTreeVisibility` / `details.childTreeVisibility`.

**Section:** `08_recursive-delegation.md` §8.3 (delegation lease JSON, lines 193-222); §8.5 (`lenny/get_task_tree`, line 447)

§8.5 explicitly describes `treeVisibility` as "the `treeVisibility` field on the delegation lease" with three valid values (`full`, `parent-and-self`, `self-only`) and states "the parent controls sibling visibility when issuing the delegation lease." The iter3 DEL-010 fix (catalogue of `TREE_VISIBILITY_INSUFFICIENT_FOR_MESSAGING_SCOPE`) depends on this field being a first-class lease attribute that the gateway can compare against the child's effective `messagingScope`. However, the canonical delegation lease JSON schema at §8.3 lines 193-222 does **not** list `treeVisibility` at all. Absent from the schema means: (a) there is no spec-defined default, (b) inheritance rules (can a child narrow it? can a child widen it?) are unspecified, and (c) storage/serialization in the delegation lease record is undefined. The `§8.5` description says "default: full" but the schema does not carry that default, and there is no monotonicity rule analogous to `minIsolationProfile` ("at least as restrictive as parent") or `maxDelegationPolicy` ("at least as restrictive").

**Recommendation:** Add `treeVisibility` to the §8.3 delegation lease JSON schema (with `"full"` as the default value in the example, matching the §8.5 prose) and add an inheritance paragraph alongside the existing `minIsolationProfile` / `maxDelegationPolicy` / `contentPolicy` rules stating that child leases may narrow (`full → parent-and-self → self-only`) but not widen the parent's effective `treeVisibility`, and that the gateway rejects widening with a new `TREE_VISIBILITY_WEAKENING` error (same category as `CONTENT_POLICY_WEAKENING` / `DELEGATION_POLICY_WEAKENING`). Also clarify in §8.3 whether `treeVisibility` is part of `snapshotPolicyAtLease` snapshotting and whether it is extendable via lease extension (suggest: not extendable, it is a visibility boundary).

### DEL-013. `messagingScope` not in lease schema — delegation-time `TREE_VISIBILITY_INSUFFICIENT_FOR_MESSAGING_SCOPE` check has unclear inputs [Medium]

**Status: Fixed (iter4)** — Added a normative "`treeVisibility` vs. `messagingScope` — delegation-time compatibility check" paragraph in §8.3 (immediately after the `treeVisibility` snapshot/extension paragraph) that makes the check's inputs explicit: `messagingScope` is not a per-delegation lease field; the gateway resolves the child's effective `messagingScope` per §7.2 hierarchy and resolves the child's effective `treeVisibility` per the lease-inheritance rules, and rejects with `TREE_VISIBILITY_INSUFFICIENT_FOR_MESSAGING_SCOPE` when effective `messagingScope` is `siblings` AND effective `treeVisibility` is not `full`; error `details` now specify `effectiveMessagingScope`, `effectiveTreeVisibility`, and `requiredTreeVisibility: "full"`. Added a "Post-delegation configuration change — existing-tree behavior" paragraph covering the already-running-tree case: post-delegation hierarchy changes do not re-evaluate active children, and the gateway preserves the existing lease's `treeVisibility` while narrowing the child's effective `messagingScope` to `direct` if a hierarchy change would otherwise produce an inconsistent combination. Tightened the §8.5 `lenny/get_task_tree` row wording to describe the check in the same resolved-effective terms and cross-link §8.3. Updated the §15.1 `TREE_VISIBILITY_INSUFFICIENT_FOR_MESSAGING_SCOPE` catalogue entry to rename `details.messagingScope` / `details.treeVisibility` to `details.effectiveMessagingScope` / `details.effectiveTreeVisibility`, add the hierarchy-resolution rationale, and cross-link §8.3's new normative clause.

**Section:** `08_recursive-delegation.md` §8.3 (lease schema), §8.5 line 447; `07_session-lifecycle.md` §7.2 lines 210-240

Per §7.2, the effective `messagingScope` is computed from the deployment-level / tenant-level / runtime-level hierarchy ("narrowest of deployment maxScope, tenant scope if set, top-most parent runtime scope if set") — it is **not** a field the parent sets per-delegation on the `lease_slice` or lease. However, the iter3 DEL-010 fix at §8.5 line 447 says "the gateway rejects `messagingScope: siblings` when `treeVisibility` is `self-only` or `parent-and-self` at delegation time." This phrasing reads as if `messagingScope` is a delegation-time input comparable against a lease field, but the §7.2 resolution path makes `messagingScope` a tenant/runtime-config resolution that is fixed for the child at child-session-creation time. As a result:

1. The `lease_slice` / `TaskSpec` at §8.2 does not expose `messagingScope`, so a delegating parent cannot request `messagingScope: siblings` for its child.
2. The delegation-time rejection must therefore be: "the child's resolved effective `messagingScope` (from hierarchy) is `siblings` AND the parent's `treeVisibility` on the child lease is weaker than `full`." This is never stated.
3. A consequence: if a tenant's `messagingScope` is raised from `direct` to `siblings` after a delegation tree is running, existing children with `treeVisibility: parent-and-self` would suddenly have the inconsistent combination — but §7.2 says label/config changes "do not affect active child sessions," which means the inconsistent combination can exist in running trees.

**Recommendation:** In §8.5 (or preferably §8.3), add a normative resolution clause:

&gt; At delegation time, the gateway resolves the child's effective `messagingScope` per §7.2 hierarchy and compares it against the child lease's `treeVisibility`. If `messagingScope` resolves to `siblings` AND the child lease's `treeVisibility` is not `full`, the delegation is rejected with `TREE_VISIBILITY_INSUFFICIENT_FOR_MESSAGING_SCOPE`. The error `details` should include `effectiveMessagingScope` and `effectiveTreeVisibility` so SDK authors know which side produced the mismatch.

Also clarify the already-running-tree case: when a post-delegation `messagingScope` hierarchy change would make an active child's effective scope `siblings` while its lease carries `treeVisibility != full`, the gateway keeps the existing lease visibility and the child's effective `messagingScope` silently narrows to `direct` (or equivalent) for that subtree — matching the "once approved, active sessions are not re-evaluated" principle of §8.3.

### DEL-008. Orphan tenant-cap fallback still emits no audit event or parent signal [Low]

**Section:** `08_recursive-delegation.md` §8.10 (line 1002, Note block); `11_policy-and-controls.md` §11.7 (audit event catalog, lines 62-68 — no delegation.orphan_cap_fallback entry); `16_observability.md` (§16.5 alerting rules, only gauge-based OrphanTasksPerTenantHigh at line 427)

Carried forward unchanged from iter2 DEL-007 and iter3 DEL-008 — still unfixed in iter3. The `maxOrphanTasksPerTenant` cap at §8.10 line 1002 silently downgrades a deployer-requested `cascadeOnFailure: detach` to `cancel_all` without (a) an audit event in the §11.7 catalog, and (b) any signal to the parent session's event stream. The existing observability is a cap-level gauge (`lenny_orphan_tasks_active_per_tenant`) and a threshold alert — neither captures the per-decision fallback instance nor lets an orchestrator agent programmatically detect that a specific `detach` request was converted to `cancel_all` for its tree. Because this is a policy-changing gateway decision that crosses a delegation boundary, it should be visible on both the audit path and the parent-facing event stream.

**Recommendation:** Add (1) a `delegation.orphan_cap_fallback` audit event in §11.7 (category POLICY) with `tenant_id`, `root_session_id`, `parent_session_id`, `requested_policy: detach`, `applied_policy: cancel_all`, `reason: tenant_orphan_cap_exceeded`, `orphan_count_at_fallback`, `orphan_cap`; and (2) annotate the `child_failed` / `child_cancelled` event schema in §8.10 with an optional `cascade.reason` field (enum including `tenant_orphan_cap_exceeded`) so the parent agent can programmatically detect the cap-driven cancellation and halt further `detach`-mode delegations.

### DEL-011. `TRACING_CONTEXT_TOO_LARGE` / `TRACING_CONTEXT_SENSITIVE_KEY` / `TRACING_CONTEXT_URL_NOT_ALLOWED` still not catalogued in §15.1 [Low]

**Section:** `08_recursive-delegation.md` §8.3 (lines 232-239, tracingContext validation table); `15_external-api-surface.md` §15.1 error catalog

Carried forward unchanged from iter3 DEL-011 — still unfixed. All three error codes appear in the §8.3 validation table but have zero §15.1 catalog coverage: no canonical HTTP status, no category, no retryable flag, no details schema. The iter3 fix for DEL-009 / DEL-010 (which closed the catalogue gap for three other delegation error codes) did not sweep these. `tracingContext` propagation is a DEL platform primitive (the gateway auto-attaches parent context to child leases and enforces parent-child merge rules), so SDK authors need the codes catalogued to emit meaningful errors when runtime authors mis-populate tracing context.

**Recommendation:** Add three §15.1 rows adjacent to `DELEGATION_AUDIT_CONTENTION`:

- `TRACING_CONTEXT_TOO_LARGE` — `PERMANENT`, 413, not retryable. `details.limit` (enum: `size_bytes` | `key_length` | `value_length` | `entry_count`), `details.observed`, `details.max`.
- `TRACING_CONTEXT_SENSITIVE_KEY` — `PERMANENT`, 400, not retryable. `details.key` (offending key name), `details.matchedPattern`.
- `TRACING_CONTEXT_URL_NOT_ALLOWED` — `PERMANENT`, 400, not retryable. `details.key`, `details.valuePrefix` (first 32 chars).

Cross-link all three to §8.3 tracingContext validation table and §16.3 distributed tracing.

### DEL-014. `DELEGATION_PARENT_REVOKED` status code 409 conflicts with `retryable: false` for `revocationReason: token_rotated` [Low]

**Section:** `15_external-api-surface.md` §15.1 error catalog (line 919); `08_recursive-delegation.md` §8.2 (lines 59-61); `13_security-model.md` §13.3 (line 548)

The iter3 DEL-009 fix catalogued `DELEGATION_PARENT_REVOKED` as `PERMANENT`/409/non-retryable with `details.revocationReason` example values `token_rotated` and `recursive_revocation`. These two reasons are semantically distinct:

- **`recursive_revocation`** (parent was deliberately revoked via `grant_type=urn:ietf:params:oauth:grant-type:token-exchange` with `requested_token_type=...access_token:revoked`, per §13.3 line 554) — truly terminal. `retryable: false` is correct. The parent session is also terminated.
- **`token_rotated`** (parent performed a normal rotation, §13.3 line 548) — the parent session is **not** terminated, only the old `jti` is revoked. A re-authenticated caller with the new parent token can retry the same `lenny/delegate_task` successfully. This is a **transient / authentication-refresh** scenario, not a permanent failure. Marking it `retryable: false` across both reasons loses a distinction that SDK authors need: on `token_rotated`, the SDK should trigger re-authentication and retry; on `recursive_revocation`, the SDK must fail upward.

**Recommendation:** Either (a) split `DELEGATION_PARENT_REVOKED` into two codes — `DELEGATION_PARENT_TOKEN_ROTATED` (`AUTH`, 401, retryable after re-auth) and `DELEGATION_PARENT_REVOKED` (`PERMANENT`, 409, non-retryable) — mirroring the `401 token_revoked` / `401 token_expired_or_revoked` distinction already used by §13.3 for direct token validation; or (b) keep a single code but clarify in the §15.1 entry that `retryable` is conditional on `details.revocationReason`: `retryable: true` when `token_rotated` (retry after re-auth), `retryable: false` when `recursive_revocation` or `admin_revoked`. Option (a) is preferred for SDK ergonomics (the `retryable` boolean cannot otherwise vary per-instance).

Also canonically enumerate the `revocationReason` enum values — the catalog entry currently says "e.g., `token_rotated`, `recursive_revocation`" which is not an enum specification. Add the full set (including `admin_revoked`) in §8.2 alongside the `DELEGATION_PARENT_REVOKED` mention.

### DEL-015. `DELEGATION_AUDIT_CONTENTION.details.retryAfterSeconds` duplicates `Retry-After` header, using a third convention relative to §25.2 / other entries [Low]

**Section:** `15_external-api-surface.md` §15.1 (line 920, `DELEGATION_AUDIT_CONTENTION`); §15.1 line 1003 (`Retry-After` header contract); `25_agent-operability.md` §25.12 line 305 (`suggestedRetryAfter` envelope field)

The iter3 DEL-009 fix added `DELEGATION_AUDIT_CONTENTION` with `details.retryAfterSeconds` (integer) while also populating the HTTP `Retry-After` header. Other 503/retryable entries in §15.1 either use the `Retry-After` header alone (e.g., `POOL_DRAINING` at line 917) or embed retry-after data in the `details` via more specific fields (e.g., `estimatedDrainSeconds`). The §25.2 canonical error envelope for the admin/operability API uses `suggestedRetryAfter` (string, e.g., `"30s"`). This creates three divergent conventions for expressing the same information:

1. HTTP `Retry-After` header only (most §15.1 503 entries).
2. HTTP `Retry-After` header + `details.&lt;name&gt;Seconds` integer (DELEGATION_AUDIT_CONTENTION, POOL_DRAINING).
3. Response envelope `suggestedRetryAfter` string (§25.12).

**Recommendation:** Pick one. Preferred: drop `details.retryAfterSeconds` from the DELEGATION_AUDIT_CONTENTION entry — the `Retry-After` header is canonical on 503 and SDK authors can read it uniformly. If `details` should include additional retry context, use a field with clearer semantics than "retry after" (e.g., `details.contentionWindowSeconds` — the observed Postgres lock hold time — which is a diagnostic value, not a redundant re-statement of `Retry-After`).

Now writing up the findings.

---

---

## Cross-Cutting Themes

Reviewing across perspectives, seven systemic themes emerged from iter4:

### 1. Iter3-fix regressions

Several iter3 fixes either introduced new gaps (as new-class regressions) or were applied partially, surfacing as iter4 findings:

- **PRF-006**: iter3 PRF-005's flat `maxUnavailable: 1` PDB contradicts §17.8.2's Tier 3 HPA scale-down throughput claims (3 pods/60s).
- **FLR-012**: iter3 FLR-008's cache-priming fix only fires when `last_checkpoint_workspace_bytes` is non-null — fresh sessions on a Postgres-healthy path still fall through to the 30s default.
- **WPL-001/WPL-002**: iter3 EXM-008's "host node is schedulable" precondition was added to one transition (line 133) but not its scrub_warning sibling; and the gateway lacks the Node RBAC/informer to actually evaluate `.spec.unschedulable`.
- **OBS-022**: iter3 OBS-013's fix added `billing_write_ahead_buffer_utilization` without the `lenny_` prefix, regressing against iter3 OBS-012's mandatory-prefix rule.
- **DOC-013/014/015/016**: four new broken anchors introduced by iter3 CPS-004, NET-051, and OBS-018 fixes, all of the same classes iter3 DOC-008/009/010 closed.
- **CMP-049**: iter3 CMP-046 added a legal-hold freshness gate to the post-restore reconciler but omitted a symmetric DeleteByUser preflight.
- **BLD-009/K8S-040**: iter3 BLD-005's Phase 3.5 deferral creates a normative expectation Phase 8 does not honor for `lenny-drain-readiness`.

### 2. Admission-plane completeness and preflight semantics

The admission-webhook surface continues to accumulate findings on HA configuration, phase-aware preflight, and selector correctness:

- **BLD-011**: Phase-aware `lenny-preflight` enumeration is still unspecified; pre-Phase-13 installs will fail-closed against a hard-coded expected set.
- **K8S-041/NET-061/NET-065/NET-067**: NetworkPolicy selector gaps (missing `podSelector` for storage/DNS, missing cluster-CIDR exclusions for ops-egress, over-broad admission-webhook selector).
- **K8S-042**: `lenny-sandboxclaim-guard` references `.status.phase` on the wrong CRD (SandboxClaim vs Sandbox).
- **K8S-045**: `lenny-pool-config-validator` rule set 1 can wedge PSC reconciliation under stale-Postgres conditions.
- **POL-018/019/020/021/022**: AdmissionController audit schema, Redis-outage posture, and phase vocabulary all converge on §11.6.

### 3. Partial-checkpoint and partial-manifest completeness

The CheckpointBarrier + partial-manifest protocol introduced in iter3 has open gaps:

- **CPS-006**: orphaned partial-chunk objects when gateway crashes between chunk commit and manifest write (storage leak + quota-integrity gap).
- **CPS-007**: CheckpointBarrier timeout discards recoverable partial state — asymmetric with Stage-2 tier-cap timeout.
- **CPS-008**: `partial-{n}.tar.gz` naming inconsistent with `chunk_encoding: tar`.
- **CPS-011**: three iter3 CPS findings (CPS-003/006/007) remain unresolved.

### 4. Playground (§27) security and onboarding asymmetries

The retargeted `apiKey` mode (iter3 TNT-005) left several surfaces asymmetric with the other auth modes:

- **WPP-010**: startup-vs-bootstrap deadlock — gateway refuses to start on missing default tenant but `lenny-bootstrap` runs post-install.
- **WPP-011**: no production-safety guardrail on `apiKey` mode (no banner, no OIDC-configured gate, no preflight warning).
- **WPP-012**: bearer mint point is named but endpoint is not defined (the only documented mint rejects `Authorization: Bearer`).
- **TNT-008/009/010**: subject-token-type and scope-narrowing invariants missing; OIDC callback rejection codes not cross-referenced; `playground.devTenantId` is startup-validated only.

### 5. Observability metric/alert/event catalog drift

Iter3's metric-prefix and alert-catalog consolidation has several carry-forward gaps:

- **OBS-022**: missing `lenny_` prefix regression.
- **OBS-023**: §25.13 references alert names not defined in §16.5 (PostgresUnreachable, RedisUnreachable, GatewayQueueDepthHigh, etc.).
- **CNT-010**: workspace_plan warning events not registered in §16.6 catalog.
- **MSG-012**: LTRIM missing from §12.4 after iter3 MSG-007's fix (§7.2 line 268 depends on it).
- **EXP-013**: experiment-router events not in §16.6 catalog.
- **CRD-014/018**: audit catalog gaps.

### 6. Delegation, interceptor, and cross-tenant trust boundaries

- **SEC-008/009/010**: upload archive limits under-specified, exported-file interceptor bypass, trust-based chained interceptor exception.
- **SEC-013**: interceptor weakening cooldown timestamp is admin-writable.
- **TNT-012**: cross-tenant `?tenantId=` query parameter has ambiguous authorization outcome.
- **NET-063**: gateway ↔ interceptor hop lacks mTLS + SPIFFE peer validation.
- **NET-064**: `spiffeTrustDomain` defaults only warn, permitting cross-deployment impersonation.

### 7. Cross-reference and documentation drift

- **DOC-017/018**: headings and TOC omissions now in third iteration unchanged (DOC-011/012 never fixed).
- **DXP-006/007/008, OPS-005/006/009, EXP-005/007/008, CRD-008/010/011, DEL-007/008/011, FLR-009/010, API-006**: carry-forwards from iter3 where fixes were skipped or never landed.
