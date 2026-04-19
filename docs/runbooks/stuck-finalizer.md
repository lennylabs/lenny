---
layout: default
title: "stuck-finalizer"
parent: "Runbooks"
triggers:
  - alert: FinalizerStuck
    severity: warning
components:
  - controllers
symptoms:
  - "Sandbox in Terminating state past the configured alert threshold"
  - "warm pool does not replenish"
  - "kubectl get sandbox shows stuck resources"
tags:
  - finalizer
  - cleanup
  - sandbox
  - controllers
requires:
  - admin-api
  - cluster-access
related:
  - warm-pool-exhaustion
  - controller-leader-election
  - session-eviction-loss
---

# stuck-finalizer

A `Sandbox` resource is stuck in `Terminating` because its `lenny.dev/session-cleanup` finalizer has not been removed. This blocks pod deletion and, by extension, warm pool replenishment.

## Trigger

- `FinalizerStuck` alert — Sandbox in `Terminating` past the configured alert threshold.
- Warm pool does not replenish to `minWarm`.
- `kubectl get sandbox -A` lists stuck Terminating resources.

Exact alert thresholds are deployer-configurable — see [Metrics Reference](../reference/metrics.html#alert-rules).

## Diagnosis

### Step 1 — Identify the stuck resource

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get sandbox -A --field-selector=metadata.deletionTimestamp!='' \
  | grep Terminating
```

Record the resource name and namespace for the next steps.

### Step 2 — Check for an active SandboxClaim

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get sandboxclaim -A -o json | \
  jq '.items[] | select(.spec.sandboxRef == "<sandbox-name>")'
```

If a claim exists, the session is still logically active. **Do not remove the finalizer yet** — resolve the claim first.

### Step 3 — Verify session state in Postgres

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl exec deploy/lenny-gateway -- psql "$POSTGRES_DSN" -c \
  "SELECT session_id, state, last_checkpoint_at
   FROM sessions WHERE pod_name = '<sandbox-name>';"
```

Safe to proceed if state is `completed`, `failed`, `expired`, or `cancelled`. If `active` or `running` with no recent checkpoint, coordinate with the gateway to trigger a checkpoint first.

### Step 4 — Verify artifacts uploaded

<!-- access: kubectl requires=cluster-access -->
```bash
mc ls <alias>/lenny-artifacts/workspaces/<session-id>/
```

If artifacts are missing and the session is still active, do not proceed — artifacts will be lost.

### Step 5 — Controller reconciliation

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl logs -l app=lenny-warm-pool-controller --since=10m | grep <sandbox-name>
```

Common root causes surface here: leader-election gap during pod termination, API-server throttling, or a panic in the session-cleanup path.

## Remediation

### Step 1 — Confirm safe to remove

Proceed only if ALL of these are true:

- Step 2 found no active SandboxClaim.
- Step 3 shows session in a terminal state OR a recent checkpoint exists.
- Step 4 shows artifacts present in object storage (or the session was intentionally ephemeral).

### Step 2 — Remove the finalizer

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl patch sandbox <sandbox-name> -n <namespace> --type=json \
  -p '[{"op":"remove","path":"/metadata/finalizers","value":["lenny.dev/session-cleanup"]}]'
```

Kubernetes completes pod deletion immediately.

### Step 3 — Verify pod deletion

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl get sandbox <sandbox-name> -n <namespace>
# Expected: Error from server (NotFound)
```

### Step 4 — Verify warm pool replenishes

<!-- access: api method=GET path=/v1/admin/metrics -->
```
GET /v1/admin/metrics?q=lenny_warmpool_idle_pods&window=5m
```

Should return to `minWarm` within the pool's configured replenishment window. If not, investigate WarmPoolController logs for reconcile errors.

### Step 5 — Root cause

<!-- access: kubectl requires=cluster-access -->
```bash
kubectl logs -l app=lenny-warm-pool-controller --since=30m | grep -E "leader|throttle|<sandbox-name>"
```

Typical causes:

- **Leader-election gap.** Confirm `lenny_controller_leader_election_gap_seconds` during the window. If it crossed the configured alert threshold, see [controller-leader-election](controller-leader-election.html).
- **API-server throttling.** Check `lenny_controller_api_throttle_total`. Look at the kube-apiserver audit log for 429s.
- **Panic in cleanup.** Grep for `panic:` in controller logs; file an incident.

### Step 6 — Incident threshold

File an incident if stuck finalizers recur across multiple pods within a short window. Recurrence indicates a controller-level bug or a sustained API-server issue.

## Escalation

Escalate if:

- You cannot verify session state (Step 3) — session state corruption is worse than a stuck pod; bring in platform engineering.
- Artifacts are missing (Step 4) for a session the tenant expected to retain — inform the tenant before removing the finalizer.
- Force-removal is being considered and none of the safety checks pass — stop and page the platform on-call.
