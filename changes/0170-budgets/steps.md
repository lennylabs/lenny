# Steps: 0170 Budgets

## Validation owed

| Item | Why it matters | Waits |
| :-- | :-- | :-- |
| Confirm that no route of §15.1, and no code path, changes a session's `labels` after creation. | BGR-4 computes the budget set once, from attributes fixed at creation. A label change would move a session between budgets. | 0170.S1 |
| Measure the latency of a model call's reservation in a session budget of one to eight limits, and in a session budget with a funding ancestor, on a Redis Cluster at the proxy's request rate. | Every proxied call pays the reservation (decision 24). | 0170.S1 |
| Measure a shared budget's overshoot against `budgets.shared.flushSeconds` at 1, 5, and 15 seconds, with the replica counts and charge rates of the Tier 3 sizing of §17.8, and the Redis write rate of the flush. | The default of `budgets.shared.flushSeconds` and the bound of BGR-9 for shared budgets come from it. | 0170.S1 |
| Measure the rebuild of a budget's open period from checkpoints and durable records after a Redis loss, for a tenant budget over a month of `llm_usage`, `session_pod_windows`, and `artifact_store` rows. | "BG-9" refuses a budget's charges until its rebuild ends. | 0170.S1 |
| Confirm that a session's directory groups are available at session creation for every identity source of 0149. | The `groups` selector of decision 8 reads them at creation. | 0170.S1 (part) |
| Measure the Redis write rate of the compute tick at the session counts of the Tier 3 sizing of §17.8, at the default tick of 60 seconds and at 10 seconds. | Every session holding a pod writes once per tick in its session budget, and its shared budgets receive the tick through the flush. | 0170.S3 |
| Confirm that the release of a pod binding writes `closed_at` of `session_pod_windows` in the same transaction as the release in every release path of 0121, the eviction and the resume onto another pod included. | The recovery sweep settles an expired compute tick from `closed_at`. | 0170.S3 |

## Steps

The steps stay provisional until the owner settles the open decisions in `decisions.md`.

### 0170.S1 Budget objects and the `llm` meter
- status: Planned
- aliases: [BG-1, BG-2, BG-3, BG-7, BG-9, BG-10, BG-4 (part), BG-6 (part), BG-8 (part)]
- lanes: [specification, code, documentation]
- depends-on: [0133.S3, 0149.S3, 0143.S1 (part), 0149.S9 (part)]

Budget records with their limits, the meter and unit pairs of "BG-4" (decisions 16 to 18), their selectors, references, partitions, periods, schedules, resets, thresholds, notices, modes, and exhaustion actions for new work; the session budget with its defaults and ceilings; the budget set and its invalidation; the `llm` meter in `llmCredits` and, from 0170.S2, in money, which reserves and settles each proxied call in the session budgets that hold it and counts it in shared budgets through the flush, in place of the scopes of 0133 "LU-6"; the reservation of session budgets with the recovery index, the flush of shared budgets with each replica's cached state and watermark, the recovery of a lapsed replica's unflushed charges, and the rebuild (decision 24); lease extension of session budget limits that count `llm`; the reservation that funds another session's budget, which 0166.S2 calls to create, move, transfer, and release a carve; `budgetRefs` and `budget` on session creation; the routes, the RBAC rows, the audit events, the metrics, the usage reports, and `BUDGET_EXHAUSTED` with its details; and the retirement of the token quota fields of the tenant and of 0133's `quotas.llmCredits.perUser`, `perTenant`, and `global` under O16.

This is the step that adds budget objects, on which 0166.S2 waits under decision 1: 0166.S2 lists 0170.S1 in its `depends-on`.

It depends on 0133.S3 for the credit reservation, settlement, and recovery sweep it reserves over, to which 0133.S3 narrows under decision 9. It depends on 0149.S3 for the session's stored environment that the `environments` selector reads. The part that waits on 0143.S1 is the `principalKinds` selector, which needs the subject namespaces of service accounts, and the part that waits on 0149.S9 is the `groups` selector of decision 8.

### 0170.S2 Price lists and limits in money
- status: Planned
- aliases: [BG-5]
- lanes: [specification, code, documentation]
- depends-on: [0170.S1]

`budgets.currency`; the platform price list with its versions and validation, and a tenant's credit conversions with their order of application (decision 7); the `money` unit, with conversion at the version in force, rounding per charge, and the reservation of money equivalents for `llm` charges; the estimate label on every reported money figure; and no money on billing events (decision 27). The money equivalents of compute and storage charges land with 0170.S3 and 0170.S4, whichever of each pair merges second.

### 0170.S3 Compute budgets
- status: Planned
- aliases: [BG-4 (part), BG-6 (part)]
- lanes: [specification, code, documentation]
- depends-on: [0170.S1, 0121.S1, 0121.S2, 0146.S1]

The compute meter: compute units from a pod's resources, with the rates, the isolation multipliers, and the pins on resource classes and pools, written by a `platform-admin` alone (decision 4); the compute tick at bind, at each tick, and at release; admission of creations and resumes by the first tick; `onExhausted.running` with `continue`, `release`, and `end`; lease extension of session budget limits that count `compute`; and the recovery of a compute tick from `session_pod_windows`. It depends on 0121.S1 for resource classes in configuration, on 0121.S2 for `session_pod_windows`, and on 0146.S1 for `dormant`, into which `release` moves a session.

### 0170.S4 Storage budgets
- status: Planned
- aliases: [BG-4 (part), BG-6 (part)]
- lanes: [specification, code, documentation]
- depends-on: [0170.S1, 0131.S4, 0121.S4]

The `storage` meter: limits in `bytes` reserved at each upload, export, and checkpoint chunk grant in the owning session's budget and counted in its shared budgets through the flush, in place of `storage_quota_reserve.lua` and the tenant's `storageQuotaBytes`; the checkpoint of `release` admitted past an exhausted storage limit (decision 25); limits in `byteHours` with each budget's bytes held and the leader's accrual; the treatment of storage after a session ends under decision 21; the retirement of `STORAGE_QUOTA_EXCEEDED` and `StorageQuotaHigh`; and the rebuild of bytes held from the ledger. It depends on 0131.S4 for the pack catalog and its owner sessions, and on 0121.S4 for the storage records it rebuilds from.

### 0170.S5 Tenant-level meters
- status: Cancelled
- aliases: [BG-4 (part), BG-6 (part)]
- lanes: [specification, code, documentation]
- depends-on: [0170.S2, 0121.S3, 0121.S6, 0121.S7]

Cancelled by decision 29: the costs that 0121 attributes to a tenant and to no session feed no budget.
