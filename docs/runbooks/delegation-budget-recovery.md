---
layout: default
title: "delegation-budget-recovery"
parent: "Runbooks"
triggers:
  - alert: DelegationBudgetKeysExpired
    severity: critical
  - alert: DelegationBudgetNearExhaustion
    severity: warning
components:
  - redis
  - gateway
symptoms:
  - "delegation requests rejected with BUDGET_EXHAUSTED"
  - "budget utilization approaching exhaustion for a delegation tree"
  - "BUDGET_KEYS_EXPIRED returned by Lua script"
tags:
  - delegation
  - budget
  - redis
  - limits
requires:
  - admin-api
  - cluster-access
related:
  - redis-failure
  - credential-pool-exhaustion
---

# delegation-budget-recovery

A delegation tree's budget keys in Redis have expired (losing accumulated spend) or are near exhaustion. Delegation mints will be rejected with `BUDGET_EXHAUSTED` until the budget is reset or the tree is retired.

## Trigger

- `DelegationBudgetKeysExpired` — `BUDGET_KEYS_EXPIRED` returned by the budget Lua script.
- `DelegationBudgetNearExhaustion` — budget utilization for a tree crossed the configured warning threshold (see [Metrics Reference](../reference/metrics.html#alert-rules)).
- Client calls to `/v1/delegations` return `BUDGET_EXHAUSTED`.

## Diagnosis

### Step 1 — Identify the tree

<!-- access: api method=GET path=/v1/admin/delegation-trees -->
```
GET /v1/admin/delegation-trees?utilizationGt=<warn-threshold>
```

Each tree reports: `rootSessionId`, `budgetRemaining`, `budgetTotal`, `keyTtlRemainingSeconds`.

### Step 2 — Are keys actually expired?

<!-- access: kubectl requires=cluster-access -->
```bash
redis-cli -h <host> -a "$REDIS_PASSWORD" --tls \
  EXISTS "delegation:budget:<tree-id>"
redis-cli -h <host> -a "$REDIS_PASSWORD" --tls \
  TTL "delegation:budget:<tree-id>"
```

`0` on EXISTS means the key has expired. TTL returns `-2` for missing, `-1` for no TTL, positive seconds for remaining TTL.

### Step 3 — Was the tree legitimately long-running?

<!-- access: api method=GET path=/v1/admin/audit-events -->
```
GET /v1/admin/audit-events?event_type=delegation.minted&treeId=<id>&since=24h
```

Long-running trees with sustained delegation traffic are expected to accumulate spend; near-exhaustion for them is normal.

## Remediation

### Step 1 — Budget keys expired

The budget key TTL is designed to cap the lifetime of a delegation tree. The gateway sets it to `delegation.budgetKeyTTLSeconds` (default 691200s, 8 days) at tree creation and re-arms it whenever the root session enters or leaves `suspended`, or leaves `awaiting_client_action` for `resume_pending`. `delegation.budgetKeyTTLSeconds` must exceed the suspended-session lifetime `gateway.maxSuspendedSessionSeconds` (default 604800s, 7 days), and the gateway does not start when it does not, so a root that stays `suspended` until it expires never loses its tree's keys. A root that spends longer than the TTL outside `suspended` since the last re-arm meets `BUDGET_KEYS_EXPIRED`. Expiry with an active tree is otherwise a misconfiguration or a Redis-failure event:

1. Confirm Redis health; if unhealthy, see [redis-failure](redis-failure.html).
2. Decide with the tree owner (tenant) whether to:
   - **Retire the tree.** Call `/v1/delegations/{tree}/close` — safe if the tree is idle.
   - **Extend the budget.** Adjust `delegation.budgets.*` (per-tenant `total`, `ttl`) in the tenant's Helm values and run `helm upgrade`. The controller picks up the new budget and refreshes the key TTL on the next delegation mint.

### Step 2 — Near-exhaustion

If exhaustion is imminent but expected, raise the tree's allowance by updating `delegation.budgets.<tenant>.total` in the tenant's Helm values and running `helm upgrade`. Budgets are contracts with the client; changes require explicit operator intent recorded in source control.

### Step 3 — Redis pressure

If keys are expiring unexpectedly due to Redis `maxmemory` eviction:

1. Redis `maxmemory-policy` must be `noeviction` for the Lenny key pattern (Spec §12.5). Verify:
   <!-- access: kubectl requires=cluster-access -->
   ```bash
   redis-cli -h <host> -a "$REDIS_PASSWORD" --tls CONFIG GET maxmemory-policy
   ```
2. If eviction is happening, follow [redis-failure](redis-failure.html) memory pressure remediation — the design invariant requires no eviction of budget keys.

### Step 4 — Verify

<!-- access: api method=GET path=/v1/admin/delegation-trees -->
```
GET /v1/admin/delegation-trees?treeId=<id>
```

- Tree reports `budgetRemaining > 0` and `keyTtlRemainingSeconds > 0`.
- Alert clears within its evaluation window.

## Escalation

Escalate to:

- **Tenant owner** for budget-increase approvals — the budget is a contractual limit.
- **Redis operators** if eviction policy cannot be set to `noeviction` at the managed-service tier.
- **Platform engineering** if `BUDGET_KEYS_EXPIRED` recurs on the same tree after reset — may indicate a Lua-script or TTL-refresh bug.
