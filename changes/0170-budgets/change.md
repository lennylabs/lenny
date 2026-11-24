# Budgets

Status: `Draft`, revision 2, 2026-11-23. Programs: [v1](../programs/v1.md).

Each factual claim about behaviour carries one of these tags where the source matters:

| Tag | Meaning |
| :-- | :-- |
| **[source]** | Read in the Lenny tree, the specification, or another change on 2026-11-23. |
| **[design]** | What this change proposes. Nothing implements it, and the owner has settled none of it unless "Decisions" in `decisions.md` says so. |
| **[unverified]** | A claim this change makes without having confirmed it in the tree. "Validation owed" in `steps.md` lists each one. |

## Problem

"Terms", after "Scope", defines the vocabulary this document uses.

The owner asked on 2026-11-23 for budgeting to be enhanced in a change of its own:

> At a high-level, what I want is:
> - Budgeting by credits and not tokens
> - Budgeting by infrastructure resources (compute time, storage, etc.). The operator would need to translate that into $ so we need to think about that (a budget may be set in terms of total storage or $ for storage)
> - Budget objects like you described. Session budget would become an object and the session would match to it plus any other applicable active budget object
> - Budgets can reset on demand or every X time (including beginning of month, week, etc.)

"Budget objects like you described" refers to an option of what was open decision O4 of [`0166-delegation-primitives`](../0166-delegation-primitives/change.md): a budget resource that a client or a tenant administrator creates and names, from which sub-budgets are carved for child sessions and for unrelated sessions, serving a team's or a project's spending limit across many session trees and a workflow engine that runs many root sessions under one limit. The owner then settled it as decision 12 of [`0166-delegation-primitives`](../0166-delegation-primitives/change.md) on 2026-11-23: a child session's carved budget is a reservation from the budget objects that apply to the parent, with the child's share as a budget object of the child's session funded by that reservation, and step 0166.S2 depends on 0170.S1, the step of this change that adds budget objects. Under 0166's decision 35 the carve's mechanics belong to this change, which states them in full ("BG-8"), and 0166's ["DP-3"](../0166-delegation-primitives/change.md#dp-3-carved-budget) cites it.

Today's budgets do not meet that request **[source]**:

- [§11.2](../../spec/11_policy-and-controls.md#112-budgets-and-quotas) states budgets as fixed scopes: LLM token limits per request, session, user window, tenant window, global window, and task tree, a per-tenant storage quota in bytes, and a wall-clock limit per session. Each scope has its own counter, its own setting, and its own enforcement path. No scope covers a team, a project, a label, a runtime, or a set of unrelated root sessions.
- [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md#lu-6-credit-budgets) converts the token limits into credit limits at the same fixed scopes ("LU-6"). Its credits are unitless and tied to no currency by decision D3 of that change.
- Pod time and stored bytes have no budget. Under [`0121-billing-resource-attribution`](../0121-billing-resource-attribution/change.md), Lenny records pod time and byte-hours for the operator's billing system, and no budget reads them. The per-tenant storage quota limits bytes held, and nothing limits byte-hours or the money stored data costs.
- Nothing in the specification or the tree states a price or a currency, so no budget can be stated in money.
- Quota windows reset on fixed UTC boundaries (hourly, daily, monthly) or roll. No budget resets on demand, at the start of a week, in a time zone, or on an operator's schedule.

## Scope

**Proposed [design]:**

- Budget objects: named records that state a limit in one unit, the sessions they apply to, a reset schedule, thresholds, and the action at exhaustion, created by a platform administrator, a tenant administrator, or a client for its own sessions ("BG-1").
- The session budget as a budget object, and the carve of 0166's "DP-3" over budget objects ("BG-2", "BG-8").
- Matching: how a session and each charge find every active budget that applies ("BG-3").
- Meters and units: credits for model calls, compute units for the time a session holds a pod, and bytes and byte-hours for stored data, with the tenant-level meters of 0121 ("BG-4").
- Price lists: the operator's translation of each unit into an amount of money, versioned, and how a money budget counts charges that arrive in several units ("BG-5").
- Enforcement: where each meter reserves and charges, the soft thresholds, the actions at exhaustion, and lease extension for session budgets ("BG-6").
- Resets: on demand and on a schedule, with time zones, rollover, and the treatment of reservations held across a reset ("BG-7").
- Counter storage, failure behaviour, and the precision of each meter ("BG-9").
- API routes, RBAC, audit events, metrics, and usage reports by budget object ("BG-10").
- A statement of what this change supersedes in 0133 and in 0166 ("BG-11").

**Out of scope:**

- Charging, invoicing, payments, and taxes. Lenny charges no one, and a price list is an operator setting that Lenny uses for enforcement and reports as an estimate.
- The cost the operator pays its providers. The operator's billing system prices the quantities in the billing events, as 0121 states, and decides whether to show Lenny's estimate.
- Calls a runtime sends to a provider outside the LLM proxy, which 0133 leaves unmetered (OD-15 of 0133).
- Concurrency and rate limits, which stay quotas under [§11.1](../../spec/11_policy-and-controls.md#111-admission-and-fairness).
- Costs that reach no tenant: unpinned idle pods on shared pools, node capacity that no pod requests, and data held in Postgres, as 0121 states.
- Egress bytes. `egress.tunnel` records of `0127-egress-and-git` carry bytes per tunnel and are lossy ("Current state"), so this change meters no egress (O19).

## Terms

| Term | Meaning |
| :-- | :-- |
| Budget, or budget object | A named record that states a limit in one unit, the charges it counts, which sessions it applies to, a period with its reset schedule, thresholds, and the action at exhaustion ("BG-1"). |
| Session budget | The budget object of one session, created with the session and ending with it. It applies to the session and, through carves, funds its descendants ("BG-2"). |
| Shared budget | Every budget object other than a session budget: a platform, tenant, or principal budget. |
| Platform budget, tenant budget, principal budget | A shared budget written by a `platform-admin` and able to span tenants, by a `tenant-admin` for its own tenant, or by a user or a service account for its own sessions. |
| Meter | A source of charges in one unit: the credit meter at the LLM proxy, the compute meter while a session holds a pod, the storage meters at write time and over time, and the tenant-level meters ("BG-4"). |
| Unit | What a limit counts: `credits`, `computeUnits`, `storageBytes`, `storageByteHours`, or `money`. |
| Charge | An amount in one unit, from one meter, attributed to one subject. |
| Subject | What a charge is attributed to: a session, with its tenant, environment, principal, groups, runtime, pool, labels, and root session, or a tenant alone for a tenant-level meter. |
| Compute unit | One second during which a session holds one slot, multiplied by the compute weight of its pod and divided by the pod's slot count ("BG-4"). |
| Compute weight | The multiplier of a pod's slot time, resolved from the pool, the resource class, and the isolation profile, which a `platform-admin` sets ("BG-4"). |
| Price list | The operator's record that translates credits, compute units, and stored bytes over time into an amount of money in the deployment's currency, with versions by `effectiveFrom` ("BG-5"). It differs from 0133's rate card, which prices tokens in credits. |
| Money budget | A budget whose unit is `money`. It counts the charges of the meters it names, each converted at the price-list version in force when the charge occurred. |
| Selector | The attributes of a shared budget that decide which subjects it applies to ("BG-3"). |
| Reference | The name of a shared budget that a session creation lists in `budgetRefs`, which attaches the budget to the session and its descendants ("BG-3"). |
| Budget set | The budgets that apply to one subject: its session budget, the shared budgets whose selectors match it, and the budgets it references, its ancestors' references included. |
| Partition | One counter of a budget whose `per` field names an attribute, such as one counter per user ("BG-1"). |
| Period | The interval over which a budget's counter accumulates, from one reset to the next. A budget with no schedule has one period for its life. |
| Reset | The close of a period and the start of the next, at a schedule boundary or on demand ("BG-7"). |
| Rollover | The part of a period's unused amount that a budget adds to its next period's limit ("BG-7"). |
| Reservation | An amount held against every budget of a charge's budget set before the work that incurs it runs, settled to the actual charge afterwards. |
| Carve | A reservation that funds a child session's budget from the budgets that apply to its parent ("BG-8"). |
| Threshold | A fraction of a budget's limit at which the gateway records a notice ("BG-6"). |
| Enforce mode and observe mode | A budget in `enforce` mode refuses what does not fit. A budget in `observe` mode counts and records notices and refuses nothing. |
| BG-n and BGR-n | The parts of this design under "Detailed design" carry BG-n, and its requirements carry BGR-n. |

### Terms from other designs and the platform

| Term | Meaning |
| :-- | :-- |
| Credits | 0133's unitless budget unit. An upstream's rate card prices each token class in credits, and Lenny stores credits as integer micro-credits ([`0133-llm-upstreams-and-cost-budgets`, "LU-5"](../0133-llm-upstreams-and-cost-budgets/change.md#lu-5-rate-cards)). |
| Credit reservation | 0133's reservation of a model call's output limit and input estimate before the proxy forwards it, settled to the call's estimated cost after the response ("LU-6" of 0133). |
| Lease extension | The [§8.6](../../spec/08_recursive-delegation.md#86-lease-extension) operation that raises a session's budget in `auto` mode or after a person approves an elicitation, with the outcomes `GRANTED`, `PARTIALLY_GRANTED`, `CEILING_REACHED`, and `REJECTED`. |
| `session.pod_usage` and `session_pod_windows` | 0121's billing event for the time a session holds a pod, and the Postgres row per pod binding from which the event is derived ([`0121-billing-resource-attribution`, "Resource fields on usage events"](../0121-billing-resource-attribution/change.md#resource-fields-on-usage-events)). |
| `storage.usage` and the artifact ledger | 0121's billing event for bytes held and byte-hours per tenant, store, and artifact type, computed from the `artifact_store` table, which holds one row per stored artifact with its tenant, session, type, size, and lifetime ([`0121-billing-resource-attribution`, "`storage.usage`"](../0121-billing-resource-attribution/change.md#storageusage)). |
| `pool.idle_usage` and `node.dedicated_usage` | 0121's periodic events for the idle pods of a tenant's owned pool or of a pinned shared pool, and for the nodes dedicated to one tenant. |
| Resource class | A named set of CPU, memory, and ephemeral storage requests for a pool's pods, held in the chart's `resourceClasses` map under 0121 ("BR-1"). |
| Isolation profile | A pool's sandbox: `standard`, `sandboxed`, or `microvm` ([§5.2](../../spec/05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). |
| Slot | One place for a session on an agent pod. A concurrent pod has more than one slot ([`0121-billing-resource-attribution`, "Terms"](../0121-billing-resource-attribution/change.md#terms)). |
| `dormant` | The state of [`0146-session-lifecycle`, "SL-1"](../0146-session-lifecycle/change.md#sl-1-states) in which a session waits for its client with its pod released and its workspace checkpointed. |
| Checkpoint packs | The storage unit of [`0131-incremental-checkpoint`, "IC-4"](../0131-incremental-checkpoint/change.md#ic-4-catalog-reference-closure-and-quota). Each pack is charged once, at confirmation, to the tenant and the session that own it. |
| Environment | A tenant's scope for sessions, with members, policies, and a session's stored environment ([`0149-tenant-scoping-identity`, "TS-1"](../0149-tenant-scoping-identity/change.md#ts-1-environment-scoping)). |
| Directory groups | The groups of a tenant's principals that 0149 records from its identity sources (step 0149.S9). |
| Service account | A principal with no person behind it, such as a CI system, under [`0143-service-account-credentials`](../0143-service-account-credentials/change.md). |
| Session `labels` | The client's map of strings on a session, set at creation, indexed, and filterable in `GET /v1/sessions`, `GET /v1/usage`, and `GET /v1/metering/events` ([§14](../../spec/14_workspace-plan-schema.md#14-workspace-plan-schema)). |
| Webhook subscriptions | The REST resource of [`0138-rest-only-client-api`, "RO-2"](../0138-rest-only-client-api/change.md#ro-2-rest-additions) (R6) through which a client receives a session's events at a URL. |
| `platform-admin`, `tenant-admin`, `tenant-viewer`, `billing-viewer`, and `user` | The built-in roles of the [§10.2](../../spec/10_gateway-internals.md#102-authentication) permission matrix. |

### Labels from other designs

| Labels | Source | What they name |
| :-- | :-- | :-- |
| LU-5, LU-6, LU-7, D3, OD-6, OD-15, and open decision 6 | [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/decisions.md#decisions) | Rate cards, credit budgets with their scopes and reservation, usage records, unitless credits, the estimated cost on billing events, budgets for proxied calls alone, and the credit reservation under Redis Cluster. |
| BR-1 to BR-7 | [`0121-billing-resource-attribution`](../0121-billing-resource-attribution/change.md#high-level-design) | Resource classes, session pod time, idle pods, stored bytes, reconciliation, escrow, and dedicated nodes as billing events. |
| SL-1, SL-6, and SL-10 | [`0146-session-lifecycle`](../0146-session-lifecycle/change.md#sl-6-idle-pods-and-dormancy) | The states, idle pods and dormancy, and admission, caps, and timers. |
| IC-4 | [`0131-incremental-checkpoint`](../0131-incremental-checkpoint/change.md#ic-4-catalog-reference-closure-and-quota) | The checkpoint catalog and its storage quota. |
| DP-1, DP-2, DP-3, DP-6, DP-7, and decisions 12, 13, 33, 35, 40, 43, and 44 | [`0166-delegation-primitives`](../0166-delegation-primitives/change.md) | The session grant with its root grant inputs and the resolution of every caveat ("Resolution" in DP-1, decision 40); transfer and release (DP-2, decision 33); the carved budget, which cites this change, and `session.set_limits` (DP-3); the disposition of each delegation setting (DP-6); what each change takes on (DP-7); the carve as a reservation from budget objects (decision 12); a child's age as a deadline (decision 13); ownership by concern (decision 35); this change's membership of the v1 program (decision 43); and `labels` set by the root's client and inherited unchanged, beside a parent's `tags` (decision 44). |
| SO-5a | [`0142-security-oversight`](../0142-security-oversight/change.md#so-5a-tool-use-approvals) | Tool-use approvals, whose `when.creditBudgetAbove` on a `session.create` rule holds a child whose carve in credits exceeds the amount, and a `session.set_limits` raise past it. |
| R6 | [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md#ro-2-rest-additions) | Webhook subscriptions. |

## High-level design

A budget is a named record that says how much of something a set of sessions may use in a period. An operator, a tenant administrator, or a client creates it. It names one unit: credits for model calls, compute units for the time a session holds a pod, bytes stored, byte-hours stored, or money. It names the sessions it applies to, either by their attributes, such as a tenant, an environment, a directory group, a runtime, or a label, or by being named when a session is created. It names when it resets: never, at the start of each day, week, month, or year in a time zone, on a cron schedule, or every fixed interval, and anyone allowed to change it can also reset it on demand.

Every session can also have its own session budget, which is a budget object like the others and ends with the session. When a session uses something, the gateway finds every active budget that applies to it, its session budget included, and the use must fit every one of them. Before a model call, the proxy holds the call's worst case against every budget, as 0133 does today against its fixed scopes. While a session holds a pod, the gateway holds one minute of compute at a time. Before a write to storage, the gateway holds the bytes. If any budget lacks room, the work is refused, and the budget's exhaustion action applies to running sessions: a model call is refused, a session releases its pod and becomes `dormant`, or a session ends, as the budget states. Stored data is never deleted because a budget ran out.

A money budget counts several units at once. The operator writes a price list that gives a price per credit, per compute unit-hour, and per GiB-month stored, in the deployment's currency. Each charge is converted at the price in force when it occurred, so a team's monthly money budget counts its model calls, its pod time, and its stored data together. The price list is an operator setting. Lenny reports its money figures as estimates and charges no one.

A delegated child gets a carve: its budget is reserved from every budget that applies to its parent, and its own use counts inside that reservation, so each budget counts the child once. When the child ends, the unused part returns. This is the model the owner chose in decision 12 of 0166, whose "DP-3" cites this change for it.

Budgets keep their counters in Redis and their definitions and closed periods in Postgres. A gateway crash loses no charge, because each reservation expires into a recovery sweep that settles it from the meter's durable record. Each meter has a stated precision: a model call can overshoot by its input estimate's error, a session holding a pod by one compute tick, and a storage write by the checkpoint grant window that §11.2 states today.

```
 charge sources (meters)                 gateway                                   stores
 ───────────────────────                 ───────                                   ──────
 LLM proxy: model call  ──credits──┐
 pod binding: each tick ──compute──┼──>  subject (session attributes)
 storage write / accrual ─bytes────┤       │
 tenant-level (0121) ─────────────-┘       ▼
                                         budget set = session budget
                                                    + shared budgets whose selector matches
                                                    + budgets referenced by the session or an ancestor
                                           │
                                           ▼
                                         for each budget: convert to its unit
                                         (money: price-list version in force)
                                           │
                                           ▼
                                         reserve in every budget ──fits──> work runs, settle
                                           │                                    Redis counters
                                           └──does not fit──> refuse, exhaustion action, notices
                                                                                Postgres: budgets,
                                                                                periods, price lists
```

## Requirements

| ID | Requirement |
| :-- | :-- |
| BGR-1 | Every charge has one meter, one unit, and one subject, and the gateway applies it to every budget in the subject's budget set that counts that meter. |
| BGR-2 | No reservation is admitted unless every budget of the charge's budget set that is in `enforce` mode and counts the charge's meter has room for it. A budget in `observe` mode counts the charge and refuses nothing. |
| BGR-3 | A principal budget applies only to sessions whose creation names it in `budgetRefs`, and to their descendants. A tenant budget never applies to a session or a charge of another tenant. |
| BGR-4 | A session's budget set is computed from attributes fixed when the session is created, so it changes only when a budget is created, changed, enabled, disabled, or deleted. |
| BGR-5 | A money budget converts each charge at the price-list version in force at the charge's time. Price-list versions are append-only, a version whose `effectiveFrom` is in the past is never written, and no release of Lenny carries a price. |
| BGR-6 | A budget with a schedule starts a new period at each boundary of its schedule in its time zone, and a reset on demand starts one at once. Each closed period is recorded with its consumption by meter, and each reservation outstanding at the reset is carried into the new period. |
| BGR-7 | A child's carve is reserved from every budget that applies to its parent and has room for it, the child's charges inside the carve count once in each of those budgets, and the carve's unused amount returns to them when the child ends. |
| BGR-8 | The exhaustion of a budget never deletes or truncates stored data. |
| BGR-9 | Overshoot is bounded per meter: for credits, by the input estimate's error on the calls in flight, as LUR-7 of 0133 states; for compute units, by one compute tick per session that holds a pod in the budget; for stored bytes, by the outstanding checkpoint grant window of §11.2. |
| BGR-10 | A gateway replica's crash loses no charge: every reservation of the replica is settled by the recovery sweep from its meter's durable record, and a Redis loss rebuilds no counter below the consumption that Postgres records for its period. |
| BGR-11 | The gateway records an audit event for each creation, change, deletion, and reset of a budget, each threshold a budget's partition crosses in a period, each exhaustion, and each price-list version. |
| BGR-12 | A budget's usage report states, for each period and partition, the consumption by meter, the reserved amount, the limit, and, for a money budget, the money amount labelled as an estimate. |

## Current state

**Budgets are fixed scopes [source].** The [§11.2](../../spec/11_policy-and-controls.md#112-budgets-and-quotas) table lists token limits per request, session, user window, tenant window, global window, and task tree; runtime limits per session and per child; a retry budget; a delegation budget of depth, fan-out, and total children; concurrent sessions per tenant and per user; a per-tenant storage quota; and eval submission rate limits. **Hierarchical Quota Model:** states that soft warnings are emitted at 80% of a quota as billing events, that hard limits refuse at 100% with `QUOTA_EXCEEDED`, and that "Quota reset periods are configurable per quota type: hourly, daily, monthly, or rolling window."

**Windows are fixed UTC buckets [source].** `quota.ResetPeriod` in `pkg/quota/quota.go` is the closed set `hourly`, `daily`, `monthly`, and `rolling`. `window` in `pkg/quota/quotastore/quotastore.go` maps a period to a key label from the UTC time, such as `monthly-202611`, and refuses `rolling`, which the fixed-window store does not keep. No time zone, weekly period, or on-demand reset exists. The tenant record carries `StorageQuotaBytes`, `TokenQuotaPerWindow`, and `QuotaResetPeriod` (`pkg/tenancy/tenantstore/tenantstore.go`).

**Separate enforcement paths and counters [source].**

- Session token budget: `sessionbudget.Enforcer` (`pkg/gateway/meteringproxy/sessionbudget`) counts per replica in process memory and refuses after exhaustion, which F-11.2.34 records and 0133.S3 closes with Redis reservation.
- Tree budget: `budget_reserve.lua`, embedded in `pkg/gateway/platformtools/treebudget/treebudget.go`, carves a child's token budget from its parent at delegation, with keys under the `{root_session_id}` hash tag ([§8.3](../../spec/08_recursive-delegation.md#83-delegation-policy-and-lease), **Budget Reservation Model:**).
- Storage quota: `storage_quota_reserve.lua` in `pkg/quota/storagequota/redisstore` reserves bytes against the tenant's `storageQuotaBytes` before an upload or a checkpoint write, and the counter is rebuilt from the `artifact_store` sum and the outstanding checkpoint reservations (§11.2, **Storage quota enforcement mechanism.**).

**Lease extension is per session or tree [source].** [§8.6](../../spec/08_recursive-delegation.md#86-lease-extension) extends `maxChildrenTotal`, `maxParallelChildren`, `maxTokenBudget`, `maxTreeSize`, `perChildMaxAge`, and `fileExportLimits` in `auto` or `elicitation` mode, within ceilings resolved from the deployment, the tenant, and the runtime.

**What the tree meters [source].**

| Resource | Metered today | Planned by a v1 change |
| :-- | :-- | :-- |
| Model calls | Input and output token counts per proxied call (`pkg/gateway/meteringproxy/metering`), with cache tokens missed (F-11.2.38). | Token classes, credits, and one `llm_usage` row per call (0133 "LU-5" to "LU-7"). |
| Pod time of a session | No billing event carries pod time. `usagestore.Record` sets no `PodMinutes`, so `totalPodMinutes` sums zeros (0121 "Current state", read 2026-11-05, paths updated 2026-11-19). | `session.pod_usage` per pod-binding window with the pod's effective requests and slot count (0121 "BR-2"). |
| Idle pods | `lenny_warmpool_idle_pod_minutes` by pool and resource class, with no tenant (`pkg/controller/warmpool/idle_meter.go`). | `pool.idle_usage` for owned pools and pinned idle pods (0121 "BR-3"). |
| Dedicated nodes | Nothing. | `node.dedicated_usage` (0121 "BR-7"). |
| Stored bytes | The per-tenant Redis counter of the storage quota, and the `artifact_store` ledger. | `storage.usage` with bytes held and byte-hours per tenant, store, and artifact type (0121 "BR-4"), and legal-hold escrow (0121 "BR-6"). Checkpoint packs carry `owner_session_id` (0131 "IC-4"). |
| Egress bytes | Nothing as a meter. | `egress.tunnel` audit records carry bytes in each direction and can lack them, because Envoy's access log stream is lossy ([`0127-egress-and-git`](../0127-egress-and-git/change.md)). |
| Postgres-held data | Nothing. | A per-tenant gauge with no billing event (0121 "BR-5"). |

**No money [source].** No field of the specification or the tree states a price in money or a currency; the word "currency" appears in `spec/` and `pkg/` only for the freshness of CRD versions. 0133 decision D3 states credits as "unitless, tied to no currency", and its OD-6 makes the estimated credit cost of LLM calls the one exception to 0121's rule that Lenny records quantities and the operator prices them.

**Roles [source].** The [§10.2](../../spec/10_gateway-internals.md#102-authentication) permission matrix gives "Manage quotas" to `platform-admin` for all tenants and to `tenant-admin` for its own tenant, read-only to `tenant-viewer`, and none to `billing-viewer` and `user`. "View usage / metering" is open to every role but `user`.

**Labels are set at creation [source].** §14 defines session `labels` on the creation request. No route of §15.1 changes a session's labels after creation **[unverified]**: the search found none, and "Validation owed" lists it.

**No step has started [source].** Every step of 0121, 0131, 0133, 0146, and 0149 is `Planned` on 2026-11-23, and every step of 0166 is `Planned` as well. This change therefore replaces design text and no built code, apart from the enforcement paths above, which predate the v1 program.

## Detailed design

The detail below is a framing. Each part names the open decision in `decisions.md` that settles it.

### BG-1 Budget objects

A budget is a record with these fields **[design]**:

| Field | Meaning |
| :-- | :-- |
| `name` | Unique within its scope: the platform, one tenant, or one principal. |
| `scope` | `platform`, `tenant`, `principal`, or `session`, set by the route that creates it. |
| `limit` | `{amount, unit}`. `unit` is `credits`, `computeUnits`, `storageBytes`, `storageByteHours`, or `money`. An amount is a decimal number in its unit, and the gateway stores it as an integer in the unit's smallest step: micro-credits, milli-compute-units, bytes, byte-seconds, and micro-units of the currency. One compute unit is one weighted slot-second, and one storage byte-hour is one byte held for one hour. |
| `meters` | For a `money` budget, the meters whose charges it counts: any of `credits`, `compute`, `storage`, `poolIdle`, `dedicatedNodes`, and `escrow` (O13). It defaults to every meter the price list prices. A budget in another unit counts the meter of its unit alone. |
| `appliesTo` | The selector of "BG-3". A principal budget has none. |
| `attach` | `selector`, `reference`, or `both`. A principal budget is always `reference`. |
| `usableBy` | For a tenant budget with `attach` including `reference`, the principals and directory groups that may name it in `budgetRefs`. |
| `per` | Optional partition key: `user`, `session`, `rootSession`, `environment`, `runtime`, or `label:<key>`. The budget keeps one counter for each value, each with the full limit (O18). |
| `period` | The schedule of "BG-7", with its time zone and rollover. `storageBytes` budgets have no period, because they limit what is held at each moment. |
| `thresholds` | Fractions of the limit, default `[0.8, 1.0]`, at most `budgets.maxThresholds` (default 8). |
| `onExhausted` | The actions of "BG-6" for new work and for running sessions. |
| `mode` | `enforce`, the default, or `observe`. |
| `notify` | Optional webhook targets for the notices of "BG-6" (O14). |
| `startsAt`, `endsAt` | Optional. A budget is active between them and in `enforce` or `observe` mode, and `disabled: true` makes it inactive without deleting it. |
| `resourceVersion` | The version a `PUT` names in `If-Match`. |

**Who creates what [design].** A budget only narrows what a session may do, because every charge must fit every budget of its set. The authority to create one therefore follows the reach of its selector:

| Creator | Route | Scope | Selector | Attachment |
| :-- | :-- | :-- | :-- | :-- |
| `platform-admin` | `/v1/admin/budgets` | `platform`, across every tenant or a listed set, or `tenant` for any tenant | Any field of "BG-3" | `selector`, `reference`, or `both` |
| `tenant-admin` | `/v1/admin/budgets` | `tenant`, own tenant | Any field of "BG-3" except `tenants` | `selector`, `reference`, or `both` |
| A user or a service account | `/v1/budgets` | `principal` | None | `reference`, by the creator alone |
| Session creation | `POST /v1/sessions` | `session` | The session and, through carves, its descendants | Implicit ("BG-2") |

A principal budget cannot reach another principal's session, because only its creator can name it and a reference attaches only at the creation of the creator's own session. The gateway refuses a write beyond `budgets.maxPerTenant` (default 500) tenant budgets per tenant or `budgets.maxPerPrincipal` (default 20) principal budgets per principal with `429 QUOTA_EXCEEDED`, and a session creation that names more than `budgets.maxRefsPerSession` (default 8) references with `400 VALIDATION_ERROR` (O4).

**Defaults for a new tenant [design].** `budgets.tenantDefaults`, a Helm list of budget templates, is instantiated as tenant budgets when a tenant is created, such as a storage cap in `storageBytes`. A `tenant-admin` may change or delete a budget instantiated this way. A `platform-admin` who wants a limit a tenant cannot remove writes a platform budget instead. This replaces the tenant's `storageQuotaBytes` and `TokenQuotaPerWindow` fields (O16).

### BG-2 Session budgets

A session creation may carry `budget`, which creates the session's budget object **[design]**:

```json
{
  "budget": {
    "limits": { "credits": 400000, "computeUnits": 14400, "money": 25.00 },
    "thresholds": [0.5, 0.9],
    "onExhausted": { "running": "release" }
  }
}
```

- **Limits in several units.** A session budget holds one limit per unit it names. It is one object with one counter per unit, so a session's own limit can cap credits and compute together.
- **Defaults and ceilings.** A session budget's amount in each unit resolves under "Resolution" in [DP-1 of 0166](../0166-delegation-primitives/change.md#dp-1-session-grant), which states the order of defaults and ceilings for every limit a session receives (0166 decisions 40 and 49). This change names the per-unit settings that resolution reads: `budget` on the creation request as the requested value, `sessionBudgetDefaults` on a runtime, an environment, a tenant, and the platform as the defaults, and `sessionBudgetCeilings` at the same levels as the ceilings. A session with no default and no request has no session budget and is bound by its shared budgets alone.
- **Lifetime.** The session budget has no schedule. It is created in the transaction that creates the session, it stops counting when the session reaches a terminal state, and its record is kept with the session's usage records for the session's retention. Storage held after the session ends counts against the shared budgets alone (O12).
- **Changes.** The session's owner reads it with `GET /v1/sessions/{id}/budget` and raises or lowers a limit with `PATCH /v1/sessions/{id}/budget` within the ceilings. A raise beyond the ceilings goes through lease extension ("BG-6").
- **Removed fields.** `maxTokenBudget` of the delegation lease and `maxCreditBudget`, which 0133 puts in its place, leave every input of a session grant under 0166's "DP-1": a client's `delegationLease`, a runtime's default lease, the delegation presets, and the tenant and environment ceilings carry no budget amount, and `budget`, `sessionBudgetDefaults`, and `sessionBudgetCeilings` take their place. Under decision D9 of 0133, a write that names either is refused with `400 VALIDATION_ERROR` naming the field.

### BG-3 Matching

**Selectors [design].** A selector is a conjunction of fields. Within a field, a list matches any of its values. An absent field matches every value.

| Field | Matches | Who sets the attribute |
| :-- | :-- | :-- |
| `tenants` | The session's tenant. Platform budgets alone. | The platform. |
| `environments` | The session's stored environment under 0149 "TS-1". | The client chooses among environments it is a member of. |
| `principals` | The session owner's subject, a user or a service account. | The identity provider. |
| `principalKinds` | `user` or `serviceAccount`. | The identity provider. |
| `groups` | The owner's directory groups at the session's creation (0149.S9, O5). | The identity provider or the directory. |
| `runtimes` | The session's runtime after the `PreRoute` chain. | The client and the routing interceptors. |
| `pools` | The session's pool. | The routing interceptors. |
| `labels` | Each key and value of `matchLabels` among the session's `labels`. | The client. |

A charge of a tenant-level meter has a tenant as its subject and matches only a budget whose selector names no field other than `tenants`.

**References [design].** `budgetRefs` on `POST /v1/sessions` names shared budgets by name, a tenant budget as `tenant/<name>` and a principal budget as `principal/<name>`. The gateway refuses a name the caller may not use with `403 BUDGET_NOT_ATTACHABLE`, and one that does not exist with `404 RESOURCE_NOT_FOUND`, so that a caller learns no more about another principal's budgets than about a missing one. A child of any depth inherits its ancestors' references, which serves a named session tree: a client creates a principal budget and names it on the root, and the whole tree draws on it.

**The budget set [design].** At creation, the gateway computes the session's budget set and stores the identifiers on the session row. Every attribute a selector reads is fixed at creation, so the set changes only when a budget changes. On a budget's creation, change, enablement, disablement, or deletion, the gateway writes an invalidation, each replica refreshes the tenant's budgets from Postgres, and the next charge of each session reads the new set. A charge in flight keeps the set it reserved against. A new tenant budget with a selector therefore applies to running sessions from their next charge.

**Labels are client-chosen [design].** A session's `labels` are the ones its root's client set at the root's creation, inherited unchanged by every descendant, and no parent writes them (0166 decision 44). A parent's `tags` are its own bookkeeping, and no selector reads them. A parent, or a model steered by injected content, therefore cannot add a `team` key that moves a child into another team's budget. A budget that selects on `labels` still binds only a root client that sets the label and is evaded by one that omits it. A budget that must bind selects on `environments`, `principals`, `groups`, `runtimes`, or `pools`, which the client cannot choose freely. O5 asks whether the core adds more.

### BG-4 Meters and units

| Meter | Unit | Quantity | Subject | Source of record |
| :-- | :-- | :-- | :-- | :-- |
| `credits` | `credits` | The estimated cost of a proxied model call under its rate-card version, in micro-credits. | The calling session. | `llm_usage` rows (0133 "LU-7"). |
| `compute` | `computeUnits` | Slot time weighted by the compute weight. | The session bound to the slot. | `session_pod_windows` rows (0121 "BR-2"). |
| `storage` | `storageBytes` and `storageByteHours` | Bytes held, and their integral over time. | The session that owns each artifact or pack, or the tenant for a tenant-owned platform chunk set. | `artifact_store` rows (0121 "BR-4", 0131 "IC-4"). |
| `poolIdle` | `computeUnits` | Idle pod time of the tenant's owned pools and pinned idle pods. | The tenant. | `pool_usage_accrual` rows (0121 "BR-3"). |
| `dedicatedNodes` | `money` alone | Node minutes of the tenant's dedicated nodes, priced per node class. | The tenant. | `dedicated_node_accrual` rows (0121 "BR-7"). |
| `escrow` | `storageByteHours` | Legal-hold escrow bytes over time. | The tenant. | `legal_hold_escrow_records` (0121 "BR-6"). |

The tenant-level meters `poolIdle`, `dedicatedNodes`, and `escrow` feed budgets only under O13.

**Compute units [design].** A session's compute charge for an interval during which it holds a slot is the interval's length in seconds times the compute weight of its pod, divided by the pod's `max_concurrent_sessions_per_pod`, the per-slot share of 0121. The weight is resolved once, at the bind, and recorded on the binding:

1. The pool's `computeWeight`, when a `platform-admin` set one on the pool row.
2. Otherwise the resource class's `computeWeight` in the chart's `resourceClasses` map, times `budgets.compute.isolationProfileWeights[<profile>]`.
3. Each value defaults to 1, so an unconfigured deployment counts one compute unit per second a session holds a whole pod.

A `tenant-admin` cannot set a weight, including on a tenant's owned pool, because a lower weight would loosen every platform budget in compute units. A session holds a pod in the states that SL-1 of 0146 marks as holding one: setup with a pod, `running`, `idle`, `suspended` and `blocked` while the pod is held, and `resuming`. `dormant` and every state without a pod charge nothing. O1 asks whether compute is counted this way or as resource-seconds.

**Storage [design].** Each stored artifact, checkpoint pack, output file, and upload has an owning session in the ledger, and a pack is charged once, to its owner, whichever session references it, as IC-4 charges the quota today. A pack of a tenant-owned platform chunk set has the tenant as its subject. A pack of a platform-owned set reaches no budget. `storageBytes` counts the bytes held now. `storageByteHours` counts their integral, in byte-seconds, over the period.

### BG-5 Price lists and money

**The record [design].** A price list has versions, each with an `effectiveFrom` time, appended with `POST /v1/admin/price-lists/{scope}/versions` and never edited:

```yaml
scope: platform                     # or a tenant (O3)
version: 4
effectiveFrom: 2026-12-01T00:00:00Z
currency: EUR                       # must equal budgets.currency
credits:
  perMillionCredits: 2.00
  byUpstream:                       # optional, when two upstreams' cards use different credit scales
    acme-bedrock: 2.10
compute:
  perComputeUnitHour: 0.12
storage:
  perGiBMonth: 0.025                # a GiB-month is 2^30 bytes held for 730 hours
  byArtifactType:
    checkpoint: 0.020
poolIdle:
  perComputeUnitHour: 0.08
dedicatedNodes:
  perNodeHour:
    c6i-4xlarge: 0.68
escrow:
  perGiBMonth: 0.004
```

The figures are illustrative, and no figure in this document is a price Lenny ships.

- **Currency.** `budgets.currency`, an ISO 4217 code, is one Helm value for the deployment. Money amounts are stored as integer micro-units of it. With no currency set, the gateway refuses a money budget and a price list with `400 VALIDATION_ERROR` (O20).
- **Validation.** The admin API refuses a version that leaves a meter unpriced while a money budget in its scope counts that meter, a version whose `effectiveFrom` lies in the past, and a version whose currency differs from `budgets.currency`. `lenny-ctl price-list validate` checks a version against the deployment.
- **Conversion.** Each charge is converted at the version in force at its time: a model call at its start, a compute tick at its start, and a storage accrual interval at its end. Each conversion multiplies in integers and rounds up once per charge, so no drift accumulates, and the version used is recorded with the charge.
- **Several units.** A money budget's counter is in money alone. A credit reservation and a compute tick each reserve their money equivalent in every money budget of the set, and settlement converts the actual charge at the same version. Stored data reaches a money budget through the accrual of byte-hours, which reserves nothing. A refusal names the budget and its unit, so a caller sees that a money budget refused a compute tick.
- **Scope.** A platform money budget converts at the platform price list. A tenant or principal money budget converts at the tenant's own price list when O3 allows one and the tenant has written one, and at the platform price list otherwise.
- **Estimates.** Every money figure Lenny reports carries `estimated: true` and the price-list version. Lenny records quantities on billing events, as 0121 states, and whether money appears on them is O15.

### BG-6 Enforcement

**Per meter [design].**

| Meter | When it reserves | When it settles | What a refusal stops |
| :-- | :-- | :-- | :-- |
| `credits` | Before the proxy forwards a call, the output limit and input estimate of 0133 "LU-6", in every budget of the set that counts credits or money. | When the response's usage arrives, under 0133. | The call, with `BUDGET_EXHAUSTED`. The session continues, as decision 5 of 0133 states. |
| `compute` | At each bind, and at the start of each compute tick, `budgets.compute.tickSeconds` (default 60) of the pod's weighted slot time. | At the end of each tick and at the release, for the time the session held the slot. | At a bind, the session's creation, its resume, or a child's creation. At a tick, the running session, under `onExhausted.running`. |
| `storage` (`storageBytes`) | Before an upload, an export, or a checkpoint chunk grant, the incoming bytes, as `storage_quota_reserve.lua` does for the tenant today. | At confirmation, to the confirmed size, and at deletion, by the deleted size. | The write, with `BUDGET_EXHAUSTED`. |
| `storage` (`storageByteHours`) | No reservation. Each budget keeps the bytes its subjects hold, and a leader sweep adds bytes held times elapsed time every `budgets.storage.accrualIntervalSeconds` (default 300). | At each accrual. | New writes in the subjects of an exhausted budget. |
| `poolIdle`, `dedicatedNodes`, `escrow` | No reservation. | When 0121's emitter commits each periodic event, hourly by default. | New session creations in the tenant, after the fact (O13). |

**Admission [design].** A session creation, a resume, and a child's creation are refused with `429 BUDGET_EXHAUSTED` when any `enforce` budget of the new session's set is exhausted in a unit the session would charge, or when the first compute tick does not fit. Concurrency stays under §11.1.

**Thresholds [design].** When a charge's settlement carries a budget's partition past a threshold for the first time in a period, the gateway writes `budget.threshold_crossed` with the budget, the partition, the threshold, the unit, the consumption, and the limit. A crossing is detected by the settlement script, so it is reported once per threshold per period. Where the notice goes beyond the audit log, such as a billing event, the budget's webhook targets, and the sessions it binds, is O14.

**Exhaustion [design].** A budget is exhausted in a period when its consumption plus its reservations reach its limit. `onExhausted` states the action for new work and the action for running sessions:

| Field | Values | Default |
| :-- | :-- | :-- |
| `onExhausted.new` | `refuse`: refuse each new reservation, which refuses new sessions, resumes, children, model calls, and writes in the budget's subjects. | `refuse` |
| `onExhausted.running` | `continue`: a running session keeps its pod and is refused at its next model call or write. `release`: at its next refused compute tick, the session's open turn is cut and it becomes `dormant` under SL-6 of 0146, which checkpoints its workspace and releases its pod; a session whose `checkpointPolicy` is `sealOnly` or `none` cannot become `dormant` and expires. `end`: the session expires with the reason `budget_exhausted`. | `release` for a budget that counts compute, `continue` otherwise (O9) |

A storage budget never deletes data (BGR-8). A checkpoint whose chunk grant a storage budget refuses aborts with `BUDGET_EXHAUSTED`, as a refusal by the tenant's storage quota aborts it today, and the session keeps its previous checkpoint. O9 asks whether checkpoints keep a margin past exhaustion.

**Lease extension [design].** §8.6 stays for session budgets and carves, with `additional` naming an amount per unit in place of `additionalTokenBudget`. When a session budget or a carve refuses a credit reservation or a compute tick, the gateway requests an extension under §8.6's approval modes, ceilings, and layering before it applies the refusal. A granted extension of a carve reserves the added amount from the parent's budgets as `session.set_limits` does ("BG-8"). A shared budget has no extension: a refusal by a shared budget is final until an administrator raises its limit, resets it, or its period ends (O10).

**Errors [design].** Every budget refusal is `429 BUDGET_EXHAUSTED` with `details.budget` (its scope and name, or `session`), `details.unit`, `details.meter`, `details.partition` when the caller may read the budget, and `details.resetsAt` when the budget has a schedule. A caller that may not read the budget receives `details.budget` as `shared` and no other detail of it. `STORAGE_QUOTA_EXCEEDED` and `details.limitType` of 0133 retire in favour of these details.

### BG-7 Resets and periods

**Schedules [design].**

```yaml
period:
  schedule: calendar          # none | calendar | cron | interval
  calendar:
    every: month              # day | week | month | quarter | year
    dayOfMonth: 1             # month, quarter, and year: 1 to 28, or last
    weekday: monday           # week
    at: "00:00"
  cron: "0 6 * * 1"           # cron: five fields, in timeZone
  interval: 7d                # interval: from startsAt
  timeZone: Europe/Madrid     # IANA name, default UTC
  rollover:
    mode: none                # none | carry
    maxAmount: 0              # carry: the most of the unused amount added to the next period
```

O8 asks whether rollover is offered at all.

- **Boundaries.** The gateway computes each boundary from the schedule and the time zone. A local time that a daylight-saving change skips moves to the next instant that exists, and a local time that occurs twice takes its first occurrence. A cron schedule whose boundaries fall closer than `budgets.minPeriodSeconds` (default 3600) is refused at write.
- **Period keys.** A period is named by its start instant, which every replica computes from the schedule, so a charge after a boundary lands in the new period without coordination.
- **On demand.** `POST /v1/admin/budgets/{name}/reset`, or `POST /v1/budgets/{name}/reset` for a principal budget, closes the current period at once and starts a new one, for every partition or for the one the request names. The request carries a `reason`. The schedule continues at its next boundary, so the period after an on-demand reset is shorter. Whoever may change the budget may reset it. A session budget has no reset.
- **Rollover.** Under `carry`, the next period's limit is the budget's limit plus the period's unused amount, at most `maxAmount`. A period's unused amount is its limit, its own carried amount included, less its consumption.
- **Reservations across a reset.** Each reservation outstanding at a boundary, including the unconsumed part of every carve, moves into the new period: the new period's counter starts at the sum of the outstanding reservations plus nothing consumed, and each later settlement settles in the new period. A charge that straddles the boundary is therefore counted in the period in which it settles (O7).
- **Records.** Closing a period writes a `budget_periods` row with the period's bounds, its consumption by meter, the money estimate of a money budget, the reservations carried, the rollover, and the trigger (`schedule` or `api`, with the actor), and `budget.reset` names the closed period.

### BG-8 Carves for delegated children

The owner settled this in decision 12 of 0166 on 2026-11-23: a child's carved budget is a reservation from the budget objects that apply to the parent, and the child's share is a budget object of the child's session funded by that reservation. This change owns the carve's mechanics, and 0166 keeps DPR-4 and the operations that move a carve (0166 decision 35) **[design]**:

- **Creating the carve.** `session.create` of 0166 carries `limits.budget`, an amount per unit, at most what the parent's session budget has left. When it names none and the parent has a session budget, the child receives `defaultDelegationFraction` (0.5 by default) of what the parent's session budget has left in each unit, settable per unit on the runtime, the environment, and the tenant. The gateway reserves each amount in every budget of the parent's budget set that counts it, in money where the budget is a money budget, at the price-list version in force, and creates the child's session budget with those limits. A budget that lacks room refuses the child with `BUDGET_EXHAUSTED`. An SO-5a rule of 0142 with `when.creditBudgetAbove` reads the carve's credits and holds the creation for approval before the gateway reserves it.
- **Charges inside the carve.** A charge of the child is applied to the child's budget set. A budget that holds the child's carve, or the carve of an ancestor through which the child is funded, counts the charge inside that carve: the carve's reserved amount falls by the charge and the budget's consumption rises by it, so its total does not change and the child is counted once. A budget of the child's set that holds no carve for it, such as one that selects the child's runtime alone, takes the charge directly.
- **Return.** When the child reaches a terminal state, each budget releases the carve's unconsumed amount after the child's in-flight reservations settle or expire, as 0133's quiescence rule states for the tree scope today.
- **Moving budget.** `session.set_limits` of 0166 raises a carve by reserving the added amount in the parent's budgets, and lowers it by releasing part of the unconsumed amount, never below what is reserved for the child's calls in flight. A raise that takes the carve's credits past an SO-5a rule's `when.creditBudgetAbove` waits for that rule's approval before the gateway reserves it.
- **Transfer.** Under decisions 32 and 33 of 0166, `session.transfer` keeps a child in its tree with the root's client as its client. Its carve moves from the parent's reservation to the root's: the root's session budget reserves the carve's unconsumed amount and the parent's session budget releases it, while every shared budget keeps its reservation unchanged, because the child stays inside the same tree.
- **Release.** Under decisions 32 and 33 of 0166, `session.release` makes a child a root session outside its tree. Its unused carve becomes its own session budget's remaining amount. The parent's session budget records the released amount as consumed by the transfer. Every shared budget releases its carve reservation, and the released session's later charges reach its shared budgets directly, because it still matches them through its inherited attributes and references.
- **Resets.** A carve held from a scheduled budget moves into the new period at a reset, as every reservation does ("BG-7").
- **Cost.** A carve holds capacity in every budget of the parent's set for as long as the child lives, so a long-lived child can hold credits that other sessions under a team budget need. A deadline on the child, such as `perChildMaxAge` under decision 13 of 0166, bounds it.

### BG-9 Counters, failure, and precision

**Redis [design].** Each budget partition and period has a counter with `consumed` and `reserved` fields under its own hash tag, `{bdg:<budgetId>:<partition>}`, and a reservation index ordered by expiry beside it. A reservation across a budget set touches several hash slots, which the rule of [§12.4](../../spec/12_storage-architecture.md#124-redis-ha-and-failure-modes) that "Every Redis store keeps each script, transaction, and multi-key command inside one hash slot" forbids in one script. The gateway therefore reserves budget by budget in a fixed order of budget identifiers, records each step in the reservation with its expiry, and releases the earlier steps when a later one refuses, as option (a) of 0133's open decision 6 proposes for its scopes (O11). A budget that every charge of a tenant or the platform reaches, such as a tenant's monthly money budget, is a hot key, and O11 asks whether such budgets hand each replica an allotment, as §12.4's in-memory quota mode does for tenant token budgets.

**Postgres [design].** The `budgets` table holds definitions, the `budget_periods` table holds closed periods, the `budget_checkpoints` table holds each open period's counters every `quotaSyncIntervalSeconds`, and the `price_list_versions` table holds the price lists. The session row carries its budget set and its references.

**Failure [design].**

- **A replica crash.** A reservation whose replica stopped expires, and the recovery sweep of 0133 "LU-6" ("Recovery") settles it from its meter's durable record: a model call at its reservation, or at its usage when the `llm_usage` row exists; a compute tick at the binding's `closed_at` in `session_pod_windows`, or at the tick's end when the binding is still open on a live pod; a storage reservation at the confirmed size in the ledger, or at zero when no row exists.
- **A Redis loss.** On recovery, each counter is rebuilt as the maximum of its last checkpoint and the consumption that the durable records hold for the open period, computed lazily per budget on its first charge, which refuses until the rebuild ends. During the outage, the gateway enforces session budgets in memory and lets shared budgets drift within the per-replica ceiling of §12.4, as §11.2 does for its windows today, and applies the MAX rule on recovery.
- **A reset during an outage.** A boundary that passes while Redis is unavailable is applied at recovery from the schedule, and the closed period's record is written then.

**Precision [design].**

| Meter | Step | Overshoot of a budget |
| :-- | :-- | :-- |
| `credits` | One micro-credit, per call. | The input estimate's error on the calls in flight in the budget (0133 LUR-7), plus fail-open drift. |
| `compute` | One milli-compute-unit, per tick. | One tick of weighted slot time per session holding a pod in the budget, plus the time until a refused tick's action takes hold. |
| `storageBytes` | One byte, per write. | The outstanding checkpoint grant window of §11.2. |
| `storageByteHours` | One byte-second, per accrual interval. | Byte-hours accrue while data is held, so a budget's consumption keeps rising after exhaustion until data is deleted. |
| `poolIdle`, `dedicatedNodes`, `escrow` | One emitter window, hourly by default. | The charges of one window. |
| `money` | One micro-unit of the currency, rounded up per charge. | The sum of the overshoots of the meters it counts. |

### BG-10 API, RBAC, audit, metrics, and usage

**Routes [design].**

| Route | Purpose |
| :-- | :-- |
| `POST`, `GET /v1/admin/budgets` | Create and list platform and tenant budgets. A `platform-admin` passes `?tenant=` or `scope: platform`. Every admin write accepts `?dryRun=true` under §15.1. |
| `GET`, `PUT`, `DELETE /v1/admin/budgets/{name}` | Read, replace under `If-Match`, and delete. Deletion releases every reservation the budget holds. |
| `POST /v1/admin/budgets/{name}/reset` | Reset on demand ("BG-7"). |
| `GET /v1/admin/budgets/{name}/usage` | The current period's consumption by meter, reservations, limit, and money estimate, per partition with paging. |
| `GET /v1/admin/budgets/{name}/periods` | Closed periods. |
| `/v1/budgets`, with the same sub-routes | Principal budgets of the caller. |
| `GET`, `PATCH /v1/sessions/{id}/budget` | The session budget ("BG-2"). |
| `GET /v1/sessions/{id}/budgets` | The session's budget set, each with its state (`ok`, `low`, or `exhausted`), and the amounts of each budget the caller may read. |
| `POST`, `GET /v1/admin/price-lists/{scope}/versions` | Price-list versions ("BG-5"). |
| `GET /v1/usage?budget=` | The usage report filtered to one budget's subjects. |

**RBAC [design].** The §10.2 permission matrix gains these rows:

| Operation | `platform-admin` | `tenant-admin` | `tenant-viewer` | `billing-viewer` | `user` |
| :-- | :-- | :-- | :-- | :-- | :-- |
| Manage budgets | Yes (all tenants, and platform) | Yes (own tenant) | Read-only (own tenant) | Read-only (own tenant) | Own principal budgets, and the state of each budget in its own sessions' sets |
| Manage price lists | Yes | Own tenant's price list under O3 | Read-only (own tenant and platform) | Read-only (own tenant and platform) | No |

A service account holds the `user` row's rights over its own principal budgets. "Manage quotas" keeps concurrency and rate limits.

**Audit events [design].** [§11.7](../../spec/11_policy-and-controls.md#117-audit-logging) gains `budget.created`, `budget.updated` with the fields changed, `budget.deleted`, `budget.reset` with the trigger, the actor, and the closed period's summary, `budget.threshold_crossed`, `budget.exhausted` once per budget, partition, and period, `session_budget.changed` for a `PATCH`, an extension, a carve, a transfer, or a release, and `price_list.version_added`.

**Metrics [design].** A budget name is not a metric label, because tenants create budgets without bound, and per-budget figures come from the usage route. The metrics are `lenny_budget_reservations_total` by `meter` and `outcome`, `lenny_budget_refusals_total` by `tenant_id`, `unit`, `scope`, and `action`, `lenny_budget_reservation_duration_seconds` by `meter`, `lenny_budget_threshold_crossings_total` by `tenant_id` and `unit`, `lenny_budget_resets_total` by `trigger`, `lenny_budget_rebuilds_total` by `outcome`, `lenny_budget_compute_tick_lag_seconds`, and `lenny_budget_storage_accrual_lag_seconds`. The alerts are `BudgetRebuildFailing`, `BudgetAccrualLagging` when the accrual lag passes twice its interval, and `BudgetPriceListGap` when a money budget counts a meter with no price in force.

**Usage reports [design].** The usage route of a budget answers from its counters for the open period and from `budget_periods` for closed ones, with each meter's consumption, so a team sees its model, compute, and storage spend apart. `GET /v1/usage?budget=` aggregates the usage records of the budget's subjects in a window, which reproduces a budget's consumption from records for a budget with a selector, and returns `400 VALIDATION_ERROR` for a principal budget, whose subjects are listed by session.

**What a runtime sees [design].** A runtime meets budgets as refusals: `BUDGET_EXHAUSTED` from the proxy, from `session.create`, and from a file export, and the cut turn of a session that becomes `dormant` or expires under `onExhausted.running`. O17 asks whether a runtime can read its session budget.

### BG-11 What this change supersedes

| Change | Parts superseded [design] | Parts kept |
| :-- | :-- | :-- |
| [0133](../0133-llm-upstreams-and-cost-budgets/change.md#lu-6-credit-budgets) | The scope table of "LU-6" ("Scopes"): the session, tree, user-window, tenant-window, and global-window scopes become session budgets, carves, and shared budgets. Step 4 of "Reservation", one script across the scopes, and open decision 6, which O11 restates for budget sets. Step 5 of "Reservation", the extension for a session or tree scope, which becomes the extension of a session budget or carve. "Delegation" and "The tree scope" in "LU-6", which revision 11 of 0133 already points at "BG-8" for the carve, apart from its name `maxCreditBudget` for a child's credit amount, which becomes `limits.budget`. `maxCreditBudget` on the lease, on `lease_slice`, and in the presets. The `quotas.llmCredits.perUser`, `perTenant`, and `global` settings. Decision D3's "tied to no currency" and OD-6, which a price list amends for budgets ("BG-5"). | "LU-5" rate cards, credits as the unit of model calls, the input estimate, settlement, the recovery sweep, the bounds of LUR-7, "LU-7", and `quotas.llmCredits.perCall` as a ceiling per call. |
| [`0166-delegation-primitives`](../0166-delegation-primitives/change.md) | None left to supersede: revision 8 cites this change for budget objects and the carve ("DP-3", and the 0170 row of "DP-7"). This change states in full what that row lists: the carve as a reservation from the budget objects that apply to the parent, `limits.budget` on `session.create` with `defaultDelegationFraction` per unit, the budget part of `session.set_limits`, `session.transfer`, and `session.release`, and lease extension of carves ("BG-6", "BG-8"). The `maxCreditBudget` among 0166's root grant inputs, in its examples, and in a client's `delegationLease` becomes `sessionBudgetCeilings` and `budget` per unit ("BG-2"). | DPR-4, the operations that move a carve, the resolution order of "DP-1", which "BG-2" follows, and every other resource caveat with its ceilings. |
| [0121](../0121-billing-resource-attribution/change.md) | None. | Every part. This change reads 0121's records as the durable source of compute and storage charges. |
| [0131](../0131-incremental-checkpoint/change.md#ic-4-catalog-reference-closure-and-quota) | "Quota" in IC-4 charges the tenant's storage quota, which becomes every `storageBytes` budget of the owning session's set. | Every other part. |

0133 revision 11 hands its tree scope to this change in "The tree scope" under LU-6, and 0166 revision 8 cites this change for the carve. The other scopes of "LU-6" stay in 0133 as written until O6 settles whether 0133.S3 builds them before this change replaces them. The edits to 0121, 0131, and the rest of 0133 that this table lists land when the owner settles the open decisions that name them.

## Worked examples

Each example states what the operator, the tenant administrator, and the client configure, and what happens. Every example is **[design]**, assumes the recommended options of the open decisions, and uses field names that this change and 0166 propose. The tenant is `acme`, `alice` is its `tenant-admin`, and `bob` is a user.

### Example 1: A team's monthly money budget across many sessions

The payments team of `acme` may spend 5,000 EUR a month on everything its agents use.

**Operator.** The operator sets the currency and the compute weights in Helm, and appends a price-list version:

```yaml
budgets:
  currency: EUR
  compute:
    isolationProfileWeights: { standard: 1.0, sandboxed: 1.2, microvm: 1.5 }
resourceClasses:
  small:  { computeWeight: 0.5, requests: { cpu: 500m, memory: 2Gi } }
  medium: { computeWeight: 1.0, requests: { cpu: "1", memory: 4Gi } }
  large:  { computeWeight: 2.0, requests: { cpu: "2", memory: 8Gi } }
```

```yaml
# POST /v1/admin/price-lists/platform/versions
version: 1
effectiveFrom: 2026-11-24T00:00:00Z
currency: EUR
credits: { perMillionCredits: 2.00 }
compute: { perComputeUnitHour: 0.12 }
storage: { perGiBMonth: 0.025 }
```

**Tenant administrator.** `alice` creates a tenant budget that selects the team's directory group:

```http
POST /v1/admin/budgets
{
  "name": "payments-monthly",
  "limit": { "amount": 5000.00, "unit": "money" },
  "meters": ["credits", "compute", "storage"],
  "appliesTo": { "groups": ["payments-eng"] },
  "attach": "selector",
  "period": { "schedule": "calendar", "calendar": { "every": "month", "dayOfMonth": 1, "at": "00:00" },
              "timeZone": "Europe/Madrid", "rollover": { "mode": "none" } },
  "thresholds": [0.8, 1.0],
  "onExhausted": { "new": "refuse", "running": "release" },
  "notify": [{ "url": "https://alerts.acme.com/lenny-budgets" }]
}
```

**Client.** `bob`, a member of `payments-eng`, creates sessions as usual. He names no budget.

**What happens.**

1. Each of `bob`'s sessions has `tenant/payments-monthly` in its budget set, because his groups at creation include `payments-eng`.
2. A model call reserves its credit worst case, converted at 2.00 EUR per million credits, in the budget. A session on a `medium` pod in a `sandboxed` pool reserves 60 seconds times 1.0 times 1.2, which is 72 compute units, or 0.0024 EUR, at each tick. A checkpoint pack adds its bytes to the budget's bytes held, and the accrual adds byte-seconds every 5 minutes, converted at 0.025 EUR per GiB-month.
3. At 4,000 EUR, `budget.threshold_crossed` records the 0.8 threshold and the notice reaches `alerts.acme.com`.
4. At 5,000 EUR, new sessions of the group are refused with `429 BUDGET_EXHAUSTED` and `details.resetsAt` of `2026-12-01T00:00:00+01:00`. Each running session's next compute tick is refused, its turn is cut, and it becomes `dormant` with its workspace checkpointed. Stored data stays.
5. At 00:00 on 1 December in Madrid, 23:00 UTC on 30 November, the period closes. `budget_periods` records the November consumption by meter, and `dormant` sessions resume when their users send a message.

A variant gives each user of the group 200 EUR a month inside the team's 5,000 EUR: `alice` adds a second budget with the same selector, `"limit": {"amount": 200.00, "unit": "money"}`, and `"per": "user"`. Each charge must fit both.

### Example 2: A tenant storage budget in GB and in money

`acme` may hold at most 2 TiB, and may spend at most 300 EUR a month on storage.

**Operator.** The operator instantiates a storage cap for every new tenant:

```yaml
budgets:
  tenantDefaults:
    - name: storage-cap
      limit: { amount: 1099511627776, unit: storageBytes }   # 1 TiB
      attach: selector
      appliesTo: {}
```

**Tenant administrator.** A `platform-admin` raises `acme`'s cap to 2 TiB with `PUT /v1/admin/budgets/storage-cap?tenant=acme`, and `alice` adds a money budget for storage:

```http
POST /v1/admin/budgets
{
  "name": "storage-spend",
  "limit": { "amount": 300.00, "unit": "money" },
  "meters": ["storage"],
  "attach": "selector",
  "appliesTo": {},
  "period": { "schedule": "calendar", "calendar": { "every": "month", "dayOfMonth": 1 }, "timeZone": "UTC" }
}
```

**What happens.**

1. A checkpoint of a session in `acme` asks for a chunk grant of 64 MiB. The gateway reserves 64 MiB in `storage-cap`, which holds 1.9 TiB and has room, and in `storage-spend`, which reserves no bytes and checks that it is not exhausted.
2. `acme` holds 1.5 TiB for the whole month. The accrual adds 1.5 TiB times 730 hours to `storage-spend`, which converts at 0.025 EUR per GiB-month to 38.40 EUR, well within 300 EUR.
3. An upload that would carry `storage-cap` past 2 TiB is refused with `429 BUDGET_EXHAUSTED`, `details.unit: storageBytes`. The data already held stays.
4. If `acme` held 12 TiB under a larger cap, `storage-spend` would pass 300 EUR during the month and refuse new writes, while the stored bytes keep accruing into it until data is deleted or the month ends.

### Example 3: A CI engine running many root sessions under one weekly budget

A CI system creates one root session per pipeline job, and the operator wants all of them under 20 million credits and 2,000 compute-unit-hours a week, reset on Monday at 06:00 UTC.

**Tenant administrator.** `alice` creates a service account `ci-runner` under 0143 with the `user` role in the `ci` environment.

**Client.** The CI system, authenticated as `ci-runner`, creates principal budgets, one per unit, because a budget has one unit:

```http
POST /v1/budgets
{ "name": "ci-weekly-credits",
  "limit": { "amount": 20000000, "unit": "credits" },
  "period": { "schedule": "cron", "cron": "0 6 * * 1", "timeZone": "UTC" },
  "onExhausted": { "new": "refuse", "running": "continue" } }
```

```http
POST /v1/budgets
{ "name": "ci-weekly-compute",
  "limit": { "amount": 7200000, "unit": "computeUnits" },
  "period": { "schedule": "cron", "cron": "0 6 * * 1", "timeZone": "UTC" },
  "onExhausted": { "new": "refuse", "running": "end" } }
```

Each job names the budgets and caps itself:

```http
POST /v1/environments/ci/sessions
{
  "runtime": "claude-code",
  "labels": { "pipeline": "release-482" },
  "budgetRefs": ["principal/ci-weekly-credits", "principal/ci-weekly-compute"],
  "budget": { "limits": { "credits": 400000, "computeUnits": 7200 } }
}
```

**What happens.**

1. Each job's budget set holds its session budget, the two principal budgets, and every tenant budget whose selector matches it, such as one on the `ci` environment.
2. On Sunday the credit budget reaches 20 million. New jobs are refused with `429 BUDGET_EXHAUSTED`, and each running job's next model call is refused, so the CI system fails the job.
3. A release cannot wait for Monday. A principal budget is reset by its creator, so the CI system resets it with `POST /v1/budgets/ci-weekly-credits/reset` and `"reason": "release 4.2 crunch"`. `budget.reset` records the actor and the closed period, and the next Monday boundary still applies.
4. A model call reserved at 05:59:58 on Monday settles at 06:00:03. Its reservation moved into the new period at the boundary, so it settles there.
5. A job that delegates under 0166 carves its children from its own session budget and from both principal budgets, because its children inherit its references.

### Example 4: A session budget with a delegated child carving from it

`bob` runs an orchestrating session that delegates a review to a child. `acme` also has a tenant budget `acme-monthly` of 50 million credits a month, and `alice` has a budget `reviewer-runtime` of 2 million credits a month that selects the `code-reviewer` runtime.

**Client.**

```http
POST /v1/sessions
{
  "runtime": "claude-code",
  "labels": { "project": "checkout" },
  "budget": { "limits": { "credits": 400000 } },
  "delegationLease": { "maxDepth": 1, "maxChildrenTotal": 4 }
}
```

**What happens.**

1. The root's budget set is its session budget (400,000 credits) and `tenant/acme-monthly`. `reviewer-runtime` does not select `claude-code`.
2. The root's model calls reserve and settle in both budgets. After a while the root has used 100,000 credits.
3. The root calls `session.create` with `target: "code-reviewer"` and `limits.budget: {"credits": 50000}`. The gateway reserves 50,000 credits in the root's session budget and in `acme-monthly`, which both have room, and creates the child with a session budget of 50,000 credits funded by that carve. The child's budget set is its session budget, `acme-monthly`, which it matches through its inherited tenant, and `reviewer-runtime`, which selects its runtime.
4. A child model call of 2,000 credits reserves in the child's session budget, inside the carve in the root's session budget and in `acme-monthly`, and directly in `reviewer-runtime`. When it settles, the root's session budget and `acme-monthly` show the 2,000 credits as consumption and 48,000 as reserved for the child, so their totals do not change, and `reviewer-runtime` rises by 2,000.
5. The review needs more. The root calls `session.set_limits` with `+20000` credits, which reserves 20,000 more in the root's session budget and in `acme-monthly`.
6. The child ends having used 30,000 credits. Both funding budgets release the unconsumed 40,000. The root's session budget now shows 130,000 consumed of 400,000, and `acme-monthly` shows the same 30,000 for the child once.
7. Had the child exhausted its 50,000 credits before step 5, the proxy would have requested a §8.6 extension for the child's carve, which, in `elicitation` mode, asks the root's client, `bob`, under 0166's "DP-3".

## Limits, and alternatives rejected

**Limits [design]:**

- Enforcement is best effort per meter, with the bounds of BGR-9. A byte-hour budget cannot stop the time that data is held.
- A budget that selects on `labels` binds only clients that set the label (O5).
- Money figures are estimates under the operator's price list, which leaves out volume tiers, commitments, credits, and taxes, as 0133's credit estimate does.
- A carve holds capacity in every budget of the parent's set while the child lives ("BG-8").
- Budgets cover model calls through the proxy alone, as OD-15 of 0133 states, and costs that reach no tenant under 0121.
- A refused compute tick under `release` costs a checkpoint and a later restore, and a session under `sealOnly` or `none` expires.
- The tenant-level meters report hourly, so their budgets refuse after the fact (O13).

**Alternatives considered:**

- **Keep fixed scopes and add more.** A team scope, a project scope, a weekly window, and a storage money limit would each need a setting, a counter, and an enforcement path. The selector and the partition express each of them with one mechanism.
- **Price everything in credits.** Compute and storage would carry rate cards in credits, and one exchange rate would convert credits to money. The owner asked for credits for model usage and money for infrastructure, and a credit scale set per upstream cannot also be the scale of pod time. O2 presents it as an option.
- **Enforce money only when the billing system has priced it.** The billing system prices after the fact, so no work could be refused before it costs money, which is what a budget is for.
- **Budgets as Kubernetes custom resources.** Tenant administrators and clients reach Lenny through its API and hold no cluster access.
- **One counter per budget across all its subjects, with no partitions.** A per-user limit would need one budget per user, written and kept in step with the directory by an administrator.

## Specification and non-specification changes

Each page is named by its section heading and its page code, and an edit lands on the numbered section or on the page that holds the content when the step is built.

**Specification [design]:**

- [§11.2 Budgets and Quotas](../../spec/11_policy-and-controls.md#112-budgets-and-quotas) and the page BUDGET: budget objects, session budgets, matching, meters, enforcement, resets, counters, failure, and precision. The budget rows of the table, **Budget inheritance:**, **Hierarchical Quota Model:** for budgets, **Storage quota enforcement mechanism.**, **Quota Update Timing:**, **Crash Recovery for Quota Counters:**, and **Maximum Overshoot Formula:** are rewritten, and the quota rows stay on the page QUOTA.
- The page RATE gains the price list beside 0133's rate cards.
- [§11.2.1 Billing Event Stream](../../spec/11_policy-and-controls.md#1121-billing-event-stream) and the page BILLEV: the estimate label of money figures, and what O15 settles.
- [§11.7 Audit Logging](../../spec/11_policy-and-controls.md#117-audit-logging) and the catalog CATEVENT: the events of "BG-10".
- [§8.3 Delegation Policy and Lease](../../spec/08_recursive-delegation.md#83-delegation-policy-and-lease), **Budget Reservation Model:**, and the page LEASE: the carve over budget objects, with 0166.
- [§8.6 Lease Extension](../../spec/08_recursive-delegation.md#86-lease-extension) and the page LEASEXT: extension of session budgets and carves per unit.
- [§10.2 Authentication](../../spec/10_gateway-internals.md#102-authentication), **Authorization and RBAC**, and the page AUTHZ: the rows that "BG-10" adds.
- [§10.6 Environment Resource and RBAC Model](../../spec/10_gateway-internals.md#106-environment-resource-and-rbac-model) and the page ENV: `sessionBudgetDefaults` and `sessionBudgetCeilings` on an environment. The page TSET gains them on a tenant.
- [§12.4 Redis HA and Failure Modes](../../spec/12_storage-architecture.md#124-redis-ha-and-failure-modes): the budget keys in the key table and the reservation across slots.
- [§14 Workspace Plan Schema](../../spec/14_workspace-plan-schema.md#14-workspace-plan-schema), **`CreateSessionRequest` (outer, sibling fields).**: `budget` and `budgetRefs`.
- [§15.1 REST API](../../spec/15_external-api-surface.md#151-rest-api) and the catalogs CATOPS and CATERR: the routes, `BUDGET_EXHAUSTED` with its details, `BUDGET_NOT_ATTACHABLE`, and the retirement of `STORAGE_QUOTA_EXCEEDED`.
- [§16.1 Metrics](../../spec/16_observability.md#161-metrics), [§16.5 Alerting Rules and SLOs](../../spec/16_observability.md#165-alerting-rules-and-slos), and the catalogs CATMETRIC and CATALERT: the metrics and alerts of "BG-10", with `StorageQuotaHigh` replaced by a threshold notice.
- The catalogs CATSET and CATLIMIT: every setting and limit of "Documentation".

**Non-specification [design]:**

- A package for budget records, selectors, schedules, and price lists, with its Postgres store and its Redis scripts for reservation, settlement, and the reservation index.
- `pkg/gateway/meteringproxy` reserves credits over the budget set in place of the scopes of 0133 "LU-6".
- `pkg/quota/storagequota` becomes the storage meter over budget sets, and `pkg/quota/quotastore` loses its token windows.
- `pkg/gateway/platformtools/treebudget` and `delegationbudget` become the carve, with 0166.
- The session server runs the compute tick at bind and release, and the gateway's leader sweeps gain the storage accrual, the period close, and the reservation recovery.
- Migrations for `budgets`, `budget_periods`, `budget_checkpoints`, and `price_list_versions`, and the removal of the tenant's quota fields that budgets replace.
- The chart gains `budgets.*` and `computeWeight` in `resourceClasses`, and `lenny-ctl` gains `budget list`, `budget get`, `budget usage`, `budget reset`, and `price-list validate`.
- The alerting rules lose `StorageQuotaHigh` and its checkpoint variants for budget notices.

## Documentation

Each page below is **[design]** and lands with the step that changes what it describes.

| Page | Reader | What changes |
| :-- | :-- | :-- |
| `docs/operator-guide/budgets.md` (new) | Operator | Budgets, meters, and units; `budgets.currency` (no default; money budgets are refused without it); price lists and their versions, validation, and estimate label; compute weights with `resourceClasses.<name>.computeWeight` (default 1), `budgets.compute.isolationProfileWeights` (default 1 per profile), and the pool's `computeWeight` (unset); `budgets.compute.tickSeconds` (default 60, from 10 to 600); `budgets.storage.accrualIntervalSeconds` (default 300, from 60 to 3600); `budgets.tenantDefaults`; `budgets.maxPerTenant` (default 500), `budgets.maxPerPrincipal` (default 20), `budgets.maxRefsPerSession` (default 8), `budgets.maxThresholds` (default 8), and `budgets.minPeriodSeconds` (default 3600); `defaultDelegationFraction` per unit (default 0.5); the platform's `sessionBudgetDefaults` and `sessionBudgetCeilings`; the reservation across slots and the hot-budget question; failure behaviour, rebuild, and precision per meter; the metrics and alerts. |
| `docs/operator-guide/multi-tenancy.md` | Operator, tenant administrator | Tenant budgets and their RBAC, selectors, partitions, references and `usableBy`, schedules with time zones and rollover, resets on demand, thresholds and notices, exhaustion actions and their effect on running sessions, observe mode, and the tenant's and environment's session budget defaults and ceilings. The storage quota section becomes a storage budget. |
| `docs/operator-guide/configuration.md` and `docs/reference/configuration.md` | Operator | Every `budgets.*` value with its default and range, and the removal of `storageQuotaBytes` and the token quota settings. |
| `docs/client-guide/budgets.md` (new) | Client | The session budget with its defaults, ceilings, `PATCH`, and extension; principal budgets and `budgetRefs`; reading a session's budget set; resets of a principal budget; and how a refusal appears. |
| `docs/client-guide/error-handling.md` and `docs/reference/error-catalog.md` | Client, operator | `BUDGET_EXHAUSTED` with its details, `BUDGET_NOT_ATTACHABLE`, and the retirement of `STORAGE_QUOTA_EXCEEDED`. |
| `docs/client-guide/delegation-and-tasks.md` | Client | Carves, and the extension of a child's carve that reaches the root's client. |
| `docs/runtime-author-guide/delegation.md` | Runtime author | `limits.budget` on `session.create`, `defaultDelegationFraction`, `session.set_limits` for budget, what `session.transfer` and `session.release` do to a carve, and the refusals of a carve. |
| `docs/runtime-author-guide/lifecycle.md` | Runtime author | A refused compute tick under `release` cuts the turn and makes the session `dormant`, and under `end` expires it; a refused model call and a refused export return `BUDGET_EXHAUSTED`. |
| `docs/runtime-author-guide/platform-tools.md` | Runtime author | The budget read of O17, when settled. |
| `docs/api/admin.md` and `docs/api/rest.md` | Operator, client | The routes of "BG-10". |
| `docs/reference/metrics.md` | Operator | The metrics and alerts of "BG-10". |
| `docs/runbooks/storage-quota-high.md` | Operator | Becomes a runbook for a storage budget's threshold notice. |
| `docs/runbooks/delegation-budget-exhaustion.md` | Operator | Carves over budget objects. |
| `docs/runbooks/budget-rebuild.md` (new) | Operator | The rebuild after a Redis loss and `BudgetRebuildFailing`. |
| `docs/reference/glossary.md` | Every reader | The terms of "Terms". |

## Tests

The tiers are those of `.claude/rules/test-coverage.md`.

| Tier | Tests | Requirements |
| :-- | :-- | :-- |
| `unit` | Selector matching for each field and for tenant-level subjects; the budget set's invalidation; schedule boundaries for each schedule kind across daylight-saving changes in several time zones, cron spacing, on-demand resets, and rollover; money conversion with rounding per charge and version selection by time; compute weight resolution and the per-slot share; partition keys. | BGR-1, BGR-4, BGR-5, BGR-6 |
| `component` | The reservation, settlement, and reservation-index scripts on a Redis Cluster across budgets in different slots, with the compensating release on a refusal; a period boundary with outstanding reservations and carves; the recovery sweep for each meter; the rebuild from checkpoints and durable records. | BGR-2, BGR-6, BGR-7, BGR-10 |
| `contract` | The routes' schemas, `If-Match`, `dryRun`, the error details, and the redaction of `details.budget` for a caller that may not read it. | BGR-11, BGR-12 |
| `integration` | Example 1 to Example 4 on the compose stack: a team money budget across sessions with a threshold notice and exhaustion; a storage cap and a storage money budget; a CI engine under principal budgets with a reset on demand; a carve with a transfer and a return. | BGR-1 to BGR-8, BGR-12 |
| `e2e_kind` | A compute tick refused under `release` makes a session `dormant` with its workspace checkpointed, and its resume is refused until the budget has room; a session under `sealOnly` expires. | BGR-8, BGR-9 |
| `load_local` | Concurrent reservations from many sessions against one hot budget, with the measured overshoot against BGR-9's bounds; concurrent carves against one parent. | BGR-2, BGR-9 |
| `chaos` | A replica killed with reservations in each meter; Redis lost and recovered across a period boundary. | BGR-6, BGR-10 |
| `security` | A principal budget cannot reach another principal's session; a tenant budget cannot match another tenant's charge; a `tenant-admin` cannot set a compute weight or write the platform price list; `budgetRefs` naming another principal's budget is refused as missing; a client cannot loosen a shared budget through its own session budget. | BGR-2, BGR-3 |
| `docs` | The pages of "Documentation" name only settings, metrics, alerts, and errors the catalogs hold, with matching defaults. | BGR-11 |
