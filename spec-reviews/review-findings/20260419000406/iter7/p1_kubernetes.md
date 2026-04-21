# Perspective 1 — Kubernetes Infrastructure & Controller Design (KIN) — Iteration 7

## Scope and method

Iter7 anchors to the iter5 K8s baseline (`p01_kubernetes.md`). The iter6 P1 perspective was dispatched but sub-agent rate-limit exhausted before producing findings (see `iter6/p1_k8s.md`), so no iter6 K8s findings exist; the iter6 fix commit (`8604ce9`) touched observability, API, credentials, and content — **no `spec/04_system-components.md` §4.6 lines, no §6 warm-pod lines, no §17 topology lines, and no §18 build-sequence lines were modified**. The iter6 `summary.md` does confirm `spec/04_system-components.md` had small edits for CRD-020 (line 1345 credential-pool flows, outside the pod-lifecycle controller sections). The controller, warm-pod, admission-plane, and build-sequence surfaces are therefore identical to the tree iter5 declared converged.

Method:
1. Re-verify iter5 carry-forward Lows (KIN-021 through KIN-025) against the current tree — none has been addressed; all remain Low under the iter5 severity rubric.
2. Scan iter5-era K8s surfaces for NEW correctness / reliability / security issues that iter5's compact scope did not exercise, using the iter1–iter5 severity-calibration anchors (`feedback_severity_calibration_iter5.md`): correctness/reliability bugs that produce wrong outcomes at Medium+; forward-compat / defense-in-depth / observability-completeness hedges remain Low.
3. Cross-check the iter5 fix sites for subtle hazards that earlier reviews missed — particularly the crash-recovery / admission-denial interaction surfaces, the host-schedulability pod label flow, feature-gated chart inventory, and the new Lease-based control plane components introduced after iter3 (e.g., `lenny-ops-leader`).

**Continuation of KIN numbering:** Iter5 stopped at KIN-025. New iter7 findings begin at KIN-026. No numbers are reused.

## Prior-iteration carry-forwards

All five iter5 carry-forward Lows (KIN-021 through KIN-025) remain open in the current tree and carry forward unchanged to iter7. Summary, with severity held to Low per the iter5 calibration rubric:

- **KIN-021 (iter4 K8S-046) [Low]** — `lenny-preflight` enumerates deployed webhooks at install/upgrade time only; continuous inventory drift (manual `kubectl delete`, `helm rollback`) is not detected. Runtime webhook-unavailability alerts cover reachability, not inventory. Not a correctness defect; hardening only.
- **KIN-022 (iter4 K8S-047) [Low]** — Per-template Kata PDB `maxUnavailable: 1` does not bound *cross-template* simultaneous drain; multiple Kata templates on a small dedicated Kata node pool could concurrently evict. Capacity-planning concern implicit in §17.8.
- **KIN-023 (iter4 K8S-048) [Low]** — Admission webhook Deployments ship with `replicas: 2 + PDB minAvailable: 1` but no topology spread constraint; a zonal failure can take both replicas out together. Forward-compat HA hedge.
- **KIN-024 (iter4 K8S-049) [Low]** — `Sandbox` finalizer orphan-reclaim runs on the WPC leader and detects orphan `SandboxClaim` resources, but the reverse case (stranded `claimed`-but-no-claim `Sandbox` after a leader-failover window race) depends on the next reconcile cycle re-observing the in-flight state. Narrow, recoverable.
- **KIN-025 (iter4 K8S-050) [Low]** — PSC reads `SandboxWarmPool.status` (owned by WPC) to compute scaling; there is no `status.lastTransitionTime` staleness guard. The PSC formula already includes `failover_seconds + pod_startup_seconds` headroom. Defense-in-depth only.

None of these blocks convergence under the iter5 severity rubric.

---

## New findings (iter7)

### KIN-026. `lenny-ops-leader` Lease is absent from `ControllerLeaderElectionFailed` [Low]

**Section:** `spec/16_observability.md` §16.5 alert catalogue (`ControllerLeaderElectionFailed` condition); `spec/17_deployment-topology.md` §17.1 row for `lenny-ops` ("1–2 replicas with K8s Lease leader election (`lenny-ops-leader`)").

**Description.** The `ControllerLeaderElectionFailed` **Critical** alert condition names *only* the two pod-lifecycle controllers' leases (`lenny-warm-pool-controller`, `lenny-pool-scaling-controller`). `lenny-ops` was introduced as a mandatory control-plane component from Phase 3.5 onward and runs its own K8s Lease (`lenny-ops-leader`) per §17.1, but no alert covers a stale or un-renewed `lenny-ops-leader` lease. The `lenny_controller_leader_lease_renewal_age_seconds` gauge is described as "per controller", which in principle could include `lenny-ops`, but the alert rule itself filters on the two named controller leases — a `lenny-ops` leader that silently fails to renew (e.g., split-brain where the leader writes webhook-delivery/backup schedules while a second replica believes it is leader) will not page on-call. Because `lenny-ops` drives backup/restore scheduling, admin-API fan-out, and recommendations, a silent leader-election failure is operationally harmful but not a correctness defect in the pod-lifecycle plane.

**Severity rationale.** Low under the iter5 calibration — this is an observability gap on a non-pod-lifecycle component, not a control-plane correctness bug. The backup/restore path has its own per-Job alerting; the failure mode is "operator sees backups stop" rather than "data loss".

**Recommendation.** Extend `ControllerLeaderElectionFailed` to include the `lenny-ops-leader` lease (or add a paired `OpsLeaderElectionFailed` alert) and enumerate `lenny-ops` in the `lenny_controller_leader_lease_renewal_age_seconds` gauge's label set. If the alert expression is `max by (lease) (lenny_controller_leader_lease_renewal_age_seconds{lease=~"lenny-warm-pool-controller|lenny-pool-scaling-controller"})`, widen the regex to include `lenny-ops-leader`.

---

### KIN-027. `lenny.dev/host-schedulable` absence is treated as "unschedulable", but WPC applies the label at creation time only *after* scheduler binds the pod [Low]

**Section:** `spec/04_system-components.md` §4.6.1 "Host-node schedulability labeling" — the paragraph specifies "Pods newly created by WPC carry `lenny.dev/host-schedulable: \"true\"` by default (WPC applies the label at creation time after the scheduler binds the pod to a node); pods whose `spec.nodeName` is not yet set — i.e., still `Pending` at the scheduler — are not eligible for `task_cleanup → sdk_connecting` transitions, so the absence of the label on an unscheduled pod is never encountered by the gateway's precondition check." `spec/06_warm-pod-model.md` §6.2 "Host-node schedulability precondition" — "If the label reads `\"false\"` (or is absent, which is treated as unschedulable for fail-safe behavior), the pod transitions to `draining` instead".

**Description.** The claim that "absence of the label on an unscheduled pod is never encountered" is correct for the `task_cleanup → sdk_connecting` edge (only scheduled pods reach `task_cleanup`). But between the moment a pod is **scheduled** (`spec.nodeName` is set) and the moment WPC's next reconciliation runs and applies the label — a window bounded by reconcile latency, WPC rate limiters (20 QPS pod creation, 30 QPS status update), WPC leader-failover, and Node-informer cache warm-up — a newly-scheduled pod's label is absent. The "absent = unschedulable" fail-safe rule is only exercised at `task_cleanup → sdk_connecting`, not at `warming → sdk_connecting → idle`, so for Phase 3.5-era fresh creations there is no correctness issue. However, the window becomes observable during the **concurrent WPC leader failover + Node cordon** scenario: if the WPC leader crashes after creating a Pod but before labeling it, and the new leader's first reconciliation happens after a subsequent cordon on the host Node, the label may flip straight from `absent` to `false` without ever being `true` — idempotent and safe because of the fail-safe rule, but the claim in §4.6.1 that newly-created pods "carry `\"true\"` by default" is stronger than the actual invariant.

**Severity rationale.** Low — the fail-safe rule (`absent ⇒ unschedulable`) preserves correctness; the finding is that the spec *overstates* the post-creation invariant. No pod-placement or double-claim bug results.

**Recommendation.** Reword §4.6.1 to: "Pods newly created by WPC SHOULD carry `lenny.dev/host-schedulable: \"true\"` within one reconcile cycle of the scheduler binding; until the label is applied, the gateway's precondition check (which only inspects this label at `task_cleanup → sdk_connecting`) is guarded by the fail-safe rule in §6.2 that treats absence as unschedulable." Optionally surface the label-lag as a `lenny_host_schedulable_label_lag_seconds` gauge so operators can detect systemic WPC labeling delays independently of the reconciliation lag metric.

---

### KIN-028. Feature-gate downgrade (`true → false`) is declared "invalid" but has no enforcement mechanism [Medium]

**Section:** `spec/17_deployment-topology.md` §17.2 "Feature-gated chart inventory (single source of truth)": *"Flipping a flag from `true` to `false` after a phase has been reached is an invalid downgrade: the §16.5 per-webhook unavailability alerts and associated runtime enforcement paths depend on the webhook's continued presence."* §18 Phase 5.8 / Phase 8 / Phase 13 flip `features.llmProxy` / `features.drainReadiness` / `features.compliance` respectively from `false` to `true`.

**Description.** The downgrade prohibition is stated as a normative invariant but no machinery enforces it. The `lenny-preflight` Job computes its **expected** webhook set by union from the three flags' current values, so a deployer flipping a flag back to `false` on a subsequent `helm upgrade` produces a narrower expected set that *matches* the narrower rendered set — preflight passes, the webhook is uninstalled by Helm, and the per-webhook `*Unavailable` alert silently stops firing because its `PrometheusRule` template is gated on the same flag. The runtime enforcement paths that depended on the webhook (e.g., `lenny-direct-mode-isolation` enforcing `tenancy.mode: multi` + `deliveryMode: direct` + `isolationProfile: standard` rejection; `lenny-data-residency-validator` enforcing `dataResidencyRegion`; `lenny-drain-readiness` gating evictions on MinIO health) vanish on the next admission cycle — no alert fires, no chart validation fails, no `lenny-preflight` error appears. A downgrade is effectively an invisible security regression. The comparable guards elsewhere in the spec (§17.8.5 "rejects any attempt to disable `lenny-ops` at chart validation"; §11.7 compliance-profile-downgrade ratchet with `COMPLIANCE_PROFILE_DOWNGRADE_PROHIBITED`) do have active enforcement — the admission-plane feature flags do not.

**Severity rationale.** Medium under the iter5 calibration. This is a correctness/security regression surface that the spec prohibits but does not enforce; a production operator running `helm upgrade` with a mistakenly-reverted `values.yaml` silently weakens the admission plane. The remediation is low-cost (chart render-time validation against a previous-state marker or a ConfigMap-persisted phase stamp) but the current gap is a real regression, not a forward-compat hedge.

**Recommendation.** Add chart-install-time validation that reads a persisted `lenny-phase-stamp` (e.g., a ConfigMap written on each phase flip) and refuses a render where a flag transitions `true → false` with `FEATURE_FLAG_DOWNGRADE_PROHIBITED`; operators with a genuine need to remove a feature must explicitly `kubectl delete configmap lenny-phase-stamp` and confirm, matching the `POST /v1/admin/tenants/{id}/compliance-profile/decommission` pattern. Add an `AdmissionPlaneFeatureFlagDowngrade` **Warning** alert that fires on any observed `lenny_admission_webhook_inventory_flag` gauge transition from 1 to 0 without the corresponding operator-ack ConfigMap annotation. Cross-reference this enforcement row in the §17.2 "Feature-gated chart inventory" table.

---

### KIN-029. `lenny-preflight` feature-flag expected-set computation has no tamper-evidence against ConfigMap drift between Job ConfigMap and Helm render [Low]

**Section:** `spec/17_deployment-topology.md` §17.2 "Feature-gated chart inventory": *"The `lenny-preflight` Job reads the same three feature-flag Helm values (injected into the Job's ConfigMap at template time) and composes its expected set by union".*

**Description.** The expected-set / rendered-set parity is secured by the Phase 3.5 `admission_webhook_inventory_test.go` **CI** suite, not at runtime. If an operator rotates credentials by editing the Job's ConfigMap directly (`kubectl edit cm lenny-preflight-flags`) between `helm install` and the next `lenny-preflight` run — or if a stale Helm release's ConfigMap survives a `helm upgrade` due to a cross-release ConfigMap-resource-skew bug — the preflight Job may run with flag values that don't match the rendered template set. The `lenny-preflight` Job fails on the *absence* of an expected webhook but not on the *presence* of an unexpected one; a hand-edited ConfigMap that narrows the expected set while the chart renders the wider set produces a silent pass. The iter5 CI coverage (four-row table over flag combinations) validates the chart template but doesn't detect runtime ConfigMap drift.

**Severity rationale.** Low under the iter5 rubric — this is an operator-error-surface defense-in-depth gap, not a correctness bug in the chart or controllers. The same class as iter5 KIN-021.

**Recommendation.** Sign the `lenny-preflight` Job ConfigMap (e.g., include a `helm.sh/release-revision: $REVISION` annotation and a SHA-256 of the rendered `values.yaml` feature-flag subtree) at template time and have the Job verify the annotation matches `helm get values <release> -o yaml` before evaluating the inventory; abort with `PREFLIGHT_FLAG_CONFIGMAP_DRIFT` if they disagree.

---

### KIN-030. WPC bulk-UPSERT `agent_pod_state` reconciliation on leader acquisition interacts with the 20 QPS / 30 QPS API-server rate limiters [Low]

**Section:** `spec/04_system-components.md` §4.6.1 "WarmPoolController mirror reconciliation on recovery" (bulk-UPSERT keyed on `pod_id`); "API server rate limiting" (pod creation 20 QPS / burst 50; status update 30 QPS / burst 100).

**Description.** The mirror-reconciliation bulk UPSERT runs against Postgres, not the K8s API server, so it is not directly subject to the 20/30 QPS buckets. However, the paragraph says: "establishing a steady-state invariant: after any WPC outage, the mirror converges to the authoritative etcd state before the controller resumes incremental state-transition writes." At Tier 3 scale (pool sizes up to 15 000 pods per the §17.2 ResourceQuota floor), the **list phase** of `re-list all Sandbox resources` is a K8s API read — it does not consume the pod-creation or status-update buckets but it does consume the default-bucket (10 QPS / burst 100) shared with deletions and finalizer updates. A single `LIST sandboxes` with pagination over 15 000 entries easily exhausts the burst budget, starving finalizer updates during the same reconcile. Spec is silent on whether the list phase uses `resourceVersion=0` (which returns cached state from the API-server watch cache without a quorum read) — using default `resourceVersion` forces etcd round-trips and amplifies the post-crash etcd pressure precisely when etcd is most sensitive.

**Severity rationale.** Low — the iter5 spec explicitly accepts the 25 s failover window as the sizing basis; a slightly longer-than-documented mirror-reconciliation duration at Tier 3 is a performance concern within the already-accepted envelope, not a correctness defect. The rate-limiter interaction doesn't produce double-claims or orphan states; the fallback-claim mirror-staleness gate (`podClaimFallbackMaxMirrorLagSeconds: 10s`) already suppresses the stale-mirror double-claim path during recovery.

**Recommendation.** Specify that the mirror-reconciliation list phase MUST use `resourceVersion="0"` to read from the API-server watch cache, and document an upper bound on list latency in terms of pool size and the default-bucket rate limiter. Add a `lenny_warmpool_mirror_reconciliation_duration_seconds` histogram so operators can correlate reconciliation tail-latency with the `lenny_agent_pod_state_mirror_lag_seconds` gauge during post-crash recovery.

---

### KIN-031. PSC admission-denied exponential backoff resets on leader handoff — re-apply flood possible during rolling PSC updates [Low]

**Section:** `spec/04_system-components.md` §4.6.3 PSC admission-denial handling policy, item 2: *"Backoff is maintained in-memory by the leader; a leader handoff resets the counter (the new leader re-reads Postgres and may succeed if the underlying stale read has cleared, so starting from zero is the correct behavior on failover)."*

**Description.** The "reset on failover" rationale is sound for a crash-triggered failover (new leader has genuinely new information). For a **rolling update** of the PSC Deployment, however, leader handoff is orchestrated via `LeaderCallbacks.OnStoppedLeading` — handoff is clean and frequent (once per rolling update per replica), and the new leader reads the same Postgres state the old leader had. If a stuck-pool abort was approaching its ceiling (say, 8 consecutive denials out of 10) at the moment of handoff, the new leader starts at zero and may issue up to 10 more admission-denied applies before tripping the abort. In the worst case, a deployment-wide misconfiguration that trips the backoff uniformly across all pools, coupled with a slow rolling-update cycle, produces a burst of admission-denied applies at each handoff — observably amplifying `lenny_pool_scaling_admission_denied_total` and the `lenny-pool-config-validator` webhook load at every rollout.

**Severity rationale.** Low — the exponential backoff ceiling (60 s) still bounds per-pool apply rate, so this does not produce a hot-loop. The iter5 PoolScalingAdmissionStuck alert still fires after 10 consecutive denials on each leader term, so operators are not blind. This is a defense-in-depth / noise-reduction hedge, not a correctness bug.

**Recommendation.** On clean-shutdown leader handoff (via `LeaderCallbacks.OnStoppedLeading`), serialize the in-memory denial counter map into a `SandboxWarmPool.status.poolScalingBackoff` subresource (or a small `lenny-psc-backoff-state` ConfigMap in `lenny-system`), and have the new leader deserialize it on lease acquisition. Crash-triggered handoff continues to reset (correctness preserved). Add a sentence to §4.6.3 item 2 distinguishing clean-shutdown handoff (preserve state) from crash-triggered handoff (reset state) — matching the `sdkWarmCircuitBreaker.minOpenUntil` pattern established in iter4.

---

### KIN-032. SSA conflict retry policy "bounded retry with backoff" is specified but the cumulative deadline is not [Low]

**Section:** `spec/04_system-components.md` §4.6.3 SSA conflict retry policy, item 3: *"If a second 409 occurs (concurrent apply from the other controller), the controller backs off with jitter (initial 100ms, max 2s) and re-reads again. After 5 consecutive 409s without progress, the controller emits a `crd_ssa_conflict_stuck` structured log event ... and increments the `lenny_crd_ssa_conflict_total` counter, then continues with exponential backoff."*

**Description.** The retry policy specifies per-retry backoff (100 ms → 2 s) and logs/metrics after 5 consecutive failures, but "continues with exponential backoff" has no cumulative deadline. A pathological ownership-dispute scenario (e.g., a mis-authored operator tool that holds the same field manager name and repeatedly applies, or a bug that causes both WPC and PSC to believe they own the same carve-out) produces an indefinite retry loop per controller. The `CRDSSAConflictStuck` alert fires when the counter exceeds 10 in 5 minutes — that's observability coverage — but nothing *breaks the loop* or hands control back to a higher-level retry policy. Contrast with the PSC admission-denial handling (§4.6.3 item 3), which has a hard `admissionDeniedRetryCeiling: 10` after which the controller stops retrying that `(pool, crd)` tuple until operator intervention. SSA conflict retries have no analogous ceiling.

**Severity rationale.** Low — the work queue is bounded (default depth 500) and the controller's rate limiters (20 QPS pod creation, 30 QPS status) cap total API-server load, so a hot-retry loop cannot overwhelm the control plane. The rate limiters are the backstop, so the absence of an SSA-retry ceiling is a noise / operator-ergonomics concern rather than a correctness failure.

**Recommendation.** Add an `ssaConflictRetryCeiling` (default: 20) analogous to `admissionDeniedRetryCeiling`. After the ceiling, the controller stops retrying the offending apply, emits `CRDSSAConflictAborted` **Warning**, and waits for one of: (a) leader re-election, (b) operator-initiated `POST /v1/admin/crd/{name}/resume-reconciliation`, or (c) resource deletion. Update §4.6.3 item 3 with the ceiling and specify the escape conditions.

---

### KIN-033. Admission-webhook unavailability alert thresholds are inconsistent (30 s vs 5 min) across the 8-webhook set [Low]

**Section:** `spec/16_observability.md` §16.5 per-webhook alerts (lines 419–476): `AdmissionWebhookUnavailable` (30 s), `SandboxClaimGuardUnavailable` (30 s), `PoolConfigValidatorUnavailable` (30 s), `DataResidencyWebhookUnavailable` (30 s) — all Critical; versus `LabelImmutabilityWebhookUnavailable` (5 min), `DirectModeIsolationWebhookUnavailable` (5 min), `T4NodeIsolationWebhookUnavailable` (5 min), `DrainReadinessWebhookUnavailable` (5 min), `CrdConversionWebhookUnavailable` (5 min) — all Warning.

**Description.** The five-minute threshold webhooks are **Warning**-severity while the 30-second threshold webhooks are **Critical**. `spec/17_deployment-topology.md` §17.2 says *"All fail-closed webhooks (`failurePolicy: Fail`) — which includes every webhook in the list above — must maintain the 99.9% availability SLO; an availability drop below this threshold triggers the per-webhook unavailability alerts enumerated in §16.5"*. Every webhook in the set has `failurePolicy: Fail`, meaning unreachability blocks the gated operation. A `DrainReadinessWebhookUnavailable` of 5 min stalls all node drains and rolling updates during that window — operationally as severe as the 30 s Critical webhooks, arguably more so because it blocks a remediation path rather than a single write. The 5-min-Warning versus 30-s-Critical split is historical (the 30-s webhooks are older, per iter3 K8S findings) rather than principled.

**Severity rationale.** Low — this is an observability-calibration inconsistency, not a correctness defect. The `AdmissionWebhookUnavailable` (30 s Critical) fires for the *umbrella* webhook that catches all admission policies, so operators have at least one Critical-severity backstop on admission-plane outages.

**Recommendation.** Normalize the thresholds and severity: either (a) all 8 webhooks use 30 s Critical (favors faster pager wake-up; slightly more noise on brief flaps), or (b) apply a principled split — fail-closed webhooks that gate *new session creation* (`SandboxClaimGuard`, `PoolConfigValidator`, `DirectModeIsolation`, `DataResidency`) at 30 s Critical; fail-closed webhooks that gate *infrastructure operations* (`LabelImmutability`, `T4NodeIsolation`, `DrainReadiness`, `CrdConversion`) at 60 s–2 min Warning. Document the rationale in the §16.5 prose prelude.

---

### KIN-034. `lenny-t4-node-isolation` webhook phase assignment conflicts with iter5 Phase 13 deployment [Info]

**Section:** `spec/18_build-sequence.md` Phase 13 — *"Admission control enforcement — deploy `lenny-data-residency-validator` and `lenny-t4-node-isolation`"*; `spec/06_warm-pod-model.md` §6.4 "Pod Filesystem Layout" references T4 isolation as a v1 capability.

**Description.** T4 isolation (dedicated-node placement for `isolationProfile: t4`) is gated behind the `features.compliance` flag, which first flips to `true` at Phase 13 alongside `lenny-data-residency-validator`. T4 isolation has no data-residency coupling — it is a pod-placement hardening for high-blast-radius workloads that could be meaningful from Phase 3.5 onward. Bundling T4 isolation with `features.compliance` means early Phase 3.5–12 deployments cannot enable T4 isolation even when the underlying node pool is available, because the webhook template won't render. Conversely, a deployer who flips `features.compliance` to enable T4 isolation gets `lenny-data-residency-validator` with its more complex operational surface "for free".

**Severity rationale.** Info — this is a feature-coupling / packaging design concern, not a correctness or security bug. No runtime regression; no spec invariant violated.

**Recommendation.** Split `features.compliance` into two flags: `features.dataResidency` (gates `lenny-data-residency-validator`) and `features.t4Isolation` (gates `lenny-t4-node-isolation`). Update §17.2 Feature-gated chart inventory table, §18 Phase 13, and the `admission_webhook_inventory_test.go` table accordingly. This is explicitly outside the iter5 severity bar and is noted as an optional architectural-polish item.

---

### KIN-035. `PoolConfigValidator` unavailability now blocks PSC reconciliation — the "pool config updates stall" wording understates the downstream radius [Low]

**Section:** `spec/16_observability.md` §16.5 `PoolConfigValidatorUnavailable` — *"BOTH consequences apply simultaneously: (1) manual `kubectl edit` or `kubectl apply` updates to `SandboxTemplate.spec` and `SandboxWarmPool.spec` are denied ... AND (2) PoolScalingController SSA reconciliation writes are also denied because the semantic budget rules ... apply to every writer including the PSC. Pool configuration updates (both manual and reconciliation-driven) stall until the webhook recovers — operators should expect `PoolConfigDrift` to follow if the outage persists."* Severity: Warning.

**Description.** The consequence chain extends further: if the PSC cannot reconcile because the validator webhook is down, and the webhook outage persists past `scaleToZero` schedule boundaries (e.g., 06:00 resume-at), pools that should have scaled from `minWarm: 0` back to a positive value cannot transition — the PSC's scheduled `minWarm` write is rejected at admission. `WarmPoolLow` and `WarmPoolExhausted` will fire downstream, and pod-claim requests arriving during the scheduled resume window will hit the Postgres fallback path (itself gated on the `lenny-sandboxclaim-guard` webhook reachability). The `PoolConfigValidatorUnavailable` **Warning** severity does not convey this. §17.8.2 scaleToZero recovery guidance assumes the PSC can always write a `minWarm` adjustment when the schedule fires; the validator-unavailable path silently breaks that assumption.

**Severity rationale.** Low — the downstream `WarmPoolLow` / `WarmPoolExhausted` alerts still fire, so on-call is not blind. This is an observability-completeness gap (the severity of the root-cause alert understates the correlated production impact) rather than a correctness bug. The validator itself is deployed with the uniform HA contract (`replicas: 2` + PDB + 99.9% SLO), so a sustained outage is unlikely.

**Recommendation.** Elevate `PoolConfigValidatorUnavailable` to **Critical** when correlated with a scheduled `scaleToZero` transition (e.g., "Warning by default; Critical if any pool has an active `scaleToZero.resumeAt` within the next hour"). Alternatively, add a narrative bullet in the §16.5 alert description enumerating the `scaleToZero` resumption path as a second-order impact.

---

### KIN-036. `lenny.dev/webhook-name: drain-readiness` additive label is Exception 2 to NET-047/NET-050 but `lenny-preflight` audit treats *unreferenced* additive labels only as lint-level warnings [Low]

**Section:** `spec/17_deployment-topology.md` §17.2 — *"The additive label is permitted by Exception 2 of the NET-047/NET-050 canonical-selector invariant (§13.2) and MAY appear only in egress allow-lists paired with the canonical `lenny.dev/component: admission-webhook` key; it MUST NOT appear on the ingress side of any NetworkPolicy rule. Chart authors MUST NOT add per-webhook labels to the other seven webhooks unless a future egress-narrowing need is documented in §13.2 alongside a corresponding sub-rule; the `lenny-preflight` audit treats unreferenced additive labels as lint-level warnings."*

**Description.** The NET-068 Exception 2 carve-out is narrowly scoped (drain-readiness only, egress-only, additive to the canonical component label). The enforcement story splits:
- Ingress-side occurrence of the additive label is `MUST NOT`, but the sentence doesn't specify a preflight failure mode or an alert. "MUST NOT" without a detection mechanism is aspirational.
- Unreferenced additive labels on the *pod* side (additive label appears on a Deployment pod but no NetworkPolicy references it) are lint-level warnings. The iter5 spec explicitly says warning-only, not fail-closed.
- Referenced additive labels on the *NetworkPolicy* side for webhooks other than `drain-readiness` (i.e., a chart author adding `lenny.dev/webhook-name: sandboxclaim-guard` on both a webhook pod and a NetworkPolicy egress rule, without an accompanying §13.2 sub-rule) is what the "MUST NOT" targets, but no check surfaces this either.

**Severity rationale.** Low under iter5 calibration — the egress-narrowing invariant (the seven non-drain webhooks must not gain gateway-internal-port egress) is *positively* enforced by the single sub-rule in §13.2 granting that egress only to pods carrying `lenny.dev/webhook-name: drain-readiness`. A misconfigured additive label does not silently grant additional egress; it grants nothing unless a matching NetworkPolicy rule references it. The failure mode is "an operator adds an additive label, expecting it to do something, and it silently does nothing" — confusing but not a security regression.

**Recommendation.** Promote the unreferenced-additive-label check from lint-level warning to preflight failure. Add an ingress-side presence check that fail-closes on any `lenny.dev/webhook-name: *` key appearing in a NetworkPolicy `ingress.from.podSelector.matchLabels`. Document both in §17.9 "Admission webhook inventory" preflight check list.

---

### KIN-037. `SandboxWarmPool.status.sdkWarmCircuitBreaker` carve-out — PSC RBAC gains `get`/`patch` on `status`, but no audit-trail for operator override [Low]

**Section:** `spec/04_system-components.md` §4.6.3 field ownership table — row for `status.sdkWarmCircuitBreaker.*` (PSC-owned); RBAC paragraph: *"The PoolScalingController ServiceAccount has ... `get`/`patch` on the `status` subresource of `SandboxWarmPool` (required for SSA writes to the PSC-owned `status.sdkWarmCircuitBreaker` carve-out per the ownership table above)".* `spec/06_warm-pod-model.md` §6.1 — "The operator override (`circuitBreakerOverride: \"enabled\"`) unconditionally clears all three status fields".

**Description.** The operator override bypass (manually clearing the PSC's persisted circuit-breaker state to re-enable SDK-warm despite a recent trip) is a high-consequence policy exception — the breaker was tripped to prevent a cold-start cascade, and the override reverses that decision. The iter4 fix (KIN-018) established `minOpenUntil` so a crash can't silently re-enable SDK-warm, but an explicit operator override is the intentional escape hatch. The spec does not specify an audit event for this override, nor an alert. Compare with `LegalHoldOverrideUsed`, `LegalHoldOverrideUsedTenant`, `CompliancePostureDecommissioned` — all of which fire warnings / emit audit events because each is a "policy exception that must be reviewed". Setting `circuitBreakerOverride: "enabled"` has the same character but no corresponding observability.

**Severity rationale.** Low — circuit-breaker override is a pool-scoped capacity/availability lever, not a security or compliance decision. Misuse produces degraded cold-start latency, not data exposure. The absence of an audit event is an operational-review gap rather than a correctness failure.

**Recommendation.** Emit a `pool.sdk_warm_circuit_breaker_overridden` audit event (fields: `pool`, `override_by`, `previous_openedAt`, `previous_openedReason`, `justification`) on each observed transition of `spec.circuitBreakerOverride` to `"enabled"`, and add an `SdkWarmCircuitBreakerOverrideUsed` **Warning** alert that fires on event emission. Cross-reference this in §4.6.3 and §6.1.

---

### KIN-038. `lenny-sandboxclaim-guard` `failurePolicy: Fail` + `podClaimFallbackMaxMirrorLagSeconds: 10s` + Postgres-backed fallback — webhook reachability probe uses `GET /readyz`, which is not a health signal for the specific `ValidatingAdmissionWebhook` instance [Low]

**Section:** `spec/04_system-components.md` §4.6.1 "Fallback preconditions (mirror freshness and admission reachability)", item 2: *"The `lenny-sandboxclaim-guard` CREATE-time check (deployed with `failurePolicy: Fail`) is the only defense against the fallback path racing with a concurrent CRD-based claim for the same `Sandbox`. Because the webhook callback traverses the Kubernetes API server, the fallback requires the API server to be reachable for admission calls even when it is unreachable (or degraded) for the gateway's own watch/list operations. The gateway probes API server reachability before initiating the fallback (a lightweight `GET /readyz` or equivalent)".*

**Description.** `GET /readyz` on the API server tests *API server* reachability, not whether the `lenny-sandboxclaim-guard` Deployment's `ValidatingWebhookConfiguration` webhook endpoint is reachable from the API server. The two can diverge: the API server is up and `/readyz`-healthy, but the webhook Service has zero ready endpoints (e.g., both replicas are `CrashLoopBackOff`). In that scenario, the gateway passes the `/readyz` probe, initiates the fallback, creates the `SandboxClaim` CRD, the API server attempts to call the (unreachable) webhook, `failurePolicy: Fail` kicks in, the CREATE is denied — correct fail-closed behavior, but from the gateway's perspective this is an unexpected post-probe failure that the gateway treats as `WARM_POOL_EXHAUSTED`-path failure rather than a probe-gated skip. Result: the gateway's `lenny_pod_claim_fallback_skipped_total{reason=apiserver_unreachable}` counter does not increment (the probe passed); instead the fallback is *attempted* and fails mid-flight, potentially after allocating a row-lock in `agent_pod_state` via `SELECT ... FOR UPDATE SKIP LOCKED`. The spec does not say whether the row-lock is released on mid-flight failure; presumably the enclosing transaction is rolled back, but the gateway has burned one probe + one Postgres transaction + one admission-webhook call per fallback attempt during the outage.

**Severity rationale.** Low — the `SandboxClaimGuardUnavailable` **Critical** alert fires after 30 s of webhook unreachability, so the operator is not blind. The counter/log attribution is mildly misleading but the outcome (`WARM_POOL_EXHAUSTED` returned to client, retryable error) is correct. This is a diagnostic-fidelity / operator-ergonomics concern rather than a correctness defect.

**Recommendation.** Replace the `GET /readyz` probe with a more specific probe: either (a) `GET /apis/admissionregistration.k8s.io/v1/validatingwebhookconfigurations/lenny-sandboxclaim-guard` (API-server-gated, tests that the webhook config exists) combined with (b) an SNS-style health check on the webhook Service endpoints (via `lenny-preflight`'s cached inventory), or (c) a counter-based probe that tracks recent `lenny_sandboxclaim_guard_rejections_total` evaluations against API-server calls — a zero-rate over 30 s during otherwise-busy periods is a strong signal. Document in §4.6.1 that mid-flight admission failure during fallback is treated as `WARM_POOL_EXHAUSTED` with `reason=admission_denied`, and add a distinct `lenny_pod_claim_fallback_admission_denied_total` counter.

---

## Convergence assessment

**Counts (new findings this iteration):**
- Critical: 0
- High: 0
- Medium: 1 (KIN-028 — feature-flag downgrade enforcement gap)
- Low: 11 (KIN-026, KIN-027, KIN-029, KIN-030, KIN-031, KIN-032, KIN-033, KIN-035, KIN-036, KIN-037, KIN-038)
- Info: 1 (KIN-034)

**Verified carry-forward Lows (iter5):** KIN-021, KIN-022, KIN-023, KIN-024, KIN-025 — five carry-forwards remain accurate at Low severity.

**Verified fixed from iter4 (confirmed still in place):** KIN-015 (K8S-040), KIN-016 (K8S-041), KIN-017 (K8S-042), KIN-018 (K8S-043), KIN-019 (K8S-044), KIN-020 (K8S-045) — no regressions surfaced by iter7 spot-checks.

**Converged (Y/N):** **N** — one new Medium (KIN-028) represents a correctness-adjacent enforcement gap (admission-plane feature flag downgrade is declared prohibited but not enforced; a `helm upgrade` with a reverted `values.yaml` silently weakens the admission plane). The Low findings (KIN-026, KIN-027, KIN-029–KIN-038) and the Info finding (KIN-034) are all hedges, observability calibrations, or packaging-design observations that individually do not block convergence, but the Medium does.

**Rationale for severity calibration:**
- **Medium (KIN-028):** A fail-closed security-boundary webhook vanishing on operator mistake with no runtime-observable signal is a production regression, not a forward-compat hedge. It parallels iter4 K8S-040 (Phase 8 drain-readiness first-deploy gap, High when missed; Medium once packaging was correct but enforcement thin), applying the iter5 rubric: "correctness/security regression surfaces with no runtime detection" = Medium.
- **All Lows:** Forward-compat hedges, observability-completeness gaps, or defense-in-depth strengthening, matching the iter4/iter5 precedent (K8S-046 through K8S-050).
- **Info (KIN-034):** Architectural-polish observation on feature-flag packaging; does not claim a correctness or operability defect.

To reach convergence at iter8, the fix for KIN-028 should (a) add a persisted phase-stamp ConfigMap that chart-render-time validation reads to refuse `true → false` flag transitions, (b) add the `AdmissionPlaneFeatureFlagDowngrade` observation alert, and (c) cross-reference this enforcement in the §17.2 "Feature-gated chart inventory" table. The Low findings and Info finding are reasonable hardenings but non-blocking.
