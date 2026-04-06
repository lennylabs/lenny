# Technical Design Review Findings — Operator & Deployer Experience
# Perspective: 7. Operator & Deployer Experience

**Document reviewed:** `docs/technical-design.md`
**Date:** 2026-04-04
**Reviewer perspective:** OPS — Operator & Deployer Experience
**Focus:** Evaluate the experience of deploying and running Lenny in production: initial setup, day-2 operations, configuration management, upgrade paths, and local development story.

---

## Findings Summary

| Severity | Count |
|----------|-------|
| Critical | 2 |
| High | 5 |
| Medium | 8 |
| Low | 5 |
| Info | 3 |

---

## Critical Findings

### OPS-001 CRD Upgrades Require Out-of-Band Manual Step That Helm Cannot Enforce [Critical] — VALIDATED/FIXED
**Section:** 10.5, 17.6

The spec correctly identifies that Helm does not update CRDs on `helm upgrade` and provides the mitigation: apply CRDs manually before upgrading, with controller startup validation to detect stale CRDs. However, the spec positions this as a documented procedure rather than a workflow-enforced constraint. An operator who runs `helm upgrade` without the preceding `kubectl apply -f charts/lenny/crds/` step will see controllers start, fail their CRD version check, crash-loop, and surface a `FATAL` log message. This is a correct detection mechanism but a poor operational experience: the upgrade partially applies before the crash is noticed, Helm marks the release as deployed successfully (because Helm's success criterion is "all non-CRD resources applied"), and the operator must manually recover.

Concretely: Section 17.6 says "Run `helm upgrade`" as step 2 and controllers "will refuse to start if CRDs are stale." The preflight Job (`lenny-preflight`) does check for stale CRDs. But the preflight Job runs as a pre-install/pre-upgrade hook — meaning it runs before the upgrade attempt. A deployer who has old CRDs and runs `helm upgrade` will have the preflight Job fail with an informative message, which does block the upgrade. That is the correct behavior. What the spec does **not** cover: what happens when the preflight Job is skipped (`preflight.enabled: false`) in a production upgrade, which is documented as allowed for "air-gapped or constrained environments." In that case the partial-upgrade failure scenario described above applies. Additionally, the GitOps path (ArgoCD sync-wave "-5") is described but the ordering between CRD sync-wave and controller Deployments depends on ArgoCD sync-wave configuration that the chart cannot enforce — a misconfigured ArgoCD application drops back to the broken upgrade path.

**Recommendation:** (1) Document the partial-upgrade failure mode explicitly in the upgrade runbook so operators know what to look for if it happens. (2) Add a section to the CRD upgrade runbook covering recovery steps: how to detect that CRDs were not applied before an upgrade, how to apply them retroactively without downtime, and how to restart the affected controller Deployments. (3) For the ArgoCD path, provide a tested example `Application` manifest showing the correct sync-wave configuration, not just a prose description. (4) Make `preflight.enabled` default to `true` even in upgrades (it already is) and add a stern warning in documentation that disabling preflight in production upgrades risks partial-upgrade failures.

**Resolution:** Section 17.6 now includes: (1) a detailed "CRD upgrade procedure" block with the exact required sequence (apply CRDs first, then `helm upgrade`), marked as **required reading before every upgrade**. (2) A "Recovery procedure for stale CRDs after a failed upgrade" with 5 concrete steps covering symptom identification, correct CRD application, controller restart, verification, and rollback as a last resort. (3) GitOps sync-wave configuration is marked as **not optional** with a clear statement that ArgoCD/Flux must use a separate sync wave for CRDs. (4) A `lenny-crd-validate` post-upgrade hook Job that polls controller Deployments for the `Available` condition within 120s, converting a soft runtime failure into a visible upgrade failure. (5) The CRD upgrade failure runbook is listed in Section 17.7 with a cross-reference to the detailed recovery steps in Section 17.6.

---

### OPS-002 No Runbook for the CRD Finalizer Stuck Scenario [Critical] — VALIDATED/FIXED
**Section:** 4.6.1, 17.7

Section 4.6.1 describes a `FinalizerStuck` alert that fires when a `Sandbox` resource remains in `Terminating` state for more than 5 minutes because the warm pool controller did not remove its finalizer (`lenny.dev/session-cleanup`). The spec states: "Operators can then investigate and manually remove the finalizer once they have confirmed the session state is safe."

This is a production incident scenario with real user impact (a stuck pod cannot be freed; the warm pool loses a slot permanently until recovery). The spec instructs operators to "investigate and manually remove the finalizer" but provides no guidance on how to do this safely. The `FinalizerStuck` alert is referenced in Section 16.5 but is absent from the runbook list in Section 17.7. The consequences of removing the finalizer prematurely (while a session is still partially active) are not described — specifically whether that can corrupt session state, lose artifacts, or create a race with the gateway.

This is categorized Critical because a stuck finalizer degrades warm pool capacity and the only documented recovery action is "manually remove the finalizer" without a safe procedure.

**Recommendation:** Add a `FinalizerStuck` runbook to Section 17.7 covering: (1) how to diagnose whether the session referenced by the stuck `Sandbox` is actually terminated (check `SandboxClaim`, session state in Postgres, gateway logs), (2) the exact `kubectl patch` command to remove the finalizer with an explanation of what Kubernetes will do after removal, (3) what state to verify before and after removal to confirm the session artifact seal-and-export completed (check MinIO and Postgres), and (4) when it is safe to force-remove even without confirmation.

**Resolution:** Section 17.7 now includes a full "Stuck finalizer remediation" runbook (`docs/runbooks/stuck-finalizer.md`) with 7 concrete steps: (1) identify the stuck pod via `kubectl get sandbox` filtering on `Terminating` state, (2) check for an active `SandboxClaim` — do not remove the finalizer if a claim exists, (3) verify session state in Postgres confirming terminal state or recent checkpoint, (4) verify no in-flight artifacts remain in MinIO, (5) exact `kubectl patch` command to remove the `lenny.dev/session-cleanup` finalizer, (6) post-removal verification that the pod is deleted and warm pool replenishes within 2 minutes, (7) root cause investigation with common causes (leader election gap, API server throttling). The runbook includes guidance on when force-removal is safe even without confirmation, and specifies filing an incident if stuck finalizers recur on >2 pods within 24h.

---

## High Findings

### OPS-003 Bootstrap Plane vs. Operational Plane Split Incomplete — Credential Pool Secrets Require Kubernetes Secrets [High]
**Section:** 15.1, 4.9, 17.6

The spec defines a clean bootstrap/operational plane split: bootstrap infrastructure (DB URLs, Redis, MinIO, KMS, cert paths) is Helm-only; operational config (runtimes, pools, delegation policies, credential pools) is admin-API-managed. However, the credential pool configuration in Section 4.9 references Kubernetes Secrets directly:

```yaml
credentialPools:
  - name: claude-direct-prod
    credentials:
      - id: key-1
        secretRef: lenny-system/anthropic-key-1  # K8s Secret reference
```

This `secretRef` is a Kubernetes Secret reference baked into credential pool definitions that are otherwise admin-API-managed. This means: (1) changing a credential (rotating an API key) requires both updating a Kubernetes Secret **and** updating the credential pool definition in the admin API — two out-of-band operations that must stay consistent. (2) Deployers using GitOps expect Kubernetes Secrets to be managed via sealed secrets or external-secrets-operator, but the admin API is the source of truth for the pool definition. There is no documented workflow for keeping these in sync. (3) The bootstrap seed mechanism (Section 17.6) can seed credential pool definitions but cannot create Kubernetes Secrets — the operator must create the secrets separately before the bootstrap job runs.

**Recommendation:** (1) Add a documented workflow for the credential lifecycle: create the K8s Secret → register the credential pool via admin API (or bootstrap seed), and for rotation: update the K8s Secret → the Token Service picks up the new value (describe the mechanism — does the Token Service watch the secret? Is a pool reload required?). (2) Clarify whether the Token Service watches the referenced K8s Secrets for changes or requires an explicit reload/notification. If a reload is required, document the admin API call for it. (3) For GitOps users, describe the recommended pattern for managing credential secrets (e.g., external-secrets-operator populating `lenny-system/anthropic-key-*` from a vault, combined with the bootstrap seed for pool definitions).

---

### OPS-004 Expand-Contract Migration Requires Three Separate Deployments — No Guidance on Timeline or Minimum Cadence [High]
**Section:** 10.5

The spec correctly requires expand-contract schema migrations: Phase 1 (add new columns, deploy code writing both old and new), Phase 2 (switch reads to new schema), Phase 3 (drop old columns). Each phase is "a separate migration file and a separate deployment." This is sound practice, but the spec provides no guidance on:

- **Minimum time between phases.** Can Phase 2 follow Phase 1 immediately? Or must the operator wait for all in-flight requests to drain before switching reads? For a platform with 2h sessions (`maxSessionAge: 7200s`), a session that started during Phase 1 may have persisted records in the old schema. Reading them via Phase 2 code requires the old column to still exist — meaning Phase 3 (dropping old columns) cannot happen until all records written with the old schema have either been migrated or expired. There is no guidance on calculating this window.
- **What to do if a migration is interrupted.** Section 10.5 states "Each migration step should be idempotent where possible" but does not address what "possible" means — which steps are inherently non-idempotent (e.g., `ALTER TABLE ... DROP COLUMN` after partial application)?
- **Mixed-version pod coexistence.** During a rolling deploy, old and new gateway replicas coexist and both write to Postgres. If the Phase 1 migration adds a non-nullable column, old replicas will fail their INSERTs. The spec does not address this: all new columns must be nullable until Phase 3 removes the old column. This constraint is implied but not stated.

**Recommendation:** Add an "Expand-contract operation guide" subsection in Section 10.5 covering: (1) the rule that new columns must be nullable until old code no longer runs, (2) the minimum inter-phase wait as a function of `maxSessionAge` (or whatever the maximum record age is for the affected table), (3) idempotency requirements per step (ALTER TABLE ADD COLUMN IF NOT EXISTS is idempotent; DROP COLUMN is not), (4) a worked example showing an actual migration through all three phases with timeline.

---

### OPS-005 No Operational Guidance for Adjusting etcd Tuning on a Live Cluster [High]
**Section:** 4.6.1

Section 4.6.1 provides detailed etcd operational tuning: compaction settings, defragmentation schedule, quota monitoring, snapshot frequency. However, all guidance is written as initial configuration. There is no guidance on:

- How to apply compaction settings (`--auto-compaction-mode`, `--auto-compaction-retention`) to a running etcd cluster without downtime — these are etcd startup flags, not runtime-adjustable parameters.
- How to perform defragmentation on a running etcd without disrupting Kubernetes API availability — `etcdctl defrag` on a primary blocks writes while defrag runs.
- What the recovery procedure is if etcd hits its quota limit (`--quota-backend-bytes`) before the operator notices — etcd enters alarm state and refuses all writes, which means the Kubernetes API server becomes read-only for all CRD operations. The `EtcdQuotaNearLimit` alert (80%) gives some lead time, but the recovery procedure (defrag + `etcdctl alarm disarm`) is not described.

At Tier 3, etcd defrag is scheduled "every 12h off-peak" — on a production cluster running 10,000 concurrent sessions, "off-peak" may be hard to define.

**Recommendation:** Add an etcd operations runbook to Section 17.7 covering: (1) the `etcdctl defrag --cluster` command and when to use cluster-wide vs single-member defrag, (2) how to safely change compaction settings on a managed Kubernetes cluster (where etcd flags are set by the cloud provider), (3) the recovery procedure if etcd enters alarm state including `etcdctl alarm disarm` and the required preconditions, (4) the on-call escalation path for etcd-related incidents that cannot be resolved at the Lenny operator level (e.g., cloud-provider etcd unavailability).

---

### OPS-006 Warm Pool Sizing Formula Requires Metric Data Not Available at First Deployment [High]
**Section:** 4.6.2, 17.8

The PoolScalingController formula in Section 4.6.2 requires `base_demand_p95`, `burst_p99_claims`, and `pod_warmup_seconds` as inputs. The formula in Section 17.8 similarly requires `peak_claims_per_second` and `burst_p99_claims`. These are metrics derived from historical traffic — they are not available at first deployment. The spec acknowledges this for task mode (`mode_factor` falls back to 1.0 during cold start) but does not address the general case: what value should an operator use for `minWarm` before any traffic has been observed?

Section 17.8 provides per-tier recommended `minWarm` values (15, 125, 750), but these are presented without explanation of the assumptions behind them. An operator deploying at Tier 1 for the first time with no traffic history has no way to evaluate whether 15 is appropriate for their specific workload.

**Recommendation:** (1) Add a "First deployment sizing" subsection to Section 17.8 explaining that the formula requires historical metrics and providing a conservative starting recommendation that does not depend on observed traffic (e.g., `minWarm = tier_baseline` as a safe default until traffic data is available). (2) Describe the monitoring workflow for the first week: which metrics to watch (`lenny_warmpool_idle_pod_minutes`, `WarmPoolLow` alert threshold), how to know if `minWarm` is too low (sustained `WarmPoolLow` warnings) or too high (high `lenny_warmpool_idle_pod_minutes`), and how often to recalculate. (3) Add a note that the PoolScalingController cannot auto-configure `minWarm` for a new pool because it has no demand signal — the initial value must always be set manually.

---

### OPS-007 `docker compose up` Dev Mode Lacks mTLS — Real LLM Credentials May Be Used Without TLS [High]
**Section:** 17.4

Section 17.4 explicitly states that Tier 2 (`docker compose up`) runs "no mTLS (plain HTTP)." This is a reasonable dev default, but the spec also describes a "zero-credential mode" using the echo runtime and then says "real LLM providers" can be tested "from Phase 6 onward." The danger is that a developer iterating on credential leasing (Phase 5.5+) may use `docker compose up` with real LLM credentials configured, and those credentials flow over plain HTTP between the gateway and the "agent pod" container.

For a security-conscious development team or a CI environment that runs integration tests with real API keys, plain HTTP may be unacceptable. The spec provides `LENNY_DEV_TLS=true` as an option that generates self-signed certificates, but this requires `LENNY_DEV_MODE=true` and is described only for "adapter authors who need to test TLS behavior" — not positioned as the recommended mode for credential testing.

**Recommendation:** (1) Add a note in Section 17.4 explicitly warning that `docker compose up` (Tier 2) uses plain HTTP and should not be used with real LLM credentials unless `LENNY_DEV_TLS=true` is set. (2) Update the docker-compose profile for credential testing (`docker compose --profile credentials up` or similar) to enable `LENNY_DEV_TLS=true` by default. (3) Document how to generate and trust the self-signed certificates that `LENNY_DEV_TLS=true` produces so that the API client in the developer's machine or CI system can verify them.

---

## Medium Findings

### OPS-008 Runbook List (Section 17.7) Is Missing Several High-Impact Failure Scenarios [Medium]
**Section:** 17.7

Section 17.7 defines the minimum required runbooks. The list covers seven scenarios. Several high-impact scenarios mentioned in the spec body are absent:

- **etcd quota alarm / API server read-only mode** (described in Section 4.6.1 — `EtcdQuotaNearLimit`, `EtcdUnavailable` alerts, but no runbook)
- **Token Service unavailability** (Section 4.3 describes the failure behavior in detail — new credential-dependent sessions fail — but no runbook)
- **KMS key rotation** (Section 10.5 describes the 4-step procedure but it is buried in the upgrade section, not referenced from Section 17.7)
- **PgBouncer pool mode misconfiguration detected post-install** (Section 12.3 describes the `lenny-preflight` check, but if a deployer accidentally changes PgBouncer to session mode on a running deployment, there is no runbook for detecting and fixing it without downtime)
- **CRD finalizer stuck / warm pool slot leaked** (see OPS-002)
- **Audit SIEM connectivity failure** (Section 11.7 describes fail behavior — `/healthz` goes degraded — but no runbook)
- **Dual-store unavailability (Redis + Postgres both down)** (Section 10.1 describes the degraded mode in detail but no runbook)

**Recommendation:** Extend the runbook list in Section 17.7 to include the above scenarios. For each, at minimum specify: the triggering alert, the immediate triage steps (what to look at first), the remediation steps, and the rollback/recovery path if remediation fails.

---

### OPS-009 Operational Defaults Table (Section 17.9) Does Not Distinguish Helm Defaults from CRD Defaults [Medium]
**Section:** 17.9

Section 17.9 provides a quick-reference table of operational defaults. The table lists values like "Artifact retention TTL: 7 days" and "Pod cert TTL: 4h" but does not indicate how each default is set. Some are Helm chart defaults (overridable at install time), some are CRD field defaults (set in the `SandboxTemplate`), some are gateway config defaults (loaded from environment variables or config maps), and some are compile-time constants in the runtime adapter.

An operator wanting to change "Max idle time" from 600s to 1200s needs to know: is this a Helm value, an admin API call, a CRD field update, or a gateway restart? The table provides no answer. The "Reference" column points to spec sections (e.g., `§11.3`) where the default is defined, but those sections describe the behavior, not the configuration mechanism.

**Recommendation:** Add a "Configuration mechanism" column to the Section 17.9 defaults table with values such as: `Helm value`, `Admin API — Runtime resource`, `Admin API — Pool resource`, `Helm value (global)`, `CRD field (SandboxTemplate)`. This allows operators to immediately know where to go to change a default without cross-referencing each section.

---

### OPS-010 No Guidance on Monitoring Stack Setup — Alerts Reference Metrics Without Describing How to Install Them [Medium]
**Section:** 16.1, 16.5

Section 16.1 lists ~40 Prometheus metrics. Section 16.5 defines critical and warning alert rules. However, neither section describes how to install the monitoring stack. Questions unanswered:
- Are alert rules shipped as Prometheus Operator `PrometheusRule` CRDs in the Helm chart, or must operators define them manually?
- Are Grafana dashboards provided as ConfigMaps or as a separate Helm dependency?
- For custom metrics used by HPA (`lenny_gateway_active_streams`, `lenny_gateway_request_queue_depth`), must the operator install the Prometheus Adapter separately, or is it a chart dependency?

Section 17.4 mentions "Tier 2 includes optional observability containers: Prometheus, Grafana, Jaeger" but only for dev mode. The spec does not address the production observability stack installation.

**Recommendation:** Add a subsection "Monitoring Stack Installation" to Section 17.6 (Packaging and Installation) covering: (1) whether Prometheus Operator CRDs are expected to already exist in the cluster (and minimum version), (2) whether alert rules are included in the Helm chart as `PrometheusRule` resources, (3) whether Grafana dashboards are bundled (as ConfigMaps or via grafana-operator), (4) the required Prometheus Adapter configuration for HPA custom metrics, and (5) whether KEDA is an alternative to the Prometheus Adapter and what the chart provides for it.

---

### OPS-011 Rolling Upgrade of Agent Pools Requires Admin API Calls Not Documented in the Upgrade Procedure [Medium]
**Section:** 10.5, 17.6

Section 10.5 describes the pool rotation upgrade procedure: deploy a new `SandboxTemplate`, wait for new warm pods, set old pool's `minWarm` to 0, drain, delete old template. This is the correct approach. However, the spec says `SandboxTemplate` CRDs are derived state — the **source of truth is Postgres, managed by the admin API** (Section 4.6.2: "CRDs become derived state reconciled from Postgres by PoolScalingController"). This means the pool rotation procedure in Section 10.5 — which describes it as "Deploy new `SandboxTemplate` CRD with updated image" — is inaccurate. Operators cannot manually create `SandboxTemplate` CRDs; they will be overwritten by the PoolScalingController on the next reconciliation cycle. The actual procedure must go through the admin API (`POST /v1/admin/pools` to create the new pool, `PUT /v1/admin/pools/{name}/warm-count` to zero out the old pool's minWarm).

The validating webhook (Section 4.6.3) explicitly rejects manual `kubectl edit` or `kubectl apply` updates to `SandboxTemplate.spec` that don't carry the controller annotation — confirming that operators cannot follow the Section 10.5 procedure as written.

**Recommendation:** Rewrite the pool rotation upgrade procedure in Section 10.5 to use admin API calls instead of CRD operations. The corrected procedure: (1) `POST /v1/admin/pools` to register the new pool with updated runtime image, (2) observe new warm pods starting via `GET /v1/pools`, (3) run canary traffic, (4) `PUT /v1/admin/pools/{old-pool}/warm-count` with `minWarm: 0`, (5) wait for old pool to drain (monitor via `GET /v1/pools`), (6) `DELETE /v1/admin/pools/{old-pool}`. Add the `lenny-ctl` commands for each step if a CLI is provided.

---

### OPS-012 `make run` Local Mode Uses SQLite — Behavior Differences with Postgres Not Documented [Medium]
**Section:** 17.4

Tier 1 (`make run`) uses embedded SQLite instead of Postgres. SQLite and Postgres have significant behavioral differences that can mask bugs:
- SQLite does not enforce Row-Level Security (RLS) — the central multi-tenancy mechanism. A bug in `SET LOCAL app.current_tenant` handling will never surface in `make run`.
- SQLite has different locking semantics (file-level locks vs. row-level locks), different transaction isolation defaults, and does not support all PostgreSQL-specific SQL syntax.
- SQLite does not support PgBouncer `connect_query` sentinel validation.

A developer testing multi-tenancy logic with `make run` will see no errors that Postgres RLS would catch. Section 17.4 mentions SQLite as a replacement but does not warn about these behavioral gaps.

**Recommendation:** Add a "Known behavioral differences in Tier 1 (`make run`)" subsection to Section 17.4 explicitly listing: (1) RLS is not enforced in SQLite — multi-tenancy logic must be tested with Tier 2, (2) SQL syntax differences that may cause queries to succeed in SQLite but fail in Postgres, (3) locking behavior differences. Recommend that contributors working on multi-tenancy, quota enforcement, or session store logic always use Tier 2 (`docker compose up`) rather than Tier 1.

---

### OPS-013 No Defined Process for Adding New RuntimeClasses to a Running Production Cluster [Medium]
**Section:** 5.3, 17.2

Section 5.3 documents three isolation profiles (runc, gVisor, Kata) and notes that the warm pool controller validates RuntimeClass existence at startup and sets pools to `Degraded` if the referenced RuntimeClass is missing. The `lenny-preflight` Job also checks for required RuntimeClasses.

What is not documented: how to add a new RuntimeClass (e.g., enabling gVisor on a cluster that started with runc only) without disrupting the running platform. Questions: Does the preflight Job need to re-run? Does the warm pool controller automatically recover from `Degraded` when the RuntimeClass appears? (The spec says "the controller logs an error and sets the pool's status to Degraded" — it does not say the controller continuously polls for the RuntimeClass to appear.) Is there a node pool creation dependency (Kata requires dedicated nodes)? Adding gVisor to an existing cluster requires installing the gVisor containerd shim on all nodes and updating the `RuntimeClass` object — this is a cluster-level operation outside Lenny's Helm chart.

**Recommendation:** Add an "Adding a new isolation profile" operational procedure to Section 17.7 (or a new Section 17.10) covering: (1) cluster-level prerequisites for each RuntimeClass (gVisor: install `runsc` shim on all agent nodes; Kata: provision dedicated node pool with hardware virtualization support), (2) creating the Kubernetes `RuntimeClass` object, (3) verifying that the warm pool controller detects the new RuntimeClass and recovers affected pools from `Degraded`, (4) creating the new pool definition via the admin API.

---

### OPS-014 No Capacity Planning Guidance for the Token Service Separate from the Gateway [Medium]
**Section:** 4.3, 17.8

Section 4.3 describes the Token Service as a separate Deployment with 2+ replicas. Section 17.8 provides per-tier recommendations for gateway replicas, warm pool sizing, Postgres, and Redis — but the Token Service is absent from the capacity tier reference. The Token Service is in the hot path for credential leasing: every new session that requires LLM credentials calls the Token Service. At Tier 3 (200 sessions/second creation rate), the Token Service must handle at least 200 credential lease requests per second — potentially more if rotation events are frequent.

The spec does not provide: Token Service replica counts per tier, CPU/memory sizing, rate limiter settings, or any indication of the Token Service's throughput characteristics.

**Recommendation:** Add Token Service sizing to the Section 17.8 capacity tier reference table, including: replica counts per tier, resource requests/limits, and an estimate of requests-per-second (credential leases at session creation + renewal requests). Also document the Token Service's interaction with the KMS service: at high throughput, KMS signing calls may become a latency or quota bottleneck — this should be noted with a reference to KMS rate limit configuration.

---

### OPS-015 `lenny-restore-test` CronJob Described But Not Included in Helm Chart Explicitly [Medium]
**Section:** 17.3

Section 17.3 describes the `lenny-restore-test` CronJob in detail: it creates a temporary Postgres instance, verifies schema integrity, runs a smoke query, emits metrics, and tears down. This is excellent operational hygiene. However, the spec says "Restore testing recommended monthly (configurable by deployer via CronJob schedule)" — the word "recommended" and "configurable" suggest this CronJob ships in the Helm chart. Section 17.6, which describes the Helm chart contents, does not mention the restore test CronJob.

If the CronJob is not shipped in the Helm chart, operators must implement it themselves, which most will not do. If it is shipped, it requires permissions to create temporary Postgres instances (either in-cluster StatefulSets or cloud-managed), which is a non-trivial RBAC and infrastructure concern not addressed anywhere.

**Recommendation:** Clarify in Section 17.6 whether the `lenny-restore-test` CronJob is included in the Helm chart. If yes: add it to the chart description, document the required RBAC (what ClusterRole does the CronJob's ServiceAccount need to spin up a temporary Postgres?), and describe the available `restoreTest.schedule` Helm value. If no: remove the detailed description from Section 17.3 and replace with a link to a community example or provide a standalone manifest in the repository.

---

## Low Findings

### OPS-016 `lenny-ctl` CLI Scope Is Underspecified [Low]
**Section:** 17.6, 21.8

Section 17.6 mentions `lenny-ctl bootstrap` and `lenny-ctl preflight` commands. Section 21.8 describes `lenny-ctl` as a "thin client" over the admin API. However, the CLI's scope is never defined: which commands exist beyond `bootstrap` and `preflight`? Does it provide session management (`lenny-ctl sessions list`), pool management (`lenny-ctl pools drain`), or runtime management? Without a CLI command reference, operators default to raw `curl` calls to the admin API, which is error-prone and provides no discoverability.

**Recommendation:** Add a "CLI Command Reference" stub to Section 17.6 (or as a separate documentation section) listing the v1 CLI commands with their admin API equivalents. Even a table of command → API endpoint would help operators understand the intended CLI surface. Mark post-v1 commands as "planned."

---

### OPS-017 Preflight Check for PgBouncer `connect_query` Is Fragile [Low]
**Section:** 17.6

The `lenny-preflight` Job checks that PgBouncer's `connect_query` contains `SET app.current_tenant`. The check is described as "Verify `connect_query` contains `SET app.current_tenant` sentinel." This string match is fragile: a deployer might write `SET app.current_tenant = '__unset__'` vs. `SET "app.current_tenant" = '__unset__'` vs. `SET local app.current_tenant = '__unset__'` — all equivalent SQL but different strings. The check might also false-positive if the connect_query contains `SET app.current_tenant` as part of a comment or a longer variable name.

**Recommendation:** Replace the string-match check with a behavioral test: have the preflight Job open a direct connection through PgBouncer, immediately execute a query that would fail RLS without the sentinel (e.g., `SELECT current_setting('app.current_tenant', false)` and verify it returns `__unset__`), and treat any result other than `__unset__` as a configuration failure. This tests actual behavior rather than configuration format.

---

### OPS-018 No Guidance on Log Aggregation Stack Selection or Configuration [Low]
**Section:** 16.4

Section 16.4 mentions that "deployers should configure an external log aggregation stack (ELK, Loki, CloudWatch, etc.) for long-term retention beyond the Postgres window" but provides no further guidance. At Tier 3, log volume is ~3 GB/day. The choice of log aggregation stack has significant operational and cost implications. Lenny's structured JSON logs are directly compatible with most stacks, but there are Lenny-specific considerations: the `session_id`, `tenant_id`, `trace_id`, `span_id` correlation fields are load-bearing for incident investigation — any log aggregation configuration that doesn't index these fields loses the primary debugging workflow.

**Recommendation:** Add a short "Log aggregation integration" note to Section 16.4 that: (1) lists the mandatory indexed fields (`session_id`, `tenant_id`, `trace_id`), (2) provides a recommended log parsing rule (or Fluent Bit / Logstash filter configuration) that extracts these fields from JSON logs, and (3) estimates log volume per tier so operators can make retention vs. cost decisions.

---

### OPS-019 No Documentation for Multi-Region Deployment Operations [Low]
**Section:** 12.8

Section 12.8 describes a multi-region reference architecture with one Lenny control plane per region and a global load balancer routing tenants to their region. This is a significant operational undertaking. The spec provides the architecture but no operational guidance: how are runtime definitions synchronized across regions? (The spec says each control plane is independent — does that mean operators must register the same runtime twice?) How is the global load balancer configured to route by tenant? (The spec mentions "a global load balancer" and "a lightweight global catalog" but neither is specified.)

**Recommendation:** Either (a) expand Section 12.8 to include a minimal operational checklist for a two-region deployment (what must be configured in each region, what the global catalog contains and how it is populated, and which admin API calls are per-region vs. global), or (b) explicitly defer multi-region operations to post-v1 and remove the architecture description to avoid misleading operators who might attempt it.

---

### OPS-020 Section 17.9 Deployment Profiles Not Integrated into Preflight Checks [Low]
**Section:** 17.6, 17.9

Section 17.9 defines two deployment profiles: `cloud-managed` and `self-managed`. The preflight Job (Section 17.6) checks for PgBouncer-specific configuration (pool mode, connect_query), but cloud-managed deployments use the provider's connection proxy (RDS Proxy, Cloud SQL Auth Proxy) rather than PgBouncer. The preflight Job as described would fail on a valid cloud-managed deployment that uses RDS Proxy without a PgBouncer, even though RDS Proxy supports transaction-mode pooling natively.

**Recommendation:** Make the preflight checks profile-aware: when `deploymentProfile: cloud-managed`, the PgBouncer-specific checks (pool mode, connect_query) are replaced by equivalent checks on the provider proxy (e.g., verify the RDS Proxy endpoint responds and that `SET LOCAL app.current_tenant` works correctly through it). The preflight Job should read `deploymentProfile` from the Helm values and branch accordingly.

---

## Info Findings

### OPS-021 Section 17.9 Cloud SQL Auth Proxy Limitation Is Easy to Miss [Info]
**Section:** 17.9

Section 17.9 (cloud-managed profile) notes in a table cell that "Cloud SQL Auth Proxy terminates IAM auth but does not pool — if using Cloud SQL, deploy PgBouncer or pgcat alongside it, or use AlloyDB with built-in pooling." This is a significant operational gotcha (Cloud SQL users may not realize they still need PgBouncer) buried in a dense table. GCP is a major cloud target. A deployer who reads "cloud-managed = no PgBouncer to operate" and then picks Cloud SQL will end up without connection pooling and hit Postgres connection exhaustion under load.

**Recommendation:** Promote this note to a visible callout box or a dedicated "Cloud SQL users" subsection in Section 17.9, explicitly stating that Cloud SQL with Cloud SQL Auth Proxy is not a sufficient connection pooling solution and requires PgBouncer or AlloyDB.

---

### OPS-022 `WarmPoolIdleCostHigh` Alert Threshold Is Deployer-Configured but No Default Is Provided [Info]
**Section:** 4.6.1

Section 4.6.1 describes the `WarmPoolIdleCostHigh` warning alert that fires "when idle pod-minutes exceed a deployer-configured threshold over a 24h window." No default threshold value is provided — unlike every other alert in Section 16.5, which has a concrete condition. An operator who installs Lenny without configuring this threshold gets no alert for idle cost accumulation.

**Recommendation:** Provide a formula for a sensible default threshold, e.g., `idle_pod_minutes_threshold = minWarm * 24h * 60 * cost_warning_factor` where `cost_warning_factor` defaults to 2.0 (alert when idle minutes exceed twice the theoretical minimum). Document this in Section 4.6.1 and Section 16.5.

---

### OPS-023 Observability in Dev Mode Mentions Jaeger but Section 16.3 Configures OTLP Collector [Info]
**Section:** 17.4, 16.3

Section 17.4 says dev mode (Tier 2) "includes optional observability containers: Prometheus, Grafana, Jaeger." Section 16.3 says "The trace pipeline uses OpenTelemetry Collector; the platform emits OTLP traces and the collector handles sampling, batching, and export." In dev mode, the spec says "with a local Jaeger instance (or stdout exporter for `make run`)." The relationship between the OTel Collector and Jaeger in dev mode is not described: does dev mode skip the OTel Collector and send OTLP directly to Jaeger? Or does it include an OTel Collector configured to export to Jaeger? These have different implications for testing the OTel Collector configuration (e.g., sampling rules).

**Recommendation:** Clarify in Section 17.4 the dev mode trace pipeline: does it use an OTel Collector (configured to forward to Jaeger) or does the gateway send OTLP directly to Jaeger? If the latter, note that sampling rules configured via OTel Collector are not exercised in dev mode.

---

## Summary of Must-Check Items

| Must-Check Item | Finding | Assessment |
|----------------|---------|------------|
| Bootstrap vs operational plane split (Helm-only vs API-managed) | OPS-003 | **Gap**: Credential pool secrets require K8s Secrets management outside the clean API plane split; no documented workflow for keeping them in sync. |
| Operational runbooks — sufficient for common failure scenarios? | OPS-002, OPS-008 | **Gap**: `FinalizerStuck` scenario has no runbook despite being the documented recovery for a pool slot leak. Multiple high-impact scenarios (etcd alarm, Token Service failure, dual-store outage, KMS rotation) missing from the runbook list. |
| Two-tier local dev mode (`make run` vs `docker compose`) — realistic? | OPS-007, OPS-012 | **Gap**: `docker compose` lacks mTLS — real credentials transmitted in plaintext. SQLite behavioral differences vs Postgres (especially RLS) not documented. The two-tier model is realistic but gaps exist for credential testing and multi-tenancy testing. |
| Expand-contract migration strategy — practical for rolling upgrades? | OPS-004 | **Gap**: Three-deployment requirement is sound but minimum inter-phase wait window (determined by `maxSessionAge`) is not documented. Non-idempotent migration steps not flagged. |
| Operational defaults — sensible for a first deployment? | OPS-009, OPS-006 | **Gap**: Defaults table doesn't show which mechanism controls each default. Warm pool sizing formula requires traffic history not available at first deployment. |
