---
layout: default
title: "runtime-upgrade-stuck"
parent: "Runbooks"
triggers:
  - alert: RuntimeUpgradeStuck
    severity: warning
components:
  - controllers
symptoms:
  - "runtime upgrade in non-terminal phase beyond phase timeout"
  - "new runtime image not rolling out to warm pools"
  - "RuntimeUpgrade CR stuck in Reconciling state"
tags:
  - runtime
  - upgrade
  - crd
  - rollout
requires:
  - admin-api
  - cluster-access
related:
  - warm-pool-exhaustion
  - controller-leader-election
---

# runtime-upgrade-stuck

A `RuntimeUpgrade` custom resource has been in a non-terminal phase longer than the configured phase timeout. The new runtime image isn't reaching warm pools on schedule.

## Trigger

- `RuntimeUpgradeStuck` alert.
- `RuntimeUpgrade` CR status phase is `Reconciling`, `Draining`, or `RollingOut` past the deadline.

## Diagnosis

### Step 1 — Upgrade status

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get runtimeupgrade -A
kubectl describe runtimeupgrade <name> -n lenny-system
```

The status includes `phase`, `phaseStartedAt`, `reason`, `message`.

### Step 2 — Controller logs

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl logs -l app=lenny-runtime-controller --since=15m \
  | grep -E "<upgrade-name>|runtime-upgrade"
```

### Step 3 — Image pull

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get events -n lenny-agents --sort-by='.lastTimestamp' \
  | grep -iE "pull|failed" | tail
```

`ImagePullBackOff` on the new image is the most common stall.

### Step 4 — Drain blockers

If phase is `Draining`:

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get sandboxclaim -A | grep -v Released | head
```

Long-running sessions holding claims slow the drain. The upgrade controller respects session lifetimes and will not force-terminate unless configured.

## Remediation

### Step 1 — Image pull failure

1. Verify the image digest exists in the registry.
2. Verify `imagePullSecrets` reference is still valid.
3. Correct the `RuntimeUpgrade.spec.imageDigest` if it's pinned to a bad digest:
   <!-- access: kubectl requires=cluster-access -->
   ```bash
   kubectl edit runtimeupgrade <name> -n lenny-system
   ```

### Step 2 — Controller stuck

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl rollout restart deployment lenny-runtime-controller -n lenny-system
kubectl rollout status deployment lenny-runtime-controller -n lenny-system --timeout=2m
```

The controller re-reconciles and continues where it left off.

### Step 3 — Drain taking too long

If legitimate traffic is holding the drain:

1. Accept the longer window.
2. If urgent, raise the force-terminate threshold in the `RuntimeUpgrade.spec.drain.maxGracefulWaitSeconds`.
3. Do NOT force-terminate sessions manually unless you have tenant sign-off.

### Step 4 — Cancel and retry

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin runtime-upgrades cancel <name>
```

Then re-create after correcting the spec.

### Step 5 — Verify

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get runtimeupgrade <name> -o jsonpath='{.status.phase}'
kubectl get pods -n lenny-system -l app=lenny-runtime -o jsonpath='{.items[*].spec.containers[*].image}'
```

- Phase transitions to `Completed`.
- New image digest visible in fresh warm pool pods.
- Alert clears.

## Escalation

Escalate to:

- **Release engineer / release owner** for image-pull failures that require rebuilding or re-publishing.
- **Platform engineering** if the controller is looping or panicking — may indicate a bug in the upgrade state machine.
- **Capacity owner** for drains that cannot complete within the phase timeout due to sustained long-running sessions — may need to revisit session lifetime limits.
