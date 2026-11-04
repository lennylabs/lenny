# Tenant-owned pools

Status: alternative for owner review, revision 4, 2026-11-04. Nothing here is built or staged, and no test or spike was run for it. Statements about the tree were checked by reading the code at commit `7c1f91e05`. Revision 2 applies an independent review of revision 1, and revisions 3 and 4 apply two stop checks. All are under "Review of revision 1", and no review has read revision 4's changes. It reads against `tenant-derived-runtimes-design.md` revision 6, `tenant-scoping-identity-design.md` revision 19, and `egress-and-git-design.md` revision 56. Proposals carry the label OP-n. Nothing here replaces `tenant-derived-runtimes-design.md` unless the owner adopts it.

## Question

The owner asked whether `tenant-derived-runtimes-design.md` solves the problems it was raised for, and whether a better design lets tenants set aside their own runtimes. This document answers both and states the alternative.

## Problems to solve

| # | Problem | Source |
| :-- | :-- | :-- |
| P1 | An environment with anything of its own cannot use a concurrent or service-mode runtime. | `tenant-scoping-identity-design.md` decision 15 |
| P2 | State the scrub misses passes between environments on a recycling pool. | Same document, decision 5 |
| P3 | A customer cannot add its private CA or other pod-creation settings on a shared pool. | `tenant-outbound-connector-design.md`, `egress-and-git-design.md` "Tenant pool overlays" |
| P4 | One tenant's admin can change shared runtimes and pools that serve other tenants. | DF-1, F-5.2.67 |
| P5 | Any tenant admin can drain any pool through an experiment. | DF-9 |
| P6 | Grants do not gate runtime or pool use. | DF-2 |
| P7 | Derived runtimes produce no pods, have no base pool fallback, and their `sessionPolicy` sets nothing on pods. | DF-3, DF-5, DF-6 |
| P8 | Setup commands run with the adapter's identity. | DF-4 |
| P9 | A tenant wants its own default files, setup commands, agent card, narrower limits, or narrower `setupCommandPolicy`, `delegationPolicyRef`, or `supportedProviders` on a runtime. | `tenant-derived-runtimes-design.md`, "Problem" |
| P10 | An environment with a deny or a route refuses every transparent client of each runtime it admits. | `tenant-scoping-identity-design.md` decision 3 |
| P11 | The owner wants a tenant admin able to create derived runtimes. | `tenant-derived-runtimes-design.md`, R1 |

## What the derived runtimes design does

It solves all of these except a service-mode P1, which it states as out of scope. DR-1, DR-2, and DR-3 solve P4 to P8, and those three stand alone.

The design bundles two things. A tenant-derived runtime is a new definition of what runs, and it comes with an owned pool, which is capacity and pod policy. P1, P2, and P3 need only the pool. P9, P10, and P11 need the definition.

The definition carries most of the design's weight and risk:

- A tenant runtime table, a name prefix grammar, and a tenant view replacing the store at every session read, because a missed read continues on a not-found answer.
- Tenant code in every session when a grant allows defaults, with DR-3 as a dependency.
- A live merge against the base with an `incompatible` state.
- A client changes the runtime it names. Bob's client on `chat` in `payments` keeps getting a refusal until it names `acme--chat-payments`, and delegation allow lists and experiment bases that name `chat` need the same change.

The same definition gives P10 a route: `payments` admits only `acme--chat-payments`, so `chat` stays open to transparent clients.

## Alternative: a tenant-owned pool on the unchanged base

A `tenant-admin` holding a platform grant creates a pool for a runtime the tenant already holds, from a template the platform wrote, and then maps an environment's use of that runtime to it. No new runtime exists.

```yaml
# POST /v1/admin/owned-pools
name: acme--chat-payments
runtimeRef: chat
sessionPolicy: { maxConcurrentSessions: 1, recycle: { enabled: false } }
warmCount: 2
resourceClass: small

# part of tenantEgressPolicy
environmentPolicies:
  payments:
    pools: { chat: acme--chat-payments }
```

A tenant-wide map, `tenantPools: { chat: acme--chat-default }`, covers every session of the tenant on that runtime with no environment entry of its own, including E = ∅. Both maps are part of `tenantEgressPolicy`, so EG-9's single write path, its audit event, and SO-2's guardrails apply to a repoint, and a `tenant-admin` writes them, as `tenant-scoping-identity-design.md` states for `environmentPolicies`. A shadow candidate's `environmentPolicies` carries `pools` with no effect on selection.

### Selection

A map answers the question directly, so several pools on one runtime need no ranking rules, and a pool warms before a write sends sessions to it.

Once per session, on its final runtime, the gateway selects one pool:

1. An entry in `environmentPolicies[E].pools` for runtime R selects that pool.
2. Otherwise an entry in `tenantPools` for R selects that pool.
3. Otherwise `ResolvePool` runs as today over platform pools. On a platform runtime, a pool with an owner never matches an unpinned `ResolvePool`, so today's one-platform-pool-per-runtime rule and `ErrAmbiguousPool` keep their meaning. A later derived runtime's own pool is the exception that layer states.

Selection runs ahead of the create-time gates, and again if `preRouteBeforeExperiment`, `routeExperiment`, or `preRouteAfterExperiment` changes the runtime, and `requirePoolNotDraining`, `resolveIsolationLevel`, and the claim all use its result, so a drain of a shared pool does not refuse a session mapped elsewhere, and a drain of a mapped pool does refuse it. Service mode has no claim and no map, because the grant requires `executionMode: session`, so its unpinned reads give today's answer.

A mapped pool ends selection. The session's requested isolation profile must equal the pool's, a pin must name that pool, and a mismatch answers `400` as an unsatisfiable pool, which `writePodClaimError` gains. A client therefore cannot pin or request its way to a shared pool that lacks the environment's narrowing or that carries another environment's residue. A delegated child follows the same steps with its own E. An exhausted mapped pool answers by its `onPoolExhausted` and never falls through.

`POST /v1/admin/owned-pools` and the map have these write rules:

- An entry that a write adds or changes must name an owned pool of this tenant, backed by R, with no `PoolWarmingUp` condition and `readyCount` above 0, so a write cannot send sessions to a pool that answers `503`. Entries the write leaves alone are not re-checked, so an urgent `denied` edit does not depend on warm pods.
- A tenant cannot change `maxConcurrentSessions` or `recycle.enabled` on an owned pool, unlike the derived design's table, because a running session's formula depends on them. A template's narrowing still propagates under "Template copy", and it reaches pods created afterwards. Replacing a pool is a repoint followed by a drain, so changing either, or a §10.5 style replacement, needs no second rule.
- A tenant cannot delete a pool that a map names. A platform admin's delete leaves the entry, so selection answers `409 OWNED_POOL_UNAVAILABLE` and `runtime.base_changed` goes out, and no session falls through. §12.8 erasure removes the entry. An environment's map leaves with its policy key, which `DELETE /v1/admin/environments/{name}` already requires removing first. The write that adds a pool's first entry answers with a `Warning` when the pool recycles and another environment already maps it, because residue then passes between them, as decision 5 accepts.

### One pool per session

`row.Pool` holds a client's pin and `row.PoolRef` the claimed pool. Today most sites re-run `ResolvePool` with `row.Pool`, which is safe only while one runtime and profile has one answer. These sites read concurrency, recycling, cleanup, and credential delivery from the result: `claimAtCreate`, `prepareAtFinalize`, `startOnPod`, `launchOnPod`, `resumeOnPod`, and `accountFailedSlot`.

Under OP-2, selection runs once. Every later site passes `row.PoolRef` as a pinned `ResolvePool`, which still checks template, runtime, and profile, with no map check, as `ResolveReadoptPool` already does. A later map edit therefore affects only new sessions. Before the claim, `row.PoolRef` can hold a variant pool, a derive source's pool, or a replay target, so selection does not read it, and `claimForNewRow` overwrites it.

A resume that claims a pod reads the pool's status. It stays on `row.PoolRef` while that pool is live, so §7.1's frozen `ExecutionMode` and `ScrubPolicy` stay true. A pool that is soft-deleted, retired, suspended, or under a warm stop counts as gone. The resume then re-selects, runs the final-row checks, refuses if the new pool differs in a field §7.1 freezes, and writes the new `row.PoolRef` in its commit, because `ResolveReadoptPool` and `accountFailedSlot` read that column. No resume path writes it today.

The gate on `PoolWarmingUp` applies only to a row with no live claim. Today `launchOnPod` and `startOnPod` answer `503` for a session already holding a pod when no pod is idle and one is warming, which is routine at a small `warmCount`.

A wrong pool read gives a valid answer on the wrong pool with no error, where the derived design's missed read gives a not-found answer. The compiler catches a signature change and no more, so tier 4 pins this behaviour at each listed site.

### Experiments

§7.1 step 3a says "the variant's pool overrides the default pool selection", while `claimAtCreate` resolves from `row.Pool` and then overwrites `row.PoolRef`, so a variant's `Pool` does not steer an exclusive claim today. Under OP-2 a variant's pool is a pin that the routing step sets, along with the variant's isolation profile, because `checkVariantIsolation` admits a stronger variant and a pin must match the profile. A session whose selection reaches a map entry refuses a different variant pool, as `VARIANT_ISOLATION_UNAVAILABLE` does, and a session whose selection reaches no map entry can use it. The routing step applies `requirePoolSelectable`'s grant check and selection's status read to the variant's pool, and a miss refuses the variant, because `validateRequestEnvelope` runs before `routeExperiment`. A pool that a map names cannot be a variant, because `DrainAndDelete` would otherwise remove it around the delete rule below. An experiment has no environment, so §10.7's admission check keeps comparing against the base runtime. DR-1's single-tenant rule for experiment effects applies.

### Readers keyed by runtime

Adding an owned pool must change nothing for another tenant (OP6). Readers that list pools by runtime would break that:

| Reader | Effect without a change |
| :-- | :-- |
| `PoolStoreBasePoolResolver` | It returns a base pool only when `len(pools) == 1`, so an owned pool on `chat` ends base-pool adjustment for every tenant. |
| `handleListPools` in `sessionserver/pools.go` | A `globex` user who can see `chat` receives `acme--chat-payments`, its warm count, and its resource class. |
| `emitPoolIsolationWarnings` and `AuditIsolation` | A `globex` delegation rule on `chat` matches `acme`'s pool, and the warning names it on `globex`'s audit chain. |

`poolstore.List` therefore excludes owned rows unless the caller passes an owner or a named platform-reader flag, so each reader has to choose. The PoolScalingController passes the flag. `lenny/delegate_task.poolRef` is refused outside what selection would pick, because `resolvePoolIsolation` reads any tenant's pool today. The per-pool rate limit moves after environment admission, so a non-member naming `environment: payments` cannot spend that pool's bucket.

### Base and grant lifecycle

OP-1 carries these from `tenant-derived-runtimes-design.md`, DR-6:

- The `retire` route with its notice and emergency forms, the outbox row, and `runtime.base_changed`.
- A pool status read at selection and at a resume that claims a pod. A pool outside its grant entry, on a retired base, or under a guardrail's `suspendedBy` answers `409 OWNED_POOL_UNAVAILABLE`, and it never falls through. This gives a dropped base, a narrowed `resourceClasses`, a revoked `pool_tenant_access`, and a forbid their effect, and each has a row in that design's "Base changes" table.
- Owned pools do not count as dependents under §15.1 **Deletion semantics** for a runtime, so a tenant's pool never blocks a platform delete. `handleDeleteRuntime` calls `SoftDelete` directly today, with no dependents check. On a base with owned pools, it instead starts retirement at the minimum notice and says so in its answer, and the emergency form remains for a compromised image. The derived design answers `409` before the date, which needs one rule when the two layer.

### What stays from the derived runtimes design

These apply as written, reading "owned pool" for "a derived runtime's pool":

- DR-1 in full, which closes P4, P5, and P6.
- "Who turns it on", without `allowWorkspaceFiles` and `allowSetupCommands`.
- "Template copy", `ownedOverlays`, "`sessionPolicy`", "Bounds" without `maxRuntimes`, which limits nothing here, "Kubernetes permissions", "Who drains" with `warm_stop_reasons`, and "Tenant suspension and deletion".
- The tenant `drain` route, on `/v1/admin/owned-pools`, because DR-1 removes the pool route and replacing a pool relies on a drain.
- "Pods", except `podRuntimeRef`. `lenny.dev/served` and the owner stamp stay.
- The name grammar, applied to pool names, because `SandboxWarmPool` names are global.
- DR-7's guardrail and records, with `all`, `recycle`, and `concurrency`.

### What goes away for now

- `tenant_runtime_definitions`, the tenant view, `ErrTenantScopedName`, and the live merge. DR-1's grant read still touches those sites, as a tenant check on platform rows.
- `podRuntimeRef` and DR-2 for this route. A review traced a second pool on `chat` through `toConfig`, the WarmPoolController, `createPod`, `resolveSDKWarm`, `Claimer.Claim`, and `foldPoolPolicy`, and each works by pool name or by the base's `Runtime` resource. An admin-registered base still has no pods, and the grant's check refuses it. DF-3, DF-5, and DF-6 stay as defects to fix or to correct in §5.1, and they stop blocking decision 15's route.
- DR-3 as a dependency, provided a grant's `ownedOverlays` does not list `clientProfile`. Listed, a tenant template such as a `pip.conf` selects what a base command fetches, so that entry takes the conditions the derived design gives its file flag. DF-4 remains a defect in its own right.
- The field table for runtime fields, and decisions 1, 4, 7, 9, and 11 of that design.

## Coverage

| Problem | Derived runtimes as written | Owned pools |
| :-- | :-- | :-- |
| P1, concurrent base | Yes, at one session per pod, and clients change the runtime they name | Yes, at one session per pod, with no change for a client that names an environment |
| P1, service-mode base | No | No. The grant requires `executionMode: session`. |
| P2 | Yes, through `recycle.enabled: false` or a runtime one environment admits | Yes, through `recycle.enabled: false` or a pool one environment maps. A recycling pool two environments map still passes residue. |
| P3 | Yes, through `ownedOverlays` | Yes, the same way |
| P4, P5, P6 | Yes, DR-1 | Yes, the same DR-1 |
| P7 | Yes, DR-2 | Not needed for this route. Remains a defect for operator-derived runtimes. |
| P8 | Yes for its own defaults, DR-3 | Not needed for this route, under the condition above. Remains a defect. |
| P9 | Yes | **No.** |
| P10 | Yes. An environment admits only its derived runtime. | **No.** `payments` must admit `chat` for its clients to name `chat`, so a deny in `payments` refuses every transparent client of `chat` in the tenant, and a non-member has no path to it. |
| P11 | Yes | **No.** A tenant admin creates pools and no runtimes. The owner asked for runtimes, so this is a change to what was asked. |

The real trade on clients is this. Under the derived design, an environment's clients change the name they use and the tenant's transparent clients keep working. Under owned pools, an environment's clients change nothing and the tenant's transparent clients of that runtime break once that environment has a deny or a route. A tenant whose clients all name an environment loses nothing.

## Other options considered

- **An exclusive claim on a shared pool.** A session in E takes a whole idle pod of a concurrent shared pool, the pod's formula becomes one-session, and the pod drains at the end. A stop check traced this by reading. `Binder.Resume` already takes a whole pod through `Claimer.Claim`, and `Binder.Release` can retire a pod. It needs a claim mode that admits only unlabelled pods and neither ends other tenants' holds nor drains their pinned pods, a new marker that `SlotClaimer` reads, a per-session mode at about the same sites as OP-2, and a per-pod decision input, which changes what the decision service and the Token Service enforce. It serves P1 on session-mode pools, it serves P2 only with a per-environment flag, and it does not serve P3. Unbudgeted, one tenant adding a deny can turn 50 pods into 1000 from a pool other tenants depend on, so it needs a platform cap and opt-in. It removes tenant pool creation for P1 and leaves OP-1 needed for P3.
- **Operator-written owned pools first.** Keep the owner and the map, and defer the grant and tenant routes. This has the least surface and does not let a tenant admin act.
- **A warm capacity reservation inside a shared pool.** It gives a tenant warm pods. It does not change pod policy, so P1 and P2 stay.
- **Pinning pods to an environment at claim.** Decision 15's original alternative. It needs a pin record, arbitration between gateway replicas, and warm capacity split at claim.
- **Environment-dedicated concurrent pods (OP-3).** See below. Deferred.
- **A separate tenant per environment.** This exists today and gives stronger isolation, with no shared policy or admin.
- **Dedicated nodes or a namespace per tenant.** This would give the capacity reservation R5 of the derived design concedes it lacks. It is an additive deployment concern, because the template carries placement.

### Environment-dedicated concurrent pods (OP-3, deferred)

A one-session pool costs a pod per session: at 20 sessions per pod, 1000 sessions move from 50 pods to 1000. A concurrent owned pool created for exactly one environment could apply that environment's policy at pod level, and the pod's environment would be known from creation. A review found this buildable, because `pkg/binding` as `egress-and-git-design.md` specifies it reads pod labels in the decision service and the Token Service. That package is design text and is not in the tree yet. This is the only part of this document that changes what those services enforce, so it waits for a spike. Any later design meets these conditions:

- A `dedicatedEnvironment` field is fixed at pool create, and it reaches pods through `SandboxTemplate.spec` and a new `lenny-label-immutability` rule. A claim admits a pod only when its label equals E, and an absent label refuses, so a pool cannot list one environment with unlabelled pods.
- The formula covers the warm state: `ceiling ∩ (always(E) ∪ warm-up additions) − denied(E)`, with `visible(bindings, E)` for `always` bindings.
- `egress.allowedHosts`, `egressLockHosts`, `onRequest` bindings, and `userCredential` bindings stay refused.
- A child under a sharing path is refused on such a pod. Otherwise it would share a process, `/tmp`, memory, and network with members' sessions, and "Git on covered hosts" lets any slot push to any repository in the union. Decision 14 accepted that a non-member drives the target's credentials and did not accept reach into other sessions' scopes.
- The union now holds E-scoped repositories and bindings across members' sessions, which today's concurrent pods do not hold, so one member's session reaches what another member's plan declared.

## Security properties

- OP1. An owned pool's owner comes from the authenticated principal at create and never changes, and its pods serve only that tenant, from creation to end.
- OP2. A session's pool is chosen once, from the session's tenant, environment, and runtime and the tenant's map. A client's pin, profile, or `poolRef` cannot select a pool the map does not give it. A variant can do so only for a session whose selection reaches no map entry, under the grant and status checks at routing.
- OP3. A session never falls through from a mapped pool to another pool.
- OP4. An owned pool's image, process, isolation, execution mode, delivery mode, and ceiling come from platform-written records. A tenant writes `sessionPolicy` narrowings, `warmCount`, `resourceClass` within the grant, the map, and the overlay rows the grant lists.
- OP5. With `clientProfile` unlisted, no tenant-written content runs in a session through this feature. Other listed overlay rows change a pod's warm-up list, trust, or egress profile, within what the platform listed.
- OP6. Adding an owned pool changes nothing for another tenant, including base-pool scaling, listings, audit chains, and selection.

## Costs and residual risks

- **P9, P10, and P11 are unsolved.** A tenant that needs them waits for the derived runtimes design, which remains the design for them.
- **A session's limits change with no change in what the client named.** A map edit moves an environment's new sessions, and the `Warning` and the effective-policy read report it.
- **Capacity.** "Bounds" applies, including the shared namespace `ResourceQuota`. An exhausted mapped pool refuses while a shared pool has room.
- **Trust in the base.** A tenant takes base changes with no approval step, as on any shared pool today.
- **Operating load.** Tenant-chosen pool names appear in logs and metric labels, which raises label cardinality. Warm-pod minutes appear only as `lenny_warmpool_idle_pod_minutes`, so idle owned pods are not billed unless the operator adds that.
- **Upgrades.** §10.5 runs per pool, and how an upgrade reaches pods was not established, as the derived design also records. A change to the `Runtime` resource reaches owned pods as it reaches any pod created afterwards.
- **Unproven.** Nothing has run, including several pools on one runtime through the claim path, which `ErrAmbiguousPool` has kept from being a normal path. The `cycle` package's `(runtime_name, pool_name)` tuples were not checked against two pools on one runtime.

## Specification sections affected

- §5.2 Pool Configuration and Execution Modes: owned pools, and several pools on one runtime.
- §7.1, §7.3, and §14.1: selection once per session, what resume reads, the `pool` selector, and step 3a.
- §8.2 and §10.7: the `(runtime_name, pool_name)` tuple and the admission check's base comparison.
- §10.2, §10.5, §10.6, and §15.1: the routes and permission, the map on `environmentPolicies`, DR-1's changes, per-pool upgrades, the runtime deletion rule, and replay's "default pool for `targetRuntime`".
- §13.2: nothing new, because the template's profile selects a pre-created policy.

## Proposals and dependencies

| Label | Contents | Depends on |
| :-- | :-- | :-- |
| DR-1 | From the derived runtimes design, unchanged. It stands alone and can land first. | None |
| OP-1 | The grant, the `/v1/admin/owned-pools` routes, the owner column and CRD field, template copy, `lenny.dev/served`, bounds, the owner filter on `poolstore.List`, the base and grant lifecycle, and the guardrail. | DR-1, EG-9's `ownerTenantId` |
| OP-2 | The maps, selection, one pool per session at every listed site, the experiment rule, `poolRef`, the rate limit's position, and the effective-policy read. | OP-1, TS-1 |
| OP-3 | Environment-dedicated concurrent pods, after a spike. | OP-2, EG-2's decision inputs |
| Derived runtimes, DR-2 to DR-8 | The definition layer, on top of owned pools. Its owned pool becomes this document's. | OP-1 |

## Tests

- **Tier 1.** Selection covers each step, E = ∅, a pin or profile outside a mapped pool, a variant, and `poolRef`. A map entry naming another tenant's pool, a wrong runtime, or a pool not `Ready` is refused.
- **Tier 2.** A second tenant's owned pool on `chat` leaves the first tenant's selection, `PoolStoreBasePoolResolver`, `GET /v1/pools`, and audit chain unchanged. Concurrent creates cannot pass the budget.
- **Tier 4.** After a session's first claim, a map edit changes nothing at finalize, start, launch, resume, slot failure, or re-adoption, and each reads `row.PoolRef`. A resume whose pool is gone re-selects, refuses a changed frozen field, and writes `row.PoolRef`, and re-adoption afterwards succeeds. `requirePoolNotDraining` and `resolveIsolationLevel` read the selected pool. A session holding a claim gets no `503` while its pool warms.
- **Tier 5.** On Kind, Bob's client names `chat` in `payments`, lands on a one-session owned pod, and meets `payments`' deny. Carol in `web` stays on the shared pool. An exhausted mapped pool refuses. A dropped grant entry answers `409` with no fall through.
- **Tier 9.** A tenant admin cannot name another tenant's pool, cannot widen a copied setting, and cannot reach a template through an overlay row the grant does not list.

## Review of revision 1

An independent review attacked revision 1. It confirmed that a second pool on an unchanged base avoids `podRuntimeRef`, DR-2, and DR-3, and it found the document worse than it claimed in other ways.

| Finding | Verdict | Resolution |
| :-- | :-- | :-- |
| H1, selection re-ran during a session's life | Holds. | Selection runs once, and every later site reads `row.PoolRef`. |
| M1, the base and grant lifecycle had no owner | Holds. | OP-1 carries it, with a status read at selection. |
| M2, an environment cannot separate from a runtime, so transparent clients stay refused | Holds, and has no fix within this design. | P10, the coverage table, and decision 1 state it for the owner. |
| M3, a pin or a profile escaped the owned pool | Holds. | A mapped pool ends selection. |
| M4, OP6 was false | Holds. | An owner filter on `poolstore.List`. |
| M5, variant pools were undefined | Holds. | A variant's pool is a pin. |
| M6, the uniqueness refusal conflicted with replacing a pool | Holds. | The map removes the refusal. |
| M7, a pod's environment failed after an edit | Holds. | No `environments` field on a pool, and OP-3's condition on the label. |
| M8, OP-3 reached crossing children | Holds. | OP-3 is deferred, with this as a condition. |
| Minors | Hold. | OP-3's formula, the service-mode row, OP4 and OP5, warm-up on a mapped pool, `poolRef`, the rate limit, deletion of a mapped pool, and the specification list. Revision 1 also overstated its comparison on P2 and mislabelled an earlier finding, which this revision corrects. |

A stop check on revision 2 found no Critical, High, or Major issue, and it confirmed that `row.PoolRef` is set before each listed site reads it, and that the map cannot evade `ENVIRONMENT_POLICY_NOT_ENFORCEABLE` or `ENVIRONMENT_REQUIRED`. A map can move new sessions to a weaker pool only at a `tenant-admin`'s hand, and environments give no boundary against that role. Its Minors are applied: selection ahead of the create-time gates, the status read on a resume, the resume writing `row.PoolRef`, immutable `maxConcurrentSessions` and `recycle.enabled`, the variant rules, the delete rules, the `PoolWarmingUp` gate, the map's write path, and the corrected text on an exclusive claim and on `pkg/binding`.

A second stop check found one low-end Major, a variant pin with no grant or status check at routing, and it found nothing across tenants through a tenant's own action. That fix and its Minors are applied: selection on the final runtime, what immutability binds, the delete rules, the write test for a map entry, and the items carried from the derived design.

The first reviewer proposed the map. Its judgement was to recommend owned pools as the first layer, with these changes, and it added that if transparent clients on a shared runtime matter, or the owner's request for runtimes is literal, the derived runtime cannot be deferred far.

## Decisions

| # | Decision | Recommendation | Cost | Alternative |
| :-- | :-- | :-- | :-- | :-- |
| 1 | Layering | Land DR-1, then owned pools, and keep derived runtimes as the later layer for P9, P10, and P11. | No tenant defaults at first, and a deny in an environment refuses transparent clients of each runtime it admits. A tenant admin does not get the runtimes the owner asked for until the later layer. | Build derived runtimes as written, which covers those now and carries the definition layer's surface. |
| 2 | How a session reaches a pool | An explicit map per environment and tenant. | A second write after creating a pool. | Implicit selection by the pool's own `environments`, which revision 1 proposed and a review broke, or a client naming the pool, which makes every client change. |
| 3 | Concurrency for one environment | One session per pod in the first version, with OP-3 after a spike. | A pod per session for these environments. | OP-3 in the first version, which changes what the decision service and Token Service enforce. |
| 4 | An exclusive claim on a shared pool | Skip it for the first version. | P1 keeps needing a tenant's or operator's pool. | A gateway-only spike, which can disprove it cheaply and cannot prove it, because the decision half needs EG-2. It needs a platform budget and opt-in, a per-environment flag for P2, and a decision service change, so it is no smaller than OP-1 and OP-2, and it spends capacity other tenants depend on. |
