# Proposal: Runtime isolation and placement auditor

- **Status:** Draft for review.
- **Date:** 2026-06-03.
- **Scope:** Adds a read-only runtime auditor that detects and alerts on pod isolation and placement invariant violations. No automatic remediation.

This document stages the proposed spec and chart edits. It does not modify any spec file. Apply the changes in the "Proposed spec and chart changes" section after sign-off.

## 1. Problem

The spec mandates several pod isolation and placement invariants and enforces them preventively through admission webhooks, scheduling constraints, and pool-config validation. Enforcement is fail-closed at admission time. Admission cannot verify the property that exists only after the scheduler binds a pod to a node: that the pod came to rest on a node whose labels and taints match the pool it belongs to.

The following conditions place a pod in violation of an invariant with no runtime signal today:

- A controller regression that omits an injected node affinity, toleration, or `securityContext` field on some code path.
- A node mislabeled with a dedicated node-pool label, or a dedicated node provisioned without its taint.
- A node-pool label removed after the pod scheduled (`IgnoredDuringExecution` keeps the pod in place).
- An admission webhook disabled through the feature-flag downgrade path, or a pod created by a privileged controller that bypasses admission.
- A manual `kubectl` edit that adds a toleration, weakens `securityContext`, or changes a label.

The Kata dedicated-node requirement ([§17.2](../spec/17_deployment-topology.md)) is one instance. The same gap runs through the other hard isolation invariants.

The existing detective precedent is NET-022 CIDR drift detection ([§13.2](../spec/13_security-model.md)): a periodic in-process check that re-reads cluster state, compares it against the desired invariant, increments a drift counter, and fires a critical alert. The CIDR detector does not auto-remediate; the operator re-runs `helm upgrade`. This proposal applies the same detect-and-alert stance to pod isolation and placement.

## 2. Decisions

The following were decided in review and constrain this design.

- The auditor detects and alerts. It performs no remediation. This matches the NET-022 stance, whose operational implications are already understood.
- The auditor is a standalone read-only component. No detection logic is added to the enforcing controller, so a fault in the WarmPoolController cannot blind detection.
- The home is `lenny-ops`. It is mandatory in every tier, it already owns drift detection, it already reads Nodes and `lenny.dev` custom resources and emits audit events, metrics, and alerts, and it runs in a separate failure domain from the WarmPoolController.
- The auditor runs as a periodic sweep on the `lenny-ops` leader.
- Coverage is the hard isolation and placement invariant families listed in Section 3. Advisory soft topology spread and pool-config-validity drift are out of scope (Section 9).

## 3. Scope: invariant families

Each managed agent pod (carrying `lenny.dev/managed: "true"`) is checked against the invariants its owning pool and runtime imply. The families and their checks:

| Class (`invariant_class`) | Invariant | Runtime check | Spec basis |
|---------------------------|-----------|---------------|------------|
| `node_placement` | A `microvm` pod runs only on a `lenny.dev/node-pool: kata` node; a T4 pod runs only on a T4-labeled and T4-tainted node; no foreign pod sits on a dedicated node | Resolve the pod's isolation profile and workspace tier, read the bound node, and compare the node's labels and taints and the pod's tolerations against the required dedicated-pool values | §17.2 node isolation; §6.4 T4 dedicated-node requirement |
| `security_posture` | A pod's `runtimeClassName`, `securityContext`, capabilities, seccomp, root filesystem, privilege escalation, host-namespace flags, and credential `fsGroup` match the restricted posture for its RuntimeClass | Re-validate the live pod (including ephemeral containers) against the RuntimeClass-appropriate posture, honoring the documented gVisor and Kata relaxations | §13.1 pod security; §17.2 PSS split and `POD_SPEC_HOST_SHARING_FORBIDDEN` |
| `governance_label` | The governance labels (`managed`, `tenant-id`, `delivery-mode`, `egress-profile`, `dns-policy`, `workspace-tier`) are present and match the pool and runtime config | Compare each label against the value the owning pool and runtime imply, restricted to pods whose stamped governance-config hash matches the current pool and runtime so an in-place reconfiguration does not flag mid-rollout pods | §13.2 NetworkPolicy label selection; §17.2 label immutability |
| `namespace` | Agent pods run only in the agent namespaces, and Kata pods run only in the Kata agent namespace | Compare the pod's namespace against the namespace its isolation profile requires | §17.2 namespace layout |
| `tenant_cotenancy` | A pod serves one tenant for its lifetime, a T4 node hosts one tenant, and cross-tenant reuse occurs only under the permitted conditions | Verify the `tenant-id` label is single-valued and group T4 pods by node to check tenant cardinality, both from cached pod labels with no database query | §5.2 tenant pinning; §6.4 T4 isolation |

The Sandbox custom resource status mirrors `nodeName` (`pkg/controller/sandbox/controller.go:599`), so the pod-to-node mapping is read from a resource `lenny-ops` already has access to. The bound node's labels and taints come from the existing Node read grant. Raw pod read in the agent namespaces is required for `security_posture` (the `securityContext` and `runtimeClassName`), `governance_label` (the live label set), and the toleration side of `node_placement`.

## 4. Design

### 4.1 Component

The auditor is a leader-gated background goroutine in `lenny-ops`, within the existing drift-detection subsystem ([§25.10](../spec/25_agent-operability.md)). It runs on the elected leader alongside the existing reconciliation goroutines (webhook delivery, drift-snapshot validation). It serves no client traffic and adds no ingress, so the external-only security boundary of `lenny-ops` ([§25.4](../spec/25_agent-operability.md)) is unchanged.

### 4.2 Cadence

The sweep runs every `ops.isolationAudit.intervalSeconds` (default 300, matching the NET-022 cadence). The interval is operator-tunable through Helm values. With the single-replica default, the sweep runs on the sole replica; during a leader failover the sweep pauses for the lease expiry (15 seconds) and resumes on the new leader.

### 4.3 Detection model

The auditor maintains watch-backed pod and node informer caches and evaluates the cache on each interval. This avoids a full `List` of every agent pod per tick at Tier 3 scale, where the agent-pod count reaches the low tens of thousands. The node set is small, and the `lenny.dev` custom resources cache cheaply.

### 4.4 Derivation of expected state

For each managed agent pod, the auditor resolves the owning `SandboxWarmPool` and `Runtime`, derives the expected isolation profile, RuntimeClass, dedicated node-pool label and taint, namespace, security posture, and governance labels, and compares them against the live pod and its bound node. The pool and runtime config is the source of truth, so the auditor detects a pod whose own labels agree with its node yet disagree with the pool (a case a label-versus-node comparison alone would miss).

### 4.5 Action on a violation

On each violating pod the auditor increments `lenny_pod_isolation_violation_total`, writes an append-only `pod.isolation_violation` audit event, and routes an operational event to the event stream. It performs no mutation and holds no write RBAC on pods or nodes.

### 4.6 Read surface, consistency, and database independence

The auditor evaluates its informer caches and issues no database query, which keeps each sweep cheap and uniform and avoids granting `lenny-ops` access to session tables. The current violation set from the last sweep is served through `GET /v1/admin/drift?scope=isolation`, which reuses the drift endpoint's caching and `?fresh=true` bypass, so operators and AI DevOps agents read current state directly rather than reconstructing it from the audit event stream.

For the `governance_label` class, an in-place pool reconfiguration leaves already-running pods on their previous immutable labels until they cycle out. The auditor flags a label mismatch only on pods built under the current governance config, so a planned reconfiguration produces no violations. At creation the controller stamps each pod with `lenny.dev/governance-config-hash`, a hash of the resolved governance-relevant inputs from both the pool and the runtime (`egress-profile`, `delivery-mode`, `dns-policy`, `isolation-profile`, `workspace-tier`, and the target namespace). The auditor recomputes that hash from the current pool and runtime and compares. A match means the pod was built under the current config, so a label mismatch is a genuine violation. A difference means the governance config changed since the pod was created, so the auditor skips the pod's `governance_label` check until it cycles out. The hash covers only the governance-relevant fields, so an unrelated change such as a `minWarm` edit does not suppress the check.

## 5. Observability surface

### 5.1 Metrics

- `lenny_pod_isolation_violation_total` (counter, labeled by `invariant_class`, `pool`, `isolation_profile`, `namespace`). Incremented once per violating pod per `invariant_class` per sweep. Steady-state value is zero. Per-pod and per-node identity is carried on the audit event rather than on the counter, to bound cardinality. `invariant_class` takes the values `node_placement`, `security_posture`, `governance_label`, `namespace`, and `tenant_cotenancy`. Emitted by `lenny-ops`.
- `lenny_pod_isolation_audit_last_sweep_timestamp_seconds` (gauge). Set to the wall-clock completion time of the last successful sweep. A detective control that stops sweeping is itself a risk, so this gauge drives the staleness alert below, mirroring the drift-snapshot staleness pattern ([§25.10](../spec/25_agent-operability.md)).
- `lenny_pod_isolation_audit_pods_scanned` (gauge). Count of managed agent pods evaluated in the last sweep, for coverage visibility.

### 5.2 Audit event

`pod.isolation_violation`, written through the append-only audit path ([§11.7](../spec/11_policy-and-controls.md)) and routed to the operational event stream with `source: "lenny-ops"`, mirroring `DataResidencyViolationAttempt`. Critical severity. Not sampled, because a placement or posture breach is security-salient regardless of volume. Retained under `audit.gdprRetentionDays`. Fields: `invariant_class`, `pool`, `runtime`, `isolation_profile`, `workspace_tier`, `k8s_pod_name`, `k8s_node_name`, `namespace`, `tenant_id`, `expected`, `observed`, and `detected_at`.

### 5.3 Alerts

A single violation alert fires critical across every class, and a staleness alert covers auditor liveness.

- `PodIsolationViolation` (critical). Fires when any class violation is observed in recent sweeps. Each flagged violation reflects a controller fault, a bypassed admission control, or an unauthorized change, so every class is treated as critical (Section 11 records the severity decision). The `invariant_class` label distinguishes the cases.
- `PodIsolationAuditStale` (warning). Fires when the last successful sweep is older than `intervalSeconds × stalenessFactor`. Detection is blind while this fires.

## 6. RBAC change

The required addition is `get`, `list`, and `watch` on core Pods in the agent namespaces, bound to `lenny-ops-sa`. The least-privilege shape is one namespaced Role plus RoleBinding per agent namespace, rather than a cluster-wide pod-read grant. This matches the chart's stated philosophy of a namespace Role where possible and a ClusterRole only for genuinely cluster-scoped resources (`charts/lenny/templates/ops-rbac.yaml:8-12`). The `lenny-preflight` Job already runs `kubectl auth can-i` over every `lenny-ops-sa` grant ([§17.6](../spec/17_deployment-topology.md)), so the new Roles are verified on every install.

Node read (`get`, `list`), `lenny.dev` custom resource read, and pod read in the release namespace are already granted (`charts/lenny/templates/ops-rbac.yaml`), so no further grants are required.

## 7. Helm values

```yaml
ops:
  isolationAudit:
    # Interval between isolation and placement audit sweeps on the lenny-ops leader.
    intervalSeconds: 300
    # PodIsolationAuditStale fires when the last successful sweep is older than
    # intervalSeconds * stalenessFactor.
    stalenessFactor: 3
```

The auditor itself cannot be disabled, consistent with `lenny-ops` being mandatory in every tier. The interval and the staleness factor are operator-tunable.

## 8. Proposed spec and chart changes

### 8.1 `spec/17_deployment-topology.md` §17.2 (node isolation)

Append a fourth item to the node-isolation controls list, after the dedicated-node taint:

```markdown
4. **Runtime placement verification (detective backstop):** Controls 1 through 3 are preventive and act at admission and scheduling time. Because pod-to-node binding completes after admission, no admission webhook can confirm that a pod came to rest on a node whose labels and taints match its required pool. The `lenny-ops` isolation auditor ([Section 25.10](25_agent-operability.md#2510-configuration-drift-detection)) re-verifies actual placement at runtime. On each sweep it reads every managed agent pod and the node it is bound to, and it records a violation when a `microvm` pod runs on a node lacking the `lenny.dev/node-pool: kata` label, when a pod without the Kata toleration runs on a Kata-tainted node, or when the equivalent T4 invariant ([Section 6.4](06_warm-pod-model.md#64-pod-filesystem-layout)) is broken. The auditor increments `lenny_pod_isolation_violation_total` and fires the `PodIsolationViolation` alert ([Section 16.5](16_observability.md#165-alerting-rules-and-slos)). The auditor performs no remediation, consistent with the NET-022 detect-and-alert stance ([Section 13.2](13_security-model.md#132-network-isolation)).
```

### 8.2 `spec/16_observability.md` §16.1 (metrics)

Add three rows to the metrics table:

```markdown
| Pod isolation violations (`lenny_pod_isolation_violation_total`, counter labeled by `invariant_class` (`node_placement`, `security_posture`, `governance_label`, `namespace`, `tenant_cotenancy`), `pool`, `isolation_profile`, `namespace` — incremented once per violating managed agent pod per class per audit sweep by the `lenny-ops` isolation auditor ([Section 25.10](25_agent-operability.md#2510-configuration-drift-detection)); steady-state value is zero; per-pod and per-node identity is carried on the `pod.isolation_violation` audit event rather than on the counter to bound cardinality; drives the `PodIsolationViolation` alert ([Section 16.5](#165-alerting-rules-and-slos))) | Counter |
| Isolation auditor last sweep (`lenny_pod_isolation_audit_last_sweep_timestamp_seconds`, gauge — wall-clock completion time of the last successful isolation audit sweep; drives the `PodIsolationAuditStale` alert ([Section 16.5](#165-alerting-rules-and-slos)) so a silently stopped auditor is detectable) | Gauge |
| Isolation auditor pods scanned (`lenny_pod_isolation_audit_pods_scanned`, gauge — number of managed agent pods evaluated in the last sweep, for coverage visibility) | Gauge |
```

### 8.3 `spec/16_observability.md` §16.5 (alerting rules)

Add two rows to the alerting-rules table:

```markdown
| `PodIsolationViolation` | The `lenny-ops` isolation auditor observed an isolation or placement violation on a managed agent pod within recent sweeps (`sum by (invariant_class, pool, namespace) (increase(lenny_pod_isolation_violation_total[15m])) > 0`). The `invariant_class` label identifies the case: a pod on the wrong hardware (`node_placement`), a weakened security posture (`security_posture`), a cross-tenant exposure (`tenant_cotenancy`), or a misrouting into the wrong NetworkPolicy or Pod Security policy set (`governance_label`, `namespace`). Runbook: `docs/runbooks/node-isolation-violation.md` ([Section 17.7](17_deployment-topology.md#177-operational-runbooks)). | Critical |
| `PodIsolationAuditStale` | The isolation auditor has not completed a sweep within `ops.isolationAudit.intervalSeconds × stalenessFactor` (`time() - lenny_pod_isolation_audit_last_sweep_timestamp_seconds > 900` at default settings). Placement and posture violations go undetected until the auditor recovers. | Warning |
```

### 8.4 `spec/25_agent-operability.md` §25.10 (new subsection: isolation and placement audit)

Add a subsection after the configuration drift detection content:

```markdown
#### Isolation and placement audit

`lenny-ops` runs an isolation auditor as a leader-gated background goroutine on the same interval cadence as configuration drift detection. The auditor re-verifies, against live cluster state, the pod isolation and placement invariants that admission control and scheduling constraints enforce preventively. It exists because pod-to-node binding completes after admission, so no admission webhook can confirm a pod's actual placement, and because a controller regression, node mislabel, missing taint, disabled webhook, or manual edit can break an invariant without any runtime signal.

On each sweep, for every managed agent pod (`lenny.dev/managed: "true"`), the auditor resolves the owning `SandboxWarmPool` and `Runtime`, derives the expected isolation profile, RuntimeClass, dedicated node-pool label and taint, namespace, security posture, and governance labels, and compares them against the live pod and its bound node. It checks the following invariant classes: `node_placement` (the pod is on a node whose labels and taints match its dedicated pool), `security_posture` (the pod's `runtimeClassName` and `securityContext` match the restricted posture for its RuntimeClass), `governance_label` (the governance labels are present and match the pool and runtime config), `namespace` (the pod is in the namespace its isolation profile requires), and `tenant_cotenancy` (the pod serves one tenant, a T4 node hosts one tenant, and cross-tenant reuse is within the permitted conditions).

The auditor is read-only and performs no remediation, consistent with the NET-022 detect-and-alert stance ([Section 13.2](13_security-model.md#132-network-isolation)). On each violating pod it increments `lenny_pod_isolation_violation_total` ([Section 16.1](16_observability.md#161-metrics)), writes a `pod.isolation_violation` audit event ([Section 16.7](16_observability.md#167-section-25-audit-events)), and routes an operational event to the event stream. It records `lenny_pod_isolation_audit_last_sweep_timestamp_seconds` on each successful sweep so a stalled auditor is detectable through the `PodIsolationAuditStale` alert.

The auditor reads Pods in the agent namespaces, Nodes, and `lenny.dev` custom resources. Node and custom-resource read are already part of the `lenny-ops-sa` grant; agent-namespace Pod read is added as one namespaced Role per agent namespace ([Section 17.2](17_deployment-topology.md#172-namespace-layout)). The sweep interval and the staleness factor are configurable via `ops.isolationAudit.intervalSeconds` (default 300) and `ops.isolationAudit.stalenessFactor` (default 3); the auditor cannot be disabled. The auditor operates on cached Kubernetes objects and issues no database query. The current violation set from the last sweep is served through `GET /v1/admin/drift?scope=isolation`, which reuses the drift endpoint's `?fresh=true` cache bypass. For the `governance_label` class, the controller stamps each pod with `lenny.dev/governance-config-hash` (a hash of the resolved pool and runtime governance inputs), and the auditor flags a mismatch only on pods whose stamp matches the current config, so an in-place pool reconfiguration does not produce violations for pods awaiting their next replacement.
```

Add the `pod.isolation_violation` event to the §16.7 audit-event catalog with the fields listed in Section 5.2 of this proposal.

### 8.5 `docs/alerting/rules.yaml`

Add to the appropriate group:

```yaml
        - alert: PodIsolationViolation
          expr: sum by (invariant_class, pool, namespace) (increase(lenny_pod_isolation_violation_total[15m])) > 0
          for: 1m
          labels:
            severity: critical
          annotations:
            description: 'The lenny-ops isolation auditor observed an isolation or placement violation on a managed agent pod. The invariant_class label identifies the case: node_placement, security_posture, tenant_cotenancy, governance_label, or namespace. Identify the pod and node from the pod.isolation_violation audit event or GET /v1/admin/drift?scope=isolation.'
            runbook_url: https://docs.lenny.dev/runbooks/node-isolation-violation
            summary: Pod isolation or placement invariant violated
        - alert: PodIsolationAuditStale
          expr: time() - lenny_pod_isolation_audit_last_sweep_timestamp_seconds > 900
          for: 10m
          labels:
            severity: warning
          annotations:
            description: 'The lenny-ops isolation auditor has not completed a sweep in over 15 minutes (3x the default 300s interval). Pod placement and posture violations go undetected until the auditor recovers. Check the lenny-ops leader pod and its logs.'
            runbook_url: https://docs.lenny.dev/runbooks/node-isolation-violation
            summary: Isolation auditor sweep is stale
```

The same rules are added to the chart-bundled `charts/lenny/files/alerting-rules.yaml` (or `pkg/alerting/rules`) so the rendered `PrometheusRule` carries them ([§16.9](../spec/16_observability.md), §25.13).

### 8.6 `docs/runbooks/node-isolation-violation.md` (new)

```markdown
---
layout: default
title: "node-isolation-violation"
parent: "Runbooks"
components:
  - lenny-ops
  - admission
symptoms:
  - "PodIsolationViolation firing"
  - "pod.isolation_violation audit events present"
tags:
  - isolation
  - placement
  - security
requires:
  - admin-api
related:
  - admission-plane-feature-flag-downgrade
  - drift-snapshot-refresh
---

# node-isolation-violation

The `lenny-ops` isolation auditor re-verifies pod isolation and placement invariants against live cluster state and records a violation when a managed agent pod does not match the posture its pool and runtime imply. This runbook covers triage and remediation for a fired `PodIsolationViolation` alert.

## Trigger

- `PodIsolationViolation` is firing.
- The alert labels carry `invariant_class`, `pool`, and `namespace`.

## Diagnosis

### Step 1 — Identify the pod and node

Query the audit log for the violation detail, which carries the pod, node, expected value, and observed value:

`GET /v1/admin/audit-events?event_type=pod.isolation_violation&since=30m`

### Step 2 — Classify the violation

- `node_placement`: the pod is on a node whose labels or taints do not match its dedicated pool. Inspect the node: `kubectl get node <node> -o jsonpath='{.metadata.labels}{"\n"}{.spec.taints}'`.
- `security_posture`: the pod's `runtimeClassName` or `securityContext` is weaker than its RuntimeClass requires. Inspect: `kubectl get pod <pod> -n <ns> -o yaml`.
- `governance_label`: a governance label is missing or disagrees with the pool config.
- `namespace`: the pod is in the wrong namespace.
- `tenant_cotenancy`: a T4 node hosts more than one tenant, or a pod's tenant label is inconsistent.

### Step 3 — Find the cause

Common causes are a node mislabeled or provisioned without its taint, a disabled admission webhook (cross-check `AdmissionPlaneFeatureFlagDowngrade`), a controller regression in pod-spec injection, or a manual edit.

## Remediation

### Step 1 — Contain

For a `node_placement` or `tenant_cotenancy` violation, cordon the affected node to stop further scheduling onto it: `kubectl cordon <node>`.

### Step 2 — Replace the pod

Delete the violating pod so the pool recreates it with correct constraints: `kubectl delete pod <pod> -n <ns>`. For an active session pod, drain it through the admin API to avoid abrupt session loss where the violation is not an immediate exposure.

### Step 3 — Fix the root cause

Correct the node label or taint, re-enable the missing webhook, or roll back the controller change. Verify the alert clears on the next sweep.
```

### 8.7 `charts/lenny/templates/ops-rbac.yaml`

Add one namespaced Role plus RoleBinding per agent namespace, granting `lenny-ops-sa` pod read. The exact field path follows the chart's agent-namespace values structure:

```yaml
{{- range $ns := .Values.agentNamespaces }}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: lenny-ops-agent-pod-read
  namespace: {{ $ns.name }}
  labels:
    {{- include "lenny.labels" $ | nindent 4 }}
rules:
  # Isolation and placement auditor (§25.10) reads agent pods to verify
  # securityContext, runtimeClassName, tolerations, and governance labels.
  - apiGroups: [""]
    resources: ["pods"]
    verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: lenny-ops-agent-pod-read
  namespace: {{ $ns.name }}
  labels:
    {{- include "lenny.labels" $ | nindent 4 }}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: lenny-ops-agent-pod-read
subjects:
  - kind: ServiceAccount
    name: lenny-ops-sa
    namespace: {{ $.Release.Namespace }}
{{- end }}
```

### 8.8 `charts/lenny/values.yaml`

Add the `ops.isolationAudit` block from Section 7.

## 9. Non-goals

- Automatic remediation. Excluded by the detect-and-alert decision.
- Soft topology spread compliance. Spread constraints default to `ScheduleAnyway`, so a skew is expected behavior rather than a violation.
- Pool-config-validity drift (acknowledgment flags, termination budget, egress and delivery coherence). These describe pool definitions and are validated at registration by the pool-config validator. A re-scan of live pool definitions is a separate concern.
- Logical isolation monotonicity for delegation and experiments. These already have detective signals (`delegation.isolation_violation`, `lenny_experiment_isolation_rejections_total`).
- Forensic cross-tenant reuse-history detection. The v1 auditor verifies current co-tenancy from pod labels. Detecting a reuse that already completed requires historical data and is deferred to a later iteration (Section 11).

## 10. Testing

- Unit: table-driven tests for each per-family check function, covering the compliant case and each violation mode.
- Integration: fixtures that produce a Kata pod on a mislabeled node, a non-tolerating pod on a Kata-tainted node, a pod with a weakened `securityContext`, a pod with a missing governance label, a pod in the wrong namespace, and two T4 pods of different tenants on one node. Assert that `lenny_pod_isolation_violation_total` increments with the correct `invariant_class` and that the `pod.isolation_violation` audit event is written. Include a negative fixture: a pod whose governance label differs from its pool because the pool was reconfigured after the pod was created, and assert that no violation is recorded (mid-rollout exclusion).
- Chart: assert `ops-rbac.yaml` renders one `lenny-ops-agent-pod-read` Role and RoleBinding per agent namespace, and that the preflight `auth can-i` check covers them.
- Alerting: `promtool` rule tests for `PodIsolationViolation` and `PodIsolationAuditStale`.

## 11. Resolved in review

- Severity. Every invariant class fires at critical severity through one `PodIsolationViolation` alert. A flagged violation reflects a controller fault, a bypassed admission control, or an unauthorized change in every class. A `namespace` mismatch cannot arise by chance, because a pod's namespace is immutable and the isolation-profile-to-namespace mapping is fixed. A `governance_label` mismatch is similarly anomalous once mid-rollout pods are excluded: pool runtime fields are mutable in place ([Section 5 base runtime mutability](../spec/05_runtime-registry-and-pool-model.md)) while pod governance labels are immutable, so a legitimate reconfiguration leaves running pods on the previous label until they cycle out. The auditor flags a `governance_label` mismatch only on pods whose `lenny.dev/governance-config-hash` stamp (written by the controller over the resolved pool and runtime governance inputs) matches the current config, which removes that benign case.
- Tenant co-tenancy stays database-free. The reuse-history sub-check is the only part that would require a database query, because the live `tenant-id` label shows the current tenant and a completed improper reuse follows the legitimate-looking `{tenant} → unassigned → {other tenant}` relabel path that leaves no trace on the pod. The v1 auditor covers `tenant_cotenancy` with the single-valued-label check and the node-level T4 cardinality check, both from cached pod labels. Forensic reuse-history detection is deferred (Section 9); if added, the audit log is the source, scoped to candidate pools, batched per sweep, bounded to each pod's lifetime, and backed by an index on the pod identifier.
- Read API. The current violation set is exposed as a scope on the existing drift endpoint, `GET /v1/admin/drift?scope=isolation`, returning the last sweep's violating pods with their expected and observed values, the sweep timestamp, and the scanned-pod count, and honoring the endpoint's `?fresh=true` cache bypass. This reuses the drift API and its Operations Inventory integration, gives an AI DevOps agent a current-state query for runbook step 1, and matches the precedent that `GET /v1/admin/drift` already serves current state for configuration drift.

## 12. Files touched on application

| File | Change |
|------|--------|
| `spec/17_deployment-topology.md` | §17.2 control 4 (runtime placement verification) |
| `spec/16_observability.md` | §16.1 three metric rows; §16.5 three alert rows; §16.7 audit event |
| `spec/25_agent-operability.md` | §25.10 isolation and placement audit subsection; `?scope=isolation` on `GET /v1/admin/drift` |
| `docs/alerting/rules.yaml` | Two alert rules |
| `docs/runbooks/node-isolation-violation.md` | New runbook |
| `charts/lenny/templates/ops-rbac.yaml` | Per-agent-namespace pod-read Role and RoleBinding |
| `charts/lenny/files/alerting-rules.yaml` (or `pkg/alerting/rules`) | Three bundled alert rules |
| `charts/lenny/values.yaml` | `ops.isolationAudit` block |
| `charts/lenny/templates/preflight-job` and tests | Coverage for the new Roles and rules |
| `pkg/controller/sandbox/podspec` | Stamp `lenny.dev/governance-config-hash` (hash of resolved pool and runtime governance inputs) on created pods for the governance-label mid-rollout exclusion |
| `lenny-ops` drift handler | `?scope=isolation` support on `GET /v1/admin/drift` |
