# Steps: 0170 Budgets

## Validation owed

| Item | Why it matters | Waits |
| :-- | :-- | :-- |
| Confirm that no route of §15.1, and no code path, changes a session's `labels` after creation. | BGR-4 computes the budget set once, from attributes fixed at creation. A label change would move a session between budgets. | 0170.S1 |
| Measure the latency of a compute tick's reservation in a session budget of one to eight limits, and the ingestion of a batch of 1000 charges, on a Redis Cluster. | Every tick pays the reservation (decision 24), and "BG-12" sizes its batches. | 0170.S1, 0170.S6 |
| Measure a shared budget's overshoot against `budgets.shared.flushSeconds` at 1, 5, and 15 seconds, with the replica counts and charge rates of the Tier 3 sizing of §17.8, and the Redis write rate of the flush. | The default of `budgets.shared.flushSeconds` and the bound of BGR-9 for shared budgets come from it. | 0170.S1 |
| Measure the rebuild of a budget's open period from checkpoints and durable records after a Redis loss, for a tenant budget over a month of `external_charges`, `session_pod_windows`, and `artifact_store` rows. | "BG-9" refuses a budget's charges until its rebuild ends. | 0170.S1 |
| Confirm that a session's directory groups are available at session creation for every identity source of 0149. | The `groups` selector of decision 8 reads them at creation. | 0170.S1 (part) |
| Measure the Redis write rate of the compute tick at the session counts of the Tier 3 sizing of §17.8, at the default tick of 60 seconds and at 10 seconds. | Every session holding a pod writes once per tick in its session budget, and its shared budgets receive the tick through the flush. | 0170.S3 |
| Confirm that the release of a pod binding writes `closed_at` of `session_pod_windows` in the same transaction as the release in every release path of 0121, the eviction and the resume onto another pod included. | The recovery sweep settles an expired compute tick from `closed_at`. | 0170.S3 |

## Steps

The owner settled decisions 1 to 70 of `decisions.md` on 2026-11-23 and 2026-11-24, and the steps below follow them.

### 0170.S1 Budget objects
- status: Planned
- aliases: [BG-1, BG-2, BG-3, BG-7, BG-9, BG-10, BG-4 (part), BG-6 (part), BG-8 (part)]
- lanes: [specification, code, documentation]
- depends-on: [0149.S3, 0143.S1 (part), 0149.S9 (part), 0141.S1 (part), 0149.S12 (part)]
- closes: [F-11.2.34]

Budget records with their limits, the meters of "BG-4" with the unit each fixes and the refusal of a limit whose meters differ in unit or that names `unit` (decisions 18, 66, and 67), their selectors, references, partitions, periods, schedules, resets, thresholds, notices, modes, and exhaustion actions for new work; the `Warning` on a budget whose label is free in a selected environment (decision 49); `maxPartitions` with `BUDGET_PARTITIONS_FULL` and the places freed at boundaries, resets, and session ends (decision 56); the session budget with its defaults, ceilings, and `defaultDelegationFraction`, its `enforce` mode, and `onExhausted` resolved to the strictest across levels with `continue` on a limit that counts a compute meter refused on every route (decision 50); the budget set of a derive from its own request (decision 48); the budget set and its invalidation; the reservation of session budgets with the recovery index, the flush of shared budgets with each replica's cached state and watermark, the fail-open share and the turn to refusing (decision 35), the recovery of a lapsed replica's unflushed charges with the fence of a returning replica, and the rebuild, with no TTL on a budget's keys (decisions 24 and 43); the caps of "BG-1", partitions qualified by tenant, and `per: tenant`; "Read session attributes" (decision 40); `budget_notice` to the client (decision 45); lease extension of session budget limits; the reservation that funds another session's budget, which 0166.S2 calls to create, move, transfer, and release a carve; `budgetRefs` and `budget` on session creation, and their additions by a `PreRoute` interceptor named in `usableBy` (decision 13); the routes, the RBAC rows, the audit events, the metrics, the usage reports, and `BUDGET_EXHAUSTED` with its details; and the retirement of the token quota fields of the tenant and of 0133's `quotas.llmCredits.perUser`, `perTenant`, and `global` under decision 30. It removes the in-process session budget enforcer with `GWSEAM-TerminateForBudget`, `GWSEAM-TrackedBudgetSessions`, and the state `budget_state_sweep.go` sweeps, and reshapes or removes `GWSEAM-ForgetBudget`, which 0133.S3 leaves to it, and closes F-11.2.34 by holding a session budget in Redis for every replica.

This is the step that adds budget objects, on which 0166.S2 waits under decision 1: 0166.S2 lists 0170.S1 in its `depends-on`. The tree budget of §8.3, `maxTokenBudget`, and the TTL of the tree's keys stay until 0166.S2 replaces them with the tree counter and caps, so `delegate_task` carves tokens as today between the two steps, and 0166.S2 removes `delegation.budgetKeyTTLSeconds`, `BUDGET_KEYS_EXPIRED`, `GWSEAM-RearmBudget`, and `GWSEAM-HandleBudgetKeysExpired` with the tree budget (decision 43).

It depends on 0149.S3 for the session's stored environment that the `environments` selector reads. The part that waits on 0143.S1 is the `principalKinds` selector, which needs the subject namespaces of service accounts, the part that waits on 0149.S9 is the `groups` selector of decision 8, the part that waits on 0141.S1 is the attachment of budgets by an interceptor, whose payload fields and add-only check 0141.S1 adds, and the part that waits on 0149.S12 is the `Warning` on a budget that selects on a label key no rule governs (decision 41).

### 0170.S2 Price lists and currency limits
- status: Planned
- aliases: [BG-5]
- lanes: [specification, code, documentation]
- depends-on: [0170.S1]

`budgets.currency`; the platform price list with its versions and validation, the tenant price lists that a `platform-admin` writes, the lookup of one price per charge, and the effective prices a tenant reads (decision 54); the currency meters `computeCost` and `storageCost` priced at the version in force with rounding per charge, and `budgets.currency` named once, with no currency field on a limit or a price list version and a charge's optional `currency` checked against it (decision 66); the estimate label on every reported money figure; and no money on billing events (decision 27). The money equivalents of compute and storage charges land with 0170.S3 and 0170.S4, whichever of each pair merges second.

### 0170.S3 Compute budgets
- status: Planned
- aliases: [BG-4 (part), BG-6 (part)]
- lanes: [specification, code, documentation]
- depends-on: [0170.S1, 0121.S1, 0121.S2, 0146.S1, 0135.S5 (part), 0146.S2 (part)]

The compute meters `computeCost` and `computeUnits`, the latter with amounts in compute-unit-hours (decision 67): compute units from a pod's resources, with the rates, the isolation multipliers, and the pins on resource classes and pools, written by a `platform-admin` alone (decision 4); the compute tick at bind, at each tick, and at release; admission of creations and resumes by the first tick; `onExhausted.running` with `continue`, which a limit that counts a compute meter refuses, `suspend`, and `end`, triggered by the first refused charge of any meter the limit counts and by a compute tick refused on a paused session that holds a pod (decision 51), with the budget suspension of a subtree, its pod hold under `budgets.suspendPodHoldSeconds`, and its resume when the limit gains room (decision 42); lease extension of session budget limits on the compute meters; the `computeUnitsPerSecond` pin in 0121's `resourceClasses` and the rate column of `session_pod_windows`; and the recovery of a compute tick from `session_pod_windows`. It depends on 0121.S1 for resource classes in configuration, on 0121.S2 for `session_pod_windows`, and on 0146.S1 for `suspended`, `blocked`, and `suspension_cause`, which a budget suspension uses. The `suspended` child event of 0135 "MD-22" with the reason `budget` waits on 0135.S5 and 0146.S2, which add child events.

### 0170.S4 Storage budgets
- status: Planned
- aliases: [BG-4 (part), BG-6 (part)]
- lanes: [specification, code, documentation]
- depends-on: [0170.S1, 0131.S4, 0121.S4, 0146.S1, 0170.S3 (part), 0144.S1 (part), 0135.S4 (part), 0153.S2 (part)]

The storage meters `storageCost` and `storageBytes` (decision 67): capacity limits on `storageBytes`, with amounts as quantity strings, counted at the tree's root and bounded by a child's cap, reserved at each upload, export, and checkpoint chunk grant in the owning session's budget and counted in its shared budgets through the flush, in place of the storage quota's `reserveScript` and the tenant's `storageQuotaBytes`; the count of a write to a shared storage limit at admission, its check against the cached remaining amount, and its correction at completion, with the stream cut past its declared size (decisions 36 and 53); the checkpoint that releases a pod, at the end of a budget suspension's hold or at a move to `dormant`, admitted past an exhausted storage limit (decisions 25, 28, and 42); the refusal of new and resumed sessions while a storage limit is exhausted (decision 37); a derive's upload copies as declared writes (0144); limits on `storageCost` with each budget's bytes held and the leader's accrual; the treatment of storage after a session ends under decision 21; the retirement of `STORAGE_QUOTA_EXCEEDED` and `StorageQuotaHigh`; and the rebuild of bytes held from the ledger. It depends on 0131.S4 for the pack catalog and its owner sessions, on 0121.S4 for the storage records it rebuilds from, on 0146.S1 for `dormant` and resume admission, and on 0170.S3 for the checkpoint at the end of a budget suspension's hold. The parts that wait on 0144.S1, 0135.S4, and 0153.S2 are the writes of a derive's upload copies, an output file's export of "MD-18", and an upload through `lenny-materialize`, which each of those steps adds.

### 0170.S5 Tenant-level meters
- status: Cancelled
- aliases: [BG-4 (part), BG-6 (part)]
- lanes: [specification, code, documentation]
- depends-on: [0170.S2, 0121.S3, 0121.S6, 0121.S7]

Cancelled by decision 29: the costs that 0121 attributes to a tenant and to no session feed no budget.

### 0170.S6 External charges
- status: Planned
- aliases: [BG-12, BG-4 (part), BG-6 (part)]
- lanes: [specification, code, documentation]
- depends-on: [0170.S1, 0170.S2, 0124.S2, 0127.S4 (part), 0146.S1 (part)]

The `externalCost` meter of decisions 57 and 67 and "BG-12": the reporter registry, `POST /v1/charges` with its batch limits, proofs by `jti` checked against the Token Service's mint index, the refusal of a `jti` minted under a key retired in an emergency rotation, the refusal of a tenant reporter on another tenant's host, the checks on each charge, results per charge, idempotency, the per-reporter rate, voids, the `external_charges` record and its place in the recovery sweep, the suspension of attribution bindings at exhaustion, the neutral `usage` member, the attribution assertions that a session's owner mints and the charges it posts with `POST /v1/sessions/{id}/attribution-tokens` and `POST /v1/sessions/{id}/charges` (decision 61), limit notices with their checks and the suspension they cause under `suspension_cause: external_limit` (decision 68), the deletion of a session budget's keys `budgets.external.maxReportDelaySeconds` after the session ends, and the metrics and audit events. It depends on 0124.S2 for the attribution assertion and its mint index, and the part that waits on 0127.S4 is the suspension of attribution bindings, which 0127.S4 attaches and suspends. The part that waits on 0146.S1 is the suspension of a session by a limit notice, which uses `suspended`, `blocked`, and `suspension_cause`.
