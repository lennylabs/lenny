# Budgets

Status: `Draft`, revision 6, 2026-11-23. Programs: [v1](../programs/v1.md).

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

- Budget objects: named records that hold one or more limits, each counting one or more meters in one unit, under one set of sessions, one reset schedule, thresholds, and actions at exhaustion, created by a platform administrator, a tenant administrator, or a client for its own sessions ("BG-1").
- The session budget as a budget object, and the carve of 0166's "DP-3" over budget objects ("BG-2", "BG-8").
- Matching: how a session and each charge find every active budget that applies ("BG-3").
- Meters and units: what a charge measures, model calls, pod time, and stored data, and the units a limit counts each in: LLM credits, compute units, bytes, byte-hours, and money ("BG-4").
- Price lists: the operator's translation of each meter's charges into an amount of money, versioned, and how a limit in money counts the charges of several meters ("BG-5").
- Enforcement: budgets as best effort, with session budgets and carves reserved ahead and shared budgets counted afterwards, where each meter charges, the soft thresholds, the actions at exhaustion, and lease extension for session budgets ("BG-6").
- Resets: on demand and on a schedule, with time zones, and the period in which a charge counts ("BG-7").
- Counter storage, failure behaviour, and the precision of each meter ("BG-9").
- API routes, RBAC, audit events, metrics, and usage reports by budget object ("BG-10").
- A statement of what this change supersedes in 0133 and in 0166 ("BG-11").

**Out of scope:**

- Charging, invoicing, payments, and taxes. Lenny charges no one, and a price list is an operator setting that Lenny uses for enforcement and reports as an estimate.
- Equality between a budget's figures and a bill. Budgets and billing read the same records and are independent: discounts, commitments, and special terms can make a bill differ from a budget's money estimate, and the operator keeps the two aligned (decision 3).
- Chargeback and markups from a tenant to its own teams. A tenant runs them outside Lenny from usage reports (decision 7).
- The cost the operator pays its providers. The operator's billing system prices the quantities in the billing events, as 0121 states, and decides whether to show Lenny's estimate.
- Calls a runtime sends to a provider outside the LLM proxy, which 0133 leaves unmetered (OD-15 of 0133).
- Concurrency and rate limits, which stay quotas under [§11.1](../../spec/11_policy-and-controls.md#111-admission-and-fairness).
- Costs that reach no tenant: unpinned idle pods on shared pools, node capacity that no pod requests, and data held in Postgres, as 0121 states.
- Costs that reach a tenant and no session: the idle pods of a tenant's owned or pinned pools, its dedicated nodes, and legal-hold escrow. They stay billing events of 0121 (decision 29).
- Meters that a deployer defines. Lenny meters model calls through its proxy, pod time, and stored data, and nothing else (decision 14). A deployer that pays for other services attributes their costs to sessions with the identity Lenny sends ahead of each call ("BG-3", **External costs**), and enforces its own limits on them.
- Egress bytes. `egress.tunnel` records of `0127-egress-and-git` carry bytes per tunnel and are lossy ("Current state"), so this change meters no egress (decision 33).

## Terms

| Term | Meaning |
| :-- | :-- |
| Budget, or budget object | A named record that holds one or more limits, which sessions it applies to, a period with its reset schedule, thresholds, and the actions at exhaustion ("BG-1"). |
| Limit | One named entry of a budget: the meters it counts, the unit it counts them in, and an amount, with optional thresholds and exhaustion actions of its own. A budget's limits share its selector, attachment, partition, and period ("BG-1"). |
| Session budget | The budget object of one session, created with the session and ending with it. It holds limits as every budget does and has no period. It applies to the session and, through carves, funds its descendants ("BG-2"). |
| Shared budget | Every budget object other than a session budget: a platform, tenant, or principal budget. |
| Platform budget, tenant budget, principal budget | A shared budget written by a `platform-admin` and able to span tenants, by a `tenant-admin` for its own tenant, or by a user or a service account for its own sessions. |
| Meter | What a charge measures: `llm`, model calls at the LLM proxy; `compute`, the time a session holds a pod; and `storage`, the data a session owns ("BG-4"). |
| Unit | What a limit counts its meters in: `llmCredits`, `computeUnits`, `bytes`, `byteHours`, or `money`. "BG-4" states which units each meter allows. |
| Charge | A quantity from one meter, attributed to one subject. Each limit that counts the meter converts it to the limit's unit. |
| Subject | What a charge is attributed to: a session, with its tenant, environment, principal, groups, runtime, pool, labels, and root session. |
| Compute unit | The unit of pod time. A pod's rate in compute units per second follows its resources: its vCPUs, its GiB of memory, and its GPUs, each at a rate a `platform-admin` sets, times the multiplier of its isolation profile. A session's charge is its pod's rate divided by the pod's slot count, for each second it holds a slot ("BG-4"). With the default rates, one compute unit is one vCPU held for one second. |
| Compute rates | The figures that turn a pod's resources into compute units per second: a rate per vCPU, a rate per GiB of memory, a rate per GPU type, and a multiplier per isolation profile, with an optional fixed figure for one resource class or pool. A `platform-admin` sets them ("BG-4"). |
| Price list | The operator's record that translates LLM credits, compute units, and stored bytes over time into an amount of money in the deployment's currency, with versions by `effectiveFrom` ("BG-5"). A tenant's credit conversions override its price per LLM credit for the tenant's own limits in money. It differs from 0133's rate card, which prices tokens in credits. |
| Limit in money | A limit whose unit is `money`. It counts the charges of the meters it names, each converted at the price-list version in force when the charge occurred. |
| Selector | The attributes of a shared budget that decide which subjects it applies to ("BG-3"). |
| Reference | The name of a shared budget that a session creation lists in `budgetRefs`, which attaches the budget to the session and its descendants ("BG-3"). |
| Budget set | The budgets that apply to one subject: its session budget, the shared budgets whose selectors match it, and the budgets it references, its ancestors' references included. |
| Partition | One set of counters of a budget whose `per` field names an attribute, such as one set per user, with a counter for each limit ("BG-1"). |
| Period | The interval over which a budget's counters accumulate, from one reset to the next. A budget with no schedule has one period for its life, and a limit in `bytes` ignores the period. |
| Reset | The close of a period and the start of the next, at a schedule boundary or on demand ("BG-7"). |
| Adjustment | An amount that an administrator adds to or takes from one or more limits of a budget, named by key and stated in each limit's unit, for the periods that start within a window, for one partition or every partition ("BG-7"). |
| Reservation | An amount held against the limits of a session budget, and of the session budgets that fund it through carves, before the work that incurs it runs, settled to the actual charge afterwards ("BG-6"). Shared budgets hold no reservation. |
| Budget suspension | The pause of a session and its subtree by a limit under `onExhausted.running: suspend`, with `suspension_cause: budget`, a pod hold, and a resume when the limit gains room ("BG-6"). |
| Flush | A gateway replica's addition of the charges it settled, summed in memory, to the Redis counters of a shared budget, every `budgets.shared.flushSeconds` ("BG-6"). |
| Carve | A reservation that funds a child session's budget from its parent's session budget ("BG-8"). |
| Threshold | A fraction of a limit's amount at which the gateway records a notice ("BG-6"). |
| Enforce mode and observe mode | A budget in `enforce` mode refuses what does not fit. A budget in `observe` mode counts and records notices and refuses nothing. |
| BG-n and BGR-n | The parts of this design under "Detailed design" carry BG-n, and its requirements carry BGR-n. |

### Terms from other designs and the platform

| Term | Meaning |
| :-- | :-- |
| Credits, and the unit `llmCredits` | 0133's unitless measure of a model call's cost. An upstream's rate card prices each token class and server tool in credits, and Lenny stores credits as integer micro-credits ([`0133-llm-upstreams-and-cost-budgets`, "LU-5"](../0133-llm-upstreams-and-cost-budgets/change.md#lu-5-rate-cards)). A budget names this unit `llmCredits` (decision 16). |
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
| LU-5, LU-6, LU-7, D3, OD-6, OD-15, and decision 6 | [`0133-llm-upstreams-and-cost-budgets`](../0133-llm-upstreams-and-cost-budgets/decisions.md#decisions) | Rate cards, credit budgets with their scopes and reservation, usage records, unitless credits, the estimated cost on billing events, budgets for proxied calls alone, and the credit reservation under Redis Cluster, which decision 6 of 0133 and decision 24 of this change settle. |
| BR-1 to BR-7 | [`0121-billing-resource-attribution`](../0121-billing-resource-attribution/change.md#high-level-design) | Resource classes, session pod time, idle pods, stored bytes, reconciliation, escrow, and dedicated nodes as billing events. |
| SL-1, SL-4, SL-6, SL-10, L35, and L36 | [`0146-session-lifecycle`](../0146-session-lifecycle/change.md#sl-4-tree-suspension) | The states with `suspension_cause`, tree suspension and the budget suspension that reuses it, idle pods and dormancy, and admission, caps, and timers. |
| RT-2 | [`0141-runtime-routing`](../0141-runtime-routing/change.md#rt-2-the-interceptor-contract) | The interceptor contract, with `budget_refs` and `session_budget` in the `PreRoute` payload and the post-chain point. |
| TS-10 | [`0149-tenant-scoping-identity`](../0149-tenant-scoping-identity/change.md#label-rules-ts-10) | Label rules. |
| MD-22 | [`0135-message-delivery`](../0135-message-delivery/change.md#md-22-parents-and-children) | Child events, with `suspended` and `resumed` carrying `reason`. |
| IC-4 | [`0131-incremental-checkpoint`](../0131-incremental-checkpoint/change.md#ic-4-catalog-reference-closure-and-quota) | The checkpoint catalog and its storage quota. |
| DP-1, DP-2, DP-3, DP-6, DP-7, and decisions 12, 13, 33, 35, 40, 43, and 44 | [`0166-delegation-primitives`](../0166-delegation-primitives/change.md) | The session grant with its root grant inputs and the resolution of every caveat ("Resolution" in DP-1, decision 40); transfer and release (DP-2, decision 33); the carved budget, which cites this change, and `session.set_limits` (DP-3); the disposition of each delegation setting (DP-6); what each change takes on (DP-7); the carve as a reservation from budget objects (decision 12); a child's age as a deadline (decision 13); ownership by concern (decision 35); this change's membership of the v1 program (decision 43); and `labels` set by the root's client and inherited unchanged, beside a parent's `tags` (decision 44). |
| SO-5a | [`0142-security-oversight`](../0142-security-oversight/change.md#so-5a-tool-use-approvals) | Tool-use approvals, whose `when.llmCreditsAbove` on a `session.create` rule holds a child whose carve in `llmCredits` exceeds the amount, and a `session.set_limits` raise past it. |
| R6 | [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md#ro-2-rest-additions) | Webhook subscriptions. |

## High-level design

A budget is a named record that says how much of something a set of sessions may use in a period. An operator, a tenant administrator, or a client creates it. It holds one or more limits. Each limit names what it counts, model calls, pod time, or stored data, and the unit it counts them in: model calls in LLM credits or money, pod time in compute units or money, and stored data in bytes held, byte-hours, or money. A team's monthly budget can therefore cap its model calls in LLM credits and its total spend in money in one record. It names the sessions it applies to, either by their attributes, such as a tenant, an environment, a directory group, a runtime, or a label, or by being named when a session is created. It names when it resets: never, at the start of each day, week, month, or year in a time zone, on a cron schedule, or every fixed interval, and anyone allowed to change it can also reset it on demand.

Every session can also have its own session budget, which is a budget object like the others and ends with the session. When a session uses something, the gateway finds every active budget that applies to it, its session budget included, and the use counts against every limit of those budgets that counts it. Budgets are best effort, and how closely each kind is held differs (decision 24). The session's own budget, and the carves that fund it, are held exactly: before a model call the proxy holds the call's worst case in them, while a session holds a pod the gateway holds one minute of compute at a time, and before a write the gateway holds the bytes. Shared budgets, which every session of a team or a tenant reaches, are counted afterwards: each gateway replica adds up what its sessions spend and writes the sums to the shared counters every few seconds, and it refuses new work in a shared budget that its last read showed as spent. A shared budget can therefore pass its limit by a few seconds of spending. A write to a shared budget's storage limit is checked against the replica's share of what is left, so a burst of writes stops near the limit without a call to Redis for each. If a limit lacks room, the work is refused, and the limit's exhaustion action applies to running sessions: a model call is refused, a session and every session below it are suspended together, keeping their pods for a short hold so that a raise of the limit resumes them at once and releasing them after it, or a session ends, as the budget states. Stored data is never deleted because a budget ran out, and the checkpoint that saves a session's workspace when its pod is released is always stored.

A limit in money can count several meters at once. The operator writes a price list that gives a price per LLM credit, per compute unit-hour, and per GiB-month stored, in the deployment's currency. Each charge is converted at the price in force when it occurred, so a team's monthly limit in money counts its model calls, its pod time, and its stored data together. The price list is an operator setting. Lenny reports its money figures as estimates and charges no one.

A delegated child gets a carve: its budget is reserved from its parent's session budget, and its own use counts inside that reservation, so the parent's budget counts the child once. Shared budgets count the child's use as it happens, as they count any session's. When the child ends, the unused part returns. This is the model the owner chose in decision 12 of 0166, whose "DP-3" cites this change for it.

Budgets keep their counters in Redis and their definitions and closed periods in Postgres. A gateway crash loses no charge: a reservation expires into a recovery sweep that settles it from the meter's durable record, and the sums a crashed replica had not yet written are added from the same records. Each kind of budget has a stated overshoot: a session budget by a model call's input estimate error, one compute tick, or the checkpoint grant window that §11.2 states today, and a shared budget by one flush interval of spending plus the work in flight when it runs out. A suspension's pod hold, the checkpoint at a pod's release, and a Redis outage within the per-replica fail-open share add their own stated terms (BGR-9).

```
 charge sources (meters)                 gateway                                   stores
 ───────────────────────                 ───────                                   ──────
 LLM proxy: model call  ──llm──────┐
 pod binding: each tick ──compute──┼──>  subject (session attributes)
 storage write / accrual ─storage──┤       │
 (no other meter) ────────────────┘       ▼
                                         budget set = session budget
                                                    + shared budgets whose selector matches
                                                    + budgets referenced by the session or an ancestor
                                           │
                                           ▼
                                         for each limit that counts the meter:
                                         convert to its unit
                                         (money: price-list version in force)
                                           │
                                           ▼
                                         session budget and carves: reserve ahead
                                         shared budgets: check cached state
                                           │
                                           ├──fits──> work runs, settle in session budget,
                                           │          add to replica's in-memory sums  Redis counters
                                           │          ──every flushSeconds──> flush to shared counters
                                           └──does not fit──> refuse, exhaustion action, notices
                                                                                Postgres: budgets,
                                                                                periods, price lists
```

## Requirements

| ID | Requirement |
| :-- | :-- |
| BGR-1 | Every charge has one meter and one subject, and the gateway applies it to every limit that counts that meter, in every budget of the subject's budget set, converted to the limit's unit. |
| BGR-2 | No reservation is admitted unless every limit that counts the charge's meter, in the session's own budget and in each ancestor's session budget that takes the session's charges directly ("BG-8"), has room for it. No work is admitted while a replica's cached state shows a limit that counts its meter, in a shared budget of the session's set in `enforce` mode, as exhausted, and no write is admitted to a shared limit that counts `storage` beyond the replica's share of what the limit has left ("BG-6"). A budget in `observe` mode counts the charge and refuses nothing. |
| BGR-3 | A principal budget applies only to sessions whose creation names it in `budgetRefs`, and to their descendants. A tenant budget never applies to a session or a charge of another tenant. |
| BGR-4 | A session's budget set is computed from attributes fixed when the session is created, so it changes only when a budget is created, changed, enabled, disabled, or deleted. |
| BGR-5 | A limit in money converts each charge at the price-list version in force at the charge's time. Price-list versions are append-only, a version whose `effectiveFrom` is in the past is never written, and no release of Lenny carries a price. |
| BGR-6 | A budget with a schedule starts a new period at each boundary of its schedule in its time zone, and a reset on demand starts one at once. Each closed period is recorded with its consumption by limit and by meter, and each charge counts in the period in which it settles. |
| BGR-7 | A child's carve is reserved from its parent's session budget, the child's charges inside the carve count once in that budget and once in each shared budget of the child's set, and the carve's unused amount returns to the parent's session budget when the child ends. |
| BGR-8 | The exhaustion of a budget never deletes or truncates stored data. |
| BGR-9 | Overshoot is bounded per meter and per kind of budget. For a session budget and a carve: for `llm`, by the input estimate's error on the calls in flight, as LUR-7 of 0133 states; for `compute`, by one compute tick; for `storage` in `bytes`, by the outstanding checkpoint grant window of §11.2. For a shared budget: by the charges that every replica settles in one `budgets.shared.flushSeconds`, plus the work in flight when a replica's cached state turns to exhausted, in which each model call in flight counts at most `quotas.llmCredits.perCall` and Lenny states no cap on a session's calls in flight; for `storage` in `bytes`, by what the replicas admit within their shares between flushes. During a Redis outage or a run of failed flushes, by each replica's fail-open share of what each limit had left, until `quotaFailOpenCumulativeMaxSeconds` turns the replica to refusing. For either kind: the pod time of a suspension's hold, `budgets.suspendPodHoldSeconds` per suspended session, and one checkpoint of a session's workspace each time its pod is released by a suspension's hold or a move to `dormant`. |
| BGR-10 | A gateway replica's crash loses no charge: every reservation of the replica is settled by the recovery sweep from its meter's durable record, the charges it settled after its last flush to a shared budget are added to that budget from the same records, a replica whose lease lapsed drops its unflushed sums when it returns, so no charge counts twice, and a Redis loss rebuilds no counter below the consumption that Postgres records for its period. |
| BGR-11 | The gateway records an audit event for each creation, change, deletion, and reset of a budget, each threshold a limit crosses in a partition and a period, each exhaustion of a limit, and each price-list version. |
| BGR-12 | A budget's usage report states, for each period, partition, and limit, the consumption by meter, the reserved amount, the limit's amount, and, for a limit in money, the money amount labelled as an estimate. |

## Current state

**Budgets are fixed scopes [source].** The [§11.2](../../spec/11_policy-and-controls.md#112-budgets-and-quotas) table lists token limits per request, session, user window, tenant window, global window, and task tree; runtime limits per session and per child; a retry budget; a delegation budget of depth, fan-out, and total children; concurrent sessions per tenant and per user; a per-tenant storage quota; and eval submission rate limits. **Hierarchical Quota Model:** states that soft warnings are emitted at 80% of a quota as billing events, that hard limits refuse at 100% with `QUOTA_EXCEEDED`, and that "Quota reset periods are configurable per quota type: hourly, daily, monthly, or rolling window."

**Windows are fixed UTC buckets [source].** `quota.ResetPeriod` in `pkg/quota/quota.go` is the closed set `hourly`, `daily`, `monthly`, and `rolling`. `window` in `pkg/quota/quotastore/quotastore.go` maps a period to a key label from the UTC time, such as `monthly-202611`, and refuses `rolling`, which the fixed-window store does not keep. No time zone, weekly period, or on-demand reset exists. The tenant record carries `StorageQuotaBytes`, `TokenQuotaPerWindow`, and `QuotaResetPeriod` (`pkg/tenancy/tenantstore/tenantstore.go`).

**Separate enforcement paths and counters [source].**

- Session token budget: `sessionbudget.Enforcer` (`pkg/gateway/meteringproxy/sessionbudget`) counts per replica in process memory and refuses after exhaustion, which F-11.2.34 records and 0133.S3 closes with Redis reservation.
- Tree budget: `budget_reserve.lua`, embedded in `pkg/gateway/platformtools/treebudget/treebudget.go`, carves a child's token budget from its parent at delegation, with keys under the `{root_session_id}` hash tag ([§8.3](../../spec/08_recursive-delegation.md#83-delegation-policy-and-lease), **Budget Reservation Model:**).
- Storage quota: the script `reserveScript` in `pkg/quota/storagequota/redisstore/redisstore.go`, which a comment in `pkg/quota/quota.go` names `storage_quota_reserve.lua`, reserves bytes against the tenant's `storageQuotaBytes` before an upload or a checkpoint write, and the counter is rebuilt from the `artifact_store` sum and the outstanding checkpoint reservations (§11.2, **Storage quota enforcement mechanism.**).

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

The detail below states the design that the decisions of `decisions.md` settle, and each part names the decisions it follows.

### BG-1 Budget objects

A budget is a record with these fields **[design]**:

| Field | Meaning |
| :-- | :-- |
| `name` | Unique within its scope: the platform, one tenant, or one principal. |
| `scope` | `platform`, `tenant`, `principal`, or `session`, set by the route that creates it. |
| `limits` | A map from a key, unique within the budget, to a limit: `meters`, the meters it counts; `unit`, one unit that every meter of `meters` allows under "BG-4"; `amount`; and optional `thresholds` and `onExhausted`, which override the budget's. A limit in `money` with no `meters` counts every meter the price list prices. An amount is a decimal number in its unit, and the gateway stores it as an integer in the unit's smallest step: micro-credits, milli-compute-units, bytes, byte-seconds, and micro-units of the currency. A budget holds at least one limit and at most `budgets.maxLimitsPerBudget` (default 8). |
| `appliesTo` | The selector of "BG-3". A principal budget has none. |
| `attach` | `selector`, `reference`, or `both`. A principal budget is always `reference`. |
| `usableBy` | For a tenant or platform budget with `attach` including `reference`, the principals and directory groups that may name it in `budgetRefs`, and `interceptors`, the names of the interceptor registrations of 0141 that may attach it ("BG-3"). |
| `per` | Optional partition key: `tenant`, `user`, `session`, `rootSession`, `environment`, `runtime`, or `label:<key>`. The budget keeps one set of counters for each value, each limit at its full amount (decision 32). Every partition key is qualified by the session's tenant, so a platform budget keeps apart the `prod` environments of two tenants. |
| `period` | The schedule of "BG-7", with its time zone, shared by every limit. A limit in `bytes` ignores it, because it limits what is held at each moment. |
| `thresholds` | Fractions of each limit's amount, default `[0.8, 1.0]`, at most `budgets.maxThresholds` (default 8). A limit may set its own. |
| `onExhausted` | The actions of "BG-6" for new work and for running sessions. A limit may set its own. |
| `mode` | `enforce`, the default, or `observe`. |
| `startsAt`, `endsAt` | Optional. A budget is active between them and in `enforce` or `observe` mode, and `disabled: true` makes it inactive without deleting it. |
| `resourceVersion` | The version a `PUT` names in `If-Match`. |

**Limits [design].** A charge must fit every limit that counts its meter, in every budget of its set, so a model call must fit both a limit in `llmCredits` that counts `llm` and a limit in `money` that counts `llm` and `compute` (decision 18), within the bounds that "BG-6" states under **Best effort**. A limit that counts several meters adds their charges in its unit, which every one of them must allow. When one limit is exhausted, the work of its meters stops and the work of the budget's other meters continues: a budget whose `llmCredits` limit is exhausted refuses model calls and keeps charging pod time to its `computeUnits` limit. A budget whose selectors, partitions, or periods differ is a separate budget.

**Who creates what [design].** A budget only narrows what a session may do, because every charge must fit every budget of its set. The authority to create one therefore follows the reach of its selector:

| Creator | Route | Scope | Selector | Attachment |
| :-- | :-- | :-- | :-- | :-- |
| `platform-admin` | `/v1/admin/budgets` | `platform`, across every tenant or a listed set, or `tenant` for any tenant | Any field of "BG-3" | `selector`, `reference`, or `both` |
| `tenant-admin` | `/v1/admin/budgets` | `tenant`, own tenant | Any field of "BG-3" except `tenants` | `selector`, `reference`, or `both` |
| A user or a service account | `/v1/budgets` | `principal` | None | `reference`, by the creator alone |
| Session creation | `POST /v1/sessions` | `session` | The session and, through carves, its descendants | Implicit ("BG-2") |

A principal budget cannot reach another principal's session, because only its creator can name it and a reference attaches only at the creation of the creator's own session.

**Caps on the number of budgets [design].** Every budget is a stored record with counters, and every charge reaches each budget of its set, so the number of budgets has caps (decision 6):

| Cap | Default | Set by | What it bounds |
| :-- | :-- | :-- | :-- |
| Tenant budgets per tenant | `budgets.maxPerTenant`, 500 | The Helm value for every tenant. A `platform-admin` may set `budgetCaps.maxPerTenant` on one tenant's record, up to `budgets.maxPerTenantCeiling` (default 5000). | The records, counters, and selector matching one tenant puts on the shared stores. |
| Principal budgets per principal | `budgets.maxPerPrincipal`, 20 | The Helm value for every tenant. A `platform-admin` may set `budgetCaps.maxPerPrincipal` on one tenant's record, up to `budgets.maxPerPrincipalCeiling` (default 200), and a `tenant-admin` may lower it for its own tenant's principals. | The personal budgets one user or service account, or a script holding its credential, can create. |
| References per session creation | `budgets.maxRefsPerSession`, 8 | The Helm value alone. | The references one creation names, the client's and an interceptor's together. |
| Budgets in one session's set | `budgets.maxBudgetSetSize`, 32 | The Helm value alone. | The shared budgets each charge of a session reaches, selector matches and references together, which each replica caches and flushes. |
| Limits per budget | `budgets.maxLimitsPerBudget`, 8 | The Helm value alone. | The counters each reservation in one budget updates. |
| Partitions per budget and period | `budgets.maxPartitionsPerBudget`, 10000 | The Helm value alone. | The counters a budget with `per: label:<key>` opens for label values that clients choose. Partitions of the other keys follow principals, environments, runtimes, and sessions that the platform admits. |
| Adjustments per budget | `budgets.maxAdjustmentsPerBudget`, 100 | The Helm value alone. | The adjustments whose window has not ended. |
| Resets on demand | `budgets.minResetIntervalSeconds`, 300 | The Helm value alone. | The period rows and audit events that resets of one budget and partition write. |

The gateway refuses a write beyond a tenant or principal cap, an adjustment beyond `budgets.maxAdjustmentsPerBudget`, and a reset sooner than `budgets.minResetIntervalSeconds` after the last reset of the same budget and partition with `429 QUOTA_EXCEEDED` naming the cap. It refuses a session creation that names more references than the cap with `400 VALIDATION_ERROR`, and a session creation whose budget set would exceed `budgets.maxBudgetSetSize`, or that would open a partition past `budgets.maxPartitionsPerBudget`, with `429 QUOTA_EXCEEDED` naming the cap, and the audit event names the budgets, so a tenant administrator who writes too many overlapping selector budgets refuses its own sessions rather than loading the shared Redis. A lower cap set later deletes no budget and refuses new ones until the count falls below it. A partition (`per`) keeps one counter per value inside one budget, so a limit per user or per environment counts as one budget.

**Defaults for a new tenant [design].** `budgets.tenantDefaults`, a Helm list of budget templates, is instantiated as tenant budgets when a tenant is created, such as a storage cap with a limit in `bytes`. A `tenant-admin` may change or delete a budget instantiated this way. A `platform-admin` who wants a limit a tenant cannot remove writes a platform budget instead. This replaces the tenant's `storageQuotaBytes`, `TokenQuotaPerWindow`, and `QuotaResetPeriod` fields and 0133's `quotas.llmCredits.perUser`, `perTenant`, and `global`, which leave, and `STORAGE_QUOTA_EXCEEDED` retires into `BUDGET_EXHAUSTED` (decision 30). `quotas.llmCredits.perCall` stays, because it caps one call.

### BG-2 Session budgets

A session creation may carry `budget`, which creates the session's budget object **[design]**:

```json
{
  "budget": {
    "limits": {
      "models": { "meters": ["llm"], "amount": 400000, "unit": "llmCredits" },
      "pods":   { "meters": ["compute"], "amount": 14400, "unit": "computeUnits" },
      "spend":  { "meters": ["llm", "compute", "storage"], "amount": 25.00, "unit": "money" }
    },
    "thresholds": [0.5, 0.9],
    "onExhausted": { "running": "suspend" }
  }
}
```

- **Limits.** A session budget holds limits as every budget does ("BG-1"), so a session's own budget can cap model calls, pod time, and spend together.
- **Defaults and ceilings.** A session budget's limits resolve under "Resolution" in [DP-1 of 0166](../0166-delegation-primitives/change.md#dp-1-session-grant), which states the order of defaults and ceilings for every limit a session receives (0166 decisions 40 and 49). This change names the settings that resolution reads: `budget` on the creation request as the requested value, `sessionBudgetDefaults` on a runtime, an environment, a tenant, and the platform as the defaults, and `sessionBudgetCeilings` at the same levels as the ceilings, each a `limits` map. Keys join the levels: a requested limit, a default, and a ceiling with the same key resolve as one limit, and the gateway refuses with `400 VALIDATION_ERROR` a key whose meters or unit differ between levels. A ceiling whose key no request or default names stays in the session budget at its amount, so a ceiling binds whatever keys a client chooses. A requested limit whose key no ceiling names only narrows, because every charge must fit every limit. A session with no default, no ceiling, and no request has no session budget and is bound by its shared budgets alone. Whether such a session may delegate, or be created holding a grant, is `gateway.delegation.sessionBudget` of 0166 "DP-1" (decision 39), which by default refuses a child of a session without a session budget, so every child is carved. The platform's defaults and ceilings are the Helm values `budgets.sessionBudgetDefaults` and `budgets.sessionBudgetCeilings`. A tenant's are written with `PUT /v1/admin/tenants/{tenant}/session-budget`, which takes `defaults` and `ceilings`, by a `tenant-admin` for its own tenant or a `platform-admin`, an environment's are fields of the environment resource, and a runtime's are fields of the runtime record. The numeric `ceilings` of 0166's tenant and environment delegation settings carry no budget amount.
- **Lifetime.** The session budget has no schedule. It is created in the transaction that creates the session, it stops counting when the session reaches a terminal state, and its record is kept with the session's usage records for the session's retention. Storage held after the session ends counts against the shared budgets alone (decision 21).
- **Changes.** The session's owner reads it with `GET /v1/sessions/{id}/budget` and raises or lowers a limit, by key, with `PATCH /v1/sessions/{id}/budget` within the ceilings. A raise beyond the ceilings goes through lease extension ("BG-6"). A carved session budget is funded by its parent's reservation, so a `PATCH` of one is refused with `409 SESSION_BUDGET_CARVED`, and its parent changes it with `session.set_limits` ("BG-8", decision 47).
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

**External costs [design].** A deployer that pays for a service outside Lenny, such as a search API that a tool server calls, matches each cost to a session with what Lenny already sends ahead of the call, and enforces its own limit there (decision 14):

| Call | What identifies the session | Source |
| :-- | :-- | :-- |
| A request from a pod to a deployer's service through egress | The identity assertion that `netagent` attaches: tenant, environment, user or principal type, session, root session, and depth, signed by Lenny | [`0124-connector-removal`, "CR-2"](../0124-connector-removal/change.md#cr-2-identity-assertion) |
| A model call that the proxy forwards to a deployer's LLM gateway | The `Lenny-*` headers: tenant, environment, user, session, root session, runtime, pool, and upstream | [`0133-llm-upstreams-and-cost-budgets`, "LU-11"](../0133-llm-upstreams-and-cost-budgets/change.md#lu-11-forwarded-attribution) |
| A call that the client makes | The session identifier, which the client holds | The client's own record |

From a session identifier, `GET /v1/sessions/{id}` returns the session's attributes and `GET /v1/sessions/{id}/budgets` its budget set. For a caller that holds "Read session attributes" ("BG-10"), the read also returns the directory groups recorded at the session's creation, which the `groups` selector reads, so a deployer's service that looks a session up once and caches it matches an external cost to the same team that Lenny's selectors match, with a credential that grants nothing else (decisions 15 and 40). A deployer that finds its own limit spent refuses new sessions with a `PostAuth` or `PreRoute` interceptor that returns `REJECT`, refuses model calls with a `PreLLMRequest` interceptor, and refuses its own service's calls itself.

**The budget set [design].** At creation, the gateway computes the session's budget set and stores the identifiers on the session row. Every attribute a selector reads is fixed at creation, so the set changes only when a budget changes. On a budget's creation, change, enablement, disablement, or deletion, the gateway writes an invalidation, each replica refreshes the tenant's budgets from Postgres, and the next charge of each session reads the new set. A charge in flight keeps the set it was admitted against. A new tenant budget with a selector therefore applies to running sessions from their next charge.

**Labels and label rules [design].** A session's `labels` are the ones its root's client set at the root's creation, inherited unchanged by every descendant, and no parent writes them (0166 decision 44). A parent's `tags` are its own bookkeeping, and no selector reads them. A tenant governs who may set a label key and value with the label rules of [`0149-tenant-scoping-identity`, "Label rules (TS-10)"](../0149-tenant-scoping-identity/change.md#label-rules-ts-10), which refuse a session that sets a governed key its owner may not set, and may require a key (decision 41). A budget that selects on a governed key therefore binds the sessions its tenant's rules admit to that label, and no client outside them can join it or spend it. A key with no rule stays free: any client may set it or omit it, so a budget that selects on such a key binds only the clients that choose to set it. The admin API accepts such a budget and answers its write with a `Warning` naming the key. A budget that must bind selects on a governed or required key, or on `environments`, `principals`, `groups`, `runtimes`, or `pools` (decision 8).

### BG-4 Meters and units

A meter is what a charge measures, and a unit is what a limit counts it in (decision 17). Each meter allows the units below, and this table is the one statement of which units a limit may use for its meters **[design]**:

| Meter | Units a limit may count it in | Quantity | Subject | Source of record |
| :-- | :-- | :-- | :-- | :-- |
| `llm` | `llmCredits`, `money` | The estimated cost of a proxied model call under its rate-card version, in micro-credits. | The calling session. | `llm_usage` rows (0133 "LU-7"). |
| `compute` | `computeUnits`, `money` | Slot time at the pod's rate in compute units per second. | The session bound to the slot. | `session_pod_windows` rows (0121 "BR-2"). |
| `storage` | `bytes`, `byteHours`, `money` | Bytes held, and their integral over time. | The session that owns each artifact or pack, or the tenant for a tenant-owned platform chunk set. | `artifact_store` rows (0121 "BR-4", 0131 "IC-4"). |

A limit that counts several meters uses a unit that each of them allows: `money` for any set. The gateway refuses any other limit at write with `400 VALIDATION_ERROR` naming the meter and the unit.

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

**Billing [design].** Every meter reads the records that 0121's billing events report, so budgets and billing count from the same sources. The billing stream gains nothing from this change: it already carries each pod's resources, isolation profile, slot count, and time, from which a billing system can apply the formula above or its own (decision 3).

**Storage [design].** Each stored artifact, checkpoint pack, output file, and upload has an owning session in the ledger, and a pack is charged once, to its owner, whichever session references it, as IC-4 charges the quota today. A pack of a tenant-owned platform chunk set has the tenant as its subject. A pack of a platform-owned set reaches no budget. A limit in `bytes` counts the bytes held now, at each moment, and ignores the period. A limit in `byteHours` counts their integral, in byte-seconds, over the period. A limit in `money` counts the byte-hours at the price list's storage price.

### BG-5 Price lists and money

**The record [design].** The deployment has one price list, which a `platform-admin` writes (decision 7). It has versions, each with an `effectiveFrom` time, appended with `POST /v1/admin/price-lists/platform/versions` and never edited:

```yaml
scope: platform
version: 4
effectiveFrom: 2026-12-01T00:00:00Z
currency: EUR                       # must equal budgets.currency
llm:
  perMillionCredits: 2.00
  byUpstream:                       # optional, for an upstream whose card does not follow the standard credit scale
    bedrock-eu: 2.10
compute:
  perComputeUnitHour: 0.12
storage:
  perGiBMonth: 0.025                # a GiB-month is 2^30 bytes held for 730 hours
  byArtifactType:
    checkpoint: 0.020
```

The figures are illustrative, and no figure in this document is a price Lenny ships. Rate cards stay in credits, and no rate card carries money.

**A tenant's credit conversions [design].** A `tenant-admin` may append versions of its tenant's credit conversions with `POST /v1/admin/price-lists/tenant/versions`, with the same `effectiveFrom` rules. A tenant version carries the `llm` section alone, a `perMillionCredits` and a `byUpstream` map, and the admin API refuses any other section. It may name any upstream the tenant can use, a platform upstream included. The `compute` and `storage` meters count at the platform's prices in every budget, because the operator knows what the infrastructure costs.

```yaml
# POST /v1/admin/price-lists/tenant/versions, by a tenant-admin of acme
version: 2
effectiveFrom: 2026-12-01T00:00:00Z
currency: EUR
llm:
  byUpstream:
    acme-azure-openai: 1.40           # acme's own contract with its provider
    bedrock-eu: 1.95                  # a platform upstream, at acme's negotiated rate
```

- **Currency.** `budgets.currency` is one Helm value for the deployment: an ISO 4217 code such as `EUR`, or a code the operator invents, such as `PTS` for an internal points scheme, of 3 to 12 uppercase letters (decision 34). Lenny converts between no currencies and attaches no meaning to the code beyond labelling money figures. Money amounts are stored as integer micro-units of it. With no currency set, the gateway refuses a limit in money and a price list with `400 VALIDATION_ERROR`.
- **Validation.** The admin API refuses a version that leaves a meter unpriced while a limit in money in its scope counts that meter, a version whose `effectiveFrom` lies in the past, and a version whose currency differs from `budgets.currency`. `lenny-ctl price-list validate` checks a version against the deployment.
- **Conversion.** Each charge is converted at the version in force at its time: a model call at its start, a compute tick at its start, and a storage accrual interval at its end. Each conversion multiplies in integers and rounds up once per charge, so no drift accumulates, and the version used is recorded with the charge.
- **Several meters.** A limit in money keeps one counter in money for every meter it counts. A model call and a compute tick each reserve their money equivalent in every limit in money of the session budgets that hold them, and every limit in money of a shared budget that counts their meter receives the settled charge converted at the same version. Stored data reaches a limit in money through the accrual of byte-hours, which reserves nothing. A refusal names the budget, the limit, and its unit, so a caller sees that a limit in money refused a compute tick.
- **Which conversion applies.** A limit converts at the price list of the level that wrote it (decision 38). A limit written by a `platform-admin`, which is a limit of a platform budget, a platform ceiling or default of a session budget, or a limit of a tenant budget that `budgets.tenantDefaults` instantiated and no `tenant-admin` has changed, converts every charge at the platform price list. A limit written by a `tenant-admin`, a principal, or a client, which is a limit of a tenant or principal budget, a tenant, environment, or tenant-derived runtime ceiling or default, or a limit a client requests, converts an `llm` charge at the first of these that prices the call's upstream: the tenant's `byUpstream` entry, the tenant's `perMillionCredits`, the platform's `byUpstream` entry, and the platform's `perMillionCredits`. It converts every other charge at the platform price list. A session budget limit records the level that wrote the amount that bound it after resolution, so a tenant's conversion never loosens a platform ceiling. One charge can therefore count a different amount of money under two limits, and each limit records the version it used.
- **Estimates.** Every money figure Lenny reports carries `estimated: true` and the price-list versions used. Lenny records quantities on billing events, as 0121 states, and no billing event carries money (decision 27).

### BG-6 Enforcement

**Best effort [design].** Budgets are best effort: a limit stops work near its amount and can pass it by the bounds of BGR-9 (decision 24). Every other section cites this statement. The gateway holds a budget in one of two ways:

- **Session budgets and carves reserve ahead.** Before a model call, at each compute tick, and before a write, the gateway reserves the charge in the limits of the session's own budget that count its meter, which share one hash tag, and in each limit of an ancestor's session budget that takes the session's charges directly under "BG-8". A limit that lacks room refuses the work. The reservation is settled to the actual charge afterwards.
- **Shared budgets count afterwards.** Each gateway replica keeps a cached state of every limit, partition, and period of the shared budgets its sessions reach: the amount left, or exhausted. Before it admits work, it reads that state, and it refuses the work when a limit in `enforce` mode that counts the work's meter is exhausted. When a charge settles, the replica adds it to an in-memory sum for each shared budget, partition, period, and limit that counts it. Every `budgets.shared.flushSeconds` (default 5, from 1 to 60) the replica adds each sum to the Redis counter in a script that also records the replica's flush watermark for that counter, and refreshes its cached state from the counters it wrote and read. A shared budget holds no reservation, and a replica never reserves per call in one, however close the budget is to its amount.
- **Writes to a shared storage limit check a size (decision 36).** Every write declares its size before its bytes move: an upload its declared length, an export its size, and a checkpoint chunk its grant. A replica admits a write to a shared budget's limit that counts `storage` in `bytes` when the size fits the replica's share, which is what the limit had left at the replica's last flush divided by the number of live replicas, less the bytes the replica has admitted to the limit since that flush, writes in flight included. A write that does not fit is refused with `BUDGET_EXHAUSTED`, and `details.available` gives the replica's share left. The gateway makes no Redis call for the check, and it cuts off a stream that sends more than it declared. The checkpoint that a suspension's hold or a move to `dormant` takes is admitted whatever the share ("Exhaustion").
- **Fail-open (decision 35).** While a replica cannot reach Redis, or its flushes fail, it admits work in each shared limit, partition, and period up to its fail-open share: what the limit had left at its last successful flush, divided by the number of replicas it last saw, and at most the per-replica hard cap of [§12.4](../../spec/12_storage-architecture.md#124-redis-ha-and-failure-modes). It keeps its sums in memory. `quotaFailOpenCumulativeMaxSeconds` (300 s by default) of cumulative fail-open turns the replica to refusing work that charges a shared limit until a flush succeeds, as §12.4 states for quotas today. Session budgets fail as "BG-9" states.

**Per meter [design].**

The table states when a session budget reserves and settles. A shared budget receives each charge when it settles, through the next flush.

| Meter | When a session budget reserves | When it settles | What a refusal stops |
| :-- | :-- | :-- | :-- |
| `llm` | Before the proxy forwards a call, the output limit and input estimate of 0133 "LU-6", in every limit of the session budgets that hold the call and count `llm`. | When the response's usage arrives, under 0133. | The call, with `BUDGET_EXHAUSTED`. The session continues, as decision 5 of 0133 states. |
| `compute` | At each bind, and at the start of each compute tick, `budgets.compute.tickSeconds` (default 60) of the slot's compute units. | At the end of each tick and at the release, for the time the session held the slot. | At a bind, the session's creation, its resume, or a child's creation. At a tick, the running session, under `onExhausted.running`. |
| `storage`, limits in `bytes` | Before an upload, an export, or a checkpoint chunk grant, the declared bytes. A shared budget's limit in `bytes`, such as a tenant's storage cap, checks the declared size against the replica's share, as "Best effort" states. | At confirmation, to the confirmed size, and at deletion, by the deleted size. | The write, with `BUDGET_EXHAUSTED`. |
| `storage`, limits in `byteHours` or `money` | No reservation. Each budget keeps the bytes its subjects hold, and a leader sweep adds bytes held times elapsed time every `budgets.storage.accrualIntervalSeconds` (default 300). | At each accrual. | New writes in the subjects of an exhausted limit. |

**Admission [design].** A session creation, a resume, and a child's creation are refused with `429 BUDGET_EXHAUSTED` when the replica's cached state shows a limit of an `enforce` shared budget of the new session's set as exhausted and the limit counts `llm`, `compute`, or `storage`, every session charging all three, or when the first compute tick does not fit the session budget or the carve. A limit that counts `storage` therefore stops new and resumed sessions as well as writes, so the checkpoint admitted at a pod's release only serves sessions that were live when the limit ran out (decision 37). Resume admission of [`0146-session-lifecycle`, "SL-10"](../0146-session-lifecycle/change.md#sl-10-admission-caps-and-timers) runs this check at every move toward a pod. Concurrency stays under §11.1.

**Thresholds [design].** When a charge carries a limit past a threshold in a partition for the first time in a period, the gateway writes `budget.threshold_crossed` with the budget, the limit's key, the partition, the threshold, the unit, the consumption, and the amount. A session budget's crossing is detected by the settlement script, and a shared budget's by the flush script that carries the counter past it, so each crossing is reported once per threshold per period. **Notices [design].** Each budget notice is an audit event and an event on the operational event stream of [§25.5](../../spec/25_agent-operability.md#255-operational-event-stream): `dev.lenny.budget_threshold_crossed`, `dev.lenny.budget_exhausted`, and `dev.lenny.budget_period_closed`, each carrying the budget's scope and name, the limit's key, the partition, the period's bounds, the consumption, and the amount. A subscriber receives them over the stream's server-sent events, polling, or webhook delivery, with its tenant filter, so a `tenant-admin` receives only its own tenant's budget events (decision 26). A budget carries no webhook targets of its own. When a session's own charge carries a limit of its session budget past a threshold, the session's client also receives a `budget_notice` event on the session event stream of [§15.1](../../spec/15_external-api-surface.md#151-rest-api), defined in the catalog CATEVENT. The runtime receives none, because a runtime learns of budgets through refusals alone (decisions 31 and 45). A shared budget sends no event to the sessions it binds.

**Exhaustion [design].** A limit of a session budget is exhausted when its consumption plus its reservations reach its amount. A limit of a shared budget is exhausted in a period when its flushed consumption reaches its amount, and each replica learns of it at its next flush. Exhaustion stops the work of the limit's meters alone. `onExhausted`, on the budget or on the limit, states the action for new work and the action for running sessions:

| Field | Values | Default |
| :-- | :-- | :-- |
| `onExhausted.new` | `refuse`: refuse each new reservation of the limit's meters, which refuses new sessions, resumes, children, model calls, and writes in the budget's subjects, as each counts the meter. | `refuse` |
| `onExhausted.running` | `continue`: a running session keeps its pod and is refused at its next model call or write. The admin API refuses `continue` on a limit that counts `compute`, because a session would keep charging its pod without bound; a budget that should only report uses `mode: observe`. `suspend`: at its next refused compute tick, the session is suspended with its subtree, as "Budget suspension" below states. `end`: the session expires with the reason `budget_exhausted`. | `suspend` for a limit that counts `compute`, `continue` otherwise (decisions 19 and 42) |

**Budget suspension [design].** A refused compute tick under `suspend` pauses the session and every session below it together, reusing the suspension of [`0146-session-lifecycle`, "SL-4"](../0146-session-lifecycle/change.md#sl-4-tree-suspension) (decision 42):

1. The session becomes `suspended` with `suspension_cause: budget`, and each descendant becomes `blocked` under it, by SL-4's steps. Each paused session's open turn stops as SL-3 states. Everything below the session was funded by the exhausted budget through carves, or is charged to the same shared budget, so nothing it funds keeps running while it sleeps. An ancestor stays live, and the session's parent receives the `suspended` child event of 0135 "MD-22" with the reason `budget`.
2. For a limit of a session budget or a carve, the gateway requests the §8.6 extension meanwhile ("Lease extension" below).
3. Room in the limit resumes the subtree. The owner raising the limit, a parent's `session.set_limits` raise of the carve, an administrator's raise, adjustment, or reset, a granted extension, and a new period each give room, and the gateway then resumes the session through resume admission and unblocks its subtree as SL-4's "Resuming a tree" states. Within the hold, each session resumes on the pod it kept, with no restore.
4. Each paused session keeps its pod for `budgets.suspendPodHoldSeconds` (default 300, from 0 to 3600), and then checkpoints its workspace and releases its pod, as SL-4 does after `maxSuspendedPodHoldSeconds`. A later resume restores it. A session whose effective `checkpointPolicy` is `sealOnly` or `none` cannot release its pod with a checkpoint, so at the end of the hold it expires with the reason `budget_exhausted`.
5. A client may resume a budget-suspended session. The resume passes resume admission, which refuses it with `BUDGET_EXHAUSTED` while the limit has no room. A client's suspension of a budget-suspended session sets `suspension_cause: client`, after which only the client resumes it.

The hold's pod time is charged, which BGR-9 bounds.

A limit that counts `storage` never deletes data (BGR-8). A checkpoint whose chunk grant such a limit refuses aborts with `BUDGET_EXHAUSTED`, as a refusal by the tenant's storage quota aborts it today, and the session keeps its previous checkpoint (decision 19). The checkpoint that releases a session's pod is the exception: the checkpoint at the end of a budget suspension's hold, and every checkpoint that makes a session `dormant`, including the one that SL-6 of 0146 takes when a session's idle timer fires, is admitted past an exhausted limit that counts `storage` and past a replica's share, so the session's work survives the release of its pod (decisions 25, 28, and 42). The overshoot is one checkpoint of the session's workspace per release, which the size limits of the pod's volumes bound, and decision 37 keeps new and resumed sessions from adding more.

**Lease extension [design].** §8.6 stays for session budgets and carves, with `additional` naming amounts by limit key in place of `additionalTokenBudget`. When a limit of a session budget or a carve refuses a model call's reservation or a compute tick, the gateway requests an extension under §8.6's approval modes, ceilings, and layering before it applies the refusal. A granted extension of a carve reserves the added amount in the parent's session budget as `session.set_limits` does ("BG-8"), and an SO-5a rule of 0142 with `when.llmCreditsAbove` holds an extension that crosses its amount, as [`0142-security-oversight`, "SO-5a"](../0142-security-oversight/change.md#so-5a-tool-use-approvals) states (decision 46). A shared budget has no extension: a refusal by a shared budget is final until an administrator raises or adjusts the limit, resets the budget, or its period ends (decision 20).

**Errors [design].** Every budget refusal is `429 BUDGET_EXHAUSTED` with `details.budget` (its scope and name, or `session`), `details.limit` (the limit's key), `details.unit`, `details.meter`, `details.partition` when the caller may read the budget, and `details.resetsAt` when the budget has a schedule. A caller that may not read the budget receives `details.budget` as `shared` and no other detail of it. `STORAGE_QUOTA_EXCEEDED` and `details.limitType` of 0133 retire in favour of these details (decision 30). A cap that is no budget, such as 0147's `maxAssetUploadBytes` on a tenant's stored asset uploads, answers `413 UPLOAD_TOO_LARGE` (decision 44), whose fit 0147's open decision 57 asks about.

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

A new period starts each limit at its amount, whatever the last period left unused (decision 11). A limit in `bytes` ignores the period.

- **Boundaries.** The gateway computes each boundary from the schedule and the time zone. A local time that a daylight-saving change skips moves to the next instant that exists, and a local time that occurs twice takes its first occurrence. A cron schedule whose boundaries fall closer than `budgets.minPeriodSeconds` (default 3600) is refused at write.
- **Adjustments.** An administrator raises or lowers a budget's limits for chosen periods with an adjustment, without changing the amounts of the other periods (decision 12). An adjustment names the budget in its route, `limits`, a map from limit keys to signed amounts, each in the unit of its limit, a window `from` and `until`, an optional `partition`, and a `reason` (decision 18):

  ```json
  POST /v1/admin/budgets/daily-per-user/adjustments
  { "partition": "user:bob",
    "from": "2026-11-24T00:00:00+01:00", "until": "2026-11-27T00:00:00+01:00",
    "limits": { "models": 500000, "pods": 360000 },
    "reason": "load tests" }
  ```

  The gateway refuses a key the budget does not hold with `400 VALIDATION_ERROR`. Without `partition`, an adjustment applies to every partition of a budget with `per`, each by the full amount, and to the single set of counters of a budget without `per`. It applies to every period whose start falls in the window, and to the current period when `from` is at or before its start. A limit's amount in a period is its amount plus every adjustment of its key that applies to the period, and never less than zero. A limit in `bytes` takes the adjustment for as long as the window lasts. An adjustment of a limit in money raises the combined amount of every meter it counts. `POST /v1/admin/budget-adjustments` applies a list of adjustments to several budgets, all or none. Whoever may change a budget may adjust it, up to `budgets.maxAdjustmentsPerBudget` adjustments whose window has not ended ("BG-1"). An adjustment whose first period has not started may be deleted, and one in progress may be ended at the next boundary. Thresholds and exhaustion read the adjusted amount. A session budget has no period, and its owner changes it with `PATCH` ("BG-2").
- **Period keys.** A period is named by its start instant, which every replica computes from the schedule, so a charge after a boundary lands in the new period without coordination.
- **On demand.** `POST /v1/admin/budgets/{name}/reset`, or `POST /v1/budgets/{name}/reset` for a principal budget, closes the current period at once and starts a new one for every limit, for every partition or for the one the request names. The request carries a `reason`. The schedule continues at its next boundary, so the period after an on-demand reset is shorter. Whoever may change the budget may reset it, at most once in `budgets.minResetIntervalSeconds` for a budget and partition ("BG-1"). A session budget has no reset.
- **The period of a charge.** A charge counts in the period in which it settles (decision 10). A replica keeps its in-memory sums per period, so a flush after a boundary adds each sum to the period of the charges in it, and a charge that straddles a boundary counts in the new period. A session budget has no period, and a shared budget holds no reservation, so no reservation crosses a reset (decision 24).
- **Records.** Closing a period writes a `budget_periods` row with the period's bounds, the adjustments that applied, its consumption by limit and by meter, the money estimate of each limit in money, and the trigger (`schedule` or `api`, with the actor), and `budget.reset` names the closed period.

### BG-8 Carves for delegated children

The owner settled this in decision 12 of 0166 on 2026-11-23: a child's carved budget is a reservation from the budget objects that apply to the parent, and the child's share is a budget object of the child's session funded by that reservation. Under decision 24 only session budgets hold reservations, so a carve is reserved from the parent's session budget, and shared budgets count the child's charges as they count any session's. This change owns the carve's mechanics, and 0166 keeps DPR-4 and the operations that move a carve (0166 decision 35) **[design]**:

- **Creating the carve.** `session.create` of 0166 carries `limits.budget`, a map from the keys of the parent's session budget to amounts, each at most what that limit of the parent has left. The child's session budget holds a limit for each key named, with the parent limit's meters and unit. When it names none and the parent has a session budget, the child receives `defaultDelegationFraction` (0.5 by default) of what each limit of the parent's session budget has left, settable per key on the runtime, the environment, and the tenant. The gateway reserves each carved amount in every limit of the parent's session budget that counts the carved limit's meters and can state the amount: a limit in the same unit, and a limit in money, converted at the price-list version in force (decision 23). A limit of the parent's session budget that the carve does not reach takes the child's charges directly, reserved ahead as "BG-6" states. A limit that lacks room refuses the child with `BUDGET_EXHAUSTED`. A parent with no session budget carves nothing. Under the default of `gateway.delegation.sessionBudget` such a parent cannot create a child, so every child is carved, and under `optional` its child has the session budget that its defaults and ceilings give it ("BG-2", decision 39). An SO-5a rule of 0142 with `when.llmCreditsAbove` holds the creation for approval before the gateway reserves it, on the amounts that SO-5a states it reads (decision 46).
- **Charges inside the carve.** A charge of the child is reserved in the child's own session budget, which the carve funds, so the parent's session budget needs no step on admission. When the charge settles, each session budget that holds the child's carve, or the carve of an ancestor through which the child is funded, moves the settled amount from reserved to consumed, so its total does not change and the child is counted once. The shared budgets of the child's set, such as one that selects the child's runtime, count the settled charge through the flush.
- **Return.** When the child reaches a terminal state, each budget releases the carve's unconsumed amount after the child's in-flight reservations settle or expire, as 0133's quiescence rule states for the tree scope today.
- **Moving budget.** `session.set_limits` of 0166 raises a carve, by key, by reserving the added amount in the parent's session budget, and lowers it by releasing part of the unconsumed amount, never below what is reserved for the child's calls in flight. A raise of a child's budget is refused with `409 SESSION_BUDGET_NOT_CARVED` when no carve of the caller funds the child, because the caller has no reservation to draw from (decision 47). A raise that crosses an SO-5a rule's `when.llmCreditsAbove` waits for that rule's approval before the gateway reserves it.
- **Transfer.** Under decisions 32 and 33 of 0166, `session.transfer` keeps a child in its tree with the root's client as its client. Its carve moves from the parent's reservation to the root's: the root's session budget reserves the carve's unconsumed amount, key by key, and the parent's session budget releases it in the same step. A transfer is refused with `BUDGET_EXHAUSTED` when the root has no session budget, lacks a key of the carve, or lacks room for the amount, and the child stays with its parent (decision 47). Shared budgets hold no carve and are unaffected.
- **Release.** Under decisions 32 and 33 of 0166, `session.release` makes a child a root session outside its tree. Its unused carve becomes its own session budget's remaining amount. The parent's session budget records the released amount as consumed by the transfer. Shared budgets hold no carve, and the released session's later charges reach them as before, because it still matches them through its inherited attributes and references.
- **Cost.** A carve holds capacity in the parent's session budget for as long as the child lives, so a long-lived child keeps the parent from spending what it holds. A deadline on the child, such as `perChildMaxAge` under decision 13 of 0166, bounds it. A carve holds nothing in a shared budget, so a child never holds capacity that other sessions under a team budget need.

### BG-9 Counters, failure, and precision

**Redis [design].** Each budget partition and period has, under its own hash tag, `{bdg:<budgetId>:<partition>}`, a `consumed` field for each limit and, for a session budget, a `reserved` field for each limit and a reservation index ordered by expiry. A shared budget's hash also holds each replica's flush watermark. A session budget's limits share one hash tag, so a reservation in them is one script (decision 18). A charge reserves in the session's own budget, and in an ancestor's session budget only for a limit that takes the child's charges directly ("BG-8"), so a call in the common case is one Redis step on admission. A shared budget receives one script per replica per flush interval, whatever the rate of charges, so a tenant's budget that every charge reaches is no hot key, and no reservation crosses hash slots (decision 24).

**Postgres [design].** The `budgets` table holds definitions, the `budget_periods` table holds closed periods, the `budget_adjustments` table holds adjustments, which a counter rebuild reads, the `budget_checkpoints` table holds each open period's counters every `quotaSyncIntervalSeconds`, and the `price_list_versions` table holds the price lists. The session row carries its budget set and its references.

**Failure [design].**

- **A replica crash.** A reservation in a session budget whose replica stopped expires, and the recovery sweep of 0133 "LU-6" ("Recovery") settles it from its meter's durable record: a model call at its reservation, or at its usage when the `llm_usage` row exists; a compute tick at the binding's `closed_at` in `session_pod_windows`, or at the tick's end when the binding is still open on a live pod; a storage reservation at the confirmed size in the ledger, or at zero when no row exists. Every durable record names the replica that settled it and the time it settled. When a replica's gateway lease lapses, the recovery sweep adds to each shared budget's counter the charges of that replica's records settled after the counter's watermark for the replica, and the charges of the reservations the sweep recovers, and advances the watermarks to the last charge it added. A charge counts once, because the flush script and the recovery each advance the watermark in the script that adds the charges. The sweep also writes a fence for the lapsed lease. A replica that returns after its lease lapsed, such as one cut off by a network partition, reads the fence, drops its unflushed sums, rebuilds its cached state, and admits work only under a new lease, so its sums are never added after the sweep counted them.
- **A Redis loss.** On recovery, each counter is rebuilt as the maximum of its last checkpoint and the consumption that the durable records hold for the open period, computed lazily per budget, for every limit, on its first charge, which refuses until the rebuild ends. During the outage, the gateway enforces session budgets in memory, admits work in shared budgets within each replica's fail-open share and turns to refusing after `quotaFailOpenCumulativeMaxSeconds` ("Best effort" in "BG-6", decision 35), and flushes the sums and applies the MAX rule on recovery.
- **No key expiry.** A session budget's keys carry no TTL. They are created with the session and deleted when it reaches a terminal state and its reservations have settled, and a shared budget's keys are deleted when its period closes and its record is written. `delegation.budgetKeyTTLSeconds`, the TTL re-arming of §8.3, and `BUDGET_KEYS_EXPIRED` retire, and 0166 "DP-6" lists them (decision 43).
- **A reset during an outage.** A boundary that passes while Redis is unavailable is applied at recovery from the schedule, and the closed period's record is written then.

**Precision [design].**

| Meter | Step | Overshoot of a session budget or carve | Overshoot of a shared budget |
| :-- | :-- | :-- | :-- |
| `llm` | One micro-credit, per call. | The input estimate's error on the calls in flight (0133 LUR-7), plus fail-open drift. | The calls that every replica settles in one flush interval, plus the calls in flight when a replica's cached state turns to exhausted. |
| `compute` | One milli-compute-unit, per tick. | One tick of compute units, plus a suspension's hold of `budgets.suspendPodHoldSeconds`. | The ticks that every replica settles in one flush interval, plus one tick per session holding a pod in the budget, plus a suspension's hold per suspended session. |
| `storage`, in `bytes` | One byte, per write. | The outstanding checkpoint grant window of §11.2, plus one checkpoint per pod release by a suspension's hold or a move to `dormant` (decisions 25, 28, and 42). | What the replicas admit within their shares between flushes, plus one checkpoint per pod release by a suspension's hold or a move to `dormant`. |
| `storage`, in `byteHours` | One byte-second, per accrual interval. | A session budget stops counting at the session's end (decision 21). | Byte-hours accrue while data is held, so a limit's consumption keeps rising after exhaustion until data is deleted. |
| Any meter, in `money` | One micro-unit of the currency, rounded up per charge. | The sum of the overshoots of the meters it counts. | The sum of the overshoots of the meters it counts. |

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
| `GET`, `PATCH /v1/sessions/{id}/budget` | The session budget ("BG-2"). |
| `GET /v1/sessions/{id}/budgets` | The session's budget set, each limit with its state (`ok`, `low`, or `exhausted`), and the name and amounts of each budget the caller may read. A budget the caller may not read appears as `shared`, with its limits' states and no name. |
| `PUT`, `GET /v1/admin/tenants/{tenant}/session-budget` | A tenant's `defaults` and `ceilings` for session budgets ("BG-2"). |
| `POST`, `GET /v1/admin/price-lists/{scope}/versions` | Price-list versions ("BG-5"). |
| `GET /v1/usage?budget=` | The usage report filtered to one budget's subjects. |

**RBAC [design].** The §10.2 permission matrix gains these rows:

| Operation | `platform-admin` | `tenant-admin` | `tenant-viewer` | `billing-viewer` | `user` |
| :-- | :-- | :-- | :-- | :-- | :-- |
| Manage budgets | Yes (all tenants, and platform) | Yes (own tenant) | Read-only (own tenant) | Read-only (own tenant) | Own principal budgets, and the state of each budget in its own sessions' sets |
| Manage price lists | Yes (platform) | Own tenant's credit conversions | Read-only (own tenant and platform) | Read-only (own tenant and platform) | No |
| Set budget caps | Yes (on any tenant's record) | Lower the principal cap for its own tenant | No | No | No |
| Read session attributes | Yes | Yes (own tenant) | No | Grantable (own tenant) | No |

A service account holds the `user` row's rights over its own principal budgets. "Manage quotas" keeps concurrency and rate limits. "Read session attributes" returns a session's recorded directory groups, its labels, and its budget set through `GET /v1/sessions/{id}` and `GET /v1/sessions/{id}/budgets`, and nothing else. A `tenant-admin` grants it to `billing-viewer` or to a custom role of its tenant, so a deployer's cost service holds it without an administrator's rights (decision 40).

**Audit events [design].** [§11.7](../../spec/11_policy-and-controls.md#117-audit-logging) gains `budget.created`, `budget.updated` with the fields changed, `budget.deleted`, `budget.reset` with the trigger, the actor, and the closed period's summary, `budget.adjusted` with the actor, the reason, the amount of each limit key, the window, and the partition, `budget.attachment_skipped`, `budget.suspended` and `budget.resumed` with the session, the limit, and the trigger, `budget.threshold_crossed`, `budget.exhausted` once per limit, partition, and period, `session_budget.changed` for a `PATCH`, an extension, a carve, a transfer, or a release, and `price_list.version_added`.

**Metrics [design].** A budget name and a limit key are not metric labels, because tenants create budgets without bound, and per-budget figures come from the usage route. A metric that names what was counted carries `meter` and `unit`. The metrics are `lenny_budget_reservations_total` by `meter` and `outcome`, `lenny_budget_refusals_total` by `tenant_id`, `meter`, `unit`, `scope`, and `action`, `lenny_budget_reservation_duration_seconds` by `meter`, `lenny_budget_threshold_crossings_total` by `tenant_id`, `meter`, and `unit`, `lenny_budget_resets_total` by `trigger`, `lenny_budget_rebuilds_total` by `outcome`, `lenny_budget_compute_tick_lag_seconds`, `lenny_budget_storage_accrual_lag_seconds`, `lenny_budget_flush_lag_seconds` per replica, the time since the replica's last completed flush, and `lenny_budget_flush_failures_total` by `outcome`. The alerts are `BudgetRebuildFailing`, `BudgetAccrualLagging` when the accrual lag passes twice its interval, `BudgetFlushLagging` when a replica's flush lag passes three times `budgets.shared.flushSeconds`, and `BudgetPriceListGap` when a limit in money counts a meter with no price in force.

**Usage reports [design].** The usage route of a budget answers from its counters for the open period and from `budget_periods` for closed ones, with each limit's consumption by meter, so a team sees its model, compute, and storage spend apart. `GET /v1/usage?budget=` aggregates the usage records of the budget's subjects in a window, which reproduces a budget's consumption from records for a budget with a selector, and returns `400 VALIDATION_ERROR` for a principal budget, whose subjects are listed by session.

**What a runtime sees [design].** A runtime meets budgets as refusals: `BUDGET_EXHAUSTED` from the proxy, from `session.create`, and from a file export, `SESSION_BUDGET_REQUIRED` from `session.create` under 0166's setting, and the stopped turn of a session that a budget suspends or ends under `onExhausted.running`. A runtime has no operation that reads a budget (decision 31). A session budget is optional, so such a read would answer for some sessions and not others, and an orchestrator that carves a child relies on `defaultDelegationFraction` or on the amount its client names.

### BG-11 What this change supersedes

| Change | Parts superseded [design] | Parts kept |
| :-- | :-- | :-- |
| [0133](../0133-llm-upstreams-and-cost-budgets/change.md#lu-6-credit-budgets) | The scope table of "LU-6" ("Scopes"): the session, tree, user-window, tenant-window, and global-window scopes become session budgets, carves, and shared budgets. Step 4 of "Reservation", one script across the scopes, and open decision 6, which decision 24 settles with 0133's decision 6: a call reserves in session budgets alone, and shared budgets count afterwards. Step 5 of "Reservation", the extension for a session or tree scope, which becomes the extension of a session budget or carve. "Delegation" and "The tree scope" in "LU-6", which revision 11 of 0133 already points at "BG-8" for the carve, apart from its name `maxCreditBudget` for a child's credit amount, which becomes `limits.budget`. `maxCreditBudget` on the lease, on `lease_slice`, and in the presets. The `quotas.llmCredits.perUser`, `perTenant`, and `global` settings. Decision D3's "tied to no currency" and OD-6, which a price list amends for budgets ("BG-5"). | "LU-5" rate cards, credits as the measure of a model call's cost, which a budget names `llmCredits` (decision 16), the input estimate, settlement, the recovery sweep, the bounds of LUR-7, "LU-7", and `quotas.llmCredits.perCall` as a ceiling per call. |
| [`0166-delegation-primitives`](../0166-delegation-primitives/change.md) | None left to supersede: revision 8 cites this change for budget objects and the carve ("DP-3", and the 0170 row of "DP-7"). This change states in full what that row lists: the carve as a reservation from the budget objects that apply to the parent, `limits.budget` on `session.create` with `defaultDelegationFraction` per limit key, the budget part of `session.set_limits`, `session.transfer`, and `session.release`, and lease extension of carves ("BG-6", "BG-8"). The `maxCreditBudget` among 0166's root grant inputs, in its examples, and in a client's `delegationLease` becomes `sessionBudgetCeilings` and `budget` by limit key ("BG-2"), and SO-5a's `creditBudgetAbove` of 0142 becomes `llmCreditsAbove` (decision 16). | DPR-4, the operations that move a carve, the resolution order of "DP-1", which "BG-2" follows, and every other resource caveat with its ceilings. |
| [0121](../0121-billing-resource-attribution/change.md) | None superseded. This change adds the optional `computeUnitsPerSecond` pin to an entry of the `resourceClasses` map, and the compute rate resolved at the bind as a column of `session_pod_windows` ("BG-4"). | Every part. This change reads 0121's records as the durable source of compute and storage charges. |
| [0131](../0131-incremental-checkpoint/change.md#ic-4-catalog-reference-closure-and-quota) | "Quota" in IC-4, and R4's statement that the tenant's storage quota bounds workspace size: the charge becomes every limit that counts `storage` in the owning session's set, under "BG-6". | Every other part. |
| [0144](../0144-session-derive/change.md) | The reservation of a derive's upload copies against the tenant's storage quota, and `STORAGE_QUOTA_EXCEEDED`: each copy is a write declared by its size to the limits that count `storage` in the new session's budget set, refused with `BUDGET_EXHAUSTED`. | Every other part. |
| [0147](../0147-tenant-derived-runtimes/change.md) | `429 STORAGE_QUOTA_EXCEEDED` for an upload past `maxAssetUploadBytes`, which becomes `413 UPLOAD_TOO_LARGE`, because the cap is no budget (decision 44, and 0147's open decision 57). | The cap and every other part. |
| [0128](../0128-forensics-evidence-preservation/change.md) | The exclusion of evidence from `storageQuotaBytes`, which becomes the exclusion of evidence from every limit that counts `storage`. | Every other part. |
| [0146](../0146-session-lifecycle/change.md) | None superseded. It gains `suspension_cause` and the budget suspension of "BG-6" in SL-1, SL-2, SL-4, SL-6, and SL-10, and SL-10's budget keys follow "BG-9" (decisions 42 and 43). | Every other part. |
| [0141](../0141-runtime-routing/change.md#rt-2-the-interceptor-contract) | None superseded. RT-2 gains `budget_refs` and `session_budget` in the `PreRoute` payload and the add-only check at the post-chain point ("BG-3"). | Every other part. |
| [0149](../0149-tenant-scoping-identity/change.md#label-rules-ts-10) | None superseded. It owns the label rules that "BG-3" reads (decision 41). | Every other part. |

0133 revision 12 replaces the scopes of "LU-6" with this change's budget set and drops `maxCreditBudget` (decision 9), and 0133 revision 13 names the limits that count `llm` in `llmCredits` (decision 16). 0166 revision 8 cites this change for the carve, and its revision 10 states `limits.budget` by limit key. 0142 revision 64 renames `creditBudgetAbove` to `llmCreditsAbove`. Every decision is settled, and the edits this table lists are owed now: the review of 2026-11-23 made the edits to 0131, 0144, 0147, 0128, 0146, 0141, and 0149, and the edits to 0121 land with 0170.S3, which adds the pin and the column.

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
currency: EUR
llm: { perMillionCredits: 2.00 }
compute: { perComputeUnitHour: 0.12 }
storage: { perGiBMonth: 0.025 }
```

**Tenant administrator.** `alice` creates a tenant budget that selects the team's directory group:

```http
POST /v1/admin/budgets
{
  "name": "payments-monthly",
  "limits": {
    "models": { "meters": ["llm"], "amount": 1500000000, "unit": "llmCredits" },
    "total":  { "meters": ["llm", "compute", "storage"], "amount": 5000.00, "unit": "money" }
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
2. `payments-monthly` is a shared budget, so it is counted afterwards ("BG-6"). Each model call's settled cost reaches `models`, and the same amount converted at 2.00 EUR per million credits reaches `total`, through each replica's flush every 5 seconds. A session's pod time and stored data reach `total` alone. A session on a `medium` pod with one slot in a `sandboxed` pool spends 60 seconds times 1.5 times 1.2, which is 108 compute units, or 0.0036 EUR, in each tick. A checkpoint pack adds its bytes to the budget's bytes held, and the accrual adds byte-seconds every 5 minutes, converted at 0.025 EUR per GiB-month.
3. At 4,000 EUR, `budget.threshold_crossed` records the 0.8 threshold of `total`, and `dev.lenny.budget_threshold_crossed` reaches `alerts.acme.com` through the webhook subscription that `alice` holds on the operational event stream with her tenant's filter.
4. Had `models` run out first, model calls would be refused while pods kept running. At 5,000 EUR in `total`, the flush that carries the counter past the amount marks `total` exhausted, and each replica learns it at its next flush, so the team's spend can pass 5,000 EUR by about 5 seconds of its spending plus the calls in flight. New sessions of the group are refused with `429 BUDGET_EXHAUSTED` and `details.resetsAt` of `2026-12-01T00:00:00+01:00`. Each running session's next compute tick is refused, and the session is suspended with the sessions below it, keeping its pod for 300 seconds. When `alice` adds an adjustment to `total` within those 300 seconds, the sessions resume on their pods. Otherwise each checkpoints its workspace and releases its pod, the checkpoint admitted past any storage limit. Stored data stays.
5. At 00:00 on 1 December in Madrid, 23:00 UTC on 30 November, the period closes. `budget_periods` records the November consumption of each limit by meter, and the gateway resumes the suspended sessions, now with room.

A variant gives each user of the group 200 EUR a month inside the team's 5,000 EUR: `alice` adds a second budget with the same selector, `"per": "user"`, and one limit in money of 200.00. A per-user amount needs its own budget, because a budget's limits share its partition. Each charge must fit both budgets.

### Example 2: A tenant storage budget in bytes and in money

`acme` may hold at most 2 TiB, and may spend at most 300 EUR a month on storage. One budget holds both limits.

**Operator.** The operator instantiates a storage cap for every new tenant:

```yaml
budgets:
  tenantDefaults:
    - name: storage
      limits:
        held: { meters: [storage], amount: 1099511627776, unit: bytes }   # 1 TiB
      attach: selector
      appliesTo: {}
```

**Tenant administrator.** A `platform-admin` raises `acme`'s `held` limit to 2 TiB with `PUT /v1/admin/budgets/storage?tenant=acme`, and `alice` adds a limit in money and a monthly period to the same budget:

```http
PUT /v1/admin/budgets/storage
If-Match: "7"
{
  "name": "storage",
  "limits": {
    "held":  { "meters": ["storage"], "amount": 2199023255552, "unit": "bytes" },
    "spend": { "meters": ["storage"], "amount": 300.00, "unit": "money" }
  },
  "attach": "selector",
  "appliesTo": {},
  "period": { "schedule": "calendar", "calendar": { "every": "month", "dayOfMonth": 1 }, "timeZone": "UTC" }
}
```

The period applies to `spend`, and `held` ignores it.

**What happens.**

1. A checkpoint of a session in `acme` asks for a chunk grant of 64 MiB. `storage` is a shared budget, so the replica checks the 64 MiB against its share of what `held` had left at its last flush, 0.1 TiB divided among its replicas less what it admitted since, and checks that `spend` is not exhausted, and grants the chunk. The confirmed bytes reach `held` at the replica's next flush.
2. `acme` holds 1.5 TiB for the whole month. The accrual adds 1.5 TiB times 730 hours to `spend`, which converts at 0.025 EUR per GiB-month to 38.40 EUR, well within 300 EUR.
3. Near 2 TiB, an upload whose declared size exceeds the replica's share is refused with `429 BUDGET_EXHAUSTED`, `details.limit: held`, `details.unit: bytes`, and `details.available`. Each replica's share is a slice of what was left at its last flush, so `held` can pass 2 TiB only by what stale shares admit ("BG-6", **Best effort**). Once `held` is exhausted, new and resumed sessions of `acme` are refused too (decision 37). The data already held stays.
4. If `acme` held 12 TiB under a larger `held` amount, `spend` would pass 300 EUR during the month and refuse new writes, while the stored bytes keep accruing into it until data is deleted or the month ends.

### Example 3: A CI engine running many root sessions under one weekly budget

A CI system creates one root session per pipeline job, and the operator wants all of them under 20 million LLM credits and 2,000 compute-unit-hours a week, reset on Monday at 06:00 UTC.

**Tenant administrator.** `alice` creates a service account `ci-runner` under 0143 with the `user` role in the `ci` environment.

**Client.** The CI system, authenticated as `ci-runner`, creates one principal budget with a limit for each meter:

```http
POST /v1/budgets
{ "name": "ci-weekly",
  "limits": {
    "models": { "meters": ["llm"], "amount": 20000000, "unit": "llmCredits",
                "onExhausted": { "running": "continue" } },
    "pods":   { "meters": ["compute"], "amount": 7200000, "unit": "computeUnits",
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
    "models": { "meters": ["llm"], "amount": 400000, "unit": "llmCredits" },
    "pods":   { "meters": ["compute"], "amount": 7200, "unit": "computeUnits" } } }
}
```

**What happens.**

1. Each job's budget set holds its session budget, the principal budget, and every tenant budget whose selector matches it, such as one on the `ci` environment.
2. On Sunday `models` reaches 20 million. New jobs are refused with `429 BUDGET_EXHAUSTED`, and each running job's next model call is refused, so the CI system fails the job. `pods` has room, so running jobs keep their pods until they end.
3. A release cannot wait for Monday. A principal budget is reset by its creator, so the CI system resets it with `POST /v1/budgets/ci-weekly/reset` and `"reason": "release 4.2 crunch"`. The reset starts a new period for both limits, because a budget's limits share one period. `budget.reset` records the actor and the closed period, and the next Monday boundary still applies. To raise `models` alone, the CI system would adjust it for the current period ("BG-7").
4. A model call that starts at 05:59:58 on Monday settles at 06:00:03, so its cost counts in the new week (decision 10).
5. A job that delegates under 0166 carves its children from its own session budget. Its children inherit its references, so `ci-weekly` counts their charges as they settle.

### Example 4: A session budget with a delegated child carving from it

`bob` runs an orchestrating session that delegates a review to a child. `acme` also has a tenant budget `acme-monthly` with a limit `models` of 50 million LLM credits a month, and `alice` has a budget `reviewer-runtime` with a limit `models` of 2 million LLM credits a month that selects the `code-reviewer` runtime. Each limit counts `llm` in `llmCredits`.

**Client.**

```http
POST /v1/sessions
{
  "runtime": "claude-code",
  "labels": { "project": "checkout" },
  "budget": { "limits": { "models": { "meters": ["llm"], "amount": 400000, "unit": "llmCredits" } } },
  "delegationLease": { "maxDepth": 1, "maxChildrenTotal": 4 }
}
```

**What happens.**

1. The root's budget set is its session budget (`models`, 400,000 LLM credits) and `tenant/acme-monthly`. `reviewer-runtime` does not select `claude-code`.
2. The root's model calls reserve and settle in its session budget, and their settled cost reaches `acme-monthly` through the flush. After a while the root has used 100,000 credits.
3. The root calls `session.create` with `target: "code-reviewer"` and `limits.budget: {"models": 50000}`. The gateway reserves 50,000 credits in the `models` limit of the root's session budget, which has room, and creates the child with a session budget whose `models` limit is 50,000 credits, funded by that carve. `acme-monthly` reserves nothing. The child's budget set is its session budget, `acme-monthly`, which it matches through its inherited tenant, and `reviewer-runtime`, which selects its runtime.
4. A child model call of 2,000 credits reserves in the child's session budget alone. When it settles, the root's session budget shows the 2,000 credits as consumption and 48,000 as reserved for the child, so its total does not change, and `acme-monthly` and `reviewer-runtime` each rise by 2,000 at the next flush.
5. The review needs more. The root calls `session.set_limits` with `+20000` on `models`, which reserves 20,000 more in the root's session budget.
6. The child ends having used 30,000 credits. The root's session budget releases the unconsumed 40,000 and shows 130,000 consumed of 400,000. `acme-monthly` has counted the child's 30,000 once, as it settled.
7. Had the child exhausted its 50,000 credits before step 5, the proxy would have requested a §8.6 extension for the child's carve, which, in `elicitation` mode, asks the root's client, `bob`, under 0166's "DP-3".

## Limits, and alternatives rejected

**Limits [design]:**

- Budgets are best effort ("BG-6", **Best effort**), with the bounds of BGR-9. A shared budget passes its amount by up to one flush interval of its subjects' spending plus the work in flight, which grows with the rate of spending. A limit in `byteHours` cannot stop the time that data is held.
- A budget's limits share its selector, partition, and period, so a limit per user beside a team-wide limit, or a weekly limit beside a monthly one, needs a second budget (decision 18).
- A budget that selects on a label key that no label rule of 0149 governs binds only the clients that choose to set the label (decisions 8 and 41).
- Money figures are estimates under the operator's price list, which leaves out volume tiers, commitments, credits, and taxes, as 0133's credit estimate does.
- A carve holds capacity in the parent's session budget while the child lives ("BG-8").
- Budgets cover model calls through the proxy alone, as OD-15 of 0133 states, and costs that reach no tenant under 0121.
- A budget suspension that outlasts its hold costs a checkpoint and a later restore for each session of the subtree, and a session under `sealOnly` or `none` expires at the end of the hold.
- Under `gateway.delegation.sessionBudget: optional`, a parent with no session budget creates children that no carve bounds, so DPR-4 of 0166 holds only for a parent with a session budget.
- Costs attributed to a tenant and to no session, such as idle pods, dedicated nodes, and legal-hold escrow, reach no budget (decision 29).

**Alternatives considered:**

- **Reserve ahead in every budget, with allotments for busy budgets.** Every charge would reserve in every budget of its set, budget by budget across hash slots with a compensating release, and a budget whose charge rate passed a threshold would hand each replica a share of its remaining amount. Every model call would wait on one Redis step per budget, and the protocol would need a recovery index across slots. Session budgets and carves keep the reservation, where a small amount and one key make it cheap and exact, and shared budgets count afterwards (decision 24).
- **Count afterwards, and reserve per call near the amount.** A shared budget would switch to per-call reservation when its remaining amount fell below its recent spending over a few flush intervals. It narrows the overshoot near the amount at the cost of the cross-slot protocol for those budgets. The owner chose to keep shared budgets best effort (decision 24).
- **Keep fixed scopes and add more.** A team scope, a project scope, a weekly window, and a storage money limit would each need a setting, a counter, and an enforcement path. The selector and the partition express each of them with one mechanism.
- **Price everything in credits.** Compute and storage would carry rate cards in credits, and one exchange rate would convert credits to money. The owner asked for credits for model usage and money for infrastructure, and a credit scale set per upstream cannot also be the scale of pod time (decision 5).
- **A compute weight set by hand on each resource class.** Each class, including one a tenant defines, would need a figure, and a class without one would count like any other. The formula prices every class from its requests, and a pin covers the exceptions (decision 4).
- **A compute weight on billing events.** The billing stream would carry a budget setting, and a billing system that applies its own terms would read a figure it does not use. Billing reads the resources and applies its own formula (decision 3).
- **Price lists per tenant for every meter, and a tenant markup.** A tenant would set its own prices for compute and storage, whose cost only the operator knows, and would run chargeback inside Lenny. A tenant overrides credit conversions alone and runs chargeback from usage reports (decision 7).
- **Enforce money only when the billing system has priced it.** The billing system prices after the fact, so no work could be refused before it costs money, which is what a budget is for.
- **Budgets as Kubernetes custom resources.** Tenant administrators and clients reach Lenny through its API and hold no cluster access.
- **One unit per budget.** A team that limits model calls in LLM credits and its total spend in money would keep two budgets with the same selector and period, and an administrator would keep them in step. A budget's limits express both in one record (decision 18).
- **The meter fixed by the unit.** Model calls could be budgeted only in credits and pod time only in compute units. Separating the meter from the unit lets a limit count model calls in money (decision 17).
- **Sessions go `dormant` at once when a budget runs out.** Each session in a subtree would release its pod and pay a restore even when a raise followed a minute later, and some sessions of a tree would sleep while others ran. A suspension pauses the subtree together and holds the pods for a short window (decision 42).
- **`usableBy` on a budget selected by labels.** It would keep out sessions whose owner it does not list, but every budget would repeat who may claim a label, and reports and external costs would still read unchecked labels. Label rules of 0149 govern the label itself (decision 41).
- **One counter per budget across all its subjects, with no partitions.** A per-user limit would need one budget per user, written and kept in step with the directory by an administrator.

## Specification and non-specification changes

Each page is named by its section heading and its page code, and an edit lands on the numbered section or on the page that holds the content when the step is built.

**Specification [design]:**

- [§11.2 Budgets and Quotas](../../spec/11_policy-and-controls.md#112-budgets-and-quotas) and the page BUDGET: budget objects, session budgets, matching, meters, enforcement, resets, counters, failure, and precision. The budget rows of the table, **Budget inheritance:**, **Hierarchical Quota Model:** for budgets, **Storage quota enforcement mechanism.**, **Quota Update Timing:**, **Crash Recovery for Quota Counters:**, and **Maximum Overshoot Formula:** are rewritten, and the quota rows stay on the page QUOTA.
- The page RATE gains the price list beside 0133's rate cards.
- [§11.2.1 Billing Event Stream](../../spec/11_policy-and-controls.md#1121-billing-event-stream) and the page BILLEV: the estimate label of money figures, and the statement that no billing event carries money (decision 27).
- [§11.7 Audit Logging](../../spec/11_policy-and-controls.md#117-audit-logging) and the catalog CATEVENT: the events of "BG-10".
- [§8.3 Delegation Policy and Lease](../../spec/08_recursive-delegation.md#83-delegation-policy-and-lease), **Budget Reservation Model:**, and the page LEASE: the carve over budget objects, with 0166.
- [§8.6 Lease Extension](../../spec/08_recursive-delegation.md#86-lease-extension) and the page LEASEXT: extension of session budgets and carves by limit key.
- [§10.2 Authentication](../../spec/10_gateway-internals.md#102-authentication), **Authorization and RBAC**, and the page AUTHZ: the rows that "BG-10" adds.
- [§10.6 Environment Resource and RBAC Model](../../spec/10_gateway-internals.md#106-environment-resource-and-rbac-model) and the page ENV: `sessionBudgetDefaults` and `sessionBudgetCeilings` on an environment. The page TSET gains them on a tenant.
- [§12.4 Redis HA and Failure Modes](../../spec/12_storage-architecture.md#124-redis-ha-and-failure-modes): the budget keys in the key table, the flush watermarks, and the failure rules of shared budgets.
- [§14 Workspace Plan Schema](../../spec/14_workspace-plan-schema.md#14-workspace-plan-schema), **`CreateSessionRequest` (outer, sibling fields).**: `budget` and `budgetRefs`.
- [§15.1 REST API](../../spec/15_external-api-surface.md#151-rest-api) and the catalogs CATOPS and CATERR: the routes, `BUDGET_EXHAUSTED` with its details, `BUDGET_NOT_ATTACHABLE` for an interceptor's attachment, `SESSION_BUDGET_CARVED`, `SESSION_BUDGET_NOT_CARVED`, `UPLOAD_TOO_LARGE`, and the retirement of `STORAGE_QUOTA_EXCEEDED` and `BUDGET_KEYS_EXPIRED`; the `budget_notice` session event in the catalog CATEVENT.
- [§4.8 Gateway Policy Engine](../../spec/04_system-components.md#48-gateway-policy-engine): `budget_refs` and `session_budget` in the `PreRoute` payload, with 0141.
- [§6.2](../../spec/06_warm-pod-model.md) and [§7](../../spec/07_session-lifecycle.md) with the catalog of state machines: the budget suspension, with 0146.
- [§16.1 Metrics](../../spec/16_observability.md#161-metrics), [§16.5 Alerting Rules and SLOs](../../spec/16_observability.md#165-alerting-rules-and-slos), and the catalogs CATMETRIC and CATALERT: the metrics and alerts of "BG-10", with `StorageQuotaHigh` replaced by a threshold notice.
- The catalogs CATSET and CATLIMIT: every setting and limit of "Documentation".

**Non-specification [design]:**

- A package for budget records, selectors, schedules, and price lists, with its Postgres store, its Redis scripts for the reservation, settlement, and reservation index of session budgets and for the flush of shared budgets, and each replica's cache of shared budget state.
- `pkg/gateway/meteringproxy` reserves credits over the budget set in place of the scopes of 0133 "LU-6".
- `pkg/quota/storagequota` becomes the storage meter over budget sets, and `pkg/quota/quotastore` loses its token windows.
- `pkg/gateway/platformtools/treebudget` and `delegationbudget` become the carve, with 0166.
- The session server runs the compute tick at bind and release, and the gateway's leader sweeps gain the storage accrual, the period close, the reservation recovery, and the recovery of a lapsed replica's unflushed charges.
- Migrations for `budgets`, `budget_periods`, `budget_adjustments`, `budget_checkpoints`, and `price_list_versions`, and the removal of the tenant's quota fields that budgets replace.
- The tenant record gains `budgetCaps` with `maxPerTenant` and `maxPerPrincipal`.
- The chart gains `budgets.*` and an optional `computeUnitsPerSecond` pin in `resourceClasses`, and `lenny-ctl` gains `budget list`, `budget get`, `budget usage`, `budget reset`, and `price-list validate`.
- The alerting rules lose `StorageQuotaHigh` and its checkpoint variants for budget notices.

## Documentation

Each page below is **[design]** and lands with the step that changes what it describes.

| Page | Reader | What changes |
| :-- | :-- | :-- |
| `docs/operator-guide/budgets.md` (new) | Operator | Budgets, limits, meters, and units with the table of the units each meter allows; `budgets.currency` (no default; money budgets are refused without it); the independence of budgets and billing, and what the billing stream carries for a billing system to compute compute units itself; price lists and their versions, validation, and estimate label; compute rates with `budgets.compute.rates.cpuPerVCPU` (default 1), `budgets.compute.rates.memoryPerGiB` (default 0.125), `budgets.compute.rates.gpu.<type>` (no default), `budgets.compute.isolationMultipliers` (default 1 per profile), and the pins `resourceClasses.<name>.computeUnitsPerSecond` and the pool's `computeUnitsPerSecond` (unset), with the formula and a worked rate for each shipped class; `budgets.compute.tickSeconds` (default 60, from 10 to 600); `budgets.storage.accrualIntervalSeconds` (default 300, from 60 to 3600); budgets as best effort, with session budgets and carves reserved ahead, shared budgets counted afterwards, and `budgets.shared.flushSeconds` (default 5, from 1 to 60) with the overshoot it allows; `budgets.tenantDefaults`; `budgets.maxPerTenant` (default 500), `budgets.maxPerPrincipal` (default 20), `budgets.maxPerTenantCeiling` (default 5000), `budgets.maxPerPrincipalCeiling` (default 200), and the tenant record's `budgetCaps`; `budgets.maxRefsPerSession` (default 8), `budgets.maxBudgetSetSize` (default 32), `budgets.maxPartitionsPerBudget` (default 10000), `budgets.maxAdjustmentsPerBudget` (default 100), `budgets.minResetIntervalSeconds` (default 300), `budgets.maxLimitsPerBudget` (default 8), `budgets.suspendPodHoldSeconds` (default 300, from 0 to 3600), the platform's `budgets.sessionBudgetDefaults` and `budgets.sessionBudgetCeilings`, the fail-open share of shared budgets and `quotaFailOpenCumulativeMaxSeconds`, the size check of writes to a shared storage limit, `budgets.maxThresholds` (default 8), and `budgets.minPeriodSeconds` (default 3600); `defaultDelegationFraction` per limit key (default 0.5); the platform's `sessionBudgetDefaults` and `sessionBudgetCeilings`; failure behaviour, the recovery of a replica's unflushed charges, rebuild, and precision per meter and kind of budget; the metrics and alerts, `BudgetFlushLagging` included. |
| `docs/operator-guide/multi-tenancy.md` | Operator, tenant administrator | Tenant budgets and their RBAC, the caps on their number with a tenant's override and a `tenant-admin`'s lower principal cap, a tenant's credit conversions and which budgets use them, chargeback from usage reports, selectors, partitions qualified by tenant, references and `usableBy` with interceptor registrations, budgets that select on labels and the label rules of 0149 that govern them, "Read session attributes", schedules with time zones, resets on demand, thresholds and notices, exhaustion actions and their effect on running sessions, observe mode, and the tenant's and environment's session budget defaults and ceilings. The storage quota section becomes a storage budget. |
| `docs/operator-guide/configuration.md` and `docs/reference/configuration.md` | Operator | Every `budgets.*` value with its default and range, and the removal of `storageQuotaBytes` and the token quota settings. |
| `docs/client-guide/budgets.md` (new) | Client | Budgets as best effort, and the overshoot a shared budget allows; the session budget with its limits, its defaults and ceilings joined by key, `PATCH` and its refusal for a carved budget, and extension; a budget suspension, its hold, its automatic resume, and `budget_notice`; principal budgets and `budgetRefs`; reading a session's budget set; resets of a principal budget; and how a refusal appears. |
| `docs/client-guide/error-handling.md` and `docs/reference/error-catalog.md` | Client, operator | `BUDGET_EXHAUSTED` with its details, `BUDGET_NOT_ATTACHABLE`, and the retirement of `STORAGE_QUOTA_EXCEEDED`. |
| `docs/client-guide/delegation-and-tasks.md` | Client | Carves, and the extension of a child's carve that reaches the root's client. |
| `docs/runtime-author-guide/delegation.md` | Runtime author | `limits.budget` on `session.create` by limit key, `defaultDelegationFraction`, `session.set_limits` for budget, what `session.transfer` and `session.release` do to a carve, and the refusals of a carve. |
| `docs/runtime-author-guide/lifecycle.md` | Runtime author | A refused compute tick under `suspend` stops the turn and suspends the session with its subtree, which resumes on its pod within the hold or restores after it, and under `end` expires it; a refused model call and a refused export return `BUDGET_EXHAUSTED`. |
| `docs/api/admin.md` and `docs/api/rest.md` | Operator, client | The routes of "BG-10". |
| `docs/reference/metrics.md` | Operator | The metrics and alerts of "BG-10". |
| `docs/runbooks/storage-quota-high.md` | Operator | Becomes a runbook for a storage budget's threshold notice. |
| `docs/runbooks/delegation-budget-exhaustion.md` | Operator | Carves over budget objects. |
| `docs/runbooks/budget-rebuild.md` (new) | Operator | The rebuild after a Redis loss and `BudgetRebuildFailing`, and a replica whose flush lags under `BudgetFlushLagging`. |
| `docs/reference/glossary.md` | Every reader | The terms of "Terms". |

## Tests

The tiers are those of `.claude/rules/test-coverage.md`.

| Tier | Tests | Requirements |
| :-- | :-- | :-- |
| `unit` | The meter and unit pairs of "BG-4", with the refusal of a limit whose unit one of its meters does not allow; the join of requested, default, and ceiling limits by key, with the refusal of a key whose meters or unit differ between levels; selector matching for each field; the budget set's invalidation; schedule boundaries for each schedule kind across daylight-saving changes in several time zones, cron spacing, and on-demand resets; money conversion with rounding per charge and version selection by time, and the order of a tenant's and the platform's credit conversions for a tenant budget and for a platform budget; the compute rate from resources and isolation multipliers, the pins of a pool and a class, and the per-slot share; partition keys qualified by tenant, `per: tenant`, and `budgets.maxPartitionsPerBudget`; the caps with a tenant's override, its ceiling, and a lower principal cap, `budgets.maxLimitsPerBudget`, `budgets.maxBudgetSetSize`, `budgets.maxAdjustmentsPerBudget`, and `budgets.minResetIntervalSeconds`; the conversion of a limit at the price list of the level that wrote it; the size check of a write against a replica's share, and a stream cut past its declared size; the refusal of `continue` on a limit that counts `compute`; an adjustment by key, its refusal of an unknown key, and a limit in `bytes` under an adjustment window. | BGR-1, BGR-4, BGR-5, BGR-6 |
| `component` | The reservation, settlement, and reservation-index scripts of a session budget on a Redis Cluster, with every limit of one budget reserved in one script; a carve's settlement moved from reserved to consumed in each funding session budget; the flush script of a shared budget with its watermark, a threshold crossed by a flush, and a cached state refreshed to exhausted; the exhaustion of one limit, which refuses its meters' charges while the budget's other limits keep charging; a flush after a period boundary, which adds each sum to its own period; the recovery sweep for each meter and the recovery of a lapsed replica's unflushed charges counted once, with the fence that makes a returning replica drop its sums; the fail-open share and the turn to refusing after `quotaFailOpenCumulativeMaxSeconds`; the rebuild from checkpoints and durable records. | BGR-2, BGR-6, BGR-7, BGR-10 |
| `contract` | The routes' schemas, `If-Match`, `dryRun`, the error details, and the redaction of `details.budget` for a caller that may not read it. | BGR-11, BGR-12 |
| `integration` | Example 1 to Example 4 on the compose stack: a team budget with a limit in money across sessions, with a threshold notice and exhaustion; a storage budget with a limit in `bytes` and a limit in money; a CI engine under one principal budget with two limits and a reset on demand; a carve with a transfer and a return. | BGR-1 to BGR-8, BGR-12 |
| `e2e_kind` | A compute tick refused under `suspend` suspends a parent and its children together; a raise within the hold resumes them on their pods; after the hold each checkpoints, the checkpoint admitted past an exhausted storage limit, and releases its pod, and a client's resume is refused until the budget has room; a session under `sealOnly` expires at the end of the hold; a new session is refused while a storage limit is exhausted. | BGR-8, BGR-9 |
| `load_local` | Many sessions on several replicas charging one shared budget, with the measured overshoot at several `budgets.shared.flushSeconds` against BGR-9's bound; concurrent carves against one parent. | BGR-2, BGR-9 |
| `chaos` | A replica killed with reservations in each meter and with unflushed sums for shared budgets, each charge counted once after recovery; Redis lost and recovered across a period boundary. | BGR-6, BGR-10 |
| `security` | A principal budget cannot reach another principal's session; a tenant budget cannot match another tenant's charge; a `tenant-admin` cannot set a compute rate, multiplier, or pin, write the platform price list, write a tenant price list section other than `llm`, or raise a budget cap; `budgetRefs` naming another principal's budget, or a tenant budget the caller may not use, is refused as missing, and the budget set read names no budget the caller may not read; a client cannot loosen a shared budget through its own session budget; a tenant's credit conversion does not change a platform ceiling's money; an interceptor not named in a budget's `usableBy` cannot attach it; a `PATCH` of a carved session budget, a `set_limits` raise with no carve, and a transfer to a root that cannot reserve the carve are refused; a session whose label a rule of 0149 does not allow is refused. | BGR-2, BGR-3 |
| `docs` | The pages of "Documentation" name only settings, metrics, alerts, and errors the catalogs hold, with matching defaults. | BGR-11 |
