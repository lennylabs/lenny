# Budgets

Status: `Draft`, revision 19, 2026-11-25. Programs: [v1](../programs/v1.md).

Each factual claim about behaviour carries one of these tags where the source matters:

| Tag | Meaning |
| :-- | :-- |
| **[source]** | Read in the Lenny tree, the specification, or another change on 2026-11-23 or 2026-11-24. |
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

"Budget objects like you described" refers to an option of what was open decision O4 of [`0166-delegation-primitives`](../0166-delegation-primitives/change.md): a budget resource that a client or a tenant administrator creates and names, from which sub-budgets are carved for child sessions and for unrelated sessions, serving a team's or a project's spending limit across many session trees and a workflow engine that runs many root sessions under one limit. The owner then settled it as decision 12 of [`0166-delegation-primitives`](../0166-delegation-primitives/change.md) on 2026-11-23: a child session's carved budget is a reservation from the budget objects that apply to the parent, with the child's share as a budget object of the child's session funded by that reservation, and step 0166.S2 depends on 0170.S1, the step of this change that adds budget objects. Under 0166's decision 35 the mechanics belong to this change, which states them in full ("BG-8"), and 0166's ["DP-3"](../0166-delegation-primitives/change.md#dp-3-budget-caps) cites it. Decision 70 replaced the carve with a count of the whole tree at the root's session budget and caps on children that set nothing aside.

Today's budgets do not meet that request **[source]**:

- [§11.2](../../spec/11_policy-and-controls.md#112-budgets-and-quotas) states budgets as fixed scopes: LLM token limits per request, session, user window, tenant window, global window, and task tree, a per-tenant storage quota in bytes, and a wall-clock limit per session. Each scope has its own counter, its own setting, and its own enforcement path. No scope covers a team, a project, a label, a runtime, or a set of unrelated root sessions.
- [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/change.md) removes the LLM proxy and its credit scopes, and sends model spend to budgets as charges that an operator's LLM gateway reports (its decision 22).
- Pod time and stored bytes have no budget. Under [`0121-billing-resource-attribution`](../0121-billing-resource-attribution/change.md), Lenny records pod time and byte-hours for the operator's billing system, and no budget reads them. The per-tenant storage quota limits bytes held, and nothing limits byte-hours or the money stored data costs.
- Nothing in the specification or the tree states a price or a currency, so no budget can be stated in money.
- Quota windows reset on fixed UTC boundaries (hourly, daily, monthly) or roll. No budget resets on demand, at the start of a week, in a time zone, or on an operator's schedule.

## Scope

**Proposed [design]:**

- Budget objects: named records that hold one or more limits, each counting one or more meters of one unit, under one set of sessions, one reset schedule, thresholds, and actions at exhaustion, created by a platform administrator, a tenant administrator, or a client for its own sessions ("BG-1").
- The session budget as a budget object that counts its delegation tree at the root, and the caps on children of 0166's "DP-3" ("BG-2", "BG-8").
- Matching: how a session and each charge find every active budget that applies ("BG-3").
- Meters and their units: external charges, the cost of pod time, and the cost of stored data in the deployment's currency, pod time in compute units, and stored bytes as a capacity ("BG-4").
- External charges: the charges that registered reporters, such as an operator's LLM gateway, send for sessions, with their proofs, batches, and durable record, and the charges that a session's owner posts for its own sessions ("BG-12").
- Price lists: the operator's translation of each meter's charges into an amount of money, versioned, and how a currency limit counts the charges of several currency meters ("BG-5").
- Enforcement: budgets as best effort, with session budgets and caps reserved ahead and shared budgets counted afterwards, where each meter charges, the soft thresholds, the actions at exhaustion, and lease extension for session budgets ("BG-6").
- Resets: on demand and on a schedule, with time zones, and the period in which a charge counts ("BG-7").
- Counter storage, failure behaviour, and the precision of each meter ("BG-9").
- API routes, RBAC, audit events, metrics, and usage reports by budget object ("BG-10").
- A statement of what this change supersedes in 0133 and in 0166 ("BG-11").

**Out of scope:**

- Charging, invoicing, payments, and taxes. Lenny charges no one, and a price list is an operator setting that Lenny uses for enforcement and reports as an estimate.
- Equality between a budget's figures and a bill. Budgets and billing read the same records and are independent: discounts, commitments, and special terms can make a bill differ from a budget's money estimate, and the operator keeps the two aligned (decision 3).
- Chargeback and markups from a tenant to its own teams. A tenant runs them outside Lenny from usage reports (decision 54).
- The cost the operator pays its providers. The operator's billing system prices the quantities in the billing events, as 0121 states, and decides whether to show Lenny's estimate.
- Model calls and other paid services that no registered reporter charges, which 0133 leaves to egress (0133 "LU-7").
- Concurrency and rate limits, which stay quotas under [§11.1](../../spec/11_policy-and-controls.md#111-admission-and-fairness).
- Costs that reach no tenant: unpinned idle pods on shared pools, node capacity that no pod requests, and data held in Postgres, as 0121 states.
- Costs that reach a tenant and no session: the idle pods of a tenant's owned or pinned pools, its dedicated nodes, and legal-hold escrow. They stay billing events of 0121 (decision 29).
- Meters that a deployer defines. Lenny meters pod time and stored data, and counts the charges that registered reporters and sessions' owners send on one `externalCost` meter, which attaches no meaning to what a charge measures (decisions 14 and 57).
- Egress bytes. `egress.tunnel` records of `0127-egress-and-git` carry bytes per tunnel and are lossy ("Current state"), so this change meters no egress (decision 33).

## Terms

| Term | Meaning |
| :-- | :-- |
| Budget, or budget object | A named record that holds one or more limits, which sessions it applies to, a period with its reset schedule, thresholds, and the actions at exhaustion ("BG-1"). |
| Limit | One named entry of a budget: the meters it counts, all of one unit, and an `amount` in that unit, with optional thresholds and exhaustion actions of its own. A budget's limits share its selector, attachment, partition, and period ("BG-1"). |
| Session budget | The budget object of one session, created with the session and ending with its tree. It holds limits as every budget does and has no period. The session budget of a tree's root counts the charges of every session of the tree, and a delegated child holds caps in place of one ("BG-2", "BG-8"). |
| Shared budget | Every budget object other than a session budget: a platform, tenant, or principal budget. |
| Platform budget, tenant budget, principal budget | A shared budget written by a `platform-admin` and able to span tenants, by a `tenant-admin` for its own tenant, or by a user or a service account for its own sessions. |
| Meter | What a charge measures, with the unit in which every limit that counts it states its `amount`: `externalCost`, the charges that registered reporters and the session's owner send for a session ("BG-12"); `computeCost` and `computeUnits`, the time a session holds a pod, priced or in compute units; `storageCost`, the data a session owns over time, priced; and `storageBytes`, the bytes it holds ("BG-4"). The currency meters are `externalCost`, `computeCost`, and `storageCost`, the compute meters are `computeCost` and `computeUnits`, and the storage meters are `storageCost` and `storageBytes`. |
| Unit | What a meter counts in: the deployment's currency for the currency meters, compute-unit-hours for `computeUnits`, and bytes for `storageBytes`. A limit names no unit, because its meters fix it (decision 67). |
| Charge | A quantity from one meter, attributed to one subject. A charge of `computeCost` or `storageCost` is priced at the price list ("BG-5"). |
| Subject | What a charge is attributed to: a session, with its tenant, environment, principal, groups, runtime, pool, labels, and root session. |
| Compute unit | The unit of pod time. A pod's rate in compute units per second follows its resources: its vCPUs, its GiB of memory, and its GPUs, each at a rate a `platform-admin` sets, times the multiplier of its isolation profile. A session's charge is its pod's rate divided by the pod's slot count, for each second it holds a slot ("BG-4"). With the default rates, one compute unit is one vCPU held for one second. |
| Compute rates | The figures that turn a pod's resources into compute units per second: a rate per vCPU, a rate per GiB of memory, a rate per GPU type, and a multiplier per isolation profile, with an optional fixed figure for one resource class or pool. A `platform-admin` sets them ("BG-4"). |
| Price list | A record that translates compute units and stored bytes over time into an amount of money in the deployment's currency, with versions by `effectiveFrom` ("BG-5"). The platform price list holds the standard prices, and a tenant price list that a `platform-admin` writes overrides them for one tenant. A charge on `externalCost` arrives in the currency and needs no price. |
| Currency limit | A limit on one or more currency meters. It counts their charges together in the deployment's currency, pod time and stored data priced at the price-list version in force when they occurred. |
| Capacity limit | A limit on `storageBytes`. It counts the bytes held at each moment, ignores the period, and refuses a write that would carry it past its amount ("BG-4"). |
| Selector | The attributes of a shared budget that decide which subjects it applies to ("BG-3"). |
| Reference | The name of a shared budget that a session creation lists in `budgetRefs`, which attaches the budget to the session and its descendants ("BG-3"). |
| Budget set | The budgets that apply to one subject: its session budget, the shared budgets whose selectors match it, and the budgets it references, its ancestors' references included. |
| Partition | One set of counters of a budget whose `per` field names an attribute, such as one set per user, with a counter for each limit. A budget opens at most its `maxPartitions` partitions in a period ("BG-1", **Partitions**). |
| Period | The interval over which a budget's counters accumulate, from one reset to the next. A budget with no schedule has one period for its life, and a capacity limit ignores the period. |
| Reset | The close of a period and the start of the next, at a schedule boundary or on demand ("BG-7"). |
| Adjustment | An amount that whoever may change a budget adds to or takes from one or more limits of it, named by key and stated in the unit of each limit's meters, for the periods that start within a window, for one partition or every partition ("BG-7"). |
| Reservation | An amount held against the limits of the session budget that counts a session's tree, and against the caps on the session's path to the root, before the work that incurs it runs, settled to the actual charge afterwards ("BG-6"). Shared budgets hold no reservation. |
| Budget suspension | The pause of a session and its subtree by a limit under `onExhausted.running: suspend`, with `suspension_cause: budget`, a pod hold, and an automatic resume when the limit gains room within the hold, or by an extension or a raise for the session ("BG-6"). |
| External limit | A limit that a gateway or service enforces outside Lenny. A registered reporter's limit notice or the runtime's `external_limit` frame reports that a session hit one, and the session is suspended as for a budget, with `suspension_cause: external_limit` and no automatic resume ("BG-12" **Limit notices.**, decision 68). |
| Flush | A gateway replica's addition of the charges it settled, summed in memory, to the Redis counters of a shared budget, every `budgets.shared.flushSeconds` ("BG-6"). |
| Cap | A bound that a parent sets on a delegated child for one limit key of its tree's session budget. It bounds what the child's whole subtree consumes, or the bytes it holds, and sets nothing aside ("BG-8"). |
| Threshold | A fraction of a limit's amount at which the gateway records a notice ("BG-6"). |
| Enforce mode and observe mode | A budget in `enforce` mode refuses what does not fit. A shared budget in `observe` mode counts and records notices and refuses nothing. A session budget is always in `enforce` mode (decision 50). |
| BG-n and BGR-n | The parts of this design under "Detailed design" carry BG-n, and its requirements carry BGR-n. |

### Terms from other designs and the platform

| Term | Meaning |
| :-- | :-- |
| Charge, reporter, and `external_charges` | A priced amount that a registered reporter, or the session's owner, sends for one session, the registered client that sends a reporter's charges, and the charge's durable record ("BG-12"). |
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
| `platform-admin`, `tenant-admin`, `tenant-viewer`, `billing-viewer`, and `user` | The built-in roles of the [§10.2](../../spec/10_gateway-internals.md#102-authentication) permission matrix. |

### Labels from other designs

| Labels | Source | What they name |
| :-- | :-- | :-- |
| LU-1 to LU-8 and decision 22 | [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/decisions.md#decisions) | The operator's LLM gateway as an egress destination, the attribution binding, charges, and enforcement by binding suspension, which replace the metering proxy (0133 decision 22). |
| BR-1 to BR-7 | [`0121-billing-resource-attribution`](../0121-billing-resource-attribution/change.md#high-level-design) | Resource classes, session pod time, idle pods, stored bytes, reconciliation, escrow, and dedicated nodes as billing events. |
| SL-1, SL-4, SL-6, SL-10, and L35 to L43 except L38 | [`0146-session-lifecycle`](../0146-session-lifecycle/change.md#sl-4-tree-suspension) | The states with `suspension_cause`, tree suspension and the budget suspension that reuses it, idle pods and dormancy, and admission, caps, and timers. |
| RT-2 | [`0141-runtime-routing`](../0141-runtime-routing/change.md#rt-2-the-interceptor-contract) | The interceptor contract, with `budget_refs` and `session_budget` in the `PreRoute` payload and the post-chain point. |
| TS-10 and decision 46 | [`0149-tenant-scoping-identity`](../0149-tenant-scoping-identity/change.md#label-rules-ts-10) | Label rules, the check of a crossing child's labels, and the `Warning` on a rule write that frees a key a budget selects on. |
| Decision 14 | [`0144-session-derive`](../0144-session-derive/decisions.md#decisions) | A derive's request carries its own `labels`, `budget`, and `budgetRefs`. |
| MD-22 | [`0135-message-delivery`](../0135-message-delivery/change.md#md-22-parents-and-children) | Child events, with `suspended` and `resumed` carrying `reason`. |
| IC-4 | [`0131-incremental-checkpoint`](../0131-incremental-checkpoint/change.md#ic-4-catalog-reference-closure-and-quota) | The checkpoint catalog and its storage quota. |
| DP-1, DP-2, DP-3, DP-6, DP-7, and decisions 12, 13, 33, 35, 40, 43, 44, 52, 53, 54, 57, 58, and 59 | [`0166-delegation-primitives`](../0166-delegation-primitives/change.md) | The session grant with its root grant inputs and the resolution of every caveat ("Resolution" in DP-1, decision 40); transfer and release (DP-2, decision 33); the budget caps, which cite this change, and `session.set_limits` (DP-3); the disposition of each delegation setting (DP-6); what each change takes on (DP-7); the carve as a reservation from budget objects (decision 12), which its decision 57 replaces with caps; a child's age as a deadline (decision 13); ownership by concern (decision 35); this change's membership of the v1 program (decision 43); `labels` set by the root's client and inherited unchanged, beside a parent's `tags` (decision 44); `gateway.delegation.sessionBudget` (decisions 52 and 53); and `maxLeaves` (decision 54). |
| SO-5a | [`0142-security-oversight`](../0142-security-oversight/change.md#so-5a-tool-use-approvals) | Tool-use approvals, whose `when.spendAbove` holds a child's creation, a `session.set_limits` raise, a granted extension of a cap, and a release, when any currency limit crosses the amount (0142 decisions 46 to 50). |
| RO-2 | [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md#ro-2-rest-additions) | The session events of the REST surface, `budget_notice` among them. |

## High-level design

A budget is a named record that says how much of something a set of sessions may use in a period. An operator, a tenant administrator, or a client creates it. It holds one or more limits. Each limit names the meters it counts, and each meter has its unit: the charges of model calls and other paid services, the cost of pod time, and the cost of stored data, each in the deployment's currency; pod time in compute units; and stored bytes as a capacity that ignores the period. A team's monthly budget can therefore cap its model calls and its total spend in one record. It names the sessions it applies to, either by their attributes, such as a tenant, an environment, a directory group, a runtime, or a label, or by being named when a session is created. It names when it resets: never, at the start of each day, week, month, or year in a time zone, on a cron schedule, or every fixed interval, and anyone allowed to change it can also reset it on demand.

Every session can also have its own session budget, which is a budget object like the others and ends with the session. When a session uses something, the gateway finds every active budget that applies to it, its session budget included, and the use counts against every limit of those budgets that counts it. Budgets are best effort, and how closely each kind is held differs (decision 24). The session budget of the session's tree, with the caps on its path, is held closely: while a session holds a pod the gateway holds one minute of compute at a time, before a write the gateway holds the bytes, and a charge that a reporter sends counts in them when it arrives. Shared budgets, which every session of a team or a tenant reaches, are counted afterwards: each gateway replica adds up what its sessions spend and writes the sums to the shared counters every few seconds, and it refuses new work in a shared budget that its last read showed as spent. A shared budget can therefore pass its limit by a few seconds of spending. A write to a shared budget's storage limit is counted when it is admitted, at the size it declares, and is admitted only when that size fits what the limit had left at the replica's last flush, less what the replica admitted since, so a burst of writes stops near the limit without a call to Redis for each. If a limit lacks room, the work is refused, and the limit's exhaustion action applies to running sessions: the attribution bindings of the sessions that charge the limit are suspended, so the LLM gateway refuses their next model call, the session that holds the limit, such as a tree's root for the tree's session budget, and every session below it are suspended together, keeping their pods for a short hold so that a raise of the limit resumes them at once and releasing them after it, or a session ends, as the budget states. Stored data is never deleted because a budget ran out, and the checkpoint that saves a session's workspace when its pod is released is always stored.

A limit can count several currency meters at once. The operator writes a price list that gives a price per compute unit-hour and per GiB-month stored, in the deployment's currency, and may give a tenant its own prices. A reporter's charges arrive in that currency. Pod time and stored data are priced at the price in force when they occurred, so a team's monthly limit counts its model calls, its pod time, and its stored data together. A gateway that enforces its own limits can instead report that a session hit one, and Lenny suspends that session until its client resumes it. The price list is an operator setting. Lenny reports its money figures as estimates and charges no one.

A delegated child shares its tree's budget: every session of a tree charges the session budget of the tree's root, so the tree as a whole never spends more than the root's amount. A parent puts a cap on each child, by default half of what the parent may still spend, and the cap bounds the child and everything below it without setting anything aside, so siblings draw on what the tree has left in the order they spend it. Shared budgets count the child's use as it happens, as they count any session's. Decision 12 of 0166 chose a carve, which decision 70 replaced with this count at the root, and 0166's "DP-3" cites this change for it.

Budgets keep their counters in Redis and their definitions and closed periods in Postgres. A gateway crash loses no charge: a reservation expires into a recovery sweep that settles it from the meter's durable record, and the sums a crashed replica had not yet written are added from the same records. Each kind of budget has a stated overshoot: a session budget by the charges that arrive before its attribution is suspended, one compute tick, or the checkpoint grant window that §11.2 states today, and a shared budget by one flush interval of spending plus the work in flight when it runs out. A suspension's pod hold, the checkpoint at a pod's release, and a Redis outage within the per-replica fail-open share add their own stated terms (BGR-9).

```
 charge sources (meters)                 gateway                                   stores
 ───────────────────────                 ───────                                   ──────
 reporter: charge ───externalCost─┐
 pod binding: each tick ──compute*─┼──>  subject (session attributes)
 storage write / accrual ─storage*─┤       │
 (no other meter) ────────────────┘       ▼
                                         budget set = session budget
                                                    + shared budgets whose selector matches
                                                    + budgets referenced by the session or an ancestor
                                           │
                                           ▼
                                         for each limit that counts the meter:
                                         in the meter's unit
                                         (cost meters: price-list version in force)
                                           │
                                           ▼
                                         tree's session budget and caps: reserve ahead
                                         shared budgets: check cached state
                                           │
                                           ├──fits──> work runs, settle in session budget,
                                           │          add to replica's in-memory sums  Redis counters
                                           │          ──every flushSeconds──> flush to shared counters
                                           └──does not fit──> refuse, exhaustion action, notices
                                                                                Postgres: budgets,
                                                                                periods, price lists

 compute*: computeCost, computeUnits      storage*: storageCost, storageBytes
```

## Requirements

| ID | Requirement |
| :-- | :-- |
| BGR-1 | Every charge has one meter and one subject, and the gateway applies it to every limit that counts that meter, in every budget of the subject's budget set, in the unit of its meter, a charge of `computeCost` or `storageCost` priced under "BG-5", except that a charge on `externalCost` counts only in the budgets that its source reaches ("BG-12" **Counting by source.**). |
| BGR-2 | No reservation is admitted unless every limit that counts the charge's meter in the session budget of the session's tree, and the cap of that key of every capped session on the session's path to the root, has room for it ("BG-8"). No work is admitted while a replica's cached state shows a limit that counts its meter, in a shared budget of the session's set in `enforce` mode, as exhausted, and no write is admitted to a shared capacity limit on `storageBytes` when its declared size exceeds what the replica's cached state shows the limit has left, less the bytes the replica admitted to it since its last flush ("BG-6"). A shared budget in `observe` mode counts the charge and refuses nothing, and a session budget is always in `enforce` mode. |
| BGR-3 | A principal budget applies only to sessions whose creation names it in `budgetRefs`, and to their descendants. A tenant budget never applies to a session or a charge of another tenant. |
| BGR-4 | A session's budget set is computed from attributes fixed when the session is created, so it changes only when a budget is created, changed, enabled, disabled, or deleted. |
| BGR-5 | A charge of `computeCost` or `storageCost` is priced at the price-list version in force at the charge's time. Price-list versions are append-only, a version whose `effectiveFrom` is in the past is never written, and no release of Lenny carries a price. |
| BGR-6 | A budget with a schedule starts a new period at each boundary of its schedule in its time zone, and a reset on demand starts one at once. Each closed period is recorded with its consumption by limit and by meter, and each charge counts in the period in which it settles. |
| BGR-7 | Every charge of a session of a delegation tree counts once in the session budget of the tree's root, once in the subtree counter of every capped session on its path, and once in each shared budget of the session's set that its source reaches ("BG-12" **Counting by source.**), and a cap sets nothing aside in its parent's bound or the root's limit. |
| BGR-8 | The exhaustion of a budget never deletes or truncates stored data. |
| BGR-9 | Overshoot is bounded per meter and per kind of budget. For a session budget and a cap: for `externalCost`, by the charges of the calls that the reporter's gateway admitted before the attribution of the sessions under the exhausted limit was suspended, as LUR-6 of 0133 states, and by the charges that the session's owner posts on its own sessions; for the compute meters, by one compute tick; for `storageBytes`, by the outstanding checkpoint grant window of §11.2. For a shared budget: by the charges that every replica settles in one `budgets.shared.flushSeconds`, plus the work in flight when a replica's cached state turns to exhausted, in which each charge counts at most `budgets.external.maxChargeAmount` when it is set; for `storageBytes`, by the writes that the other replicas admit in one flush interval, once, before the flush that counts them marks the limit exhausted. During a Redis outage or a run of failed flushes, by each replica's fail-open share of what each limit had left, until `quotaFailOpenCumulativeMaxSeconds` turns the replica to refusing. For either kind: the pod time of a suspension's hold, `budgets.suspendPodHoldSeconds` per suspended session, and one checkpoint of a session's workspace each time its pod is released by a suspension's hold or a move to `dormant`. |
| BGR-10 | A gateway replica's crash loses no charge: every reservation of the replica is settled by the recovery sweep from its meter's durable record, the charges it settled after its last flush to a shared budget are added to that budget from the same records, the flush script refuses the sums of a lease the sweep has fenced, and a replica whose lease lapsed has the sweep run again for its old lease before it drops its unflushed sums when it returns, so no charge counts twice or is lost, and a Redis loss rebuilds no counter below the consumption that Postgres records for its period. |
| BGR-11 | The gateway records an audit event for each creation, change, deletion, and reset of a budget, each threshold a limit crosses in a partition and a period, each exhaustion of a limit, and each price-list version. |
| BGR-12 | A budget's usage report states, for each period, partition, and limit, the consumption by meter, the reserved amount, the limit's amount, and, for a currency limit, the amount labelled as an estimate. |
| BGR-13 | A charge counts in a budget only when its proof names a mint of the Token Service's mint index that "BG-12" accepts, or is posted by the session's owner on its own session, within `budgets.external.maxReportDelaySeconds` of the time it names, and only in the budgets that its source reaches: a platform budget counts only a platform reporter's charges, and a charge that the session's owner posts counts only in the session budget of the session's tree, the caps on its path, and the owner's own principal budgets. Each charge identifier counts once per reporter. |
| BGR-14 | A limit notice suspends only a session whose attribution assertion reached one of the reporter's hosts, with that session's subtree, the runtime's `external_limit` frame suspends only its own session, with that session's subtree, and a suspension by an external limit ends only when the root's client resumes the suspended session with an explicit resume that acknowledges the external limit (decision 68, and 0146's L39 and L45). |

## Current state

**Budgets are fixed scopes [source].** The [§11.2](../../spec/11_policy-and-controls.md#112-budgets-and-quotas) table lists token limits per request, session, user window, tenant window, global window, and task tree; runtime limits per session and per child; a retry budget; a delegation budget of depth, fan-out, and total children; concurrent sessions per tenant and per user; a per-tenant storage quota; and eval submission rate limits. **Hierarchical Quota Model:** states that soft warnings are emitted at 80% of a quota as billing events, that hard limits refuse at 100% with `QUOTA_EXCEEDED`, and that "Quota reset periods are configurable per quota type: hourly, daily, monthly, or rolling window."

**Windows are fixed UTC buckets [source].** `quota.ResetPeriod` in `pkg/quota/quota.go` is the closed set `hourly`, `daily`, `monthly`, and `rolling`. `window` in `pkg/quota/quotastore/quotastore.go` maps a period to a key label from the UTC time, such as `monthly-202611`, and refuses `rolling`, which the fixed-window store does not keep. No time zone, weekly period, or on-demand reset exists. The tenant record carries `StorageQuotaBytes`, `TokenQuotaPerWindow`, and `QuotaResetPeriod` (`pkg/tenancy/tenantstore/tenantstore.go`).

**Separate enforcement paths and counters [source].**

- Session token budget: `sessionbudget.Enforcer` (`pkg/gateway/meteringproxy/sessionbudget`) counts per replica in process memory and refuses after exhaustion, which F-11.2.34 records. 0133.S2 removes it with the proxy, and 0170.S1 closes the finding with Redis counters.
- Tree budget: the script `reserveScript`, embedded in `pkg/gateway/platformtools/treebudget/treebudget.go`, carves a child's token budget from its parent at delegation, with keys under the `{root_session_id}` hash tag ([§8.3](../../spec/08_recursive-delegation.md#83-delegation-policy-and-lease), **Budget Reservation Model:**).
- Storage quota: the script `reserveScript` in `pkg/quota/storagequota/redisstore/redisstore.go`, which a comment in `pkg/quota/quota.go` names `storage_quota_reserve.lua`, reserves bytes against the tenant's `storageQuotaBytes` before an upload or a checkpoint write, and the counter is rebuilt from the `artifact_store` sum and the outstanding checkpoint reservations (§11.2, **Storage quota enforcement mechanism.**).

**Lease extension is per session or tree [source].** [§8.6](../../spec/08_recursive-delegation.md#86-lease-extension) extends `maxChildrenTotal`, `maxParallelChildren`, `maxTokenBudget`, `maxTreeSize`, `perChildMaxAge`, and `fileExportLimits` in `auto` or `elicitation` mode, within ceilings resolved from the deployment, the tenant, and the runtime.

**What the tree meters [source].**

| Resource | Metered today | Planned by a v1 change |
| :-- | :-- | :-- |
| Model calls | Input and output token counts per proxied call (`pkg/gateway/meteringproxy/metering`), with cache tokens missed (F-11.2.38). | Charges that the operator's LLM gateway reports on `externalCost` ("BG-12"), and no parsing in Lenny (0133 decision 22). |
| Pod time of a session | No billing event carries pod time. `usagestore.Record` sets no `PodMinutes`, so `totalPodMinutes` sums zeros (0121 "Current state", read 2026-11-05, paths updated 2026-11-19). | `session.pod_usage` per pod-binding window with the pod's effective requests and slot count (0121 "BR-2"). |
| Idle pods | `lenny_warmpool_idle_pod_minutes` by pool and resource class, with no tenant (`pkg/controller/warmpool/idle_meter.go`). | `pool.idle_usage` for owned pools and pinned idle pods (0121 "BR-3"). |
| Dedicated nodes | Nothing. | `node.dedicated_usage` (0121 "BR-7"). |
| Stored bytes | The per-tenant Redis counter of the storage quota, and the `artifact_store` ledger. | `storage.usage` with bytes held and byte-hours per tenant, store, and artifact type (0121 "BR-4"), and legal-hold escrow (0121 "BR-6"). Checkpoint packs carry `owner_session_id` (0131 "IC-4"). |
| Egress bytes | Nothing as a meter. | `egress.tunnel` audit records carry bytes in each direction and can lack them, because Envoy's access log stream is lossy ([`0127-egress-and-git`](../0127-egress-and-git/change.md)). |
| Postgres-held data | Nothing. | A per-tenant gauge with no billing event (0121 "BR-5"). |

**No money [source].** No field of the specification or the tree states a price in money or a currency; the word "currency" appears in `spec/` and `pkg/` only for the freshness of CRD versions and of the legal-hold ledger. 0133 decision D3 states credits as "unitless, tied to no currency", and its OD-6 makes the estimated credit cost of LLM calls the one exception to 0121's rule that Lenny records quantities and the operator prices them.

**Roles [source].** The [§10.2](../../spec/10_gateway-internals.md#102-authentication) permission matrix gives "Manage quotas" to `platform-admin` for all tenants and to `tenant-admin` for its own tenant, read-only to `tenant-viewer`, and none to `billing-viewer` and `user`. "View usage / metering" is open to every role but `user`.

**Labels are set at creation [source].** §14 defines session `labels` on the creation request. No route of §15.1 changes a session's labels after creation **[unverified]**: the search found none, and "Validation owed" lists it.

**No step has started [source].** Every step of 0121, 0131, 0133, 0146, and 0149 is `Planned` on 2026-11-23, and every step of 0166 is `Planned` as well. This change therefore replaces design text and no built code, apart from the enforcement paths above, which predate the v1 program.

## Detailed design

The detail below states the design that the decisions of `decisions.md` settle, and each part names the decisions it follows.

### BG-1 Budget objects

A budget is a record with these fields **[design]**:

| Field | Meaning |
| :-- | :-- |
| `name` | Unique within its scope: the platform, one tenant, or one principal. |
| `scope` | `platform`, `tenant`, `principal`, or `session`, set by the route that creates it. |
| `limits` | A map from a key, unique within the budget, to a limit: `meters`, at least one meter, all of one unit under "BG-4"; `amount`, in that unit; and optional `thresholds` and `onExhausted`, which override the budget's. A limit takes no `unit` field, because its meters fix the unit, and the gateway refuses a limit that names one with `400 VALIDATION_ERROR` naming the field (decision 66). An amount is a decimal number, and on `storageBytes` also a Kubernetes quantity string such as `"100Gi"`. The gateway stores it as an integer in the unit's smallest step: micro-units of the currency, milli-compute-unit-seconds, and bytes. A budget holds at least one limit and at most `budgets.maxLimitsPerBudget` (default 8). |
| `appliesTo` | The selector of "BG-3". A principal budget has none. |
| `attach` | `selector`, `reference`, or `both`. A principal budget is always `reference`. |
| `usableBy` | For a tenant or platform budget with `attach` including `reference`, the principals and directory groups that may name it in `budgetRefs`, and `interceptors`, the names of the interceptor registrations of 0141 that may attach it ("BG-3"). |
| `per` | Optional partition key: `tenant`, `user`, `session`, `rootSession`, `environment`, `runtime`, or `label:<key>`, written as the key alone or as `{key, maxPartitions}`. The budget keeps one set of counters for each value, each limit at its full amount (decision 32), and opens at most `maxPartitions` partitions in a period, which `label:<key>` requires (decision 56, **Partitions** below). Every partition key is qualified by the session's tenant, so a platform budget keeps apart the `prod` environments of two tenants. |
| `period` | The schedule of "BG-7", with its time zone, shared by every limit. A capacity limit ignores it, because it limits what is held at each moment. |
| `thresholds` | Fractions of each limit's amount, default `[0.8, 1.0]`, at most `budgets.maxThresholds` (default 8). A limit may set its own. |
| `onExhausted` | The actions of "BG-6" for new work and for running sessions. A limit may set its own. |
| `mode` | `enforce`, the default, or `observe`. A session budget takes no `mode` and is always in `enforce` mode (decision 50). |
| `startsAt`, `endsAt` | Optional. A budget is active between them and in `enforce` or `observe` mode, and `disabled: true` makes it inactive without deleting it. A session budget takes none of the three, because it lives exactly as long as its session (decision 50). |
| `resourceVersion` | The version a `PUT` names in `If-Match`. |

**Limits [design].** A charge must fit every limit that counts its meter, in every budget of its set, so a charge on `externalCost` must fit both a limit that counts `externalCost` alone and a limit that counts `externalCost` and `computeCost` (decision 18), within the bounds that "BG-6" states under **Best effort**. A limit's meters share one unit: the currency meters combine in one limit, which adds their charges in the currency, and `computeUnits` and `storageBytes` each stand alone. The gateway refuses a limit whose meters differ in unit with `400 VALIDATION_ERROR` naming them (decision 67). When one limit is exhausted, the work of its meters stops and the work of the budget's other meters continues: a budget whose `externalCost` limit is exhausted suspends its sessions' attribution and keeps charging pod time to its `computeUnits` limit. A budget whose selectors, partitions, or periods differ is a separate budget.

**Who creates what [design].** A budget only narrows what a session may do, because every charge must fit every budget of its set. The authority to create one therefore follows the reach of its selector:

| Creator | Route | Scope | Selector | Attachment |
| :-- | :-- | :-- | :-- | :-- |
| `platform-admin` | `/v1/admin/budgets` | `platform`, across every tenant or a listed set, or `tenant` for any tenant | Any field of "BG-3" | `selector`, `reference`, or `both` |
| `tenant-admin` | `/v1/admin/budgets` | `tenant`, own tenant | Any field of "BG-3" except `tenants` | `selector`, `reference`, or `both` |
| A user or a service account | `/v1/budgets` | `principal` | None | `reference`, by the creator alone |
| Session creation | `POST /v1/sessions` | `session` | The session and its delegation tree | Implicit ("BG-2") |

A principal budget cannot reach another principal's session, because only its creator can name it and a reference attaches only at the creation of the creator's own session.

**Caps on the number of budgets [design].** Every budget is a stored record with counters, and every charge reaches each budget of its set, so the number of budgets has caps (decision 6):

| Cap | Default | Set by | What it bounds |
| :-- | :-- | :-- | :-- |
| Tenant budgets per tenant | `budgets.maxPerTenant`, 500 | The Helm value for every tenant. A `platform-admin` may set `budgetCaps.maxPerTenant` on one tenant's record, up to `budgets.maxPerTenantCeiling` (default 5000). | The records, counters, and selector matching one tenant puts on the shared stores. |
| Principal budgets per principal | `budgets.maxPerPrincipal`, 20 | The Helm value for every tenant. A `platform-admin` may set `budgetCaps.maxPerPrincipal` on one tenant's record, up to `budgets.maxPerPrincipalCeiling` (default 200), and a `tenant-admin` may lower it for its own tenant's principals. | The personal budgets one user or service account, or a script holding its credential, can create. |
| References per session creation | `budgets.maxRefsPerSession`, 8 | The Helm value alone. | The references one creation names, the client's and an interceptor's together. |
| Budgets in one session's set | `budgets.maxBudgetSetSize`, 32 | The Helm value alone. | The shared budgets each charge of a session reaches, selector matches and references together, which each replica caches and flushes. |
| Limits per budget | `budgets.maxLimitsPerBudget`, 8 | The Helm value alone. | The counters each reservation in one budget updates. |
| Partitions per budget and period | `budgets.maxPartitionsPerBudget`, 10000 | The Helm value alone. It bounds the `maxPartitions` a budget may declare. | The counters one budget opens in a period, for each tenant on a platform budget (decision 56). |
| Adjustments per budget | `budgets.maxAdjustmentsPerBudget`, 100 | The Helm value alone. | The adjustments whose window has not ended. |
| Resets on demand | `budgets.minResetIntervalSeconds`, 300 | The Helm value alone. | The period rows and audit events that resets of one budget and partition write. |

The gateway refuses a write beyond a tenant or principal cap, an adjustment beyond `budgets.maxAdjustmentsPerBudget`, and a reset sooner than `budgets.minResetIntervalSeconds` after the last reset of the same budget and partition with `429 QUOTA_EXCEEDED` naming the cap. It refuses a budget write whose `maxPartitions` exceeds `budgets.maxPartitionsPerBudget` with `400 VALIDATION_ERROR`. It refuses a session creation that names more references than the cap with `400 VALIDATION_ERROR`, a session creation that would open a partition past a budget's `maxPartitions` with `429 BUDGET_PARTITIONS_FULL` (**Partitions** below), and a session creation whose budget set would exceed `budgets.maxBudgetSetSize` with `429 QUOTA_EXCEEDED` naming the cap, and the audit event names the budgets, so a tenant administrator who writes too many overlapping selector budgets refuses its own sessions rather than loading the shared Redis. A lower cap set later deletes no budget and refuses new ones until the count falls below it. A partition (`per`) keeps one counter per value inside one budget, so a limit per user or per environment counts as one budget.

**Partitions [design].** A budget with `per` opens a partition the first time a session with a new value is charged in a period, and holds at most `maxPartitions` open partitions in the period, which bounds what a budget can give out to `maxPartitions` times each limit's amount, whatever values clients choose (decision 56). `per: label:<key>` requires `maxPartitions`, and the other keys take `budgets.maxPartitionsPerBudget` when they name none. A session creation whose charge would open a partition past the cap is refused with `429 BUDGET_PARTITIONS_FULL`, whose details name the budget when the caller may read it. On a platform budget the cap counts the partitions of each tenant apart, so one tenant cannot fill a platform budget's partitions for the others. Partitions close and free their places as follows:

- At each period boundary every partition's counters start over at their amounts, and the set of open partitions empties. A partition reopens at its value's next charge.
- A partition that still holds stored bytes under a capacity limit stays open across the boundary, because that limit ignores the period.
- A reset of every partition starts a new period and frees every place. A reset of one partition closes it and frees its place, and its value reopens with full amounts at its next charge ("BG-7").
- A budget with no schedule has one period for its life, so its places free only by a reset or a raised `maxPartitions`.
- A partition of `per: session` or `per: rootSession` closes when its session, or the root, reaches a terminal state, because its value never recurs.

A partition whose sessions have all ended keeps its place until the period closes, because it still holds the period's consumption, and closing it earlier would let a value return with fresh amounts.

**Defaults for a new tenant [design].** `budgets.tenantDefaults`, a Helm list of budget templates, is instantiated as tenant budgets when a tenant is created, such as a storage cap with a capacity limit on `storageBytes`. A `tenant-admin` may change or delete a budget instantiated this way. A `platform-admin` who wants a limit a tenant cannot remove writes a platform budget instead. This replaces the tenant's `storageQuotaBytes`, `TokenQuotaPerWindow`, and `QuotaResetPeriod` fields and 0133's `quotas.llmCredits.perUser`, `perTenant`, and `global`, which leave, and `STORAGE_QUOTA_EXCEEDED` retires into `BUDGET_EXHAUSTED` (decision 30). `quotas.llmCredits.perCall` leaves with the proxy under 0133's decision 22.

### BG-2 Session budgets

A session creation may carry `budget`, which creates the session's budget object **[design]**:

```json
{
  "budget": {
    "limits": {
      "models": { "meters": ["externalCost"], "amount": 40.00 },
      "pods":   { "meters": ["computeUnits"], "amount": 4 },
      "spend":  { "meters": ["externalCost", "computeCost", "storageCost"], "amount": 60.00 }
    },
    "thresholds": [0.5, 0.9],
    "onExhausted": { "running": "suspend" }
  }
}
```

- **Limits.** A session budget holds limits as every budget does ("BG-1"), so a session's own budget can cap model calls, pod time, and spend together.
- **Defaults and ceilings.** A session budget's limits resolve under "Resolution" in [DP-1 of 0166](../0166-delegation-primitives/change.md#dp-1-session-grant), which states the order of defaults and ceilings for every limit a session receives (0166 decisions 40 and 49). This change names the settings that resolution reads: `budget` on the creation request as the requested value, `sessionBudgetDefaults` on a runtime, an environment, a tenant, and the platform as the defaults, and `sessionBudgetCeilings` at the same levels as the ceilings, each a `limits` map. Keys join the levels: a requested limit, a default, and a ceiling with the same key resolve as one limit, and the gateway refuses with `400 VALIDATION_ERROR` a key whose meters differ between levels. A ceiling whose key no request or default names stays in the session budget at its amount, so a ceiling binds whatever keys a client chooses. A requested limit whose key no ceiling names only narrows, because every charge must fit every limit. A session with no default, no ceiling, and no request has no session budget and is bound by its shared budgets alone. Whether such a session may delegate, or be created holding a grant, is `gateway.delegation.sessionBudget` of 0166 "DP-1" (decision 39), which by default refuses a child of a session whose tree has no session budget, so every child is capped, and the requirement reads the session budget of the session's tree, which a child reads at its root. The platform's defaults and ceilings are the Helm values `budgets.sessionBudgetDefaults` and `budgets.sessionBudgetCeilings`. A tenant's are written with `PUT /v1/admin/tenants/{tenant}/session-budget`, which takes `defaults`, `ceilings`, and `defaultDelegationFraction` by limit key ("BG-8"), by a `tenant-admin` for its own tenant or a `platform-admin`. An environment's are the fields `sessionBudgetDefaults`, `sessionBudgetCeilings`, and `defaultDelegationFraction` of the environment resource, which 0149 "TS-3" lists, and a runtime's are the same fields of the runtime record, which 0147 "DR-4" and 0140's field classes list. The numeric `ceilings` of 0166's tenant and environment delegation settings carry no budget amount.
- **Mode and actions.** A session budget is always in `enforce` mode and takes no `mode`, `startsAt`, `endsAt`, or `disabled`, which the gateway refuses with `400 VALIDATION_ERROR` (decision 50). For each limit, `onExhausted.running` resolves to the strictest action that any level sets for its key, the request, a default, or a ceiling, in the order `end`, then `suspend`, then `continue`, so a client makes its own budget stricter and never looser. `continue` on a limit that counts a compute meter is refused on every route that writes a session budget limit: the session creation, `PATCH`, `session.set_limits`, a lease extension, and an interceptor's `session_budget`. A child's cap takes the action that the root's limit of the same key resolved to ("BG-8"). A client that wants to try amounts without refusals uses a principal budget in `observe` mode with `per: rootSession`, named in `budgetRefs` on its sessions, and reads each tree's consumption from its usage route.
- **Derived sessions.** A derive of [`0144-session-derive`](../0144-session-derive/change.md) creates a new root. Its request carries its own `budget`, `budgetRefs`, and `labels`, which resolve and are checked as at any root's creation, and nothing of the source's budget set is copied (0144 decision 14, decision 48).
- **Lifetime.** The session budget has no schedule. It is created in the transaction that creates the session, it refuses nothing once every session of the tree it counts has reached a terminal state, while the late charges of "BG-12" **Ended sessions.** still count in it, and its record is kept with the session's usage records for the session's retention. Storage held after the session ends counts against the shared budgets alone (decision 21).
- **Changes.** The session's owner reads it with `GET /v1/sessions/{id}/budget` and raises or lowers a limit, by key, with `PATCH /v1/sessions/{id}/budget` within the ceilings. A raise beyond the ceilings goes through lease extension ("BG-6"). A delegated child holds caps in place of a session budget: `GET` returns its caps, what its subtree has consumed against each, and the limits of its tree's session budget, its parent changes its caps with `session.set_limits`, and the root's client changes them with `PATCH`, each within the bound of the child's parent ("BG-8", decisions 52 and 70).
- **Removed fields.** `maxTokenBudget` of the delegation lease, and the `maxCreditBudget` that 0133 put in its place until its revision 12, leave every input of a session grant under 0166's "DP-1": a client's `delegationLease`, a runtime's default lease, the delegation presets, and the tenant and environment ceilings carry no budget amount, and `budget`, `sessionBudgetDefaults`, and `sessionBudgetCeilings` take their place. Under decision D9 of 0133, a write that names either is refused with `400 VALIDATION_ERROR` naming the field.

### BG-3 Matching

**Selectors [design].** A selector is a conjunction of fields. Within a field, a list matches any of its values. An absent field matches every value.

| Field | Matches | Who sets the attribute |
| :-- | :-- | :-- |
| `tenants` | The session's tenant. Platform budgets alone. | The platform. |
| `environments` | The session's stored environment under 0149 "TS-1". | The client chooses among environments it is a member of. |
| `principals` | The session owner's subject, a user or a service account. | The identity provider. |
| `principalKinds` | `user` or `serviceAccount`. | The identity provider. |
| `groups` | The owner's directory groups at the session's creation (0149.S9, decision 8). | The identity provider or the directory. |
| `runtimes` | The session's runtime after the `PreRoute` chain. | The client and the routing interceptors. |
| `pools` | The session's pool. | The routing interceptors. |
| `labels` | Each key and value of `matchLabels` among the session's `labels`. | The client. |


**References [design].** `budgetRefs` on `POST /v1/sessions` names shared budgets by name, a tenant budget as `tenant/<name>` and a principal budget as `principal/<name>`. The gateway refuses a name the caller may not use and a name that does not exist alike, with `404 RESOURCE_NOT_FOUND`, so that a caller learns nothing about budgets it may not use. A child of any depth inherits its ancestors' references, which serves a named session tree: a client creates a principal budget and names it on the root, and the whole tree draws on it.

**Budgets attached by an interceptor [design].** A `PreRoute` interceptor of [§4.8](../../spec/04_system-components.md#48-gateway-policy-engine) may return, with `MODIFY`, `budgetRefs` to add and session budget limits to lower or add, so a deployer's own logic, such as a lookup in a project registry or a cost-center database, decides which budgets a session draws on (decision 13):

- **Add only.** The interceptor adds references, lowers the amount of a session budget limit by key, and adds session budget limits. It cannot remove a reference the client named, a budget that matches by selector, or a limit, and it cannot raise a limit, so a faulty interceptor cannot loosen one.
- **Which budgets.** It may name a tenant budget of the session's tenant, or a platform budget, whose `attach` includes `reference` and whose `usableBy` includes the session's owner or names the interceptor's registration in `interceptors`. A budget that permits neither cannot be charged by an interceptor's choice, so an interceptor registered for one team cannot spend another team's budget. A name outside these rules fails the creation with `403 BUDGET_NOT_ATTACHABLE`, whose details name the interceptor's registration and no budget, and the audit event names both.
- **When.** The additions travel as the `budget_refs` and `session_budget` fields of the `PreRoute` payload, and 0141 holds the add-only rule with the immutable field enforcement of §4.8 and checks each added name at its post-chain point ([`0141-runtime-routing`, RT-2](../0141-runtime-routing/change.md#rt-2-the-interceptor-contract)). They count toward `budgets.maxRefsPerSession` and `budgets.maxBudgetSetSize`, and are fixed for the session's life and inherited by its descendants as the client's references are. The interceptor runs once per session, and no model call or compute tick waits for it.
- **Failure.** The registration's `failPolicy` applies. Under `fail-closed` a session whose interceptor does not answer is refused. Under `fail-open` it is created without the additions, so the budgets the interceptor would have attached do not bind it, and the gateway records `budget.attachment_skipped` naming the interceptor and the session. A deployer that relies on an attachment registers its interceptor `fail-closed`.

**External costs [design].** A deployer that pays for a service outside Lenny, such as a search API that a tool server calls, matches each cost to a session with what Lenny already sends ahead of the call, and either reports it as a charge on `externalCost` ("BG-12") or enforces its own limit there and reports that a session hit it ("BG-12" **Limit notices.**) (decisions 14 and 57):

| Call | What identifies the session | Source |
| :-- | :-- | :-- |
| A request from a pod to a deployer's service through egress | The identity assertion that `netagent` attaches: tenant, environment, user or principal type, session, root session, and depth, signed by Lenny | [`0124-connector-removal`, "CR-2"](../0124-connector-removal/change.md#cr-2-identity-assertion) |
| A model call that a runtime sends to an operator's LLM gateway | The attribution assertion that `netagent` attaches on the gateway's host: tenant, environment, pseudonymous user, session, root session, and depth | [`0124-connector-removal`, "CR-2"](../0124-connector-removal/change.md#cr-2-identity-assertion) **Attribution assertion.**, and 0133 "LU-2" |
| A call that the client makes, or a service to which the client hands work | The session identifier that the client holds: the client calls the service with its own credentials, such as a key of its own for the LLM gateway, the service tells the client what the work cost, and the client posts that cost as a charge on the session | "BG-12" **Direct charges by the owner.** (decision 71) |

From a session identifier, `GET /v1/sessions/{id}` returns the session's attributes and `GET /v1/sessions/{id}/budgets` its budget set. For a caller that holds "Read session attributes" ("BG-10"), the read also returns the directory groups recorded at the session's creation, which the `groups` selector reads, so a deployer's service that looks a session up once and caches it matches an external cost to the same team that Lenny's selectors match, with a credential that grants nothing else (decisions 15 and 40). A deployer that finds its own limit spent refuses new sessions with a `PostAuth` or `PreRoute` interceptor that returns `REJECT`, refuses model calls in its LLM gateway, and refuses its own service's calls itself.

**The budget set [design].** At creation, the gateway computes the session's budget set and stores the identifiers on the session row. Every attribute a selector reads is fixed at creation, so the set changes only when a budget changes. On a budget's creation, change, enablement, disablement, or deletion, the gateway writes an invalidation, each replica refreshes the tenant's budgets from Postgres, and the next charge of each session reads the new set. A charge in flight keeps the set it was admitted against. A new tenant budget with a selector therefore applies to running sessions from their next charge.

**Labels and label rules [design].** A session's `labels` are the ones its root's client set at the root's creation, inherited unchanged by every descendant, and no parent writes them (0166 decision 44). A parent's `tags` are its own bookkeeping, and no selector reads them. A tenant governs who may set a label key and value with the label rules of [`0149-tenant-scoping-identity`, "Label rules (TS-10)"](../0149-tenant-scoping-identity/change.md#label-rules-ts-10), which refuse a session that sets a governed key its owner may not set, and may require a key (decision 41). A budget that selects on a governed key therefore binds the sessions its tenant's rules admit to that label, and no client outside them can join it or spend it. A key with no rule stays free: any client may set it or omit it, so a budget that selects on such a key binds only the clients that choose to set it. For a budget, a key is governed when a tenant rule governs it, or when the budget selects on `environments` and every environment it names governs the key. The admin API accepts a budget that selects on a key governed neither way and answers its write with a `Warning` naming the key and the environments where it is free (decision 49). A write of label rules that frees a key some budget selects on answers with a `Warning` naming those budgets, and a child that crosses into another environment has its labels checked against that environment's rules (0149 decision 46). A budget that must bind selects on a governed or required key, or on `environments`, `principals`, `groups`, `runtimes`, or `pools` (decision 8). A budget partitioned by a label bounds its exposure with `maxPartitions` whether the key is governed or not ("BG-1", **Partitions**).

### BG-4 Meters and units

A meter is what a charge measures, and each meter has one unit, in which every limit that counts it states its `amount` (decision 67). This table is the one statement of the meters and their units **[design]**:

| Meter | Unit of `amount` | Kind | Quantity | Subject | Source of record |
| :-- | :-- | :-- | :-- | :-- | :-- |
| `externalCost` | The deployment's currency | Spend in a period | The amount a registered reporter or the session's owner charged (decision 57). | The session the charge's proof names, or the session of the owner's route. | `external_charges` rows ("BG-12"). |
| `computeCost` | The deployment's currency | Spend in a period | Slot time at the pod's rate in compute units, priced at the price list's compute price ("BG-5"). | The session bound to the slot. | `session_pod_windows` rows (0121 "BR-2"). |
| `computeUnits` | Compute-unit-hours | Spend in a period | Slot time at the pod's rate in compute units, by the formula below or a pin. | The session bound to the slot. | `session_pod_windows` rows. |
| `storageCost` | The deployment's currency | Spend in a period | Bytes held over time, priced at the price list's storage price. | The session that owns each artifact or pack, or the tenant for a tenant-owned platform chunk set. | `artifact_store` rows (0121 "BR-4", 0131 "IC-4"). |
| `storageBytes` | Bytes | Capacity | Bytes held at each moment. | As for `storageCost`. | `artifact_store` rows. |

**Combining meters [design].** A limit counts meters of one unit. The currency meters combine in one limit, which counts their charges together, and `computeUnits` and `storageBytes` each stand alone. The gateway refuses any other limit at write with `400 VALIDATION_ERROR` naming the meters. A limit carries no `unit` field, and `budgets.currency` names the currency once ("BG-5", decision 66).

**Capacity limits [design].** A limit on `storageBytes` is a capacity limit. It counts the bytes held now, ignores the period, and has no reset. It refuses a write whose declared size would carry it past its amount, under the write rules of "BG-6", and the checkpoint that releases a pod passes it as decisions 25 and 28 state. It takes no extension, because bytes held are not spent and return when data is deleted. As every limit of a session budget does, it counts its whole delegation tree at the root: one counter counts the bytes that the root and every descendant hold, and a child's cap on its key bounds the bytes the child's subtree holds, so a write by any session of the tree is admitted against the root's counter and the caps on its path ("BG-8", decisions 69 and 70). A transfer keeps the session's bytes in the tree's counter ("BG-8" **Transfer.**). A released session takes its held bytes out of the tree's counter, and its cap on the key becomes the capacity limit of its own session budget, which counts them from then on, so a released session holds a capacity limit whenever its tree did ("BG-8" **Release.**, decision 75). A shared capacity limit, such as a tenant's storage cap, bounds every tree of its scope. The owner raises a session budget's capacity limit with `PATCH` within its ceilings ("BG-2").

The costs that 0121 attributes to a tenant and to no session, the idle pods of its owned or pinned pools (`pool.idle_usage`), its dedicated nodes (`node.dedicated_usage`), and legal-hold escrow, feed no budget (decision 29). A refusal of sessions cannot stop them, so a budget that counted them would refuse a tenant's sessions while the costs went on. They stay billing events, from which the operator, the deployer, and the tenant follow that spend at their own prices outside Lenny.

**Compute units [design].** One minute on a small pod counts less than one minute on a large one, because a pod's rate follows its resources (decision 4). A pod's rate in compute units per second is:

```
rate = (vCPU × cpuPerVCPU + GiB of memory × memoryPerGiB + Σ GPUs × gpu[<type>])
       × isolationMultipliers[<profile>]
```

The vCPUs, memory, and GPUs are the pod's effective requests, which 0121 "BR-2" records on each binding in `session_pod_windows`. A session's compute charge for an interval during which it holds a slot is the interval's length in seconds times the rate, divided by the pod's `max_concurrent_sessions_per_pod`, the per-slot share of 0121. The rate is resolved once, at the bind, and recorded on the binding:

1. The pool's `computeUnitsPerSecond`, when a `platform-admin` pinned one on the pool.
2. Otherwise the resource class's `computeUnitsPerSecond` in the chart's `resourceClasses` map, when a `platform-admin` pinned one.
3. Otherwise the formula above, with `budgets.compute.rates.cpuPerVCPU` (default 1), `budgets.compute.rates.memoryPerGiB` (default 0.125), `budgets.compute.rates.gpu.<type>` (no default), and `budgets.compute.isolationMultipliers.<profile>` (default 1 for each profile).

Every resource class, including one a tenant defines, is priced by the formula from its resources, so a new class needs no compute setting. A class under 0121 holds CPU, memory, and ephemeral storage, and 0121 leaves GPUs out of its scope, so the GPU term counts nothing until a later change adds GPUs to a class. That change makes the chart and the admin API refuse a pool whose pods request a GPU type that has no rate. Ephemeral storage carries no compute rate, because the storage meters count stored data. A pin serves a class whose cost the formula misstates, such as one on reserved hardware. A `tenant-admin` sets no rate, multiplier, or pin, including on a tenant's owned pool, because a lower rate would loosen every platform budget in compute units. The operator prices a compute unit in money in the price list ("BG-5").

A session holds a pod in the states that SL-1 of 0146 marks as holding one: setup with a pod, `running`, `idle`, `suspended` and `blocked` while the pod is held, and `resuming`. `dormant` and every state without a pod charge nothing.

**Compute meters [design].** A limit on `computeUnits` states its amount in compute-unit-hours, and the gateway counts it in milli-compute-unit-seconds. A limit on `computeCost` counts the same compute units priced at the price list's `perComputeUnitHour`. One compute tick charges both meters, so a budget can cap pod time in compute units and in the currency at once.

**Billing [design].** Every meter reads the records that 0121's billing events report, so budgets and billing count from the same sources. The billing stream gains nothing from this change: it already carries each pod's resources, isolation profile, slot count, and time, from which a billing system can apply the formula above or its own (decision 3).

**Storage [design].** Each stored artifact, checkpoint pack, output file, and upload has an owning session in the ledger, and a pack is charged once, to its owner, whichever session references it, as IC-4 charges the quota today. A pack of a tenant-owned platform chunk set has the tenant as its subject. A pack of a platform-owned set reaches no budget. A limit on `storageBytes` counts the bytes held now, at each moment, and ignores the period. A limit on `storageCost` counts their integral over the period, in byte-seconds, priced at the price list's storage price.

### BG-5 Price lists and money

**The record [design].** The deployment has one platform price list, which a `platform-admin` writes and which holds the standard prices (decision 54). It has versions, each with an `effectiveFrom` time, appended with `POST /v1/admin/price-lists/platform/versions` and never edited:

```yaml
scope: platform
version: 4
effectiveFrom: 2026-12-01T00:00:00Z
compute:
  perComputeUnitHour: 0.12
storage:
  perGiBMonth: 0.025                # a GiB-month is 2^30 bytes held for 730 hours
  byArtifactType:
    checkpoint: 0.020
```

The figures are illustrative, and no figure in this document is a price Lenny ships. A charge on `externalCost` arrives in the currency and is priced by no price list (decision 57). A version carries no currency field, because `budgets.currency` names the currency once (decision 66).

**A tenant's prices [design].** A tenant's charges can have prices of their own (decision 54):

- **The tenant price list.** A `platform-admin` may append versions of one tenant's price list with `POST /v1/admin/price-lists/tenants/{tenantId}/versions`, with the same `effectiveFrom` rules and sections as the platform list. Each entry it holds overrides the platform list's entry of the same name for that tenant's charges: `compute.perComputeUnitHour` and the `storage` prices. An operator that charges a tenant negotiated rates states them here.

```yaml
# POST /v1/admin/price-lists/tenants/acme/versions, by a platform-admin
version: 2
effectiveFrom: 2026-12-01T00:00:00Z
compute:
  perComputeUnitHour: 0.10
```


- **Currency.** `budgets.currency` is one Helm value for the deployment: an ISO 4217 code such as `EUR`, or a code the operator invents, such as `PTS` for an internal points scheme, of 3 to 12 uppercase letters (decision 34). It is the one place the currency is named: limits and price lists carry no currency field, a charge may name it only to have it checked, and every money figure Lenny reports is labelled with the code (decision 66). Lenny converts between no currencies and attaches no meaning to the code beyond labelling money figures. Money amounts are stored as integer micro-units of it. With no currency set, the gateway refuses a limit on a currency meter and a price list with `400 VALIDATION_ERROR`.
- **Validation.** The admin API refuses a version that leaves the compute or the storage price unset while a limit in its scope counts `computeCost` or `storageCost`, a version whose `effectiveFrom` lies in the past, and a version that names a `currency` field. `lenny-ctl price-list validate` checks a version against the deployment.
- **Conversion.** Each charge is converted at the version in force at its time: a compute tick at its start, and a storage accrual interval at its end. Each conversion multiplies in integers and rounds up once per charge, so no drift accumulates, and the version used is recorded with the charge.
- **Several meters.** A currency limit keeps one counter in the currency for every meter it counts. A compute tick reserves its compute units in every limit on `computeUnits`, and their price in every limit on `computeCost`, of the session budgets that hold it, and every shared limit on either meter receives the settled charge, priced at the same version. A charge on `externalCost` is already in the currency. Stored data reaches a limit on `storageCost` through the accrual of byte-seconds, which reserves nothing. A refusal names the budget, the limit, and its meters, so a caller sees which meter a limit refused.
- **Which price applies.** Each charge has one price, the same in every budget that counts it, platform budgets included (decision 54). A compute charge and a storage charge convert at the tenant price list's entry when it has one, and at the platform list's otherwise. An operator that wants a platform budget at its own cost, rather than at the prices it charges tenants, writes that budget's limits on `computeUnits`, and caps bytes held on `storageBytes` (decision 67).
- **What a tenant reads.** A `tenant-admin`, `tenant-viewer`, and `billing-viewer` read their tenant's effective prices, every entry with the tenant's overrides applied and the version each comes from, at `GET /v1/admin/price-lists/effective`, and no longer read the platform list or another tenant's, so a tenant learns neither another tenant's rates nor the operator's standard prices where its own differ.
- **Estimates.** Every money figure Lenny reports carries `estimated: true`, the currency code, and the price-list versions used. Lenny records quantities on billing events, as 0121 states, and no billing event carries money (decision 27).

### BG-6 Enforcement

**Best effort [design].** Budgets are best effort: a limit stops work near its amount and can pass it by the bounds of BGR-9 (decision 24). Every other section cites this statement. The gateway holds a budget in one of two ways:

- **Session budgets and caps reserve ahead.** At each compute tick and before a write, the gateway reserves the charge in the limits that count its meter of the session budget that counts the session's tree, and in the cap of each such key of every capped session on the session's path to the root, which share the root's hash tag, so the reservation is one script ("BG-8", "BG-9"). A limit or a cap that lacks room refuses the work. The reservation is settled to the actual charge afterwards. A charge on `externalCost` reserves nothing: it counts in the tree's session budget and the caps on the session's path when "BG-12" accepts it.
- **Shared budgets count afterwards.** Each gateway replica keeps a cached state of every limit, partition, and period of the shared budgets its sessions reach: the amount left, or exhausted. Before it admits work, it reads that state, and it refuses the work when a limit in `enforce` mode that counts the work's meter is exhausted. When a charge settles, the replica adds it to an in-memory sum for each shared budget, partition, period, and limit that counts it. Every `budgets.shared.flushSeconds` (default 5, from 1 to 60) the replica adds each sum to the Redis counter in a script that also records the replica's flush watermark for that counter, and refreshes its cached state from the counters it wrote and read. A shared budget holds no reservation, and a replica never reserves per call in one, however close the budget is to its amount.
- **Writes to a shared storage limit count at admission (decisions 36 and 53).** Every write declares its size before its bytes move: an upload its declared length, an export its size, and a checkpoint chunk its grant. A replica admits a write to a shared budget's capacity limit on `storageBytes` when the declared size is at most what its cached state shows the limit had left at its last flush, less the bytes the replica has admitted to the limit since that flush. It counts the declared size at admission, in the in-memory sum that its next flush adds to the counter, whether or not the write has finished. When the write completes, the replica corrects the count to the bytes stored, and a write that is abandoned gives back what it did not write. A write that does not fit is refused with `BUDGET_EXHAUSTED`, and `details.available` gives what the replica can still admit. The gateway makes no Redis call for the check, and it cuts off a stream that sends more than it declared. The overshoot is the writes that the other replicas admit in the same flush interval, once: after the flush that counts them the limit shows exhausted on every replica. The checkpoint that a suspension's hold or a move to `dormant` takes is admitted whatever the limit has left ("Exhaustion").
- **Fail-open (decision 35).** While a replica cannot reach Redis, or its flushes fail, it admits work in each shared limit, partition, and period up to its fail-open share: what the limit had left at its last successful flush, divided by the number of replicas it last saw, and at most `quotaPerReplicaHardCap` of the limit's amount. For budgets, `quotaPerReplicaHardCap` is a fraction of each limit's amount (default 0.5), configurable as [§12.4](../../spec/12_storage-architecture.md#124-redis-ha-and-failure-modes) makes the `per_replica_hard_cap` it replaces, whose `tenant_limit` decision 30 retires (decision 35). It keeps its sums in memory. `quotaFailOpenCumulativeMaxSeconds` (300 s by default) of cumulative fail-open turns the replica to refusing work that charges a shared limit until a flush succeeds, as §12.4 states for quotas today. Session budgets fail as "BG-9" states.

**Per meter [design].**

The table states when a session budget reserves and settles. A shared budget receives each charge when it settles, through the next flush.

| Meter | When a session budget reserves | When it settles | What a refusal stops |
| :-- | :-- | :-- | :-- |
| `externalCost` | No reservation. A charge counts when "BG-12" accepts it, in the tree's session budget and the caps on the session's path. | At acceptance. | The next model call or service call of every session under the exhausted limit, through the suspension of their attribution bindings ("BG-12" **Exhaustion**, 0133 "LU-5"). The sessions continue under `onExhausted.running`. |
| `computeCost`, `computeUnits` | At each bind, and at the start of each compute tick, `budgets.compute.tickSeconds` (default 60) of the slot's compute units. | At the end of each tick and at the release, for the time the session held the slot. | At a bind, the session's creation, its resume, or a child's creation. At a tick, the running session, under `onExhausted.running`. |
| `storageBytes` | Before an upload, an export, or a checkpoint chunk grant, the declared bytes. A shared budget's capacity limit, such as a tenant's storage cap, counts the declared size at admission, as "Best effort" states. | At confirmation, to the confirmed size, and at deletion, by the deleted size. | The write, with `BUDGET_EXHAUSTED`. |
| `storageCost` | No reservation. Each budget keeps the bytes its subjects hold, and a leader sweep adds bytes held times elapsed time every `budgets.storage.accrualIntervalSeconds` (default 300). | At each accrual. | New writes in the subjects of an exhausted limit. |

**Admission [design].** A session creation, a resume, and a child's creation are refused with `429 BUDGET_EXHAUSTED` when the replica's cached state shows a limit of an `enforce` shared budget of the new session's set as exhausted, whatever meter the limit counts, or when the first compute tick does not fit the tree's session budget or a cap on the session's path. A resume is also refused while a limit of the tree's session budget or a cap on the session's path whose `onExhausted.running` resolved to `suspend` has no room, whatever meter it counts, so a session that a budget left paused or `dormant` does not wake before the limit gains room (decision 82). A limit on a storage meter therefore stops new and resumed sessions as well as writes, so the checkpoint admitted at a pod's release only serves sessions that were live when the limit ran out (decision 37). Resume admission of [`0146-session-lifecycle`, "SL-10"](../0146-session-lifecycle/change.md#sl-10-admission-caps-and-timers) runs this check at every move toward a pod, a retry from `awaiting_client_action` included. Concurrency stays under §11.1.

**Thresholds [design].** When a charge carries a limit past a threshold in a partition for the first time in a period, the gateway writes `budget.threshold_crossed` with the budget, the limit's key, the partition, the threshold, the unit, the consumption, and the amount. A session budget's crossing is detected by the settlement script, and a shared budget's by the flush script that carries the counter past it, so each crossing is reported once per threshold per period. **Notices [design].** Each budget notice is an audit event and an event on the operational event stream of [§25.5](../../spec/25_agent-operability.md#255-operational-event-stream): `dev.lenny.budget_threshold_crossed`, `dev.lenny.budget_exhausted`, and `dev.lenny.budget_period_closed`, each carrying the budget's scope and name, the limit's key, the partition, the period's bounds, the consumption, and the amount. A subscriber receives them over the stream's server-sent events, polling, or webhook delivery, with its tenant filter, so a `tenant-admin` receives only its own tenant's budget events (decision 26). A budget carries no webhook targets of its own. When a charge carries a limit of the tree's session budget or a cap past a threshold, the root's client also receives a `budget_notice` event on the root session's event stream of [§15.1](../../spec/15_external-api-surface.md#151-rest-api), defined in the catalog CATEVENT, naming the session that holds the limit, the root or a capped session (decision 77). The runtime receives none, because a runtime learns of budgets through refusals alone (decisions 31 and 45). A shared budget sends no event to the sessions it binds.

**Exhaustion [design].** A limit of a session budget is exhausted when its consumption plus its reservations reach its amount, and a cap is exhausted, for its subtree alone, in the same way. A limit of a shared budget is exhausted in a period when its flushed consumption reaches its amount, and each replica learns of it at its next flush. Exhaustion stops the work of the limit's meters alone. `onExhausted`, on the budget or on the limit, states the action for new work and the action for running sessions:

| Field | Values | Default |
| :-- | :-- | :-- |
| `onExhausted.new` | `refuse`: refuse each new reservation of the limit's meters, which refuses new sessions, resumes, children, and writes in the budget's subjects, and suspends their attribution bindings, as each counts the meter. | `refuse` |
| `onExhausted.running` | `continue`: a running session keeps its pod, its attribution bindings are suspended, and it is refused at its next write. Every route that writes a limit refuses `continue` on a limit that counts a compute meter (decision 50), because a session would keep charging its pod without bound; a shared budget that should only report uses `mode: observe`. `suspend`: at the first charge of any meter the limit counts that the limit refuses, an accepted charge on `externalCost` that exhausts it, a compute tick, or a write, the session is suspended with its subtree, as "Budget suspension" below states. `end`: at the same refusal, the session that holds the exhausted limit expires with the reason `budget_exhausted` in any non-terminal state, and for a limit of a shared budget each session that "Budget suspension" names expires (decisions 51, 82, and 83). | `suspend` for a limit that counts a compute meter, `continue` otherwise (decisions 19 and 42) |

**Budget suspension [design].** A charge refused under `suspend` pauses, together, the session that holds the exhausted limit and every session below it: the tree's root and the whole tree for a limit of the tree's session budget, the capped session and its subtree for a cap, and, for a limit of a shared budget, the charging session and its subtree, or every live session that the budget binds for a limit that counts only `externalCost` (decisions 73 and 83). When the holder is not `running` or `idle`, L41 of 0146 states what pauses: a `blocked` or restoring holder by its own state, and the highest live sessions under a `dormant`, waiting, or ended holder, which keeps its state, and the limit gaining room resumes only what a budget suspended (decision 82). It runs through the budget suspension that [`0146-session-lifecycle`, "SL-4"](../0146-session-lifecycle/change.md#sl-4-tree-suspension) states as "Budget suspension (L35)" (decisions 42 and 51). 0146 owns the states, the pod hold and its end, the expiry of a session that cannot checkpoint, and what a client's resume and suspension do. This change owns the trigger, what gives room, and the hold's length:

1. **Trigger.** The first charge of a meter the limit counts that the limit refuses: an accepted charge on `externalCost` that exhausts it, a compute tick, or a write. A compute tick refused on a session that is already paused for another cause and still holds its pod also starts the hold, whatever the cause, so a paused session never keeps a pod past an exhausted compute limit beyond the hold. The holder of the exhausted limit is the suspension root, so every session that charges the limit pauses with it, and one raise or grant resumes them all. A limit of a shared budget has no holder in the tree. For a limit that counts a compute or a storage meter, the charging session is the suspension root, and each other session that the budget binds meets the refusal at its own next charge. A limit that counts only `externalCost` refuses no charge, because a charge counts at acceptance and the limit's exhaustion is learned at a flush, so at the flush that marks it exhausted each replica suspends, or under `end` ends, every `running` or `idle` session it serves whose budget set holds the budget, each with its subtree as its own suspension root, and a session that is not live meets resume admission (decision 83). Under the default `continue` of a limit with no compute meter, nothing is suspended.
2. **Extension.** For a limit of a session budget or a cap, the gateway suspends at once and requests the §8.6 extension meanwhile ("Lease extension" below).
3. **Room.** Room in the limit lets the subtree resume: the owner raising the limit, a parent's `session.set_limits` raise of a cap, the root client's `PATCH` of a cap, an administrator's raise, adjustment, or reset, a void of a charge ("BG-12" **Voids**), a granted extension, and a new period each give room. A void subtracts its amount from the counters that its charge incremented ("BG-12" **Voids**), so it gives room to every budget suspension of that limit, whichever charge exhausted the limit, and no record ties a suspension to a charge (decision 72). Room resumes a session that the budget suspended by itself only while the session still holds its pod in the hold: the gateway then resumes it through resume admission, which needs room in every limit on the session's path, as SL-4 states. After the release, room resumes nothing and the root's client resumes the session, except that a granted extension of the limit, and a raise of the limit or cap that the session holds by `PATCH /v1/sessions/{id}/budget` or `session.set_limits`, resume it at any time, because each acts on that session. An administrator's raise, adjustment, or reset, a void, and a new period resume only the sessions still within their hold (decision 85, 0146's L44). Room wakes no session that a budget left `dormant` or waiting (decision 82). A suspension by an external limit takes no room and none lifts it (decision 68).
4. **Hold.** Each paused session keeps its pod for `budgets.suspendPodHoldSeconds` (default 300, from 0 to 3600), which SL-4 reads.

The hold's pod time is charged, which BGR-9 bounds.

A limit on a storage meter never deletes data (BGR-8). A checkpoint whose chunk grant such a limit refuses aborts with `BUDGET_EXHAUSTED`, as a refusal by the tenant's storage quota aborts it today, and the session keeps its previous checkpoint (decision 19). The checkpoint that releases a session's pod is the exception: the checkpoint at the end of a budget suspension's hold, and every checkpoint that makes a session `dormant`, including the one that SL-6 of 0146 takes when a session's idle timer fires, is admitted past an exhausted limit on a storage meter, so the session's work survives the release of its pod (decisions 25, 28, and 42). The overshoot is one checkpoint of the session's workspace per release, which the size limits of the pod's volumes bound, and decision 37 keeps new and resumed sessions from adding more.

**Lease extension [design].** §8.6 stays for session budgets and caps, with `additional` naming amounts by limit key in place of `additionalTokenBudget`. When charges exhaust a limit of a session budget or a cap, the gateway suspends the attribution bindings of every session under the limit's holder ("BG-12" **Exhaustion**) and requests an extension under §8.6's approval modes, ceilings, and layering. The extension holds no call, and a grant lifts the suspension (0133 "LU-5"). When such a limit refuses a compute tick or a write, the gateway applies the refusal and its `onExhausted.running` action at once and requests the extension meanwhile. The request is made for the session that holds the limit that refused the charge, the root or a capped session, and names that limit. A grant raises it by the amount granted and sets nothing aside: the root's limit within its ceilings, and a cap at most to the current bound of its holder's parent, the parent's cap or the root's amount. A grant never raises an ancestor's cap, so §8.6's hard ceiling that a child's extension cannot exceed what its parent was granted stays (decision 76). Room past a parent's bound comes only from that bound being raised, by the parent's own parent with `session.set_limits` or by the root's client with `PATCH /v1/sessions/{id}/budget`, each within its own bound, whatever `extensionApproval` says, and a person who approves an extension under `elicitation` grants within the same bound. While the request is pending, the parent of a child whose cap refused may raise that cap with `session.set_limits` within its own bound, which gives room as **Budget suspension** states ("BG-8", decisions 70 and 76). An SO-5a rule of 0142 with `when.spendAbove` holds an extension that crosses its amount, of a cap or of a released session, as [`0142-security-oversight`, "SO-5a"](../0142-security-oversight/change.md#so-5a-tool-use-approvals) states (decisions 46, 55, and 70). A shared budget has no extension: a refusal by a shared budget is final until an administrator raises or adjusts the limit, resets the budget, or its period ends (decision 20).

**Errors [design].** Every budget refusal is `429 BUDGET_EXHAUSTED` with `details.budget` (its scope and name, or `session`), `details.limit` (the limit's key), `details.meter`, `details.partition` when the caller may read the budget, and `details.resetsAt` when the budget has a schedule. A caller that may not read the budget receives `details.budget` as `shared` and no other detail of it. `STORAGE_QUOTA_EXCEEDED` and `details.limitType` of 0133 retire in favour of these details (decision 30). A cap that is no budget, such as 0147's `maxAssetUploadBytes` on a tenant's stored asset uploads, answers `429 ASSET_UPLOAD_LIMIT_EXCEEDED` (decision 44, and 0147's decision 57). A partition that a budget's `maxPartitions` keeps from opening answers `429 BUDGET_PARTITIONS_FULL` (decision 56).

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
```

A new period starts each limit at its amount, whatever the last period left unused (decision 11). A capacity limit ignores the period.

- **Boundaries.** The gateway computes each boundary from the schedule and the time zone. A local time that a daylight-saving change skips moves to the next instant that exists, and a local time that occurs twice takes its first occurrence. A cron schedule whose boundaries fall closer than `budgets.minPeriodSeconds` (default 3600) is refused at write.
- **Adjustments.** Whoever may change a budget, an administrator or the creator of a principal budget, raises or lowers its limits for chosen periods with an adjustment, without changing the amounts of the other periods (decision 12). An adjustment names the budget in its route, `limits`, a map from limit keys to signed amounts, each in the unit of its limit, a window `from` and `until`, an optional `partition`, and a `reason` (decision 18):

  ```json
  POST /v1/admin/budgets/daily-per-user/adjustments
  { "partition": "user:bob",
    "from": "2026-11-24T00:00:00+01:00", "until": "2026-11-27T00:00:00+01:00",
    "limits": { "models": 50.00, "pods": 10 },
    "reason": "load tests" }
  ```

  The gateway refuses a key the budget does not hold with `400 VALIDATION_ERROR`. Without `partition`, an adjustment applies to every partition of a budget with `per`, each by the full amount, and to the single set of counters of a budget without `per`. It applies to every period whose start falls in the window, and to the current period when `from` is at or before its start. A limit's amount in a period is its amount plus every adjustment of its key that applies to the period, and never less than zero. A capacity limit takes the adjustment for as long as the window lasts. An adjustment of a currency limit raises the combined amount of every meter it counts. `POST /v1/admin/budget-adjustments` applies a list of adjustments to several budgets, all or none. Whoever may change a budget may adjust it, up to `budgets.maxAdjustmentsPerBudget` adjustments whose window has not ended ("BG-1"). An adjustment whose first period has not started may be deleted, and one in progress may be ended at the next boundary. Thresholds and exhaustion read the adjusted amount. A session budget has no period, and its owner changes it with `PATCH` ("BG-2").
- **Period keys.** A period is named by its start instant, which every replica computes from the schedule, so a charge after a boundary lands in the new period without coordination.
- **On demand.** `POST /v1/admin/budgets/{name}/reset`, or `POST /v1/budgets/{name}/reset` for a principal budget, closes the current period at once and starts a new one for every limit, for every partition or for the one the request names. The request carries a `reason`. The schedule continues at its next boundary, so the period after an on-demand reset is shorter. A reset frees the places of the partitions it closes ("BG-1", **Partitions**). Whoever may change the budget may reset it, at most once in `budgets.minResetIntervalSeconds` for a budget and partition ("BG-1"). A session budget has no reset.
- **The period of a charge.** A charge counts in the period in which it settles (decision 10). A replica keeps its in-memory sums per period, so a flush after a boundary adds each sum to the period of the charges in it, and a charge that straddles a boundary counts in the new period. A session budget has no period, and a shared budget holds no reservation, so no reservation crosses a reset (decision 24).
- **Records.** Closing a period writes a `budget_periods` row with the period's bounds, the adjustments that applied, its consumption by limit and by meter, the estimate of each currency limit, and the trigger (`schedule` or `api`, with the actor), and `budget.reset` names the closed period.

### BG-8 Tree counters and caps

The owner settled the carve in decision 12 of 0166 on 2026-11-23 and replaced it on 2026-11-24 with a count of the whole tree at its root and caps that set nothing aside (decision 70). This change owns the mechanics, and 0166 keeps DPR-4 and the operations that set a cap (0166 decisions 35 and 57) **[design]**:

- **Counting at the root.** The session budget of a tree's root counts the charges of every session of the tree on every limit, `storageBytes` included: one counter per limit key at the root, which every session of the tree charges, so the tree's consumption stays within the root's amount, past which BGR-9 bounds the overshoot. A delegated child holds no session budget of its own. A session budget is held by a root, by a released session, and, under `gateway.delegation.sessionBudget: optional`, by a child whose tree has none, which takes the session budget its own defaults and ceilings give it ("BG-2", decision 39), and each counts its own subtree as a root's counts its tree. Every session keeps counters of its own consumption for its usage report and `session.get`, and only the session budget of its tree and the caps on its path refuse. Shared budgets count each session's charges as they count any session's.
- **Caps.** Each delegated child holds a cap for every limit key of its tree's session budget. A cap bounds the consumption of the child's whole subtree on that key, counted by a subtree counter at the child, and, on a capacity key, the bytes the subtree holds. A charge is admitted only when it fits the root's limit and the cap of every capped session on its path from the charging session to the root, a path that `maxDepth` bounds. A cap sets nothing aside: the caps of siblings may add up to more than their parent's bound or the root's amount, and the root's counter bounds their total, so siblings draw on what the tree has left in the order they spend it. A cap takes the `onExhausted` action that the root's limit of the same key resolved to ("BG-2").
- **Setting a cap.** `session.create` of 0166 carries `limits.budget`, a map from the keys of the tree's session budget to amounts. Each amount is at most the parent's bound for that key, which is the parent's own cap, or the root's amount for a child of the root. A larger amount, or a key that the tree's session budget does not hold, is refused with `400 VALIDATION_ERROR` naming the key. A key that `limits.budget` does not name takes `defaultDelegationFraction` (0.5 by default) of what the parent's bound has left, and `defaultDelegationFraction` is settable per key on the runtime, the environment, and the tenant ("BG-2"). Every child therefore holds a cap on every key, capacity keys included, and a parent that wants a child to draw on its whole bound names that amount. A parent whose tree has no session budget sets no cap, and under the default of `gateway.delegation.sessionBudget` such a parent cannot create a child. An SO-5a rule of 0142 with `when.spendAbove` holds the creation for approval before the gateway creates the child, on the caps that SO-5a states it reads (decisions 46, 55, and 70).
- **Changing a cap.** `session.set_limits` of 0166 raises or lowers a child's cap by key, within the parent's bound. A cap may be lowered below what the child's subtree has already consumed or holds, which refuses the subtree's further charges on that key until the cap is raised. A raise that crosses an SO-5a rule's `when.spendAbove` waits for that rule's approval before the gateway applies it. The root's client changes the cap of any descendant of its root with `PATCH /v1/sessions/{id}/budget`, within the bound of the descendant's parent, and it alone changes the caps of a transferred session (decision 52).
- **End of a child.** Nothing returns when a child ends, because nothing was set aside: what its subtree consumed stays counted in the root's counter and in the subtree counter of each capped ancestor.
- **Transfer.** Under decisions 32 and 33 of 0166, `session.transfer` keeps a session in its tree with the root's client as its client, so every meter of the session and of its subtree keeps counting in the root's counter, and a transfer is never refused for budget. What the subtree consumed before the transfer stays counted in the subtree counter of each former ancestor, as at a child's end, so a capped session cannot spend its cap again by transferring a child. The bytes the subtree holds leave the subtree counter of each former ancestor below the root, because those ancestors no longer hold them, and stay in the root's counter. The session keeps its caps and its own subtree counters, its parent's bound is the root's amount from then on, and its former parent has no operation on it (decisions 70 and 74).
- **Release.** Under decisions 32 and 33 of 0166, `session.release` makes a child a root outside its tree with a session budget of its own. On each key other than a capacity key, that budget holds what the session could still spend at the release: the least of what its own cap, the cap of each capped ancestor, and the root's limit had left. The old tree records that amount as consumed by the release, in the root's counter and in each former ancestor's subtree counter, and keeps what the session's subtree consumed before the release, so a release takes out of the tree no more than the session could have spent in it. On a capacity key, the subtree's held bytes leave the old tree's counters, and the released session's cap on the key becomes the capacity limit of its session budget, which counts them, so a released session always holds a capacity limit when its tree held one (decisions 69 and 75). The subtree's caps stay, bounded by the released session's budget. An SO-5a rule with `when.spendAbove` holds a release, and an extension of the released session, whose amount crosses it (0142 decision 47). Shared budgets are unaffected, and the released session's later charges reach them as before, because it still matches them through its inherited attributes and references.
- **Late charges.** A charge counts in the tree the session belongs to when the charge arrives, whatever its `occurredAt`: after a transfer it counts on the session's new path, and after a release in the released session's own budget (decision 70, which amends decision 65).
- **Leaves.** A transfer and a release move counters as "BG-9" **A leave.** states, with a durable record that the rebuild reads.
- **Cost.** A cap holds nothing, so a long-lived idle child keeps no amount from its parent or its siblings. Siblings compete for what the tree has left, so a child can be refused below its cap when the rest of the tree has spent the root's amount ("Limits").

### BG-9 Counters, failure, and precision

**Redis [design].** Each budget partition and period has, under its own hash tag, `{bdg:<budgetId>:<partition>}`, a `consumed` field for each limit and, for a session budget, a `reserved` field for each limit and a reservation index ordered by expiry. A shared budget's hash also holds each replica's flush watermark. A session budget's hash also holds, for each capped session of its tree, the session's cap and its subtree's `consumed` and `reserved` fields for each key, so the root's limits and every cap on a session's path share one hash tag, and a reservation in them is one script that checks the path, at most `maxDepth` caps long (decisions 18 and 70). A call is therefore one Redis step on admission, and `maxTreeSize` bounds the sessions that charge one hash. A transfer rewrites the held bytes in the subtree counters of the moved session's former ancestors in the same hash, and a release moves the released subtree's counters under the hash tag of the released session's budget and records the release's consumption in the old tree's hash, in the order **A leave.** below states ("BG-8"). A shared budget receives one script per replica per flush interval, whatever the rate of charges, so a tenant's budget that every charge reaches is no hot key, and no reservation crosses hash slots (decision 24).

**Postgres [design].** The `budgets` table holds definitions, the `budget_periods` table holds closed periods, the `budget_adjustments` table holds adjustments and the `budget_leaves` table holds transfers and releases, both of which a counter rebuild reads, the `budget_checkpoints` table holds each open period's counters every `quotaSyncIntervalSeconds`, the `price_list_versions` table holds the price lists, the `external_charges` table holds accepted charges ("BG-12" **Durability.**), and the `charge_reporters` table holds the registered reporters with their scope, hosts, and credential digest. The session row carries its budget set and its references.

**Failure [design].**

- **A replica crash.** A reservation in a session budget whose replica stopped expires, and the recovery sweep settles it from its meter's durable record: a compute tick at the binding's `closed_at` in `session_pod_windows`, or at the tick's end when the binding is still open on a live pod; a storage reservation at the confirmed size in the ledger, or at zero when no row exists. Every durable record names the replica that settled it and the time it settled. An accepted charge on `externalCost` is its own durable record in `external_charges`, so the sweep reads it as it reads the other records. When a replica's gateway lease lapses, the recovery sweep adds to each shared budget's counter the charges of that replica's records settled after the counter's watermark for the replica, and the charges of the reservations the sweep recovers, and advances the watermarks to the last charge it added. A charge counts once, because the flush script and the recovery each advance the watermark in the script that adds the charges. The sweep also writes a fence for the lapsed lease. The flush script receives the replica's lease epoch and refuses a flush whose epoch is fenced, so a flush sent before the replica learns of the fence adds nothing. A replica that returns after its lease lapsed, such as one cut off by a network partition, reads the fence, has the sweep run again for its old lease up to the fence, so the charges it settled after the first sweep are counted from their durable records, and then drops its unflushed sums, rebuilds its cached state, and admits work only under a new lease. The sweep reads durable records in the order of their commit sequence, so a record that commits after a sweep with an earlier settle time is counted by the next sweep.
- **A Redis loss.** On recovery, each counter is rebuilt as the maximum of its last checkpoint and the consumption that the durable records hold for the open period, computed lazily per budget, for every limit, on its first charge, which refuses until the rebuild ends. During the outage, the gateway enforces session budgets in memory, admits work in shared budgets within each replica's fail-open share and turns to refusing after `quotaFailOpenCumulativeMaxSeconds` ("Best effort" in "BG-6", decision 35), and flushes the sums and applies the MAX rule on recovery.
- **A leave.** A transfer and a release each write a `budget_leaves` row in the transaction that records the leave, with the leave's identifier, the session, the old and the new root, and, per key, the amount a release records as consumed and the bytes the subtree holds. Before it moves counters, the gateway settles the subtree's open reservations in the old hash, or fences them so that the recovery sweep settles them there from their durable records, and it admits no new reservation for the subtree until the move ends. A transfer then rewrites the old hash in one script. A release debits the old hash first, in one script that records the leave's identifier, and then writes the released session's hash in a second script that is idempotent on the leave's identifier, so a retried step counts nothing twice. The leader sweep finishes a leave whose row exists and whose second step has not run, and the rebuild after a Redis loss reads `budget_leaves` with the other durable records, so it restores a release's consumption in the old tree, the released budget's counters, and the bytes a transfer moved.
- **No key expiry.** A session budget's keys carry no TTL. They are created with the session and deleted by the leader sweep once every session of its tree has reached a terminal state, their reservations have settled, and 900 seconds, the longest lifetime of an attribution assertion under 0124 CR-2, plus `budgets.external.maxReportDelaySeconds` have passed, so a late charge of decision 65 that cites an assertion minted just before the end still finds them, and a shared budget's keys are deleted when its period closes and its record is written. `delegation.budgetKeyTTLSeconds`, the TTL re-arming of §8.3, and `BUDGET_KEYS_EXPIRED` retire, and 0166 "DP-6" lists them (decision 43).
- **A reset during an outage.** A boundary that passes while Redis is unavailable is applied at recovery from the schedule, and the closed period's record is written then.

**Precision [design].**

| Meter | Step | Overshoot of a session budget or cap | Overshoot of a shared budget |
| :-- | :-- | :-- | :-- |
| `externalCost` | One micro-unit of `budgets.currency`, per charge. | The charges of the calls the gateway admitted before the attribution of the sessions under the exhausted limit was suspended, plus the charges the session's owner posts, plus fail-open drift. | The charges that every replica accepts in one flush interval, plus the calls in flight when the suspension takes hold. |
| `computeCost`, `computeUnits` | One micro-unit of the currency or one milli-compute-unit-second, per tick. | One tick of compute units, plus a suspension's hold of `budgets.suspendPodHoldSeconds`. | The ticks that every replica settles in one flush interval, plus one tick per session holding a pod in the budget, plus a suspension's hold per suspended session. |
| `storageBytes` | One byte, per write. | The outstanding checkpoint grant window of §11.2, plus one checkpoint per pod release by a suspension's hold or a move to `dormant` (decisions 25, 28, and 42). | The writes that the other replicas admit in one flush interval, once (decision 53), plus one checkpoint per pod release by a suspension's hold or a move to `dormant`. |
| `storageCost` | One micro-unit of the currency, rounded up per accrual interval. | A session budget stops counting at the session's end (decision 21). | Stored data accrues cost while it is held, so a limit's consumption keeps rising after exhaustion until data is deleted. |
| A currency limit on several meters | One micro-unit of the currency, rounded up per charge. | The sum of the overshoots of the meters it counts. | The sum of the overshoots of the meters it counts. |

### BG-10 API, RBAC, audit, metrics, and usage

**Routes [design].**

| Route | Purpose |
| :-- | :-- |
| `POST`, `GET /v1/admin/budgets` | Create and list platform and tenant budgets. A `platform-admin` passes `?tenant=` or `scope: platform`. Every admin write accepts `?dryRun=true` under §15.1. |
| `GET`, `PUT`, `DELETE /v1/admin/budgets/{name}` | Read, replace under `If-Match`, and delete. A shared budget holds no reservation, and each replica drops its sums for a deleted budget at its next refresh. |
| `POST /v1/admin/budgets/{name}/reset` | Reset on demand ("BG-7"). |
| `POST`, `GET /v1/admin/budgets/{name}/adjustments`, `DELETE /v1/admin/budgets/{name}/adjustments/{id}`, and `POST /v1/admin/budget-adjustments` | Adjustments, to one budget or to several at once ("BG-7"). |
| `GET /v1/admin/budgets/{name}/usage` | The current period's consumption, reservations, amount, and money estimate of each limit, by meter, per partition with paging. |
| `GET /v1/admin/budgets/{name}/periods` | Closed periods. |
| `/v1/budgets`, with the same sub-routes | Principal budgets of the caller. |
| `GET`, `PATCH /v1/sessions/{id}/budget` | The session budget, or a delegated child's caps ("BG-2", "BG-8"). |
| `GET /v1/sessions/{id}/budgets` | The session's budget set, each limit with its state (`ok`, `low`, or `exhausted`), and the name and amounts of each budget the caller may read. A budget the caller may not read appears as `shared`, with its limits' states and no name. |
| `PUT`, `GET /v1/admin/tenants/{tenant}/session-budget` | A tenant's `defaults`, `ceilings`, and `defaultDelegationFraction` for session budgets ("BG-2"). |
| `POST`, `GET /v1/admin/price-lists/platform/versions` | The platform price list's versions, `platform-admin` alone ("BG-5"). |
| `POST`, `GET /v1/admin/price-lists/tenants/{tenantId}/versions` | A tenant price list's versions, `platform-admin` alone. |
| `POST`, `GET`, and `DELETE /v1/admin/charge-reporters[/{name}]` | Charge reporters: `platform-admin` for platform reporters and any tenant's, `tenant-admin` for its own tenant's ("BG-12"). |
| `POST /v1/charges` | A batch of charges from a registered reporter ("BG-12"). |
| `POST /v1/sessions/{id}/charges` | A batch of charges that the session's owner posts for the session ("BG-12"). |
| `POST /v1/admin/charges/{reporter}/{id}/void` | The void of a charge, with a reason ("BG-12"). |
| `GET /v1/admin/price-lists/effective` | The caller's tenant's effective prices, with the version of each entry. A `platform-admin` passes `?tenant=`. |
| `GET /v1/usage?budget=` | The usage report filtered to one budget's subjects. |

**RBAC [design].** The §10.2 permission matrix gains these rows:

| Operation | `platform-admin` | `tenant-admin` | `tenant-viewer` | `billing-viewer` | `user` |
| :-- | :-- | :-- | :-- | :-- | :-- |
| Manage budgets | Yes (all tenants, and platform) | Yes (own tenant) | Read-only (own tenant) | Read-only (own tenant) | Own principal budgets, and the state of each budget in its own sessions' sets |
| Manage price lists | Yes (platform, and every tenant price list) | Its tenant's effective prices | Its tenant's effective prices | Its tenant's effective prices | No |
| Set budget caps | Yes (on any tenant's record) | Lower the principal cap for its own tenant | No | No | No |
| Read session attributes | Yes | Yes (own tenant) | No | Grantable (own tenant) | No |

A service account holds the `user` row's rights over its own principal budgets. `POST /v1/sessions/{id}/charges` acts on a session, so the permission matrix checks it against "Create / cancel own sessions" and [§10.2](../../spec/10_gateway-internals.md#102-authentication) **Session ownership.** admits the session's owner alone, with no row of its own. A principal that carries a session claim, which the gateway gives a runtime's platform tool calls, is refused on `PATCH /v1/sessions/{id}/budget`, `POST /v1/sessions/{id}/charges`, and the principal budget routes under `/v1/budgets`, by the predicate of SLR-22 of [`0146-session-lifecycle`](../0146-session-lifecycle/change.md#sl-4-tree-suspension) ("Who suspends and who lifts"), even when its subject owns the session, so a runtime cannot raise its tree's budget, change a cap, post a charge, or spend its owner's principal budgets. "Manage quotas" keeps concurrency and rate limits. "Read session attributes" returns a session's recorded directory groups, its labels, and its budget set through `GET /v1/sessions/{id}` and `GET /v1/sessions/{id}/budgets`, and nothing else. A `tenant-admin` grants it to `billing-viewer` or to a custom role of its tenant, so a deployer's cost service holds it without an administrator's rights (decision 40).

**Audit events [design].** [§11.7](../../spec/11_policy-and-controls.md#117-audit-logging) gains `budget.created`, `budget.updated` with the fields changed, `budget.deleted`, `budget.reset` with the trigger, the actor, and the closed period's summary, `budget.adjusted` with the actor, the reason, the amount of each limit key, the window, and the partition, `budget.partitions_full` once per budget, tenant, and period, `budget.attachment_skipped`, `budget.suspended` and `budget.resumed` with the session, the limit, and the trigger, `budget.threshold_crossed`, `budget.exhausted` once per limit, partition, and period, `session_budget.changed` for a `PATCH`, an extension, a cap set or changed, a transfer, or a release, and `price_list.version_added` with the list it extends.

**Metrics [design].** A budget name and a limit key are not metric labels, because tenants create budgets without bound, and per-budget figures come from the usage route. A metric that names what was counted carries `meter`. The metrics are `lenny_budget_reservations_total` by `meter` and `outcome`, `lenny_budget_refusals_total` by `tenant_id`, `meter`, `scope`, and `action`, `lenny_budget_reservation_duration_seconds` by `meter`, `lenny_budget_threshold_crossings_total` by `tenant_id` and `meter`, `lenny_budget_resets_total` by `trigger`, `lenny_budget_rebuilds_total` by `outcome`, `lenny_budget_compute_tick_lag_seconds`, `lenny_budget_storage_accrual_lag_seconds`, `lenny_budget_flush_lag_seconds` per replica, the time since the replica's last completed flush, and `lenny_budget_flush_failures_total` by `outcome`. The alerts are `BudgetRebuildFailing`, `BudgetAccrualLagging` when the accrual lag passes twice its interval, `BudgetFlushLagging` when a replica's flush lag passes three times `budgets.shared.flushSeconds`, and `BudgetPriceListGap` when a limit on `computeCost` or `storageCost` has no price in force.

**Usage reports [design].** The usage route of a budget answers from its counters for the open period and from `budget_periods` for closed ones, with each limit's consumption by meter, so a team sees its model, compute, and storage spend apart. `GET /v1/usage?budget=` aggregates the usage records of the budget's subjects in a window, which reproduces a budget's consumption from records for a budget with a selector, and returns `400 VALIDATION_ERROR` for a principal budget, whose subjects are listed by session. On `externalCost` it counts only the charges whose source the budget counts ("BG-12" **Counting by source.**), so a tenant or platform budget's figures leave out the owner's direct charges, and a platform budget's leave out a tenant reporter's charges, which the session's own usage still shows.

**What a runtime sees [design].** A runtime meets budgets as refusals: a refused call from its LLM gateway once its attribution is suspended, `BUDGET_EXHAUSTED` from `session.create`, and from a file export, `SESSION_BUDGET_REQUIRED` from `session.create` under 0166's setting, and the stopped turn of a session that a budget suspends or ends under `onExhausted.running`. A runtime has no operation that reads a budget (decision 31). A runtime that meets a limit a gateway or service enforces reports it with the `external_limit` frame of 0139 "RP-9", which suspends its own session (decision 68). A session budget is optional, so such a read would answer for some sessions and not others, and an orchestrator that caps a child relies on `defaultDelegationFraction` or on the amount its client names.

### BG-11 What this change supersedes

| Change | Parts superseded [design] | Parts kept |
| :-- | :-- | :-- |
| [0133](../0133-llm-upstreams-and-cost-budgets/change.md) | The scopes, the reservation, and the credit budgets of "LU-6" of 0133's revisions 1 to 19, `maxCreditBudget`, and the `quotas.llmCredits` settings, which 0133's decision 22 removes with the proxy. | Nothing: 0133 holds no budget mechanism after its decision 22. |
| [`0166-delegation-primitives`](../0166-delegation-primitives/change.md) | None left to supersede: revision 8 cites this change for budget objects and the carve, which decision 70 replaces with caps ("DP-3", and the 0170 row of "DP-7"). This change states in full what that row lists: the count of a tree at its root's session budget and a cap on every key of every child (decision 70), `limits.budget` on `session.create` with `defaultDelegationFraction` per limit key, the budget part of `session.set_limits`, `session.transfer`, and `session.release`, and lease extension of caps ("BG-6", "BG-8"). The `maxCreditBudget` among 0166's root grant inputs, in its examples, and in a client's `delegationLease` becomes `sessionBudgetCeilings` and `budget` by limit key ("BG-2"), and SO-5a's `creditBudgetAbove` of 0142 becomes `spendAbove` (decisions 16 and 57). | DPR-4, the operations that set a cap, the resolution order of "DP-1", which "BG-2" follows, and every other resource caveat with its ceilings. |
| [0121](../0121-billing-resource-attribution/change.md) | None superseded. This change adds the optional `computeUnitsPerSecond` pin to an entry of the `resourceClasses` map, and the compute rate resolved at the bind as a column of `session_pod_windows` ("BG-4"). | Every part. This change reads 0121's records as the durable source of compute and storage charges. |
| [0131](../0131-incremental-checkpoint/change.md#ic-4-catalog-reference-closure-and-quota) | "Quota" in IC-4, its `reserved_bytes` and headroom check, `quota_exceeded`, and R4's statement that the tenant's storage quota bounds workspace size: a chunk grant is a write declared by its size to every limit on a storage meter in the owning session's set, under "BG-6". | Every other part. |
| [0144](../0144-session-derive/change.md) | The reservation of a derive's upload copies against the tenant's storage quota, and `STORAGE_QUOTA_EXCEEDED`: each copy is a write declared by its size to the limits on the storage meters in the new session's budget set, refused with `BUDGET_EXHAUSTED`. A derive's request carries its own `labels`, `budget`, and `budgetRefs` (0144 decision 14). | Every other part. |
| [0135](../0135-message-delivery/change.md#md-18-files) | The reservation of an output file's export bytes against the tenant's storage quota in "MD-18", and `quota_exceeded`: the export is a write declared by its size to the limits on the storage meters in the owning session's budget set, refused with `BUDGET_EXHAUSTED` (decision 53). | Every other part. |
| [0153](../0153-workspace-materialization/change.md) | The inbound body cap of `remaining_quota_bytes`, read from the storage quota: an upload is a write declared by its length to the limits on the storage meters in the session's budget set. | Every other part. |
| [0147](../0147-tenant-derived-runtimes/change.md) | `429 STORAGE_QUOTA_EXCEEDED` for an upload past `maxAssetUploadBytes`, which becomes `429 ASSET_UPLOAD_LIMIT_EXCEEDED`, with `413 UPLOAD_TOO_LARGE` for an upload larger than the cap by itself, because the cap is no budget (decision 44, and 0147's decision 57). | The cap and every other part. |
| [0128](../0128-forensics-evidence-preservation/change.md) | The exclusion of evidence from `storageQuotaBytes`, which becomes the exclusion of evidence from every limit on a storage meter. | Every other part. |
| [0146](../0146-session-lifecycle/change.md) | None superseded. It gains `suspension_cause` and the budget suspension of "BG-6" in SL-1, SL-2, SL-4, SL-6, and SL-10, and SL-10's budget keys follow "BG-9" (decisions 42 and 43). | Every other part. |
| [0141](../0141-runtime-routing/change.md#rt-2-the-interceptor-contract) | None superseded. RT-2 gains `budget_refs` and `session_budget` in the `PreRoute` payload and the add-only check at the post-chain point ("BG-3"). | Every other part. |
| [0149](../0149-tenant-scoping-identity/change.md#label-rules-ts-10) | None superseded. It owns the label rules that "BG-3" reads (decision 41). | Every other part. |

0133 revision 12 replaces the scopes of "LU-6" with this change's budget set and drops `maxCreditBudget` (decision 9), and 0133 revision 13 names the limits that count `llm` in `llmCredits` (decision 16). 0166 revision 8 cites this change for the carve, and its revision 10 states `limits.budget` by limit key. 0142 revision 64 renames `creditBudgetAbove` to `llmCreditsAbove`, and its decision 48 renames it `spendAbove` under decision 57. 0133 revision 20 removes the proxy, and decision 57 replaces the `llm` meter with `external`, which decision 67 renames `externalCost`. Every decision is settled, and the edits this table lists are owed now: the reviews of 2026-11-23 made the edits to 0131, 0135, 0144, 0147, 0128, 0146, 0141, 0149, and 0153, and the edits to 0121 land with 0170.S3, which adds the pin and the column.

### BG-12 External charges

**The meter [design].** The `externalCost` meter counts the charges that registered reporters send for sessions, and those that a session's owner posts for its own sessions, in the deployment's currency (decisions 57 and 67). A charge names one session, so decision 29 holds. Lenny attaches no meaning to what a charge measures: a model call that an operator's LLM gateway prices, or any other paid service that a deployer wants in its budgets. A limit that counts `externalCost` reserves nothing ahead: a charge counts in the session budget of the session's tree and the caps on its path when it is accepted, and in shared budgets through the flush ("BG-6"). A cap on `externalCost` sets nothing aside, as for any meter ("BG-8").

**Reporters [design].** A reporter is a registered client of `POST /v1/charges` (decision 59):

- A tenant reporter, which a `tenant-admin` or a `platform-admin` registers, charges sessions of its tenant alone. A charge that names a session of another tenant is refused as not found.
- A platform reporter, which a `platform-admin` registers, charges sessions of any tenant, or of the tenants it lists. It serves an LLM gateway that the operator runs for every tenant.
- Each reporter names its `hosts`, the hosts whose assertions it may cite: the hosts of the attribution bindings of 0133 "LU-2", or of identity-assertion bindings of 0124 CR-2 for another service. A tenant reporter's hosts are bound to its tenant, as [`0124-connector-removal`, "CR-2 Identity assertion"](../0124-connector-removal/change.md#cr-2-identity-assertion) **Hosts bound to one tenant (D20, D25).** states: a write that names a platform host is refused, and one that names another tenant's host is refused under the `deployment` key set and accepted under `perTenant`, where each tenant's assertions carry its own key, each refusal with `400 VALIDATION_ERROR` naming the host. The checks below keep a reporter on a shared host to its own tenant's sessions.
- Its credential is a client secret or a signed-JWT bearer issued at registration. Routes: `POST`, `GET`, and `DELETE /v1/admin/charge-reporters[/{name}]`.

**Counting by source [design].** A charge counts only in the budgets whose spending its sender answers for (decision 71):

- A charge from a platform reporter counts in every budget of the session's set.
- A charge from a tenant reporter counts in every budget of the session's set except platform budgets.
- A charge that the session's owner posts counts in the session budget of the session's tree, the caps on its path, and the owner's own principal budgets, and in no tenant or platform budget.

A platform budget therefore counts `externalCost` only from the reporters that a `platform-admin` registers as platform reporters, whatever its `per` or `tenants`, so no `tenant-admin` and no user moves it with amounts it reports. A tenant budget counts its own reporters' charges and the platform reporters', and no user moves it with a direct charge. Every charge is an `external_charges` row with its source, and usage reports show it whichever budgets it counts in.

**Ingestion [design].** `POST /v1/charges` takes a batch. Each charge cites as its proof the `jti` of the assertion that the reporter received with the call it prices (decision 62):

```json
{ "charges": [
    { "id": "gw-7f3a91", "proof": { "jti": "8f9c2d4e-6b1a-4c3e-9d7f-2a5b8c1e0f34" },
      "amount": "0.0423", "currency": "EUR",
      "occurredAt": "2026-11-24T10:15:02Z", "category": "llm",
      "usage": { "model": "claude-sonnet-5-5", "inputTokens": 2000, "outputTokens": 1500,
                 "cacheReadTokens": 110000, "cacheWriteTokens": 6000 } },
    { "id": "gw-7f3a92", "proof": { "jti": "8f9c2d4e-6b1a-4c3e-9d7f-2a5b8c1e0f34" },
      "amount": "0.0310", "occurredAt": "2026-11-24T10:15:04Z" },
    { "id": "gw-7f3a93", "proof": { "jti": "2b417a06-93d1-4f6e-8c52-0d1e7b9a4c11" },
      "amount": "0.0007", "occurredAt": "2026-11-24T10:15:04Z" } ] }
```

- **Batches.** A request holds at most `budgets.external.maxChargesPerRequest` (default 1000) charges and 1 MiB. A larger request is refused with `413 CHARGE_BATCH_TOO_LARGE`. The documentation recommends a flush every second or every 500 charges, whichever comes first, and states that a longer interval adds to the overshoot of BGR-9. A charge prices one call or one use. A reporter does not add charges together per session or per window, because a resent sum could count twice and the usage of each call would be lost.
- **Proofs.** A proof names the `jti` of an attribution assertion, or of an identity assertion for another service. An attribution assertion reaches a reporter only on a request from the pod, to which `netagent` attaches it, because only `netagent` obtains one (0124's D24). Lenny looks it up in the Token Service's mint index ([`0124-connector-removal`, "CR-2 Identity assertion"](../0124-connector-removal/change.md#cr-2-identity-assertion) **Mint index (D18).**), which holds each mint's tenant, session, root session, binding, audience, `kid`, and validity for `budgets.external.maxReportDelaySeconds` after its `exp`, and takes the session from the entry. A batch carries no assertion, so no credential travels into a gateway's logs. A `jti` minted under a key that an emergency rotation retired is refused with `CHARGE_PROOF_KEY_REVOKED` and the audit event `charge.proof_key_revoked`, because a leaked key could have signed forged assertions that reused the `jti`.
- **Checks on each charge.** The `jti` is in the mint index; its audience is one of the reporter's `hosts`; its tenant is within the reporter's scope; `occurredAt`, the start of the call the charge prices, lies within the assertion's validity window, plus 60 seconds of leeway; the charge arrives within `budgets.external.maxReportDelaySeconds` (default 86400, decision 60) of `occurredAt`; `currency`, which a charge may omit, equals `budgets.currency` when it is present; the amount is a positive decimal string of at most 1,000,000,000 with at most six decimal places, so no counter can overflow, and at most `budgets.external.maxChargeAmount` when it is set; `id` and `category` are at most 128 bytes each, and `usage` at most 4 KiB. A charge that fails a check is refused with `VALIDATION_ERROR` naming the field.
- **Results.** Each charge answers `accepted`, `duplicate`, or `refused` with a code. A batch with one bad proof refuses only the charges that cite it.
- **Idempotency.** `(reporter, id)` is unique. A repeated identifier returns the first result and counts nothing, and Lenny keeps the key for `budgets.external.maxReportDelaySeconds` plus one hour, so a partly failed batch is resent whole.
- **Rate.** Each reporter is limited to `budgets.external.maxChargesPerSecondPerReporter` (default 2000) charges per second. A batch beyond it is refused with `429 QUOTA_EXCEEDED`, so batching neither escapes nor trips the limit.
- **Period.** A charge counts in the period in which Lenny accepts it (decision 10).
- **Durability.** Each accepted charge is a row of `external_charges`, written with the other charges of its batch in one insert: tenant, session, root session, `source` (`reporter` or `client`), the reporter or the owner, identifier, the proof's `jti`, amount, category, usage, the receiving replica, the time of acceptance, and the counters its acceptance incremented: the session budget of the session's tree with the cap path at acceptance, and each shared budget with its partition and period. It is the meter's durable record for "BG-9". A row is kept until the last period it counted in has closed, or its tree has ended for a charge that counted in no shared budget, plus `billing.retentionDays` of [§11.2.1 Billing Event Stream](../../spec/11_policy-and-controls.md#1121-billing-event-stream) (default 395 days), so it outlives the budget figures it explains for as long as billing events do (decision 81). No billing event carries a charge: charges reach a reader through usage reports and these rows (decision 78).
- **Voids.** A `platform-admin`, or a `tenant-admin` for its own tenant's reporters, voids a charge with a reason. A void reverses exactly the counters that the charge's row records: it subtracts the amount from the session budget of the tree and the caps on the path at acceptance, and from each shared budget's partition in the period the charge counted in. A void of a charge whose period has closed corrects that period's `budget_periods` record and gives no room in the open period. A void is the audit event `charge.voided`, and one that gives a limit room lifts the budget suspensions of that limit, as **Budget suspension** in "BG-6" states (decision 72). A charge that the session's owner posted has no void, because it counts only in budgets that the owner raises itself (**Direct charges by the owner.**).
- **Exhaustion.** When an accepted charge carries a limit that counts `externalCost` past its amount, in the session budget of the session's tree or a cap on its path, the gateway suspends the attribution bindings of every session under the session that holds the limit, the whole tree for the root's limit and the capped session's subtree for a cap ([`0127-egress-and-git`, "Credential bindings"](../0127-egress-and-git/change.md#credential-bindings) **Binding suspension.**), and applies `onExhausted` ("BG-6"), whose `suspend` takes the holder as the suspension root (decision 73). When a replica's cached state turns a shared limit that counts `externalCost` to exhausted, the replica suspends the attribution bindings of its sessions whose set holds the limit, and applies `onExhausted.running` as "Budget suspension" in "BG-6" states (decision 83). A grant, an adjustment, a raise, a void, or a new period that restores room lifts the suspension.
- **Reporting rules.** The gateway guide of 0133 states the rules a reporter follows: `occurredAt` is the start of the call, because a streamed call can end after its assertion expires; a reporter flushes every second or every 500 charges; amounts are in `budgets.currency`, so a reporter whose gateway prices in another currency, as LiteLLM prices in USD **[external]**, converts, or the operator sets `budgets.currency` to that currency; and the gateway alerts on a model it cannot price, because LiteLLM prices a model missing from its cost map at zero **[external]** (decision 62).
- **Usage.** The optional `usage` member carries a call's model and token counts in a neutral schema. [`0135-message-delivery`](../0135-message-delivery/change.md#md-20-turn-and-usage-events) "MD-20" shows it as `source: external`, and no budget reads it.
- **Metrics and audit.** `lenny_external_charges_total` by `source`, outcome, and code, `lenny_external_charge_batch_size`, and `lenny_external_charge_delay_seconds`; the audit events `charge_reporter.created`, `charge_reporter.deleted`, `charge.voided`, `charge.proof_key_revoked`, `charge.owner_posted` for each batch an owner posts, with the session, the caller, the count, and the total.

**Direct charges by the owner [design].** The session's owner may post charges for its own sessions with `POST /v1/sessions/{id}/charges`, a batch in the form of `POST /v1/charges` whose charges carry no `proof`, because the owner is the authority over its sessions (decisions 61 and 71). It serves a cost incurred outside the pod: the client's own app calls a service, such as the operator's LLM gateway with a key of the client's own, or hands work to one, and the service tells the client what the work cost:

- **Who.** The route acts on the session under [§10.2](../../spec/10_gateway-internals.md#102-authentication) **Session ownership.**: only the session's owner posts, which is the principal whose request created the session, a service account included, and for a delegated child its parent's owner, which is its root's owner. Any other caller receives `404 RESOURCE_NOT_FOUND`. A runtime holds no credential of the client API and has no platform tool for it, so a runtime posts no charge.
- **Checks.** Each charge passes the checks of `POST /v1/charges` that need no proof: `occurredAt` lies within `budgets.external.maxReportDelaySeconds` of its arrival and, for a session that has ended, at most 60 seconds after its end, `currency`, when present, equals `budgets.currency`, and the amount and the fields meet the bounds of **Checks on each charge.** `(owner, id)` is unique, with the idempotency of `POST /v1/charges`, and the batch limits apply.
- **Rate.** An owner posts at most `budgets.external.maxChargesPerSecondPerOwner` (default 100) charges per second across its sessions, and the owners of one tenant together post at most `budgets.external.maxOwnerChargesPerSecondPerTenant` (default 1000), each batch beyond either refused with `429 QUOTA_EXCEEDED`.
- **Counting.** An accepted charge is an `external_charges` row with `source: client`, and it counts on `externalCost` in the session budget of the session's tree, the caps on its path, and the owner's own principal budgets, and in no tenant or platform budget (**Counting by source.**). Each batch is the audit event `charge.owner_posted`.
- **Reach.** An owner's charges consume only budget, so they never widen what a session may do, and they reach only budgets the owner controls, so an owner that posts too much exhausts only its own tree and its own principal budgets, which it raises itself with `PATCH /v1/sessions/{id}/budget` and the principal budget routes (decision 71).
- **Tenant switch.** `budgets.external.ownerCharges` takes `enabled` (the default) or `disabled`. The Helm value of that name sets the deployment's default, and a `tenant-admin` sets it for its tenant on the tenant record. While it is `disabled`, `POST /v1/sessions/{id}/charges` is refused with `422 OWNER_CHARGES_DISABLED`, category `POLICY`, for every session of the tenant (decision 64). The switch leaves reporters' charges unaffected.

**Ended sessions [design].** Charges for a session that has ended are accepted, because a reporter or an owner may report a call or a job after the session ends, within `budgets.external.maxReportDelaySeconds` (86400 seconds, 24 hours, decision 60) of `occurredAt`, under the checks above (decision 65):

- **Minting.** No assertion is minted for an ended session, as 0124's D23 states. An assertion minted before the end stays valid as proof until its `exp`, and its `jti` is accepted within the delay.
- **Counting.** A late charge counts in the shared budgets of the session's set that its source reaches (**Counting by source.**), and in the session budget of the tree that the session belongs to when the charge arrives and the caps on its path ("BG-8" **Late charges.**), so the tree is counted once and the root's session budget still bounds its spending. A session budget or a cap whose sessions have all ended records it as consumption past its amount and refuses nothing, because nothing of them remains to stop. A tree with a session still running takes the charge like any other, and an exhausted limit there acts as **Exhaustion** states.
- **Record.** The check reads the session's record, its budget set, and its session budget's counters, so Lenny keeps them at least `budgets.external.maxReportDelaySeconds` after the session ends. The session row and its budget set stay until a user erasure or a tenant deletion of §12.8 removes them, because the artifact retention TTL of §7.1 and §12.5 governs artifacts alone, and the leader sweep deletes a session budget's Redis keys 900 seconds plus `budgets.external.maxReportDelaySeconds` after its tree ends ("BG-9" **No key expiry.**), so no setting can make them shorter and no chart check is owed. A charge for a session whose record no longer exists, after an erasure or a deletion, is refused with `404 RESOURCE_NOT_FOUND`.

**Limit notices [design].** A gateway or service that enforces its own limits, such as LiteLLM's team budgets, reports through its registered reporter that a session hit one, so Lenny stops the session instead of letting it retry against refusals (decision 68). `POST /v1/charges` accepts, beside `charges`, a `limitNotices` list:

```json
{ "limitNotices": [
    { "id": "gw-n-0192", "proof": { "jti": "8f9c2d4e-6b1a-4c3e-9d7f-2a5b8c1e0f34" },
      "occurredAt": "2026-11-24T10:15:09Z", "status": 429,
      "message": "team acme-payments budget exceeded", "resetsAt": "2026-12-01T00:00:00Z" } ] }
```

- **Checks.** A notice passes the checks of a charge that apply to it: its `jti` is in the mint index, its audience is one of the reporter's `hosts`, its tenant is within the reporter's scope, `occurredAt` lies within the assertion's validity plus 60 seconds and arrives within `budgets.external.maxReportDelaySeconds`, and `(reporter, id)` is unique. A reporter therefore names only sessions whose assertion reached its hosts. Each notice answers `accepted`, `duplicate`, or `refused` with a code, as a charge does.
- **Effect.** An accepted notice suspends the session that the proof names, with its subtree, with `suspension_cause: external_limit`, as [`0146-session-lifecycle`, "SL-4"](../0146-session-lifecycle/change.md#sl-4-tree-suspension) **External limit suspension (L39).** states: the pause, the pod hold, and the release are those of a budget suspension, and no limit gaining room resumes it, so it ends only when the root's client resumes the suspended session by an explicit resume carrying `acknowledgeExternalLimit: true`, and a message from the root's client lifts nothing (0146's L45). A notice suspends the session in any non-terminal state, as L42 of 0146 states, and a notice for a session that has ended is accepted, recorded, and changes nothing (decision 84). `status`, `message`, and `resetsAt` reach the client with the suspension, and a notice that names `retryAfter` and no `resetsAt` takes `resetsAt` as its `occurredAt` plus `retryAfter`. The time is advice: Lenny never acts on it and never refuses a resume before it, and a session left suspended expires at `gateway.maxSuspendedSessionSeconds` (decision 84).
- **Rate and audit.** Notices count against the reporter's `budgets.external.maxChargesPerSecondPerReporter` with its charges. Each accepted notice is the audit event `external_limit.reported` with the reporter, the session, the host, and the detail, and `lenny_external_limit_notices_total` counts notices by `source` and outcome.

A runtime reports the same kind of limit from inside the session with the `external_limit` frame of [`0139-runtime-protocol`, "RP-9"](../0139-runtime-protocol/change.md#rp-9-external-limits), which suspends its own session and that session's subtree, with the same effect. The two signals are a second way to enforce what a gateway or service spends: the service enforces its own limits, and Lenny stops the session that hit one, with no charges reported and no budget on `externalCost`. The gateway guide of 0133 describes both ways ("LU-3").


## Worked examples

Each example states what the operator, the tenant administrator, and the client configure, and what happens. Every example is **[design]**, follows the decisions of `decisions.md`, and uses field names that this change and 0166 propose. The tenant is `acme`, `alice` is its `tenant-admin`, and `bob` is a user.

### Example 1: A team's monthly spend across many sessions

The payments team of `acme` may spend 5,000 EUR a month on everything its agents use.

**Operator.** The operator sets the currency and the compute rates in Helm, and appends a price-list version. The resource classes carry no compute setting, because their rates follow from their requests:

```yaml
budgets:
  currency: EUR
  compute:
    rates: { cpuPerVCPU: 1.0, memoryPerGiB: 0.125 }
    isolationMultipliers: { standard: 1.0, sandboxed: 1.2, microvm: 1.5 }
resourceClasses:
  small:  { requests: { cpu: 500m, memory: 2Gi } }   # 0.75 compute units per second
  medium: { requests: { cpu: "1", memory: 4Gi } }    # 1.5
  large:  { requests: { cpu: "2", memory: 8Gi } }    # 3.0
```

```yaml
# POST /v1/admin/price-lists/platform/versions
version: 1
effectiveFrom: 2026-11-24T00:00:00Z
compute: { perComputeUnitHour: 0.12 }
storage: { perGiBMonth: 0.025 }
```

**Tenant administrator.** `alice` creates a tenant budget that selects the team's directory group:

```http
POST /v1/admin/budgets
{
  "name": "payments-monthly",
  "limits": {
    "models": { "meters": ["externalCost"], "amount": 3000.00 },
    "total":  { "meters": ["externalCost", "computeCost", "storageCost"], "amount": 5000.00 }
  },
  "appliesTo": { "groups": ["payments-eng"] },
  "attach": "selector",
  "period": { "schedule": "calendar", "calendar": { "every": "month", "dayOfMonth": 1, "at": "00:00" },
              "timeZone": "Europe/Madrid" },
  "thresholds": [0.8, 1.0],
  "onExhausted": { "new": "refuse", "running": "suspend" }
}
```

**Client.** `bob`, a member of `payments-eng`, creates sessions as usual. He names no budget.

**What happens.**

1. Each of `bob`'s sessions has `tenant/payments-monthly` in its budget set, because his groups at creation include `payments-eng`.
2. `payments-monthly` is a shared budget, so it is counted afterwards ("BG-6"). Each charge that the team's LLM gateway reports reaches `models` and `total` alike, through each replica's flush every 5 seconds. A session's pod time and stored data reach `total` alone. A session on a `medium` pod with one slot in a `sandboxed` pool spends 60 seconds times 1.5 times 1.2, which is 108 compute units, or 0.0036 EUR, in each tick. A checkpoint pack adds its bytes to the budget's bytes held, and the accrual adds byte-seconds every 5 minutes, converted at 0.025 EUR per GiB-month.
3. At 4,000 EUR, `budget.threshold_crossed` records the 0.8 threshold of `total`, and `dev.lenny.budget_threshold_crossed` reaches `alerts.acme.com` through the webhook subscription that `alice` holds on the operational event stream with her tenant's filter.
4. Had `models` run out first, the flush that carries its counter past 3,000 EUR would mark it exhausted, and because the budget sets `"running": "suspend"`, each replica would suspend the team's `running` and `idle` sessions that it serves, each with the sessions below it, within about two flushes, their attribution bindings suspended with them. An adjustment by `alice` within the 300-second hold would resume them on their pods, after the hold each user would resume their own sessions, and a session of the team that was `dormant` would stay `dormant`, its wake refused until there is room. At 5,000 EUR in `total`, the flush that carries the counter past the amount marks `total` exhausted, and each replica learns it at its next flush, so the team's spend can pass 5,000 EUR by about 5 seconds of its spending plus the calls in flight. New sessions of the group are refused with `429 BUDGET_EXHAUSTED` and `details.resetsAt` of `2026-12-01T00:00:00+01:00`. Each running session's next compute tick is refused, and the session is suspended with the sessions below it, keeping its pod for 300 seconds. When `alice` adds an adjustment to `total` within those 300 seconds, the sessions resume on their pods. Otherwise each checkpoints its workspace and releases its pod, the checkpoint admitted past any storage limit, and an adjustment after the hold resumes none of them: each user resumes their sessions, which resume admission admits once `total` has room (decision 85). Stored data stays.
5. At 00:00 on 1 December in Madrid, 23:00 UTC on 30 November, the period closes. `budget_periods` records the November consumption of each limit by meter, and the sessions suspended in November stay suspended, because their holds ended long before: the new period resumes only sessions still within their hold, and resume admission now admits each user's resume (decision 85). A session suspended more than `maxSuspendedSessionSeconds` (seven days by default) before the boundary has already expired, as 0146 "SL-10" states.

A variant gives each user of the group 200 EUR a month inside the team's 5,000 EUR: `alice` adds a second budget with the same selector, `"per": "user"`, and one limit of 200.00 on the three currency meters. A per-user amount needs its own budget, because a budget's limits share its partition. Each charge must fit both budgets.

### Example 2: A tenant storage budget with a capacity and a cost

`acme` may hold at most 2 TiB, and may spend at most 300 EUR a month on storage. One budget holds both limits.

**Operator.** The operator instantiates a storage cap for every new tenant:

```yaml
budgets:
  tenantDefaults:
    - name: storage
      limits:
        held: { meters: [storageBytes], amount: 1Ti }
      attach: selector
      appliesTo: {}
```

**Tenant administrator.** A `platform-admin` raises `acme`'s `held` limit to 2 TiB with `PUT /v1/admin/budgets/storage?tenant=acme`, and `alice` adds a cost limit and a monthly period to the same budget:

```http
PUT /v1/admin/budgets/storage
If-Match: "7"
{
  "name": "storage",
  "limits": {
    "held":  { "meters": ["storageBytes"], "amount": "2Ti" },
    "spend": { "meters": ["storageCost"], "amount": 300.00 }
  },
  "attach": "selector",
  "appliesTo": {},
  "period": { "schedule": "calendar", "calendar": { "every": "month", "dayOfMonth": 1 }, "timeZone": "UTC" }
}
```

The period applies to `spend`, and `held` ignores it.

**What happens.**

1. A checkpoint of a session in `acme` asks for a chunk grant of 64 MiB. `storage` is a shared budget, so the replica checks the 64 MiB against what `held` had left at its last flush, 0.1 TiB less what the replica admitted since, checks that `spend` is not exhausted, grants the chunk, and counts the 64 MiB at once. The bytes reach `held` at the replica's next flush, and the confirmation corrects them to the size stored.
2. `acme` holds 1.5 TiB for the whole month. The accrual adds 1.5 TiB times 730 hours to `spend`, which converts at 0.025 EUR per GiB-month to 38.40 EUR, well within 300 EUR.
3. Near 2 TiB, an upload whose declared size exceeds what the replica can still admit is refused with `429 BUDGET_EXHAUSTED`, `details.limit: held`, `details.meter: storageBytes`, and `details.available`. Each replica admits against what was left at its last flush, so `held` can pass 2 TiB by what the other replicas admit in one flush interval ("BG-6", **Best effort**). Once `held` is exhausted, new and resumed sessions of `acme` are refused too (decision 37). The data already held stays.
4. If `acme` held 12 TiB under a larger `held` amount, `spend` would pass 300 EUR during the month and refuse new writes, while the stored bytes keep accruing into it until data is deleted or the month ends.

### Example 3: A CI engine running many root sessions under one weekly budget

A CI system creates one root session per pipeline job, and the operator wants all of them under 400 EUR of model charges and 2,000 compute-unit-hours a week, reset on Monday at 06:00 UTC.

**Tenant administrator.** `alice` creates a service account `ci-runner` under 0143 with the `user` role in the `ci` environment.

**Client.** The CI system, authenticated as `ci-runner`, creates one principal budget with a limit for each meter:

```http
POST /v1/budgets
{ "name": "ci-weekly",
  "limits": {
    "models": { "meters": ["externalCost"], "amount": 400.00,
                "onExhausted": { "running": "continue" } },
    "pods":   { "meters": ["computeUnits"], "amount": 2000,
                "onExhausted": { "running": "end" } }
  },
  "period": { "schedule": "cron", "cron": "0 6 * * 1", "timeZone": "UTC" },
  "onExhausted": { "new": "refuse" } }
```

Each job names the budget and caps itself:

```http
POST /v1/environments/ci/sessions
{
  "runtime": "claude-code",
  "labels": { "pipeline": "release-482" },
  "budgetRefs": ["principal/ci-weekly"],
  "budget": { "limits": {
    "models": { "meters": ["externalCost"], "amount": 8.00 },
    "pods":   { "meters": ["computeUnits"], "amount": 2 } } }
}
```

**What happens.**

1. Each job's budget set holds its session budget, the principal budget, and every tenant budget whose selector matches it, such as one on the `ci` environment.
2. On Sunday `models` reaches 400 EUR. New jobs are refused with `429 BUDGET_EXHAUSTED`, and each running job's attribution bindings are suspended, so `netagent` answers its next model call with `403` naming the budget and the CI system fails the job. `pods` has room, so running jobs keep their pods until they end.
3. A release cannot wait for Monday. A principal budget is reset by its creator, so the CI system resets it with `POST /v1/budgets/ci-weekly/reset` and `"reason": "release 4.2 crunch"`. The reset starts a new period for both limits, because a budget's limits share one period. `budget.reset` records the actor and the closed period, and the next Monday boundary still applies. To raise `models` alone, the CI system would adjust it for the current period ("BG-7").
4. A charge for a model call made at 05:59:58 on Monday that Lenny accepts at 06:00:03 counts in the new week (decision 10).
5. A job that delegates under 0166 caps its children under its own session budget, which counts them. Its children inherit its references, so `ci-weekly` counts their charges as they arrive.

### Example 4: A session budget shared with a capped child

`bob` runs an orchestrating session that delegates a review to a child. `acme` also has a tenant budget `acme-monthly` with a limit `models` of 5,000 EUR a month, and `alice` has a budget `reviewer-runtime` with a limit `models` of 200 EUR a month that selects the `code-reviewer` runtime. Each limit counts `externalCost`, and `acme`'s LLM gateway reports charges under 0133.

**Client.**

```http
POST /v1/sessions
{
  "runtime": "claude-code",
  "labels": { "project": "checkout" },
  "budget": { "limits": {
    "models": { "meters": ["externalCost"], "amount": 40.00 },
    "pods":   { "meters": ["computeUnits"], "amount": 4 } } },
  "delegationLease": { "maxDepth": 1, "maxChildrenTotal": 4 }
}
```

**What happens.**

1. The root's budget set is its session budget (`models`, 40 EUR, and `pods`, 4 compute-unit-hours, which together meet the required limits of 0166's `requiredToDelegate`) and `tenant/acme-monthly`. `reviewer-runtime` does not select `claude-code`.
2. The gateway's charges for the root's model calls count in its session budget as they arrive, and in `acme-monthly` through the flush. After a while the root has used 10 EUR.
3. The root calls `session.create` with `target: "code-reviewer"` and `limits.budget: {"models": 5.00}`. The gateway checks that 5 EUR is within the root's 40 EUR, sets nothing aside, and creates the child with a cap of 5 EUR on `models` and, on `pods`, which `limits.budget` leaves out, `defaultDelegationFraction` of what the root's `pods` limit has left. `acme-monthly` reserves nothing. The child's budget set is the root's session budget, which counts the whole tree, `acme-monthly`, which it matches through its inherited tenant, and `reviewer-runtime`, which selects its runtime.
4. A charge of 0.20 EUR for a child model call counts in the root's `models` counter and in the child's subtree counter for its cap. The root's session budget shows 10.20 EUR consumed, the child's 0.20 EUR among them, and `acme-monthly` and `reviewer-runtime` each rise by 0.20 EUR at the next flush.
5. The review needs more. The root calls `session.set_limits` with `+2.00` on `models`, which raises the child's cap to 7 EUR and sets nothing aside.
6. The child ends having used 3 EUR. Nothing returns, because nothing was set aside, and the root's session budget shows 13 EUR consumed of 40 EUR. `acme-monthly` has counted the child's 3 EUR once, as the charges arrived.
7. Had the child's charges reached its 5 EUR cap before step 5, Lenny would have suspended the attribution bindings of the child and its subtree, so the gateway refused their next model call, and requested a §8.6 extension of the child's cap, which, in `elicitation` mode, asks the root's client, `bob`, under 0166's "DP-3", while the root kept spending within its 40 EUR. The root's `session.set_limits` raise, or a grant, would lift the suspension, and the child's runtime would retry the call. Had the tree spent the root's whole 40 EUR first, the charge that carried the root's `models` limit past its amount would have suspended the attribution bindings of every session of the tree, the child's included while below its cap, because a charge on `externalCost` is counted after the call rather than refused (decision 73).

## Limits, and alternatives rejected

**Limits [design]:**

- Budgets are best effort ("BG-6", **Best effort**), with the bounds of BGR-9. A shared budget passes its amount by up to one flush interval of its subjects' spending plus the work in flight, which grows with the rate of spending. A limit on `storageCost` cannot stop the time that data is held.
- A budget's limits share its selector, partition, and period, so a limit per user beside a team-wide limit, or a weekly limit beside a monthly one, needs a second budget (decision 18).
- A budget that selects on a label key that no label rule of 0149 governs binds only the clients that choose to set the label (decisions 8, 41, and 49).
- A budget partitioned by a label gives each value its full amount, up to `maxPartitions` values a period, and one client can take the places a teammate's new value needs within that cap (decision 56).
- A write to a shared storage limit can pass the limit by what the other replicas admit in one flush interval (decision 53).
- A budget suspension lasts at most `maxSuspendedSessionSeconds` of 0146, after which the session expires, so a suspension that waits for a monthly boundary can end the session first.
- Money figures are estimates under the operator's price list, which leaves out volume tiers, commitments, credits, and taxes, as a reporter's prices do.
- A cap sets nothing aside, so siblings compete for what their tree has left: five children capped at 50 EUR each under a root of 200 EUR all start, and the last of them to spend are refused partway when the tree has spent the 200 EUR ("BG-8", decision 70).
- A charge that a tenant reporter or a session's owner sends counts in no platform budget, so a platform budget on `externalCost` counts only what platform reporters report, and the tenant's own budgets bound what it spends on services it runs itself (decision 71).
- A service to which the client hands work reports no charge for the session itself: the client posts what the service tells it, as a direct charge (decision 71).
- Budgets cover model calls only through the charges that a registered reporter sends, as 0133 "LU-7" states, and costs that reach no tenant under 0121.
- A budget suspension that outlasts its hold costs a checkpoint and a later restore for each session of the subtree, and a session under `sealOnly` or `none` expires at the end of the hold.
- Under `gateway.delegation.sessionBudget: optional`, a parent with no session budget creates children that no cap bounds, so DPR-4 of 0166 holds only for a parent with a session budget.
- Costs attributed to a tenant and to no session, such as idle pods, dedicated nodes, and legal-hold escrow, reach no budget (decision 29).
- Every limit of a session budget counts its whole delegation tree at the root, so a tree shares one amount on each key, the bytes it holds included (decisions 69 and 70).
- A charge that arrives after a transfer counts on the session's new path, whatever its `occurredAt`, so the charges of calls in flight at the transfer skip the former ancestors' caps. This arises only where `leaveMaxDepth` is above 1, because a child of the root has no capped ancestor, and it is bounded by the calls in flight at each transfer, for at most `maxLeaves` transfers of a tree (decision 79).
- A released session's cap on `storageBytes` becomes its capacity limit and its held bytes leave the old tree, so in the worst case a tree and the sessions it releases together hold up to (`maxLeaves` + 1) times the root's storage amount. An SO-5a rule with `when.spendAbove` reads currency amounts alone, so it holds no release for storage, and a tenant's or the platform's shared storage budgets bound the total (decisions 75 and 80).
- A session that an external limit suspends stays suspended after the gateway's limit resets, until the root's client resumes it, and a runtime that never sends `external_limit` behind a gateway that sends no notices keeps retrying until its own logic stops (decision 68).

**Alternatives considered:**

- **Reserve ahead in every budget, with allotments for busy budgets.** Every charge would reserve in every budget of its set, budget by budget across hash slots with a compensating release, and a budget whose charge rate passed a threshold would hand each replica a share of its remaining amount. Every model call would wait on one Redis step per budget, and the protocol would need a recovery index across slots. Session budgets and caps keep the reservation, where a small amount and one hash tag make it cheap and exact, and shared budgets count afterwards (decision 24).
- **Count afterwards, and reserve per call near the amount.** A shared budget would switch to per-call reservation when its remaining amount fell below its recent spending over a few flush intervals. It narrows the overshoot near the amount at the cost of the cross-slot protocol for those budgets. The owner chose to keep shared budgets best effort (decision 24).
- **Attribution assertions minted by the session's owner.** The owner would mint the attribution assertion for a service it calls, up to a day long. The gateway takes the same assertion as its caller credential, so an owner could call the gateway outside the pod with it, and binding suspension, which acts on `netagent`, could not stop those calls. Decision 71 removed them.
- **A charge-only grant with a cap.** The owner would obtain an opaque reference that a registered reporter cites, with an amount past which Lenny refuses its charges, which no gateway accepts as a login. It adds a record, a counter, and a proof kind, and the owner kept it for a later need (decision 71).
- **Owner charges in every shared budget, with a default maximum per charge.** One user could still exhaust a team's or a tenant's budget for everyone in it, in small charges (decision 71).
- **Carves.** A child's budget would be reserved from its parent's session budget at creation and returned at its end, so a child would hold a guaranteed amount. It needs a reservation per child, carve returns that wait for a child's reservations to settle, refusals of a transfer that cannot reserve and of a raise with no carve behind it, a parent's budget held by idle children, and, on `externalCost`, which counts after the fact, an amount reserved only on paper. Decisions 12 and 55 chose it, and decision 70 replaced it with caps.
- **A reserved slice on request.** A parent would choose, child by child, between a cap and a reservation, for a child that must not be starved. The owner kept one mechanism (decision 70).
- **Keep fixed scopes and add more.** A team scope, a project scope, a weekly window, and a storage money limit would each need a setting, a counter, and an enforcement path. The selector and the partition express each of them with one mechanism.
- **Money as the only unit.** Every limit would count in the deployment's currency, and a capacity on bytes held would need a mechanism of its own. An operator could not budget pod time at its own cost or without a price list. Each meter carries its unit, so `computeUnits` and `storageBytes` stand beside the currency meters (decision 67).
- **A compute weight set by hand on each resource class.** Each class, including one a tenant defines, would need a figure, and a class without one would count like any other. The formula prices every class from its requests, and a pin covers the exceptions (decision 4).
- **A compute weight on billing events.** The billing stream would carry a budget setting, and a billing system that applies its own terms would read a figure it does not use. Billing reads the resources and applies its own formula (decision 3).
- **Credit conversions that a tenant writes for every upstream.** A tenant would set the price of the platform upstreams it calls, and a low price would loosen every currency limit that counts those calls, including a platform ceiling, unless each limit recorded who wrote it. A `platform-admin` writes a tenant's prices, and chargeback runs from usage reports (decision 54).
- **A storage share per replica.** Each replica would admit writes within what the limit had left divided by the number of replicas, which refuses a write larger than the share while the limit has room for it, and forgets a slow write across a flush. Counting a write at admission bounds the overshoot to one flush interval (decision 53).
- **Partitions by a label only on a governed key.** A rule removed later would leave the budget open, so the budget declares its own `maxPartitions` (decision 56).
- **`observe` mode on a session budget.** A client could make its administrators' ceilings advisory. A principal budget in `observe` mode with `per: rootSession` gives a client the same trial (decision 50).
- **Enforce money only when the billing system has priced it.** The billing system prices after the fact, so no work could be refused before it costs money, which is what a budget is for.
- **Budgets as Kubernetes custom resources.** Tenant administrators and clients reach Lenny through its API and hold no cluster access.
- **One unit per budget.** A team that limits its pod time in compute units and its total spend in the currency would keep two budgets with the same selector and period, and an administrator would keep them in step. A budget's limits express both in one record (decision 18).
- **A unit field on each limit.** A limit would name `money` and the deployment would name the currency, so one fact sat in two places, and a limit could name a unit its meters do not use. Each meter fixes its unit, and `budgets.currency` names the currency once (decisions 66 and 67).
- **`netagent` observing responses.** `netagent` would read the status codes and headers of responses from a gateway's host and raise an external limit for every runtime with no gateway code. The owner declined it: "No to netagent observing responses" (decision 68).
- **An automatic resume at an external limit's reset.** The session would resume at `resetsAt` or after `retryAfter`, which a runtime or a gateway states and Lenny cannot check. The owner chose "No automatic wake up" (decision 68).
- **Sessions go `dormant` at once when a budget runs out.** Each session in a subtree would release its pod and pay a restore even when a raise followed a minute later, and some sessions of a tree would sleep while others ran. A suspension pauses the subtree together and holds the pods for a short window (decision 42).
- **`usableBy` on a budget selected by labels.** It would keep out sessions whose owner it does not list, but every budget would repeat who may claim a label, and reports and external costs would still read unchecked labels. Label rules of 0149 govern the label itself (decision 41).
- **One counter per budget across all its subjects, with no partitions.** A per-user limit would need one budget per user, written and kept in step with the directory by an administrator.

## Specification and non-specification changes

Each page is named by its section heading and its page code, and an edit lands on the numbered section or on the page that holds the content when the step is built.

**Specification [design]:**

- [§11.2 Budgets and Quotas](../../spec/11_policy-and-controls.md#112-budgets-and-quotas) and the page BUDGET: budget objects, session budgets, matching, meters, enforcement, resets, counters, failure, and precision. The budget rows of the table, **Budget inheritance:**, **Hierarchical Quota Model:** for budgets, **Storage quota enforcement mechanism.**, **Quota Update Timing:**, **Crash Recovery for Quota Counters:**, and **Maximum Overshoot Formula:** are rewritten, and the quota rows stay on the page QUOTA.
- The page RATE gains the platform price list, tenant price lists, and the effective prices.
- The page BUDGET gains external charges, reporters, the ingestion API, and limit notices ("BG-12").
- [§11.2.1 Billing Event Stream](../../spec/11_policy-and-controls.md#1121-billing-event-stream) and the page BILLEV: the estimate label of money figures, and the statement that no billing event carries money (decision 27).
- [§11.7 Audit Logging](../../spec/11_policy-and-controls.md#117-audit-logging) and the catalog CATEVENT: the events of "BG-10".
- [§25.5 Operational Event Stream](../../spec/25_agent-operability.md#255-operational-event-stream): `dev.lenny.budget_threshold_crossed`, `dev.lenny.budget_exhausted`, and `dev.lenny.budget_period_closed` (decision 26).
- [§8.3 Delegation Policy and Lease](../../spec/08_recursive-delegation.md#83-delegation-policy-and-lease), **Budget Reservation Model:**, and the page LEASE: the count of a tree at its root's session budget and the caps on children, over budget objects, with 0166.
- [§8.6 Lease Extension](../../spec/08_recursive-delegation.md#86-lease-extension) and the page LEASEXT: extension of session budgets and caps by limit key.
- [§10.2 Authentication](../../spec/10_gateway-internals.md#102-authentication), **Authorization and RBAC**, and the page AUTHZ: the rows that "BG-10" adds.
- [§10.6 Environment Resource and RBAC Model](../../spec/10_gateway-internals.md#106-environment-resource-and-rbac-model) and the page ENV: `sessionBudgetDefaults`, `sessionBudgetCeilings`, and `defaultDelegationFraction` on an environment, with 0149. The page TSET gains them on a tenant.
- [§12.4 Redis HA and Failure Modes](../../spec/12_storage-architecture.md#124-redis-ha-and-failure-modes): the budget keys in the key table, the flush watermarks, and the failure rules of shared budgets.
- [§14 Workspace Plan Schema](../../spec/14_workspace-plan-schema.md#14-workspace-plan-schema), **`CreateSessionRequest` (outer, sibling fields).**: `budget` and `budgetRefs`.
- [§15.1 REST API](../../spec/15_external-api-surface.md#151-rest-api) and the catalogs CATOPS and CATERR: the routes, `BUDGET_EXHAUSTED` with its details, `BUDGET_NOT_ATTACHABLE` for an interceptor's attachment, `BUDGET_PARTITIONS_FULL`, `CHARGE_BATCH_TOO_LARGE`, `CHARGE_PROOF_KEY_REVOKED`, and `OWNER_CHARGES_DISABLED` of "BG-12", `ASSET_UPLOAD_LIMIT_EXCEEDED` and `UPLOAD_TOO_LARGE` of 0147, and the retirement of `STORAGE_QUOTA_EXCEEDED` and `BUDGET_KEYS_EXPIRED`; the `budget_notice` session event in the catalog CATEVENT.
- [§4.8 Gateway Policy Engine](../../spec/04_system-components.md#48-gateway-policy-engine): `budget_refs` and `session_budget` in the `PreRoute` payload, with 0141.
- [§6.2](../../spec/06_warm-pod-model.md) and [§7](../../spec/07_session-lifecycle.md) with the catalog of state machines: the budget suspension, with 0146.
- [§16.1 Metrics](../../spec/16_observability.md#161-metrics), [§16.5 Alerting Rules and SLOs](../../spec/16_observability.md#165-alerting-rules-and-slos), and the catalogs CATMETRIC and CATALERT: the metrics and alerts of "BG-10", with `StorageQuotaHigh` replaced by a threshold notice.
- The catalogs CATSET and CATLIMIT: every setting and limit of "Documentation".

**Non-specification [design]:**

- A package for budget records, selectors, schedules, and price lists, with its Postgres store, its Redis scripts for the reservation, settlement, and reservation index of session budgets and for the flush of shared budgets, and each replica's cache of shared budget state.
- A charge ingestion package receives batches, checks proofs against the mint index, writes `external_charges`, counts charges on `externalCost`, and suspends the sessions that limit notices name ("BG-12"). `pkg/gateway/meteringproxy` leaves with 0133.S2.
- `pkg/quota/storagequota` becomes the storage meter over budget sets, and `pkg/quota/quotastore` loses its token windows.
- `pkg/gateway/platformtools/treebudget` and `delegationbudget` become the tree counter and the caps, with 0166.
- The session server runs the compute tick at bind and release, and the gateway's leader sweeps gain the storage accrual, the period close, the reservation recovery, and the recovery of a lapsed replica's unflushed charges.
- Migrations for `budgets`, `budget_periods`, `budget_adjustments`, `budget_checkpoints`, `budget_leaves`, `price_list_versions`, `external_charges`, and `charge_reporters`, and the removal of the tenant's quota fields that budgets replace.
- The tenant record gains `budgetCaps` with `maxPerTenant` and `maxPerPrincipal`.
- The chart gains `budgets.*` and an optional `computeUnitsPerSecond` pin in `resourceClasses`, and `lenny-ctl` gains `budget list`, `budget get`, `budget usage`, `budget reset`, and `price-list validate`.
- The alerting rules lose `StorageQuotaHigh` and its checkpoint variants for budget notices.

## Documentation

Each page below is **[design]** and lands with the step that changes what it describes.

| Page | Reader | What changes |
| :-- | :-- | :-- |
| `docs/operator-guide/budgets.md` (new) | Operator | Budgets, limits, and the meters with the unit of each; currency limits that combine the currency meters, and capacity limits on `storageBytes` with their quantity strings; `budgets.currency`, which names the currency once (no default; limits on currency meters are refused without it); the independence of budgets and billing, and what the billing stream carries for a billing system to compute compute units itself; price lists and their versions, validation, and estimate label; compute rates with `budgets.compute.rates.cpuPerVCPU` (default 1), `budgets.compute.rates.memoryPerGiB` (default 0.125), `budgets.compute.rates.gpu.<type>` (no default), `budgets.compute.isolationMultipliers` (default 1 per profile), and the pins `resourceClasses.<name>.computeUnitsPerSecond` and the pool's `computeUnitsPerSecond` (unset), with the formula and a worked rate for each shipped class; `budgets.compute.tickSeconds` (default 60, from 10 to 600); `budgets.storage.accrualIntervalSeconds` (default 300, from 60 to 3600); budgets as best effort, with session budgets and caps reserved ahead, shared budgets counted afterwards, and `budgets.shared.flushSeconds` (default 5, from 1 to 60) with the overshoot it allows; `budgets.tenantDefaults`; `budgets.maxPerTenant` (default 500), `budgets.maxPerPrincipal` (default 20), `budgets.maxPerTenantCeiling` (default 5000), `budgets.maxPerPrincipalCeiling` (default 200), and the tenant record's `budgetCaps`; `budgets.maxRefsPerSession` (default 8), `budgets.maxBudgetSetSize` (default 32), `budgets.maxPartitionsPerBudget` (default 10000), `budgets.maxAdjustmentsPerBudget` (default 100), `budgets.minResetIntervalSeconds` (default 300), `budgets.maxLimitsPerBudget` (default 8), `budgets.suspendPodHoldSeconds` (default 300, from 0 to 3600), the platform's `budgets.sessionBudgetDefaults` and `budgets.sessionBudgetCeilings`, the fail-open share of shared budgets with its per-limit hard cap `quotaPerReplicaHardCap` (default 0.5 of the amount) and `quotaFailOpenCumulativeMaxSeconds`, the count of a write to a shared storage limit at admission and its overshoot, `budgets.maxThresholds` (default 8), and `budgets.minPeriodSeconds` (default 3600); `defaultDelegationFraction` per limit key (default 0.5); `maxPartitions` and `BUDGET_PARTITIONS_FULL`; the platform price list, tenant price lists, and effective prices; failure behaviour, the recovery of a replica's unflushed charges, rebuild, and precision per meter and kind of budget; the metrics and alerts, `BudgetFlushLagging` included; charge reporters and their registration with `/v1/admin/charge-reporters`, their scope and hosts; `budgets.external.maxChargesPerRequest` (default 1000), `budgets.external.maxChargesPerSecondPerReporter` (default 2000), `budgets.external.maxChargesPerSecondPerOwner` (default 100), `budgets.external.maxOwnerChargesPerSecondPerTenant` (default 1000), `budgets.external.maxChargeAmount` (unset), and `budgets.external.maxReportDelaySeconds` (default 86400); the bounds on a charge's amount and fields; the retention of `external_charges` under `billing.retentionDays`; voids, which reverse the counters a charge incremented; `budgets.external.ownerCharges` (default `enabled`) with its tenant setting; the budgets each source of a charge counts in, a platform budget counting only platform reporters; a void that lifts a suspension; limit notices from reporters and the `external_limit` frame, the suspension they cause, and its manual resume. |
| `docs/operator-guide/multi-tenancy.md` | Operator, tenant administrator | Tenant budgets and their RBAC, the caps on their number with a tenant's override and a `tenant-admin`'s lower principal cap, the tenant price list that a `platform-admin` writes, the effective prices a tenant reads, the tenant's charge reporters, whose charges count in no platform budget, chargeback from usage reports, selectors, partitions qualified by tenant, references and `usableBy` with interceptor registrations, budgets that select on labels, the label rules of 0149 that govern them, and the `Warning` on a budget whose label is free in some environment, partitions with `maxPartitions` and when their places free, "Read session attributes", schedules with time zones, resets on demand, thresholds and notices, exhaustion actions and their effect on running sessions, observe mode on shared budgets, and the tenant's and environment's session budget defaults, ceilings, and `defaultDelegationFraction`. The storage quota section becomes a storage budget. |
| `docs/operator-guide/configuration.md` and `docs/reference/configuration.md` | Operator | Every `budgets.*` value with its default and range, and the removal of `storageQuotaBytes` and the token quota settings. |
| `docs/client-guide/budgets.md` (new) | Client | Budgets as best effort, and the overshoot a shared budget allows; the session budget with its limits, its defaults and ceilings joined by key, its `enforce` mode and the strictest `onExhausted` across levels, `PATCH`, and the root client's `PATCH` of a delegated child's caps, and extension; the whole tree suspended when the root's limit runs out, and a capped child's subtree when its cap does; trying amounts with a principal budget in `observe` mode with `per: rootSession`, named in `budgetRefs` and read from its usage route; a budget suspension, its hold, its automatic resume within the hold or by an extension or a raise for the session, the client's resume after the hold, and `budget_notice`; principal budgets and `budgetRefs`; reading a session's budget set; resets of a principal budget; charges it posts for its own sessions, for work outside the pod, with their checks, their rate, and the budgets they count in, which are the session's tree and the owner's principal budgets alone, `422 OWNER_CHARGES_DISABLED` when its tenant turns them off, and the acceptance of late charges for an ended session within 24 hours; a session suspended by an external limit, its `suspension_cause: external_limit` with the reported status, message, and reset time, and the client's explicit resume with `acknowledgeExternalLimit: true` that alone ends it, a message being held; and how a refusal appears. |
| `docs/client-guide/error-handling.md` and `docs/reference/error-catalog.md` | Client, operator | `BUDGET_EXHAUSTED` with its details, `BUDGET_NOT_ATTACHABLE`, `BUDGET_PARTITIONS_FULL`, `CHARGE_BATCH_TOO_LARGE`, `CHARGE_PROOF_KEY_REVOKED`, `OWNER_CHARGES_DISABLED`, and the retirement of `STORAGE_QUOTA_EXCEEDED` and `BUDGET_KEYS_EXPIRED`. |
| `docs/client-guide/delegation-and-tasks.md` | Client | A tree that shares its root's session budget, caps on children, and the extension of a child's cap that reaches the root's client. |
| `docs/runtime-author-guide/delegation.md` | Runtime author | `limits.budget` on `session.create` by limit key, `defaultDelegationFraction`, `session.set_limits` for budget, what `session.transfer` and `session.release` do to a tree's counters and a cap, spend staying in the former ancestors' caps on a transfer and a released session's cap becoming its storage limit, and the refusals of a cap. |
| `docs/runtime-author-guide/lifecycle.md` | Runtime author | A charge refused under `suspend`, an exhausting external charge, a compute tick, or a write, stops the turn and suspends the session with its subtree, which resumes on its pod within the hold or restores after it, and under `end` expires it; a model call while attribution is suspended is answered by `netagent` with `429` and `Retry-After` while an extension is pending and `403` naming the budget once the exhaustion is final, and a refused export returns `BUDGET_EXHAUSTED`; a runtime that meets a limit its gateway or another service enforces sends the `external_limit` frame of 0139 "RP-9", which suspends its session. |
| `docs/api/admin.md` and `docs/api/rest.md` | Operator, client | The routes of "BG-10". |
| `docs/reference/metrics.md` | Operator | The metrics and alerts of "BG-10". |
| `docs/runbooks/storage-quota-high.md` | Operator | Becomes a runbook for a storage budget's threshold notice. |
| `docs/runbooks/delegation-budget-exhaustion.md` | Operator | Tree counters and caps over budget objects. |
| `docs/runbooks/budget-rebuild.md` (new) | Operator | The rebuild after a Redis loss and `BudgetRebuildFailing`, and a replica whose flush lags under `BudgetFlushLagging`. |
| `docs/reference/glossary.md` | Every reader | The terms of "Terms". |

## Tests

The tiers are those of `.claude/rules/test-coverage.md`.

| Tier | Tests | Requirements |
| :-- | :-- | :-- |
| `unit` | The meters of "BG-4" with their units, the refusal of a limit whose meters differ in unit and of a limit that names `unit`, a `storageBytes` amount as a quantity string, and a capacity limit that ignores the period, counts its tree at the root and takes a child's cap, takes no extension, and refuses a write past it; the refusal of a price list version that names `currency`, and of a charge whose `currency` differs from `budgets.currency`; the join of requested, default, and ceiling limits by key, with the refusal of a key whose meters differ between levels; selector matching for each field; the budget set's invalidation; schedule boundaries for each schedule kind across daylight-saving changes in several time zones, cron spacing, and on-demand resets; money conversion with rounding per charge and version selection by time, and the price lookup of "BG-5" for a compute and a storage charge with and without a tenant price list, with one price in a tenant and a platform budget; the compute rate from resources and isolation multipliers, the pins of a pool and a class, and the per-slot share; partition keys qualified by tenant, `per: tenant`, `maxPartitions` with its refusal and its count per tenant on a platform budget, the places freed at a boundary, by a reset of all or one partition, and at a session's end, a partition holding bytes kept across a boundary, and `budgets.maxPartitionsPerBudget`; the caps with a tenant's override, its ceiling, and a lower principal cap, `budgets.maxLimitsPerBudget`, `budgets.maxBudgetSetSize`, `budgets.maxAdjustmentsPerBudget`, and `budgets.minResetIntervalSeconds`; the size check of a write against the cached remaining amount less the replica's admitted bytes, its count at admission, its correction at completion and on abandonment, and a stream cut past its declared size; the refusal of `continue` on a limit that counts a compute meter on every route; the refusal of `mode`, `startsAt`, `endsAt`, and `disabled` on a session budget, and `onExhausted` resolved to the strictest action across levels; `suspend` and `end` triggered by an exhausting external charge and a refused write; a cap on every key of every child, with `defaultDelegationFraction` of the parent's remaining bound for an unnamed key and the refusal of a cap above the parent's bound; an adjustment by key, its refusal of an unknown key, and a capacity limit under an adjustment window. | BGR-1, BGR-4, BGR-5, BGR-6 |
| `component` | The reservation, settlement, and reservation-index scripts of a session budget on a Redis Cluster, with every limit of one budget reserved in one script; a reservation across the root's limits and every cap on a path of `maxDepth` caps in one script, and the rewrite of subtree counters at a transfer and a release, with a release's old hash debited first and its second step idempotent on the leave's identifier; the flush script of a shared budget with its watermark, a threshold crossed by a flush, and a cached state refreshed to exhausted; the exhaustion of one limit, which refuses its meters' charges while the budget's other limits keep charging; a flush after a period boundary, which adds each sum to its own period; the recovery sweep for each meter and the recovery of a lapsed replica's unflushed charges counted once, with the fence that makes a returning replica drop its sums, the flush script's refusal of a fenced epoch, the sweep run again before the sums are dropped, and a record committed after a sweep counted by the next; the fail-open share and the turn to refusing after `quotaFailOpenCumulativeMaxSeconds`; the rebuild from checkpoints and durable records. | BGR-2, BGR-6, BGR-7, BGR-10 |
| `contract` | The routes' schemas, `If-Match`, `dryRun`, the error details, and the redaction of `details.budget` for a caller that may not read it. | BGR-11, BGR-12 |
| `integration` | Example 1 to Example 4 on the compose stack, with a gateway fake that reports charges: a team budget with a currency limit across sessions, with a threshold notice and exhaustion; a storage budget with a capacity limit and a cost limit; a CI engine under one principal budget with two limits and a reset on demand; a capped child with a transfer and a release; the root's `externalCost` limit exhausted, which suspends the attribution of every session of the tree, a void of the exhausting charge within the hold that resumes the tree, and one after the hold that resumes nothing while a granted extension does (decision 85); a reporter's limit notice and a runtime's `external_limit` frame each suspend a session and its subtree, which stay suspended past `resetsAt` until the root's client resumes the suspended session itself with `acknowledgeExternalLimit: true`, a message from the root's client being held (0146's L45); a team budget whose `models` limit counts only `externalCost` under `suspend`, whose exhaustion at a flush suspends the team's live sessions and leaves a `dormant` one `dormant`, and an adjustment that resumes them (decision 83); a limit notice for a `dormant` session, which becomes `suspended` without a pod, and a notice with `retryAfter` alone, recorded as `resetsAt` (decision 84) | BGR-1 to BGR-8, BGR-12 |
| `e2e_kind` | A compute tick refused under `suspend` suspends a parent and its children together; a raise within the hold resumes them on their pods; after the hold each checkpoints, the checkpoint admitted past an exhausted storage limit, and releases its pod, and a client's resume is refused until the budget has room, and an administrator's raise after the hold resumes none of them until each client resumes; a session under `sealOnly` expires at the end of the hold; a new session is refused while a storage limit is exhausted. | BGR-8, BGR-9 |
| `load_local` | Many sessions on several replicas charging one shared budget, with the measured overshoot at several `budgets.shared.flushSeconds` against BGR-9's bound; concurrent children of one root charging its counter under their caps. | BGR-2, BGR-9 |
| `chaos` | A replica killed with reservations in each meter and with unflushed sums for shared budgets, each charge counted once after recovery; Redis lost and recovered across a period boundary; a gateway killed between the two steps of a release, which the leader sweep finishes, and Redis lost after a release and a transfer, whose rebuild restores the release's consumption, the released budget, and the moved bytes from `budget_leaves`. | BGR-6, BGR-10 |
| `security` | A principal budget cannot reach another principal's session; a tenant budget cannot match another tenant's charge; a `tenant-admin` cannot set a compute rate, multiplier, or pin, write the platform price list or its tenant price list, read the platform price list or another tenant's prices, or raise a budget cap; `budgetRefs` naming another principal's budget, or a tenant budget the caller may not use, is refused as missing, and the budget set read names no budget the caller may not read; a client cannot loosen a shared budget through its own session budget, or set `mode` or a looser `onExhausted` on it; a child created with no `limits.budget` holds a cap on every key and SO-5a reads it; a reporter cannot charge a session of a tenant outside its scope, a session whose assertion names another audience, or a call outside the assertion's validity window, and a charge signed by a key retired in an emergency rotation is refused; a caller that does not own a session cannot post a charge to it, and receives `404`; no route mints an attribution assertion for a client; a charge from a tenant reporter or an owner counts in no platform budget, and an owner's charge in no tenant budget; a principal that carries a session claim is refused on `PATCH /v1/sessions/{id}/budget`, `POST /v1/sessions/{id}/charges`, and the principal budget routes; a runtime has no route to post a charge; a capped session that transfers a child cannot spend its cap again; a limit notice that cites a `jti` of another audience or another tenant is refused, and an `external_limit` frame suspends no session but the runtime's own; a paused session with a pod cannot keep its pod past an exhausted compute limit beyond the hold; an interceptor not named in a budget's `usableBy` cannot attach it; a cap above the parent's bound is refused, and a charge past the cap of any capped ancestor is refused; a granted extension, under `extensionApproval: auto` included, raises no cap past its holder's parent's bound and no ancestor's cap; a `tenant-admin`'s void of its reporter's charge gives no room in a platform budget, and a void gives room only in the counters its charge incremented; an owner's direct charge for an ended session with an `occurredAt` more than 60 seconds after the end is refused; a session whose label a rule of 0149 does not allow is refused. | BGR-2, BGR-3 |
| `unit` | The proof checks of "BG-12" for each refusal code: a `jti` outside the mint index, of another audience or tenant, or outside its validity window, and a `jti` minted under a key retired in an emergency rotation; a tenant reporter refused on another tenant's host under the `deployment` key set and accepted under `perTenant`, and refused on a platform host under both; per-charge results, and the batch limits; an owner's direct charge accepted with `source: client`, its `(owner, id)` idempotency, and its rate; the budgets each source counts in; a void that gives a limit room lifting its suspensions, a void that reverses exactly the counters its row records, and a void of a charge from a closed period that corrects that period's record and gives no room in the open one; the amount and field bounds of a charge; the per-tenant rate of owners' direct charges; `budget_notice` to the root's client for a crossing of the root's limit and of a cap, naming the holder; a usage report of a tenant or platform budget that leaves out the charges its sources do not reach; `422 OWNER_CHARGES_DISABLED` under the tenant switch; a late charge for an ended session counted in its shared budgets, its session budget, in the root's counter and the caps on its path after the session ended, and on its path at arrival after a transfer or a release, and refused once the session's record is gone; limit notices with their checks, per-notice results, and a notice for an ended or already paused session that changes nothing. | BGR-13, BGR-14 |
| `docs` | The pages of "Documentation" name only settings, metrics, alerts, and errors the catalogs hold, with matching defaults. | BGR-11 |
