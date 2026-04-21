---
layout: default
title: "cycle-detection-mode-unsafe"
parent: "Runbooks"
triggers:
  - alert: CycleDetectionModeUnsafe
    severity: warning
  - alert: CycleDetectionWarnModeBlocking
    severity: warning
components:
  - gateway
symptoms:
  - "deployment running with gateway.cycleDetection.mode below enforce"
  - "warn-mode would-have-blocked rate sustained above zero — flip to enforce will start rejecting hops"
  - "Helm gateway.cycleDetection.mode value lowered to warn or permissive"
tags:
  - delegation
  - cycle-detection
  - enforcement-posture
  - security
requires:
  - admin-api
  - helm
related:
  - delegation-budget-recovery
---

# cycle-detection-mode-unsafe

Cycle detection on recursive delegation is governed by the deployment-scope `gateway.cycleDetection.mode` Helm value (`enforce` | `warn` | `permissive`). Under `enforce`, the §8.2 three-layer AND gate (Helm `gateway.allowSelfRecursion` × `Runtime.spec.allowSelfRecursion` × `DelegationPolicy.allowSelfRecursion`) decides admission; rejected hops return `DELEGATION_CYCLE_DETECTED` with `details.blockedBy` naming the first `false` layer. Under `warn`, every self-recursive hop is admitted and a `delegation.cycle_warning` audit event records which layers would have blocked. Under `permissive`, no runtime-identity check runs at all — only the always-on `maxDepth` ceiling (Helm fallback `gateway.delegation.defaultMaxDepth`) bounds chain length.

This runbook covers the two warning alerts that fire when the cycle-detection posture is below `enforce`:

- **`CycleDetectionModeUnsafe`** is a **standing** alert that fires while `gateway.cycleDetection.mode: permissive` is in effect (regardless of whether any self-recursive hop has occurred). It signals an ongoing configuration choice — typically a development cluster — not an incident. `gateway.allowSelfRecursion: no` is the safer-than-default master-gate posture and does **not** raise this alert.
- **`CycleDetectionWarnModeBlocking`** fires when `sum by (tenant_id) (rate(lenny_delegation_would_have_blocked_total{mode="warn"}[10m])) > 0` is sustained for more than 10 minutes. The deployment is running with `gateway.cycleDetection.mode: warn` (a diagnostic rollout mode) and at least one tenant is producing self-recursive hops that would be rejected under `enforce`. The hops are admitted; the alert exists so the rollout cannot be forgotten and so any flip to `enforce` is preceded by a deliberate review.

See SPEC §8.2 "Delegation mechanism" cycle-detection decision matrix; SPEC §8.3 "DelegationPolicy and lease" Helm settings table; SPEC §15.1 error rows `DELEGATION_CYCLE_DETECTED` and `DELEGATION_POLICY_WEAKENING`; SPEC §16.5 alerts `CycleDetectionModeUnsafe` and `CycleDetectionWarnModeBlocking`; SPEC §16.7 audit events `delegation.self_recursion_allowed`, `delegation.cycle_warning`, `gateway.cycle_detection_mode_changed`.

## Trigger

- `CycleDetectionModeUnsafe` — standing warning while `gateway.cycleDetection.mode: permissive` is in effect. Intended as a persistent configuration signal, not a time-bounded incident.
- `CycleDetectionWarnModeBlocking` — `sum by (tenant_id) (rate(lenny_delegation_would_have_blocked_total{mode="warn"}[10m])) > 0` sustained for more than 10 minutes.

Thresholds and evaluation windows are deployer-configurable — see [Metrics Reference](../reference/metrics.html#alert-rules).

## Diagnosis

### Step 1 — Read the current Helm posture

Confirm the rendered `gateway.cycleDetection.mode` value the gateway is running with:

<!-- access: api method=GET path=/v1/admin/platform/config -->
```
GET /v1/admin/platform/config
```

Inspect `gateway.cycleDetection.mode`, `gateway.allowSelfRecursion`, and `gateway.delegation.defaultMaxDepth`. The first determines which alert is firing; the second is the master kill-switch (a `no` value is the safer posture and does not raise the standing alert); the third bounds chain length even under `permissive`.

### Step 2 — Correlate with the most recent Helm transition

Find the most recent `helm install`/`upgrade` that changed the posture:

<!-- access: api method=GET path=/v1/admin/audit-events -->
```
GET /v1/admin/audit-events?event_type=gateway.cycle_detection_mode_changed&since=30d
```

The event payload carries `previous_mode`, `new_mode`, `changed_by_sub`, `justification` (required when `new_mode` is `warn` or `permissive`), and `changed_at`. Companion events `gateway.allow_self_recursion_changed` and `gateway.default_max_depth_changed` capture parallel transitions on the master gate and the depth fallback:

<!-- access: api method=GET path=/v1/admin/audit-events -->
```
GET /v1/admin/audit-events?event_type=gateway.allow_self_recursion_changed&since=30d
GET /v1/admin/audit-events?event_type=gateway.default_max_depth_changed&since=30d
```

If the latest change is recent and expected (e.g., a diagnostic `warn` rollout window), the alert is acknowledged configuration; confirm the `justification` matches the approval record. If the change is unexpected, escalate immediately on the operator named in `changed_by_sub`.

### Step 3 — For warn-mode: identify the affected tenants and layers

For `CycleDetectionWarnModeBlocking`, break down the would-have-blocked counter by tenant and layer to predict which tenants will start receiving `DELEGATION_CYCLE_DETECTED` rejections after a flip to `enforce`:

```
sum by (tenant_id, layer) (rate(lenny_delegation_would_have_blocked_total{mode="warn"}[1h]))
```

Each row tells you (a) which tenant is producing self-recursive hops and (b) which of `{platform, runtime, policy}` would fail closed under `enforce`. Pull the per-hop audit rows for high-rate tenants:

<!-- access: api method=GET path=/v1/admin/audit-events -->
```
GET /v1/admin/audit-events?event_type=delegation.cycle_warning&tenant_id=<tenant_id>&since=1h
```

The event payload carries `parent_session_id`, `runtime_name`, `pool_name`, `delegation_depth`, `would_have_blocked_layers` (the per-hop set drawn from `{platform, runtime, policy}`), and `effectiveSettings`. Use this to decide whether each tenant's self-recursion is intentional (the runtime author needs to opt in via `Runtime.spec.allowSelfRecursion: true`) or accidental (the loop is a bug and the tenant should accept the post-flip rejections).

### Step 4 — Determine the remediation path

For `CycleDetectionModeUnsafe`, the only lever is the Helm `gateway.cycleDetection.mode` value — restore it to `warn` or `enforce` per Step 1 of Remediation.

For `CycleDetectionWarnModeBlocking`, three independent paths cover the offending tenants:

- **Opt the tenant in** if the self-recursion is intended — set `Runtime.spec.allowSelfRecursion: true` on the recursing runtime and `DelegationPolicy.allowSelfRecursion: true` on the tenant's policy. Flipping all three layers produces a `delegation.self_recursion_allowed` audit event per admitted hop instead of a rejection.
- **Accept the rejections** if the self-recursion is a bug — leave the runtime/policy at the default `false` and proceed to flip the mode to `enforce`; the offending hops will start returning `DELEGATION_CYCLE_DETECTED` and the calling agents will need to break their loops.
- **Defer the flip** if the would-have-blocked rate is non-trivial and the operator wants more time to triage — leave `mode: warn` in place and re-evaluate after the per-tenant breakdown is steady-state-zero.

## Remediation

### Step 1 — Restore `gateway.cycleDetection.mode` (deployment-wide)

Edit the deployment's Helm values file:

```yaml
gateway:
  cycleDetection:
    mode: enforce        # tightens from warn / permissive
```

Run `helm upgrade`. The render writes the new value to the rendered gateway configuration and the chart emits one `gateway.cycle_detection_mode_changed` audit event. `justification` is not required when raising to `enforce`; it is required when `mode` is `warn` or `permissive` (omitting it fails the chart render with `CYCLE_DETECTION_MODE_JUSTIFICATION_REQUIRED`).

### Step 2 — Opt tenants in (warn-mode rollout, intended self-recursion)

For each runtime that legitimately needs self-recursion, the runtime author updates the `Runtime` resource:

```yaml
apiVersion: lenny.dev/v1
kind: Runtime
metadata:
  name: <runtime_name>
spec:
  allowSelfRecursion: true        # Layer 2 of the §8.2 three-layer AND gate
```

The tenant operator updates the `DelegationPolicy` resource for the affected runtime:

```yaml
apiVersion: lenny.dev/v1
kind: DelegationPolicy
metadata:
  name: <policy_name>
spec:
  allowSelfRecursion: true        # Layer 3 of the §8.2 three-layer AND gate
```

Both axes default to `false` and the platform layer (`gateway.allowSelfRecursion`) defaults to `yes`. All three must be `true`/`yes` for an admitted hop. `DelegationPolicy.allowSelfRecursion` is monotonic across delegation hops: a child lease may narrow `true → false` but cannot widen `false → true` (rejected with `DELEGATION_POLICY_WEAKENING` and `details.field: "allowSelfRecursion"`).

After both opt-ins are in place, the next self-recursive hop emits `delegation.self_recursion_allowed` (admitted) instead of `delegation.cycle_warning` (would have blocked), and `lenny_delegation_would_have_blocked_total{tenant_id="<tenant>"}` returns to zero.

### Step 3 — Verify the alert clears

For `CycleDetectionModeUnsafe`:

<!-- access: api method=GET path=/v1/admin/platform/config -->
```
GET /v1/admin/platform/config
```

Confirm `gateway.cycleDetection.mode` is `warn` or `enforce`. The standing alert resolves automatically once the rendered value is no longer `permissive`.

For `CycleDetectionWarnModeBlocking`, confirm the rolled-up counter returns to zero for a sustained 10-minute window:

```
sum by (tenant_id) (rate(lenny_delegation_would_have_blocked_total{mode="warn"}[10m])) == 0
```

If the operator chose to flip to `enforce` instead of opting tenants in, expect a corresponding rise in `DELEGATION_CYCLE_DETECTED` rejections on the same tenants — these are the desired enforcement outcome, not a regression.

## Escalation

Escalate to:

- **Platform operator who changed the posture** (as named in `changed_by_sub` on the most recent `gateway.cycle_detection_mode_changed` audit event) if the weakened state is unintended — they are the fastest path to restoring the prior posture and can attest to the change's intent.
- **Runtime author** for the runtime named in `delegation.cycle_warning` events when the self-recursion is intentional but the runtime has not been opted in — they own the `Runtime.spec.allowSelfRecursion` flag.
- **Tenant operator** when the self-recursion is intentional and the runtime is opted in but the tenant's `DelegationPolicy.allowSelfRecursion` is still `false` — they own Layer 3 of the gate.
- **Security on-call** for any deployment running `mode: permissive` outside of a development cluster — the no-cycle-checks posture is intended for development only.

Cross-reference: [SPEC §8.2](https://github.com/lennylabs/lenny/blob/main/spec/08_recursive-delegation.md#82-delegation-mechanism) cycle-detection decision matrix; [SPEC §8.3](https://github.com/lennylabs/lenny/blob/main/spec/08_recursive-delegation.md#83-delegation-policy-and-lease) Helm settings table; [SPEC §15.1](https://github.com/lennylabs/lenny/blob/main/spec/15_external-api-surface.md#151-rest-api) error rows `DELEGATION_CYCLE_DETECTED` and `DELEGATION_POLICY_WEAKENING`; [SPEC §16.5](https://github.com/lennylabs/lenny/blob/main/spec/16_observability.md#165-alerting-rules-and-slos) warning alert definitions; [SPEC §16.7](https://github.com/lennylabs/lenny/blob/main/spec/16_observability.md#167-section-25-audit-events) audit event catalog; [Metrics Reference](../reference/metrics.html#alert-rules); [Operator Guide — Configuration](../operator-guide/configuration.html); [Runtime Author Guide — Delegation](../runtime-author-guide/delegation.html).
