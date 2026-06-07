---
layout: default
title: "runtime-upgrade-stuck"
parent: "Runbooks"
triggers:
  - alert: RuntimeUpgradeStuck
    severity: warning
components:
  - gateway
symptoms:
  - "runtime upgrade held in a non-terminal phase beyond the phase timeout"
  - "new runtime image not rolling out to a warm pool"
  - "upgrade phase stuck in expanding, draining, or contracting"
tags:
  - runtime
  - upgrade
  - rollout
requires:
  - admin-api
  - cluster-access
related:
  - warm-pool-exhaustion
  - controller-leader-election
---

# runtime-upgrade-stuck

A pool's `RuntimeUpgrade` record (§10.5) has stayed in a non-terminal phase longer than `runtimeUpgrade.phaseTimeoutSeconds` (default 600s). The new runtime image is not reaching the warm pool on schedule.

## Trigger

- `RuntimeUpgradeStuck` alert.
- `lenny_runtime_upgrade_state{state=~"expanding|draining|contracting"} == 1` for a pool held past the phase timeout.

## Diagnosis

### Step 1 — Read the upgrade status

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin pools upgrade status --pool claude-worker-sandboxed-medium
```

<!-- access: api method=GET path=/v1/admin/pools/{name}/upgrade-status -->
```bash
curl -sS -H "Authorization: Bearer $LENNY_ADMIN_TOKEN" \
  "$LENNY_API/v1/admin/pools/claude-worker-sandboxed-medium/upgrade-status"
```

The response reports `phase` (one of `pending`, `expanding`, `draining`, `contracting`, `complete`, `paused`), `phaseEnteredAt`, `phaseDurationSeconds`, `drainingSessions`, `pauseReason`, `newImage`, `canaryPercent`, and `schemaVersion`. A large `phaseDurationSeconds` in `expanding`, `draining`, or `contracting` identifies the stalled phase.

### Step 2 — Confirm the stalled phase from metrics

<!-- access: api method=GET path=/v1/admin/metrics -->
```bash
curl -sS "$LENNY_API/v1/admin/metrics" \
  | grep -E 'lenny_runtime_upgrade_(state|phase_duration_seconds|draining_sessions)'
```

`lenny_runtime_upgrade_phase_duration_seconds` gives the dwell per phase. `lenny_runtime_upgrade_draining_sessions` reports how many sessions still hold the old pool when the phase is `draining`.

### Step 3 — Image pull on the new pool

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get events -n lenny-agents --sort-by='.lastTimestamp' \
  | grep -iE "pull|failed" | tail
```

`ImagePullBackOff` on the new image digest is the most common `expanding` stall. The new pool's pods run in the `lenny-agents` namespace.

### Step 4 — Drain blockers

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get sandboxclaim -A | grep -v Released | head
```

When the phase is `draining`, long-running sessions holding claims on the old pool hold `drainingSessions` above zero. The state machine waits for the old pool to reach `activePodCount == 0` or for `drainTimeoutSeconds` to expire before it advances to `contracting`.

## Remediation

### Step 1 — Image pull failure

1. Verify the image digest exists in the registry.
2. Verify the `imagePullSecrets` reference is still valid.
3. If the upgrade pins a bad digest, roll back and restart with a corrected image:
   <!-- access: lenny-ctl -->
   ```bash
   lenny-ctl admin pools upgrade rollback --pool claude-worker-sandboxed-medium
   lenny-ctl admin pools upgrade start \
     --pool claude-worker-sandboxed-medium \
     --new-image registry.example.com/claude-worker@sha256:corrected
   ```

   Rollback from `expanding` sets the new pool's `minWarm` to 0, restores routing to the old pool, and moves the record to `paused`.

### Step 2 — Pause while investigating

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin pools upgrade pause --pool claude-worker-sandboxed-medium \
  --reason "investigating stalled expanding phase"
```

Pause halts the state machine and records the reason and timestamp on the upgrade record. The old pool keeps its current state. Resume with the command below once the cause is resolved.

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin pools upgrade resume --pool claude-worker-sandboxed-medium
```

### Step 3 — Drain taking too long

If legitimate traffic is holding the drain:

1. Accept the longer window and let the sessions complete.
2. Force the next phase only after confirming the remaining sessions are safe to checkpoint:
   <!-- access: lenny-ctl -->
   ```bash
   lenny-ctl admin pools upgrade proceed --pool claude-worker-sandboxed-medium
   ```

   Advancing from `draining` to `contracting` force-terminates the remaining old-pool sessions with a checkpoint. Do not advance without tenant sign-off when sessions are long-running.

### Step 4 — Late-stage rollback

If the new pool is broken after the old pool has begun draining or contracting, recreate the old pool from the preserved `previousPoolSpec`:

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin pools upgrade rollback --pool claude-worker-sandboxed-medium \
  --restore-old-pool
```

This is valid only while the old `SandboxTemplate` still exists. The old pool spec is preserved on the record until the upgrade reaches `complete`.

### Step 5 — Verify

<!-- access: lenny-ctl -->
```bash
lenny-ctl admin pools upgrade status --pool claude-worker-sandboxed-medium
```

- `phase` advances toward `complete`, or `paused` after a deliberate rollback.
- `phaseDurationSeconds` resets when the phase changes.
- The new image digest is visible on fresh warm pool pods.
- The alert clears.

## Escalation

Escalate to:

- **Release engineer / release owner** for image-pull failures that require rebuilding or re-publishing.
- **Platform engineering** if the phase does not advance after a resume or proceed, which may indicate a bug in the state machine.
- **Capacity owner** for drains that cannot complete within the phase timeout due to sustained long-running sessions, which may need a revisit of session lifetime limits.
