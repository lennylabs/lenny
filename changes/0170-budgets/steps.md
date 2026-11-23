# Steps: 0170 Budgets

## Validation owed

| Item | Why it matters | Waits |
| :-- | :-- | :-- |
| Confirm that no route of §15.1, and no code path, changes a session's `labels` after creation. | BGR-4 computes the budget set once, from attributes fixed at creation. A label change would move a session between budgets. | 0170.S1 |
| Measure the latency of a credit reservation across budget sets of 1, 4, and 16 budgets on a Redis Cluster at the proxy's request rate, and the charge rate at which one budget's key serializes a tenant's calls. | O11 sets the allotment threshold from it, and every proxied call pays the reservation. | 0170.S1 |
| Measure the rebuild of a budget's open period from checkpoints and durable records after a Redis loss, for a tenant budget over a month of `llm_usage`, `session_pod_windows`, and `artifact_store` rows. | "BG-9" refuses a budget's charges until its rebuild ends. | 0170.S1 |
| Confirm that a session's directory groups are available at session creation for every identity source of 0149. | The `groups` selector of O5 reads them at creation. | 0170.S1 (part) |
| Measure the Redis write rate of the compute tick at the session counts of the Tier 3 sizing of §17.8, at the default tick of 60 seconds and at 10 seconds. | Every session holding a pod writes once per tick in every budget of its set. | 0170.S3 |
| Confirm that the release of a pod binding writes `closed_at` of `session_pod_windows` in the same transaction as the release in every release path of 0121, the eviction and the resume onto another pod included. | The recovery sweep settles an expired compute tick from `closed_at`. | 0170.S3 |

## Steps

The steps stay provisional until the owner settles the open decisions in `decisions.md`.

### 0170.S1 Budget objects and credit budgets
- status: Planned
- aliases: [BG-1, BG-2, BG-3, BG-7, BG-9, BG-10, BG-4 (part), BG-6 (part), BG-8 (part)]
- lanes: [specification, code, documentation]
- depends-on: [0133.S3, 0149.S3, 0143.S1 (part), 0149.S9 (part)]

Budget records with their selectors, references, partitions, periods, schedules, resets, thresholds, notices, modes, and exhaustion actions for new work; the session budget with its defaults and ceilings; the budget set and its invalidation; the credit meter, which reserves and settles each proxied call over the budget set in place of the scopes of 0133 "LU-6"; the reservation protocol across budgets under Redis Cluster with the recovery index, the rebuild, and any allotment O11 settles; lease extension of session budgets in credits; the reservation that funds another session's budget, which 0166.S2 calls to create, move, transfer, and release a carve; `budgetRefs` and `budget` on session creation; the routes, the RBAC rows, the audit events, the metrics, the usage reports, and `BUDGET_EXHAUSTED` with its details; and the retirement of the token quota fields of the tenant and of 0133's `quotas.llmCredits.perUser`, `perTenant`, and `global` under O16.

This is the step that adds budget objects, on which 0166.S2 waits under decision 1: 0166.S2 lists 0170.S1 in its `depends-on`.

It depends on 0133.S3 for the credit reservation, settlement, and recovery sweep it reserves over, and O6 asks whether 0133.S3 narrows to them. It depends on 0149.S3 for the session's stored environment that the `environments` selector reads. The part that waits on 0143.S1 is the `principalKinds` selector, which needs the subject namespaces of service accounts, and the part that waits on 0149.S9 is the `groups` selector of O5.

### 0170.S2 Price lists and money budgets
- status: Planned
- aliases: [BG-5]
- lanes: [specification, code, documentation]
- depends-on: [0170.S1]

`budgets.currency`; price lists with their versions and validation, and the tenant price lists O3 settles; the `money` unit, with conversion at the version in force, rounding per charge, and the reservation of money equivalents for credit charges; the estimate label on every reported money figure; and the billing-event outcome of O15. The money equivalents of compute and storage charges land with 0170.S3 and 0170.S4, whichever of each pair merges second.

### 0170.S3 Compute budgets
- status: Planned
- aliases: [BG-4 (part), BG-6 (part)]
- lanes: [specification, code, documentation]
- depends-on: [0170.S1, 0121.S1, 0121.S2, 0146.S1]

The compute meter: compute weights on resource classes, isolation profiles, and pools, written by a `platform-admin` alone; the compute tick at bind, at each tick, and at release; admission of creations and resumes by the first tick; `onExhausted.running` with `continue`, `release`, and `end`; lease extension of session budgets in compute units; and the recovery of a compute tick from `session_pod_windows`. It depends on 0121.S1 for resource classes in configuration, on 0121.S2 for `session_pod_windows`, and on 0146.S1 for `dormant`, into which `release` moves a session.

### 0170.S4 Storage budgets
- status: Planned
- aliases: [BG-4 (part), BG-6 (part)]
- lanes: [specification, code, documentation]
- depends-on: [0170.S1, 0131.S4, 0121.S4]

The storage meter: `storageBytes` reserved at each upload, export, and checkpoint chunk grant in every budget of the owning session's set, in place of `storage_quota_reserve.lua` and the tenant's `storageQuotaBytes`; `storageByteHours` with each budget's bytes held and the leader's accrual; the treatment of storage after a session ends under O12; the retirement of `STORAGE_QUOTA_EXCEEDED` and `StorageQuotaHigh`; and the rebuild of bytes held from the ledger. It depends on 0131.S4 for the pack catalog and its owner sessions, and on 0121.S4 for the storage records it rebuilds from.

### 0170.S5 Tenant-level meters
- status: Planned
- aliases: [BG-4 (part), BG-6 (part)]
- lanes: [specification, code, documentation]
- depends-on: [0170.S2, 0121.S3, 0121.S6, 0121.S7]

Under O13, the `poolIdle`, `dedicatedNodes`, and `escrow` meters, charged to tenant budgets when 0121's emitter commits each periodic event, with the refusal of new sessions in an exhausted tenant. The step is cancelled if the owner chooses option (b) of O13.
