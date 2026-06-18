---
layout: default
title: "nonce-only-mode"
parent: "Runbooks"
triggers:
  - alert: PoolSecurityDegraded
    severity: warning
components:
  - warmPools
  - runtimes
symptoms:
  - "lenny_pool_security_degraded == 1 for one or more pools"
  - "SandboxTemplate carries SecurityDegradedMode=True"
  - "member Sandboxes carry SOPeercredDisabled=True"
tags:
  - security
  - so-peercred
  - nonce-only
  - adapter-agent-boundary
requires:
  - admin-api
  - cluster-access
related:
  - credential-revocation
---

# nonce-only-mode

A pool is running in nonce-only mode for the adapter-agent authentication boundary. In nonce-only mode the adapter does not require a functional `SO_PEERCRED` UID check and relies on the manifest nonce handshake alone, a deliberate fallback for confirmed gVisor `SO_PEERCRED` divergence. The mode is an audited security-degradation state under a human-review SLA.

A pool enters nonce-only mode when its `deploymentModel: sidecar` runtime sets `Runtime.spec.requireSoPeercred: false` and the pool carries `acknowledgeNonceOnlyAuth: true`. The pool remains degraded after an operator reverts the runtime field for as long as a pre-revert nonce-only pod is still serving. The WarmPoolController publishes `lenny_pool_security_degraded` and writes `SecurityDegradedMode=True` on the `SandboxTemplate` in the same reconcile, and writes `SOPeercredDisabled=True` on each rendered `Sandbox`.

This alert is expected when an operator has deliberately enabled nonce-only mode after confirmed gVisor divergence. It is a defect when no operator activated the mode, which indicates an unauthorized `Runtime` CR edit. Alert thresholds are deployer-configurable — see [Metrics Reference](../reference/metrics.html#alert-rules).

## Trigger

- `PoolSecurityDegraded` — `lenny_pool_security_degraded == 1` for any pool. The pool is rendering nonce-only pods. The bundled rule is defined on the controller-published gauge because the adapter metric `lenny_adapter_sopeercred_disabled_total` is emitted inside agent pods and is outside the default scrape target set; deployers who wire an adapter scrape target additionally alert on that counter.

## Diagnosis

### Step 1 — Identify the degraded pool and its runtime

Read the pool's `SandboxTemplate` condition and the member Sandboxes carrying the per-pod condition:

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get sandboxtemplate -n lenny-system \
  -o jsonpath='{range .items[?(@.status.conditions[?(@.type=="SecurityDegradedMode")].status=="True")]}{.metadata.name}{"\n"}{end}'
kubectl get sandbox -n lenny-system \
  -l lenny.dev/pool=<pool> \
  -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.spec.requireSoPeercred}{"\n"}{end}'
```

Note the pool's `runtimeRef`. A member `Sandbox` with `spec.requireSoPeercred: false` or a `SOPeercredDisabled=True` condition is running nonce-only.

### Step 2 — Confirm the activating runtime field

Read the `Runtime` CR the pool references and confirm whether the field is set deliberately:

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get runtime <runtime> -o jsonpath='{.spec.deploymentModel}{"\t"}{.spec.requireSoPeercred}{"\n"}'
```

`requireSoPeercred: false` on a `deploymentModel: sidecar` runtime is the activating configuration. If the field is `false` and no operator authorized it, treat this as an unauthorized CR edit and escalate.

### Step 3 — Confirm the pool acknowledgment

Confirm the pool carries the acknowledgment that admitted nonce-only mode:

<!-- access: api method=GET path=/v1/admin/pools/{name} -->
```
GET /v1/admin/pools/<pool>
```

`acknowledgeNonceOnlyAuth: true` is required for the gateway to admit a pool referencing a nonce-only runtime. A pool without it renders no nonce-only pods and does not trip this alert.

## Remediation

### Step 1 — If the degradation is expected

If the mode was enabled deliberately after confirmed gVisor `SO_PEERCRED` divergence, keep it under the human-review SLA. Re-run the Phase 3.5 `SO_PEERCRED` integration tests when the container runtime version changes. The setting must be re-evaluated at each runtime upgrade.

### Step 2 — Return the runtime to full enforcement

When the divergence is resolved, set the runtime back to the default to restore full `SO_PEERCRED` enforcement:

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl patch runtime <runtime> --type=merge -p '{"spec":{"requireSoPeercred":true}}'
```

Newly created Sandboxes render no `--require-so-peercred=false` flag and carry neither `spec.requireSoPeercred: false` nor `SOPeercredDisabled=True`. Pre-revert pods continue in nonce-only mode until normal pool reconciliation replaces them.

### Step 3 — Replace pre-revert nonce-only pods

The pool condition stays `True` while any member `Sandbox` still carries the nonce-only carrier or condition. To clear the degraded state promptly, recycle the pool's warm pods:

<!-- access: api method=PUT path=/v1/admin/pools/{name}/recycle -->
```
PUT /v1/admin/pools/<pool>/recycle
```

The condition transitions to `False` only when no member `Sandbox` carries `spec.requireSoPeercred: false` and none still carries `SOPeercredDisabled=True`.

### Step 4 — Verify

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get sandboxtemplate <pool> -n lenny-system \
  -o jsonpath='{.status.conditions[?(@.type=="SecurityDegradedMode")].status}{"\n"}'
```

- `lenny_pool_security_degraded` returns to `0` for the pool.
- `SecurityDegradedMode` is `False` on the `SandboxTemplate`.
- No member `Sandbox` carries `SOPeercredDisabled=True`.

## Escalation

Escalate to:

- **Security on-call** when the `Runtime.spec.requireSoPeercred: false` field is set and no operator authorized it — an unauthorized `Runtime` CR edit can weaken the adapter-agent authentication boundary.
- **Runtime author** for the runtime named in the pool's `runtimeRef` when the divergence needs re-validation against a new container runtime version.
- **Platform operator** when a reverted pool stays degraded longer than expected — pre-revert nonce-only pods may need to be recycled to clear the condition.
