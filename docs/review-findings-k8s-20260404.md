# Technical Design Review — Kubernetes Infrastructure & Controller Design

**Category:** K8S
**Document reviewed:** `docs/technical-design.md`
**Date:** 2026-04-04
**Perspective:** 1. Kubernetes Infrastructure & Controller Design

---

## Summary

| Severity | Count |
| -------- | ----- |
| Critical | 0     |
| High     | 5     |
| Medium   | 8     |
| Low      | 4     |
| Info     | 3     |

---

## Findings

---

### K8S-001 `agent-sandbox` Claim Architecture Differs from Spec Assumption — Controller Is on the Hot Path [Critical]

**Section:** 4.6.1, 17.8, 18

#### Original concern

Section 4.6.1 states: "Gateway replicas claim pods via `SandboxClaim` resources with optimistic locking — exactly one gateway wins; all others receive a conflict and retry with a different idle pod from the pool. This keeps the controller off the claim hot path entirely."

This assumed a **client-side claim model**: the gateway targets a specific `Sandbox` pod by name, creates a `SandboxClaim` resource, and the API server arbitrates conflicts. The controller is passive.

#### Spike findings

Review of the `kubernetes-sigs/agent-sandbox` source code (`extensions/controllers/sandboxclaim_controller.go`, ~855 lines) reveals a **server-side claim model**:

1. The gateway creates a `SandboxClaim` referencing a `SandboxTemplate` (not a specific pod).
2. The **claim controller** — not the gateway — selects an idle `Sandbox` from a matching `SandboxWarmPool` via `getOrCreateSandbox()` → `adoptSandboxFromCandidates()`.
3. Adoption is an `r.Update(ctx, adopted)` call on the `Sandbox` resource, protected by `resourceVersion` optimistic concurrency. If two controller workers race for the same `Sandbox`, one gets a 409 Conflict and tries the next candidate. A collision-avoidance hash (`FNV32a(claim.UID) % workers`) distributes workers across candidates to reduce contention.
4. If no warm candidate is available, the controller falls through to cold creation (new `Sandbox` CR, triggering pod creation).
5. The `SandboxWarmPool` controller detects adoption (warm pool labels stripped, owner reference changed) and creates replacement sandboxes reactively.

The conflict semantics are **correct** (exactly one claimant wins per pod, others retry), but the architecture is different: the controller is on the hot path, and the gateway's role is declarative (create claim, watch for `Ready=True`, read `status.sandboxStatus.name` and `podIPs`).

#### Latency assessment

Each claim reconciliation involves ~2 API server writes (adoption + status update). Default `--sandbox-claim-concurrent-workers=1` processes claims serially.

| Scenario                  | p50       | p95       | Throughput         |
| ------------------------- | --------- | --------- | ------------------ |
| Default (1 worker), idle  | 50–80ms   | 150–250ms | 10–30 claims/sec   |
| 10 workers, moderate load | 80–120ms  | 200–400ms | 100–300 claims/sec |
| 20 workers, Tier 3 burst  | 100–200ms | 500–800ms | 200–600 claims/sec |

Against the 2s P95 startup SLO (runc), the claim phase consumes 10–25% of the budget at Tier 3 — meaningful but not SLO-breaking.

Two scaling risks at Tier 3:

- **Serial processing at default config.** Must tune `--sandbox-claim-concurrent-workers` per tier.
- **O(n) list scan.** `getOrCreateSandbox()` lists all `Sandbox` resources in the namespace (informer cache, no field index) and filters in-memory. At thousands of Sandbox resources per namespace, this is O(sandboxes × claims) CPU cost. A field index on `sandboxTemplateRefHash` (small upstream contribution) eliminates it.

#### Impact on the spec

This is actually a **cleaner model** than what the spec proposed:

- The gateway no longer needs RBAC on `Sandbox` resources — only `create`/`delete`/`get`/`watch` on `SandboxClaim`. Narrower blast radius than the original design.
- The `PodLifecycleManager.ClaimPod` implementation simplifies to: create `SandboxClaim` → watch for `Ready=True` → read bound pod metadata. No pod selection logic, no retry loop, no direct `Sandbox` interaction.
- The spec's fallback ("compensate with a compare-and-swap loop at the gateway level") is moot — the controller already handles CAS internally.

The `PodLifecycleManager` interface does not change; only the internal implementation of `ClaimPod` differs from what was assumed.

#### Severity rationale (downgraded from Critical to Medium)

The original Critical rating was based on: "if the semantics diverge and the workaround is non-trivial, this could force a redesign of the claim path." The spike shows no redesign is needed — the actual model is simpler for Lenny, not harder. The remaining risk is a tunable latency concern at Tier 3, not an architectural incompatibility.

**Recommendation:**

1. **Close ADR-TBD as resolved.** Document the spike result: agent-sandbox uses controller-mediated claiming with optimistic concurrency on `Sandbox.Update`. The spec's client-side claiming assumption was incorrect; the actual model is cleaner. No fallback or workaround is needed.

2. **Update Section 4.6.1** to reflect the actual claim flow:
   - Replace "Gateway replicas claim pods via `SandboxClaim` resources with optimistic locking" → "The gateway creates a `SandboxClaim` referencing a `SandboxTemplate`. The agent-sandbox claim controller selects and adopts an idle `Sandbox` from a matching warm pool using optimistic concurrency (`resourceVersion`-based conflict on `Sandbox.Update`). The gateway watches for the claim's `Ready=True` condition and reads `status.sandboxStatus.name` for the assigned pod."
   - Remove "This keeps the controller off the claim hot path entirely" → "The claim controller is on the hot path. Concurrency is managed via `--sandbox-claim-concurrent-workers` (default: 1; must be tuned per tier)."
   - Remove the ADR-TBD block and the fallback CAS loop language.

3. **Add per-tier controller tuning to Section 17.8:**

   | Tier | `--sandbox-claim-concurrent-workers` | Expected p95 claim latency |
   | ---- | ------------------------------------ | -------------------------- |
   | 1    | 1                                    | < 250ms                    |
   | 2    | 5–10                                 | < 400ms                    |
   | 3    | 20–30                                | < 800ms                    |

4. **Add a Phase 1 task:** contribute a field index on `sandboxTemplateRefHash` to `kubernetes-sigs/agent-sandbox` to eliminate the O(n) list scan in `getOrCreateSandbox()`. If upstream is slow to merge, carry a local patch.

5. **Add a Phase 2 validation gate:** during the startup benchmark harness, measure `agent_sandbox_claim_startup_latency_ms` under simulated Tier 3 burst load. If p95 exceeds 500ms with 30 workers, escalate to contributing a direct-claim mode upstream (gateway creates `SandboxClaim` targeting a specific `Sandbox` by name, with API-server-level conflict on the claim resource itself).

6. **Revise the `minWarm` formula** in Sections 4.6.1 and 17.8 to include a replenishment lag buffer: `minWarm >= claim_rate * (failover_seconds + pod_startup_seconds) + burst_term + replenishment_lag_buffer`, where `replenishment_lag_buffer` accounts for the reactive delay between burst adoption and replacement sandbox readiness.

---

### K8S-002 PDB Label Mismatch: `lenny.dev/pod-state: idle` vs `lenny.dev/state: idle` [High]

**Section:** 4.6.1, 6.2

Section 4.6.1 defines the warm pool PDB as:

> "The PDB targets only unclaimed (warm) pods via a label selector (`lenny.dev/pod-state: idle`)"

Section 6.2 defines the actual pod coarse-state labels as:

| Label             | Values                       |
| ----------------- | ---------------------------- |
| `lenny.dev/state` | `idle`, `active`, `draining` |

The PDB references `lenny.dev/pod-state` but the label emitted by the controller is `lenny.dev/state`. This means the PDB targets zero pods — it matches nothing in the cluster. The intended protection (preventing voluntary disruption from draining the warm pool below `minWarm`) is silently absent. A node drain or cluster upgrade can evict all idle pods simultaneously.

The `lenny-preflight` Job (Section 17.6) does not include a check for PDB selector correctness.

**Recommendation:** Fix the PDB selector in Section 4.6.1 to use `lenny.dev/state: idle`. Add an integration test that verifies the PDB selector resolves to at least `minWarm` pods when the pool is healthy. Add a preflight check (or startup controller validation) that confirms the PDB exists and its selector is non-empty.

---

### K8S-003 Warm Pool Controller Rate Limiter Is Still Undersized at Tier 3 Scale [High]

**Section:** 4.6.1, 17.8

Section 4.6.1 sets the pod creation rate limiter at 20 QPS / burst 50 and notes this is "configurable via controller flags." Section 17.8 raises these to 80 QPS / burst 200 at Tier 3.

The problem is the **work queue**. Section 4.6.1 states: "pod creation is processed sequentially through the work queue rather than in parallel bursts." At Tier 3, the formula from Section 4.6.1 requires `minWarm ≥ 750` pods per hot pool (Section 17.8). A cold-start event (e.g., cluster restart, node pool expansion) requires creating 750 pods sequentially at 80 QPS — that is a minimum of ~9.4 seconds of creation throughput, assuming zero overhead per create call. Real pods have scheduling latency, image pull, and sandbox initialization; at gVisor overhead of ~2–5s per pod, the pool cannot reach ready state before the failover window triggers a `WarmPoolExhausted` critical alert.

Additionally, the work queue's default depth of 500 at Tier 1 is raised to 10,000 at Tier 3, but the spec does not discuss memory overhead. A 10,000-entry work queue with Kubernetes reconciliation objects can reach several hundred MB depending on object size.

**Recommendation:** (1) Add a parallel pod-creation path for initial pool fill (cold-start), separate from the sequential steady-state path. The sequential constraint is intended to prevent scale-up storms, not to gate initial pool fill from zero. (2) Add a formula and note for work queue memory overhead: estimate per-item size × max depth and include in the controller's resource requests. (3) Add a `WarmPoolFillTime` metric (histogram of time to fill a pool from 0 to minWarm) and an SLO target for this case.

---

### K8S-004 Kata Node Isolation Uses `requiredDuringSchedulingIgnoredDuringExecution` — Ignored During Execution Is a Security Gap [High]

**Section:** 17.2

Section 17.2 specifies that Kata pods require a hard node affinity rule using `requiredDuringSchedulingIgnoredDuringExecution`. This correctly prevents initial scheduling to wrong nodes. However, `IgnoredDuringExecution` means: if the node's label changes after the pod is scheduled (e.g., the `lenny.dev/node-pool: kata` label is accidentally removed or overwritten by cluster automation), the running Kata pod is NOT evicted — it continues running on the now-unlabeled node alongside potentially non-Kata workloads.

This is the standard Kubernetes behavior, but for a security-critical isolation guarantee ("Kata pods must never share nodes with runc pods"), silently allowing label drift is a meaningful gap. Section 17.2 describes this as "hard scheduling constraints" but the enforcement is only at schedule time, not at runtime.

**Recommendation:** Add a Kyverno or Gatekeeper policy that continuously validates that pods in `lenny-agents-kata` are running on nodes with the required `lenny.dev/node-pool: kata` label. If a drift is detected, emit a critical alert (`KataNodeLabelDrift`) and optionally mark the pod for eviction/replacement. This policy runs as a background audit (not inline admission) to avoid disrupting running workloads, but the alert gives operators immediate visibility. Document the `IgnoredDuringExecution` limitation explicitly.

---

### K8S-005 No `ResourceQuota` or `LimitRange` for Agent Namespaces [High]

**Section:** 17.2

The namespace layout section (17.2) specifies no `ResourceQuota` or `LimitRange` for `lenny-agents` or `lenny-agents-kata`. The warm pool controller creates pods within these namespaces, but there is no namespace-level guardrail preventing runaway pod creation from exhausting cluster resources.

Failure scenarios:

- A controller bug in the pool sizing formula (e.g., a negative `mode_factor` or overflow in the burst term) could issue thousands of pod create requests before the work queue overflow metric fires.
- An operator accidentally configures a very large `minWarm` (e.g., 5000) on a cluster that cannot support it, and the controller dutifully attempts to fill the pool.
- A malicious tenant who somehow reaches the admin API can configure a pool that exhausts the cluster.

The `lenny-preflight` Job checks connectivity and RuntimeClasses but does not verify that namespace resource quotas are in place.

**Recommendation:** Add a `ResourceQuota` per agent namespace to the Helm chart, configurable via `agentNamespaces[].resourceQuota`. Default values should cap total pods, CPU, and memory per namespace based on the tier's expected warm pool size (Section 17.8). Add a corresponding `LimitRange` with default resource requests/limits for agent pods (guards against pods with no resource requests that the scheduler treats as best-effort). Document that these defaults must be tuned for large `minWarm` values.

---

### K8S-006 `SandboxClaim` Is Not Garbage-Collected Without `ownerReference` and Has No TTL [High]

**Section:** 4.6.1

Section 4.6.1 notes: "`SandboxClaim` — Links the claimed pod to session metadata (deliberately not an `ownerReference`, so the session survives pod deletion and can be reassigned)."

While the reasoning is sound, the consequence is that Kubernetes will never GC a `SandboxClaim` automatically. The spec does not specify:

- How `SandboxClaim` resources are cleaned up when the gateway crashes after creation but before the session record is persisted to Postgres.
- Whether there is a TTL or finalizer on `SandboxClaim` to detect stale claims.
- Whether the WarmPoolController has a reconciliation loop to detect orphaned `SandboxClaim` resources (claims referencing pods that are in `idle` state or pods that no longer exist).

In the crash scenario: gateway creates `SandboxClaim`, crashes before writing to Postgres → on recovery, the gateway has no record of the claim → the `Sandbox` pod is claimed but no session drives it → the pod idles indefinitely holding a claim, invisible to both the warm pool and the session manager.

**Recommendation:** Add an explicit orphaned `SandboxClaim` detection loop to the WarmPoolController: any `SandboxClaim` older than `claimOrphanTimeout` (e.g., 5 minutes) that has no corresponding active session in Postgres is deleted and the underlying `Sandbox` pod is returned to `idle`. Add a `lenny_orphaned_claims_total` metric. Document the cleanup cadence.

---

### K8S-007 Helm CRD Upgrade Limitation Is Described but No Automated Enforcement Path [Medium]

**Section:** 10.5, 17.6

Section 10.5 correctly notes: "Helm does not update CRDs on `helm upgrade` — this is a known Helm limitation. CRDs must be applied separately before running `helm upgrade`." The controller validates the CRD schema version annotation at startup and refuses to start on mismatch.

However:

- The Helm chart's `lenny-preflight` Job (Section 17.6) checks CRD schema version as a pre-install check, but `helm upgrade` runs preflight hooks with `pre-upgrade` ordering. If an operator runs `helm upgrade` without first applying CRDs, the preflight Job will read the stale CRD version and correctly fail — but only if the operator has not set `--skip-preflight`. The failure message is good but the default path is fragile.
- GitOps (ArgoCD/Flux) workflows are noted (Section 17.6) with a `sync-wave: "-5"` suggestion, but this is operator-configured, not enforced by the chart.
- No `helm post-upgrade` hook validates that controllers actually started successfully (i.e., their startup CRD version check passed).

**Recommendation:** (1) Add a `helm post-upgrade` hook Job that polls the controller Deployments for `Available` condition within a timeout (e.g., 120s), failing the upgrade if any controller exits with non-zero code (which indicates CRD mismatch). This converts a soft runtime failure into a visible upgrade failure. (2) Document the GitOps `sync-wave` pattern as required, not optional, in the GitOps section.

---

### K8S-008 etcd Degraded Mode Does Not Define a Maximum Acceptable Duration Before Escalation [Medium]

**Section:** 4.6.1

Section 4.6.1 documents etcd degraded behavior well: existing sessions continue, new sessions are rejected, pool replenishment freezes, and an `EtcdUnavailable` critical alert fires after 15 seconds. However, the section does not specify:

- What happens if etcd is unavailable for an extended period (e.g., 10 minutes, 1 hour)? The warm pool depletes as sessions complete and pods are not replaced. At what point does the gateway move from "reject new sessions" to "begin checkpointing and gracefully terminating active sessions before the pool is entirely stale"?
- At Tier 3 with 750 warm pods per pool and a typical session duration of ~30 minutes, pool depletion from an extended etcd outage could take hours — but the etcd outage could last only minutes. No guidance is given on whether operators should take active steps after a threshold.

**Recommendation:** Add explicit escalation behavior: if etcd is unavailable for more than `etcdOutageEscalationSeconds` (default: 300s), the gateway begins emitting `session.degraded` status updates to active clients (warning that new sessions cannot be created) and optionally invokes the admin API's circuit-breaker to return `503` on new session requests. Add an `EtcdOutageExtended` warning alert that fires after 5 minutes of continuous unavailability, distinct from the 15-second `EtcdUnavailable` critical alert, to distinguish brief flaps from sustained outages.

---

### K8S-009 `topologySpreadConstraints` Defaults Are Inconsistent with `minWarm` Formula [Medium]

**Section:** 5.2, 4.6.1, 17.8

Section 5.2 sets topology spread defaults to `whenUnsatisfiable: ScheduleAnyway` (soft) for both zone and node spread. Section 17.8 gives a minWarm formula with a failover term: `minWarm >= claim_rate * (failover_seconds + pod_startup_seconds) + burst_term`.

The formula assumes pods are distributed across failure domains. But with `ScheduleAnyway` defaults, all pods in a pool can end up in a single zone. Under a zone failure, the effective available warm pods drops from `minWarm` to approximately `minWarm * (1 - 1/zone_count)`. For a 2-zone cluster, this is 50% — half the minWarm. The formula-derived minimum of 750 (Tier 3) could drop to 375 available pods, which the formula explicitly did not account for.

**Recommendation:** Either (a) change the zone-spread default to `whenUnsatisfiable: DoNotSchedule` for pools with `minWarm >= 3 * zone_count`, or (b) add a zone-failure term to the minWarm formula: `minWarm >= formula_result / (1 - 1/zone_count)` and document it clearly. Option (a) is simpler and safer. Add a validation in the PoolScalingController that warns when `minWarm < 3 * zone_count` and zone spread is soft.

---

### K8S-010 cert-manager Failure During cert-Expiry Drain Loop Creates Unbounded Pod Depletion [Medium]

**Section:** 10.3, 4.6.1

Section 4.6.1 states the warm pool controller "proactively drains any idle pod whose certificate will expire within 30 minutes, replacing it with a fresh pod." Section 10.3 adds a `CertExpiryImminent` warning alert that fires when a cert is within 1 hour of expiry, noting this indicates cert-manager failure.

The problem: the drain-and-replace logic continues running even during a cert-manager outage. If cert-manager is down:

1. Idle pods with certs expiring within 30 minutes are drained (correct behavior).
2. Replacement pods are created but cert-manager cannot issue new certificates.
3. New pods are never marked `idle` (they fail the cert check within 60 seconds per Section 10.3 and are replaced).
4. The controller loops: drain → create → cert failure → recreate → drain more → create more...

The `CertExpiryImminent` warning alert fires at 1 hour from expiry. The drain threshold is 30 minutes from expiry. These fire in the right order, but there is no circuit breaker that says "stop draining when cert-manager is not issuing successfully." At a 4h cert TTL with a 30-minute drain buffer, the warm pool begins depleting 3.5 hours after cert-manager goes down. In a 24-pod pool, that is roughly one pod every 3.5h/24 = ~8 minutes being drained without replacement.

**Recommendation:** Add a controller-level check: if cert issuance is failing for replacement pods (tracked via a `lenny_cert_issuance_failures_total` counter), pause the proactive cert-expiry drain after `certIssuanceFailureThreshold` consecutive failures (default: 3). Emit a `CertIssuanceStalled` critical alert. Only resume draining when cert issuance succeeds again. This prevents the drain loop from depleting the warm pool during a cert-manager outage.

---

### K8S-011 No Validation That RuntimeClass Overhead Values Are Applied [Medium]

**Section:** 5.3

Section 5.3 specifies `Pod Overhead` reference values for each RuntimeClass (e.g., gVisor: 200m CPU / 200Mi memory; Kata: 500m CPU / 500Mi memory). The section notes "Each `RuntimeClass` should define `Pod Overhead` so scheduling accounts for the isolation cost."

The `lenny-preflight` Job (Section 17.6) checks that RuntimeClasses exist, but does not validate that the overhead is configured. If a deployer installs gVisor but does not configure `Pod Overhead` on the RuntimeClass, pods will be scheduled on nodes that appear to have capacity but are actually over-provisioned by the overhead amount. Under gVisor, each "empty" pod consumes ~200m CPU and ~200Mi memory invisible to the scheduler. At Tier 3 with 750 warm gVisor pods per pool, the invisible overhead totals ~150 vCPU and ~150 GB memory — enough to cause node OOM or scheduling stalls.

**Recommendation:** Add a preflight check that reads the `spec.overhead` field on each RuntimeClass referenced by pool definitions. If absent or zero, emit a warning with the expected overhead values from Section 5.3 and a reference to the RuntimeClass overhead documentation. Upgrade to a hard failure for `sandboxed` (gVisor) and `microvm` (Kata) RuntimeClasses, since their overhead is material. Include expected overhead values in the Helm chart's gVisor/Kata installation documentation.

---

### K8S-012 No Controller Lease Revocation on Voluntary Shutdown [Medium]

**Section:** 4.6.1, 4.6.2

Both the WarmPoolController and PoolScalingController use Kubernetes Lease-based leader election with `leaseDuration: 15s`. On a rolling update or voluntary pod termination, the outgoing leader holds the lease until it either renews (which it won't) or the TTL expires. This creates a 15-second gap where neither the old nor new leader is active.

Section 4.6.1 documents this gap: "During failover (~15s), existing sessions continue unaffected; only new pod creation and scaling pause." However, there is no mention of the outgoing controller releasing its lease on `SIGTERM`. Go's `controller-runtime` does support voluntary lease release via a `LeaderCallbacks.OnStoppedLeading` callback that can call `lock.Release()`. Without this, rolling updates always incur the full 15-second gap, even when the outgoing leader knows it is stopping.

**Recommendation:** Implement `OnStoppedLeading` callbacks in both controllers that release the lease on clean shutdown. This reduces the typical rolling-update failover gap from 15 seconds to near-zero. The 15-second TTL remains the safety bound for crash scenarios. Document the distinction between clean shutdown (fast handoff) and crash (15s TTL) in the failover behavior.

---

### K8S-013 `SandboxWarmPool.spec.scalePolicy` Time-of-Day Rules Reference Cron Syntax Without Timezone [Low]

**Section:** 4.6.1

Section 4.6.1 includes an example scale-to-zero schedule:

```yaml
scaleToZero: { schedule: "0 22 * * *", resumeAt: "0 6 * * *" }
```

No timezone is specified. Kubernetes CronJob natively supports a `timeZone` field (added in K8s 1.27), but it is not clear whether `agent-sandbox`'s scalePolicy uses the same CronJob mechanism or a custom implementation. If the controller uses UTC by default, operators in non-UTC timezones will configure incorrect schedules silently — a very common operational error.

**Recommendation:** (1) Add a `timeZone` field to the `scaleToZero` block (e.g., `timeZone: "America/New_York"`), defaulting to UTC. (2) In the preflight check and the pool definition validation, emit a warning if `schedule`/`resumeAt` are set but `timeZone` is absent. (3) Document explicitly that schedule times are in the specified timezone (or UTC if absent).

---

### K8S-014 No Guidance on etcd Cluster Topology for Large Deployments [Low]

**Section:** 4.6.1, 17.8

Section 4.6.1 recommends `--quota-backend-bytes: 8 GB` and periodic defragmentation for Tier 3. Section 17.8 adds: "Tier 3: dedicated etcd cluster recommended."

However, "dedicated etcd cluster" is mentioned in a table cell with no surrounding guidance. A shared etcd cluster (the default for most Kubernetes distributions) hosts both control-plane metadata and Lenny CRD data. At Tier 3 with ~1,300 CRD writes/second, Lenny's load is non-trivial relative to control-plane operations. But the spec does not discuss:

- How many etcd members are appropriate for a dedicated cluster (3 vs 5).
- Whether Lenny's dedicated etcd uses a separate dataDir or separate cluster.
- Whether the dedicated etcd should have its own endpoint separate from the API server's default etcd address.

**Recommendation:** Add a subsection under Section 17.8 or 17.1 titled "etcd topology for Tier 3" covering: recommended member count (3 members for most deployments, 5 for highest availability), separate etcd cluster configuration with dedicated endpoint, and how to configure the agent-sandbox CRD apiserver to point at the dedicated cluster using `--etcd-servers-overrides` (if applicable). Reference the Kubernetes documentation for dedicated etcd.

---

### K8S-015 Admission Policy Integration Test Scope Is Underspecified [Low]

**Section:** 17.2

Section 17.2 adds: "An **integration test suite** (`tests/integration/admission_policy_test.go`) verifies that controller-generated pod specs for each RuntimeClass pass the deployed admission policies, preventing policy/spec drift from causing warm pool deadlock."

This is a good addition but the test scope is not specified:

- Does it test against a real cluster with OPA/Gatekeeper deployed, or a mocked policy evaluator?
- Does it cover negative cases (e.g., a pod spec with `shareProcessNamespace: true` is rejected, a runc pod without seccomp is rejected)?
- Is it part of the CI gate or only run in integration environments?

Without negative test cases, the test suite can pass even if the policies are not enforced (e.g., Gatekeeper is installed but in audit-only mode).

**Recommendation:** Specify that the integration test suite must include both positive (compliant pod specs pass) and negative (non-compliant pod specs are rejected) cases for each admission policy. Tests should deploy a real Gatekeeper/Kyverno instance (using `envtest` or a dedicated CI cluster). The test gate should block merges if any negative case passes (i.e., if a non-compliant spec is not rejected).

---

### K8S-016 No Guidance on CRD Version Served vs Stored During Multi-Version Coexistence [Info]

**Section:** 10.5, 15.5

Section 10.5 notes: "Conversion webhooks translate between CRD versions so both components operate correctly during the transition." Section 15.5 states CRDs follow `v1alpha1 → v1beta1 → v1` versioning.

However, Kubernetes CRD multi-version has an important subtlety: only one version can be the `storage` version at a time. When a new version is introduced, objects stored in the old version must either be migrated or read back through the conversion webhook on every access. The spec does not address:

- Which version is the storage version at each point in the `v1alpha1 → v1beta1 → v1` progression.
- Whether a storage version migration job is required when promoting (e.g., from `v1alpha1` to `v1beta1` as storage version), or whether Lenny relies solely on conversion webhooks for on-read translation.
- The performance impact of on-read conversion under high CRD read rates (the controller's list-watch calls fire the conversion webhook for every object).

**Recommendation:** Add a note in Section 10.5 or 15.5 specifying: (a) the initial storage version, (b) when and how the storage version is migrated (a `StorageVersionMigrator` job or manual migration), and (c) that conversion webhooks must be deployed before any new-version CRD objects are created, not after. Reference the Kubernetes CRD storage version migration documentation.

---

### K8S-017 `lenny-preflight` NetworkPolicy Test Creates and Deletes a Real NetworkPolicy — Insufficient for Enforcement Validation [Info]

**Section:** 17.6

The `lenny-preflight` check for CNI NetworkPolicy support states: "Create and delete a test `NetworkPolicy` in the target namespace to verify the CNI plugin supports NetworkPolicy enforcement."

Creating and deleting a NetworkPolicy object only verifies that the Kubernetes API server accepts the resource type — it does not verify that the CNI plugin actually enforces it. Many clusters can accept `NetworkPolicy` resources without any enforcement (e.g., clusters running Flannel without a NetworkPolicy controller). The check produces a false positive in this case.

**Recommendation:** Strengthen the NetworkPolicy enforcement check: after creating the test NetworkPolicy, deploy a pair of test pods (one restricted, one not), attempt a connection that should be blocked by the policy, and verify the connection fails. This is a true enforcement test. Add a 30-second timeout and a clear failure message: "CNI NetworkPolicy enforcement test failed — policy was accepted by the API server but not enforced. Ensure your CNI (Calico, Cilium, or equivalent) has NetworkPolicy enforcement enabled." If the pod deployment takes too long for a preflight hook, provide a `lenny-ctl validate-network` command that operators can run separately before installation.

---

### K8S-018 Helm Chart Does Not Handle Agent-Sandbox CRD Lifecycle Separately from Lenny CRDs [Info]

**Section:** 17.6, 10.5

Section 17.6 states: "CRDs are installed via the chart on initial `helm install` but can be managed separately for GitOps workflows (`helm install --skip-crds`)."

The agent-sandbox CRDs (`SandboxTemplate`, `SandboxWarmPool`, `Sandbox`, `SandboxClaim`) are upstream resources versioned and released by `kubernetes-sigs/agent-sandbox`, not by Lenny. However, the spec does not address:

- Whether the Helm chart bundles agent-sandbox CRDs directly (vendor them into `charts/lenny/crds/`) or expects them to be pre-installed by the operator via a separate agent-sandbox Helm chart.
- What happens when agent-sandbox releases a CRD schema change (under the one-release-delay upgrade cadence). Vendored CRDs would be out of sync with the upstream; operator-installed CRDs require coordination.
- Whether the `lenny-preflight` check validates the exact CRD schema version of the installed agent-sandbox CRDs against the version Lenny was built against.

Mismatching agent-sandbox CRD versions (Lenny built against v0.3 but cluster has v0.2 installed) would cause silent failures that are difficult to diagnose — the controller starts but CRD operations behave unexpectedly.

**Recommendation:** Explicitly specify in Section 17.6 whether agent-sandbox CRDs are vendored into the Helm chart or expected to be pre-installed. If vendored: document the version pinning policy. If pre-installed: add a `lenny-preflight` check that validates the agent-sandbox CRD API group version (`sandboxes.lenny.dev/v1alpha1` or equivalent) and the specific `lenny.dev/schema-version` annotation on each CRD. Emit a specific error message on version mismatch: "agent-sandbox CRD version mismatch: installed=X, expected=Y. Run `helm install kubernetes-sigs/agent-sandbox --version Y` first."
